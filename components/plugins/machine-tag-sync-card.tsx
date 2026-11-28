"use client"

import { useEffect, useMemo, useState } from "react"
import { Check, Copy, RefreshCw, Tag } from "lucide-react"
import { sessionIsAdmin, type Session } from "@/lib/client"
import { assetRequest } from "@/lib/assets"
import {
  MAX_MACHINE_TAGS,
  buildMachineTagUpdatePayload,
  computeMergedMachineTags,
  normalizeSuggestedTags,
  type MachineInventorySummary,
} from "@/lib/machine-tag-discovery"
import { Badge, Button, Card, CardContent, CardHeader, CardTitle, Select, StatusBanner } from "@/components/ui"

export function MachineTagSyncCard({
  session,
  cluster,
  instance,
  artifactJson,
}: {
  session: Session
  cluster: string
  instance: string
  artifactJson: unknown
}) {
  const isAdmin = sessionIsAdmin(session)
  const suggested = useMemo(() => normalizeSuggestedTags(artifactJson), [artifactJson])
  const [machines, setMachines] = useState<MachineInventorySummary[]>([])
  const [loadingMachines, setLoadingMachines] = useState(false)
  const [loadError, setLoadError] = useState("")
  const [selectedAssetId, setSelectedAssetId] = useState("")
  const [checkedTags, setCheckedTags] = useState<Record<string, boolean>>({})
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState("")
  const [saveSuccess, setSaveSuccess] = useState("")
  const [copied, setCopied] = useState(false)
  const [reloadTick, setReloadTick] = useState(0)

  const tagSignature = useMemo(() => suggested.map((s) => s.tag).join(","), [suggested])

  useEffect(() => {
    const next: Record<string, boolean> = {}
    for (const s of suggested) next[s.tag] = true
    setCheckedTags(next)
    setSaveError("")
    setSaveSuccess("")
  }, [tagSignature, suggested])

  useEffect(() => {
    if (suggested.length === 0) return
    let active = true
    setLoadingMachines(true)
    setLoadError("")
    assetRequest<{ machines?: MachineInventorySummary[] }>("machines")
      .then((res) => {
        if (!active) return
        const matched = (res.machines ?? []).filter(
          (m) => m.cluster === cluster && m.instance === instance,
        )
        setMachines(matched)
        setSelectedAssetId((prev) =>
          matched.some((m) => m.asset_id === prev) ? prev : (matched[0]?.asset_id ?? ""),
        )
      })
      .catch((err) => {
        if (!active) return
        setLoadError(err instanceof Error ? err.message : "读取机器台账失败")
      })
      .finally(() => {
        if (active) setLoadingMachines(false)
      })
    return () => {
      active = false
    }
  }, [cluster, instance, suggested.length, reloadTick])

  const activeMachine = useMemo(
    () => machines.find((m) => m.asset_id === selectedAssetId) ?? machines[0] ?? null,
    [machines, selectedAssetId],
  )

  const selectedList = useMemo(
    () => suggested.filter((s) => checkedTags[s.tag]).map((s) => s.tag),
    [suggested, checkedTags],
  )

  const mergePlan = useMemo(
    () => computeMergedMachineTags(activeMachine?.tags ?? [], selectedList),
    [activeMachine, selectedList],
  )

  if (suggested.length === 0) return null

  function toggleTag(tag: string) {
    setSaveError("")
    setSaveSuccess("")
    setCheckedTags((prev) => ({ ...prev, [tag]: !prev[tag] }))
  }

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(selectedList.join(" "))
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1800)
    } catch {
      setSaveError("复制失败，请手动选中标签文本")
    }
  }

  async function handleApplyTags() {
    if (!isAdmin || !activeMachine || saving || mergePlan.added.length === 0 || mergePlan.exceedsLimit) return
    setSaving(true)
    setSaveError("")
    setSaveSuccess("")
    try {
      const payload = buildMachineTagUpdatePayload(activeMachine, mergePlan.merged)
      const res = await assetRequest<{ machine?: MachineInventorySummary }>("machines", payload)
      if (res.machine) {
        setMachines((prev) =>
          prev.map((m) => (m.asset_id === res.machine!.asset_id ? res.machine! : m)),
        )
      } else {
        setReloadTick((n) => n + 1)
      }
      setSaveSuccess(
        `已成功合并写入 ${mergePlan.added.length} 个新标签到机器「${activeMachine.name}」（共 ${mergePlan.merged.length}/${MAX_MACHINE_TAGS} 个，已记入机器台账审计）`,
      )
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : "写入机器台账标签失败")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card className="border-primary/30 bg-card">
      <CardHeader className="pb-2">
        <CardTitle className="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span className="flex items-center gap-1.5">
            <Tag className="size-4 text-primary" aria-hidden="true" />
            一键同步挖掘标签至「机器台账」（CMDB）
          </span>
          <div className="flex items-center gap-1.5">
            <Badge variant="outline">
              合并后 {mergePlan.merged.length} / {MAX_MACHINE_TAGS}
            </Badge>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={loadingMachines}
              onClick={() => setReloadTick((n) => n + 1)}
            >
              <RefreshCw className={`size-3.5 ${loadingMachines ? "animate-spin" : ""}`} aria-hidden="true" />
              刷新台账
            </Button>
          </div>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-xs">
        {loadError && <StatusBanner tone="warning">{loadError}</StatusBanner>}
        {saveError && <StatusBanner tone="error">{saveError}</StatusBanner>}
        {saveSuccess && <StatusBanner tone="success">{saveSuccess}</StatusBanner>}

        {activeMachine ? (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border bg-muted/20 px-3 py-2">
            <div className="space-y-0.5">
              <div className="font-medium text-foreground">
                关联机器：{activeMachine.name}（<code className="font-mono">{activeMachine.asset_id}</code> · r{activeMachine.revision}）
              </div>
              <div className="text-muted-foreground">
                现有标签（保留不覆盖）：
                {mergePlan.existing.length === 0 ? "暂无" : mergePlan.existing.join("、")}
              </div>
            </div>
            {machines.length > 1 && (
              <Select
                scale="sm"
                aria-label="选择目标关联机器"
                value={activeMachine.asset_id}
                onChange={(e) => setSelectedAssetId(e.target.value)}
              >
                {machines.map((m) => (
                  <option key={m.asset_id} value={m.asset_id}>
                    {m.name} ({m.asset_id})
                  </option>
                ))}
              </Select>
            )}
          </div>
        ) : !loadingMachines ? (
          <div className="rounded-md border border-dashed bg-muted/15 px-3 py-2 text-muted-foreground">
            当前目标 <code className="font-mono">{cluster}/{instance}</code> 尚未在「机器台账」绑定对应机器记录。请先到「机器台账」关联此目标，或点击下方「复制推荐标签」手工粘贴。
          </div>
        ) : null}

        <div className="space-y-1.5">
          <div className="text-muted-foreground">勾选本次要合并的新标签（已有标签自动保留，绝不覆写手工业务标签）：</div>
          <div className="flex flex-wrap gap-1.5">
            {suggested.map((item) => {
              const already = mergePlan.alreadyPresent.includes(item.tag)
              const checked = Boolean(checkedTags[item.tag])
              return (
                <button
                  key={item.tag}
                  type="button"
                  disabled={already || saving}
                  onClick={() => toggleTag(item.tag)}
                  title={`${item.evidence || item.source}（置信度：${item.confidence}）`}
                  className={`inline-flex items-center gap-1 rounded-md border px-2 py-1 font-mono text-2xs transition-colors ${
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
                      className={`inline-block size-2 rounded-full ${checked ? "bg-primary" : "bg-muted-foreground/40"}`}
                      aria-hidden="true"
                    />
                  )}
                  <span>{item.tag}</span>
                  {already && <span className="font-sans text-2xs">(已有)</span>}
                </button>
              )
            })}
          </div>
        </div>

        {mergePlan.exceedsLimit && (
          <StatusBanner tone="warning">
            合并后标签总数（{mergePlan.merged.length}）超过机器台账上限（{MAX_MACHINE_TAGS} 个），请先取消勾选至少 {mergePlan.overflowCount} 个标签后再写入。
          </StatusBanner>
        )}

        <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
          <span className="text-muted-foreground">
            本次拟新增 <strong className="text-foreground">{mergePlan.added.length}</strong> 个 · 已有 {mergePlan.alreadyPresent.length} 个
          </span>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" size="sm" variant="default" disabled={selectedList.length === 0} onClick={() => void handleCopy()}>
              {copied ? <Check className="size-3.5 text-success" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
              {copied ? "已复制标签" : "复制推荐标签"}
            </Button>
            {isAdmin ? (
              <Button
                type="button"
                size="sm"
                variant="primary"
                disabled={!activeMachine || saving || mergePlan.added.length === 0 || mergePlan.exceedsLimit}
                onClick={() => void handleApplyTags()}
              >
                <Tag className="size-3.5" aria-hidden="true" />
                {saving
                  ? "正在写入机器台账…"
                  : mergePlan.added.length === 0
                    ? "台账标签已是最新"
                    : `写入机器台账 (+${mergePlan.added.length})`}
              </Button>
            ) : (
              <Badge variant="outline">仅平台管理员可写入机器台账</Badge>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
