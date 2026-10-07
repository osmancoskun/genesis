#!/usr/bin/env bash
# Assemble a build-time genesis.spec with a git-derived %changelog.
# Does not modify packaging/fedora/genesis.spec in the repo.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/packaging/fedora/genesis.spec"
OUT="${1:?usage: assemble-rpm-spec.sh <out-spec> [version] [release]}"
VERSION="${2:-}"
RELEASE="${3:-1}"

if [[ -z "$VERSION" ]]; then
	VERSION="$("$ROOT/scripts/rpm-version.sh" "$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo 0.1.0)")"
fi

mkdir -p "$(dirname "$OUT")"
# Drop committed %changelog (placeholder) and any trailing comment above it.
sed '/^# %changelog is injected/d; /^%changelog$/,$d' "$SRC" > "$OUT"
{
	echo '%changelog'
	"$ROOT/scripts/gen-rpm-changelog.sh" "$VERSION" "$RELEASE"
} >> "$OUT"

echo "assembled $OUT (changelog from git, ${VERSION}-${RELEASE})"
