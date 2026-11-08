#!/bin/sh
# Prints the CHANGELOG.md section for a version, followed by install steps.
# Usage: scripts/release-notes.sh 0.1.0
set -eu
version="$1"
repo=${GITHUB_REPOSITORY:-PurpleDefender249/beebox}
owner=$(printf '%s' "${repo%%/*}" | tr '[:upper:]' '[:lower:]')
site="https://$owner.github.io/${repo#*/}"

awk -v v="$version" '
  /^## \[/ { if (found) exit; if (index($0, "[" v "]")) { found = 1; next } }
  found { print }
' CHANGELOG.md

cat <<EOF

## Install

The easiest way, on Debian, Ubuntu, Kali, Mint, Fedora and Arch:

\`\`\`sh
curl -fsSL $site/install.sh | sh
\`\`\`

On Debian-family systems and Fedora this adds the signed BeeBox repository, so
updates arrive with your normal system updates.

Or install a package from this page:

| Distribution | Command |
| --- | --- |
| Debian, Ubuntu, Kali, Mint | \`sudo apt install ./beebox_${version}_amd64.deb\` |
| Fedora | \`sudo dnf install ./beebox-${version}-1.x86_64.rpm\` |
| Arch, Manjaro | \`sudo pacman -U ./beebox-${version}-1-x86_64.pkg.tar.zst && sudo systemctl enable --now beebox\` |

ARM64 packages are included too. Then run \`beebox doctor\`.

Verify a download: \`sha256sum -c SHA256SUMS --ignore-missing\`, check the
checksum list's signature with \`gpg --verify SHA256SUMS.asc SHA256SUMS\`
(key: $site/beebox.asc), or check the build provenance with
\`gh attestation verify <file> --repo $repo\`.
EOF
