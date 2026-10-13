#!/usr/bin/env bash
# Normalize a version string into RPM Version (must start with a digit).
# With no args, delegates to git-version.sh (VERSION / VERSION_MODE respected).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [[ $# -eq 0 || -z "${1:-}" ]]; then
	exec "$ROOT/scripts/git-version.sh"
fi

# Reuse git-version override path for consistent X.Y.Z output.
VERSION="$1" exec "$ROOT/scripts/git-version.sh"
