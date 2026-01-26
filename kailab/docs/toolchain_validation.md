# Toolchain Integration Validation

This checklist tracks validation status for CI systems, IDEs, and Git GUIs when using Kai's Git-compatible SSH endpoints.

## Status

Validated (automated):
- Git CLI over SSH: clone, shallow clone, push (integration tests under `kailab/sshserver` with build tag `integration`)

Pending (manual validation):
- GitHub Actions checkout via SSH
- GitLab CI checkout via SSH
- VS Code Git extension (clone/fetch/push)
- JetBrains IDEs (IntelliJ, GoLand)
- Sourcetree / Fork / GitKraken

## Manual validation checklist

### Git CLI
1. `git clone ssh://git@<host>:<port>/<tenant>/<repo>`
2. `git clone --depth 1 ssh://git@<host>:<port>/<tenant>/<repo>`
3. `git push origin main` from a new repo

### GitHub Actions
1. Use `actions/checkout` with SSH URL.
2. Confirm `git fetch` succeeds and `git status` is clean.

### GitLab CI
1. Use SSH deploy key.
2. Ensure checkout and fetch succeed with shallow disabled and enabled.

### VS Code
1. Clone via SSH.
2. Fetch/pull and push a commit.

### JetBrains IDEs
1. Clone via SSH.
2. Fetch/pull and push a commit.

### Git GUIs
1. Clone via SSH.
2. Fetch/pull and push a commit.

## Notes

- Protocol v2 is not supported yet.
- Thin packs and `multi_ack` are not supported.
- Shallow negotiation is supported (server emits `shallow` lines), but packs remain synthetic.
