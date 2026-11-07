#!/usr/bin/env bash
# Builds the signed package repository that is published on GitHub Pages:
#
#   apt/           APT repository (InRelease and Release.gpg signed)
#   rpm/           DNF repository (packages signed, repomd.xml.asc)
#   beebox.asc     public signing key (ASCII) for DNF
#   beebox.gpg     public signing key (binary) for APT's Signed-By
#   beebox.sources, beebox.repo   ready-made repository files
#   install.sh     the one-line installer
#   latest         the current version number
#   index.html     a short page with install instructions
#
# Usage: scripts/build-repo.sh PACKAGES_DIR OUT_DIR KEY VERSION [BASE_URL]
#   PACKAGES_DIR  directory with the .deb and .rpm files from `make dist`
#   KEY           GnuPG key ID or fingerprint to sign with (already imported)
#   BASE_URL      where OUT_DIR will be served (default: the project's Pages)
#
# Needs: gpg, apt-ftparchive (apt-utils), createrepo_c, gzip.
set -euo pipefail

pkgs=$(realpath "$1")
out=$2
key=$3
version=$4
base=${5:-https://purpledefender249.github.io/beebox}
here=$(cd "$(dirname "$0")" && pwd)

for tool in gpg apt-ftparchive createrepo_c gzip; do
    command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
sign() { gpg --batch --yes --pinentry-mode loopback --passphrase '' --local-user "$key" "$@"; }

rm -rf "$out"
mkdir -p "$out/apt/pool/main/b/beebox" "$out/rpm"
out=$(realpath "$out")
cp "$pkgs"/*.deb "$out/apt/pool/main/b/beebox/"
cp "$pkgs"/*.rpm "$out/rpm/"

echo "==> APT repository"
(
    cd "$out/apt"
    for arch in amd64 arm64; do
        dir=dists/stable/main/binary-$arch
        mkdir -p "$dir"
        apt-ftparchive --arch "$arch" packages pool >"$dir/Packages"
        gzip -9nk "$dir/Packages"
    done
    apt-ftparchive \
        -o APT::FTPArchive::Release::Origin=BeeBox \
        -o APT::FTPArchive::Release::Label=BeeBox \
        -o APT::FTPArchive::Release::Suite=stable \
        -o APT::FTPArchive::Release::Codename=stable \
        -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
        -o APT::FTPArchive::Release::Components=main \
        -o APT::FTPArchive::Release::Description="BeeBox flight recorder" \
        release dists/stable >dists/stable/Release
    sign --clearsign -o dists/stable/InRelease dists/stable/Release
    sign --armor --detach-sign -o dists/stable/Release.gpg dists/stable/Release
)

echo "==> DNF repository"
# gzip metadata is read by every dnf and zypper version.
createrepo_c --quiet --general-compress-type=gz "$out/rpm"
sign --armor --detach-sign -o "$out/rpm/repodata/repomd.xml.asc" "$out/rpm/repodata/repomd.xml"

echo "==> keys, repository files, installer"
gpg --batch --armor --export "$key" >"$out/beebox.asc"
gpg --batch --export "$key" >"$out/beebox.gpg"
fingerprint=$(gpg --batch --with-colons --fingerprint "$key" | awk -F: '/^fpr:/ {print $10; exit}')
printf 'Types: deb\nURIs: %s/apt\nSuites: stable\nComponents: main\nSigned-By: /etc/apt/keyrings/beebox.gpg\n' "$base" >"$out/beebox.sources"
printf '[beebox]\nname=BeeBox\nbaseurl=%s/rpm\nenabled=1\ngpgcheck=1\nrepo_gpgcheck=1\ngpgkey=%s/beebox.asc\n' "$base" "$base" >"$out/beebox.repo"
cp "$here/install.sh" "$out/install.sh"
printf '%s\n' "$version" >"$out/latest"
touch "$out/.nojekyll"

cat >"$out/index.html" <<EOF
<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>BeeBox packages</title>
<style>
body{font:16px/1.55 system-ui,sans-serif;max-width:46rem;margin:2.5rem auto;padding:0 1.25rem;color:#1d2125}
h1{border-bottom:6px solid #f5b301;padding-bottom:.3rem}code,pre{font-family:ui-monospace,monospace}
pre{background:#f3f4f6;padding:.9rem 1rem;border-radius:8px;overflow-x:auto}
@media (prefers-color-scheme:dark){body{background:#16191d;color:#e7e9ec}pre{background:#23272c}a{color:#f5b301}}
</style></head><body>
<h1>BeeBox $version</h1>
<p>A flight recorder for Linux that explains why your computer crashed, froze or lost power.
Project page: <a href="https://github.com/PurpleDefender249/beebox">github.com/PurpleDefender249/beebox</a>.</p>
<h2>Install</h2>
<pre>curl -fsSL $base/install.sh | sh</pre>
<p>Debian, Ubuntu, Kali and Mint get the APT repository and Fedora gets the DNF repository, so updates
arrive with normal system updates. Arch Linux gets the latest release package, checked against its signed checksums.</p>
<h2>Or add the repository yourself</h2>
<p>Debian, Ubuntu, Kali, Mint:</p>
<pre>sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL $base/beebox.gpg | sudo tee /etc/apt/keyrings/beebox.gpg &gt;/dev/null
curl -fsSL $base/beebox.sources | sudo tee /etc/apt/sources.list.d/beebox.sources &gt;/dev/null
sudo apt update &amp;&amp; sudo apt install beebox</pre>
<p>Fedora:</p>
<pre>sudo curl -fsSL -o /etc/yum.repos.d/beebox.repo $base/beebox.repo
sudo dnf install beebox</pre>
<p>Packages are signed with the key <code>$fingerprint</code>.</p>
</body></html>
EOF
echo "==> done: $out"
