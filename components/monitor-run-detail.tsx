"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { Session, Target } from "@/lib/client"
import { describeInspectionRunTitle } from "@/lib/inspection-run-groups"
import {
  ASSET_KIND_LABELS,
  COLLECTOR_METHOD_LABELS,
  STRATEGY_LABELS,
  TRIGGER_LABELS,
  buildRunReportCSV,
  describeMonitorStatus,
  fetchMonitorRunDetail,
  triggerMonitorRun,
  type MonitorRunDetailData,
  type MonitorRunItemRecord,
  type MonitorScheduleRecord,
} from "@/lib/monitor-engine"
import { toInspectionViewerProps } from "@/lib/monitor-metrics"
import type { PlatformSkillRecord } from "@/lib/skills-registry"
import { RefreshIcon } from "./icons"
import { InspectionArtifactViewer } from "./inspection-artifact-viewer"
import { Badge, Button, Card, CardContent, Inline, Select, Stack, StatusBanner } from "./ui"

function fmtStamp(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  return new Date(ms).toLocaleString("zh-CN", { hour12: false })
}

function fmtDuration(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "—"
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

function downloadText(filename: string, content: string, mime: string) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function buildLegacyFallbackArtifact(item: MonitorRunItemRecord): Record<string, unknown> {
  const out: Record<string, unknown> = {
    asset_id: item.asset_id,
    asset_name: item.asset_name,
    asset_kind: item.asset_kind,
    strategy: item.strategy,
    status: item.status,
  }
  const m = item.metrics || {}
  if (m.cpu_percent != null) out.cpu_percent = m.cpu_percent
  if (m.memory_percent != null) out.memory_percent = m.memory_percent
  if (m.disk_percent != null) out.disk_percent = m.disk_percent
  if (m.disk_iops != null) out.disk_iops = m.disk_iops
  if (m.response_time_ms != null) out.response_time_ms = m.response_time_ms
  if (m.os_info) out.os_info = m.os_info
  if (m.kernel_info) out.kernel_info = m.kernel_info
  if (item.checks?.length) {
    out.checks = item.checks.map((c) => ({
      name: c.name,
      status: c.status,
      value: c.value || "—",
      threshold: c.threshold || "—",
      detail: c.detail || "—",
    }))
  }
  if (item.evidence && Object.keys(item.evidence).length > 0) {
    out.evidence = item.evidence
  }
  return out
}

export function MonitorRunDetailView({
  session,
  runId,
  skillBySlug,
  scheduleById,
  focusTarget,
  headingLabel = "执行报告与产物",
  backLabel = "返回执行记录",
  onBack,
  onSelectRun,
  onRunCreated,
}: {
  session: Session
  runId: string
  skillBySlug: ReadonlyMap<string, PlatformSkillRecord>
  scheduleById: ReadonlyMap<string, MonitorScheduleRecord>
  focusTarget?: Target | null
  headingLabel?: string
  backLabel?: string
  onBack: () => void
  onSelectRun: (nextRunId: string) => void
  onRunCreated?: () => void
}) {
  const [runDetail, setRunDetail] = useState<MonitorRunDetailData | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [itemStatusFilter, setItemStatusFilter] = useState("")
  const [itemStrategyFilter, setItemStrategyFilter] = useState("")
  const [showAllTargets, setShowAllTargets] = useState(false)
  const [revision, setRevision] = useState(0)

  const reload = useCallback(() => setRevision((v) => v + 1), [])

  useEffect(() => {
    setShowAllTargets(false)
    setItemStatusFilter("")
    setItemStrategyFilter("")
  }, [runId, focusTarget?.key])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError("")
    fetchMonitorRunDetail(session, runId, ctrl.signal)
      .then((res) => {
        if (!ctrl.signal.aborted) {
          setRunDetail(res)
          setLoading(false)
        }
      })
      .catch((err) => {
        if (!ctrl.signal.aborted) {
          setError((err as Error)?.message || "无法加载执行产物")
          setLoading(false)
        }
      })
    return () => ctrl.abort()
  }, [session, runId, revision])

  const skillNameBySlug = useMemo(() => {
    const map = new Map<string, string>()
    for (const [slug, sk] of skillBySlug) map.set(slug, sk.name)
    return map
  }, [skillBySlug])

  const focusTargetItems = useMemo(() => {
    const items = runDetail?.items ?? []
    if (!focusTarget) return items
    return items.filter((it) => it.cluster === focusTarget.cluster && it.instance === focusTarget.instance)
  }, [runDetail, focusTarget])

  const hasOtherTargets = Boolean(
    focusTarget && runDetail && runDetail.items.length > focusTargetItems.length,
  )
  const scopeItems =
    focusTarget && hasOtherTargets && focusTargetItems.length > 0 && !showAllTargets
      ? focusTargetItems
      : (runDetail?.items ?? [])

  const filteredItems = useMemo(() => {
    return scopeItems.filter((it) => {
      if (itemStatusFilter && it.status !== itemStatusFilter) return false
      if (itemStrategyFilter && it.strategy !== itemStrategyFilter) return false
      return true
    })
  }, [scopeItems, itemStatusFilter, itemStrategyFilter])

  async function retestRun() {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, { retest_run_id: runId })
      setNotice(`已完成异常项复检（#${res.run.id}）`)
      onRunCreated?.()
      onSelectRun(res.run.id)
    } catch (err) {
      setError((err as Error)?.message || "复检失败")
    } finally {
      setBusy(false)
    }
  }

  const run = runDetail?.run
  const st = describeMonitorStatus(run?.status || "running")
  const hasRetestable = (runDetail?.items ?? []).some((i) =>
    ["warning", "critical", "error", "offline"].includes(i.status),
  )

  return (
    <Stack gap={4}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={onBack}>
            {backLabel}
          </Button>
          <h2 className="text-base font-semibold text-foreground">
            {headingLabel} <span className="font-mono text-sm text-muted-foreground">#{runId}</span>
          </h2>
          {run && <Badge variant={st.variant}>{st.label}</Badge>}
        </div>
        <Inline gap={2} wrap>
          {hasRetestable && (
            <Button size="sm" variant="primary" disabled={busy} onClick={() => void retestRun()}>
              {busy ? "复检中…" : "复检异常项"}
            </Button>
          )}
          {runDetail && (
            <>
              <Button
                size="sm"
                onClick={() => downloadText(`run-${runId}.csv`, buildRunReportCSV(runDetail), "text/csv;charset=utf-8")}
              >
                导出 CSV
              </Button>
              <Button
                size="sm"
                onClick={() => downloadText(`run-${runId}.json`, JSON.stringify(runDetail, null, 2), "application/json")}
              >
                导出完整 JSON
              </Button>
            </>
          )}
          <Button size="sm" disabled={loading} onClick={reload}>
            <RefreshIcon width={14} height={14} className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
        </Inline>
      </div>

      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {notice && <StatusBanner tone="info">{notice}</StatusBanner>}

      {loading && !runDetail ? (
        <p role="status" className="py-8 text-sm text-muted-foreground">正在加载执行产物…</p>
      ) : runDetail && run ? (
        <>
          <Card>
            <CardContent className="space-y-3 p-4">
              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                <span>
                  任务：<strong className="text-foreground">{describeInspectionRunTitle(run, skillNameBySlug, scheduleById)}</strong>
                  {" · "}触发：{TRIGGER_LABELS[run.trigger_kind] || run.trigger_kind}
                  {" · "}执行人：{run.triggered_by || "system"}
                </span>
                <span className="font-mono">
                  {fmtStamp(run.started_at_ms)} · 耗时 {fmtDuration(run.duration_ms)}
                </span>
              </div>

              {(run.skills?.length || run.prompt || run.command) && (
                <div className="space-y-2 rounded-md border bg-muted/20 p-3 text-xs">
                  {run.skills && run.skills.length > 0 && (
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="text-muted-foreground">关联 Skill：</span>
                      {run.skills.map((slug) => (
                        <Badge key={slug} variant="primary">
                          {skillBySlug.get(slug)?.name || slug}
                        </Badge>
                      ))}
                    </div>
                  )}
                  {run.prompt && (
                    <div className="space-y-0.5">
                      <span className="text-muted-foreground">执行提示词：</span>
                      <p className="whitespace-pre-wrap text-foreground">{run.prompt}</p>
                    </div>
                  )}
                  {run.command && (
                    <div className="space-y-0.5">
                      <span className="text-muted-foreground">执行命令：</span>
                      <pre className="max-h-28 overflow-auto rounded border bg-background px-2 py-1.5 font-mono text-2xs text-foreground">
                        {run.command}
                      </pre>
                    </div>
                  )}
                </div>
              )}

              {run.summary && <p className="text-sm font-medium text-foreground">{run.summary}</p>}
              <Inline gap={2} wrap>
                <div className="min-w-[112px] flex-1 rounded-md border bg-background/60 p-2.5">
                  <div className="text-2xs text-muted-foreground">目标 / 产物数</div>
                  <div className="font-mono text-base font-semibold">{run.total_targets} / {run.total_assets}</div>
                </div>
                <div className="min-w-[112px] flex-1 rounded-md border bg-background/60 p-2.5">
                  <div className="text-2xs text-muted-foreground">通过</div>
                  <div className="font-mono text-base font-semibold text-success">{run.passed_checks}</div>
                </div>
                <div className="min-w-[112px] flex-1 rounded-md border bg-background/60 p-2.5">
                  <div className="text-2xs text-muted-foreground">告警</div>
                  <div className="font-mono text-base font-semibold text-warning">{run.warning_checks}</div>
                </div>
                <div className="min-w-[112px] flex-1 rounded-md border bg-background/60 p-2.5">
                  <div className="text-2xs text-muted-foreground">严重 / 失败</div>
                  <div className="font-mono text-base font-semibold text-destructive">{run.failed_checks}</div>
                </div>
                <div className="min-w-[112px] flex-1 rounded-md border bg-background/60 p-2.5">
                  <div className="text-2xs text-muted-foreground">离线 / 跳过</div>
                  <div className="font-mono text-base font-semibold">{run.offline_targets} / {run.skipped_checks}</div>
                </div>
              </Inline>
            </CardContent>
          </Card>

          {(hasOtherTargets || scopeItems.length > 1) && (
            <div className="flex flex-wrap items-center gap-2">
              {hasOtherTargets && focusTarget && focusTargetItems.length > 0 && (
                <div className="flex items-center gap-1.5" role="group" aria-label="产物目标筛选">
                  <button
                    type="button"
                    aria-pressed={!showAllTargets}
                    onClick={() => setShowAllTargets(false)}
                    className={`rounded-md border px-2.5 py-1 text-xs transition-colors ${
                      !showAllTargets
                        ? "border-primary/60 bg-primary/15 font-medium text-primary"
                        : "border-border bg-card text-muted-foreground hover:bg-muted/50 hover:text-foreground"
                    }`}
                  >
                    仅当前目标 ({focusTargetItems.length})
                  </button>
                  <button
                    type="button"
                    aria-pressed={showAllTargets}
                    onClick={() => setShowAllTargets(true)}
                    className={`rounded-md border px-2.5 py-1 text-xs transition-colors ${
                      showAllTargets
                        ? "border-primary/60 bg-primary/15 font-medium text-primary"
                        : "border-border bg-card text-muted-foreground hover:bg-muted/50 hover:text-foreground"
                    }`}
                  >
                    全部目标 ({runDetail.items.length})
                  </button>
                </div>
              )}
              {scopeItems.length > 1 && (
                <>
                  <Select scale="sm" className="w-36" value={itemStatusFilter} onChange={(e) => setItemStatusFilter(e.target.value)}>
                    <option value="">全部状态</option>
                    <option value="critical">严重</option>
                    <option value="warning">告警</option>
                    <option value="offline">离线</option>
                    <option value="error">错误</option>
                    <option value="pass">正常</option>
                    <option value="skipped">跳过</option>
                  </Select>
                  <Select scale="sm" className="w-52" value={itemStrategyFilter} onChange={(e) => setItemStrategyFilter(e.target.value)}>
                    <option value="">全部来源 / Skill</option>
                    {Array.from(new Set(scopeItems.map((i) => i.strategy))).map((k) => (
                      <option key={k} value={k}>
                        {k.startsWith("skill:") ? `Skill: ${k.slice(6)}` : STRATEGY_LABELS[k] || k}
                      </option>
                    ))}
                  </Select>
                </>
              )}
              <span className="text-xs text-muted-foreground">
                显示 {filteredItems.length} / {runDetail.items.length} 份目标产物
              </span>
            </div>
          )}

          <Stack gap={3}>
            {filteredItems.map((it) => {
              const itSt = describeMonitorStatus(it.status)
              const drivenSkillSlug = it.strategy.startsWith("skill:")
                ? it.strategy.slice(6)
                : typeof it.evidence?.skill_slug === "string"
                  ? it.evidence.skill_slug
                  : ""
              const hasNativeArtifact = Boolean(
                it.artifact_json !== undefined ||
                  (it.artifact_markdown && it.artifact_markdown.trim()) ||
                  (it.raw_output && it.raw_output.trim()),
              )
              const effectiveJson = hasNativeArtifact ? it.artifact_json : buildLegacyFallbackArtifact(it)
              const effectiveFormat = hasNativeArtifact ? it.artifact_format : "json"

              return (
                <Card key={it.id}>
                  <CardContent className="space-y-3 p-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge variant={itSt.variant}>{itSt.label}</Badge>
                        <strong className="text-sm text-foreground">{it.asset_name || it.asset_id}</strong>
                        <span className="font-mono text-xs text-muted-foreground">{it.cluster}/{it.instance}</span>
                        {drivenSkillSlug && <Badge variant="primary">Skill: {drivenSkillSlug}</Badge>}
                        <Badge variant="outline">{COLLECTOR_METHOD_LABELS[it.collector_method] || it.collector_method}</Badge>
                        <Badge variant="outline">{ASSET_KIND_LABELS[it.asset_kind] || it.asset_kind}</Badge>
                      </div>
                      <span className="font-mono text-2xs text-muted-foreground">
                        耗时 {fmtDuration(it.duration_ms)}{it.reason ? ` · ${it.reason}` : ""}
                      </span>
                    </div>

                    <InspectionArtifactViewer
                      format={effectiveFormat}
                      markdown={it.artifact_markdown}
                      jsonData={effectiveJson}
                      rawOutput={it.raw_output}
                      filePrefix={`${runId}-${it.cluster}-${it.instance}-${it.asset_id}`}
                      {...toInspectionViewerProps(it, session, drivenSkillSlug)}
                    />
                  </CardContent>
                </Card>
              )
            })}
          </Stack>
        </>
      ) : null}
    </Stack>
  )
}
