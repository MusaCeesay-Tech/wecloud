# WeCloud P2P — Verification Guide

Step-by-step terminal workflow to verify the full mesh end-to-end with `curl`.

## 0. Prerequisites

- Docker Engine running (verify: `docker version`)
- Python 3.10+ on the worker host
- Go 1.22+ (only to build coordinator/proxy)
- Tools: `curl`

Install the worker deps:

```powershell
pip install -r worker/requirements.txt
```

Build the Go components (in their own folders):

```powershell
# coordinator
cd coordinator
go mod tidy
go build -o wecloud-coordinator.exe .

# proxy
cd ..\proxy
go mod tidy
go build -o wecloud-proxy.exe .
```

> The proxy listens on `:8081` by default (port 80 needs admin rights on Windows).
> Override with `$env:WECLOUD_PROXY_ADDR=":8081"`.

## 1. Start the Coordinator

```powershell
cd wecloud\coordinator
.\wecloud-coordinator.exe
```

Expected:
```
WeCloud Core Orchestrator running on port :8080...
```

## 2. Start the Worker daemon (second terminal)

```powershell
cd wecloud\worker
python worker.py
```

> Running coordinator, worker, and proxy on **one machine**? Set the worker's advertised
> address to loopback so the coordinator always reaches it, even when Wi-Fi roams:
> `$env:WECLOUD_LOCAL_IP="127.0.0.1"` before launching. When unset, the worker re-detects
> its LAN IP on every heartbeat, so it follows DHCP changes automatically.

Expected:
```
WeCloud Worker daemon actively running on port 5001...
Registering with coordinator at http://localhost:8080 as '<hostname>'
```

Give it ~10 seconds for the first heartbeat, then check registration:

```powershell
curl.exe http://localhost:8080/api/workers
```

Expected: your node listed with **real** psutil-reported CPU/RAM/Disk values
(`cpu_cores`, `ram_mb`, `disk_gb`) and `active_apps: 0`.

## 3. Start the Mesh Proxy (third terminal)

```powershell
cd wecloud\proxy
.\wecloud-proxy.exe
```

Expected:
```
WeCloud Global Entry Mesh Router listening on :8081...
```

## 4. Create an Account (magic link) and Check Credits

Accounts are passwordless. Request a magic link, then consume it to open a session
(email delivery is not wired up yet, so the token is echoed in the response and logged):

```powershell
# request a link
curl.exe -X POST http://localhost:8080/api/auth/request ^
  -H "Content-Type: application/json" -d "{\"email\":\"you@example.com\"}"

# consume it (use the token from the response) to receive a session token
curl.exe "http://localhost:8080/api/auth/verify?token=<token>"
```

Expected: `{"status":"ok", ..., "created":true, "session_token":"...", "credits":100}` —
a brand-new account is granted the **100-credit signup bonus**.

With the CLI instead:

```powershell
python wecloud\cli\wecloud.py login --email you@example.com
python wecloud\cli\wecloud.py balance
python wecloud\cli\wecloud.py credits
```

## 5. Deploy an App (authenticated)

Deploys require a session and cost `1` credit (`deploy_fee`). Export the session token
returned above (or read it from the CLI session file at `~/.wecloud/session.json`):

```powershell
$token = "<session_token>"
curl.exe -X POST http://localhost:8080/api/developer/deploy ^
  -H "Content-Type: application/json" -H "Authorization: Bearer $token" ^
  -d "{\"name\":\"hello\",\"image\":\"nginx:alpine\"}"
```

or use the CLI (attaches the session automatically):

```powershell
python wecloud\cli\wecloud.py deploy --name hello --image nginx:alpine
```

An unauthenticated deploy returns **401**; an account with fewer than 1 credit returns
**402 Payment Required**.

Expected response (HTTP 201) shows the coordinator already pushed `/spawn` to the
worker and persisted the **verified** record:

```json
{
  "app_id": "app-172113...",
  "host_port": 32768,
  "internal_port": 80,
  "status": "running",
  "owner_id": "usr-172113...",
  "charged": 1,
  "credits": 99,
  "target_ip": "http://192.168.x.x:5001",
  "url": "http://app-172113....wecloud.local"
}
```

Invoke the worker directly to confirm the sandbox constraints:

```powershell
curl.exe http://localhost:5001/spawn
```

Expected:
```json
{
  "status": "ready",
  "sandbox": {
    "mem_limit": "256m",
    "nano_cpus": 1000000000,
    "network_mode": "bridge",
    "cap_drop": ["ALL"],
    "cap_add": ["CHOWN","DAC_OVERRIDE","FOWNER","FSETID","SETGID","SETUID","NET_BIND_SERVICE"],
    "security_opt": ["no-new-privileges:true"],
    "pids_limit": 128,
    "tmpfs": {"/tmp": "size=64m"},
    "auto_remove": false
  }
}
```

Inspect the live container on the worker to confirm hard limits applied:

```powershell
docker inspect <app-id> --format '{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}} {{.HostConfig.PidsLimit}}'
# 268435456 1000000000 128
```

## 6. Route Traffic Through the Mesh Proxy

Point a virtual host at the proxy (hosts file, `admin` shell):

```powershell
Add-Content C:\Windows\System32\drivers\etc\hosts "127.0.0.1 app-172113....wecloud.local"
```

Then hit the app through the mesh, not the worker directly:

```powershell
curl.exe -H "Host: app-172113....wecloud.local" http://localhost:8081/
# or
curl.exe http://app-172113....wecloud.local:8081/
```

Expected: the default `nginx` welcome page, proxied:
Worker `:5001/spawn` ran `nginx:alpine` -> proxy resolved `/api/lookup/<app-id>` -> `200 OK`.

## 7. Verify Failed-Deployment Handling

Try to deploy a non-existent image (authentication required):

```powershell
curl.exe -X POST http://localhost:8080/api/developer/deploy ^
  -H "Content-Type: application/json" -H "Authorization: Bearer <session_token>" ^
  -d "{\"name\":\"broken\",\"image\":\"does-not-exist:latest\"}"
```

Expected: HTTP 502 with `"status": "failed"` and an `"error"` from the worker.
The app record is persisted with `status=failed` (not silently dropped), and no
credit is charged because the deploy never succeeded:

```powershell
curl.exe http://localhost:8080/api/apps
```

## 8. Cleanup

```powershell
curl.exe -X POST http://localhost:5001/reap
```

Removes exited/dead `app-*` containers. Stop the three processes with `Ctrl+C`.

## 9. Web dashboard (Next.js)

Build and start the site (it proxies `/api/*` to the coordinator, so no CORS):

```powershell
cd wecloud\web
npm install
npm run build
npm run start        # http://localhost:3000
```

Point magic links at the web origin so the browser flow lands on the dashboard:

```powershell
$env:WECLOUD_PUBLIC_URL="http://localhost:3000"   # set before starting the coordinator
```

Verify pages and the proxy:

```powershell
curl.exe -o NUL -w "%{http_code}`n" http://localhost:3000/            # 200 (landing)
curl.exe -o NUL -w "%{http_code}`n" http://localhost:3000/login       # 200
curl.exe -o NUL -w "%{http_code}`n" http://localhost:3000/dashboard   # 200 (client-guarded)
curl.exe http://localhost:3000/api/workers                            # proxied JSON
```

Verify the browser auth flow with a cookie jar on `:3000`:

```powershell
$req = curl.exe -s -X POST http://localhost:3000/api/auth/request ^
  -H "Content-Type: application/json" -d "{\"email\":\"web@example.com\"}" | ConvertFrom-Json

curl.exe -s -c cookies.txt "http://localhost:3000/api/auth/verify?token=$($req.token)"   # sets cookie
curl.exe -s -b cookies.txt http://localhost:3000/api/auth/me                              # 100 credits
curl.exe -s -b cookies.txt "http://localhost:3000/api/apps?mine=1"                        # your apps only
```