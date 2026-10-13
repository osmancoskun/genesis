#!/usr/bin/env bash
# Inside a Fedora build root (CI container or local docker): install tools and package-rpm.
# BuildRequires needs the golang RPM; compile may use a newer Go under /usr/local/go.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

dnf -y install \
	golang make git meson ninja-build rpm-build systemd-rpm-macros \
	tar gzip which curl ca-certificates

# Prefer a pinned toolchain when Fedora golang lags go.mod.
GO_VER="${GO_VER:-1.26.0}"
if [[ ! -x /usr/local/go/bin/go ]]; then
	curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz" \
		| tar -C /usr/local -xz
fi
export PATH="/usr/local/go/bin:${PATH}"
go version

git config --global --add safe.directory "$ROOT"
if [[ -z "$(git config user.name 2>/dev/null || true)" ]]; then
	git config --global user.name "github-actions[bot]"
	git config --global user.email "41898282+github-actions[bot]@users.noreply.github.com"
fi

make package-rpm
