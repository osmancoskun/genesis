# ctl / config manual

Genesis is configured with one YAML file (agent + rules together).  
Interactive entry points: `go run ./cmd/ctl menu` or `go run ./cmd/ctl setup`.

Worked Discord + Ethernet example: [`configs/discord.config.yaml`](../configs/discord.config.yaml).  
Your live file is usually `configs/local.yaml` (gitignored) or `GENESIS_CONFIG`.

---

## Config file locations

| Priority | Path |
|----------|------|
| 1 | `-config` / `--config` on `ctl` or `agent` (sets `GENESIS_CONFIG`) |
| 2 | `$GENESIS_CONFIG` |
| 3 | `configs/local.yaml` |
| 4 | `~/.config/genesis/config.yaml` |

Listen override only: `$GENESIS_LISTEN` or `agent -listen`.

---

## Field reference

### `version`

Must be `1`.

### `agent`

| Field | Values | Meaning |
|-------|--------|---------|
| `listen` | `host:port` | DNS stub bind. Default `127.0.0.1:5553` (not 5353/mDNS). |
| `pins` | `auto` \| `dry-run` \| `netlink` \| `noop` | How matched answer IPs are pinned to a rule iface. |
| `default_path_mode` | `auto` \| `dry-run` \| `netlink` \| `off` | How `default_path.interface` is applied. |
| `dns_upstream` | IP / host | Default resolver for setup probes and new rules. |

**`pins` detail**

- `dry-run` — log planned `ip rule` / route ops; **no kernel change** (Discord will not actually use WARP).
- `netlink` — live pins; needs `CAP_NET_ADMIN` (menu **run** will `sudo` if needed).
- `auto` — netlink if privileged, else dry-run.
- `noop` — memory only, quiet.

**`default_path_mode` detail**

- `off` — do not install catch-all (KERNEL default-route unchanged by us).
- `dry-run` — print plan only.
- `netlink` — install catch-all in owned table **18990**, rule prio **5100**.
- `auto` — follows `pins` when resolving “live vs dry”.

Pins at prio **5000** win before default-path **5100**. Typical Cloudflare WARP catch-all is ~**5209**, so our default-path (if enabled) steers general IPv4 **before** WARP’s catch-all — that is intentional when you want “Internet via NIC, Discord via WARP”.

### `defaults`

Applied when a rule omits a field.

| Field | Meaning |
|-------|---------|
| `dns` | Upstream DNS for rules that omit `dns`. |
| `interface` | Default iface (`auto` = unbound / default-route path for DNS bind). |
| `pin_connections` | Default for installing pins on A answers. |
| `on_iface_down` | `fail_closed` (SERVFAIL, no fallback) or `fail_open`. |

### `default_path`

| Field | Meaning |
|-------|---------|
| `interface` | Real iface name for catch-all (e.g. `eno1`). Empty = disabled. **Not** `auto`. |
| `on_iface_down` | Same semantics as rules; default inherits `defaults`. |

Does **not** edit the main-table default route. KERNEL column in `ctl` lists = live system routing; CONFIG column = this YAML.

### `rules[]`

First match wins.

| Field | Meaning |
|-------|---------|
| `name` | Required id. |
| `match.domains` | Suffix match. `*.example.com` and `example.com` both match `a.example.com`. |
| `match.ips` | Exact IPv4 or CIDR destinations to pin without DNS. |
| `dns` | Upstream used when resolving via this rule (bound to `interface` when possible). |
| `interface` | Pin / DNS-bind iface (`CloudflareWARP`, `eno1`, …). |
| `pin_connections` | Override defaults. |
| `on_iface_down` | Override defaults. |
| `action` | Reserved (empty = forward). |

Cover every name Mode B sends to the agent. Missing match → `SERVFAIL` and log `no rule for …` (browser breaks on CDN/auth hosts).

---

## Menu (`go run ./cmd/ctl menu`)

Screen clears on each entry. After a submenu, press Enter to return.

| # | Action | What it does |
|---|--------|----------------|
| 1 | setup | Full wizard (see below); writes config. |
| 2 | run | Starts agent with found config; `sudo` if live netlink needed. |
| 3 | view config | Summary of listen / pins / default_path / rules. |
| 4 | list networks | Table: `#`, NAME, UP, KERNEL, CONFIG, IPv4, IPv6. |
| 5 | default-path | Set `default_path.interface` + `default_path_mode`; optional live apply. |
| 6 | doctor | Host readiness. |
| 7 | pin check | `CAP_NET_ADMIN` probe. |
| 0 | quit | Exit. |

**Menu 5 prompts**

1. Allowed interfaces (name or `#` from the table). Empty disables default-path.
2. `default_path_mode`: `1) auto` `2) dry-run` `3) netlink` `4) off` (name or `#`).
3. If mode is live-capable, may ask **Apply now?** → can `sudo` for password.

For “Internet via Ethernet, Discord via WARP” pick **iface = your LAN NIC** and **mode = netlink**, and keep Discord rules on the WARP iface with **`pins: netlink`**.

---

## Setup wizard (`go run ./cmd/ctl setup`)

Writes `configs/local.yaml` (or `--out` / `GENESIS_CONFIG`).

| Step | Prompt | Typical / allowed values |
|------|--------|---------------------------|
| 1 | (shows iface table) | Read-only snapshot. |
| 2 | Agent listen address | Default `127.0.0.1:5553`. |
| 3 | Default DNS upstream | Default `1.1.1.1`. |
| 4 | Default-path interface | Name or `#`, or empty to disable. Hint: current KERNEL default-route. |
| 5 | `default_path_mode` | Only if step 4 non-empty: `auto` \| `dry-run` \| `netlink` \| `off` (or `#`). |
| 6 | Pins mode | `auto` \| `dry-run` \| `netlink` \| `noop` (or `#`). |
| 7 | Domains | Comma-separated. Example Discord set in the prompt. Empty skips. |
| 8 | IPs | Optional IPv4/CIDR list. Empty skips. |
| 9 | Per domain/IP | Probe each up iface; pick iface by name or `#` (OK probes listed). |

Same-iface domain/IP picks are merged into fewer rules.

---

## Discord example checklist

1. **Point ctl/agent at the example** (pick one):
   ```bash
   # A) default path
   cp configs/discord.config.yaml configs/local.yaml
   go run ./cmd/ctl menu          # or: go run ./cmd/ctl run

   # B) explicit file (no copy)
   go run ./cmd/ctl -config configs/discord.config.yaml menu
   go run ./cmd/ctl -config configs/discord.config.yaml run
   go run ./cmd/agent -config configs/discord.config.yaml
   ```
   Edit `eno1` / `CloudflareWARP` if your interface names differ.  
   (`configs/local.yaml` is gitignored when you use option A.)
2. `./scripts/host-resolved-modeb.sh install 5553`
3. Browser: disable Secure DNS / DoH.
4. Start agent (menu **2) run** or `ctl run` / `agent -config`) — sudo when asked.
5. Confirm agent log: `pin install … via CloudflareWARP` for `discord.com` **and** CDN hosts (`cdn.discordapp.com`, `*.discord.gg`, …).
6. Tear down: Ctrl-C agent, `./scripts/host-cleanup.sh`.

---

## Non-interactive ctl

```text
go run ./cmd/ctl doctor
go run ./cmd/ctl ifaces
go run ./cmd/ctl config show
go run ./cmd/ctl rules [path]
go run ./cmd/ctl pin check
go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface eno1
go run ./cmd/ctl default-path check
go run ./cmd/ctl default-path dry-run --iface eno1
go run ./cmd/ctl default-path apply --iface eno1   # live; needs root
```
