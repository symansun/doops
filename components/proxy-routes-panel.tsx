"use client"

import { useCallback, useEffect, useState } from "react"
import type { Session } from "@/lib/client"
import {
  listProxyRoutes,
  setProxyRoute,
  deleteProxyRoute,
  listInstances,
  type ProxyRoute,
  type AdminInstance,
} from "@/lib/admin"
import {
  EMPTY_PROXY_ROUTE_FORM,
  isPermissionError,
  proxyRouteBody,
  proxyRouteForm,
  type ProxyRouteForm,
} from "@/lib/proxy-routes"
import { RefreshIcon, PlusIcon, TrashIcon, BanIcon, CheckIcon } from "./icons"

function fmtTime(v?: string): string {
  if (!v) return "—"
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleString()
}

// 网站代理路由：对应 CLI `doops admin proxy list/set/delete`，仅平台管理员可用。
export function ProxyRoutesPanel({ session }: { session: Session }) {
  const [rows, setRows] = useState<ProxyRoute[]>([])
  const [instances, setInstances] = useState<AdminInstance[]>([])
  const [loaded, setLoaded] = useState(false)
  const [loading, setLoading] = useState(false)
  const [forbidden, setForbidden] = useState(false)
  const [error, setError] = useState("")
  const [form, setForm] = useState<ProxyRouteForm | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      setRows(await listProxyRoutes(session))
      setForbidden(false)
      setLoaded(true)
    } catch (e) {
      if (isPermissionError(e)) setForbidden(true)
      else setError(`加载代理路由失败：${(e as Error).message}`)
    } finally {
      setLoading(false)
    }
    // 实例列表只用于目标下拉提示，失败不影响路由管理。
    listInstances(session).then(setInstances, () => setInstances([]))
  }, [session])

  useEffect(() => {
    load()
  }, [load])

  function patch(change: Partial<ProxyRouteForm>) {
    setForm((f) => (f ? { ...f, ...change } : f))
  }

  async function save(next: ProxyRouteForm) {
    const body = proxyRouteBody(next)
    if (!body.ok) {
      setError(body.error)
      return false
    }
    setBusy(true)
    setError("")
    try {
      await setProxyRoute(session, body.value)
      await load()
      return true
    } catch (e) {
      if (isPermissionError(e)) setForbidden(true)
      else setError(`保存失败：${(e as Error).message}`)
      return false
    } finally {
      setBusy(false)
    }
  }

  async function submit() {
    if (form && (await save(form))) setForm(null)
  }

  async function toggle(route: ProxyRoute) {
    const action = route.enabled ? "停用" : "启用"
    if (!window.confirm(`确认${action}域名 ${route.host} 的代理路由？`)) return
    await save({ ...proxyRouteForm(route), enabled: !route.enabled })
  }

  async function remove(route: ProxyRoute) {
    if (!window.confirm(`确认删除域名 ${route.host} 的代理路由？删除后该域名的请求不再转发。`)) return
    setBusy(true)
    setError("")
    try {
      await deleteProxyRoute(session, route.id)
      if (form?.id === route.id) setForm(null)
      await load()
    } catch (e) {
      if (isPermissionError(e)) setForbidden(true)
      else setError(`删除失败：${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const targetKey = form ? `${form.cluster}/${form.instance}` : ""
  const knownTarget = instances.some((i) => `${i.cluster}/${i.instance}` === targetKey)

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-card/50 px-4 py-3">
        <div>
          <h2 className="text-sm font-semibold text-foreground">代理路由</h2>
          <p className="text-xs text-muted-foreground">
            把公网域名经 Gateway 转发到指定 target 的上游地址；上游主机需在该 Agent 的 proxy.allowedHosts 中放行
          </p>
        </div>
        <div className="flex items-center gap-2">
          {!forbidden && (
            <button
              onClick={() => {
                setError("")
                setForm(form && !form.id ? null : { ...EMPTY_PROXY_ROUTE_FORM })
              }}
              className="flex items-center gap-1.5 rounded-lg bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90"
            >
              <PlusIcon width={14} height={14} /> 新建路由
            </button>
          )}
          <button
            onClick={load}
            disabled={loading}
            className="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs text-foreground transition-colors hover:bg-muted disabled:opacity-50"
          >
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
          </button>
        </div>
      </div>

      {error && (
        <div className="mx-4 mt-3 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {error}
        </div>
      )}

      {forbidden ? (
        <div className="mx-4 mt-6 rounded-lg border bg-muted/40 px-4 py-8 text-center text-sm text-muted-foreground">
          当前账号不是平台管理员，无权查看或修改代理路由。
        </div>
      ) : (
        <>
          {form && (
            <div className="mx-4 mt-3 flex flex-col gap-3 rounded-lg border bg-muted/40 p-3">
              <div className="text-xs font-medium text-foreground">
                {form.id ? `编辑路由 ${form.id}` : "新建路由"}
              </div>
              <div className="flex flex-wrap items-end gap-3">
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  域名
                  <input
                    value={form.host}
                    onChange={(e) => patch({ host: e.target.value })}
                    className="input w-52"
                    placeholder="app.example.com"
                  />
                </label>
                {instances.length > 0 && (
                  <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                    已注册实例
                    <select
                      value={knownTarget ? targetKey : ""}
                      onChange={(e) => {
                        const [cluster, ...rest] = e.target.value.split("/")
                        if (e.target.value) patch({ cluster, instance: rest.join("/") })
                      }}
                      className="input w-48"
                    >
                      <option value="">手动填写…</option>
                      {instances.map((i) => (
                        <option key={`${i.cluster}/${i.instance}`} value={`${i.cluster}/${i.instance}`}>
                          {i.cluster}/{i.instance}
                          {i.status === "online" ? "" : "（离线）"}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  集群
                  <input
                    value={form.cluster}
                    onChange={(e) => patch({ cluster: e.target.value })}
                    className="input w-32"
                    placeholder="prod-cn"
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  实例
                  <input
                    value={form.instance}
                    onChange={(e) => patch({ instance: e.target.value })}
                    className="input w-32"
                    placeholder="web-01"
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  上游地址
                  <input
                    value={form.upstream}
                    onChange={(e) => patch({ upstream: e.target.value })}
                    className="input w-56"
                    placeholder="http://127.0.0.1:8080"
                  />
                </label>
                <label className="flex items-center gap-1.5 pb-2 text-xs text-foreground">
                  <input
                    type="checkbox"
                    checked={form.enabled}
                    onChange={(e) => patch({ enabled: e.target.checked })}
                  />
                  启用
                </label>
              </div>
              <p className="text-xs text-muted-foreground">
                域名只填主机名，不带协议、端口和路径；上游是该 Agent 所在网络可访问的 http(s) 地址，不能带账号密码、查询参数或锚点。
              </p>
              <div className="flex gap-2">
                <button
                  onClick={submit}
                  disabled={busy}
                  className="rounded-lg bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
                >
                  {busy ? "保存中…" : "保存"}
                </button>
                <button
                  onClick={() => setForm(null)}
                  disabled={busy}
                  className="rounded-lg border px-3 py-2 text-xs text-foreground transition-colors hover:bg-muted disabled:opacity-40"
                >
                  取消
                </button>
              </div>
            </div>
          )}

          <div className="overflow-x-auto p-4">
            <table className="w-full min-w-[900px] border-collapse text-sm">
              <thead>
                <tr className="border-b text-left text-xs text-muted-foreground">
                  <th className="px-3 py-2 font-medium">域名</th>
                  <th className="px-3 py-2 font-medium">目标</th>
                  <th className="px-3 py-2 font-medium">上游</th>
                  <th className="px-3 py-2 font-medium">状态</th>
                  <th className="px-3 py-2 font-medium">更新时间</th>
                  <th className="px-3 py-2 font-medium text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.id} className="border-b border-border/50">
                    <td className="px-3 py-2.5">
                      <div className="font-medium text-foreground">{r.host}</div>
                      <div className="font-mono text-[11px] text-muted-foreground">{r.id}</div>
                    </td>
                    <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">
                      {r.cluster}/{r.instance}
                    </td>
                    <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">{r.upstream}</td>
                    <td className="px-3 py-2.5">
                      <span className={`text-xs ${r.enabled ? "text-success" : "text-muted-foreground"}`}>
                        {r.enabled ? "启用" : "已停用"}
                      </span>
                    </td>
                    <td className="px-3 py-2.5 text-xs text-muted-foreground">{fmtTime(r.updated_at)}</td>
                    <td className="px-3 py-2.5">
                      <div className="flex justify-end gap-1.5">
                        <button
                          onClick={() => {
                            setError("")
                            setForm(proxyRouteForm(r))
                          }}
                          disabled={busy}
                          className="rounded-md border px-2 py-1 text-xs text-foreground transition-colors hover:bg-muted disabled:opacity-40"
                        >
                          编辑
                        </button>
                        <button
                          onClick={() => toggle(r)}
                          disabled={busy}
                          className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-foreground transition-colors hover:bg-muted disabled:opacity-40"
                        >
                          {r.enabled ? <BanIcon width={12} height={12} /> : <CheckIcon width={12} height={12} />}
                          {r.enabled ? "停用" : "启用"}
                        </button>
                        <button
                          onClick={() => remove(r)}
                          disabled={busy}
                          className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-destructive transition-colors hover:bg-muted disabled:opacity-40"
                        >
                          <TrashIcon width={12} height={12} /> 删除
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
                {!rows.length && (
                  <tr>
                    <td colSpan={6} className="px-3 py-8 text-center text-sm text-muted-foreground">
                      {loading || !loaded ? (error ? "未能加载代理路由" : "加载中…") : "暂无代理路由"}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  )
}
