import assert from "node:assert/strict"
import test from "node:test"
import { callTool, restoreServerSession } from "./client.ts"

const session = { gateway: "https://gateway.example.test", token: "synthetic-token" }
const operation = { cluster: "test", instance: "node", tool: "doops_agent_prompt", arguments: { session_id: "browser-test", operation: "apply", instruction: "synthetic deployment" } }

function mockResponse(t, values, status = 200) {
  t.mock.method(globalThis, "fetch", async (_url, request) => {
    assert.equal(request.headers["Content-Type"], "application/json")
    assert.equal(request.headers.Authorization, undefined)
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
