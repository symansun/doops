"use client"

import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type FormEvent } from "react"
import {
  FileCode2,
  FlaskConical,
  FolderOpen,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Send,
  Trash2,
  Upload,
} from "lucide-react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import type { MonitorRunDiff } from "@/lib/monitor-metrics"
import {
  describeMonitorStatus,
  fetchMonitorRunDetail,
  formatBytesCompact,
  triggerMonitorRun,
  type MonitorRunItemRecord,
  type MonitorRunRecord,
} from "@/lib/monitor-engine"
import {
  SKILL_CATEGORY_LABELS,
  deletePlatformSkill,
  extractSkillZipFile,
  fetchPlatformSkillDetail,
  fetchPlatformSkills,
  parseSkillFrontmatter,
  resetSystemSkill,
  slugifySkillName,
  syncSkillsToAgents,
  togglePlatformSkill,
  upsertPlatformSkill,
  type AgentLoadedSkillInfo,
  type PlatformSkillFile,
  type PlatformSkillRecord,
  type SkillSyncTargetResult,
} from "@/lib/skills-registry"
import { InspectionArtifactViewer } from "./inspection-artifact-viewer"
import { Badge, Button, Card, CardContent, EmptyState, FilterBar, Inline, Input, PageHeader, PageTabs, Select, Skeleton, Stack, StatusBanner, Switch, Textarea } from "./ui"

const CUSTOM_SKILL_TEMPLATE = `---
name: custom-nginx-audit
description: 只读核查主机 Nginx 配置基线、TLS 证书与访问日志异常
---

# Nginx 安全与配置只读巡检 Skill

## 检查项
1. **配置文件语法与安全头**：只读检查 \`nginx -t\`、是否启用 \`server_tokens off\` 及 HSTS / X-Content-Type-Options。
2. **证书有效期检查**：读取配置的 HTTPS 证书到期时间，剩余不足 14 天时输出 \`warning\`。
3. **近期 5xx 错误统计**：只读统计最近 10 分钟错误日志中的 502/503/504 频次。

## 执行约束
- 仅执行只读命令，禁止 reload/restart 或修改任何配置文件。
- 不得输出私钥或凭据内容。
`

function fmtStamp(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "尚未执行"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

type FilterMode = "all" | "system" | "custom" | "inspection" | "security" | "ops"
type SkillPanelTab = "registry" | "agent"

export function SkillRegistryPanel({
  session,
  targets,
  initialSlug,
  onOpenInspection,
  onOpenMcp,
}: {
  session: Session
  targets: Target[]
  initialSlug?: string
  onOpenInspection?: (runId?: string) => void
  onOpenMcp?: () => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const onlineTargets = useMemo(() => targets.filter((t) => t.status !== "offline"), [targets])
  const [activeTab, setActiveTab] = useState<SkillPanelTab>("registry")
  const [skills, setSkills] = useState<PlatformSkillRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [runningSlug, setRunningSlug] = useState("")
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [lastRunId, setLastRunId] = useState("")
  const [syncResults, setSyncResults] = useState<SkillSyncTargetResult[]>([])
  const [revision, setRevision] = useState(0)

  // Agent runtime skills inspection state
  const [selectedTarget, setSelectedTarget] = useState("")
  const [agentSkills, setAgentSkills] = useState<AgentLoadedSkillInfo[]>([])
  const [agentSkillsRoot, setAgentSkillsRoot] = useState("")
  const [agentError, setAgentError] = useState("")
  const [agentLoading, setAgentLoading] = useState(false)
  const [agentQuery, setAgentQuery] = useState("")
  const [agentRevision, setAgentRevision] = useState(0)

  // Inline Skill test-run verification state
  const [testSkill, setTestSkill] = useState<PlatformSkillRecord | null>(null)
  const [testTarget, setTestTarget] = useState("")
  const [testPrompt, setTestPrompt] = useState("")
  const [testBusy, setTestBusy] = useState(false)
  const [testError, setTestError] = useState("")
  const [testResult, setTestResult] = useState<{ run: MonitorRunRecord; items: MonitorRunItemRecord[] } | null>(null)

  // Filters
  const [filter, setFilter] = useState<FilterMode>("all")
  const [query, setQuery] = useState("")

  // Upload / Create / Edit drawer state
  const [editorOpen, setEditorOpen] = useState(false)
  const [editorReadOnly, setEditorReadOnly] = useState(false)
  const [editingExisting, setEditingExisting] = useState<PlatformSkillRecord | null>(null)
  const [formSlug, setFormSlug] = useState("")
  const [formName, setFormName] = useState("")
  const [formDesc, setFormDesc] = useState("")
  const [formScope, setFormScope] = useState<"system" | "custom">("custom")
  const [formCategory, setFormCategory] = useState<"inspection" | "security" | "ops">("inspection")
  const [formEnabled, setFormEnabled] = useState(true)
  const [formSkillMD, setFormSkillMD] = useState("")
  const [formFiles, setFormFiles] = useState<PlatformSkillFile[]>([])
  const [newFilePath, setNewFilePath] = useState("")
  const [newFileContent, setNewFileContent] = useState("")
  const [dragActive, setDragActive] = useState(false)
  const [uploadSummary, setUploadSummary] = useState("")
  const fileInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!selectedTarget && onlineTargets.length > 0) {
      setSelectedTarget(onlineTargets[0].key)
    }
    if (!testTarget && onlineTargets.length > 0) {
      setTestTarget(onlineTargets[0].key)
    }
  }, [onlineTargets, selectedTarget, testTarget])

  const reload = useCallback(() => {
    setRevision((v) => v + 1)
    setAgentRevision((v) => v + 1)
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    fetchPlatformSkills(session, {}, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        setSkills(res.skills || [])
        setLoading(false)
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error).message)
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, revision])

  useEffect(() => {
    if (activeTab !== "agent" || !selectedTarget) return
    const ctrl = new AbortController()
    setAgentLoading(true)
    setAgentError("")
    fetchPlatformSkills(session, { target: selectedTarget }, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        setAgentSkills(res.agent_skills || [])
        setAgentSkillsRoot(res.agent_skills_root || "/root/.agent/skills")
        setAgentError(res.agent_error || "")
        setAgentLoading(false)
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setAgentError((err as Error).message)
          setAgentLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, activeTab, selectedTarget, agentRevision])

  const openSkillEditor = useCallback(
    async (slug: string) => {
      setBusy(true)
      setError("")
      try {
        const { skill } = await fetchPlatformSkillDetail(session, slug)
        setEditingExisting(skill)
        setEditorReadOnly(!skill.can_edit)
        setFormSlug(skill.slug)
        setFormName(skill.name)
        setFormDesc(skill.description || "")
        setFormScope(skill.scope)
        setFormCategory(skill.category)
        setFormEnabled(skill.enabled)
        setFormSkillMD(skill.skill_md || "")
        setFormFiles(skill.files || [])
        setUploadSummary("")
        setEditorOpen(true)
      } catch (err) {
        setError((err as Error).message)
      } finally {
        setBusy(false)
      }
    },
    [session],
  )

  useEffect(() => {
    if (initialSlug) {
      void openSkillEditor(initialSlug)
    }
  }, [initialSlug, openSkillEditor])

  function openNewSkill(presetScope: "system" | "custom" = "custom") {
    setEditingExisting(null)
    setEditorReadOnly(false)
    setFormSlug("")
    setFormName("")
    setFormDesc("")
    setFormScope(isAdmin ? presetScope : "custom")
    setFormCategory("inspection")
    setFormEnabled(true)
    setFormSkillMD(CUSTOM_SKILL_TEMPLATE)
    setFormFiles([])
    setNewFilePath("")
    setNewFileContent("")
    setUploadSummary("")
    setEditorOpen(true)
  }

  async function ingestUploadedFile(file: File) {
    setError("")
    setUploadSummary("")
    const lower = file.name.toLowerCase()
    try {
      if (lower.endsWith(".zip")) {
        const extracted = await extractSkillZipFile(file)
        if (!editingExisting) setFormSlug(extracted.suggestedSlug)
        setFormName(extracted.name || extracted.suggestedSlug)
        setFormDesc(extracted.description || "")
        setFormSkillMD(extracted.skillMD)
        setFormFiles(extracted.files)
        setUploadSummary(
          `已解析 Skill 压缩包「${file.name}」：提取 SKILL.md 与 ${extracted.files.length} 个附属文件`,
        )
      } else if (lower.endsWith(".md") || lower.endsWith(".markdown") || lower.endsWith(".txt")) {
        const text = await file.text()
        if (!text.trim()) throw new Error("上传的 Markdown 文件内容为空")
        const fm = parseSkillFrontmatter(text)
        const baseSlug = slugifySkillName(fm.name) || slugifySkillName(file.name)
        if (!editingExisting && baseSlug) setFormSlug(baseSlug)
        if (fm.name) setFormName(fm.name)
        else if (!formName) setFormName(baseSlug || file.name.replace(/\.(md|markdown|txt)$/i, ""))
        if (fm.description) setFormDesc(fm.description)
        setFormSkillMD(text)
        setUploadSummary(`已载入文档「${file.name}」（${text.length} 字符）`)
      } else {
        throw new Error("仅支持上传 SKILL.md（.md / .markdown）或标准 Skill 压缩包（.zip）")
      }
    } catch (err) {
      setError((err as Error).message)
    }
  }

  function handleFileInput(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ""
    if (file) void ingestUploadedFile(file)
  }

  function handleDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault()
    setDragActive(false)
    if (editorReadOnly) return
    const file = e.dataTransfer.files?.[0]
    if (file) void ingestUploadedFile(file)
  }

  function addAttachedFile() {
    const cleanPath = newFilePath.trim().replace(/^\/+/, "")
    if (!cleanPath || cleanPath.includes("..") || cleanPath.toLowerCase() === "skill.md") {
      setError("附属文件路径需为相对路径（如 scripts/check.sh），不得包含 .. 或覆盖 SKILL.md")
      return
    }
    setError("")
    setFormFiles((prev) => {
      const filtered = prev.filter((f) => f.path !== cleanPath)
      return [...filtered, { path: cleanPath, content: newFileContent }]
    })
    setNewFilePath("")
    setNewFileContent("")
  }

  async function saveSkill(e: FormEvent, alsoSync: boolean) {
    e.preventDefault()
    if (busy || editorReadOnly) return
    const slug = slugifySkillName(formSlug)
    if (!slug) {
      setError("请填写有效的 Skill 标识（仅支持小写字母、数字和短横线）")
      return
    }
    setBusy(true)
    setError("")
    setNotice("")
    setSyncResults([])
    try {
      const { skill } = await upsertPlatformSkill(session, {
        slug,
        name: formName.trim() || slug,
        description: formDesc.trim(),
        scope: editingExisting ? editingExisting.scope : formScope,
        category: formCategory,
        enabled: formEnabled,
        skill_md: formSkillMD,
        files: formFiles,
      })
      setEditingExisting(skill)
      setEditorOpen(false)
      if (alsoSync) {
        try {
          const synced = await syncSkillsToAgents(session, { slug: skill.slug })
          setSyncResults(synced.results || [])
          const okCount = (synced.results || []).filter((r) => r.status === "ok").length
          setNotice(`Skill「${skill.name}」已保存，并已同步至 ${okCount} 个在线 Agent 的 /root/.agent/skills/${skill.slug}/`)
        } catch (syncErr) {
          setNotice(`Skill「${skill.name}」已保存（同步在线 Agent 提示：${(syncErr as Error).message}）`)
        }
      } else {
        setNotice(`Skill「${skill.name}」（${skill.slug}）已保存`)
      }
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleToggle(sk: PlatformSkillRecord) {
    if (busy || !sk.can_edit) return
    setBusy(true)
    setError("")
    try {
      await togglePlatformSkill(session, sk.slug, !sk.enabled)
      setNotice(`已${sk.enabled ? "停用" : "启用"} Skill「${sk.name}」`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(sk: PlatformSkillRecord) {
    if (busy || !sk.can_delete) return
    setBusy(true)
    setError("")
    try {
      await deletePlatformSkill(session, sk.slug)
      setNotice(`已删除自定义 Skill「${sk.name}」（${sk.slug}）`)
      if (editingExisting?.slug === sk.slug) setEditorOpen(false)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleResetSystem(sk: PlatformSkillRecord) {
    if (busy || !isAdmin || sk.scope !== "system") return
    setBusy(true)
    setError("")
    try {
      const { skill } = await resetSystemSkill(session, sk.slug)
      setNotice(`系统 Skill「${skill.name}」已恢复内置默认定义`)
      if (editingExisting?.slug === sk.slug) {
        setEditingExisting(skill)
        setFormName(skill.name)
        setFormDesc(skill.description || "")
        setFormSkillMD(skill.skill_md || "")
        setFormFiles(skill.files || [])
      }
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleSync(slug?: string, targetKey?: string) {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      let targetRefs: { cluster: string; instance: string }[] | undefined
      if (targetKey) {
        const [cluster, instance] = targetKey.split("/")
        if (cluster && instance) targetRefs = [{ cluster, instance }]
      }
      const res = await syncSkillsToAgents(session, { slug, targets: targetRefs })
      setSyncResults(res.results || [])
      const okCount = (res.results || []).filter((r) => r.status === "ok").length
      const totalCount = (res.results || []).length
      setNotice(
        slug
          ? `已将 Skill「${slug}」同步至 ${okCount}/${totalCount} 个目标 Agent`
          : targetKey
            ? `已将全部已启用平台 Skill 同步至 ${targetKey}（/root/.agent/skills/）`
            : `已将全部已启用 Skill 同步至 ${okCount}/${totalCount} 个目标 Agent（/root/.agent/skills/）`,
      )
      setAgentRevision((v) => v + 1)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDriveInspection(slugs: string[], label: string) {
    if (busy || slugs.length === 0) return
    setBusy(true)
    setRunningSlug(slugs.length === 1 ? slugs[0] : "__all__")
    setError("")
    setNotice("")
    setLastRunId("")
    try {
      const res = await triggerMonitorRun(session, {
        scope_mode: "all_online",
        skills: slugs,
      })
      setLastRunId(res.run.id)
      setNotice(`${label} 执行完成（#${res.run.id}）：${res.run.summary || `共 ${res.run.total_assets} 项巡检结果`}`)
      reload()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
      setRunningSlug("")
    }
  }

  function openSkillTestRun(sk: PlatformSkillRecord) {
    setTestSkill(sk)
    setTestError("")
    setTestResult(null)
    if (!testTarget && onlineTargets.length > 0) {
      setTestTarget(onlineTargets[0].key)
    }
    setTestPrompt(`请按 Skill「${sk.slug}」执行一次只读核查，并用 Markdown + JSON 输出结构化结果。`)
  }

  async function executeSkillTestRun(e: FormEvent) {
    e.preventDefault()
    if (!testSkill || testBusy) return
    const targetKey = testTarget || onlineTargets[0]?.key || ""
    const [cluster, instance] = targetKey.split("/")
    if (!cluster || !instance) {
      setTestError("请先选择一个在线 Agent 节点")
      return
    }
    setTestBusy(true)
    setTestError("")
    setTestResult(null)
    try {
      const res = await triggerMonitorRun(session, {
        scope_mode: "targets",
        targets: [{ cluster, instance }],
        skills: [testSkill.slug],
        prompt: testPrompt.trim() || undefined,
      })
      // 试跑接口只返回本次结果；再取一次运行详情，才能拿到后端计算的跨次 diff（无前次运行时为 null）。
      let detail: { run: MonitorRunRecord; items: MonitorRunItemRecord[] } | null = null
      try {
        detail = await fetchMonitorRunDetail(session, res.run.id)
      } catch {
        detail = null
      }
      setTestResult(detail ?? res)
      setLastRunId(res.run.id)
      reload()
    } catch (err) {
      setTestError((err as Error).message)
    } finally {
      setTestBusy(false)
    }
  }

  const counts = useMemo(() => {
    const system = skills.filter((s) => s.scope === "system").length
    const custom = skills.filter((s) => s.scope === "custom").length
    const enabledInspection = skills.filter((s) => s.enabled && (s.category === "inspection" || s.category === "security"))
    return { total: skills.length, system, custom, enabledInspection }
  }, [skills])

  const platformSkillMap = useMemo(() => {
    const map = new Map<string, PlatformSkillRecord>()
    for (const sk of skills) map.set(sk.slug, sk)
    return map
  }, [skills])

  const filteredSkills = useMemo(() => {
    const q = query.trim().toLowerCase()
    return skills.filter((s) => {
      if (filter === "system" && s.scope !== "system") return false
      if (filter === "custom" && s.scope !== "custom") return false
      if ((filter === "inspection" || filter === "security" || filter === "ops") && s.category !== filter) return false
      if (!q) return true
      return (
        s.slug.toLowerCase().includes(q) ||
        s.name.toLowerCase().includes(q) ||
        (s.description || "").toLowerCase().includes(q)
      )
    })
  }, [skills, filter, query])

  const filteredAgentSkills = useMemo(() => {
    const q = agentQuery.trim().toLowerCase()
    if (!q) return agentSkills
    return agentSkills.filter(
      (s) =>
        s.slug.toLowerCase().includes(q) ||
        s.name.toLowerCase().includes(q) ||
        (s.description || "").toLowerCase().includes(q),
    )
  }, [agentSkills, agentQuery])

  const onlineTargetsCount = onlineTargets.length

  return (
    <section aria-label="Skill 管理" className="space-y-4">
      <PageHeader
        title="Skill 管理"
        badge={
          <>
            <Badge variant="primary">系统 {counts.system}</Badge>
            <Badge variant="success">自定义 {counts.custom}</Badge>
            <Badge variant="outline">在线 Agent {onlineTargetsCount}</Badge>
          </>
        }
        description="文件目录化托管 Skill（支持 .md / .zip），统一同步至在线 Agent /root/.agent/skills/ 并直接在线试跑验证，供 AI 助手与智能巡检共同调用。"
        actions={
          <>
            <Button size="sm" variant="primary" onClick={() => openNewSkill("custom")}>
              <Upload width={14} height={14} />
              上传 / 新建 Skill
            </Button>
            <Button size="sm" disabled={busy || onlineTargetsCount === 0} onClick={() => void handleSync()}>
              <Send width={14} height={14} />
              同步至在线 Agent
            </Button>
            {onOpenMcp && (
              <Button size="sm" variant="ghost" onClick={onOpenMcp}>
                MCP 管理
              </Button>
            )}
            {onOpenInspection && (
              <Button size="sm" variant="ghost" onClick={() => onOpenInspection()}>
                智能巡检
              </Button>
            )}
            <Button size="sm" variant="ghost" disabled={loading || agentLoading} onClick={reload}>
              <RefreshCw width={14} height={14} className={loading || agentLoading ? "animate-spin" : ""} />
              刷新
            </Button>
          </>
        }
      />

      <PageTabs
        ariaLabel="Skill 管理视图"
        value={activeTab}
        onChange={(next) => setActiveTab(next as SkillPanelTab)}
        items={[
          { id: "registry", label: `平台 Skill 仓库 (${counts.total})` },
          {
            id: "agent",
            label: agentSkills.length > 0 ? `Agent 运行态 Skill (${agentSkills.length})` : "Agent 运行态 Skill",
          },
        ]}
      />

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && (
        <StatusBanner tone="info">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>{notice}</span>
            {lastRunId && onOpenInspection && (
              <Button size="sm" variant="primary" onClick={() => onOpenInspection(lastRunId)}>
                查看巡检报告 #{lastRunId}
              </Button>
            )}
          </div>
        </StatusBanner>
      )}

      {syncResults.length > 0 && (
        <Card>
          <CardContent className="space-y-2 p-3">
            <div className="text-xs font-medium text-foreground">最近一次 Agent 同步结果</div>
            <div className="flex flex-wrap gap-2">
              {syncResults.map((r) => (
                <div key={`${r.cluster}/${r.instance}`} className="flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs">
                  <Badge variant={r.status === "ok" ? "success" : r.status === "offline" ? "default" : "danger"}>
                    {r.status === "ok" ? "已同步" : r.status === "offline" ? "离线" : "失败"}
                  </Badge>
                  <span className="font-mono text-foreground">{r.cluster}/{r.instance}</span>
                  <span className="text-2xs text-muted-foreground">
                    {r.status === "ok" ? `${r.synced_count} 个 Skill` : r.detail || ""}
                  </span>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Inline Skill Test-Run Drawer */}
      {testSkill && (
        <Card className="border-primary/40">
          <CardContent className="space-y-3 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2">
                <FlaskConical width={15} className="text-primary" />
                <h2 className="text-sm font-semibold text-foreground">
                  在线试跑验证：{testSkill.name}
                </h2>
                <Badge variant="outline" className="font-mono">{testSkill.slug}</Badge>
              </div>
              <Button size="sm" variant="ghost" onClick={() => { setTestSkill(null); setTestResult(null); setTestError("") }}>
                关闭
              </Button>
            </div>

            <form onSubmit={(e) => void executeSkillTestRun(e)} className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-3">
                <label className="text-xs text-muted-foreground">
                  目标 Agent 节点
                  <Select
                    scale="sm"
                    className="mt-1 font-mono"
                    value={testTarget}
                    onChange={(e) => setTestTarget(e.target.value)}
                    disabled={onlineTargetsCount === 0 || testBusy}
                  >
                    {onlineTargetsCount === 0 ? (
                      <option value="">暂无在线 Agent</option>
                    ) : (
                      onlineTargets.map((t) => (
                        <option key={t.key} value={t.key}>
                          {t.key}
                        </option>
                      ))
                    )}
                  </Select>
                </label>
                <label className="text-xs text-muted-foreground sm:col-span-2">
                  附加提示词（可选；留空则直接按 Skill 默认指令运行）
                  <Input
                    scale="sm"
                    className="mt-1"
                    placeholder="如：只读检查当前节点系统负载、磁盘与监听端口，输出 Markdown + JSON"
                    value={testPrompt}
                    onChange={(e) => setTestPrompt(e.target.value)}
                    disabled={testBusy}
                  />
                </label>
              </div>

              <Inline gap={2} className="flex-wrap">
                <Button type="submit" size="sm" variant="primary" disabled={testBusy || onlineTargetsCount === 0}>
                  <Play width={13} height={13} />
                  {testBusy ? "正在 Agent 上真实执行…" : "立即试跑"}
                </Button>
                {testResult && onOpenInspection && (
                  <Button type="button" size="sm" variant="ghost" onClick={() => onOpenInspection(testResult.run.id)}>
                    在智能巡检中查看 #{testResult.run.id}
                  </Button>
                )}
              </Inline>
            </form>

            {testError && <StatusBanner tone="error">{testError}</StatusBanner>}

            {testResult && (
              <div className="space-y-3 border-t pt-3">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={describeMonitorStatus(testResult.run.status).variant}>
                      {describeMonitorStatus(testResult.run.status).label}
                    </Badge>
                    <span className="font-mono text-muted-foreground">#{testResult.run.id}</span>
                    <span className="text-muted-foreground">耗时 {testResult.run.duration_ms} ms</span>
                    {testResult.run.summary && <span className="text-foreground">{testResult.run.summary}</span>}
                  </div>
                </div>
                {testResult.items.map((item) => {
                  // 详情接口为每个 item 返回 diff：null=首次运行（渲染空态），未返回=不渲染该区块。
                  const hasDiff = Object.prototype.hasOwnProperty.call(item, "diff")
                  const itemDiff = hasDiff ? ((item as { diff?: MonitorRunDiff | null }).diff ?? null) : undefined
                  return (
                  <div key={`${item.cluster}-${item.instance}-${item.asset_id}-${item.id}`} className="space-y-2">
                    <div className="flex flex-wrap items-center gap-2 text-xs">
                      <span className="font-mono font-medium text-foreground">
                        {item.cluster}/{item.instance}
                      </span>
                      <Badge variant={describeMonitorStatus(item.status).variant}>
                        {describeMonitorStatus(item.status).label}
                      </Badge>
                      {item.reason && <span className="text-muted-foreground">{item.reason}</span>}
                    </div>
                    <InspectionArtifactViewer
                      format={item.artifact_format}
                      markdown={item.artifact_markdown}
                      jsonData={item.artifact_json}
                      rawOutput={item.raw_output}
                      filePrefix={`${testSkill.slug}-${item.cluster}-${item.instance}`}
                      diff={itemDiff}
                      session={session}
                      trendSkill={testSkill.slug}
                      trendCluster={item.cluster}
                      trendInstance={item.instance}
                    />
                  </div>
                  )
                })}
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* Upload / Create / Edit Skill Editor */}
      {editorOpen && (
        <Card className="border-primary/40">
          <CardContent className="space-y-4 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                <h2 className="text-base font-semibold text-foreground">
                  {editorReadOnly
                    ? `查看 Skill：${editingExisting?.name}`
                    : editingExisting
                      ? `编辑${editingExisting.scope === "system" ? "系统" : "自定义"} Skill：${editingExisting.slug}`
                      : "上传 / 新建 Skill"}
                </h2>
                {editingExisting && (
                  <Badge variant={editingExisting.scope === "system" ? "primary" : "success"}>
                    {editingExisting.scope === "system" ? "系统 Skill" : "自定义 Skill"}
                  </Badge>
                )}
              </div>
              <Button size="sm" variant="ghost" onClick={() => setEditorOpen(false)}>
                关闭
              </Button>
            </div>

            {!editorReadOnly && (
              <div
                onDragOver={(e) => {
                  e.preventDefault()
                  setDragActive(true)
                }}
                onDragLeave={() => setDragActive(false)}
                onDrop={handleDrop}
                className={`flex flex-wrap items-center justify-between gap-3 rounded-lg border-2 border-dashed p-3.5 transition-colors ${
                  dragActive ? "border-primary bg-primary/5" : "border-border bg-muted/20"
                }`}
              >
                <div className="space-y-0.5 text-xs">
                  <div className="font-medium text-foreground">
                    拖拽或选择本地 `.md`（SKILL.md）或 `.zip`（完整 Skill 压缩包）导入
                  </div>
                  <div className="text-muted-foreground">
                    自动解析 YAML frontmatter（name / description）以及压缩包内的 `scripts/`、`references/` 附属文件（总大小 ≤ 1 MiB）。
                  </div>
                  {uploadSummary && <div className="font-medium text-success">{uploadSummary}</div>}
                </div>
                <Inline gap={2}>
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept=".md,.markdown,.txt,.zip"
                    className="hidden"
                    onChange={handleFileInput}
                  />
                  <Button type="button" size="sm" variant="primary" onClick={() => fileInputRef.current?.click()}>
                    <Upload width={14} height={14} />
                    选择 .md / .zip 文件
                  </Button>
                  {!editingExisting && (
                    <Button
                      type="button"
                      size="sm"
                      onClick={() => {
                        setFormSlug("custom-nginx-audit")
                        setFormName("Nginx 安全与配置只读巡检 Skill")
                        setFormDesc("只读核查主机 Nginx 配置基线、TLS 证书与访问日志异常")
                        setFormCategory("security")
                        setFormSkillMD(CUSTOM_SKILL_TEMPLATE)
                        setFormFiles([
                          {
                            path: "scripts/check_nginx_readonly.sh",
                            content: "#!/usr/bin/env sh\nset -eu\nnginx -t 2>&1 || true\n",
                          },
                        ])
                        setUploadSummary("已填充示例巡检 Skill 与只读脚本，可直接保存测试")
                      }}
                    >
                      填入示例模板
                    </Button>
                  )}
                </Inline>
              </div>
            )}

            <form onSubmit={(e) => void saveSkill(e, true)} className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <label className="text-xs text-muted-foreground">
                  Skill 标识（slug）
                  <Input
                    scale="sm"
                    required
                    disabled={Boolean(editingExisting) || editorReadOnly}
                    className="mt-1 font-mono"
                    placeholder="如 custom-nginx-audit"
                    value={formSlug}
                    onChange={(e) => setFormSlug(e.target.value)}
                  />
                </label>
                <label className="text-xs text-muted-foreground">
                  显示名称
                  <Input
                    scale="sm"
                    required
                    disabled={editorReadOnly}
                    className="mt-1"
                    placeholder="如 Nginx 配置安全巡检 Skill"
                    value={formName}
                    onChange={(e) => setFormName(e.target.value)}
                  />
                </label>
                <label className="text-xs text-muted-foreground">
                  Skill 归属
                  <Select
                    scale="sm"
                    className="mt-1"
                    disabled={Boolean(editingExisting) || !isAdmin || editorReadOnly}
                    value={formScope}
                    onChange={(e) => setFormScope(e.target.value as "system" | "custom")}
                  >
                    <option value="custom">自定义 Skill（用户上传）</option>
                    {isAdmin && <option value="system">系统 Skill（平台内置）</option>}
                  </Select>
                </label>
                <label className="text-xs text-muted-foreground">
                  业务分类
                  <Select
                    scale="sm"
                    className="mt-1"
                    disabled={editorReadOnly}
                    value={formCategory}
                    onChange={(e) => setFormCategory(e.target.value as "inspection" | "security" | "ops")}
                  >
                    <option value="inspection">巡检驱动</option>
                    <option value="security">安全巡检</option>
                    <option value="ops">运维与诊断</option>
                  </Select>
                </label>
              </div>

              <label className="block text-xs text-muted-foreground">
                简介说明
                <Input
                  scale="sm"
                  disabled={editorReadOnly}
                  className="mt-1"
                  placeholder="一句话描述该 Skill 的检查目标、适用范围与只读约束"
                  value={formDesc}
                  onChange={(e) => setFormDesc(e.target.value)}
                />
              </label>

              <label className="block text-xs text-muted-foreground">
                SKILL.md 规范正文（遵循 YAML Frontmatter + Markdown 指令规范）
                <Textarea
                  rows={12}
                  required
                  readOnly={editorReadOnly}
                  className="mt-1 font-mono text-xs leading-relaxed"
                  value={formSkillMD}
                  onChange={(e) => setFormSkillMD(e.target.value)}
                />
              </label>

              {/* Attached relative files */}
              <div className="space-y-2 rounded-lg border bg-muted/20 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                    <FileCode2 width={14} />
                    附属脚本 / 参考文件（{formFiles.length} 个，将随 SKILL.md 一并同步到 Agent 目录）
                  </span>
                </div>

                {formFiles.length > 0 && (
                  <div className="space-y-1.5">
                    {formFiles.map((f) => (
                      <div key={f.path} className="rounded-md border bg-background p-2 text-xs">
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-mono font-medium text-foreground">{f.path}</span>
                          <div className="flex items-center gap-2">
                            <span className="font-mono text-2xs text-muted-foreground">{f.content.length} chars</span>
                            {!editorReadOnly && (
                              <button
                                type="button"
                                className="text-2xs text-destructive hover:underline"
                                onClick={() => setFormFiles((prev) => prev.filter((item) => item.path !== f.path))}
                              >
                                移除
                              </button>
                            )}
                          </div>
                        </div>
                        <pre className="mt-1.5 max-h-24 overflow-auto rounded bg-muted/40 p-1.5 font-mono text-2xs text-muted-foreground">
                          {f.content}
                        </pre>
                      </div>
                    ))}
                  </div>
                )}

                {!editorReadOnly && (
                  <div className="grid gap-2 pt-1 sm:grid-cols-3">
                    <Input
                      scale="sm"
                      placeholder="相对路径，如 scripts/check.sh"
                      value={newFilePath}
                      onChange={(e) => setNewFilePath(e.target.value)}
                    />
                    <Input
                      scale="sm"
                      placeholder="文件内容（脚本或参考说明）"
                      value={newFileContent}
                      onChange={(e) => setNewFileContent(e.target.value)}
                    />
                    <Button type="button" size="sm" onClick={addAttachedFile} disabled={!newFilePath.trim()}>
                      <Plus width={14} height={14} />
                      添加附属文件
                    </Button>
                  </div>
                )}
              </div>

              {!editorReadOnly && (
                <Inline gap={2} className="flex-wrap pt-1">
                  <Button type="submit" size="sm" variant="primary" disabled={busy}>
                    {busy ? "保存并同步中…" : "保存并同步至在线 Agent"}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    disabled={busy}
                    onClick={(e) => void saveSkill(e as unknown as FormEvent, false)}
                  >
                    仅保存到仓库
                  </Button>
                  {editingExisting?.scope === "system" && isAdmin && (
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={busy}
                      onClick={() => void handleResetSystem(editingExisting)}
                    >
                      <RotateCcw width={14} height={14} />
                      恢复系统默认
                    </Button>
                  )}
                  <Button type="button" size="sm" variant="ghost" onClick={() => setEditorOpen(false)}>
                    取消
                  </Button>
                </Inline>
              )}
            </form>
          </CardContent>
        </Card>
      )}

      {activeTab === "registry" ? (
        <>
          {/* Filter Toolbar */}
          <FilterBar className="justify-between">
            <div className="flex flex-wrap gap-1.5" role="group" aria-label="Skill 筛选">
              {(
                [
                  ["all", `全部 (${counts.total})`],
                  ["system", `系统 (${counts.system})`],
                  ["custom", `自定义 (${counts.custom})`],
                  ["inspection", "巡检驱动"],
                  ["security", "安全巡检"],
                  ["ops", "运维与诊断"],
                ] as const
              ).map(([key, label]) => (
                <Button
                  key={key}
                  size="sm"
                  variant={filter === key ? "primary" : "default"}
                  onClick={() => setFilter(key)}
                >
                  {label}
                </Button>
              ))}
            </div>
            <Input
              scale="sm"
              className="w-full sm:w-60"
              placeholder="搜索 Skill 名称、slug 或说明…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </FilterBar>

          {/* Skills Grid */}
          {loading && skills.length === 0 ? (
            <div role="status" className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              <span className="sr-only">正在加载平台 Skill 仓库…</span>
              <Skeleton className="h-44 w-full" />
              <Skeleton className="h-44 w-full" />
              <Skeleton className="h-44 w-full" />
            </div>
          ) : filteredSkills.length === 0 ? (
            <EmptyState
              title="当前筛选条件下暂无 Skill"
              description="点击右上角「上传 / 新建 Skill」即可导入 .md 或 .zip Skill 包。"
              action={
                query || filter !== "all" ? (
                  <Button size="sm" onClick={() => { setFilter("all"); setQuery("") }}>
                    重置筛选
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {filteredSkills.map((sk) => {
                const isRunningThis = runningSlug === sk.slug
                return (
                  <Card key={sk.slug} className={`flex h-full flex-col transition-opacity ${sk.enabled ? "" : "opacity-65"}`}>
                    <CardContent className="flex flex-1 flex-col justify-between gap-3 p-4">
                      <div className="space-y-2">
                        <div className="flex items-start justify-between gap-2">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <Badge variant={sk.scope === "system" ? "primary" : "success"}>
                              {sk.scope === "system" ? "系统 Skill" : "自定义 Skill"}
                            </Badge>
                            <Badge variant="outline">{SKILL_CATEGORY_LABELS[sk.category] || sk.category}</Badge>
                            {sk.has_ui_plugin && <Badge variant="primary">L2 UI 插件</Badge>}
                            {sk.ui_manifest_error && <Badge variant="warning">plugin.yaml 异常</Badge>}
                          </div>
                          <Switch
                            checked={sk.enabled}
                            aria-label={`${sk.enabled ? "停用" : "启用"} ${sk.name}`}
                            disabled={busy || !sk.can_edit}
                            onCheckedChange={() => void handleToggle(sk)}
                          />
                        </div>

                        <div>
                          <h3 className="text-sm font-semibold text-foreground">{sk.name}</h3>
                          <div className="mt-0.5 flex flex-wrap items-center gap-2 font-mono text-2xs text-muted-foreground">
                            <span>{sk.slug}</span>
                          </div>
                        </div>

                        <p className="text-xs leading-relaxed text-muted-foreground">
                          {sk.description || "无简介说明，点击编辑查看完整 SKILL.md 规范。"}
                        </p>
                        {sk.ui_manifest_error && (
                          <div className="rounded border border-warning/40 bg-warning/10 px-2 py-1 text-2xs text-warning">
                            plugin.yaml 校验错误：{sk.ui_manifest_error}
                          </div>
                        )}
                      </div>

                      <Stack gap={2} className="border-t pt-2.5">
                        <div className="flex flex-wrap items-center justify-between gap-1 font-mono text-2xs text-muted-foreground">
                          <span>
                            归属：{sk.owner_name || sk.owner_id} · 附属 {sk.file_count} 文件
                          </span>
                          <span>
                            驱动 {sk.run_count} 次 · {sk.sha256 ? sk.sha256.slice(0, 8) : "—"}
                          </span>
                        </div>
                        <div className="font-mono text-2xs text-muted-foreground">
                          最近执行：{fmtStamp(sk.last_run_at_ms)}
                        </div>
                        <Inline gap={2} className="flex-wrap pt-0.5">
                          <Button
                            size="sm"
                            variant="primary"
                            disabled={busy || !sk.enabled || onlineTargetsCount === 0}
                            onClick={() => openSkillTestRun(sk)}
                          >
                            <FlaskConical width={12} height={12} />
                            试跑验证
                          </Button>
                          <Button
                            size="sm"
                            disabled={busy || !sk.enabled || onlineTargetsCount === 0}
                            onClick={() => void handleDriveInspection([sk.slug], `Skill「${sk.name}」巡检`)}
                          >
                            <Play width={12} height={12} />
                            {isRunningThis ? "执行中…" : "全量巡检"}
                          </Button>
                          <Button size="sm" disabled={busy || onlineTargetsCount === 0} onClick={() => void handleSync(sk.slug)}>
                            同步
                          </Button>
                          <Button size="sm" disabled={busy} onClick={() => void openSkillEditor(sk.slug)}>
                            {sk.can_edit ? "编辑" : "查看"}
                          </Button>
                          {sk.scope === "system" && isAdmin && (
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={busy}
                              title="恢复为系统内置默认定义"
                              onClick={() => void handleResetSystem(sk)}
                            >
                              <RotateCcw width={12} height={12} />
                              恢复默认
                            </Button>
                          )}
                          {sk.can_delete && (
                            <Button
                              size="sm"
                              variant="danger-outline"
                              disabled={busy}
                              onClick={() => void handleDelete(sk)}
                            >
                              <Trash2 width={12} height={12} />
                              删除
                            </Button>
                          )}
                        </Inline>
                      </Stack>
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          )}
        </>
      ) : (
        <div className="space-y-3">
          {/* Agent Runtime Skills Toolbar */}
          <Card>
            <CardContent className="flex flex-wrap items-center justify-between gap-3 p-3.5">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs font-medium text-muted-foreground">目标 Agent</span>
                <Select
                  scale="sm"
                  className="w-56 font-mono"
                  value={selectedTarget}
                  onChange={(e) => setSelectedTarget(e.target.value)}
                  disabled={onlineTargetsCount === 0}
                >
                  {onlineTargetsCount === 0 ? (
                    <option value="">暂无在线 Agent</option>
                  ) : (
                    onlineTargets.map((t) => (
                      <option key={t.key} value={t.key}>
                        {t.key}
                      </option>
                    ))
                  )}
                </Select>
                {agentSkillsRoot && (
                  <span className="flex items-center gap-1 font-mono text-2xs text-muted-foreground">
                    <FolderOpen width={13} />
                    {agentSkillsRoot}
                  </span>
                )}
              </div>

              <Inline gap={2} className="flex-wrap">
                <Input
                  scale="sm"
                  className="w-52"
                  placeholder="搜索 Agent 已加载 Skill…"
                  value={agentQuery}
                  onChange={(e) => setAgentQuery(e.target.value)}
                />
                <Button
                  size="sm"
                  disabled={busy || !selectedTarget}
                  onClick={() => void handleSync(undefined, selectedTarget)}
                >
                  <Send width={13} height={13} />
                  同步平台 Skill 到此节点
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={agentLoading || !selectedTarget}
                  onClick={() => setAgentRevision((v) => v + 1)}
                >
                  <RefreshCw width={13} height={13} className={agentLoading ? "animate-spin" : ""} />
                  实时读取
                </Button>
              </Inline>
            </CardContent>
          </Card>

          {agentError && <StatusBanner tone="error">{agentError}</StatusBanner>}

          {onlineTargetsCount === 0 ? (
            <Card>
              <CardContent className="py-10 text-center text-sm text-muted-foreground">
                当前没有在线的 Agent 节点，无法实时读取运行态 Skill 目录。
              </CardContent>
            </Card>
          ) : agentLoading && agentSkills.length === 0 ? (
            <p role="status" className="py-10 text-center text-sm text-muted-foreground">
              正在从 {selectedTarget} 实时读取 /root/.agent/skills/ …
            </p>
          ) : filteredAgentSkills.length === 0 ? (
            <Card>
              <CardContent className="py-10 text-center text-sm text-muted-foreground">
                {agentSkills.length === 0
                  ? `${selectedTarget} 的 ${agentSkillsRoot || "/root/.agent/skills"} 目录暂未发现 Skill，可点击右上角「同步平台 Skill 到此节点」。`
                  : "当前搜索条件下无匹配的 Agent 运行态 Skill。"}
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {filteredAgentSkills.map((as) => {
                const managed = platformSkillMap.get(as.slug)
                return (
                  <Card key={as.slug} className="flex h-full flex-col">
                    <CardContent className="flex flex-1 flex-col justify-between gap-3 p-4">
                      <div className="space-y-2">
                        <div className="flex flex-wrap items-center justify-between gap-1.5">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <Badge variant={managed ? "primary" : "success"}>
                              {managed ? "平台托管" : "Agent 镜像内置"}
                            </Badge>
                            <Badge variant="outline">
                              {as.kind === "dir" ? `目录 · ${as.file_count} 文件` : "单文件"}
                            </Badge>
                          </div>
                          <span className="font-mono text-2xs text-muted-foreground">
                            {formatBytesCompact(as.size_bytes)}
                          </span>
                        </div>

                        <div>
                          <h3 className="text-sm font-semibold text-foreground">{as.name || as.slug}</h3>
                          <div className="mt-0.5 font-mono text-2xs text-muted-foreground">{as.slug}</div>
                        </div>

                        <p className="text-xs leading-relaxed text-muted-foreground">
                          {as.description || managed?.description || "Agent 运行时已挂载该 Skill，可供 AI 助手与巡检直接调用。"}
                        </p>
                      </div>

                      <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-2.5">
                        <span className="font-mono text-2xs text-muted-foreground">
                          更新：{fmtStamp(as.updated_at_ms)}
                        </span>
                        <Inline gap={1}>
                          {managed && (
                            <>
                              <Button
                                size="sm"
                                variant="primary"
                                disabled={busy || !managed.enabled}
                                onClick={() => {
                                  setActiveTab("registry")
                                  setTestTarget(selectedTarget)
                                  openSkillTestRun(managed)
                                }}
                              >
                                <FlaskConical width={12} height={12} />
                                试跑
                              </Button>
                              <Button
                                size="sm"
                                variant="ghost"
                                disabled={busy}
                                onClick={() => {
                                  setActiveTab("registry")
                                  void openSkillEditor(managed.slug)
                                }}
                              >
                                {managed.can_edit ? "编辑" : "查看"}
                              </Button>
                            </>
                          )}
                        </Inline>
                      </div>
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          )}
        </div>
      )}
    </section>
  )
}
