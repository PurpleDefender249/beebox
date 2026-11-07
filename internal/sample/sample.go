// Package sample defines one second of system state and collects it from
// /proc and /sys. Everything recorded here is already visible to any local
// user through /proc; BeeBox never records command-line arguments, file
// contents, window titles or network data.
package sample

// Sample flags.
const (
	FlagStart  uint32 = 1 << iota // first sample written by a recorder process
	FlagStop                      // written during a clean, orderly stop
	FlagResume                    // the system was asleep since the previous sample
	FlagLate                      // the sample was taken much later than scheduled
)

// Sample is one snapshot. JSON keys are short because every sample has to
// fit in a fixed-size ring slot.
type Sample struct {
	T     int64  `json:"t"`               // wall clock, Unix milliseconds
	Up    int64  `json:"up"`              // time since boot incl. sleep, ms (/proc/uptime)
	LagMs int64  `json:"lag,omitempty"`   // how late the sample was vs. schedule
	Flags uint32 `json:"f,omitempty"`     // Flag* bits
	Slept int64  `json:"slept,omitempty"` // ms spent asleep since previous sample

	CPU   float64 `json:"cpu"`   // whole-machine busy %, 0-100
	Load1 float64 `json:"load1"` // 1-minute load average
	Procs int     `json:"procs"` // number of processes

	MemTotal  uint64 `json:"mt"` // kB
	MemAvail  uint64 `json:"ma"` // kB
	SwapTotal uint64 `json:"st"` // kB
	SwapFree  uint64 `json:"sf"` // kB

	PSI PSI `json:"psi"`

	MajFault uint64 `json:"mf,omitempty"`  // major page faults since previous sample
	SwapIn   uint64 `json:"si,omitempty"`  // pages swapped in since previous sample
	SwapOut  uint64 `json:"so,omitempty"`  // pages swapped out since previous sample
	OOMKills uint64 `json:"oom,omitempty"` // kernel OOM kills since previous sample

	RootFree float64 `json:"rf"` // free space on / in percent (-1 if unknown)

	Temps   []Temp   `json:"tmp,omitempty"`
	Battery *Battery `json:"bat,omitempty"`
	OnAC    *bool    `json:"ac,omitempty"`
	GPUBusy *int     `json:"gpu,omitempty"` // percent, amdgpu only

	TopMem []App `json:"tm,omitempty"`
	TopCPU []App `json:"tc,omitempty"`

	Kmsg []string `json:"k,omitempty"` // notable kernel messages since previous sample
}

// PSI holds pressure-stall averages (percent of time stalled, 10 s window).
// "Some" means at least one task was stalled; "Full" means all non-idle
// tasks were stalled at once, i.e. the machine got no useful work done.
type PSI struct {
	CPUSome float64 `json:"cs"`
	MemSome float64 `json:"ms"`
	MemFull float64 `json:"mf"`
	IOSome  float64 `json:"is"`
	IOFull  float64 `json:"if"`
	OK      bool    `json:"ok"` // false when the kernel lacks PSI
}

// Temp is the hottest reading of one sensor group.
type Temp struct {
	Name string  `json:"n"`              // cpu, gpu, nvme, board, ...
	C    float64 `json:"c"`              // degrees Celsius
	Crit float64 `json:"crit,omitempty"` // critical threshold, 0 if unknown
}

// Battery is the main system battery.
type Battery struct {
	Pct    int     `json:"p"`
	Status string  `json:"s"`           // Charging, Discharging, Full, Not charging, Unknown
	PowerW float64 `json:"w,omitempty"` // instantaneous draw/charge in watts
}

// App aggregates all processes that share an executable name.
type App struct {
	Name  string  `json:"n"`
	Count int     `json:"c"`
	MemKB uint64  `json:"m"`   // memory in use: private + swapped out, plus the app's largest shared-memory use
	CPU   float64 `json:"cpu"` // whole-machine %, 0-100
}

// MemAvailPct is available memory as a percent of total.
func (s *Sample) MemAvailPct() float64 {
	if s.MemTotal == 0 {
		return 0
	}
	return float64(s.MemAvail) * 100 / float64(s.MemTotal)
}

// SwapUsedPct is used swap as a percent of total, or -1 without swap.
func (s *Sample) SwapUsedPct() float64 {
	if s.SwapTotal == 0 {
		return -1
	}
	return float64(s.SwapTotal-s.SwapFree) * 100 / float64(s.SwapTotal)
}

// MaxTemp returns the hottest sensor, if any.
func (s *Sample) MaxTemp() (Temp, bool) {
	var best Temp
	found := false
	for _, t := range s.Temps {
		if !found || t.C > best.C {
			best, found = t, true
		}
	}
	return best, found
}
