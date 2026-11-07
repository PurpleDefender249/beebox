#!/bin/sh
# Fedora, openSUSE: runs after beebox is installed ($1 = 1) or upgraded
# ($1 >= 2). Enabling follows the preset shipped in the package
# (/usr/lib/systemd/system-preset/80-beebox.preset), which an administrator
# can override in /etc/systemd/system-preset/.
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || :
    if [ "$1" = 1 ]; then
        systemctl --no-reload preset beebox.service >/dev/null 2>&1 || :
        if systemctl is-enabled --quiet beebox.service 2>/dev/null; then
            systemctl start beebox.service >/dev/null 2>&1 || :
        fi
    fi
fi
if [ "$1" = 1 ]; then
    echo
    if [ -d /run/systemd/system ] && systemctl is-active --quiet beebox.service 2>/dev/null; then
        echo "BeeBox is recording. If this computer crashes, freezes or loses power,"
        echo "the next start-up will tell you why."
    else
        echo "Start recording with:  sudo systemctl enable --now beebox"
    fi
    echo
    echo "  beebox status          check it's recording"
    echo "  beebox doctor          make sure nothing is missing to catch the next crash"
    echo "  beebox view --sample   see what a report looks like"
    echo
fi
exit 0
