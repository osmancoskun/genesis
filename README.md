# Genesis

Self-hosted Linux **desktop/laptop** agent that steers DNS and connection traffic onto preferred interfaces (VPN, NICs) using domain / subdomain / IP rules.

Architecture: [`docs/dns-redirector-plan.md`](docs/dns-redirector-plan.md).  
Resolved coexistence: [`docs/resolved-coexistence.md`](docs/resolved-coexistence.md).  
**ctl / config / setup manual:** [`docs/ctl-manual.md`](docs/ctl-manual.md).  
**Background service (up/down/apply):** [`docs/service.md`](docs/service.md).  
Conventions: [`docs/engineering-conventions.md`](docs/engineering-conventions.md).

## Status

Phase 0→1 on this machine:

- `cmd/verify` — vet + staticcheck + nilaway
- `cmd/ctl` — interactive **menu** / **setup** wizard, doctor, rules, status, pin / default-path dry-run
- `cmd/agent` — localhost DNS stub + iface-bound DNS + path pins (`-config` or `-rules`)

MVP defaults: **fail closed**, **IPv4-first** pins, **DoH warn-only**, **Mode A** listen `127.0.0.1:5553` (avoids mDNS/Avahi on 5353; does not take over systemd-resolved).

Example Discord + Ethernet split: [`configs/discord.config.yaml`](configs/discord.config.yaml).

**To run the Discord example** you can either copy to the default path or pass `-config`:

```bash
# Option A — copy to default local.yaml
cp configs/discord.config.yaml configs/local.yaml
# edit eno1 / CloudflareWARP if needed
./scripts/host-resolved-modeb.sh install 5553
go run ./cmd/ctl menu   # → 2) run

# Option B — start with the example file directly (no copy)
./scripts/host-resolved-modeb.sh install 5553
go run ./cmd/ctl -config configs/discord.config.yaml run
# or: go run ./cmd/agent -config configs/discord.config.yaml
```

`configs/local.yaml` is gitignored (your machine-local active config).

## Requirements

### Runtime (agent / ctl)

| Need | Why |
|------|-----|
| **Linux** (amd64/arm64) | Policy routing via netlink |
| **Go 1.22+** | Build `cmd/agent`, `cmd/ctl`, `cmd/verify` |
| **`CAP_NET_ADMIN`** (or root) | Live `pins=netlink` / `default_path_mode=netlink` |
| **`CAP_NET_RAW`** (or root) | `SO_BINDTODEVICE` when DNS is bound to a named iface |

The agent talks to the kernel with **netlink** and reads `/proc` — it does **not** require `ip`, `ss`, or `netstat` on the PATH.

### Host helpers & debugging (recommended)

| Tool | Package (Fedora) | Used for |
|------|------------------|----------|
| `dig` | `bind-utils` | Probe agent / Mode B (`dig @127.0.0.1 -p 5553 …`) |
| `ip` | `iproute` | Inspect rules/routes (`ip rule`, `ip route get`) — scripts + ops |
| `ss` | `iproute` | See who owns UDP ports (e.g. `:5553`) |
| `resolvectl` | `systemd` | Mode B / split-DNS checks |
| `systemctl` | `systemd` | Mode B reload; **service** `ctl up/down/apply` |
| `sudo` | `sudo` | Elevate for netlink / systemd unit |

Optional for the Discord/WARP workflow: **`warp-cli`** (Cloudflare WARP), iface name like `CloudflareWARP`.

`netstat` is **not** required (`ss` replaces it). There is no `ns` tool dependency.

### Background service

Tailscale-like daemon UX: [`docs/service.md`](docs/service.md) — `ctl up` / `down` / `apply` (hot reload via SIGHUP), unit in `deploy/systemd/genesis.service`.

## Quick start (safe)

```bash
export PATH="$HOME/.local/go/bin:$PATH"   # if needed

# Interactive: setup wizard → menu (setup / run / view config / list nets / default-path)
go run ./cmd/ctl setup
go run ./cmd/ctl menu

# Or start from the Discord example
cp configs/discord.config.yaml configs/local.yaml   # edit iface names
./scripts/host-resolved-modeb.sh install 5553
go run ./cmd/ctl menu    # → 2) run

# Same without copying:
go run ./cmd/ctl -config configs/discord.config.yaml run
go run ./cmd/agent -config configs/discord.config.yaml

# Or non-interactive checks
go run ./cmd/ctl doctor
go run ./cmd/ctl ifaces
go run ./cmd/ctl config show
go run ./cmd/ctl pin check
go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface eno1
go run ./cmd/ctl default-path dry-run --iface wg0

# Agent loads configs/local.yaml (from setup) or GENESIS_CONFIG
go run ./cmd/agent -config configs/local.yaml

dig @127.0.0.1 -p 5553 example.com A

make verify && make test
```

Env overrides: `GENESIS_CONFIG`, `GENESIS_LISTEN`.

### Docker demos (host routing untouched)

```bash
make demo-docker        # two upstream DNS + agent steer
make demo-docker-split  # lan vs warp-like path; discord.test bypass (no --network=host)
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
go run ./cmd/agent -rules configs/demo-pin.rules.yaml -listen 127.0.0.1:5553 -pins dry-run
```

Live netlink pins (`-pins netlink`) only after `pin check` succeeds. Prefer TEST-NET destinations (`192.0.2.0/24`) when experimenting.

## Pin safety

- Owned policy tables **18000–18999** and pin rule priority **5000** (default-path **5100**; below WARP ~5209)
- Never edits main-table default route; never toggles interfaces
- Missing caps → clear error + doctor/`pin check` hint (no panic, no host-network wipe)

## Layout

```
cmd/verify, cmd/ctl, cmd/agent
internal/appconfig, rules, doctor, dnsstub, ifacedns, netinfo, pathpin
configs/          # example + host rules; local.yaml from setup (gitignored)
docs/
```

## Conventions

- Conventional commits: `feat(scope): …` / `fix` / `chore` / `test`
- English-only code/comments; see `docs/engineering-conventions.md`
- `make verify` on Go changes
