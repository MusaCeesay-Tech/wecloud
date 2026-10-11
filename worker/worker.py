import os
import socket
import sys
import threading
import time

import docker
import psutil
import requests
from flask import Flask, jsonify, request

COORDINATOR_URL = os.environ.get("WECLOUD_COORDINATOR", "http://localhost:8080")
WORKER_ID = os.environ.get("WECLOUD_WORKER_ID", socket.gethostname())
WORKER_PORT = int(os.environ.get("WECLOUD_WORKER_PORT", 5001))
HEARTBEAT_SECONDS = int(os.environ.get("WECLOUD_HEARTBEAT_SECONDS", 10))

# Hard sandboxing limits applied to every spawned developer container.
#
# Isolation model:
#   * hard CPU + RAM caps (no noisy-neighbour)
#   * bridge network only (no host network namespace)
#   * drop ALL capabilities, then re-add only the minimum a web server needs
#   * no-new-privileges, PID cap (fork-bomb guard), 64m tmpfs scratch
#   * NO host bind mounts are ever passed, so the volunteer host filesystem
#     is never exposed. If host volumes are ever added they MUST be :ro
#     (read-only host mount vectors).
CONTAINER_LIMITS = {
    "mem_limit": "256m",                 # hard RAM cap
    "nano_cpus": 1_000_000_000,          # 1 full CPU in ns
    "network_mode": "bridge",
    "cap_drop": ["ALL"],                 # drop every Linux capability
    "cap_add": [
        "CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID",
        "SETGID", "SETUID", "NET_BIND_SERVICE",
    ],                                   # minimal caps common web servers need
    "security_opt": ["no-new-privileges:true"],
    "pids_limit": 128,                   # cap fork bombs
    "tmpfs": {"/tmp": "size=64m"},       # writable scratch space
    "restart_policy": {"Name": ""},      # never auto-restart dead containers
    "auto_remove": False,                # keep exited containers so we can read logs + reap
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


def collect_system_telemetry() -> dict:
    """Read real physical hardware stats instead of hardcoded values."""
    vmem = psutil.virtual_memory()
    disk = psutil.disk_usage("/")
    cpu_percent = psutil.cpu_percent(interval=0.5)
    per_cpu = psutil.cpu_percent(interval=0.1, percpu=True)
    try:
        active_containers = len(
            docker_client.containers.list(
                filters={"name": "app-", "status": "running"}
            )
        )
    except Exception:
        active_containers = 0

    return {
        "id": WORKER_ID,
        "ip": LOCAL_ADDRESS,
        "host_ip": LOCAL_IP,
        "cpu_cores": psutil.cpu_count(logical=False) or psutil.cpu_count(),
        "cpu_percent": cpu_percent,
        "cpu_per_core_percent": per_cpu,
        "ram_mb": vmem.total // (1024 * 1024),
        "ram_used_mb": vmem.used // (1024 * 1024),
        "ram_available_mb": vmem.available // (1024 * 1024),
        "disk_gb": disk.total // (1024 * 1024 * 1024),
        "disk_free_gb": disk.free // (1024 * 1024 * 1024),
        "active_apps": active_containers,
        "last_seen_utc": time.time(),
    }


def sending_heartbeats():
    while True:
        try:
            payload = collect_system_telemetry()
            resp = requests.post(
                f"{COORDINATOR_URL}/api/worker/heartbeat",
                json=payload,
                timeout=5,
            )
            if resp.status_code != 200:
                print(f"Heartbeat rejected by coordinator: HTTP {resp.status_code}")
        except Exception as e:
            print(f"Network split detected. Retry check-in with Coordinator failed: {e}")
        time.sleep(HEARTBEAT_SECONDS)


@worker_api.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "worker_id": WORKER_ID}), 200


@worker_api.route("/spawn", methods=["POST"])
def spawn_container():
    data = request.get_json(force=True) or {}
    image = data.get("image")
    app_id = data.get("app_id")
    internal_port = int(data.get("internal_port", 80))

    if not image or not app_id:
        return jsonify({"status": "failed", "error": "image and app_id required"}), 400

    # Reject crashes that target our own framework namespaces.
    if not str(app_id).startswith("app-"):
        return jsonify({"status": "failed", "error": "app_id must start with app-"}), 400

    # Clean up a leftover container from a previous deploy of the same id.
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
            ports={f"{internal_port}/tcp": None},  # auto-assign free host port
            **CONTAINER_LIMITS,
        )
        container.reload()
        if container.status != "running":
            logs = container.logs(tail=30).decode("utf-8", "replace")
            container.remove(force=True)
            return jsonify({
                "status": "failed",
                "error": f"container exited immediately (status={container.status})",
                "logs": logs,
            }), 500
        host_port = container.ports[f"{internal_port}/tcp"][0]["HostPort"]
        print(f"Container {app_id} sandboxed and running, mapped to host port {host_port}")
        return jsonify({"status": "running", "port": int(host_port)}), 200
    except docker.errors.NotFound:
        return jsonify({"status": "failed", "error": f"image '{image}' not found"}), 404
    except docker.errors.APIError as e:
        return jsonify({"status": "failed", "error": str(e)}), 500
    except Exception as e:
        return jsonify({"status": "failed", "error": str(e)}), 500


@worker_api.route("/reap", methods=["POST"])
def reap_dead_containers():
    """Delete any exited app containers to free stolen resources."""
    removed = []
    try:
        for c in docker_client.containers.list(
            all=True, filters={"name": "app-"}
        ):
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
    print(f"WeCloud Worker daemon actively running on port {WORKER_PORT}...")
    print(f"Registering with coordinator at {COORDINATOR_URL} as '{WORKER_ID}'")
    worker_api.run(host="0.0.0.0", port=WORKER_PORT)