"use client"

import { Badge, Button, Input, Select, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Textarea } from "./ui"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import type { Session } from "@/lib/client"
import {
  addSensitiveRules,
  addSensitiveWhitelist,
  deleteSensitiveRule,
  deleteSensitiveWhitelist,
  listInstances,
  listSensitiveFilter,
  listUsers,
  setSensitiveFilterEnabled,
  setSensitiveRuleEnabled,
  type AdminInstance,
  type AdminUser,
  type SensitiveFilterView,
  type SensitiveRule,
  type SensitiveWhitelistEntry,
} from "@/lib/admin"
import {
  SENSITIVE_SCOPE_OPTIONS,
  describeSensitiveFilterError,
  filterSensitiveRules,
  filterSensitiveWhitelist,
  parseSensitiveRules,
  parseSensitiveWhitelist,
  sensitiveScopeLabel,
  sensitiveWhitelistLabel,
  type SensitiveFilterOp,
  type SensitiveScope,
  type SensitiveWhitelistKind,
} from "@/lib/sensitive-filter"
import { BanIcon, CheckIcon, PlusIcon, RefreshIcon, TrashIcon } from "./icons"

function fmtTime(v?: string): string {
  if (!v) return "—"
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleString()
}

// 敏感词与白名单：对应 Gateway /v1/admin/sensitive-filter，仅平台管理员可用。
// 提示词和原始命令共用一个过滤器；白名单按用户或目标整体放行。
export function SensitiveFilterPanel({ session }: { session: Session }) {
  const [view, setView] = useState<SensitiveFilterView>({ enabled: false, rules: [], whitelist: [] })
  const [users, setUsers] = useState<AdminUser[]>([])
  const [instances, setInstances] = useState<AdminInstance[]>([])
  const [loaded, setLoaded] = useState(false)
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)
  const [blocked, setBlocked] = useState<{ message: string; kind: string } | null>(null)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  // 只采用最后一次列表请求的结果，避免慢的旧响应覆盖新数据。
  const seq = useRef(0)

  const [ruleText, setRuleText] = useState("")
  const [ruleScope, setRuleScope] = useState<SensitiveScope>("all")
  const [ruleQuery, setRuleQuery] = useState("")
  const [whitelistQuery, setWhitelistQuery] = useState("")
  const [kind, setKind] = useState<SensitiveWhitelistKind>("user")
  const [userID, setUserID] = useState("")
  const [cluster, setCluster] = useState("")
  const [instance, setInstance] = useState("")

  const showError = useCallback((e: unknown, op: SensitiveFilterOp) => {
    const view = describeSensitiveFilterError(e, op)
    if (view.kind === "error") setError(view.message)
    else setBlocked({ message: view.message, kind: view.kind })
    return view
  }, [])

  const refresh = useCallback(async (): Promise<boolean> => {
    const id = ++seq.current
    try {
      const next = await listSensitiveFilter(session)
      if (id !== seq.current) return true
      setView(next)
      setLoaded(true)
      setFailed(false)
      setBlocked(null)
      return true
    } catch (e) {
      if (id === seq.current) {
        setFailed(true)
        showError(e, "load")
      }
      return false
    }
  }, [session, showError])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      await refresh()
    } finally {
      if (seq.current) setLoading(false)
    }
    // 用户与实例列表用于白名单选择，失败不影响规则管理。
    listUsers(session).then(setUsers, () => setUsers([]))
    listInstances(session).then(setInstances, () => setInstances([]))
  }, [session, refresh])

  useEffect(() => {
    load()
  }, [load])

  async function run(op: SensitiveFilterOp, action: () => Promise<void>) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setError("")
    setNotice("")
    try {
      await action()
    } catch (e) {
      const view = showError(e, op)
      if (view.reload) await refresh()
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  async function toggleFilter() {
    const next = !view.enabled
    await run("toggleFilter", async () => {
      const enabled = await setSensitiveFilterEnabled(session, next)
      setNotice(enabled
        ? "敏感词过滤已启用，新的提示词和命令会立即被检查"
        : "敏感词过滤已停用，规则将不再拦截任何请求")
      await refresh()
    })
  }

  async function addRules() {
    const parsed = parseSensitiveRules(ruleText, ruleScope)
    if (!parsed.ok) {
      setError(parsed.error)
      setNotice("")
      return
    }
    await run("addRules", async () => {
      const result = await addSensitiveRules(session, parsed.rules)
      const added = result.rules.length
      const skipped = result.skipped.length
      if (added === 0) setNotice("这些规则都已存在，没有新增")
      else if (skipped > 0) setNotice("已添加 " + added + " 条规则，跳过 " + skipped + " 条重复项")
      else setNotice("已添加 " + added + " 条规则")
      setRuleText("")
      await refresh()
    })
  }

  async function toggle(rule: SensitiveRule) {
    await run("toggleRule", async () => {
      await setSensitiveRuleEnabled(session, rule.id, !rule.enabled)
      setNotice((rule.enabled ? "已停用规则 " : "已启用规则 ") + rule.text)
      await refresh()
    })
  }

  async function removeRule(rule: SensitiveRule) {
    await run("deleteRule", async () => {
      await deleteSensitiveRule(session, rule.id)
      setNotice("已删除规则 " + rule.text)
      await refresh()
    })
  }

  async function addWhitelist() {
    const parsed = parseSensitiveWhitelist(kind, { userID, cluster, instance })
    if (!parsed.ok) {
      setError(parsed.error)
      setNotice("")
      return
    }
    await run("addWhitelist", async () => {
      const entry = await addSensitiveWhitelist(session, parsed.value)
      setNotice("已添加白名单：" + sensitiveWhitelistLabel(entry))
      if (kind === "user") setUserID("")
      else {
        setCluster("")
        setInstance("")
      }
      await refresh()
    })
  }

  async function removeWhitelist(entry: SensitiveWhitelistEntry) {
    await run("deleteWhitelist", async () => {
      await deleteSensitiveWhitelist(session, {
        kind: entry.kind,
        user_id: entry.user_id,
        cluster: entry.cluster,
        instance: entry.instance,
      })
      setNotice("已删除白名单：" + sensitiveWhitelistLabel(entry))
      await refresh()
    })
  }

  const rules = useMemo(() => filterSensitiveRules(view.rules, ruleQuery), [view.rules, ruleQuery])
  const whitelist = useMemo(() => filterSensitiveWhitelist(view.whitelist, whitelistQuery), [view.whitelist, whitelistQuery])
  const clusters = useMemo(() => Array.from(new Set(instances.map((item) => item.cluster))).sort(), [instances])
  const instanceOptions = useMemo(
    () => instances.filter((item) => !cluster.trim() || item.cluster === cluster.trim()),
    [instances, cluster],
  )
  const enabledCount = view.rules.filter((rule) => rule.enabled).length
  const empty = loaded && !failed && view.rules.length === 0 && view.whitelist.length === 0

  if (blocked) {
    return (
      <div className="p-4">
        <StatusBanner tone={blocked.kind === "auth" ? "warning" : "error"}>{blocked.message}</StatusBanner>
      </div>
    )
  }

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-card/50 px-4 py-3">
        <div>
          <h2 className="text-sm font-semibold text-foreground">敏感词</h2>
          <p className="text-xs text-muted-foreground">
            Gateway 在鉴权后统一检查用户提交的提示词、终端和 Shell 原始命令；命中即拦截，且不会把命中词回显给用户
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={view.enabled ? "success" : "default"}>
            {view.enabled ? "过滤已开启" : "过滤已关闭"}
          </Badge>
          <Button
            onClick={() => toggleFilter()}
            disabled={!loaded || loading || busy}
            variant={view.enabled ? "default" : "primary"}
            size="sm"
          >
            {view.enabled ? <BanIcon className="size-4" /> : <CheckIcon className="size-4" />}
            {view.enabled ? "停用过滤" : "启用过滤"}
          </Button>
          <Button onClick={() => load()} disabled={loading || busy} variant="default" size="sm">
            <RefreshIcon className="size-4" />
            刷新
          </Button>
        </div>
      </div>

      <div className="space-y-4 p-4">
        {loaded && !failed && !view.enabled && (
          <StatusBanner tone="warning">
            敏感词过滤当前已关闭（默认状态）。规则和白名单会保留，但不会拦截任何提示词或命令；启用后对新的请求立即生效。
          </StatusBanner>
        )}

        <StatusBanner tone="info" compact>
          白名单按用户或 Agent 目标整体放行本过滤器。Agent 自身的静态 commandDenyRules 继续负责 AI 自行发起的 Bash 与结构化 K8s 命令；
          Gateway 看不到 doagent 内部工具调用，两层规则互补而非替代。
        </StatusBanner>

        {error && <StatusBanner tone="error">{error}</StatusBanner>}
        {notice && <StatusBanner tone="success">{notice}</StatusBanner>}
        {failed && !error && <StatusBanner tone="error">敏感词配置加载失败，请稍后重试。</StatusBanner>}
        {loading && !loaded && <StatusBanner tone="info">正在加载敏感词配置…</StatusBanner>}

        <section className="rounded-lg border bg-card">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
            <div>
              <h3 className="text-sm font-semibold">规则</h3>
              <p className="text-xs text-muted-foreground">
                共 {view.rules.length} 条，启用 {enabledCount} 条；{view.enabled ? "总开关已开启" : "总开关已关闭，规则暂不生效"}；匹配忽略大小写并折叠连续空白
              </p>
            </div>
            <Input
              value={ruleQuery}
              onChange={(e) => setRuleQuery(e.target.value)}
              placeholder="搜索敏感词"
              scale="sm"
              className="w-48"
              aria-label="搜索敏感词规则"
            />
          </div>

          <div className="flex flex-col gap-2 border-b px-4 py-3 md:flex-row md:items-start">
            <Textarea
              value={ruleText}
              onChange={(e) => setRuleText(e.target.value)}
              placeholder="每行一条，例如：rm -rf /"
              rows={3}
              className="min-h-20 flex-1 font-mono"
              aria-label="批量输入敏感词"
            />
            <div className="flex shrink-0 flex-col gap-2 md:w-56">
              <Select
                value={ruleScope}
                onChange={(e) => setRuleScope(e.target.value as SensitiveScope)}
                scale="sm"
                aria-label="规则适用范围"
              >
                {SENSITIVE_SCOPE_OPTIONS.map((item) => (
                  <option key={item.value} value={item.value}>{item.label}</option>
                ))}
              </Select>
              <Button onClick={() => addRules()} disabled={busy || !ruleText.trim()} variant="primary" size="sm">
                <PlusIcon className="size-4" />
                添加规则
              </Button>
            </div>
          </div>

          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>敏感词</TableHead>
                  <TableHead>适用范围</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>更新时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rules.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                      {view.rules.length === 0 ? "还没有敏感词规则" : "没有匹配的规则"}
                    </TableCell>
                  </TableRow>
                ) : rules.map((rule) => (
                  <TableRow key={rule.id}>
                    <TableCell className="font-mono text-xs">{rule.text}</TableCell>
                    <TableCell><Badge variant="outline">{sensitiveScopeLabel(rule.scope)}</Badge></TableCell>
                    <TableCell>
                      <Badge variant={rule.enabled ? "success" : "default"}>{rule.enabled ? "已启用" : "已停用"}</Badge>
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{fmtTime(rule.updated_at || rule.created_at)}</TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1">
                        <Button onClick={() => toggle(rule)} disabled={busy} variant="ghost" size="sm">
                          {rule.enabled ? <BanIcon className="size-4" /> : <CheckIcon className="size-4" />}
                          {rule.enabled ? "停用" : "启用"}
                        </Button>
                        <Button onClick={() => removeRule(rule)} disabled={busy} variant="danger-outline" size="sm">
                          <TrashIcon className="size-4" />
                          删除
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </section>

        <section className="rounded-lg border bg-card">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
            <div>
              <h3 className="text-sm font-semibold">白名单</h3>
              <p className="text-xs text-muted-foreground">命中的用户或目标跳过全部规则；实例留空表示该集群下所有 Agent</p>
            </div>
            <Input
              value={whitelistQuery}
              onChange={(e) => setWhitelistQuery(e.target.value)}
              placeholder="搜索用户或目标"
              scale="sm"
              className="w-48"
              aria-label="搜索白名单"
            />
          </div>

          <div className="grid gap-2 border-b px-4 py-3 md:grid-cols-[8rem_1fr_1fr_auto] md:items-end">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              类型
              <Select
                value={kind}
                onChange={(e) => {
                  setKind(e.target.value as SensitiveWhitelistKind)
                  setError("")
                }}
                scale="sm"
                aria-label="白名单类型"
              >
                <option value="user">用户</option>
                <option value="target">Agent 目标</option>
              </Select>
            </label>
            {kind === "user" ? (
              <label className="flex flex-col gap-1 text-xs text-muted-foreground md:col-span-2">
                用户
                <Select value={userID} onChange={(e) => setUserID(e.target.value)} scale="sm" aria-label="选择用户">
                  <option value="">{users.length === 0 ? "暂无可选用户" : "请选择用户"}</option>
                  {users.map((user) => (
                    <option key={user.id} value={user.id}>{user.name + "（" + user.id + "）"}</option>
                  ))}
                </Select>
              </label>
            ) : (
              <>
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  集群
                  <Input
                    value={cluster}
                    onChange={(e) => setCluster(e.target.value)}
                    list="sensitive-cluster-options"
                    placeholder="例如 prod-cn"
                    scale="sm"
                    aria-label="集群"
                  />
                  <datalist id="sensitive-cluster-options">
                    {clusters.map((item) => <option key={item} value={item} />)}
                  </datalist>
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                  实例（可留空）
                  <Input
                    value={instance}
                    onChange={(e) => setInstance(e.target.value)}
                    list="sensitive-instance-options"
                    placeholder="留空表示整个集群"
                    scale="sm"
                    aria-label="实例"
                  />
                  <datalist id="sensitive-instance-options">
                    {instanceOptions.map((item) => <option key={item.cluster + "/" + item.instance} value={item.instance} />)}
                  </datalist>
                </label>
              </>
            )}
            <Button onClick={() => addWhitelist()} disabled={busy} variant="primary" size="sm" className="md:mb-0.5">
              <PlusIcon className="size-4" />
              添加白名单
            </Button>
          </div>

          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>类型</TableHead>
                  <TableHead>对象</TableHead>
                  <TableHead>创建时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {whitelist.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={4} className="py-8 text-center text-muted-foreground">
                      {view.whitelist.length === 0 ? "还没有白名单" : "没有匹配的白名单"}
                    </TableCell>
                  </TableRow>
                ) : whitelist.map((entry, index) => (
                  <TableRow key={entry.kind + "-" + (entry.user_id || entry.cluster + "/" + (entry.instance || "*")) + "-" + index}>
                    <TableCell><Badge variant={entry.kind === "user" ? "primary" : "outline"}>{entry.kind === "user" ? "用户" : "目标"}</Badge></TableCell>
                    <TableCell className="text-xs">{sensitiveWhitelistLabel(entry)}</TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{fmtTime(entry.created_at)}</TableCell>
                    <TableCell>
                      <div className="flex justify-end">
                        <Button onClick={() => removeWhitelist(entry)} disabled={busy} variant="danger-outline" size="sm">
                          <TrashIcon className="size-4" />
                          删除
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </section>

        {empty && (
          <p className="text-xs text-muted-foreground">
            尚未配置任何规则或白名单，{view.enabled ? "当前没有可拦截的内容。" : "且过滤总开关已关闭，Gateway 不会拦截提示词或命令。"}
          </p>
        )}
      </div>
    </div>
  )
}
