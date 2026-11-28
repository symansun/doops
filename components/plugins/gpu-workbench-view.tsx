"use client"

import { Cpu, Flame, HardDrive, ShieldAlert, Sparkles, Zap } from "lucide-react"
import {
  formatMibCompact,
  parseGpuArtifact,
  type GpuDeviceRecord,
} from "@/lib/plugin-workbench"
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Grid,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui"

function barColor(pct: number, warn: number, crit: number): string {
  if (pct >= crit) return "bg-destructive"
  if (pct >= warn) return "bg-warning"
  return "bg-primary"
}

function textTone(pct: number, warn: number, crit: number): string {
  if (pct >= crit) return "text-destructive"
  if (pct >= warn) return "text-warning"
  return "text-foreground"
}

function GpuMetricBar({
  label,
  pct,
  detail,
  warn,
  crit,
}: {
  label: string
  pct: number | null
  detail?: string
  warn: number
  crit: number
}) {
  if (pct == null) {
    return (
      <div className="space-y-1">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>{label}</span>
          <span className="font-mono">—</span>
        </div>
        <div className="h-2 rounded-full bg-muted" />
      </div>
    )
  }
  const clamped = Math.max(0, Math.min(100, pct))
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className={`font-mono tabular-nums font-medium ${textTone(clamped, warn, crit)}`}>
          {detail ? `${detail} · ` : ""}
          {clamped.toFixed(1)}%
        </span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={`h-full rounded-full transition-all ${barColor(clamped, warn, crit)}`}
          style={{ width: `${Math.max(2, clamped)}%` }}
        />
      </div>
    </div>
  )
}

function GpuDeviceCard({
  gpu,
  warnTempC,
  warnMemPct,
  onInspectGpu,
  onPlanThermal,
}: {
  gpu: GpuDeviceRecord
  warnTempC: number
  warnMemPct: number
  onInspectGpu?: (contextText: string) => void
  onPlanThermal?: (contextText: string) => void
}) {
  const critMemPct = Math.max(warnMemPct + 2, 98)
  const critTempC = Math.min(112, warnTempC + 7)
  const healthBadge =
    gpu.health === "critical"
      ? { label: "严重", variant: "danger" as const }
      : gpu.health === "warning"
        ? { label: "告警", variant: "warning" as const }
        : { label: "正常", variant: "success" as const }

  const vramDetail =
    gpu.memUsedMib != null && gpu.memTotalMib != null
      ? `${formatMibCompact(gpu.memUsedMib)} / ${formatMibCompact(gpu.memTotalMib)}`
      : undefined
  const ctxSummary = `GPU #${gpu.index} (${gpu.name}) · 算力 ${gpu.utilPercent ?? "—"}% · 显存 ${gpu.memPercent ?? "—"}% · 温度 ${gpu.tempC ?? "—"}°C`

  return (
    <Card className={gpu.health === "critical" ? "border-destructive/45" : gpu.health === "warning" ? "border-warning/45" : ""}>
      <CardContent className="space-y-3 p-4">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-1.5">
              <Badge variant="outline" className="font-mono">GPU {gpu.index}</Badge>
              <span className="truncate text-sm font-semibold text-foreground">{gpu.name}</span>
              <Badge variant={healthBadge.variant}>{healthBadge.label}</Badge>
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-3 font-mono text-2xs text-muted-foreground">
              <span>
                温度{" "}
                <strong
                  className={
                    gpu.tempC != null && gpu.tempC >= critTempC
                      ? "text-destructive"
                      : gpu.tempC != null && gpu.tempC >= warnTempC
                        ? "text-warning"
                        : "text-foreground"
                  }
                >
                  {gpu.tempC != null ? `${gpu.tempC.toFixed(0)}°C` : "—"}
                </strong>
              </span>
              {gpu.powerW != null && (
                <span>
                  功耗 <strong className="text-foreground">{gpu.powerW.toFixed(0)}W</strong>
                </span>
              )}
              <span>
                ECC 未纠正{" "}
                <strong className={gpu.eccUncorrected > 0 ? "text-destructive" : "text-success"}>
                  {gpu.eccUncorrected}
                </strong>
              </span>
            </div>
          </div>
          {(onInspectGpu || onPlanThermal) && (
            <div className="flex shrink-0 items-center gap-1">
              {onInspectGpu && (
                <Button size="sm" variant="ghost" onClick={() => onInspectGpu(ctxSummary)}>
                  查进程
                </Button>
              )}
              {onPlanThermal && gpu.health !== "ok" && (
                <Button size="sm" variant="ghost" onClick={() => onPlanThermal(ctxSummary)}>
                  降载方案
                </Button>
              )}
            </div>
          )}
        </div>

        <div className="space-y-2.5">
          <GpuMetricBar label="算力利用" pct={gpu.utilPercent} warn={95} crit={99} />
          <GpuMetricBar label="显存占用" pct={gpu.memPercent} detail={vramDetail} warn={warnMemPct} crit={critMemPct} />
        </div>
      </CardContent>
    </Card>
  )
}

export function GpuGovernanceWorkbench({
  artifactJson,
  targetLabel,
  hasActions,
  onSelectResource,
}: {
  artifactJson: unknown
  targetLabel: string
  hasActions: boolean
  onSelectResource?: (contextText: string, actionId?: string) => void
}) {
  const data = parseGpuArtifact(artifactJson)
  if (!data) return null

  if (data.totalGpus === 0) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center gap-2 py-10 text-center">
          <span className="flex size-11 items-center justify-center rounded-xl bg-muted text-muted-foreground">
            <Cpu className="size-5" aria-hidden="true" />
          </span>
          <p className="text-sm font-medium text-foreground">
            当前节点 <code className="font-mono">{targetLabel}</code> 未检测到 NVIDIA GPU
          </p>
          <p className="max-w-md text-xs text-muted-foreground">
            {data.summary || "该节点未挂载 GPU 设备或未安装 NVIDIA 驱动；可在右上角切换至 GPU 算力节点查看实时卡态与占卡进程。"}
          </p>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <Grid cols={2} lgCols={4} gap={3}>
        <Card>
          <CardContent className="flex items-center gap-3 p-3.5">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
              <Cpu className="size-4" aria-hidden="true" />
            </span>
            <div className="min-w-0">
              <div className="text-xs text-muted-foreground">物理 GPU</div>
              <div className="font-mono text-lg font-semibold tabular-nums text-foreground">
                {data.totalGpus} 张
              </div>
              <div className="truncate font-mono text-2xs text-muted-foreground">
                总显存 {formatMibCompact(data.totalMemMib)}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="flex items-center gap-3 p-3.5">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
              <Zap className="size-4" aria-hidden="true" />
            </span>
            <div className="min-w-0">
              <div className="text-xs text-muted-foreground">平均算力利用</div>
              <div className="font-mono text-lg font-semibold tabular-nums text-foreground">
                {data.avgUtilPercent != null ? `${data.avgUtilPercent.toFixed(1)}%` : "—"}
              </div>
              <div className="truncate font-mono text-2xs text-muted-foreground">
                活跃占卡进程 {data.processes.length} 个
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="flex items-center gap-3 p-3.5">
            <span
              className={`flex size-9 shrink-0 items-center justify-center rounded-lg ${
                (data.maxMemPercent ?? 0) >= data.warnMemPct
                  ? "bg-warning/20 text-warning"
                  : "bg-primary/15 text-primary"
              }`}
            >
              <HardDrive className="size-4" aria-hidden="true" />
            </span>
            <div className="min-w-0">
              <div className="text-xs text-muted-foreground">最高显存占比</div>
              <div className="font-mono text-lg font-semibold tabular-nums text-foreground">
                {data.maxMemPercent != null ? `${data.maxMemPercent.toFixed(1)}%` : "—"}
              </div>
              <div className="truncate font-mono text-2xs text-muted-foreground">
                已用 {formatMibCompact(data.totalUsedMib)}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="flex items-center gap-3 p-3.5">
            <span
              className={`flex size-9 shrink-0 items-center justify-center rounded-lg ${
                data.eccTotal > 0 || (data.maxTempC ?? 0) >= data.warnTempC
                  ? "bg-destructive/15 text-destructive"
                  : "bg-success/15 text-success"
              }`}
            >
              {data.eccTotal > 0 ? <ShieldAlert className="size-4" aria-hidden="true" /> : <Flame className="size-4" aria-hidden="true" />}
            </span>
            <div className="min-w-0">
              <div className="text-xs text-muted-foreground">最高温度 · ECC</div>
              <div className="font-mono text-lg font-semibold tabular-nums text-foreground">
                {data.maxTempC != null ? `${data.maxTempC.toFixed(0)}°C` : "—"}
              </div>
              <div className={`truncate font-mono text-2xs ${data.eccTotal > 0 ? "text-destructive" : "text-success"}`}>
                {data.eccTotal > 0 ? `ECC 错误 ${data.eccTotal} 次` : "ECC 硬件正常"}
              </div>
            </div>
          </CardContent>
        </Card>
      </Grid>

      <section aria-label="单卡实时算力与显存" className="space-y-2.5">
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold text-foreground">GPU 单卡实时状态</h2>
          <span className="font-mono text-xs text-muted-foreground">{targetLabel}</span>
        </div>
        <Grid cols={1} lgCols={2} gap={3}>
          {data.gpus.map((gpu) => (
            <GpuDeviceCard
              key={`${gpu.index}-${gpu.uuid}`}
              gpu={gpu}
              warnTempC={data.warnTempC}
              warnMemPct={data.warnMemPct}
              onInspectGpu={hasActions && onSelectResource ? (ctx) => onSelectResource(ctx, "gpu-zombie-check") : undefined}
              onPlanThermal={hasActions && onSelectResource ? (ctx) => onSelectResource(ctx, "gpu-thermal-plan") : undefined}
            />
          ))}
        </Grid>
      </section>

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 pb-2">
          <div className="flex items-center gap-2">
            <CardTitle className="text-sm">活跃占卡进程</CardTitle>
            <Badge variant="outline" className="font-mono">{data.processes.length}</Badge>
            {data.zombieCount > 0 && (
              <Badge variant="warning">疑似空闲占卡 {data.zombieCount}</Badge>
            )}
          </div>
          <span className="text-2xs text-muted-foreground">
            显存占用高但算力利用率极低时自动标出疑似僵尸任务
          </span>
        </CardHeader>
        <CardContent>
          {data.processes.length === 0 ? (
            <p className="py-4 text-center text-xs text-muted-foreground">当前没有活跃的 GPU 计算进程。</p>
          ) : (
            <Table wrapperClassName="rounded-md border">
              <TableHeader className="bg-muted/30">
                <TableRow>
                  <TableHead className="w-24">GPU</TableHead>
                  <TableHead className="w-24">PID</TableHead>
                  <TableHead>进程</TableHead>
                  <TableHead className="w-28">显存占用</TableHead>
                  <TableHead className="w-32">诊断</TableHead>
                  {hasActions && onSelectResource && <TableHead className="w-24 text-right">操作</TableHead>}
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.processes.map((proc, idx) => (
                  <TableRow key={`${proc.gpuUuid}-${proc.pid}-${idx}`}>
                    <TableCell className="font-mono text-xs">GPU {proc.gpuIndex}</TableCell>
                    <TableCell className="font-mono text-xs text-foreground">{proc.pid}</TableCell>
                    <TableCell className="font-mono text-xs text-foreground">{proc.process}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{formatMibCompact(proc.usedMib)}</TableCell>
                    <TableCell>
                      {proc.zombieSuspect ? (
                        <Badge variant="warning">疑似空闲占卡</Badge>
                      ) : (
                        <Badge variant="success">计算活跃</Badge>
                      )}
                    </TableCell>
                    {hasActions && onSelectResource && (
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() =>
                            onSelectResource(
                              `GPU #${proc.gpuIndex} · PID ${proc.pid} (${proc.process}) · 显存 ${formatMibCompact(proc.usedMib)}`,
                              "gpu-zombie-check",
                            )
                          }
                        >
                          <Sparkles className="size-3" aria-hidden="true" />
                          排查
                        </Button>
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
