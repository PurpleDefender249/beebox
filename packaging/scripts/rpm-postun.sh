#!/bin/sh
# Fedora, openSUSE: runs after beebox is removed ($1 = 0) or after the old
# version is replaced during an upgrade ($1 >= 1). Recordings and reports in
# /var/lib/beebox are kept; remove them by hand with: sudo rm -rf /var/lib/beebox
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || :
    if [ "$1" != 0 ]; then
        systemctl try-restart beebox.service >/dev/null 2>&1 || :
    fi
fi
exit 0
