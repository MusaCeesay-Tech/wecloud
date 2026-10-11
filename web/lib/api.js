export async function apiJSON(path, options = {}) {
  const headers = { "Content-Type": "application/json", ...(options.headers || {}) };
  const res = await fetch(path, { ...options, headers, cache: "no-store" });
  let data = null;
  try {
    data = await res.json();
  } catch {
    /* non-JSON (e.g. plain-text origin) responses */
  }
  return { ok: res.ok, status: res.status, data };
}

export function shortId(id) {
  return id ? id.replace(/^app-/, "").slice(-8) : "-";
}

export function timeAgo(iso) {
  if (!iso) return "-";
  const t = Date.parse(iso.endsWith("Z") ? iso : iso + "Z");
  if (Number.isNaN(t)) return iso;
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}
