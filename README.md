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
go build -o genesis-agent ./cmd/agent
go build -o genesis-ctl ./cmd/ctl
# optional checks
make verify && make test
```

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

```bash
# Install config + binary + unit
sudo install -d /etc/genesis
sudo cp configs/discord.config.yaml /etc/genesis/config.yaml
# edit ifaces in /etc/genesis/config.yaml

go build -o /tmp/genesis-agent ./cmd/agent
sudo install -m 755 /tmp/genesis-agent /usr/local/bin/genesis-agent
sudo cp deploy/systemd/genesis.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now genesis.service

# Optional Mode B for Discord domains → :5553
./scripts/host-resolved-modeb.sh install 5553
```

Operator commands:

| Command | Meaning |
|---------|---------|
| `go run ./cmd/ctl up` | `systemctl start genesis` |
| `go run ./cmd/ctl down` | stop |
| `go run ./cmd/ctl restart` | restart |
| `go run ./cmd/ctl apply` | hot-reload config (**SIGHUP**) after editing YAML |
| `go run ./cmd/ctl service-status` | status |

After editing `/etc/genesis/config.yaml`:

```bash
sudoedit /etc/genesis/config.yaml
go run ./cmd/ctl apply
```

Details: [`docs/service.md`](docs/service.md). Unit: [`deploy/systemd/genesis.service`](deploy/systemd/genesis.service).

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
configs/          # genesis agent configs (discord.config.yaml; local.yaml gitignored)
examples/         # rules-only demos (docker, pin dry-run, example pack)
deploy/systemd/   # genesis.service
docs/
scripts/          # Mode B, host-try, cleanup
```
