#!/usr/bin/env bash
set -euo pipefail

# WeCloud leader node bootstrap (Linux/macOS). Requires Docker + Docker Compose.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

DOMAIN="${WECLOUD_BASE_DOMAIN:-}"
PUBLIC_URL="${WECLOUD_PUBLIC_URL:-}"
PROXY_PORT="${WECLOUD_PROXY_PORT:-8081}"
PUBLIC_SCHEME="${WECLOUD_PUBLIC_SCHEME:-http}"

if [[ -z "$DOMAIN" ]]; then
  echo "WECLOUD_BASE_DOMAIN not set (e.g. <your-ip>.sslip.io)" >&2
  exit 1
fi
if [[ -z "$PUBLIC_URL" ]]; then
  PUBLIC_URL="${PUBLIC_SCHEME}://${DOMAIN}"
fi

cat > .env.leader <<EOF
WECLOUD_BASE_DOMAIN=${DOMAIN}
WECLOUD_PUBLIC_SCHEME=${PUBLIC_SCHEME}
WECLOUD_PROXY_PORT=${PROXY_PORT}
WECLOUD_PUBLIC_URL=${PUBLIC_URL}
WECLOUD_REWARD_INTERVAL=60
WECLOUD_REWARD_SECONDS_PER_CREDIT=3600
EOF

echo "Starting WeCloud leader..."
docker compose -f deploy/leader.docker-compose.yml --env-file .env.leader up -d --build
echo "Leader running: coordinator :8080, proxy :8081, web :3000"
echo "Base domain: ${DOMAIN}"
echo "Public URL:  ${PUBLIC_URL}"
echo "App pattern: <app-id>.${DOMAIN}:${PROXY_PORT}"
