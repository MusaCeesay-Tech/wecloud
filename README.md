# WeCloud

Open-source "give-and-take" cloud: run a volunteer node, earn credits for hosting apps,
spend credits to deploy your own. Works behind NAT/CGNAT (HTTP reverse tunnel), with
hard container sandboxing, sslip.io routing, and a lightweight leader+volunteer mesh.

## Components

| Folder | Role | Tech |
|---|---|---|
| `coordinator/` | Central orchestrator: worker registry, bin-packing scheduler, verified deploy pipeline, accounts + credits | Go + SQLite (`modernc.org/sqlite`) |
| `worker/` | Volunteer host daemon: psutil telemetry heartbeat, `/spawn` with hard Docker sandbox | Python / Flask / Docker SDK |
| `proxy/` | Reverse mesh router: `app-xxx.wecloud.local` -> lookup -> worker origin | Go / `httputil` |
| `web/` | Public site + developer dashboard (magic-link login, deploy, credits) | Next.js / React |
| `cli/` | Developer CLI: login/deploy/balance/list in one command | Python |

## Deploy lifecycle (verified)

1. `POST /api/developer/deploy` schedules the least-loaded healthy worker.
2. Coordinator POSTs `/spawn` **directly to the chosen worker** and waits.
3. Worker pulls the image, applies hard limits, maps the host port, replies `running`.
4. Coordinator persists the record with `status=running` **only after worker confirmation**.

Sandbox on every container: `mem_limit=256m`, `nano_cpus=1e9`, `network_mode=bridge`,
`cap_drop=ALL` plus a minimal `cap_add` set (`CHOWN DAC_OVERRIDE FOWNER FSETID SETGID
SETUID NET_BIND_SERVICE`), `no-new-privileges`, `pids_limit=128`, tmpfs `/tmp`, and **no
host bind mounts** (so no host filesystem is ever exposed). The rootfs is left writable
because a `read_only` rootfs breaks common images like nginx; isolation comes from the
dropped capabilities and missing host mounts instead.

## Quickstart

**Note:** M5 = Milestone 5 (public beta features: sslip.io routing, host rewards/claim codes,
HTTP reverse tunnel, leader Docker Compose + scripts, operator/volunteer docs). See
[VERIFY.md](VERIFY.md) for M5 details.

```powershell
# 1. coordinator   (port 8080)
cd coordinator; go mod tidy; go build -o wecloud-coordinator.exe .
$env:WECLOUD_PUBLIC_URL="http://localhost:3000"; .\wecloud-coordinator.exe

# 2. worker        (port 5001)
python worker\worker.py

# 3. mesh proxy    (port 8081)
cd proxy; go mod tidy; go build -o wecloud-proxy.exe .; .\wecloud-proxy.exe

# 4. web dashboard (port 3000)
cd web; npm install; npm run dev

# 5. sign up/login and deploy — via dashboard or CLI
python cli\wecloud.py signup --username you --email you@example.com --password yourpass
# or: python cli\wecloud.py login --email you@example.com --password yourpass
# magic-link fallback: omit --password
python cli\wecloud.py deploy --name hello --image nginx:alpine
```

Open <http://localhost:3000> for the site and <http://localhost:3000/dashboard> for the
developer dashboard. The dashboard proxies `/api/*` to the coordinator (`WECLOUD_COORDINATOR`
to override), so auth cookies work same-origin with no CORS setup.

See `VERIFY.md` for full curl-based end-to-end verification.

## Accounts, auth & credits (M2–M5)

Accounts support **username + email + password** with **magic-link fallback** (less friction
by default). Sessions are HttpOnly cookies (browsers) and bearer tokens (CLI). On first
signup, accounts receive a `signup_bonus`. Deploys deduct `deploy_fee` and require a valid
session with sufficient balance.

Credits are an append-only **ledger** (`balance = SUM(delta)`):

| Reason | Delta | When |
|---|---|---|
| `signup_bonus` | `+100` | once, at account creation |
| `deploy_fee` | `-1` | per successful deploy |
| `host_reward` | `+credits` | earned for hosting (uptime of claimed node) |
| `referral_signup` | `+credits` | referral reward on eligible signup (configurable) |
| `referral_activity` | `+credits` | referral reward when referred user/node meets activity threshold |

Referrals and shareable invites are built-in to help scale the mesh (see
[docs/OPERATOR.md](docs/OPERATOR.md) and [docs/VOLUNTEER.md](docs/VOLUNTEER.md)).

## API

- `POST /api/worker/heartbeat` — worker telemetry upsert (10s cadence)
- `POST /api/developer/deploy` — `{name, image, internal_port}` -> scheduled app (auth + `deploy_fee`)
- `GET  /api/lookup/<app_id>` — resolve running app to `ip:port` origin
- `GET  /api/workers` / `GET /api/apps` — ledger inspection (`/api/apps?mine=1` = your apps)
- `POST /api/auth/request` — `{email}` -> magic link
- `GET  /api/auth/verify?token=...` — consume link, open session
- `GET  /api/auth/me` / `POST /api/auth/logout` — session info / revoke
- `GET  /api/credits/balance` / `GET /api/credits/history` — credit balance and ledger
- Worker `POST /spawn`, `POST /reap`, `GET /health`

## Architecture notes

- Scheduler favors lowest `active_apps` density among nodes seen in last 30s.
- Worker reports LAN-IP-discovered callback address and real psutil telemetry.
- Proxy caches lookup for 5s and forwards `X-Forwarded-Host` / `X-Forwarded-App`.
