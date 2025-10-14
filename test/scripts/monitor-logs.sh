#!/bin/bash

set -e

# HyperFaaS Log Monitor Script
# This script starts a tmux session to monitor all HyperFaaS service logs

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Resolve paths dynamically for multi-user setups
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

export HYPERFAAS_REPO_ROOT="$REPO_ROOT"
export HYPERFAAS_COMPOSE_FILE="$REPO_ROOT/full-compose.yaml"
TMUX_CONF="$REPO_ROOT/test/scripts/tmux-logs.conf"

echo -e "${BLUE}Checking if HyperFaaS services are running...${NC}"
if ! docker compose -f "$HYPERFAAS_COMPOSE_FILE" ps 2>/dev/null | grep -q "Up"; then
    echo -e "${YELLOW}Warning: No HyperFaaS services appear to be running.${NC}"
    echo "You may want to start them first with:"
    echo "  docker compose -f \"$HYPERFAAS_COMPOSE_FILE\" up -d"
    echo ""
    read -p "Continue anyway? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# Kill existing session if it exists
if tmux has-session -t hyperfaas-logs 2>/dev/null; then
    echo -e "${YELLOW}Existing hyperfaas-logs session found. Killing it...${NC}"
    tmux kill-session -t hyperfaas-logs
fi

echo -e "${GREEN}Starting HyperFaaS log monitor...${NC}"
echo -e "${BLUE}This will open a tmux session showing logs from:${NC}"
echo "  - Top: Load Balancer (lb), Leaf-A, Leaf-B"
echo "  - Bottom: Workers (A-1, A-2, B-1, B-2)"
echo ""
echo -e "${YELLOW}Navigation:${NC}"
echo "  - Use Ctrl+b then arrow keys to navigate between panes"
echo "  - Use Ctrl+b then 'q' to quit the session"
echo "  - Use Ctrl+b then 'z' to zoom/unzoom a pane"
echo ""

# Start and attach to the tmux session defined in the config
tmux -f "$TMUX_CONF" attach -t hyperfaas-logs

echo -e "${GREEN}Log monitor session ended.${NC}"


