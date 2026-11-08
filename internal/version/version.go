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

// Long returns the version with its build details, one per line, for
// `beebox version --verbose` and bug reports.
func Long() string {
	d := Details()
	return fmt.Sprintf("beebox %s\n  commit    %s\n  built     %s\n  go        %s\n  platform  %s",
		d["version"], orUnknown(d["commit"]), orUnknown(d["date"]), d["go"], d["platform"])
}

// Details returns the version and build details as a map, for
// `beebox version --json`.
func Details() map[string]string {
	return map[string]string{
		"version":  Short(),
		"commit":   Commit,
		"date":     Date,
		"go":       runtime.Version(),
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
