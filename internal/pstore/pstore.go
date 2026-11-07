// Package pstore reads crash logs that the kernel saved to persistent
// storage (EFI variables, ramoops or ACPI ERST) before a panic.
package pstore

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Record is one saved crash log.
type Record struct {
	Path string
	Time time.Time
	Text string
}

// DefaultDirs are the live pstore mount and systemd-pstore's archive.
var DefaultDirs = []string{"/sys/fs/pstore", "/var/lib/systemd/pstore"}

const maxRead = 128 * 1024

// Find returns dmesg-type records modified at or after since.
func Find(dirs []string, since time.Time) []Record {
	var out []Record
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if strings.Count(strings.TrimPrefix(path, dir), "/") > 2 {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasPrefix(name, "dmesg") {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.ModTime().Before(since) {
				return nil
			}
			b, err := readLimited(path)
			if err != nil || len(b) == 0 {
				return nil
			}
			out = append(out, Record{Path: path, Time: info.ModTime(), Text: string(b)})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- paths found under fixed pstore dirs
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, maxRead)
	n, err := f.Read(b)
	if n > 0 {
		return b[:n], nil
	}
	return nil, err
}

// Backend returns the active pstore backend name, or "" if none.
func Backend() string {
	b, err := os.ReadFile("/sys/module/pstore/parameters/backend")
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "(null)" {
		return ""
	}
	return s
}
