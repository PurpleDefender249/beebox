package analyze

import (
	"fmt"
	"strings"
	"time"
)

func pct(x float64) string {
	switch {
	case x < 0:
		return "n/a"
	case x < 10 && x != float64(int(x)):
		return fmt.Sprintf("%.1f%%", x)
	default:
		return fmt.Sprintf("%.0f%%", x)
	}
}

// fmtKB formats kibibytes for people.
func fmtKB(kb uint64) string {
	mb := float64(kb) / 1024
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

func roundDur(d time.Duration) string {
	switch {
	case d >= time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	default:
		return fmt.Sprintf("%d s", int(d.Seconds()+0.5))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

var friendly = map[string]string{
	"firefox": "Firefox", "firefox-bin": "Firefox", "firefox-esr": "Firefox", "x-www-browser": "Web browser", "Web Content": "Firefox", "Isolated Web Co": "Firefox",
	"chrome": "Google Chrome", "google-chrome": "Google Chrome", "chromium": "Chromium", "chromium-browser": "Chromium",
	"brave": "Brave", "msedge": "Microsoft Edge", "opera": "Opera", "vivaldi-bin": "Vivaldi",
	"code": "VS Code", "code-oss": "VS Code", "codium": "VSCodium", "cursor": "Cursor",
	"thunderbird": "Thunderbird", "slack": "Slack", "discord": "Discord", "Discord": "Discord",
	"teams-for-linux": "Teams", "zoom": "Zoom", "spotify": "Spotify", "steam": "Steam",
	"gnome-shell": "GNOME Shell", "plasmashell": "KDE Plasma", "kwin_wayland": "KWin", "kwin_x11": "KWin",
	"Xorg": "Xorg", "Xwayland": "Xwayland", "java": "Java", "python3": "Python", "python": "Python",
	"node": "Node.js", "qemu-system-x86_64": "QEMU virtual machine", "VirtualBoxVM": "VirtualBox VM",
	"gimp": "GIMP", "blender": "Blender", "obs": "OBS Studio", "libreoffice": "LibreOffice", "soffice.bin": "LibreOffice",
	"dotnet": ".NET", "electron": "an Electron app", "tracker-miner-fs-3": "GNOME file indexer", "baloo_file": "KDE file indexer",
}

// displayName turns a process name into what people call the app.
func displayName(name string) string {
	if f, ok := friendly[name]; ok {
		return f
	}
	return name
}

func sensorLabel(name string) string {
	switch {
	case strings.HasPrefix(name, "cpu"):
		return "processor"
	case strings.HasPrefix(name, "gpu"):
		return "graphics card"
	case strings.HasPrefix(name, "nvme"), strings.HasPrefix(name, "disk"):
		return "drive"
	case strings.HasPrefix(name, "board"):
		return "motherboard"
	default:
		return name
	}
}

// DisplayName is exported for renderers.
func DisplayName(name string) string { return displayName(name) }

// ConfidenceWord describes a confidence score in words.
func ConfidenceWord(c int) string {
	switch {
	case c >= 80:
		return "Very likely"
	case c >= 60:
		return "Likely"
	case c >= 40:
		return "Possibly"
	case c > 0:
		return "Unclear"
	default:
		return "Unknown"
	}
}
