import { isIP } from "node:net"

// browserAddress returns the browser address the Gateway should use for a
// console request, or null when no trustworthy address is available.
//
// The Next.js server sits behind a reverse proxy (an Ingress, optionally a
// CDN). Both append their peer to `X-Forwarded-For`, and the leftmost entries
// are whatever the browser chose to send, so the address is read from the
// right: with DOOPS_CONSOLE_TRUSTED_PROXY_HOPS=N, the Nth entry from the right
// is the address the outermost trusted proxy saw.
//
// DOOPS_CONSOLE_TRUSTED_PROXY_HOPS counts the proxies between the browser and
// this process (0 = direct, 1 = Ingress, 2 = CDN + Ingress). The default 0
// disables forwarding, as does any value that is not an integer in 1..16.
// The result must pass node:net `isIP`; anything else yields null.
export function browserAddress(req: Request, env: NodeJS.ProcessEnv = process.env): string | null {
  const hops = trustedProxyHops(env.DOOPS_CONSOLE_TRUSTED_PROXY_HOPS)
  if (hops === 0) return null

  // Accept IPv6 brackets around the address the proxy recorded, never a port.
  const entries = (req.headers.get("x-forwarded-for") || "")
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean)
  if (entries.length < hops) return null
  const candidate = entries[entries.length - hops].replace(/^\[|\]$/g, "")
  return isIP(candidate) === 0 ? null : candidate
}

export function trustedProxyHops(raw: string | undefined): number {
  const value = (raw ?? "").trim()
  if (!/^\d{1,2}$/.test(value)) return 0
  const hops = Number(value)
  return hops >= 1 && hops <= 16 ? hops : 0
}
