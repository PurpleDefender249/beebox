package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/PurpleDefender249/beebox/internal/term"
	"github.com/PurpleDefender249/beebox/internal/version"
)

// The banner follows the convention of terminal tools such as sqlmap and
// the ProjectDiscovery tools: artwork with the version in braces, a
// tagline and the project link. It is decoration, so it only appears where
// a person is reading:
//
//   - on human-readable output in an interactive terminal,
//   - never with --json, --ids, --print-path or --watch,
//   - never when the output is piped or redirected, with --plain,
//     BEEBOX_PLAIN=1, or when TERM is unset or "dumb" (screen readers,
//     logs, scripts, Docker builds, CI),
//   - never with --no-banner or BEEBOX_NO_BANNER=1.
//
// What is drawn depends on the terminal:
//
//   - colour and a UTF-8 locale, at least 77 columns: the logo as pixel art
//     (honeycomb cell, bee, heartbeat line) beside "BEEBOX" in shaded 3D
//     letters with a honey gradient;
//   - no colour (NO_COLOR) or 50-76 columns: the 3D letters alone;
//   - no UTF-8 locale, or the Linux console: 3D letters in plain ASCII;
//   - under 50 columns: nothing.
//
// Colours come in 24-bit, 256-colour and 16-colour versions.

const (
	projectURL    = "https://github.com/PurpleDefender249/beebox"
	bannerTagline = "flight recorder for Linux crashes"

	emblemWidth   = 24 // columns
	wordmarkWidth = 49 // columns, both wordmarks
	bannerGap     = "   "
	fullWidth     = emblemWidth + len(bannerGap) + wordmarkWidth
)

// emblemPixels is the BeeBox logo as 24x20 pixel art, drawn two pixels per
// character cell with half blocks. Letters name colours (emblemColours);
// '.' is transparent.
var emblemPixels = []string{
	"...............HH.......",
	".............HHDDHH.....",
	"...........HHDDDDDDHH...",
	".........HHDDDDDDDDDDHH.",
	"........HDDWWDDDDDDDDDDH",
	".....A..HDWWWWDDDDDDDDDH",
	"....A.A.HDWWWWWwwDADDADH",
	"....A.A.HDDWWWWwwwADDADH",
	"....A.A.HDDDWWWwwDDAADDH",
	"....A.A.HDDDDWWwDDDYYDDH",
	"....A.A.HDDLKLLKDDYKEYDH",
	"..A.A.A.HDYYKYYKYDYKKYDH",
	"AA.AA.A.HYYYKYYKYDYKKYDH",
	"......A.HDYYKYYKYDDYYDDH",
	"......A.HDDYKYYKDDDDDDDH",
	"......A.HDDDDDDDDDDDDDDH",
	"......AA.HHDDDDDDDDDDHH.",
	"...........HHDDDDDDHH...",
	".............HHDDHH.....",
	"...............HH.......",
}

// wordmark3D is "BEEBOX" in figlet's "ANSI Shadow" letters: solid faces
// with a double-line shadow, which reads as 3D.
var wordmark3D = []string{
	"██████╗ ███████╗███████╗██████╗  ██████╗ ██╗  ██╗",
	"██╔══██╗██╔════╝██╔════╝██╔══██╗██╔═══██╗╚██╗██╔╝",
	"██████╔╝█████╗  █████╗  ██████╔╝██║   ██║ ╚███╔╝",
	"██╔══██╗██╔══╝  ██╔══╝  ██╔══██╗██║   ██║ ██╔██╗",
	"██████╔╝███████╗███████╗██████╔╝╚██████╔╝██╔╝ ██╗",
	"╚═════╝ ╚══════╝╚══════╝╚═════╝  ╚═════╝ ╚═╝  ╚═╝",
}

// wordmarkASCII is "BeeBox" in figlet's "Larry 3D" letters, for terminals
// that can't draw block characters.
var wordmarkASCII = []string{
	" ____                   ____",
	"/\\  _`\\                /\\  _`\\",
	"\\ \\ \\L\\ \\     __     __\\ \\ \\L\\ \\    ___   __  _",
	" \\ \\  _ <'  /'__`\\ /'__`\\ \\  _ <'  / __`\\/\\ \\/'\\",
	"  \\ \\ \\L\\ \\/\\  __//\\  __/\\ \\ \\L\\ \\/\\ \\L\\ \\/>  </",
	"   \\ \\____/\\ \\____\\ \\____\\\\ \\____/\\ \\____//\\_/\\_\\",
	"    \\/___/  \\/____/\\/____/ \\/___/  \\/___/ \\//\\/_/",
}

// colour is a 24-bit colour and its nearest basic-16 SGR code.
type colour struct {
	r, g, b uint8
	ansi    int // 30-37 or 90-97
}

var (
	honey     = colour{245, 179, 1, 33}
	linkBlue  = colour{108, 199, 255, 36}
	shadow3D  = colour{150, 96, 0, 90}
	gradStops = []colour{{255, 226, 128, 93}, {255, 198, 46, 93}, {242, 152, 0, 33}, {218, 112, 0, 33}}

	emblemColours = map[byte]colour{
		'H': honey,               // cell rim
		'A': honey,               // heartbeat line, antennae
		'Y': honey,               // body, head ring
		'L': {255, 211, 77, 93},  // light on the body
		'D': {29, 33, 37, 30},    // inside the cell
		'K': {29, 33, 37, 30},    // stripes, head
		'W': {230, 240, 251, 97}, // front wing
		'w': {169, 194, 221, 37}, // back wing
		'E': {255, 255, 255, 97}, // eye
	}
)

// gradient spreads gradStops over n rows, lit from above.
func gradient(n int) []colour {
	out := make([]colour, n)
	for i := range out {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		seg := min(int(t*float64(len(gradStops)-1)), len(gradStops)-2)
		f := t*float64(len(gradStops)-1) - float64(seg)
		a, b := gradStops[seg], gradStops[seg+1]
		mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f + 0.5) }
		out[i] = colour{mix(a.r, b.r), mix(a.g, b.g), mix(a.b, b.b), 93}
		if i >= n/2 {
			out[i].ansi = 33
		}
	}
	return out
}

// bannerLook is what the terminal can show.
type bannerLook struct {
	color   bool
	depth   term.Depth
	unicode bool
}

func detectLook(color bool) bannerLook {
	return bannerLook{color: color, depth: term.ColorDepth(), unicode: term.Unicode()}
}

func (l bannerLook) fg(c colour) string {
	switch l.depth {
	case term.DepthTrue:
		return fmt.Sprintf("38;2;%d;%d;%d", c.r, c.g, c.b)
	case term.Depth256:
		return "38;5;" + strconv.Itoa(to256(c))
	}
	return strconv.Itoa(c.ansi)
}

func (l bannerLook) bg(c colour) string {
	switch l.depth {
	case term.DepthTrue:
		return fmt.Sprintf("48;2;%d;%d;%d", c.r, c.g, c.b)
	case term.Depth256:
		return "48;5;" + strconv.Itoa(to256(c))
	}
	return strconv.Itoa(c.ansi + 10)
}

// to256 returns the nearest colour of the xterm 256-colour palette.
func to256(c colour) int {
	levels := [6]int{0, 95, 135, 175, 215, 255}
	q := func(v uint8) int {
		switch {
		case v < 48:
			return 0
		case v < 115:
			return 1
		}
		return (int(v) - 35) / 40
	}
	dist := func(r, g, b int) int {
		dr, dg, db := int(c.r)-r, int(c.g)-g, int(c.b)-b
		return dr*dr + dg*dg + db*db
	}
	ri, gi, bi := q(c.r), q(c.g), q(c.b)
	cube := 16 + 36*ri + 6*gi + bi
	grey := min(max((int(c.r)+int(c.g)+int(c.b))/3-3, 0)/10, 23)
	gv := 8 + grey*10
	if dist(gv, gv, gv) < dist(levels[ri], levels[gi], levels[bi]) {
		return 232 + grey
	}
	return cube
}

// line builds one line of output, emitting an SGR sequence only when the
// attributes change.
type line struct {
	b     strings.Builder
	cur   string
	color bool
}

func (s *line) put(attrs, text string) {
	if s.color && attrs != s.cur {
		if s.cur != "" {
			s.b.WriteString("\x1b[0m")
		}
		if attrs != "" {
			s.b.WriteString("\x1b[" + attrs + "m")
		}
		s.cur = attrs
	}
	s.b.WriteString(text)
}

// text writes characters that only have a foreground, so the current
// attributes can carry on through spaces.
func (s *line) text(t string) { s.b.WriteString(t) }

func (s *line) String() string {
	if s.cur != "" {
		s.b.WriteString("\x1b[0m")
		s.cur = ""
	}
	return s.b.String()
}

// emblemLines draws the pixel-art logo (needs colour and Unicode).
func (l bannerLook) emblemLines() []string {
	var out []string
	for y := 0; y+1 < len(emblemPixels); y += 2 {
		s := &line{color: true}
		top, bot := emblemPixels[y], emblemPixels[y+1]
		for x := 0; x < len(top); x++ {
			t, tok := emblemColours[top[x]]
			b, bok := emblemColours[bot[x]]
			switch {
			case !tok && !bok:
				s.put("", " ")
			case tok && bok && t == b:
				s.put(l.fg(t), "█")
			case tok && bok:
				s.put(l.fg(t)+";"+l.bg(b), "▀")
			case tok:
				s.put(l.fg(t), "▀")
			default:
				s.put(l.fg(b), "▄")
			}
		}
		out = append(out, s.String())
	}
	return out
}

// wordmarkLines draws the 3D letters: faces in a honey gradient, the
// shadow darker, so the letters seem to stand off the screen.
func (l bannerLook) wordmarkLines() []string {
	art := wordmarkASCII
	if l.unicode {
		art = wordmark3D
	}
	rows := gradient(len(art))
	out := make([]string, len(art))
	for i, row := range art {
		s := &line{color: l.color}
		face, shade := "1;"+l.fg(rows[i]), l.fg(shadow3D)
		for _, r := range row {
			switch {
			case r == ' ':
				s.text(" ")
			case !l.unicode || r == '█':
				s.put(face, string(r))
			default:
				s.put(shade, string(r))
			}
		}
		out[i] = s.String()
	}
	return out
}

// textLines are the version, tagline and link shown under the letters.
func (l bannerLook) textLines(ver string) []string {
	a := &line{color: l.color}
	a.put("2", "{")
	a.put("1", ver)
	a.put("2", "}  "+bannerTagline)
	b := &line{color: l.color}
	b.put("4;"+l.fg(linkBlue), projectURL)
	return []string{a.String(), b.String()}
}

// writeBanner draws the banner for a terminal width columns wide.
func writeBanner(w io.Writer, l bannerLook, width int, ver string) {
	text := l.textLines(ver)
	switch {
	case l.color && l.unicode && width > fullWidth:
		right := append(l.wordmarkLines(), text...)
		for i, left := range l.emblemLines() {
			if j := i - 1; j >= 0 && j < len(right) {
				fmt.Fprintln(w, left+bannerGap+right[j])
			} else {
				fmt.Fprintln(w, strings.TrimRight(left, " "))
			}
		}
	case width > wordmarkWidth:
		for _, s := range append(l.wordmarkLines(), text...) {
			fmt.Fprintln(w, s)
		}
	}
}

// showBanner prints the banner once per run, followed by a blank line,
// when a person is reading the output.
func (a *App) showBanner() {
	if !a.Banner || a.Style.Plain || a.bannerShown {
		return
	}
	a.bannerShown = true
	writeBanner(a.Stdout, detectLook(a.Style.Color), a.Width, version.Short())
	fmt.Fprintln(a.Stdout)
}

// machineOutput reports whether a command was asked for output meant for
// scripts or a live screen, where a banner would get in the way.
func machineOutput(fs *flag.FlagSet) bool {
	for _, name := range []string{"json", "ids", "print-path", "watch"} {
		if f := fs.Lookup(name); f != nil && f.Value.String() == "true" {
			return true
		}
	}
	return false
}

// bannerWanted reads BEEBOX_NO_BANNER and checks stdout is a terminal that
// a person is using. TERM is set by every terminal emulator, SSH and the
// Linux console, but not by Docker builds, CI runners or cron, and apt runs
// package scripts on a pseudo-terminal even when its own output is a log.
func bannerWanted(out *os.File) bool {
	if v := os.Getenv("BEEBOX_NO_BANNER"); v != "" && v != "0" {
		return false
	}
	if t := os.Getenv("TERM"); t == "" || t == "dumb" {
		return false
	}
	return out != nil && term.IsTerminal(out)
}

func init() {
	register(&command{
		name: "banner", group: "Other", hidden: true, summary: "Print the BeeBox banner",
		long: "Prints the banner the way interactive commands show it. Used by the package scripts after installation; prints nothing when the output is not a terminal.",
		run: func(ctx context.Context, a *App, args []string) error {
			a.showBanner()
			return nil
		},
	})
}
