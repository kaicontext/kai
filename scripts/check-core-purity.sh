#!/usr/bin/env bash
# Verify kai-core has no network dependencies or server coupling.
# Run this in CI to enforce the architectural boundary between core and server.

set -euo pipefail

ERRORS=0

echo "=== Core Purity Check ==="
echo ""

# 1. No server directories tracked in git
echo "Checking: no server/cloud directories tracked in git..."
for dir in cloud server enterprise billing sso rbac audit kailab kailab-control; do
    if git ls-files --error-unmatch "$dir" >/dev/null 2>&1; then
        echo "  FAIL: directory '$dir' must not be tracked in this repo"
        ERRORS=$((ERRORS + 1))
    fi
done
echo "  PASS"

# 2. No net/http imports in kai-core
echo "Checking: no net/http imports in kai-core..."
if grep -r '"net/http"' kai-core/ --include='*.go' 2>/dev/null; then
    echo "  FAIL: kai-core must not import net/http"
    ERRORS=$((ERRORS + 1))
else
    echo "  PASS"
fi

# 3. No HTTP client libraries in kai-core/go.mod
echo "Checking: no HTTP client dependencies in kai-core/go.mod..."
for dep in "net/http" "github.com/go-resty" "github.com/hashicorp/go-retryablehttp"; do
    if grep -q "$dep" kai-core/go.mod 2>/dev/null; then
        echo "  FAIL: kai-core/go.mod contains HTTP dependency: $dep"
        ERRORS=$((ERRORS + 1))
    fi
done
echo "  PASS"

# 4. No cloud URLs in kai-core
echo "Checking: no cloud URLs in kai-core..."
if grep -rE '(amazonaws\.com|googleapis\.com|azure\.com)' kai-core/ --include='*.go' 2>/dev/null; then
    echo "  FAIL: kai-core must not reference cloud URLs"
    ERRORS=$((ERRORS + 1))
else
    echo "  PASS"
fi

# 5. No cloud SDK dependencies in kai-core/go.mod
echo "Checking: no cloud SDK dependencies in kai-core/go.mod..."
for dep in "cloud.google.com" "github.com/aws/aws-sdk" "github.com/Azure/azure-sdk" "github.com/stripe" "github.com/auth0" "github.com/okta"; do
    if grep -q "$dep" kai-core/go.mod 2>/dev/null; then
        echo "  FAIL: kai-core/go.mod contains cloud SDK: $dep"
        ERRORS=$((ERRORS + 1))
    fi
done
echo "  PASS"

# 6. No forbidden strings in core packages (excluding tests and taxonomy keywords)
echo "Checking: no server-specific concepts in kai-core..."
if grep -rEi '(tenant|org_id|sso|billing|cloud_url|KAI_CLOUD)' kai-core/ --include='*.go' \
    | grep -v '_test\.go' | grep -v 'taxonomy\.go' 2>/dev/null; then
    echo "  FAIL: kai-core contains server-specific concepts"
    ERRORS=$((ERRORS + 1))
else
    echo "  PASS"
fi

echo ""
if [[ $ERRORS -gt 0 ]]; then
    echo "FAILED: $ERRORS purity violations found."
    exit 1
else
    echo "All checks passed. Repo is clean."
fi
