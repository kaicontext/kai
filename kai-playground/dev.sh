#!/bin/bash
# Development mode - runs backend and frontend separately with hot reload

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[0;33m'
NC='\033[0m'

echo -e "${BLUE}=== Kai Playground (Development Mode) ===${NC}"

# Check if kai binary exists
KAI_BIN="${KAI_BIN:-../kai-cli/kai}"
if [ ! -f "$KAI_BIN" ]; then
    echo "Building kai binary..."
    (cd ../kai-cli && go build -o kai ./cmd/kai)
    KAI_BIN="../kai-cli/kai"
fi

export KAI_BIN="$(cd "$(dirname "$KAI_BIN")" && pwd)/$(basename "$KAI_BIN")"
echo -e "Using kai binary: ${GREEN}$KAI_BIN${NC}"

# Install frontend deps if needed
if [ ! -d "frontend/node_modules" ]; then
    echo "Installing frontend dependencies..."
    (cd frontend && npm install)
fi

# Start backend in background
echo -e "${YELLOW}Starting backend on :8090...${NC}"
(cd backend && go run .) &
BACKEND_PID=$!

# Give backend time to start
sleep 2

# Start frontend dev server
echo -e "${YELLOW}Starting frontend on :3001...${NC}"
echo -e "${GREEN}Open http://localhost:3001 in your browser${NC}"
(cd frontend && npm run dev)

# Cleanup on exit
trap "kill $BACKEND_PID 2>/dev/null" EXIT
