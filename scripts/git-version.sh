#!/usr/bin/env bash
# Emit an RPM-safe X.Y.Z version from git tags (never creates tags).
#
# Env:
#   VERSION       — hard override (normalized to X.Y.Z)
#   VERSION_MODE  — auto (default) | tag
#     auto: HEAD on tag → tag version; commits after tag → patch +1
#     tag:  always the newest v* tag version (no bump)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MODE="${VERSION_MODE:-auto}"
OVERRIDE="${VERSION:-}"

normalize() {
	local raw="$1"
	local v
	v="$(printf '%s' "$raw" | sed -E 's/^v//; s/[^0-9.].*$//; s/[^0-9.]+/./g; s/^\.+//; s/\.+$//')"
	# Ensure three numeric components.
	local major minor patch
	IFS=. read -r major minor patch _ <<<"$v"
	major="${major:-0}"
	minor="${minor:-0}"
	patch="${patch:-0}"
	# Non-numeric → 0
	[[ "$major" =~ ^[0-9]+$ ]] || major=0
	[[ "$minor" =~ ^[0-9]+$ ]] || minor=0
	[[ "$patch" =~ ^[0-9]+$ ]] || patch=0
	printf '%s.%s.%s\n' "$major" "$minor" "$patch"
}

bump_patch() {
	local v
	v="$(normalize "$1")"
	local major minor patch
	IFS=. read -r major minor patch <<<"$v"
	printf '%s.%s.%s\n' "$major" "$minor" "$((patch + 1))"
}

if [[ -n "$OVERRIDE" ]]; then
	normalize "$OVERRIDE"
	exit 0
fi

if ! command -v git >/dev/null 2>&1 || ! git rev-parse --git-dir >/dev/null 2>&1; then
	echo "0.0.1"
	exit 0
fi

if [[ -n "$(git status --porcelain 2>/dev/null || true)" ]]; then
	echo "warn: working tree dirty (version ignores dirty; not reflected in RPM Version)" >&2
fi

mapfile -t TAGS < <(git tag -l 'v[0-9]*' --sort=-v:refname)
if [[ "${#TAGS[@]}" -eq 0 || -z "${TAGS[0]:-}" ]]; then
	# No tags: auto starts at 0.0.1; tag mode still needs a digit version.
	echo "0.0.1"
	exit 0
fi

newest="${TAGS[0]}"
base="$(normalize "$newest")"

case "$MODE" in
tag)
	printf '%s\n' "$base"
	;;
auto)
	# Exact tag on HEAD?
	if git describe --tags --exact-match HEAD >/dev/null 2>&1; then
		printf '%s\n' "$base"
		exit 0
	fi
	# Commits after newest tag?
	ahead="$(git rev-list --count "${newest}..HEAD" 2>/dev/null || echo 0)"
	if [[ "$ahead" -gt 0 ]]; then
		bump_patch "$base"
	else
		# Detached / same commit as tag under another name, etc.
		printf '%s\n' "$base"
	fi
	;;
*)
	echo "error: VERSION_MODE must be auto or tag (got: $MODE)" >&2
	exit 1
	;;
esac
