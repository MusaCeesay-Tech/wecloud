#!/usr/bin/env python3
"""WeCloud developer CLI.

Deploy and inspect applications on the WeCloud peer-to-peer mesh.

Examples:
    python wecloud.py deploy --name hello --image nginx:alpine
    python wecloud.py list
    python wecloud.py status app-1234567890
    python wecloud.py lookup app-1234567890
"""

import argparse
import json
import os
import sys

import requests

COORDINATOR_URL = os.environ.get("WECLOUD_COORDINATOR", "http://localhost:8080")
SESSION_PATH = os.path.expanduser("~/.wecloud/session.json")


def _print_json(payload):
    print(json.dumps(payload, indent=2, sort_keys=True))


def load_session():
    try:
        with open(SESSION_PATH, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return {}


def save_session(data):
    os.makedirs(os.path.dirname(SESSION_PATH), exist_ok=True)
    with open(SESSION_PATH, "w", encoding="utf-8") as fh:
        json.dump(data, fh)


def clear_session():
    try:
        os.remove(SESSION_PATH)
    except OSError:
        pass


def auth_headers():
    token = load_session().get("session_token")
    return {"Authorization": f"Bearer {token}"} if token else {}


def _require_login():
    if not load_session().get("session_token"):
        sys.exit("Not logged in. Run: wecloud login --email you@example.com")


def cmd_login(args):
    try:
        req = requests.post(
            f"{COORDINATOR_URL}/api/auth/request", json={"email": args.email}, timeout=15
        )
    except requests.RequestException as e:
        sys.exit(f"Login request failed: {e}")
    if req.status_code != 200:
        sys.exit(f"Login request failed (HTTP {req.status_code}): {req.text}")

    link = req.json()
    print(f"Magic link for {link['email']}:")
    print(f"  {link['magic_link']}")

    verify = requests.get(
        f"{COORDINATOR_URL}/api/auth/verify",
        params={"token": link["token"]},
        timeout=15,
    )
    if verify.status_code != 200:
        sys.exit(f"Link verification failed (HTTP {verify.status_code}): {verify.text}")

    data = verify.json()
    save_session({"session_token": data["session_token"], "email": data["email"]})
    print(f"\nLogged in as {data['email']} ({data['user_id']}).")
    print(f"Credit balance: {data['credits']}")


def cmd_logout(args):
    try:
        requests.post(f"{COORDINATOR_URL}/api/auth/logout", headers=auth_headers(), timeout=10)
    except requests.RequestException:
        pass
    clear_session()
    print("Logged out.")


def cmd_whoami(args):
    _require_login()
    resp = requests.get(f"{COORDINATOR_URL}/api/auth/me", headers=auth_headers(), timeout=10)
    if resp.status_code != 200:
        sys.exit(f"whoami failed (HTTP {resp.status_code}): {resp.text}")
    data = resp.json()
    print(f"{data['email']} ({data['user_id']}) - {data['credits']} credits")


def cmd_balance(args):
    _require_login()
    resp = requests.get(f"{COORDINATOR_URL}/api/credits/balance", headers=auth_headers(), timeout=10)
    if resp.status_code != 200:
        sys.exit(f"balance failed (HTTP {resp.status_code}): {resp.text}")
    print(f"{resp.json()['credits']} credits")


def cmd_credits(args):
    _require_login()
    resp = requests.get(f"{COORDINATOR_URL}/api/credits/history", headers=auth_headers(), timeout=10)
    if resp.status_code != 200:
        sys.exit(f"credits failed (HTTP {resp.status_code}): {resp.text}")
    data = resp.json()
    print(f"Balance: {data['credits']} credits\n")
    for entry in data["entries"]:
        sign = "+" if entry["delta"] >= 0 else ""
        print(f"{entry['created_at']:<20} {sign}{entry['delta']:<5} {entry['reason']:<14} {entry['app_id'] or '-'}")


def cmd_deploy(args):
    _require_login()
    body = {
        "name": args.name,
        "image": args.image,
        "internal_port": args.internal_port,
    }
    try:
        resp = requests.post(
            f"{COORDINATOR_URL}/api/developer/deploy",
            json=body,
            headers=auth_headers(),
            timeout=120,
        )
    except requests.RequestException as e:
        sys.exit(f"Deploy request failed: {e}")

    if resp.status_code == 401:
        sys.exit("Session expired. Run: wecloud login --email you@example.com")
    if resp.status_code == 402:
        sys.exit(f"Insufficient credits: {resp.text}")
    if resp.status_code not in (200, 201):
        sys.exit(f"Deploy failed (HTTP {resp.status_code}): {resp.text}")

    data = resp.json()
    _print_json(data)

    if data.get("status") == "running":
        url = data.get("url", "").replace("http://", "")
        print(f"\nYour app is live ({data.get('credits')} credits left).")
        print(f"  {url}")
        print("  curl -H 'Host: %s' http://localhost:8081/" % url)


def cmd_list(args):
    resp = requests.get(f"{COORDINATOR_URL}/api/apps", timeout=10)
    if resp.status_code != 200:
        sys.exit(f"Failed to list apps (HTTP {resp.status_code}): {resp.text}")
    payload = resp.json()
    if not payload:
        print("No applications deployed yet.")
        return
    for app in payload:
        print(
            f"{app['id']:<24} {app['name']:<16} {app['image']:<24} "
            f"worker={app['worker_id'] or '-'} port={app['port'] or '-'} "
            f"status={app['status']}"
        )


def cmd_workers(args):
    resp = requests.get(f"{COORDINATOR_URL}/api/workers", timeout=10)
    if resp.status_code != 200:
        sys.exit(f"Failed to list workers (HTTP {resp.status_code}): {resp.text}")
    payload = resp.json()
    if not payload:
        print("No worker nodes connected.")
        return
    for w in payload:
        print(
            f"{w['id']:<16} {w['ip']:<28} cpu={w['cpu_cores']} "
            f"ram={w['ram_mb']} MB disk={w['disk_gb']} GB "
            f"apps={w['active_apps']} last_seen={w['last_seen']}"
        )


def cmd_status(args):
    resp = requests.get(f"{COORDINATOR_URL}/api/lookup/{args.app_id}", timeout=10)
    if resp.status_code == 404:
        sys.exit(f"App '{args.app_id}' not found in the mesh.")
    if resp.status_code != 200:
        sys.exit(f"Status check failed (HTTP {resp.status_code}): {resp.text}")
    print(f"{args.app_id} is reachable at http://{resp.text}")


cmd_lookup = cmd_status


def main():
    parser = argparse.ArgumentParser(prog="wecloud", description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)

    dep = sub.add_parser("deploy", help="Deploy a docker image to the mesh")
    dep.add_argument("--name", required=True, help="Human-readable app name")
    dep.add_argument("--image", required=True, help="Docker image, e.g., nginx:alpine")
    dep.add_argument("--internal-port", type=int, default=80, help="Port inside container")
    dep.set_defaults(func=cmd_deploy)

    lst = sub.add_parser("list", help="List deployed applications")
    lst.set_defaults(func=cmd_list)

    wrk = sub.add_parser("workers", help="List connected worker nodes")
    wrk.set_defaults(func=cmd_workers)

    login = sub.add_parser("login", help="Log in via a magic link")
    login.add_argument("--email", required=True, help="Your email address")
    login.set_defaults(func=cmd_login)

    logout = sub.add_parser("logout", help="Log out and clear the local session")
    logout.set_defaults(func=cmd_logout)

    whoami = sub.add_parser("whoami", help="Show the logged-in account")
    whoami.set_defaults(func=cmd_whoami)

    bal = sub.add_parser("balance", help="Show your credit balance")
    bal.set_defaults(func=cmd_balance)

    cred = sub.add_parser("credits", help="Show your credit ledger")
    cred.set_defaults(func=cmd_credits)

    for cmd, hlp in (("status", "App runtime status"), ("lookup", "Resolve an app origin")):
        st = sub.add_parser(cmd, help=hlp)
        st.add_argument("app_id", help="Application id, e.g. app-1721...")
        st.set_defaults(func=cmd_status)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()