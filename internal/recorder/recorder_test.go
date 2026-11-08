package recorder

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PurpleDefender249/beebox/internal/config"
	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
)

// fakeJournal serves canned entries for any boot.
type fakeJournal struct{ entries []journal.Entry }

func (f fakeJournal) Entries(_ context.Context, _ string, kernelOnly bool, _ int) ([]journal.Entry, error) {
	if !kernelOnly {
		return f.entries, nil
	}
	var k []journal.Entry
	for _, e := range f.entries {
		if e.Kernel {
			k = append(k, e)
		}
	}
	return k, nil
}

func (f fakeJournal) Warnings(context.Context, string, int) ([]journal.Entry, error) { return nil, nil }

func fakeProc(t *testing.T, bootID string) string {
	t.Helper()
	proc := t.TempDir()
	files := map[string]string{
		"stat":                      "cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 100 0 50 800 50 0 0 0 0 0\nbtime 1700000000\n",
		"meminfo":                   "MemTotal: 8000000 kB\nMemAvailable: 6000000 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n",
		"loadavg":                   "0.10 0.10 0.10 1/100 1\n",
		"uptime":                    "100.0 100.0\n",
		"vmstat":                    "pgmajfault 0\n",
		"sys/kernel/random/boot_id": bootID + "\n",
		"sys/kernel/osrelease":      "6.11.0-test\n",
	}
	for rel, content := range files {
		p := filepath.Join(proc, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return proc
}

func run(t *testing.T, opt Options, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if err := Run(ctx, opt); err != nil {
		t.Fatal(err)
	}
}

func TestCrashOnPreviousBootIsAnalysed(t *testing.T) {
	state := t.TempDir()
	p := paths.Paths{StateDir: state, RuntimeDir: filepath.Join(state, "run")}
	if err := os.MkdirAll(p.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Interval = 250 * time.Millisecond
	cfg.Flush = 250 * time.Millisecond
	prevBoot := strings.Repeat("a", 32)

	// Simulate the previous boot: a ring that was never closed cleanly.
	w, err := ring.Create(p.RingFile(), ring.Header{SlotCount: cfg.Slots(), Interval: cfg.Interval, BootID: prevBoot, BootStart: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-10 * time.Minute)
	for i := 0; i < 30; i++ {
		_ = w.Append(sample.Sample{T: now.Add(time.Duration(i) * time.Second).UnixMilli(), MemTotal: 8000000, MemAvail: 6000000, RootFree: 40, PSI: sample.PSI{OK: true}})
	}
	_ = w.Sync() // no Close: the machine "lost power"

	last := now.Add(29 * time.Second)
	j := fakeJournal{entries: []journal.Entry{
		{Time: last.Add(-5 * time.Second), Kernel: true, Ident: "kernel", Prio: 3, Msg: "amdgpu 0000:03:00.0: amdgpu: ring gfx_0.0.0 timeout"},
	}}
	opt := Options{
		Config: cfg, Paths: p, Journal: j,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Collector: sample.Options{ProcDir: fakeProc(t, strings.Repeat("b", 32)), SysDir: t.TempDir(), KmsgPath: "-"},
	}
	run(t, opt, 1500*time.Millisecond)

	reports, _ := report.Store{Dir: p.ReportsDir()}.All()
	if len(reports) != 1 {
		t.Fatalf("want 1 report, got %d", len(reports))
	}
	if reports[0].Verdict.ID != "gpu-hang" || reports[0].BootID != prevBoot {
		t.Fatalf("unexpected report: %s for %s", reports[0].Verdict.ID, reports[0].BootID)
	}
	archived, _ := filepath.Glob(filepath.Join(p.BootsDir(), "*.ring"))
	if len(archived) != 1 {
		t.Fatalf("previous ring should be archived, found %v", archived)
	}
	c, err := ring.Read(p.RingFile())
	if err != nil || !c.Header.Clean || c.Header.BootID != strings.Repeat("b", 32) || len(c.Samples) < 3 {
		t.Fatalf("current ring: %v clean=%v samples=%d", err, c.Header.Clean, len(c.Samples))
	}
	if last := c.Samples[len(c.Samples)-1]; last.Flags&sample.FlagStop == 0 {
		t.Fatal("final sample should carry the stop flag")
	}
	for _, s := range c.Samples[:len(c.Samples)-1] {
		if s.Flags&sample.FlagCrashProbe == 0 {
			t.Fatal("every sample should say whether a kernel crash log could be saved")
		}
	}
	if _, err := os.Stat(p.AnalyzingMarker()); !os.IsNotExist(err) {
		t.Fatal("analysis marker should be removed")
	}
}

func TestRestartWithinBootResumes(t *testing.T) {
	state := t.TempDir()
	p := paths.Paths{StateDir: state, RuntimeDir: filepath.Join(state, "run")}
	cfg := config.Default()
	cfg.Interval = 200 * time.Millisecond
	cfg.Flush = 200 * time.Millisecond
	opt := Options{
		Config: cfg, Paths: p, Journal: fakeJournal{},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Collector: sample.Options{ProcDir: fakeProc(t, strings.Repeat("c", 32)), SysDir: t.TempDir(), KmsgPath: "-"},
	}
	run(t, opt, 700*time.Millisecond)
	first, _ := ring.Read(p.RingFile())
	run(t, opt, 700*time.Millisecond)
	second, _ := ring.Read(p.RingFile())
	if len(second.Samples) <= len(first.Samples) {
		t.Fatalf("restart should continue the same ring: %d then %d samples", len(first.Samples), len(second.Samples))
	}
	if reports, _ := (report.Store{Dir: p.ReportsDir()}).IDs(); len(reports) != 0 {
		t.Fatal("a restart within the same boot must not produce a crash report")
	}
}

func TestSecondRecorderIsRefused(t *testing.T) {
	state := t.TempDir()
	unlock, err := lockDir(state)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := lockDir(state); err == nil {
		t.Fatal("second lock should fail")
	}
}
