#!/usr/bin/env bash
# Host try-out: Discord via WARP + browser (Mode B DNS).
#
# IMPORTANT: With Cloudflare WARP connected, default_path (prio 5100) must
# exempt WARP's fwmark 0x100cf (same as WARP rule 5209). Older agents without
# that exemption forced tunnel packets onto eno1 and left warp-cli Reconnecting.
#
# Modes:
#   ./scripts/host-try.sh                      # dry-run dig only
#   HOST_LIVE=1 ./scripts/host-try.sh          # live Discord pins + Mode B
#   HOST_LIVE=1 HOST_DEFAULT_PATH=1 ./scripts/host-try.sh  # eno1 catch-all + WARP pins

# Browser: disable DoH, then open discord.com / discord.app
# Emergency: ./scripts/host-cleanup.sh
set -euo pipefail
export PATH="${HOME}/.local/go/bin:${PATH}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

RULES=configs/host-warp-discord.rules.yaml
LISTEN="${LISTEN:-127.0.0.1:5553}"
MODE="${HOST_LIVE:-0}"
WANT_DP="${HOST_DEFAULT_PATH:-0}"
MODEB_ON=0
CLEANING=0

need() { command -v "$1" >/dev/null || { echo "missing $1" >&2; exit 1; }; }
need dig
need ip
need warp-cli

echo "== preflight =="
ip -br link show eno1 >/dev/null
ip -br link show CloudflareWARP >/dev/null || {
  echo "CloudflareWARP missing — start WARP first: warp-cli connect" >&2
  exit 1
}
warp-cli status || true
echo "default route: $(ip -4 route show default | head -1)"
echo "WARP policy rule (expect ~5209):"
ip -4 rule list | grep -E '5209|65743' || true

echo "== planned pin (no kernel changes) =="
go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface CloudflareWARP

PINS=dry-run
DP=off
if [[ "$MODE" == "1" ]]; then
  echo
  echo "HOST_LIVE=1 enables:"
  echo "  - pins=netlink (Discord* → CloudflareWARP)"
  echo "  - systemd-resolved Mode B: Discord* → ${LISTEN}"
  if [[ "$WANT_DP" == "1" ]]; then
    echo "  - default-path=netlink via eno1 (exempts WARP fwmark 0x100cf so tunnel stays up)"
  else
    echo "  - default-path=off (required while WARP is connected; set HOST_DEFAULT_PATH=1 to force)"
  fi
  echo
  echo "Note: port 5353 is mDNS/Avahi — agent uses ${LISTEN}."
  echo "Browser: turn OFF secure DNS / DoH, then open discord.com / discord.app"
  read -r -p "Type LIVE to continue: " ans
  [[ "$ans" == "LIVE" ]] || { echo "aborted"; exit 1; }
  if [[ "$(id -u)" -ne 0 ]]; then
    echo "re-exec with sudo…"
    exec sudo -E env PATH="$PATH" HOME="$HOME" HOST_LIVE=1 HOST_DEFAULT_PATH="$WANT_DP" LISTEN="$LISTEN" "$0" "$@"
  fi
  PINS=netlink
  if [[ "$WANT_DP" == "1" ]]; then
    DP=netlink
  else
    DP=off
  fi
else
  echo
  echo "Dry-run mode (browser will NOT use the agent). For browser test:"
  echo "  HOST_LIVE=1 $0"
fi

cleanup() {
  [[ "$CLEANING" == "1" ]] && return 0
  CLEANING=1
  trap '' INT TERM
  if [[ -n "${AGENT_PID:-}" ]] && kill -0 "$AGENT_PID" 2>/dev/null; then
    kill "$AGENT_PID" 2>/dev/null || true
    wait "$AGENT_PID" 2>/dev/null || true
  fi
  if [[ "$MODEB_ON" == "1" ]]; then
    "$ROOT/scripts/host-resolved-modeb.sh" remove || true
    MODEB_ON=0
  fi
  if [[ "$MODE" == "1" ]]; then
    "$ROOT/scripts/host-cleanup.sh" || true
  fi
}
trap cleanup EXIT INT TERM

listen_port="${LISTEN##*:}"
if ss -uln | awk '{print $5}' | grep -qE "^(0\\.0\\.0\\.0|\\*|\\[::\\]):${listen_port}$"; then
  echo "UDP :${listen_port} already bound on wildcard (often Avahi on 5353). Use LISTEN=127.0.0.1:5553" >&2
  exit 1
fi

echo "== starting agent pins=$PINS default-path=$DP listen=$LISTEN =="
go run ./cmd/agent \
  -rules "$RULES" \
  -listen "$LISTEN" \
  -pins "$PINS" \
  -default-path "$DP" &
AGENT_PID=$!
sleep 2
if ! kill -0 "$AGENT_PID" 2>/dev/null; then
  echo "agent exited early" >&2
  exit 1
fi

if [[ "$MODE" == "1" ]]; then
  LISTEN_PORT="${LISTEN#*:}"
  "$ROOT/scripts/host-resolved-modeb.sh" install "$LISTEN_PORT"
  MODEB_ON=1

  echo "== check WARP still up after agent start =="
  if ! ip -br link show CloudflareWARP | grep -q UP; then
    echo "CloudflareWARP is not UP — aborting before browser test" >&2
    exit 1
  fi
  warp-cli status || true

  echo "== pre-warm Discord (pins via WARP) =="
  for name in discord.com discord.app www.discord.com gateway.discord.gg cdn.discordapp.com; do
    echo -n "  $name: "
    dig @"${LISTEN%:*}" -p "${LISTEN#*:}" +time=5 +tries=2 "$name" A +short | head -3 || echo "(timeout/fail)"
  done
  echo
  echo "Browser ready:"
  echo "  1) Disable DoH / secure DNS"
  echo "  2) Open https://discord.com and https://discord.app"
  echo "  3) resolvectl query discord.com"
  echo "  4) ip -4 route get <discord-ip>"
  echo
else
  echo
  echo "Agent running (pid $AGENT_PID). dig @${LISTEN%:*} -p ${LISTEN#*:} discord.com A"
  echo
fi

echo "Ctrl-C to stop (removes Mode B + owned rules)."
wait "$AGENT_PID" || true
