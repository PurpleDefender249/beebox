// Package recorder is the BeeBox daemon: it samples the system once per
// interval into a crash-safe ring and, at start-up, analyses the previous
// boot if it ended badly.
package recorder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/config"
	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
	"github.com/PurpleDefender249/beebox/internal/sdnotify"
	"github.com/PurpleDefender249/beebox/internal/version"
)

// Options configures the recorder.
type Options struct {
	Config  config.Config
	Paths   paths.Paths
	Log     *slog.Logger
	Journal journal.Source // nil = journalctl
	// AnalysisDelay lets journald settle before the previous boot is read.
	AnalysisDelay time.Duration
	// Collector overrides /proc, /sys etc. (tests).
	Collector sample.Options
}

// Run records until ctx is cancelled, then marks the ring clean.
func Run(ctx context.Context, opt Options) error {
	log := opt.Log
	if log == nil {
		log = slog.Default()
	}
	if opt.Journal == nil {
		opt.Journal = journal.Journalctl{}
	}
	cfg := opt.Config
	p := opt.Paths

	// Keep the recorder small and resident: it has to keep working while
	// the rest of the system is thrashing.
	debug.SetMemoryLimit(48 << 20)
	debug.SetGCPercent(50)
	// MCL_ONFAULT (Linux 4.4+) locks pages as they are touched instead of
	// pre-faulting every mapping, so the resident size stays a few MB.
	const mclOnFault = 4
	if err := syscall.Mlockall(syscall.MCL_CURRENT | syscall.MCL_FUTURE | mclOnFault); err != nil {
		log.Warn("could not lock recorder memory; it may stall under heavy memory pressure", "err", err)
	}

	for _, d := range []string{p.StateDir, p.BootsDir(), p.ReportsDir(), p.RuntimeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil { // #nosec G301 -- world-readable state, see PRIVACY.md
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	unlock, err := lockDir(p.StateDir)
	if err != nil {
		return err
	}
	defer unlock()

	bootID, err := sample.BootID(opt.Collector.ProcDir)
	if err != nil {
		return fmt.Errorf("read boot id: %w", err)
	}
	bootStart := sample.BootTime(opt.Collector.ProcDir)
	kernel := sample.KernelRelease(opt.Collector.ProcDir)

	slotCount := cfg.Slots()
	w, prev, err := openRing(p, cfg, slotCount, bootID, bootStart, kernel, log)
	if err != nil {
		return err
	}
	clean := false
	defer func() {
		if err := w.Close(clean); err != nil {
			log.Error("closing ring", "err", err)
		}
	}()

	copt := withTop(opt.Collector, cfg.TopProcesses)
	copt.Async = true
	col := sample.NewCollector(ctx, copt)
	col.Prime()
	log.Info("recording",
		"interval", cfg.Interval, "window", cfg.Window, "slots", slotCount,
		"psi", col.HasPSI(), "kernel_log", col.KernelLogAvailable(),
		"sensors", strings.Join(col.SensorNames(), ","), "battery", col.HasBattery(),
		"version", version.Short())
	_ = sdnotify.Notify("READY=1\nSTATUS=Recording")

	if prev != nil {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(opt.AnalysisDelay):
			}
			analysePrevious(ctx, opt, *prev, log)
		}()
	}

	return loop(ctx, cfg, w, col, log, &clean)
}

func withTop(o sample.Options, top int) sample.Options {
	o.Top = top
	return o
}

// previousBoot describes an archived ring that needs analysis.
type previousBoot struct {
	ringPath string
	header   ring.Header
}

// openRing archives a ring left over from an earlier boot (returning it for
// analysis), resumes a ring from this boot after a restart, or creates one.
func openRing(p paths.Paths, cfg config.Config, slots int, bootID string, bootStart time.Time, kernel string, log *slog.Logger) (*ring.Writer, *previousBoot, error) {
	var prev *previousBoot
	path := p.RingFile()
	hdr, err := ring.ReadHeader(path)
	switch {
	case err == nil && hdr.BootID == bootID && hdr.SlotCount == slots && hdr.Interval == cfg.Interval:
		w, err := ring.Resume(path)
		if err == nil {
			log.Info("resumed recording for this boot", "samples", w.Seq())
			return w, nil, nil
		}
		log.Warn("could not resume ring; starting a new one", "err", err)
		archive(p, path, hdr, "restart", log)
	case err == nil:
		dst := archive(p, path, hdr, "", log)
		if hdr.BootID != bootID && dst != "" {
			prev = &previousBoot{ringPath: dst, header: hdr}
		}
	case errors.Is(err, fs.ErrNotExist):
		// First run, nothing to archive.
	default:
		log.Warn("existing ring is unreadable; replacing it", "err", err)
		_ = os.Rename(path, path+".corrupt")
	}
	w, err := ring.Create(path, ring.Header{
		SlotCount: slots, SlotSize: ring.DefaultSlotSize, Interval: cfg.Interval,
		BootID: bootID, BootStart: bootStart, Kernel: kernel,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ring: %w", err)
	}
	pruneBoots(p.BootsDir(), cfg.KeepRings, log)
	return w, prev, nil
}

func archive(p paths.Paths, path string, hdr ring.Header, tag string, log *slog.Logger) string {
	id := hdr.BootID
	if len(id) > 8 {
		id = id[:8]
	}
	name := hdr.Created.UTC().Format("20060102-150405") + "-" + id
	if tag != "" {
		name += "-" + tag
	}
	dst := filepath.Join(p.BootsDir(), name+".ring")
	if err := os.Rename(path, dst); err != nil {
		log.Error("archiving previous ring", "err", err)
		return ""
	}
	return dst
}

func pruneBoots(dir string, keep int, log *slog.Logger) {
	m, _ := filepath.Glob(filepath.Join(dir, "*.ring"))
	sort.Strings(m)
	for len(m) > keep {
		if err := os.Remove(m[0]); err != nil {
			log.Warn("pruning old ring", "file", m[0], "err", err)
		}
		m = m[1:]
	}
}

// flusher writes the ring to disk in the background, so the sampling loop
// never waits for the disk. Samples land in locked memory straight away;
// the flusher makes them durable every cfg.Flush, or at once when a sample
// looks like trouble is starting (urgent).
type flusher struct {
	w      *ring.Writer
	urgent chan struct{}
	done   chan struct{}
}

func startFlusher(ctx context.Context, w *ring.Writer, every time.Duration, log *slog.Logger) *flusher {
	f := &flusher{w: w, urgent: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		defer close(f.done)
		t := time.NewTicker(every)
		defer t.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-f.urgent:
			}
			if err := w.Sync(); err != nil {
				failures++
				if failures == 1 || failures%60 == 0 {
					log.Error("flushing recording to disk", "err", err, "failures", failures)
				}
			} else {
				failures = 0
			}
		}
	}()
	return f
}

// now asks for an immediate flush without waiting for it.
func (f *flusher) now() {
	select {
	case f.urgent <- struct{}{}:
	default: // a flush is already queued
	}
}

// stop waits for the flusher to finish its current write.
func (f *flusher) stop() { <-f.done }

// troubling reports whether a sample shows the start of something a crash
// report would need, so it should reach the disk now rather than in a few
// seconds. Memory and disk stalls, OOM kills, a late sample and new kernel
// messages all qualify.
func troubling(s sample.Sample) bool {
	return s.PSI.MemSome >= 10 || s.PSI.MemFull >= 2 || s.PSI.IOFull >= 10 ||
		s.OOMKills > 0 || s.LagMs >= 1500 || len(s.Kmsg) > 0 ||
		(s.MemTotal > 0 && s.MemAvailPct() < 5)
}

func loop(ctx context.Context, cfg config.Config, w *ring.Writer, col *sample.Collector, log *slog.Logger, clean *bool) error {
	interval := cfg.Interval
	wd := sdnotify.WatchdogInterval()
	lastWD := time.Now()
	lastStatus := time.Now()
	first := true
	failures := 0

	// The flusher gets its own context: it must keep running after ctx is
	// cancelled, until the final sample has been appended.
	flushCtx, stopFlush := context.WithCancel(context.Background()) //nolint:contextcheck // see above
	fl := startFlusher(flushCtx, w, cfg.Flush, log)
	defer func() { stopFlush(); fl.stop() }()

	timer := time.NewTimer(interval)
	defer timer.Stop()
	next := time.Now().Add(interval)
	for {
		select {
		case <-ctx.Done():
			s := col.Collect(time.Now())
			s.Flags |= sample.FlagStop
			if err := w.Append(s); err != nil {
				log.Error("writing final sample", "err", err)
			}
			stopFlush()
			fl.stop() // Close(clean) does the final flush
			*clean = true
			_ = sdnotify.Notify("STOPPING=1")
			log.Info("stopped cleanly", "samples", w.Seq())
			return nil
		case <-timer.C:
		}
		s := col.Collect(time.Now())
		late := time.Since(next)
		if late > 0 && s.Slept == 0 {
			s.LagMs = late.Milliseconds()
			if late > interval {
				s.Flags |= sample.FlagLate
			}
		}
		if first {
			s.Flags |= sample.FlagStart
			first = false
		}
		if err := w.Append(s); err != nil {
			failures++
			if failures == 1 || failures%60 == 0 {
				log.Error("writing sample", "err", err, "failures", failures)
			}
		} else {
			failures = 0
			if troubling(s) {
				fl.now()
			}
		}

		now := time.Now()
		if wd > 0 && now.Sub(lastWD) >= wd {
			_ = sdnotify.Notify("WATCHDOG=1")
			lastWD = now
		}
		if now.Sub(lastStatus) >= time.Minute {
			_ = sdnotify.Notify(fmt.Sprintf("STATUS=Recording · %s of history · last sample %s", historyLen(w, interval), now.Format("15:04:05")))
			lastStatus = now
		}
		next = next.Add(interval)
		if next.Before(now) {
			next = now.Add(interval)
		}
		timer.Reset(time.Until(next))
	}
}

func historyLen(w *ring.Writer, interval time.Duration) string {
	n := w.Seq()
	if max := uint64(w.Header().SlotCount); n > max { // #nosec G115
		n = max
	}
	d := time.Duration(n) * interval // #nosec G115
	return d.Round(time.Minute).String()
}

func analysePrevious(ctx context.Context, opt Options, prev previousBoot, log *slog.Logger) {
	p := opt.Paths
	marker := p.AnalyzingMarker()
	if err := os.WriteFile(marker, []byte(prev.header.BootID+"\n"), 0o644); err != nil { // #nosec G306
		log.Warn("could not create analysis marker", "err", err)
	}
	defer os.Remove(marker)

	start := time.Now()
	in, err := analyze.Gather(ctx, analyze.GatherOptions{
		RingPath:   prev.ringPath,
		Previous:   true,
		Journal:    opt.Journal,
		PstoreDirs: pstore.DefaultDirs,
		ProcDir:    opt.Collector.ProcDir,
		Generator:  "beebox " + version.Short(),
	})
	if err != nil {
		log.Error("gathering evidence for previous boot", "err", err)
		return
	}
	r := analyze.Analyze(in)
	if r == nil {
		log.Info("previous boot ended cleanly", "boot", prev.header.BootID, "took", time.Since(start).Round(time.Millisecond))
		return
	}
	store := report.Store{Dir: p.ReportsDir()}
	file, err := store.Save(r)
	if err != nil {
		log.Error("saving report", "err", err)
		return
	}
	if err := store.Prune(opt.Config.KeepReports); err != nil {
		log.Warn("pruning reports", "err", err)
	}
	log.Warn("previous boot ended unexpectedly",
		"verdict", r.Verdict.Title, "confidence", r.Verdict.Confidence,
		"report", r.ID, "file", file, "took", time.Since(start).Round(time.Millisecond))
}

// lockDir prevents two recorders from writing the same ring.
func lockDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644) // #nosec G302 G304
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115
		f.Close()
		return nil, errors.New("another beebox recorder is already running (see: systemctl status beebox)")
	}
	return func() { f.Close() }, nil
}
