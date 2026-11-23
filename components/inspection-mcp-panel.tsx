"use client"

import { useEffect, useState, type FormEvent } from "react"
import type { Session } from "@/lib/client"
import { triggerMonitorRun } from "@/lib/monitor-engine"
import {
  getInspectionMcpServices,
  mapMcpServicesToStrategies,
  recordInspectionMcpRun,
  registerCustomInspectionMcp,
  removeCustomInspectionMcp,
  setAllInspectionMcpEnabled,
  setInspectionMcpEnabled,
  subscribeInspectionMcpServices,
  type InspectionMcpService,
} from "@/lib/inspection-mcp-store"
import { RocketIcon, SparkIcon } from "./icons"
import { Badge, Button, Card, CardContent, Inline, Input, Stack, StatusBanner } from "./ui"
import { TiltCard3D } from "./viz-3d"

function mcpCapabilityBadge(svc: InspectionMcpService, isDemo: boolean) {
  if (!svc.builtin) return <Badge variant="outline">自定义扩展 MCP</Badge>
  if (svc.id === "mcp-vm-inspect" || svc.id === "mcp-k8s-inspect") {
    return <Badge variant="success">DoOps 原生直采</Badge>
  }
  if (svc.id === "mcp-database-inspect" || svc.id === "mcp-vuln-web-inspect") {
    return <Badge variant="primary">{isDemo ? "DoOps + 扩展演示" : "DoOps 基础探活"}</Badge>
  }
  return <Badge variant="warning">{isDemo ? "演示 / 外挂安全 MCP" : "需接入外部安全 MCP"}</Badge>
}

function mcpStatusBadge(status: InspectionMcpService["lastStatus"], enabled: boolean) {
  if (!enabled) return <Badge variant="outline">已关闭</Badge>
  if (status === "critical") return <Badge variant="danger">严重告警</Badge>
  if (status === "warning") return <Badge variant="warning">存在告警</Badge>
  return <Badge variant="success">在线健康</Badge>
}

export function InspectionMcpPanel({
  session,
  onOpenRunDetail,
}: {
  session: Session
  onOpenRunDetail?: (runId: string) => void
}) {
  const [services, setServices] = useState<InspectionMcpService[]>(() => getInspectionMcpServices())
  const [busyId, setBusyId] = useState<string>("")
  const [notice, setNotice] = useState("")
  const [lastRunId, setLastRunId] = useState("")
  const [error, setError] = useState("")
  const [addOpen, setAddOpen] = useState(false)
  const [newId, setNewId] = useState("")
  const [newName, setNewName] = useState("")
  const [newCategory, setNewCategory] = useState("")
  const [newEndpoint, setNewEndpoint] = useState("")
  const [newDesc, setNewDesc] = useState("")
  const [newChecks, setNewChecks] = useState("")

  useEffect(() => {
    setServices(getInspectionMcpServices())
    return subscribeInspectionMcpServices(() => {
      setServices([...getInspectionMcpServices()])
    })
  }, [])

  const enabledList = services.filter((s) => s.enabled)

  async function runSelectedMcps(targetList: InspectionMcpService[], title: string, key: string) {
    if (busyId || targetList.length === 0) return
    setBusyId(key)
    setError("")
    setNotice("")
    setLastRunId("")
    try {
      const strategies = mapMcpServicesToStrategies(targetList)
      const mcpIds = targetList.map((s) => s.id)
      const res = await triggerMonitorRun(session, {
        scope_mode: "all_online",
        strategies,
        mcp_services: mcpIds,
        mcp_title: title,
      })
      const statusMap: Record<string, "pass" | "warning" | "critical"> = {}
      for (const s of targetList) {
        statusMap[s.id] = res.run.failed_checks > 0 ? "critical" : res.run.warning_checks > 0 ? "warning" : "pass"
      }
      recordInspectionMcpRun(mcpIds, statusMap)
      setLastRunId(res.run.id)
      setNotice(`已完成「${title}」（#${res.run.id} · 耗时 ${(res.run.duration_ms / 1000).toFixed(1)}s）：${res.run.summary}`)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusyId("")
    }
  }

  function handleRegisterCustom(e: FormEvent) {
    e.preventDefault()
    if (!newName.trim()) {
      setError("请填写巡检 MCP 服务名称")
      return
    }
    setError("")
    const checks = newChecks
      .split(/[,，、\n]/)
      .map((x) => x.trim())
      .filter(Boolean)
    registerCustomInspectionMcp({
      id: newId || `mcp-${newCategory || "custom"}-inspect`,
      name: newName,
      category: newCategory || "扩展安全巡检",
      endpoint: newEndpoint,
      description: newDesc,
      checks,
    })
    setAddOpen(false)
    setNewId("")
    setNewName("")
    setNewCategory("")
    setNewEndpoint("")
    setNewDesc("")
    setNewChecks("")
    setNotice("已注册新的安全巡检 MCP 服务并接入智能体平台，可立即开启并单独发起巡检。")
  }

  return (
    <Stack gap={4}>
      <Card>
        <CardContent className="space-y-3 p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0 space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <SparkIcon width={16} height={16} className="text-primary" />
                <h2 className="text-base font-semibold text-foreground">安全巡检 MCP 智能服务开关与调度</h2>
                <Badge variant={enabledList.length >= 5 ? "success" : "warning"}>
                  已启用 {enabledList.length} / {services.length} 个 MCP 服务
                </Badge>
                <Badge variant="primary">已接入智能体平台</Badge>
              </div>
              <p className="text-xs text-muted-foreground">
                每类巡检（虚拟机、K8s、边界防火墙、上网行为管理、违规翻墙检测、数据库、应用漏洞与 Web/API）均以独立 MCP 智能服务形式接入。可随时开启或关闭单个服务，支持单项巡检或按当前已启用清单发起联合巡检。
              </p>
            </div>
            <Inline gap={2} className="flex-wrap">
              <Button
                size="sm"
                variant="primary"
                disabled={Boolean(busyId) || enabledList.length === 0}
                onClick={() =>
                  void runSelectedMcps(
                    enabledList,
                    `已启用 ${enabledList.length} 个安全巡检 MCP 联合巡检`,
                    "__all__",
                  )
                }
              >
                <RocketIcon width={14} height={14} />
                {busyId === "__all__" ? "联合巡检执行中…" : `按已启用的 ${enabledList.length} 个 MCP 发起巡检`}
              </Button>
              <Button size="sm" onClick={() => setAllInspectionMcpEnabled(true)}>
                全部开启
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setAllInspectionMcpEnabled(false)}>
                全部关闭
              </Button>
              <Button size="sm" onClick={() => setAddOpen((v) => !v)}>
                {addOpen ? "取消接入" : "+ 接入自定义巡检 MCP"}
              </Button>
            </Inline>
          </div>

          {error && <StatusBanner tone="error">{error}</StatusBanner>}
          {!session.demo && (
            <StatusBanner tone="info">
              当前连接真实 Gateway：DoOps 运维 Agent 原生执行虚机/物理机/SSH、K8s、容器、网站 HTTP/TLS 与数据库 TCP 探活策略；边界防火墙、上网行为管理与出口翻墙检测属于外部安全设备域，需通过独立安全 MCP 端点接入。
            </StatusBanner>
          )}
          {notice && (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-primary/30 bg-primary/5 px-3 py-2 text-xs text-foreground">
              <span>{notice}</span>
              {lastRunId && onOpenRunDetail && (
                <Button size="sm" variant="primary" onClick={() => onOpenRunDetail(lastRunId)}>
                  查看巡检报告 #{lastRunId}
                </Button>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      {addOpen && (
        <Card>
          <CardContent className="p-4">
            <form onSubmit={handleRegisterCustom} className="space-y-3">
              <h3 className="text-sm font-semibold text-foreground">注册新的安全巡检 MCP 服务</h3>
              <div className="grid gap-3 md:grid-cols-3">
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">服务名称 *</span>
                  <Input scale="sm" placeholder="例如：堡垒机安全巡检 MCP" value={newName} onChange={(e) => setNewName(e.target.value)} required />
                </label>
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">巡检分类</span>
                  <Input scale="sm" placeholder="例如：堡垒机 / WAF / 交换机" value={newCategory} onChange={(e) => setNewCategory(e.target.value)} />
                </label>
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">MCP 服务标识</span>
                  <Input scale="sm" placeholder="例如：mcp-bastion-inspect" value={newId} onChange={(e) => setNewId(e.target.value)} />
                </label>
              </div>
              <div className="grid gap-3 md:grid-cols-2">
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">MCP 服务端点</span>
                  <Input scale="sm" placeholder="mcp://sec-gateway/bastion-inspect" value={newEndpoint} onChange={(e) => setNewEndpoint(e.target.value)} />
                </label>
                <label className="space-y-1 text-xs">
                  <span className="text-muted-foreground">核心巡检项（逗号分隔）</span>
                  <Input scale="sm" placeholder="特权账号审计, 录像留存合规, 异常会话检测" value={newChecks} onChange={(e) => setNewChecks(e.target.value)} />
                </label>
              </div>
              <label className="block space-y-1 text-xs">
                <span className="text-muted-foreground">能力说明</span>
                <Input scale="sm" placeholder="描述该 MCP 巡检服务的检查范围与联动目标" value={newDesc} onChange={(e) => setNewDesc(e.target.value)} />
              </label>
              <Inline gap={2}>
                <Button size="sm" type="submit" variant="primary">确认接入并启用</Button>
                <Button size="sm" type="button" variant="ghost" onClick={() => setAddOpen(false)}>取消</Button>
              </Inline>
            </form>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
        {services.map((svc) => (
          <TiltCard3D
            key={svc.id}
            maxTilt={5}
            glowTone={
              !svc.enabled
                ? "primary"
                : svc.lastStatus === "critical"
                  ? "danger"
                  : svc.lastStatus === "warning"
                    ? "warning"
                    : "success"
            }
            className="h-full rounded-xl"
          >
            <Card className={`h-full ${svc.enabled ? "border-primary/25" : "opacity-70"}`}>
              <CardContent className="flex h-full flex-col justify-between gap-3 p-4">
              <div className="space-y-2.5">
                <div className="flex items-start justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <Badge variant="outline">{svc.category}</Badge>
                    {mcpCapabilityBadge(svc, Boolean(session.demo))}
                    {mcpStatusBadge(svc.lastStatus, svc.enabled)}
                  </div>
                  <button
                    type="button"
                    role="switch"
                    aria-checked={svc.enabled}
                    aria-label={`${svc.enabled ? "关闭" : "开启"} ${svc.name}`}
                    onClick={() => setInspectionMcpEnabled(svc.id, !svc.enabled)}
                    className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-2xs font-medium transition ${
                      svc.enabled
                        ? "border-primary bg-primary text-primary-foreground"
                        : "border-border bg-muted text-muted-foreground"
                    }`}
                  >
                    <span className={`h-2 w-2 rounded-full ${svc.enabled ? "bg-primary-foreground" : "bg-muted-foreground/60"}`} />
                    {svc.enabled ? "已开启" : "已关闭"}
                  </button>
                </div>

                <div>
                  <h3 className="text-sm font-semibold text-foreground">{svc.name}</h3>
                  <div className="mt-0.5 font-mono text-2xs text-primary">{svc.id}</div>
                  <div className="truncate font-mono text-2xs text-muted-foreground" title={svc.endpoint}>
                    {svc.endpoint}
                  </div>
                </div>

                <p className="text-xs leading-relaxed text-muted-foreground">{svc.description}</p>

                <div className="space-y-1">
                  <div className="text-2xs font-medium text-muted-foreground">覆盖检查项</div>
                  <div className="flex flex-wrap gap-1">
                    {svc.checks.map((c) => (
                      <span key={c} className="rounded border bg-background/70 px-1.5 py-0.5 text-2xs text-foreground">
                        {c}
                      </span>
                    ))}
                  </div>
                </div>

                <div className="space-y-1">
                  <div className="text-2xs font-medium text-muted-foreground">已注册 MCP 工具</div>
                  <div className="flex flex-wrap gap-1">
                    {svc.tools.map((t) => (
                      <code key={t} className="rounded bg-muted px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
                        {t}
                      </code>
                    ))}
                  </div>
                </div>
              </div>

              <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-2.5">
                <span className="font-mono text-2xs text-muted-foreground">
                  24h {svc.calls24h} 次 · {svc.avgLatencyMs}ms
                </span>
                <Inline gap={1}>
                  {!svc.builtin && (
                    <Button size="sm" variant="ghost" onClick={() => removeCustomInspectionMcp(svc.id)}>
                      移除
                    </Button>
                  )}
                  <Button
                    size="sm"
                    variant={svc.enabled ? "primary" : "default"}
                    disabled={!svc.enabled || Boolean(busyId)}
                    onClick={() => void runSelectedMcps([svc], `${svc.name} 单项巡检`, svc.id)}
                  >
                    {busyId === svc.id ? "巡检中…" : "单独发起巡检"}
                  </Button>
                </Inline>
              </div>
            </CardContent>
          </Card>
          </TiltCard3D>
        ))}
      </div>
    </Stack>
  )
}
