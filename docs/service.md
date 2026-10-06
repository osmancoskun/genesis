# Running genesis as a background service

Goal: Tailscale-like UX — daemon always on, `ctl` for operator actions.

| Command | Effect |
|---------|--------|
| `ctl up` / `ctl start` | Start systemd unit `genesis.service` |
| `ctl down` / `ctl stop` | Stop unit (or SIGTERM via pidfile) |
| `ctl restart` | down + up |
| `ctl apply` / `ctl reload` | Hot-reload config (`systemctl reload` → **SIGHUP**) |
| `ctl service-status` | `systemctl status` or pidfile probe |

Without the unit installed, `apply` still works against a foreground `ctl run` / `agent` via the pidfile (`$XDG_RUNTIME_DIR/genesis.pid`).

## Hot reload (`apply`)

On **SIGHUP** the agent:

1. Re-reads the same config path
2. Swaps DNS rule pack in-process (new queries use new rules)
3. Re-applies `default_path` if the iface changed

Existing destination pins keep their TTL until expiry; new answers follow the reloaded rules.

## Install (system)

```bash
sudo install -d /etc/genesis
sudo cp configs/discord.config.yaml /etc/genesis/config.yaml
# edit CloudflareWARP / eno1

go build -o /tmp/genesis-agent ./cmd/agent
sudo install -m 755 /tmp/genesis-agent /usr/local/bin/genesis-agent
sudo cp deploy/systemd/genesis.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now genesis.service

./scripts/host-resolved-modeb.sh install 5553

go run ./cmd/ctl apply          # after editing /etc/genesis/config.yaml
go run ./cmd/ctl service-status
go run ./cmd/ctl down
```

Unit file: [`deploy/systemd/genesis.service`](../deploy/systemd/genesis.service).

## Design notes

- **Agent** never shells out to `systemctl` / `ip` (netlink + signals only).
- **ctl** may call `systemctl` / `sudo` as the operator front-end.
- Env: `GENESIS_CONFIG`, `GENESIS_LISTEN`.
