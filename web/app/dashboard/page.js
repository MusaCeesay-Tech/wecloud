"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { apiJSON, shortId, timeAgo } from "@/lib/api";

export default function Dashboard() {
  const router = useRouter();
  const [me, setMe] = useState(null);
  const [apps, setApps] = useState([]);
  const [workers, setWorkers] = useState([]);
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(true);

  const [form, setForm] = useState({ name: "", image: "nginx:alpine", internal_port: 80 });
  const [deploying, setDeploying] = useState(false);
  const [deployMsg, setDeployMsg] = useState(null);

  const load = useCallback(async () => {
    const meRes = await apiJSON("/api/auth/me");
    if (!meRes.ok) {
      router.replace("/login");
      return;
    }
    setMe(meRes.data);

    const [appsRes, workersRes, creditsRes] = await Promise.all([
      apiJSON("/api/apps?mine=1"),
      apiJSON("/api/workers"),
      apiJSON("/api/credits/history"),
    ]);
    if (appsRes.ok) setApps(appsRes.data || []);
    if (workersRes.ok) setWorkers(workersRes.data || []);
    if (creditsRes.ok) setEntries(creditsRes.data.entries || []);
    setLoading(false);
  }, [router]);

  useEffect(() => {
    load();
    const t = setInterval(load, 15000); // live-ish refresh
    return () => clearInterval(t);
  }, [load]);

  async function deploy(e) {
    e.preventDefault();
    setDeploying(true);
    setDeployMsg(null);
    const { ok, status, data } = await apiJSON("/api/developer/deploy", {
      method: "POST",
      body: JSON.stringify({
        name: form.name,
        image: form.image,
        internal_port: Number(form.internal_port) || 80,
      }),
    });
    setDeploying(false);
    if (!ok) {
      setDeployMsg({
        kind: "err",
        text:
          data?.error ||
          (status === 402
            ? "Not enough credits to deploy."
            : `Deploy failed (HTTP ${status}).`),
      });
      return;
    }
    setDeployMsg({
      kind: "ok",
      text: `Deployed ${data.app_id} (${data.status}). ${data.credits} credits left.`,
    });
    setForm((f) => ({ ...f, name: "" }));
    load();
  }

  async function logout() {
    await apiJSON("/api/auth/logout", { method: "POST" });
    router.replace("/login");
  }

  if (loading || !me) {
    return (
      <div className="container">
        <p className="empty" style={{ marginTop: 60 }}>
          Loading…
        </p>
      </div>
    );
  }

  const running = apps.filter((a) => a.status === "running").length;

  return (
    <>
      <nav className="nav">
        <div className="container nav-inner">
          <Link href="/" className="brand">
            <span className="dot" />
            WeCloud
          </Link>
          <div className="nav-links">
            <span className="muted mono">{me.email}</span>
            <button className="btn" onClick={logout}>
              Log out
            </button>
          </div>
        </div>
      </nav>

      <div className="container">
        <div className="page-head">
          <div>
            <h1>Dashboard</h1>
            <span className="muted mono">{me.user_id}</span>
          </div>
        </div>

        <div className="grid-cards">
          <div className="stat">
            <div className="label">Credits</div>
            <div className="value">{me.credits}</div>
          </div>
          <div className="stat">
            <div className="label">Your apps running</div>
            <div className="value">
              {running}
              <span className="muted" style={{ fontSize: 16 }}> / {apps.length}</span>
            </div>
          </div>
          <div className="stat">
            <div className="label">Worker nodes online</div>
            <div className="value">{workers.length}</div>
          </div>
          <div className="stat">
            <div className="label">Account</div>
            <div className="value small">{me.email}</div>
          </div>
        </div>

        <div className="panel">
          <h2>Deploy an app</h2>
          <p className="hint">
            Any public Docker image works. Each successful deploy costs 1 credit.
          </p>
          {deployMsg && (
            <div className={`alert ${deployMsg.kind === "ok" ? "alert-ok" : "alert-err"}`}>
              {deployMsg.text}
            </div>
          )}
          <form onSubmit={deploy}>
            <div className="form-row">
              <label className="field">
                Name
                <input
                  required
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="hello"
                />
              </label>
              <label className="field">
                Docker image
                <input
                  required
                  value={form.image}
                  onChange={(e) => setForm({ ...form, image: e.target.value })}
                  placeholder="nginx:alpine"
                />
              </label>
              <label className="field">
                Internal port
                <input
                  type="number"
                  value={form.internal_port}
                  onChange={(e) => setForm({ ...form, internal_port: e.target.value })}
                />
              </label>
              <button className="btn btn-primary" disabled={deploying}>
                {deploying ? "Deploying…" : "Deploy"}
              </button>
            </div>
          </form>
        </div>

        <div className="panel">
          <h2>Your applications</h2>
          <p className="hint">Apps you have deployed to the mesh.</p>
          {apps.length === 0 ? (
            <p className="empty">No apps yet — deploy your first one above.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>App</th>
                  <th>Name</th>
                  <th>Image</th>
                  <th>Worker</th>
                  <th>Port</th>
                  <th>URL</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {apps.map((a) => (
                  <tr key={a.id}>
                    <td className="mono" title={a.id}>
                      {shortId(a.id)}
                    </td>
                    <td>{a.name || "-"}</td>
                    <td className="mono">{a.image}</td>
                    <td className="mono">{a.worker_id || "-"}</td>
                    <td className="mono">{a.port || "-"}</td>
                    <td className="mono">
                      {a.status === "running" && a.url ? (
                        <a href={a.url} target="_blank" rel="noreferrer">
                          {a.url}
                        </a>
                      ) : (
                        "-"
                      )}
                    </td>
                    <td>
                      <span className={`badge badge-${a.status}`}>{a.status}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div className="panel">
          <h2>Credit activity</h2>
          <p className="hint">Append-only ledger. Balance = sum of all entries.</p>
          {entries.length === 0 ? (
            <p className="empty">No activity yet.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>When</th>
                  <th>Change</th>
                  <th>Reason</th>
                  <th>App</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e, i) => (
                  <tr key={i}>
                    <td className="muted">{timeAgo(e.created_at)}</td>
                    <td
                      className="mono"
                      style={{ color: e.delta >= 0 ? "var(--ok)" : "var(--err)" }}
                    >
                      {e.delta >= 0 ? "+" : ""}
                      {e.delta}
                    </td>
                    <td className="mono">{e.reason}</td>
                    <td className="mono">{e.app_id ? shortId(e.app_id) : "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div className="panel">
          <h2>Network workers</h2>
          <p className="hint">Volunteer machines currently connected to the mesh.</p>
          {workers.length === 0 ? (
            <p className="empty">No workers online.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Node</th>
                  <th>CPU</th>
                  <th>RAM</th>
                  <th>Disk</th>
                  <th>Apps</th>
                  <th>Last seen</th>
                </tr>
              </thead>
              <tbody>
                {workers.map((w) => (
                  <tr key={w.id}>
                    <td className="mono">{w.id}</td>
                    <td>{w.cpu_cores} cores</td>
                    <td>{w.ram_mb} MB</td>
                    <td>{w.disk_gb} GB</td>
                    <td>{w.active_apps}</td>
                    <td className="muted">{timeAgo(w.last_seen)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <footer className="footer">
          <div className="row-between">
            <span>WeCloud dashboard</span>
            <Link href="/">← Back to home</Link>
          </div>
        </footer>
      </div>
    </>
  );
}
