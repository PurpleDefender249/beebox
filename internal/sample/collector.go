package sample

import (
	"context"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

// Options configures a Collector. Empty paths mean the real system.
type Options struct {
	ProcDir  string // default /proc
	SysDir   string // default /sys
	RootPath string // filesystem whose free space is tracked; default /
	KmsgPath string // default /dev/kmsg; "-" disables kernel log tailing
	Top      int    // apps kept per list
	// Async moves the slow probes (the per-process scan, temperature
	// sensors, battery, GPU) to a background goroutine. The recorder uses
	// it so that a process stuck in memory reclaim, or a hung disk whose
	// temperature sensor no longer answers, can never stop the
	// once-a-second sample of the core numbers.
	Async bool
}

// slowSnapshot is the result of one run of the slow probes.
type slowSnapshot struct {
	at      time.Time
	procs   int
	topMem  []App
	topCPU  []App
	temps   []Temp
	battery *Battery
	onAC    *bool
	gpuBusy *int
}

// staleAfter is how old a slow snapshot may be and still be attached.
// Keeping slightly stale app lists is deliberate: during a freeze, knowing
// which app was biggest a few seconds earlier is exactly what is needed.
const staleAfter = 60 * time.Second

// Collector produces Samples. Collect must not be called concurrently.
type Collector struct {
	opt   Options
	procs *procScanner
	kmsg  *kmsgReader
	psiOK bool

	// What was found at start-up, for logging; never changed afterwards.
	sensorNames []string
	hasBattery  bool

	// Hardware discovery; owned by whichever goroutine runs the slow probes.
	sensors []sensorGroup
	power   powerSupplies
	gpuPath string
	slowRun uint64

	trigger chan struct{}
	latest  atomic.Pointer[slowSnapshot]

	prevCPU    cpuTimes
	prevVM     vmCounters
	prevWall   time.Time
	prevUptime float64
	primed     bool
	ncpu       atomic.Int64
}

// NewCollector discovers hardware and, if ctx is non-nil, starts tailing
// the kernel log (and, with Options.Async, the slow-probe goroutine).
func NewCollector(ctx context.Context, opt Options) *Collector {
	if opt.ProcDir == "" {
		opt.ProcDir = "/proc"
	}
	if opt.SysDir == "" {
		opt.SysDir = "/sys"
	}
	if opt.RootPath == "" {
		opt.RootPath = "/"
	}
	if opt.KmsgPath == "" {
		opt.KmsgPath = "/dev/kmsg"
	}
	if opt.Top <= 0 {
		opt.Top = 5
	}
	c := &Collector{opt: opt, procs: newProcScanner(opt.ProcDir), trigger: make(chan struct{}, 1)}
	c.ncpu.Store(1)
	c.rediscover()
	for _, s := range c.sensors {
		c.sensorNames = append(c.sensorNames, s.name)
	}
	c.hasBattery = c.power.battery != ""
	c.psiOK = fileExists(filepath.Join(opt.ProcDir, "pressure/memory"))
	if ctx != nil && opt.KmsgPath != "-" {
		c.kmsg = startKmsg(ctx, opt.KmsgPath)
	}
	if opt.Async {
		if ctx == nil {
			ctx = context.Background() //nolint:contextcheck // no caller context: run until the process exits
		}
		go c.slowLoop(ctx)
	}
	return c
}

func (c *Collector) rediscover() {
	c.sensors = discoverSensors(c.opt.SysDir)
	c.power = discoverPower(c.opt.SysDir)
	c.gpuPath = discoverGPUBusy(c.opt.SysDir)
}

// HasPSI reports whether the kernel exposes pressure-stall information.
func (c *Collector) HasPSI() bool { return c.psiOK }

// KernelLogAvailable reports whether /dev/kmsg is being tailed.
func (c *Collector) KernelLogAvailable() bool { return c.kmsg != nil }

// SensorNames lists temperature sensor groups found at start-up.
func (c *Collector) SensorNames() []string { return append([]string(nil), c.sensorNames...) }

// HasBattery reports whether a system battery was found at start-up.
func (c *Collector) HasBattery() bool { return c.hasBattery }

// Prime takes the baseline readings that later deltas are computed from.
func (c *Collector) Prime() {
	c.Collect(time.Now())
	c.primed = true
}

func (c *Collector) slowLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.trigger:
		}
		snap := c.runSlow()
		c.latest.Store(&snap)
	}
}

// runSlow runs every probe that may block: the per-process scan (reading
// another process's command line takes its memory-map lock) and hardware
// sensors (an NVMe temperature read is a command to the drive).
func (c *Collector) runSlow() slowSnapshot {
	c.slowRun++
	if c.slowRun%60 == 0 {
		c.rediscover() // pick up hot-plugged batteries, docks and GPUs
	}
	snap := slowSnapshot{}
	snap.procs, snap.topMem, snap.topCPU = c.procs.scan(int(c.ncpu.Load()), c.opt.Top)
	snap.temps = readSensors(c.sensors)
	snap.battery, snap.onAC = readPower(c.power)
	if c.gpuPath != "" {
		if v, err := readInt(c.gpuPath); err == nil {
			g := int(v)
			snap.gpuBusy = &g
		}
	}
	snap.at = time.Now()
	return snap
}

// Collect takes one sample. now should come from time.Now() so that the
// monotonic reading is preserved for interval maths.
func (c *Collector) Collect(now time.Time) Sample {
	s := Sample{T: now.UnixMilli(), RootFree: -1}

	elapsed := 0.0
	if !c.prevWall.IsZero() {
		elapsed = now.Sub(c.prevWall).Seconds() // monotonic: excludes sleep
	}

	if b, err := readFile(filepath.Join(c.opt.ProcDir, "uptime")); err == nil {
		if up, err := ParseUptime(b); err == nil {
			s.Up = int64(up * 1000)
			if c.prevUptime > 0 && elapsed > 0 {
				// uptime includes sleep, the monotonic clock does not.
				slept := (up - c.prevUptime) - elapsed
				if slept > 2 {
					s.Slept = int64(slept * 1000)
					s.Flags |= FlagResume
				}
			}
			c.prevUptime = up
		}
	}

	if b, err := readFile(filepath.Join(c.opt.ProcDir, "stat")); err == nil {
		if t, n, err := ParseStat(b); err == nil {
			c.ncpu.Store(int64(n))
			if c.prevCPU.total > 0 && t.total > c.prevCPU.total {
				s.CPU = round1(float64(t.busy-c.prevCPU.busy) * 100 / float64(t.total-c.prevCPU.total))
			}
			c.prevCPU = t
		}
	}

	if b, err := readFile(filepath.Join(c.opt.ProcDir, "loadavg")); err == nil {
		s.Load1 = ParseLoadavg(b)
	}
	if b, err := readFile(filepath.Join(c.opt.ProcDir, "meminfo")); err == nil {
		s.MemTotal, s.MemAvail, s.SwapTotal, s.SwapFree = ParseMeminfo(b)
	}
	if b, err := readFile(filepath.Join(c.opt.ProcDir, "vmstat")); err == nil {
		v := ParseVmstat(b)
		if c.primed {
			s.MajFault = sub(v.majFault, c.prevVM.majFault)
			s.SwapIn = sub(v.swapIn, c.prevVM.swapIn)
			s.SwapOut = sub(v.swapOut, c.prevVM.swapOut)
			s.OOMKills = sub(v.oomKill, c.prevVM.oomKill)
		}
		c.prevVM = v
	}
	if c.psiOK {
		s.PSI.OK = true
		readPSI := func(name string) (float64, float64) {
			b, err := readFile(filepath.Join(c.opt.ProcDir, "pressure", name))
			if err != nil {
				return 0, 0
			}
			some, full, _ := ParsePSI(b)
			return some, full
		}
		s.PSI.CPUSome, _ = readPSI("cpu")
		s.PSI.MemSome, s.PSI.MemFull = readPSI("memory")
		s.PSI.IOSome, s.PSI.IOFull = readPSI("io")
	}
	// Under memory pressure, stop reading other processes' command lines:
	// that read waits on the target's memory-map lock, which a thrashing
	// process can hold for a long time.
	c.procs.avoidCmdline.Store(s.PSI.MemSome >= 10 || s.PSI.MemFull >= 2 || (s.MemTotal > 0 && s.MemAvailPct() < 5))

	var st syscall.Statfs_t
	if err := syscall.Statfs(c.opt.RootPath, &st); err == nil && st.Blocks > 0 {
		s.RootFree = round1(float64(st.Bavail) * 100 / float64(st.Blocks))
	}

	var snap *slowSnapshot
	if c.opt.Async {
		select { // start a new slow run unless one is still going
		case c.trigger <- struct{}{}:
		default:
		}
		if l := c.latest.Load(); l != nil && now.Sub(l.at) < staleAfter {
			snap = l
		}
	} else {
		l := c.runSlow()
		snap = &l
	}
	if snap != nil {
		s.Procs, s.TopMem, s.TopCPU = snap.procs, snap.topMem, snap.topCPU
		s.Temps, s.Battery, s.OnAC, s.GPUBusy = snap.temps, snap.battery, snap.onAC, snap.gpuBusy
	}
	s.Kmsg = c.kmsg.drain(8)

	c.prevWall = now
	return s
}

func sub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// BootID returns the current kernel boot ID without dashes, matching the
// format journalctl uses.
func BootID(procDir string) (string, error) {
	if procDir == "" {
		procDir = "/proc"
	}
	s, err := readTrim(filepath.Join(procDir, "sys/kernel/random/boot_id"))
	if err != nil {
		return "", err
	}
	return NormalizeBootID(s), nil
}

// NormalizeBootID lower-cases an ID and strips dashes.
func NormalizeBootID(s string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= '0' && ch <= '9', ch >= 'a' && ch <= 'f':
			out = append(out, ch)
		case ch >= 'A' && ch <= 'F':
			out = append(out, ch+('a'-'A'))
		}
	}
	return string(out)
}

// ValidBootID reports whether s is a 32-character hex boot ID.
func ValidBootID(s string) bool {
	if len(s) != 32 {
		return false
	}
	return NormalizeBootID(s) == s
}

// KernelRelease returns `uname -r`.
func KernelRelease(procDir string) string {
	if procDir == "" {
		procDir = "/proc"
	}
	s, err := readTrim(filepath.Join(procDir, "sys/kernel/osrelease"))
	if err != nil {
		return "unknown"
	}
	return s
}

// BootTime returns when the current boot started, from /proc/stat btime.
func BootTime(procDir string) time.Time {
	if procDir == "" {
		procDir = "/proc"
	}
	b, err := readFile(filepath.Join(procDir, "stat"))
	if err != nil {
		return time.Time{}
	}
	for _, line := range splitLines(b) {
		if len(line) > 6 && line[:6] == "btime " {
			if v, err := strconv.ParseInt(line[6:], 10, 64); err == nil {
				return time.Unix(v, 0)
			}
		}
	}
	return time.Time{}
}

func splitLines(b []byte) []string {
	var out []string
	start := 0
	for i, ch := range b {
		if ch == '\n' {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}
