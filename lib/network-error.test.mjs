import assert from "node:assert/strict"
import test from "node:test"
import { listUsers } from "./admin.ts"
import { assetRequest } from "./assets.ts"
import { callTool, fetchAudit, fetchTargets, login, persistTokenSession, restoreServerSession } from "./client.ts"
import { createCredentialBundle, getCredentialBundle, listCredentials, verifyCredentialVersion } from "./credentials.ts"
import {
  NETWORK_FAILURE_MESSAGE,
  NetworkError,
  consoleFetch,
  httpGatewayErrorMessage,
  isNetworkError,
  readJsonObject,
  subscribeNetworkStatus,
} from "./network-error.ts"

const session = { gateway: "https://gateway.example.test" }
const target = { cluster: "doops-zyj", instance: "zyj", key: "doops-zyj/zyj" }
const scope = { cluster: "doops-zyj", instance: "zyj", namespace: "agents-test", use: "opaqueSecret", workload: { kind: "", name: "" } }

test("Given fetch gets no HTTP response, When consoleFetch runs, Then one Chinese network error is thrown", async (t) => {
  for (const cause of [new TypeError("Failed to fetch"), new TypeError("NetworkError when attempting to fetch resource."), new TypeError("Load failed")]) {
    t.mock.method(globalThis, "fetch", async () => { throw cause })
    await assert.rejects(consoleFetch("/api/targets"), (err) => {
      assert.ok(isNetworkError(err), cause.message)
      assert.ok(err instanceof TypeError, "callers that treat a fetch TypeError as an interrupted request keep working")
      assert.equal(err.message, NETWORK_FAILURE_MESSAGE)
      return true
    })
    t.mock.restoreAll()
  }
})

test("Given fetch returns an HTTP response, When consoleFetch runs, Then the response is returned unchanged", async (t) => {
  for (const status of [200, 401, 403, 404, 409, 500, 502, 503]) {
    const original = new Response(JSON.stringify({ error: `gateway said ${status}` }), { status })
    t.mock.method(globalThis, "fetch", async () => original)
    assert.equal(await consoleFetch("/api/targets"), original, String(status))
    t.mock.restoreAll()
  }
})

test("Given a request the caller aborted or a non-network failure, When consoleFetch runs, Then the original error is kept", async (t) => {
  const controller = new AbortController()
  controller.abort()
  const abort = new DOMException("The operation was aborted.", "AbortError")
  for (const [name, cause, init] of [
    ["aborted signal", new TypeError("Failed to fetch"), { signal: controller.signal }],
    ["AbortError", abort, undefined],
    ["other error", new RangeError("bad init"), undefined],
  ]) {
    t.mock.method(globalThis, "fetch", async () => { throw cause })
    await assert.rejects(consoleFetch("/api/targets", init), (err) => err === cause, name)
    t.mock.restoreAll()
  }
})

// Every console request function, so each one shows the same text when offline.
const REQUESTS = [
  ["targets", () => fetchTargets(session)],
  ["audit", () => fetchAudit(session, { limit: "10" })],
  ["session restore", () => restoreServerSession()],
  ["credential list", () => listCredentials(session, target, "managed")],
  ["credential verify", () => verifyCredentialVersion(session, "cred_1", { version_id: "credver_1", session_id: "verify-1", scope })],
  ["bundle view", () => getCredentialBundle(session, "release")],
  ["bundle create", () => createCredentialBundle(session, { name: "release", scope: "personal", credentialIDs: ["cred_1"] })],
  ["admin users", () => listUsers(session)],
  ["rpc", () => callTool(session, { cluster: "doops-zyj", instance: "zyj", tool: "doops_shell", arguments: { session_id: "s1", command: "uptime" } }, () => {})],
]

test("Given the browser is offline, When any console request runs, Then the Chinese network message is shown", async (t) => {
  for (const [name, run] of REQUESTS) {
    t.mock.method(globalThis, "fetch", async () => { throw new TypeError("Failed to fetch") })
    await assert.rejects(run, (err) => {
      assert.equal(err.message, NETWORK_FAILURE_MESSAGE, name)
      assert.doesNotMatch(err.message, /Failed to fetch/, name)
      return true
    })
    t.mock.restoreAll()
  }
})

test("Given an HTTP error response, When a console request runs, Then its own message is not replaced by the network text", async (t) => {
  for (const [name, run] of REQUESTS) {
    t.mock.method(globalThis, "fetch", async () => new Response(JSON.stringify({ error: "Forbidden", status: 403 }), { status: 403 }))
    let message = ""
    try {
      const result = await run()
      message = result?.error ?? ""
    } catch (err) {
      message = err.message
    }
    assert.ok(message, `${name} reported the HTTP failure`)
    assert.notEqual(message, NETWORK_FAILURE_MESSAGE, name)
    t.mock.restoreAll()
  }
})

test("Given offline and online events, When the console subscribes, Then each change is reported once until unsubscribed", () => {
  const events = new EventTarget()
  const seen = []
  const unsubscribe = subscribeNetworkStatus(events, (online) => seen.push(online))
  events.dispatchEvent(new Event("offline"))
  events.dispatchEvent(new Event("online"))
  unsubscribe()
  events.dispatchEvent(new Event("offline"))
  assert.deepEqual(seen, [false, true])
  assert.ok(new NetworkError() instanceof Error)
})

test("Given an HTML 502/503/504 proxy page, When console requests run, Then readable Chinese errors are returned without raw JSON SyntaxError", async (t) => {
  const html502 = "<html><head><title>502 Bad Gateway</title></head><body><h1>502 Bad Gateway</h1></body></html>"
  const htmlRequests = [
    ["login", () => login(session.gateway, "admin", "synthetic-pass")],
    ["restoreServerSession", () => restoreServerSession()],
    ["persistTokenSession", () => persistTokenSession(session.gateway, "synthetic-token")],
    ["fetchTargets", () => fetchTargets(session)],
    ["fetchAudit", () => fetchAudit(session, { limit: "10" })],
    ["assetRequest", () => assetRequest("workspace")],
    ["callTool", () => callTool(session, { cluster: "doops-zyj", instance: "zyj", tool: "doops_shell", arguments: { session_id: "s1", command: "uptime" } }, () => {})],
  ]
  for (const [name, run] of htmlRequests) {
    t.mock.method(globalThis, "fetch", async () => new Response(html502, { status: 502, headers: { "Content-Type": "text/html" } }))
    let message = ""
    try {
      const result = await run()
      message = result?.error ?? ""
    } catch (err) {
      message = err.message
    }
    assert.match(message, /HTTP 502/, name)
    assert.doesNotMatch(message, /Unexpected token|is not valid JSON/i, name)
    t.mock.restoreAll()
  }
  assert.equal(await readJsonObject(new Response(html502, { status: 502 })), null)
  assert.match(httpGatewayErrorMessage(503, "读取失败"), /HTTP 503/)
})
