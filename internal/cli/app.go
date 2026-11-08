// Package cli implements the beebox command-line interface.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/term"
	"github.com/PurpleDefender249/beebox/internal/version"
)

// Exit codes (see beebox(1), EXIT STATUS).
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitNotRecording = 3
)

// App holds per-invocation state shared by commands.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Out    *os.File // for terminal detection; may be nil in tests
	Paths  paths.Paths
	Style  term.Style
	Width  int
	Banner bool // show the banner on interactive output (see banner.go)

	bannerShown bool
}

// UsageError is reported with exit code 2.
type UsageError struct{ msg string }

func (e UsageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return UsageError{fmt.Sprintf(format, a...)} }

// ExitCodeError lets a command choose its exit code after printing output.
type ExitCodeError struct{ Code int }

func (e ExitCodeError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

type command struct {
	name    string
	group   string
	args    string
	summary string
	long    string
	flags   func(fs *flag.FlagSet) func() // registers flags, returns a finaliser
	run     func(ctx context.Context, a *App, args []string) error
	hidden  bool
	banner  bool // show the banner above human-readable output
}

var commands []*command

func register(c *command) { commands = append(commands, c) }

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// Main runs beebox with args (excluding the program name) and returns the
// process exit code.
func Main(ctx context.Context, args []string) int {
	a := &App{Stdout: os.Stdout, Stderr: os.Stderr, Out: os.Stdout, Paths: paths.FromEnv()}
	a.Style = term.Detect(os.Stdout)
	a.Width = term.Width(os.Stdout, 80)
	a.Banner = bannerWanted(os.Stdout)
	return a.Run(ctx, args)
}

// Run dispatches a command.
func (a *App) Run(ctx context.Context, args []string) int {
	// Global flags may appear before the command.
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "--no-color", "--no-colour":
			a.Style.Color = false
		case "--plain":
			a.Style.Plain = true
			a.Style.Color = false
		case "--no-banner":
			a.Banner = false
		case "-h", "--help":
			a.printHelp()
			return ExitOK
		case "-V", "--version":
			fmt.Fprintln(a.Stdout, "beebox "+version.Short())
			return ExitOK
		default:
			fmt.Fprintf(a.Stderr, "beebox: unknown option %s\nRun 'beebox help' for usage.\n", args[0])
			return ExitUsage
		}
		args = args[1:]
	}
	if len(args) == 0 {
		a.printHelp()
		return ExitOK
	}
	name := args[0]
	c := lookup(name)
	if c == nil {
		msg := fmt.Sprintf("beebox: %q is not a beebox command.", name)
		if s := suggest(name); s != "" {
			msg += fmt.Sprintf(" Did you mean 'beebox %s'?", s)
		}
		fmt.Fprintf(a.Stderr, "%s\nRun 'beebox help' to see all commands.\n", msg)
		return ExitUsage
	}

	fs := flag.NewFlagSet("beebox "+c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	help := fs.Bool("help", false, "show help for this command")
	fs.BoolVar(help, "h", false, "show help for this command")
	noColor := fs.Bool("no-color", false, "disable colour")
	plain := fs.Bool("plain", false, "plain output: no colour or graphics (good for screen readers)")
	noBanner := fs.Bool("no-banner", false, "don't show the BeeBox banner")
	var finish func()
	if c.flags != nil {
		finish = c.flags(fs)
	}
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		fmt.Fprintf(a.Stderr, "beebox %s: %v\nRun 'beebox help %s' for usage.\n", c.name, err, c.name)
		return ExitUsage
	}
	if *help {
		a.printCommandHelp(c, fs)
		return ExitOK
	}
	if *noColor {
		a.Style.Color = false
	}
	if *plain {
		a.Style.Plain, a.Style.Color = true, false
	}
	if *noBanner {
		a.Banner = false
	}
	if finish != nil {
		finish()
	}
	if c.banner && !machineOutput(fs) {
		a.showBanner()
	}
	if err := c.run(ctx, a, pos); err != nil {
		var ue UsageError
		var ec ExitCodeError
		switch {
		case errors.As(err, &ec):
			return ec.Code
		case errors.As(err, &ue):
			fmt.Fprintf(a.Stderr, "beebox %s: %v\nRun 'beebox help %s' for usage.\n", c.name, err, c.name)
			return ExitUsage
		case errors.Is(err, context.Canceled):
			return 130
		default:
			fmt.Fprintf(a.Stderr, "beebox: %v\n", err)
			return ExitError
		}
	}
	return ExitOK
}

// parseInterspersed lets flags appear before or after positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if rest[0] == "--" {
			return append(pos, rest[1:]...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func (a *App) printHelp() {
	a.showBanner()
	st := a.Style
	w := a.Stdout
	fmt.Fprintf(w, "%s explains why your Linux computer crashed, froze or lost power.\n\n", st.Bold("beebox"))
	fmt.Fprintf(w, "%s\n  beebox <command> [options]\n\n", st.Bold("Usage"))
	groups := []string{"Everyday", "Investigate", "Service", "Other"}
	for _, g := range groups {
		var cs []*command
		for _, c := range commands {
			if c.group == g && !c.hidden {
				cs = append(cs, c)
			}
		}
		if len(cs) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s\n", st.Bold(g))
		for _, c := range cs {
			fmt.Fprintf(w, "  %s %s\n", term.Pad(c.name+" "+c.args, 20), c.summary)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "%s\n", st.Bold("Options"))
	fmt.Fprintln(w, "  --plain       no colour, graphics or banner (also: BEEBOX_PLAIN=1)")
	fmt.Fprintln(w, "  --no-color    no colour (also: NO_COLOR=1)")
	fmt.Fprintln(w, "  --no-banner   no banner (also: BEEBOX_NO_BANNER=1)")
	fmt.Fprintln(w, "  -h, --help    help for beebox or a command")
	fmt.Fprintln(w, "  -V, --version print the version")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Run 'beebox help <command>' for details, or see 'man beebox'.\n")
}

func (a *App) printCommandHelp(c *command, fs *flag.FlagSet) {
	st := a.Style
	w := a.Stdout
	fmt.Fprintf(w, "%s\n\n", st.Bold("beebox "+c.name)+" — "+c.summary)
	fmt.Fprintf(w, "%s\n  beebox %s %s\n\n", st.Bold("Usage"), c.name, strings.TrimSpace(c.args+" [options]"))
	if c.long != "" {
		fmt.Fprintf(w, "%s\n\n", term.Wrap(c.long, min(a.Width, 90), "  "))
	}
	var names []string
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name != "h" {
			names = append(names, f.Name)
		}
	})
	sort.Strings(names)
	fmt.Fprintf(w, "%s\n", st.Bold("Options"))
	for _, n := range names {
		f := fs.Lookup(n)
		name, usage := flag.UnquoteUsage(f)
		arg := ""
		if _, isBool := f.Value.(interface{ IsBoolFlag() bool }); !isBool {
			if name == "" {
				name = "value"
			}
			arg = " " + strings.ToUpper(name)
		}
		def := ""
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			def = st.Dim(" (default " + f.DefValue + ")")
		}
		fmt.Fprintf(w, "  %s %s%s\n", term.Pad("--"+n+arg, 22), usage, def)
	}
}

// suggest returns the closest command name for a typo.
func suggest(s string) string {
	best, bestD := "", 3
	for _, c := range commands {
		if c.hidden {
			continue
		}
		if d := levenshtein(s, c.name); d < bestD {
			best, bestD = c.name, d
		}
	}
	return best
}

// levenshtein is the optimal-string-alignment edit distance, so a swapped
// pair of letters ("lsit") counts as one edit.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
