#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
docker compose -f deploy/leader.docker-compose.yml down -v --remove-orphans 2>/dev/null || true
docker compose -f deploy/leader.docker-compose.yml down --remove-orphans
echo "WeCloud leader stopped."
