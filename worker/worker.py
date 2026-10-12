import json
import os
import secrets
import socket
import sys
import threading
import time
from urllib.parse import urlparse

import docker
import psutil
import requests
from flask import Flask, jsonify, request

COORDINATOR_URL = os.environ.get("WECLOUD_COORDINATOR", "http://localhost:8080")
WORKER_ID = os.environ.get("WECLOUD_WORKER_ID", socket.gethostname())
WORKER_PORT = int(os.environ.get("WECLOUD_WORKER_PORT", 5001))
HEARTBEAT_SECONDS = int(os.environ.get("WECLOUD_HEARTBEAT_SECONDS", 10))

# Stable per-process secret used to authenticate reverse-tunnel connections.
WORKER_SECRET = os.environ.get("WECLOUD_WORKER_SECRET") or secrets.token_hex(16)
# Optional one-time code (from `POST /api/nodes/claim`) that links this node to a user.
OWNER_CODE = os.environ.get("WECLOUD_OWNER_CODE", "")

# Reverse tunnel: lets a node behind NAT receive spawns and serve app traffic.
TUNNEL_ENABLED = os.environ.get("WECLOUD_ENABLE_TUNNEL", "1").lower() not in ("0", "false", "no")
REVERSE_POOL_SIZE = int(os.environ.get("WECLOUD_REVERSE_POOL", 4))
_coord = urlparse(COORDINATOR_URL)
COORD_HOST = _coord.hostname or "localhost"
COORD_PORT = _coord.port or 80

# Set once the coordinator accepts our first heartbeat; the tunnel threads wait
# on it so they don't connect before the coordinator has stored our secret.
heartbeat_ok = threading.Event()

# Hard sandboxing limits applied to every spawned developer container.
#
# Isolation model:
#   * hard CPU + RAM caps (no noisy-neighbour)
#   * bridge network only (no host network namespace)
#   * drop ALL capabilities, then re-add only the minimum a web server needs
#   * no-new-privileges, PID cap (fork-bomb guard), 64m tmpfs scratch
#   * auto_remove=False, restart disabled
CONTAINER_LIMITS = {
    "mem_limit": "256m",
    "nano_cpus": 1000000000,
    "network_mode": "bridge",
    "cap_drop": ["ALL"],
    "cap_add": ["CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETGID", "SETUID", "NET_BIND_SERVICE"],
    "security_opt": ["no-new-privileges:true"],
    "pids_limit": 128,
    "tmpfs": {"/tmp": "size=64m"},
    "restart_policy": {"Name": ""},
    "auto_remove": False,
}

try:
    # Generous timeout: image pulls + container start can be slow on a busy host.
    docker_client = docker.from_env(timeout=300)
except Exception as e:
    print(f"Cannot connect to Docker backend. Ensure Docker is running: {e}")
    sys.exit(1)

worker_api = Flask(__name__)


def get_local_ip() -> str:
    """Best-effort detection of the machine's reachable LAN IP."""
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        return ip
    except Exception:
        return "127.0.0.1"


LOCAL_IP = os.environ.get("WECLOUD_LOCAL_IP", get_local_ip())
LOCAL_ADDRESS = f"http://{LOCAL_IP}:{WORKER_PORT}"


def current_local_ip() -> str:
    """Re-detect the LAN IP on every heartbeat so DHCP roaming doesn't pin a stale address."""
    if os.environ.get("WECLOUD_LOCAL_IP"):
        return os.environ["WECLOUD_LOCAL_IP"]
    return get_local_ip()


def collect_system_telemetry() -> dict:
    """Read real physical hardware stats instead of hardcoded values."""
    vmem = psutil.virtual_memory()
    disk = psutil.disk_usage("/")
    cpu_percent = psutil.cpu_percent(interval=0.5)
    ip = current_local_ip()
    try:
        active_containers = len(
            docker_client.containers.list(filters={"name": "app-", "status": "running"})
        )
    except Exception:
        active_containers = 0

    return {
        "id": WORKER_ID,
        "ip": f"http://{ip}:{WORKER_PORT}",
        "host_ip": ip,
        "cpu_cores": psutil.cpu_count(logical=False) or psutil.cpu_count(),
        "cpu_percent": cpu_percent,
        "ram_mb": vmem.total // (1024 * 1024),
        "ram_used_mb": vmem.used // (1024 * 1024),
        "ram_available_mb": vmem.available // (1024 * 1024),
        "disk_gb": disk.total // (1024 * 1024 * 1024),
        "disk_free_gb": disk.free // (1024 * 1024 * 1024),
        "active_apps": active_containers,
        "last_seen_utc": time.time(),
        "secret": WORKER_SECRET,
        "owner_code": OWNER_CODE,
    }


def do_spawn(app_id, image, internal_port) -> dict:
    """Run one developer container and return a status dict."""
    internal_port = int(internal_port or 80)
    if not image or not app_id:
        return {"status": "failed", "error": "image and app_id required"}
    if not str(app_id).startswith("app-"):
        return {"status": "failed", "error": "app_id must start with app-"}

    try:
        docker_client.containers.get(app_id).remove(force=True)
    except docker.errors.NotFound:
        pass
    except Exception:
        pass

    try:
        print(f"Pulling and spin-up sequence initialized for: {image}...")
        container = docker_client.containers.run(
            image,
            detach=True,
            name=app_id,
            ports={f"{internal_port}/tcp": None},
            **CONTAINER_LIMITS,
        )
        container.reload()
        if container.status != "running":
            logs = container.logs(tail=30).decode("utf-8", "replace")
            container.remove(force=True)
            return {
                "status": "failed",
                "error": f"container exited immediately (status={container.status})",
                "logs": logs,
            }
        host_port = container.ports[f"{internal_port}/tcp"][0]["HostPort"]
        print(f"Container {app_id} sandboxed and running, mapped to host port {host_port}")
        return {"status": "running", "port": int(host_port)}
    except docker.errors.NotFound:
        return {"status": "failed", "error": f"image '{image}' not found"}
    except docker.errors.APIError as e:
        return {"status": "failed", "error": str(e)}
    except Exception as e:
        return {"status": "failed", "error": str(e)}


def sending_heartbeats():
    while True:
        try:
            resp = requests.post(
                f"{COORDINATOR_URL}/api/worker/heartbeat",
                json=collect_system_telemetry(),
                timeout=5,
            )
            if resp.status_code != 200:
                print(f"Heartbeat rejected by coordinator: HTTP {resp.status_code}")
            else:
                heartbeat_ok.set()
        except Exception as e:
            print(f"Network split detected. Retry check-in with Coordinator failed: {e}")
        time.sleep(HEARTBEAT_SECONDS)


def _tunnel_request(path: str) -> socket.socket:
    """Open an authenticated raw tunnel socket, failing fast if rejected."""
    s = socket.create_connection((COORD_HOST, COORD_PORT), timeout=10)
    req = (
        f"GET {path} HTTP/1.1\r\nHost: {COORD_HOST}\r\n"
        f"X-Worker-Id: {WORKER_ID}\r\nX-Worker-Secret: {WORKER_SECRET}\r\n\r\n"
    )
    s.sendall(req.encode())
    s.settimeout(3.0)
    try:
        head = s.recv(1, socket.MSG_PEEK)
    except socket.timeout:
        head = b""
    except OSError as e:
        s.close()
        raise ConnectionError(f"tunnel socket error: {e}")
    if head.startswith(b"H"):
        s.close()
        raise ConnectionError("tunnel request rejected by coordinator")
    s.settimeout(None)
    return s


def tunnel_control_loop():
    """Keep an outbound control channel open; handle spawn commands from it."""
    while True:
        try:
            heartbeat_ok.wait(timeout=60)
            s = _tunnel_request("/api/worker/control")
            reader = s.makefile("rb")
            print("tunnel control channel established")
            while True:
                line = reader.readline()
                if not line:
                    break
                try:
                    msg = json.loads(line.decode())
                except Exception:
                    continue
                if msg.get("type") == "spawn":
                    result = do_spawn(msg.get("app_id"), msg.get("image"), msg.get("internal_port", 80))
                    resp = {"type": "spawn_result", "id": msg.get("id")}
                    resp.update(result)
                    s.sendall((json.dumps(resp) + "\n").encode())
        except Exception as e:
            print(f"tunnel control disconnected: {e}")
        time.sleep(3)


def _relay(a, b):
    try:
        while True:
            data = a.recv(65536)
            if not data:
                break
            b.sendall(data)
    except Exception:
        pass
    try:
        b.shutdown(socket.SHUT_WR)
    except Exception:
        pass


def reverse_stream_once():
    """Open one reverse stream; serve a single bridged connection, then return."""
    s = _tunnel_request("/api/worker/reverse")

    buf = b""
    while b"\n" not in buf:
        chunk = s.recv(1024)
        if not chunk:
            raise ConnectionError("coordinator closed before PORT header")
        buf += chunk
    line, _, rest = buf.partition(b"\n")
    parts = line.decode(errors="replace").split()
    if len(parts) < 2 or parts[0] != "PORT":
        raise ValueError(f"unexpected stream header: {line!r}")
    port = int(parts[1])

    app = socket.create_connection(("127.0.0.1", port), timeout=10)
    s.settimeout(None)
    app.settimeout(None)
    if rest:
        app.sendall(rest)

    t = threading.Thread(target=_relay, args=(s, app), daemon=True)
    t.start()
    _relay(app, s)
    t.join(timeout=5)
    s.close()
    app.close()


def reverse_pool_loop():
    while True:
        try:
            heartbeat_ok.wait(timeout=60)
            reverse_stream_once()
        except Exception as e:
            print(f"reverse stream error: {e}")
            time.sleep(1)


@worker_api.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "worker_id": WORKER_ID}), 200


@worker_api.route("/spawn", methods=["POST"])
def spawn_container():
    data = request.get_json(force=True) or {}
    result = do_spawn(data.get("app_id"), data.get("image"), data.get("internal_port", 80))
    code = 200 if result.get("status") == "running" else 400
    if result.get("status") != "running":
        code = 404 if "not found" in result.get("error", "") else 500
    return jsonify(result), code


@worker_api.route("/reap", methods=["POST"])
def reap_dead_containers():
    """Delete any exited app containers to free stolen resources."""
    removed = []
    try:
        for c in docker_client.containers.list(all=True, filters={"name": "app-"}):
            if c.status in ("exited", "dead", "created"):
                c.remove(force=True)
                removed.append(c.name)
    except Exception as e:
        return jsonify({"status": "failed", "error": str(e)}), 500
    return jsonify({"status": "ok", "removed": removed}), 200


@worker_api.route("/spawn", methods=["GET"], strict_slashes=False)
def spawn_index():
    return jsonify({"status": "ready", "sandbox": CONTAINER_LIMITS}), 200


if __name__ == "__main__":
    threading.Thread(target=sending_heartbeats, daemon=True).start()
    if TUNNEL_ENABLED:
        threading.Thread(target=tunnel_control_loop, daemon=True).start()
        for _ in range(REVERSE_POOL_SIZE):
            threading.Thread(target=reverse_pool_loop, daemon=True).start()
        print(f"Reverse tunnel enabled (pool={REVERSE_POOL_SIZE}) via {COORD_HOST}:{COORD_PORT}")
    print(f"WeCloud Worker daemon actively running on port {WORKER_PORT}...")
    print(f"Registering with coordinator at {COORDINATOR_URL} as '{WORKER_ID}'")
    worker_api.run(host="0.0.0.0", port=WORKER_PORT)
