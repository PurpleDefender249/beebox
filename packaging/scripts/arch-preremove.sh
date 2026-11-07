#!/bin/sh
# Arch Linux: stop and disable the recorder before its files are removed.
if [ -d /run/systemd/system ]; then
    systemctl disable --now beebox.service >/dev/null 2>&1 || true
fi
exit 0
