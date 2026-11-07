// Package paths centralises every filesystem location BeeBox uses, so tests and
// development runs can relocate them with BEEBOX_STATE_DIR / BEEBOX_RUNTIME_DIR.
package paths

import (
	"os"
	"path/filepath"
)

// Default locations. They match the systemd unit's StateDirectory=,
// RuntimeDirectory= and ConfigurationDirectory= settings.
const (
	DefaultStateDir   = "/var/lib/beebox"
	DefaultRuntimeDir = "/run/beebox"
	DefaultConfigFile = "/etc/beebox/beebox.conf"
)

// Paths is the resolved set of locations for one invocation.
type Paths struct {
	StateDir   string
	RuntimeDir string
	ConfigFile string
}

// FromEnv resolves locations, honouring the BEEBOX_* override variables.
func FromEnv() Paths {
	p := Paths{
		StateDir:   DefaultStateDir,
		RuntimeDir: DefaultRuntimeDir,
		ConfigFile: DefaultConfigFile,
	}
	if v := os.Getenv("BEEBOX_STATE_DIR"); v != "" {
		p.StateDir = v
	}
	if v := os.Getenv("BEEBOX_RUNTIME_DIR"); v != "" {
		p.RuntimeDir = v
	}
	if v := os.Getenv("BEEBOX_CONFIG"); v != "" {
		p.ConfigFile = v
	}
	return p
}

// RingFile is the ring buffer for the current boot.
func (p Paths) RingFile() string { return filepath.Join(p.StateDir, "current.ring") }

// BootsDir holds archived rings from earlier boots.
func (p Paths) BootsDir() string { return filepath.Join(p.StateDir, "boots") }

// ReportsDir holds JSON crash reports.
func (p Paths) ReportsDir() string { return filepath.Join(p.StateDir, "reports") }

// AnalyzingMarker exists while the recorder analyses the previous boot.
func (p Paths) AnalyzingMarker() string { return filepath.Join(p.RuntimeDir, "analyzing") }

// UserStateDir is where per-user state (e.g. last notified report) lives.
func UserStateDir() string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "beebox")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "beebox-state")
	}
	return filepath.Join(home, ".local", "state", "beebox")
}

// UserCacheDir is where rendered HTML reports are written for viewing.
func UserCacheDir() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "beebox")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "beebox-cache")
	}
	return filepath.Join(home, ".cache", "beebox")
}
