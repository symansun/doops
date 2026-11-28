import assert from "node:assert/strict"
import test from "node:test"
import * as admin from "./admin.ts"
import { ADMIN_TABS } from "./console-views.ts"
import { buildConsoleSearch, parseConsoleSearch } from "./console-url.ts"
import { buttonNamed, componentHarness, componentStubs, elementText } from "./component-test-harness.mjs"

const session = { gateway: "https://gateway.test", token: "session-token", isAdmin: true }
const demoSession = { ...session, demo: true }
const SECRET = "synthetic"
const FULL_URL = "https://hooks.example.test/private/webhook"
const BODY_CANARY = "body-" + SECRET
const ENDPOINT_ID = "endpoint/with space"
const ENCODED_ID = encodeURIComponent(ENDPOINT_ID)

const endpoint = {
  id: ENDPOINT_ID,
  name: "Production alerts",
  description: "Security notifications",
  url_host: "hooks.example.test",
  url_sha256: "abc123",
  enabled: true,
  scope_cluster: "prod",
  scope_instance: "web-1",
  event_types: ["execution.finished"],
  created_by: "admin",
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  last_success_at_ms: 0,
  last_failure_at_ms: 0,
  last_error_category: "",
}

function jsonResponse(value, status = 200, headers = {}) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  })
}

function installFetch(t, responder) {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init = {}) => {
    const call = { url: String(url), init: { ...init } }
    calls.push(call)
    return responder(call, calls.length - 1)
  })
  return calls
}

function bodyOf(call) {
  return call.init.body === undefined ? undefined : JSON.parse(call.init.body)
}

async function rejected(promise) {
  return promise.then(() => null, (error) => error)
}

function uiStubs() {
  const names = [
    "Badge", "Button", "Card", "CardContent", "CardDescription", "CardHeader", "CardTitle",
    "Checkbox", "Input", "Modal", "StatusBanner", "Table", "TableBody", "TableCell", "TableHead",
    "TableHeader", "TableRow", "Textarea",
  ]
  return Object.fromEntries(names.map((name) => [name, name]))
}

function makePanel(adminOverrides = {}, options = {}) {
  const confirm = options.confirm || (async () => true)
  return componentHarness("../components/admin-webhooks-panel.tsx", "AdminWebhooksPanel", {
    session: options.session || session,
  }, {
    "./ui": uiStubs(),
    "./icons": componentStubs,
    "./app-dialog": { useAppDialog: () => ({ confirm, dialog: null }) },
    "@/lib/admin": {
      listWebhooks: async () => [],
      createWebhook: async () => ({ id: "new", endpoint: { ...endpoint, secret: SECRET } }),
      updateWebhook: async () => endpoint,
      deleteWebhook: async () => undefined,
      rotateWebhookSecret: async () => ({ id: endpoint.id, secret: SECRET }),
      testWebhook: async () => ({ ok: true, event_id: "evt-test" }),
      listWebhookDeliveries: async () => [],
      ...adminOverrides,
    },
    "@/lib/format": {
      formatDateTime: (value) => String(value === undefined || value === null ? "—" : value),
      formatNumber: (value) => String(value === undefined || value === null ? "—" : value),
    },
  })
}

function panelText(view) {
  return elementText(view.one((node) => node.type === "section"))
}

function pendingMutation() {
  const requests = []
  return {
    requests,
    run: (...args) => new Promise((resolve, reject) => requests.push({ args, resolve, reject })),
  }
}

test("admin navigation exposes the Webhook tab", () => {
  assert.deepEqual(ADMIN_TABS.find((tab) => tab.id === "webhooks"), {
    id: "webhooks",
    label: "出站 Webhook",
  })
})

test("the admin Webhook tab survives a URL round-trip without dropping demo flags", () => {
  const route = parseConsoleSearch("?view=admin&tab=webhooks&demo=1")
  assert.equal(route.view, "admin")
  assert.equal(route.adminTab, "webhooks")
  const search = buildConsoleSearch("?demo=1", route)
  const params = new URLSearchParams(search)
  assert.equal(params.get("view"), "admin")
  assert.equal(params.get("tab"), "webhooks")
  assert.equal(params.get("demo"), "1")
  assert.equal(parseConsoleSearch(search).adminTab, "webhooks")
})

test("webhook helpers use the Next proxy, encode IDs, and preserve Gateway shapes", async (t) => {
  const responses = [
    jsonResponse({ endpoints: [endpoint] }),
    jsonResponse({ id: "created", endpoint: { ...endpoint, id: "created", secret: SECRET } }, 201, { "Cache-Control": "no-store" }),
    jsonResponse({ endpoint }),
    jsonResponse({ ok: true }),
    jsonResponse({ id: ENDPOINT_ID, secret: SECRET }, 200, { "Cache-Control": "no-store" }),
    jsonResponse({ ok: true, event_id: "evt-test" }),
    jsonResponse({ deliveries: [{ id: "delivery-1", endpoint_id: ENDPOINT_ID, event_id: "evt-1", status: "delivered", attempt: 1, response_status: 200, error_category: "", created_at_ms: 1, updated_at_ms: 2, delivered_at_ms: 3 }] }),
  ]
  const calls = installFetch(t, (_call, index) => responses[index])

  assert.deepEqual(await admin.listWebhooks(session), [endpoint])
  const created = await admin.createWebhook(session, {
    name: "Production alerts",
    description: "Security notifications",
    url: FULL_URL,
    event_types: ["execution.finished"],
    scope_cluster: "prod",
    scope_instance: "web-1",
    enabled: true,
  })
  assert.equal(created.id, "created")
  assert.equal(created.endpoint.secret, SECRET)
  assert.deepEqual(await admin.updateWebhook(session, ENDPOINT_ID, { name: "Renamed", event_types: ["agent.connected"] }), endpoint)
  assert.equal(await admin.deleteWebhook(session, ENDPOINT_ID), undefined)
  assert.deepEqual(await admin.rotateWebhookSecret(session, ENDPOINT_ID), { id: ENDPOINT_ID, secret: SECRET })
  assert.deepEqual(await admin.testWebhook(session, ENDPOINT_ID), { ok: true, event_id: "evt-test" })
  assert.deepEqual(await admin.listWebhookDeliveries(session, ENDPOINT_ID, { limit: 50, status: "failed", before: 123 }), [
    { id: "delivery-1", endpoint_id: ENDPOINT_ID, event_id: "evt-1", status: "delivered", attempt: 1, next_attempt_at_ms: 0, response_status: 200, error_category: "", created_at_ms: 1, updated_at_ms: 2, delivered_at_ms: 3 },
  ])

  assert.deepEqual(calls.map((call) => [call.url, call.init.method || "GET"]), [
    ["/api/admin/webhooks", "GET"],
    ["/api/admin/webhooks", "POST"],
    ["/api/admin/webhooks/" + ENCODED_ID, "PATCH"],
    ["/api/admin/webhooks/" + ENCODED_ID, "DELETE"],
    ["/api/admin/webhooks/" + ENCODED_ID + "/rotate-secret", "POST"],
    ["/api/admin/webhooks/" + ENCODED_ID + "/test", "POST"],
    ["/api/admin/webhooks/" + ENCODED_ID + "/deliveries?limit=50&status=failed&before=123", "GET"],
  ])
  assert.deepEqual(bodyOf(calls[1]), {
    name: "Production alerts",
    description: "Security notifications",
    url: FULL_URL,
    event_types: ["execution.finished"],
    scope_cluster: "prod",
    scope_instance: "web-1",
    enabled: true,
  })
  assert.deepEqual(bodyOf(calls[2]), { name: "Renamed", event_types: ["agent.connected"] })
  assert.equal(bodyOf(calls[4]), undefined, "secret rotation does not send a body")
  assert.equal(bodyOf(calls[5]), undefined, "test delivery does not send a body")
  for (const call of calls) assert.equal(call.init.headers["Content-Type"], "application/json")
  assert.ok(!JSON.stringify(calls).includes(SECRET), "one-time secret is not persisted in request arguments")
})

test("delivery records are normalized from either snake_case or Go field names", async (t) => {
  installFetch(t, () =>
    jsonResponse({
      deliveries: [
        {
          ID: "dlv-1",
          EndpointID: ENDPOINT_ID,
          EventID: "evt-1",
          Status: "pending",
          Attempt: 2,
          NextAttemptAtMs: 1795856588023,
          ResponseStatus: 0,
          ErrorCategory: "network",
          CreatedAtMs: 1795856428015,
          UpdatedAtMs: 1795856468023,
          DeliveredAtMs: 0,
        },
        {
          id: "dlv-2",
          endpoint_id: ENDPOINT_ID,
          event_id: "evt-2",
          status: "delivered",
          attempt: 1,
          next_attempt_at_ms: 0,
          response_status: 200,
          error_category: "",
          created_at_ms: 10,
          updated_at_ms: 20,
          delivered_at_ms: 20,
        },
      ],
    }),
  )

  const deliveries = await admin.listWebhookDeliveries(session, ENDPOINT_ID)
  assert.deepEqual(deliveries[0], {
    id: "dlv-1",
    endpoint_id: ENDPOINT_ID,
    event_id: "evt-1",
    status: "pending",
    attempt: 2,
    next_attempt_at_ms: 1795856588023,
    response_status: 0,
    error_category: "network",
    created_at_ms: 1795856428015,
    updated_at_ms: 1795856468023,
    delivered_at_ms: 0,
  })
  assert.equal(deliveries[1].status, "delivered")
  assert.equal(deliveries[1].response_status, 200)
  assert.equal(deliveries[1].delivered_at_ms, 20)
  for (const delivery of deliveries) {
    assert.equal(typeof delivery.attempt, "number")
    assert.equal(delivery.id.startsWith("dlv-"), true)
  }
})

test("the UI only renders redacted endpoint and delivery metadata", async () => {
  const delivery = {
    id: "delivery-1", endpoint_id: ENDPOINT_ID, event_id: "evt-123", status: "failed", attempt: 2,
    response_status: 500, error_category: "remote_error", created_at_ms: 1, updated_at_ms: 2, delivered_at_ms: 0,
    secret: SECRET, body: BODY_CANARY, full_url: FULL_URL,
  }
  const view = makePanel({
    listWebhooks: async () => [{ ...endpoint, secret: SECRET, url: FULL_URL }],
    listWebhookDeliveries: async () => [delivery],
  })
  await view.flush()
  const listText = panelText(view)
  assert.match(listText, /hooks\.example\.test/)
  assert.doesNotMatch(listText, /private\//)
  assert.doesNotMatch(listText, new RegExp(SECRET))
  await view.one(buttonNamed("投递")).props.onClick()
  await view.flush()
  const deliveryText = panelText(view)
  assert.match(deliveryText, /evt-123/)
  assert.match(deliveryText, /remote_error/)
  assert.doesNotMatch(deliveryText, new RegExp(SECRET))
  assert.doesNotMatch(deliveryText, new RegExp(BODY_CANARY))
  assert.doesNotMatch(deliveryText, new RegExp(FULL_URL))
})

test("pending delivery rows show the waiting state and no fabricated HTTP status", async () => {
  const pending = {
    id: "delivery-pending", endpoint_id: session.id || "endpoint", event_id: "evt-pending", status: "pending", attempt: 2,
    next_attempt_at_ms: 1, response_status: 0, error_category: "network", created_at_ms: 1, updated_at_ms: 2, delivered_at_ms: 0,
  }
  const view = makePanel({
    listWebhooks: async () => [endpoint],
    listWebhookDeliveries: async () => [pending],
  })
  await view.flush()
  await view.one(buttonNamed("投递")).props.onClick()
  await view.flush()
  const deliveryText = panelText(view)
  assert.match(deliveryText, /evt-pending/)
  assert.match(deliveryText, /等待中/)
  assert.match(deliveryText, /network/)
  assert.match(deliveryText, /—/)
  assert.doesNotMatch(deliveryText, /evt-pending等待中2 0/)
})

for (const [status, category, expectedMessage] of [
  [401, "gateway_auth_expired", "登录状态已过期，请重新登录"],
  [403, "forbidden", "当前账号没有管理员权限"],
  [404, "webhook_not_found", "Webhook 不存在或已被删除"],
  [409, "webhook_endpoint_disabled", "Webhook 端点已停用，无法执行该操作"],
  [500, "gateway_internal_error", "Gateway 内部错误，请稍后重试"],
  [502, "gateway_bad_gateway", "Gateway 网关不可用，请稍后重试"],
  [503, "webhook_secret_unavailable", "Gateway 主密钥不可用，Webhook 密钥操作暂不可用"],
]) {
  test("webhook helper preserves HTTP " + status + " and a safe Gateway category", async (t) => {
    installFetch(t, () => jsonResponse({ error: category, message: "safe " + status }, status))
    const error = await rejected(admin.listWebhooks(session))
    assert.ok(error)
    assert.equal(error.status, status)
    assert.equal(error.category, category)
    assert.equal(error.message, expectedMessage)
  })
}

test("webhook helper redacts JSON error messages carrying URL, secret, or body canaries", async (t) => {
  installFetch(t, () => jsonResponse({
    error: "webhook_invalid_url",
    message: "rejected " + FULL_URL + " secret=" + SECRET + " body=" + BODY_CANARY,
  }, 400))
  const error = await rejected(admin.listWebhooks(session))
  assert.ok(error)
  assert.equal(error.status, 400)
  assert.equal(error.category, "webhook_invalid_url")
  assert.equal(error.message, "Webhook URL 必须是合法的 HTTPS 地址")
  assert.doesNotMatch(error.message, /private\//)
  assert.ok(!error.message.includes(SECRET))
  assert.ok(!error.message.includes(BODY_CANARY))
})

test("Gateway plain-text webhook categories stay status-bearing without echoing the body", async (t) => {
  installFetch(t, () => new Response("webhook_secret_unavailable\n", { status: 503, headers: { "Content-Type": "text/plain" } }))
  const error = await rejected(admin.listWebhooks(session))
  assert.ok(error)
  assert.equal(error.status, 503)
  assert.equal(error.category, "webhook_secret_unavailable")
  assert.equal(error.message, "Gateway 主密钥不可用，Webhook 密钥操作暂不可用")
})

test("webhook helper reports unmapped categories with a generic message and no raw text", async (t) => {
  installFetch(t, () => new Response("upstream said " + SECRET + " at " + FULL_URL, { status: 502, headers: { "Content-Type": "text/plain" } }))
  const error = await rejected(admin.listWebhooks(session))
  assert.ok(error)
  assert.equal(error.status, 502)
  assert.equal(error.category, undefined)
  assert.equal(error.message, "Webhook 请求失败（HTTP 502）")
  assert.ok(!error.message.includes(SECRET))
  assert.ok(!error.message.includes(FULL_URL))
})

test("HTML errors become status-bearing errors without echoing the HTML body", async (t) => {
  installFetch(t, () => new Response("<!doctype html><title>Bad Gateway</title> secret=" + SECRET, { status: 502, headers: { "Content-Type": "text/html" } }))
  const error = await rejected(admin.listWebhooks(session))
  assert.ok(error)
  assert.equal(error.status, 502)
  assert.equal(error.category, undefined)
  assert.equal(error.message, "Webhook 请求失败（HTTP 502）")
  assert.doesNotMatch(error.message, /Bad Gateway/)
  assert.ok(!error.message.includes(SECRET))
})

test("network errors become the shared NetworkError without transport details", async (t) => {
  installFetch(t, () => { throw new TypeError("connect ECONNREFUSED 10.0.0.5:42222") })
  const error = await rejected(admin.listWebhooks(session))
  assert.ok(error)
  assert.equal(error.name, "NetworkError")
  assert.match(error.message, /网络连接中断/)
  assert.ok(!error.message.includes("ECONNREFUSED"))
})

test("demo webhooks are empty and mutations are read-only without fetch", async (t) => {
  let calls = 0
  t.mock.method(globalThis, "fetch", async () => { calls += 1; throw new Error("demo must not call the Gateway") })
  assert.deepEqual(await admin.listWebhooks(demoSession), [])
  assert.deepEqual(await admin.listWebhookDeliveries(demoSession, ENDPOINT_ID), [])
  const mutations = [
    () => admin.createWebhook(demoSession, { name: "demo", url: FULL_URL, event_types: ["execution.finished"] }),
    () => admin.updateWebhook(demoSession, ENDPOINT_ID, { enabled: false }),
    () => admin.deleteWebhook(demoSession, ENDPOINT_ID),
    () => admin.rotateWebhookSecret(demoSession, ENDPOINT_ID),
    () => admin.testWebhook(demoSession, ENDPOINT_ID),
  ]
  for (const operation of mutations) await assert.rejects(operation, /read-only in demo/)
  assert.equal(calls, 0)
})

test("Webhook panel has deterministic loading and empty states", async () => {
  const view = makePanel({ listWebhooks: async () => [] })
  view.render()
  assert.match(panelText(view), /正在加载 Webhook 端点/)
  await view.flush()
  assert.match(panelText(view), /还没有 Webhook 端点/)
  assert.doesNotMatch(panelText(view), new RegExp(SECRET))
})

for (const status of [401, 403, 404, 409, 500, 502, 503]) {
  test("Webhook panel renders status " + status + " with a retry action", async () => {
    const error = Object.assign(new Error("Gateway failure " + status), { status })
    const view = makePanel({ listWebhooks: async () => { throw error } })
    await view.flush()
    const text = panelText(view)
    if (status === 403) assert.match(text, /没有管理员权限/)
    else assert.match(text, new RegExp("Gateway failure " + status))
    assert.equal(view.nodes(buttonNamed("重试")).length, 1)
  })
}

test("Webhook panel renders network errors without raw transport text", async () => {
  const error = new TypeError("网络连接中断，无法连接控制台服务，请检查网络后重试")
  const view = makePanel({ listWebhooks: async () => { throw error } })
  await view.flush()
  assert.match(panelText(view), /网络连接中断/)
  assert.doesNotMatch(panelText(view), /ECONNREFUSED|10\\.0\\.0\\.5/)
})

test("Webhook mutations require confirmation and remain locked until the request settles", async () => {
  const mutation = pendingMutation()
  const confirmations = []
  let listCalls = 0
  const view = makePanel({
    listWebhooks: async () => { listCalls += 1; return [endpoint] },
    deleteWebhook: mutation.run,
  }, { confirm: async (options) => { confirmations.push(options); return true } })
  await view.flush()
  const first = view.one(buttonNamed("删除")).props.onClick()
  await new Promise((resolve) => setImmediate(resolve))
  view.render()
  const locked = view.one(buttonNamed("删除"))
  assert.equal(locked.props.disabled, true)
  await locked.props.onClick()
  assert.equal(mutation.requests.length, 1, "a duplicate click cannot issue a second delete")
  assert.equal(confirmations.length, 1)
  assert.equal(confirmations[0].confirmLabel, "删除")
  mutation.requests[0].resolve()
  await first
  await view.flush()
  assert.equal(listCalls, 2)
  assert.equal(view.one(buttonNamed("删除")).props.disabled, false)
})

test("Webhook panel does not mutate when confirmation is declined", async () => {
  let deleted = 0
  let confirms = 0
  const view = makePanel({
    listWebhooks: async () => [endpoint],
    deleteWebhook: async () => { deleted += 1 },
  }, { confirm: async () => { confirms += 1; return false } })
  await view.flush()
  await view.one(buttonNamed("删除")).props.onClick()
  assert.equal(confirms, 1)
  assert.equal(deleted, 0)
})

test("Secret is shown only in the one-time modal and is cleared on close", async () => {
  const view = makePanel({
    listWebhooks: async () => [endpoint],
    rotateWebhookSecret: async () => ({ id: endpoint.id, secret: SECRET }),
  })
  await view.flush()
  await view.one(buttonNamed("轮换")).props.onClick()
  await view.flush()
  assert.ok(panelText(view).includes(SECRET))
  const secretModal = view.one((node) => node.type === "Modal" && elementText(node).includes(SECRET))
  secretModal.props.onClose()
  await view.flush()
  assert.ok(!panelText(view).includes(SECRET))
})

test("Delivery modal renders metadata only, never secret, body, or full URL", async () => {
  const delivery = {
    id: "delivery-1", endpoint_id: endpoint.id, event_id: "evt-123", status: "failed", attempt: 2,
    response_status: 500, error_category: "remote_error", created_at_ms: 1, updated_at_ms: 2, delivered_at_ms: 0,
    secret: SECRET, body: BODY_CANARY, full_url: FULL_URL,
  }
  const view = makePanel({
    listWebhooks: async () => [endpoint],
    listWebhookDeliveries: async () => [delivery],
  })
  await view.flush()
  await view.one(buttonNamed("投递")).props.onClick()
  await view.flush()
  const text = panelText(view)
  assert.match(text, /evt-123/)
  assert.match(text, /remote_error/)
  assert.doesNotMatch(text, new RegExp(SECRET))
  assert.doesNotMatch(text, new RegExp(BODY_CANARY))
  assert.doesNotMatch(text, new RegExp(FULL_URL))
})
