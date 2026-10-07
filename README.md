# Genesis

Self-hosted Linux agent that steers DNS and connection traffic onto preferred interfaces (VPN, NICs) using domain / subdomain / IP rules.

Docs: [architecture](docs/dns-redirector-plan.md) · [ctl manual](docs/ctl-manual.md) · [service](docs/service.md) · [resolved coexistence](docs/resolved-coexistence.md) · [conventions](docs/engineering-conventions.md)

MVP defaults: **fail closed**, IPv4-first pins, DoH warn-only, DNS UDP `127.0.0.1:5553`, Web UI TCP `http://127.0.0.1:8787` (localhost only).

---

## Fedora: install requirements

```bash
# Build tools + Go
sudo dnf install -y golang git make

# Ops / debug helpers (agent itself uses netlink, not these CLIs)
sudo dnf install -y bind-utils iproute systemd sudo

# Optional — Discord-via-WARP workflow
# Install Cloudflare WARP for Linux, then:
#   warp-cli registration new   # if needed
#   warp-cli connect
```

| Tool | Fedora package | Used for |
|------|----------------|----------|
| `go` | `golang` | Build `cmd/agent`, `cmd/ctl` |
| `dig` | `bind-utils` | Test DNS (`dig @127.0.0.1 -p 5553 …`) |
| `ip` / `ss` | `iproute` | Inspect routes / listening ports |
| `resolvectl` / `systemctl` | `systemd` | Mode B + genesis service |
| `sudo` | `sudo` | Live netlink / install unit |
| `warp-cli` | Cloudflare WARP | Optional VPN iface `CloudflareWARP` |

Live pins need **`CAP_NET_ADMIN`** (and often **`CAP_NET_RAW`** for bind-to-device). Without them, use `pins: dry-run` or let `ctl` elevate with `sudo`.

---

## Build

```bash
git clone git@github.com:osmancoskun/genesis.git
cd genesis
make check-deps
make build                    # → bin/genesis-agent, bin/genesis-ctl
# optional checks
make verify && make test
```

Or: `go build -o bin/genesis-agent ./cmd/agent` and `go build -o bin/genesis-ctl ./cmd/ctl`.

---

## Install (system, Makefile)

Developer default installs under `/usr/local` and stages the same layout RPM uses (`DESTDIR` + `PREFIX`).

```bash
make check-deps
make build
sudo make install ENABLE=1    # binaries, unit, blank /etc/genesis/config.yaml if missing
# open http://127.0.0.1:8787 — set default path / rules (or Import Discord example)
# optional Mode B (resolved Domains → :5553) — not part of make install:
./scripts/host-resolved-modeb.sh install 5553
```

| Target | Meaning |
|--------|---------|
| `make check-deps` | Require `go` (≥ `go.mod`), `install` |
| `make build` | `CGO_ENABLED=0` binaries in `bin/` |
| `sudo make install` | Install; does **not** start the unit |
| `sudo make install ENABLE=1` | Install + `systemctl enable --now` |
| `sudo make enable` | Enable/start after install |
| `sudo make uninstall` | Remove binaries/unit/docs; keep `/etc/genesis` |
| `sudo make uninstall PURGE=1` | Also remove `/etc/genesis` |
| `make rpm` | Fedora RPM via [`packaging/fedora/`](packaging/fedora/) |

Unit template: [`deploy/systemd/genesis.service.in`](deploy/systemd/genesis.service.in) (`@PREFIX@` → `/usr/local` or `/usr`).

---

## Basic use (foreground)

```bash
# 1) Config — copy example or run setup
cp configs/discord.config.yaml configs/local.yaml
# edit eno1 / CloudflareWARP to match: go run ./cmd/ctl ifaces
# or: go run ./cmd/ctl setup

# 2) (Discord in browser) send Discord* DNS to the agent
./scripts/host-resolved-modeb.sh install 5553
# Turn OFF browser Secure DNS / DoH

# 3) Run agent (sudo if pins/default_path are netlink)
go run ./cmd/ctl -config configs/local.yaml run
# or: ./genesis-ctl -config configs/local.yaml run
# or: sudo ./genesis-agent -config configs/local.yaml

# 4) Check
dig @127.0.0.1 -p 5553 discord.com A
resolvectl query discord.com
# Web UI (same agent process, TCP — not the DNS UDP port):
#   open http://127.0.0.1:8787
```

Interactive menu: `go run ./cmd/ctl menu` (setup, run, view config, ifaces, default-path, service).

Without copying the example:

```bash
go run ./cmd/ctl -config configs/discord.config.yaml run
```

Env: `GENESIS_CONFIG`, `GENESIS_LISTEN`, `GENESIS_UI` (Web UI bind; `off` disables).

### Local Web UI

When the agent runs, it also serves a simple localhost UI (TCP, default `127.0.0.1:8787`) for iface overview, a small traffic-path animation (apps → CONFIG/KERNEL iface), and default-path edits. DNS stays on UDP `:5553` — different protocol, separate listener.

```bash
go run ./cmd/agent -config configs/local.yaml
# browser → http://127.0.0.1:8787
```

Stop foreground agent with **Ctrl-C**. Cleanup policy rules / Mode B: `./scripts/host-cleanup.sh`.

---

## Run as a systemd service

Like Tailscale: daemon in the background, `ctl` for up / down / apply.

Preferred:

```bash
sudo make install ENABLE=1
# Web UI: http://127.0.0.1:8787  (first-boot blank config; no hard-coded ifaces)
./scripts/host-resolved-modeb.sh install 5553   # optional
genesis-ctl apply                               # after YAML edits outside the UI
```

Operator commands:

| Command | Meaning |
|---------|---------|
| `genesis-ctl up` | `systemctl start genesis` |
| `genesis-ctl down` | stop |
| `genesis-ctl restart` | restart |
| `genesis-ctl apply` | hot-reload config (**SIGHUP**) after editing YAML |
| `genesis-ctl service-status` | status |

(`go run ./cmd/ctl …` works the same from a git checkout.)

Details: [`docs/service.md`](docs/service.md). Fedora RPM: [`packaging/fedora/README.md`](packaging/fedora/README.md).

---

## Discord example (checklist)

1. Config: `cp configs/discord.config.yaml configs/local.yaml` **or** `-config configs/discord.config.yaml`
2. Edit `eno1` / `CloudflareWARP` if needed (`ctl ifaces`)
3. `pins: netlink`, `default_path_mode: netlink`, `default_path.interface: eno1`
4. Mode B + disable browser DoH
5. `ctl run` or systemd `ctl up`
6. Agent log should show `pin install … via CloudflareWARP` for `discord.com` **and** CDN hosts (`*.discordapp.com`, `*.discord.gg`, …)

Full field / setup reference: [`docs/ctl-manual.md`](docs/ctl-manual.md).

---

## Safety

- Owned tables **18000–18999**; pin prio **5000**; default-path **5100** (below typical WARP ~5209)
- Never edits main-table default; never toggles links
- Prefer `ctl pin dry-run` / `default-path dry-run` before live netlink

---

## Docker demos (host routing untouched)

```bash
make demo-docker
make demo-docker-split
```

---

## Layout

```
cmd/agent cmd/ctl cmd/verify
internal/appconfig rules doctor dnsstub ifacedns netinfo pathpin
configs/          # default.config.yaml (first-boot), discord example; local.yaml gitignored
examples/         # rules-only demos (docker, pin dry-run, example pack)
deploy/systemd/   # genesis.service.in (+ generated genesis.service)
packaging/fedora/ # RPM spec (same make install stage)
docs/
scripts/          # check-deps, Mode B, host-try, cleanup
```
