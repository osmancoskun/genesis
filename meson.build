# Packaging orchestration only. Daily build/install stays in the Makefile.
project(
  'genesis',
  version: run_command(
    find_program('scripts/git-version.sh'),
    check: true,
  ).stdout().strip(),
  meson_version: '>=1.0.0',
  license: 'MIT',
)

package_rpm_sh = find_program('scripts/package-rpm.sh')

run_target(
  'package_rpm',
  command: [package_rpm_sh, meson.project_version(), '1'],
)
