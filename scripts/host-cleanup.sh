#!/usr/bin/env bash
# Remove genesis owned policy rules/routes and Mode B resolved drop-in.
# Fast, non-reentrant, ignores Ctrl-C while running.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [[ "$(id -u)" -ne 0 ]]; then
  exec sudo -E "$0" "$@"
fi

trap '' INT TERM

echo "Removing Mode B resolved drop-in (if any)…"
"$ROOT/scripts/host-resolved-modeb.sh" remove || true

echo "Deleting owned rule priorities 5000/5050/5100…"
for p in 5000 5050 5100; do
  # delete all duplicates at this priority
  for _ in $(seq 1 32); do
    ip -4 rule del priority "$p" 2>/dev/null || break
  done
done

echo "Flushing default-path table 18990 + any 18xxx tables still referenced…"
ip -4 route flush table 18990 2>/dev/null || true
# Only flush pin tables that still appear in rules (avoid 1000× flush).
while read -r t; do
  case "$t" in
    18[0-9][0-9][0-9]) ip -4 route flush table "$t" 2>/dev/null || true ;;
  esac
done < <(ip -4 rule list 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="lookup") print $(i+1)}' | sort -u)

echo "Done. Current default:"
ip -4 route show default || true
echo "Remaining rules (head):"
ip -4 rule list | head -15
