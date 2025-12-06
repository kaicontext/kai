#!/bin/bash
# Start the Kai Playground

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}=== Kai Playground ===${NC}"

# Check if kai binary exists
KAI_BIN="${KAI_BIN:-../kai-cli/kai}"
if [ ! -f "$KAI_BIN" ]; then
    echo "Building kai binary..."
    (cd ../kai-cli && go build -o kai ./cmd/kai)
    KAI_BIN="../kai-cli/kai"
fi

export KAI_BIN="$(cd "$(dirname "$KAI_BIN")" && pwd)/$(basename "$KAI_BIN")"
echo -e "Using kai binary: ${GREEN}$KAI_BIN${NC}"

# Build frontend
echo "Building frontend..."
(cd frontend && npm install && npm run build)

# Start backend
echo -e "${GREEN}Starting playground server on http://localhost:8090${NC}"
(cd backend && go run .)
