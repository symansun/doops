"use client"

import {
  useEffect,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react"
import { Badge, Button, Inline } from "./ui"

export interface ArchNode3D {
  id: string
  layer: "business" | "app" | "platform" | "infra"
  title: string
  subtitle: string
  metricLine: string
  status: "ok" | "warning" | "critical"
  cluster?: string
  instance?: string
  assetId?: string
  impactNote: string
}

export interface CoreNode3D {
  id: string
  label: string
  status: "ok" | "warning" | "critical"
  lat: number
  lon: number
  shell?: number
}

export interface ResourceBar3DItem {
  id: string
  label: string
  sublabel: string
  cpu: number
  memory: number
  disk: number
  gpu?: number | null
  status: "ok" | "warning" | "critical"
}

/**
 * Interactive 3D perspective-tilt card with subtle cursor-tracking specular sheen.
 */
export function TiltCard3D({
  children,
  className = "",
  maxTilt = 6,
  glowTone = "primary",
}: {
  children: ReactNode
  className?: string
  maxTilt?: number
  glowTone?: "primary" | "success" | "warning" | "danger"
}) {
  const ref = useRef<HTMLDivElement | null>(null)
  const [style, setStyle] = useState<{ rx: number; ry: number; gx: number; gy: number; active: boolean }>({
    rx: 0,
    ry: 0,
    gx: 50,
    gy: 50,
    active: false,
  })

  function handleMove(e: ReactMouseEvent<HTMLDivElement>) {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    if (rect.width <= 0 || rect.height <= 0) return
    const px = (e.clientX - rect.left) / rect.width
    const py = (e.clientY - rect.top) / rect.height
    const ry = (px - 0.5) * maxTilt * 2
    const rx = (0.5 - py) * maxTilt * 2
    setStyle({ rx, ry, gx: px * 100, gy: py * 100, active: true })
  }

  function handleLeave() {
    setStyle({ rx: 0, ry: 0, gx: 50, gy: 50, active: false })
  }

  const glowColor =
    glowTone === "success"
      ? "rgba(52, 211, 153, 0.22)"
      : glowTone === "warning"
        ? "rgba(251, 191, 36, 0.22)"
        : glowTone === "danger"
          ? "rgba(248, 113, 113, 0.24)"
          : "rgba(56, 189, 248, 0.22)"

  return (
    <div
      ref={ref}
      onMouseMove={handleMove}
      onMouseLeave={handleLeave}
      className={`relative transition-transform duration-200 ease-out will-change-transform ${className}`}
      style={{
        transform: style.active
          ? `perspective(900px) rotateX(${style.rx.toFixed(2)}deg) rotateY(${style.ry.toFixed(2)}deg) translateZ(6px)`
          : "perspective(900px) rotateX(0deg) rotateY(0deg) translateZ(0px)",
        transformStyle: "preserve-3d",
      }}
    >
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 rounded-[inherit] transition-opacity duration-200"
        style={{
          opacity: style.active ? 1 : 0,
          background: `radial-gradient(240px circle at ${style.gx}% ${style.gy}%, ${glowColor}, transparent 72%)`,
        }}
      />
      {children}
    </div>
  )
}

const DEFAULT_CORE_NODES: CoreNode3D[] = [
  { id: "llm", label: "浙音大模型", status: "ok", lat: 5, lon: 0, shell: 1.04 },
  { id: "portal", label: "教务门户", status: "ok", lat: 34, lon: 35, shell: 1 },
  { id: "sso", label: "统一 SSO", status: "ok", lat: 24, lon: 112, shell: 1 },
  { id: "k8s", label: "K8s 集群", status: "warning", lat: -10, lon: 68, shell: 1.03 },
  { id: "gpu", label: "双卡 A100", status: "ok", lat: 46, lon: -42, shell: 1.04 },
  { id: "pg", label: "PG 主库", status: "ok", lat: -24, lon: -25, shell: 1 },
  { id: "fw", label: "边界防火墙", status: "warning", lat: 14, lon: -108, shell: 1.02 },
  { id: "ac", label: "上网行为", status: "ok", lat: -36, lon: 148, shell: 1 },
  { id: "evasion", label: "翻墙巡检", status: "warning", lat: -12, lon: -162, shell: 1.03 },
  { id: "archive", label: "SSH 归档", status: "warning", lat: -48, lon: 20, shell: 0.98 },
  { id: "music", label: "智慧琴房", status: "ok", lat: 52, lon: 158, shell: 1 },
  { id: "vuln", label: "漏洞基线", status: "ok", lat: -42, lon: -96, shell: 1 },
]

/**
 * Interactive 60fps high-DPI 3D Holographic Campus Situational Core (drag to rotate).
 */
export function HolographicCore3D({
  nodes,
  scanning = false,
  title = "3D 全息架构态势核心",
  metrics = [
    { label: "探针覆盖", value: "100%", tone: "success" },
    { label: "业务系统", value: "—", tone: "accent" },
    { label: "告警", value: "0", tone: "warning" },
  ],
}: {
  nodes?: CoreNode3D[]
  scanning?: boolean
  title?: string
  metrics?: { label: string; value: string; tone?: "success" | "accent" | "warning" | "danger" }[]
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const dragRef = useRef<{ dragging: boolean; x: number; y: number; rotY: number; rotX: number }>({
    dragging: false,
    x: 0,
    y: 0,
    rotY: 0.35,
    rotX: 0.28,
  })
  const scanningRef = useRef(scanning)
  scanningRef.current = scanning

  const items = nodes && nodes.length > 0 ? nodes : DEFAULT_CORE_NODES
  const itemsRef = useRef(items)
  itemsRef.current = items

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext("2d")
    if (!ctx) return

    let rafId = 0
    let t = 0
    const reduceMotion =
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches

    const resize = () => {
      const rect = canvas.getBoundingClientRect()
      const dpr = Math.min(window.devicePixelRatio || 1, 2)
      const w = Math.max(220, Math.floor(rect.width))
      const h = Math.max(190, Math.floor(rect.height))
      if (canvas.width !== Math.floor(w * dpr) || canvas.height !== Math.floor(h * dpr)) {
        canvas.width = Math.floor(w * dpr)
        canvas.height = Math.floor(h * dpr)
      }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      return { w, h }
    }

    const project = (x: number, y: number, z: number, cx: number, cy: number, fov: number) => {
      const { rotX, rotY } = dragRef.current
      const cosY = Math.cos(rotY)
      const sinY = Math.sin(rotY)
      const x1 = x * cosY - z * sinY
      const z1 = x * sinY + z * cosY
      const cosX = Math.cos(rotX)
      const sinX = Math.sin(rotX)
      const y2 = y * cosX - z1 * sinX
      const z2 = y * sinX + z1 * cosX
      const scale = fov / (fov + z2 + 230)
      return { sx: cx + x1 * scale, sy: cy + y2 * scale, z: z2, scale }
    }

    const colorOf = (st: "ok" | "warning" | "critical") =>
      st === "critical" ? "#f87171" : st === "warning" ? "#fbbf24" : "#34d399"

    const draw = () => {
      const { w, h } = resize()
      const cx = w / 2
      const cy = h / 2 + 2
      const radius = Math.min(w, h) * 0.36
      const fov = 420
      const isScan = scanningRef.current

      if (!reduceMotion && !dragRef.current.dragging) {
        dragRef.current.rotY += isScan ? 0.022 : 0.0055
      }
      t += isScan ? 0.045 : 0.018

      ctx.clearRect(0, 0, w, h)

      // Ambient holographic halo
      const bgGrad = ctx.createRadialGradient(cx, cy, radius * 0.1, cx, cy, radius * 1.48)
      bgGrad.addColorStop(0, isScan ? "rgba(45, 212, 191, 0.24)" : "rgba(59, 130, 246, 0.20)")
      bgGrad.addColorStop(0.55, "rgba(30, 64, 175, 0.08)")
      bgGrad.addColorStop(1, "rgba(15, 23, 42, 0)")
      ctx.fillStyle = bgGrad
      ctx.fillRect(0, 0, w, h)

      // Latitude wireframe rings
      ctx.lineWidth = 0.8
      for (let latDeg = -60; latDeg <= 60; latDeg += 30) {
        const lat = (latDeg * Math.PI) / 180
        const rLat = Math.cos(lat) * radius
        const yLat = Math.sin(lat) * radius
        ctx.beginPath()
        for (let step = 0; step <= 48; step++) {
          const lon = (step / 48) * Math.PI * 2
          const p = project(Math.cos(lon) * rLat, yLat, Math.sin(lon) * rLat, cx, cy, fov)
          if (step === 0) ctx.moveTo(p.sx, p.sy)
          else ctx.lineTo(p.sx, p.sy)
        }
        ctx.strokeStyle = latDeg === 0 ? "rgba(56, 189, 248, 0.35)" : "rgba(96, 165, 250, 0.16)"
        ctx.stroke()
      }

      // Longitude meridians
      for (let m = 0; m < 8; m++) {
        const lon = (m / 8) * Math.PI * 2
        ctx.beginPath()
        for (let step = 0; step <= 36; step++) {
          const lat = -Math.PI / 2 + (step / 36) * Math.PI
          const rLat = Math.cos(lat) * radius
          const p = project(Math.cos(lon) * rLat, Math.sin(lat) * radius, Math.sin(lon) * rLat, cx, cy, fov)
          if (step === 0) ctx.moveTo(p.sx, p.sy)
          else ctx.lineTo(p.sx, p.sy)
        }
        ctx.strokeStyle = "rgba(96, 165, 250, 0.14)"
        ctx.stroke()
      }

      // Outer tilted telemetry orbit ring
      ctx.beginPath()
      const orbitR = radius * 1.24
      for (let step = 0; step <= 64; step++) {
        const a = (step / 64) * Math.PI * 2
        const ox = Math.cos(a) * orbitR
        const oz = Math.sin(a) * orbitR
        const oy = Math.sin(a + t * 0.4) * radius * 0.24
        const p = project(ox, oy, oz, cx, cy, fov)
        if (step === 0) ctx.moveTo(p.sx, p.sy)
        else ctx.lineTo(p.sx, p.sy)
      }
      ctx.strokeStyle = isScan ? "rgba(45, 212, 191, 0.68)" : "rgba(45, 212, 191, 0.34)"
      ctx.lineWidth = isScan ? 1.6 : 1.1
      ctx.stroke()

      // Live radar scan latitude wave during inspection
      if (isScan) {
        const scanLat = Math.sin(t * 1.6) * (Math.PI * 0.46)
        const rScan = Math.cos(scanLat) * (radius * 1.06)
        const yScan = Math.sin(scanLat) * (radius * 1.06)
        ctx.beginPath()
        for (let step = 0; step <= 48; step++) {
          const lon = (step / 48) * Math.PI * 2
          const p = project(Math.cos(lon) * rScan, yScan, Math.sin(lon) * rScan, cx, cy, fov)
          if (step === 0) ctx.moveTo(p.sx, p.sy)
          else ctx.lineTo(p.sx, p.sy)
        }
        ctx.strokeStyle = "rgba(45, 212, 191, 0.9)"
        ctx.lineWidth = 2.4
        ctx.stroke()
      }

      // Project all 3D campus nodes
      const projected = itemsRef.current.map((n) => {
        const lat = (n.lat * Math.PI) / 180
        const lon = (n.lon * Math.PI) / 180
        const r = radius * (n.shell ?? 1)
        const x = Math.cos(lat) * Math.cos(lon) * r
        const y = -Math.sin(lat) * r
        const z = Math.cos(lat) * Math.sin(lon) * r
        return { node: n, ...project(x, y, z, cx, cy, fov) }
      })

      // Draw glowing energy arcs from hub (index 0: 浙音大模型) to satellite nodes
      const hub = projected[0]
      if (hub) {
        for (let i = 1; i < projected.length; i++) {
          const target = projected[i]
          const depthAlpha = Math.max(0.12, Math.min(0.65, (target.scale - 0.55) * 1.2))
          const midX = (hub.sx + target.sx) / 2
          const midY = (hub.sy + target.sy) / 2 - 14
          ctx.beginPath()
          ctx.moveTo(hub.sx, hub.sy)
          ctx.quadraticCurveTo(midX, midY, target.sx, target.sy)
          ctx.strokeStyle =
            target.node.status === "warning"
              ? `rgba(251, 191, 36, ${depthAlpha * 0.7})`
              : target.node.status === "critical"
                ? `rgba(248, 113, 113, ${depthAlpha * 0.8})`
                : `rgba(56, 189, 248, ${depthAlpha * 0.55})`
          ctx.lineWidth = 0.95
          ctx.stroke()

          // Traveling glowing data packet along the quadratic arc
          const u = (t * 0.45 + i * 0.21) % 1
          const inv = 1 - u
          const px = inv * inv * hub.sx + 2 * inv * u * midX + u * u * target.sx
          const py = inv * inv * hub.sy + 2 * inv * u * midY + u * u * target.sy
          ctx.beginPath()
          ctx.arc(px, py, 2.1, 0, Math.PI * 2)
          ctx.fillStyle = colorOf(target.node.status)
          ctx.fill()
        }
      }

      // Depth-sort back to front and render nodes + crisp labels for front hemisphere
      projected.sort((a, b) => b.z - a.z)
      for (const p of projected) {
        const isFront = p.z < radius * 0.32
        const alpha = isFront ? 0.96 : 0.34
        const dotR = (p.node.id === "llm" ? 5.2 : 3.7) * Math.max(0.75, p.scale)
        const c = colorOf(p.node.status)

        // Pulse ring on warning / critical / hub
        if (isFront && (p.node.status !== "ok" || p.node.id === "llm" || isScan)) {
          const pulse = ((t * 1.1 + p.sx * 0.01) % 1) * 10
          ctx.beginPath()
          ctx.arc(p.sx, p.sy, dotR + pulse, 0, Math.PI * 2)
          ctx.strokeStyle = c
          ctx.globalAlpha = Math.max(0, 0.55 - pulse * 0.05)
          ctx.lineWidth = 1.1
          ctx.stroke()
          ctx.globalAlpha = 1
        }

        // Core glowing sphere
        ctx.beginPath()
        ctx.arc(p.sx, p.sy, dotR, 0, Math.PI * 2)
        ctx.fillStyle = c
        ctx.globalAlpha = alpha
        ctx.fill()

        if (isFront) {
          ctx.font = '600 10.5px "Geist", ui-sans-serif, system-ui, sans-serif'
          ctx.fillStyle = p.node.id === "llm" ? "#e0f2fe" : "#e2e8f0"
          ctx.globalAlpha = Math.min(1, alpha)
          ctx.fillText(p.node.label, p.sx + dotR + 4, p.sy + 3.5)
        }
        ctx.globalAlpha = 1
      }

      if (!reduceMotion) {
        rafId = window.requestAnimationFrame(draw)
      }
    }

    draw()
    return () => {
      if (rafId) window.cancelAnimationFrame(rafId)
    }
  }, [])

  function onPointerDown(e: ReactPointerEvent<HTMLCanvasElement>) {
    dragRef.current.dragging = true
    dragRef.current.x = e.clientX
    dragRef.current.y = e.clientY
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  function onPointerMove(e: ReactPointerEvent<HTMLCanvasElement>) {
    if (!dragRef.current.dragging) return
    const dx = e.clientX - dragRef.current.x
    const dy = e.clientY - dragRef.current.y
    dragRef.current.x = e.clientX
    dragRef.current.y = e.clientY
    dragRef.current.rotY += dx * 0.0085
    dragRef.current.rotX = Math.max(-0.95, Math.min(0.95, dragRef.current.rotX + dy * 0.0065))
  }

  function onPointerUp(e: ReactPointerEvent<HTMLCanvasElement>) {
    dragRef.current.dragging = false
    if (e.currentTarget.hasPointerCapture(e.pointerId)) {
      e.currentTarget.releasePointerCapture(e.pointerId)
    }
  }

  return (
    <div className="doops-3d-stage relative flex flex-col justify-between rounded-xl border border-primary/30 p-3">
      <div className="pointer-events-none flex items-center justify-between gap-2 text-2xs">
        <span className="inline-flex items-center gap-1.5 font-mono uppercase tracking-wider text-accent">
          <span className={`h-2 w-2 rounded-full ${scanning ? "animate-ping bg-accent" : "bg-success"}`} />
          {title}
        </span>
        <span className="rounded border border-primary/30 bg-background/60 px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
          {scanning ? "全网扫描中…" : "可拖拽 360° 旋转"}
        </span>
      </div>

      <canvas
        ref={canvasRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        aria-label="3D 全息架构态势核心球，支持鼠标拖拽旋转"
        className="my-1 h-52 w-full cursor-grab touch-none active:cursor-grabbing"
      />

      <div className="pointer-events-none grid grid-cols-3 gap-1.5 border-t border-white/10 pt-2 text-center font-mono text-2xs">
        {metrics.slice(0, 3).map((m) => (
          <div key={m.label} className="rounded bg-background/45 px-1.5 py-1">
            <div className="text-muted-foreground">{m.label}</div>
            <div
              className={`font-semibold ${
                m.tone === "success"
                  ? "text-success"
                  : m.tone === "danger"
                    ? "text-destructive"
                    : m.tone === "warning"
                      ? "text-warning"
                      : "text-accent"
              }`}
            >
              {m.value}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * Interactive True-3D 4-Layer Floating Campus Architecture Stack (drag to orbit / preset camera views).
 */
export function LayeredArchitecture3D({
  nodes,
  selectedNodeId,
  onSelectNode,
  scanning = false,
  onOpenResources,
  onOpenLayerAction,
}: {
  nodes: ArchNode3D[]
  selectedNodeId: string
  onSelectNode: (id: string) => void
  scanning?: boolean
  onOpenResources?: () => void
  onOpenLayerAction?: (layer: ArchNode3D["layer"]) => void
}) {
  const [viewMode, setViewMode] = useState<"iso" | "aerial" | "flat">("iso")
  const [tilt, setTilt] = useState<{ rx: number; ry: number }>({ rx: 17, ry: -9 })
  const [autoOrbit, setAutoOrbit] = useState(false)
  const dragRef = useRef<{ dragging: boolean; x: number; y: number }>({ dragging: false, x: 0, y: 0 })

  useEffect(() => {
    if (viewMode === "iso") setTilt({ rx: 17, ry: -9 })
    else if (viewMode === "aerial") setTilt({ rx: 30, ry: -16 })
    else setTilt({ rx: 0, ry: 0 })
  }, [viewMode])

  useEffect(() => {
    if (!autoOrbit || viewMode === "flat") return
    let raf = 0
    let phase = 0
    const tick = () => {
      phase += 0.015
      setTilt((prev) => ({
        rx: prev.rx,
        ry: Number((Math.sin(phase) * 14).toFixed(2)),
      }))
      raf = window.requestAnimationFrame(tick)
    }
    raf = window.requestAnimationFrame(tick)
    return () => window.cancelAnimationFrame(raf)
  }, [autoOrbit, viewMode])

  function handlePointerDown(e: ReactPointerEvent<HTMLDivElement>) {
    if ((e.target as HTMLElement).closest("button")) return
    if (viewMode === "flat") return
    setAutoOrbit(false)
    dragRef.current = { dragging: true, x: e.clientX, y: e.clientY }
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  function handlePointerMove(e: ReactPointerEvent<HTMLDivElement>) {
    if (!dragRef.current.dragging) return
    const dx = e.clientX - dragRef.current.x
    const dy = e.clientY - dragRef.current.y
    dragRef.current.x = e.clientX
    dragRef.current.y = e.clientY
    setTilt((prev) => ({
      rx: Math.max(0, Math.min(38, prev.rx - dy * 0.25)),
      ry: Math.max(-24, Math.min(24, prev.ry + dx * 0.25)),
    }))
  }

  function handlePointerUp(e: ReactPointerEvent<HTMLDivElement>) {
    dragRef.current.dragging = false
    if (e.currentTarget.hasPointerCapture(e.pointerId)) {
      e.currentTarget.releasePointerCapture(e.pointerId)
    }
  }

  const layers = [
    {
      key: "business" as const,
      label: "L4 · 业务系统层（Business）",
      emptyText: "暂未登记在役业务系统，可前往「业务系统」台账录入业务与部署映射。",
      actionLabel: "登记业务系统",
      accent: "border-cyan-400/35 bg-cyan-950/20",
      tagColor: "text-cyan-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-5",
      zOffset: 36,
    },
    {
      key: "app" as const,
      label: "L3 · 应用服务、容器与数据库层（Workload / App / DB）",
      emptyText: "暂无已探测的应用、容器或数据库实例，可登记网站/数据库或发起巡检。",
      actionLabel: "查看资源明细",
      accent: "border-blue-400/35 bg-blue-950/20",
      tagColor: "text-blue-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-4",
      zOffset: 24,
    },
    {
      key: "platform" as const,
      label: "L2 · Kubernetes 集群与编排层（Cluster）",
      emptyText: "暂未关联 Kubernetes 集群，可前往「资产 → Kubernetes」从在线 Agent 导入。",
      actionLabel: "管理集群台账",
      accent: "border-indigo-400/35 bg-indigo-950/25",
      tagColor: "text-indigo-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-3",
      zOffset: 12,
    },
    {
      key: "infra" as const,
      label: "L1 · 机器资源层（物理机 / GPU / 虚拟机 / SSH 托管主机）",
      emptyText: "暂未登记或采集到机器资源，可前往「机器台账」登记并绑定在线 Agent。",
      actionLabel: "管理机器台账",
      accent: "border-emerald-400/35 bg-emerald-950/20",
      tagColor: "text-emerald-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-4",
      zOffset: 0,
    },
  ]

  const is3D = viewMode !== "flat"

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold text-foreground">业务 → 集群 → 机器 3D 立体分层架构与实时运行态势</h3>
            <Badge variant="primary">{is3D ? "3D 空间投影" : "2D 平面视图"}</Badge>
            {is3D && <span className="font-mono text-2xs text-muted-foreground">空白处拖拽可自由旋转视角</span>}
          </div>
          <p className="text-xs text-muted-foreground">
            自上而下打通「业务系统 → 应用/数据库 → Kubernetes 集群 → 物理/GPU/虚机/SSH 机器资源」四层实时关联，全部节点由真实台账与监控快照驱动。
          </p>
        </div>

        <Inline gap={1} className="flex-wrap">
          <Button size="sm" variant={viewMode === "iso" ? "primary" : "default"} onClick={() => setViewMode("iso")}>
            3D 立体
          </Button>
          <Button size="sm" variant={viewMode === "aerial" ? "primary" : "default"} onClick={() => setViewMode("aerial")}>
            3D 俯瞰
          </Button>
          <Button size="sm" variant={viewMode === "flat" ? "primary" : "default"} onClick={() => setViewMode("flat")}>
            2D 平面
          </Button>
          {is3D && (
            <Button size="sm" variant={autoOrbit ? "primary" : "ghost"} onClick={() => setAutoOrbit((v) => !v)}>
              {autoOrbit ? "停止巡航" : "自动微旋"}
            </Button>
          )}
          {onOpenResources && (
            <Button size="sm" variant="ghost" onClick={onOpenResources}>
              展开资源明细
            </Button>
          )}
        </Inline>
      </div>

      {/* 3D Holographic Stage */}
      <div
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
        className={`doops-3d-stage rounded-xl border border-primary/25 p-4 md:p-6 ${
          is3D ? "cursor-grab select-none active:cursor-grabbing" : ""
        }`}
        style={{ perspective: is3D ? "1500px" : "none" }}
      >
        {is3D && <div aria-hidden="true" className="doops-3d-grid-floor" />}
        {scanning && <div aria-hidden="true" className="doops-scan-beam" />}

        <div
          className="relative mx-auto space-y-3 transition-transform duration-300 ease-out"
          style={{
            transformStyle: "preserve-3d",
            transform: is3D
              ? `rotateX(${tilt.rx}deg) rotateY(${tilt.ry}deg) scale(${viewMode === "aerial" ? 0.94 : 0.97})`
              : "none",
          }}
        >
          {layers.map((layer, layerIdx) => {
            const layerNodes = nodes.filter((n) => n.layer === layer.key)
            return (
              <div
                key={layer.key}
                className="relative"
                style={{
                  transformStyle: "preserve-3d",
                  transform: is3D ? `translateZ(${layer.zOffset}px)` : "none",
                }}
              >
                <div className={`doops-3d-slab rounded-xl border p-3 ${layer.accent}`}>
                  <div className="mb-2 flex items-center justify-between gap-2 text-2xs font-medium">
                    <span className={`font-mono tracking-wide ${layer.tagColor}`}>{layer.label}</span>
                    <span className="font-mono text-muted-foreground/80">
                      Z+{layer.zOffset}px · {layerNodes.length} 节点
                    </span>
                  </div>

                  {layerNodes.length === 0 ? (
                    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-dashed border-white/15 bg-background/40 px-3 py-2.5 text-xs text-muted-foreground">
                      <span>{layer.emptyText}</span>
                      {onOpenLayerAction && (
                        <Button size="sm" variant="ghost" onClick={() => onOpenLayerAction(layer.key)}>
                          {layer.actionLabel}
                        </Button>
                      )}
                    </div>
                  ) : (
                    <div className={`grid gap-2.5 ${layer.cols}`} style={{ transformStyle: "preserve-3d" }}>
                      {layerNodes.map((node) => {
                        const active = selectedNodeId === node.id
                        const dotCls =
                          node.status === "critical"
                            ? "bg-destructive shadow-[0_0_8px_rgba(248,113,113,0.9)]"
                            : node.status === "warning"
                              ? "bg-warning shadow-[0_0_8px_rgba(251,191,36,0.85)]"
                              : "bg-success shadow-[0_0_8px_rgba(52,211,153,0.85)]"
                        const borderCls = active
                          ? "border-accent bg-primary/15 doops-3d-node-active"
                          : node.status === "critical"
                            ? "border-destructive/60 bg-destructive/10 hover:border-destructive"
                            : node.status === "warning"
                              ? "border-warning/60 bg-warning/10 hover:border-warning"
                              : "border-white/15 bg-card/85 hover:border-accent/70"

                        return (
                          <button
                            key={node.id}
                            type="button"
                            onClick={() => onSelectNode(node.id)}
                            className={`doops-3d-node rounded-lg border p-2.5 text-left ${borderCls}`}
                          >
                            <div className="flex items-center justify-between gap-1.5">
                              <span className="truncate text-xs font-semibold text-foreground">{node.title}</span>
                              <span className={`h-2.5 w-2.5 shrink-0 rounded-full ${dotCls}`} />
                            </div>
                            <div className="mt-0.5 truncate font-mono text-2xs text-muted-foreground">{node.subtitle}</div>
                            <div className="mt-1.5 truncate font-mono text-2xs text-foreground">{node.metricLine}</div>
                          </button>
                        )
                      })}
                    </div>
                  )}
                </div>

                {/* Glowing vertical laser data conduits between 3D planes */}
                {layerIdx < layers.length - 1 && (
                  <div
                    aria-hidden="true"
                    className="pointer-events-none flex items-center justify-around px-10 py-0.5"
                  >
                    {[0, 1, 2, 3].map((beamIdx) => (
                      <div
                        key={beamIdx}
                        className="doops-beam-pulse h-4 w-0.5 rounded-full bg-gradient-to-b from-cyan-400/50 via-blue-400/30 to-emerald-400/50"
                      />
                    ))}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

/**
 * Interactive Isometric 3D Prism Resource Bar Chart (CPU / Memory / GPU / Disk).
 */
export function ResourceBars3D({ items }: { items: ResourceBar3DItem[] }) {
  const [metric, setMetric] = useState<"cpu" | "memory" | "disk" | "gpu">("memory")
  const [hoveredId, setHoveredId] = useState<string>("")

  const metricMeta: Record<"cpu" | "memory" | "disk" | "gpu", { label: string; unit: string }> = {
    cpu: { label: "CPU 水位", unit: "%" },
    memory: { label: "内存水位", unit: "%" },
    disk: { label: "磁盘容量", unit: "%" },
    gpu: { label: "GPU 算力", unit: "%" },
  }

  if (items.length === 0) {
    return (
      <div className="doops-3d-stage rounded-lg border border-primary/20 p-6 text-center text-xs text-muted-foreground">
        暂无实时机器或集群资源采样快照，可前往「智能巡检」发起巡检或点击「立即发起全网巡检」拉取真实指标。
      </div>
    )
  }

  const pickVal = (item: ResourceBar3DItem): number => {
    if (metric === "cpu") return item.cpu
    if (metric === "memory") return item.memory
    if (metric === "disk") return item.disk
    return item.gpu ?? 0
  }

  const W = 680
  const H = 225
  const baseY = 178
  const maxBarH = 122
  const barW = 28
  const depthX = 14
  const depthY = 9
  const spacing = items.length > 0 ? Math.min(104, (W - 90) / items.length) : 90
  const startX = (W - spacing * (items.length - 1)) / 2

  const hovered = items.find((i) => i.id === hoveredId)

  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-foreground">全校核心资产 3D 立体资源柱阵</span>
          <Badge variant="outline">{metricMeta[metric].label}</Badge>
        </div>
        <Inline gap={1}>
          {(["memory", "cpu", "disk", "gpu"] as const).map((m) => (
            <Button key={m} size="sm" variant={metric === m ? "primary" : "ghost"} onClick={() => setMetric(m)}>
              {m === "cpu" ? "CPU" : m === "memory" ? "内存" : m === "disk" ? "磁盘" : "GPU"}
            </Button>
          ))}
        </Inline>
      </div>

      <div className="doops-3d-stage rounded-lg border border-primary/20 p-2">
        <svg viewBox={`0 0 ${W} ${H}`} className="h-52 w-full select-none" role="img" aria-label="3D 立体资源水位柱状图">
          <defs>
            <linearGradient id="doops3dFloor" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#38bdf8" stopOpacity="0.16" />
              <stop offset="100%" stopColor="#1e3a8a" stopOpacity="0.02" />
            </linearGradient>
          </defs>

          {/* Isometric glowing stage floor */}
          <polygon
            points={`56,${baseY - 12} ${W - 36},${baseY - 12} ${W - 14},${baseY + 24} 32,${baseY + 24}`}
            fill="url(#doops3dFloor)"
            stroke="rgba(56,189,248,0.28)"
            strokeWidth="1"
          />

          {/* Horizontal reference lines */}
          {[25, 50, 75, 100].map((pct) => {
            const y = baseY - (pct / 100) * maxBarH
            return (
              <g key={pct}>
                <line x1={48} y1={y} x2={W - 24} y2={y} stroke="rgba(148,163,184,0.14)" strokeDasharray="3 4" />
                <text x={40} y={y + 3} textAnchor="end" fontSize="10" fill="#94a3b8">
                  {pct}%
                </text>
              </g>
            )
          })}

          {items.map((item, idx) => {
            const rawVal = pickVal(item)
            const val = Math.max(0, Math.min(100, rawVal))
            const h = Math.max(6, (val / 100) * maxBarH)
            const cx = startX + idx * spacing
            const x = cx - barW / 2
            const yTop = baseY - h
            const isHover = hoveredId === item.id
            const lift = isHover ? 6 : 0

            const colors =
              val >= 85 || item.status === "critical"
                ? { front: "#ef4444", side: "#b91c1c", top: "#fca5a5", glow: "rgba(239,68,68,0.45)" }
                : val >= 70 || item.status === "warning"
                  ? { front: "#f59e0b", side: "#b45309", top: "#fde68a", glow: "rgba(245,158,11,0.42)" }
                  : metric === "gpu" && val > 0
                    ? { front: "#14b8a6", side: "#0f766e", top: "#99f6e4", glow: "rgba(45,212,191,0.45)" }
                    : { front: "#3b82f6", side: "#1d4ed8", top: "#93c5fd", glow: "rgba(59,130,246,0.42)" }

            const frontPts = `${x},${yTop - lift} ${x + barW},${yTop - lift} ${x + barW},${baseY - lift} ${x},${baseY - lift}`
            const sidePts = `${x + barW},${yTop - lift} ${x + barW + depthX},${yTop - depthY - lift} ${x + barW + depthX},${baseY - depthY - lift} ${x + barW},${baseY - lift}`
            const topPts = `${x},${yTop - lift} ${x + depthX},${yTop - depthY - lift} ${x + barW + depthX},${yTop - depthY - lift} ${x + barW},${yTop - lift}`

            return (
              <g
                key={item.id}
                onMouseEnter={() => setHoveredId(item.id)}
                onMouseLeave={() => setHoveredId("")}
                className="cursor-pointer transition-opacity"
                opacity={hoveredId && !isHover ? 0.58 : 1}
              >
                {/* Ground shadow / reflection */}
                <ellipse cx={cx + depthX * 0.45} cy={baseY + 4} rx={barW * 0.85} ry={6} fill={colors.glow} opacity={isHover ? 0.85 : 0.45} />

                {/* 3D extruded prism faces */}
                <polygon points={sidePts} fill={colors.side} stroke="rgba(255,255,255,0.12)" strokeWidth="0.6" />
                <polygon points={frontPts} fill={colors.front} fillOpacity={0.92} stroke="rgba(255,255,255,0.18)" strokeWidth="0.7" />
                <polygon points={topPts} fill={colors.top} stroke="rgba(255,255,255,0.35)" strokeWidth="0.8" />

                {/* Floating value badge above top cap */}
                <text
                  x={cx + depthX / 2}
                  y={yTop - depthY - lift - 6}
                  textAnchor="middle"
                  fontSize="10.5"
                  fontWeight="600"
                  fill={isHover ? "#f8fafc" : "#cbd5e1"}
                >
                  {metric === "gpu" && item.gpu == null ? "—" : `${val.toFixed(0)}%`}
                </text>

                {/* Asset label */}
                <text x={cx + 4} y={baseY + 18} textAnchor="middle" fontSize="10.5" fontWeight="500" fill="#e2e8f0">
                  {item.label}
                </text>
                <text x={cx + 4} y={baseY + 32} textAnchor="middle" fontSize="9.5" fill="#94a3b8">
                  {item.sublabel}
                </text>
              </g>
            )
          })}
        </svg>

        {hovered && (
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 border-t border-white/10 px-2 pt-1.5 font-mono text-2xs text-slate-200">
            <span>
              选中资产：<strong>{hovered.label}</strong>（{hovered.sublabel}）
            </span>
            <span>
              CPU {hovered.cpu.toFixed(1)}% · 内存 {hovered.memory.toFixed(1)}% · 磁盘 {hovered.disk.toFixed(1)}%
              {hovered.gpu != null ? ` · GPU ${hovered.gpu.toFixed(1)}%` : ""}
            </span>
          </div>
        )}
      </div>
    </div>
  )
}
