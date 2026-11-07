package analyze

import (
	"bufio"
	"os"
	"strings"

	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/report"
)

// DetectEnv inspects the running system to tailor advice.
func DetectEnv(procDir string) Env {
	if procDir == "" {
		procDir = "/proc"
	}
	return Env{PkgManager: detectPkgManager("/etc/os-release"), OOMProtector: detectOOMProtector(procDir), CrashLogs: pstore.Backend() != ""}
}

func detectPkgManager(osRelease string) string {
	f, err := os.Open(osRelease) // #nosec G304 -- fixed path
	if err != nil {
		return ""
	}
	defer f.Close()
	var ids []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		if k == "ID" || k == "ID_LIKE" {
			ids = append(ids, strings.Fields(v)...)
		}
	}
	for _, id := range ids {
		switch id {
		case "debian", "ubuntu", "linuxmint", "pop", "elementary", "zorin", "kali", "raspbian":
			return "apt"
		case "fedora", "rhel", "centos", "rocky", "almalinux", "nobara":
			return "dnf"
		case "arch", "manjaro", "endeavouros", "cachyos", "garuda":
			return "pacman"
		case "opensuse", "suse", "opensuse-tumbleweed", "opensuse-leap", "sles":
			return "zypper"
		}
	}
	return ""
}

func detectOOMProtector(procDir string) string {
	d, err := os.ReadDir(procDir)
	if err != nil {
		return ""
	}
	for _, e := range d {
		n := e.Name()
		if n == "" || n[0] < '0' || n[0] > '9' {
			continue
		}
		b, err := os.ReadFile(procDir + "/" + n + "/comm") // #nosec G304 -- /proc paths
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(b)) {
		case "systemd-oomd":
			return "systemd-oomd"
		case "earlyoom":
			return "earlyoom"
		case "nohang":
			return "nohang"
		}
	}
	return ""
}

// Install returns a package install command for this distribution.
func (e Env) Install(pkg string) string { return e.install(pkg) }

// ZramCommand returns a command that sets up compressed swap in RAM.
func (e Env) ZramCommand() string {
	switch e.PkgManager {
	case "apt":
		return "sudo apt install zram-tools"
	case "dnf":
		return "sudo dnf install zram-generator-defaults && sudo systemctl restart systemd-zram-setup@zram0"
	case "pacman":
		return "sudo pacman -S zram-generator && printf '[zram0]\\n' | sudo tee /etc/systemd/zram-generator.conf && sudo systemctl daemon-reload && sudo systemctl start systemd-zram-setup@zram0"
	case "zypper":
		return "sudo zypper install systemd-zram-service && sudo zramswapon"
	default:
		return ""
	}
}

// NotifyPackage is the package that provides notify-send.
func (e Env) NotifyPackage() string {
	if e.PkgManager == "apt" {
		return "libnotify-bin"
	}
	return "libnotify"
}

func (e Env) install(pkg string) string {
	switch e.PkgManager {
	case "apt":
		return "sudo apt install " + pkg
	case "dnf":
		return "sudo dnf install " + pkg
	case "pacman":
		return "sudo pacman -S " + pkg
	case "zypper":
		return "sudo zypper install " + pkg
	default:
		return ""
	}
}

// update returns the command that installs pending updates.
func (e Env) update() string {
	switch e.PkgManager {
	case "apt":
		return "sudo apt update && sudo apt upgrade"
	case "dnf":
		return "sudo dnf upgrade --refresh"
	case "pacman":
		return "sudo pacman -Syu"
	case "zypper":
		return "sudo zypper update"
	default:
		return ""
	}
}

func adv(text, cmd string) report.Advice { return report.Advice{Text: text, Command: cmd} }

func shareAdvice() report.Advice {
	return adv("If you report this as a bug, attach the full report (it contains no personal files or command-line arguments).", "beebox show --json > beebox-report.json")
}
