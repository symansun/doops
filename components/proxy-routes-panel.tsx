"use client"

import { Badge, Button, Checkbox, Input, Select, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"
import { useCallback, useEffect, useRef, useState } from "react"
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
  describeProxyRouteError,
  planProxyRouteDelete,
  planProxyRouteSave,
  proxyRouteForm,
  proxyRouteListState,
  proxyRouteSavedNotice,
  proxyRouteTargetStatus,
  proxyRouteTargetWarning,
  type ProxyRouteErrorView,
  type ProxyRouteForm,
  type ProxyRouteTargetStatus,
} from "@/lib/proxy-routes"
import { RefreshIcon, PlusIcon, TrashIcon, BanIcon, CheckIcon } from "./icons"

function fmtTime(v?: string): string {
  if (!v) return "—"
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleString()
}

const TARGET_BADGE: Partial<Record<ProxyRouteTargetStatus, string>> = {
  offline: "离线",
  unregistered: "未注册",
}

// 网站代理路由：对应 CLI `doops admin proxy list/set/delete`，仅平台管理员可用。
export function ProxyRoutesPanel({ session }: { session: Session }) {
  const [rows, setRows] = useState<ProxyRoute[]>([])
  // null 表示实例列表不可用，此时不提示目标状态。
  const [instances, setInstances] = useState<AdminInstance[] | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)
  const [blocked, setBlocked] = useState<ProxyRouteErrorView | null>(null)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [form, setForm] = useState<ProxyRouteForm | null>(null)
  // 打开编辑时的路由快照；保存时拿它和最新列表做三方合并。
  const [base, setBase] = useState<ProxyRoute | null>(null)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  // 只采用最后一次列表请求的结果，避免慢的旧响应覆盖新数据。
  const seq = useRef(0)

  const showError = useCallback((view: ProxyRouteErrorView) => {
    if (view.kind === "error") setError(view.message)
    else setBlocked(view)
  }, [])

  // 读最新列表并刷新表格，失败返回 null。suffix 非空表示为修改操作取数，失败时总是提示。
  const fetchRoutes = useCallback(
    async (suffix = ""): Promise<ProxyRoute[] | null> => {
      const id = ++seq.current
      try {
        const latest = await listProxyRoutes(session)
        if (id === seq.current) {
          setRows(latest)
          setLoaded(true)
          setFailed(false)
          setBlocked(null)
        }
        return latest
      } catch (e) {
        if (id === seq.current || suffix) {
          if (id === seq.current) setFailed(true)
          const view = describeProxyRouteError(e, "load")
          showError(suffix && view.kind === "error" ? { ...view, message: view.message + suffix } : view)
        }
        return null
      }
    },
    [session, showError],
  )

  const load = useCallback(async () => {
    setLoading(true)
    try {
      await fetchRoutes()
    } finally {
      setLoading(false)
    }
    // 实例列表只用于目标提示，失败不影响路由管理。
    listInstances(session).then(setInstances, () => setInstances(null))
  }, [session, fetchRoutes])

  useEffect(() => {
    load()
  }, [load])

  function patch(change: Partial<ProxyRouteForm>) {
    setForm((f) => (f ? { ...f, ...change } : f))
  }

  function closeForm() {
    setForm(null)
    setBase(null)
  }

  // 保存前重读列表：请求不带 updated_at 时 Gateway 不做版本校验，要在这里查重复域名和并发修改。
  async function save(next: ProxyRouteForm, from: ProxyRoute | null): Promise<boolean> {
    if (busyRef.current) return false
    busyRef.current = true
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const latest = await fetchRoutes("；本次修改未保存")
      if (!latest) return false
      const plan = planProxyRouteSave(next, from, latest)
      if (!plan.ok) {
        if (plan.reason === "conflict") {
          setBase(plan.latest)
          setForm(plan.form)
        }
        setError(plan.error)
        return false
      }
      if (plan.unchanged) {
        setNotice("没有需要保存的修改（最新数据已是这些值）")
        return true
      }
      const saved = await setProxyRoute(session, plan.body)
      setNotice(proxyRouteSavedNotice(saved))
      await fetchRoutes()
      return true
    } catch (e) {
      const view = describeProxyRouteError(e, "save")
      showError(view)
      if (view.reload) await fetchRoutes()
      return false
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  async function submit() {
    if (form && (await save(form, base))) closeForm()
  }

  async function toggle(route: ProxyRoute) {
    await save({ ...proxyRouteForm(route), enabled: !route.enabled }, route)
  }

  // 删除前检查最新列表，避免按过期的域名删掉已修改的路由。
  async function remove(route: ProxyRoute) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const latest = await fetchRoutes("；未执行删除")
      if (!latest) return
      const plan = planProxyRouteDelete(route, latest)
      if (!plan.ok) {
        setError(plan.error)
        return
      }
      const target = plan.value
      await deleteProxyRoute(session, target.id)
      if (form?.id === target.id) closeForm()
      setNotice(`已删除域名 ${target.host} 的代理路由`)
      await fetchRoutes()
    } catch (e) {
      const view = describeProxyRouteError(e, "delete")
      showError(view)
      if (view.reload) await fetchRoutes()
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const listState = proxyRouteListState({ loading, loaded, failed, count: rows.length })
  const targetKey = form ? `${form.cluster}/${form.instance}` : ""
  const knownTarget = (instances || []).some((i) => `${i.cluster}/${i.instance}` === targetKey)
  const targetWarning = form ? proxyRouteTargetWarning(form, proxyRouteTargetStatus(form, instances)) : ""

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
          {!blocked && (
            <Button
              onClick={() => {
                setError("")
                setNotice("")
                if (form && !form.id) closeForm()
                else {
                  setBase(null)
                  setForm({ ...EMPTY_PROXY_ROUTE_FORM })
                }
              }}
              disabled={busy}
              variant="primary"
              size="sm"
            >
              <PlusIcon width={14} height={14} /> 新建路由
            </Button>
          )}
          <Button
            onClick={() => {
              setError("")
              setNotice("")
              load()
            }}
            disabled={loading || busy}
            size="sm"
          >
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
          </Button>
        </div>
      </div>

      {error && (
        <StatusBanner tone="error" compact className="mx-4 mt-3">
          {error}
        </StatusBanner>
      )}
      {notice && !blocked && (
        <StatusBanner tone="success" compact className="mx-4 mt-3">
          {notice}
        </StatusBanner>
      )}

      {blocked ? (
        <div className="mx-4 mt-6 rounded-lg border bg-muted/40 px-4 py-8 text-center text-sm text-muted-foreground">
          {blocked.message}
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
                  <Input
                    value={form.host}
                    onChange={(e) => patch({ host: e.target.value })}
                    className="w-52"
                    placeholder="app.example.com"
                  />
                </label>
                {instances && instances.length > 0 && (
                  <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                    已注册实例
                    <Select
                      value={knownTarget ? targetKey : ""}
                      onChange={(e) => {
                        const [cluster, ...rest] = e.target.value.split("/")
                        if (e.target.value) patch({ cluster, instance: rest.join("/") })
                      }}
                      className="w-48"
                    >
                      <option value="">手动填写…</option>
                      {instances.map((i) => (
                        <option key={`${i.cluster}/${i.instance}`} value={`${i.cluster}/${i.instance}`}>
                          {i.cluster}/{i.instance}
                          {i.status === "online" ? "" : "（离线）"}
                        </option>
                      ))}
                    </Select>
                  </label>
                )}
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  集群
                  <Input
                    value={form.cluster}
                    onChange={(e) => patch({ cluster: e.target.value })}
                    className="w-32"
                    placeholder="prod-cn"
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  实例
                  <Input
                    value={form.instance}
                    onChange={(e) => patch({ instance: e.target.value })}
                    className="w-32"
                    placeholder="web-01"
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  上游地址
                  <Input
                    value={form.upstream}
                    onChange={(e) => patch({ upstream: e.target.value })}
                    className="w-56"
                    placeholder="http://127.0.0.1:8080"
                  />
                </label>
                <label className="flex items-center gap-1.5 pb-2 text-xs text-foreground">
                  <Checkbox
                    checked={form.enabled}
                    onChange={(e) => patch({ enabled: e.target.checked })}
                  />
                  启用
                </label>
              </div>
              {targetWarning && <p className="text-xs text-warning">{targetWarning}</p>}
              <p className="text-xs text-muted-foreground">
                域名只填主机名，不带协议、端口和路径；上游是该 Agent 所在网络可访问的 http(s) 地址，不能带账号密码、查询参数或锚点。
              </p>
              <div className="flex gap-2">
                <Button
                  onClick={submit}
                  disabled={busy}
                  variant="primary"
                  size="sm"
                >
                  {busy ? "保存中…" : "保存"}
                </Button>
                <Button
                  onClick={closeForm}
                  disabled={busy}
                  size="sm"
                >
                  取消
                </Button>
              </div>
            </div>
          )}

          <div className="p-4">
            <Table className="min-w-[900px]">
              <TableHeader>
                <tr>
                  <TableHead>域名</TableHead>
                  <TableHead>目标</TableHead>
                  <TableHead>上游</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>更新时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </tr>
              </TableHeader>
              <TableBody>
                {listState === "rows" &&
                  rows.map((r) => {
                    const badge = TARGET_BADGE[proxyRouteTargetStatus(r, instances)]
                    return (
                      <TableRow key={r.id}>
                        <TableCell>
                          <div className="font-medium text-foreground">{r.host}</div>
                          <div className="font-mono text-[11px] text-muted-foreground">{r.id}</div>
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">
                          {r.cluster}/{r.instance}
                          {badge && <Badge variant="warning" className="ml-1.5 font-sans">{badge}</Badge>}
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">{r.upstream}</TableCell>
                        <TableCell>
                          <Badge variant={r.enabled ? "success" : "default"}>
                            {r.enabled ? "启用" : "已停用"}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">{fmtTime(r.updated_at)}</TableCell>
                        <TableCell>
                          <div className="flex justify-end gap-1.5">
                            <Button
                              onClick={() => {
                                setError("")
                                setNotice("")
                                setBase(r)
                                setForm(proxyRouteForm(r))
                              }}
                              disabled={busy}
                              size="sm"
                            >
                              编辑
                            </Button>
                            <Button
                              onClick={() => toggle(r)}
                              disabled={busy}
                              size="sm"
                            >
                              {r.enabled ? <BanIcon width={12} height={12} /> : <CheckIcon width={12} height={12} />}
                              {r.enabled ? "停用" : "启用"}
                            </Button>
                            <Button
                              onClick={() => remove(r)}
                              disabled={busy}
                              variant="danger-outline"
                              size="sm"
                            >
                              <TrashIcon width={12} height={12} /> 删除
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                {listState !== "rows" && (
                  <tr>
                    <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                      {listState === "loading" && "加载中…"}
                      {listState === "empty" && "暂无代理路由"}
                      {listState === "error" && "未能加载代理路由，请点击刷新重试"}
                    </TableCell>
                  </tr>
                )}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </div>
  )
}
