package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
	"github.com/PurpleDefender249/beebox/internal/scenario"
	"github.com/PurpleDefender249/beebox/internal/term"
)

func testApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	var out, errb bytes.Buffer
	a := &App{
		Stdout: &out, Stderr: &errb,
		Paths: paths.Paths{StateDir: dir, RuntimeDir: filepath.Join(dir, "run"), ConfigFile: filepath.Join(dir, "beebox.conf")},
		Style: term.Style{Plain: true}, Width: 80,
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return a, &out, &errb
}

func saveScenario(t *testing.T, a *App, kind string, end time.Time) *report.Report {
	t.Helper()
	in, err := scenario.Build(kind, end)
	if err != nil {
		t.Fatal(err)
	}
	r := analyze.Analyze(in)
	if _, err := a.store().Save(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHelpAndUsageErrors(t *testing.T) {
	a, out, errb := testApp(t)
	if code := a.Run(context.Background(), nil); code != ExitOK || !strings.Contains(out.String(), "beebox <command>") {
		t.Fatalf("help: code %d, %q", code, out.String())
	}
	if code := a.Run(context.Background(), []string{"lsit"}); code != ExitUsage || !strings.Contains(errb.String(), "beebox list") {
		t.Fatalf("typo: code %d, %q", code, errb.String())
	}
	errb.Reset()
	if code := a.Run(context.Background(), []string{"show", "--nope"}); code != ExitUsage {
		t.Fatalf("bad flag: code %d", code)
	}
	out.Reset()
	if code := a.Run(context.Background(), []string{"help", "analyze"}); code != ExitOK || !strings.Contains(out.String(), "--boot") {
		t.Fatalf("help analyze: %d %q", code, out.String())
	}
}

func TestNoReportsIsFriendly(t *testing.T) {
	a, out, _ := testApp(t)
	if code := a.Run(context.Background(), []string{"last"}); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "No crash reports yet") {
		t.Fatalf("got %q", out.String())
	}
	out.Reset()
	a.Run(context.Background(), []string{"last", "--json"})
	if strings.TrimSpace(out.String()) != "null" {
		t.Fatalf("json with no reports: %q", out.String())
	}
}

func TestLastShowList(t *testing.T) {
	a, out, _ := testApp(t)
	end := time.Date(2026, 11, 4, 23, 11, 0, 0, time.UTC)
	saveScenario(t, a, "battery", end.Add(-48*time.Hour))
	r := saveScenario(t, a, "out-of-memory", end)

	if code := a.Run(context.Background(), []string{"last"}); code != ExitOK || !strings.Contains(out.String(), "ran out of memory") {
		t.Fatalf("last: %d %q", code, out.String())
	}
	out.Reset()
	if code := a.Run(context.Background(), []string{"show", r.ID[:15], "--json"}); code != ExitOK {
		t.Fatalf("show prefix: %d", code)
	}
	var got report.Report
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.ID != r.ID {
		t.Fatalf("show --json: %v %q", err, got.ID)
	}
	out.Reset()
	a.Run(context.Background(), []string{"list", "--ids"})
	ids := strings.Fields(out.String())
	if len(ids) != 2 || ids[0] != r.ID {
		t.Fatalf("list --ids should be newest first: %v", ids)
	}
	out.Reset()
	html := filepath.Join(t.TempDir(), "r.html")
	if code := a.Run(context.Background(), []string{"show", "--html", html}); code != ExitOK {
		t.Fatalf("show --html: %d", code)
	}
	if b, err := os.ReadFile(html); err != nil || !bytes.Contains(b, []byte("<!DOCTYPE html>")) {
		t.Fatalf("html file: %v", err)
	}
}

func TestStatusNotRecordingExitCode(t *testing.T) {
	a, out, _ := testApp(t)
	if code := a.Run(context.Background(), []string{"status"}); code != ExitNotRecording {
		t.Fatalf("status code = %d, want %d", code, ExitNotRecording)
	}
	if !strings.Contains(out.String(), "not recording") {
		t.Fatalf("got %q", out.String())
	}
}

func TestViewSamplePrintPath(t *testing.T) {
	a, out, _ := testApp(t)
	if code := a.Run(context.Background(), []string{"view", "--sample", "--print-path"}); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	path := strings.TrimSpace(out.String())
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(b, []byte("Sample report")) {
		t.Fatalf("sample page: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("cached page should be private, mode %v", info.Mode())
	}
}

func TestCompletionScriptsAreValid(t *testing.T) {
	a, out, _ := testApp(t)
	for _, sh := range []string{"bash", "zsh", "fish"} {
		out.Reset()
		if code := a.Run(context.Background(), []string{"completion", sh}); code != ExitOK || out.Len() == 0 {
			t.Fatalf("%s completion failed", sh)
		}
		if bin, err := exec.LookPath(sh); err == nil {
			f := filepath.Join(t.TempDir(), "c."+sh)
			if err := os.WriteFile(f, out.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			flag := "-n"
			if sh == "fish" {
				flag = "--no-execute"
			}
			if b, err := exec.Command(bin, flag, f).CombinedOutput(); err != nil { // #nosec G204 -- test
				t.Fatalf("%s syntax error: %v\n%s", sh, err, b)
			}
		}
	}
}

func TestSuggest(t *testing.T) {
	for in, want := range map[string]string{"lsit": "list", "stauts": "status", "docter": "doctor", "zzzzzz": ""} {
		if got := suggest(in); got != want {
			t.Errorf("suggest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDumpPrintsRawSamples(t *testing.T) {
	a, out, errb := testApp(t)
	if code := a.Run(context.Background(), []string{"dump"}); code == ExitOK {
		t.Fatal("dump with no recordings should fail")
	}
	if err := os.MkdirAll(a.Paths.BootsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := ring.Create(filepath.Join(a.Paths.BootsDir(), "20261107-150000-abcdef12.ring"), ring.Header{SlotCount: 10, Interval: time.Second, BootID: strings.Repeat("ab", 16)})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		s := sample.Sample{T: int64(1762527600000 + i*1000), CPU: 10, MemTotal: 1000, MemAvail: 100}
		s.TopMem = []sample.App{{Name: "stress-ng", Count: 4, MemKB: 3 << 20}}
		if i == 4 {
			s.OOMKills = 1
			s.Kmsg = []string{"Out of memory: Killed process 42 (stress-ng)"}
		}
		if err := w.Append(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(false); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := a.Run(context.Background(), []string{"dump", "--last", "3"}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"5 readable, 0 corrupt; showing 3", "stress-ng 3.0 GiB", "killed for memory", "kernel: Out of memory"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dump output missing %q:\n%s", want, got)
		}
	}
	// Hidden from help.
	out.Reset()
	a.Run(context.Background(), []string{"help"})
	if strings.Contains(out.String(), "dump") {
		t.Fatal("dump should not be listed in help")
	}
}
