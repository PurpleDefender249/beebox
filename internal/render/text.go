// Package render presents reports as terminal text and as a standalone,
// accessible HTML page.
package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/term"
)

// TextOptions controls terminal rendering.
type TextOptions struct {
	Style   term.Style
	Width   int
	Verbose bool // include every evidence line and other findings in full
}

// Badge returns the confidence marker: symbol + word, so meaning never
// depends on colour.
func Badge(st term.Style, f report.Finding) string {
	word := analyze.ConfidenceWord(f.Confidence)
	switch {
	case f.ID == "unknown":
		return st.Yellow("?") + " " + st.Bold("Unknown")
	case f.Confidence >= 80:
		return st.Red("●") + " " + st.Bold(fmt.Sprintf("%s (%d%%)", word, f.Confidence))
	case f.Confidence >= 60:
		return st.Yellow("●") + " " + st.Bold(fmt.Sprintf("%s (%d%%)", word, f.Confidence))
	default:
		return st.Cyan("○") + " " + st.Bold(fmt.Sprintf("%s (%d%%)", word, f.Confidence))
	}
}

// Text writes a full report.
func Text(w io.Writer, r *report.Report, o TextOptions) {
	st := o.Style
	width := o.Width
	if width <= 0 || width > 100 {
		width = min(max(width, 60), 100)
	}
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	end := r.LastSeen.Local()
	heading := fmt.Sprintf("BeeBox report for the boot that ended %s", end.Format("Mon 2 Jan 2006, 15:04"))
	if up := r.Uptime(); up > 0 {
		heading += fmt.Sprintf(" (after %s)", humanDuration(up))
	}
	p("%s\n\n", st.Bold(heading))
	if r.Sample {
		p("  %s\n\n", st.Yellow("Sample report: nothing happened to your computer."))
	}

	v := r.Verdict
	p("  %s\n", Badge(st, v))
	p("%s\n\n", term.Wrap(st.Bold(v.Title), width, "  "))
	p("%s\n\n", term.Wrap(v.Summary, width, "  "))

	if len(v.Advice) > 0 {
		p("  %s\n", st.Bold("What you can do"))
		for i, a := range v.Advice {
			p("%s\n", term.WrapHanging(a.Text, width, fmt.Sprintf("  %d. ", i+1), "     "))
			if a.Command != "" {
				p("     %s\n", st.Cyan(a.Command))
			}
		}
		p("\n")
	}

	if len(v.Evidence) > 0 {
		p("  %s\n", st.Bold("Evidence"))
		lim := 6
		if o.Verbose {
			lim = len(v.Evidence)
		}
		for i, e := range v.Evidence {
			if i == lim {
				p("  %s\n", st.Dim(fmt.Sprintf("… %d more (use --verbose)", len(v.Evidence)-lim)))
				break
			}
			prefix := "  " + evidenceTime(e) + " " + term.Pad(e.Source, 9)
			p("%s\n", term.WrapHanging(e.Text, width, prefix, strings.Repeat(" ", 21)))
		}
		p("\n")
	}

	if len(r.Others) > 0 || len(r.Context) > 0 {
		p("  %s\n", st.Bold("Also noticed"))
		for _, f := range append(append([]report.Finding{}, r.Context...), r.Others...) {
			p("%s %s\n", term.WrapHanging(f.Title, width, "  - ", "    "), st.Dim(fmt.Sprintf("(%s, %d%%)", strings.ToLower(analyze.ConfidenceWord(f.Confidence)), f.Confidence)))
			if o.Verbose {
				p("%s\n", term.Wrap(f.Summary, width, "    "))
			}
		}
		p("\n")
	}

	if len(r.Timeline) > 1 {
		p("  %s\n", st.Bold("The last "+humanDuration(r.Timeline[len(r.Timeline)-1].T.Sub(r.Timeline[0].T))+" before it stopped"))
		writeTrends(w, r, st)
		p("\n")
	}

	if len(r.TopMem) > 0 {
		heading := "Biggest memory users at the end"
		if !r.TopMemAt.IsZero() {
			heading = "Biggest memory users when memory was lowest (" + r.TopMemAt.Local().Format("15:04:05") + ")"
		}
		p("  %s\n", st.Bold(heading))
		for _, a := range r.TopMem {
			extra := ""
			if a.GrowthMB >= 100 {
				extra = st.Dim(fmt.Sprintf("  +%s in the last 10 min", fmtMB(a.GrowthMB)))
			}
			procs := ""
			if a.Count > 1 {
				procs = st.Dim(fmt.Sprintf(" (%d processes)", a.Count))
			}
			p("    %s %8s%s%s\n", term.Pad(analyze.DisplayName(a.Name), 24), fmtMB(a.MemMB), procs, extra)
		}
		p("\n")
	}

	for _, n := range r.Notes {
		p("%s\n", term.Wrap(st.Dim("Note: "+n), width, "  "))
	}
	if len(r.Notes) > 0 {
		p("\n")
	}
	p("  %s\n", st.Dim("Report "+r.ID+". To open it in your browser: beebox view "+r.ID))
}

func writeTrends(w io.Writer, r *report.Report, st term.Style) {
	pts := r.Timeline
	// Downsample to at most 40 columns, keeping the worst value per bucket.
	type series struct {
		label  string
		unit   string
		get    func(report.Point) float64
		lo, hi float64
		worst  func(a, b float64) float64
		skip   func() bool
	}
	maxF := func(a, b float64) float64 { return max(a, b) }
	minF := func(a, b float64) float64 { return min(a, b) }
	hasTemp, hasBat, hasSwap := false, false, false
	for _, p := range pts {
		hasTemp = hasTemp || p.Temp > 0
		hasBat = hasBat || p.Battery >= 0
		hasSwap = hasSwap || p.SwapUsed >= 0
	}
	list := []series{
		{"Free memory", "%", func(p report.Point) float64 { return p.MemAvail }, 0, 100, minF, func() bool { return false }},
		{"Memory stall", "%", func(p report.Point) float64 { return p.MemStall }, 0, 100, maxF, func() bool { return false }},
		{"Swap used", "%", func(p report.Point) float64 { return p.SwapUsed }, 0, 100, maxF, func() bool { return !hasSwap }},
		{"Disk stall", "%", func(p report.Point) float64 { return p.IOStall }, 0, 100, maxF, func() bool { return false }},
		{"CPU busy", "%", func(p report.Point) float64 { return p.CPU }, 0, 100, maxF, func() bool { return false }},
		{"Hottest sensor", " °C", func(p report.Point) float64 { return p.Temp }, 30, 105, maxF, func() bool { return !hasTemp }},
		{"Battery", "%", func(p report.Point) float64 { return float64(p.Battery) }, 0, 100, minF, func() bool { return !hasBat }},
	}
	const cols = 40
	for _, s := range list {
		if s.skip() {
			continue
		}
		vals := make([]float64, 0, cols)
		bucket := (len(pts) + cols - 1) / cols
		for i := 0; i < len(pts); i += bucket {
			v := s.get(pts[i])
			for j := i + 1; j < i+bucket && j < len(pts); j++ {
				v = s.worst(v, s.get(pts[j]))
			}
			vals = append(vals, v)
		}
		lo, hi, last := vals[0], vals[0], vals[len(vals)-1]
		for _, v := range vals {
			lo, hi = min(lo, v), max(hi, v)
		}
		label := term.Pad(s.label, 15)
		nums := fmt.Sprintf("%s%s at the end (lowest %s%s, highest %s%s)", num(last), s.unit, num(lo), s.unit, num(hi), s.unit)
		if hi-lo < 1 {
			nums = fmt.Sprintf("steady at %s%s", num(last), s.unit)
		}
		if st.Plain {
			fmt.Fprintf(w, "    %s %s\n", label, nums)
		} else {
			fmt.Fprintf(w, "    %s %s  %s\n", label, st.Cyan(term.Sparkline(vals, s.lo, s.hi)), st.Dim(nums))
		}
	}
}

func num(v float64) string {
	r := float64(int64(v*10+0.5)) / 10
	if v < 0 {
		r = -float64(int64(-v*10+0.5)) / 10
	}
	if r > -10 && r < 10 && r != float64(int64(r)) {
		return fmt.Sprintf("%.1f", r)
	}
	return fmt.Sprintf("%.0f", r)
}

func fmtMB(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

func evidenceTime(e report.Line) string {
	if e.Time.IsZero() {
		return "        "
	}
	return e.Time.Local().Format("15:04:05")
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	default:
		return fmt.Sprintf("%d s", int(d.Seconds()+0.5))
	}
}

// HumanDuration is exported for the CLI.
func HumanDuration(d time.Duration) string { return humanDuration(d) }
