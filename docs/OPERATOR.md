# M5: Leader Node + Public Beta

WeCloud's free public beta runs with a **single leader node** (run by maintainers/hosters) and volunteer workers that join via the public worker daemon. This document covers running the leader locally or on a VPS, exposing it over NAT using a public base domain (recommended: `sslip.io` for ad-hoc IPs), and onboarding volunteers.

## 1. Prerequisites

- Docker Engine + Docker Compose (v2)
- A public-reachable IP or a stable DNS name (for beta simplicity we use `<leader-ip>.sslip.io`)
- Open ports: `8080` (coordinator), `8081` (proxy), `3000` (web dashboard) on the leader
- No real money/credits system (credits are sandbox-only)
- Reverse tunnel is HTTP-only for M5 (HTTPS termination can be added later)

## 2. Leader Node (Compose)

The leader orchestrates: coordinator (auth, credits, spawn routing, host rewards, tunnel control), proxy (HTTP reverse proxy for `<app-id>.<base-domain>`), web (Next.js dashboard).

Compose file: `deploy/leader.docker-compose.yml`

Required env vars:
- `WECLOUD_BASE_DOMAIN` — e.g. `203.0.113.10.sslip.io` (leader public IP + sslip.io)
- `WECLOUD_PUBLIC_SCHEME` — `http` for sslip.io (no TLS) or `https`
- `WECLOUD_PROXY_PORT` — public proxy port (default `8081`)
- `WECLOUD_PUBLIC_URL` — web origin for magic links (e.g. `http://203.0.113.10:3000`)

Defaults: rewards accrue 1 credit per 3600s online (`WECLOUD_REWARD_SECONDS_PER_CREDIT`), tick every 60s (`WECLOUD_REWARD_INTERVAL`).

## 3. Start/Stop Leader

### Linux/macOS
```bash
export WECLOUD_BASE_DOMAIN="<your-public-ip>.sslip.io"
export WECLOUD_PUBLIC_SCHEME=http
export WECLOUD_PROXY_PORT=8081
export WECLOUD_PUBLIC_URL="http://<your-public-ip>:3000"
./scripts/leader-up.sh
```
Stop: `./scripts/leader-down.sh`

### Windows (PowerShell)
```powershell
$env:WECLOUD_BASE_DOMAIN="<your-public-ip>.sslip.io"
$env:WECLOUD_PUBLIC_SCHEME="http"
$env:WECLOUD_PROXY_PORT="8081"
$env:WECLOUD_PUBLIC_URL="http://<your-public-ip>:3000"
.\scripts\leader-up.ps1
```
Stop: `.\scripts\leader-down.ps1`

Services: coordinator `:8080`, proxy `:8081`, web `:3000`. Data persisted in Docker volume `coord_data` (`/data/wecloud.db`).

## 4. Routing (sslip.io + Reverse Tunnel)

Deployed apps are served as `<app-id>.<base-domain>[:proxy-port]`. For example `http://app-abc123.203.0.113.10.sslip.io:8081/`.

- Workers behind NAT register via outbound heartbeats and open a persistent **control** connection plus a **reverse stream pool** to the coordinator.
- When a proxy request arrives for an app on a tunneled worker, the coordinator acquires an idle reverse stream and bridges HTTP bytes between proxy and worker's local container port (no inbound port forwarding required on the worker).
- Direct `host_ip:port` is still attempted as a fallback when the worker is directly reachable.

## 5. Host Rewards (Claim Codes)

Workers periodically send telemetry (`secret`, optional `owner_code`). To credit uptime to a user:
1. Login via web dashboard or CLI (`wecloud login --email ...`) to receive a session.
2. Generate a one-time **node claim code**: `POST /api/nodes/claim` (CLI: `wecloud claim`).
3. Start worker with `WECLOUD_OWNER_CODE=<code>` (or set once). On next heartbeat/control handshake the node is linked to that user.
4. Rewards accrue while the owned node is online and recently seen: 1 whole credit per `WECLOUD_REWARD_SECONDS_PER_CREDIT` seconds (ms accumulator). Credits are recorded with reason `host_reward`.

CLI: `wecloud nodes` lists your claimed nodes and last-seen times.

## 6. Volunteer Operator Guide

Volunteers run the worker daemon on their machines (home PCs, laptops, lab boxes). Requirements: Docker available, outbound HTTPS/HTTP allowed to leader `:8080`.

### Run worker (local dev)
```bash
export WECLOUD_COORDINATOR="http://<leader-ip>:8080"
export WECLOUD_WORKER_ID="$(hostname)"
export WECLOUD_ENABLE_TUNNEL=1  # default on
python worker/worker.py
```
(Windows `set` with quoted forms recommended: `set "VAR=value"`)

### Claim your node (optional rewards)
1. `wecloud login --email you@example.com` (uses leader's web origin for magic links)
2. `wecloud claim` → copy code
3. Restart worker with `WECLOUD_OWNER_CODE=<code>`

### Notes
- Tunnel enabled by default; works behind CGNAT/NAT with no port forwarding.
- Worker sends a stable `secret` on first contact (persisted by coordinator) for mutual auth.
- `WECLOUD_LOCAL_IP` may be overridden; for tunnel-only testing set to an unreachable IP to force tunnel path.

## 7. Public Beta Checklist (M5)

- [x] M5-A: sslip.io routing + configurable base domain (`/api/config`, app URLs, proxy Host handling)
- [x] M5-B: host rewards + node claiming (claim codes, `/api/nodes`, reward loop with SQLite-safe accrual, RFC3339 time parsing)
- [x] M5-C: full HTTP reverse tunnel (control channel + reverse stream pool, tunnel-aware proxy, worker tunnel client)
- [x] M5-D: leader Docker Compose, deploy scripts (bash/PowerShell), operator/volunteer docs
- [ ] Smoke test against a public IP (optional) and update README/VERIFY with final results

## 8. Security & Limits

- Per-container sandbox unchanged: mem 256m, 1 CPU, bridge, `cap_drop ALL`, minimal `cap_add`, `no-new-privileges`, pids 128, tmpfs /tmp 64m, no host mounts, read-only not enforced on rootfs (sandbox limits as before).
- SQLite: `db.SetMaxOpenConns(1)` to avoid write contention under the reward loop.
- Magic-link auth (short-lived sessions), per-user balances/credits, deploy fees as configured.
