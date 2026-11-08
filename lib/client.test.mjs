import assert from "node:assert/strict"
import test from "node:test"
import { fileURLToPath } from "node:url"
import { sourceLoader } from "./test-source.mjs"

const load = sourceLoader(fileURLToPath(new URL("../", import.meta.url)))
const { callTool, restoreServerSession, login, persistTokenSession, sessionIsAdmin } = await load("lib/client.ts")
const { SESSION_GENERATION_HEADER, SessionChangedError } = await load("lib/auth-session.ts")
const session = { gateway: "https://gateway.example.test", token: "synthetic-token", sessionGeneration: "generation-A" }
const operation = { cluster: "test", instance: "node", tool: "doops_agent_prompt", arguments: { session_id: "browser-test", operation: "apply", instruction: "synthetic deployment" } }

function mockResponse(t, values, status = 200) {
  t.mock.method(globalThis, "fetch", async (_url, request) => {
    assert.equal(request.headers["Content-Type"], "application/json")
    assert.equal(request.headers.Authorization, undefined)
    assert.equal(request.headers[SESSION_GENERATION_HEADER], session.sessionGeneration)
    assert.equal(JSON.parse(request.body).arguments.operation, "apply")
    return new Response(status === 200 ? values.join("\n") + "\n" : JSON.stringify({ error: "Gateway authentication rejected" }), { status })
  })
}

test("browser receives assistant text separately from tool output and final result", async (t) => {
  mockResponse(t, [JSON.stringify({ type: "output", kind: "assistant_delta", data: "verified" }), JSON.stringify({ type: "result", result: { isError: false, content: [{ text: "Operation complete." }] } }), JSON.stringify({ type: "done" })])
  const events = []
  await callTool(session, operation, (event) => events.push(event))
  assert.equal(events[0].kind, "assistant_delta")
  assert.equal(events[1].type, "result")
})

test("explicit cancel uses a separate request and leaves the original RPC stream open", async (t) => {
  let cancel
  const requests = []
  t.mock.method(globalThis, "fetch", async (_url, request) => {
    const body = JSON.parse(request.body)
    requests.push(body)
    if (body.cancelRequestId) {
      return Response.json({ request_id: body.cancelRequestId, cancel_requested: true })
    }
    return new Response([
      JSON.stringify({ type: "output", data: "still draining\n" }),
      JSON.stringify({ type: "result", result: { isError: false, content: [{ text: "terminal evidence" }] } }),
      JSON.stringify({ type: "done" }),
    ].join("\n") + "\n")
  })
  const events = []
  const completion = callTool(session, {
    ...operation,
    onCancelReady: (requestCancel) => { cancel = requestCancel },
  }, (event) => events.push(event))
  assert.equal(typeof cancel, "function")
  await cancel()
  await completion
  assert.equal(requests.length, 2)
  assert.ok(requests[0].requestId)
  assert.equal(requests[1].cancelRequestId, requests[0].requestId)
  assert.equal(events.some((event) => event.type === "result"), true)
  assert.equal(events.some((event) => event.type === "error"), false)
})

test("remote cancellation uncertainty is terminal failure, not local abort completion", async (t) => {
  t.mock.method(globalThis, "fetch", async (_url, request) => {
    const body = JSON.parse(request.body)
    if (body.cancelRequestId) return Response.json({ request_id: body.cancelRequestId, cancel_requested: true })
    return new Response([
      JSON.stringify({ type: "error", error: "已请求停止，远端终态未确认", code: -32008 }),
      JSON.stringify({ type: "done" }),
    ].join("\n") + "\n")
  })
  const events = []
  let cancel
  const completion = callTool(session, {
    ...operation,
    onCancelReady: (requestCancel) => { cancel = requestCancel },
  }, (event) => events.push(event))
  await cancel()
  await assert.rejects(completion, /远端终态未确认/)
  assert.deepEqual(events.filter((event) => event.type === "error").map((event) => event.error), ["已请求停止，远端终态未确认"])
})

test("an immediate stop waits for admission and failed stop requests can be retried", async (t) => {
  let accept
  let finish
  let cancel
  let cancellations = 0
  const admitted = new Promise((resolve) => { accept = resolve })
  const encoder = new TextEncoder()
  const stream = new ReadableStream({
    start(controller) {
      finish = () => {
        controller.enqueue(encoder.encode(JSON.stringify({ type: "result", result: { content: [{ text: "completed" }] } }) + "\n"))
        controller.close()
      }
    },
  })
  t.mock.method(globalThis, "fetch", async (_url, request) => {
    const body = JSON.parse(request.body)
    if (!body.cancelRequestId) {
      await admitted
      return new Response(stream)
    }
    cancellations++
    if (cancellations === 1) return Response.json({ error: "synthetic stop failure" }, { status: 503 })
    return Response.json({ request_id: body.cancelRequestId, cancel_requested: true })
  })
  const completion = callTool(session, { ...operation, onCancelReady: (stop) => { cancel = stop } }, () => {})
  const firstStop = assert.rejects(cancel(), /synthetic stop failure/)
  await Promise.resolve()
  assert.equal(cancellations, 0)
  accept()
  await firstStop
  await cancel()
  assert.equal(cancellations, 2)
  finish()
  await completion
})

test("native MCP tool errors cannot become successful browser results", async (t) => {
  mockResponse(t, [JSON.stringify({ type: "result", result: { isError: true, content: [{ text: "helm verification failed" }] } }), JSON.stringify({ type: "done" })])
  const events = []
  await assert.rejects(callTool(session, operation, (event) => events.push(event)), /helm verification failed/)
  assert.equal(events.some((event) => event.type === "result"), false)
})

test("Gateway authentication failure rejects the operation promise", async (t) => {
  mockResponse(t, [], 401)
  await assert.rejects(callTool(session, operation, () => {}), /Gateway authentication rejected/)
})

test("a Gateway upgrade refusal reaches the caller as its Chinese reason and code", async (t) => {
  const refusal = { type: "error", error: "无权访问该目标，请联系管理员授权（HTTP 403）", code: -32003 }
  mockResponse(t, [JSON.stringify(refusal), JSON.stringify({ type: "done" })])
  const events = []
  await assert.rejects(callTool(session, operation, (event) => events.push(event)), (error) => {
    assert.equal(error.message, refusal.error)
    assert.doesNotMatch(error.message, /Unexpected server response/)
    return true
  })
  assert.deepEqual(events.filter((event) => event.type === "error"), [refusal])
})

test("closed or malformed event streams cannot be declared complete", async (t) => {
  for (const values of [[JSON.stringify({ type: "done" })], ["not json"], [JSON.stringify({ type: "unknown" })]]) {
    const context = t.mock
    context.method(globalThis, "fetch", async () => new Response(values.join("\n")))
    await assert.rejects(callTool(session, operation, () => {}))
    context.restoreAll()
  }
})

test("session restoration distinguishes missing sessions from HTTP and contract errors", async (t) => {
  for (const status of [401, 403, 502]) {
    t.mock.method(globalThis, "fetch", async () => Response.json({ session: null, error: "Gateway session rejected" }, { status }))
    await assert.rejects(restoreServerSession(), /Gateway session rejected/)
    t.mock.restoreAll()
  }
  t.mock.method(globalThis, "fetch", async () => Response.json({ session: null }))
  assert.equal(await restoreServerSession(), null)
  t.mock.restoreAll()
  t.mock.method(globalThis, "fetch", async () => Response.json({ session: {} }))
  await assert.rejects(restoreServerSession(), /Gateway 会话响应无效/)
})

test("only an explicit admin session or a default demo session shows the admin console", () => {
  const cases = [
    { name: "real session without the flag", session: { gateway: "https://gateway.example.test" }, want: false },
    { name: "real ordinary user", session: { gateway: "https://gateway.example.test", isAdmin: false }, want: false },
    { name: "real platform admin", session: { gateway: "https://gateway.example.test", isAdmin: true }, want: true },
    { name: "truthy non-boolean flag", session: { gateway: "https://gateway.example.test", isAdmin: "true" }, want: false },
    { name: "default demo session", session: { gateway: "demo://local", demo: true }, want: true },
    { name: "demo admin", session: { gateway: "demo://local", demo: true, isAdmin: true }, want: true },
    { name: "demo ordinary user", session: { gateway: "demo://local", demo: true, isAdmin: false }, want: false },
  ]
  for (const { name, session: value, want } of cases) {
    assert.equal(sessionIsAdmin(value), want, name)
  }
})

test("browser sessions take isAdmin only when the server returns true", async (t) => {
  const cases = [
    { name: "admin", flag: true, want: true },
    { name: "ordinary user", flag: false, want: false },
    { name: "missing flag", flag: undefined, want: false },
    { name: "string flag", flag: "true", want: false },
  ]
  for (const { name, flag, want } of cases) {
    const body = { authenticated: true, gateway: "https://gateway.example.test", username: "ops-zhang", isAdmin: flag, sessionGeneration: session.sessionGeneration }
    const entries = [
      { label: "login", run: () => login("https://gateway.example.test", "ops-zhang", "synthetic-password"), response: body },
      { label: "token", run: () => persistTokenSession("https://gateway.example.test", "synthetic-token"), response: body },
      { label: "restore", run: () => restoreServerSession(), response: { session: { gateway: body.gateway, username: body.username, isAdmin: flag, sessionGeneration: session.sessionGeneration } } },
    ]
    for (const entry of entries) {
      t.mock.method(globalThis, "fetch", async () => Response.json(entry.response))
      const result = await entry.run()
      assert.equal(result.isAdmin, want, `${entry.label}: ${name}`)
      t.mock.restoreAll()
    }
  }
})

test("login, token login and restoration retain the server-issued generation", async (t) => {
  const expected = {
    gateway: session.gateway,
    username: "review",
    sessionGeneration: session.sessionGeneration,
    isAdmin: false,
    tokenId: "tok_current_review",
    issuedAt: "2026-11-01T00:00:00Z",
    expiresAt: "2026-12-01T00:00:00Z",
  }
  t.mock.method(globalThis, "fetch", async () => Response.json(expected))
  assert.deepEqual(await login(session.gateway, "review", "synthetic-password"), expected)
  assert.deepEqual(await persistTokenSession(session.gateway, "synthetic-token"), expected)
  t.mock.restoreAll()
  t.mock.method(globalThis, "fetch", async () => Response.json({ session: expected }))
  assert.deepEqual(await restoreServerSession(), expected)
})

test("missing generation is a contract error and an RPC session change is typed", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ gateway: session.gateway }))
  await assert.rejects(login(session.gateway, "review", "synthetic-password"), /缺少代次/)
  await assert.rejects(persistTokenSession(session.gateway, "synthetic-token"), /缺少代次/)
  t.mock.restoreAll()
  t.mock.method(globalThis, "fetch", async () => Response.json({
    error: "session changed", code: "browser_session_changed",
  }, { status: 409 }))
  const events = []
  await assert.rejects(callTool(session, operation, (event) => events.push(event)), SessionChangedError)
  assert.deepEqual(events.map((event) => event.type), ["error", "done"])
})
