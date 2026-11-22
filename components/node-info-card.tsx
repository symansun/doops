"use client"

import { useEffect, useState } from "react"
import { Badge, Button, StatusBanner } from "./ui"
import { ServerIcon } from "./icons"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"

const CAPABILITY_LINE = /^(kubectl|container-runtime|buildctl|skopeo|buildkit-sock|kubeconfig):\s*(OK|MISSING|nerdctl|docker|podman)\b/

interface Capability {
  name: string
  state: string
}

function parseCapabilities(output: string): Capability[] {
  const caps: Capability[] = []
  for (const line of output.split("\n")) {
    const match = CAPABILITY_LINE.exec(line.trim())
    if (match) caps.push({ name: match[1], state: match[2] })
  }
  return caps
}

export function NodeInfoCard({
  session,
  target,
  sessionId,
  online,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  online: boolean
  onRunningChange?: (running: boolean) => void
}) {
  const [running, setRunning] = useState(false)
  const [error, setError] = useState("")
  const [output, setOutput] = useState("")
  const [capabilities, setCapabilities] = useState<Capability[]>([])

  useEffect(() => {
    onRunningChange?.(running)
    return () => onRunningChange?.(false)
  }, [running, onRunningChange])

  async function readNodeInfo() {
    if (running) return
    setRunning(true)
    setError("")
    let buf = ""
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.nodeInfo,
          arguments: { session_id: sessionId },
        },
        (ev) => {
          if (ev.type === "output") buf += ev.data
          else if (ev.type === "result") {
            const text = extractText(ev.result)
            if (text && text !== "Operation complete.") buf += text
          }
          else if (ev.type === "error") setError(ev.error)
        },
      )
      setOutput(buf)
      setCapabilities(parseCapabilities(buf))
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setRunning(false)
    }
  }

  return (
    <section className="card-surface p-5">
      <div className="flex items-center gap-2">
        <ServerIcon width={16} height={16} className="text-primary" />
        <h3 className="text-sm font-semibold text-foreground">节点信息</h3>
      </div>
      <p className="mt-1 text-xs text-muted-foreground">
        通过 doops_node_info 读取 {target.instance} 的系统信息与工具链可用性。
      </p>
      <div className="mt-3">
        <Button size="sm" onClick={() => void readNodeInfo()} disabled={running || !online}>
          {running ? "正在读取…" : "读取节点信息"}
        </Button>
        {!online && <p className="mt-1.5 text-xs text-muted-foreground">Agent 离线，重新连接后可读取</p>}
      </div>
      {error && (
        <StatusBanner tone="error" compact className="mt-3">
          {error}
        </StatusBanner>
      )}
      {capabilities.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          {capabilities.map((cap) => (
            <Badge key={cap.name} variant={cap.state === "MISSING" ? "warning" : "success"} title={cap.state}>
              {cap.name}: {cap.state}
            </Badge>
          ))}
        </div>
      )}
      {output && (
        <pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border bg-muted/40 p-3 font-mono text-xs leading-relaxed text-foreground">
          {output}
        </pre>
      )}
    </section>
  )
}
