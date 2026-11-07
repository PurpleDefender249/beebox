#!/bin/sh
# Fedora, openSUSE: runs before beebox is removed ($1 = 0) or upgraded.
if [ "$1" = 0 ] && [ -d /run/systemd/system ]; then
    systemctl --no-reload disable --now beebox.service >/dev/null 2>&1 || :
fi
exit 0
