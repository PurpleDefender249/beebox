#!/bin/sh
# Fedora, openSUSE: runs after beebox is installed ($1 = 1) or upgraded
# ($1 >= 2). Enabling follows the preset shipped in the package
# (/usr/lib/systemd/system-preset/80-beebox.preset), which an administrator
# can override in /etc/systemd/system-preset/. Like Fedora's %systemd_post,
# the preset is applied even when systemd isn't running (image builds).
if [ "$1" = 1 ] && command -v systemctl >/dev/null 2>&1; then
    systemctl --no-reload preset beebox.service >/dev/null 2>&1 || :
fi
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || :
    if [ "$1" = 1 ] && systemctl is-enabled --quiet beebox.service 2>/dev/null; then
        systemctl start beebox.service >/dev/null 2>&1 || :
    fi
fi
if [ "$1" = 1 ]; then
    echo
    # The banner appears only on a terminal (never in logs, Docker builds or
    # CI) and honours BEEBOX_NO_BANNER=1, BEEBOX_PLAIN=1 and NO_COLOR. It
    # can't fail the installation.
    beebox banner 2>/dev/null || true
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
