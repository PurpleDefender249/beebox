package sample

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
)

// readFile reads a small pseudo-file. /proc and /sys files report size 0,
// so os.ReadFile's size hint is useless; a fixed buffer avoids regrowth.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- fixed /proc and /sys paths
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	buf.Grow(4096)
	_, err = buf.ReadFrom(f)
	return buf.Bytes(), err
}

func readTrim(path string) (string, error) {
	b, err := readFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func readInt(path string) (int64, error) {
	s, err := readTrim(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

// ParseMeminfo extracts the fields BeeBox needs from /proc/meminfo (kB).
func ParseMeminfo(b []byte) (total, avail, swapTotal, swapFree uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	var free, buffers, cached uint64
	haveAvail := false
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = v
		case "MemAvailable":
			avail, haveAvail = v, true
		case "MemFree":
			free = v
		case "Buffers":
			buffers = v
		case "Cached":
			cached = v
		case "SwapTotal":
			swapTotal = v
		case "SwapFree":
			swapFree = v
		}
	}
	if !haveAvail { // kernels before 3.14
		avail = free + buffers + cached
	}
	return
}

// ParsePSI returns avg10 for the "some" and "full" lines of a
// /proc/pressure/* file.
func ParsePSI(b []byte) (some, full float64, err error) {
	found := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		for _, kv := range f[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k != "avg10" {
				continue
			}
			x, perr := strconv.ParseFloat(v, 64)
			if perr != nil {
				continue
			}
			switch f[0] {
			case "some":
				some, found = x, true
			case "full":
				full = x
			}
		}
	}
	if !found {
		return 0, 0, errors.New("no avg10 in pressure file")
	}
	return some, full, nil
}

// cpuTimes is the aggregate line of /proc/stat.
type cpuTimes struct {
	busy, total uint64
}

// ParseStat returns aggregate CPU times and the number of CPUs.
func ParseStat(b []byte) (cpuTimes, int, error) {
	var t cpuTimes
	n := 0
	found := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		f := strings.Fields(line)
		if f[0] != "cpu" {
			n++
			continue
		}
		var vals [10]uint64
		for i := 1; i < len(f) && i <= 10; i++ {
			vals[i-1], _ = strconv.ParseUint(f[i], 10, 64)
		}
		// user nice system idle iowait irq softirq steal guest guest_nice
		// guest time is already included in user/nice, so stop at steal.
		var total uint64
		for i := 0; i < 8; i++ {
			total += vals[i]
		}
		idle := vals[3] + vals[4]
		t = cpuTimes{busy: total - idle, total: total}
		found = true
	}
	if !found {
		return t, 0, errors.New("no cpu line in /proc/stat")
	}
	if n == 0 {
		n = 1
	}
	return t, n, nil
}

// vmCounters are cumulative counters from /proc/vmstat.
type vmCounters struct {
	majFault, swapIn, swapOut, oomKill uint64
}

// ParseVmstat extracts the counters BeeBox tracks.
func ParseVmstat(b []byte) vmCounters {
	var v vmCounters
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, val, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "pgmajfault":
			v.majFault = n
		case "pswpin":
			v.swapIn = n
		case "pswpout":
			v.swapOut = n
		case "oom_kill":
			v.oomKill = n
		}
	}
	return v
}

// ParseLoadavg returns the 1-minute load average.
func ParseLoadavg(b []byte) float64 {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	x, _ := strconv.ParseFloat(f[0], 64)
	return x
}

// ParseUptime returns seconds since boot including time asleep.
func ParseUptime(b []byte) (float64, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, errors.New("empty /proc/uptime")
	}
	return strconv.ParseFloat(f[0], 64)
}

// pidStat is the subset of /proc/<pid>/stat BeeBox uses.
type pidStat struct {
	comm      string
	ticks     uint64 // utime + stime
	startTime uint64 // clock ticks after boot; distinguishes reused PIDs
	kernel    bool   // kernel thread (PF_KTHREAD)
}

const pfKthread = 0x00200000

// ParsePidStat parses /proc/<pid>/stat. The command name is wrapped in
// parentheses and may itself contain spaces or parentheses, so fields are
// counted from the last ')'.
func ParsePidStat(b []byte) (pidStat, error) {
	var ps pidStat
	open := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if open < 0 || end < open || end+2 > len(b) {
		return ps, errors.New("malformed stat")
	}
	ps.comm = string(b[open+1 : end])
	f := strings.Fields(string(b[end+2:]))
	// f[0] is field 3 (state); field N is f[N-3].
	if len(f) < 20 {
		return ps, errors.New("short stat")
	}
	flags, _ := strconv.ParseUint(f[6], 10, 64) // field 9
	ut, _ := strconv.ParseUint(f[11], 10, 64)   // field 14
	st, _ := strconv.ParseUint(f[12], 10, 64)   // field 15
	start, _ := strconv.ParseUint(f[19], 10, 64)
	ps.ticks = ut + st
	ps.startTime = start
	ps.kernel = flags&pfKthread != 0
	return ps, nil
}

// ParsePidStatus extracts, in kB, a process's private resident memory
// (RssAnon), its resident shared memory (RssShmem: tmpfs, memfd and shared
// anonymous mappings) and how much of it is swapped out (VmSwap) from
// /proc/<pid>/status. Kernel threads have none of these fields.
func ParsePidStatus(b []byte) (anon, shmem, swap uint64) {
	for len(b) > 0 {
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		var dst *uint64
		switch {
		case bytes.HasPrefix(line, []byte("RssAnon:")):
			dst = &anon
		case bytes.HasPrefix(line, []byte("RssShmem:")):
			dst = &shmem
		case bytes.HasPrefix(line, []byte("VmSwap:")):
			dst = &swap
		default:
			continue
		}
		f := bytes.Fields(line)
		if len(f) >= 2 {
			*dst, _ = strconv.ParseUint(string(f[1]), 10, 64)
		}
	}
	return anon, shmem, swap
}
