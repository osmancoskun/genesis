#!/usr/bin/env bash
# Build a Fedora RPM: source tarball + git changelog + rpmbuild.
# Invoked by Meson run_target package_rpm (or directly).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${1:-}"
RELEASE="${2:-1}"

if [[ -z "$VERSION" ]]; then
	VERSION="$("$ROOT/scripts/git-version.sh")"
fi

echo "package-rpm: version=${VERSION} release=${RELEASE}"

mkdir -p dist
TARBALL="dist/genesis-${VERSION}.tar.gz"
tar -czf "$TARBALL" \
	--exclude='.git' \
	--exclude='bin' \
	--exclude='dist' \
	--exclude='build' \
	--exclude='docker/split/.build' \
	--exclude='configs/local.yaml' \
	--exclude='configs/local-*.yaml' \
	--transform="s,^\\./,genesis-${VERSION}/," \
	.

mkdir -p dist/rpm/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
cp "$TARBALL" dist/rpm/SOURCES/
"$ROOT/scripts/assemble-rpm-spec.sh" dist/rpm/SPECS/genesis.spec "$VERSION" "$RELEASE"

rpmbuild -ba \
	--define "_topdir ${ROOT}/dist/rpm" \
	--define "genesis_version ${VERSION}" \
	dist/rpm/SPECS/genesis.spec

echo "RPMs under dist/rpm/RPMS/"
find dist/rpm/RPMS -name '*.rpm' -print
