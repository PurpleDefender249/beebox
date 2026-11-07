#!/bin/sh
# Arch Linux: if the recorder is running, restart it on the new version.
if [ -d /run/systemd/system ]; then
    systemctl try-restart beebox.service >/dev/null 2>&1 || true
fi
exit 0
