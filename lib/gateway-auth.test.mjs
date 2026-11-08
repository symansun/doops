import assert from "node:assert/strict"
import test from "node:test"
import { gatewayAuthCode, gatewayAuthMessage } from "./gateway-auth.ts"
import { gatewayErrorBody, gatewayProxyErrorBody } from "./server/gateway-errors.ts"
import { gatewayUpgradeRejection } from "./server/gateway-upgrade.ts"

for (const [reason, expected] of [
  ["missing", /缺少/], ["invalid", /无效/], ["expired", /已过期/], ["revoked", /已撤销/],
  ["user_disabled", /用户已停用/], ["invalid_expiry", /到期数据异常/], ["unavailable", /认证服务暂时不可用/],
]) {
  test(`Gateway ${reason} keeps its cause across HTTP proxies and WebSocket rejection`, () => {
    const code = `gateway_auth_${reason}`
    const raw = JSON.stringify({ code, message: "synthetic-secret-looking-upstream-text", next_action: "synthetic-internals" })
    const status = reason === "unavailable" ? 503 : 401
    assert.equal(gatewayAuthCode(raw), code)
    for (const message of [gatewayAuthMessage(raw), gatewayErrorBody(status, raw).error,
      gatewayProxyErrorBody(status, raw).message, gatewayUpgradeRejection(status, raw).message]) {
      assert.match(message, expected)
      assert.doesNotMatch(message, /synthetic/)
    }
    assert.equal(gatewayUpgradeRejection(status, raw).status, status)
  })
}

test("unrecognized upstream text is never used as an authentication reason", () => {
  assert.equal(gatewayAuthCode("<html>secret-looking-body</html>"), undefined)
  assert.equal(gatewayAuthMessage("gateway_auth_future_reason"), undefined)
  assert.equal(gatewayErrorBody(401, "<html>secret-looking-body</html>").error, "身份验证失败，请检查登录信息")
})
