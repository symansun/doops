"use client"

import { useMemo, useState } from "react"
import { Check, Play, Sparkles, Tag, X } from "lucide-react"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { assetRequest } from "@/lib/assets"
import { triggerMonitorRun } from "@/lib/monitor-engine"
import {
  MAX_MACHINE_TAGS,
  buildMachineTagUpdatePayload,
  computeMergedMachineTags,
  type MachineInventorySummary,
} from "@/lib/machine-tag-discovery"
import { buildBatchMachineTagPlans, type BatchMachineTagPlan } from "@/lib/catalog-extensions"
import { Badge, Button, Card, CardContent, CardHeader, CardTitle, StatusBanner } from "@/components/ui"

export function MachineBatchTagPanel({
  session,
  machines,
  onlineTargets,
  onClose,
  onApplied,
}: {
  session: Session
  machines: MachineInventorySummary[]
  onlineTargets: Map<string, Target>
  onClose: () => void
  onApplied: () => void
}) {
  const isAdmin = sessionIsAdmin(session)
  const [running, setRunning] = useState(false)
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [runId, setRunId] = useState("")
  const [plans, setPlans] = useState<BatchMachineTagPlan[]>([])
  const [selectedByAsset, setSelectedByAsset] = useState<Record<string, Record<string, boolean>>>({})

  const eligibleMachines = useMemo(
    () =>
      machines.filter(
        (m) => m.cluster && m.instance && onlineTargets.has(JSON.stringify([m.cluster, m.instance])),
      ),
    [machines, onlineTargets],
  )

  const uniqueTargets = useMemo(() => {
    const seen = new Map<string, { cluster: string; instance: string }>()
    for (const m of eligibleMachines) {
      if (!m.cluster || !m.instance) continue
      const key = `${m.cluster}/${m.instance}`
      if (!seen.has(key)) seen.set(key, { cluster: m.cluster, instance: m.instance })
    }
    return Array.from(seen.values())
  }, [eligibleMachines])

  async function handleRunBatchDiscovery() {
    if (running || uniqueTargets.length === 0) return
    setRunning(true)
    setError("")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, {
        scope_mode: "targets",
        targets: uniqueTargets,
        skills: ["machine-workload-discovery"],
      })
      const items = (res.items || []).filter(
        (it) =>
          it.asset_id === "machine-workload-discovery" ||
          it.strategy === "skill:machine-workload-discovery",
      )
      const built = buildBatchMachineTagPlans(eligibleMachines, items)
      const initialChecks: Record<string, Record<string, boolean>> = {}
      for (const p of built) {
        const map: Record<string, boolean> = {}
        for (const s of p.suggested) map[s.tag] = true
        initialChecks[p.machine.asset_id] = map
      }
      setRunId(res.run.id)
      setPlans(built)
      setSelectedByAsset(initialChecks)
      setNotice(
        `已完成 ${uniqueTargets.length} 个在线 Agent 节点的只读部署挖掘（#${res.run.id}，耗时 ${res.run.duration_ms} ms），请勾选确认后合并写入机器台账。`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : "批量执行部署挖掘失败")
    } finally {
      setRunning(false)
    }
  }

  function toggleTag(assetId: string, tag: string) {
    setSelectedByAsset((prev) => ({
      ...prev,
      [assetId]: {
        ...(prev[assetId] ?? {}),
        [tag]: !(prev[assetId]?.[tag] ?? false),
      },
    }))
  }

  const computedPlans = useMemo(
    () =>
      plans.map((plan) => {
        const map = selectedByAsset[plan.machine.asset_id] ?? {}
        const chosen = plan.suggested.filter((s) => map[s.tag]).map((s) => s.tag)
        const merge = computeMergedMachineTags(plan.machine.tags ?? [], chosen)
        return { ...plan, merge }
      }),
    [plans, selectedByAsset],
  )

  const actionablePlans = useMemo(
    () => computedPlans.filter((p) => p.merge.added.length > 0 && !p.merge.exceedsLimit),
    [computedPlans],
  )
  const overflowCount = useMemo(
    () => computedPlans.filter((p) => p.merge.exceedsLimit).length,
    [computedPlans],
  )

  async function handleApplyAll() {
    if (!isAdmin || applying || actionablePlans.length === 0) return
    setApplying(true)
    setError("")
    setNotice("")
    let succeeded = 0
    const failed: string[] = []
    for (const plan of actionablePlans) {
      try {
        const payload = buildMachineTagUpdatePayload(plan.machine, plan.merge.merged)
        await assetRequest("machines", payload)
        succeeded += 1
      } catch (err) {
        failed.push(`${plan.machine.name}: ${err instanceof Error ? err.message : "写入失败"}`)
      }
    }
    setApplying(false)
    if (failed.length > 0) {
      setError(`部分机器标签写入失败（成功 ${succeeded}/${actionablePlans.length}）：${failed.join("；")}`)
    }
    if (succeeded > 0) {
      setNotice(`已批量将挖掘标签合并写入 ${succeeded} 台机器的 CMDB 台账（保留原有手工标签，已记入审计）。`)
      onApplied()
    }
  }

  return (
    <Card className="border-primary/35 bg-card">
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
            <Sparkles className="size-4 text-primary" aria-hidden="true" />
            <span>批量主机部署挖掘与 CMDB 打标</span>
            <Badge variant="outline">
              当前筛选中在线可挖掘：{eligibleMachines.length} 台（{uniqueTargets.length} 个 Agent）
            </Badge>
            {runId && <span className="font-mono text-xs text-muted-foreground">#{runId}</span>}
          </CardTitle>
          <div className="flex items-center gap-1.5">
            <Button
              type="button"
              size="sm"
              variant="primary"
              disabled={running || applying || uniqueTargets.length === 0}
              onClick={() => void handleRunBatchDiscovery()}
            >
              <Play className="size-3.5" aria-hidden="true" />
              {running ? "正在挖掘部署栈…" : plans.length > 0 ? "重新扫描挖掘" : "开始批量挖掘"}
            </Button>
            <Button type="button" size="sm" variant="ghost" aria-label="关闭批量打标面板" onClick={onClose}>
              <X className="size-3.5" aria-hidden="true" />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3 text-xs">
        <p className="text-muted-foreground">
          只读执行 <code>machine-workload-discovery</code> 识别各节点运行中的数据库、中间件、编程语言/框架（Java/Python/PHP/Node/Go）与业务应用版本（绝不读取 <code>.env</code> 或口令），支持一键合并写入机器台账标签。
        </p>
        {uniqueTargets.length === 0 && (
          <StatusBanner tone="warning">
            当前筛选结果中没有已绑定且在线的 Agent 机器，请先调整筛选条件或为机器绑定在线 Agent。
          </StatusBanner>
        )}
        {error && <StatusBanner tone="error">{error}</StatusBanner>}
        {notice && <StatusBanner tone="success">{notice}</StatusBanner>}
        {overflowCount > 0 && (
          <StatusBanner tone="warning">
            有 {overflowCount} 台机器合并后超过 {MAX_MACHINE_TAGS} 个标签上限，请先取消勾选部分新标签后再写入。
          </StatusBanner>
        )}

        {computedPlans.length > 0 && (
          <div className="space-y-2.5">
            {computedPlans.map((plan) => (
              <div key={plan.machine.asset_id} className="space-y-2 rounded-lg border bg-background/60 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-semibold text-foreground">{plan.machine.name}</span>
                    <code className="font-mono text-2xs text-muted-foreground">{plan.machine.asset_id}</code>
                    <Badge variant="outline">{plan.targetKey}</Badge>
                    <Badge
                      variant={
                        plan.runError
                          ? "danger"
                          : plan.merge.exceedsLimit
                            ? "danger"
                            : plan.merge.added.length > 0
                              ? "primary"
                              : "success"
                      }
                    >
                      {plan.runError
                        ? `采集失败 (${plan.runStatus})`
                        : plan.merge.exceedsLimit
                          ? `超上限 ${plan.merge.merged.length}/${MAX_MACHINE_TAGS}`
                          : plan.merge.added.length > 0
                            ? `拟新增 +${plan.merge.added.length}（共 ${plan.merge.merged.length}/${MAX_MACHINE_TAGS}）`
                            : "标签已是最新"}
                    </Badge>
                  </div>
                  <span className="text-2xs text-muted-foreground">
                    已有标签：{plan.merge.existing.length > 0 ? plan.merge.existing.join("、") : "暂无"}
                  </span>
                </div>

                {plan.runError ? (
                  <p className="text-danger">采集异常：{plan.runError}</p>
                ) : plan.suggested.length === 0 ? (
                  <p className="text-muted-foreground">未识别到新增数据库/运行环境/业务系统标签。</p>
                ) : (
                  <div className="flex flex-wrap gap-1.5">
                    {plan.suggested.map((item) => {
                      const already = plan.merge.alreadyPresent.includes(item.tag)
                      const checked = Boolean(selectedByAsset[plan.machine.asset_id]?.[item.tag])
                      return (
                        <button
                          key={item.tag}
                          type="button"
                          disabled={already || applying}
                          onClick={() => toggleTag(plan.machine.asset_id, item.tag)}
                          title={`${item.evidence || item.source}（置信度：${item.confidence}）`}
                          className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-2xs transition-colors ${
                            already
                              ? "border-border bg-muted/40 text-muted-foreground"
                              : checked
                                ? "border-primary/60 bg-primary/10 text-foreground"
                                : "border-border bg-background text-muted-foreground opacity-65 hover:opacity-100"
                          }`}
                        >
                          {already ? (
                            <Check className="size-3 text-success" aria-hidden="true" />
                          ) : (
                            <span
                              className={`inline-block size-1.5 rounded-full ${checked ? "bg-primary" : "bg-muted-foreground/40"}`}
                              aria-hidden="true"
                            />
                          )}
                          <span>{item.tag}</span>
                          {already && <span className="font-sans text-2xs">(已有)</span>}
                        </button>
                      )
                    })}
                  </div>
                )}
              </div>
            ))}

            <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-2">
              <span className="text-muted-foreground">
                待写入机器：<strong className="text-foreground">{actionablePlans.length}</strong> / {computedPlans.length} 台
              </span>
              {isAdmin ? (
                <Button
                  type="button"
                  size="sm"
                  variant="primary"
                  disabled={applying || actionablePlans.length === 0}
                  onClick={() => void handleApplyAll()}
                >
                  <Tag className="size-3.5" aria-hidden="true" />
                  {applying
                    ? "正在批量写入台账…"
                    : `批量写入已勾选标签（${actionablePlans.length} 台）`}
                </Button>
              ) : (
                <Badge variant="outline">仅平台管理员可批量写入机器台账</Badge>
              )}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
