#!/usr/bin/env bash
# End-to-end test of the package repository and the one-line installer:
# signs a build with a throwaway key, builds the APT/DNF repository, serves
# it over HTTP, then runs install.sh (install, then uninstall) in clean
# Debian, Ubuntu, Fedora and Arch containers.
#
# Usage: scripts/test-repo.sh
# Needs: docker, gpg, apt-ftparchive, createrepo_c, python3, nfpm, go.
# EXTRA_CA=/path/ca.crt adds a CA to the containers (for TLS-intercepting
# proxies); leave it unset normally.
set -euo pipefail

cd "$(dirname "$0")/.."
version=0.0.0-test
port=${PORT:-8765}
work=$(mktemp -d)
export GNUPGHOME="$work/gnupg"
mkdir -m 700 "$GNUPGHOME"
server=''
cleanup() { [ -n "$server" ] && kill "$server" 2>/dev/null; rm -rf "$work"; }
trap cleanup EXIT

echo "==> throwaway signing key"
gpg --batch --passphrase '' --quick-gen-key "BeeBox test <test@example.invalid>" rsa3072 sign 1d 2>/dev/null
fpr=$(gpg --batch --with-colons --fingerprint test@example.invalid | awk -F: '/^fpr:/ {print $10; exit}')
gpg --batch --armor --export-secret-keys "$fpr" >"$work/key.asc"

echo "==> signed build and repository"
make dist VERSION="$version" SIGNING_KEY="$work/key.asc" >/dev/null
(cd build/dist && gpg --batch --yes --armor --detach-sign -u "$fpr" -o SHA256SUMS.asc SHA256SUMS)
url="http://host.docker.internal:$port"
scripts/build-repo.sh build/dist "$work/www" "$fpr" "$version" "$url" >/dev/null
mkdir -p "$work/www/releases/download/v$version"
cp build/dist/*.pkg.tar.zst build/dist/SHA256SUMS build/dist/SHA256SUMS.asc "$work/www/releases/download/v$version/"
python3 -m http.server "$port" --bind 0.0.0.0 --directory "$work/www" >/dev/null 2>&1 &
server=$!
sleep 1

ca_setup=':'
ca_mount=()
if [ -n "${EXTRA_CA:-}" ]; then
    ca_mount=(-v "$EXTRA_CA:/tmp/extra-ca.crt:ro")
    ca_setup='if [ -d /etc/pki/ca-trust/source/anchors ]; then cp /tmp/extra-ca.crt /etc/pki/ca-trust/source/anchors/ && update-ca-trust; elif [ -d /etc/ca-certificates/trust-source/anchors ]; then cp /tmp/extra-ca.crt /etc/ca-certificates/trust-source/anchors/ && update-ca-trust; fi'
fi

run_in() { # image, preparation command
    local image=$1 prep=$2
    echo "==> $image"
    docker run --rm --add-host=host.docker.internal:host-gateway "${ca_mount[@]}" \
        -e BEEBOX_REPO_URL="$url" -e BEEBOX_RELEASES_URL="$url/releases" \
        "$image" sh -euc "
            $ca_setup
            $prep
            curl -fsSL $url/install.sh | sh -s -- --yes
            beebox version
            curl -fsSL $url/install.sh | sh -s -- --uninstall --purge --yes
            if [ -e /usr/bin/beebox ]; then echo 'still installed' >&2; exit 1; fi
            echo PASS"
}

run_in "${DEBIAN_IMAGE:-debian:12}" 'apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null'
run_in "${UBUNTU_IMAGE:-ubuntu:24.04}" 'apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null'
run_in "${FEDORA_IMAGE:-fedora:42}" 'true'
run_in "${ARCH_IMAGE:-archlinux:latest}" 'pacman -Sy --noconfirm >/dev/null'
echo "==> all installer paths passed"
