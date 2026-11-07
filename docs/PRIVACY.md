# Privacy

BeeBox is built to be safe to run on a personal computer and safe to share a
report from.

## What BeeBox records

- Whole-system numbers: CPU, memory, swap, pressure stalls, temperatures,
  battery, free disk space.
- For the five apps using the most memory and the five using the most CPU:
  the app's name, how many processes it has, and its memory and CPU use.
- Kernel messages that are warnings or worse, plus a few informational ones
  that explain crashes (out-of-memory kills, suspend and resume, thermal events).

App names come from the basename of the program's path (`/usr/lib/firefox/firefox`
becomes `firefox`). If a program writes something else there — `sshd`, for
example, puts the user name in it — BeeBox ignores it and uses the kernel's short
process name instead.

## What BeeBox never records

- Command-line arguments, environment variables or anything else a program was
  started with.
- File names, file contents, window titles, clipboard or keystrokes.
- Network traffic or addresses (the service runs with networking disabled).
- Anything from your home folder (the service cannot see home folders at all).

Everything BeeBox records is already visible to every user of the computer
through `/proc`, which is why the recording and reports are readable by all
local users (mode 0644).

## What reports contain

A report adds the verdict, the advice and the evidence lines it relied on,
which can include lines from the system journal and kernel log. Before saving,
BeeBox replaces:

- `/home/<name>` with `/home/<user>`;
- IPv4 addresses with `<ip>`;
- MAC addresses with `<mac>`.

Reports also include the kernel version and the boot ID. They do not include
the computer's host name.

Read a report before posting it publicly, as with any log.

## Where data lives and how long

| Path | Kept |
| --- | --- |
| `/var/lib/beebox/current.ring` | the current boot, last 15 minutes by default |
| `/var/lib/beebox/boots/` | raw recordings of the last 10 boots |
| `/var/lib/beebox/reports/` | the last 50 reports |
| `~/.cache/beebox/` | web pages you opened with `beebox view` (private to you, mode 0600) |
| `~/.local/state/beebox/notified` | the ID of the last report you were notified about |

Uninstalling keeps `/var/lib/beebox` so reinstalling keeps your history. On
Debian and Ubuntu, `sudo apt purge beebox` removes it; elsewhere,
`sudo rm -rf /var/lib/beebox`.

## Network

BeeBox never connects to the network. The systemd unit enforces this with
`PrivateNetwork=yes`, `IPAddressDeny=any` and `RestrictAddressFamilies=AF_UNIX`.
Report pages carry a Content-Security-Policy that blocks every external
request.
