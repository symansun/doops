"use client"

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import {
  CheckCircle2,
  Plug,
  Plus,
  RefreshCw,
  Send,
  Terminal,
  Trash2,
  Wrench,
  XCircle,
} from "lucide-react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import {
  deletePlatformMCPServer,
  fetchPlatformMCPServers,
  probeMCPServerOnTarget,
  syncMCPServersToAgents,
  togglePlatformMCPServer,
  upsertPlatformMCPServer,
  type MCPInspectServerResult,
  type MCPSyncTargetResult,
  type PlatformMCPServerRecord,
} from "@/lib/mcp-registry"
import { Badge, Button, Card, CardContent, EmptyState, FilterBar, Inline, Input, PageHeader, Select, Skeleton, Stack, StatusBanner, Switch, Textarea } from "./ui"

function fmtStamp(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "尚未探测"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function parseKeyValueLines(raw: string, sep: "=" | ":"): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const idx = trimmed.indexOf(sep)
    if (idx <= 0) continue
    const k = trimmed.slice(0, idx).trim()
    const v = trimmed.slice(idx + 1).trim()
    if (k) out[k] = v
  }
  return out
}

export function MCPRegistryPanel({
  session,
  targets,
  onOpenSkills,
  onOpenInspection,
}: {
  session: Session
  targets: Target[]
  onOpenSkills?: () => void
  onOpenInspection?: () => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const onlineTargets = useMemo(() => targets.filter((t) => t.status !== "offline"), [targets])
  const [selectedTarget, setSelectedTarget] = useState<string>("")
  const [servers, setServers] = useState<PlatformMCPServerRecord[]>([])
  const [liveByName, setLiveByName] = useState<Map<string, MCPInspectServerResult>>(new Map())
  const [expandedTools, setExpandedTools] = useState<Record<string, boolean>>({ doops: true, dosql: true })
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [probingSlug, setProbingSlug] = useState("")
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [syncResults, setSyncResults] = useState<MCPSyncTargetResult[]>([])
  const [revision, setRevision] = useState(0)

  // Add / Edit form state
  const [formOpen, setFormOpen] = useState(false)
  const [editingSlug, setEditingSlug] = useState("")
  const [editingEnvKeys, setEditingEnvKeys] = useState<string[]>([])
  const [editingHeaderKeys, setEditingHeaderKeys] = useState<string[]>([])
  const [envDirty, setEnvDirty] = useState(false)
  const [headersDirty, setHeadersDirty] = useState(false)
  const [slug, setSlug] = useState("")
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [transport, setTransport] = useState<"stdio" | "http" | "sse">("stdio")
  const [command, setCommand] = useState("")
  const [argsText, setArgsText] = useState("")
  const [url, setUrl] = useState("")
  const [envText, setEnvText] = useState("")
  const [headersText, setHeadersText] = useState("")
  const [timeoutSecs, setTimeoutSecs] = useState("120")

  useEffect(() => {
    if (!selectedTarget && onlineTargets.length > 0) {
      setSelectedTarget(onlineTargets[0].key)
    }
  }, [onlineTargets, selectedTarget])

  const reload = useCallback(() => setRevision((v) => v + 1), [])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    fetchPlatformMCPServers(session, { target: selectedTarget || undefined, probe: false }, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        setServers(res.servers || [])
        if (res.agent_servers) {
          const map = new Map<string, MCPInspectServerResult>()
          for (const item of res.agent_servers) map.set(item.name, item)
          setLiveByName(map)
        }
        setLoading(false)
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error).message)
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, selectedTarget, revision])

  function openNewForm() {
    setEditingSlug("")
    setEditingEnvKeys([])
    setEditingHeaderKeys([])
    setEnvDirty(false)
    setHeadersDirty(false)
    setSlug("")
    setName("")
    setDescription("")
    setTransport("stdio")
    setCommand("")
    setArgsText("")
    setUrl("")
    setEnvText("")
    setHeadersText("")
    setTimeoutSecs("120")
    setFormOpen(true)
  }

  function openEditForm(srv: PlatformMCPServerRecord) {
    setEditingSlug(srv.slug)
    setEditingEnvKeys(srv.env_keys || [])
    setEditingHeaderKeys(srv.header_keys || [])
    setEnvDirty(false)
    setHeadersDirty(false)
    setSlug(srv.slug)
    setName(srv.name)
    setDescription(srv.description || "")
    setTransport(srv.transport)
    setCommand(srv.command || "")
    setArgsText((srv.args || []).join("\n"))
    setUrl(srv.url || "")
    setEnvText("")
    setHeadersText("")
    setTimeoutSecs(String(srv.tool_timeout_secs || 120))
    setFormOpen(true)
  }

  function currentTargetRef() {
    if (!selectedTarget) return undefined
    const [cluster, instance] = selectedTarget.split("/")
    return cluster && instance ? { cluster, instance } : undefined
  }

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const cleanArgs = argsText
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter(Boolean)
      const payload: Parameters<typeof upsertPlatformMCPServer>[1] = {
        slug: (editingSlug || slug).trim().toLowerCase(),
        name: name.trim() || slug.trim(),
        description: description.trim(),
        transport,
        command: transport === "stdio" ? command.trim() : "",
        args: transport === "stdio" ? cleanArgs : [],
        url: transport !== "stdio" ? url.trim() : "",
        tool_timeout_secs: Math.max(10, Math.min(1800, Number(timeoutSecs) || 120)),
      }
      if (transport === "stdio") {
        if (!editingSlug || envDirty) {
          payload.env = parseKeyValueLines(envText, "=")
        }
      } else if (!editingSlug || headersDirty) {
        payload.headers = parseKeyValueLines(headersText, ":")
      }
      const res = await upsertPlatformMCPServer(session, payload)
      setFormOpen(false)
      setNotice(`已保存 MCP「${res.server.name}」（${res.server.slug}），可点击「探测工具」验证连通性并「同步到在线 Agent」使其生效。`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleProbe(targetSlug?: string) {
    if (busy || probingSlug) return
    const tgt = currentTargetRef()
    if (!tgt) {
      setError("当前没有在线 Agent 可用于执行真实 MCP tools/list 探测")
      return
    }
    setProbingSlug(targetSlug || "__all__")
    setError("")
    setNotice("")
    try {
      const res = await probeMCPServerOnTarget(session, { slug: targetSlug, target: tgt })
      setServers(res.servers || [])
      setLiveByName((prev) => {
        const next = new Map(prev)
        for (const item of res.probe_results || []) next.set(item.name, item)
        return next
      })
      if (targetSlug) {
        setExpandedTools((prev) => ({ ...prev, [targetSlug]: true }))
      }
      const okCount = (res.probe_results || []).filter((r) => r.status === "ok").length
      const toolCount = (res.probe_results || []).reduce((sum, r) => sum + (r.tools?.length || 0), 0)
      setNotice(
        `已在 ${tgt.cluster}/${tgt.instance} 完成真实 MCP 协议探测：${okCount}/${res.probe_results.length} 个连通，共发现 ${toolCount} 个可用工具。`,
      )
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setProbingSlug("")
    }
  }

  async function handleSync() {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const res = await syncMCPServersToAgents(session)
      setSyncResults(res.results || [])
      const ok = (res.results || []).filter((r) => r.status === "ok").length
      setNotice(`已将启用的自定义 MCP 同步至 ${ok}/${res.results.length} 个在线 Agent 的 /root/.agent/settings.json（AI 助手与智能巡检统一生效）。`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleToggle(srv: PlatformMCPServerRecord) {
    if (busy) return
    setBusy(true)
    setError("")
    try {
      await togglePlatformMCPServer(session, srv.slug, !srv.enabled)
      setNotice(`已${srv.enabled ? "停用" : "启用"}「${srv.name}」，点击「同步到在线 Agent」使变更在目标生效。`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(srv: PlatformMCPServerRecord) {
    if (busy) return
    setBusy(true)
    setError("")
    try {
      await deletePlatformMCPServer(session, srv.slug)
      setNotice(`已删除自定义 MCP「${srv.name}」，点击「同步到在线 Agent」可从目标配置移除。`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const systemCount = servers.filter((s) => s.scope === "system").length
  const customCount = servers.filter((s) => s.scope === "custom").length
  const totalTools = servers.reduce((sum, s) => sum + (s.tools?.length || 0), 0)

  return (
    <section aria-label="MCP 管理" className="space-y-4">
      <PageHeader
        title="MCP 管理"
        badge={
          <>
            <Badge variant="outline">内置 {systemCount}</Badge>
            <Badge variant="outline">自定义 {customCount}</Badge>
            {totalTools > 0 && <Badge variant="primary">已探测工具 {totalTools}</Badge>}
          </>
        }
        description="管理 Agent 真实挂载的 MCP Server（stdio / HTTP / SSE），执行真实 JSON-RPC tools/list 探测并同步至目标 settings.json，供 AI 助手与智能巡检统一调用。"
        actions={
          <>
            {onOpenSkills && (
              <Button size="sm" variant="ghost" onClick={onOpenSkills}>
                Skill 管理
              </Button>
            )}
            {onOpenInspection && (
              <Button size="sm" variant="ghost" onClick={onOpenInspection}>
                智能巡检
              </Button>
            )}
            {isAdmin && (
              <Button size="sm" variant="primary" onClick={() => (formOpen ? setFormOpen(false) : openNewForm())}>
                <Plus width={14} height={14} />
                {formOpen ? "收起表单" : "接入 MCP"}
              </Button>
            )}
          </>
        }
      />

      {/* Action Toolbar */}
      <FilterBar className="justify-between">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs text-muted-foreground">探测目标：</span>
          <Select
            scale="sm"
            aria-label="选择探测目标"
            className="w-52"
            value={selectedTarget}
            onChange={(e) => setSelectedTarget(e.target.value)}
          >
            {onlineTargets.length === 0 ? (
              <option value="">无在线 Agent</option>
            ) : (
              onlineTargets.map((t) => (
                <option key={t.key} value={t.key}>
                  {t.cluster}/{t.instance}
                </option>
              ))
            )}
          </Select>
          <Button
            size="sm"
            disabled={busy || Boolean(probingSlug) || onlineTargets.length === 0}
            onClick={() => void handleProbe()}
          >
            <Wrench width={14} height={14} />
            {probingSlug === "__all__" ? "探测中…" : "探测全部工具 (tools/list)"}
          </Button>
        </div>
        <Inline gap={2}>
          {isAdmin && (
            <Button
              size="sm"
              disabled={busy || onlineTargets.length === 0}
              onClick={() => void handleSync()}
            >
              <Send width={14} height={14} />
              同步到在线 Agent ({onlineTargets.length})
            </Button>
          )}
          <Button size="sm" variant="ghost" disabled={loading} onClick={reload}>
            <RefreshCw width={14} height={14} className={loading ? "animate-spin" : ""} />
          </Button>
        </Inline>
      </FilterBar>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

      {syncResults.length > 0 && (
        <Card>
          <CardContent className="space-y-1.5 p-3 text-xs">
            <div className="font-medium text-foreground">最近一次同步回执</div>
            <div className="flex flex-wrap gap-2">
              {syncResults.map((r) => (
                <Badge key={`${r.cluster}/${r.instance}`} variant={r.status === "ok" ? "success" : "danger"}>
                  {r.cluster}/{r.instance} · {r.status === "ok" ? `已生效 ${r.total_servers} 个（含自定义 ${r.synced_count}）` : r.error || r.status}
                </Badge>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Create / Edit Custom MCP Form */}
      {isAdmin && formOpen && (
        <Card>
          <CardContent className="p-4">
            <form onSubmit={(e) => void handleSave(e)} className="space-y-3">
              <div className="flex items-center justify-between">
                <h2 className="text-sm font-semibold text-foreground">
                  {editingSlug ? `编辑自定义 MCP：${editingSlug}` : "接入外部 MCP Server"}
                </h2>
                <span className="text-2xs text-muted-foreground">
                  仅配置协议参数，切勿在普通环境变量或 Header 中粘贴高危明文私钥
                </span>
              </div>

              <div className="grid gap-3 sm:grid-cols-4">
                <label className="text-xs text-muted-foreground">
                  唯一标识 (slug)
                  <Input
                    scale="sm"
                    required
                    disabled={Boolean(editingSlug)}
                    className="mt-1 w-full font-mono"
                    placeholder="如 prometheus 或 net-probe"
                    value={slug}
                    onChange={(e) => setSlug(e.target.value)}
                  />
                </label>
                <label className="text-xs text-muted-foreground">
                  显示名称
                  <Input
                    scale="sm"
                    required
                    className="mt-1 w-full"
                    placeholder="如 Prometheus 指标 MCP"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </label>
                <label className="text-xs text-muted-foreground">
                  传输协议
                  <Select
                    scale="sm"
                    className="mt-1 w-full"
                    value={transport}
                    onChange={(e) => setTransport(e.target.value as "stdio" | "http" | "sse")}
                  >
                    <option value="stdio">stdio（本地命令进程）</option>
                    <option value="http">http（Streamable HTTP）</option>
                    <option value="sse">sse（Server-Sent Events）</option>
                  </Select>
                </label>
                <label className="text-xs text-muted-foreground">
                  单次工具超时 (秒)
                  <Input
                    scale="sm"
                    type="number"
                    min={10}
                    max={1800}
                    className="mt-1 w-full font-mono"
                    value={timeoutSecs}
                    onChange={(e) => setTimeoutSecs(e.target.value)}
                  />
                </label>
              </div>

              {transport === "stdio" ? (
                <div className="grid gap-3 sm:grid-cols-2">
                  <label className="text-xs text-muted-foreground">
                    启动命令 (command)
                    <Input
                      scale="sm"
                      required
                      className="mt-1 w-full font-mono"
                      placeholder="例如：/usr/local/bin/my-mcp 或 npx / python3"
                      value={command}
                      onChange={(e) => setCommand(e.target.value)}
                    />
                  </label>
                  <label className="text-xs text-muted-foreground">
                    命令参数（每行一个 arg，可选）
                    <Textarea
                      rows={2}
                      className="mt-1 w-full font-mono text-xs"
                      placeholder={"-y\n@modelcontextprotocol/server-filesystem\n/var/log"}
                      value={argsText}
                      onChange={(e) => setArgsText(e.target.value)}
                    />
                  </label>
                  <label className="text-xs text-muted-foreground sm:col-span-2">
                    环境变量（可选，每行 KEY=VALUE；服务端加密保存且不再明文回显{editingSlug && editingEnvKeys.length > 0 ? `，当前已配置键：${editingEnvKeys.join(", ")}，留空不修改` : ""}）
                    <Textarea
                      rows={2}
                      className="mt-1 w-full font-mono text-xs"
                      placeholder={editingSlug && editingEnvKeys.length > 0 ? "留空保留已加密配置；输入新值将整体替换" : "LOG_LEVEL=info"}
                      value={envText}
                      onChange={(e) => {
                        setEnvText(e.target.value)
                        setEnvDirty(true)
                      }}
                    />
                  </label>
                </div>
              ) : (
                <div className="grid gap-3 sm:grid-cols-2">
                  <label className="text-xs text-muted-foreground">
                    MCP 端点地址 (URL)
                    <Input
                      scale="sm"
                      required
                      className="mt-1 w-full font-mono"
                      placeholder="https://mcp.internal.example.com/mcp"
                      value={url}
                      onChange={(e) => setUrl(e.target.value)}
                    />
                  </label>
                  <label className="text-xs text-muted-foreground">
                    自定义请求头（可选，每行 Header: Value；服务端加密保存且不再明文回显{editingSlug && editingHeaderKeys.length > 0 ? `，当前已配置键：${editingHeaderKeys.join(", ")}，留空不修改` : ""}）
                    <Textarea
                      rows={2}
                      className="mt-1 w-full font-mono text-xs"
                      placeholder={editingSlug && editingHeaderKeys.length > 0 ? "留空保留已加密配置；输入新值将整体替换" : "X-Client: doops-agent"}
                      value={headersText}
                      onChange={(e) => {
                        setHeadersText(e.target.value)
                        setHeadersDirty(true)
                      }}
                    />
                  </label>
                </div>
              )}

              <label className="block text-xs text-muted-foreground">
                能力说明（可选）
                <Input
                  scale="sm"
                  className="mt-1 w-full"
                  placeholder="说明该 MCP 提供哪些运维工具，便于团队在 AI 助手与巡检中复用"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </label>

              <Inline gap={2}>
                <Button type="submit" size="sm" variant="primary" disabled={busy}>
                  {busy ? "保存中…" : editingSlug ? "保存修改" : "保存 MCP"}
                </Button>
                <Button type="button" size="sm" variant="ghost" onClick={() => setFormOpen(false)}>
                  取消
                </Button>
              </Inline>
            </form>
          </CardContent>
        </Card>
      )}

      {/* Real MCP Server List */}
      {loading && servers.length === 0 ? (
        <div role="status" className="space-y-3">
          <span className="sr-only">正在加载 MCP Server 注册表…</span>
          <Skeleton className="h-28 w-full" />
          <Skeleton className="h-28 w-full" />
        </div>
      ) : servers.length === 0 ? (
        <EmptyState
          icon={<Plug className="size-6" aria-hidden="true" />}
          title="尚未接入 MCP Server"
          description="点击右上角「接入 MCP」配置 stdio / HTTP / SSE 工具服务，并同步至在线 Agent。"
          action={
            isAdmin ? (
              <Button size="sm" variant="primary" onClick={openNewForm}>
                <Plus width={14} height={14} />
                接入 MCP
              </Button>
            ) : undefined
          }
        />
      ) : (
        <Stack gap={3}>
          {servers.map((srv) => {
            const live = liveByName.get(srv.slug)
            const tools = live && live.tools.length > 0 ? live.tools : srv.tools || []
            const probeStatus = live && live.status !== "configured" ? live.status : srv.last_probe_status
            const probeError = live?.error || srv.last_probe_error
            const latencyMs = live?.latency_ms ?? srv.last_latency_ms
            const isMountedOnAgent = Boolean(live)
            const isExpanded = Boolean(expandedTools[srv.slug])

            return (
              <Card key={srv.slug}>
                <CardContent className="space-y-3 p-4">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0 flex-1 space-y-1.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <Plug width={15} height={15} className="shrink-0 text-primary" />
                        <strong className="text-sm text-foreground">{srv.name}</strong>
                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-2xs text-foreground">{srv.slug}</code>
                        <Badge variant={srv.scope === "system" ? "primary" : "success"}>
                          {srv.scope === "system" ? "Agent 内置" : "自定义"}
                        </Badge>
                        <Badge variant="outline">{srv.transport}</Badge>
                        <Badge variant={srv.enabled ? "success" : "default"}>
                          {srv.enabled ? "已启用" : "已停用"}
                        </Badge>
                        {selectedTarget && (
                          <Badge variant={isMountedOnAgent ? "success" : "outline"}>
                            {isMountedOnAgent ? "目标已挂载" : "待同步至目标"}
                          </Badge>
                        )}
                      </div>

                      {srv.description && <p className="text-xs text-muted-foreground">{srv.description}</p>}

                      <div className="flex flex-wrap items-center gap-2 font-mono text-2xs text-muted-foreground">
                        <span className="inline-flex items-center gap-1 rounded border bg-background/60 px-2 py-0.5 text-foreground">
                          <Terminal width={11} height={11} />
                          {srv.transport === "stdio"
                            ? `${srv.command || ""}${srv.args?.length ? ` ${srv.args.join(" ")}` : ""}`
                            : srv.url || "—"}
                        </span>
                        <span>超时 {srv.tool_timeout_secs}s</span>
                        {srv.env_keys && srv.env_keys.length > 0 && (
                          <span>Env 键：{srv.env_keys.join(", ")}</span>
                        )}
                        {srv.header_keys && srv.header_keys.length > 0 && (
                          <span>Header 键：{srv.header_keys.join(", ")}</span>
                        )}
                        {probeStatus === "ok" ? (
                          <span className="inline-flex items-center gap-1 text-success">
                            <CheckCircle2 width={12} height={12} />
                            连通正常 · {latencyMs ?? 0} ms · {tools.length} 个工具
                          </span>
                        ) : probeStatus === "error" ? (
                          <span className="inline-flex items-center gap-1 text-destructive">
                            <XCircle width={12} height={12} />
                            探测失败：{probeError || "未知错误"}
                          </span>
                        ) : (
                          <span>上次探测：{fmtStamp(srv.last_probe_at_ms)}</span>
                        )}
                      </div>
                    </div>

                    <Inline gap={1} className="flex-wrap">
                      {(srv.scope === "system" || isAdmin) && (
                        <Button
                          size="sm"
                          variant="default"
                          disabled={Boolean(probingSlug) || onlineTargets.length === 0}
                          onClick={() => void handleProbe(srv.slug)}
                        >
                          {probingSlug === srv.slug ? "探测中…" : "探测工具"}
                        </Button>
                      )}
                      {tools.length > 0 && (
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() =>
                            setExpandedTools((prev) => ({ ...prev, [srv.slug]: !prev[srv.slug] }))
                          }
                        >
                          {isExpanded ? `收起工具 (${tools.length})` : `查看工具 (${tools.length})`}
                        </Button>
                      )}
                      {srv.can_edit && (
                        <>
                          <Button size="sm" variant="ghost" disabled={busy} onClick={() => openEditForm(srv)}>
                            编辑
                          </Button>
                          <Switch
                            checked={srv.enabled}
                            disabled={busy}
                            aria-label={`${srv.enabled ? "停用" : "启用"} ${srv.name}`}
                            onCheckedChange={() => void handleToggle(srv)}
                          />
                        </>
                      )}
                      {srv.can_delete && (
                        <Button
                          size="sm"
                          variant="danger-outline"
                          disabled={busy}
                          onClick={() => void handleDelete(srv)}
                        >
                          <Trash2 width={13} height={13} />
                        </Button>
                      )}
                    </Inline>
                  </div>

                  {isExpanded && tools.length > 0 && (
                    <div className="space-y-1.5 border-t pt-2.5">
                      <div className="text-2xs font-medium text-muted-foreground">
                        真实探测到的 MCP 工具（AI 助手与智能巡检中可直接调用）：
                      </div>
                      <div className="grid gap-2 sm:grid-cols-2">
                        {tools.map((tool) => {
                          const props =
                            tool.input_schema &&
                            typeof tool.input_schema === "object" &&
                            "properties" in tool.input_schema &&
                            tool.input_schema.properties &&
                            typeof tool.input_schema.properties === "object"
                              ? Object.keys(tool.input_schema.properties as Record<string, unknown>)
                              : []
                          return (
                            <div key={tool.name} className="space-y-1 rounded-md border bg-background/70 p-2.5 text-xs">
                              <div className="flex flex-wrap items-center gap-1.5">
                                <code className="font-mono font-semibold text-primary">
                                  mcp_{srv.slug}_{tool.name}
                                </code>
                              </div>
                              {tool.description && (
                                <p className="text-2xs leading-relaxed text-muted-foreground">{tool.description}</p>
                              )}
                              {props.length > 0 && (
                                <div className="flex flex-wrap gap-1 pt-0.5">
                                  {props.slice(0, 8).map((p) => (
                                    <Badge key={p} variant="outline">{p}</Badge>
                                  ))}
                                  {props.length > 8 && <Badge variant="outline">+{props.length - 8}</Badge>}
                                </div>
                              )}
                            </div>
                          )
                        })}
                      </div>
                    </div>
                  )}
                </CardContent>
              </Card>
            )
          })}
        </Stack>
      )}
    </section>
  )
}
