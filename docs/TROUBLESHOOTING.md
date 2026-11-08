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
beebox doctor          # see the "Kernel crash logs" line and its fix
```

Legacy-BIOS computers and virtual machines can't save crash logs; UEFI ones
usually can after the one command `beebox doctor` shows.

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
  republish: `gh workflow run release.yml -f tag=v0.1.1`.

Check which address answers:

```sh
curl -sI https://purpledefender249.github.io/beebox/latest | head -1
```

## Collecting information for a bug report

```sh
{
  beebox version; uname -r; grep PRETTY_NAME /etc/os-release; systemctl --version | head -1
  systemctl status beebox --no-pager -l
  journalctl -u beebox -b --no-pager -n 200
  beebox doctor --plain
  sudo beebox dump --last 150 --plain
} > beebox-report.txt 2>&1
```

Read the file before attaching it: it contains app names and kernel messages,
never file contents or anything you typed.
