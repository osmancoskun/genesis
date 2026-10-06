# Engineering conventions

Project rules from Osman. Agents and humans follow these when changing the repo.

## Branches

- Use conventional prefixes: `feature/`, `fix/`, `chore/`, `refactor/`, `test/`, `docs/`.
- Examples: `feature/path-director-mvp`, `fix/dns-bind-iface`, `chore/verify-in-process`.

## Commits

- Style: Conventional Commits with a scope, imperative/descriptive subject.
- Examples:
  - `feat(verify): add go static verifier for nil safety`
  - `fix(dns): bind upstream queries to rule interface`
  - `chore(repo): add readme and module root`
  - `test(rules): cover subdomain suffix matching`
- Always check recent `git log` and match that style.
- Commit messages are subject and body only — no git trailers.

## Language

- Osman may switch between Turkish and English in chat.
- **All code, comments, identifiers, commit messages, and user-facing CLI strings stay in English.**

## Layout

- Prefer **idiomatic Go** layout (`cmd/`, `internal/`, packages by responsibility).
- Do not invent stubborn or over-nested folder trees “for architecture.”
- Add packages when code needs them; avoid empty scaffolding.

## Host integration (no subprocesses)

- Runtime / product code must **not** shell out (`os/exec`, `systemctl`, `ss`, `ip`, `nmcli`, …).
- Prefer **native Linux APIs** (netlink, `/proc`, raw sockets) and **D-Bus** (systemd, NetworkManager, resolved).
- `cmd/verify` also runs **in-process** via `go/analysis` (vet passes, Staticcheck, NilAway) — no `os/exec`.

## Go verifier

- Run the project verifier before considering Go changes done:
  - `go run ./cmd/verify`
  - or `make verify`
- The verifier loads packages with `go/packages` and runs analyzers in-process: vet-equivalent passes, Staticcheck, and Uber NilAway.
- New analyzers can be added to the verifier; do not bypass it for convenience.
