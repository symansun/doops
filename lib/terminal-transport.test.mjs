import assert from "node:assert/strict"
import test from "node:test"
import {
  classifyTerminalDisconnect,
  nextTerminalReconnectDelayMs,
  requestTerminalAi,
  streamTerminalOutput,
  supportsTerminalLongPoll,
  TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS,
  TerminalAiError,
  terminalRequest,
  TerminalTransportError,
} from "./terminal-transport.ts"

test("supportsTerminalLongPoll detects new vs legacy Agent payloads", () => {
  assert.equal(supportsTerminalLongPoll({ ok: true, term: "t1", features: { long_poll: true } }), true)
  assert.equal(supportsTerminalLongPoll({ ok: true, term: "t1", features: { long_poll: false } }), false)
  assert.equal(supportsTerminalLongPoll({ ok: true, term: "t1" }), false)
  assert.equal(supportsTerminalLongPoll(null), false)
})

test("terminalRequest posts to /api/terminal-input and surfaces errors", async () => {
  let capturedUrl = ""
  let capturedBody = null
  const okFetch = async (url, init) => {
    capturedUrl = String(url)
    capturedBody = JSON.parse(String(init.body))
    return new Response(JSON.stringify({ ok: true, term: "term_1", cursor: 0, features: { long_poll: true } }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })
  }
  const payload = await terminalRequest(
    { cluster: "prod", instance: "node-1" },
    "console-1",
    { op: "open", cols: 100, rows: 30 },
    { fetchImpl: okFetch },
  )
  assert.equal(capturedUrl, "/api/terminal-input")
  assert.deepEqual(capturedBody, {
    cluster: "prod",
    instance: "node-1",
    session_id: "console-1",
    op: "open",
    cols: 100,
    rows: 30,
  })
  assert.equal(payload.term, "term_1")
  assert.equal(supportsTerminalLongPoll(payload), true)

  const failFetch = async () =>
    new Response(JSON.stringify({ error: "无权访问该目标" }), {
      status: 403,
      headers: { "Content-Type": "application/json" },
    })
  await assert.rejects(
    () => terminalRequest({ cluster: "prod", instance: "node-1" }, "console-1", { op: "open" }, { fetchImpl: failFetch }),
    /无权访问该目标/,
  )
})

test("streamTerminalOutput reassembles chunked NDJSON events", async () => {
  const encoder = new TextEncoder()
  const chunks = [
    '{"type":"features","features":{"long_poll":true}}\n{"type":"da',
    'ta","data":"aGVsbG8=","cursor":5}\n',
    '{"type":"exit","exit":0,"cursor":5}\n',
  ]
  const body = new ReadableStream({
    start(controller) {
      for (const c of chunks) controller.enqueue(encoder.encode(c))
      controller.close()
    },
  })
  const events = []
  await streamTerminalOutput(
    {
      target: { cluster: "prod", instance: "node-1" },
      sessionId: "console-1",
      term: "term_1",
      cursor: 0,
      waitMs: 8000,
      fetchImpl: async () => new Response(body, { status: 200 }),
    },
    (ev) => events.push(ev),
  )
  assert.deepEqual(events, [
    { type: "features", features: { long_poll: true } },
    { type: "data", data: "aGVsbG8=", cursor: 5 },
    { type: "exit", exit: 0, cursor: 5 },
  ])
})

test("TERM-03: TerminalTransportError preserves HTTP status and JSON-RPC code", async () => {
  const fail401 = async () =>
    new Response(JSON.stringify({ error: "gateway_auth_revoked", code: -32001 }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    })
  await assert.rejects(
    () => terminalRequest({ cluster: "prod", instance: "node-1" }, "s1", { op: "read" }, { fetchImpl: fail401 }),
    (err) => err instanceof TerminalTransportError && err.status === 401 && err.code === -32001,
  )

  const fail503 = async () =>
    new Response(JSON.stringify({ error: "Gateway 暂时不可用" }), {
      status: 503,
      headers: { "Content-Type": "application/json" },
    })
  await assert.rejects(
    () =>
      streamTerminalOutput(
        { target: { cluster: "prod", instance: "node-1" }, sessionId: "s1", term: "t1", cursor: 0, fetchImpl: fail503 },
        () => {},
      ),
    (err) => err instanceof TerminalTransportError && err.status === 503,
  )
})

test("TERM-03: classifyTerminalDisconnect distinguishes non-retryable auth/forbidden, session_gone reopen, and transient resume", () => {
  // 401 / -32001 鉴权失效：严禁自动重试
  assert.deepEqual(
    classifyTerminalDisconnect(new TerminalTransportError("gateway_auth_revoked", 401, -32001)),
    { retry: false, action: "stop", reason: "auth" },
  )
  // 403 / -32003 权限拒绝或敏感词拦截：严禁自动重试
  assert.deepEqual(
    classifyTerminalDisconnect(new TerminalTransportError("forbidden", 403, -32003)),
    { retry: false, action: "stop", reason: "forbidden" },
  )
  // 旧 Agent 不支持 doops_terminal：严禁自动重试
  assert.deepEqual(
    classifyTerminalDisconnect(new Error("Unknown tool over WS: doops_terminal")),
    { retry: false, action: "stop", reason: "forbidden" },
  )
  // 远端 PTY 句柄因 Agent 重启或空闲回收消失：自动重开新会话
  assert.deepEqual(
    classifyTerminalDisconnect(new TerminalTransportError("terminal not found or already closed", 502)),
    { retry: true, action: "reopen", reason: "session_gone" },
  )
  // Gateway -32004 target offline 属于 Agent 重连过渡态，绝不能误判为 session_gone 而销毁原 term！
  assert.deepEqual(
    classifyTerminalDisconnect(new TerminalTransportError("target offline: e2e-reconn/bash5", 503, -32004)),
    { retry: true, action: "resume", reason: "transient" },
  )
  assert.deepEqual(
    classifyTerminalDisconnect(new TerminalTransportError("target offline: e2e-reconn/bash5", 404, -32004)),
    { retry: true, action: "resume", reason: "transient" },
  )
  // 网络闪断 / Gateway 重启 / 502 / 503 / 504：保留原 term+cursor 自动续连
  assert.deepEqual(
    classifyTerminalDisconnect(new TerminalTransportError("Gateway 终端连接中断", 503)),
    { retry: true, action: "resume", reason: "transient" },
  )
  assert.deepEqual(
    classifyTerminalDisconnect(new TypeError("Failed to fetch")),
    { retry: true, action: "resume", reason: "transient" },
  )
})

test("TERM-03: nextTerminalReconnectDelayMs follows bounded exponential backoff schedule", () => {
  assert.equal(TERMINAL_AUTO_RECONNECT_MAX_ATTEMPTS, 8)
  assert.equal(nextTerminalReconnectDelayMs(0), 1000)
  assert.equal(nextTerminalReconnectDelayMs(1), 2000)
  assert.equal(nextTerminalReconnectDelayMs(2), 4000)
  assert.equal(nextTerminalReconnectDelayMs(3), 8000)
  assert.equal(nextTerminalReconnectDelayMs(4), 15_000)
  assert.equal(nextTerminalReconnectDelayMs(7), 15_000)
  assert.equal(nextTerminalReconnectDelayMs(-1), 1000)
})

test("TERM-04: requestTerminalAi posts to /api/terminal-ai and surfaces typed errors", async () => {
  let seenBody = null
  const okFetch = async (url, init) => {
    assert.equal(url, "/api/terminal-ai")
    assert.equal(init.method, "POST")
    seenBody = JSON.parse(init.body)
    return new Response(
      JSON.stringify({
        ok: true,
        model: "deepseek/deepseek-flash",
        provider: "deepseek",
        explanation: "查看系统 CPU 核心数与实时负载。",
        commands: [{ command: "lscpu", description: "查看 CPU 型号与核心数" }],
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    )
  }

  const res = await requestTerminalAi({
    target: { cluster: "prod", instance: "node-1" },
    sessionId: "s1",
    question: "我想查一下系统的cpu",
    action: "ask",
    cwd: "/workspace",
    fetchImpl: okFetch,
  })
  assert.equal(seenBody.question, "我想查一下系统的cpu")
  assert.equal(res.model, "deepseek/deepseek-flash")
  assert.equal(res.commands.length, 1)
  assert.equal(res.commands[0].command, "lscpu")

  const errFetch = async () =>
    new Response(
      JSON.stringify({
        error: "当前目标未绑定可用的模型服务凭据",
        code: "model_provider_not_configured",
        next_action: "请先绑定 modelProvider",
      }),
      { status: 412, headers: { "Content-Type": "application/json" } },
    )
  await assert.rejects(
    () =>
      requestTerminalAi({
        target: { cluster: "prod", instance: "node-1" },
        sessionId: "s1",
        question: "查cpu",
        fetchImpl: errFetch,
      }),
    (err) => err instanceof TerminalAiError && err.status === 412 && err.code === "model_provider_not_configured",
  )
})
