/** @type {import('next').NextConfig} */
const securityHeaders = [
  // The console is an operator surface: it must never be framed by another site.
  { key: "Content-Security-Policy", value: "frame-ancestors 'none'" },
  // Keep the legacy header for browsers without frame-ancestors support.
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "same-origin" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
]

const nextConfig = {
  output: "standalone",
  poweredByHeader: false,
  async headers() {
    return [{ source: "/:path*", headers: securityHeaders }]
  },
}

export default nextConfig
