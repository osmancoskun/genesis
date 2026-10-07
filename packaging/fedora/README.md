# Fedora packaging

Uses the same install stage as a developer tree:

```bash
make install DESTDIR=$buildroot PREFIX=/usr ENABLE=0
```

## Quick RPM build

From the repository root (needs `rpmbuild`, `golang`, network for modules):

```bash
sudo dnf install -y rpm-build golang make systemd-rpm-macros
make rpm
# or pin the Version: make rpm RPM_VERSION=0.1.0
# artifacts: dist/rpm/RPMS/*/genesis-*.rpm
```

## Changelog

`packaging/fedora/genesis.spec` keeps an empty `%changelog` placeholder. On `make rpm`, [`scripts/assemble-rpm-spec.sh`](../../scripts/assemble-rpm-spec.sh) writes a build-time spec under `dist/rpm/SPECS/` with a changelog from git (`git config user.name` / `user.email`). The repo file is not modified.

With no `v*` tags, one entry lists all commit subjects. With tags, entries are grouped per tag (plus commits ahead of the newest tag).

## Notes

- Unit and binaries land under `/usr` (`PREFIX=/usr`), not `/usr/local`.
- `/etc/genesis/config.yaml` is `%config(noreplace)` (blank first-boot from `default.config.yaml`).
- Examples live under `%{_datadir}/genesis/` (import via Web UI).
- Mode B (`scripts/host-resolved-modeb.sh`) is **not** part of the RPM; run it after install if you want systemd-resolved Domains= → `:5553`.
- Before COPR: consider committing `vendor/` and building with `-mod=vendor` for offline reproducibility.
