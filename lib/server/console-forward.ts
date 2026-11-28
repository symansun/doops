import { createHmac } from "node:crypto"
import { browserAddress } from "@/lib/server/client-ip"

// Headers that carry the console's view of the browser address to the Gateway.
// The Gateway accepts them only with a valid signature made with the shared
// DOOPS_CONSOLE_FORWARD_SECRET, so a direct client cannot choose its own login
// bucket through them.
export const CLIENT_IP_HEADER = "X-DoOps-Client-IP"
export const CLIENT_IP_SIGNATURE_HEADER = "X-DoOps-Client-IP-Signature"

// forwardHeaders returns the signed client-address headers for a login request,
// or an empty object when forwarding is not configured or no address is known.
// The signature is `t=<unix seconds>,v1=<hex HMAC-SHA256(secret, t + "\n" + ip)>`.
export function forwardHeaders(req: Request, env: NodeJS.ProcessEnv = process.env, now: number = Date.now()): Record<string, string> {
  const secret = (env.DOOPS_CONSOLE_FORWARD_SECRET || "").trim()
  if (!secret) return {}
  const ip = browserAddress(req, env)
  if (!ip) return {}
  const timestamp = String(Math.floor(now / 1000))
  const mac = createHmac("sha256", secret).update(`${timestamp}\n${ip}`).digest("hex")
  return {
    [CLIENT_IP_HEADER]: ip,
    [CLIENT_IP_SIGNATURE_HEADER]: `t=${timestamp},v1=${mac}`,
  }
}
