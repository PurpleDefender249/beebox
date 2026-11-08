// Package pstore reads kernel crash logs that survive a crash: the ones the
// kernel saves to persistent storage before a panic (pstore: EFI variables,
// ramoops or ACPI ERST), and the kernel log that kdump saves from the
// crashed kernel's memory before restarting.
package pstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Record is one saved crash log.
type Record struct {
	Path   string
	Time   time.Time
	Text   string
	Source string // "pstore" or "kdump"
}

// KdumpDir is where kdump-tools (Debian, Ubuntu, Kali) and kexec-tools
// (Fedora, RHEL) save crash dumps by default.
const KdumpDir = "/var/crash"

// DefaultDirs are the live pstore mount, systemd-pstore's archive and the
// kdump directory.
var DefaultDirs = []string{"/sys/fs/pstore", "/var/lib/systemd/pstore", KdumpDir}

// maxRead caps how much of one log is read. kdump saves the crashed
// kernel's whole log, so the end, where the crash is, is what's kept.
const maxRead = 256 * 1024

// isCrashLog recognises the files that hold a kernel log:
//
//	pstore:      dmesg-efi-…, dmesg-ramoops-…, dmesg-erst-…
//	kdump-tools: /var/crash/202611081123/dmesg.202611081123
//	kexec-tools: /var/crash/127.0.0.1-2026-11-08-11:23:41/vmcore-dmesg.txt
func isCrashLog(name string) bool {
	return strings.HasPrefix(name, "dmesg") || name == "vmcore-dmesg.txt"
}

// sourceOf tells kdump's files ("dmesg.<date>", "vmcore-dmesg.txt") from
// pstore's ("dmesg-<backend>-<id>").
func sourceOf(name string) string {
	if strings.HasPrefix(name, "dmesg.") || name == "vmcore-dmesg.txt" {
		return "kdump"
	}
	return "pstore"
}

// trustedUID owns every genuine crash log (tests change it).
var trustedUID uint32

// Find returns crash logs written at or after since.
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
			if !d.Type().IsRegular() || !isCrashLog(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.ModTime().Before(since) {
				return nil
			}
			b, err := readTail(path)
			if err != nil || len(b) == 0 {
				return nil
			}
			out = append(out, Record{Path: path, Time: info.ModTime(), Text: string(b), Source: sourceOf(d.Name())})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// errUntrusted is returned for a file that the kernel, systemd-pstore or
// kdump can't have written.
var errUntrusted = errors.New("not a root-owned regular file")

// readTail reads a whole small file, or the last maxRead bytes of a large
// one, starting at a line boundary.
//
// /var/crash is world-writable on Ubuntu (apport uses it), so a file there
// is only trusted if it is a regular file owned by root with a single link:
// a user can't plant a fake crash log, or point a link at a secret file and
// have it copied into a report.
func readTail(path string) ([]byte, error) {
	// #nosec G304 -- paths found under fixed crash-log dirs, opened without following links
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || !ok || st.Uid != trustedUID || st.Nlink != 1 {
		return nil, errUntrusted
	}
	size := fi.Size()
	skip := int64(0)
	if size > maxRead {
		skip = size - maxRead
		if _, err := f.Seek(skip, io.SeekStart); err != nil {
			return nil, err
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, maxRead))
	if err != nil && len(b) == 0 {
		return nil, err
	}
	if skip > 0 {
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, nil
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

// KdumpReady reports whether a crash kernel is loaded, so that kdump will
// save the kernel log if this kernel crashes.
func KdumpReady() bool { return kdumpReady("/sys") }

func kdumpReady(sys string) bool {
	b, err := os.ReadFile(filepath.Join(sys, "kernel/kexec_crash_loaded")) // #nosec G304 -- fixed sysfs path
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

// Available reports whether this computer saves a kernel crash log that
// survives a restart, by either means.
func Available() bool { return Backend() != "" || KdumpReady() }

// WaitForKdump gives kdump's service time to load the crash kernel during
// start-up when memory is reserved for it but it isn't loaded yet, so that
// an analysis made right after boot knows crash logs are switched on. It
// returns at once when kdump is ready or not set up, and after max at most.
func WaitForKdump(ctx context.Context, max time.Duration) {
	waitForKdump(ctx, "/sys", max, time.Second)
}

func waitForKdump(ctx context.Context, sys string, max, every time.Duration) {
	if kdumpReady(sys) || !crashMemoryReserved(sys) {
		return
	}
	deadline := time.NewTimer(max)
	defer deadline.Stop()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
			if kdumpReady(sys) {
				return
			}
		}
	}
}

// crashMemoryReserved reports whether the kernel set memory aside for a
// crash kernel (the crashkernel= boot option).
func crashMemoryReserved(sys string) bool {
	b, err := os.ReadFile(filepath.Join(sys, "kernel/kexec_crash_size")) // #nosec G304 -- fixed sysfs path
	s := strings.TrimSpace(string(b))
	return err == nil && s != "" && s != "0"
}
