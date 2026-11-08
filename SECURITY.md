# Security policy

BeeBox's recorder runs as root, so security reports are taken seriously.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private vulnerability
reporting: on the repository page choose **Security → Report a
vulnerability**. You'll get a reply within a week. Please include steps to
reproduce and the BeeBox version (`beebox version --verbose`).

## Supported versions

Security fixes go into the latest release.

## Design measures

- No third-party Go dependencies; releases are built with a pinned, patched
  Go toolchain and scanned with `govulncheck` and `gosec` in CI.
- The service is sandboxed by systemd: capabilities limited to `CAP_SYSLOG`
  and `CAP_IPC_LOCK`, read-only system, no home folders, no network, no new
  privileges, a system-call allow-list, and only `/dev/kmsg` readable.
  `systemd-analyze security beebox` scores it 1.4 ("OK"). It also tells
  systemd-oomd to avoid it and keeps running if a helper process is killed
  for memory (`OOMPolicy=continue`).
- `journalctl` is run with an argument list (never a shell) and boot
  selectors are validated before use.
- Report IDs from the command line are validated before they are used in a
  path, preventing path traversal.
- Report pages escape all log text, carry `Content-Security-Policy:
  default-src 'none'`, and insert dynamic text with `textContent`.
- Release artefacts include SHA-256 checksums (with a detached signature,
  `SHA256SUMS.asc`) and GitHub build provenance attestations
  (`gh attestation verify <file> --repo PurpleDefender249/beebox`).
- `.rpm` packages, the APT repository (`InRelease`) and the DNF repository
  metadata (`repomd.xml.asc`) are signed with a dedicated key held only in
  GitHub Actions secrets and an offline backup. APT binds it to the BeeBox
  repository alone with `Signed-By`, never trusting it system-wide.
- Binaries are position-independent executables with full RELRO, built with
  `-trimpath` from a pinned Go toolchain; CI pins every GitHub Action to a
  commit SHA.
