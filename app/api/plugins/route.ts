import { gatewayPolicyResponse, readAllowedAuth } from "@/lib/gateway-policy"
import { gatewayProxyErrorResponse, gatewayUnreachableResponse } from "@/lib/server/gateway-errors"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const SAFE_IF_NONE_MATCH = /^[A-Za-z0-9"*,./_=+\s-]{1,256}$/

// 只读转发 GET /v1/plugins（任意已登录用户），透传 If-None-Match 与 ETag，
// 并保留旧版 Gateway 返回的 404 以便前端显式提示升级而非静默降级。
export async function GET(req: Request) {
  try {
    const { gateway, token } = readAllowedAuth(req)
    if (!gateway || !token) {
      return Response.json(
        { error: "未建立 Gateway 会话", message: "未建立 Gateway 会话，请重新登录", status: 401 },
        { status: 401, headers: { "Cache-Control": "no-store" } },
      )
    }

    const headers: Record<string, string> = {
      Authorization: `Bearer ${token}`,
      Accept: "application/json",
    }
    const inm = req.headers.get("if-none-match")?.trim()
    if (inm && SAFE_IF_NONE_MATCH.test(inm)) {
      headers["If-None-Match"] = inm
    }

    const response = await fetch(`${gateway}/v1/plugins`, {
      method: "GET",
      headers,
      cache: "no-store",
      redirect: "manual",
      signal: req.signal,
    })

    const etag = response.headers.get("etag")
    if (response.status === 304) {
      const out = new Headers({ "Cache-Control": "no-store" })
      if (etag) out.set("ETag", etag)
      return new Response(null, { status: 304, headers: out })
    }

    if (!response.ok) {
      return await gatewayProxyErrorResponse(response, "插件配置服务")
    }

    const body = await response.text()
    const out = new Headers({
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "no-store",
    })
    if (etag) out.set("ETag", etag)
    return new Response(body, { status: 200, headers: out })
  } catch (error) {
    return gatewayPolicyResponse(error) || gatewayUnreachableResponse()
  }
}
