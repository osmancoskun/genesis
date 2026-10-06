#!/bin/sh
# Map docker networks (by IP) → iface names, then start agent. Never uses host netns.
set -eu
LAN_IF=$(ifacebyip 172.30.0.0/24) || true
WARP_IF=$(ifacebyip 172.30.1.0/24) || true
if [ -z "${LAN_IF:-}" ] || [ -z "${WARP_IF:-}" ]; then
	echo "entrypoint: could not find lan/warp ifaces (lan='${LAN_IF:-}' warp='${WARP_IF:-}')" >&2
	exit 1
fi
echo "entrypoint: lan=$LAN_IF warp=$WARP_IF"
sed -e "s/__LAN_IF__/${LAN_IF}/g" -e "s/__WARP_IF__/${WARP_IF}/g" \
	/etc/genesis/rules.template.yaml > /tmp/rules.yaml
exec /usr/local/bin/agent \
	-rules /tmp/rules.yaml \
	-listen 0.0.0.0:5353 \
	-pins dry-run \
	-default-path dry-run
