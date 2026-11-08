# Troubleshooting

Start with the two built-in checks. They explain most problems and print the
command that fixes each one:

```sh
beebox status
beebox doctor
```

If that doesn't help, work through the sections below. Every fix has a way to
check it worked and a way to undo it.

## Quick reference

| What you see | Most likely cause | Go to |
| --- | --- | --- |
| `beebox status`: "BeeBox isn't recording" | The service is stopped or disabled | [1](#1-the-service-isnt-running) |
| `systemctl status beebox`: `failed` / `start request repeated too quickly` | The recorder crashed several times in a row | [2](#2-the-service-keeps-failing) |
| Journal: `Watchdog timeout` | The recorder was stuck for 30 s | [3](#3-watchdog-timeouts) |
| No notification after a crash | `notify-send` missing, or not a desktop login | [4](#4-no-notification-after-a-crash) |
| Every report says "stopped suddenly" | No crash logs and no warning signs | [5](#5-every-report-says-stopped-suddenly) |
| `apt update`: `NO_PUBKEY`, `not signed`, `Release file … is not valid yet` | Repository key or clock problem | [6](#6-repository-errors) |
| `dnf`: `GPG check FAILED` / `repomd.xml GPG signature verification error` | Repository key problem | [6](#6-repository-errors) |
| Install says "unpacking … over (0.1.0)" and the service is gone | An early test build was upgraded in place | [7](#7-reinstalling-cleanly) |
| One-line installer: `curl: (22) … 404` | The package site isn't published at that address yet | [8](#8-the-one-line-installer-says-404) |
| Odd characters such as `[1;33m`, question marks or empty boxes above the output, or you'd rather not see the banner | A terminal without colour, or a font without block characters, or personal taste | [9](#9-the-banner) |

## 1. The service isn't running

```sh
systemctl status beebox --no-pager -l
systemctl is-enabled beebox
```

Fix:

```sh
sudo systemctl enable --now beebox
```

Check: `beebox status` says **BeeBox is recording** within a few seconds.

Undo: `sudo systemctl disable --now beebox`.

## 2. The service keeps failing

Read what it said before it stopped (the last 100 lines of this boot):

```sh
journalctl -u beebox -b --no-pager -n 100
```

| Message | Meaning | Fix |
| --- | --- | --- |
| `another beebox recorder is already running` | A second `beebox record` was started by hand | `pgrep -a beebox`, stop the extra one |
| `create /var/lib/beebox…: read-only file system` | The root filesystem is mounted read-only | Fix the filesystem; `beebox doctor` |
| `read boot id` | `/proc` isn't mounted normally (containers) | Run BeeBox on the host, not in a container |
| `status=203/EXEC` | `/usr/bin/beebox` is missing | Reinstall: `sudo apt install --reinstall beebox` |
| `status=31/SYS` or `Bad system call` | A newer systemd's system-call filter blocked something | Please open an issue with the journal output; work-around below |

After fixing the cause, clear the failure counter and start again:

```sh
sudo systemctl reset-failed beebox
sudo systemctl start beebox
```

Work-around for a sandbox problem while you wait for a fix (keeps everything
else the same, removes only the system-call filter):

```sh
sudo systemctl edit beebox
# add these two lines in the editor, save, and close:
#   [Service]
#   SystemCallFilter=
sudo systemctl restart beebox
```

Undo the work-around: `sudo systemctl revert beebox`.

## 3. Watchdog timeouts

`beebox.service: Watchdog timeout (limit 30s)!` means the recorder didn't
report for 30 seconds, so systemd restarted it. It resumes the same recording,
so nothing is lost. A single timeout during a heavy freeze is expected: it is
evidence of the freeze. Repeated timeouts on an idle system are a bug; please
open an issue and attach:

```sh
journalctl -u beebox -b --no-pager -n 300 > beebox-journal.txt
```

(The journal then contains a stack dump of what the recorder was waiting for.)

## 4. No notification after a crash

```sh
beebox doctor          # see the "Desktop notifications" line
command -v notify-send || sudo apt install libnotify-bin   # Fedora: sudo dnf install libnotify
ls /etc/xdg/autostart/beebox-notify.desktop
```

The notification is shown by a small program that starts when you log in to a
desktop (GNOME, KDE, Xfce and others that support XDG autostart). Test it:

```sh
beebox notify --test
```

Reports are written either way: `beebox last` shows the latest.

## 5. Every report says "stopped suddenly"

That verdict means the recording ended with everything looking normal and
nothing in the logs. The usual causes are a power cut, holding the power
button, or a virtual machine being powered off. A kernel crash looks exactly
the same unless the kernel can save a crash log:

```sh
beebox doctor          # see the "Kernel crash logs" line
sudo beebox setup      # switch it on (and anything else missing), then restart
```

UEFI computers keep crash logs in firmware once `beebox setup` has loaded the
kernel's crash log store. Computers and virtual machines that start in legacy
BIOS mode have no firmware storage for crash logs, so `beebox setup` sets up
kdump instead (Debian, Ubuntu, Kali, Mint and Fedora): a spare kernel, given
320–512 MB of memory depending on how much the computer has, saves the crashed
kernel's log to `/var/crash` and restarts the computer. After the restart the
doctor's line should say **Saved if the kernel crashes (kdump, in /var/crash)**,
and still say so after the next restart.

The doctor also tells you when kdump won't manage to save a crash, and
`beebox setup` fixes it:

- *On now, but off again after the next restart*: kdump was loaded by hand
  (`kdump-config load`) but its service is off. Kali leaves newly installed
  services switched off.
- *kdump has too little memory to save a crash*: kdump-tools' own estimate of
  what its spare kernel needs is more than is kept for it. Kali's kernels need
  more than kdump-tools' default 192 MB. BeeBox writes its reservation to
  `/etc/default/grub.d/zz-beebox-kdump.cfg`, which wins over kdump-tools' file.
- *The last kernel crash was not saved*: kdump started after a crash but saved
  no log. Reports for such a crash say **The kernel crashed (its crash log
  couldn't be saved)** rather than blaming the power.

Each kernel crash also leaves a memory dump next to the log in `/var/crash`, often
a few hundred MB. BeeBox only reads the small `dmesg` file; delete old crash
folders when you no longer need them (`sudo ls /var/crash`).

To see exactly what was recorded in the final two minutes:

```sh
sudo beebox dump --last 120
```

## 6. Repository errors

**APT** (`NO_PUBKEY`, `The following signatures couldn't be verified`):
the key file is missing or old. Fetch it again:

```sh
curl -fsSL https://purpledefender249.github.io/beebox/beebox.gpg | sudo tee /etc/apt/keyrings/beebox.gpg >/dev/null
sudo apt update
```

**APT** (`Release file … is not valid yet`): the computer's clock is wrong.
`timedatectl` shows it; `sudo timedatectl set-ntp true` fixes it.

**DNF** (`GPG check FAILED`, `repomd.xml GPG signature verification error`):

```sh
sudo rpm --import https://purpledefender249.github.io/beebox/beebox.asc
sudo dnf clean metadata && sudo dnf makecache --repo beebox
```

Remove the repository completely (keeps BeeBox installed):

```sh
sudo rm -f /etc/apt/sources.list.d/beebox.sources /etc/apt/keyrings/beebox.gpg   # Debian family
sudo rm -f /etc/yum.repos.d/beebox.repo                                          # Fedora
```

## 7. Reinstalling cleanly

Upgrading normally keeps your settings, recordings and reports. To start from
scratch (for example after testing early builds):

```sh
sudo apt purge beebox        # Debian family: also deletes recordings and reports
sudo apt install beebox      # or: sudo apt install ./beebox_*_amd64.deb
```

Fedora: `sudo dnf remove beebox && sudo rm -rf /var/lib/beebox && sudo dnf install beebox`.
Arch: `sudo pacman -R beebox && sudo rm -rf /var/lib/beebox && sudo pacman -U ./beebox-*.pkg.tar.zst`.

## 8. The one-line installer says 404

The installer and the package repository are served by GitHub Pages from the
`gh-pages` branch, which the release workflow creates. A 404 means one of:

- No release has been published yet, or Pages isn't switched on
  (repository **Settings → Pages → Deploy from a branch → gh-pages → / (root)**).
- The address doesn't match the repository name exactly. Pages addresses are
  case-sensitive: a repository named `BeeBox` lives at `…github.io/BeeBox/`.
  Rename it to `beebox` (**Settings → General → Repository name**), then
  republish: `gh workflow run release.yml -f tag=v0.4.0` (the latest tag).

Check which address answers:

```sh
curl -sI https://purpledefender249.github.io/beebox/latest | head -1
```

## 9. The banner

Commands you read in a terminal (`status`, `last`, `doctor`, `beebox` on its own…)
open with the BeeBox banner. It is never printed into a pipe, a file, a log or with
`--json`. What it draws depends on the terminal: the pixel-art logo and 3D letters
with colour and a UTF-8 locale (`echo $LANG` ends in `UTF-8`), the letters alone
without colour or in a window narrower than 77 columns, and ASCII letters
elsewhere.

```sh
beebox status --no-banner                         # once
echo 'export BEEBOX_NO_BANNER=1' >> ~/.bashrc     # always (use ~/.zshrc for zsh, Kali's default)
NO_COLOR=1 beebox status                          # keep the banner, drop the colour
beebox status --plain                             # no colour, banner or graphics (screen readers)
```

If you see codes like `[1;33m` instead of colours, your terminal doesn't
understand colour: use `NO_COLOR=1`, or set `TERM` correctly
(`echo $TERM` should not say `dumb` or be empty in a normal terminal).

If you see question marks or empty boxes instead of the letters, the terminal font
has no block or box-drawing characters. Pick a font that does (DejaVu Sans Mono,
Fira Code, Hack, JetBrains Mono, Noto Sans Mono), or use the ASCII banner:
`LC_ALL=C beebox status`.

## Collecting information for a bug report

```sh
{
  beebox version --verbose; uname -r; grep PRETTY_NAME /etc/os-release; systemctl --version | head -1
  systemctl status beebox --no-pager -l
  journalctl -u beebox -b --no-pager -n 200
  beebox doctor --plain
  sudo beebox dump --last 150 --plain
} > beebox-report.txt 2>&1
```

Read the file before attaching it: it contains app names and kernel messages,
never file contents or anything you typed.
