package render

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/version"
)

//go:embed report.html.tmpl
var reportTmpl string

var tmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"clock":    func(t time.Time) string { return t.Local().Format("15:04:05") },
	"iso":      func(t time.Time) string { return t.Format(time.RFC3339) },
	"longDate": func(t time.Time) string { return t.Local().Format("Monday 2 January 2006 at 15:04") },
	"word":     analyze.ConfidenceWord,
	"display":  analyze.DisplayName,
	"mb":       fmtMB,
	"lower":    strings.ToLower,
	"add":      func(a, b int) int { return a + b },
	"hasTime":  func(t time.Time) bool { return !t.IsZero() },
	"pct1":     func(v float64) string { return num(v) },
	"ypx":      func(pos float64) float64 { return vbH - pos/100*vbH },
}).Parse(reportTmpl))

// Tick is an axis label positioned in percent of the plot.
type Tick struct {
	Pos   float64
	Label string
}

// Chart is one small-multiple line chart.
type Chart struct {
	ID        string
	Title     string
	Caption   string
	AriaLabel string
	Unit      string
	YTicks    []Tick
	XTicks    []Tick
	Line      string
	Area      string
	Threshold *Tick
	EndX      float64
	EndY      float64
	Points    string // JSON [[unixMs, value], ...] for the hover layer
	Rows      []Row
}

// Row is one line of a chart's data table.
type Row struct {
	Time  time.Time
	Value string
}

type page struct {
	R          *report.Report
	Empty      *EmptyState
	Charts     []Chart
	Confidence int
	Level      int // 1-4 position on the confidence scale
	Uptime     string
	Version    string
	Generated  time.Time
}

// EmptyState is shown by `beebox view` before any crash has been recorded.
type EmptyState struct {
	Recording bool
	Since     time.Time
	Message   string
}

// HTML writes a complete, self-contained report page.
func HTML(w io.Writer, r *report.Report) error {
	p := page{R: r, Confidence: r.Verdict.Confidence, Version: version.Short(), Generated: time.Now()}
	p.Level = confidenceLevel(r.Verdict.Confidence)
	if up := r.Uptime(); up > 0 {
		p.Uptime = humanDuration(up)
	}
	p.Charts = buildCharts(r)
	return tmpl.Execute(w, p)
}

// HTMLEmpty writes the page shown when there are no reports.
func HTMLEmpty(w io.Writer, e EmptyState) error {
	return tmpl.Execute(w, page{Empty: &e, Version: version.Short(), Generated: time.Now()})
}

func confidenceLevel(c int) int {
	switch {
	case c >= 80:
		return 4
	case c >= 60:
		return 3
	case c >= 40:
		return 2
	case c > 0:
		return 1
	default:
		return 0
	}
}

type metric struct {
	id, title, unit string
	get             func(report.Point) float64
	lo, hi          float64
	higherIsBad     bool
	present         func([]report.Point) bool
	threshold       func(*report.Report) *float64
}

func allMetrics() []metric {
	always := func([]report.Point) bool { return true }
	return []metric{
		{"mem", "Free memory", "%", func(p report.Point) float64 { return p.MemAvail }, 0, 100, false, always, nil},
		{"memstall", "Time stalled on memory", "%", func(p report.Point) float64 { return p.MemStall }, 0, 100, true, always, nil},
		{"swap", "Swap in use", "%", func(p report.Point) float64 { return p.SwapUsed }, 0, 100, true,
			func(ps []report.Point) bool { return len(ps) > 0 && ps[len(ps)-1].SwapUsed >= 0 }, nil},
		{"iostall", "Time stalled on disk", "%", func(p report.Point) float64 { return p.IOStall }, 0, 100, true, always, nil},
		{"cpu", "Processor busy", "%", func(p report.Point) float64 { return p.CPU }, 0, 100, true, always, nil},
		{"temp", "Hottest temperature", "°C", func(p report.Point) float64 { return p.Temp }, 20, 110, true,
			func(ps []report.Point) bool {
				for _, p := range ps {
					if p.Temp > 0 {
						return true
					}
				}
				return false
			}, nil},
		{"battery", "Battery charge", "%", func(p report.Point) float64 { return float64(p.Battery) }, 0, 100, false,
			func(ps []report.Point) bool { return len(ps) > 0 && ps[len(ps)-1].Battery >= 0 }, nil},
	}
}

// chartOrder puts the measurements most relevant to the verdict first.
func chartOrder(verdict string) []string {
	switch verdict {
	case "out-of-memory", "froze":
		return []string{"mem", "memstall", "swap", "cpu", "iostall", "temp", "battery"}
	case "overheating":
		return []string{"temp", "cpu", "mem", "memstall", "iostall", "swap", "battery"}
	case "battery-depleted", "died-in-sleep":
		return []string{"battery", "cpu", "temp", "mem", "memstall", "iostall", "swap"}
	case "io-stall", "storage-errors", "disk-full", "hung-tasks":
		return []string{"iostall", "memstall", "mem", "cpu", "swap", "temp", "battery"}
	default:
		return []string{"mem", "memstall", "cpu", "iostall", "temp", "battery", "swap"}
	}
}

func buildCharts(r *report.Report) []Chart {
	pts := r.Timeline
	if len(pts) < 2 {
		return nil
	}
	byID := map[string]metric{}
	for _, m := range allMetrics() {
		byID[m.id] = m
	}
	var crit float64
	for _, f := range append([]report.Finding{r.Verdict}, r.Others...) {
		if f.ID == "overheating" {
			crit = critFromEvidence(f)
		}
	}
	var out []Chart
	for _, id := range chartOrder(r.Verdict.ID) {
		m := byID[id]
		if !m.present(pts) {
			continue
		}
		c := buildChart(m, pts)
		if id == "temp" && crit > 0 {
			c.Threshold = &Tick{Pos: yPos(crit, m.lo, m.hi), Label: fmt.Sprintf("critical %.0f °C", crit)}
		}
		out = append(out, c)
	}
	return out
}

func critFromEvidence(f report.Finding) float64 {
	for _, e := range f.Evidence {
		var c float64
		if i := strings.Index(e.Text, "(critical "); i >= 0 {
			if _, err := fmt.Sscanf(e.Text[i:], "(critical %f", &c); err == nil {
				return c
			}
		}
	}
	return 0
}

const vbW, vbH = 1000.0, 200.0

func yPos(v, lo, hi float64) float64 {
	f := (v - lo) / (hi - lo)
	return math.Max(0, math.Min(1, f)) * 100
}

func buildChart(m metric, pts []report.Point) Chart {
	t0, t1 := pts[0].T, pts[len(pts)-1].T
	span := t1.Sub(t0).Seconds()
	if span <= 0 {
		span = 1
	}
	lo, hi := m.lo, m.hi
	if m.id == "temp" {
		peak := 0.0
		for _, p := range pts {
			peak = math.Max(peak, m.get(p))
		}
		hi = math.Max(80, math.Ceil((peak+10)/10)*10)
		lo = 20
	}
	var line, area strings.Builder
	xs := make([]float64, len(pts))
	ys := make([]float64, len(pts))
	data := make([][2]float64, 0, len(pts))
	for i, p := range pts {
		v := m.get(p)
		x := p.T.Sub(t0).Seconds() / span * vbW
		y := vbH - yPos(v, lo, hi)/100*vbH
		xs[i], ys[i] = x, y
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&line, "%s%.1f %.1f", cmd, x, y)
		data = append(data, [2]float64{float64(p.T.UnixMilli()), v})
	}
	fmt.Fprintf(&area, "%s L%.1f %.1f L%.1f %.1f Z", line.String(), xs[len(xs)-1], vbH, xs[0], vbH)

	first, last := m.get(pts[0]), m.get(pts[len(pts)-1])
	minV, maxV := first, first
	for _, p := range pts {
		v := m.get(p)
		minV, maxV = math.Min(minV, v), math.Max(maxV, v)
	}
	caption := describe(m, first, last, minV, maxV)
	window := humanDuration(t1.Sub(t0))
	aria := fmt.Sprintf("Line chart of %s over the last %s. %s Values range from %s%s to %s%s.",
		strings.ToLower(m.title), window, caption, num(minV), m.unit, num(maxV), m.unit)

	pj, _ := json.Marshal(data)
	c := Chart{
		ID: "chart-" + m.id, Title: m.title, Caption: caption, AriaLabel: aria, Unit: m.unit,
		Line: line.String(), Area: area.String(),
		EndX: xs[len(xs)-1] / vbW * 100, EndY: 100 - ys[len(ys)-1]/vbH*100,
		Points: string(pj),
	}
	for _, v := range niceTicks(lo, hi) {
		c.YTicks = append(c.YTicks, Tick{Pos: yPos(v, lo, hi), Label: num(v) + m.unit})
	}
	c.XTicks = timeTicks(t0, t1)
	c.Rows = tableRows(m, pts)
	return c
}

func describe(m metric, first, last, minV, maxV float64) string {
	u := m.unit
	switch {
	case !m.higherIsBad && last <= first-10:
		return fmt.Sprintf("Fell from %s%s to %s%s.", num(first), u, num(last), u)
	case !m.higherIsBad && last >= first+10:
		return fmt.Sprintf("Rose from %s%s to %s%s.", num(first), u, num(last), u)
	case m.higherIsBad && maxV >= first+10 && maxV > 5:
		if math.Abs(maxV-last) < 1 {
			return fmt.Sprintf("Climbed from %s%s to %s%s at the end.", num(first), u, num(last), u)
		}
		return fmt.Sprintf("Peaked at %s%s; %s%s at the end.", num(maxV), u, num(last), u)
	case maxV-minV < 3:
		return fmt.Sprintf("Steady around %s%s.", num(last), u)
	default:
		return fmt.Sprintf("Between %s%s and %s%s; %s%s at the end.", num(minV), u, num(maxV), u, num(last), u)
	}
}

func niceTicks(lo, hi float64) []float64 {
	step := 25.0
	if hi-lo > 100 {
		step = 50
	} else if hi-lo <= 60 {
		step = 20
	}
	var out []float64
	start := math.Ceil(lo/step) * step
	for v := start; v <= hi+0.001; v += step {
		out = append(out, v)
	}
	return out
}

func timeTicks(t0, t1 time.Time) []Tick {
	span := t1.Sub(t0)
	step := time.Minute
	for _, s := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour} {
		step = s
		if span/s <= 5 {
			break
		}
	}
	var out []Tick
	first := t0.Truncate(step)
	if first.Before(t0) {
		first = first.Add(step)
	}
	for t := first; !t.After(t1); t = t.Add(step) {
		pos := t.Sub(t0).Seconds() / math.Max(span.Seconds(), 1) * 100
		if pos > 82 { // leave room for the "stopped" label
			break
		}
		out = append(out, Tick{Pos: pos, Label: t.Local().Format("15:04")})
	}
	return out
}

func tableRows(m metric, pts []report.Point) []Row {
	step := len(pts) / 30
	if step < 1 {
		step = 1
	}
	var rows []Row
	for i := 0; i < len(pts); i += step {
		rows = append(rows, Row{Time: pts[i].T, Value: num(m.get(pts[i])) + " " + m.unit})
	}
	lastPt := pts[len(pts)-1]
	if len(rows) == 0 || !rows[len(rows)-1].Time.Equal(lastPt.T) {
		rows = append(rows, Row{Time: lastPt.T, Value: num(m.get(lastPt)) + " " + m.unit})
	}
	return rows
}
