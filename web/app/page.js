import Link from "next/link";
import SiteNav from "@/components/SiteNav";

export default function Home() {
  return (
    <>
      <SiteNav />

      <header className="hero">
        <div className="container">
          <span className="pill">
            <span className="dot-live" />
            Open-source · peer-to-peer · free
          </span>
          <h1 className="hero-title">
            The cloud that <span className="grad">belongs to everyone</span>.
          </h1>
          <p className="hero-sub">
            Deploy web apps to a global mesh of volunteer machines. Give compute to
            the network, earn credits, and spend them to run your own. No data
            centers, no bills — just peers.
          </p>
          <div className="cta-row">
            <Link href="/dashboard" className="btn btn-primary">
              Open the dashboard
            </Link>
            <Link href="/#how" className="btn">
              See how it works
            </Link>
          </div>
        </div>
      </header>

      <section id="how" className="container" style={{ paddingTop: 24 }}>
        <h2>How it works</h2>
        <p className="hero-sub" style={{ margin: "0 0 22px" }}>
          WeCloud turns idle laptops, desktops and servers into a shared compute
          mesh using three small pieces.
        </p>
        <div className="grid" style={{ gridTemplateColumns: "repeat(auto-fit,minmax(260px,1fr))" }}>
          <div className="card">
            <div className="step-n">01 — Give</div>
            <h3>Run a host daemon</h3>
            <p>
              Install the worker, and the machine starts accepting app containers
              inside a hard Docker sandbox. Every hour you host earns credits.
            </p>
          </div>
          <div className="card">
            <div className="step-n">02 — Take</div>
            <h3>Deploy an image</h3>
            <p>
              Point WeCloud at any Docker image. The scheduler bin-packs it onto
              the healthiest peer and confirms it&apos;s actually running.
            </p>
          </div>
          <div className="card">
            <div className="step-n">03 — Reach</div>
            <h3>Get a live URL</h3>
            <p>
              The mesh router maps <code>app-*.wecloud</code> to the worker origin,
              so your app is reachable without touching a firewall.
            </p>
          </div>
        </div>
      </section>

      <section id="credits" className="container" style={{ paddingTop: 26 }}>
        <div className="panel">
          <h2>Give &amp; take, in credits</h2>
          <p className="hint">
            The credit ledger keeps the mesh fair: resources you share come back as
            compute you can spend.
          </p>
          <table>
            <thead>
              <tr>
                <th>Event</th>
                <th>Credits</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td>Signup bonus</td>
                <td className="mono" style={{ color: "var(--ok)" }}>+100</td>
                <td className="muted">Once, when your account is created</td>
              </tr>
              <tr>
                <td>Deploy fee</td>
                <td className="mono" style={{ color: "var(--err)" }}>-1</td>
                <td className="muted">Per successful deployment</td>
              </tr>
              <tr>
                <td>Host reward</td>
                <td className="mono" style={{ color: "var(--brand-2)" }}>+</td>
                <td className="muted">For hosting other developers&apos; apps</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section id="start" className="container">
        <h2>Get started</h2>
        <p className="hero-sub" style={{ margin: "0 0 18px" }}>
          Deploy your first app from the CLI in seconds.
        </p>
        <pre className="term">
          <span className="c"># log in (passwordless magic link)</span>
          {"\n"}python cli\wecloud.py login --email you@example.com
          {"\n\n"}
          <span className="c"># deploy any docker image</span>
          {"\n"}python cli\wecloud.py deploy --name hello --image nginx:alpine
          {"\n\n"}
          <span className="c"># check your credits</span>
          {"\n"}python cli\wecloud.py credits
        </pre>
      </section>

      <footer className="footer">
        <div className="container row-between">
          <span>WeCloud — a Give-and-Take peer-to-peer cloud.</span>
          <span>
            <Link href="/dashboard">Dashboard</Link>
            {" · "}
            <Link href="https://github.com/musacee">GitHub</Link>
          </span>
        </div>
      </footer>
    </>
  );
}
