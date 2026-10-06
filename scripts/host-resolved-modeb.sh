#!/usr/bin/env bash
# Install / remove systemd-resolved Mode B drop-in so Discord* goes to the agent.
# Usage:
#   ./scripts/host-resolved-modeb.sh install [port]   # default port 5553
#   ./scripts/host-resolved-modeb.sh remove
set -euo pipefail

DROP_DIR=/etc/systemd/resolved.conf.d
DROP_FILE="${DROP_DIR}/genesis-discord.conf"
PORT="${2:-5553}"

if [[ "$(id -u)" -ne 0 ]]; then
  exec sudo -E "$0" "$@"
fi

cmd="${1:-}"
case "$cmd" in
install)
	mkdir -p "$DROP_DIR"
	cat >"$DROP_FILE" <<EOF
# Temporary genesis Mode B — Discord domains only → agent :${PORT}
# Removed by scripts/host-resolved-modeb.sh remove / host-cleanup.sh
[Resolve]
DNS=127.0.0.1:${PORT}
Domains=~discord.com ~discord.app ~discord.gg ~discordapp.com ~discordapp.net ~discord.media
EOF
	systemctl reload systemd-resolved
	echo "installed $DROP_FILE (DNS=127.0.0.1:${PORT}) and reloaded systemd-resolved"
	resolvectl status | head -40 || true
	;;
remove)
	removed=0
	for f in "$DROP_FILE" "${DROP_DIR}/path-director-discord.conf"; do
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
