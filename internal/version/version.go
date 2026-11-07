// Package version exposes build metadata injected at link time.
//
//	go build -ldflags "-X github.com/PurpleDefender249/beebox/internal/version.Version=v0.1.0 \
//	    -X github.com/PurpleDefender249/beebox/internal/version.Commit=$(git rev-parse --short HEAD) \
//	    -X github.com/PurpleDefender249/beebox/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set via -ldflags at build time.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Short returns the version string, falling back to module build info.
func Short() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

// Long returns a multi-field description suitable for `beebox version`.
func Long() string {
	commit := Commit
	if commit == "" {
		commit = "unknown"
	}
	date := Date
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("beebox %s (commit %s, built %s, %s, %s/%s)",
		Short(), commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
