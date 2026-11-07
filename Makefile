# BeeBox build, test and packaging.
#
#   make              build ./build/beebox
#   make check        everything CI runs (format, vet, tests with -race, lint)
#   sudo make install install to /usr/local (override with PREFIX=/usr)
#   make dist         release archives and .deb/.rpm/.pkg.tar.zst packages
#   make repo         signed APT/DNF repository from build/dist (needs REPO_KEY)
#
# Zero third-party Go dependencies: only the Go toolchain is needed to build.

MODULE   := $(shell go list -m)
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo 0.0.0-dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w -bindnow -X $(MODULE)/internal/version.Version=$(VERSION) \
            -X $(MODULE)/internal/version.Commit=$(COMMIT) \
            -X $(MODULE)/internal/version.Date=$(DATE)
GOFLAGS  := -trimpath -buildvcs=false -buildmode=pie
ARCHES   ?= amd64 arm64
MAINTAINER := PurpleDefender249 <PurpleDefender249@users.noreply.github.com>
# Path to an ASCII-armoured private key (no passphrase) to sign the .rpm
# files with. The release workflow sets it; local builds leave it empty.
SIGNING_KEY ?=

PREFIX   ?= /usr/local
BINDIR   ?= $(PREFIX)/bin
DATADIR  ?= $(PREFIX)/share
UNITDIR  ?= $(PREFIX)/lib/systemd/system
SYSCONFDIR ?= /etc
DESTDIR  ?=

BUILD := build

.PHONY: all build test race vet fmt-check lint vuln check man completions install uninstall dist repo clean

all: build

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD)/beebox ./cmd/beebox

test:
	go test ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "Run gofmt on:"; echo "$$out"; exit 1; fi

lint:
	@command -v staticcheck >/dev/null || { echo "install: go install honnef.co/go/tools/cmd/staticcheck@latest"; exit 1; }
	staticcheck ./...

vuln:
	@command -v govulncheck >/dev/null || { echo "install: go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	govulncheck ./...

check: fmt-check vet race lint

man: $(BUILD)/beebox.1.gz
$(BUILD)/beebox.1.gz: packaging/man/beebox.1
	@mkdir -p $(BUILD)
	sed 's/@VERSION@/$(VERSION)/' $< | gzip -9n > $@

completions: build
	@mkdir -p $(BUILD)/completions
	$(BUILD)/beebox completion bash > $(BUILD)/completions/beebox.bash
	$(BUILD)/beebox completion zsh  > $(BUILD)/completions/_beebox
	$(BUILD)/beebox completion fish > $(BUILD)/completions/beebox.fish

install: build man completions
	install -Dm755 $(BUILD)/beebox $(DESTDIR)$(BINDIR)/beebox
	sed 's|/usr/bin/beebox|$(BINDIR)/beebox|' packaging/systemd/beebox.service > $(BUILD)/beebox.service
	install -Dm644 $(BUILD)/beebox.service $(DESTDIR)$(UNITDIR)/beebox.service
	install -Dm644 packaging/xdg/beebox-notify.desktop $(DESTDIR)$(SYSCONFDIR)/xdg/autostart/beebox-notify.desktop
	install -Dm644 packaging/xdg/beebox.desktop $(DESTDIR)$(DATADIR)/applications/beebox.desktop
	cd packaging/icons/hicolor && for f in */apps/beebox.*; do \
		install -Dm644 "$$f" "$(DESTDIR)$(DATADIR)/icons/hicolor/$$f"; done
	install -Dm644 $(BUILD)/beebox.1.gz $(DESTDIR)$(DATADIR)/man/man1/beebox.1.gz
	install -Dm644 $(BUILD)/completions/beebox.bash $(DESTDIR)$(DATADIR)/bash-completion/completions/beebox
	install -Dm644 $(BUILD)/completions/_beebox $(DESTDIR)$(DATADIR)/zsh/site-functions/_beebox
	install -Dm644 $(BUILD)/completions/beebox.fish $(DESTDIR)$(DATADIR)/fish/vendor_completions.d/beebox.fish
	@if [ ! -e $(DESTDIR)$(SYSCONFDIR)/beebox/beebox.conf ]; then \
		install -Dm644 packaging/beebox.conf $(DESTDIR)$(SYSCONFDIR)/beebox/beebox.conf; fi
	@if [ -z "$(DESTDIR)" ] && [ -d /run/systemd/system ]; then \
		systemctl daemon-reload; \
		echo; echo "Installed. Start recording with:  sudo systemctl enable --now beebox"; echo; fi

uninstall:
	-@if [ -z "$(DESTDIR)" ] && [ -d /run/systemd/system ]; then systemctl disable --now beebox.service 2>/dev/null; fi
	rm -f $(DESTDIR)$(BINDIR)/beebox $(DESTDIR)$(UNITDIR)/beebox.service \
	      $(DESTDIR)$(SYSCONFDIR)/xdg/autostart/beebox-notify.desktop \
	      $(DESTDIR)$(DATADIR)/applications/beebox.desktop \
	      $(DESTDIR)$(DATADIR)/icons/hicolor/*/apps/beebox.svg \
	      $(DESTDIR)$(DATADIR)/icons/hicolor/*/apps/beebox.png \
	      $(DESTDIR)$(DATADIR)/man/man1/beebox.1.gz \
	      $(DESTDIR)$(DATADIR)/bash-completion/completions/beebox \
	      $(DESTDIR)$(DATADIR)/zsh/site-functions/_beebox \
	      $(DESTDIR)$(DATADIR)/fish/vendor_completions.d/beebox.fish
	@echo "Settings in $(SYSCONFDIR)/beebox and data in /var/lib/beebox were kept; remove them by hand if you like."

# Release artefacts: one static binary per architecture, a tarball, native
# packages built with nfpm (https://nfpm.goreleaser.com), and checksums.
dist: man
	@command -v nfpm >/dev/null || { echo "install: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"; exit 1; }
	rm -rf $(BUILD)/dist && mkdir -p $(BUILD)/dist
	@# Package changelogs: one entry for this release, pointing to CHANGELOG.md.
	printf 'beebox (%s) stable; urgency=medium\n\n  * Release %s.\n    Notes: https://github.com/PurpleDefender249/beebox/blob/main/CHANGELOG.md\n\n -- %s  %s\n' \
		"$(VERSION)" "$(VERSION)" "$(MAINTAINER)" "$$(date -u -d '$(DATE)' -R)" | gzip -9n > $(BUILD)/changelog.Debian.gz
	printf -- '- semver: %s\n  date: %s\n  packager: %s\n  changes:\n    - note: Release %s. Full notes in CHANGELOG.md.\n' \
		"$(VERSION)-1" "$(DATE)" "$(MAINTAINER)" "$(VERSION)" > $(BUILD)/changelog.yml
	@for arch in $(ARCHES); do \
		echo "==> linux/$$arch"; \
		mkdir -p $(BUILD)/linux-$$arch/completions; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD)/linux-$$arch/beebox ./cmd/beebox || exit 1; \
		go run ./cmd/beebox completion bash > $(BUILD)/linux-$$arch/completions/beebox.bash; \
		go run ./cmd/beebox completion zsh  > $(BUILD)/linux-$$arch/completions/_beebox; \
		go run ./cmd/beebox completion fish > $(BUILD)/linux-$$arch/completions/beebox.fish; \
		sed -e "s/\$${GOARCH}/$$arch/g" -e "s/\$${VERSION}/$(VERSION)/g" packaging/nfpm.yaml > $(BUILD)/nfpm-$$arch.yaml; \
		{ cat $(BUILD)/nfpm-$$arch.yaml; echo "changelog: $(BUILD)/changelog.yml"; } > $(BUILD)/nfpm-$$arch-rpm.yaml; \
		if [ -n "$(SIGNING_KEY)" ]; then \
			sed -i '/^rpm:$$/a\  signature:\n    key_file: $(SIGNING_KEY)' $(BUILD)/nfpm-$$arch-rpm.yaml; \
		fi; \
		nfpm package -f $(BUILD)/nfpm-$$arch.yaml -p deb -t $(BUILD)/dist/ || exit 1; \
		nfpm package -f $(BUILD)/nfpm-$$arch-rpm.yaml -p rpm -t $(BUILD)/dist/ || exit 1; \
		nfpm package -f $(BUILD)/nfpm-$$arch.yaml -p archlinux -t $(BUILD)/dist/ || exit 1; \
		tar -C $(BUILD) -czf $(BUILD)/dist/beebox_$(VERSION)_linux_$$arch.tar.gz \
			--transform "s|^linux-$$arch|beebox_$(VERSION)_linux_$$arch|" \
			linux-$$arch/beebox linux-$$arch/completions; \
	done
	cd $(BUILD)/dist && sha256sum * > SHA256SUMS
	@ls -l $(BUILD)/dist

# The signed package repository published on GitHub Pages. REPO_KEY is a
# GnuPG key ID or fingerprint already in your keyring.
repo:
	@test -n "$(REPO_KEY)" || { echo "set REPO_KEY to the signing key's fingerprint"; exit 1; }
	scripts/build-repo.sh $(BUILD)/dist $(BUILD)/site "$(REPO_KEY)" "$(VERSION)" $(REPO_URL)

clean:
	rm -rf $(BUILD)
