import { readAllowedAuth } from "@/lib/gateway-policy"

export async function proxyGatewayRequest(
  req: Request,
  path: string,
  options: { errorLabel?: string } = {},
): Promise<Response> {
  const { gateway, token } = readAllowedAuth(req)
  if (!gateway || !token) {
    return Response.json({ error: "未建立 Gateway 会话" }, { status: 401 })
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
    const text = await response.text()
    if (!response.ok) {
      return Response.json(
        { error: text.trim() || `${label}失败 (${response.status})`, status: response.status },
        { status: response.status },
      )
    }
    return new Response(response.status === 204 ? null : text || "{}", {
      status: response.status,
      headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
    })
  } catch (error) {
    return Response.json(
      { error: `无法连接 gateway: ${(error as Error).message}` },
      { status: 502 },
    )
  }
}
