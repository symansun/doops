const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"])

// rejectCrossOriginMutation refuses cookie-authenticated changes whose Origin
// is not this console. Sibling sites on the same registrable domain are a
// different origin and are rejected.
export function rejectCrossOriginMutation(req: Request): Response | null {
  if (!UNSAFE_METHODS.has(req.method.toUpperCase())) return null
  const expected = consoleOrigin(req)
  const origin = (req.headers.get("origin") || "").trim()
  if (!expected || origin !== expected) {
    return Response.json({ error: "跨站请求被拒绝" }, { status: 403 })
  }
  const site = (req.headers.get("sec-fetch-site") || "").trim().toLowerCase()
  if (site && site !== "same-origin" && site !== "none") {
    return Response.json({ error: "跨站请求被拒绝" }, { status: 403 })
  }
  return null
}

function consoleOrigin(req: Request): string | null {
  const configured = process.env.DOOPS_CONSOLE_ORIGIN?.trim()
  if (configured) {
    try {
      return new URL(configured).origin
    } catch {
      return null
    }
  }
  let url: URL
  try {
    url = new URL(req.url)
  } catch {
    return null
  }
  const host = (req.headers.get("host") || "").split(",")[0].trim()
  if (host && host !== url.host) return `${url.protocol}//${host}`
  return url.origin
}
