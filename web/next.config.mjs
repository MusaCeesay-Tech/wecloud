/** @type {import('next').NextConfig} */
const coordinator = process.env.WECLOUD_COORDINATOR || "http://localhost:8080";

const nextConfig = {
  reactStrictMode: true,
  async rewrites() {
    // Proxy API + auth calls to the coordinator so the browser talks to a single
    // origin (no CORS, cookies "just work").
    return [{ source: "/api/:path*", destination: `${coordinator}/api/:path*` }];
  },
};

export default nextConfig;
