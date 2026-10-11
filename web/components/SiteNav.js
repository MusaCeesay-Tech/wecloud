import Link from "next/link";

export default function SiteNav() {
  return (
    <nav className="nav">
      <div className="container nav-inner">
        <Link href="/" className="brand">
          <span className="dot" />
          WeCloud
        </Link>
        <div className="nav-links">
          <Link href="/#how">How it works</Link>
          <Link href="/#credits">Credits</Link>
          <Link href="/#start">Get started</Link>
          <Link href="/dashboard">Dashboard</Link>
          <Link href="/login" className="btn btn-primary">
            Log in
          </Link>
        </div>
      </div>
    </nav>
  );
}
