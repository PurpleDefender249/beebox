# Maintaining BeeBox

Step-by-step instructions for the maintainer: the one-time setup, everyday
development, and publishing a release. Commands are for Kali/Debian/Ubuntu;
on Fedora use `dnf` instead of `apt`.

## 1. One-time setup on your computer

```sh
sudo apt update
sudo apt install -y git gh make golang-go shellcheck lintian docker.io gnupg
sudo usermod -aG docker "$USER"     # then log out and back in
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.bashrc && . ~/.bashrc     # zsh (Kali default): use ~/.zshrc
```

Any Go 1.22 or newer works: `go.mod` asks for the exact, patched compiler
(`toolchain go1.26.10`) and Go downloads it automatically the first time.

Sign in to GitHub once: `gh auth login` (GitHub.com → HTTPS → authenticate Git
→ log in with a browser), then `gh auth setup-git`.

## 2. Package signing key (once, before the first release)

The release workflow signs the `.rpm` packages, the checksum list and the
APT/DNF repository with a key that only GitHub Actions and your backup hold.

```sh
# 1. Create a signing-only key with no passphrase. It never expires, so users'
#    computers keep trusting updates; if it is ever leaked, revoke it (step 5).
gpg --batch --passphrase '' --quick-gen-key \
  "BeeBox package signing <PurpleDefender249@users.noreply.github.com>" rsa4096 sign 0

# 2. Note its fingerprint (40 hex characters).
FPR=$(gpg --with-colons --list-secret-keys "BeeBox package signing" | awk -F: '/^fpr:/ {print $10; exit}')
echo "$FPR"

# 3. Give it to GitHub Actions as a secret (it goes straight from gpg to GitHub).
gpg --armor --export-secret-keys "$FPR" | gh secret set BEEBOX_SIGNING_KEY --repo PurpleDefender249/beebox

# 4. Back it up offline (USB stick or password manager), then delete the file.
gpg --armor --export-secret-keys "$FPR" > beebox-signing-key.asc
gpg --output beebox-revoke.asc --gen-revoke "$FPR"     # answer: 1 (key compromised), then y
```

Never commit these files. Check the secret exists:
`gh secret list --repo PurpleDefender249/beebox` shows `BEEBOX_SIGNING_KEY`.

5. If the key is ever leaked: `gpg --import beebox-revoke.asc`, create a new
   key (steps 1–3), release a new version, and tell users to fetch the new key
   (`docs/TROUBLESHOOTING.md` → Repository errors).

Without the secret, releases still work, but packages are unsigned and the
APT/DNF repository and one-line installer are not published.

## 3. Everyday development

```sh
make check                    # formatting, vet, race tests, staticcheck
make                          # build ./build/beebox
./build/beebox view --sample  # try the report page
make dist VERSION=0.0.0-dev   # all packages into build/dist/
```

Test the packages in clean containers (as CI does), for example:

```sh
docker run --rm -v "$PWD:/src" -w /src debian:12 scripts/test-packages.sh
docker run --rm -v "$PWD:/src" -w /src fedora:42 scripts/test-packages.sh
docker run --rm -v "$PWD:/src" -w /src archlinux scripts/test-packages.sh
scripts/test-repo.sh          # signed repository + one-line installer, 4 distributions
```

Then commit and push:

```sh
git add -A
git commit -m "Describe the change"
git push
```

Open the repository's **Actions** tab: the **CI** workflow lints, tests on
x86_64 and ARM64, builds the packages, runs the real service under systemd,
installs on eight distributions, and tests the installer. A red step's log
says what failed.

## 4. Publishing a release

1. Pick the version: `0.1.1` for fixes, `0.2.0` for new features.
2. In `CHANGELOG.md`, add a section at the top with today's date and what changed:

   ```markdown
   ## [0.2.0] - 2026-12-01

   ### Added
   - …

   ### Fixed
   - …
   ```

   and a link line at the bottom: `[0.2.0]: https://github.com/PurpleDefender249/beebox/releases/tag/v0.2.0`

3. Commit, tag and push:

   ```sh
   git add CHANGELOG.md
   git commit -m "Release 0.2.0"
   git tag -a v0.2.0 -m "BeeBox 0.2.0"
   git push && git push origin v0.2.0
   ```

4. Watch **Actions → Release**. It builds and signs everything, creates the
   GitHub release, and publishes the package repository to the `gh-pages`
   branch. Users get the update with their next `apt upgrade` or `dnf upgrade`.

**The very first time only:** after the first release finishes, open
**Settings → Pages**, set **Source** to **Deploy from a branch**, pick
**gh-pages** and **/ (root)**, and save. A minute later
<https://purpledefender249.github.io/beebox/> shows the install page and the
one-line installer works.

Keep the repository name **lowercase** (`beebox`). GitHub Pages addresses
are case-sensitive, so a repository called `BeeBox` is served at
`/BeeBox/`, not at the `/beebox/` address the README and installer use.

To republish the package repository for an existing release (after renaming
the repository, or if the Pages branch was lost), run:

```sh
gh workflow run release.yml -f tag=v0.1.0
gh run watch          # pick the run; it takes about a minute
```

Pre-releases: tag `v0.2.0-rc1`. They appear on the releases page marked as
pre-release and are not pushed to the package repository.

## 5. If a release goes wrong

- **The workflow failed:** fix the cause, delete the tag, and tag again:

  ```sh
  git tag -d v0.2.0 && git push --delete origin v0.2.0
  gh release delete v0.2.0 --yes 2>/dev/null || true
  # fix, commit, push, then repeat step 4.3
  ```

- **A published version is broken:** don't reuse its number. Fix it and
  release the next patch version (`0.2.1`). Package managers only move
  forward, so this is the fastest way to reach users.

## 6. Keeping dependencies current

BeeBox has no third-party Go dependencies. Dependabot opens pull requests for
the GitHub Actions it uses (pinned to exact commits); merge them when CI is
green. When a Go security release comes out, update the `toolchain` line in
`go.mod` (for example `toolchain go1.26.11`), run `make check`, and release a
patch version. `govulncheck` in CI fails when the pinned toolchain has a known
vulnerability that BeeBox's code reaches.
