package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/PurpleDefender249/beebox/internal/doctor"
	"github.com/PurpleDefender249/beebox/internal/term"
)

// "beebox setup" makes the changes "beebox doctor" recommends, so nobody
// has to copy commands: out-of-memory protection, kernel crash logs
// (kdump or UEFI pstore), logs kept across restarts, safe SysRq restarts,
// compressed swap, desktop notifications. It shows the plan first, asks
// once, runs each step, then checks again and says what needs a restart.

// registerSetup is called from system.go, so "setup" is listed after
// "doctor" in the help.
func registerSetup() {
	var yes, dryRun bool
	var skip string
	register(&command{
		name: "setup", banner: true, group: "Everyday", summary: "Switch on everything BeeBox needs to catch the next crash",
		long: "Makes the changes 'beebox doctor' recommends: out-of-memory protection, kernel crash logs (kdump on computers that start in legacy BIOS mode, the UEFI crash log store otherwise), logs kept across restarts, safe restarts from a freeze with SysRq, compressed swap and desktop notifications. Every command is shown before it runs, and nothing runs until you agree. Run it again any time: it only changes what is still missing. Some changes need a restart; setup says which.",
		flags: func(fs *flag.FlagSet) func() {
			fs.BoolVar(&yes, "yes", false, "don't ask before making the changes")
			fs.BoolVar(&dryRun, "dry-run", false, "only show what would change")
			fs.StringVar(&skip, "skip", "", "leave these checks alone, comma-separated `IDS` (see 'beebox doctor --json'), e.g. pstore,swap")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error {
			if len(args) > 0 {
				return usagef("'setup' takes no arguments")
			}
			return a.setup(ctx, yes, dryRun, skip)
		},
	})
}

// setupStep is one change setup makes.
type setupStep struct {
	check   doctor.Check
	script  string // run as root with sh -c
	restart bool
}

const restartNote = "# then restart"

var sudoPrefix = regexp.MustCompile(`(^|&&\s*|\|\s*|;\s*)sudo\s+`)

// asRoot turns a fix as the doctor shows it ("sudo …   # then restart")
// into a script to run as root. Fixes that are advice rather than a
// command are left to the user.
func asRoot(fix string) (script string, restart, ok bool) {
	fix = strings.TrimSpace(fix)
	if i := strings.LastIndex(fix, restartNote); i >= 0 && strings.TrimSpace(fix[i+len(restartNote):]) == "" {
		restart = true
		fix = strings.TrimSpace(fix[:i])
	}
	f := strings.Fields(fix)
	if len(f) == 0 {
		return "", false, false
	}
	switch f[0] {
	case "sudo", "echo", "printf":
	default:
		return "", false, false
	}
	script = sudoPrefix.ReplaceAllString(fix, "$1")
	// apt is for people; apt-get is its stable interface for scripts.
	script = aptCmd.ReplaceAllString(script, "${1}apt-get install")
	return script, restart, true
}

var aptCmd = regexp.MustCompile(`(^|\s)apt install`)

// planSetup turns the doctor's results into steps, and lists the problems
// that need a person (advice that isn't a command).
func planSetup(checks []doctor.Check, skip map[string]bool) (steps []setupStep, manual []doctor.Check) {
	for _, c := range checks {
		if c.Status == doctor.OK || c.Fix == "" || skip[c.ID] {
			continue
		}
		if script, restart, ok := asRoot(c.Fix); ok {
			steps = append(steps, setupStep{check: c, script: script, restart: restart})
		} else {
			manual = append(manual, c)
		}
	}
	return steps, manual
}

func (a *App) setup(ctx context.Context, yes, dryRun bool, skipList string) error {
	st, w := a.Style, a.Stdout
	skip := map[string]bool{}
	for _, id := range strings.Split(skipList, ",") {
		if id = strings.TrimSpace(id); id != "" {
			skip[id] = true
		}
	}
	steps, manual := planSetup(doctor.Run(a.Paths), skip)

	fmt.Fprintf(w, "%s\n\n", st.Bold("BeeBox setup"))
	if len(steps) == 0 {
		fmt.Fprintln(w, st.Green("✓")+" Everything BeeBox can set up is already in place.")
		printManual(a, manual)
		return nil
	}
	fmt.Fprintln(w, "These changes let BeeBox catch and explain the next crash:")
	fmt.Fprintln(w)
	for i, s := range steps {
		note := ""
		if s.restart {
			note = st.Dim(" (needs a restart)")
		}
		fmt.Fprintf(w, "  %d. %s%s%s\n", i+1, st.Bold(s.check.Name), st.Dim(" ["+s.check.ID+"]"), note)
		fmt.Fprintf(w, "%s\n", st.Dim(term.Wrap(s.check.Result, min(a.Width, 96), "     ")))
		fmt.Fprintf(w, "     %s\n\n", st.Cyan("$ "+s.script))
	}
	printManual(a, manual)
	if dryRun {
		fmt.Fprintln(w, "Nothing was changed (--dry-run).")
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("setup changes system settings, so it needs root: sudo beebox setup")
	}
	if !yes {
		ok, err := confirm(a, "Make these changes? [Y/n] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Nothing was changed.")
			return nil
		}
	}

	failed := map[string]error{}
	for _, s := range steps {
		fmt.Fprintf(w, "\n%s %s\n", st.Yellow("==>"), st.Bold(s.check.Name))
		fmt.Fprintf(w, "  %s\n", st.Cyan("$ "+s.script))
		if err := runAsRoot(ctx, s.script, w, a.Stderr); err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return ctx.Err()
			}
			failed[s.check.ID] = err
			fmt.Fprintf(w, "%s %s: %v\n", st.Red("✗"), s.check.Name, err)
		}
	}

	// Check again, and say what is done, what waits for a restart and what
	// failed.
	after := map[string]doctor.Check{}
	for _, c := range doctor.Run(a.Paths) {
		after[c.ID] = c
	}
	var restart []string
	fmt.Fprintf(w, "\n%s\n", st.Bold("Result"))
	for _, s := range steps {
		c := after[s.check.ID]
		switch {
		case failed[s.check.ID] != nil:
			fmt.Fprintf(w, "  %s %s %s\n", st.Red("✗"), term.Pad(s.check.Name, 26), "Failed: "+failed[s.check.ID].Error())
		case s.restart:
			restart = append(restart, s.check.Name)
			fmt.Fprintf(w, "  %s %s %s\n", st.Yellow("!"), term.Pad(s.check.Name, 26), "Done; takes effect after a restart")
		case c.Status == doctor.OK:
			fmt.Fprintf(w, "  %s %s %s\n", st.Green("✓"), term.Pad(s.check.Name, 26), c.Result)
		default:
			fmt.Fprintf(w, "  %s %s %s\n", st.Red("✗"), term.Pad(s.check.Name, 26), c.Result)
		}
	}
	fmt.Fprintln(w)
	if len(restart) > 0 {
		fmt.Fprintf(w, "Restart the computer to finish (%s):  %s\n", strings.ToLower(strings.Join(restart, ", ")), st.Cyan("sudo reboot"))
		fmt.Fprintln(w, "Then run 'beebox doctor' to check, or 'sudo beebox setup' again: it only changes what is still missing.")
	} else if len(failed) == 0 {
		fmt.Fprintln(w, st.Green("✓")+" Done. BeeBox is ready for the next crash.")
	}
	if len(failed) > 0 {
		fmt.Fprintf(w, "%s %d change(s) failed; the messages above say why. Fix that and run 'sudo beebox setup' again.\n", st.Red("✗"), len(failed))
		return ExitCodeError{ExitError}
	}
	return nil
}

func printManual(a *App, manual []doctor.Check) {
	if len(manual) == 0 {
		return
	}
	st, w := a.Style, a.Stdout
	fmt.Fprintln(w, "These need you to do something by hand:")
	for _, c := range manual {
		fmt.Fprintf(w, "  %s %s\n", st.Yellow("!"), st.Bold(c.Name)+": "+c.Result)
		fmt.Fprintf(w, "%s\n", term.Wrap(c.Fix, min(a.Width, 96), "    "))
	}
	fmt.Fprintln(w)
}

// confirm asks a yes/no question on the terminal; Enter means yes.
func confirm(a *App, question string) (bool, error) {
	in := os.Stdin
	if !term.IsTerminal(in) {
		return false, errors.New("no terminal to ask on; to make the changes without asking, run: sudo beebox setup --yes")
	}
	fmt.Fprint(a.Stdout, question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true, nil
	}
	return false, nil
}

// runAsRoot runs one step. Package managers are told not to ask questions.
func runAsRoot(ctx context.Context, script string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script) // #nosec G204 -- scripts come from the doctor's fixed fixes
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "BEEBOX_NO_BANNER=1")
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}
