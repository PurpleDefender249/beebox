#!/bin/sh
# Debian family: runs before beebox is removed or upgraded.
set -e
if [ -z "${DPKG_ROOT:-}" ] && [ "$1" = remove ] && [ -d /run/systemd/system ]; then
    if command -v deb-systemd-invoke >/dev/null 2>&1; then
        deb-systemd-invoke stop beebox.service >/dev/null || true
    fi
fi
exit 0
