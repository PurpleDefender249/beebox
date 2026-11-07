// Package scenario builds realistic synthetic evidence for the classic ways
// a Linux machine dies. It powers the test suite and `beebox view --sample`.
package scenario

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
)

// Kinds lists the available scenarios.
func Kinds() []string {
	k := make([]string, 0, len(builders))
	for name := range builders {
		k = append(k, name)
	}
	sort.Strings(k)
	return k
}

// Build returns evidence for the named scenario ending at end.
func Build(kind string, end time.Time) (analyze.Input, error) {
	b, ok := builders[kind]
	if !ok {
		return analyze.Input{}, fmt.Errorf("unknown scenario %q", kind)
	}
	in := b(end)
	in.BootID = "5c0ffee0b0a7d00d8badf00dfeedface"
	in.Kernel = "6.11.0-19-generic"
	in.BootStart = end.Add(-2*time.Hour - 14*time.Minute)
	in.Generator = "beebox scenario"
	in.Now = end.Add(40 * time.Second)
	if in.Ring != nil {
		in.Ring.Header.BootID = in.BootID
		in.Ring.Header.BootStart = in.BootStart
		in.Ring.Header.Kernel = in.Kernel
		in.Ring.Header.Interval = time.Second
		in.Ring.Header.SlotCount = 900
	}
	return in, nil
}

var builders = map[string]func(time.Time) analyze.Input{
	"out-of-memory": oom,
	"power-loss":    powerLoss,
	"overheating":   overheat,
	"battery":       batteryDead,
	"sleep":         diedAsleep,
	"gpu-hang":      gpuHang,
	"sysrq-crash":   sysrqCrash,
	"kernel-panic":  kernelPanic,
	"storage":       storageErrors,
	"clean":         cleanShutdown,
}

var gib = float64(1024 * 1024) // kB per GiB

// base produces n one-second samples ending at end with a calm desktop.
func base(end time.Time, n int, f func(i, n int, s *sample.Sample)) *ring.Contents {
	ss := make([]sample.Sample, 0, n)
	t := end.Add(-time.Duration(n-1) * time.Second)
	for i := 0; i < n; i++ {
		s := sample.Sample{
			T: t.UnixMilli(), Up: int64((2*time.Hour + 14*time.Minute).Milliseconds()),
			CPU: 6 + 4*math.Sin(float64(i)/23), Load1: 0.6, Procs: 412,
			MemTotal: uint64(16 * gib), MemAvail: uint64(9.4 * gib), SwapTotal: uint64(8 * gib), SwapFree: uint64(8 * gib),
			PSI:      sample.PSI{OK: true, CPUSome: 1.2},
			RootFree: 38.5,
			Temps:    []sample.Temp{{Name: "cpu", C: 52 + 3*math.Sin(float64(i)/40), Crit: 100}, {Name: "nvme", C: 41, Crit: 84}},
			TopMem: []sample.App{
				{Name: "firefox", Count: 23, MemKB: uint64(3.1 * gib)},
				{Name: "code", Count: 14, MemKB: uint64(1.3 * gib)},
				{Name: "slack", Count: 6, MemKB: 880 * 1024},
				{Name: "gnome-shell", Count: 1, MemKB: 420 * 1024},
				{Name: "Xwayland", Count: 1, MemKB: 160 * 1024},
			},
			TopCPU: []sample.App{
				{Name: "firefox", Count: 23, CPU: 3.1},
				{Name: "gnome-shell", Count: 1, CPU: 1.4},
				{Name: "code", Count: 14, CPU: 0.8},
			},
		}
		s.LagMs = 3
		if f != nil {
			f(i, n, &s)
		}
		ss = append(ss, s)
		step := time.Second
		if s.LagMs > 1000 {
			step += time.Duration(s.LagMs) * time.Millisecond
		}
		t = t.Add(step)
	}
	// Re-anchor so the last sample lands exactly on end.
	shift := end.UnixMilli() - ss[len(ss)-1].T
	for i := range ss {
		ss[i].T += shift
	}
	return &ring.Contents{Samples: ss}
}

func ramp(i, n, startFromEnd int) float64 {
	k := i - (n - startFromEnd)
	if k <= 0 {
		return 0
	}
	return float64(k) / float64(startFromEnd)
}

func kern(t time.Time, prio int, msg string) journal.Entry {
	return journal.Entry{Time: t, Prio: prio, Ident: "kernel", Kernel: true, Msg: msg}
}

func svc(t time.Time, ident string, prio int, msg string) journal.Entry {
	return journal.Entry{Time: t, Prio: prio, Ident: ident, Msg: msg}
}

func routine(end time.Time) []journal.Entry {
	return []journal.Entry{
		svc(end.Add(-14*time.Minute), "NetworkManager", 6, "<info>  [1730730000.1234] device (wlp2s0): state change: activated"),
		svc(end.Add(-9*time.Minute), "gnome-shell", 4, "Window manager warning: Overwriting existing binding of keysym 31"),
		svc(end.Add(-3*time.Minute), "systemd", 6, "Started session-3.scope - Session 3 of User alex."),
	}
}

func oom(end time.Time) analyze.Input {
	n := 900
	rc := base(end, n, func(i, n int, s *sample.Sample) {
		g := ramp(i, n, 600) // Firefox leak over the last 10 minutes
		ff := 3.1 + 8.6*math.Pow(g, 1.6)
		s.TopMem[0].MemKB = uint64(ff * gib)
		used := ff + 1.3 + 0.86 + 0.41 + 0.16 + 1.0
		avail := 16 - used
		sw := 0.0
		if avail < 1.2 {
			sw = math.Min(7.9, (1.2-avail)*3)
			avail = 0.2 + 0.05*math.Sin(float64(i))
		}
		s.MemAvail = uint64(math.Max(avail, 0.18) * gib)
		s.SwapFree = uint64((8 - sw) * gib)
		stall := ramp(i, n, 200)
		s.PSI.MemSome = math.Min(97, 90*math.Pow(stall, 1.3))
		s.PSI.MemFull = math.Min(83, 76*math.Pow(stall, 1.5))
		s.PSI.IOSome = 40 * stall
		s.PSI.IOFull = 26 * math.Pow(stall, 1.4)
		s.CPU = 8 + 60*stall
		s.MajFault = uint64(9000 * stall)
		s.SwapOut = uint64(12000 * stall)
		s.SwapIn = uint64(8000 * stall)
		if stall > 0.7 {
			s.LagMs = int64(1500 + 5200*(stall-0.7)/0.3)
		}
		s.TopCPU = []sample.App{
			{Name: "firefox", Count: 23, CPU: 6 + 30*stall},
			{Name: "[kswapd0]", Count: 1, CPU: 1 + 24*stall},
			{Name: "gnome-shell", Count: 1, CPU: 1.4 + 6*stall},
		}
		if i == n-24 {
			s.OOMKills = 1
			s.Kmsg = []string{"Out of memory: Killed process 48213 (Isolated Web Co) total-vm:4108172kB, anon-rss:1183232kB, file-rss:0kB, shmem-rss:2048kB, UID:1000 pgtables:5112kB oom_score_adj:167"}
		}
		if i == n-2 {
			s.Kmsg = []string{"sysrq: Emergency Sync"}
		}
		if i == n-1 {
			s.Kmsg = []string{"sysrq: Resetting"}
		}
	})
	last := time.UnixMilli(rc.Samples[len(rc.Samples)-1].T)
	killAt := time.UnixMilli(rc.Samples[len(rc.Samples)-24].T)
	k := []journal.Entry{
		kern(killAt.Add(-1500*time.Millisecond), 4, "Isolated Web Co invoked oom-killer: gfp_mask=0x140cca(GFP_HIGHUSER_MOVABLE|__GFP_COMP), order=0, oom_score_adj=167"),
		kern(killAt.Add(-800*time.Millisecond), 3, "Out of memory: Killed process 48213 (Isolated Web Co) total-vm:4108172kB, anon-rss:1183232kB, file-rss:0kB, shmem-rss:2048kB, UID:1000 pgtables:5112kB oom_score_adj:167"),
		kern(last.Add(-2*time.Second), 6, "sysrq: Emergency Sync"),
		kern(last.Add(-1*time.Second), 6, "sysrq: Resetting"),
	}
	return analyze.Input{Ring: rc, Journal: append(routine(end), k...), KernelLog: k, Env: analyze.Env{PkgManager: "apt"}}
}

func powerLoss(end time.Time) analyze.Input {
	rc := base(end, 900, nil)
	return analyze.Input{Ring: rc, Journal: routine(end), Env: analyze.Env{PkgManager: "apt", OOMProtector: "systemd-oomd", CrashLogs: true}}
}

func overheat(end time.Time) analyze.Input {
	rc := base(end, 900, func(i, n int, s *sample.Sample) {
		h := ramp(i, n, 420)
		s.CPU = 12 + 85*math.Min(1, h*1.4)
		s.Temps[0].C = 55 + 46*h
		s.TopCPU = []sample.App{{Name: "blender", Count: 1, CPU: 10 + 80*h}, {Name: "gnome-shell", Count: 1, CPU: 1.5}}
		if i == n-90 {
			s.Kmsg = []string{"CPU3: Package temperature above threshold, cpu clock throttled (total events = 1132)"}
		}
	})
	return analyze.Input{Ring: rc, Journal: routine(end), Env: analyze.Env{PkgManager: "dnf"}}
}

func batteryDead(end time.Time) analyze.Input {
	ac := false
	rc := base(end, 900, func(i, n int, s *sample.Sample) {
		pct := 12 - int(10*float64(i)/float64(n)) // 12% -> 2%
		s.Battery = &sample.Battery{Pct: pct, Status: "Discharging", PowerW: 14.2}
		s.OnAC = &ac
	})
	return analyze.Input{Ring: rc, Journal: routine(end), Env: analyze.Env{PkgManager: "pacman"}}
}

func diedAsleep(end time.Time) analyze.Input {
	ac := false
	sleepAt := end
	rc := base(end, 600, func(i, n int, s *sample.Sample) {
		s.Battery = &sample.Battery{Pct: 9, Status: "Discharging", PowerW: 7.1}
		s.OnAC = &ac
	})
	j := append(routine(end),
		svc(sleepAt.Add(-3*time.Second), "systemd-sleep", 6, "Entering sleep state 'suspend'..."),
		kern(sleepAt.Add(-2*time.Second), 6, "PM: suspend entry (s2idle)"),
	)
	return analyze.Input{Ring: rc, Journal: j, KernelLog: []journal.Entry{kern(sleepAt.Add(-2*time.Second), 6, "PM: suspend entry (s2idle)")}, Env: analyze.Env{PkgManager: "apt"}}
}

func gpuHang(end time.Time) analyze.Input {
	rc := base(end, 900, func(i, n int, s *sample.Sample) {
		g := 61
		s.GPUBusy = &g
	})
	last := end
	k := []journal.Entry{
		kern(last.Add(-50*time.Second), 3, "amdgpu 0000:03:00.0: amdgpu: ring gfx_0.0.0 timeout, signaled seq=1849122, emitted seq=1849124"),
		kern(last.Add(-50*time.Second), 3, "amdgpu 0000:03:00.0: amdgpu: Process information: process steamwebhelper pid 8812 thread steamwebhe:cs0 pid 8840"),
		kern(last.Add(-49*time.Second), 6, "amdgpu 0000:03:00.0: amdgpu: GPU reset begin!"),
		kern(last.Add(-40*time.Second), 3, "amdgpu 0000:03:00.0: amdgpu: GPU reset(2) failed"),
	}
	return analyze.Input{Ring: rc, Journal: append(routine(end), k...), KernelLog: k, Env: analyze.Env{PkgManager: "pacman"}}
}

func sysrqCrash(end time.Time) analyze.Input {
	rc := base(end, 300, nil)
	k := []journal.Entry{kern(end.Add(-time.Second), 6, "sysrq: Trigger a crash")}
	ps := []pstore.Record{{Path: "/var/lib/systemd/pstore/1730730000/dmesg-efi-173073000001001", Time: end.Add(2 * time.Minute),
		Text: "<6>[ 8040.112233] sysrq: Trigger a crash\n<0>[ 8040.112301] Kernel panic - not syncing: sysrq triggered crash\n<4>[ 8040.112380] CPU: 2 PID: 9120 Comm: bash Not tainted 6.11.0-19-generic\n"}}
	return analyze.Input{Ring: rc, Journal: append(routine(end), k...), KernelLog: k, Pstore: ps, Env: analyze.Env{PkgManager: "apt"}}
}

func kernelPanic(end time.Time) analyze.Input {
	rc := base(end, 900, nil)
	ps := []pstore.Record{{Path: "/var/lib/systemd/pstore/1730730000/dmesg-efi-173073000001001", Time: end.Add(2 * time.Minute),
		Text: "<1>[ 8031.004411] BUG: kernel NULL pointer dereference, address: 0000000000000008\n" +
			"<4>[ 8031.004420] CPU: 5 PID: 2231 Comm: kworker/5:2 Tainted: G           OE      6.11.0-19-generic\n" +
			"<4>[ 8031.004431] RIP: 0010:iwl_mvm_rx_mpdu_mq+0x2a5/0x1e30 [iwlmvm]\n" +
			"<0>[ 8031.004990] Kernel panic - not syncing: Fatal exception in interrupt\n"}}
	return analyze.Input{Ring: rc, Journal: routine(end), Pstore: ps, Env: analyze.Env{PkgManager: "apt"}}
}

func storageErrors(end time.Time) analyze.Input {
	rc := base(end, 900, func(i, n int, s *sample.Sample) {
		st := ramp(i, n, 120)
		s.PSI.IOSome = 90 * st
		s.PSI.IOFull = 72 * st
		if st > 0.6 {
			s.LagMs = 2500
		}
	})
	var k []journal.Entry
	for j := 0; j < 6; j++ {
		t := end.Add(-time.Duration(90-j*12) * time.Second)
		k = append(k, kern(t, 3, fmt.Sprintf("nvme nvme0: I/O %d (Read) QID 3 timeout, aborting", 412+j)))
	}
	k = append(k, kern(end.Add(-20*time.Second), 3, "I/O error, dev nvme0n1, sector 412337152 op 0x0:(READ) flags 0x80700 phys_seg 4 prio class 2"))
	k = append(k, kern(end.Add(-12*time.Second), 2, "EXT4-fs error (device nvme0n1p2): ext4_find_entry:1683: inode #2: comm gnome-shell: reading directory lblock 0"))
	return analyze.Input{Ring: rc, Journal: append(routine(end), k...), KernelLog: k, Env: analyze.Env{PkgManager: "apt"}}
}

func cleanShutdown(end time.Time) analyze.Input {
	rc := base(end, 300, nil)
	rc.Samples[len(rc.Samples)-1].Flags |= sample.FlagStop
	j := append(routine(end), svc(end.Add(time.Second), "systemd", 6, "Reached target System Power Off."))
	in := analyze.Input{Ring: rc, Journal: j, Env: analyze.Env{PkgManager: "apt"}}
	in.Clean = true
	return in
}
