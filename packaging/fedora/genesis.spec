# Fedora / RHEL-style package for genesis.
#
# Build from repo root (network needed for go mod download unless vendored):
#   make rpm
#
# Or manually:
#   make dist
#   rpmbuild -ba --define "_topdir $PWD/dist/rpm" \
#     --define "genesis_version <ver>" packaging/fedora/genesis.spec
#
# Layout matches: make install DESTDIR=%{buildroot} PREFIX=/usr ENABLE=0
#
# Note: first skeleton uses network module download. Prefer vendor/ +
# -mod=vendor before COPR / official Fedora packaging.

%global __brp_golang_fix_auto_filelist 0
# Stripped Go binaries produce empty debugsource; skip debuginfo packages.
%global debug_package %{nil}

Name:           genesis
Version:        %{?genesis_version}%{!?genesis_version:0.1.0}
Release:        1%{?dist}
Summary:        DNS stub and policy path pins for preferred interfaces
License:        MIT
URL:            https://github.com/osmancoskun/genesis
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  golang
BuildRequires:  make
BuildRequires:  systemd-rpm-macros
Requires:       systemd

%description
Self-hosted Linux agent that steers DNS and connection traffic onto
preferred interfaces (VPN, NICs) using domain / subdomain / IP rules.
Installs genesis-agent, genesis-ctl, a systemd unit, a blank first-boot
config under /etc/genesis (config noreplace), and share examples.

%prep
%autosetup -n %{name}-%{version}

%build
# Network build for modules; switch to vendor/ for offline / COPR.
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
make build

%install
make install DESTDIR=%{buildroot} PREFIX=/usr ENABLE=0

%post
%systemd_post genesis.service

%preun
%systemd_preun genesis.service

%postun
%systemd_postun_with_restart genesis.service

%files
%{_datadir}/doc/genesis
%{_bindir}/genesis-agent
%{_bindir}/genesis-ctl
%{_unitdir}/genesis.service
%{_datadir}/genesis
%dir %{_sysconfdir}/genesis
%config(noreplace) %{_sysconfdir}/genesis/config.yaml

# %changelog is injected at package build time (make rpm → scripts/gen-rpm-changelog.sh).
%changelog

