# WeCloud Volunteer Guide (Public Beta)

Join the WeCloud mesh as a volunteer worker node and earn host rewards for uptime (sandbox credits during beta).

## 1. Quick Start

1. Install Docker (Desktop/Engine). Ensure it's running.
2. Get the leader coordinator URL from the operator (e.g. `http://<leader-ip>:8080`).
3. Run worker daemon:

**Linux/macOS**
```bash
export WECLOUD_COORDINATOR="http://<leader-ip>:8080"
python wecloud/worker/worker.py
```

**Windows (PowerShell, quoted form recommended)**
```powershell
$env:WECLOUD_COORDINATOR="http://<leader-ip>:8080"
python .\wecloud\worker\worker.py
```

The worker will:
- Register with coordinator, open an outbound **control channel** and a small **reverse stream pool** (default 4).
- Work behind NAT/CGNAT with no inbound port forwarding (HTTP reverse tunnel).
- Advertise its local IP for direct routing if reachable; tunnel is used otherwise.

## 2. Claim Your Node (Host Rewards)

To receive uptime credits:
1. Login to the web dashboard at the leader's `WECLOUD_PUBLIC_URL` (e.g. `http://<leader-ip>:3000`) or via CLI: `wecloud login --email you@example.com` (magic link sent).
2. Claim a code: `wecloud claim` (returns a one-time `owner_code`).
3. Restart worker with that code set:

**Linux/macOS**
```bash
export WECLOUD_COORDINATOR="http://<leader-ip>:8080"
export WECLOUD_OWNER_CODE="<code>"
python wecloud/worker/worker.py
```

**Windows**
```powershell
$env:WECLOUD_COORDINATOR="http://<leader-ip>:8080"
$env:WECLOUD_OWNER_CODE="<code>"
python .\wecloud\worker\worker.py
```

Once linked, owned nodes accrue 1 credit per `WECLOUD_REWARD_SECONDS_PER_CREDIT` seconds while online. View your nodes: `wecloud nodes`.

## 3. Configuration

| Env Var | Default | Description |
|---|---|---|
| `WECLOUD_COORDINATOR` | `http://localhost:8080` | Leader coordinator URL |
| `WECLOUD_WORKER_ID` | `hostname` | Node identifier |
| `WECLOUD_WORKER_PORT` | `5001` | Local Flask health/metadata port (internal) |
| `WECLOUD_ENABLE_TUNNEL` | `1` | Enable HTTP reverse tunnel client |
| `WECLOUD_REVERSE_POOL` | `4` | Number of idle reverse streams to keep open |
| `WECLOUD_WORKER_SECRET` | random | Stable per-process secret (auto-generated if unset) |
| `WECLOUD_OWNER_CODE` | empty | One-time claim code to link node to your account |
| `WECLOUD_LOCAL_IP` | auto-detected | Advertised IP for direct connects (set unreachable to force tunnel) |
| `WECLOUD_HEARTBEAT_SECONDS` | `10` | Telemetry heartbeat interval |

## 4. Troubleshooting

- **Behind strict firewall**: outbound TCP to leader `:8080` is required. No inbound ports needed when tunnel enabled.
- **Tunnel not active**: check logs for `tunnel control channel established` and `Reverse tunnel enabled`. Coordinator must be M5-C build.
- **Force tunnel path**: `set "WECLOUD_LOCAL_IP=10.255.255.1"` (Windows) or export unreachable IP to verify traffic routes through reverse stream.
- **Docker pull/spawn**: first deploy may pull images; sandbox limits apply to all spawned containers.

## 5. Notes (Beta)

- Credits are non-monetary sandbox credits for testing resource accounting.
- HTTP-only tunnel in M5; HTTPS can be layered at the edge later.
- One node per worker process; claim codes are single-use in effect once linked.
