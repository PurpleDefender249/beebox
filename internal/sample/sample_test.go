package sample

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseMeminfo(t *testing.T) {
	b := []byte("MemTotal:       16303428 kB\nMemFree:          412000 kB\nMemAvailable:    9876543 kB\nSwapTotal:       8388604 kB\nSwapFree:        8000000 kB\n")
	total, avail, st, sf := ParseMeminfo(b)
	if total != 16303428 || avail != 9876543 || st != 8388604 || sf != 8000000 {
		t.Fatalf("got %d %d %d %d", total, avail, st, sf)
	}
}

func TestParseMeminfoOldKernel(t *testing.T) {
	_, avail, _, _ := ParseMeminfo([]byte("MemTotal: 100 kB\nMemFree: 10 kB\nBuffers: 5 kB\nCached: 20 kB\n"))
	if avail != 35 {
		t.Fatalf("estimated avail = %d, want 35", avail)
	}
}

func TestParsePSI(t *testing.T) {
	some, full, err := ParsePSI([]byte("some avg10=12.50 avg60=3.00 avg300=1.00 total=123\nfull avg10=7.25 avg60=1.00 avg300=0.10 total=45\n"))
	if err != nil || some != 12.5 || full != 7.25 {
		t.Fatalf("got %v %v %v", some, full, err)
	}
	if _, _, err := ParsePSI([]byte("garbage")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseStat(t *testing.T) {
	b := []byte("cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 50 0 25 400 25 0 0 0 0 0\ncpu1 50 0 25 400 25 0 0 0 0 0\nbtime 1700000000\n")
	ct, n, err := ParseStat(b)
	if err != nil || n != 2 || ct.total != 1000 || ct.busy != 150 {
		t.Fatalf("got %+v n=%d err=%v", ct, n, err)
	}
}

func TestParsePidStatWithAwkwardName(t *testing.T) {
	// The comm field may contain spaces and parentheses.
	line := "4242 (Web (Content) x) S 1 4242 4242 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 30 0 98765 1000000 2500 18446744073709551615"
	ps, err := ParsePidStat([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if ps.comm != "Web (Content) x" || ps.ticks != 300 || ps.startTime != 98765 || ps.kernel {
		t.Fatalf("got %+v", ps)
	}
}

func TestParseKmsgRecord(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"3,1234,5678,-;Out of memory: Killed process 1 (x)\n SUBSYSTEM=foo", "Out of memory: Killed process 1 (x)", true},
		{"6,1,2,-;PM: suspend entry (s2idle)", "PM: suspend entry (s2idle)", true},
		{"6,1,2,-;usb 1-1: new high-speed USB device", "", false},
		{"no header", "", false},
	}
	for _, c := range cases {
		got, ok := ParseKmsgRecord([]byte(c.in))
		if ok != c.ok || got != c.want {
			t.Errorf("ParseKmsgRecord(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeBootID(t *testing.T) {
	got := NormalizeBootID("821D22C5-0605-4499-9b7e-62edde7e6cc4\n")
	if got != "821d22c5060544999b7e62edde7e6cc4" || !ValidBootID(got) {
		t.Fatalf("got %q", got)
	}
	if ValidBootID("../../etc/passwd") {
		t.Fatal("path accepted as boot id")
	}
}

// TestCollectFromFakeProc runs the collector against a synthetic /proc.
func TestCollectFromFakeProc(t *testing.T) {
	proc := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(proc, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stat", "cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 100 0 50 800 50 0 0 0 0 0\nbtime 1700000000\n")
	write("meminfo", "MemTotal: 1000 kB\nMemAvailable: 250 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	write("loadavg", "1.50 1.00 0.50 2/300 999\n")
	write("uptime", "1234.56 2000.00\n")
	write("vmstat", "pgmajfault 10\npswpin 0\npswpout 0\noom_kill 0\n")
	write("pressure/memory", "some avg10=5.00 avg60=0 avg300=0 total=0\nfull avg10=1.00 avg60=0 avg300=0 total=0\n")
	write("pressure/cpu", "some avg10=1.00 avg60=0 avg300=0 total=0\n")
	write("pressure/io", "some avg10=3.00 avg60=0 avg300=0 total=0\nfull avg10=1.00 avg60=0 avg300=0 total=0\n")
	write("100/stat", "100 (firefox) S 1 100 100 0 -1 4194560 0 0 0 0 10 5 0 0 20 0 1 0 500 0 0")
	write("100/status", "Name:\tx\nRssAnon:\t   2000 kB\nRssShmem:\t       0 kB\nVmSwap:\t       0 kB\n")
	write("100/cmdline", "/usr/lib/firefox/firefox\x00-contentproc\x00secret-arg")
	write("101/stat", "101 (Web Content) S 100 100 100 0 -1 4194560 0 0 0 0 20 5 0 0 20 0 1 0 600 0 0")
	write("101/status", "Name:\tx\nRssAnon:\t   800 kB\nRssShmem:\t       0 kB\nVmSwap:\t       0 kB\n")
	write("101/cmdline", "/usr/lib/firefox/firefox\x00-contentproc")
	write("102/stat", "102 (sshd) S 1 102 102 0 -1 4194560 0 0 0 0 1 1 0 0 20 0 1 0 700 0 0")
	write("102/status", "Name:\tx\nRssAnon:\t   160 kB\nRssShmem:\t       0 kB\nVmSwap:\t       0 kB\n")
	write("102/cmdline", "sshd: alex@pts/0")

	c := NewCollector(context.Background(), Options{ProcDir: proc, SysDir: t.TempDir(), KmsgPath: "-", Top: 5})
	c.Prime()
	s := c.Collect(time.Now())
	if s.MemTotal != 1000 || s.MemAvail != 250 || !s.PSI.OK || s.PSI.MemFull != 1 || s.PSI.IOFull != 1 || s.Load1 != 1.5 {
		t.Fatalf("unexpected sample: %+v", s)
	}
	if s.Procs != 3 {
		t.Fatalf("procs = %d, want 3", s.Procs)
	}
	if len(s.TopMem) == 0 || s.TopMem[0].Name != "firefox" || s.TopMem[0].Count != 2 {
		t.Fatalf("firefox processes should group by argv[0]: %+v", s.TopMem)
	}
	for _, a := range s.TopMem {
		if a.Name == "sshd: alex@pts/0" {
			t.Fatal("argv[0] containing a user name must not be recorded")
		}
	}
	if BootTime(proc).Unix() != 1700000000 {
		t.Fatalf("boot time = %v", BootTime(proc))
	}

	// Under heavy memory pressure the collector stops reading command lines
	// (that read can block on a thrashing process) and uses the kernel's
	// short name, then upgrades the name once the pressure has gone.
	write("pressure/memory", "some avg10=40.00 avg60=0 avg300=0 total=0\nfull avg10=20.00 avg60=0 avg300=0 total=0\n")
	write("103/stat", "103 (Isolated Web) S 100 100 100 0 -1 4194560 0 0 0 0 1 1 0 0 20 0 1 0 800 0 0")
	write("103/status", "Name:\tx\nRssAnon:\t   800 kB\nRssShmem:\t       0 kB\nVmSwap:\t       0 kB\n")
	write("103/cmdline", "/usr/lib/firefox/firefox\x00-contentproc")
	c.Collect(time.Now()) // sees the pressure, sets the flag
	s = c.Collect(time.Now())
	short := false
	for _, a := range s.TopMem {
		short = short || a.Name == "Isolated Web"
	}
	if !short {
		t.Fatalf("under pressure the new process should use its short name: %+v", s.TopMem)
	}
	write("pressure/memory", "some avg10=0.00 avg60=0 avg300=0 total=0\nfull avg10=0.00 avg60=0 avg300=0 total=0\n")
	c.Collect(time.Now())
	s = c.Collect(time.Now())
	if len(s.TopMem) == 0 || s.TopMem[0].Name != "firefox" || s.TopMem[0].Count != 3 {
		t.Fatalf("after the pressure the name should be upgraded: %+v", s.TopMem)
	}
}

// TestAsyncCollectorNeverWaitsForSlowProbes checks that, in recorder mode,
// a sample is returned even before the first process scan has finished,
// and that app lists appear once one has.
func TestAsyncCollectorNeverWaitsForSlowProbes(t *testing.T) {
	proc := t.TempDir()
	for rel, content := range map[string]string{
		"stat":      "cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 100 0 50 800 50 0 0 0 0 0\n",
		"meminfo":   "MemTotal: 1000 kB\nMemAvailable: 500 kB\n",
		"uptime":    "10.0 10.0\n",
		"vmstat":    "pgmajfault 0\n",
		"loadavg":   "0.10 0 0 1/1 1\n",
		"7/stat":    "7 (bash) S 1 7 7 0 -1 4194560 0 0 0 0 1 1 0 0 20 0 1 0 50 0 0",
		"7/status":  "Name:\tbash\nRssAnon:\t 200 kB\nRssShmem:\t 0 kB\nVmSwap:\t 0 kB\n",
		"7/cmdline": "/bin/bash\x00",
	} {
		p := filepath.Join(proc, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewCollector(ctx, Options{ProcDir: proc, SysDir: t.TempDir(), KmsgPath: "-", Async: true})
	c.Prime()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := c.Collect(time.Now())
		if s.MemTotal != 1000 {
			t.Fatalf("core numbers missing: %+v", s)
		}
		if len(s.TopMem) == 1 && s.TopMem[0].Name == "bash" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("app list never arrived: %+v", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestParsePidStatus(t *testing.T) {
	b := []byte("Name:\tstress-ng\nVmRSS:\t  900 kB\nRssAnon:\t  800 kB\nRssFile:\t  50 kB\nRssShmem:\t  50 kB\nVmSwap:\t 3000 kB\n")
	anon, shmem, swap := ParsePidStatus(b)
	if anon != 800 || shmem != 50 || swap != 3000 {
		t.Fatalf("got anon=%d shmem=%d swap=%d", anon, shmem, swap)
	}
	if a, s, w := ParsePidStatus([]byte("Name:\tkthreadd\n")); a+s+w != 0 {
		t.Fatal("kernel thread should have no memory")
	}
}

// An OOM kill prints a long report at warning level; only the lines that
// explain it should be kept, so the "Killed process" line isn't crowded out.
func TestKernelNoiseIsDropped(t *testing.T) {
	keep := []string{
		"python3 invoked oom-killer: gfp_mask=0x100cca(GFP_HIGHUSER_MOVABLE), order=0, oom_score_adj=0",
		"CPU: 1 UID: 0 PID: 2608 Comm: python3 Not tainted 6.18.44 #1 NONE",
		"RIP: 0010:nvme_poll_cq+0x2b/0x2f0 [nvme]",
		"pc : drm_sched_job_timedout+0x40/0x120",
		"Out of memory: Killed process 2608 (python3) total-vm:6326996kB, anon-rss:3200kB",
		"Memory cgroup out of memory: Killed process 2608 (python3) total-vm:6326996kB",
	}
	drop := []string{
		"dump_stack_lvl+0x57/0x80",
		"? alloc_pages_mpol+0x86/0x160",
		"nvme_irq+0x1c/0x40 [nvme]",
		"<TASK>", "</TASK>", "Call Trace:",
		"RSP: 002b:00007ffe38b59790 EFLAGS: 00010202",
		"RAX: 0000000000000000 RBX: 000000000132d5a0 RCX: 00007f315dc00000",
		"R13: 00007f32de160088 R14: 00007f32ddcb0900 R15: 00007f32ddc58d10",
		"x29: ffff800082b3bd30 x28: ffff0000c4a8e000 x27: 0000000000000000",
		"Code: Unable to access opcode bytes at 0x161567e.",
		"oom-kill:constraint=CONSTRAINT_NONE,nodemask=(null),cpuset=/,mems_allowed=0,task_memcg=/user.slice/user-1000.slice",
		"Mem-Info:",
		"Node 0 active_anon:12kB inactive_anon:6245632kB",
		"DMA32 free:3020kB boost:0kB min:3392kB",
		"[   2608]     0  2608  1581749  1562313 12619776        0             0 python3",
		"Hardware name: VMware, Inc. VMware Virtual Platform/440BX Desktop Reference Platform",
	}
	for _, s := range keep {
		if _, ok := ParseKmsgRecord([]byte("4,1,2,-;" + s)); !ok {
			t.Errorf("should keep %q", s)
		}
	}
	for _, s := range drop {
		if _, ok := ParseKmsgRecord([]byte("4,1,2,-;" + s)); ok {
			t.Errorf("should drop %q", s)
		}
	}
}

// Shared memory is counted once per app (its largest use), swapped-out
// memory is counted in full.
func TestAppMemoryCountsSwapAndSharedOnce(t *testing.T) {
	proc := t.TempDir()
	files := map[string]string{
		"10/stat":    "10 (postgres) S 1 10 10 0 -1 4194560 0 0 0 0 1 1 0 0 20 0 1 0 50 0 0",
		"10/status":  "RssAnon:\t 1000 kB\nRssShmem:\t 500000 kB\nVmSwap:\t 0 kB\n",
		"10/cmdline": "postgres\x00",
		"11/stat":    "11 (postgres) S 10 10 10 0 -1 4194560 0 0 0 0 1 1 0 0 20 0 1 0 60 0 0",
		"11/status":  "RssAnon:\t 2000 kB\nRssShmem:\t 400000 kB\nVmSwap:\t 0 kB\n",
		"11/cmdline": "postgres\x00",
		"12/stat":    "12 (hog) S 1 12 12 0 -1 4194560 0 0 0 0 1 1 0 0 20 0 1 0 70 0 0",
		"12/status":  "RssAnon:\t 100000 kB\nRssShmem:\t 0 kB\nVmSwap:\t 900000 kB\n",
		"12/cmdline": "./hog\x00",
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
	ps := newProcScanner(proc)
	_, top, _ := ps.scan(1, 5)
	got := map[string]uint64{}
	for _, a := range top {
		got[a.Name] = a.MemKB
	}
	if got["hog"] != 1000000 {
		t.Errorf("hog = %d kB, want 1000000 (resident + swapped out)", got["hog"])
	}
	if got["postgres"] != 503000 {
		t.Errorf("postgres = %d kB, want 503000 (private + largest shared, not shared twice)", got["postgres"])
	}
	if len(top) == 0 || top[0].Name != "hog" {
		t.Errorf("hog should be the biggest: %+v", top)
	}
}
