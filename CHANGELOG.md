# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [0.1.1] - 2026-11-08

### Fixed

- Kali Linux (and any system whose default policy disables newly installed
  services): installing the package now enables and starts the recorder,
  through a preset file shipped in the package. On 0.1.0, run
  `sudo systemctl enable --now beebox` once.
- The published one-line installer always points at the address it is
  served from, and says clearly which download failed if one does.
- `beebox doctor` and reports give a working command to turn on
  out-of-memory protection on Debian, Ubuntu and Kali, where systemd-oomd is
  a separate package (and Kali leaves new services switched off).

### Added

- The release workflow can republish the package repository for an existing
  release: `gh workflow run release.yml -f tag=v0.1.1`.

## [0.1.0] - 2026-11-07

First release.

### Added

- Crash-safe recorder: one sample a second into a checksummed, preallocated,
  memory-mapped ring (memory, pressure stalls, swap, CPU, temperatures,
  battery, disk space, top apps, kernel warnings), locked in memory and
  sandboxed by systemd. Slow probes run on their own goroutine and disk
  flushes happen in the background, immediately when trouble starts, so a
  memory storm can't stop the recording.
- Start-up analysis of the previous boot using the recording, the journal and
  pstore kernel crash logs, with verdicts for out-of-memory, kernel panics,
  lockups and bugs, hardware errors, graphics hangs, overheating, empty
  batteries, failed resume from sleep, disk errors, full disks, disk stalls,
  freezes, power loss and deliberate SysRq crashes. Without kernel crash
  logs, an abrupt stop is reported as a "sudden stop" rather than a power
  cut, because a kernel crash would look the same.
- Reports note when the processor was fully busy at the end and name the
  app responsible.
- Commands: `last`, `show`, `list`, `view`, `status`, `now`, `doctor`,
  `analyze`, `record`, `notify`, `completion`, `version`, `help`.
- Accessible, self-contained HTML reports with charts, data tables and
  keyboard navigation; light and dark mode.
- Desktop notification at login with a button that opens the report.
- One-line installer and a signed APT/DNF package repository on GitHub
  Pages, so updates arrive with normal system updates; signed checksums for
  direct downloads.
- Packages for Debian, Ubuntu, Kali, Mint, Fedora and Arch (x86_64 and
  ARM64), built as position-independent executables with full RELRO, with
  distribution-correct service handling (debhelper-style on Debian, presets
  on Fedora, opt-in on Arch); man page; bash, zsh and fish completions.
- `beebox doctor` explains how to enable kernel crash logs (UEFI pstore),
  and a hidden `beebox dump` command prints raw recordings for bug reports.
- The BeeBox logo: a bee with a striped recorder-box body in a honeycomb
  cell, with a simplified version for 16–32 px and PNGs for icon themes.

[0.1.1]: https://github.com/PurpleDefender249/beebox/releases/tag/v0.1.1
[0.1.0]: https://github.com/PurpleDefender249/beebox/releases/tag/v0.1.0
