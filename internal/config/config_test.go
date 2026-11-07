package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	c, warns, err := Parse(strings.NewReader(`
# comment
interval = 2s
window = "30m"   # inline comment
top_processes = 3
bogus = 1
keep_reports=abc
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval != 2*time.Second || c.Window != 30*time.Minute || c.TopProcesses != 3 || c.KeepReports != 50 {
		t.Fatalf("unexpected config %+v", c)
	}
	if len(warns) != 2 {
		t.Fatalf("want 2 warnings (unknown key, bad number), got %v", warns)
	}
	if c.Slots() != 900 {
		t.Fatalf("slots = %d", c.Slots())
	}
}

func TestClamp(t *testing.T) {
	c, warns, _ := Parse(strings.NewReader("interval = 1ms\nwindow = 100h\n"))
	if c.Interval != time.Second || c.Window != 15*time.Minute || len(warns) != 2 {
		t.Fatalf("clamp failed: %+v %v", c, warns)
	}
}

func TestMissingFileIsDefault(t *testing.T) {
	c, warns, err := Load(filepath.Join(t.TempDir(), "nope.conf"))
	if err != nil || len(warns) != 0 || c != Default() {
		t.Fatalf("got %+v %v %v", c, warns, err)
	}
}
