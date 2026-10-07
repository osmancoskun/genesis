#!/usr/bin/env bash
# Emit an RPM %changelog body from git history.
# Author: git config user.name / user.email
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${1:-0.1.0}"
RELEASE="${2:-1}"

NAME="$(git config user.name 2>/dev/null || true)"
EMAIL="$(git config user.email 2>/dev/null || true)"
if [[ -z "$NAME" || -z "$EMAIL" ]]; then
	echo "error: git user.name and user.email must be set" >&2
	exit 1
fi

# RPM wants English abbreviated weekday/month (e.g. Wed Oct 07 2026).
fmt_date() {
	LC_ALL=C date -d "$1" '+%a %b %d %Y' 2>/dev/null || LC_ALL=C date -ud "$1" '+%a %b %d %Y'
}

emit_entry() {
	local ver="$1" rel="$2" when="$3"
	shift 3
	local date msg
	date="$(fmt_date "$when")"
	printf '* %s %s <%s> - %s-%s\n' "$date" "$NAME" "$EMAIL" "$ver" "$rel"
	if [[ "$#" -eq 0 ]]; then
		printf -- '- Packaging update\n\n'
		return
	fi
	for msg in "$@"; do
		[[ -n "$msg" ]] || continue
		printf -- '- %s\n' "$msg"
	done
	printf '\n'
}

mapfile -t TAGS < <(git tag -l 'v[0-9]*' --sort=-creatordate)

if [[ "${#TAGS[@]}" -eq 0 ]]; then
	when="$(git log -1 --format=%cI)"
	mapfile -t MSGS < <(git log --pretty=format:'%s')
	emit_entry "$VERSION" "$RELEASE" "$when" "${MSGS[@]}"
	exit 0
fi

# Commits on HEAD after the newest tag → current packaging version entry.
newest="${TAGS[0]}"
mapfile -t AHEAD < <(git log "${newest}..HEAD" --pretty=format:'%s')
if [[ "${#AHEAD[@]}" -gt 0 && -n "${AHEAD[0]:-}" ]]; then
	emit_entry "$VERSION" "$RELEASE" "$(git log -1 --format=%cI HEAD)" "${AHEAD[@]}"
fi

# One entry per tag (newest → oldest).
for i in "${!TAGS[@]}"; do
	tag="${TAGS[$i]}"
	ver="${tag#v}"
	when="$(git log -1 --format=%cI "$tag")"
	if [[ $((i + 1)) -lt "${#TAGS[@]}" ]]; then
		older="${TAGS[$((i + 1))]}"
		mapfile -t MSGS < <(git log "${older}..${tag}" --pretty=format:'%s')
	else
		mapfile -t MSGS < <(git log "$tag" --pretty=format:'%s')
	fi
	emit_entry "$ver" "1" "$when" "${MSGS[@]}"
done
