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

Preferred — Makefile stage (default `PREFIX=/usr/local`):

```bash
make check-deps
make build
sudo make install ENABLE=1
# First-boot config is blank — configure via http://127.0.0.1:8787
# (Default path / Rules, or Import Discord example then fix iface names)

# Optional: send Discord* DNS to the agent (not done by make install)
./scripts/host-resolved-modeb.sh install 5553

genesis-ctl apply               # after editing /etc/genesis/config.yaml outside the UI
genesis-ctl service-status
genesis-ctl down
```

| Make target | Effect |
|-------------|--------|
| `sudo make install` | Binaries, unit, docs, share examples; blank `/etc/genesis/config.yaml` only if missing |
| `sudo make install ENABLE=1` | Same + `systemctl enable --now genesis.service` |
| `sudo make enable` | Enable/start only |
| `sudo make uninstall` | Stop/disable; remove binaries/unit/docs; keep `/etc/genesis` |
| `sudo make uninstall PURGE=1` | Also remove `/etc/genesis` |

RPM uses the same stage with `PREFIX=/usr` — see [`packaging/fedora/README.md`](../packaging/fedora/README.md).

Unit template: [`deploy/systemd/genesis.service.in`](../deploy/systemd/genesis.service.in) (`@PREFIX@` substituted at install).

## Design notes

- **Agent** never shells out to `systemctl` / `ip` (netlink + signals only).
- **ctl** may call `systemctl` / `sudo` as the operator front-end.
- Env: `GENESIS_CONFIG`, `GENESIS_LISTEN`.
