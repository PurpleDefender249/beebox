// Package pstore reads kernel crash logs that survive a crash: the ones the
// kernel saves to persistent storage before a panic (pstore: EFI variables,
// ramoops or ACPI ERST), and the kernel log that kdump saves from the
// crashed kernel's memory before restarting.
package pstore

import (
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

// Attempt is a kdump save that started but left no kernel log: kdump
// creates a dated folder in /var/crash first, saves the log, then the
// memory dump. A folder with no log means the kernel crashed, but the
// spare kernel ran out of memory, couldn't read the log (a kernel too new
// for makedumpfile) or was switched off before it finished.
type Attempt struct {
	Path   string
	Time   time.Time
	Files  []string // what kdump did save, e.g. "dump-incomplete"
	HasLog bool     // the kernel log was saved
}

// KdumpAttempts returns kdump folders in dir modified at or after since
// that hold no kernel log.
func KdumpAttempts(dir string, since time.Time) []Attempt {
	var out []Attempt
	for _, a := range KdumpFolders(dir, since) {
		if !a.HasLog {
			out = append(out, a)
		}
	}
	return out
}

// KdumpFolders returns every kdump folder in dir modified at or after
// since, oldest first. Only root-owned folders with kdump's names are
// considered, because /var/crash is world-writable on Ubuntu. Listing them
// needs no root.
func KdumpFolders(dir string, since time.Time) []Attempt {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Attempt
	for _, e := range ents {
		if !e.IsDir() || !kdumpStamp(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fi, err := os.Lstat(path)
		if err != nil || !fi.IsDir() || fi.ModTime().Before(since) {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != trustedUID {
			continue
		}
		files, _ := os.ReadDir(path)
		a := Attempt{Path: path, Time: fi.ModTime()}
		for _, f := range files {
			if isCrashLog(f.Name()) {
				a.HasLog = true
			}
			a.Files = append(a.Files, f.Name())
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// kdumpStamp recognises kdump's folder names: kdump-tools' "202611081123"
// and kexec-tools' "127.0.0.1-2026-11-08-11:23:41".
func kdumpStamp(name string) bool {
	digits := func(s string) bool {
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return s != ""
	}
	if len(name) == 12 && digits(name) {
		return true
	}
	host, date, ok := strings.Cut(name, "-")
	return ok && host != "" && len(date) == len("2026-11-08-11:23:41") && digits(date[:4])
}
