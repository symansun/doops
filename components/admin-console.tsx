"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { Session } from "@/lib/client"
import {
  GRANT_ALL,
  grantInstanceChoices,
  grantScopeOptions,
  grantScopeWarning,
  validateGrantForm,
} from "@/lib/grant-form"
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
  type AdminUser,
  type AdminGrant,
  type AdminToken,
  type AdminInstance,
} from "@/lib/admin"
import { AdminConfigDispatch } from "./admin-config-dispatch"
import { ProxyRoutesPanel } from "./proxy-routes-panel"
import {
  ServerIcon,
  UsersIcon,
  ShieldIcon,
  KeyIcon,
  RefreshIcon,
  PlusIcon,
  TrashIcon,
  BanIcon,
  CheckIcon,
  CopyIcon,
  LayersIcon,
  PlugIcon,
} from "./icons"

type AdminTab =
  | "instances"
  | "config"
  | "users"
  | "grants"
  | "tokens"
  | "proxy"

const ADMIN_TABS: { id: AdminTab; label: string; icon: typeof ServerIcon }[] = [
  { id: "instances", label: "实例", icon: ServerIcon },
  { id: "config", label: "配置中心", icon: LayersIcon },
  { id: "users", label: "用户", icon: UsersIcon },
  { id: "grants", label: "权限", icon: ShieldIcon },
  { id: "tokens", label: "令牌", icon: KeyIcon },
  { id: "proxy", label: "代理路由", icon: PlugIcon },
]

function fmtTime(v?: string): string {
  if (!v) return "—"
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleString()
}

export function AdminConsole({
  session,
  defaultTab = "instances",
}: {
  session: Session
  defaultTab?: AdminTab
}) {
  const [tab, setTab] = useState<AdminTab>(defaultTab)

  useEffect(() => {
    setTab(defaultTab)
  }, [defaultTab])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <nav className="flex shrink-0 gap-1 overflow-x-auto border-b bg-card px-2">
        {ADMIN_TABS.map((t) => {
          const Icon = t.icon
          const active = tab === t.id
          return (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2.5 text-sm transition-colors ${
                active
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
            >
              <Icon width={16} height={16} />
              {t.label}
            </button>
          )
        })}
      </nav>
      <div className="min-h-0 flex-1 overflow-auto">
        {tab === "instances" && <InstancesView session={session} />}
        {tab === "config" && <AdminConfigDispatch session={session} />}
        {tab === "users" && <UsersView session={session} />}
        {tab === "grants" && <GrantsView session={session} />}
        {tab === "tokens" && <TokensView session={session} />}
        {tab === "proxy" && <ProxyRoutesPanel session={session} />}
      </div>
    </div>
  )
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
        <button
          onClick={onRefresh}
          disabled={loading}
          className="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs text-foreground transition-colors hover:bg-muted disabled:opacity-50"
        >
          <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} /> 刷新
        </button>
      </div>
    </div>
  )
}

function ErrorBar({ msg }: { msg: string }) {
  if (!msg) return null
  return (
    <div className="mx-4 mt-3 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
      {msg}
    </div>
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
  const [rows, setRows] = useState<AdminInstance[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      setRows(await listInstances(session))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    load()
  }, [load])

  return (
    <div>
      <Toolbar
        title="实例"
        desc="已注册到 gateway 的边缘节点及其连接状态"
        onRefresh={load}
        loading={loading}
      />
      <ErrorBar msg={error} />
      <div className="overflow-x-auto p-4">
        <table className="w-full min-w-[640px] border-collapse text-sm">
          <thead>
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">集群</th>
              <th className="px-3 py-2 font-medium">实例</th>
              <th className="px-3 py-2 font-medium">远端地址</th>
              <th className="px-3 py-2 font-medium">活动/排队</th>
              <th className="px-3 py-2 font-medium">最后心跳</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={`${r.cluster}/${r.instance}`} className="border-b border-border/50">
                <td className="px-3 py-2.5">
                  <span className="flex items-center gap-2">
                    <StatusDot status={r.status} />
                    <span className="text-xs text-muted-foreground">
                      {r.status === "online" ? (r.busy ? "忙碌" : "在线") : "离线"}
                    </span>
                  </span>
                </td>
                <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">{r.cluster}</td>
                <td className="px-3 py-2.5 font-medium text-foreground">{r.instance}</td>
                <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">
                  {r.remote || "—"}
                </td>
                <td className="px-3 py-2.5 text-xs">
                  <span className="text-primary">{r.active_ops}</span>
                  <span className="text-muted-foreground"> / {r.queued_ops}</span>
                </td>
                <td className="px-3 py-2.5 text-xs text-muted-foreground">{fmtTime(r.last_seen)}</td>
              </tr>
            ))}
            {!rows.length && !loading && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-sm text-muted-foreground">
                  暂无注册实例
                </td>
              </tr>
            )}
          </tbody>
        </table>
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
    const pw = window.prompt(`为用户「${u.name}」设置新密码：`)
    if (!pw) return
    setError("")
    try {
      await setUserPassword(session, { user_id: u.id, password: pw })
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div>
      <Toolbar title="用户" desc="管理可登录 gateway 的账号" onRefresh={load} loading={loading}>
        <button
          onClick={() => setShowCreate((v) => !v)}
          className="flex items-center gap-1.5 rounded-lg bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90"
        >
          <PlusIcon width={14} height={14} /> 新建用户
        </button>
      </Toolbar>
      <ErrorBar msg={error} />

      {showCreate && (
        <div className="mx-4 mt-3 flex flex-wrap items-end gap-3 rounded-lg border bg-muted/40 p-3">
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            用户名
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="input w-40"
              placeholder="ops-wang"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            初始密码
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="input w-40"
              placeholder="••••••••"
            />
          </label>
          <label className="flex items-center gap-1.5 py-2 text-xs text-foreground">
            <input type="checkbox" checked={admin} onChange={(e) => setAdmin(e.target.checked)} />
            授予 admin
          </label>
          <button
            onClick={submit}
            disabled={busy || !name.trim() || !password.trim()}
            className="rounded-lg bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
          >
            {busy ? "创建中…" : "创建"}
          </button>
        </div>
      )}

      <div className="overflow-x-auto p-4">
        <table className="w-full min-w-[620px] border-collapse text-sm">
          <thead>
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-3 py-2 font-medium">用户名</th>
              <th className="px-3 py-2 font-medium">角色</th>
              <th className="px-3 py-2 font-medium">授权数</th>
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">创建时间</th>
              <th className="px-3 py-2 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((u) => (
              <tr key={u.id} className="border-b border-border/50">
                <td className="px-3 py-2.5 font-medium text-foreground">{u.name}</td>
                <td className="px-3 py-2.5">
                  {u.is_admin ? (
                    <span className="rounded bg-primary/15 px-1.5 py-0.5 text-xs font-medium text-primary">
                      admin
                    </span>
                  ) : (
                    <span className="text-xs text-muted-foreground">user</span>
                  )}
                </td>
                <td className="px-3 py-2.5 text-xs text-muted-foreground">{u.grant_count}</td>
                <td className="px-3 py-2.5">
                  <span
                    className={`text-xs ${u.disabled ? "text-destructive" : "text-success"}`}
                  >
                    {u.disabled ? "已停用" : "正常"}
                  </span>
                </td>
                <td className="px-3 py-2.5 text-xs text-muted-foreground">{fmtTime(u.created_at)}</td>
                <td className="px-3 py-2.5">
                  <div className="flex items-center justify-end gap-1.5">
                    <button
                      onClick={() => resetPassword(u)}
                      className="rounded-md border px-2 py-1 text-xs text-foreground transition-colors hover:bg-muted"
                    >
                      改密码
                    </button>
                    <button
                      onClick={() => toggleDisabled(u)}
                      className={`flex items-center gap-1 rounded-md border px-2 py-1 text-xs transition-colors hover:bg-muted ${
                        u.disabled ? "text-success" : "text-destructive"
                      }`}
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
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {!rows.length && !loading && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-sm text-muted-foreground">
                  暂无用户
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
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
        listInstances(session).catch((e: Error) => {
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
    const checked = validateGrantForm({ userId, cluster, instance }, scope)
    if (!checked.ok) {
      setError(checked.error)
      return
    }
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
      setBusy(false)
    }
  }

  async function remove(id: number) {
    if (!window.confirm("确认删除该授权？")) return
    setError("")
    try {
      await deleteGrant(session, id)
      await load()
    } catch (e) {
      setError((e as Error).message)
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
        <button
          onClick={() => setShowCreate((v) => !v)}
          className="flex items-center gap-1.5 rounded-lg bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90"
        >
          <PlusIcon width={14} height={14} /> 新建授权
        </button>
      </Toolbar>
      <ErrorBar msg={error} />

      {showCreate && (
        <div className="mx-4 mt-3 flex flex-col gap-3 rounded-lg border bg-muted/40 p-3">
          <div className="flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              用户
              <select value={userId} onChange={(e) => setUserId(e.target.value)} className="input w-44">
                <option value="">选择用户…</option>
                {users.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              集群
              <select aria-label="集群" value={cluster} onChange={(e) => selectCluster(e.target.value)} className="input w-44">
                <option value="">选择集群…</option>
                {scope.map((item) => (
                  <option key={item.name} value={item.name}>
                    {item.name}
                  </option>
                ))}
                <option value={GRANT_ALL}>全部集群（*）</option>
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              实例
              <select
                aria-label="实例"
                value={instance}
                onChange={(e) => setInstance(e.target.value)}
                disabled={!cluster || cluster === GRANT_ALL}
                className="input w-44 disabled:opacity-60"
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
              </select>
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
            <button
              onClick={submit}
              disabled={busy || !validation.ok}
              className="rounded-lg bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
            >
              {busy ? "授权中…" : "授权"}
            </button>
          </div>
        </div>
      )}

      <div className="overflow-x-auto p-4">
        <table className="w-full min-w-[680px] border-collapse text-sm">
          <thead>
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-3 py-2 font-medium">用户</th>
              <th className="px-3 py-2 font-medium">集群</th>
              <th className="px-3 py-2 font-medium">实例</th>
              <th className="px-3 py-2 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {grants.map((g) => (
              <tr key={g.id} className="border-b border-border/50">
                <td className="px-3 py-2.5 font-medium text-foreground">{g.user_name || g.user_id}</td>
                <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">{g.cluster}</td>
                <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">{g.instance}</td>
                <td className="px-3 py-2.5">
                  <div className="flex justify-end">
                    <button
                      onClick={() => remove(g.id)}
                      className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-destructive transition-colors hover:bg-muted"
                    >
                      <TrashIcon width={12} height={12} /> 删除
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {!grants.length && !loading && (
              <tr>
                <td colSpan={5} className="px-3 py-8 text-center text-sm text-muted-foreground">
                  暂无授权
                </td>
              </tr>
            )}
          </tbody>
        </table>
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
  const [created, setCreated] = useState("")

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      const [t, u] = await Promise.all([listTokens(session), listUsers(session)])
      setRows(t)
      setUsers(u)
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
      setBusy(false)
    }
  }

  async function revoke(id: string) {
    if (!window.confirm("确认撤销该令牌？撤销后立即失效。")) return
    setError("")
    try {
      await revokeToken(session, id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div>
      <Toolbar
        title="令牌"
        desc="用户访问令牌"
        onRefresh={load}
        loading={loading}
      >
        <button
          onClick={() => {
            setShowCreate((v) => !v)
            setCreated("")
          }}
          className="flex items-center gap-1.5 rounded-lg bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90"
        >
          <PlusIcon width={14} height={14} /> 签发令牌
        </button>
      </Toolbar>
      <ErrorBar msg={error} />

      {showCreate && (
        <div className="mx-4 mt-3 flex flex-col gap-3 rounded-lg border bg-muted/40 p-3">
          <div className="flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              所属用户
              <select value={userName} onChange={(e) => setUserName(e.target.value)} className="input w-40">
                <option value="">选择用户…</option>
                {users.map((u) => (
                  <option key={u.id} value={u.name}>
                    {u.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              备注名
              <input value={name} onChange={(e) => setName(e.target.value)} className="input w-36" placeholder="可选" />
            </label>
            <button
              onClick={submit}
              disabled={busy || !userName}
              className="rounded-lg bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
            >
              {busy ? "签发中…" : "签发"}
            </button>
          </div>
          {created && (
            <div className="flex items-center gap-2 rounded-lg border border-success/40 bg-success/10 p-2.5">
              <span className="text-xs text-muted-foreground">请立即复制（仅显示一次）：</span>
              <code className="flex-1 truncate font-mono text-xs text-foreground">{created}</code>
              <button
                onClick={() => navigator.clipboard?.writeText(created)}
                className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-foreground transition-colors hover:bg-muted"
              >
                <CopyIcon width={12} height={12} /> 复制
              </button>
            </div>
          )}
        </div>
      )}

      <div className="overflow-x-auto p-4">
        <table className="w-full min-w-[900px] border-collapse text-sm">
          <thead>
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-3 py-2 font-medium">类型</th>
              <th className="px-3 py-2 font-medium">归属</th>
              <th className="px-3 py-2 font-medium">前缀</th>
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">创建时间</th>
              <th className="px-3 py-2 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((t) => (
              <tr key={t.id} className="border-b border-border/50">
                <td className="px-3 py-2.5">
                  <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                    user
                  </span>
                </td>
                <td className="px-3 py-2.5 text-xs text-foreground">
                  {t.user_name || t.user_id || "—"}
                  {t.name ? <span className="text-muted-foreground"> · {t.name}</span> : null}
                </td>
                <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">{t.prefix}…</td>
                <td className="px-3 py-2.5">
                  <span className={`text-xs ${t.revoked ? "text-destructive" : "text-success"}`}>
                    {t.revoked ? "已撤销" : "有效"}
                  </span>
                </td>
                <td className="px-3 py-2.5 text-xs text-muted-foreground">{fmtTime(t.created_at)}</td>
                <td className="px-3 py-2.5">
                  <div className="flex justify-end">
                    {!t.revoked && (
                      <button
                        onClick={() => revoke(t.id)}
                        className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-destructive transition-colors hover:bg-muted"
                      >
                        <BanIcon width={12} height={12} /> 撤销
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
            {!rows.length && !loading && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-sm text-muted-foreground">
                  暂无令牌
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
