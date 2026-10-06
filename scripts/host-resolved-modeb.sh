#!/usr/bin/env bash
# Install / remove systemd-resolved Mode B drop-in so rule domains go to the agent.
# Usage:
#   ./scripts/host-resolved-modeb.sh install [port]   # default port 5553
#   ./scripts/host-resolved-modeb.sh remove
# When the agent runs as root, it also rewrites genesis-domains.conf on apply/add-rule.
set -euo pipefail

DROP_DIR=/etc/systemd/resolved.conf.d
DROP_FILE="${DROP_DIR}/genesis-domains.conf"
LEGACY_DROP="${DROP_DIR}/genesis-discord.conf"
PORT="${2:-5553}"

if [[ "$(id -u)" -ne 0 ]]; then
  exec sudo -E "$0" "$@"
fi

cmd="${1:-}"
case "$cmd" in
install)
	mkdir -p "$DROP_DIR"
	DOMAINS="~discord.com ~discord.app ~discord.gg ~discordapp.com ~discordapp.net ~discord.media"
	CFG="${GENESIS_CONFIG:-}"
	if [[ -z "$CFG" && -f configs/local.yaml ]]; then
		CFG=configs/local.yaml
	fi
	if [[ -n "$CFG" && -f "$CFG" ]]; then
		extracted=$(awk '
			/match:/ { inmatch=1; next }
			inmatch && /domains:/ { indom=1; next }
			indom && /^[[:space:]]+-[[:space:]]+/ {
				gsub(/["'\''*]/, "", $2)
				gsub(/^\./, "", $2)
				if ($2 != "") print "~" tolower($2)
				next
			}
			indom && /^[[:space:]]*[a-z]/ { indom=0; inmatch=0 }
		' "$CFG" | sort -u | tr '\n' ' ' | sed 's/[[:space:]]*$//') || true
		if [[ -n "${extracted:-}" ]]; then
			DOMAINS="$extracted"
		fi
	fi
	rm -f "$LEGACY_DROP"
	cat >"$DROP_FILE" <<EOF
# genesis Mode B — domains → agent :${PORT}
# Refreshed by root agent on config apply / Web UI add-rule.
[Resolve]
DNS=127.0.0.1:${PORT}
Domains=${DOMAINS}
EOF
	systemctl reload systemd-resolved
	echo "installed $DROP_FILE (DNS=127.0.0.1:${PORT}) Domains=${DOMAINS}"
	resolvectl status | head -40 || true
	;;
remove)
	removed=0
	for f in "$DROP_FILE" "$LEGACY_DROP" "${DROP_DIR}/path-director-discord.conf"; do
		if [[ -f "$f" ]]; then
			rm -f "$f"
			echo "removed $f"
			removed=1
		fi
	done
	if [[ "$removed" -eq 1 ]]; then
		systemctl reload systemd-resolved
	else
		echo "no drop-in present"
	fi
	;;
*)
	echo "usage: $0 install [port]|remove" >&2
	exit 2
	;;
esac
