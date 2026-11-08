# Contributing to BeeBox

Thanks for helping. BeeBox is most useful when its verdicts are right, so the
most valuable contributions are **reports of wrong or unclear verdicts** and
**new detection rules**.

## Reporting a wrong verdict

Open an issue with the "Wrong verdict" template and include:

- what actually happened, if you know;
- `beebox show --json` for the report (read it first, see [docs/PRIVACY.md](docs/PRIVACY.md));
- your distribution, kernel (`uname -r`) and BeeBox version (`beebox version --verbose`).

## Development

You only need Go (1.22+; `go.mod` pins the toolchain used for releases).

```sh
make            # build ./build/beebox
make check      # gofmt, go vet, tests with -race, staticcheck
make vuln       # govulncheck
```

Packaging changes: build with `make dist VERSION=0.0.0-dev` (needs
[nfpm](https://nfpm.goreleaser.com)) and test in a clean container, for
example `docker run --rm -v "$PWD:/src" -w /src fedora:42 scripts/test-packages.sh`.
`scripts/test-repo.sh` tests the package repository and the one-line installer
end to end. [docs/MAINTAINING.md](docs/MAINTAINING.md) covers releases.

Run the recorder without root by pointing it at a scratch directory:

```sh
BEEBOX_STATE_DIR=/tmp/beebox-dev BEEBOX_RUNTIME_DIR=/tmp/beebox-dev/run ./build/beebox record
BEEBOX_STATE_DIR=/tmp/beebox-dev ./build/beebox status
```

Render every built-in scenario to see how verdicts read:

```sh
./build/beebox view --sample --scenario storage
```

## Guidelines

- **No new dependencies.** The recorder runs as root on every boot; the
  standard library covers what BeeBox needs. Discuss in an issue first if you
  believe one is unavoidable.
- **Privacy first.** Never record command-line arguments, file contents or
  anything from home folders. New evidence lines must pass through `redact`.
- **Plain language.** Verdicts and advice are read by people who just lost
  work. Say what happened and what to do, in short sentences, without jargon
  where a plain word exists. Give exact commands, and tailor them with `Env`.
- **Be honest about confidence.** Prefer a lower score and "unclear" to a
  confident wrong answer.
- **Accessibility.** Colour never carries meaning alone; charts need text
  alternatives; check pages with the keyboard only.
- **Tests.** Add a scenario in `internal/scenario` and a case in
  `internal/analyze/analyze_test.go` for every new rule.
- Commit messages: imperative mood, short subject ("Detect NVMe controller resets").

By contributing you agree that your contributions are licensed under the MIT
licence and that you'll follow the [code of conduct](CODE_OF_CONDUCT.md).
