// Package doctor checks that a machine is set up so BeeBox can explain the
// next crash, and says exactly how to fix anything that is missing.
package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
)

// Status of one check.
type Status string

// Check statuses, most to least serious.
const (
	Fail Status = "fail"
	Warn Status = "warn"
	Info Status = "info"
	OK   Status = "ok"
)

// Check is one result.
type Check struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Result string `json:"result"`
	Why    string `json:"why,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Recording describes the recorder's state as seen from its ring file.
type Recording struct {
	Active    bool           `json:"active"`
	Exists    bool           `json:"ring_exists"`
	ThisBoot  bool           `json:"this_boot"`
	LastAge   time.Duration  `json:"last_sample_age_ns"`
	Samples   int            `json:"samples"`
	Since     time.Time      `json:"since,omitempty"`
	Interval  time.Duration  `json:"interval_ns"`
	SlotCount int            `json:"slots"`
	Last      *sample.Sample `json:"last,omitempty"`
	Err       string         `json:"error,omitempty"`
}

// ReadRecording inspects the current ring.
func ReadRecording(p paths.Paths) Recording {
	var r Recording
	c, err := ring.Read(p.RingFile())
	if err != nil {
		if !os.IsNotExist(err) {
			r.Err = err.Error()
		}
		return r
	}
	r.Exists = true
	r.Interval = c.Header.Interval
	r.SlotCount = c.Header.SlotCount
	r.Samples = len(c.Samples)
	if id, err := sample.BootID(""); err == nil {
		r.ThisBoot = id == c.Header.BootID
	}
	if len(c.Samples) > 0 {
		last := c.Samples[len(c.Samples)-1]
		r.Last = &last
		r.LastAge = time.Since(time.UnixMilli(last.T))
		r.Since = c.Header.Created
		limit := 3*r.Interval + 10*time.Second
		r.Active = r.ThisBoot && r.LastAge < limit && last.Flags&sample.FlagStop == 0
	}
	return r
}

// Run performs every check.
func Run(p paths.Paths) []Check {
	var cs []Check
	rec := ReadRecording(p)
	cs = append(cs, checkRecorder(rec))
	cs = append(cs, checkJournal())
	cs = append(cs, checkPSI())
	cs = append(cs, checkOOM())
	cs = append(cs, checkSwap())
	cs = append(cs, checkSysrq())
	cs = append(cs, checkPstore())
	cs = append(cs, checkNotify())
	cs = append(cs, checkSpace(p))
	cs = append(cs, checkSensors(rec, virtualMachine("/proc/cpuinfo", "/sys/class/dmi/id"), analyze.DetectEnv("").PkgManager))
	return cs
}

func checkRecorder(r Recording) Check {
	c := Check{ID: "recorder", Name: "Recorder"}
	switch {
	case r.Active:
		c.Status = OK
		c.Result = fmt.Sprintf("Recording (last sample %s ago)", short(r.LastAge))
	case r.Err != "":
		c.Status = Fail
		c.Result = "The recording file can't be read: " + r.Err
		c.Fix = "sudo systemctl restart beebox"
	case !r.Exists:
		c.Status = Fail
		c.Result = "Not recording yet"
		c.Why = "Without the recorder, BeeBox can only guess from system logs."
		c.Fix = "sudo systemctl enable --now beebox"
	default:
		c.Status = Fail
		c.Result = fmt.Sprintf("Not recording (last sample %s ago)", short(r.LastAge))
		c.Why = "The recorder service has stopped."
		c.Fix = "sudo systemctl enable --now beebox"
	}
	return c
}

func checkJournal() Check {
	c := Check{ID: "journal", Name: "System logs"}
	_, err := os.Stat("/var/log/journal")
	persistent, storage := journal.Persistent(journal.ConfFiles(), err == nil)
	if persistent {
		c.Status = OK
		c.Result = "Kept across restarts"
		return c
	}
	c.Status = Warn
	c.Result = "Deleted at every restart (Storage=" + storage + ")"
	c.Why = "Kernel messages from a crashed boot are lost. BeeBox keeps its own copy of the important ones, but full logs help with rarer problems."
	if storage == "volatile" || storage == "none" {
		// A drop-in wins over journald.conf without editing it.
		c.Fix = "sudo mkdir -p /etc/systemd/journald.conf.d && printf '[Journal]\\nStorage=persistent\\n' | sudo tee /etc/systemd/journald.conf.d/90-beebox.conf >/dev/null && sudo systemctl restart systemd-journald"
	} else {
		c.Fix = "sudo mkdir -p /var/log/journal && sudo systemd-tmpfiles --create --prefix /var/log/journal && sudo systemctl restart systemd-journald"
	}
	return c
}

func checkPSI() Check {
	c := Check{ID: "psi", Name: "Pressure stall info"}
	if _, err := os.Stat("/proc/pressure/memory"); err == nil {
		c.Status = OK
		c.Result = "Available"
		return c
	}
	c.Status = Warn
	c.Result = "Not enabled in this kernel"
	c.Why = "It is BeeBox's most reliable signal that memory or the disk is choking the system."
	switch {
	case hasCmd("grubby"):
		c.Fix = "sudo grubby --update-kernel=ALL --args=psi=1   # then restart"
	case hasCmd("update-grub"):
		c.Fix = `sudo sed -i 's/^GRUB_CMDLINE_LINUX="/&psi=1 /' /etc/default/grub && sudo update-grub   # then restart`
	default:
		c.Why += " To turn it on, add psi=1 to the kernel command line and restart."
	}
	return c
}

func checkOOM() Check {
	c := Check{ID: "oom", Name: "Out-of-memory protection"}
	env := analyze.DetectEnv("")
	if env.OOMProtector != "" {
		c.Status = OK
		c.Result = env.OOMProtector + " is running"
		return c
	}
	c.Status = Warn
	c.Result = "None running"
	c.Why = "When memory runs out, Linux can freeze for minutes before it closes anything. A protector closes the greediest app early instead."
	c.Fix = env.OOMProtectorCommand()
	return c
}

func checkSwap() Check {
	c := Check{ID: "swap", Name: "Swap"}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		c.Status = Info
		c.Result = "Unknown"
		return c
	}
	_, _, total, _ := sample.ParseMeminfo(b)
	if total > 0 {
		c.Status = OK
		c.Result = fmt.Sprintf("%.1f GB available", float64(total)/1024/1024)
		if zram, _ := os.ReadFile("/proc/swaps"); bytes.Contains(zram, []byte("zram")) {
			c.Result += " (includes zram)"
		}
		return c
	}
	c.Status = Warn
	c.Result = "No swap"
	c.Why = "With no swap, running out of memory is abrupt. Compressed swap in RAM (zram) costs little and gives the system room to recover."
	c.Fix = analyze.DetectEnv("").ZramCommand()
	return c
}

func checkSysrq() Check {
	c := Check{ID: "sysrq", Name: "Magic SysRq keys"}
	b, err := os.ReadFile("/proc/sys/kernel/sysrq")
	if err != nil {
		c.Status = Info
		c.Result = "Unknown"
		return c
	}
	v, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	const sync, reboot = 16, 128
	switch {
	case v == 1 || (v&sync != 0 && v&reboot != 0):
		c.Status = OK
		c.Result = "Safe restart from a freeze is enabled (Alt + SysRq + R E I S U B)"
	default:
		c.Status = Info
		c.Result = fmt.Sprintf("Restart from a freeze is limited (kernel.sysrq=%d)", v)
		c.Why = "Holding Alt + SysRq and typing R E I S U B restarts a frozen computer without losing data on disk, and BeeBox records that you did."
		c.Fix = "echo kernel.sysrq=176 | sudo tee /etc/sysctl.d/90-sysrq.conf && sudo sysctl --system"
	}
	return c
}

func checkPstore() Check {
	return pstoreCheck(crashEnv{
		sys:       "/sys",
		lookPath:  lookPathSbin,
		enabled:   unitEnabled,
		pm:        analyze.DetectEnv("").PkgManager,
		suggested: kdumpSuggestedMB,
		folders: func() []pstore.Attempt {
			return pstore.KdumpFolders(pstore.KdumpDir, time.Time{})
		},
		handled: func() time.Time {
			if fi, err := os.Stat(kdumpGrubFile); err == nil {
				return fi.ModTime()
			}
			return time.Time{}
		},
	})
}

// crashEnv is what the kernel-crash-log check looks at (replaced in tests).
type crashEnv struct {
	sys       string                       // /sys
	lookPath  func(string) (string, error) // finds commands
	enabled   func(unit string) bool       // systemd unit enabled?
	pm        string                       // apt, dnf, ...
	suggested func() int                   // kdump's own memory estimate in MB, 0 if unknown
	folders   func() []pstore.Attempt      // kdump's folders in /var/crash, oldest first
	handled   func() time.Time             // when BeeBox last changed kdump's memory
}

// kdumpGrubFile is BeeBox's own boot-menu setting for kdump's memory. It
// is read after kdump-tools.cfg and the kernel uses the last crashkernel=,
// so it wins without editing kdump-tools' own file.
const kdumpGrubFile = "/etc/default/grub.d/zz-beebox-kdump.cfg"

// kdumpMemory is how much memory kdump keeps, by the computer's memory:
// Ubuntu's sizes, which also suit Kali's large kernels, and kdump-tools'
// own for small machines.
const kdumpMemory = "512M-2G:192M,2G-4G:320M,4G-32G:512M,32G-:1G"

// reserveKdump sets kdump's memory reservation in the boot menu.
func reserveKdump(spec string) string {
	return "sudo mkdir -p /etc/default/grub.d && echo 'GRUB_CMDLINE_LINUX_DEFAULT=\"$GRUB_CMDLINE_LINUX_DEFAULT crashkernel=" + spec + "\"' | sudo tee " + kdumpGrubFile + " >/dev/null && sudo update-grub"
}

// kdumpSuggestedMB asks kdump-tools how much memory its spare kernel needs
// ("crashkernel suggested size: 204M" in "kdump-config show").
func kdumpSuggestedMB() int {
	path, err := lookPathSbin("kdump-config")
	if err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "show").Output() // #nosec G204 -- fixed command
	return parseSuggestedMB(string(out))
}

func parseSuggestedMB(out string) int {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "crashkernel suggested size:"); ok {
			v = strings.TrimSuffix(strings.TrimSpace(v), "M")
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 0
}

// roundUp64 rounds MB up to a multiple of 64.
func roundUp64(mb int) int { return (mb + 63) / 64 * 64 }

// unitEnabled reports whether a systemd unit is enabled, the way
// "systemctl enable" records it: a link in a .wants directory of
// /etc/systemd/system.
func unitEnabled(unit string) bool {
	m, _ := filepath.Glob(filepath.Join("/etc/systemd/system", "*.wants", unit))
	return len(m) > 0
}

// kdumpService is the systemd unit that loads the crash kernel at every
// start-up: kdump-tools.service on the Debian family, kdump.service on
// Fedora. Kali switches newly installed services off, so it has to be
// enabled explicitly, or kdump only works until the next restart.
func kdumpService(has func(string) bool) string {
	switch {
	case has("kdump-config"):
		return "kdump-tools"
	case has("kdumpctl"):
		return "kdump"
	}
	return ""
}

func hasCmd(name string) bool { _, err := lookPathSbin(name); return err == nil }

// lookPathSbin finds administrator commands (update-grub, kdump-config)
// even when the doctor runs as a normal user, whose PATH on Debian has no
// /usr/sbin.
func lookPathSbin(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// useKdump switches kdump-tools on (Debian ships it switched off).
const useKdump = "sudo sed -i 's/^USE_KDUMP=.*/USE_KDUMP=1/' /etc/default/kdump-tools"

// pstoreCheck reports whether a kernel crash leaves a log BeeBox can read:
// pstore (UEFI variables, ramoops, ERST) or kdump, which also works on
// computers that start in legacy BIOS mode.
func pstoreCheck(e crashEnv) Check {
	c := Check{ID: "pstore", Name: "Kernel crash logs"}
	sys, lookPath, enabled, pm := e.sys, e.lookPath, e.enabled, e.pm
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(sys, rel)) // #nosec G304 -- fixed sysfs paths
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	has := func(name string) bool { _, err := lookPath(name); return err == nil }
	if b := read("module/pstore/parameters/backend"); b != "" && b != "(null)" {
		c.Status = OK
		c.Result = "Saved if the kernel crashes (backend: " + b + ")"
		return c
	}
	svc := kdumpService(has)
	if read("kernel/kexec_crash_loaded") == "1" {
		if svc != "" && !enabled(svc+".service") {
			c.Status = Warn
			c.Result = "On now, but off again after the next restart"
			c.Why = "kdump is loaded, but its service is switched off, so nothing loads it at start-up. A kernel crash after a restart would leave no trace, and BeeBox would report a sudden stop."
			c.Fix = "sudo systemctl enable " + svc
			return c
		}
		reservedMB := 0
		if n, err := strconv.ParseInt(read("kernel/kexec_crash_size"), 10, 64); err == nil {
			reservedMB = int(n >> 20)
		}
		canReserve := svc == "kdump-tools" && has("update-grub")
		need := e.suggested()
		if need == 0 && reservedMB > 0 && reservedMB < 256 {
			// kdump-tools can't estimate before its first start-up after
			// install. Current kernels (Kali's in particular) rarely start in
			// less than 256 MB, so ask for kdumpMemory's sizes instead.
			c.Status = Warn
			c.Result = fmt.Sprintf("kdump may have too little memory to save a crash (%d MB kept)", reservedMB)
			c.Why = "When the kernel crashes, kdump's spare kernel starts in the memory kept for it. Recent kernels usually need more than 256 MB; with too little, kdump fails before saving anything and the crash looks like a power cut."
			if canReserve {
				c.Fix = reserveKdump(kdumpMemory) + "   # then restart"
			}
			return c
		}
		if need > 0 && reservedMB > 0 && need > reservedMB {
			c.Status = Warn
			c.Result = fmt.Sprintf("kdump has too little memory to save a crash (%d MB kept, about %d MB needed)", reservedMB, need)
			c.Why = "When the kernel crashes, kdump's spare kernel starts in the memory kept for it. With too little, it fails before saving anything, and the crash looks like a power cut."
			if canReserve {
				c.Fix = reserveKdump(fmt.Sprintf("512M-:%dM", roundUp64(need*5/4))) + "   # then restart"
			}
			return c
		}
		if fs := e.folders(); len(fs) > 0 {
			// Only the latest crash counts: a later one that was saved
			// shows kdump works now.
			if last := fs[len(fs)-1]; !last.HasLog && last.Time.After(e.handled()) {
				c.Status = Warn
				c.Result = "The last kernel crash (" + last.Time.Local().Format("2 Jan 15:04") + ") was not saved"
				c.Why = "kdump started after that crash but saved no kernel log. It usually needs more memory; it can also be too old for this kernel (install updates), or the computer was switched off before it finished (it restarts by itself when done)."
				if canReserve && reservedMB > 0 {
					c.Fix = reserveKdump(fmt.Sprintf("512M-:%dM", roundUp64(reservedMB+128))) + "   # then restart"
				}
				return c
			}
		}
		c.Status = OK
		c.Result = "Saved if the kernel crashes (kdump, in /var/crash)"
		return c
	}
	c.Status = Info
	c.Why = "Without this, a kernel crash leaves no trace at all, so BeeBox can't tell it apart from a power cut and reports a sudden stop instead."
	uefi := fileExists(filepath.Join(sys, "firmware/efi"))
	switch {
	case !uefi:
		c.Why += " Legacy BIOS has no firmware storage for crash logs, but kdump works: it keeps about 200 MB of memory for a spare kernel that saves the crashed kernel's log to /var/crash, then restarts."
		reserved := read("kernel/kexec_crash_size")
		switch {
		case svc != "" && reserved != "" && reserved != "0":
			c.Result = "kdump is set up but not switched on"
			switch svc {
			case "kdump-tools":
				// restart, not start: a service that already ran with
				// USE_KDUMP=0 counts as started and would do nothing.
				c.Fix = useKdump + " && sudo systemctl enable kdump-tools && sudo systemctl restart kdump-tools"
			default:
				c.Fix = "sudo systemctl enable kdump && sudo systemctl restart kdump"
			}
		case svc != "":
			c.Result = "kdump is installed but has no memory reserved yet"
			switch {
			case svc == "kdump-tools" && has("update-grub"):
				c.Fix = useKdump + " && sudo systemctl enable kdump-tools && " + reserveKdump(kdumpMemory) + "   # then restart"
			case svc == "kdump-tools":
				c.Fix = useKdump + " && sudo systemctl enable kdump-tools   # then restart"
			default:
				c.Fix = "sudo kdumpctl reset-crashkernel --kernel=ALL && sudo systemctl enable kdump   # then restart"
			}
		default:
			c.Result = "Not set up: this computer starts in legacy BIOS mode, so it needs kdump"
			switch pm {
			case "apt":
				c.Fix = "sudo env DEBIAN_FRONTEND=noninteractive apt install -y kdump-tools && " + useKdump + " && sudo systemctl enable kdump-tools"
				if has("update-grub") {
					c.Fix += " && " + reserveKdump(kdumpMemory)
				}
				c.Fix += "   # then restart"
			case "dnf":
				c.Fix = "sudo dnf install -y kexec-tools && sudo kdumpctl reset-crashkernel --kernel=ALL && sudo systemctl enable kdump   # then restart"
			default:
				c.Why += " Your distribution's kdump guide explains how to set it up. A new install or a new virtual machine can use UEFI instead."
			}
		}
	case read("module/efi_pstore/parameters/pstore_disable") == "Y":
		c.Result = "Not saved: the kernel's UEFI crash log store is switched off"
		const arg = "efi_pstore.pstore_disable=0"
		if has("grubby") {
			c.Fix = "sudo grubby --update-kernel=ALL --args=" + arg + "   # then restart"
		} else if has("update-grub") {
			c.Fix = `sudo sed -i 's/^GRUB_CMDLINE_LINUX="/&` + arg + ` /' /etc/default/grub && sudo update-grub   # then restart`
		} else {
			c.Why += " To turn it on, add " + arg + " to the kernel command line and restart."
		}
	default:
		c.Result = "Not saved: the UEFI crash log store isn't loaded"
		c.Fix = "sudo modprobe efi_pstore && echo efi_pstore | sudo tee /etc/modules-load.d/beebox-pstore.conf"
	}
	return c
}

func checkNotify() Check {
	c := Check{ID: "notify", Name: "Desktop notifications"}
	_, err := exec.LookPath("notify-send")
	autostart := fileExists("/etc/xdg/autostart/beebox-notify.desktop")
	switch {
	case err == nil && autostart:
		c.Status = OK
		c.Result = "You'll be told after a crash when you log in"
	case err != nil:
		c.Status = Info
		c.Result = "notify-send is not installed"
		c.Why = "Without it, BeeBox can't pop up a message after a crash; run 'beebox last' instead."
		env := analyze.DetectEnv("")
		c.Fix = env.InstallYes(env.NotifyPackage())
	default:
		c.Status = Info
		c.Result = "Login notification is not installed"
		c.Fix = "Reinstall the beebox package, or copy packaging/xdg/beebox-notify.desktop to /etc/xdg/autostart/"
	}
	return c
}

func checkSpace(p paths.Paths) Check {
	c := Check{ID: "space", Name: "Disk space for BeeBox"}
	dir := p.StateDir
	if !fileExists(dir) {
		dir = "/var/lib"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		c.Status = Info
		c.Result = "Unknown"
		return c
	}
	free := float64(st.Bavail) * float64(st.Bsize) / 1024 / 1024 // #nosec G115
	if free < 50 {
		c.Status = Warn
		c.Result = fmt.Sprintf("Only %.0f MB free in %s", free, dir)
		c.Why = "BeeBox needs a few MB to save its recording and reports."
		return c
	}
	c.Status = OK
	c.Result = fmt.Sprintf("%.1f GB free", free/1024)
	return c
}

func checkSensors(r Recording, vm bool, pm string) Check {
	c := Check{ID: "sensors", Name: "Temperature sensors", Status: Info}
	if r.Last != nil && len(r.Last.Temps) > 0 {
		var names []string
		for _, t := range r.Last.Temps {
			names = append(names, t.Name)
		}
		c.Status = OK
		c.Result = "Found: " + strings.Join(names, ", ")
		return c
	}
	if vm {
		c.Result = "None: virtual machines have no temperature sensors"
		c.Why = "The host computer's sensors aren't passed to the virtual machine, so there is nothing to turn on here. Overheating is still caught on real hardware."
		return c
	}
	c.Result = "None found"
	c.Why = "Without sensors, BeeBox can't tell when overheating stopped the computer. Most computers have them, but some need a driver that lm-sensors finds and loads."
	switch pm {
	case "apt":
		c.Fix = "sudo apt install -y lm-sensors && sudo sensors-detect --auto   # then restart"
	case "dnf":
		c.Fix = "sudo dnf install -y lm_sensors && sudo sensors-detect --auto   # then restart"
	case "pacman":
		c.Fix = "sudo pacman -S --needed lm_sensors && sudo sensors-detect --auto   # then restart"
	case "zypper":
		c.Fix = "sudo zypper install sensors && sudo sensors-detect --auto   # then restart"
	}
	return c
}

// virtualMachine reports whether this is a virtual machine: x86 CPUs show
// a "hypervisor" flag, and the firmware names the virtual hardware.
func virtualMachine(cpuinfo, dmi string) bool {
	if b, err := os.ReadFile(cpuinfo); err == nil { // #nosec G304 -- fixed /proc path
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "flags") && strings.Contains(line+" ", " hypervisor ") {
				return true
			}
		}
	}
	for _, f := range []string{"sys_vendor", "product_name", "board_vendor"} {
		b, err := os.ReadFile(filepath.Join(dmi, f)) // #nosec G304 -- fixed sysfs paths
		if err != nil {
			continue
		}
		v := strings.ToLower(string(b))
		for _, k := range []string{"vmware", "virtualbox", "innotek", "qemu", "kvm", "xen", "parallels", "bochs", "virtual machine", "hyper-v", "cloud hypervisor"} {
			if strings.Contains(v, k) {
				return true
			}
		}
	}
	return false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()+0.5))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()+0.5))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
