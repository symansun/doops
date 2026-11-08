import assert from "node:assert/strict"
import test from "node:test"
import { CICD_GATEWAY_PLACEHOLDER, cicdGatewaySnippetValue, validateCicdGateway } from "./cicd-gateway.ts"

const SECRET = "synthetic-pass-3c9e" // gitleaks:allow -- synthetic test canary

const CASES = [
  // 合法地址
  { name: "https host", input: "https://gw.example.com", want: "https://gw.example.com" },
  { name: "https host with surrounding spaces", input: "  https://gw.example.com  ", want: "https://gw.example.com" },
  { name: "https host trailing slash", input: "https://gw.example.com/", want: "https://gw.example.com" },
  { name: "https host with port", input: "https://gw.example.com:8443", want: "https://gw.example.com:8443" },
  { name: "https host with path", input: "https://example.com/doops/", want: "https://example.com/doops" },
  { name: "uppercase host is normalized", input: "https://GW.Example.COM", want: "https://gw.example.com" },
  { name: "https IPv4", input: "https://10.0.0.5:42222", want: "https://10.0.0.5:42222" },
  { name: "http localhost", input: "http://localhost:42222", want: "http://localhost:42222" },
  { name: "http 127.0.0.1", input: "http://127.0.0.1:42222", want: "http://127.0.0.1:42222" },
  { name: "http [::1]", input: "http://[::1]:42222", want: "http://[::1]:42222" },
  // 带凭据
  { name: "username and password", input: `https://u:${SECRET}@gw.example.com`, error: /用户名或密码/ },
  { name: "username only", input: "https://deploy@gw.example.com", error: /用户名或密码/ },
  { name: "empty credentials", input: "https://:@gw.example.com", error: /用户名或密码/ },
  { name: "credentials on loopback http", input: `http://u:${SECRET}@127.0.0.1:42222`, error: /用户名或密码/ },
  // http
  { name: "http public host", input: "http://gw.example.com", error: /https:\/\// },
  { name: "http public IP", input: "http://192.0.2.10:42222", error: /https:\/\// },
  { name: "ws scheme", input: "ws://gw.example.com", error: /https:\/\// },
  { name: "wss scheme", input: "wss://gw.example.com", error: /https:\/\// },
  { name: "ftp scheme", input: "ftp://gw.example.com", error: /https:\/\// },
  { name: "loopback-looking public host", input: "http://localhost.example.com", error: /https:\/\// },
  // query
  { name: "query", input: `https://gw.example.com/?token=${SECRET}`, error: /查询参数/ },
  { name: "empty query", input: "https://gw.example.com?", error: /查询参数/ },
  // hash
  { name: "hash", input: `https://gw.example.com/#${SECRET}`, error: /片段/ },
  { name: "empty hash", input: "https://gw.example.com#", error: /片段/ },
  // 格式和特殊字符
  { name: "no scheme", input: "gw.example.com", error: /格式无效/ },
  { name: "empty host", input: "https://:443", error: /格式无效/ },
  { name: "double quote in path", input: "https://gw.example.com/\"; rm -rf /", error: /特殊字符/ },
  { name: "dollar in path", input: "https://gw.example.com/$HOME", error: /特殊字符/ },
  { name: "backtick in path", input: "https://gw.example.com/`id`", error: /特殊字符/ },
  { name: "space in path", input: "https://gw.example.com/a b", error: /特殊字符/ },
  { name: "newline in host", input: "https://gw.example.com\nDOOPS_API_KEY: x", error: /格式无效|特殊字符/ },
]

for (const item of CASES) {
  test(`cicd gateway: ${item.name}`, () => {
    const result = validateCicdGateway(item.input)
    const snippet = cicdGatewaySnippetValue(item.input)
    if (item.want) {
      assert.deepEqual(result, { ok: true, value: item.want })
      assert.deepEqual(snippet, { value: item.want, error: "" })
      return
    }
    assert.equal(result.ok, false)
    assert.match(result.error, item.error)
    // 不合法时片段继续用占位符，并给出中文提示；原文和其中的秘密值都不进入片段。
    assert.equal(snippet.value, CICD_GATEWAY_PLACEHOLDER)
    assert.equal(snippet.error, result.error)
    assert.ok(!snippet.value.includes(SECRET))
    assert.ok(!snippet.error.includes(SECRET))
    assert.ok(!snippet.error.includes(item.input.trim()))
  })
}

test("cicd gateway: an empty input keeps the placeholder without an error", () => {
  for (const input of ["", "   "]) {
    assert.deepEqual(cicdGatewaySnippetValue(input), { value: CICD_GATEWAY_PLACEHOLDER, error: "" })
  }
})

test("cicd gateway: an accepted value never carries characters that change shell or YAML meaning", () => {
  for (const item of CASES.filter((c) => c.want)) {
    const { value } = cicdGatewaySnippetValue(item.input)
    assert.match(value, /^https?:\/\/[A-Za-z0-9.:[\]_~/-]+$/, item.name)
  }
})
