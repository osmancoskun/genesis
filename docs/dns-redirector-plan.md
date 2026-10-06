# DNS Redirector — Architecture Plan

**Audience:** Osman (product owner)  
**Goal:** A self-hosted Linux **desktop/laptop** client (+ optional later server) that steers DNS queries *and* subsequent connection traffic onto preferred interfaces (VPN, physical NICs, etc.) based on domain / subdomain / IP rules.

### Locked decisions (2026-10-06)

| Decision | Choice |
|---|---|
| Connection pinning (policy routing for answered IPs) | **Required in MVP** |
| Primary target | **Desktop / laptop** (not LAN gateway) |
| Language | **Go** |
| Rule sync server | Local rule files first; server later |
| VPN / iface down | **Fail closed** (per-rule override later) |
| IPv6 | **v4-first MVP** (AAAA pinning can follow) |
| DoH | **Warn-only** in MVP |
| Engineering conventions | See [engineering-conventions.md](./engineering-conventions.md) |
| Go safety gate | **`cmd/verify`** (vet + staticcheck + nilaway) before merge-worthy work |

---

## 1. Problem summary

On a typical desktop/laptop Linux stack you get three overlapping owners of DNS and routing:

| Layer | What it owns | Failure mode with VPN / multi-NIC |
|---|---|---|
| **systemd-resolved** | Stub resolver, per-link DNS, Domains=, DNS= | Link DNS “wins” unpredictably; VPN push often overrides split DNS; `resolv.conf` points at stub (127.0.0.53) and hides interface binding |
| **NetworkManager** | Interface lifecycle, dispatcher scripts, sometimes DNS | Race with resolved; VPN plugins rewrite DNS/routes; dispatcher is ad-hoc glue, not a product |
| **Kernel FIB + policy routing** | Where packets actually go | DNS returning the “right” IP does **nothing** if the default route is the wrong iface |

User intent (examples):

- `q.com` → resolve via DNS `1.2.3.4`, **and** use interface `enp2` for that DNS *and* for connections to the answered IPs  
- `w.com` → resolve via DNS `1.1.2.2` via `vpn0`, same for connections  
- Rules may match domains, subdomains, or literal IPs

**Core insight:** “DNS redirector” is an incomplete name for the product.  
**DNS-only** fixes *which resolver answers*. It does **not** fix *which interface carries TCP/UDP* after A/AAAA are known. A useful product must couple:

1. **Split / steered DNS** (query path), and  
2. **Answer-driven policy routing** (connection path), optionally plus static IP→iface rules.

Network namespaces can hard-isolate stacks, but they are a *deployment mode*, not the default UX for every desktop app.

---

## 2. Recommended approach

### Recommendation: **Local steering agent + optional rule server**

Ship a privileged Linux **client agent** that:

1. Owns the system stub DNS (or sits in front of it).  
2. Evaluates a rule table: domain / subdomain / IP → `{dns_server, interface, optional mark/table}`.  
3. Sends DNS queries **bound to that interface** (or via a per-iface routing table).  
4. On each answer (and for static IP rules), installs **ephemeral policy routes / nftables marks** so connection traffic to those destinations uses the same interface.  
5. Syncs rule packs from an optional **self-hosted server** (Git-like config, API, or pull).

Call the product **genesis** in docs and CLI; keep “DNS redirector” / “path director” only as historical labels in older notes.

### Why this, not the common alternatives

| Approach | What it solves | Why it is not enough alone |
|---|---|---|
| **systemd-resolved split DNS** (`Domains=`, `DNS=`, `RoutingDomain=`) | Per-link DNS for *queries* | No durable “connection must leave iface X”; fragile under NM+VPN; no central rule UX; DoH bypass untouched |
| **NM dispatcher scripts** | Glue after VPN up/down | Not versioned product logic; races; hard to test; no answer→route lifecycle |
| **dnsmasq / unbound** | Excellent split DNS / forwarding | Bind-to-iface is awkward; no automatic policy routing for answered IPs |
| **nftables / ip rule alone** | Can force dest→iface once you know IPs | Does not map *names* → IPs; rule explosion without DNS integration |
| **Full network namespaces** | Strong isolation (DNS + routes + sockets) | Heavy UX: apps must run *in* the ns; breaks desktop defaults; good as **opt-in mode**, not MVP default |
| **SOCKS / transparent proxy** | App-level path control | Incomplete (UDP, non-proxied apps); different threat model; not “use my NIC/VPN iface” |
| **cgroup / eBPF socket routing** | Pin *processes* to paths | Orthogonal: great for “this app via VPN”; weaker for “this *domain* via enp2” unless combined with DNS |

**Verdict:** Build DNS steering + answer-driven policy routing as the default path. Treat netns and cgroup routing as later *modes* for hard isolation / per-app cases.

### What DNS-only solves vs what needs routing

| User need | DNS steering enough? | Also need |
|---|---|---|
| Use a specific resolver for `corp.example` | Yes | — |
| DNS query packets leave via `vpn0` | Partially (bind/`SO_BINDTODEVICE` / per-table route) | Interface must be up and routeable to that DNS |
| Browser/SSH to answered IPs leave via `vpn0` | **No** | Policy routing / fwmark / route table per iface |
| Known IP `10.0.0.5` always via `enp2` | N/A (no DNS) | Static dest→iface policy route |
| “All Firefox via VPN” | No | cgroup / netns / proxy |

Be explicit in the product: **Name rules without connection pinning are half a fix.** MVP must include pinning for answered A/AAAA (with TTL-aware expiry).

---

## 3. High-level architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Optional server (self-hosted)                                  │
│  - Auth'd rule packs, versioning, audit                         │
│  - Push/pull to clients (HTTPS API or signed bundle)            │
└────────────────────────────┬────────────────────────────────────┘
                             │ sync
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  Client agent (root / CAP_NET_ADMIN + CAP_NET_BIND_SERVICE)     │
│                                                                 │
│  ┌──────────────┐   ┌──────────────┐   ┌─────────────────────┐  │
│  │ Rule engine  │──▶│ DNS stub     │──▶│ Upstream DNS via    │  │
│  │ match domain │   │ :53 / stub   │   │ bound iface / table │  │
│  │ / IP / CIDR  │   │ cache        │   └─────────────────────┘  │
│  └──────┬───────┘   └──────┬───────┘                            │
│         │                  │ answers (A/AAAA + TTL)             │
│         │                  ▼                                    │
│         │           ┌──────────────┐                            │
│         └──────────▶│ Path pin     │──▶ nftables mark +         │
│                     │ manager      │    ip rule / route table   │
│                     │ (TTL expiry) │    per destination         │
│                     └──────────────┘                            │
│                                                                 │
│  Hooks: NM / systemd-networkd / VPN up-down → refresh ifaces    │
└─────────────────────────────────────────────────────────────────┘
          ▲
          │ point system DNS here (resolved stub drop-in,
          │ or replace stub; or iptables REDIRECT of :53)
          │
   Apps / glibc / browsers (system DNS only)
```

### Client role

- Load local + synced rules.  
- Be the DNS stub (or the only forwarder resolved uses).  
- Bind DNS egress to the rule’s interface.  
- Install/refresh/expire path pins for answered IPs and static IP rules.  
- Expose status: active pins, last match, iface health, conflicts with VPN.  
- Survive VPN flap: re-apply pins when iface returns; quarantine rules whose iface is down.

### Server role (phase 2+)

- Store and version **rule packs** (org / personal).  
- Distribute signed configs to clients.  
- Optional: telemetry of match counts (privacy-sensitive — default off).  
- **Not** a public recursive DNS for the client’s queries (unless Osman wants a hosted resolver later — out of MVP).

### Data flow (happy path)

1. App queries `w.example` → stub.  
2. Rule matches `*.example` → DNS `1.1.2.2`, iface `vpn0`.  
3. Agent sends query via `vpn0` to `1.1.2.2`.  
4. Answer `203.0.113.9` TTL 300 → cache + **pin** `203.0.113.9/32 → table vpn0` (or fwmark) for ≥ TTL (plus grace).  
5. App connects to `203.0.113.9` → kernel policy routing selects `vpn0`.  
6. TTL expires → pin removed unless refreshed by another query.

---

## 4. Rule model

Declarative, ordered, first-match-wins (with explicit priority).

```yaml
# Conceptual schema — not final file format
version: 1
defaults:
  dns: 9.9.9.9
  interface: auto   # default route iface
  pin_connections: true

rules:
  - name: corp-dns
    match:
      domains: ["corp.example", "*.corp.example"]
    dns: 10.0.0.53
    interface: vpn0
    pin_connections: true

  - name: lab-via-nic
    match:
      domains: ["q.com", "*.q.com"]
    dns: 1.2.3.4
    interface: enp2
    pin_connections: true

  - name: static-printer
    match:
      ips: ["192.168.10.20/32"]
    interface: enp2
    # no dns field — IP-only pin

  - name: block-or-sinkhole   # later
    match:
      domains: ["ads.tracker.example"]
    action: nxdomain
```

### Match types (MVP → v1)

| Match | MVP | Notes |
|---|---|---|
| Exact domain | Yes | `q.com` |
| Subdomain / suffix | Yes | `*.q.com` or suffix `q.com` (pick one semantics; prefer **suffix** with clear docs) |
| Literal IP / CIDR | Yes | Connection pin only |
| Port / proto | No (v1+) | Needs more than DNS |
| Process / cgroup | Later | Separate mode |
| DoH hostname allowlist | Later | For interception |

### Interface binding semantics

- Prefer **interface name** (`enp2`, `vpn0`) with resolution to ifindex at apply time.  
- Fallback: “routing table N dedicated to that iface” (common with WireGuard / NM).  
- If iface down: see **§ VPN interface down (explained)** below — behavior is per-rule / configurable.

### Pin implementation sketch

- Maintain route table per managed iface in owned range **18000–18999** (never main table).  
- `ip rule` `to <dst>/32` lookup that table at priority **5000** (below typical WARP ~5209).  
- Prefer **nftables** marks later for scale; MVP uses `to <ip>` rules.  
- Backends: `-pins dry-run` (default safe path without caps), `-pins netlink` when `CAP_NET_ADMIN` present, `-pins auto` picks netlink or dry-run.  
- **Default path** (optional `default_path.interface` in rules): installs `0.0.0.0/0` in owned table **18990** with catch-all rule priority **5100** (after per-dst pins at **5000**). Connected IPv4 prefixes in main get preserve rules at **5050** (`lookup main`). Priorities stay below typical Cloudflare WARP catch-all (~5209). Never edits the main-table default route; iface down → refuse (fail closed). CLI: `ctl default-path dry-run|check`.  
- Track pins in agent memory; expiry = max(DNS TTL, floor).  
- See `internal/pathpin` and `docs/resolved-coexistence.md`.

### systemd-resolved

Default **Mode A**: agent on `127.0.0.1:5353`, leave resolved alone. Optional Mode B (Domains= upstream) and destructive Mode C (:53 handoff) are documented only — not automated.

---

## 5. Implementation sketch

### Language pick: **Go** for MVP

| Criterion | Go | Rust |
|---|---|---|
| netlink / route / link APIs | Mature (`vishvananda/netlink`, etc.) | Good but more ceremony |
| DNS server/stub | Easy (`miekg/dns`) | Good crates exist |
| nftables | CLI wrap or `google/nftables` | Feasible |
| Privileged daemon + packaging | Fast path to systemd unit | Slower MVP |
| Memory safety at CAP_NET_ADMIN boundary | Weaker | Stronger |

**Opinion:** Go for client agent MVP (speed of iteration, DNS + netlink ecosystem). Revisit Rust for a later **privilege-separated** helper if the attack surface (parsing DNS from untrusted nets) becomes a concern — or isolate that in a small sandbox from day one.

### Core packages / OS integration

- systemd unit: `dnsredirector.service` after `network-online.target`; conflict carefully with `systemd-resolved`.  
- Preferred integration: **resolved stub listener disabled / Domains emptied**, agent listens on `127.0.0.1:53` (and optionally `::1`), `/etc/resolv.conf` → `nameserver 127.0.0.1`. Alternative: become resolved’s only upstream via drop-in (weaker control over bind-to-iface).  
- NM: subscribe to device events (D-Bus) to remap interface names ↔ ifindex.  
- CLI: `dnsredirector status|rules|pin list|doctor`.

### Repo layout (idiomatic Go — grow as needed)

Start small; only add packages when code exists:

```
cmd/verify/         # static verifier (vet + staticcheck + nilaway) — exists first
cmd/agent/          # client daemon (when Phase 0/1 starts)
cmd/ctl/            # CLI (when needed)
internal/...        # libraries by responsibility (rules, dns, pathpin, …)
```

Do not pre-create empty trees. Prefer standard Go module layout over custom nesting.

### Go verifier

`cmd/verify` is the project safety gate for Go code. It runs:

1. `go vet` — standard suspicious constructs  
2. **Staticcheck** — bugs, dead code, API misuse  
3. **Uber NilAway** — nil pointer / nil-flow issues  

Use `go run ./cmd/verify` or `make verify` before calling a change done.

---

## 6. MVP phases

### Phase 0 — Tooling + doctor (thin slice)

- Go module + **`cmd/verify`** safety gate (done as first code).  
- Detect resolved / NM / VPN DNS owners.  
- Print “who will fight us” report.  
- No steers yet. Builds trust and install path.

### Phase 1 — MVP (ship-worthy)

**In:**

- Local YAML/JSON rules: domain suffix + IP/CIDR → DNS + iface.  
- Stub DNS on localhost; forward bound to iface.  
- Answer-driven path pins with TTL expiry.  
- Static IP pins.  
- CLI status + doctor.  
- systemd install notes; coexistence mode documented.  
- IPv4 first (pin A answers); AAAA pinning deferred past MVP unless explicitly added.
- DoH: detect/warn only (no active blocking in MVP).

**Out:**

- Server sync, GUI, DoH interception/blocking, netns mode, Docker/K8s CNI, captive portal helper, Windows/macOS.

### Phase 2 — v1 operations

- Self-hosted rule server (auth, signed packs, pull).  
- Better VPN flap handling + NM integration.  
- Metrics / structured logs.  
- Optional: redirect UDP/TCP 53 from LAN (gateway mode) for “house DNS director”.  
- Explicit IPv6 policy (see risks).

### Phase 3 — hard modes

- Opt-in **netns** profiles (“run this command in vpn-ns”).  
- cgroup / eBPF “this app via iface”.  
- DoH/DoT detection warnings; optional transparent block of known DoH IPs (controversial — document ethics/ops).  
- Docker/K8s sidecar or node agent notes (likely separate product surface).

---

## 7. Risks and edge cases

| Risk | Impact | Mitigation |
|---|---|---|
| **DoH / DoT / hard-coded DNS** (Chrome, Firefox, apps) | Bypass stub entirely | Document; browser policy; optional block of public DoH endpoints (ops choice); cannot “fully” win |
| **Apps ignoring system DNS** | Same | App-specific docs; netns mode later |
| **IPv6** | Happy Eyeballs may prefer AAAA over wrong path | Pin AAAA too; or disable IPv6 on managed ifaces; explicit policy |
| **Captive portals** | Split DNS breaks portal detection | Bypass list / “passthrough while captive” detector |
| **VPN kill-switch conflict** | Double marks / blackhole | Doctor detects nft/wg kill-switch; document precedence |
| **TTL / CDN / anycast churn** | Stale pins / pin storm | Cap pin count; coalesce by prefix; short TTL floor/ceiling |
| **Shared CDN IPs** | `a.com` and `b.com` same IP, different ifaces | Last-match-wins warning; prefer longer-lived / higher-priority rule; document ambiguity |
| **Docker / K8s** | Separate netns, own resolv.conf | Out of MVP; later: container DNS + host pins don’t apply inside |
| **Privilege** | Root daemon parses network input | Drop caps after bind; separate unprivileged DNS parser if needed |
| **resolved/NM races** | Flapping DNS owner | Phase 0 doctor; take exclusive ownership model |
| **Asymmetric routing / RP filter** | Reply path issues | `rp_filter` guidance; policy routing for both directions where needed |

---

## 8. VPN interface down (explained)

This is **not** about the VPN app crashing the laptop. It is: *a rule names an interface (e.g. `vpn0`), and that interface is currently missing or down* — typical when you disconnect WireGuard/OpenVPN or the tunnel flaps.

Example rule: `w.com → DNS 1.1.2.2 via vpn0`, plus connection pins on answered IPs via `vpn0`.

While `vpn0` is up: queries and connections for `w.com` leave through the VPN. Good.

When you disconnect VPN, `vpn0` disappears. The agent must choose one of two policies:

| Mode | What happens for that rule | Why you’d want it |
|---|---|---|
| **Fail closed** (strict) | DNS for matching names returns failure (e.g. SERVFAIL); no pin via Wi‑Fi/Ethernet. Site may not load. | Stops accidental **leak**: corp/VPN-only traffic must not fall back to the public NIC. |
| **Fail open** (lenient) | Fall back to default DNS + default route (e.g. `wlan0`). Site still works offline-VPN. | Convenience on a laptop when VPN is often toggled. |

**Both are valid.** This is a product preference, often **per-rule** (mark corp rules `on_iface_down: fail_closed`, personal rules `fail_open`).

**MVP default (locked 2026-10-06):** **fail closed**. Per-rule `on_iface_down` override can follow; until then a down/missing iface → SERVFAIL (no public-NIC fallback) for that rule.

---

## 9. Remaining open questions

**Answered (locked):**

| Topic | Choice |
|---|---|
| Connection pinning | Required in MVP |
| Primary target | Desktop / laptop |
| Language | Go |
| Rule sync | Local files first; server later |
| VPN/iface down | **Fail closed** (per-rule override later) |
| IPv6 | **v4-first MVP** (AAAA pinning can follow) |
| DoH | **Warn-only** in MVP |
| Engineering + verifier | See conventions; `make verify` |

Still open / later:

1. **Netns hard mode:** Phase 3 only, or needed earlier?
2. Exact production packaging (systemd unit details, resolved coexistence playbook polish).

---

## 10. Opinionated bottom line

Build a **Linux desktop path-steering agent**: stub DNS + bind-to-iface forwarding + TTL-scoped policy routes for answered IPs. Pinning is mandatory. Use **Go**, idiomatic layout, English-only code/comments, conventional commits (`feat(scope): …`), and **`cmd/verify`** as the static safety gate. Ship verifier + doctor, then local agent, before any control-plane server. Be honest that **apps that skip system DNS will bypass you** until interception or isolation modes exist.
