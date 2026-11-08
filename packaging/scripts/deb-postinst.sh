#!/bin/sh
# Debian, Ubuntu, Kali, Mint: runs after beebox is installed or upgraded.
# The service handling mirrors what debhelper's dh_installsystemd generates,
# so it honours policy-rc.d and an administrator's "systemctl disable".
set -e

case "$1" in
    configure|abort-upgrade|abort-deconfigure|abort-remove) ;;
    *) exit 0 ;;
esac

if command -v deb-systemd-helper >/dev/null 2>&1; then
    # Removes only the mask deb-systemd-helper adds when the package is removed.
    deb-systemd-helper unmask beebox.service >/dev/null || true
    # "was-enabled" is true for new installations, so they get enabled (via
    # "systemctl preset" and the 80-beebox.preset file this package ships,
    # which beats a distribution-wide "disable *.service" such as Kali's);
    # an upgrade keeps whatever the administrator chose.
    if deb-systemd-helper --quiet was-enabled beebox.service; then
        deb-systemd-helper enable beebox.service >/dev/null || true
    else
        deb-systemd-helper update-state beebox.service >/dev/null || true
    fi
fi

if [ -d /run/systemd/system ]; then
    systemctl --system daemon-reload >/dev/null || true
    if [ -n "$2" ]; then action=restart; else action=start; fi
    if command -v deb-systemd-invoke >/dev/null 2>&1; then
        deb-systemd-invoke "$action" beebox.service >/dev/null || true
    fi
fi

if [ "$1" = configure ] && [ -z "$2" ]; then
    echo
    # The banner appears only on a terminal (never in logs, Docker builds or
    # CI) and honours BEEBOX_NO_BANNER=1, BEEBOX_PLAIN=1 and NO_COLOR. apt
    # runs this script on a pseudo-terminal even when its output is a log,
    # so unattended installs (DEBIAN_FRONTEND=noninteractive) skip it too.
    # It can't fail the installation.
    if [ "${DEBIAN_FRONTEND:-}" != noninteractive ]; then
        beebox banner 2>/dev/null || true
    fi
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
