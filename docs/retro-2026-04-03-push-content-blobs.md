# Retrospective: Push silently dropping content blobs for non-parseable files

**Date:** 2026-04-03
**Duration of impact:** ~18 hours (v0.9.44 released Apr 2 18:59 — v0.9.50 released Apr 3 ~14:15)
**Severity:** High — broke CI deployments for any project using Kai archive checkouts

## Summary

A capture optimization in v0.9.44 stopped writing content blobs to the local object store for non-parseable files (svelte, json, yaml, markdown, html, css, etc.). The push code only read blobs from the object store, so these files were silently omitted from all pushes after v0.9.44. This caused incomplete server-side archives, which broke CI checkouts that relied on Kai archives instead of git clones.

## Timeline

- **Apr 2 18:59** — Commit `f8a0fdb` ("Skip blob writes for non-parseable files") shipped in v0.9.44. The change was an optimization: non-parseable files don't need their content stored locally since only tree-sitter-parseable files are read back during `Analyze`. The digest was still computed, so snapshots looked correct. But the push code (`runPush`) read content blobs exclusively from `db.ReadObject()`, which reads from the object store — where the blobs no longer existed.
- **Apr 3 ~10:00** — A commit modifying `[...path]/+page.svelte` (a Svelte file, non-parseable) invalidated the kaniko Docker layer cache for the kai-server CI build. Previous builds had been using a cached layer that contained the file from an older git-clone-based checkout. With the cache busted, kaniko rebuilt from the Kai archive, which was missing the file.
- **Apr 3 ~10:30** — kaicontext.com starts showing 404 on all repository file pages. The SvelteKit `[...path]` catch-all route was missing from the build.
- **Apr 3 ~11:00–13:00** — Investigation. Initially suspected kaniko bug with bracket characters in directory names. Then Cloudflare caching. Then a Kai snapshot bug. Eventually traced to the content blob being absent from both the local object store and the remote server.
- **Apr 3 ~13:00** — Root cause identified: commit `f8a0fdb` stopped writing blobs, push code never updated to compensate.
- **Apr 3 ~13:10** — Also discovered the kai-server repo's Kai remote was configured to push to `1m/kai-server` instead of `kai/kai-server`, so pushes were going to the wrong org. This had been masking the issue since the CI reads from `kai/kai-server`.
- **Apr 3 ~14:00** — Fix implemented: push code falls back to reading files from disk when blobs aren't in the object store, with digest verification.
- **Apr 3 ~14:15** — v0.9.50 released. Force push sent 4,600+ objects (mostly content blobs that were never pushed before). CI rebuilt and deployed successfully.

## Root cause

The change was a performance optimization to make `kai capture` faster. Writing hundreds of content blobs to disk on every capture is slow, and most of them (json, yaml, markdown, svelte, html, css, etc.) are never read back — only tree-sitter-parseable files (go, python, rust, js, ts, etc.) need their content during `Analyze`. Skipping the blob writes made capture noticeably faster.

The problem: the capture and push code had an implicit contract that every file's content blob exists in the object store. When the optimization broke that contract, nothing failed loudly — `db.ReadObject()` returned an error, the push code `continue`'d past it, and the file was silently omitted.

## Why it wasn't caught immediately

1. **No tests for push completeness.** The push code had no test verifying that all files in a snapshot are actually sent.
2. **Silent failure.** The push succeeded (exit 0) even when content blobs were missing. No warning or error was logged.
3. **Kaniko layer caching.** The CI Docker build cached the `COPY frontend/ ./` layer, so the missing file in the archive didn't matter until a change to that specific file busted the cache.
4. **Wrong remote config.** The kai-server repo was pushing to `1m/kai-server` instead of `kai/kai-server`, so the content was never on the server the CI reads from — but this was also masked by kaniko caching.

## Fix

**v0.9.50** (`8372fbc`): When `db.ReadObject()` fails for a content blob, fall back to reading the file from disk using the path stored in the File node. Verify the blake3 digest matches before sending to avoid pushing stale content.

## Action items

- [ ] Add a push completeness test: after push, verify the server has content blobs for all files in the snapshot
- [ ] Log a warning (not just `debugf`) when a content blob is missing from the object store during push
- [ ] Consider writing all content blobs to the object store regardless of parseability — the disk space savings aren't worth the coupling risk
- [ ] Add a `kai doctor` or `kai fsck` command that verifies local object store integrity against snapshot metadata
- [ ] Fix the duplicate CI trigger issue (git push + kai push both fire CI)
