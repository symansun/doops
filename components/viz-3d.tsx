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

export interface ArchLink3D {
  from: string
  to: string
  label?: string
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

/**
 * Interactive 60fps high-DPI 3D Holographic Situational Core (drag to rotate, click node to trace).
 */
export function HolographicCore3D({
  nodes,
  selectedNodeId,
  onSelectNode,
  scanning = false,
  title = "3D 全息架构态势核心",
  metrics = [
    { label: "探针覆盖", value: "—", tone: "success" },
    { label: "业务系统", value: "—", tone: "accent" },
    { label: "告警", value: "0", tone: "warning" },
  ],
}: {
  nodes?: CoreNode3D[]
  selectedNodeId?: string
  onSelectNode?: (id: string) => void
  scanning?: boolean
  title?: string
  metrics?: { label: string; value: string; tone?: "success" | "accent" | "warning" | "danger" }[]
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const dragRef = useRef<{ dragging: boolean; moved: boolean; x: number; y: number; rotY: number; rotX: number }>({
    dragging: false,
    moved: false,
    x: 0,
    y: 0,
    rotY: 0.35,
    rotX: 0.24,
  })
  const hitNodesRef = useRef<Array<{ id: string; sx: number; sy: number; r: number }>>([])
  const scanningRef = useRef(scanning)
  scanningRef.current = scanning
  const selectedRef = useRef(selectedNodeId || "")
  selectedRef.current = selectedNodeId || ""

  const items = nodes ?? []
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

      // Draw glowing energy arcs from hub (index 0) to satellite nodes
      const hub = projected[0]
      if (!hub) {
        ctx.font = '500 11px -apple-system, BlinkMacSystemFont, "PingFang SC", sans-serif'
        ctx.textAlign = "center"
        ctx.textBaseline = "middle"
        ctx.fillStyle = "rgba(148, 163, 184, 0.85)"
        ctx.fillText("暂无在线资产节点", cx, cy)
      }
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

      // Depth-sort back to front for dots, then render non-overlapping pill labels for front nodes
      projected.sort((a, b) => b.z - a.z)
      const hitList: Array<{ id: string; sx: number; sy: number; r: number }> = []
      const labelCandidates: Array<{
        p: (typeof projected)[number]
        dotR: number
        priority: number
      }> = []

      for (const p of projected) {
        const isFront = p.z < radius * 0.22
        const isSelected = selectedRef.current === p.node.id
        const alpha = isSelected ? 1 : isFront ? 0.96 : 0.28
        const dotR = (isSelected ? 5.6 : p.node.id === "llm" ? 5.0 : 3.6) * Math.max(0.75, p.scale)
        const c = colorOf(p.node.status)

        if (isFront) {
          hitList.push({ id: p.node.id, sx: p.sx, sy: p.sy, r: Math.max(12, dotR + 8) })
        }

        // Pulse ring on selected / warning / critical / scan
        if (isFront && (isSelected || p.node.status !== "ok" || isScan)) {
          const pulse = ((t * 1.05 + p.sx * 0.01) % 1) * 9
          ctx.beginPath()
          ctx.arc(p.sx, p.sy, dotR + pulse, 0, Math.PI * 2)
          ctx.strokeStyle = isSelected ? "#38bdf8" : c
          ctx.globalAlpha = Math.max(0, 0.6 - pulse * 0.055)
          ctx.lineWidth = isSelected ? 1.5 : 1.1
          ctx.stroke()
          ctx.globalAlpha = 1
        }

        // Core glowing sphere
        ctx.beginPath()
        ctx.arc(p.sx, p.sy, dotR, 0, Math.PI * 2)
        ctx.fillStyle = c
        ctx.globalAlpha = alpha
        ctx.fill()
        if (isSelected) {
          ctx.strokeStyle = "#f8fafc"
          ctx.lineWidth = 1.4
          ctx.stroke()
        }
        ctx.globalAlpha = 1

        if (p.z < radius * 0.14 || isSelected) {
          const statusWeight = p.node.status === "critical" ? 300 : p.node.status === "warning" ? 200 : 100
          const selWeight = isSelected ? 500 : 0
          labelCandidates.push({ p, dotR, priority: selWeight + statusWeight - p.z })
        }
      }
      hitNodesRef.current = hitList

      // Draw crisp pill-backed labels with strict 2D bounding-box collision culling (max 5 visible)
      labelCandidates.sort((a, b) => b.priority - a.priority)
      const placedBoxes: Array<{ x1: number; y1: number; x2: number; y2: number }> = []
      ctx.font = '600 10px "Geist", ui-sans-serif, system-ui, sans-serif'

      for (const cand of labelCandidates) {
        if (placedBoxes.length >= 5) break
        const { p, dotR } = cand
        const label = p.node.label.length > 8 ? `${p.node.label.slice(0, 7)}…` : p.node.label
        const textW = ctx.measureText(label).width
        const padX = 5
        const boxW = textW + padX * 2
        const boxH = 16
        const placeLeft = p.sx + dotR + 6 + boxW > w - 4
        const bx = Math.max(4, Math.min(w - boxW - 4, placeLeft ? p.sx - dotR - 6 - boxW : p.sx + dotR + 6))
        const by = Math.max(4, Math.min(h - boxH - 4, p.sy - boxH / 2))
        const box = { x1: bx - 3, y1: by - 3, x2: bx + boxW + 3, y2: by + boxH + 3 }

        const collides = placedBoxes.some(
          (b) => !(box.x2 < b.x1 || box.x1 > b.x2 || box.y2 < b.y1 || box.y1 > b.y2),
        )
        if (collides) continue
        placedBoxes.push(box)

        const isSelected = selectedRef.current === p.node.id
        const borderCol = isSelected
          ? "rgba(56, 189, 248, 0.9)"
          : p.node.status === "critical"
            ? "rgba(248, 113, 113, 0.7)"
            : p.node.status === "warning"
              ? "rgba(251, 191, 36, 0.7)"
              : "rgba(148, 163, 184, 0.35)"

        ctx.fillStyle = "rgba(15, 23, 42, 0.84)"
        ctx.strokeStyle = borderCol
        ctx.lineWidth = isSelected ? 1.2 : 0.8
        ctx.beginPath()
        if (typeof ctx.roundRect === "function") {
          ctx.roundRect(bx, by, boxW, boxH, 4)
        } else {
          ctx.rect(bx, by, boxW, boxH)
        }
        ctx.fill()
        ctx.stroke()

        ctx.fillStyle = isSelected ? "#ffffff" : "#e2e8f0"
        ctx.fillText(label, bx + padX, by + 11.5)
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
    dragRef.current.moved = false
    dragRef.current.x = e.clientX
    dragRef.current.y = e.clientY
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  function onPointerMove(e: ReactPointerEvent<HTMLCanvasElement>) {
    if (!dragRef.current.dragging) return
    const dx = e.clientX - dragRef.current.x
    const dy = e.clientY - dragRef.current.y
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) dragRef.current.moved = true
    dragRef.current.x = e.clientX
    dragRef.current.y = e.clientY
    dragRef.current.rotY += dx * 0.0085
    dragRef.current.rotX = Math.max(-0.95, Math.min(0.95, dragRef.current.rotX + dy * 0.0065))
  }

  function onPointerUp(e: ReactPointerEvent<HTMLCanvasElement>) {
    const wasClick = dragRef.current.dragging && !dragRef.current.moved
    dragRef.current.dragging = false
    if (e.currentTarget.hasPointerCapture(e.pointerId)) {
      e.currentTarget.releasePointerCapture(e.pointerId)
    }
    if (wasClick && onSelectNode) {
      const rect = e.currentTarget.getBoundingClientRect()
      const mx = e.clientX - rect.left
      const my = e.clientY - rect.top
      const hit = hitNodesRef.current.find((h) => Math.hypot(mx - h.sx, my - h.sy) <= h.r)
      if (hit) onSelectNode(hit.id)
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
 * Interactive 4-Layer Architecture Stack with cross-layer blast-radius tracing
 * and optional True-3D floating projection (flat crisp view by default).
 */
export function LayeredArchitecture3D({
  nodes,
  links = [],
  selectedNodeId,
  onSelectNode,
  onlyAnomalies = false,
  onToggleAnomalies,
  scanning = false,
  onOpenResources,
  onOpenLayerAction,
}: {
  nodes: ArchNode3D[]
  links?: ArchLink3D[]
  selectedNodeId: string
  onSelectNode: (id: string) => void
  onlyAnomalies?: boolean
  onToggleAnomalies?: () => void
  scanning?: boolean
  onOpenResources?: () => void
  onOpenLayerAction?: (layer: ArchNode3D["layer"]) => void
}) {
  const [viewMode, setViewMode] = useState<"flat" | "iso" | "aerial">("flat")
  const [tilt, setTilt] = useState<{ rx: number; ry: number }>({ rx: 0, ry: 0 })
  const [autoOrbit, setAutoOrbit] = useState(false)
  const [hoveredNodeId, setHoveredNodeId] = useState("")
  const dragRef = useRef<{ dragging: boolean; x: number; y: number }>({ dragging: false, x: 0, y: 0 })

  useEffect(() => {
    if (viewMode === "iso") setTilt({ rx: 14, ry: -6 })
    else if (viewMode === "aerial") setTilt({ rx: 26, ry: -12 })
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
        ry: Number((Math.sin(phase) * 12).toFixed(2)),
      }))
      raf = window.requestAnimationFrame(tick)
    }
    raf = window.requestAnimationFrame(tick)
    return () => window.cancelAnimationFrame(raf)
  }, [autoOrbit, viewMode])

  // Compute full upstream (impacted) and downstream (supporting) transitive chain for active node
  const focusId = hoveredNodeId || selectedNodeId
  const upstreamIds = new Set<string>()
  const downstreamIds = new Set<string>()
  if (focusId && links.length > 0) {
    const downQueue = [focusId]
    while (downQueue.length > 0) {
      const cur = downQueue.shift()!
      for (const lk of links) {
        if (lk.from === cur && !downstreamIds.has(lk.to) && lk.to !== focusId) {
          downstreamIds.add(lk.to)
          downQueue.push(lk.to)
        }
      }
    }
    const upQueue = [focusId]
    while (upQueue.length > 0) {
      const cur = upQueue.shift()!
      for (const lk of links) {
        if (lk.to === cur && !upstreamIds.has(lk.from) && lk.from !== focusId) {
          upstreamIds.add(lk.from)
          upQueue.push(lk.from)
        }
      }
    }
  }
  const hasActiveChain = Boolean(focusId && (upstreamIds.size > 0 || downstreamIds.size > 0))

  // When onlyAnomalies is enabled, keep warning/critical nodes AND their connected blast-radius chain
  const anomalyRelatedIds = new Set<string>()
  if (onlyAnomalies) {
    const seeds = nodes.filter((n) => n.status !== "ok").map((n) => n.id)
    for (const s of seeds) anomalyRelatedIds.add(s)
    const queue = [...seeds]
    while (queue.length > 0) {
      const cur = queue.shift()!
      for (const lk of links) {
        if (lk.from === cur && !anomalyRelatedIds.has(lk.to)) {
          anomalyRelatedIds.add(lk.to)
          queue.push(lk.to)
        }
        if (lk.to === cur && !anomalyRelatedIds.has(lk.from)) {
          anomalyRelatedIds.add(lk.from)
          queue.push(lk.from)
        }
      }
    }
  }

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
      rx: Math.max(0, Math.min(34, prev.rx - dy * 0.25)),
      ry: Math.max(-20, Math.min(20, prev.ry + dx * 0.25)),
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
      emptyText: "暂未登记在役业务系统",
      actionLabel: "登记业务系统",
      accent: "border-cyan-400/30 bg-cyan-950/15",
      tagColor: "text-cyan-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-5",
      zOffset: 30,
    },
    {
      key: "app" as const,
      label: "L3 · 应用服务、容器与数据库层（App / Container / DB）",
      emptyText: "暂无已探测的应用、容器或数据库实例",
      actionLabel: "查看资源明细",
      accent: "border-blue-400/30 bg-blue-950/15",
      tagColor: "text-blue-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-4",
      zOffset: 20,
    },
    {
      key: "platform" as const,
      label: "L2 · Kubernetes 集群与编排层（Cluster）",
      emptyText: "暂未关联 Kubernetes 集群",
      actionLabel: "管理集群台账",
      accent: "border-indigo-400/30 bg-indigo-950/20",
      tagColor: "text-indigo-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-3",
      zOffset: 10,
    },
    {
      key: "infra" as const,
      label: "L1 · 机器资源层（物理机 / GPU / 虚拟机 / SSH 托管主机）",
      emptyText: "暂未登记或采集到机器资源",
      actionLabel: "管理机器台账",
      accent: "border-emerald-400/30 bg-emerald-950/15",
      tagColor: "text-emerald-300",
      cols: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-4",
      zOffset: 0,
    },
  ]

  const is3D = viewMode !== "flat"
  const anomalyCount = nodes.filter((n) => n.status !== "ok").length
  const layerOrder: Record<ArchNode3D["layer"], number> = { business: 0, app: 1, platform: 2, infra: 3 }
  const nodeLayerById = new Map(nodes.map((n) => [n.id, n.layer]))

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="space-y-0.5">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold text-foreground">业务 → 应用/库 → 集群 → 机器 联动影响架构</h3>
            <Badge variant={hasActiveChain ? "primary" : "outline"}>
              {hasActiveChain
                ? `已追踪：上游影响 ${upstreamIds.size} · 下游承载 ${downstreamIds.size}`
                : `共 ${nodes.length} 节点 · ${links.length} 条链路`}
            </Badge>
            {is3D && <span className="font-mono text-2xs text-muted-foreground">空白处可拖拽旋转视角</span>}
          </div>
          <p className="text-xs text-muted-foreground">
            点击或悬停任意节点，实时高亮其上游受影响业务与下游承载集群/机器；支持一键筛选异常链路或切换 3D 立体视角。
          </p>
        </div>

        <Inline gap={1} className="flex-wrap">
          {onToggleAnomalies && (
            <Button
              size="sm"
              variant={onlyAnomalies ? "danger-outline" : "default"}
              onClick={onToggleAnomalies}
            >
              {onlyAnomalies ? `显示全部 (${nodes.length})` : `仅看异常链路 (${anomalyCount})`}
            </Button>
          )}
          <Button size="sm" variant={viewMode === "flat" ? "primary" : "default"} onClick={() => setViewMode("flat")}>
            标准分层
          </Button>
          <Button size="sm" variant={viewMode === "iso" ? "primary" : "default"} onClick={() => setViewMode("iso")}>
            3D 立体
          </Button>
          <Button size="sm" variant={viewMode === "aerial" ? "primary" : "default"} onClick={() => setViewMode("aerial")}>
            3D 俯瞰
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

      {/* Architecture Stage (crisp flat by default; true 3D on demand) */}
      <div
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
        className={`rounded-xl border border-primary/20 p-3.5 md:p-4 ${
          is3D ? "doops-3d-stage cursor-grab select-none active:cursor-grabbing md:p-6" : "bg-background/40"
        }`}
        style={{ perspective: is3D ? "1500px" : "none" }}
      >
        {is3D && <div aria-hidden="true" className="doops-3d-grid-floor" />}
        {scanning && <div aria-hidden="true" className="doops-scan-beam" />}

        <div
          className="relative mx-auto space-y-2.5 transition-transform duration-300 ease-out"
          style={{
            transformStyle: is3D ? "preserve-3d" : "flat",
            transform: is3D
              ? `rotateX(${tilt.rx}deg) rotateY(${tilt.ry}deg) scale(${viewMode === "aerial" ? 0.94 : 0.98})`
              : "none",
          }}
        >
          {layers.map((layer, layerIdx) => {
            const rawLayerNodes = nodes.filter((n) => n.layer === layer.key)
            const layerNodes = onlyAnomalies
              ? rawLayerNodes.filter((n) => anomalyRelatedIds.has(n.id))
              : rawLayerNodes
            const layerAnomalies = rawLayerNodes.filter((n) => n.status !== "ok").length
            const nextLayer = layers[layerIdx + 1]
            const betweenCount = nextLayer
              ? links.filter((lk) => {
                  const a = nodeLayerById.get(lk.from)
                  const b = nodeLayerById.get(lk.to)
                  return a && b && layerOrder[a] <= layerIdx && layerOrder[b] >= layerIdx + 1
                }).length
              : 0
            const activeBetween = nextLayer && hasActiveChain
              ? links.some((lk) => {
                  const inChain =
                    (lk.from === focusId || upstreamIds.has(lk.from) || downstreamIds.has(lk.from)) &&
                    (lk.to === focusId || upstreamIds.has(lk.to) || downstreamIds.has(lk.to))
                  const a = nodeLayerById.get(lk.from)
                  const b = nodeLayerById.get(lk.to)
                  return inChain && a && b && layerOrder[a] <= layerIdx && layerOrder[b] >= layerIdx + 1
                })
              : false

            return (
              <div
                key={layer.key}
                className="relative"
                style={{
                  transformStyle: is3D ? "preserve-3d" : "flat",
                  transform: is3D ? `translateZ(${layer.zOffset}px)` : "none",
                }}
              >
                {rawLayerNodes.length === 0 ? (
                  /* Compact single-line onboarding strip so empty layers never crowd out populated layers */
                  <div className={`flex flex-wrap items-center justify-between gap-2 rounded-lg border border-dashed px-3 py-2 text-xs ${layer.accent}`}>
                    <div className="flex flex-wrap items-center gap-2">
                      <span className={`font-mono font-medium ${layer.tagColor}`}>{layer.label}</span>
                      <span className="text-muted-foreground">· {layer.emptyText}</span>
                    </div>
                    {onOpenLayerAction && (
                      <Button size="sm" variant="ghost" onClick={() => onOpenLayerAction(layer.key)}>
                        {layer.actionLabel} →
                      </Button>
                    )}
                  </div>
                ) : (
                  <div className={`rounded-xl border p-3 ${is3D ? "doops-3d-slab" : ""} ${layer.accent}`}>
                    <div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-2xs font-medium">
                      <span className={`font-mono tracking-wide ${layer.tagColor}`}>{layer.label}</span>
                      <span className="flex items-center gap-2 font-mono text-muted-foreground">
                        <span>{layerNodes.length} / {rawLayerNodes.length} 节点</span>
                        {layerAnomalies > 0 && <span className="text-warning">· {layerAnomalies} 异常</span>}
                        {is3D && <span>· Z+{layer.zOffset}px</span>}
                      </span>
                    </div>

                    {layerNodes.length === 0 ? (
                      <div className="rounded-md border border-dashed border-white/10 px-3 py-2 text-xs text-muted-foreground">
                        本层当前无异常或关联受影响节点。
                      </div>
                    ) : (
                      <div className={`grid gap-2 ${layer.cols}`} style={{ transformStyle: is3D ? "preserve-3d" : "flat" }}>
                        {layerNodes.map((node) => {
                          const isSelected = selectedNodeId === node.id
                          const isFocus = focusId === node.id
                          const isUp = upstreamIds.has(node.id)
                          const isDown = downstreamIds.has(node.id)
                          const isDimmed = hasActiveChain && !isFocus && !isUp && !isDown

                          const dotCls =
                            node.status === "critical"
                              ? "bg-destructive shadow-[0_0_8px_rgba(248,113,113,0.9)]"
                              : node.status === "warning"
                                ? "bg-warning shadow-[0_0_8px_rgba(251,191,36,0.85)]"
                                : "bg-success shadow-[0_0_8px_rgba(52,211,153,0.85)]"
                          const borderCls = isSelected
                            ? "border-accent bg-primary/15 ring-1 ring-accent/50"
                            : isUp
                              ? "border-amber-400/70 bg-amber-500/10"
                              : isDown
                                ? "border-cyan-400/70 bg-cyan-500/10"
                                : node.status === "critical"
                                  ? "border-destructive/60 bg-destructive/10 hover:border-destructive"
                                  : node.status === "warning"
                                    ? "border-warning/60 bg-warning/10 hover:border-warning"
                                    : "border-white/12 bg-card/85 hover:border-accent/60"

                          return (
                            <button
                              key={node.id}
                              type="button"
                              onClick={() => onSelectNode(node.id)}
                              onMouseEnter={() => setHoveredNodeId(node.id)}
                              onMouseLeave={() => setHoveredNodeId("")}
                              className={`rounded-lg border p-2.5 text-left transition-all ${borderCls} ${
                                isDimmed ? "opacity-35" : "opacity-100"
                              }`}
                            >
                              <div className="flex items-center justify-between gap-1.5">
                                <span className="truncate text-xs font-semibold text-foreground">{node.title}</span>
                                <span className="flex shrink-0 items-center gap-1">
                                  {isUp && (
                                    <span className="rounded bg-amber-500/20 px-1 py-0.2 font-mono text-[10px] text-amber-300">
                                      上游影响
                                    </span>
                                  )}
                                  {isDown && (
                                    <span className="rounded bg-cyan-500/20 px-1 py-0.2 font-mono text-[10px] text-cyan-300">
                                      下游承载
                                    </span>
                                  )}
                                  <span className={`h-2.5 w-2.5 rounded-full ${dotCls}`} />
                                </span>
                              </div>
                              <div className="mt-0.5 truncate font-mono text-2xs text-muted-foreground">{node.subtitle}</div>
                              <div className="mt-1.5 truncate font-mono text-2xs text-foreground">{node.metricLine}</div>
                            </button>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )}

                {/* Meaningful inter-layer dependency indicator */}
                {layerIdx < layers.length - 1 && (
                  <div
                    aria-hidden="true"
                    className="pointer-events-none flex items-center justify-center gap-3 py-1 font-mono text-[11px]"
                  >
                    <span
                      className={`h-px flex-1 max-w-28 transition-colors ${
                        activeBetween ? "bg-cyan-400/70" : "bg-border/50"
                      }`}
                    />
                    <span
                      className={`rounded-full border px-2 py-0.5 transition-colors ${
                        activeBetween
                          ? "border-cyan-400/60 bg-cyan-500/15 text-cyan-300"
                          : "border-border/50 bg-background/60 text-muted-foreground"
                      }`}
                    >
                      ↓ {betweenCount > 0 ? `${betweenCount} 条依赖承载链路` : "待建立上下层映射"}
                    </span>
                    <span
                      className={`h-px flex-1 max-w-28 transition-colors ${
                        activeBetween ? "bg-cyan-400/70" : "bg-border/50"
                      }`}
                    />
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
export function ResourceBars3D({
  items,
  title = "核心资产 3D 立体资源柱阵",
}: {
  items: ResourceBar3DItem[]
  title?: string
}) {
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
          <span className="font-semibold text-foreground">{title}</span>
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
