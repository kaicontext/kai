#!/usr/bin/env bash
# End-to-end smoke test for 'kai init' against a real kai-server.
#
# Runs in a throwaway temp directory with a per-run unique email so that
# state from a previous run cannot bleed into this one. Exercises the
# critical paths that broke in production in the past:
#
#   - magic-link signup flow (email -> DevToken -> JWT)
#   - personal org auto-creation
#   - repo creation and remote wiring
#   - first push to kai-server
#   - post-commit / pre-push git hooks installed at v3 (never block git)
#   - broken .kai directory does NOT block git commit
#   - selfHealHooks() upgrades a v1 hook to v3 on any kai invocation
#
# Environment variables (all required):
#   KAI_SERVER               - base URL, e.g. https://staging.kaicontext.com
#   KAI_STAGING_RESET_TOKEN  - bearer token for DELETE /api/v1/admin/test/reset
#
# The script exits non-zero on any assertion failure; the calling
# workflow treats that as a failed release gate.

set -euo pipefail

: "${KAI_SERVER:?KAI_SERVER must be set}"
: "${KAI_STAGING_RESET_TOKEN:?KAI_STAGING_RESET_TOKEN must be set}"

command -v kai >/dev/null 2>&1 || { echo "kai binary not on PATH"; exit 1; }

export KAI_SERVER

echo "=== 1. Wipe staging DB ==="
RESET_STATUS=$(curl -sS -o /tmp/reset.out -w "%{http_code}" \
  -X DELETE "$KAI_SERVER/api/v1/admin/test/reset" \
  -H "Authorization: Bearer $KAI_STAGING_RESET_TOKEN")
if [ "$RESET_STATUS" != "200" ]; then
  echo "  reset endpoint returned $RESET_STATUS"
  cat /tmp/reset.out
  exit 1
fi
echo "  reset ok: $(cat /tmp/reset.out)"
echo

echo "=== 2. Create throwaway repo ==="
WORK=$(mktemp -d)
cd "$WORK"
git init -q
git config user.email "smoke@kaitest.local"
git config user.name "Smoke Test"
printf 'hello from smoke %s\n' "${GITHUB_SHA:-local}" > README.md
git add README.md
git commit -qm "initial"
echo "  repo at $WORK"
echo

echo "=== 3. Run kai init, piping a unique email ==="
SMOKE_EMAIL="smoke-${GITHUB_SHA:-local}-$(date +%s)@kaitest.local"
printf '%s\n' "$SMOKE_EMAIL" | kai init 2>&1 | tee /tmp/init.log | tail -40
echo

echo "=== 4. Post-init assertions ==="
fail() { echo "  ASSERT FAIL: $1"; exit 1; }

[ -d .kai ] || fail ".kai directory not created"
grep -q "kai-managed-hook v3" .git/hooks/pre-commit || fail "pre-commit hook is not v3"
grep -q "kai-managed-hook v3" .git/hooks/pre-push || fail "pre-push hook is not v3"
grep -q "Already logged in\|Logged in as" /tmp/init.log || fail "init did not reach login step"
kai doctor > /tmp/doctor.log 2>&1 || true
grep -q "logged in" /tmp/doctor.log || fail "kai doctor does not see an active login"
echo "  all post-init assertions passed"
echo

echo "=== 5. Foot-gun test: broken .kai must NOT block git commit ==="
rm -rf .kai
printf 'broken state\n' >> README.md
git add README.md
if ! git commit -qm "must not block"; then
  fail "git commit was blocked by pre-commit hook when .kai was missing"
fi
echo "  git commit proceeded with .kai missing"
echo

echo "=== 6. Self-heal test: downgrade hook to v1, run kai, verify auto-upgrade ==="
cat > .git/hooks/pre-commit <<'OLDHOOK'
#!/bin/sh
# kai-managed-hook
# Automatically update Kai semantic graph before commit.
kai capture
OLDHOOK
chmod +x .git/hooks/pre-commit
grep -q "kai-managed-hook v3" .git/hooks/pre-commit && fail "test setup wrong: hook is still v3"

# Invoke a real subcommand so cobra runs PersistentPreRun (and thus
# selfHealHooks). `kai --version` and `kai --help` short-circuit in
# cobra and do NOT run PreRun hooks — don't use them for this test.
kai doctor >/dev/null 2>&1 || true

grep -q "kai-managed-hook v3" .git/hooks/pre-commit || fail "selfHealHooks did not upgrade v1 -> v3"
echo "  hook upgraded to v3 after kai invocation"
echo

echo "=== SMOKE PASSED ==="
