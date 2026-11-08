#!/bin/sh
# Installs, checks, reinstalls and removes a BeeBox package inside a clean
# distribution container. Used by CI and runnable by hand:
#
#   docker run --rm -v "$PWD:/src" -w /src debian:12 scripts/test-packages.sh
#   docker run --rm -v "$PWD:/src" -w /src fedora:42 scripts/test-packages.sh
#   docker run --rm -v "$PWD:/src" -w /src archlinux scripts/test-packages.sh
#
# Expects the packages from `make dist` in build/dist/. There is no systemd
# running in a container, so this checks files, dependencies and the
# maintainer scripts' no-systemd paths; the service itself is tested on the
# CI runner, which does run systemd.
set -eu

dist=${DIST_DIR:-build/dist}
arch=$(uname -m)
step() { printf '\n==> %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# shellcheck source=/dev/null
. /etc/os-release
family=${ID_LIKE:-$ID}
case " $ID $family " in
    *" debian "*|*" ubuntu "*) family=deb ;;
    *" fedora "*|*" rhel "*|*" suse "*) family=rpm ;;
    *" arch "*) family=arch ;;
    *) fail "unsupported distribution: $ID" ;;
esac
step "$PRETTY_NAME ($arch, $family)"

common_checks() {
    step "checks"
    beebox version
    test -x /usr/bin/beebox || fail "binary missing"
    test -f /usr/lib/systemd/system/beebox.service || fail "unit missing"
    test -f /etc/beebox/beebox.conf || fail "config missing"
    # Minimal images often exclude /usr/share/man, so check the package list.
    case $family in
        deb) dpkg -L beebox ;; rpm) rpm -ql beebox ;; arch) pacman -Qlq beebox ;;
    esac | grep -q '/usr/share/man/man1/beebox.1.gz' || fail "man page not packaged"
    test -f /usr/share/bash-completion/completions/beebox || fail "bash completion missing"
    test -f /usr/share/icons/hicolor/scalable/apps/beebox.svg || fail "icon missing"
    beebox doctor --plain || true # several checks need a running system
    page=$(beebox view --sample --print-path)
    grep -q 'Content-Security-Policy' "$page" || fail "sample report page is wrong"
    if command -v systemd-analyze >/dev/null 2>&1; then
        # Only judge BeeBox's own unit where systemd supports that (250+).
        recursive=''
        if systemd-analyze --help 2>/dev/null | grep -q -- '--recursive-errors'; then
            recursive='--recursive-errors=no'
        fi
        # shellcheck disable=SC2086 # $recursive is empty or one option
        systemd-analyze verify --man=no $recursive /usr/lib/systemd/system/beebox.service
        systemd-analyze security --offline=true --threshold=2 /usr/lib/systemd/system/beebox.service | tail -n 1
    fi
}

case $family in
deb)
    pkg=$(ls "$dist"/beebox_*_"$(dpkg --print-architecture)".deb)
    export DEBIAN_FRONTEND=noninteractive
    step "install $pkg"
    apt-get update -qq
    apt-get install -y -qq "./$pkg" >/dev/null
    dpkg -s beebox | grep -q '^Status: install ok installed' || fail "not installed"
    # Enabled through the shipped preset, even where the distribution's
    # default preset disables new services (Kali).
    [ "$(systemctl is-enabled beebox.service 2>/dev/null)" = enabled ] || fail "not enabled after install"
    common_checks
    step "reinstall (upgrade path)"
    apt-get install -y -qq --reinstall "./$pkg" >/dev/null
    mkdir -p /var/lib/beebox/reports && touch /var/lib/beebox/reports/keep.json
    step "remove keeps history"
    apt-get remove -y -qq beebox >/dev/null
    test ! -e /usr/bin/beebox || fail "binary left after remove"
    test -e /var/lib/beebox/reports/keep.json || fail "remove deleted history"
    step "purge deletes history"
    apt-get purge -y -qq beebox >/dev/null
    test ! -e /var/lib/beebox || fail "purge left /var/lib/beebox"
    test ! -e /etc/beebox/beebox.conf || fail "purge left the config"
    ;;
rpm)
    rarch=$arch
    pkg=$(ls "$dist"/beebox-*."$rarch".rpm)
    step "install $pkg"
    dnf install -y -q "./$pkg"
    rpm -V beebox || fail "rpm -V found changed files"
    [ "$(systemctl is-enabled beebox.service 2>/dev/null)" = enabled ] || fail "not enabled after install"
    common_checks
    test -f /usr/lib/systemd/system-preset/80-beebox.preset || fail "preset missing"
    if dnf install -y -q rpmlint >/dev/null 2>&1; then
        step "rpmlint"
        rpmlint "$pkg" || true
    fi
    step "reinstall (upgrade path)"
    dnf reinstall -y -q "./$pkg"
    step "remove"
    dnf remove -y -q beebox
    test ! -e /usr/bin/beebox || fail "binary left after remove"
    ;;
arch)
    pkg=$(ls "$dist"/beebox-*-"$arch".pkg.tar.zst)
    step "install $pkg"
    pacman -Sy --noconfirm --needed systemd >/dev/null
    pacman -U --noconfirm "$pkg"
    # Arch packages leave enabling to the user.
    [ "$(systemctl is-enabled beebox.service 2>/dev/null)" = disabled ] || fail "Arch package enabled the service"
    common_checks
    if pacman -S --noconfirm --needed namcap >/dev/null 2>&1; then
        step "namcap"
        namcap "$pkg" || true
    fi
    step "reinstall (upgrade path)"
    pacman -U --noconfirm "$pkg"
    step "remove"
    pacman -R --noconfirm beebox
    test ! -e /usr/bin/beebox || fail "binary left after remove"
    ;;
esac
step "OK"
