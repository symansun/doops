// Gateway /v1/rpc checks login and the target grant before the WebSocket
// upgrade, so a rejection arrives as a plain HTTP status. Nothing was
// dispatched yet; the caller reports a terminal error, never an unknown outcome.
// The Gateway response body is not passed on; the message keeps only the status.

export interface GatewayUpgradeRejection {
  // JSON-RPC code matching what Gateway itself returns for the same reason.
  code: number
  // HTTP status the console answers with: 401/403/404 as given, anything else 502.
  status: number
  message: string
}

export function gatewayUpgradeRejection(upstreamStatus: number | undefined): GatewayUpgradeRejection {
  const status = Number.isInteger(upstreamStatus) ? upstreamStatus as number : 0
  switch (status) {
    case 401:
      return { code: -32001, status, message: "登录已失效，请重新登录（HTTP 401）" }
    case 403:
      return { code: -32003, status, message: "无权访问该目标，请联系管理员授权（HTTP 403）" }
    case 404:
      return { code: -32004, status, message: "目标不存在或已下线（HTTP 404）" }
    default:
      return { code: -32000, status: 502, message: `Gateway 拒绝连接（HTTP ${status || "未知"}）` }
  }
}
