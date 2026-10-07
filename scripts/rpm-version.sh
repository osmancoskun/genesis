#!/usr/bin/env bash
# Normalize a git describe / tag into an RPM Version (must start with a digit).
set -euo pipefail
raw="${1:-0.1.0}"
v="$(printf '%s' "$raw" | sed -E 's/^v//; s/[^0-9A-Za-z.+~]+/./g; s/^\.+//; s/\.+$//')"
case "$v" in
[0-9]*) printf '%s\n' "$v" ;;
*) printf '0.1.0\n' ;;
esac
