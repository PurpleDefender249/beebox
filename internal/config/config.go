// Package config loads /etc/beebox/beebox.conf, a minimal "key = value" file.
// The file is optional: a missing file means defaults.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds recorder settings.
type Config struct {
	Interval     time.Duration // time between samples
	Window       time.Duration // how much history the ring keeps
	Flush        time.Duration // how often samples are forced to disk
	TopProcesses int           // apps listed per sample
	KeepReports  int           // crash reports retained
	KeepRings    int           // raw rings from earlier boots retained
}

// Default returns the shipped defaults.
func Default() Config {
	return Config{
		Interval:     time.Second,
		Window:       15 * time.Minute,
		Flush:        5 * time.Second,
		TopProcesses: 5,
		KeepReports:  50,
		KeepRings:    10,
	}
}

// Slots is the number of ring slots needed for Window at Interval.
func (c Config) Slots() int {
	n := int(c.Window / c.Interval)
	if n < 60 {
		n = 60
	}
	return n
}

// Load reads path. A missing file yields defaults and no error. Warnings
// describe values that were invalid and replaced by defaults or clamped.
func Load(path string) (Config, []string, error) {
	f, err := os.Open(path) // #nosec G304 -- admin-controlled config path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil, nil
		}
		return Default(), nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads configuration from r.
func Parse(r io.Reader) (Config, []string, error) {
	c := Default()
	var warns []string
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		key, val, ok := strings.Cut(text, "=")
		if !ok {
			warns = append(warns, fmt.Sprintf("line %d: expected key = value, ignored", line))
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if i := strings.Index(val, " #"); i >= 0 {
			val = val[:i]
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if err := c.set(key, val); err != nil {
			warns = append(warns, fmt.Sprintf("line %d: %v", line, err))
		}
	}
	if err := sc.Err(); err != nil {
		return Default(), warns, fmt.Errorf("read config: %w", err)
	}
	warns = append(warns, c.clamp()...)
	return c, warns, nil
}

func (c *Config) set(key, val string) error {
	dur := func(dst *time.Duration) error {
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("%s: %q is not a duration (examples: 1s, 500ms, 15m)", key, val)
		}
		*dst = d
		return nil
	}
	num := func(dst *int) error {
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("%s: %q is not a whole number", key, val)
		}
		*dst = n
		return nil
	}
	switch key {
	case "interval":
		return dur(&c.Interval)
	case "window":
		return dur(&c.Window)
	case "flush":
		return dur(&c.Flush)
	case "top_processes":
		return num(&c.TopProcesses)
	case "keep_reports":
		return num(&c.KeepReports)
	case "keep_rings":
		return num(&c.KeepRings)
	default:
		return fmt.Errorf("unknown setting %q, ignored", key)
	}
}

func (c *Config) clamp() []string {
	var w []string
	d := Default()
	clampDur := func(name string, v *time.Duration, lo, hi, def time.Duration) {
		if *v < lo || *v > hi {
			w = append(w, fmt.Sprintf("%s must be between %s and %s; using %s", name, lo, hi, def))
			*v = def
		}
	}
	clampInt := func(name string, v *int, lo, hi, def int) {
		if *v < lo || *v > hi {
			w = append(w, fmt.Sprintf("%s must be between %d and %d; using %d", name, lo, hi, def))
			*v = def
		}
	}
	clampDur("interval", &c.Interval, 250*time.Millisecond, 10*time.Second, d.Interval)
	clampDur("window", &c.Window, time.Minute, 2*time.Hour, d.Window)
	clampDur("flush", &c.Flush, c.Interval, time.Minute, d.Flush)
	clampInt("top_processes", &c.TopProcesses, 1, 10, d.TopProcesses)
	clampInt("keep_reports", &c.KeepReports, 1, 1000, d.KeepReports)
	clampInt("keep_rings", &c.KeepRings, 1, 100, d.KeepRings)
	if c.Slots() > 14400 {
		w = append(w, "window/interval needs more than 14400 slots; using defaults")
		c.Interval, c.Window = d.Interval, d.Window
	}
	return w
}
