# Fedora packaging

Daily `make build` / `make install` stay Makefile-driven. RPM packaging is
orchestrated by Meson (`make package-rpm`), which derives version and changelog
from git, then runs `rpmbuild`. Inside the spec, `%build` / `%install` still call Make:

```bash
make build
make install DESTDIR=$buildroot PREFIX=/usr ENABLE=0
```

## CI (tag → Release + Packages)

Pushing a `v*` tag runs [`.github/workflows/release.yml`](../../.github/workflows/release.yml):

1. Builds the Fedora RPM (`make package-rpm` in Fedora 44)
2. Creates a GitHub **Release** with `.rpm` / `.src.rpm` assets
3. Pushes the agent image to **GHCR** (`ghcr.io/<owner>/genesis`)
4. Pushes the binary RPM as an OCI artifact (`ghcr.io/<owner>/genesis-rpm`)

```bash
git tag -a v0.1.2 -m "v0.1.2"
git push origin v0.1.2
```

Local dry-run (same Fedora 44 container as CI, no GitHub push):

```bash
docker run --rm -v "$PWD:/src" -w /src \
  -e VERSION_MODE=tag -e VERSION=0.1.1 \
  fedora:44 bash -lc './scripts/ci-package-rpm.sh'
```

## Quick RPM build

From the repository root:

```bash
sudo dnf install -y rpm-build golang make meson ninja-build systemd-rpm-macros git \
  systemd systemd-resolved dbus-broker iproute nftables
make build-deps   # Go toolchain
make deps         # runtime: systemd, resolved, D-Bus, iproute, …
make package-rpm
# alias: make rpm
# artifacts: dist/rpm/RPMS/*/genesis-*.rpm
```

RPM `Requires`: `systemd`, `systemd-resolved`, `dbus`, `iproute` (`nftables` recommended).

## Version (no static Meson version)

[`scripts/git-version.sh`](../../scripts/git-version.sh) emits `X.Y.Z`:

| Mode | Behavior |
|------|----------|
| `VERSION_MODE=auto` (default) | Newest `v*` tag; if HEAD has commits after that tag, **patch +1** (e.g. `v0.1.0` + commits → `0.1.1`). Does **not** create a git tag. |
| `VERSION_MODE=tag` | Exact newest `v*` tag version (no bump). |
| `VERSION=1.2.3` | Hard override. |

Examples:

```bash
make package-rpm
VERSION_MODE=tag make package-rpm
VERSION=0.2.0 make package-rpm
```

## Changelog

`packaging/fedora/genesis.spec` keeps an empty `%changelog` placeholder. On
`make package-rpm`, Meson runs [`scripts/package-rpm.sh`](../../scripts/package-rpm.sh),
which calls [`scripts/assemble-rpm-spec.sh`](../../scripts/assemble-rpm-spec.sh) to write
a build-time spec under `dist/rpm/SPECS/` with a changelog from git
(`git config user.name` / `user.email`). The repo file is not modified.

With no `v*` tags, one entry lists all commit subjects. With tags, entries are
grouped per tag (plus commits ahead of the newest tag under the packaging version).

## Notes

- Unit and binaries land under `/usr` (`PREFIX=/usr`), not `/usr/local`.
- `/etc/genesis/config.yaml` is `%config(noreplace)` (blank first-boot from `default.config.yaml`).
- Examples live under `%{_datadir}/genesis/` (import via Web UI).
- Mode B (`scripts/host-resolved-modeb.sh`) is **not** part of the RPM; run it after install if you want systemd-resolved Domains= → `:5553`.
- Before COPR: consider committing `vendor/` and building with `-mod=vendor` for offline reproducibility.
