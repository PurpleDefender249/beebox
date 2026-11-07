package analyze

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/report"
)

type ruleCtx struct {
	in       *Input
	lines    []logLine
	m        metrics
	lastSeen time.Time
}

// near reports whether t is within d of the end of the boot.
func (c *ruleCtx) near(t time.Time, d time.Duration) bool {
	if t.IsZero() {
		return false
	}
	return c.lastSeen.Sub(t) <= d
}

func (c *ruleCtx) nearLines(ls []logLine, d time.Duration) []logLine {
	var out []logLine
	for _, l := range ls {
		if c.near(l.Time, d) {
			out = append(out, l)
		}
	}
	return out
}

type rule func(*ruleCtx) *report.Finding

var rules = []rule{
	rulePanic,
	ruleHardwareError,
	ruleOOM,
	ruleGPU,
	ruleThermal,
	ruleBattery,
	ruleSleep,
	ruleStorage,
	ruleIOStall,
	ruleDiskFull,
	ruleFreeze,
	rulePowerLoss,
	ruleSysrq,
	ruleRecorderGap,
	ruleBusyCPU,
}

func rulePanic(c *ruleCtx) *report.Finding {
	if trig := matchLines(c.lines, reSysrqC, ""); len(trig) > 0 {
		last, _ := lastLine(trig)
		if c.near(last.Time, 5*time.Minute) {
			return &report.Finding{
				ID: "kernel-panic", Kind: report.KindCause, Confidence: 97,
				Title:    "A crash was triggered on purpose (SysRq + C)",
				Summary:  "Someone pressed Alt + SysRq + C, or wrote “c” to /proc/sysrq-trigger, which tells the kernel to crash on the spot. This is how crash tools, including BeeBox, are tested, so there is nothing to fix.",
				Evidence: evidence(append(trig, matchLines(c.lines, rePanic, "")...), 4),
			}
		}
	}

	panics := matchLines(c.lines, rePanic, "")
	fn, comm := crashLocation(c.lines)
	where := ""
	switch {
	case fn != "" && comm != "":
		where = fmt.Sprintf(" The fault was in the kernel function %s while the program “%s” was running.", fn, comm)
	case fn != "":
		where = fmt.Sprintf(" The fault was in the kernel function %s.", fn)
	}
	advice := []report.Advice{
		adv("Install pending updates — kernel and driver fixes resolve many crashes like this.", c.in.Env.update()),
		adv("If it started after an update, choose the previous kernel from the boot menu (hold Shift or press Esc while the computer starts) to confirm.", ""),
	}
	if fn != "" {
		advice = append(advice, adv(fmt.Sprintf("Search the web for “%s” together with your kernel version (%s) to find known bugs.", fn, c.in.Kernel), ""))
	}
	advice = append(advice, shareAdvice())

	if len(panics) > 0 {
		conf := 85
		for _, p := range panics {
			if p.Source == "pstore" {
				conf = 95
			}
		}
		reason := ""
		if last, ok := lastLine(panics); ok {
			if _, r, ok := strings.Cut(last.Text, "not syncing:"); ok {
				reason = strings.TrimSpace(r)
			}
		}
		summary := "The kernel hit an error it could not recover from and stopped the whole system."
		if reason != "" {
			summary += fmt.Sprintf(" Its last words were “%s”.", truncate(reason, 120))
		}
		summary += where
		return &report.Finding{
			ID: "kernel-panic", Kind: report.KindCause, Confidence: conf,
			Title: "The Linux kernel crashed (kernel panic)", Summary: summary,
			Advice: advice, Evidence: evidence(append(locationLines(c.lines), panics...), 6),
		}
	}

	lock := append(matchLines(c.lines, reLockup, ""), matchLines(c.lines, reRCU, "")...)
	sortLines(lock)
	if len(lock) > 0 {
		last, _ := lastLine(lock)
		conf := 50
		if c.near(last.Time, 10*time.Minute) {
			conf = 78
		}
		return &report.Finding{
			ID: "kernel-lockup", Kind: report.KindCause, Confidence: conf,
			Title:   "The kernel locked up",
			Summary: "A processor core stopped responding to the rest of the system (a “lockup”), which freezes everything that depends on it. This is usually a driver bug, and less often faulty hardware." + where,
			Advice:  advice, Evidence: evidence(lock, 5),
		}
	}

	oops := matchLines(c.lines, reOops, "")
	if len(oops) > 0 {
		last, _ := lastLine(oops)
		conf := 45
		if c.near(last.Time, 10*time.Minute) {
			conf = 70
		}
		return &report.Finding{
			ID: "kernel-bug", Kind: report.KindCause, Confidence: conf,
			Title:   "The kernel hit a bug",
			Summary: "The kernel reported an internal error (an “oops”). The system can limp along for a while afterwards, but it often freezes soon after." + where,
			Advice:  advice, Evidence: evidence(append(locationLines(c.lines), oops...), 5),
		}
	}

	hung := c.nearLines(matchLines(c.lines, reHungTask, ""), 10*time.Minute)
	if len(hung) > 0 {
		return &report.Finding{
			ID: "hung-tasks", Kind: report.KindCause, Confidence: 45,
			Title:   "Programs got stuck waiting on the kernel",
			Summary: fmt.Sprintf("The kernel warned %d time(s) that programs had been stuck for minutes, usually waiting for a disk, network share or driver that stopped answering.", len(hung)),
			Advice: []report.Advice{
				adv("Check the disk's health.", c.in.Env.install("smartmontools")),
				adv("If you use network drives (NFS, SMB), check that the server was reachable.", ""),
			},
			Evidence: evidence(hung, 4),
		}
	}
	return nil
}

func ruleHardwareError(c *ruleCtx) *report.Finding {
	hw := matchLines(c.lines, reMCE, "")
	if len(hw) == 0 {
		return nil
	}
	last, _ := lastLine(hw)
	conf := 45
	if c.near(last.Time, 10*time.Minute) {
		conf = 80
	}
	return &report.Finding{
		ID: "hardware-error", Kind: report.KindCause, Confidence: conf,
		Title:   "The processor or memory reported a hardware error",
		Summary: "The CPU raised a machine-check error, which means the hardware itself detected a fault (often memory, the CPU, or unstable overclocking/undervolting).",
		Advice: []report.Advice{
			adv("Test your memory with memtest86+ (often available from the boot menu).", c.in.Env.install("memtest86+")),
			adv("Undo any overclocking or undervolting and update your BIOS/UEFI.", ""),
			adv("Check CPU temperatures while the computer is busy.", "sensors"),
		},
		Evidence: evidence(hw, 4),
	}
}

func ruleOOM(c *ruleCtx) *report.Finding {
	m := c.m
	kills := matchLines(c.lines, reOOMKill, "")
	var daemonKills []logLine
	for _, l := range c.lines {
		switch l.Ident {
		case "systemd-oomd", "earlyoom", "nohang":
			if reOOMDaemon.MatchString(l.Text) {
				daemonKills = append(daemonKills, l)
			}
		}
	}
	recentKills := c.nearLines(kills, 5*time.Minute)
	recentDaemon := c.nearLines(daemonKills, 5*time.Minute)

	score := 0
	if m.n > 0 {
		if m.psi {
			switch {
			case m.maxMemFull >= 40:
				score += 40
			case m.maxMemFull >= 10:
				score += 25
			case m.maxMemFull >= 3:
				score += 10
			}
		}
		switch {
		case m.minMemAvail < 3:
			score += 25
		case m.minMemAvail < 8:
			score += 12
		}
		if m.maxSwapUsed >= 90 {
			score += 10
		}
		if m.maxLagMs >= 3000 && score >= 20 {
			score += 10
		}
		if m.majFaults > 50000 {
			score += 5
		}
		if m.oomKills > 0 && len(kills) == 0 && c.near(m.lastOOMKill, 5*time.Minute) {
			score += 20
		}
	}
	switch {
	case len(recentKills) > 0:
		score += 35
	case len(kills) > 0:
		score += 8
	}
	switch {
	case len(recentDaemon) > 0:
		score += 25
	case len(daemonKills) > 0:
		score += 5
	}
	if score > 95 {
		score = 95
	}

	victims := victimNames(append(kills, daemonKills...))
	if score < 30 {
		if len(kills)+len(daemonKills) == 0 {
			return nil
		}
		return &report.Finding{
			ID: "low-memory-earlier", Kind: report.KindContext, Confidence: 60,
			Title:    "Earlier, the system ran low on memory",
			Summary:  fmt.Sprintf("During this boot, programs were closed to free memory (%s). That happened well before the end, so it is probably not why the system stopped.", listOr(victims, "a program")),
			Evidence: evidence(append(kills, daemonKills...), 3),
		}
	}

	var parts []string
	if m.n > 0 {
		s := fmt.Sprintf("In the last minute before it stopped, available memory fell to %s", pct(m.minMemAvail))
		if m.maxSwapUsed >= 50 {
			s += fmt.Sprintf(" and swap was %s full", pct(m.maxSwapUsed))
		}
		if m.psi && m.maxMemFull >= 3 {
			s += fmt.Sprintf(", and the system spent up to %s of its time stalled waiting for memory", pct(m.maxMemFull))
		}
		parts = append(parts, s+".")
	}
	if top := c.topMemApp(); top != "" {
		parts = append(parts, top)
	}
	if len(victims) > 0 {
		parts = append(parts, fmt.Sprintf("To free memory, %s.", killedPhrase(victims)))
	}
	if m.recovered() {
		// Memory was fine again by the end, so the crisis may not be what
		// finally stopped the computer.
		score -= 20
		parts = append(parts, fmt.Sprintf("Memory had recovered by the last recorded second (%s available), so this may not be what finally stopped the computer.", pct(m.last.MemAvailPct())))
	} else if !c.in.Clean {
		parts = append(parts, "The system did not recover and had to be restarted.")
	}
	if score < 30 {
		return &report.Finding{
			ID: "low-memory-earlier", Kind: report.KindContext, Confidence: 60,
			Title:    "Shortly before the end, the system ran low on memory",
			Summary:  strings.Join(parts, " "),
			Evidence: evidence(append(kills, daemonKills...), 3),
		}
	}

	advice := c.oomAdvice()
	return &report.Finding{
		ID: "out-of-memory", Kind: report.KindCause, Confidence: score,
		Title:   "Your computer ran out of memory",
		Summary: strings.Join(parts, " "),
		Advice:  advice,
		Evidence: append(c.memEvidence(),
			evidence(append(kills, daemonKills...), 4)...),
	}
}

func (c *ruleCtx) oomAdvice() []report.Advice {
	var a []report.Advice
	switch c.in.Env.OOMProtector {
	case "":
		a = append(a, adv("Turn on early out-of-memory protection, so one greedy app gets closed instead of the whole system freezing.", "sudo systemctl enable --now systemd-oomd"))
		if cmd := c.in.Env.install("earlyoom"); cmd != "" {
			a = append(a, adv("If systemd-oomd is not available on your system, install earlyoom instead.", cmd+" && sudo systemctl enable --now earlyoom"))
		}
	default:
		a = append(a, adv(fmt.Sprintf("%s is running but could not act in time. Giving it more room (swap or zram) usually fixes this.", c.in.Env.OOMProtector), ""))
	}
	if c.m.n > 0 && c.m.last.SwapTotal == 0 {
		a = append(a, adv("This computer has no swap. Compressed swap in RAM (zram) gives the system breathing room when memory runs out.", c.in.Env.ZramCommand()))
	}
	if name := c.topMemName(); name != "" {
		a = append(a, adv(fmt.Sprintf("Keep an eye on %s: close unused tabs or windows, and check for extensions or plugins that leak memory.", name), ""))
	}
	return a
}

func (c *ruleCtx) topMemName() string {
	if c.m.n == 0 {
		return ""
	}
	tm := c.m.worst.TopMem
	if len(tm) == 0 {
		return ""
	}
	return displayName(tm[0].Name)
}

func (c *ruleCtx) topMemApp() string {
	if c.m.n == 0 {
		return ""
	}
	tm := c.m.worst.TopMem
	if len(tm) == 0 {
		return ""
	}
	a := tm[0]
	s := fmt.Sprintf("The biggest user was %s (%s", displayName(a.Name), fmtKB(a.MemKB))
	if a.Count > 1 {
		s += fmt.Sprintf(" across %d processes", a.Count)
	}
	s += ")"
	if g := c.m.growth[a.Name]; g > 300*1024 && c.m.growthWindow >= time.Minute {
		s += fmt.Sprintf(", which grew by %s in %s", fmtKB(uint64(g)), roundDur(c.m.growthWindow)) // #nosec G115 -- g > 0
	}
	return s + "."
}

func (c *ruleCtx) memEvidence() []report.Line {
	if c.m.n == 0 {
		return nil
	}
	t := c.m.lastT
	var out []report.Line
	out = append(out, report.Line{Time: t, Source: "recorder", Text: fmt.Sprintf("lowest available memory in final minute: %s of %s", pct(c.m.minMemAvail), fmtKB(c.m.last.MemTotal))})
	if c.m.psi {
		out = append(out, report.Line{Time: t, Source: "recorder", Text: fmt.Sprintf("memory pressure (full, 10 s average) peaked at %s", pct(c.m.maxMemFull))})
	}
	if c.m.maxLagMs >= 1000 {
		out = append(out, report.Line{Time: t, Source: "recorder", Text: fmt.Sprintf("BeeBox's once-a-second check ran up to %.1f s late", float64(c.m.maxLagMs)/1000)})
	}
	return out
}

func ruleGPU(c *ruleCtx) *report.Finding {
	g := matchLines(c.lines, reGPU, "")
	if len(g) == 0 {
		return nil
	}
	last, _ := lastLine(g)
	isNear := c.near(last.Time, 3*time.Minute)
	conf := 40
	if isNear {
		conf = 72
	}
	vendor := gpuVendor(g)
	summary := fmt.Sprintf("The %s graphics driver reported that the GPU hung or had to be reset", vendor)
	if isNear {
		summary += " just before the system stopped"
	}
	summary += ". When the GPU stops answering, the screen freezes even if the rest of the system is still running."
	fellOff := false
	for _, l := range g {
		if strings.Contains(l.Text, "fallen off the bus") {
			fellOff = true
		}
		if mm := reNVXid.FindStringSubmatch(l.Text); mm != nil && mm[1] == "79" {
			fellOff = true
		}
	}
	advice := []report.Advice{
		adv("Install pending updates for your kernel and graphics driver.", c.in.Env.update()),
		adv("If it only happens in one app or game, try turning off hardware acceleration in that app, or a different graphics backend.", ""),
	}
	if fellOff {
		summary += " The GPU disappeared from the system entirely (“fell off the bus”), which usually points to power delivery or overheating rather than software."
		conf += 8
		advice = append([]report.Advice{adv("Check the graphics card's power cables and temperatures, and undo any overclock.", "")}, advice...)
	}
	return &report.Finding{
		ID: "gpu-hang", Kind: report.KindCause, Confidence: conf,
		Title: "The graphics driver stopped responding", Summary: summary,
		Advice: advice, Evidence: evidence(g, 5),
	}
}

func ruleThermal(c *ruleCtx) *report.Finding {
	crit := matchLines(c.lines, reThermalCrit, "")
	warn := c.nearLines(matchLines(c.lines, reThermalWarn, ""), 5*time.Minute)
	m := c.m
	score := 0
	var why string
	if last, ok := lastLine(crit); ok && c.near(last.Time, 5*time.Minute) {
		score = 90
		why = "The kernel reported that a critical temperature was reached and switched the computer off to protect the hardware."
	}
	if m.n > 0 && m.tempNearCrit && score < 70 {
		score = 70
		why = fmt.Sprintf("The %s sensor reached %.0f °C, at or near its critical limit of %.0f °C.", sensorLabel(m.maxTemp.Name), m.maxTemp.C, m.maxTemp.Crit)
	}
	if m.n > 0 && score < 55 && m.maxTemp.C >= 97 && strings.HasPrefix(m.maxTemp.Name, "cpu") {
		score = 55
		why = fmt.Sprintf("The processor reached %.0f °C in the final minute, which is hot enough for many CPUs to shut down.", m.maxTemp.C)
	}
	if score == 0 && len(warn) > 0 {
		score = 30
		why = "The processor was throttling its speed because it was too hot."
	}
	if score == 0 {
		return nil
	}
	title := "Your computer overheated"
	if c.in.Clean {
		title = "Your computer shut down to protect itself from overheating"
	}
	return &report.Finding{
		ID: "overheating", Kind: report.KindCause, Confidence: score,
		Title: title, Summary: why,
		Advice: []report.Advice{
			adv("Clean the dust out of vents and fans, and keep the vents clear (a bed or sofa blocks them).", ""),
			adv("Watch temperatures and fan speeds while the computer is busy.", "sensors"),
			adv("On a computer that is a few years old, fresh thermal paste can lower temperatures a lot.", ""),
		},
		Evidence: append(evidence(append(crit, warn...), 4), c.tempEvidence()...),
	}
}

func (c *ruleCtx) tempEvidence() []report.Line {
	if c.m.n == 0 || c.m.maxTemp.Name == "" {
		return nil
	}
	t := c.m.maxTemp
	text := fmt.Sprintf("hottest reading in final minute: %s %.0f °C", sensorLabel(t.Name), t.C)
	if t.Crit > 0 {
		text += fmt.Sprintf(" (critical %.0f °C)", t.Crit)
	}
	return []report.Line{{Time: c.m.lastT, Source: "recorder", Text: text}}
}

func ruleBattery(c *ruleCtx) *report.Finding {
	m := c.m
	crit := c.nearLines(matchLines(c.lines, reBatteryCrit, ""), 5*time.Minute)
	score := 0
	pctLeft := -1
	if m.n > 0 && m.last.Battery != nil && m.last.Battery.Status == "Discharging" && (m.last.OnAC == nil || !*m.last.OnAC) {
		pctLeft = m.last.Battery.Pct
		switch {
		case pctLeft <= 3:
			score = 90
		case pctLeft <= 7:
			score = 75
		case pctLeft <= 12:
			score = 45
		}
	}
	if len(crit) > 0 && score < 80 {
		score = 80
	}
	if score == 0 {
		return nil
	}
	var summary string
	if c.in.Clean {
		summary = "Your computer shut itself down because the battery was critically low"
	} else {
		summary = "The battery ran flat and the computer switched off without shutting down properly"
	}
	if pctLeft >= 0 {
		summary += fmt.Sprintf(" (last reading: %d%%", pctLeft)
		if m.last.Battery.PowerW > 0 {
			summary += fmt.Sprintf(", draining at %.1f W", m.last.Battery.PowerW)
		}
		summary += ")"
	}
	summary += "."
	ev := evidence(crit, 3)
	if pctLeft >= 0 {
		ev = append(ev, report.Line{Time: m.lastT, Source: "recorder", Text: fmt.Sprintf("battery %d%%, %s", pctLeft, strings.ToLower(m.last.Battery.Status))})
	}
	return &report.Finding{
		ID: "battery-depleted", Kind: report.KindCause, Confidence: score,
		Title: "The battery ran out", Summary: summary,
		Advice: []report.Advice{
			adv("Plug in before the battery drops below 10%. Your desktop's power settings can warn you sooner or hibernate automatically.", ""),
			adv("Check the battery's health — an old battery can die suddenly even when it shows charge left.", "upower -i $(upower -e | grep -m1 BAT)"),
		},
		Evidence: ev,
	}
}

func ruleSleep(c *ruleCtx) *report.Finding {
	entries := matchLines(c.lines, reSleepEntry, "")
	lastEntry, ok := lastLine(entries)
	if !ok {
		return nil
	}
	lastExit, hasExit := lastLine(matchLines(c.lines, reSleepExit, ""))
	asleep := !hasExit || lastExit.Time.Before(lastEntry.Time)
	if !asleep || !c.near(lastEntry.Time, 2*time.Minute) {
		fails := c.nearLines(matchLines(c.lines, reResumeFail, ""), 5*time.Minute)
		if len(fails) == 0 {
			return nil
		}
		return &report.Finding{
			ID: "resume-trouble", Kind: report.KindCause, Confidence: 50,
			Title:    "Your computer had trouble waking from sleep",
			Summary:  "A device failed to resume after sleep shortly before the system stopped. Faulty wake-up handling can leave the screen black or the system frozen.",
			Advice:   sleepAdvice(c),
			Evidence: evidence(fails, 4),
		}
	}
	mode := ""
	if i := strings.Index(lastEntry.Text, "("); i >= 0 {
		if j := strings.Index(lastEntry.Text[i:], ")"); j > 0 {
			mode = lastEntry.Text[i+1 : i+j]
		}
	}
	conf := 75
	title := "Your computer never woke up from sleep"
	summary := fmt.Sprintf("The last thing the system did was go to sleep at %s", lastEntry.Time.Local().Format("15:04"))
	if mode != "" {
		summary += fmt.Sprintf(" (mode: %s)", mode)
	}
	summary += ". It never resumed — the next start was a fresh boot. Either it failed to wake up, or it lost power while asleep."
	if c.m.n > 0 && c.m.last.Battery != nil && c.m.last.Battery.Status == "Discharging" && c.m.last.Battery.Pct <= 15 {
		conf = 85
		title = "The battery ran out while the computer was asleep"
		summary = fmt.Sprintf("The computer went to sleep at %s with the battery at %d%% and never woke up. Sleep still uses a little power, and it ran out.", lastEntry.Time.Local().Format("15:04"), c.m.last.Battery.Pct)
	}
	return &report.Finding{
		ID: "died-in-sleep", Kind: report.KindCause, Confidence: conf,
		Title: title, Summary: summary,
		Advice: sleepAdvice(c), Evidence: evidence([]logLine{lastEntry}, 1),
	}
}

func sleepAdvice(c *ruleCtx) []report.Advice {
	return []report.Advice{
		adv("See which sleep mode is used. “[s2idle]” (modern standby) is handled badly by some laptops; “deep” is often more reliable.", "cat /sys/power/mem_sleep"),
		adv("Update your BIOS/UEFI firmware — suspend bugs are often fixed there.", "fwupdmgr update"),
		adv("If the battery drains while asleep, consider hibernation or suspend-then-hibernate.", ""),
	}
}

func ruleStorage(c *ruleCtx) *report.Finding {
	errs := matchLines(c.lines, reStorage, "")
	if len(errs) == 0 {
		return nil
	}
	recent := c.nearLines(errs, 10*time.Minute)
	conf := 35
	if len(recent) > 0 {
		conf = 70
		if len(recent) >= 5 {
			conf = 78
		}
	}
	names := storageDevices(errs)
	summary := fmt.Sprintf("The kernel logged %d storage %s", len(errs), map[bool]string{true: "error", false: "errors"}[len(errs) == 1])
	if len(names) > 0 {
		summary += " on " + strings.Join(names, ", ")
	}
	if len(recent) > 0 {
		summary += ", including some just before the system stopped"
	}
	summary += ". Failed reads and writes can freeze the whole system, and they are often an early warning that a drive is failing."
	dev := "/dev/nvme0"
	if len(names) > 0 {
		dev = "/dev/" + names[0]
	}
	return &report.Finding{
		ID: "storage-errors", Kind: report.KindCause, Confidence: conf,
		Title: "Your disk reported errors", Summary: summary,
		Advice: []report.Advice{
			adv("Back up anything important now.", ""),
			adv("Check the drive's health report (install smartmontools first if needed).", "sudo smartctl -a "+dev),
			adv("On a desktop, reseat the drive's cables; update the drive's firmware if the maker offers one.", ""),
		},
		Evidence: evidence(errs, 5),
	}
}

func ruleIOStall(c *ruleCtx) *report.Finding {
	m := c.m
	if m.n == 0 || !m.psi || m.maxIOFull < 40 {
		return nil
	}
	conf := 45
	if m.maxLagMs >= 3000 {
		conf = 55
	}
	return &report.Finding{
		ID: "io-stall", Kind: report.KindCause, Confidence: conf,
		Title:   "The system was stuck waiting for the disk",
		Summary: fmt.Sprintf("In the final minute, all running programs were stalled on disk access up to %s of the time. A slow or failing drive, a full disk, or heavy swapping can cause this.", pct(m.maxIOFull)),
		Advice: []report.Advice{
			adv("Check the drive's health.", "sudo smartctl -a /dev/nvme0"),
			adv("See which programs read and write the most next time it slows down.", c.in.Env.install("iotop")),
		},
		Evidence: []report.Line{{Time: m.lastT, Source: "recorder", Text: fmt.Sprintf("disk pressure (full, 10 s average) peaked at %s", pct(m.maxIOFull))}},
	}
}

func ruleDiskFull(c *ruleCtx) *report.Finding {
	m := c.m
	if m.n == 0 || m.last.RootFree < 0 || m.last.RootFree >= 1 {
		return nil
	}
	return &report.Finding{
		ID: "disk-full", Kind: report.KindCause, Confidence: 45,
		Title:   "Your main drive was full",
		Summary: fmt.Sprintf("Only %s of the main drive was free. When the disk fills up, apps and the desktop can freeze or fail to save, and login can get stuck.", pct(m.last.RootFree)),
		Advice: []report.Advice{
			adv("See which folders in your home use the most space.", "du -h -d1 ~ | sort -h | tail"),
			adv("Shrink old system logs.", "sudo journalctl --vacuum-size=200M"),
		},
		Evidence: []report.Line{{Time: m.lastT, Source: "recorder", Text: fmt.Sprintf("free space on /: %s", pct(m.last.RootFree))}},
	}
}

func ruleFreeze(c *ruleCtx) *report.Finding {
	m := c.m
	if m.n == 0 || c.in.Clean || m.maxLagMs < 5000 {
		return nil
	}
	return &report.Finding{
		ID: "froze", Kind: report.KindCause, Confidence: 40,
		Title:   "The system froze before it was restarted",
		Summary: fmt.Sprintf("In its final moments, BeeBox's own once-a-second check ran up to %.0f seconds late — the system was barely responding before it was reset.", float64(m.maxLagMs)/1000),
		Advice: []report.Advice{
			adv("Next time it freezes, try Ctrl + Alt + F3 to reach a text console; if that works, only the desktop is stuck.", ""),
			adv("Enable the Magic SysRq keys so you can restart safely from a freeze (hold Alt + SysRq, then press R E I S U B slowly).", "echo kernel.sysrq=1 | sudo tee /etc/sysctl.d/90-sysrq.conf"),
		},
		Evidence: c.memEvidence(),
	}
}

func rulePowerLoss(c *ruleCtx) *report.Finding {
	if c.in.Clean {
		return nil
	}
	m := c.m
	// Any serious log line near the end means something other than a
	// sudden power loss was going on. An OOM kill the system recovered
	// from is the exception: it is reported as context, and the stop that
	// followed still needs explaining.
	serious := []*regexp.Regexp{rePanic, reOops, reLockup, reGPU, reStorage, reThermalCrit, reSleepEntry, reMCE}
	if !m.recovered() {
		serious = append(serious, reOOMKill)
	}
	for _, re := range serious {
		if len(c.nearLines(matchLines(c.lines, re, ""), 2*time.Minute)) > 0 {
			return nil
		}
	}
	crashNote := " This computer can't save kernel crash reports yet, so a kernel crash would look exactly the same — run \"beebox doctor\" to turn that on."
	if c.in.Env.CrashLogs {
		crashNote = " The kernel can save crash reports on this computer and none was saved, so a kernel crash is unlikely."
	}
	if m.n == 0 {
		if len(c.in.Journal) == 0 {
			return nil
		}
		return &report.Finding{
			ID: "sudden-stop", Kind: report.KindCause, Confidence: 35,
			Title:   "The computer stopped suddenly",
			Summary: "The system log simply ends, with no errors and no shutdown. That is what a power cut, a hard reset, or holding the power button looks like." + crashNote,
			Advice:  powerAdvice(c),
		}
	}
	if b := m.last.Battery; b != nil && b.Status == "Discharging" && b.Pct <= 12 {
		return nil // the battery rule explains this
	}
	memOK := (m.maxMemFull < 5 && m.minMemAvail > 5) || m.recovered()
	healthy := memOK && m.maxIOFull < 10 && m.maxLagMs < 1500 && !m.tempNearCrit
	if !healthy {
		return nil
	}
	// Describe only what was actually normal: a processor running flat
	// out is not "normal", even though it does not stop a computer.
	parts := []string{"disk", "temperatures"}
	if m.busyFor < time.Minute {
		parts = append(parts, "CPU")
	}
	lead := "Memory, " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1] + " all looked normal right up to the last recorded second"
	if m.recovered() && (m.maxMemFull >= 5 || m.minMemAvail <= 5) {
		lead = "Memory had recovered from a shortage, and " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1] + " looked normal at the last recorded second"
	}
	if m.busyFor >= time.Minute {
		lead += fmt.Sprintf(" (the processor was fully busy for the last %s, but nothing ran short)", roundDur(m.busyFor))
	}
	onBattery := m.last.Battery != nil && m.last.Battery.Status == "Discharging"
	ev := []report.Line{{Time: m.lastT, Source: "recorder", Text: fmt.Sprintf("final sample: CPU %.0f%%, available memory %s, memory stall %s, disk stall %s", m.last.CPU, pct(m.last.MemAvailPct()), pct(m.last.PSI.MemFull), pct(m.last.PSI.IOFull))}}
	if onBattery {
		return &report.Finding{
			ID: "sudden-stop", Kind: report.KindCause, Confidence: 35,
			Title:    "The computer stopped suddenly with no warning signs",
			Summary:  fmt.Sprintf("Everything looked normal right up to the last recorded second, and the battery still had %d%%. Then recording just stopped and nothing was logged. On a laptop, that usually means it was forced off (the power button was held down) or the hardware locked up.", m.last.Battery.Pct) + crashNote,
			Advice:   powerAdvice(c),
			Evidence: ev,
		}
	}
	if !c.in.Env.CrashLogs {
		return &report.Finding{
			ID: "sudden-stop", Kind: report.KindCause, Confidence: 45,
			Title:    "The computer stopped suddenly, with no warning signs",
			Summary:  lead + ", and then recording simply stopped without anything in the logs. The usual causes are a power cut, a loose plug, a failing power supply, or holding the power button down." + crashNote,
			Advice:   powerAdvice(c),
			Evidence: ev,
		}
	}
	return &report.Finding{
		ID: "power-loss", Kind: report.KindCause, Confidence: 55,
		Title:    "The power was cut (or the computer was hard-reset)",
		Summary:  lead + ", and then recording simply stopped without anything in the logs. That is what happens when power disappears instantly: a power cut, a loose plug, a failing power supply, or holding the power button down." + crashNote,
		Advice:   powerAdvice(c),
		Evidence: ev,
	}
}

func powerAdvice(c *ruleCtx) []report.Advice {
	a := []report.Advice{
		adv("If the power went out, a UPS (desktop) or a healthy battery (laptop) prevents data loss next time.", ""),
		adv("If you held the power button because the screen froze, try Ctrl + Alt + F3 first next time — if a text console appears, only the graphics stack was stuck.", ""),
		adv("If it happens again with no warning, test the memory (memtest86+) and check the power supply.", ""),
	}
	if !c.in.Env.CrashLogs {
		a = append(a, adv("Let the kernel save crash reports, so BeeBox can tell a kernel crash from a power cut next time.", "beebox doctor"))
	}
	return a
}

func ruleSysrq(c *ruleCtx) *report.Finding {
	ls := c.nearLines(matchLines(c.lines, reSysrq, ""), 5*time.Minute)
	if len(ls) == 0 {
		return nil
	}
	for _, l := range ls {
		if strings.Contains(l.Text, "Trigger a crash") {
			return nil // handled as a deliberate panic
		}
	}
	return &report.Finding{
		ID: "sysrq-restart", Kind: report.KindContext, Confidence: 90,
		Title:    "You restarted with the Magic SysRq keys",
		Summary:  "That's the safest way out of a frozen system: it saves data to disk before restarting. The main finding explains what froze it.",
		Evidence: evidence(ls, 4),
	}
}

// ruleBusyCPU notes a processor that was running flat out when recording
// stopped. A busy CPU alone doesn't stop a computer, but it is worth
// knowing: it can make a desktop feel frozen, and it names a suspect.
func ruleBusyCPU(c *ruleCtx) *report.Finding {
	m := c.m
	if m.n == 0 || c.in.Clean || m.busyFor < time.Minute {
		return nil
	}
	summary := fmt.Sprintf("For the last %s before recording stopped, the processor was at least 95%% busy.", roundDur(m.busyFor))
	if m.busyApp.Name != "" {
		summary += fmt.Sprintf(" The busiest app was %s (about %.0f%% of the whole processor", displayName(m.busyApp.Name), m.busyApp.CPU)
		if m.busyApp.Count > 1 {
			summary += fmt.Sprintf(", across %d processes", m.busyApp.Count)
		}
		summary += ")."
	}
	summary += " A busy processor makes the desktop slow but does not by itself stop a computer."
	return &report.Finding{
		ID: "busy-cpu", Kind: report.KindContext, Confidence: 90,
		Title:   "The processor was fully busy at the end",
		Summary: summary,
	}
}

func ruleRecorderGap(c *ruleCtx) *report.Finding {
	if c.m.n == 0 || c.in.Clean || len(c.in.Journal) == 0 {
		return nil
	}
	lastJ := c.in.Journal[len(c.in.Journal)-1].Time
	gap := lastJ.Sub(c.m.lastT)
	if gap < 2*time.Minute {
		return nil
	}
	return &report.Finding{
		ID: "recorder-gap", Kind: report.KindContext, Confidence: 70,
		Title:   "BeeBox stopped recording before the system did",
		Summary: fmt.Sprintf("The recorder's last sample was %s before the system's last log entry, so the final stretch is based on logs only.", roundDur(gap)),
	}
}

func unknownFinding(c *ruleCtx) report.Finding {
	var have []string
	if c.m.n > 0 {
		have = append(have, "the recorder's measurements")
	}
	if len(c.in.Journal) > 0 {
		have = append(have, "the system log")
	}
	if len(c.in.Pstore) > 0 {
		have = append(have, "a kernel crash log")
	}
	summary := "None of the usual warning signs (memory, heat, disk, graphics, battery, kernel errors) showed up"
	if len(have) > 0 {
		summary += " in " + strings.Join(have, " or ")
	}
	summary += "."
	if len(have) == 0 {
		summary = "There was no evidence to work with: BeeBox wasn't recording during this boot and the system log didn't survive the restart."
	}
	return report.Finding{
		ID: "unknown", Kind: report.KindCause, Confidence: 0,
		Title:   "BeeBox couldn't find a clear cause",
		Summary: summary,
		Advice: []report.Advice{
			adv("Check that BeeBox and your system logs are set up to catch the next one.", "beebox doctor"),
		},
	}
}

// crashLocation extracts the faulting function and program from oops text.
func crashLocation(lines []logLine) (fn, comm string) {
	start := len(lines) - 400
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		t := lines[i].Text
		if fn == "" {
			if mm := reRIP.FindStringSubmatch(t); mm != nil {
				fn = mm[1]
			} else if mm := rePC.FindStringSubmatch(t); mm != nil {
				fn = mm[1]
			}
		}
		if comm == "" {
			if mm := reComm.FindStringSubmatch(t); mm != nil {
				comm = mm[1]
			}
		}
		if fn != "" && comm != "" {
			break
		}
	}
	return fn, comm
}

func locationLines(lines []logLine) []logLine {
	var out []logLine
	start := len(lines) - 400
	if start < 0 {
		start = 0
	}
	for _, l := range lines[start:] {
		if reRIP.MatchString(l.Text) || rePC.MatchString(l.Text) {
			out = append(out, l)
		}
	}
	if len(out) > 1 {
		out = out[len(out)-1:]
	}
	return out
}

func sortLines(ls []logLine) {
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].Time.Before(ls[j].Time) })
}

func victimNames(ls []logLine) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range ls {
		name := ""
		if mm := reOOMKill.FindStringSubmatch(l.Text); mm != nil {
			name = mm[2]
		} else if i := strings.Index(l.Text, "app-"); i >= 0 {
			// systemd-oomd: ".../app-gnome-firefox-1234.scope"
			rest := l.Text[i+4:]
			parts := strings.Split(strings.Split(rest, ".")[0], "-")
			if len(parts) >= 2 {
				name = parts[len(parts)-2]
			}
		}
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, displayName(name))
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func killedPhrase(names []string) string {
	return "the system force-closed " + listOr(names, "a program")
}

func listOr(names []string, fallback string) string {
	switch len(names) {
	case 0:
		return fallback
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

func gpuVendor(ls []logLine) string {
	for _, l := range ls {
		t := strings.ToLower(l.Text)
		switch {
		case strings.Contains(t, "amdgpu"), strings.Contains(t, "radeon"):
			return "AMD"
		case strings.Contains(t, "i915"), strings.Contains(t, " xe "):
			return "Intel"
		case strings.Contains(t, "nvrm"), strings.Contains(t, "nouveau"):
			return "NVIDIA"
		}
	}
	return "system's"
}

var rePartition = regexp.MustCompile(`^((?:nvme\d+n\d+)|(?:mmcblk\d+))p\d+$|^((?:sd|vd|hd)[a-z]+)\d+$`)

// storageDevices returns the whole-disk names mentioned in storage errors,
// e.g. nvme0n1 for "nvme0n1p2" and "nvme0".
func storageDevices(ls []logLine) []string {
	set := map[string]bool{}
	for _, l := range ls {
		mm := reStorageDev.FindStringSubmatch(l.Text)
		if mm == nil {
			continue
		}
		name := mm[1]
		if p := rePartition.FindStringSubmatch(name); p != nil {
			name = p[1] + p[2]
		}
		if strings.HasPrefix(name, "ata") || name == "" {
			continue
		}
		set[name] = true
	}
	var out []string
	for n := range set {
		// Drop a controller name ("nvme0") when its namespace is listed.
		covered := false
		for m := range set {
			if m != n && strings.HasPrefix(m, n) {
				covered = true
			}
		}
		if !covered {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// supersedes lists findings that are explained by (part of) a verdict and
// would only repeat it under "Also noticed".
var supersedes = map[string][]string{
	"out-of-memory":    {"froze", "io-stall", "low-memory-earlier"},
	"died-in-sleep":    {"battery-depleted", "sudden-stop", "power-loss"},
	"battery-depleted": {"sudden-stop", "power-loss"},
	"storage-errors":   {"io-stall", "hung-tasks"},
	"kernel-panic":     {"kernel-bug", "kernel-lockup", "sudden-stop", "power-loss"},
	"overheating":      {"sudden-stop", "power-loss"},
	"gpu-hang":         {"froze"},
}

func dropSuperseded(verdict string, fs []report.Finding) []report.Finding {
	drop := map[string]bool{}
	for _, id := range supersedes[verdict] {
		drop[id] = true
	}
	out := fs[:0]
	for _, f := range fs {
		if !drop[f.ID] {
			out = append(out, f)
		}
	}
	return out
}
