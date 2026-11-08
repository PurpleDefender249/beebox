#!/bin/sh
# BeeBox installer.
#
#   curl -fsSL https://purpledefender249.github.io/beebox/install.sh | sh
#
# Debian, Ubuntu, Kali, Mint: adds the signed BeeBox APT repository and
#   installs the package, so updates arrive with your normal system updates.
# Fedora: adds the signed BeeBox DNF repository, the same way.
# Arch Linux: downloads the latest release package, checks it, installs it.
#
# Options:
#   --uninstall   remove BeeBox and its repository (keeps recordings/reports)
#   --purge       with --uninstall, also delete recordings and reports
#   --no-start    install, but don't start recording now
#   --yes         don't ask for confirmation
#   --help        show this help
#
# Everything this script does is printed before it happens. To read it
# first, download it, look at it, then run: sh install.sh
#
# The whole script is inside main(), called on the last line, so a
# download cut short can never run half a script.

set -eu

REPO_URL=${BEEBOX_REPO_URL:-https://purpledefender249.github.io/beebox}
RELEASES_URL=${BEEBOX_RELEASES_URL:-https://github.com/PurpleDefender249/beebox/releases}
KEYRING=/etc/apt/keyrings/beebox.gpg
APT_SOURCE=/etc/apt/sources.list.d/beebox.sources
DNF_REPO=/etc/yum.repos.d/beebox.repo

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    B=$(printf '\033[1m') G=$(printf '\033[32m') Y=$(printf '\033[33m') R=$(printf '\033[31m') N=$(printf '\033[0m')
else
    B='' G='' Y='' R='' N=''
fi
say() { printf '%s\n' "$*"; }
step() { printf '\n%s==>%s %s%s%s\n' "$Y" "$N" "$B" "$*" "$N"; }
ok() { printf '%s✓%s %s\n' "$G" "$N" "$*"; }
die() { printf '%sError:%s %s\n' "$R" "$N" "$*" >&2; exit 1; }

# run prints a command, then runs it as root.
run() {
    printf '  %s$ %s%s\n' "$B" "$*" "$N"
    if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi
}

usage() {
    cat <<EOF
BeeBox installer

  curl -fsSL $REPO_URL/install.sh | sh
  curl -fsSL $REPO_URL/install.sh | sh -s -- [options]

Options:
  --uninstall   remove BeeBox and its repository (keeps recordings/reports)
  --purge       with --uninstall, also delete recordings and reports
  --no-start    install, but don't start recording now
  --yes         don't ask for confirmation
  --help        show this help
EOF
}

fetch() { # fetch URL FILE
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --proto '=https,http' --retry 3 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$2" "$1"
    else
        die "this needs curl or wget"
    fi
}

detect() {
    [ "$(uname -s)" = Linux ] || die "BeeBox runs on Linux only."
    [ -r /etc/os-release ] || die "can't tell which Linux this is (/etc/os-release is missing)."
    # shellcheck source=/dev/null
    . /etc/os-release
    NAME_PRETTY=${PRETTY_NAME:-$ID}
    family=''
    for id in $ID ${ID_LIKE:-}; do
        case $id in
            debian|ubuntu) family=apt; break ;;
            fedora) family=dnf; break ;;
            arch) family=pacman; break ;;
        esac
    done
    case $(uname -m) in
        x86_64|amd64) DEB_ARCH=amd64 RPM_ARCH=x86_64 ;;
        aarch64|arm64) DEB_ARCH=arm64 RPM_ARCH=aarch64 ;;
        *) die "BeeBox has packages for x86_64 and arm64; this computer is $(uname -m)." ;;
    esac
    if [ -z "$family" ]; then
        die "$NAME_PRETTY isn't supported by this installer yet. Packages for Debian/Ubuntu, Fedora and Arch are at $RELEASES_URL"
    fi
    [ -d /run/systemd/system ] || say "${Y}Note:${N} systemd isn't running here, so BeeBox will be installed but can't record until it is."
    if [ "$(id -u)" -ne 0 ] && ! command -v sudo >/dev/null 2>&1; then
        die "run this as root, or install sudo"
    fi
}

confirm() {
    [ "$assume_yes" = yes ] && return 0
    # Read the answer from the terminal: when piped from curl, stdin is the script.
    if [ -r /dev/tty ]; then
        printf '%s [Y/n] ' "$1"
        read -r answer </dev/tty || answer=n
        case $answer in ''|y|Y|yes|Yes) return 0 ;; *) die "cancelled" ;; esac
    fi
}

install_apt() {
    step "Adding the BeeBox APT repository"
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    fetch "$REPO_URL/beebox.gpg" "$tmp/beebox.gpg" \
        || die "couldn't download the signing key from $REPO_URL/beebox.gpg (is the address right? see https://github.com/PurpleDefender249/beebox#install)"
    run install -d -m 0755 /etc/apt/keyrings
    run install -m 0644 "$tmp/beebox.gpg" "$KEYRING"
    printf 'Types: deb\nURIs: %s/apt\nSuites: stable\nComponents: main\nArchitectures: %s\nSigned-By: %s\n' \
        "$REPO_URL" "$DEB_ARCH" "$KEYRING" >"$tmp/beebox.sources"
    run install -m 0644 "$tmp/beebox.sources" "$APT_SOURCE"
    step "Installing BeeBox"
    # A problem with some other repository shouldn't stop this; if the
    # BeeBox repository itself can't be read, the install below says so.
    run env DEBIAN_FRONTEND=noninteractive apt-get update || say "${Y}Note:${N} apt-get update reported problems (see above); trying to install anyway."
    run env DEBIAN_FRONTEND=noninteractive apt-get install -y beebox
}

install_dnf() {
    step "Adding the BeeBox DNF repository"
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    printf '[beebox]\nname=BeeBox\nbaseurl=%s/rpm\nenabled=1\ngpgcheck=1\nrepo_gpgcheck=1\ngpgkey=%s/beebox.asc\n' \
        "$REPO_URL" "$REPO_URL" >"$tmp/beebox.repo"
    run install -m 0644 "$tmp/beebox.repo" "$DNF_REPO"
    step "Installing BeeBox"
    run dnf install -y beebox
}

install_pacman() {
    step "Downloading the latest BeeBox release"
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    version=${BEEBOX_VERSION:-}
    if [ -z "$version" ]; then
        fetch "$REPO_URL/latest" "$tmp/latest" \
            || die "couldn't read the latest version from $REPO_URL/latest (is the address right?)"
        version=$(tr -dc '0-9A-Za-z.+~-' <"$tmp/latest")
    fi
    [ -n "$version" ] || die "couldn't find the latest version"
    base="$RELEASES_URL/download/v$version"
    fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"
    # Take the file name from the checksum list rather than guessing how the
    # version is spelled in it.
    pkg=$(sed -n "s/^[0-9a-f]\{64\}  \(beebox-.*-$RPM_ARCH\.pkg\.tar\.zst\)\$/\1/p" "$tmp/SHA256SUMS" | head -n 1)
    [ -n "$pkg" ] || die "release $version has no Arch package for $RPM_ARCH"
    fetch "$base/$pkg" "$tmp/$pkg"
    if command -v gpg >/dev/null 2>&1 && fetch "$base/SHA256SUMS.asc" "$tmp/SHA256SUMS.asc" 2>/dev/null; then
        fetch "$REPO_URL/beebox.gpg" "$tmp/beebox.gpg"
        gpg --batch --quiet --no-default-keyring --keyring "$tmp/beebox.gpg" --verify "$tmp/SHA256SUMS.asc" "$tmp/SHA256SUMS" 2>/dev/null \
            || die "the release checksums are not signed by the BeeBox key"
        ok "Release checksums are signed by the BeeBox key"
    fi
    (cd "$tmp" && grep " $pkg\$" SHA256SUMS | sha256sum -c --quiet -) || die "the download is damaged (checksum mismatch)"
    ok "Checksum matches"
    step "Installing BeeBox $version"
    run pacman -U --noconfirm --needed "$tmp/$pkg"
    # Arch packages leave services off; you asked this installer to set it up.
    if [ "$no_start" = no ] && [ -d /run/systemd/system ]; then
        run systemctl enable --now beebox.service
    fi
}

uninstall() {
    step "Removing BeeBox"
    case $family in
        apt)
            if [ "$purge" = yes ]; then run apt-get purge -y beebox || true; else run apt-get remove -y beebox || true; fi
            [ -e "$APT_SOURCE" ] && run rm -f "$APT_SOURCE"
            [ -e "$KEYRING" ] && run rm -f "$KEYRING"
            ;;
        dnf)
            run dnf remove -y beebox || true
            [ -e "$DNF_REPO" ] && run rm -f "$DNF_REPO"
            ;;
        pacman)
            run pacman -R --noconfirm beebox || true
            ;;
    esac
    if [ "$purge" = yes ] && [ -d /var/lib/beebox ]; then
        run rm -rf /var/lib/beebox
    fi
    ok "BeeBox is removed."
    [ "$purge" = yes ] || say "Recordings and reports in /var/lib/beebox were kept. Delete them with: sudo rm -rf /var/lib/beebox"
}

main() {
    action=install assume_yes=no purge=no no_start=no
    for arg in "$@"; do
        case $arg in
            --uninstall) action=uninstall ;;
            --purge) purge=yes ;;
            --no-start) no_start=yes ;;
            --yes|-y) assume_yes=yes ;;
            --help|-h) usage; exit 0 ;;
            *) die "unknown option: $arg (try --help)" ;;
        esac
    done
    detect
    if [ "$action" = uninstall ]; then
        confirm "Remove BeeBox from $NAME_PRETTY?"
        uninstall
        exit 0
    fi

    say "${B}BeeBox installer${N} for $NAME_PRETTY ($DEB_ARCH)"
    case $family in
        apt) say "This adds the signed BeeBox repository ($REPO_URL/apt) and installs the package." ;;
        dnf) say "This adds the signed BeeBox repository ($REPO_URL/rpm) and installs the package." ;;
        pacman) say "This downloads the latest release from $RELEASES_URL, checks it and installs it." ;;
    esac
    confirm "Continue?"
    "install_$family"
    if [ "$no_start" = yes ] && [ -d /run/systemd/system ]; then
        run systemctl disable --now beebox.service || true
    fi

    step "Checking"
    if command -v beebox >/dev/null 2>&1; then
        beebox status || true
        say ""
        say "Next:  ${B}beebox doctor${N}          make sure nothing is missing to catch the next crash"
        say "       ${B}beebox view --sample${N}   see what a report looks like"
        say "Remove later with: curl -fsSL $REPO_URL/install.sh | sh -s -- --uninstall"
    else
        die "the installation didn't finish; see the messages above"
    fi
}

main "$@"
