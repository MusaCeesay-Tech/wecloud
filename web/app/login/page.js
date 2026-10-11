"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import SiteNav from "@/components/SiteNav";
import { apiJSON } from "@/lib/api";

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [link, setLink] = useState("");
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function requestLink(e) {
    e.preventDefault();
    setError("");
    setLink("");
    setBusy(true);
    const { ok, status, data } = await apiJSON("/api/auth/request", {
      method: "POST",
      body: JSON.stringify({ email }),
    });
    setBusy(false);
    if (!ok) {
      setError(data?.error || `Could not send the magic link (HTTP ${status})`);
      return;
    }
    setLink(data.magic_link);
    setToken(data.token);
  }

  async function verify() {
    if (!token) return;
    setError("");
    setBusy(true);
    const { ok, status } = await apiJSON(
      `/api/auth/verify?token=${encodeURIComponent(token)}`
    );
    setBusy(false);
    if (!ok) {
      setError(`Link is invalid or expired (HTTP ${status}). Request a new one.`);
      return;
    }
    router.push("/dashboard");
    router.refresh();
  }

  return (
    <>
      <SiteNav />
      <div className="container auth-wrap">
        <div className="panel">
          <h2>Log in to WeCloud</h2>
          <p className="hint">
            Passwordless — we&apos;ll email you a magic link. New accounts get{" "}
            <strong>100 credits</strong>.
          </p>

          {error && <div className="alert alert-err">{error}</div>}

          <form onSubmit={requestLink}>
            <label className="field">
              Email address
              <input
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                autoFocus
              />
            </label>
            <div style={{ marginTop: 14 }}>
              <button className="btn btn-primary" disabled={busy}>
                {busy ? "Sending…" : "Send magic link"}
              </button>
            </div>
          </form>

          {link && (
            <div className="alert alert-ok" style={{ marginTop: 18 }}>
              <div style={{ marginBottom: 8 }}>
                Email delivery isn&apos;t wired up yet, so use the link directly:
              </div>
              <div className="mono" style={{ marginBottom: 12, wordBreak: "break-all" }}>
                <a href={link}>{link}</a>
              </div>
              <button className="btn btn-primary" onClick={verify} disabled={busy}>
                Verify &amp; open dashboard
              </button>
            </div>
          )}
        </div>

        <p className="muted" style={{ textAlign: "center" }}>
          Prefer the terminal? <code>python cli\wecloud.py login --email you@example.com</code>
        </p>
      </div>
    </>
  );
}
