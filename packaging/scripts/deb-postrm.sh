#!/bin/sh
# Debian family: runs after beebox is removed or purged.
# "remove" keeps recordings and reports (reinstalling keeps your history);
# "purge" deletes them.
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
    systemctl --system daemon-reload >/dev/null || true
fi
if [ "$1" = remove ] && command -v deb-systemd-helper >/dev/null 2>&1; then
    deb-systemd-helper mask beebox.service >/dev/null || true
fi
if [ "$1" = purge ]; then
    if command -v deb-systemd-helper >/dev/null 2>&1; then
        deb-systemd-helper purge beebox.service >/dev/null || true
        deb-systemd-helper unmask beebox.service >/dev/null || true
    fi
    rm -rf /var/lib/beebox /run/beebox
fi
exit 0
