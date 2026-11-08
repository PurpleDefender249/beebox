# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [0.3.0] - 2026-11-08

### Added

- Kernel crash logs on computers and virtual machines that start in legacy
  BIOS mode: BeeBox reads the log that kdump saves in `/var/crash`
  (kdump-tools on Debian, Ubuntu, Kali and Mint; kexec-tools on Fedora), so
  a kernel crash there is named instead of being reported as a sudden stop,
  and a power cut is reported as one. `beebox doctor` shows the command to
  set kdump up, and says when it is installed but not ready yet. Tested end
  to end on a legacy-BIOS Debian 13 virtual machine.
- `beebox doctor` recognises virtual machines ("virtual machines have no
  temperature sensors"), and on real hardware without sensors shows the
  lm-sensors command.

### Fixed

- A processor running flat out for minutes was missed when a single sample
  dipped below 95%: the report said the CPU "looked normal" and left out the
  "processor was fully busy" note. Brief dips no longer end the stretch, and
  busy now means 90% or more.
- A browser started through Debian's default-browser command
  (`x-www-browser`) was listed twice, as "Firefox" and "Web browser". Its
  processes are now counted together (in recordings made from now on).
- An evidence line could be listed twice in a report.
- Kernel log lines saved with the kernel's caller IDs (`[ T3273]`) showed
  them in reports.
- `beebox doctor` run as a normal user didn't find `update-grub`, so it
  left out the command to switch on UEFI crash logs.

### Security

- Kernel crash logs are only read from regular, root-owned files with a
  single link, without following links: `/var/crash` is world-writable on
  Ubuntu, so a planted file can't fake a crash or copy another file into a
  report.

## [0.2.0] - 2026-11-08

### Added

- A banner: commands you read in a terminal (`beebox`, `status`, `last`,
  `show`, `list`, `view`, `now`, `doctor`, `analyze`) open with the BeeBox
  logo in pixel art beside "BEEBOX" in shaded 3D letters with a honey
  gradient, the version, a tagline and the project link. It is shown right
  after installation too. It adapts to the terminal: true colour, 256 or 16
  colours; the letters alone without colour or in narrower windows; 3D
  letters in plain ASCII where the locale isn't UTF-8 or on the Linux
  console. It is never printed into pipes, files or logs, nor with `--json`,
  `--ids`, `--print-path` or `--watch`. Turn it off with `--no-banner` or
  `BEEBOX_NO_BANNER=1`; `--plain` and `NO_COLOR` are respected.
- A redesigned README with the banner, highlights and both report themes.
- `beebox version --verbose` prints the commit, build date, Go version and
  platform; `beebox version --json` includes the Go version and platform.

### Changed

- `beebox --version`, `-V` and `beebox version` print only the version, for
  example `beebox 0.2.0`.

## [0.1.1] - 2026-11-08

### Fixed

- Kali Linux (and any system whose default policy disables newly installed
  services): installing the package now enables and starts the recorder,
  through a preset file shipped in the package. On 0.1.0, run
  `sudo systemctl enable --now beebox` once.
- The published one-line installer always points at the address it is
  served from, and says clearly which download failed if one does.
- `beebox doctor` and reports give working commands to turn on
  out-of-memory protection and compressed swap on Debian, Ubuntu and Kali,
  where systemd-oomd is a separate package and Kali leaves newly installed
  services switched off.
- CI: the systemd security check used the wrong scale for its threshold.

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

[0.3.0]: https://github.com/PurpleDefender249/beebox/releases/tag/v0.3.0
[0.2.0]: https://github.com/PurpleDefender249/beebox/releases/tag/v0.2.0
[0.1.1]: https://github.com/PurpleDefender249/beebox/releases/tag/v0.1.1
[0.1.0]: https://github.com/PurpleDefender249/beebox/releases/tag/v0.1.0
