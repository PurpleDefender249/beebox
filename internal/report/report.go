// Package report defines BeeBox crash reports and stores them on disk.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Schema is bumped on incompatible changes to Report.
const Schema = 1

// Kind classifies a finding.
type Kind string

const (
	// KindCause explains why the system stopped.
	KindCause Kind = "cause"
	// KindContext is a relevant observation that is not the cause.
	KindContext Kind = "context"
)

// Finding is one conclusion with its supporting evidence.
type Finding struct {
	ID         string   `json:"id"`
	Kind       Kind     `json:"kind"`
	Title      string   `json:"title"`
	Confidence int      `json:"confidence"` // 0-100
	Summary    string   `json:"summary"`
	Advice     []Advice `json:"advice,omitempty"`
	Evidence   []Line   `json:"evidence,omitempty"`
}

// Advice is one suggested step, optionally with a command to run.
type Advice struct {
	Text    string `json:"text"`
	Command string `json:"command,omitempty"`
}

// Line is a timestamped piece of evidence.
type Line struct {
	Time   time.Time `json:"time,omitempty"`
	Source string    `json:"source"` // kernel, journal, pstore, recorder
	Text   string    `json:"text"`
}

// Point is one timeline entry, derived from a recorded sample.
type Point struct {
	T         time.Time `json:"t"`
	MemAvail  float64   `json:"mem_avail_pct"`
	SwapUsed  float64   `json:"swap_used_pct"` // -1 without swap
	MemStall  float64   `json:"mem_stall_pct"`
	IOStall   float64   `json:"io_stall_pct"`
	CPU       float64   `json:"cpu_pct"`
	Temp      float64   `json:"temp_c"`      // hottest sensor, 0 if none
	Battery   int       `json:"battery_pct"` // -1 without battery
	LagMs     int64     `json:"lag_ms"`
	OnBattery bool      `json:"on_battery,omitempty"`
}

// App is an application's resource use at a point in time.
type App struct {
	Name     string  `json:"name"`
	Count    int     `json:"processes"`
	MemMB    float64 `json:"mem_mb"`
	CPU      float64 `json:"cpu_pct"`
	GrowthMB float64 `json:"growth_mb,omitempty"`
}

// Sources records which evidence was available.
type Sources struct {
	Ring    bool `json:"ring"`
	Journal bool `json:"journal"`
	Kernel  bool `json:"kernel_log"`
	Pstore  bool `json:"pstore"`
}

// Report is the analysis of one boot.
type Report struct {
	Schema      int       `json:"schema"`
	ID          string    `json:"id"`
	BootID      string    `json:"boot_id"`
	BootStart   time.Time `json:"boot_start,omitempty"`
	LastSeen    time.Time `json:"last_seen"`
	Kernel      string    `json:"kernel,omitempty"`
	Clean       bool      `json:"clean_shutdown"`
	Verdict     Finding   `json:"verdict"`
	Others      []Finding `json:"other_findings,omitempty"`
	Context     []Finding `json:"context,omitempty"`
	Timeline    []Point   `json:"timeline,omitempty"`
	TopMem      []App     `json:"top_memory,omitempty"`
	TopMemAt    time.Time `json:"top_memory_at,omitzero"` // when TopMem was taken, if not at the end
	TopCPU      []App     `json:"top_cpu,omitempty"`
	Sources     Sources   `json:"sources"`
	Notes       []string  `json:"notes,omitempty"`
	Sample      bool      `json:"sample,omitempty"` // made by `beebox view --sample`
	GeneratedAt time.Time `json:"generated_at"`
	Generator   string    `json:"generator"`
}

// Uptime is how long the analysed boot ran.
func (r *Report) Uptime() time.Duration {
	if r.BootStart.IsZero() || r.LastSeen.Before(r.BootStart) {
		return 0
	}
	return r.LastSeen.Sub(r.BootStart)
}

// MakeID builds a sortable report ID from the end time and boot ID.
func MakeID(lastSeen time.Time, bootID string) string {
	b := bootID
	if len(b) > 8 {
		b = b[:8]
	}
	if b == "" {
		b = "unknown"
	}
	return lastSeen.UTC().Format("20060102-150405") + "-" + b
}

var idRe = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-z]{1,8}$`)

// ValidID reports whether id has the report ID shape. It also prevents
// path traversal when IDs come from the command line.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Store reads and writes reports in a directory.
type Store struct {
	Dir string
}

// ErrNotFound is returned when no report matches.
var ErrNotFound = errors.New("report not found")

// Save writes r atomically (temp file, fsync, rename).
func (s Store) Save(r *Report) (string, error) {
	if !ValidID(r.ID) {
		return "", fmt.Errorf("invalid report id %q", r.ID)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil { // #nosec G301 -- reports are world-readable by design
		return "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	final := filepath.Join(s.Dir, r.ID+".json")
	tmp, err := os.CreateTemp(s.Dir, ".report-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return final, nil
}

// Load reads one report by ID, or by unique ID prefix.
func (s Store) Load(id string) (*Report, error) {
	ids, err := s.IDs()
	if err != nil {
		return nil, err
	}
	var match []string
	for _, x := range ids {
		if x == id {
			match = []string{x}
			break
		}
		if strings.HasPrefix(x, id) {
			match = append(match, x)
		}
	}
	switch len(match) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return s.loadExact(match[0])
	default:
		return nil, fmt.Errorf("%q matches %d reports; give more of the ID", id, len(match))
	}
}

func (s Store) loadExact(id string) (*Report, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, id+".json")) // #nosec G304 -- id validated
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return Decode(b)
}

// Decode parses a report.
func Decode(b []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	if r.Schema > Schema {
		return nil, fmt.Errorf("report schema %d is newer than this beebox understands (%d); please upgrade", r.Schema, Schema)
	}
	return &r, nil
}

// Latest returns the newest report.
func (s Store) Latest() (*Report, error) {
	ids, err := s.IDs()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrNotFound
	}
	return s.loadExact(ids[len(ids)-1])
}

// IDs lists report IDs oldest first.
func (s Store) IDs() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if ValidID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// All loads every report, oldest first, skipping unreadable files.
func (s Store) All() ([]*Report, error) {
	ids, err := s.IDs()
	if err != nil {
		return nil, err
	}
	out := make([]*Report, 0, len(ids))
	for _, id := range ids {
		if r, err := s.loadExact(id); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// Prune keeps the newest keep reports.
func (s Store) Prune(keep int) error {
	ids, err := s.IDs()
	if err != nil {
		return err
	}
	for len(ids) > keep {
		if err := os.Remove(filepath.Join(s.Dir, ids[0]+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		ids = ids[1:]
	}
	return nil
}
