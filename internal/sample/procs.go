package sample

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

// clockTicks is USER_HZ, which is 100 on every mainstream Linux
// architecture (it is fixed by the kernel ABI, independent of CONFIG_HZ).
const clockTicks = 100

type procKey struct {
	pid   int
	start uint64
}

type procPrev struct {
	ticks       uint64
	name        string
	gen         uint64
	provisional bool // named from comm under memory pressure; retry later
}

// procScanner walks /proc and aggregates processes into apps.
type procScanner struct {
	avoidCmdline atomic.Bool // set under memory pressure; see Collect
	lastScan     time.Time
	procDir      string
	gen          uint64
	prev         map[procKey]*procPrev
	scratch      map[string]*appAcc
}

// appAcc accumulates one app's processes during a scan.
type appAcc struct {
	App
	shmemMax uint64
}

func newProcScanner(procDir string) *procScanner {
	return &procScanner{
		procDir: procDir,
		prev:    make(map[procKey]*procPrev, 512),
		scratch: make(map[string]*appAcc, 128),
	}
}

// scan returns the number of processes and the top apps by memory and CPU.
// ncpu normalises CPU to a whole-machine percentage. The first scan has no
// baseline, so it reports no CPU users.
func (p *procScanner) scan(ncpu, top int) (int, []App, []App) {
	now := time.Now()
	elapsedSec := 0.0
	if !p.lastScan.IsZero() {
		elapsedSec = now.Sub(p.lastScan).Seconds()
	}
	p.lastScan = now
	d, err := os.Open(p.procDir)
	if err != nil {
		return 0, nil, nil
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return 0, nil, nil
	}
	p.gen++
	for k := range p.scratch {
		delete(p.scratch, k)
	}
	count := 0
	for _, n := range names {
		if n == "" || n[0] < '0' || n[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		dir := filepath.Join(p.procDir, n)
		b, err := readFile(dir + "/stat")
		if err != nil {
			continue // process exited between readdir and open
		}
		ps, err := ParsePidStat(b)
		if err != nil {
			continue
		}
		count++
		key := procKey{pid: pid, start: ps.startTime}
		prev, seen := p.prev[key]
		if !seen {
			prev = &procPrev{}
			p.prev[key] = prev
		}
		if !seen || (prev.provisional && !p.avoidCmdline.Load()) {
			prev.name, prev.provisional = p.appName(dir, ps)
		}
		var dt uint64
		if seen && ps.ticks >= prev.ticks {
			dt = ps.ticks - prev.ticks
		}
		prev.ticks = ps.ticks
		prev.gen = p.gen

		// Memory is counted the way the kernel's OOM killer sees it:
		// private memory, including what is swapped out (a thrashing app
		// may have most of its memory in swap), plus shared memory. Shared
		// memory is usually mapped by several processes of the same app
		// (browser tabs, database workers), so only the largest amount in
		// each app is added, rather than counting it once per process.
		var anon, shmem, swap uint64
		if !ps.kernel {
			if sb, err := readFile(dir + "/status"); err == nil {
				anon, shmem, swap = ParsePidStatus(sb)
			}
		}
		a := p.scratch[prev.name]
		if a == nil {
			a = &appAcc{App: App{Name: prev.name}}
			p.scratch[prev.name] = a
		}
		a.Count++
		a.MemKB += anon + swap
		if shmem > a.shmemMax {
			a.shmemMax = shmem
		}
		if elapsedSec > 0 && ncpu > 0 {
			a.CPU += float64(dt) * 100 / (clockTicks * elapsedSec * float64(ncpu))
		}
	}
	for k, v := range p.prev {
		if v.gen != p.gen {
			delete(p.prev, k)
		}
	}
	apps := make([]App, 0, len(p.scratch))
	for _, a := range p.scratch {
		a.CPU = round1(a.CPU)
		a.MemKB += a.shmemMax
		apps = append(apps, a.App)
	}
	return count, topBy(apps, top, func(a App) float64 { return float64(a.MemKB) }),
		topBy(apps, top, func(a App) float64 { return a.CPU })
}

func topBy(apps []App, n int, key func(App) float64) []App {
	sorted := make([]App, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool {
		ki, kj := key(sorted[i]), key(sorted[j])
		if ki != kj {
			return ki > kj
		}
		return sorted[i].Name < sorted[j].Name
	})
	out := sorted[:0]
	for _, a := range sorted {
		if len(out) == n {
			break
		}
		if key(a) <= 0 {
			break
		}
		out = append(out, a)
	}
	return out
}

// appName picks a human-friendly grouping name. It prefers the basename of
// argv[0] (so all Firefox or Chrome helper processes group together) and
// falls back to the kernel's comm. argv[0] values with spaces or colons are
// rejected because programs such as sshd write user names there; no other
// argument is ever read. provisional is true when the command line was
// skipped because of memory pressure, so the name should be looked up again
// once the pressure is gone.
func (p *procScanner) appName(dir string, ps pidStat) (name string, provisional bool) {
	if ps.kernel {
		name := ps.comm
		if i := strings.IndexAny(name, "/:"); i > 0 {
			name = name[:i]
		}
		return "[" + sanitizeName(name) + "]", false
	}
	if p.avoidCmdline.Load() {
		return sanitizeName(ps.comm), true
	}
	if b, err := readFile(dir + "/cmdline"); err == nil && len(b) > 0 {
		argv0 := b
		if i := bytes.IndexByte(b, 0); i >= 0 {
			argv0 = b[:i]
		}
		if len(argv0) > 0 && !bytes.ContainsAny(argv0, " :@") {
			base := filepath.Base(string(argv0))
			if base != "." && base != "/" && base != "" {
				return sanitizeName(base), false
			}
		}
	}
	return sanitizeName(ps.comm), false
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 32 {
			break
		}
		if unicode.IsPrint(r) && r != '"' && r != '\\' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

func round1(x float64) float64 {
	return float64(int64(x*10+0.5)) / 10
}
