/** @type {import('next').NextConfig} */
export function buildContentSecurityPolicy(production = process.env.NODE_ENV === "production") {
  return [
    "default-src 'self'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
    "object-src 'none'",
    // Next.js App Router emits inline hydration scripts; dev mode also uses eval for Fast Refresh.
    `script-src 'self' 'unsafe-inline'${production ? "" : " 'unsafe-eval'"}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    "connect-src 'self'",
    "worker-src 'self' blob:",
  ].join("; ")
}

export function buildSecurityHeaders(production = process.env.NODE_ENV === "production") {
  return [
    // The console is an operator surface: constrain script/connect origins and forbid framing.
    { key: "Content-Security-Policy", value: buildContentSecurityPolicy(production) },
    // Keep the legacy header for browsers without frame-ancestors support.
    { key: "X-Frame-Options", value: "DENY" },
    { key: "X-Content-Type-Options", value: "nosniff" },
    { key: "Referrer-Policy", value: "same-origin" },
    { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
    { key: "X-DNS-Prefetch-Control", value: "off" },
    { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
  ]
}

const apiSecurityHeaders = [
  { key: "Cross-Origin-Resource-Policy", value: "same-origin" },
]

const nextConfig = {
  output: "standalone",
  poweredByHeader: false,
  async headers() {
    return [
      { source: "/:path*", headers: buildSecurityHeaders() },
      { source: "/api/:path*", headers: apiSecurityHeaders },
    ]
  },
}

export default nextConfig
