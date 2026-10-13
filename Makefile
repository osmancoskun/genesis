# genesis — build, install, and packaging
#
# Developer install (default PREFIX=/usr/local):
#   make build-deps && make deps && make build && sudo make install ENABLE=1
#
# Fedora RPM (Meson orchestration; Go build stays in Make):
#   make package-rpm
#   VERSION_MODE=tag make package-rpm   # exact newest v* tag, no patch bump
#   VERSION=1.2.3 make package-rpm      # hard override

DESTDIR ?=
PREFIX  ?= /usr/local
ENABLE  ?= 0
PURGE   ?= 0

BINDIR      := $(PREFIX)/bin
UNITDIR     := $(PREFIX)/lib/systemd/system
DOCDIR      := $(PREFIX)/share/doc/genesis
SHAREDIR    := $(PREFIX)/share/genesis
SYSCONFDIR  := /etc/genesis

# Git-derived X.Y.Z (see scripts/git-version.sh). VERSION= override / VERSION_MODE=auto|tag.
VERSION_MODE ?= auto
VERSION      ?= $(shell VERSION_MODE='$(VERSION_MODE)' ./scripts/git-version.sh)
RPM_VERSION  ?= $(VERSION)
export VERSION
export VERSION_MODE

MESON_BUILDDIR ?= build/meson

GO       ?= go
CGO      := CGO_ENABLED=0
GOFLAGS  := -trimpath -ldflags="-s -w"
AGENT_OUT := bin/genesis-agent
CTL_OUT   := bin/genesis-ctl

UNIT_IN   := deploy/systemd/genesis.service.in
UNIT_GEN  := deploy/systemd/genesis.service
UNIT_NAME := genesis.service

.PHONY: verify test tidy doctor agent menu setup pin-check \
	demo-docker demo-docker-split host-dry host-cleanup \
	build-deps deps check-deps check-deps-install check-deps-package check-deps-runtime \
	build unit install enable uninstall \
	package-rpm rpm dist clean

verify:
	$(GO) run ./cmd/verify

test:
	$(GO) test ./...

tidy:
	$(GO) mod tidy

doctor:
	$(GO) run ./cmd/ctl doctor

pin-check:
	$(GO) run ./cmd/ctl pin check

agent:
	$(GO) run ./cmd/agent -config configs/local.yaml

menu:
	$(GO) run ./cmd/ctl menu

setup:
	$(GO) run ./cmd/ctl setup

# build-deps: Go toolchain for make build
# deps:       host runtime (systemd, resolved, D-Bus, iproute, …)
build-deps:
	@./scripts/check-deps.sh build

deps:
	@./scripts/check-deps.sh runtime

check-deps: build-deps

check-deps-runtime: deps

check-deps-install:
	@./scripts/check-deps.sh install

check-deps-package:
	@./scripts/check-deps.sh package

build: build-deps
	mkdir -p bin
	$(CGO) $(GO) build $(GOFLAGS) -o $(AGENT_OUT) ./cmd/agent
	$(CGO) $(GO) build $(GOFLAGS) -o $(CTL_OUT) ./cmd/ctl

# Regenerate convenience unit in-tree (default PREFIX=/usr/local for docs / manual copy).
unit: $(UNIT_IN)
	sed 's|@PREFIX@|$(PREFIX)|g' $(UNIT_IN) > $(UNIT_GEN)
	@echo "wrote $(UNIT_GEN) (PREFIX=$(PREFIX))"

install: check-deps-install build
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(AGENT_OUT) $(DESTDIR)$(BINDIR)/genesis-agent
	install -m 755 $(CTL_OUT) $(DESTDIR)$(BINDIR)/genesis-ctl
	install -d $(DESTDIR)$(UNITDIR)
	sed 's|@PREFIX@|$(PREFIX)|g' $(UNIT_IN) > $(DESTDIR)$(UNITDIR)/$(UNIT_NAME)
	install -d $(DESTDIR)$(DOCDIR)
	install -m 644 docs/service.md docs/ctl-manual.md docs/resolved-coexistence.md $(DESTDIR)$(DOCDIR)/
	install -d $(DESTDIR)$(SHAREDIR)
	install -m 644 configs/default.config.yaml $(DESTDIR)$(SHAREDIR)/default.config.yaml
	install -m 644 configs/discord.config.yaml $(DESTDIR)$(SHAREDIR)/discord.config.yaml
	install -d $(DESTDIR)$(SYSCONFDIR)
	install -d $(DESTDIR)/etc/systemd/resolved.conf.d
	@if [ ! -e $(DESTDIR)$(SYSCONFDIR)/config.yaml ]; then \
		install -m 644 configs/default.config.yaml $(DESTDIR)$(SYSCONFDIR)/config.yaml; \
		echo "installed $(DESTDIR)$(SYSCONFDIR)/config.yaml (blank first-boot; configure via Web UI :8787)"; \
	else \
		echo "keeping existing $(DESTDIR)$(SYSCONFDIR)/config.yaml"; \
	fi
	# Avoid user_home_t labels from cp -a (blocks systemd unit writes under Enforcing).
	@if [ -z "$(DESTDIR)" ]; then \
		if command -v restorecon >/dev/null 2>&1; then restorecon -RF $(SYSCONFDIR) || true; fi; \
		systemctl daemon-reload || true; \
		echo "installed unit $(UNITDIR)/$(UNIT_NAME)"; \
		if [ "$(ENABLE)" = "1" ]; then $(MAKE) enable; fi; \
	else \
		echo "staged under DESTDIR=$(DESTDIR) PREFIX=$(PREFIX) (no systemd enable)"; \
	fi

enable:
	@if [ -n "$(DESTDIR)" ]; then echo "enable skipped under DESTDIR"; exit 0; fi
	systemctl enable --now $(UNIT_NAME)
	@echo "enabled and started $(UNIT_NAME)"

uninstall:
	@if [ -z "$(DESTDIR)" ]; then \
		systemctl disable --now $(UNIT_NAME) 2>/dev/null || true; \
		systemctl daemon-reload || true; \
	fi
	rm -f $(DESTDIR)$(BINDIR)/genesis-agent $(DESTDIR)$(BINDIR)/genesis-ctl
	rm -f $(DESTDIR)$(UNITDIR)/$(UNIT_NAME)
	rm -rf $(DESTDIR)$(DOCDIR) $(DESTDIR)$(SHAREDIR)
	@if [ "$(PURGE)" = "1" ]; then \
		rm -rf $(DESTDIR)$(SYSCONFDIR); \
		echo "purged $(DESTDIR)$(SYSCONFDIR)"; \
	else \
		echo "left $(DESTDIR)$(SYSCONFDIR) (PURGE=1 to remove)"; \
	fi

# Source tarball (also produced by package-rpm via Meson).
dist:
	mkdir -p dist
	tar -czf dist/genesis-$(RPM_VERSION).tar.gz \
		--exclude='.git' \
		--exclude='bin' \
		--exclude='dist' \
		--exclude='build' \
		--exclude='docker/split/.build' \
		--exclude='configs/local.yaml' \
		--exclude='configs/local-*.yaml' \
		--transform='s,^\./,genesis-$(RPM_VERSION)/,' \
		.

# Fedora RPM via Meson (version + changelog from git; Go build still Make inside the spec).
package-rpm: check-deps-package
	@if [ -d "$(MESON_BUILDDIR)" ]; then \
		meson setup "$(MESON_BUILDDIR)" --reconfigure; \
	else \
		meson setup "$(MESON_BUILDDIR)"; \
	fi
	meson compile -C "$(MESON_BUILDDIR)" package_rpm

# Backward-compatible alias.
rpm: package-rpm

clean:
	rm -rf bin dist/rpm "$(MESON_BUILDDIR)"
	rm -f dist/genesis-*.tar.gz

# Two CoreDNS hello-world upstreams + agent; exits non-zero if steering fails.
demo-docker:
	docker compose -f docker/demo/compose.yaml up --build --abort-on-container-exit --exit-code-from verify
	docker compose -f docker/demo/compose.yaml down --remove-orphans

# LAN vs WARP-like split entirely in compose nets (never --network=host; host routing untouched).
demo-docker-split:
	mkdir -p docker/split/.build
	$(CGO) $(GO) build $(GOFLAGS) -o docker/split/.build/agent ./cmd/agent
	$(CGO) $(GO) build $(GOFLAGS) -o docker/split/.build/ifacebyip ./docker/split/ifacebyip
	$(CGO) $(GO) build $(GOFLAGS) -o docker/split/.build/verifycheck ./docker/split/verifycheck
	docker compose -f docker/split/compose.yaml up --build --abort-on-container-exit --exit-code-from verify
	docker compose -f docker/split/compose.yaml down --remove-orphans

# Host try: Discord via CloudflareWARP, default via eno1 (dry-run by default).
host-dry:
	./scripts/host-try.sh

host-cleanup:
	./scripts/host-cleanup.sh
