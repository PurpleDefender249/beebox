package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/scenario"
	"github.com/PurpleDefender249/beebox/internal/term"
)

func build(t *testing.T, kind string) *report.Report {
	t.Helper()
	in, err := scenario.Build(kind, time.Date(2026, 11, 4, 23, 11, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	r := analyze.Analyze(in)
	if r == nil {
		t.Fatal("no report")
	}
	return r
}

func TestHTMLIsSelfContainedAndAccessible(t *testing.T) {
	for _, kind := range []string{"out-of-memory", "overheating", "battery", "power-loss", "kernel-panic"} {
		t.Run(kind, func(t *testing.T) {
			r := build(t, kind)
			var b bytes.Buffer
			if err := HTML(&b, r); err != nil {
				t.Fatal(err)
			}
			page := b.String()
			mustContain := []string{
				`<html lang="en">`, "Content-Security-Policy", "default-src 'none'",
				`class="skip"`, `<main id="content"`, `aria-live="polite"`,
				r.Verdict.Title, "prefers-color-scheme: dark", "forced-colors",
			}
			for _, want := range mustContain {
				if !strings.Contains(page, want) {
					t.Errorf("page is missing %q", want)
				}
			}
			for _, banned := range []string{"ZgotmplZ", `src="http`, `href="http`, "url(http", "@import"} {
				if strings.Contains(page, banned) {
					t.Errorf("page contains %q (template escaping failure or external resource)", banned)
				}
			}
			if r.Timeline != nil && !strings.Contains(page, `role="img"`) {
				t.Error("charts need role=img with a text alternative")
			}
			if strings.Count(page, "<table") < 1 {
				t.Error("charts need a data table alternative")
			}
		})
	}
}

func TestHTMLEscapesUntrustedText(t *testing.T) {
	r := build(t, "storage")
	r.Verdict.Evidence = append(r.Verdict.Evidence, report.Line{Source: "kernel", Text: `<script>alert(1)</script>`})
	var b bytes.Buffer
	if err := HTML(&b, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "<script>alert(1)") {
		t.Fatal("log text was not escaped")
	}
}

func TestEmptyPage(t *testing.T) {
	var b bytes.Buffer
	if err := HTMLEmpty(&b, EmptyState{Recording: true, Message: "BeeBox has been recording since Monday."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "No crashes recorded yet") {
		t.Fatal("empty state missing")
	}
}

func TestTextPlainHasNoEscapesOrBlocks(t *testing.T) {
	r := build(t, "out-of-memory")
	var b bytes.Buffer
	Text(&b, r, TextOptions{Style: term.Style{Plain: true}, Width: 80})
	out := b.String()
	if strings.Contains(out, "\x1b[") {
		t.Fatal("plain output contains ANSI escapes")
	}
	if strings.ContainsAny(out, "▁▂▃▄▅▆▇█") {
		t.Fatal("plain output contains sparkline glyphs")
	}
	for _, want := range []string{r.Verdict.Title, "What you can do", "Evidence", "Free memory"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q", want)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if n := len([]rune(line)); n > 100 {
			t.Errorf("line too long (%d): %q", n, line)
		}
	}
}

func TestTextColour(t *testing.T) {
	r := build(t, "battery")
	var b bytes.Buffer
	Text(&b, r, TextOptions{Style: term.Style{Color: true}, Width: 80})
	if !strings.Contains(b.String(), "\x1b[") {
		t.Fatal("expected colour codes")
	}
	if !strings.Contains(b.String(), "Very likely") {
		t.Fatal("confidence must be stated in words, not only colour")
	}
}
