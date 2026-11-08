package analyze_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
	"github.com/PurpleDefender249/beebox/internal/scenario"
)

var end = time.Date(2026, 11, 4, 23, 11, 0, 0, time.UTC)

func TestScenarioVerdicts(t *testing.T) {
	cases := []struct {
		kind    string
		verdict string
		minConf int
		context string // expected context finding, optional
	}{
		{"out-of-memory", "out-of-memory", 80, "sysrq-restart"},
		{"power-loss", "power-loss", 45, ""},
		{"overheating", "overheating", 60, ""},
		{"battery", "battery-depleted", 80, ""},
		{"sleep", "died-in-sleep", 75, ""},
		{"gpu-hang", "gpu-hang", 60, ""},
		{"sysrq-crash", "kernel-panic", 95, ""},
		{"kernel-panic", "kernel-panic", 90, ""},
		{"storage", "storage-errors", 70, ""},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			in, err := scenario.Build(c.kind, end)
			if err != nil {
				t.Fatal(err)
			}
			r := analyze.Analyze(in)
			if r == nil {
				t.Fatal("expected a report")
			}
			if r.Verdict.ID != c.verdict {
				t.Fatalf("verdict = %s (%q, %d%%), want %s", r.Verdict.ID, r.Verdict.Title, r.Verdict.Confidence, c.verdict)
			}
			if r.Verdict.Confidence < c.minConf {
				t.Fatalf("confidence %d < %d", r.Verdict.Confidence, c.minConf)
			}
			if r.Verdict.Summary == "" || r.Verdict.Title == "" {
				t.Fatal("verdict must explain itself")
			}
			if c.context != "" {
				found := false
				for _, f := range r.Context {
					found = found || f.ID == c.context
				}
				if !found {
					t.Fatalf("missing context finding %s: %+v", c.context, r.Context)
				}
			}
			for _, o := range r.Others {
				if o.ID == r.Verdict.ID {
					t.Fatal("verdict repeated under other findings")
				}
			}
			for i := 1; i < len(r.Verdict.Evidence); i++ {
				if r.Verdict.Evidence[i].Time.Before(r.Verdict.Evidence[i-1].Time) {
					t.Fatal("evidence must be in time order")
				}
			}
			if !strings.HasSuffix(r.ID, in.BootID[:8]) {
				t.Fatalf("report id %q should end with the boot id prefix", r.ID)
			}
		})
	}
}

// Without pstore, a kernel crash leaves no trace and is indistinguishable
// from a power cut, so the verdict must not claim the power was cut.
func TestNoCrashLogsMeansHonestSuddenStop(t *testing.T) {
	in, err := scenario.Build("power-loss", end)
	if err != nil {
		t.Fatal(err)
	}
	in.Env.CrashLogs = false
	r := analyze.Analyze(in)
	if r == nil {
		t.Fatal("expected a report")
	}
	if r.Verdict.ID != "sudden-stop" || strings.Contains(r.Verdict.Title, "power was cut") {
		t.Fatalf("verdict = %s %q; want an honest sudden-stop", r.Verdict.ID, r.Verdict.Title)
	}
	if !strings.Contains(r.Verdict.Summary, "kernel crash") {
		t.Fatalf("summary should say a kernel crash looks the same: %q", r.Verdict.Summary)
	}
	hasDoctor := false
	for _, a := range r.Verdict.Advice {
		hasDoctor = hasDoctor || a.Command == "beebox doctor"
	}
	if !hasDoctor {
		t.Fatal("advice should point to beebox doctor to enable crash reports")
	}
}

func TestCleanShutdownProducesNoReport(t *testing.T) {
	in, _ := scenario.Build("clean", end)
	if r := analyze.Analyze(in); r != nil {
		t.Fatalf("clean shutdown produced a report: %s", r.Verdict.Title)
	}
}

func TestCleanThermalShutdownIsReported(t *testing.T) {
	in, _ := scenario.Build("clean", end)
	in.KernelLog = append(in.KernelLog, journal.Entry{Time: end.Add(-10 * time.Second), Kernel: true, Ident: "kernel", Prio: 0,
		Msg: "thermal thermal_zone0: critical temperature reached (105 C), shutting down"})
	r := analyze.Analyze(in)
	if r == nil || r.Verdict.ID != "overheating" || !strings.Contains(r.Verdict.Title, "protect itself") {
		t.Fatalf("expected an overheating shutdown report, got %+v", r)
	}
}

func TestJournalOnlyAnalysis(t *testing.T) {
	in, _ := scenario.Build("gpu-hang", end)
	in.Ring = nil
	r := analyze.Analyze(in)
	if r == nil || r.Verdict.ID != "gpu-hang" {
		t.Fatalf("journal-only analysis failed: %+v", r)
	}
	if len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "not running") {
		t.Fatalf("expected a note about the missing recording, got %v", r.Notes)
	}
}

func TestNoEvidenceIsHonest(t *testing.T) {
	r := analyze.Analyze(analyze.Input{BootID: strings.Repeat("0", 32), Now: end})
	if r == nil || r.Verdict.ID != "unknown" || r.Verdict.Confidence != 0 {
		t.Fatalf("expected an honest unknown verdict, got %+v", r)
	}
}

func TestRedactsPersonalDetails(t *testing.T) {
	in, _ := scenario.Build("storage", end)
	in.KernelLog = append(in.KernelLog, journal.Entry{Time: end.Add(-5 * time.Second), Kernel: true, Ident: "kernel", Prio: 3,
		Msg: "EXT4-fs error (device nvme0n1p2): /home/alex/secret.txt from 192.168.1.20 aa:bb:cc:dd:ee:ff"})
	r := analyze.Analyze(in)
	for _, e := range r.Verdict.Evidence {
		if strings.Contains(e.Text, "alex") || strings.Contains(e.Text, "192.168.1.20") || strings.Contains(e.Text, "aa:bb") {
			t.Fatalf("personal detail leaked into evidence: %q", e.Text)
		}
	}
}

func TestAdviceUsesDistroCommands(t *testing.T) {
	in, _ := scenario.Build("kernel-panic", end)
	in.Env.PkgManager = "dnf"
	r := analyze.Analyze(in)
	found := false
	for _, a := range r.Verdict.Advice {
		found = found || strings.Contains(a.Command, "dnf upgrade")
	}
	if !found {
		t.Fatalf("expected a dnf command in advice: %+v", r.Verdict.Advice)
	}
}

// stormRing builds a recording of a memory storm in which "hog" grows until
// it is killed. If recover is set the system is healthy for the last 10 s.
func stormRing(recover bool) *ring.Contents {
	const n = 120
	t0 := end.Add(-n * time.Second).UnixMilli()
	rc := &ring.Contents{Header: ring.Header{BootID: strings.Repeat("cd", 16), Interval: time.Second}}
	for i := 0; i < n; i++ {
		s := sample.Sample{T: t0 + int64(i)*1000, MemTotal: 8 << 20, MemAvail: 6 << 20, CPU: 5}
		s.PSI.OK = true
		s.TopMem = []sample.App{{Name: "gnome-shell", Count: 1, MemKB: 300 << 10}}
		storm := i >= 60 && (!recover || i < n-10)
		if i >= 60 {
			k := float64(i-60) / 50
			if k > 1 {
				k = 1
			}
			s.TopMem = []sample.App{{Name: "hog", Count: 4, MemKB: uint64(7.5 * k * (1 << 20))}, {Name: "gnome-shell", Count: 1, MemKB: 300 << 10}}
			s.MemAvail = uint64((1 - 0.98*k) * 8 * (1 << 20))
			s.PSI.MemSome, s.PSI.MemFull = 60*k, 45*k
		}
		if recover && !storm {
			if i >= n-10 {
				s.TopMem = []sample.App{{Name: "gnome-shell", Count: 1, MemKB: 300 << 10}}
				s.MemAvail = 7 << 20
				s.PSI.MemSome, s.PSI.MemFull = 3, 1
			}
		}
		if recover && i == n-10 {
			s.OOMKills = 1
			s.Kmsg = []string{"Out of memory: Killed process 4242 (hog) total-vm:8000000kB, anon-rss:7800000kB"}
		}
		rc.Samples = append(rc.Samples, s)
	}
	return rc
}

func TestOOMNamesTheAppAtTheWorstMoment(t *testing.T) {
	rc := stormRing(false)
	r := analyze.Analyze(analyze.Input{Ring: rc, BootID: rc.Header.BootID, Env: analyze.Env{PkgManager: "apt"}})
	if r == nil || r.Verdict.ID != "out-of-memory" {
		t.Fatalf("want out-of-memory, got %+v", r)
	}
	if !strings.Contains(r.Verdict.Summary, "biggest user was hog") || !strings.Contains(r.Verdict.Summary, "did not recover") {
		t.Fatalf("summary: %q", r.Verdict.Summary)
	}
}

func TestOOMThatRecoveredSaysSo(t *testing.T) {
	rc := stormRing(true)
	r := analyze.Analyze(analyze.Input{Ring: rc, BootID: rc.Header.BootID, Env: analyze.Env{PkgManager: "apt"}})
	if r == nil {
		t.Fatal("expected a report")
	}
	var f report.Finding
	switch {
	case r.Verdict.ID == "out-of-memory":
		f = r.Verdict
	default:
		for _, c := range r.Context {
			if c.ID == "low-memory-earlier" {
				f = c
			}
		}
	}
	if f.ID == "" {
		t.Fatalf("memory crisis not reported: verdict %s, context %+v", r.Verdict.ID, r.Context)
	}
	if strings.Contains(f.Summary, "did not recover") || !strings.Contains(f.Summary, "recovered") {
		t.Fatalf("summary should say memory recovered: %q", f.Summary)
	}
	if !strings.Contains(f.Summary, "biggest user was hog") {
		t.Fatalf("the killed app should still be named: %q", f.Summary)
	}
	if r.TopMemAt.IsZero() || len(r.TopMem) == 0 || r.TopMem[0].Name != "hog" {
		t.Fatalf("app list should come from the worst moment: at=%v %+v", r.TopMemAt, r.TopMem)
	}
}

// Reproduces a VM test report: the processor was pegged for minutes, memory
// was fine, then the VM was powered off. The report must not call the CPU
// normal, and must say what kept it busy.
func TestBusyCPUThenSuddenStop(t *testing.T) {
	const n = 330
	t0 := end.Add(-n * time.Second).UnixMilli()
	rc := &ring.Contents{Header: ring.Header{BootID: strings.Repeat("ef", 16), Interval: time.Second}}
	for i := 0; i < n; i++ {
		s := sample.Sample{T: t0 + int64(i)*1000, MemTotal: 4 << 20, MemAvail: 1500 << 10, CPU: 8, RootFree: 40}
		s.PSI.OK = true
		s.TopMem = []sample.App{{Name: "x-www-browser", Count: 6, MemKB: 188 << 10}}
		if i >= n-210 {
			s.CPU = 100
			s.TopCPU = []sample.App{{Name: "stress-ng", Count: 4, CPU: 97}, {Name: "Xorg", Count: 1, CPU: 2}}
			// Real recordings dip for a second now and then (a sample at
			// 94.7% hid three busy minutes in a report from Kali).
			if i == n-8 || i == n-120 || i == n-121 {
				s.CPU = 86
			}
		}
		rc.Samples = append(rc.Samples, s)
	}
	r := analyze.Analyze(analyze.Input{Ring: rc, BootID: rc.Header.BootID, Env: analyze.Env{PkgManager: "apt"}})
	if r == nil || r.Verdict.ID != "sudden-stop" {
		t.Fatalf("want sudden-stop, got %v", r != nil && r.Verdict.ID != "")
	}
	if strings.Contains(r.Verdict.Summary, "CPU all looked normal") || !strings.Contains(r.Verdict.Summary, "processor was fully busy") {
		t.Fatalf("summary misdescribes the processor: %q", r.Verdict.Summary)
	}
	var busy *report.Finding
	for i := range r.Context {
		if r.Context[i].ID == "busy-cpu" {
			busy = &r.Context[i]
		}
	}
	if busy == nil || !strings.Contains(busy.Summary, "stress-ng") || !strings.Contains(busy.Summary, "3 min") {
		t.Fatalf("missing or wrong busy-cpu finding: %+v", r.Context)
	}
	if len(r.TopMem) == 0 || analyze.DisplayName(r.TopMem[0].Name) != "Web browser" {
		t.Fatalf("x-www-browser should display as Web browser: %+v", r.TopMem)
	}
}

// systemd-oomd is a separate package on the Debian family, and Kali leaves
// new services disabled, so the command must install and then enable it.
func TestOOMProtectorCommand(t *testing.T) {
	for _, c := range []struct {
		env  analyze.Env
		want string
	}{
		{analyze.Env{PkgManager: "apt"}, "sudo apt install -y systemd-oomd && sudo systemctl enable --now systemd-oomd"},
		{analyze.Env{PkgManager: "apt", HasOOMD: true}, "sudo systemctl enable --now systemd-oomd"},
		{analyze.Env{PkgManager: "dnf"}, "sudo dnf install -y systemd-oomd-defaults && sudo systemctl enable --now systemd-oomd"},
		{analyze.Env{PkgManager: "pacman"}, "sudo systemctl enable --now systemd-oomd"},
	} {
		if got := c.env.OOMProtectorCommand(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.env, got, c.want)
		}
	}
}

// kdump saves the crashed kernel's log on computers without pstore (legacy
// BIOS). Its file has no "<level>" prefixes and holds the whole log; the
// journal usually lost the last lines.
func TestKdumpLogNamesAKernelCrash(t *testing.T) {
	in, err := scenario.Build("sysrq-crash", end)
	if err != nil {
		t.Fatal(err)
	}
	in.Journal, in.KernelLog = in.Journal[:0], nil
	in.Env.CrashLogs = true
	in.Pstore = []pstore.Record{{
		Path:   "/var/crash/202611081123/dmesg.202611081123",
		Time:   end.Add(40 * time.Second),
		Source: "kdump",
		// As makedumpfile writes it, with the kernel's caller IDs.
		Text: "[    0.000000] [      T0] Linux version 6.19.14+kali-amd64\n" +
			"[ 8040.112233] [   T9120] sysrq: Trigger a crash\n" +
			"[ 8040.112301] [   T9120] Kernel panic - not syncing: sysrq triggered crash\n" +
			"[ 8040.112380] [   T9120] CPU: 2 PID: 9120 Comm: bash Not tainted 6.19.14+kali-amd64\n",
	}}
	r := analyze.Analyze(in)
	if r == nil || r.Verdict.ID != "kernel-panic" || r.Verdict.Confidence < 95 || !strings.Contains(r.Verdict.Title, "on purpose") {
		t.Fatalf("want a deliberate kernel crash from the kdump log, got %+v", r.Verdict)
	}
	fromKdump := false
	seen := map[string]bool{}
	for _, l := range r.Verdict.Evidence {
		fromKdump = fromKdump || l.Source == "kdump"
		if strings.Contains(l.Text, "T9120") || seen[l.Text] {
			t.Fatalf("evidence should be clean and listed once: %+v", r.Verdict.Evidence)
		}
		seen[l.Text] = true
	}
	if !fromKdump || !r.Sources.Pstore {
		t.Fatalf("evidence should come from kdump: %+v", r.Verdict.Evidence)
	}
}

// The recording says whether the boot that ended could save a crash log;
// that wins over what the computer can do at the time of the analysis.
func TestCrashLogsFromTheRecording(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       bool
		flags     uint32
		wantPower bool
	}{
		{"kdump loaded then, off now", false, sample.FlagCrashProbe | sample.FlagCrashLog, true},
		{"no crash log then, kdump now", true, sample.FlagCrashProbe, false},
		{"old recording: use what is known now", true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := scenario.Build("power-loss", end)
			if err != nil {
				t.Fatal(err)
			}
			in.Env.CrashLogs = tc.env
			for i := range in.Ring.Samples {
				in.Ring.Samples[i].Flags |= tc.flags
			}
			r := analyze.Analyze(in)
			if got := r.Verdict.ID == "power-loss"; got != tc.wantPower {
				t.Fatalf("verdict %s (%q), want power-loss=%v", r.Verdict.ID, r.Verdict.Title, tc.wantPower)
			}
		})
	}
}

// kdump started after the crash but saved no log (too little memory, a
// kernel too new for makedumpfile, or switched off part-way): still a
// kernel crash, not a power cut.
func TestKdumpStartedButSavedNoLog(t *testing.T) {
	in, err := scenario.Build("power-loss", end)
	if err != nil {
		t.Fatal(err)
	}
	in.Env.CrashLogs = true
	in.KdumpAttempts = []pstore.Attempt{{Path: "/var/crash/202611081130", Time: end.Add(30 * time.Second), Files: []string{"dump-incomplete"}}}
	r := analyze.Analyze(in)
	if r == nil || r.Verdict.ID != "kernel-crash-unsaved" {
		t.Fatalf("want kernel-crash-unsaved, got %+v", r.Verdict)
	}
	for _, o := range r.Others {
		if o.ID == "power-loss" || o.ID == "sudden-stop" {
			t.Fatalf("a kernel crash is not a power cut: %+v", r.Others)
		}
	}
	hasDoctor := false
	for _, a := range r.Verdict.Advice {
		hasDoctor = hasDoctor || a.Command == "beebox doctor"
	}
	if !hasDoctor {
		t.Fatal("advice should point to beebox doctor")
	}
}
