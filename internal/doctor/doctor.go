// Package doctor checks that a machine is set up so BeeBox can explain the
// next crash, and says exactly how to fix anything that is missing.
package doctor

import (
	"bytes"
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
	cs = append(cs, checkSensors(rec))
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
		c.Fix = "sudo systemctl enable --now beebox && systemctl status beebox"
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
		c.Fix = "Set Storage=persistent in /etc/systemd/journald.conf, then: sudo systemctl restart systemd-journald"
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
	c.Fix = "Add psi=1 to the kernel command line (for GRUB: edit GRUB_CMDLINE_LINUX in /etc/default/grub, update GRUB, reboot)"
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

func checkPstore() Check { return pstoreCheck("/sys", exec.LookPath) }

// pstoreCheck reports whether a kernel crash can leave a log that survives
// the reboot. sys is the sysfs root and lookPath finds programs; both are
// parameters for tests. Fix is always a command (it is printed as one).
func pstoreCheck(sys string, lookPath func(string) (string, error)) Check {
	c := Check{ID: "pstore", Name: "Kernel crash logs"}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(sys, rel)) // #nosec G304 -- fixed sysfs paths
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	if b := read("module/pstore/parameters/backend"); b != "" && b != "(null)" {
		c.Status = OK
		c.Result = "Saved if the kernel crashes (backend: " + b + ")"
		return c
	}
	c.Status = Info
	c.Why = "Without this, a kernel crash leaves no trace at all, so BeeBox can't tell it apart from a power cut and reports a sudden stop instead."
	uefi := fileExists(filepath.Join(sys, "firmware/efi"))
	switch {
	case !uefi:
		c.Result = "Not available: this computer starts in legacy BIOS mode"
		c.Why += " Crash logs need UEFI firmware. Switching an existing install from BIOS to UEFI usually stops it booting, so only choose UEFI for a new install or a new virtual machine."
	case read("module/efi_pstore/parameters/pstore_disable") == "Y":
		c.Result = "Not saved: the kernel's UEFI crash log store is switched off"
		const arg = "efi_pstore.pstore_disable=0"
		if _, err := lookPath("grubby"); err == nil {
			c.Fix = "sudo grubby --update-kernel=ALL --args=" + arg + "   # then restart"
		} else if _, err := lookPath("update-grub"); err == nil {
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
		c.Fix = env.Install(env.NotifyPackage())
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

func checkSensors(r Recording) Check {
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
	c.Result = "None found (normal in virtual machines)"
	return c
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
