## M5 — Public Beta (sslip.io + Reverse Tunnel + Host Rewards)

### M5-A: sslip.io + Configurable Public Domain

Environment variables (coordinator):
- `WECLOUD_BASE_DOMAIN` — base domain for app URLs (default `localhost.sslip.io`)
- `WECLOUD_PUBLIC_SCHEME` — `http` or `https` (default `http`)
- `WECLOUD_PROXY_PORT` — public proxy port (default `8081`)
- `WECLOUD_PUBLIC_URL` — web origin used for magic links (e.g. `http://localhost:3000`)

App URLs follow `<app-id>.<base-domain>[:proxy-port]`. The proxy routes by `Host` header to the resolved worker target. Config endpoint: `GET /api/config` returns `{base_domain, scheme, proxy_port, app_pattern}`.

### M5-B: Host Rewards + Node Claiming

Workers send `secret` + optional `owner_code` in telemetry. 
1. Login (web/CLI) → session
2. `POST /api/nodes/claim` (CLI `wecloud claim`) → one-time `code`
3. Start worker with `WECLOUD_OWNER_CODE=<code>` → node linked on next heartbeat
4. Rewards: 1 credit per `WECLOUD_REWARD_SECONDS_PER_CREDIT` seconds online for owned+recently-seen nodes (ms accumulator, SQLite-safe). Ledger reason `host_reward`. List: `GET /api/nodes` (CLI `wecloud nodes`).

Env: `WECLOUD_REWARD_INTERVAL` (default 60s), `WECLOUD_REWARD_SECONDS_PER_CREDIT` (default 3600).

### M5-C: HTTP Reverse Tunnel

Workers open:
- Persistent **control channel** (`GET /api/worker/control`, authenticated by `X-Worker-Id` + `X-Worker-Secret`) to receive spawn commands over an outbound TCP connection.
- **Reverse stream pool** (`GET /api/worker/reverse`, pool size `WECLOUD_REVERSE_POOL`, default 4) holding idle outbound streams.

Proxy acquires an idle reverse stream via coordinator (`GET /api/tunnel/acquire?worker=&port=`), sends `PORT <port>\n`, and bridges HTTP bytes between proxy and the worker's local app port (no inbound NAT ports). Direct `host_ip:port` fallback remains. Worker env: `WECLOUD_ENABLE_TUNNEL` (default `1`).

### M5-D: Leader Node + Operator/Volunteer Docs

- Compose: `deploy/leader.docker-compose.yml` (coordinator+proxy+web). Volumes: `coord_data` for SQLite.
- Scripts: `scripts/leader-up.sh`, `scripts/leader-down.sh`, `scripts/leader-up.ps1`, `scripts/leader-down.ps1` (set `WECLOUD_BASE_DOMAIN` and `WECLOUD_PUBLIC_URL`).
- Docs: `docs/OPERATOR.md` (run leader, sslip.io, rewards, tunnel), `docs/VOLUNTEER.md` (join mesh, claim node, troubleshooting).
