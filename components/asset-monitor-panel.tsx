"use client"

import { useEffect, useId, useMemo, useState, type FormEvent } from "react"
import { assetRequest, type TargetManifestResponse } from "@/lib/assets"
import { sessionIsAdmin, type Session, type Target } from "@/lib/client"
import {
  buildSparklineGeometry,
  formatAssetMonitorError,
  humanBytesHint,
  matchMonitorAssetFocus,
  MONITOR_KINDS,
  MONITOR_METRICS,
  MONITOR_RANGE_MS,
  MONITOR_STATES,
  MONITOR_UNITS,
  monitorAssetKey,
  monitorDeviceLabel,
  monitorSourceName,
  resolveTargetInspectionSkills,
  resolveTrendSkillOptions,
  SKILL_CATEGORY_LABELS,
  SKILL_TREND_RANGES,
  visibleMetricsForAsset,
  type AgentMonitorRange,
  type MonitorAssetFocus,
  type MonitorAssetRef,
  type SkillTrendRange,
  type SparklinePointInput,
} from "@/lib/agent-monitor"
import { fetchMonitorMetrics, triggerMonitorRun, type MonitorMetricSeriesResponse } from "@/lib/monitor-engine"
import { consoleFetch } from "@/lib/network-error"
import { fetchPlatformSkills, type PlatformSkillRecord } from "@/lib/skills-registry"
import { extractManifestNamespaces } from "@/lib/target-manifest"
import { Badge, Button, Input, SegmentedControl, Select } from "./ui"

type Asset = MonitorAssetRef
type Point = { at_ms: number; status: string; value: number | null }
type Rule = { threshold: number; duration_seconds: number }
type Alert = Rule & { opened_ms: number; ended_ms: number | null; state: string }
type Series = {
  device_id?: string
  metric: string
  unit: string
  latest: Point
  stale: boolean
  points: Point[]
  rule: Rule | null
  alerts: Alert[]
}
type Detail = { from_ms: number; to_ms: number; max_gap_ms: number; series: Series[] }
type MonitorRange = AgentMonitorRange
type MonitorSubTab = "series" | "skill"

const stamp = (at: number) => new Date(at).toLocaleString("zh-CN", { hour12: false })

async function request<T>(url: string, signal?: AbortSignal, body?: object, demo?: boolean): Promise<T> {
  if (demo) {
    const { demoAssetMonitorRead, demoAssetMonitorWrite } = await import("@/lib/demo")
    return (body ? demoAssetMonitorWrite(body as Record<string, unknown>) : demoAssetMonitorRead(url)) as Promise<T>
  }
  const r = await consoleFetch(
    url,
    body
      ? { method: "POST", signal, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }
      : { cache: "no-store", signal },
  )
  const data = await r.json().catch(() => ({}))
  if (!r.ok) {
    throw new Error(formatAssetMonitorError(r.status, data))
  }
  return data as T
}

function useMonitorRead<T>(url: string, revision = 0, demo?: boolean) {
  const [state, setState] = useState<{ url: string; data?: T; error: string }>({ url: "", error: "" })
  useEffect(() => {
    if (!url) return
    const c = new AbortController()
    request<T>(url, c.signal, undefined, demo)
      .then((data) => { if (!c.signal.aborted) setState({ url, data, error: "" }) })
      .catch((e) => { if (!c.signal.aborted) setState({ url, error: (e as Error).message }) })
    return () => c.abort()
  }, [url, revision, demo])
  return state.url === url ? state : { data: undefined, error: "" }
}

function Sparkline({
  points,
  fromMs,
  toMs,
  maxGapMs,
  ariaLabel,
  sampleCount,
}: {
  points: readonly SparklinePointInput[]
  fromMs: number
  toMs: number
  maxGapMs?: number
  ariaLabel: string
  sampleCount: number
}) {
  const geom = useMemo(
    () => buildSparklineGeometry(points, fromMs, toMs, maxGapMs),
    [points, fromMs, toMs, maxGapMs],
  )
  if (!geom.hasData) return null
  return (
    <div className="mt-2">
      <svg viewBox="0 0 400 80" className="h-20 w-full text-primary" role="img" aria-label={ariaLabel}>
        <path d="M4 73 H396" stroke="currentColor" opacity=".2" />
        {geom.polylines.map((pts, i) => (
          <polyline key={i} points={pts} fill="none" stroke="currentColor" strokeWidth="1.5" />
        ))}
        {geom.dots.map((p, idx) => (
          <circle key={`${p.x}-${p.y}-${idx}`} cx={p.x} cy={p.y} r="2" fill="currentColor" />
        ))}
      </svg>
      <small className="text-muted-foreground">
        {stamp(fromMs)} — {stamp(toMs)} · {sampleCount} 个样本
      </small>
    </div>
  )
}

function MetricCard({
  metric,
  series: s,
  detail,
  extraBadge,
  highlighted = false,
  admin,
  save,
  removeRule,
}: {
  metric: string
  series?: Series
  detail: Detail
  extraBadge?: string
  highlighted?: boolean
  admin: boolean
  save: (metric: string, device: string | undefined, rule: Rule) => Promise<void>
  removeRule: (metric: string, device: string | undefined) => Promise<void>
}) {
  const [busy, setBusy] = useState(false)
  const [feedback, setFeedback] = useState("")

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const fields = new FormData(e.currentTarget)
    setBusy(true)
    setFeedback("")
    try {
      await save(metric, s?.device_id, {
        threshold: Number(fields.get("threshold")),
        duration_seconds: Number(fields.get("seconds")),
      })
      setFeedback("规则已保存")
    } catch (e) {
      setFeedback((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function onDelete() {
    setBusy(true)
    setFeedback("")
    try {
      await removeRule(metric, s?.device_id)
      setFeedback("规则已删除")
    } catch (e) {
      setFeedback((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <article className={`min-w-0 rounded-lg border bg-card p-3${highlighted ? " ring-2 ring-primary/60" : ""}`}>
      <div className="flex flex-wrap items-center justify-between gap-1.5">
        <h4 className="text-sm font-semibold">
          {MONITOR_METRICS[metric] ?? metric}{" "}
          <small className="text-muted-foreground">{s?.unit ?? MONITOR_UNITS[metric]}</small>
        </h4>
        <div className="flex flex-wrap items-center gap-1">
          {highlighted && <Badge variant="primary">当前定位</Badge>}
          {extraBadge && <Badge variant="outline">{extraBadge}</Badge>}
        </div>
      </div>
      {s && monitorDeviceLabel(metric, s.device_id) && (
        <p className="break-all font-mono text-xs text-muted-foreground">
          {monitorDeviceLabel(metric, s.device_id)}
        </p>
      )}
      {!s ? (
        <p className="mt-2 text-sm text-muted-foreground">尚无采集样本</p>
      ) : (
        <>
          <p className="mt-1 font-mono text-sm">
            {s.latest.status === "ok"
              ? `${s.latest.value?.toLocaleString(undefined, { maximumSignificantDigits: 7 })} ${s.unit}`
              : MONITOR_STATES[s.latest.status]}
            {s.latest.status === "ok" && humanBytesHint(s.unit, s.latest.value) && (
              <span className="ml-1.5 text-xs text-muted-foreground">
                ({humanBytesHint(s.unit, s.latest.value)})
              </span>
            )}
            {s.stale && " · 采样已过期"}
          </p>
          <small className="text-muted-foreground">最近观测 {stamp(s.latest.at_ms)}</small>
          <Sparkline
            points={s.points}
            fromMs={detail.from_ms}
            toMs={detail.to_ms}
            maxGapMs={detail.max_gap_ms}
            ariaLabel={`${MONITOR_METRICS[s.metric] ?? s.metric} 历史曲线`}
            sampleCount={s.points.length}
          />
          {s.rule && (
            <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs">
              <span>
                规则 · 高于 {s.rule.threshold} {s.unit}，持续 {s.rule.duration_seconds} 秒
              </span>
              {admin && (
                <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void onDelete()}>
                  删除规则
                </Button>
              )}
            </div>
          )}
          {s.alerts.length > 0 && (
            <ul className="mt-2 space-y-1 text-xs" aria-label="告警记录">
              {s.alerts.map((a) => (
                <li key={a.opened_ms}>
                  {MONITOR_STATES[a.state]} · {stamp(a.opened_ms)}
                  {a.ended_ms && ` 至 ${stamp(a.ended_ms)}`} · &gt; {a.threshold} {s.unit} /{" "}
                  {a.duration_seconds}s
                </li>
              ))}
            </ul>
          )}
          {admin && (
            <details className="mt-2 text-sm">
              <summary className="cursor-pointer">{s.rule ? "调整告警规则" : "设置告警规则"}</summary>
              <form className="mt-2 flex flex-wrap items-end gap-2" onSubmit={submit}>
                <label>
                  阈值（{s.unit}）
                  <Input
                    name="threshold"
                    type="number"
                    min="0"
                    max={metric === "gpu" || metric === "error_rate" ? 100 : undefined}
                    step="any"
                    required
                    defaultValue={s.rule?.threshold}
                  />
                </label>
                <label>
                  持续秒数
                  <Input
                    name="seconds"
                    type="number"
                    min="1"
                    max="604800"
                    step="1"
                    required
                    defaultValue={s.rule?.duration_seconds}
                  />
                </label>
                <Button disabled={busy} type="submit">
                  {busy ? "保存中" : "保存规则"}
                </Button>
              </form>
            </details>
          )}
          {feedback && <p role="status" className="mt-1 text-xs text-muted-foreground">{feedback}</p>}
        </>
      )}
    </article>
  )
}

function SkillTrendsView({
  session,
  target,
  skills,
  revision,
  busy,
  onRunSkill,
}: {
  session: Session
  target: Target
  skills: PlatformSkillRecord[]
  revision: number
  busy: boolean
  onRunSkill: (slug: string) => Promise<void>
}) {
  const skillOptions = useMemo(() => resolveTrendSkillOptions(target, skills), [target, skills])

  const [selectedSkill, setSelectedSkill] = useState(skillOptions[0]?.slug || "inspect-vm-host")
  const [range, setRange] = useState<SkillTrendRange>("24h")
  const [data, setData] = useState<MonitorMetricSeriesResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  const activeSkill = skillOptions.some((o) => o.slug === selectedSkill)
    ? selectedSkill
    : (skillOptions[0]?.slug || "inspect-vm-host")

  useEffect(() => {
    const c = new AbortController()
    setLoading(true)
    setError("")
    fetchMonitorMetrics(
      session,
      { skill: activeSkill, target: `${target.cluster}/${target.instance}`, range },
      c.signal,
    )
      .then((res) => { if (!c.signal.aborted) setData(res) })
      .catch((e) => { if (!c.signal.aborted) setError((e as Error).message) })
      .finally(() => { if (!c.signal.aborted) setLoading(false) })
    return () => c.abort()
  }, [session, target.cluster, target.instance, activeSkill, range, revision])

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end gap-3 text-sm">
        <label>
          巡检 Skill
          <Select value={activeSkill} onChange={(e) => setSelectedSkill(e.target.value)}>
            {skillOptions.map((o) => {
              const cat = SKILL_CATEGORY_LABELS[o.category]
              const prefix = cat && o.category !== "inspection" ? `[${cat}] ` : ""
              return (
                <option key={o.slug} value={o.slug}>
                  {prefix}{o.label} ({o.slug})
                </option>
              )
            })}
          </Select>
        </label>
        <label>
          时间范围
          <Select value={range} onChange={(e) => setRange(e.target.value as SkillTrendRange)}>
            {SKILL_TREND_RANGES.map((r) => (
              <option key={r} value={r}>{r}</option>
            ))}
          </Select>
        </label>
        <Button
          type="button"
          size="sm"
          disabled={busy || target.status === "offline"}
          onClick={() => void onRunSkill(activeSkill)}
        >
          {busy ? "执行中…" : "执行当前 Skill"}
        </Button>
      </div>
      {error ? (
        <p role="alert" className="text-sm text-destructive">{error}</p>
      ) : loading && !data ? (
        <p role="status" className="text-sm text-muted-foreground">正在读取 Skill 巡检趋势…</p>
      ) : !data?.series?.length ? (
        <p className="text-sm text-muted-foreground">
          所选时间范围内尚无 <code className="font-mono">{activeSkill}</code> 的巡检指标样本，可点击左侧「执行当前 Skill」或上方「一键巡检采集」立即采样。
        </p>
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {data.series.map((s) => {
            const latest = s.points[s.points.length - 1]
            const hint = latest ? humanBytesHint(s.unit || "", latest.value) : ""
            return (
              <article key={s.key} className="min-w-0 rounded-lg border bg-card p-3">
                <div className="flex flex-wrap items-center justify-between gap-1.5">
                  <h4 className="text-sm font-semibold">
                    {s.label || s.key}{" "}
                    {s.unit && <small className="text-muted-foreground">{s.unit}</small>}
                  </h4>
                  <Badge variant="outline" className="font-mono text-[11px]">{s.key}</Badge>
                </div>
                {latest && (
                  <>
                    <p className="mt-1 font-mono text-sm">
                      {latest.value.toLocaleString(undefined, { maximumSignificantDigits: 7 })}
                      {s.unit ? ` ${s.unit}` : ""}
                      {hint && <span className="ml-1.5 text-xs text-muted-foreground">({hint})</span>}
                    </p>
                    <small className="text-muted-foreground">最近巡检 {stamp(latest.at_ms)}</small>
                  </>
                )}
                <Sparkline
                  points={s.points.map((p) => ({ at_ms: p.at_ms, value: p.value, status: "ok" }))}
                  fromMs={data.from_ms}
                  toMs={data.to_ms}
                  ariaLabel={`${s.label || s.key} 巡检趋势`}
                  sampleCount={s.points.length}
                />
              </article>
            )
          })}
        </div>
      )}
    </div>
  )
}

export function AgentMonitor({
  session,
  target,
  clusterId,
  initialFocus,
  onOpenInspection,
  onOpenSkillCenter,
}: {
  session: Session
  target: Target
  clusterId?: string
  initialFocus?: MonitorAssetFocus | null
  onOpenInspection?: () => void
  onOpenSkillCenter?: () => void
}) {
  const [subTab, setSubTab] = useState<MonitorSubTab>("series")
  const [kind, setKind] = useState(initialFocus?.type ?? "")
  const [selected, setSelected] = useState("")
  const [focusMetric, setFocusMetric] = useState(initialFocus?.metric ?? "")
  const [range, setRange] = useState<MonitorRange>("1h")
  const [revision, refresh] = useState(0)
  const [ns, setNs] = useState("")
  const [busyMode, setBusyMode] = useState<"" | "host" | "containers" | "apps" | "k8s_pods" | "inspect" | "single_skill">("")
  const [notice, setNotice] = useState("")
  const [missingSkillsHint, setMissingSkillsHint] = useState(false)
  const [skills, setSkills] = useState<PlatformSkillRecord[]>([])
  const [namespaces, setNamespaces] = useState<string[]>([])
  const nsDatalistId = useId()

  const demo = Boolean(session.demo)
  const busy = busyMode !== ""
  const route = {
    cluster: target.cluster,
    instance: target.instance,
    ...(clusterId ? { cluster_id: clusterId } : {}),
  }

  useEffect(() => {
    const c = new AbortController()
    fetchPlatformSkills(session, {}, c.signal)
      .then((res) => { if (!c.signal.aborted) setSkills(res.skills || []) })
      .catch(() => {})
    if (!demo && target.kubernetes_available !== false) {
      assetRequest<TargetManifestResponse>(
        "target-manifest",
        undefined,
        new URLSearchParams({ cluster: target.cluster, instance: target.instance }),
        c.signal,
      )
        .then((res) => {
          if (!c.signal.aborted) {
            setNamespaces(extractManifestNamespaces(res.manifest?.resources ?? []))
          }
        })
        .catch(() => {})
    }
    return () => c.abort()
  }, [session, demo, target.cluster, target.instance, target.kubernetes_available])

  const endpoint = "/api/assets/monitor"
  const listQuery = new URLSearchParams({ ...route, ...(kind ? { type: kind } : {}) })
  const catalog = useMonitorRead<{ assets: Asset[] }>(`${endpoint}?${listQuery}`, revision, demo)
  const assets = catalog.data?.assets

  useEffect(() => {
    if (!initialFocus) return
    setSubTab("series")
    setKind(initialFocus.type ?? "")
    setSelected("")
    setFocusMetric(initialFocus.metric ?? "")
  }, [initialFocus])

  useEffect(() => {
    if (!initialFocus || !assets?.length || selected) return
    const matched = matchMonitorAssetFocus(assets, initialFocus)
    if (matched) setSelected(monitorAssetKey(matched))
  }, [initialFocus, assets, selected])

  const asset = assets?.find((a) => monitorAssetKey(a) === selected) ?? assets?.[0]
  const detailUrl = asset ? `${endpoint}?${new URLSearchParams({ ...route, ...asset, range })}` : ""
  const detail = useMonitorRead<Detail>(detailUrl, revision, demo)

  async function collectByMode(mode: "host" | "containers" | "apps" | "k8s_pods") {
    setBusyMode(mode)
    setNotice("")
    try {
      const res = await request<{ accepted: number; runtime?: string }>(
        endpoint,
        undefined,
        { ...route, action: "collect", mode, ...(mode === "k8s_pods" ? { namespace: ns } : {}) },
        demo,
      )
      const label =
        mode === "host"
          ? "虚拟机/主机指标"
          : mode === "containers"
            ? `${res.runtime || "容器运行时"} 容器指标`
            : mode === "apps"
              ? "应用响应/连接/错误率指标"
              : `命名空间 ${ns} Pod 指标`
      setNotice(`已完成 ${label} 观测，写入 ${res.accepted} 个样本`)
      if (mode === "containers" && kind && kind !== "container") setKind("container")
      if (mode === "apps" && kind && kind !== "app") setKind("app")
      if (mode === "host" && kind && kind !== "machine" && kind !== "kernel") setKind("machine")
      refresh((x) => x + 1)
    } catch (e) {
      setNotice((e as Error).message)
    } finally {
      setBusyMode("")
    }
  }

  async function runTargetSkillInspect() {
    setBusyMode("inspect")
    setNotice("")
    setMissingSkillsHint(false)
    try {
      const targetSkills = resolveTargetInspectionSkills(target, skills)
      if (!targetSkills.length) {
        setMissingSkillsHint(true)
        setNotice("当前目标没有已启用的系统巡检 Skill，请先在「场景与扩展 → 插件中心 → Skill 能力包」启用。")
        return
      }
      const res = await triggerMonitorRun(session, {
        scope_mode: "targets",
        targets: [{ cluster: target.cluster, instance: target.instance }],
        skills: targetSkills,
      })
      setNotice(
        `一键巡检采集完成（#${res.run.id}，Skill: ${targetSkills.join("、")}）：${res.run.summary || `写入 ${res.run.metrics_written} 条指标`}`,
      )
      refresh((x) => x + 1)
    } catch (e) {
      setNotice((e as Error).message)
    } finally {
      setBusyMode("")
    }
  }

  async function runSingleSkill(slug: string) {
    setBusyMode("single_skill")
    setNotice("")
    try {
      const res = await triggerMonitorRun(session, {
        scope_mode: "targets",
        targets: [{ cluster: target.cluster, instance: target.instance }],
        skills: [slug],
      })
      setNotice(
        `Skill ${slug} 执行完成（#${res.run.id}）：${res.run.summary || `写入 ${res.run.metrics_written} 条指标`}`,
      )
      refresh((x) => x + 1)
    } catch (e) {
      setNotice((e as Error).message)
    } finally {
      setBusyMode("")
    }
  }

  async function collect(e: FormEvent) {
    e.preventDefault()
    await collectByMode("k8s_pods")
  }

  async function save(metric: string, device_id: string | undefined, rule: Rule) {
    await request(endpoint, undefined, { ...route, ...asset, action: "rule", metric, device_id, ...rule }, demo)
    refresh((x) => x + 1)
    setNotice("规则已保存，从后续样本开始判定。")
  }

  async function removeRule(metric: string, device_id: string | undefined) {
    await request(endpoint, undefined, { ...route, ...asset, action: "delete_rule", metric, device_id }, demo)
    refresh((x) => x + 1)
    setNotice("告警规则已删除。")
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <SegmentedControl
          ariaLabel="单目标监控视图"
          value={subTab}
          options={[
            { value: "series", label: "资产时序与告警" },
            { value: "skill", label: "Skill 巡检趋势" },
          ]}
          onChange={setSubTab}
        />
        <div className="flex flex-wrap items-center gap-2">
          {onOpenInspection && (
            <Button type="button" size="sm" variant="ghost" onClick={onOpenInspection}>
              前往智能巡检
            </Button>
          )}
          <Button type="button" size="sm" onClick={() => refresh((x) => x + 1)}>
            刷新观测
          </Button>
        </div>
      </div>

      <div className="card-surface space-y-2.5 p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="primary"
              size="sm"
              disabled={busy || target.status === "offline"}
              onClick={() => void runTargetSkillInspect()}
            >
              {busyMode === "inspect" ? "巡检采集中…" : "一键巡检采集"}
            </Button>
            <span className="text-xs text-muted-foreground">
              按目标能力执行已启用的系统巡检 Skill（主机 / 容器 / 站点{target.kubernetes_available !== false ? " / K8s 节点与 Pod" : ""}），结果自动桥接至时序与告警。
            </span>
          </div>
        </div>

        <details className="border-t border-border/60 pt-2 text-sm">
          <summary className="cursor-pointer select-none text-xs text-muted-foreground hover:text-foreground">
            按需原生采样（指定命名空间 K8s Pod / 即时主机与容器采样，自动同步至全网资源总览）
          </summary>
          <div className="mt-2.5 flex flex-wrap items-end justify-between gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                size="sm"
                disabled={busy || target.status === "offline"}
                onClick={() => void collectByMode("host")}
              >
                {busyMode === "host" ? "采集中…" : "采集虚拟机 / 主机"}
              </Button>
              <Button
                type="button"
                size="sm"
                disabled={busy || target.status === "offline"}
                onClick={() => void collectByMode("containers")}
              >
                {busyMode === "containers" ? "采集中…" : "采集运行中容器"}
              </Button>
              <Button
                type="button"
                size="sm"
                disabled={busy || target.status === "offline"}
                onClick={() => void collectByMode("apps")}
              >
                {busyMode === "apps" ? "采集中…" : "采集应用指标"}
              </Button>
            </div>

            {target.kubernetes_available !== false && (
              <form onSubmit={collect} className="flex flex-wrap items-end gap-2">
                <label className="text-xs text-muted-foreground">
                  K8s 命名空间
                  <Input
                    scale="sm"
                    required
                    list={namespaces.length ? nsDatalistId : undefined}
                    value={ns}
                    maxLength={63}
                    placeholder={namespaces[0] ? `例如 ${namespaces[0]}` : "例如 apps"}
                    pattern="[a-z0-9]([a-z0-9\-]*[a-z0-9])?"
                    onChange={(e) => setNs(e.target.value)}
                    className="mt-1 w-44 font-mono"
                  />
                  {namespaces.length > 0 && (
                    <datalist id={nsDatalistId}>
                      {namespaces.map((name) => (
                        <option key={name} value={name} />
                      ))}
                    </datalist>
                  )}
                </label>
                <Button type="submit" size="sm" disabled={busy || target.status === "offline"}>
                  {busyMode === "k8s_pods" ? "采集中…" : "采集 K8s Pod"}
                </Button>
              </form>
            )}
          </div>
        </details>
      </div>

      {notice && (
        <div role="status" className="flex flex-wrap items-center gap-2 text-sm">
          <span>{notice}</span>
          {missingSkillsHint && onOpenSkillCenter && (
            <Button type="button" size="sm" variant="ghost" onClick={onOpenSkillCenter}>
              前往 Skill 能力包
            </Button>
          )}
        </div>
      )}

      {subTab === "skill" ? (
        <SkillTrendsView
          session={session}
          target={target}
          skills={skills}
          revision={revision}
          busy={busy}
          onRunSkill={runSingleSkill}
        />
      ) : (
        <>
          <div className="flex flex-wrap items-end gap-2 text-sm">
            <label>
              资产类型
              <Select
                value={kind}
                onChange={(e) => {
                  setKind(e.target.value)
                  setSelected("")
                }}
              >
                <option value="">全部类型</option>
                {Object.entries(MONITOR_KINDS).map(([k, l]) => (
                  <option key={k} value={k}>
                    {l}
                  </option>
                ))}
              </Select>
            </label>
            {asset && (
              <label>
                资产与来源
                <Select value={monitorAssetKey(asset)} onChange={(e) => setSelected(e.target.value)}>
                  {assets?.map((a) => (
                    <option value={monitorAssetKey(a)} key={monitorAssetKey(a)}>
                      {MONITOR_KINDS[a.type] ?? a.type} · {a.namespace && a.namespace + "/"}
                      {a.asset_id} · {monitorSourceName(a.origin)}
                    </option>
                  ))}
                </Select>
              </label>
            )}
            <label>
              时间范围
              <Select value={range} onChange={(e) => setRange(e.target.value as MonitorRange)}>
                {(Object.keys(MONITOR_RANGE_MS) as MonitorRange[]).map((r) => (
                  <option key={r} value={r}>{r}</option>
                ))}
              </Select>
            </label>
          </div>
          {catalog.error || detail.error ? (
            <p role="alert">{catalog.error || detail.error}</p>
          ) : !assets ? (
            <p role="status">正在读取授权来源…</p>
          ) : !asset ? (
            <p className="text-sm text-muted-foreground">
              当前来源和类型尚无监控样本。若目标 Agent 在线，可点击上方「一键巡检采集」或展开「按需原生采样」即时采集，也可切换至「Skill 巡检趋势」查看历史巡检指标。
            </p>
          ) : !detail.data ? (
            <p role="status">正在读取时间序列…</p>
          ) : (
            <>
              <p className="text-xs text-muted-foreground">
                {monitorSourceName(asset.origin)} · <span className="font-mono">{target.cluster}/{target.instance}</span> ·{" "}
                {asset.origin === "exporter" ? "采集器观测时间" : "Agent 隧道 / 巡检采样时间"} · 缺样或间隔超过 5 分钟时曲线断开
              </p>
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                {(() => {
                  const memVal = detail.data!.series.find((s) => s.metric === "memory" && s.latest.status === "ok")?.latest.value
                  const limVal = detail.data!.series.find((s) => s.metric === "memory_limit" && s.latest.status === "ok")?.latest.value
                  const memRatio =
                    asset.type === "container" && memVal != null && limVal != null && limVal > 0
                      ? `占上限 ${((memVal / limVal) * 100).toFixed(1)}%`
                      : undefined
                  return visibleMetricsForAsset(asset, detail.data!.series.map((s) => s.metric)).flatMap((metric) => {
                    const matches = detail.data!.series.filter((series) => series.metric === metric)
                    const cards: Array<Series | undefined> = matches.length ? matches : [undefined]
                    return cards.map((series) => (
                      <MetricCard
                        key={JSON.stringify([monitorAssetKey(asset), metric, series?.device_id])}
                        metric={metric}
                        series={series}
                        detail={detail.data!}
                        extraBadge={metric === "memory" ? memRatio : undefined}
                        highlighted={Boolean(focusMetric) && metric === focusMetric}
                        admin={sessionIsAdmin(session)}
                        save={save}
                        removeRule={removeRule}
                      />
                    ))
                  })
                })()}
              </div>
            </>
          )}
        </>
      )}
    </div>
  )
}
