/** @type {import('next').NextConfig} */
const nextConfig = {
  // Static export so the Go server can embed the output. All API calls
  // go to the backend's /api/* routes which Next.js leaves alone.
  output: "export",
  // Trailing slash friendly for SPA fallback routing.
  trailingSlash: true,
  // Dev-mode rewrites: during `npm run dev`, API calls proxy to the Go
  // backend on :8080 so the dashboard dev server (:3000) still talks
  // to the real backend. Does nothing in production export mode.
  async rewrites() {
    if (process.env.NODE_ENV !== "development") return [];
    return [
      { source: "/api/:path*", destination: "http://localhost:8080/api/:path*" },
      { source: "/auth/:path*", destination: "http://localhost:8080/auth/:path*" },
    ];
  },
  images: { unoptimized: true },
};

module.exports = nextConfig;
