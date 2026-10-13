#!/usr/bin/env bash
# Verify build / runtime / install / package prerequisites for genesis.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MIN_GO="${MIN_GO:-1.26}"
MODE="${1:-build}" # build | runtime | install | package

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

want_cmd() {
	local c="$1"
	shift
	local why="${*:-optional}"
	if command -v "$c" >/dev/null 2>&1; then
		ok "$c ($(command -v "$c"))"
	else
		warn "missing optional command: $c ($why)"
	fi
}

case "$MODE" in
build|runtime|install|package) ;;
*)
	err "usage: check-deps.sh [build|runtime|install|package]"
	printf '\nDependency check failed.\n' >&2
	exit 1
	;;
esac

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

unit_exists() {
	local u="$1"
	systemctl cat "$u" >/dev/null 2>&1
}

unit_is_active() {
	local u="$1"
	systemctl is-active --quiet "$u" 2>/dev/null
}

check_go() {
	need_cmd go
	need_cmd install
	if command -v go >/dev/null 2>&1; then
		raw="$(go env GOVERSION 2>/dev/null || go version)"
		ver="$(printf '%s\n' "$raw" | sed -n 's/.*go\([0-9][0-9]*\.[0-9][0-9]*\(\.[0-9][0-9]*\)*\).*/\1/p' | head -1)"
		if [[ -z "$ver" ]]; then
			err "could not parse Go version from: $raw"
		elif version_ge "$ver" "$MIN_GO"; then
			ok "go version $ver (>= $MIN_GO)"
		else
			err "go $ver is too old; need >= $MIN_GO (see $ROOT/go.mod)"
		fi
	fi
	want_cmd git "version tags / changelog for package builds"
	want_cmd make "usual entrypoint for build/install"
}

# Host tools the agent/ctl/Mode B expect at runtime (pins use netlink, not nft/iptables CLI).
check_runtime() {
	need_cmd systemctl
	need_cmd resolvectl
	need_cmd ip

	if [[ -S /run/dbus/system_bus_socket ]] || [[ -S /var/run/dbus/system_bus_socket ]]; then
		ok "system D-Bus socket"
	else
		err "system D-Bus socket missing (need dbus-broker or dbus-daemon; doctor/link DNS use resolve1)"
	fi

	if unit_exists systemd-resolved.service; then
		if unit_is_active systemd-resolved.service; then
			ok "systemd-resolved.service active"
		else
			warn "systemd-resolved.service installed but not active (link DNS probes + Mode B need it)"
		fi
	else
		err "systemd-resolved.service not found (package: systemd-resolved)"
	fi

	if [[ -d /etc/systemd/resolved.conf.d ]]; then
		ok "resolved.conf.d (/etc/systemd/resolved.conf.d)"
	else
		warn "missing /etc/systemd/resolved.conf.d (make install creates it; Mode B Domains= drop-ins need it)"
	fi

	# Operator / coexistence tools — agent does not shell out to these for pins.
	if command -v nft >/dev/null 2>&1; then
		ok "nft ($(command -v nft))"
	elif command -v iptables >/dev/null 2>&1; then
		ok "iptables ($(command -v iptables)) — nft preferred when available"
	else
		warn "neither nft nor iptables found (optional; useful for kill-switch / firewall coexistence checks)"
	fi

	want_cmd ss "port / listener diagnostics"
	want_cmd busctl "D-Bus debugging (resolve1 / systemd1)"
	want_cmd sudo "ctl up/down/apply and live pin elevation"

	warn "live pins need CAP_NET_ADMIN (+ CAP_NET_RAW for SO_BINDTODEVICE); genesis.service AmbientCapabilities covers the unit"
}

check_install() {
	check_runtime
	if [[ "$(id -u)" -ne 0 ]]; then
		warn "install/enable usually needs root (sudo make install ENABLE=1)"
	fi
}

check_package() {
	need_cmd git
	need_cmd make
	need_cmd meson
	need_cmd ninja
	need_cmd rpmbuild
	need_cmd tar
}

case "$MODE" in
build)
	check_go
	;;
runtime)
	check_runtime
	;;
install)
	check_go
	check_install
	;;
package)
	check_go
	check_package
	;;
esac

if [[ "$fail" -ne 0 ]]; then
	printf '\nDependency check failed.\n' >&2
	case "$MODE" in
	build)
		printf 'Fedora: sudo dnf install -y golang git make\n' >&2
		;;
	runtime)
		printf 'Fedora: sudo dnf install -y systemd systemd-resolved dbus-broker iproute nftables\n' >&2
		printf '  (dbus-daemon also fine; nftables optional — pins use netlink, not nft CLI)\n' >&2
		;;
	install)
		printf 'Fedora: sudo dnf install -y golang git make systemd systemd-resolved dbus-broker iproute nftables\n' >&2
		;;
	package)
		printf 'Fedora: sudo dnf install -y golang git make meson ninja-build rpm-build systemd-rpm-macros\n' >&2
		;;
	esac
	exit 1
fi
printf '\nAll required dependencies OK (%s).\n' "$MODE"
