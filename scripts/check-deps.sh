#!/usr/bin/env bash
# Verify build (and optional install) prerequisites for genesis.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MIN_GO="${MIN_GO:-1.26}"
MODE="${1:-build}" # build | install

fail=0
warn() { printf 'warn: %s\n' "$*" >&2; }
err()  { printf 'error: %s\n' "$*" >&2; fail=1; }
ok()   { printf 'ok: %s\n' "$*"; }

need_cmd() {
	local c="$1"
	if command -v "$c" >/dev/null 2>&1; then
		ok "$c ($(command -v "$c"))"
	else
		err "missing required command: $c"
	fi
}

# Compare dotted versions: returns 0 if $1 >= $2
version_ge() {
	local a="$1" b="$2"
	local IFS=.
	# shellcheck disable=SC2206
	local av=($a) bv=($b)
	local i
	for ((i = 0; i < ${#bv[@]}; i++)); do
		local ai="${av[i]:-0}"
		local bi="${bv[i]:-0}"
		if ((10#$ai > 10#$bi)); then return 0; fi
		if ((10#$ai < 10#$bi)); then return 1; fi
	done
	return 0
}

need_cmd go
need_cmd install

if command -v go >/dev/null 2>&1; then
	raw="$(go env GOVERSION 2>/dev/null || go version)"
	# GOVERSION like go1.26.0; go version like "go version go1.26.0 linux/amd64"
	ver="$(printf '%s\n' "$raw" | sed -n 's/.*go\([0-9][0-9]*\.[0-9][0-9]*\(\.[0-9][0-9]*\)*\).*/\1/p' | head -1)"
	if [[ -z "$ver" ]]; then
		err "could not parse Go version from: $raw"
	elif version_ge "$ver" "$MIN_GO"; then
		ok "go version $ver (>= $MIN_GO)"
	else
		err "go $ver is too old; need >= $MIN_GO (see $ROOT/go.mod)"
	fi
fi

if [[ "$MODE" == "install" ]]; then
	need_cmd systemctl
	if [[ "$(id -u)" -ne 0 ]]; then
		warn "install/enable usually needs root (sudo make install)"
	fi
	warn "live pins need CAP_NET_ADMIN (and often CAP_NET_RAW); unit AmbientCapabilities covers the service"
fi

if ! command -v git >/dev/null 2>&1; then
	warn "git not found (optional; useful for clone / version tags)"
fi
if ! command -v make >/dev/null 2>&1; then
	warn "make not found (you are probably fine if you invoked this via make)"
fi

if [[ "$fail" -ne 0 ]]; then
	printf '\nDependency check failed.\n' >&2
	printf 'Fedora: sudo dnf install -y golang git make systemd\n' >&2
	exit 1
fi
printf '\nAll required dependencies OK (%s).\n' "$MODE"
