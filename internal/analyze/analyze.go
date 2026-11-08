// Package analyze turns the evidence left behind by a boot (the recorder's
// ring, the journal, kernel crash logs) into a plain-language verdict.
package analyze

import (
	"sort"
	"time"

	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/ring"
)

// Input is all evidence about one boot.
type Input struct {
	BootID    string
	BootStart time.Time
	Kernel    string
	// Clean is true when the boot ended with an orderly shutdown, as
	// recorded by the ring header or seen in the journal.
	Clean bool

	Ring      *ring.Contents // nil when BeeBox was not recording
	Journal   []journal.Entry
	KernelLog []journal.Entry
	Warnings  []journal.Entry
	Pstore    []pstore.Record
	// KdumpAttempts are kdump saves after this boot that left no kernel
	// log: proof the kernel crashed, without the reason.
	KdumpAttempts []pstore.Attempt

	Env       Env
	Generator string
	Now       time.Time
}

// Env describes the current machine, used to tailor advice.
type Env struct {
	PkgManager   string // apt, dnf, pacman, zypper or ""
	OOMProtector string // systemd-oomd, earlyoom, nohang or ""
	// CrashLogs is true when the kernel can save a crash log that survives
	// a reboot (pstore or kdump). Without it a kernel crash leaves no trace and looks
	// exactly like a power cut, so BeeBox must not claim to tell them apart.
	CrashLogs bool
	// HasOOMD is true when the systemd-oomd unit is installed (it is a
	// separate package on Debian, Ubuntu and Kali).
	HasOOMD bool
}

// minVerdict is the confidence below which BeeBox says it is unsure.
const minVerdict = 30

// Analyze produces a report. It returns nil when the boot ended cleanly and
// nothing about the shutdown is worth telling the user.
func Analyze(in Input) *report.Report {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	// Whether the boot that ended could save a crash log matters, not
	// whether this one can: kdump may have been loaded by hand, or set up
	// or switched off since.
	if in.Ring != nil {
		if v, ok := crashLogsDuring(in.Ring.Samples); ok {
			in.Env.CrashLogs = v
		}
	}
	lines := collectLines(&in)
	m := measure(in.Ring)
	ctx := &ruleCtx{in: &in, lines: lines, m: m}
	ctx.lastSeen = lastSeen(&in, lines, m)

	if !in.Clean && journalShowsShutdown(in.Journal) {
		in.Clean = true
	}

	var causes, context []report.Finding
	for _, rule := range rules {
		f := rule(ctx)
		if f == nil {
			continue
		}
		if f.Kind == report.KindContext {
			context = append(context, *f)
		} else {
			causes = append(causes, *f)
		}
	}
	sort.SliceStable(causes, func(i, j int) bool { return causes[i].Confidence > causes[j].Confidence })

	if in.Clean {
		// An orderly shutdown is only reported when the system shut itself
		// down for a reason the user should know about.
		var notable []report.Finding
		for _, f := range causes {
			if f.Confidence >= 60 && (f.ID == "overheating" || f.ID == "battery-depleted") {
				notable = append(notable, f)
			}
		}
		if len(notable) == 0 {
			return nil
		}
		causes = notable
	}

	r := &report.Report{
		Schema:      report.Schema,
		BootID:      in.BootID,
		BootStart:   in.BootStart,
		LastSeen:    ctx.lastSeen,
		Kernel:      in.Kernel,
		Clean:       in.Clean,
		Sources:     report.Sources{Ring: in.Ring != nil && len(in.Ring.Samples) > 0, Journal: len(in.Journal) > 0, Kernel: len(in.KernelLog) > 0, Pstore: len(in.Pstore) > 0},
		GeneratedAt: in.Now,
		Generator:   in.Generator,
		Context:     context,
	}
	r.ID = report.MakeID(r.LastSeen, in.BootID)

	if len(causes) > 0 && causes[0].Confidence >= minVerdict {
		r.Verdict = causes[0]
		for _, f := range dropSuperseded(r.Verdict.ID, causes[1:]) {
			if f.Confidence >= 25 {
				r.Others = append(r.Others, f)
			}
		}
		r.Context = dropSuperseded(r.Verdict.ID, r.Context)
		sort.SliceStable(r.Verdict.Evidence, func(i, j int) bool {
			return r.Verdict.Evidence[i].Time.Before(r.Verdict.Evidence[j].Time)
		})
	} else {
		r.Verdict = unknownFinding(ctx)
		r.Others = causes
	}
	r.Timeline = timeline(in.Ring)
	r.TopMem, r.TopCPU = topApps(in.Ring, m)
	if len(r.TopMem) > 0 && m.worst.T != 0 && m.last.T-m.worst.T > 2000 {
		r.TopMemAt = time.UnixMilli(m.worst.T)
	}
	r.Notes = notes(&in, m)
	return r
}

func journalShowsShutdown(entries []journal.Entry) bool {
	n := len(entries)
	start := n - 300
	if start < 0 {
		start = 0
	}
	for _, e := range entries[start:] {
		if reShutdown.MatchString(e.Msg) {
			return true
		}
	}
	return false
}

func lastSeen(in *Input, lines []logLine, m metrics) time.Time {
	var t time.Time
	if m.n > 0 {
		t = m.lastT
	}
	for _, es := range [][]journal.Entry{in.Journal, in.KernelLog} {
		if len(es) > 0 && es[len(es)-1].Time.After(t) {
			t = es[len(es)-1].Time
		}
	}
	if t.IsZero() && len(lines) > 0 {
		t = lines[len(lines)-1].Time
	}
	if t.IsZero() {
		t = in.Now
	}
	return t
}

func timeline(rc *ring.Contents) []report.Point {
	if rc == nil {
		return nil
	}
	pts := make([]report.Point, 0, len(rc.Samples))
	for i := range rc.Samples {
		s := &rc.Samples[i]
		p := report.Point{
			T:        time.UnixMilli(s.T),
			MemAvail: round1(s.MemAvailPct()),
			SwapUsed: round1(s.SwapUsedPct()),
			MemStall: s.PSI.MemFull,
			IOStall:  s.PSI.IOFull,
			CPU:      s.CPU,
			Battery:  -1,
			LagMs:    s.LagMs,
		}
		if t, ok := s.MaxTemp(); ok {
			p.Temp = t.C
		}
		if s.Battery != nil {
			p.Battery = s.Battery.Pct
			p.OnBattery = s.Battery.Status == "Discharging"
		}
		pts = append(pts, p)
	}
	return pts
}

func topApps(rc *ring.Contents, m metrics) ([]report.App, []report.App) {
	if rc == nil || m.n == 0 {
		return nil, nil
	}
	var mem []report.App
	for _, a := range m.worst.TopMem {
		app := report.App{Name: a.Name, Count: a.Count, MemMB: round1(float64(a.MemKB) / 1024), CPU: a.CPU}
		if g, ok := m.growth[a.Name]; ok && g > 0 {
			app.GrowthMB = round1(float64(g) / 1024)
		}
		mem = append(mem, app)
	}
	// Last sample with a CPU list (the stop sample may lack one).
	var cpu []report.App
	for i := m.n - 1; i >= 0 && i >= m.n-5; i-- {
		if len(rc.Samples[i].TopCPU) > 0 {
			for _, a := range rc.Samples[i].TopCPU {
				cpu = append(cpu, report.App{Name: a.Name, Count: a.Count, MemMB: round1(float64(a.MemKB) / 1024), CPU: a.CPU})
			}
			break
		}
	}
	return mem, cpu
}

func notes(in *Input, m metrics) []string {
	var n []string
	if in.Ring == nil {
		n = append(n, "BeeBox's recorder was not running during this boot, so this verdict is based on system logs only.")
	} else if in.Ring.Corrupt > 0 {
		n = append(n, "Some recorder samples were incomplete (normal when power is cut mid-write) and were skipped.")
	}
	if len(in.Journal) == 0 {
		n = append(n, "No system journal was available for this boot. Run 'beebox doctor' to turn on persistent logs.")
	}
	if m.n > 0 && !m.psi {
		n = append(n, "This kernel does not report pressure stall information, so memory and disk stalls are estimated.")
	}
	return n
}
