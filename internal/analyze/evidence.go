package analyze

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/report"
)

// logLine is a normalised message from any evidence source.
type logLine struct {
	Time   time.Time
	Source string // kernel, journal, pstore, recorder
	Ident  string
	Text   string
	Prio   int
}

func (l logLine) toEvidence() report.Line {
	text := l.Text
	if l.Ident != "" && l.Ident != "kernel" && l.Source == "journal" {
		text = l.Ident + ": " + text
	}
	return report.Line{Time: l.Time, Source: l.Source, Text: redact(text)}
}

// collectLines merges journal, kernel log, ring kernel messages and pstore
// into one time-ordered, de-duplicated list.
func collectLines(in *Input) []logLine {
	var out []logLine
	// The same message can arrive from several sources (journal and the
	// ring's kernel tail) a moment apart; repeats further apart are kept.
	seen := map[string][]time.Time{}
	add := func(l logLine) {
		l.Text = strings.TrimSpace(l.Text)
		if l.Text == "" {
			return
		}
		for _, t := range seen[l.Text] {
			d := l.Time.Sub(t)
			if d < 0 {
				d = -d
			}
			if d <= 3*time.Second {
				return
			}
		}
		seen[l.Text] = append(seen[l.Text], l.Time)
		out = append(out, l)
	}
	addEntries := func(es []journal.Entry) {
		for _, e := range es {
			src := "journal"
			if e.Kernel {
				src = "kernel"
			}
			add(logLine{Time: e.Time, Source: src, Ident: e.Ident, Text: e.Msg, Prio: e.Prio})
		}
	}
	addEntries(in.KernelLog)
	addEntries(in.Warnings)
	addEntries(in.Journal)
	if in.Ring != nil {
		for _, s := range in.Ring.Samples {
			for _, k := range s.Kmsg {
				add(logLine{Time: time.UnixMilli(s.T), Source: "recorder", Ident: "kernel", Text: k, Prio: 4})
			}
		}
	}
	for _, rec := range in.Pstore {
		src := rec.Source
		if src == "" {
			src = "pstore"
		}
		for _, line := range strings.Split(rec.Text, "\n") {
			t, text := splitKernelTimestamp(line, in.BootStart, rec)
			add(logLine{Time: t, Source: src, Ident: "kernel", Text: text, Prio: 2})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

var (
	kernelTS = regexp.MustCompile(`^(?:<\d+>)?\[\s*(\d+)\.(\d+)\]\s?`)
	// callerID is the "[  T3273]" (task) or "[  C1]" (CPU) tag that
	// kernels built with CONFIG_PRINTK_CALLER add after the timestamp, as
	// kdump's makedumpfile writes it.
	callerID = regexp.MustCompile(`^\[\s*[TC]\d+\]\s?`)
)

// splitKernelTimestamp converts "[  123.456789] msg" into an absolute time
// using the boot start, falling back to the pstore record's time.
func splitKernelTimestamp(line string, bootStart time.Time, rec pstore.Record) (time.Time, string) {
	line = strings.TrimRight(line, "\r")
	// pstore prefixes lines with "<level>" or "Oops#1 Part1" banners.
	m := kernelTS.FindStringSubmatchIndex(line)
	if m == nil {
		return rec.Time, line
	}
	secs, _ := strconv.ParseInt(line[m[2]:m[3]], 10, 64)
	frac := line[m[4]:m[5]]
	for len(frac) < 6 {
		frac += "0"
	}
	usec, _ := strconv.ParseInt(frac[:6], 10, 64)
	text := callerID.ReplaceAllString(line[m[1]:], "")
	if bootStart.IsZero() {
		return rec.Time, text
	}
	return bootStart.Add(time.Duration(secs)*time.Second + time.Duration(usec)*time.Microsecond), text
}

// matchLines returns lines whose text matches re, optionally restricted to
// ident (empty = any).
func matchLines(lines []logLine, re *regexp.Regexp, ident string) []logLine {
	var out []logLine
	for _, l := range lines {
		if ident != "" && l.Ident != ident {
			continue
		}
		if re.MatchString(l.Text) {
			out = append(out, l)
		}
	}
	return out
}

func lastLine(ls []logLine) (logLine, bool) {
	if len(ls) == 0 {
		return logLine{}, false
	}
	return ls[len(ls)-1], true
}

// evidence converts up to max lines (the most recent ones) to report lines.
func evidence(ls []logLine, max int) []report.Line {
	// Rules often combine several matches of the same timeline, so one
	// line can arrive twice; keep each once, in time order.
	uniq := make([]logLine, 0, len(ls))
	seen := map[logLine]bool{}
	for _, l := range ls {
		if !seen[l] {
			seen[l] = true
			uniq = append(uniq, l)
		}
	}
	sort.SliceStable(uniq, func(i, j int) bool { return uniq[i].Time.Before(uniq[j].Time) })
	ls = uniq
	if len(ls) > max {
		ls = ls[len(ls)-max:]
	}
	out := make([]report.Line, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.toEvidence())
	}
	return out
}

var (
	homeRe = regexp.MustCompile(`/home/[^/\s]+`)
	macRe  = regexp.MustCompile(`\b(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\b`)
	ipv4Re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
)

// redact removes details that identify a person or network from evidence
// lines so reports are safer to share.
func redact(s string) string {
	s = homeRe.ReplaceAllString(s, "/home/<user>")
	s = macRe.ReplaceAllString(s, "<mac>")
	s = ipv4Re.ReplaceAllString(s, "<ip>")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// Patterns used by the rules.
var (
	reShutdown = regexp.MustCompile(`Reached target (System Power Off|System Reboot|Power-Off|Reboot|System Halt|Final Step|Kexec)|System is (powering down|rebooting|halting)|Journal stopped|systemd-shutdown|Shutting down\.$|Powering off\.|Rebooting\.`)

	rePanic    = regexp.MustCompile(`Kernel panic - not syncing`)
	reSysrqC   = regexp.MustCompile(`(?i)sysrq triggered crash|sysrq: Trigger a crash`)
	reOops     = regexp.MustCompile(`Oops:|BUG: unable to handle|BUG: kernel NULL pointer|general protection fault|kernel BUG at|Unable to handle kernel`)
	reLockup   = regexp.MustCompile(`soft lockup - CPU|hard LOCKUP|Watchdog detected hard LOCKUP`)
	reRCU      = regexp.MustCompile(`rcu: INFO: .*(stall|detected)|rcu_(sched|preempt) (self-)?detected stall`)
	reHungTask = regexp.MustCompile(`blocked for more than \d+ seconds`)
	reRIP      = regexp.MustCompile(`RIP: [0-9a-f]+:([A-Za-z0-9_.]+)\+`)
	rePC       = regexp.MustCompile(`pc : ([A-Za-z0-9_.]+)\+`)
	reComm     = regexp.MustCompile(`Comm: (\S+)`)
	reMCE      = regexp.MustCompile(`mce: \[Hardware Error\]|Machine check events logged|Hardware Error|MCE: .*error`)

	reOOMKill   = regexp.MustCompile(`(?:Out of memory|Memory cgroup out of memory): Killed process (\d+) \(([^)]+)\)`)
	reOOMDaemon = regexp.MustCompile(`(?i)killed|kill|sending sig|low memory`)

	reGPU = regexp.MustCompile(`(?i)amdgpu.*(ring \S+ timeout|gpu reset|gpu recovery|\*error\*.*timeout|page fault|hang)|` +
		`\[drm\].*(gpu hang|\*error\*.*(timeout|hang))|i915.*(gpu hang|resetting|reset)|gpu hang|` +
		`radeon.*(lockup|timeout)|nouveau .*(fault|trap|error)|NVRM: Xid|fallen off the bus`)
	reNVXid = regexp.MustCompile(`NVRM: Xid \([^)]*\): (\d+)`)

	reThermalCrit = regexp.MustCompile(`(?i)critical temperature reached|thermal.*critical.*shutting down|reached critical temperature`)
	reThermalWarn = regexp.MustCompile(`(?i)temperature above threshold|cpu clock throttled|thermal.*throttl`)
	reBatteryCrit = regexp.MustCompile(`(?i)battery.*critical|critical.*battery|battery is critically low|CriticalPowerAction`)
	reSleepEntry  = regexp.MustCompile(`PM: (suspend|hibernation) entry|Entering sleep state|Performing sleep operation`)
	reSleepExit   = regexp.MustCompile(`PM: (suspend|hibernation) exit|System returned from sleep|Finishing wakeup|PM: resume`)
	reResumeFail  = regexp.MustCompile(`(?i)failed to resume|resume.*failed|PM: Device .* failed`)
	reStorage     = regexp.MustCompile(`I/O error, dev \S+|blk_update_request: I/O error|Buffer I/O error|nvme nvme\d+: .*(timeout|controller is down|Device not ready|Removing after probe failure|resetting controller)|EXT4-fs error|BTRFS (error|critical)|XFS .*(metadata I/O error|Corruption|log I/O error|shut down filesystem)|ata\d+.*(failed command|exception Emask|hard resetting link)|Remounting filesystem read-only|(Medium Error|Unrecovered read error)`)
	reStorageDev  = regexp.MustCompile(`(?:I/O error, dev |Buffer I/O error on dev |\(device |nvme |ata)(\w+)`)
	reSysrq       = regexp.MustCompile(`sysrq: (Resetting|Emergency Sync|Emergency Remount R/O|Power Off|Trigger a crash|Kill All Tasks|Terminate All Tasks|Manual OOM)`)
)
