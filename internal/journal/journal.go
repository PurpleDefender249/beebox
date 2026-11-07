// Package journal reads entries for a given boot from the systemd journal by
// running journalctl. Arguments are passed as an argv array (never through a
// shell) and boot selectors are validated before use.
package journal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Entry is one journal record.
type Entry struct {
	Time   time.Time `json:"time"`
	Prio   int       `json:"prio"`
	Ident  string    `json:"ident"`
	Kernel bool      `json:"kernel"`
	Msg    string    `json:"msg"`
	BootID string    `json:"-"`
}

// Source provides journal data; tests substitute a fake.
type Source interface {
	// Entries returns up to n of the last entries of boot (a boot ID or a
	// relative offset such as "-1"). kernelOnly restricts to kernel messages.
	Entries(ctx context.Context, boot string, kernelOnly bool, n int) ([]Entry, error)
	// Warnings returns up to n entries of priority warning or more severe.
	Warnings(ctx context.Context, boot string, n int) ([]Entry, error)
}

// ErrNoBoot means the journal has no record of the requested boot, usually
// because the journal is not persistent.
var ErrNoBoot = errors.New("the journal has no record of that boot")

// ErrUnavailable means journalctl is missing.
var ErrUnavailable = errors.New("journalctl is not available")

var offsetRe = regexp.MustCompile(`^-?[0-9]{1,4}$`)
var idRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidSelector reports whether s is a boot ID or numeric offset.
func ValidSelector(s string) bool {
	return offsetRe.MatchString(s) || idRe.MatchString(s)
}

// Journalctl is the real Source.
type Journalctl struct {
	Path string // default: journalctl from PATH
}

func (j Journalctl) bin() (string, error) {
	if j.Path != "" {
		return j.Path, nil
	}
	p, err := exec.LookPath("journalctl")
	if err != nil {
		return "", ErrUnavailable
	}
	return p, nil
}

// Entries implements Source.
func (j Journalctl) Entries(ctx context.Context, boot string, kernelOnly bool, n int) ([]Entry, error) {
	args := []string{"-n", strconv.Itoa(n)}
	if kernelOnly {
		args = append(args, "-k")
	}
	entries, err := j.run(ctx, boot, args)
	if err == nil && len(entries) == 0 && !kernelOnly {
		return nil, ErrNoBoot
	}
	return entries, err
}

// Warnings implements Source.
func (j Journalctl) Warnings(ctx context.Context, boot string, n int) ([]Entry, error) {
	return j.run(ctx, boot, []string{"-p", "warning", "-n", strconv.Itoa(n)})
}

func (j Journalctl) run(ctx context.Context, boot string, extra []string) ([]Entry, error) {
	if !ValidSelector(boot) {
		return nil, fmt.Errorf("invalid boot selector %q", boot)
	}
	bin, err := j.bin()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	args := append([]string{"--no-pager", "-q", "-o", "json", "-b", boot}, extra...)
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- fixed binary, validated args
	cmd.Env = []string{"LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER="}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.ToLower(stderr.String())
		if strings.Contains(msg, "no journal boot entry") || strings.Contains(msg, "data from the specified boot") || strings.Contains(msg, "failed to look up boot") {
			return nil, ErrNoBoot
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("journalctl: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	return Parse(out)
}

// FirstEntry returns the time of the first entry of boot, i.e. roughly when
// that boot started. journalctl streams lazily, so reading one line and
// stopping is cheap even for long boots.
func (j Journalctl) FirstEntry(ctx context.Context, boot string) (time.Time, error) {
	if !ValidSelector(boot) {
		return time.Time{}, fmt.Errorf("invalid boot selector %q", boot)
	}
	bin, err := j.bin()
	if err != nil {
		return time.Time{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--no-pager", "-q", "-o", "json", "-b", boot) // #nosec G204 -- validated args
	cmd.Env = []string{"LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER="}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return time.Time{}, err
	}
	if err := cmd.Start(); err != nil {
		return time.Time{}, err
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	r := bufio.NewReaderSize(out, 64*1024)
	line, err := r.ReadBytes('\n')
	if len(line) == 0 {
		return time.Time{}, ErrNoBoot
	}
	es, perr := Parse(line)
	if perr != nil || len(es) == 0 {
		if err != nil {
			return time.Time{}, err
		}
		return time.Time{}, ErrNoBoot
	}
	return es[0].Time, nil
}

// Parse decodes journalctl -o json output (one object per line).
func Parse(b []byte) ([]Entry, error) {
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		e := Entry{Prio: 6}
		e.Msg = decodeField(raw["MESSAGE"])
		if e.Msg == "" {
			continue
		}
		if ts := decodeField(raw["__REALTIME_TIMESTAMP"]); ts != "" {
			if us, err := strconv.ParseInt(ts, 10, 64); err == nil {
				e.Time = time.UnixMicro(us)
			}
		}
		if p := decodeField(raw["PRIORITY"]); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				e.Prio = n
			}
		}
		e.Ident = decodeField(raw["SYSLOG_IDENTIFIER"])
		if e.Ident == "" {
			e.Ident = decodeField(raw["_COMM"])
		}
		e.Kernel = decodeField(raw["_TRANSPORT"]) == "kernel"
		if e.Kernel && e.Ident == "" {
			e.Ident = "kernel"
		}
		e.BootID = decodeField(raw["_BOOT_ID"])
		out = append(out, e)
	}
	return out, sc.Err()
}

// decodeField handles journal fields that are strings, or byte arrays when
// the value is not valid UTF-8.
func decodeField(r json.RawMessage) string {
	if len(r) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(r, &s); err == nil {
		return s
	}
	var bs []byte
	var nums []int
	if err := json.Unmarshal(r, &nums); err == nil {
		bs = make([]byte, 0, len(nums))
		for _, n := range nums {
			if n >= 0 && n < 256 {
				bs = append(bs, byte(n))
			}
		}
		return strings.ToValidUTF8(string(bs), "?")
	}
	return ""
}

// Persistent reports whether the journal keeps logs across reboots: either
// Storage=persistent, or Storage=auto (the default) with /var/log/journal
// present.
func Persistent(confPaths []string, varLogJournalExists bool) (bool, string) {
	storage := "auto"
	for _, p := range confPaths {
		if v := readStorage(p); v != "" {
			storage = v
		}
	}
	switch storage {
	case "persistent":
		return true, storage
	case "volatile", "none":
		return false, storage
	default:
		return varLogJournalExists, storage
	}
}
