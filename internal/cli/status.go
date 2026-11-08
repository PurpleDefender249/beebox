package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/doctor"
	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/render"
	"github.com/PurpleDefender249/beebox/internal/sample"
	"github.com/PurpleDefender249/beebox/internal/term"
)

func readRecordingState(p paths.Paths) doctor.Recording { return doctor.ReadRecording(p) }

func init() {
	var stJSON bool
	register(&command{
		name: "status", banner: true, group: "Everyday", summary: "Check that BeeBox is recording",
		long: "Shows whether the recorder is running, how much history it keeps, what it measures right now, and the latest report. Exits with status 3 when BeeBox is not recording.",
		flags: func(fs *flag.FlagSet) func() {
			fs.BoolVar(&stJSON, "json", false, "print as JSON")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error { return a.status(stJSON) },
	})

	var nowWatch, nowJSON bool
	register(&command{
		name: "now", banner: true, group: "Everyday", summary: "Show what BeeBox is measuring right now",
		long: "Shows the latest measurements: memory, stalls, temperatures, battery and the apps using the most memory. Works even when the recorder isn't running.",
		flags: func(fs *flag.FlagSet) func() {
			fs.BoolVar(&nowWatch, "watch", false, "keep updating every second (Ctrl+C to stop)")
			fs.BoolVar(&nowJSON, "json", false, "print the raw sample as JSON")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error { return a.now(ctx, nowWatch, nowJSON) },
	})
}

func (a *App) status(asJSON bool) error {
	rec := doctor.ReadRecording(a.Paths)
	latest, _ := a.store().Latest()
	if asJSON {
		out := map[string]any{"recording": rec}
		if latest != nil {
			out["latest_report"] = map[string]any{"id": latest.ID, "title": latest.Verdict.Title, "confidence": latest.Verdict.Confidence, "ended": latest.LastSeen}
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
		if !rec.Active {
			return ExitCodeError{ExitNotRecording}
		}
		return nil
	}
	st := a.Style
	w := a.Stdout
	row := func(label, value string) { fmt.Fprintf(w, "  %s %s\n", st.Dim(term.Pad(label, 14)), value) }
	if rec.Active {
		fmt.Fprintf(w, "%s %s\n\n", st.Green("✓"), st.Bold("BeeBox is recording"))
		row("Since", fmt.Sprintf("%s (%s ago)", rec.Since.Local().Format("Mon 2 Jan 15:04"), render.HumanDuration(time.Since(rec.Since))))
		hist := time.Duration(min(rec.Samples, rec.SlotCount)) * rec.Interval
		every := "second"
		if rec.Interval != time.Second {
			every = rec.Interval.String()
		}
		row("History kept", fmt.Sprintf("the last %s, one sample every %s", render.HumanDuration(hist), every))
	} else {
		fmt.Fprintf(w, "%s %s\n\n", st.Red("✗"), st.Bold("BeeBox is not recording"))
		switch {
		case !rec.Exists:
			row("Why", "The recorder has not run on this computer yet.")
		case !rec.ThisBoot:
			row("Why", "The recorder hasn't started since the computer booted.")
		case rec.Last != nil && rec.Last.Flags&sample.FlagStop != 0:
			row("Why", fmt.Sprintf("The recorder was stopped %s ago.", render.HumanDuration(rec.LastAge)))
		default:
			row("Why", fmt.Sprintf("The last sample was taken %s ago; the recorder may have crashed.", render.HumanDuration(rec.LastAge)))
		}
		row("Fix", st.Cyan("sudo systemctl enable --now beebox"))
	}
	if rec.Last != nil && rec.Active {
		row("Right now", summaryLine(rec.Last))
	}
	if latest != nil {
		row("Last report", fmt.Sprintf("%s — %s", latest.LastSeen.Local().Format("Mon 2 Jan 15:04"), latest.Verdict.Title))
		row("", st.Dim("see it with: beebox last"))
	} else {
		row("Last report", "none yet — no crashes recorded")
	}
	if !rec.Active {
		return ExitCodeError{ExitNotRecording}
	}
	return nil
}

func summaryLine(s *sample.Sample) string {
	parts := []string{
		fmt.Sprintf("memory %.0f%% free", s.MemAvailPct()),
		fmt.Sprintf("CPU %.0f%%", s.CPU),
	}
	if s.PSI.OK {
		parts = append(parts, fmt.Sprintf("memory stall %.0f%%", s.PSI.MemFull))
	}
	if t, ok := s.MaxTemp(); ok {
		parts = append(parts, fmt.Sprintf("%s %.0f °C", t.Name, t.C))
	}
	if s.Battery != nil {
		parts = append(parts, fmt.Sprintf("battery %d%%", s.Battery.Pct))
	}
	return strings.Join(parts, ", ")
}

func (a *App) now(ctx context.Context, watch, asJSON bool) error {
	get := a.sampler(ctx)
	if !watch {
		s, src := get()
		if asJSON {
			enc := json.NewEncoder(a.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(s)
		}
		a.printNow(a.Stdout, s, src)
		return nil
	}
	tty := a.Out != nil && term.IsTerminal(a.Out) && !a.Style.Plain
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		s, src := get()
		switch {
		case asJSON:
			b, _ := json.Marshal(s)
			fmt.Fprintln(a.Stdout, string(b))
		case tty:
			fmt.Fprint(a.Stdout, "\x1b[H\x1b[2J")
			a.printNow(a.Stdout, s, src)
			fmt.Fprintln(a.Stdout, a.Style.Dim("\n  Updating every second. Press Ctrl+C to stop."))
		default:
			fmt.Fprintf(a.Stdout, "%s %s\n", time.UnixMilli(s.T).Local().Format("15:04:05"), summaryLine(&s))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// sampler returns a function yielding the freshest sample: the recorder's
// latest if it is running, otherwise one measured directly.
func (a *App) sampler(ctx context.Context) func() (sample.Sample, string) {
	var col *sample.Collector
	return func() (sample.Sample, string) {
		rec := doctor.ReadRecording(a.Paths)
		if rec.Active && rec.Last != nil && rec.LastAge < 5*time.Second {
			return *rec.Last, "recorder"
		}
		if col == nil {
			col = sample.NewCollector(ctx, sample.Options{KmsgPath: "-", Top: 5})
			col.Prime()
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		return col.Collect(time.Now()), "direct"
	}
}

func (a *App) printNow(w io.Writer, s sample.Sample, src string) {
	st := a.Style
	when := time.UnixMilli(s.T).Local().Format("15:04:05")
	fmt.Fprintf(w, "%s\n", st.Bold("Right now ("+when+")"))
	if src == "direct" {
		fmt.Fprintf(w, "  %s\n", st.Dim("Measured directly; the recorder isn't running."))
	}
	fmt.Fprintln(w)
	row := func(label, value, judgement string) {
		fmt.Fprintf(w, "  %s %s %s\n", term.Pad(label, 16), term.Pad(value, 34), judgement)
	}
	judge := func(bad, warn bool) string {
		switch {
		case bad:
			return st.Red("✗") + " critical"
		case warn:
			return st.Yellow("!") + " high"
		default:
			return st.Green("✓") + " fine"
		}
	}
	gb := func(kb uint64) float64 { return float64(kb) / 1024 / 1024 }
	avail := s.MemAvailPct()
	row("Memory", fmt.Sprintf("%.1f of %.1f GB free (%.0f%%)", gb(s.MemAvail), gb(s.MemTotal), avail), judge(avail < 3, avail < 10))
	if s.PSI.OK {
		row("Memory stall", fmt.Sprintf("%.1f%% of the time", s.PSI.MemFull), judge(s.PSI.MemFull >= 40, s.PSI.MemFull >= 5))
		row("Disk stall", fmt.Sprintf("%.1f%% of the time", s.PSI.IOFull), judge(s.PSI.IOFull >= 40, s.PSI.IOFull >= 10))
	}
	if s.SwapTotal > 0 {
		used := s.SwapUsedPct()
		row("Swap", fmt.Sprintf("%.1f of %.1f GB used (%.0f%%)", gb(s.SwapTotal-s.SwapFree), gb(s.SwapTotal), used), judge(used >= 95, used >= 75))
	} else {
		row("Swap", "none", "")
	}
	row("Processor", fmt.Sprintf("%.0f%% busy, load %.2f", s.CPU, s.Load1), "")
	for _, t := range s.Temps {
		crit := t.Crit
		if crit == 0 {
			crit = 100
		}
		label := ""
		if t.Crit > 0 {
			label = fmt.Sprintf(" (critical %.0f °C)", t.Crit)
		}
		row("Temperature", fmt.Sprintf("%s %.0f °C%s", t.Name, t.C, label), judge(t.C >= crit-3, t.C >= crit-15))
	}
	if s.Battery != nil {
		b := s.Battery
		v := fmt.Sprintf("%d%%, %s", b.Pct, strings.ToLower(b.Status))
		if b.PowerW > 0 {
			v += fmt.Sprintf(", %.1f W", b.PowerW)
		}
		low := b.Status == "Discharging"
		row("Battery", v, judge(low && b.Pct <= 5, low && b.Pct <= 15))
	}
	if s.RootFree >= 0 {
		row("Disk space", fmt.Sprintf("%.0f%% free on /", s.RootFree), judge(s.RootFree < 1, s.RootFree < 5))
	}
	if len(s.TopMem) > 0 {
		fmt.Fprintf(w, "\n  %s\n", st.Bold("Using the most memory"))
		for _, app := range s.TopMem {
			fmt.Fprintf(w, "    %s %8s  %s\n", term.Pad(analyze.DisplayName(app.Name), 24), fmtGB(app.MemKB), st.Dim(plural(app.Count, "process", "processes")))
		}
	}
	if len(s.TopCPU) > 0 {
		fmt.Fprintf(w, "\n  %s\n", st.Bold("Using the most processor"))
		for _, app := range s.TopCPU {
			fmt.Fprintf(w, "    %s %7.1f%%\n", term.Pad(analyze.DisplayName(app.Name), 24), app.CPU)
		}
	}
}

func fmtGB(kb uint64) string {
	mb := float64(kb) / 1024
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
