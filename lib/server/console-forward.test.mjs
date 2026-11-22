import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-console-forward-test-"))
after(() => rmSync(fixture, { recursive: true, force: true }))
function transpile(name) {
  const output = join(fixture, `${name}.mjs`)
  const source = readFileSync(join(root, `lib/server/${name}.ts`), "utf8").replace(
    /from "@\/lib\/server\/client-ip"/,
    'from "./client-ip.mjs"',
  )
  writeFileSync(output, TypeScript.transpile(source, {
    module: TypeScript.ModuleKind.ESNext,
    target: TypeScript.ScriptTarget.ES2022,
  }))
  return output
}
transpile("client-ip")
const { browserAddress, trustedProxyHops } = await import(pathToFileURL(join(fixture, "client-ip.mjs")).href)
const { forwardHeaders, CLIENT_IP_HEADER, CLIENT_IP_SIGNATURE_HEADER } = await import(
  pathToFileURL(transpile("console-forward")).href
)

const SECRET = "test-only-forward-secret"

function loginRequest(forwardedFor) {
  const headers = forwardedFor === undefined ? {} : { "X-Forwarded-For": forwardedFor }
  return new Request("http://console.test/api/login", { method: "POST", headers })
}

test("hop count 0 or invalid disables forwarding", () => {
  const req = loginRequest("198.51.100.7")
  for (const raw of [undefined, "", "0", "-1", "1.5", "x", "17"]) {
    assert.equal(trustedProxyHops(raw), 0, String(raw))
    assert.equal(browserAddress(req, { DOOPS_CONSOLE_TRUSTED_PROXY_HOPS: raw }), null, String(raw))
  }
})

test("one trusted proxy uses the rightmost hop", () => {
  const env = { DOOPS_CONSOLE_TRUSTED_PROXY_HOPS: "1" }
  // The browser-supplied left entries are ignored.
  assert.equal(browserAddress(loginRequest("203.0.113.1, 198.51.100.7"), env), "198.51.100.7")
  assert.equal(browserAddress(loginRequest("198.51.100.7"), env), "198.51.100.7")
  assert.equal(browserAddress(loginRequest("2001:db8::7"), env), "2001:db8::7")
  assert.equal(browserAddress(loginRequest("[2001:db8::7]"), env), "2001:db8::7")
  assert.equal(browserAddress(loginRequest("not-an-ip"), env), null)
  assert.equal(browserAddress(loginRequest(""), env), null)
  assert.equal(browserAddress(loginRequest(undefined), env), null)
})

test("CDN plus Ingress reads the hop in front of the Ingress", () => {
  const env = { DOOPS_CONSOLE_TRUSTED_PROXY_HOPS: "2" }
  assert.equal(browserAddress(loginRequest("10.9.9.9, 198.51.100.7, 10.0.0.2"), env), "198.51.100.7")
  assert.equal(browserAddress(loginRequest("198.51.100.7, 10.0.0.2"), env), "198.51.100.7")
  assert.equal(browserAddress(loginRequest("10.0.0.2"), env), null)
})

test("no secret or no address sends no forwarding headers", () => {
  const req = loginRequest("198.51.100.7")
  assert.deepEqual(forwardHeaders(req, { DOOPS_CONSOLE_TRUSTED_PROXY_HOPS: "1" }), {})
  assert.deepEqual(forwardHeaders(req, { DOOPS_CONSOLE_FORWARD_SECRET: SECRET }), {})
  assert.deepEqual(forwardHeaders(req, { DOOPS_CONSOLE_FORWARD_SECRET: "  ", DOOPS_CONSOLE_TRUSTED_PROXY_HOPS: "1" }), {})
})

test("forwarding sends the address and a timestamped HMAC of it", async () => {
  const headers = forwardHeaders(loginRequest("198.51.100.7, 10.0.0.2"), {
    DOOPS_CONSOLE_FORWARD_SECRET: SECRET,
    DOOPS_CONSOLE_TRUSTED_PROXY_HOPS: "1",
  }, 1_700_000_000_500)
  assert.equal(headers[CLIENT_IP_HEADER], "10.0.0.2")
  const match = /^t=(\d+),v1=([0-9a-f]{64})$/.exec(headers[CLIENT_IP_SIGNATURE_HEADER])
  assert.ok(match, headers[CLIENT_IP_SIGNATURE_HEADER])
  assert.equal(match[1], "1700000000")
  const { createHmac } = await import("node:crypto")
  assert.equal(match[2], createHmac("sha256", SECRET).update("1700000000\n10.0.0.2").digest("hex"))
})
