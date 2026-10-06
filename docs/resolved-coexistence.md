# systemd-resolved coexistence

**Default is non-destructive.** The agent does **not** take over port 53 or rewrite `/etc/resolv.conf` unless you opt in.

## Mode A — Side-by-side stub (default / recommended for MVP)

| Piece | Value |
|---|---|
| Agent listen | `127.0.0.1:5353` (or another high port) |
| systemd-resolved | Left alone (`127.0.0.53:53` stub stays) |
| Apps | Unchanged system DNS; you test with `dig @127.0.0.1 -p 5353 …` |
| Pins | Independent of who answers DNS — policy routes still apply once answers are pinned |

Start:

```bash
go run ./cmd/agent -rules examples/example.rules.yaml -listen 127.0.0.1:5353 -pins dry-run
```

Use this mode on a daily driver laptop. It never fights resolved for `:53`.

## Mode B — resolved forwards selected domains (optional, still soft)

Keep resolved as the system stub. Point **routing domains** at the agent as an upstream (example — adapt; do not apply blindly on a production machine without review):

```ini
# e.g. /etc/systemd/resolved.conf.d/genesis.conf  (OPTIONAL — not installed by us)
[Resolve]
DNS=127.0.0.1:5353
Domains=~corp.example
```

Then `systemctl reload systemd-resolved`. Only names under those domains should be asked of the agent; everything else stays on resolved’s uplink DNS.

**Caveat:** resolved still owns the client API (`127.0.0.53`). Bind-to-iface for *queries* is only as good as the agent’s upstream exchange for domains it actually receives.

## Mode C — Full handoff (destructive, opt-in later)

Only when you intentionally want the agent as *the* system stub:

1. Stop using resolved’s stub listener / point `/etc/resolv.conf` at `127.0.0.1`.
2. Run the agent on `127.0.0.1:53` with `CAP_NET_BIND_SERVICE` (or root).
3. Document rollback before changing anything.

This repo’s MVP defaults **do not** automate Mode C.

## Doctor

`go run ./cmd/ctl doctor` reports resolved ownership as a **conflict for Mode C**, and prints an **info** tip pointing at Mode A/B. Prefer Mode A until pins and rules are trusted.

## DoH

Browsers using DoH still bypass all modes. MVP stance: **warn-only** (doctor prints this).
