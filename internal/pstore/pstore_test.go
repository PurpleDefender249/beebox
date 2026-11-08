package pstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, text string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestFindPstoreAndKdump(t *testing.T) {
	old := trustedUID
	trustedUID = uint32(os.Getuid()) // #nosec G115 -- test only
	t.Cleanup(func() { trustedUID = old })

	since := time.Date(2026, 11, 8, 11, 0, 0, 0, time.UTC)
	after := since.Add(23 * time.Minute)
	ps, crash := t.TempDir(), t.TempDir()

	write(t, filepath.Join(ps, "1762600000/dmesg-efi-176260000001001"), "<0>[ 1.0] Kernel panic - not syncing: test\n", after)
	write(t, filepath.Join(crash, "202611081123/dmesg.202611081123"), "[ 1.0] sysrq: Trigger a crash\n", after)
	write(t, filepath.Join(crash, "127.0.0.1-2026-11-08-11:23:41/vmcore-dmesg.txt"), "[ 2.0] BUG: soft lockup\n", after)
	write(t, filepath.Join(crash, "202611071000/dmesg.202611071000"), "an older crash\n", since.Add(-24*time.Hour))
	write(t, filepath.Join(crash, "_usr_bin_foo.1000.crash"), "apport report, not a kernel log\n", after)
	write(t, filepath.Join(crash, "kexec_cmd"), "kexec -p ...\n", after)

	// A link planted in a world-writable /var/crash must not be followed.
	secret := filepath.Join(t.TempDir(), "secret")
	write(t, secret, "do not copy me\n", after)
	if err := os.MkdirAll(filepath.Join(crash, "202611081124"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(crash, "202611081124", "dmesg.link")); err != nil {
		t.Fatal(err)
	}
	// ...nor a second name for someone else's file.
	if err := os.Link(secret, filepath.Join(crash, "202611081124", "dmesg.hardlink")); err != nil {
		t.Fatal(err)
	}

	recs := Find([]string{ps, crash}, since)
	got := map[string]string{}
	for _, r := range recs {
		got[filepath.Base(r.Path)] = r.Source
		if strings.Contains(r.Text, "do not copy me") {
			t.Fatalf("followed a link: %s", r.Path)
		}
	}
	want := map[string]string{
		"dmesg-efi-176260000001001": "pstore",
		"dmesg.202611081123":        "kdump",
		"vmcore-dmesg.txt":          "kdump",
	}
	if len(got) != len(want) {
		t.Fatalf("found %v, want %v", got, want)
	}
	for name, src := range want {
		if got[name] != src {
			t.Fatalf("%s: source %q, want %q (all: %v)", name, got[name], src, got)
		}
	}
}

func TestFindReadsTheEndOfALongLog(t *testing.T) {
	old := trustedUID
	trustedUID = uint32(os.Getuid()) // #nosec G115 -- test only
	t.Cleanup(func() { trustedUID = old })

	dir := t.TempDir()
	var b strings.Builder
	for b.Len() < 2*maxRead {
		b.WriteString("[ 12.345678] some ordinary boot message that fills the log\n")
	}
	b.WriteString("[ 8040.112301] Kernel panic - not syncing: sysrq triggered crash\n")
	now := time.Now()
	write(t, filepath.Join(dir, "202611081123/dmesg.202611081123"), b.String(), now)

	recs := Find([]string{dir}, now.Add(-time.Minute))
	if len(recs) != 1 {
		t.Fatalf("got %d records", len(recs))
	}
	text := recs[0].Text
	if len(text) > maxRead || !strings.Contains(text, "Kernel panic - not syncing") {
		t.Fatalf("should keep the last %d bytes, with the crash: len %d", maxRead, len(text))
	}
	if !strings.HasPrefix(text, "[ 12.345678]") {
		t.Fatalf("the tail should start at a line boundary: %q", text[:40])
	}
}

func TestKdumpReady(t *testing.T) {
	sys := t.TempDir()
	if kdumpReady(sys) {
		t.Fatal("no file means no kdump")
	}
	if err := os.MkdirAll(filepath.Join(sys, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	for content, want := range map[string]bool{"0\n": false, "1\n": true} {
		if err := os.WriteFile(filepath.Join(sys, "kernel/kexec_crash_loaded"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := kdumpReady(sys); got != want {
			t.Fatalf("kexec_crash_loaded=%q: got %v", content, got)
		}
	}
}

func TestKdumpAttempts(t *testing.T) {
	old := trustedUID
	trustedUID = uint32(os.Getuid()) // #nosec G115 -- test only
	t.Cleanup(func() { trustedUID = old })

	since := time.Now().Add(-time.Hour)
	crash := t.TempDir()
	mk := func(name string, files ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(crash, name), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			write(t, filepath.Join(crash, name, f), "x", time.Now())
		}
	}
	mk("202611081123", "dmesg.202611081123", "dump.202611081123") // saved fine
	mk("202611081130", "dump-incomplete")                         // stopped part-way
	mk("127.0.0.1-2026-11-08-11:40:02")                           // Fedora, nothing saved
	mk("not-a-kdump-folder")
	old2 := filepath.Join(crash, "202611071000")
	mk("202611071000")
	if err := os.Chtimes(old2, since.Add(-time.Hour), since.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	got := KdumpAttempts(crash, since)
	if len(got) != 2 {
		t.Fatalf("want 2 unsaved crashes, got %+v", got)
	}
	names := []string{filepath.Base(got[0].Path), filepath.Base(got[1].Path)}
	for _, want := range []string{"202611081130", "127.0.0.1-2026-11-08-11:40:02"} {
		if names[0] != want && names[1] != want {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
}
