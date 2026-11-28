import assert from "node:assert/strict"
import http from "node:http"
import test from "node:test"
import { WebSocketServer } from "ws"
import {
  extractTerminalToolPayload,
  GatewayWsPool,
  GatewayWsPoolError,
} from "./gateway-ws-pool.ts"

async function startMockGateway(handler) {
  let connectionCount = 0
  let initializeCount = 0
  const server = http.createServer((_req, res) => {
    res.writeHead(404)
    res.end()
  })
  const wss = new WebSocketServer({ noServer: true })

  server.on("upgrade", (req, socket, head) => {
    if (handler.onUpgrade) {
      const handled = handler.onUpgrade(req, socket, head)
      if (handled) return
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      connectionCount += 1
      ws.on("message", (raw) => {
        const msg = JSON.parse(raw.toString())
        if (msg.method === "initialize") {
          initializeCount += 1
          ws.send(JSON.stringify({ jsonrpc: "2.0", id: msg.id, result: { capabilities: {} } }))
          return
        }
        handler.onMessage?.(ws, msg)
      })
    })
  })

  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))
  const addr = server.address()
  const baseUrl = `http://127.0.0.1:${addr.port}`
  return {
    baseUrl,
    get connectionCount() { return connectionCount },
    get initializeCount() { return initializeCount },
    async close() {
      for (const client of wss.clients) client.terminate()
      await new Promise((resolve) => wss.close(resolve))
      await new Promise((resolve) => server.close(resolve))
    },
  }
}

test("GatewayWsPool reuses one WS across concurrent multiplexed calls", async () => {
  const gw = await startMockGateway({
    onMessage(ws, msg) {
      const op = msg.params?.arguments?.op
      const delay = op === "read" ? 60 : 5
      setTimeout(() => {
        ws.send(
          JSON.stringify({
            jsonrpc: "2.0",
            id: msg.id,
            result: { content: [{ type: "text", text: JSON.stringify({ ok: true, op }) }] },
          }),
        )
      }, delay)
    },
  })
  const pool = new GatewayWsPool({ idleTimeoutMs: 5_000 })
  try {
    const scope = { gateway: gw.baseUrl, token: "tok-1", cluster: "c1", instance: "i1" }
    // First call establishes connection; then two concurrent calls multiplex on it.
    const r0 = extractTerminalToolPayload(await pool.callTool(scope, "doops_terminal", { op: "open" }))
    assert.equal(r0.ok, true)

    const [readRes, inputRes] = await Promise.all([
      pool.callTool(scope, "doops_terminal", { op: "read" }),
      pool.callTool(scope, "doops_terminal", { op: "input" }),
    ])
    assert.equal(extractTerminalToolPayload(readRes).op, "read")
    assert.equal(extractTerminalToolPayload(inputRes).op, "input")
    assert.equal(gw.connectionCount, 1)
    assert.equal(gw.initializeCount, 1)
  } finally {
    pool.closeAll()
    await gw.close()
  }
})

test("GatewayWsPool closes idle connections after idleTimeoutMs", async () => {
  const gw = await startMockGateway({
    onMessage(ws, msg) {
      ws.send(
        JSON.stringify({
          jsonrpc: "2.0",
          id: msg.id,
          result: { content: [{ type: "text", text: JSON.stringify({ ok: true }) }] },
        }),
      )
    },
  })
  const pool = new GatewayWsPool({ idleTimeoutMs: 60 })
  try {
    const scope = { gateway: gw.baseUrl, token: "tok-idle", cluster: "c1", instance: "i1" }
    await pool.callTool(scope, "doops_terminal", { op: "open" })
    assert.equal(pool.size, 1)
    await new Promise((r) => setTimeout(r, 140))
    assert.equal(pool.size, 0)
  } finally {
    pool.closeAll()
    await gw.close()
  }
})

test("GatewayWsPool enforces maxConnections when all connections are busy", async () => {
  const gw = await startMockGateway({
    onMessage(_ws, _msg) {
      // Never reply so connection 1 stays busy.
    },
  })
  const pool = new GatewayWsPool({ maxConnections: 1, callTimeoutMs: 2_000 })
  try {
    const scope1 = { gateway: gw.baseUrl, token: "tok-a", cluster: "c1", instance: "i1" }
    const scope2 = { gateway: gw.baseUrl, token: "tok-b", cluster: "c1", instance: "i2" }
    const ac = new AbortController()
    const inflight = pool.callTool(scope1, "doops_terminal", { op: "read" }, { signal: ac.signal })
    await new Promise((r) => setTimeout(r, 40))

    await assert.rejects(
      () => pool.callTool(scope2, "doops_terminal", { op: "open" }),
      (err) => err instanceof GatewayWsPoolError && err.status === 429,
    )
    ac.abort()
    await assert.rejects(() => inflight)
  } finally {
    pool.closeAll()
    await gw.close()
  }
})

test("GatewayWsPool translates HTTP 403 upgrade rejection", async () => {
  const gw = await startMockGateway({
    onUpgrade(_req, socket) {
      socket.write("HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\nContent-Length: 9\r\n\r\nforbidden")
      socket.destroy()
      return true
    },
  })
  const pool = new GatewayWsPool()
  try {
    const scope = { gateway: gw.baseUrl, token: "tok-deny", cluster: "c1", instance: "i1" }
    await assert.rejects(
      () => pool.callTool(scope, "doops_terminal", { op: "open" }),
      (err) => err instanceof GatewayWsPoolError && err.status === 403 && err.message.includes("403"),
    )
    assert.equal(pool.size, 0)
  } finally {
    pool.closeAll()
    await gw.close()
  }
})

test("H2 regression: -32001 token revocation returns 401 and immediately tears down pooled WS", async () => {
  let callNum = 0
  const gw = await startMockGateway({
    onMessage(ws, msg) {
      callNum += 1
      if (callNum === 1) {
        ws.send(JSON.stringify({
          jsonrpc: "2.0",
          id: msg.id,
          result: { content: [{ type: "text", text: JSON.stringify({ ok: true }) }] },
        }))
        return
      }
      ws.send(JSON.stringify({
        jsonrpc: "2.0",
        id: msg.id,
        error: { code: -32001, message: "gateway_auth_revoked" },
      }))
    },
  })
  const pool = new GatewayWsPool({ idleTimeoutMs: 5_000 })
  try {
    const scope = { gateway: gw.baseUrl, token: "tok-revoked", cluster: "c1", instance: "i1" }
    await pool.callTool(scope, "doops_terminal", { op: "open" })
    assert.equal(pool.size, 1)

    await assert.rejects(
      () => pool.callTool(scope, "doops_terminal", { op: "read" }),
      (err) => err instanceof GatewayWsPoolError && err.status === 401 && err.code === -32001,
    )
    assert.equal(pool.size, 0, "revoked token connection must be evicted immediately")
  } finally {
    pool.closeAll()
    await gw.close()
  }
})

test("H2 regression: evictBySession tears down only that token's connections and maxLifetimeMs rotates WS", async () => {
  const gw = await startMockGateway({
    onMessage(ws, msg) {
      ws.send(JSON.stringify({
        jsonrpc: "2.0",
        id: msg.id,
        result: { content: [{ type: "text", text: JSON.stringify({ ok: true }) }] },
      }))
    },
  })
  const pool = new GatewayWsPool({ idleTimeoutMs: 5_000, maxLifetimeMs: 60 })
  try {
    const scope1 = { gateway: gw.baseUrl, token: "tok-user-1", cluster: "c1", instance: "i1" }
    const scope2 = { gateway: gw.baseUrl, token: "tok-user-2", cluster: "c1", instance: "i1" }
    await pool.callTool(scope1, "doops_terminal", { op: "open" })
    await pool.callTool(scope2, "doops_terminal", { op: "open" })
    assert.equal(pool.size, 2)

    const evicted = pool.evictBySession(gw.baseUrl, "tok-user-1")
    assert.equal(evicted, 1)
    assert.equal(pool.size, 1)

    // Wait past maxLifetimeMs and ensure scope2 rotates to a fresh WS on next acquire
    const connsBefore = gw.connectionCount
    await new Promise((r) => setTimeout(r, 80))
    await pool.callTool(scope2, "doops_terminal", { op: "read" })
    assert.equal(gw.connectionCount, connsBefore + 1)
  } finally {
    pool.closeAll()
    await gw.close()
  }
})

test("TERM-03: graceful maxLifetimeMs rotation never aborts in-flight long-poll and evictBySession still revokes draining WS", async () => {
  const gw = await startMockGateway({
    onMessage(ws, msg) {
      const op = msg.params?.arguments?.op
      const delay = op === "read" ? 130 : 10
      setTimeout(() => {
        if (ws.readyState !== 1) return
        ws.send(
          JSON.stringify({
            jsonrpc: "2.0",
            id: msg.id,
            result: { content: [{ type: "text", text: JSON.stringify({ ok: true, op }) }] },
          }),
        )
      }, delay)
    },
  })
  const pool = new GatewayWsPool({ idleTimeoutMs: 5_000, maxLifetimeMs: 45 })
  try {
    const scope = { gateway: gw.baseUrl, token: "tok-graceful", cluster: "c1", instance: "i1" }
    await pool.callTool(scope, "doops_terminal", { op: "open" })
    assert.equal(gw.connectionCount, 1)

    // 启动一条在途长轮询 read（130ms），跨越 maxLifetimeMs（45ms）。
    const inflightRead = pool.callTool(scope, "doops_terminal", { op: "read" })
    await new Promise((r) => setTimeout(r, 60))

    // 达到 maxLifetimeMs 后并发新请求：旧 WS 进入 draining，新请求立刻切到第 2 条 WS，且在途 read 绝不报 503。
    const inputRes = await pool.callTool(scope, "doops_terminal", { op: "input" })
    assert.equal(extractTerminalToolPayload(inputRes).op, "input")
    assert.equal(gw.connectionCount, 2, "expired WS must rotate to a fresh WS for new calls")
    assert.equal(pool.drainingSize, 1, "expired WS with in-flight read must stay in draining set until settled")

    const readRes = await inflightRead
    assert.equal(extractTerminalToolPayload(readRes).op, "read")
    assert.equal(pool.drainingSize, 0, "draining WS must close cleanly once its last in-flight RPC settles")
    assert.equal(pool.size, 1)

    // H2 安全不退化：登出驱逐必须同时立刻中断 draining 中的连接。
    const slowRead2 = pool.callTool(scope, "doops_terminal", { op: "read" })
    await new Promise((r) => setTimeout(r, 60))
    const newInput2 = pool.callTool(scope, "doops_terminal", { op: "input" })
    await newInput2
    assert.equal(pool.drainingSize, 1)
    const evicted = pool.evictBySession(gw.baseUrl, "tok-graceful")
    assert.equal(evicted, 2, "evictBySession must evict both active and draining connections")
    assert.equal(pool.size, 0)
    assert.equal(pool.drainingSize, 0)
    await assert.rejects(
      () => slowRead2,
      (err) => err instanceof GatewayWsPoolError && err.status === 401 && err.code === -32001,
    )
  } finally {
    pool.closeAll()
    await gw.close()
  }
})
