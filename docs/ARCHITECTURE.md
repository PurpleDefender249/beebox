# Architecture

BeeBox is a single Go binary with no third-party dependencies. The same binary
is the recorder service, the analyser and the command-line tool.

```
cmd/beebox            entry point (signal handling, exit codes)
internal/sample     reads /proc, /sys and /dev/kmsg into a Sample
internal/ring       the crash-safe on-disk ring buffer
internal/recorder   the daemon: sampling loop, archiving, start-up analysis
internal/journal    reads a boot's entries via journalctl
internal/pstore     reads kernel crash logs saved to firmware
internal/analyze    evidence gathering and the verdict rules
internal/report     report schema and on-disk store
internal/render     terminal text and the standalone HTML page
internal/doctor     set-up checks
internal/cli        commands, help, completions
internal/scenario   synthetic crash scenarios for tests and --sample
```

## Recording

Every `interval` (default 1 s) the collector reads:

| Source | What |
| --- | --- |
| `/proc/stat`, `/proc/loadavg` | CPU busy %, load |
| `/proc/meminfo` | total and available memory, swap |
| `/proc/pressure/{cpu,memory,io}` | pressure-stall averages (10 s window) |
| `/proc/vmstat` | major faults, swap in/out, OOM kills |
| `/proc/uptime` vs the monotonic clock | time spent asleep |
| `/proc/<pid>/{stat,statm}` | per-app CPU and private memory, grouped by the basename of argv[0] |
| `/sys/class/hwmon`, `/sys/class/thermal` | hottest reading per sensor group and its critical limit |
| `/sys/class/power_supply` | battery level, status, power draw; AC adapter |
| `/sys/class/drm/*/gpu_busy_percent` | GPU load (amdgpu) |
| `statfs("/")` | free space on the root filesystem |
| `/dev/kmsg` | notable kernel messages (warnings and above, plus suspend, OOM and thermal lines) |

The recorder also measures its own lateness: if a sample that was due at
12:00:01 is taken at 12:00:07, the system was barely responding. That signal
works even on kernels without pressure-stall information.

### The ring file

`/var/lib/beebox/current.ring` is preallocated with `fallocate`, so the recorder
never needs new disk blocks while the system is in trouble. It has a 4 KiB
header (boot ID, kernel, geometry, a clean-stop flag, CRC32) followed by
fixed-size slots. Each slot holds one JSON-encoded sample with its own
sequence number and CRC32, so:

- a torn write during a power cut loses only that slot;
- readers (`beebox status`, `beebox now`) can read while the recorder writes;
- samples are ordered by sequence number, not position, so wrap-around is free.

The recorder memory-maps the ring and locks the mapping into RAM, so writing a
sample is a plain memory copy: it never allocates, never waits for the disk,
and can't be stalled by memory pressure. A separate goroutine forces the data
to disk with `msync` every `flush` (default 5 s), **and immediately** when a
sample shows trouble starting (memory or disk stalls, an OOM kill, a late
sample, a new kernel message), so the beginning of a memory storm is on disk
before the disk itself gets swamped by swapping. The final flush happens on a
clean stop.

### Staying alive under pressure

Freezes are usually memory starvation. The recorder:

- locks its pages into RAM with `mlockall(MCL_CURRENT|MCL_FUTURE|MCL_ONFAULT)`,
  so it never waits for its own pages to be swapped back in, while keeping its
  resident size around 11 MB;
- caps its heap with `debug.SetMemoryLimit` and an aggressive GC target;
- runs with `OOMScoreAdjust=-900`, `Nice=-5` and realtime I/O priority (its
  writes are a few kilobytes every few seconds);
- splits sampling in two: the core numbers (`/proc` text files and `statfs`)
  are read on the once-a-second loop, while everything that can block — the
  per-process scan and hardware sensors (an NVMe temperature read is a command
  to the drive) — runs on its own goroutine. The loop attaches the latest
  result if it is under a minute old, so a stuck probe never stops sampling;
- reads each process's `cmdline` only once per process lifetime, and not at
  all while memory pressure is high (reading it takes the target's memory-map
  lock, which a thrashing process may hold). Processes first seen during
  pressure are named from the kernel's short name and renamed afterwards;
- pings the systemd watchdog, so a stuck recorder is restarted.

If it is restarted within the same boot it resumes the same ring, so the
history leading up to a crash stays in one place.

## Analysis

When the recorder starts and finds a ring from a different boot, it archives
it to `/var/lib/beebox/boots/` and, after a few seconds, gathers evidence:

1. the archived ring (samples and kernel messages);
2. the journal for that boot: the last 1500 entries, the last 3000 kernel
   entries and up to 500 warnings (`journalctl -b <boot-id> -o json`);
3. kernel crash logs from `/sys/fs/pstore` and `/var/lib/systemd/pstore`
   written since that boot started.

Sources are merged into one timeline, de-duplicated (the same kernel line
arrives from both the ring and the journal) and passed to a list of
independent rules (see [DETECTIONS.md](DETECTIONS.md)). Each rule returns a
finding with a confidence score, a plain-language summary, advice tailored to
the distribution's package manager, and the evidence lines it relied on.

The highest-confidence cause becomes the verdict. Findings that the verdict
already explains are removed from "Also noticed" (for example, a frozen system
is part of running out of memory). Context findings — such as "you restarted
with SysRq" — are listed separately.

A boot that ended with an orderly shutdown produces no report, unless the
system shut itself down because of heat or a critically low battery.

While analysing, the recorder creates `/run/beebox/analyzing`. `beebox notify`
(started at login) waits for it to disappear before deciding whether to show a
notification.

## Reports

Reports are JSON files in `/var/lib/beebox/reports/`, named
`<UTC end time>-<boot-id prefix>.json`, written atomically (temp file, fsync,
rename) and pruned to `keep_reports`. The schema is versioned; older binaries
refuse newer schemas rather than misreading them.

`beebox view` renders a report with `html/template` into a single file in
`~/.cache/beebox/` and opens it. The page:

- embeds all CSS and a small script; a Content-Security-Policy of
  `default-src 'none'` forbids loading anything else;
- draws charts as server-side SVG, so they work without JavaScript; the
  script only adds the crosshair, tooltip, keyboard navigation and copy buttons.

## Why Go, and why no dependencies

The recorder runs as root on every boot for years, so its supply chain should
be as small as possible. Go's standard library covers everything BeeBox needs
(including the `sd_notify` protocol, implemented in ~40 lines), produces a
single self-contained binary that runs on any distribution, and keeps the
resident size small.

Release binaries are position-independent executables linked with
`-bindnow` (full RELRO), so a root daemon gets address-space layout
randomisation like distribution-built packages do. They need no shared
libraries, only the system's program loader (`ld-linux`), which every glibc
distribution has.
