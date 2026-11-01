import assert from "node:assert/strict"
import test from "node:test"
import {
  EMPTY_PROXY_ROUTE_FORM,
  isPermissionError,
  normalizeProxyHost,
  proxyRouteBody,
  proxyRouteForm,
  validateProxyUpstream,
} from "./proxy-routes.ts"

test("host normalization matches the Gateway rules", () => {
  const cases = [
    { in: "App.Example.COM", out: "app.example.com" },
    { in: " app.example.com. ", out: "app.example.com" },
    { in: "app.example.com:443", out: "app.example.com" },
    { in: "10.0.0.1", out: "10.0.0.1" },
    { in: "[::1]:8080", out: "::1" },
    { in: "", err: "请填写域名" },
    { in: "https://app.example.com", err: "域名只填主机名，不要带协议或路径" },
    { in: "app.example.com/path", err: "域名只填主机名，不要带协议或路径" },
    { in: "app.example.com:", err: "域名中的端口无效" },
    { in: "::1", err: "域名不能带端口" },
    { in: "-bad.example.com", err: "域名格式无效" },
    { in: "bad-.example.com", err: "域名格式无效" },
    { in: "a..b", err: "域名格式无效" },
    { in: "under_score.example", err: "域名格式无效" },
    { in: `${"a".repeat(64)}.example`, err: "域名格式无效" },
  ]
  for (const c of cases) {
    const got = normalizeProxyHost(c.in)
    if (c.err) assert.deepEqual(got, { ok: false, error: c.err }, c.in)
    else assert.deepEqual(got, { ok: true, value: c.out }, c.in)
  }
})

test("upstream must be a plain http(s) URL", () => {
  const cases = [
    { in: "http://127.0.0.1:8080", ok: true },
    { in: " https://internal.example.com/app ", ok: true },
    { in: "ftp://127.0.0.1", ok: false },
    { in: "127.0.0.1:8080", ok: false },
    { in: "http://user:pass@127.0.0.1", ok: false },
    { in: "http://127.0.0.1/?a=1", ok: false },
    { in: "http://127.0.0.1/#frag", ok: false },
    { in: "", ok: false },
  ]
  for (const c of cases) assert.equal(validateProxyUpstream(c.in).ok, c.ok, c.in)
})

test("form becomes an upsert body with every field", () => {
  const cases = [
    {
      name: "new route omits id",
      form: { ...EMPTY_PROXY_ROUTE_FORM, host: "App.Example.com", cluster: " prod ", instance: " node-1 ", upstream: "http://127.0.0.1:8080" },
      want: { ok: true, value: { host: "app.example.com", cluster: "prod", instance: "node-1", upstream: "http://127.0.0.1:8080", enabled: true } },
    },
    {
      name: "edit keeps id and disabled flag",
      form: proxyRouteForm({ id: "route_1", host: "a.example", cluster: "c", instance: "i", upstream: "http://h", enabled: false }),
      want: { ok: true, value: { id: "route_1", host: "a.example", cluster: "c", instance: "i", upstream: "http://h", enabled: false } },
    },
    {
      name: "missing scope",
      form: { ...EMPTY_PROXY_ROUTE_FORM, host: "a.example", upstream: "http://h" },
      want: { ok: false, error: "请填写目标集群和实例" },
    },
    {
      name: "bad host reported first",
      form: { ...EMPTY_PROXY_ROUTE_FORM, host: "http://a", cluster: "c", instance: "i", upstream: "bad" },
      want: { ok: false, error: "域名只填主机名，不要带协议或路径" },
    },
  ]
  for (const c of cases) assert.deepEqual(proxyRouteBody(c.form), c.want, c.name)
})

test("only 401 and 403 count as permission errors", () => {
  const cases = [
    { err: Object.assign(new Error("x"), { status: 401 }), want: true },
    { err: Object.assign(new Error("x"), { status: 403 }), want: true },
    { err: Object.assign(new Error("x"), { status: 400 }), want: false },
    { err: new Error("network"), want: false },
    { err: null, want: false },
  ]
  for (const c of cases) assert.equal(isPermissionError(c.err), c.want)
})
