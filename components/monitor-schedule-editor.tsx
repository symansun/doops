"use client"

import { useEffect, useState, type FormEvent } from "react"
import type { Session, Target } from "@/lib/client"
import { validateAutomationScheduleInput } from "@/lib/automation"
import {
  INTERVAL_PRESETS,
  mutateMonitorSchedule,
  type MonitorScheduleRecord,
} from "@/lib/monitor-engine"
import type { PlatformSkillRecord } from "@/lib/skills-registry"
import {
  Badge,
  Button,
  Card,
  CardContent,
  Checkbox,
  Grid,
  Inline,
  Input,
  Select,
  StatusBanner,
  Textarea,
} from "./ui"

type ScopeChoice = "current" | "targets" | "all_online"

function toggleItem(items: string[], value: string): string[] {
  return items.includes(value) ? items.filter((v) => v !== value) : [...items, value]
}

function initialScopeChoice(
  editing: MonitorScheduleRecord | null,
  selectedTarget: Target | null,
): { scopeChoice: ScopeChoice; targetKeys: string[] } {
  if (editing) {
    if (editing.scope_mode !== "targets") {
      return { scopeChoice: "all_online", targetKeys: selectedTarget ? [selectedTarget.key] : [] }
    }
    const keys = (editing.targets || []).map((t) => `${t.cluster}/${t.instance}`)
    if (selectedTarget && keys.length === 1 && keys[0] === selectedTarget.key) {
      return { scopeChoice: "current", targetKeys: keys }
    }
    return { scopeChoice: "targets", targetKeys: keys }
  }
  if (selectedTarget) {
    return { scopeChoice: "current", targetKeys: [selectedTarget.key] }
  }
  return { scopeChoice: "all_online", targetKeys: [] }
}

export function MonitorScheduleEditor({
  session,
  targets,
  selectedTarget,
  systemSkills,
  editingSchedule,
  onCancel,
  onSaved,
  onOpenSkills,
}: {
  session: Session
  targets: Target[]
  selectedTarget: Target | null
  systemSkills: PlatformSkillRecord[]
  editingSchedule: MonitorScheduleRecord | null
  onCancel: () => void
  onSaved: (message: string, schedule?: MonitorScheduleRecord) => void | Promise<void>
  onOpenSkills?: () => void
}) {
  const isEditing = Boolean(editingSchedule)
  const onlineCount = targets.filter((t) => t.status !== "offline").length

  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [scopeChoice, setScopeChoice] = useState<ScopeChoice>("all_online")
  const [selectedTargetKeys, setSelectedTargetKeys] = useState<string[]>([])
  const [intervalSeconds, setIntervalSeconds] = useState(3600)
  const [selectedSkills, setSelectedSkills] = useState<string[]>([])
  const [prompt, setPrompt] = useState("")
  const [command, setCommand] = useState("")
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    const scope = initialScopeChoice(editingSchedule, selectedTarget)
    setScopeChoice(scope.scopeChoice)
    setSelectedTargetKeys(scope.targetKeys)
    setError("")
    if (editingSchedule) {
      setName(editingSchedule.name)
      setDescription(editingSchedule.description || "")
      setIntervalSeconds(editingSchedule.interval_seconds || 3600)
      setSelectedSkills(editingSchedule.skills || [])
      setPrompt(editingSchedule.prompt || "")
      setCommand(editingSchedule.command || "")
      return
    }
    setName("")
    setDescription("")
    setIntervalSeconds(3600)
    setSelectedSkills([])
    setPrompt("")
    setCommand("")
  }, [editingSchedule, selectedTarget])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (saving) return

    const scopeMode: "all_online" | "targets" = scopeChoice === "all_online" ? "all_online" : "targets"
    const resolvedTargets =
      scopeChoice === "current"
        ? selectedTarget
          ? [{ cluster: selectedTarget.cluster, instance: selectedTarget.instance }]
          : []
        : scopeChoice === "targets"
          ? selectedTargetKeys
              .map((key) => {
                const idx = key.indexOf("/")
                if (idx <= 0) return null
                return { cluster: key.slice(0, idx), instance: key.slice(idx + 1) }
              })
              .filter((item): item is { cluster: string; instance: string } => item !== null)
          : []

    const validationErr = validateAutomationScheduleInput({
      name,
      command,
      prompt,
      skills: selectedSkills,
      scopeMode,
      hasTarget: Boolean(selectedTarget),
      selectedTargetsCount: resolvedTargets.length,
    })
    if (validationErr) {
      setError(validationErr)
      return
    }

    setSaving(true)
    setError("")
    try {
      const res = await mutateMonitorSchedule(session, {
        action: editingSchedule ? "update" : "create",
        ...(editingSchedule ? { id: editingSchedule.id } : {}),
        name: name.trim(),
        description: description.trim(),
        enabled: editingSchedule ? editingSchedule.enabled : true,
        scope_mode: scopeMode,
        targets: scopeMode === "targets" ? resolvedTargets : [],
        skills: selectedSkills,
        prompt: prompt.trim(),
        command: command.trim(),
        interval_seconds: intervalSeconds,
      })
      const msg = editingSchedule
        ? "定时任务已更新"
        : "定时任务已创建，将由自动化调度器按周期执行"
      await onSaved(msg, res.schedule)
    } catch (err) {
      setError((err as Error).message || "保存定时任务失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardContent className="p-4">
        <form onSubmit={(e) => void handleSubmit(e)} className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold text-foreground">
              {isEditing ? "编辑定时任务" : "新建定时任务"}
            </h2>
            <span className="text-2xs text-muted-foreground">
              支持系统 Skill 巡检、Shell 命令或 AI 提示词，由 Gateway 统一按周期调度
            </span>
          </div>

          {error && <StatusBanner tone="error">{error}</StatusBanner>}

          <Grid cols={1} smCols={3} gap={3}>
            <label className="space-y-1 text-xs">
              <span className="text-muted-foreground">任务名称</span>
              <Input
                scale="sm"
                required
                placeholder="例如：全集群每 15 分钟主机巡检 / 每日 Nginx 配置检查"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>

            <label className="space-y-1 text-xs">
              <span className="text-muted-foreground">执行周期</span>
              <Select
                scale="sm"
                value={String(intervalSeconds)}
                onChange={(e) => setIntervalSeconds(Number(e.target.value))}
              >
                {INTERVAL_PRESETS.map((p) => (
                  <option key={p.value} value={p.value}>
                    {p.label}
                  </option>
                ))}
              </Select>
            </label>

            <label className="space-y-1 text-xs">
              <span className="text-muted-foreground">生效目标</span>
              <Select
                scale="sm"
                value={scopeChoice}
                onChange={(e) => {
                  const next = e.target.value as ScopeChoice
                  setScopeChoice(next)
                  if (next === "targets" && selectedTargetKeys.length === 0 && selectedTarget) {
                    setSelectedTargetKeys([selectedTarget.key])
                  }
                }}
              >
                {selectedTarget && (
                  <option value="current">
                    仅当前目标 ({selectedTarget.cluster}/{selectedTarget.instance})
                  </option>
                )}
                <option value="all_online">全部在线目标 ({onlineCount})</option>
                <option value="targets">指定 Agent 目标</option>
              </Select>
            </label>
          </Grid>

          {scopeChoice === "targets" && (
            <div className="space-y-1.5 rounded-md border bg-muted/20 p-2.5 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2 text-muted-foreground">
                <span>选择目标 Agent（已选 {selectedTargetKeys.length} 个）：</span>
                <Inline gap={2}>
                  <Button
                    type="button"
                    size="sm"
                    variant="link"
                    className="h-auto p-0 text-xs"
                    onClick={() => setSelectedTargetKeys(targets.map((t) => t.key))}
                  >
                    全选 ({targets.length})
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="link"
                    className="h-auto p-0 text-xs text-muted-foreground"
                    onClick={() => setSelectedTargetKeys([])}
                  >
                    清空
                  </Button>
                </Inline>
              </div>
              {targets.length === 0 ? (
                <p className="text-2xs text-muted-foreground">当前没有可见的 Agent 目标。</p>
              ) : (
                <div className="flex flex-wrap gap-3">
                  {targets.map((t) => (
                    <label key={t.key} className="inline-flex items-center gap-1.5 text-xs">
                      <Checkbox
                        checked={selectedTargetKeys.includes(t.key)}
                        onChange={() => setSelectedTargetKeys((prev) => toggleItem(prev, t.key))}
                      />
                      <span className="font-mono text-foreground">{t.cluster}/{t.instance}</span>
                      {t.status === "offline" && <Badge variant="outline">离线</Badge>}
                    </label>
                  ))}
                </div>
              )}
            </div>
          )}

          {systemSkills.length > 0 && (
            <div className="space-y-1.5 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2 text-muted-foreground">
                <span>关联系统 Skill（可选，已选 {selectedSkills.length}；周期任务仅支持系统 Skill）</span>
                {onOpenSkills && (
                  <Button type="button" size="sm" variant="link" className="h-auto p-0 text-xs" onClick={onOpenSkills}>
                    管理 Skill 能力包 →
                  </Button>
                )}
              </div>
              <div className="flex flex-wrap gap-2">
                {systemSkills.map((sk) => (
                  <label
                    key={sk.slug}
                    className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2.5 py-1 text-xs"
                  >
                    <Checkbox
                      checked={selectedSkills.includes(sk.slug)}
                      onChange={() => setSelectedSkills((prev) => toggleItem(prev, sk.slug))}
                    />
                    <span className="text-foreground">{sk.name}</span>
                    {sk.category === "security" && <Badge variant="outline">安全</Badge>}
                  </label>
                ))}
              </div>
            </div>
          )}

          <label className="block space-y-1 text-xs">
            <span className="text-muted-foreground">Shell 命令（可选，stdout 输出 JSON 或 Markdown+JSON 时自动可视化）</span>
            <Textarea
              rows={2}
              className="w-full font-mono text-xs"
              placeholder="例如：nginx -t && df -h /（留空则由所选 Skill 或 AI 提示词驱动）"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
            />
          </label>

          <label className="block space-y-1 text-xs">
            <span className="text-muted-foreground">AI 提示词（可选，描述巡检规则或分析要求）</span>
            <Textarea
              rows={2}
              className="w-full text-xs"
              placeholder="例如：核查磁盘水位、异常进程、关键端口与证书到期时间，输出摘要与建议"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </label>

          <div className="flex flex-wrap items-center justify-end gap-2 border-t pt-2.5">
            <Button type="button" size="sm" variant="ghost" disabled={saving} onClick={onCancel}>
              取消
            </Button>
            <Button type="submit" size="sm" variant="primary" disabled={saving}>
              {saving ? "保存中…" : isEditing ? "保存修改" : "创建定时任务"}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
