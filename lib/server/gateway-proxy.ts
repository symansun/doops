import { readAllowedAuth } from "@/lib/gateway-policy"
import { gatewayProxyErrorResponse, gatewayUnreachableResponse } from "@/lib/server/gateway-errors"
import { rejectCrossOriginMutation } from "@/lib/server/same-origin"

const SAFE_PROXY_SEGMENT = /^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$/
const CONTROL_OR_BACKSLASH = /[\x00-\x1f\x7f\\]/

// resolveSafeGatewaySubpath validates dynamic [...path] segments before
// forwarding to the Gateway, blocking dot-segment traversal (".", "..",
// "%2e%2e") and path/query smuggling.
export function resolveSafeGatewaySubpath(segments?: string[]): string | null {
  if (!segments || segments.length === 0) return ""
  if (segments.length > 8) return null
  const encoded: string[] = []
  for (const raw of segments) {
    if (
      typeof raw !== "string" ||
      !SAFE_PROXY_SEGMENT.test(raw) ||
      raw === "." ||
      raw === ".." ||
      raw.includes("..")
    ) {
      return null
    }
    encoded.push(encodeURIComponent(raw))
  }
  return `/${encoded.join("/")}`
}

function isSafeGatewayRequestPath(path: string): boolean {
  if (!path || CONTROL_OR_BACKSLASH.test(path)) return false
  const [pathname] = path.split("?", 1)
  if (!pathname.startsWith("/v1/") || pathname.includes("//")) return false
  for (const rawSegment of pathname.slice(1).split("/")) {
    let decoded: string
    try {
      decoded = decodeURIComponent(rawSegment)
    } catch {
      return false
    }
    if (
      !decoded ||
      decoded === "." ||
      decoded === ".." ||
      decoded.includes("/") ||
      decoded.includes("\\") ||
      CONTROL_OR_BACKSLASH.test(decoded)
    ) {
      return false
    }
  }
  return true
}

export async function proxyGatewayRequest(
  req: Request,
  path: string,
  options: { errorLabel?: string; csvDownload?: boolean } = {},
): Promise<Response> {
  const rejected = rejectCrossOriginMutation(req)
  if (rejected) return rejected
  if (!isSafeGatewayRequestPath(path)) {
    return Response.json({ error: "请求路径无效" }, { status: 400 })
  }
  const { gateway, token } = readAllowedAuth(req)
  if (!gateway || !token) {
    return Response.json({ error: "未建立 Gateway 会话", message: "未建立 Gateway 会话，请重新登录", status: 401 }, { status: 401 })
  }

  const init: RequestInit = {
    method: req.method,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    cache: "no-store",
    redirect: "manual",
    signal: req.signal,
  }
  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = await req.text()
  }

  const label = options.errorLabel || "Gateway 请求"
  try {
    const response = await fetch(`${gateway}${path}`, init)
    // 错误响应体可能带 Go/SQLite 错误、HTML 错误页或英文说明，只保留类别和中文说明。
    if (!response.ok) return await gatewayProxyErrorResponse(response, label)
    if (options.csvDownload) {
      if (response.headers.get("content-type")?.split(";")[0].trim().toLowerCase() !== "text/csv") {
        return Response.json({ error: "Gateway 返回了无效的 CSV 回执" }, { status: 502 })
      }
      return new Response(response.body, { status: response.status, headers: {
        "Content-Type": "text/csv; charset=utf-8",
        "Content-Disposition": 'attachment; filename="asset-coverage.csv"',
        "Cache-Control": "no-store",
        "X-Content-Type-Options": "nosniff",
      } })
    }
    const text = await response.text()
    return new Response(response.status === 204 ? null : text || "{}", {
      status: response.status,
      headers: {
        "Content-Type": "application/json",
        "Cache-Control": "no-store",
        "X-Content-Type-Options": "nosniff",
      },
    })
  } catch {
    return gatewayUnreachableResponse()
  }
}
