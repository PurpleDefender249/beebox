package analyze

import (
	"time"

	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
)

// metrics summarises the final stretch of the recording.
type metrics struct {
	n      int
	psi    bool
	firstT time.Time
	lastT  time.Time
	last   sample.Sample
	stop   bool // final sample was written during a clean stop

	// Final minute.
	maxMemFull, avgMemFull float64
	maxIOFull              float64
	minMemAvail            float64 // percent
	maxSwapUsed            float64 // percent, -1 without swap
	maxLagMs               int64
	majFaults              uint64
	maxTemp                sample.Temp
	tempNearCrit           bool

	// busyFor is how long the processor had been at least 95% busy, without
	// a break, when recording stopped. busyApp is the app using the most
	// CPU during that stretch.
	busyFor time.Duration
	busyApp sample.App

	// Whole recording.
	oomKills    uint64
	lastOOMKill time.Time
	lastResume  time.Time

	// worst is the sample from the final minute with the least available
	// memory that has an app list: the moment that explains a memory
	// crisis. By the end, the biggest app may already have been killed.
	worst sample.Sample

	// Memory growth over the last 10 minutes up to the worst moment, kB,
	// by app.
	growth       map[string]int64
	growthWindow time.Duration
}

// busyStretch measures the unbroken run of samples at the end of the
// recording in which the processor was at least 95% busy. The clean-stop
// sample is ignored (it is taken while the recorder shuts down).
func busyStretch(ss []sample.Sample) (time.Duration, sample.App) {
	end := len(ss) - 1
	if end >= 0 && ss[end].Flags&sample.FlagStop != 0 {
		end--
	}
	if end < 0 || ss[end].CPU < 95 {
		return 0, sample.App{}
	}
	start := end
	cpuBy := map[string]float64{}
	names := map[string]sample.App{}
	for i := end; i >= 0 && ss[i].CPU >= 95; i-- {
		start = i
		for _, a := range ss[i].TopCPU {
			cpuBy[a.Name] += a.CPU
			if _, ok := names[a.Name]; !ok {
				names[a.Name] = a
			}
		}
	}
	var top sample.App
	best := 0.0
	for name, total := range cpuBy {
		if total > best || (total == best && name < top.Name) {
			best, top = total, names[name]
		}
	}
	if n := float64(end - start + 1); n > 0 && top.Name != "" {
		top.CPU = round1(best / n)
	}
	return time.Duration(ss[end].T-ss[start].T) * time.Millisecond, top
}

// recovered reports whether memory was healthy again by the final sample
// after being low earlier in the final minute. Only available memory is
// used: pressure-stall figures are 10-second averages that keep rising for
// a few seconds after the crisis is over.
func (m metrics) recovered() bool {
	if m.n == 0 || m.last.MemTotal == 0 || m.last.T-m.worst.T < 3000 {
		return false
	}
	end := m.last.MemAvailPct()
	return end >= 20 && end >= m.minMemAvail+15
}

const finalWindow = 60 * time.Second

func measure(rc *ring.Contents) metrics {
	m := metrics{minMemAvail: 100, maxSwapUsed: -1, growth: map[string]int64{}}
	if rc == nil || len(rc.Samples) == 0 {
		return m
	}
	ss := rc.Samples
	m.n = len(ss)
	m.last = ss[m.n-1]
	m.firstT = time.UnixMilli(ss[0].T)
	m.lastT = time.UnixMilli(m.last.T)
	m.stop = m.last.Flags&sample.FlagStop != 0
	m.psi = m.last.PSI.OK

	cut := m.last.T - finalWindow.Milliseconds()
	var sumFull float64
	count := 0
	for i := range ss {
		s := &ss[i]
		if s.OOMKills > 0 {
			m.oomKills += s.OOMKills
			m.lastOOMKill = time.UnixMilli(s.T)
		}
		if s.Flags&sample.FlagResume != 0 {
			m.lastResume = time.UnixMilli(s.T)
		}
		if s.T < cut {
			continue
		}
		count++
		sumFull += s.PSI.MemFull
		m.maxMemFull = max(m.maxMemFull, s.PSI.MemFull)
		m.maxIOFull = max(m.maxIOFull, s.PSI.IOFull)
		if s.MemTotal > 0 {
			m.minMemAvail = min(m.minMemAvail, s.MemAvailPct())
			if len(s.TopMem) > 0 && (len(m.worst.TopMem) == 0 || s.MemAvailPct() < m.worst.MemAvailPct()) {
				m.worst = *s
			}
		}
		m.maxSwapUsed = max(m.maxSwapUsed, s.SwapUsedPct())
		m.maxLagMs = max(m.maxLagMs, s.LagMs)
		m.majFaults += s.MajFault
		for _, t := range s.Temps {
			if t.C > m.maxTemp.C {
				m.maxTemp = t
			}
			if t.Crit > 0 && t.C >= t.Crit-3 {
				m.tempNearCrit = true
			}
		}
	}
	if count > 0 {
		m.avgMemFull = sumFull / float64(count)
	}
	m.busyFor, m.busyApp = busyStretch(ss)

	// Growth: compare each app's memory now with the earliest sample in the
	// last 10 minutes where it appeared in the top list.
	gcut := m.last.T - (10 * time.Minute).Milliseconds()
	first := map[string]uint64{}
	var firstAt int64
	for i := range ss {
		s := &ss[i]
		if s.T < gcut {
			continue
		}
		if firstAt == 0 {
			firstAt = s.T
		}
		for _, a := range s.TopMem {
			if _, ok := first[a.Name]; !ok {
				first[a.Name] = a.MemKB
			}
		}
	}
	if len(m.worst.TopMem) == 0 {
		m.worst = lastWithTopMem(ss)
	}
	for _, a := range m.worst.TopMem {
		if base, ok := first[a.Name]; ok {
			m.growth[a.Name] = int64(a.MemKB) - int64(base) // #nosec G115 -- kB values are far below 2^63
		}
	}
	if firstAt > 0 && m.worst.T > firstAt {
		m.growthWindow = time.Duration(m.worst.T-firstAt) * time.Millisecond
	}
	return m
}

func lastWithTopMem(ss []sample.Sample) sample.Sample {
	for i := len(ss) - 1; i >= 0; i-- { // app lists can lag behind the core numbers
		if len(ss[i].TopMem) > 0 {
			return ss[i]
		}
	}
	return ss[len(ss)-1]
}

func round1(x float64) float64 {
	if x < 0 {
		return -float64(int64(-x*10+0.5)) / 10
	}
	return float64(int64(x*10+0.5)) / 10
}
