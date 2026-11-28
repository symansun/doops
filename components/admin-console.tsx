"use client"

import { Badge, Button, Checkbox, Input, PageTabs, Select, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import type { Session } from "@/lib/client"
import { formatDateTime } from "@/lib/format"
import {
  GRANT_ALL,
  grantInstanceChoices,
  grantScopeOptions,
  grantScopeWarning,
  validateGrantForm,
} from "@/lib/grant-form"
import {
  ACTIVE_INSTANCE_WINDOW_DAYS,
  TOKEN_STATE_LABELS,
  gatewayTime,
  groupInstances,
  groupTokensByUser,
  instanceClusterOptions,
  isTokenGroupCollapsed,
  offlineLabel,
  sortUsersForDisplay,
  summarizeInstanceScope,
  summarizeUsers,
  type TokenState,
} from "@/lib/admin-views"
import {
  listUsers,
  createUser,
  setUserPassword,
  setUserDisabled,
  listGrants,
  createGrant,
  deleteGrant,
  listTokens,
  createToken,
  revokeToken,
  listInstances,
  archiveInstance,
  unarchiveInstance,
  deleteInstance,
  type AdminUser,
  type AdminGrant,
  type AdminToken,
  type AdminInstance,
} from "@/lib/admin"
import { ADMIN_TABS, type AdminTab } from "@/lib/console-views"
import { buildAdminSearch, parseAdminTab } from "@/lib/console-url"
import { PlatformAssetScope } from "./agent-asset-scope"
import { assetRequest } from "@/lib/assets"
import type { TargetRef } from "@/lib/console-refresh"
import { AdminConfigDispatch } from "./admin-config-dispatch"
import { ProxyRoutesPanel } from "./proxy-routes-panel"
import { SensitiveFilterPanel } from "./sensitive-filter-panel"
import AdminWebhooksPanel from "./admin-webhooks-panel"
import { useAppDialog } from "./app-dialog"
import { RefreshIcon, PlusIcon, TrashIcon, BanIcon, CheckIcon, CopyIcon } from "./icons"


const fmtTime = (v?: string) => formatDateTime(v)

export function AdminConsole({
  session,
  defaultTab = "instances",
}: {
  session: Session
  defaultTab?: AdminTab
}) {
  const [tab, setTab] = useState<AdminTab>(() => {
    const search = typeof window !== "undefined" ? window.location?.search ?? "" : ""
    return search ? parseAdminTab(search) : defaultTab
  })

  // 用户切页签时把状态写入 ?tab=；前进/后退只恢复，不再入栈。
  const changeTab = useCallback((next: AdminTab) => {
    setTab(next)
    if (typeof window === "undefined" || !window.location || !window.history) return
    const search = buildAdminSearch(window.location.search, next)
    if (search !== window.location.search) {
      window.history.pushState(null, "", `${window.location.pathname}${search}`)
    }
  }, [])

  useEffect(() => {
    if (typeof window === "undefined" || !window.addEventListener) return
    function onPopState() {
      setTab(parseAdminTab(window.location?.search ?? ""))
    }
    window.addEventListener("popstate", onPopState)
    return () => window.removeEventListener("popstate", onPopState)
  }, [])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageTabs ariaLabel="平台管理功能" items={ADMIN_TABS} value={tab} onChange={changeTab} />
      <div className="min-h-0 flex-1 overflow-auto">
        {tab === "scope" && <AdminAssetScope />}
        {tab === "instances" && <InstancesView session={session} />}
        {tab === "config" && <AdminConfigDispatch session={session} />}
        {tab === "users" && <UsersView session={session} />}
        {tab === "grants" && <GrantsView session={session} />}
        {tab === "tokens" && <TokensView session={session} />}
        {tab === "sensitive" && <SensitiveFilterPanel session={session} />}
        {tab === "proxy" && <ProxyRoutesPanel session={session} />}
        {tab === "webhooks" && <AdminWebhooksPanel session={session} />}
      </div>
    </div>
  )
}

function AdminAssetScope() {
  const [targets, setTargets] = useState<TargetRef[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  useEffect(() => {
    let active = true
    assetRequest<{targets: TargetRef[]}>("workspace", undefined, new URLSearchParams({view: "targets"}))
      .then(data => { if (active) setTargets(data.targets) })
      .catch(err => { if (active) setError(err.message) })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [])
  return <section className="p-4"><PlatformAssetScope targets={targets} loading={loading} error={error} /></section>
}

function Toolbar({
  title,
  desc,
  onRefresh,
  loading,
  children,
}: {
  title: string
  desc: string
  onRefresh: () => void
  loading: boolean
  children?: React.ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-card/50 px-4 py-3">
      <div>
        <h2 className="text-sm font-semibold text-foreground">{title}</h2>
        <p className="text-xs text-muted-foreground">{desc}</p>
      </div>
      <div className="flex items-center gap-2">
        {children}
        <Button
          onClick={onRefresh}
          disabled={loading}
          size="sm"
        >
          <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
        </Button>
      </div>
    </div>
  )
}

function ErrorBar({ msg }: { msg: string }) {
  if (!msg) return null
  return (
    <StatusBanner tone="error" compact className="mx-4 mt-3">
      {msg}
    </StatusBanner>
  )
}

function StatusDot({ status }: { status: string }) {
  const online = status === "online"
  return (
    <span
      className={`inline-block h-2 w-2 rounded-full ${online ? "bg-success" : "bg-muted-foreground/40"}`}
      aria-hidden
    />
  )
}

/* ============================ 实例 ============================ */
function InstancesView({ session }: { session: Session }) {
  const [all, setAll] = useState<AdminInstance[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [now, setNow] = useState(() => Date.now())
  const [cluster, setCluster] = useState("")
  const [showHistory, setShowHistory] = useState(false)
  const [showArchived, setShowArchived] = useState(false)
  const [busyKey, setBusyKey] = useState("")
  const [busyAction, setBusyAction] = useState<"" | "archive" | "delete">("")
  const [notice, setNotice] = useState("")
  const archiveRef = useRef(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      setAll(await listInstances(session, { includeHistory: true }))
      setNow(Date.now())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    load()
  }, [load])

  const clusterOptions = useMemo(() => instanceClusterOptions(all), [all])
  const groups = useMemo(() => groupInstances(all, now, cluster), [all, now, cluster])
  const scope = useMemo(() => summarizeInstanceScope(all, now), [all, now])
  const historyKeys = new Set(groups.history.map((r) => `${r.cluster}/${r.instance}`))
  const archivedKeys = new Set(groups.archived.map((r) => `${r.cluster}/${r.instance}`))
  // Default to active rows. Historical and archived records remain discoverable
  // through separate explicit expanders so neither is mistaken for a live target.
  const rows = [
    ...groups.active,
    ...(showHistory ? groups.history : []),
    ...(showArchived ? groups.archived : []),
  ]

  async function changeArchive(row: AdminInstance) {
    if (archiveRef.current) return
    archiveRef.current = true
    const key = `${row.cluster}/${row.instance}`
    setBusyKey(key)
    setBusyAction("archive")
    setError("")
    setNotice("")
    try {
      if (row.archived) await unarchiveInstance(session, row)
      else await archiveInstance(session, row)
      await load()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      archiveRef.current = false
      setBusyKey("")
      setBusyAction("")
    }
  }

  async function removeInstance(row: AdminInstance) {
    if (archiveRef.current) return
    archiveRef.current = true
    const key = `${row.cluster}/${row.instance}`
    setBusyKey(key)
    setBusyAction("delete")
    setError("")
    setNotice("")
    try {
      const res = await deleteInstance(session, row)
      const rem = res?.remaining
      const parts: string[] = []
      if (rem?.active_credential_grants) parts.push(`有效凭据绑定 ${rem.active_credential_grants}`)
      if (rem?.revoking_credential_grants) parts.push(`吊销中凭据绑定 ${rem.revoking_credential_grants}`)
      if (rem?.proxy_routes) parts.push(`代理路由 ${rem.proxy_routes}`)
      if (rem?.user_grants) parts.push(`用户授权 ${rem.user_grants}`)
      setNotice(
        parts.length
          ? `已退役删除离线实例 ${key}（仍有关联引用未自动删除：${parts.join("、")}）`
          : `已退役删除离线实例 ${key}`,
      )
      await load()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      archiveRef.current = false
      setBusyKey("")
      setBusyAction("")
    }
  }

  return (
    <div>
      <Toolbar
        title="实例"
        desc={`在线 ${scope.online} · ${ACTIVE_INSTANCE_WINDOW_DAYS} 天内离线 ${scope.active - scope.online} · 历史 ${scope.history} · 已归档 ${scope.archived}`}
        onRefresh={load}
        loading={loading}
      >
        <Select
          aria-label="按集群筛选"
          value={cluster}
          onChange={(e) => setCluster(e.target.value)}
          className="w-56 text-xs"
        >
          <option value="">全部集群（{clusterOptions.length}）</option>
          {clusterOptions.map((option) => (
            <option key={option.name} value={option.name}>
              {option.name}（在线 {option.online}/{option.total}）
            </option>
          ))}
        </Select>
      </Toolbar>
      <ErrorBar msg={error} />
      {notice && (
        <div className="mx-4 mt-3 rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-foreground">
          {notice}
        </div>
      )}
      <div className="p-4">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>状态</TableHead>
              <TableHead>集群</TableHead>
              <TableHead>实例</TableHead>
              <TableHead>远端地址</TableHead>
              <TableHead>活动/排队</TableHead>
              <TableHead>最后心跳</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r) => (
              <TableRow
                key={`${r.cluster}/${r.instance}`}
                className={`${historyKeys.has(`${r.cluster}/${r.instance}`) ? "opacity-60" : ""} ${archivedKeys.has(`${r.cluster}/${r.instance}`) ? "bg-muted/40 opacity-60" : ""}`}
              >
                <TableCell>
                  <span className="flex items-center gap-2">
                    <StatusDot status={r.status} />
                    <span className="whitespace-nowrap text-xs text-muted-foreground">
                      {r.archived ? "已归档" : r.status === "online" ? (r.busy ? "忙碌" : "在线") : offlineLabel(r, now)}
                    </span>
                  </span>
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{r.cluster}</TableCell>
                <TableCell className="font-medium text-foreground">{r.instance}</TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">
                  {r.remote || "—"}
                </TableCell>
                <TableCell className="text-xs">
                  <span className="text-primary">{r.active_ops}</span>
                  <span className="text-muted-foreground"> / {r.queued_ops}</span>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{fmtTime(r.last_seen)}</TableCell>
                <TableCell className="text-right">
                  {r.status !== "online" && (
                    <div className="flex items-center justify-end gap-1.5">
                      <Button
                        type="button"
                        size="sm"
                        onClick={() => changeArchive(r)}
                        disabled={!!busyKey}
                      >
                        {busyKey === `${r.cluster}/${r.instance}` && busyAction === "archive" ? "处理中…" : r.archived ? "取消归档" : "归档"}
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="danger-outline"
                        onClick={() => removeInstance(r)}
                        disabled={!!busyKey}
                      >
                        {busyKey === `${r.cluster}/${r.instance}` && busyAction === "delete" ? "处理中…" : "退役删除"}
                      </Button>
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {!rows.length && !loading && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                  {groups.history.length || groups.archived.length
                    ? `没有在线或 ${ACTIVE_INSTANCE_WINDOW_DAYS} 天内离线的实例；历史和已归档实例默认折叠`
                    : cluster
                      ? "该集群暂无注册实例"
                      : "暂无注册实例"}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
        <div className="mt-2 flex flex-wrap gap-1">
          {groups.history.length > 0 && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-expanded={showHistory}
              onClick={() => setShowHistory((v) => !v)}
            >
              <span aria-hidden>{showHistory ? "▾" : "▸"}</span>
              {showHistory ? "收起" : "展开"}历史实例（{groups.history.length}）
              <span className="text-muted-foreground/70">
                · 离线超过 {ACTIVE_INSTANCE_WINDOW_DAYS} 天或没有心跳记录
              </span>
            </Button>
          )}
          {groups.archived.length > 0 && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-expanded={showArchived}
              onClick={() => setShowArchived((v) => !v)}
            >
              <span aria-hidden>{showArchived ? "▾" : "▸"}</span>
              {showArchived ? "收起" : "显示"}已归档实例（{groups.archived.length}）
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

/* ============================ 用户 ============================ */
function UsersView({ session }: { session: Session }) {
  const [rows, setRows] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [showCreate, setShowCreate] = useState(false)
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const [admin, setAdmin] = useState(false)
  const [busy, setBusy] = useState(false)
  const { prompt, dialog } = useAppDialog()

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      setRows(await listUsers(session))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    load()
  }, [load])

  async function submit() {
    if (!name.trim() || !password.trim()) return
    setBusy(true)
    setError("")
    try {
      await createUser(session, { name: name.trim(), password, admin })
      setName("")
      setPassword("")
      setAdmin(false)
      setShowCreate(false)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function toggleDisabled(u: AdminUser) {
    setError("")
    try {
      await setUserDisabled(session, { user_id: u.id, disabled: !u.disabled })
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function resetPassword(u: AdminUser) {
    const pw = await prompt({
      title: "设置新密码",
      message: `为用户「${u.name}」设置新密码。`,
      label: "新密码",
      inputType: "password",
      confirmLabel: "保存",
    })
    if (!pw) return
    setError("")
    try {
      await setUserPassword(session, { user_id: u.id, password: pw })
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const userSummary = useMemo(() => summarizeUsers(rows), [rows])
  // 已停用用户排到最后并淡化显示。
  const displayRows = useMemo(() => sortUsersForDisplay(rows), [rows])

  return (
    <div>
      <Toolbar
        title={`用户（共 ${userSummary.total} · admin ${userSummary.admins}${userSummary.disabled ? ` · 已停用 ${userSummary.disabled}` : ""}）`}
        desc="管理可登录 gateway 的账号"
        onRefresh={load}
        loading={loading}
      >
        <Button
          onClick={() => setShowCreate((v) => !v)}
          variant="primary" size="sm"
        >
          <PlusIcon width={14} height={14} /> 新建用户
        </Button>
      </Toolbar>
      <ErrorBar msg={error} />

      {showCreate && (
        <div data-assist-scope="" className="mx-4 mt-3 flex flex-wrap items-end gap-3 rounded-lg border bg-muted/40 p-3">
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            用户名
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-40"
              placeholder="ops-wang"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            初始密码
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-40"
              placeholder="••••••••"
            />
          </label>
          <label className="flex items-center gap-1.5 py-2 text-xs text-foreground">
            <Checkbox checked={admin} onChange={(e) => setAdmin(e.target.checked)} />
            授予 admin
          </label>
          <Button
            onClick={submit}
            disabled={busy || !name.trim() || !password.trim()}
            variant="primary" size="sm"
          >
            {busy ? "创建中…" : "创建"}
          </Button>
        </div>
      )}

      <div className="p-4">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>用户名</TableHead>
              <TableHead>角色</TableHead>
              <TableHead>授权数</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {displayRows.map((u) => (
              <TableRow key={u.id} className={u.disabled ? "opacity-50" : ""}>
                <TableCell className="font-medium text-foreground">{u.name}</TableCell>
                <TableCell>
                  {u.is_admin ? (
                    <Badge variant="primary">admin</Badge>
                  ) : (
                    <span className="text-xs text-muted-foreground">user</span>
                  )}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{u.grant_count}</TableCell>
                <TableCell>
                  <Badge variant={u.disabled ? "danger" : "success"}>
                    {u.disabled ? "已停用" : "正常"}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{fmtTime(u.created_at)}</TableCell>
                <TableCell>
                  <div className="flex items-center justify-end gap-1.5">
                    <Button
                      onClick={() => resetPassword(u)}
                      size="sm"
                    >
                      改密码
                    </Button>
                    <Button
                      onClick={() => toggleDisabled(u)}
                      size="sm"
                      variant={u.disabled ? "default" : "danger-outline"}
                      className={u.disabled ? "text-success" : undefined}
                    >
                      {u.disabled ? (
                        <>
                          <CheckIcon width={12} height={12} /> 启用
                        </>
                      ) : (
                        <>
                          <BanIcon width={12} height={12} /> 停用
                        </>
                      )}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {!rows.length && !loading && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                  暂无用户
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      {dialog}
    </div>
  )
}

/* ============================ 权限 ============================ */
function GrantsView({ session }: { session: Session }) {
  const [grants, setGrants] = useState<AdminGrant[]>([])
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [showCreate, setShowCreate] = useState(false)
  const [userId, setUserId] = useState("")
  // 集群和实例默认不选；Gateway 会把空值存成 "*"，所以未选择时不允许提交。
  const [cluster, setCluster] = useState("")
  const [instance, setInstance] = useState("")
  const [instances, setInstances] = useState<AdminInstance[]>([])
  const [instancesError, setInstancesError] = useState("")
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)

  const scope = useMemo(() => grantScopeOptions(instances), [instances])
  const instanceChoices = grantInstanceChoices(scope, cluster)
  const validation = validateGrantForm({ userId, cluster, instance }, scope)
  const scopeWarning = grantScopeWarning(cluster, instance)

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    setInstancesError("")
    try {
      const [g, u, i] = await Promise.all([
        listGrants(session),
        listUsers(session),
        listInstances(session, { includeHistory: true }).catch((e: Error) => {
          setInstancesError(`查询实例列表失败：${e.message}`)
          return [] as AdminInstance[]
        }),
      ])
      setGrants(g)
      setUsers(u)
      setInstances(i)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    load()
  }, [load])

  function selectCluster(next: string) {
    setCluster(next)
    setInstance(next === GRANT_ALL ? GRANT_ALL : "")
  }

  async function submit() {
    if (busyRef.current) return
    const checked = validateGrantForm({ userId, cluster, instance }, scope)
    if (!checked.ok) {
      setError(checked.error)
      return
    }
    busyRef.current = true
    setBusy(true)
    setError("")
    try {
      await createGrant(session, checked.body)
      setShowCreate(false)
      setCluster("")
      setInstance("")
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  async function remove(id: number) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setError("")
    try {
      await deleteGrant(session, id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  return (
    <div>
      <Toolbar
        title="权限"
        desc="授权：用户 × 集群/实例"
        onRefresh={load}
        loading={loading}
      >
        <Button
          onClick={() => setShowCreate((v) => !v)}
          variant="primary" size="sm"
        >
          <PlusIcon width={14} height={14} /> 新建授权
        </Button>
      </Toolbar>
      <ErrorBar msg={error} />

      {showCreate && (
        <div data-assist-scope="" className="mx-4 mt-3 flex flex-col gap-3 rounded-lg border bg-muted/40 p-3">
          <div className="flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              用户
              <Select value={userId} onChange={(e) => setUserId(e.target.value)} className="w-44">
                <option value="">选择用户…</option>
                {users.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.name}
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              集群
              <Select aria-label="集群" value={cluster} onChange={(e) => selectCluster(e.target.value)} className="w-44">
                <option value="">选择集群…</option>
                {scope.map((item) => (
                  <option key={item.name} value={item.name}>
                    {item.name}
                  </option>
                ))}
                <option value={GRANT_ALL}>全部集群（*）</option>
              </Select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              实例
              <Select
                aria-label="实例"
                value={instance}
                onChange={(e) => setInstance(e.target.value)}
                disabled={!cluster || cluster === GRANT_ALL}
                className="w-44 disabled:opacity-60"
              >
                {cluster === GRANT_ALL ? (
                  <option value={GRANT_ALL}>全部实例（*）</option>
                ) : (
                  <>
                    <option value="">{cluster ? "选择实例…" : "先选择集群"}</option>
                    {instanceChoices.map((name) => (
                      <option key={name} value={name}>
                        {name}
                      </option>
                    ))}
                    {cluster && <option value={GRANT_ALL}>该集群全部实例（*）</option>}
                  </>
                )}
              </Select>
            </label>
          </div>
          {instancesError && <p className="text-xs text-destructive">{instancesError}</p>}
          {!instancesError && !loading && scope.length === 0 && (
            <p className="text-xs text-muted-foreground">Gateway 还没有登记过实例，Agent 接入后才能按集群/实例授权。</p>
          )}
          {scopeWarning && (
            <p role="alert" className="rounded-md bg-warning/15 px-2.5 py-2 text-xs text-warning">
              {scopeWarning}
            </p>
          )}
          <p className="text-xs text-muted-foreground">
            授权后用户可使用该 target 的全部运维能力（含 Agent 升级）和它绑定的凭据；平台管理员和凭据管理不随 grant 下发。
          </p>
          <div>
            <Button
              onClick={submit}
              disabled={busy || !validation.ok}
              variant="primary" size="sm"
            >
              {busy ? "授权中…" : "授权"}
            </Button>
          </div>
        </div>
      )}

      <div className="p-4">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>用户</TableHead>
              <TableHead>集群</TableHead>
              <TableHead>实例</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {grants.map((g) => (
              <TableRow key={g.id}>
                <TableCell className="font-medium text-foreground">{g.user_name || g.user_id}</TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{g.cluster}</TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{g.instance}</TableCell>
                <TableCell>
                  <div className="flex justify-end">
                    <Button
                      onClick={() => remove(g.id)}
                      disabled={busy}
                      size="sm"
                      variant="danger-outline"
                    >
                      <TrashIcon width={12} height={12} /> 删除
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {!grants.length && !loading && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={4} className="py-8 text-center text-sm text-muted-foreground">
                  暂无授权
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

/* ============================ 令牌 ============================ */
function TokensView({ session }: { session: Session }) {
  const [rows, setRows] = useState<AdminToken[]>([])
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [showCreate, setShowCreate] = useState(false)
  const [userName, setUserName] = useState("")
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [created, setCreated] = useState("")
  const [showRevoked, setShowRevoked] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const [toggled, setToggled] = useState<ReadonlySet<string>>(() => new Set())

  const groups = useMemo(
    () => groupTokensByUser(rows, users, { now, showRevoked }),
    [rows, users, now, showRevoked],
  )
  const revokedTotal = rows.filter((t) => t.revoked).length

  function toggleGroup(key: string) {
    setToggled((current) => {
      const next = new Set(current)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      const [t, u] = await Promise.all([listTokens(session), listUsers(session)])
      setRows(t)
      setUsers(u)
      setNow(Date.now())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    load()
  }, [load])

  async function submit() {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setError("")
    setCreated("")
    try {
      const res = await createToken(session, {
        user: userName,
        name: name.trim() || undefined,
      })
      setCreated(res.token)
      setName("")
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  async function revoke(id: string) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setError("")
    try {
      await revokeToken(session, id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  return (
    <div>
      <Toolbar
        title="令牌"
        desc={`用户访问令牌 · ${groups.length} 个用户 · 共 ${rows.length} 个，已撤销 ${revokedTotal} 个`}
        onRefresh={load}
        loading={loading}
      >
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Checkbox checked={showRevoked} onChange={(e) => setShowRevoked(e.target.checked)} />
          显示已撤销（{revokedTotal}）
        </label>
        <Button
          onClick={() => {
            setShowCreate((v) => !v)
            setCreated("")
          }}
          variant="primary" size="sm"
        >
          <PlusIcon width={14} height={14} /> 签发令牌
        </Button>
      </Toolbar>
      <ErrorBar msg={error} />

      {showCreate && (
        <div data-assist-scope="" className="mx-4 mt-3 flex flex-col gap-3 rounded-lg border bg-muted/40 p-3">
          <div className="flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              所属用户
              <Select value={userName} onChange={(e) => setUserName(e.target.value)} className="w-40">
                <option value="">选择用户…</option>
                {users.map((u) => (
                  <option key={u.id} value={u.name}>
                    {u.name}
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              备注名
              <Input value={name} onChange={(e) => setName(e.target.value)} className="w-36" placeholder="可选" />
            </label>
            <Button
              onClick={submit}
              disabled={busy || !userName}
              variant="primary" size="sm"
            >
              {busy ? "签发中…" : "签发"}
            </Button>
          </div>
          {created && (
            <div className="flex items-center gap-2 rounded-lg border border-success/40 bg-success/10 p-2.5">
              <span className="text-xs text-muted-foreground">请立即复制（仅显示一次）：</span>
              <code className="flex-1 truncate font-mono text-xs text-foreground">{created}</code>
              <Button
                onClick={() => navigator.clipboard?.writeText(created)}
                size="sm"
              >
                <CopyIcon width={12} height={12} /> 复制
              </Button>
            </div>
          )}
        </div>
      )}

      <div className="p-4">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>备注名</TableHead>
              <TableHead>前缀</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead>过期时间</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          {groups.map((group) => {
            const collapsed = isTokenGroupCollapsed(group, toggled)
            return (
              <TableBody key={group.key || "unknown"}>
                <TableRow className={`bg-muted/40 ${group.userDisabled ? "opacity-60" : ""}`}>
                  <TableCell colSpan={6} className="py-2">
                    <button
                      type="button"
                      aria-expanded={!collapsed}
                      onClick={() => toggleGroup(group.key)}
                      className="flex w-full items-center gap-2 text-left text-xs"
                    >
                      <span aria-hidden className="text-muted-foreground">{collapsed ? "▸" : "▾"}</span>
                      <span className="font-medium text-foreground">{group.userName}</span>
                      {group.userDisabled && <span className="text-warning">用户已停用</span>}
                      <span className="text-muted-foreground">
                        令牌 {group.total} 个 · 可用 {group.usable}
                        {group.revoked > 0 ? ` · 已撤销 ${group.revoked}` : ""}
                      </span>
                    </button>
                  </TableCell>
                </TableRow>
                {!collapsed &&
                  group.rows.map(({ token: t, state }) => (
                    <TableRow key={t.id} className={state === "active" ? "" : "opacity-70"}>
                      <TableCell className="pl-8 text-xs text-foreground">
                        {t.name || "—"}
                        {t.id === session.tokenId && <span className="ml-2 text-primary">当前登录</span>}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{t.prefix}…</TableCell>
                      <TableCell>
                        <span className={`whitespace-nowrap text-xs ${TOKEN_STATE_TONE[state]}`}>
                          {TOKEN_STATE_LABELS[state]}
                        </span>
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">{fmtTime(t.created_at)}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {gatewayTime(t.expires_at) === null ? "不过期" : fmtTime(t.expires_at)}
                      </TableCell>
                      <TableCell>
                        <div className="flex justify-end">
                          {!t.revoked && (
                            <Button
                              onClick={() => revoke(t.id)}
                              disabled={busy}
                              size="sm"
                              variant="danger-outline"
                            >
                              <BanIcon width={12} height={12} /> 撤销
                            </Button>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
              </TableBody>
            )
          })}
          {!groups.length && !loading && (
            <TableBody>
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                  {rows.length ? "没有未撤销的令牌，已撤销令牌已隐藏" : "暂无令牌"}
                </TableCell>
              </TableRow>
            </TableBody>
          )}
        </Table>
      </div>
    </div>
  )
}

const TOKEN_STATE_TONE: Record<TokenState, string> = {
  active: "text-success",
  revoked: "text-destructive",
  user_disabled: "text-warning",
  user_missing: "text-warning",
  expired: "text-muted-foreground",
}
