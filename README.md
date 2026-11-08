<div align="center">

<img src="packaging/icons/hicolor/scalable/apps/beebox.svg" width="112" height="112" alt="The BeeBox logo: a bee whose body is a striped recorder box, flying inside a honeycomb cell and leaving a heartbeat line behind it.">

# BeeBox

**The flight recorder for your Linux computer.**<br>
When it crashes, freezes or loses power, BeeBox tells you why — in plain language — and what to do about it.

<a href="https://github.com/PurpleDefender249/beebox/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/PurpleDefender249/beebox/actions/workflows/ci.yml/badge.svg"></a>
<a href="https://github.com/PurpleDefender249/beebox/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/PurpleDefender249/beebox?style=flat&color=f5b301&label=release"></a>
<a href="go.mod"><img alt="Go 1.22 or newer" src="https://img.shields.io/github/go-mod/go-version/PurpleDefender249/beebox?style=flat&logo=go&logoColor=white&color=00add8"></a>
<a href="#privacy-and-security"><img alt="Zero third-party dependencies" src="https://img.shields.io/badge/dependencies-0-2ea44f?style=flat"></a>
<a href="#privacy-and-security"><img alt="systemd security exposure 1.4, OK" src="https://img.shields.io/badge/systemd%20exposure-1.4%20OK-2ea44f?style=flat"></a>
<a href="LICENSE"><img alt="MIT licence" src="https://img.shields.io/badge/licence-MIT-blue?style=flat"></a>

[**Install**](#install) · [**Everyday use**](#everyday-use) · [**What it detects**](#what-it-can-tell-you) · [**How it works**](#how-it-works) · [**FAQ**](#faq) · [**Troubleshooting**](docs/TROUBLESHOOTING.md)

<br>

<img src="docs/images/banner.png" width="860" alt="A terminal running 'beebox status'. It opens with the BeeBox banner: on the left, the logo drawn in pixel art — a honeycomb cell with a bee inside and a heartbeat line running into it; on the right, the word BEEBOX in tall 3D letters with a honey-yellow gradient and a dark shadow, the version {0.2.0}, the tagline 'flight recorder for Linux crashes' and the project link. Below the banner: a green check mark and 'BeeBox is recording', when recording started, how much history it keeps, and what it measures right now.">

</div>

---

The screen froze. The mouse stopped. You held the power button. When the computer
came back, there was nothing to tell you what went wrong — and the system log from
that boot is either gone or full of noise.

**BeeBox fixes that.** It quietly records the last few minutes of your system's vital
signs into a crash-safe file. The next time the computer starts after something went
wrong, it works out what most likely happened and shows you:

```text
$ beebox last
BeeBox report for the boot that ended Wed 4 Nov 2026, 23:11 (after 2 h 14 min)

  ● Very likely (95%)
  Your computer ran out of memory

  In the last minute before it stopped, available memory fell to 1.1%, and the
  system spent up to 75% of its time stalled waiting for memory. The biggest
  user was Firefox (11.7 GB across 23 processes), which grew by 6.7 GB in the
  last 10 min. The system did not recover and had to be restarted.

  What you can do
  1. Turn on early out-of-memory protection, so one greedy app gets closed
     instead of the whole system freezing.
     sudo systemctl enable --now systemd-oomd
  ...
```

If you're logged in to a desktop, a notification pops up with a button that opens the
full report in your browser — a self-contained page that follows your light or dark theme:

<table>
  <tr>
    <td width="50%" align="center"><img src="docs/images/report-light.png" alt="A BeeBox report in a web browser, light theme. A honey-yellow band says 'Probable cause: Your computer ran out of memory', with four filled honeycomb cells for 'very likely (95% confidence)'. Below it are the explanation, three numbered steps to fix it with copyable commands, and charts of free memory and of time stalled on memory over the minutes before it stopped."></td>
    <td width="50%" align="center"><img src="docs/images/report-dark.png" alt="The same BeeBox report in the dark theme."></td>
  </tr>
  <tr>
    <td align="center"><sub>Light theme</sub></td>
    <td align="center"><sub>Dark theme</sub></td>
  </tr>
</table>

<sub><strong>Why "BeeBox"?</strong> Like an aircraft's black box — a "B-box" — it keeps the last minutes before something goes wrong. Like a bee, it's small and always busy, and you'll rarely notice it until it matters.</sub>

## Highlights

<table>
  <tr>
    <td width="33%" valign="top">
      <h3>🧠 Plain-language verdicts</h3>
      Not a log dump: a probable cause, how sure it is, the evidence, and the exact commands to fix it.
    </td>
    <td width="33%" valign="top">
      <h3>🛡️ Survives the crash</h3>
      A checksummed, memory-locked recording built to outlast the freeze, the panic and the power cut.
    </td>
    <td width="33%" valign="top">
      <h3>🪶 Light</h3>
      About 11 MB of memory and well under 1% of one core. You won't notice it's there.
    </td>
  </tr>
  <tr>
    <td width="33%" valign="top">
      <h3>🔒 Private by design</h3>
      Networking disabled by systemd, no access to home folders, nothing ever leaves your computer.
    </td>
    <td width="33%" valign="top">
      <h3>📦 Native packages</h3>
      Signed APT and DNF repositories plus Arch packages, so updates arrive with your normal system updates.
    </td>
    <td width="33%" valign="top">
      <h3>♿ For everyone</h3>
      Never colour alone, a plain mode for screen readers, and report pages you can use with only a keyboard.
    </td>
  </tr>
</table>

## What it can tell you

| | What happened | How BeeBox knows |
| :-: | --- | --- |
| 🧠 | **Ran out of memory** and froze | Pressure-stall info, free memory, swap, the kernel's OOM killer, systemd-oomd/earlyoom, and its own once-a-second check running late |
| 💥 | **Kernel panic, lockup or bug** | Kernel crash logs saved to firmware (pstore), panic/oops/lockup messages, the faulting function and program |
| 🔧 | **Hardware error** | Machine-check (MCE) reports from the CPU |
| 🖥️ | **Graphics driver hang** | amdgpu, i915, nouveau and NVIDIA (Xid, "fallen off the bus") reset and timeout messages |
| 🌡️ | **Overheating** | Temperatures against each sensor's critical limit, throttling and thermal shutdown messages |
| 🔋 | **Battery ran flat** | Last battery readings and UPower's critical-battery action |
| 😴 | **Never woke from sleep** | A suspend that never resumed, with the battery level when it went to sleep |
| 💾 | **Disk errors, full disk, disk stalls** | NVMe/SATA/filesystem errors, free space, I/O pressure |
| ⚡ | **Power cut or hard reset** | A healthy system that simply stopped, with nothing in the logs (and, where the kernel can save crash logs, no crash log either) |
| 🧪 | **A test crash** | `SysRq + C` is recognised as deliberate, so testing BeeBox doesn't scare you |

When the evidence doesn't point anywhere, BeeBox says so instead of guessing.
See [docs/DETECTIONS.md](docs/DETECTIONS.md) for exactly how each verdict is reached.

## Install

<p>
  <img alt="Debian" src="https://img.shields.io/badge/Debian-A81D33?style=flat&logo=debian&logoColor=white">
  <img alt="Ubuntu" src="https://img.shields.io/badge/Ubuntu-E95420?style=flat&logo=ubuntu&logoColor=white">
  <img alt="Kali Linux" src="https://img.shields.io/badge/Kali%20Linux-557C94?style=flat&logo=kalilinux&logoColor=white">
  <img alt="Linux Mint" src="https://img.shields.io/badge/Linux%20Mint-87CF3E?style=flat&logo=linuxmint&logoColor=white">
  <img alt="Fedora" src="https://img.shields.io/badge/Fedora-51A2DA?style=flat&logo=fedora&logoColor=white">
  <img alt="Arch Linux" src="https://img.shields.io/badge/Arch%20Linux-1793D1?style=flat&logo=archlinux&logoColor=white">
  <img alt="x86_64 and ARM64" src="https://img.shields.io/badge/x86__64%20%7C%20ARM64-555555?style=flat">
</p>

**One command:**

```sh
curl -fsSL https://purpledefender249.github.io/beebox/install.sh | sh
```

On Debian-family systems and Fedora this adds the signed BeeBox package repository,
so updates arrive with your normal system updates. On Arch it installs the latest
release after checking its signed checksums.

> [!TIP]
> The script prints every command before running it. To read it first, download it
> and run `sh install.sh`. Remove BeeBox the same way:
> `curl -fsSL https://purpledefender249.github.io/beebox/install.sh | sh -s -- --uninstall`

<details>
<summary><strong>Add the repository by hand</strong></summary>

Debian, Ubuntu, Kali, Linux Mint:

```sh
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://purpledefender249.github.io/beebox/beebox.gpg | sudo tee /etc/apt/keyrings/beebox.gpg >/dev/null
curl -fsSL https://purpledefender249.github.io/beebox/beebox.sources | sudo tee /etc/apt/sources.list.d/beebox.sources >/dev/null
sudo apt update && sudo apt install beebox
```

Fedora:

```sh
sudo curl -fsSL -o /etc/yum.repos.d/beebox.repo https://purpledefender249.github.io/beebox/beebox.repo
sudo dnf install beebox
```

</details>

<details>
<summary><strong>Install a downloaded package</strong></summary>

Packages are on the [releases page](https://github.com/PurpleDefender249/beebox/releases/latest).

```sh
sudo apt install ./beebox_*_amd64.deb                     # Debian, Ubuntu, Kali, Mint
sudo dnf install ./beebox-*.x86_64.rpm                    # Fedora
sudo pacman -U ./beebox-*-x86_64.pkg.tar.zst              # Arch, Manjaro, EndeavourOS
sudo systemctl enable --now beebox                        # Arch only: start recording
```

</details>

<details>
<summary><strong>Build from source</strong> (needs Go 1.22 or newer)</summary>

```sh
git clone https://github.com/PurpleDefender249/beebox && cd beebox
make && sudo make install
sudo systemctl enable --now beebox
```

</details>

On Debian, Ubuntu, Kali, Mint and Fedora, BeeBox starts recording straight away.
Then check everything is in place:

```sh
beebox doctor
```

Something not working? See [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md).

## Everyday use

| Command | What it does |
| --- | --- |
| `beebox status` | Is BeeBox recording? What does it see right now? |
| `beebox last` | The latest crash report |
| `beebox view` | Open the latest report in your browser (or the "BeeBox" app in your menu) |
| `beebox list` | Every report, newest first |
| `beebox now --watch` | Live view of memory, stalls, temperatures and battery |
| `beebox doctor` | Checks logs, memory protection, swap and crash logs, with the command to fix each |
| `sudo beebox analyze` | Analyse the previous boot on demand — also works for crashes from before BeeBox was installed |
| `beebox view --sample` | See what a report looks like without anything going wrong |

Every command has `--help`, and the ones that print data also have `--json` for scripts.
`beebox --version` prints just the version; `beebox version --verbose` adds the commit,
build date and platform for bug reports. `man beebox` has the full reference.

> [!NOTE]
> Commands you read in a terminal open with the BeeBox banner, in the tradition of tools
> like sqlmap. It adapts to your terminal — 3D letters and the pixel-art logo where colour
> and Unicode are available, plain ASCII elsewhere — and it is never printed into a pipe,
> a file or a log, nor with `--json`. Turn it off with `--no-banner`, or for good with
> `export BEEBOX_NO_BANNER=1` in your `~/.bashrc` or `~/.zshrc`.

## How it works

```mermaid
flowchart LR
  subgraph running["While the computer runs"]
    P["/proc, /sys, /dev/kmsg"] -->|once a second| R[beebox record]
    R -->|"fixed-size, checksummed slots"| F[("/var/lib/beebox/current.ring")]
  end
  subgraph next["Next start-up"]
    F --> A[Analyser]
    J[(systemd journal)] --> A
    S[("pstore crash log")] --> A
    A --> Rep[("Report (JSON)")]
    Rep --> N[Desktop notification]
    Rep --> V["beebox last / beebox view"]
  end
```

1. The **recorder** samples once a second into a preallocated ring file. Every slot
   carries its own checksum, so a power cut mid-write loses at most one second. It
   locks itself into memory and runs with raised priority, so it keeps recording
   while the rest of the system thrashes.
2. At the **next boot** it notices that the previous recording was never closed
   cleanly, archives it, and the **analyser** runs a set of rules over the recording,
   the previous boot's journal and any kernel crash log. Each rule scores its
   confidence; the strongest becomes the verdict.
3. Clean shutdowns produce no report — except when the computer shut itself down
   to protect itself from heat or an empty battery.

More detail in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Light on your system

Measured on an idle desktop:

<table>
  <tr>
    <td align="center" width="25%"><h3>~11 MB</h3><sub>memory</sub></td>
    <td align="center" width="25%"><h3>&lt; 1%</h3><sub>of one CPU core</sub></td>
    <td align="center" width="25%"><h3>1.8 MB</h3><sub>recording (15 minutes)</sub></td>
    <td align="center" width="25%"><h3>5.5 MB</h3><sub>single binary</sub></td>
  </tr>
</table>

Besides the recording there are only small JSON reports. BeeBox writes about 200 MB a
day in total, less than a typical web browser, and needs no libraries beyond the
system's C loader.

## Privacy and security

- BeeBox records only what any user can already see in `/proc`: app names (never
  command-line arguments, file contents, window titles or anything you type),
  resource usage and kernel messages.
- The recorder runs with **networking disabled** by systemd and no access to home
  folders. Its systemd security exposure score is **1.4 ("OK")**.
- Report pages are self-contained, work offline and load nothing from the internet.
- Evidence lines have home folder names, IP addresses and MAC addresses removed, so
  a report is safe to attach to a bug report — but read it first.
- **Zero third-party Go dependencies.**

Full details: [docs/PRIVACY.md](docs/PRIVACY.md). Security policy: [SECURITY.md](SECURITY.md).

## Accessibility

- Terminal output never uses colour alone: every status has a symbol and a word.
  `--plain` (or `BEEBOX_PLAIN=1`) removes colour, the banner and sparklines for screen
  readers; `NO_COLOR` is respected. The banner is never the only place any
  information appears.
- Report pages use semantic HTML, a skip link and visible keyboard focus. Every chart
  has a text summary, a screen-reader description, keyboard navigation (arrow keys)
  and a data table. Light and dark mode follow your system, and high-contrast modes
  (`forced-colors`) are supported.

## Configuration

Optional. Edit `/etc/beebox/beebox.conf`, then `sudo systemctl restart beebox`.

```ini
interval = 1s        # time between samples
window = 15m         # history kept in the recording
flush = 5s           # how often samples are forced to disk
top_processes = 5    # apps listed per sample
keep_reports = 50
keep_rings = 10
```

## Try it safely

`beebox view --sample` shows a sample report without anything going wrong.

> [!IMPORTANT]
> To see BeeBox catch a real crash, use a virtual machine, never the computer you
> work on. [docs/TESTING-IN-A-VM.md](docs/TESTING-IN-A-VM.md) walks through a power
> cut, a kernel panic and an out-of-memory freeze.

## FAQ

<details>
<summary><strong>Does it replace the system journal?</strong></summary>
<br>
No. It complements it: the journal often loses the last seconds before a crash, or
isn't kept between boots at all. BeeBox keeps its own copy of the measurements and the
kernel's important messages.
</details>

<details>
<summary><strong>Will it slow my computer down?</strong></summary>
<br>
No. See <a href="#light-on-your-system">the numbers above</a>.
</details>

<details>
<summary><strong>What about crashes before I installed it?</strong></summary>
<br>
Run <code>sudo beebox analyze</code>. If your journal kept the previous boot, BeeBox can
still give a verdict from the logs.
</details>

<details>
<summary><strong>Does it work on servers?</strong></summary>
<br>
Yes. Everything works without a desktop; <code>beebox last --json</code> is handy for
monitoring.
</details>

## Contributing

Bug reports, new detection rules and translations of advice for more distributions
are very welcome. See [CONTRIBUTING.md](CONTRIBUTING.md). Development needs only Go:

```sh
make check      # format, vet, tests with the race detector, staticcheck
```

## Licence

[MIT](LICENSE)

<div align="center">
<br>
<img src="packaging/icons/hicolor/scalable/apps/beebox.svg" width="40" height="40" alt="">
<br>
<sub>Made for everyone who has ever had to hold down the power button. · <a href="#beebox">Back to top ↑</a></sub>
</div>
