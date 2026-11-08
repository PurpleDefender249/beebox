package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PurpleDefender249/beebox/internal/term"
	"github.com/PurpleDefender249/beebox/internal/version"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func cols(s string) int { return utf8.RuneCountInString(sgr.ReplaceAllString(s, "")) }

func TestBannerArtShape(t *testing.T) {
	if len(emblemPixels)%2 != 0 {
		t.Fatal("emblem needs an even number of pixel rows (two per text row)")
	}
	for i, row := range emblemPixels {
		if len(row) != emblemWidth {
			t.Fatalf("emblem row %d is %d pixels wide, want %d", i, len(row), emblemWidth)
		}
		for _, ch := range []byte(row) {
			if _, ok := emblemColours[ch]; !ok && ch != '.' {
				t.Fatalf("emblem row %d uses %q, which has no colour", i, ch)
			}
		}
	}
	for name, art := range map[string][]string{"3D": wordmark3D, "ASCII": wordmarkASCII} {
		widest := 0
		for i, row := range art {
			if strings.TrimRight(row, " ") != row {
				t.Errorf("%s row %d has trailing spaces", name, i)
			}
			widest = max(widest, utf8.RuneCountInString(row))
		}
		if widest != wordmarkWidth {
			t.Errorf("%s wordmark is %d columns wide, want %d", name, widest, wordmarkWidth)
		}
	}
	for i, row := range wordmarkASCII {
		for _, r := range row {
			if r > 0x7e {
				t.Fatalf("ASCII wordmark row %d has %q", i, r)
			}
		}
	}
}

func TestTo256(t *testing.T) {
	for _, tc := range []struct {
		c    colour
		want int
	}{
		{honey, 214},
		{colour{255, 255, 255, 97}, 231},
		{colour{0, 0, 0, 30}, 16},
		{colour{29, 33, 37, 30}, 235},
	} {
		if got := to256(tc.c); got != tc.want {
			t.Errorf("to256(%v) = %d, want %d", tc.c, got, tc.want)
		}
	}
	g := gradient(6)
	if g[0] != gradStops[0] || g[5] != gradStops[len(gradStops)-1] {
		t.Fatalf("gradient should run from the first to the last stop: %v", g)
	}
}

func drawBanner(l bannerLook, width int) string {
	var b bytes.Buffer
	writeBanner(&b, l, width, "1.2.3")
	return b.String()
}

func TestBannerLooks(t *testing.T) {
	for _, d := range []term.Depth{term.Depth16, term.Depth256, term.DepthTrue} {
		full := drawBanner(bannerLook{color: true, depth: d, unicode: true}, 100)
		lines := strings.Split(strings.TrimSuffix(full, "\n"), "\n")
		if len(lines) != len(emblemPixels)/2 {
			t.Fatalf("depth %d: %d lines, want %d:\n%s", d, len(lines), len(emblemPixels)/2, full)
		}
		plain := sgr.ReplaceAllString(full, "")
		for _, want := range []string{wordmark3D[0], "{1.2.3}  " + bannerTagline, projectURL, "▀"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("depth %d: banner lacks %q:\n%s", d, want, plain)
			}
		}
		for _, l := range lines {
			if cols(l) > fullWidth {
				t.Fatalf("depth %d: line wider than %d columns: %q", d, fullWidth, l)
			}
		}
		if r := []rune(sgr.ReplaceAllString(lines[1], "")); len(r) < fullWidth-wordmarkWidth ||
			string(r[fullWidth-wordmarkWidth:]) != wordmark3D[0] {
			t.Fatalf("depth %d: letters not beside the emblem: %q", d, lines[1])
		}
	}

	// Too narrow for the emblem: letters and text only.
	mid := sgr.ReplaceAllString(drawBanner(bannerLook{color: true, depth: term.DepthTrue, unicode: true}, 70), "")
	if strings.Contains(mid, "▀") || !strings.HasPrefix(mid, wordmark3D[0]+"\n") {
		t.Fatalf("70 columns should show the letters only:\n%s", mid)
	}

	// No colour: the 3D letters still work, uncoloured; no pixel art.
	nc := drawBanner(bannerLook{unicode: true}, 100)
	if strings.Contains(nc, "\x1b") || strings.Contains(nc, "▀") || !strings.Contains(nc, wordmark3D[5]) {
		t.Fatalf("no-colour banner is wrong:\n%s", nc)
	}

	// No UTF-8: ASCII letters only.
	ascii := sgr.ReplaceAllString(drawBanner(bannerLook{color: true, depth: term.Depth16}, 100), "")
	for _, r := range ascii {
		if r > 0x7e && r != '\n' {
			t.Fatalf("ASCII banner contains %q:\n%s", r, ascii)
		}
	}
	if !strings.Contains(ascii, wordmarkASCII[2]) || !strings.Contains(ascii, projectURL) {
		t.Fatalf("ASCII banner is wrong:\n%s", ascii)
	}

	if out := drawBanner(bannerLook{color: true, depth: term.DepthTrue, unicode: true}, 49); out != "" {
		t.Fatalf("a 49-column terminal should get no banner, got:\n%s", out)
	}
}

// bannerApp is a test app that behaves like an interactive terminal.
func bannerApp(t *testing.T) (*App, *bytes.Buffer) {
	a, out, _ := testApp(t)
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("TERM", "xterm-256color")
	a.Style = term.Style{}
	a.Banner = true
	return a, out
}

func TestBannerShownOnInteractiveOutputOnly(t *testing.T) {
	ctx := context.Background()
	wordmark := wordmark3D[len(wordmark3D)-1]
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, true},
		{[]string{"--help"}, true},
		{[]string{"help"}, true},
		{[]string{"list"}, true},
		{[]string{"status"}, true},
		{[]string{"banner"}, true},
		{[]string{"list", "--json"}, false},
		{[]string{"list", "--ids"}, false},
		{[]string{"status", "--json"}, false},
		{[]string{"list", "--no-banner"}, false},
		{[]string{"--no-banner", "list"}, false},
		{[]string{"--plain", "list"}, false},
		{[]string{"list", "--plain"}, false},
		{[]string{"help", "list"}, false},
		{[]string{"version"}, false},
		{[]string{"--version"}, false},
		{[]string{"completion", "bash"}, false},
	} {
		a, out := bannerApp(t)
		a.Run(ctx, tc.args)
		if got := strings.Count(out.String(), wordmark); got != map[bool]int{true: 1, false: 0}[tc.want] {
			t.Errorf("beebox %s: banner shown %d times, want shown=%v\n%s", strings.Join(tc.args, " "), got, tc.want, out.String())
		}
	}

	// Not a terminal (testApp's default): never.
	a, out, _ := testApp(t)
	a.Run(ctx, []string{"list"})
	if strings.Contains(out.String(), wordmark) {
		t.Fatal("banner shown when output is not a terminal")
	}
}

func TestBannerWantedEnv(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("BEEBOX_NO_BANNER", "1")
	if bannerWanted(nil) {
		t.Fatal("BEEBOX_NO_BANNER=1 should turn the banner off")
	}
	t.Setenv("BEEBOX_NO_BANNER", "")
	if bannerWanted(nil) {
		t.Fatal("no terminal, no banner")
	}
	// A real terminal, when the test has one (go test -v from a shell).
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no controlling terminal")
	}
	defer tty.Close()
	if !bannerWanted(tty) {
		t.Fatal("a terminal with TERM set should get the banner")
	}
	for _, v := range []string{"", "dumb"} {
		t.Setenv("TERM", v)
		if bannerWanted(tty) {
			t.Fatalf("TERM=%q should turn the banner off", v)
		}
	}
}

func TestVersionPrintsOnlyTheVersion(t *testing.T) {
	ctx := context.Background()
	want := "beebox " + version.Short() + "\n"
	for _, args := range [][]string{{"--version"}, {"-V"}, {"version"}} {
		a, out := bannerApp(t)
		if code := a.Run(ctx, args); code != ExitOK || out.String() != want {
			t.Fatalf("beebox %s = %q (exit %d), want %q", args[0], out.String(), code, want)
		}
	}

	a, out := bannerApp(t)
	a.Run(ctx, []string{"version", "--verbose"})
	for _, s := range []string{"commit", "built", "go", "platform"} {
		if !strings.Contains(out.String(), s) {
			t.Fatalf("version --verbose lacks %q:\n%s", s, out.String())
		}
	}

	a, out = bannerApp(t)
	a.Run(ctx, []string{"version", "--json"})
	var v map[string]string
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["version"] != version.Short() {
		t.Fatalf("version --json = %q (%v)", out.String(), err)
	}
}
