# Session Status - Git Replacement Readiness

## Where we are

### Implemented
- SSH git upload-pack/receive-pack path works (clone/fetch/push).
- Protocol v0 support with capabilities parsing and negotiation.
- Minimal protocol v2 support: `ls-refs` + `fetch` with `packfile` section.
- Shallow support (depth=1 only): advertises `shallow`, validates `deepen=1`, emits shallow lines before ACK.
- Side-band-64k output supported for upload-pack.
- Git ref mapping between Kai and Git: heads/tags/kai refs, create/update/delete on receive-pack.
- Delete ref support in store and mirror deletion.
- Basic metrics: expvar `/metrics` + HTTP/SSH counters.
- Compatibility matrix documented in README.
- Toolchain validation checklist doc created.

### Tests added
- Integration tests (build tag `integration`) for:
  - SSH clone
  - shallow clone
  - receive-pack push
- Unit tests for:
  - pkt-line delimiter `0001`
  - caps parsing
  - shallow validation
  - ref mapping

## What is left (Future Work)

Tracked in 1Medium → Space **Future Work**

**Project: Git Replacement Remaining**
- Protocol v2 full capabilities + fetch options (task `f893d38b-baaf-4127-9816-e1cc9438b577`)
- multi_ack / multi_ack_detailed (task `89273e1d-cabb-4ffa-944c-7c567421fe6e`)
- Partial clone / promisor remotes (task `037d2927-3f8c-4962-be0d-072bc10c3196`)
- Pack optimizations (bitmaps/commit-graph) (task `5f1c77c8-7c32-476b-be33-e28fa9417c91`)
- Toolchain validation matrix (CI/IDEs/Git GUIs) (task `b3333720-c82a-493c-8d0d-8d52d10e1813`)

**Project: Git Pack Deltas**
- Real thin-pack/delta pack generation (task `1f7cc02f-f645-4114-a54a-f7dc67677ebd`)

## Files touched recently (high level)
- `kailab/sshserver/handler.go`: protocol v2 handling, shallow flow, caps parsing, side-band, ref mapping
- `kailab/sshserver/receive_pack.go`: git ref updates/creates/deletes
- `kailab/sshserver/protocol.go`: pkt-line delimiter support, side-band writer
- `kailab/store/sqlite.go`: delete ref support
- `kailab/metrics/metrics.go`: expvar counters
- `kailab/api/routes.go`: /metrics endpoint + HTTP metrics wrapper
- `kailab/sshserver/ssh_integration_test.go`: clone/shallow/push integration tests
- `kailab/sshserver/sshserver_test.go`: protocol unit tests
- `README.md`: compatibility matrix + operational notes
- `kailab/docs/toolchain_validation.md`: validation checklist

## Quick start for manual toolchain validation (Sublime Merge / others)

Start server:
```bash
cd /Users/jacobschatz/projects/kai/kailab
KAILAB_DISABLE_GIT_RECEIVE_PACK=false \
  go run ./cmd/kailabd --data /tmp/kai-smerge --listen 127.0.0.1:7447 --ssh-listen 127.0.0.1:2222
```

Create repo:
```bash
curl -s -X POST http://127.0.0.1:7447/admin/v1/repos \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"test","repo":"smerge"}'
```

Clone URL:
```
ssh://git@127.0.0.1:2222/test/smerge
```

## Notes
- Thin-pack/delta negotiation flags are wired but still emit full packs.
- Shallow is limited to depth=1 and uses synthetic commits (no parent graph).
- Protocol v2 is minimal; no advanced options yet.

