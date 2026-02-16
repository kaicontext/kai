#!/usr/bin/env bash
# Verify kai-core has no network dependencies or cloud coupling.
# Run this in CI to enforce the OSS/SaaS boundary.

set -euo pipefail

ERRORS=0

echo "=== Core Purity Check ==="
echo ""

# 1. No net/http imports in kai-core
echo "Checking: no net/http imports in kai-core..."
if grep -r '"net/http"' kai-core/ --include='*.go' 2>/dev/null; then
    echo "  FAIL: kai-core must not import net/http"
    ERRORS=$((ERRORS + 1))
else
    echo "  PASS"
fi

# 2. No HTTP client libraries in kai-core/go.mod
echo "Checking: no HTTP client dependencies in kai-core/go.mod..."
for dep in "net/http" "github.com/go-resty" "github.com/hashicorp/go-retryablehttp"; do
    if grep -q "$dep" kai-core/go.mod 2>/dev/null; then
        echo "  FAIL: kai-core/go.mod contains HTTP dependency: $dep"
        ERRORS=$((ERRORS + 1))
    fi
done
echo "  PASS"

# 3. No cloud URLs in kai-core
echo "Checking: no cloud URLs in kai-core..."
if grep -rE '(kailayer\.com|amazonaws\.com|googleapis\.com|azure\.com)' kai-core/ --include='*.go' 2>/dev/null; then
    echo "  FAIL: kai-core must not reference cloud URLs"
    ERRORS=$((ERRORS + 1))
else
    echo "  PASS"
fi

# 4. No kailab/kailab-control imports in kai-core
echo "Checking: no kailab imports in kai-core..."
if grep -rE '"(kailab|kailab-control)/' kai-core/ --include='*.go' 2>/dev/null; then
    echo "  FAIL: kai-core must not import kailab or kailab-control"
    ERRORS=$((ERRORS + 1))
else
    echo "  PASS"
fi

# 5. No kailab/kailab-control Go package imports in kai-cli
echo "Checking: no kailab Go package imports in kai-cli..."
if grep -rE '"(kailab|kailab-control)/' kai-cli/ --include='*.go' 2>/dev/null; then
    echo "  FAIL: kai-cli must not import kailab or kailab-control Go packages"
    ERRORS=$((ERRORS + 1))
else
    echo "  PASS"
fi

# 6. No cloud SDK dependencies in kai-core/go.mod
echo "Checking: no cloud SDK dependencies in kai-core/go.mod..."
for dep in "cloud.google.com" "github.com/aws/aws-sdk" "github.com/Azure/azure-sdk"; do
    if grep -q "$dep" kai-core/go.mod 2>/dev/null; then
        echo "  FAIL: kai-core/go.mod contains cloud SDK: $dep"
        ERRORS=$((ERRORS + 1))
    fi
done
echo "  PASS"

echo ""
if [[ $ERRORS -gt 0 ]]; then
    echo "FAILED: $ERRORS purity violations found."
    exit 1
else
    echo "All checks passed. kai-core is clean."
fi
