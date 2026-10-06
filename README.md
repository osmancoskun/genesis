# DNS Redirector (path director)

Self-hosted Linux **desktop/laptop** agent that steers DNS and connection traffic onto preferred interfaces (VPN, NICs) using domain / subdomain / IP rules.

Architecture: [`docs/dns-redirector-plan.md`](docs/dns-redirector-plan.md).  
Resolved coexistence: [`docs/resolved-coexistence.md`](docs/resolved-coexistence.md).  
Conventions: [`docs/engineering-conventions.md`](docs/engineering-conventions.md).

## Status

Phase 0→1 on this machine:

- `cmd/verify` — vet + staticcheck + nilaway
- `cmd/ctl doctor|rules|status|pin` — conflicts, rule listing, **safe pin dry-run / privilege check**
- `cmd/agent` — localhost DNS stub + iface-bound DNS hook + path pins (`-pins auto|dry-run|netlink|noop`)

MVP defaults: **fail closed**, **IPv4-first** pins, **DoH warn-only**, **Mode A** listen `127.0.0.1:5353` (does not take over systemd-resolved).

## Requirements

- Go 1.22+
- Linux
- Live policy routes: `CAP_NET_ADMIN` (or root). Without it, `-pins auto` falls back to **dry-run** (no kernel changes).

## Quick start (safe)

```bash
export PATH="$HOME/.local/go/bin:$PATH"   # if needed

go run ./cmd/ctl doctor
go run ./cmd/ctl pin check
go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface eno1

# Agent: Mode A stub + dry-run pins (safe on a daily driver)
go run ./cmd/agent -rules configs/example.rules.yaml -listen 127.0.0.1:5353 -pins dry-run

dig @127.0.0.1 -p 5353 corp.example A

make verify && make test
```

### Demo answer → pin (dry-run)

Full product loop without touching the kernel or VPN ifaces:

```bash
# 1) Unit demo: successful A → dry-run APPLY → refresh → REMOVE on TTL expiry
go test ./internal/dnsstub -run TestAnswerToPinDryRunLifecycle -v

# 2) Planned kernel ops only (no DNS):
go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface eno1

# 3) Live agent with -pins dry-run: matching A answers log
#    "pin install/refresh …" and print dry-run APPLY lines.
#    Named iface + SO_BINDTODEVICE needs CAP_NET_RAW; without it you get a clear error.
#    Do not point rules at VPN/Tailscale links you need.
go run ./cmd/agent -rules configs/demo-pin.rules.yaml -listen 127.0.0.1:5353 -pins dry-run
```

Live netlink pins (`-pins netlink`) only after `pin check` succeeds. Prefer TEST-NET destinations (`192.0.2.0/24`) when experimenting.

## Pin safety

- Owned policy tables **18000–18999** and rule priority **18000** only
- Never edits main-table default route; never toggles interfaces
- Missing caps → clear error + doctor/`pin check` hint (no panic, no host-network wipe)

## Layout

```
cmd/verify, cmd/ctl, cmd/agent
internal/rules, doctor, dnsstub, ifacedns, pathpin
configs/   docs/
```

## Conventions

- Conventional commits: `feat(scope): …` / `fix` / `chore` / `test`
- English-only code/comments; see `docs/engineering-conventions.md`
- `make verify` on Go changes
