"use client"

import { Boxes, Send, Square } from "lucide-react"
import { useRef, useState } from "react"
import { callTool, extractText, type Session, type Target, type ToolCancel } from "@/lib/client"
import {
  extractDigests,
  imageBuildRequest,
  imageCopyRequest,
  registrySyncRequest,
  type ImageBuildInput,
  type ImageCopyInput,
  type RegistrySyncInput,
  type ShellToolRequest,
} from "@/lib/image"

type ImageMode = "build" | "copy" | "sync"

interface ImageLine {
  kind: "out" | "err" | "info"
  text: string
}

const MODES: { id: ImageMode; label: string; hint: string }[] = [
  { id: "build", label: "构建", hint: "doops image build：在目标上用 BuildKit 构建远端目录里的 Dockerfile" },
  { id: "copy", label: "复制", hint: "doops image copy：用 Skopeo 在任意传输方式之间复制镜像并校验 manifest digest" },
  { id: "sync", label: "Registry 同步", hint: "doops registry sync：把同一 tag 的多个镜像从源 Registry 同步到目标 Registry" },
]

const INPUT_CLASS = "min-w-0 rounded-md border bg-background px-2 py-2 text-sm text-foreground outline-none focus:border-ring"
const LABEL_CLASS = "flex min-w-0 flex-col gap-1 text-xs text-muted-foreground"

export function ImagePanel({
  session,
  target,
  sessionId,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  onRunningChange: (running: boolean) => void
}) {
  const [mode, setMode] = useState<ImageMode>("build")
  const [build, setBuild] = useState<ImageBuildInput>({ context: "", dockerfile: "", platform: "linux/amd64", output: "oci", outputPath: "", image: "", push: false })
  const [copy, setCopy] = useState<ImageCopyInput>({ source: "", destination: "", sourceAuthfile: "", destinationAuthfile: "", all: true, verify: true, timeout: 600 })
  const [sync, setSync] = useState<RegistrySyncInput>({ sourceRegistry: "", targetRegistry: "", tag: "", images: "", sourceAuthfile: "", targetAuthfile: "", timeout: 600 })
  const [confirmed, setConfirmed] = useState(false)
  const [lines, setLines] = useState<ImageLine[]>([])
  const [digests, setDigests] = useState<string[]>([])
  const [outcome, setOutcome] = useState<"" | "success" | "failed">("")
  const [running, setRunning] = useState(false)
  const [stopPending, setStopPending] = useState(false)
  const [error, setError] = useState("")
  const cancelRef = useRef<ToolCancel | null>(null)

  function push(line: ImageLine) {
    setLines((current) => [...current, line])
  }

  function changed() {
    setConfirmed(false)
    setError("")
  }

  function updateBuild<K extends keyof ImageBuildInput>(key: K, value: ImageBuildInput[K]) {
    setBuild((current) => ({ ...current, [key]: value }))
    changed()
  }
  function updateCopy<K extends keyof ImageCopyInput>(key: K, value: ImageCopyInput[K]) {
    setCopy((current) => ({ ...current, [key]: value }))
    changed()
  }
  function updateSync<K extends keyof RegistrySyncInput>(key: K, value: RegistrySyncInput[K]) {
    setSync((current) => ({ ...current, [key]: value }))
    changed()
  }

  function buildRequest(): ShellToolRequest {
    if (mode === "build") return imageBuildRequest(sessionId, build, confirmed)
    if (mode === "copy") return imageCopyRequest(sessionId, copy, confirmed)
    return registrySyncRequest(sessionId, sync, confirmed)
  }

  async function run() {
    if (running || stopPending) return
    let request: ShellToolRequest
    try {
      request = buildRequest()
    } catch (err) {
      setError((err as Error).message)
      return
    }
    setError("")
    setLines([])
    setDigests([])
    setOutcome("")
    setRunning(true)
    setStopPending(false)
    onRunningChange(true)
    cancelRef.current = null
    let collected = ""
    push({ kind: "info", text: `目标 ${target.cluster}/${target.instance} · ${MODES.find((m) => m.id === mode)?.label}` })
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: request.tool,
          arguments: request.arguments,
          onCancelReady: (cancel) => { cancelRef.current = cancel },
        },
        (event) => {
          if (event.type === "output") {
            collected += event.data
            push({ kind: "out", text: event.data })
          }
          if (event.type === "result") {
            const result = extractText(event.result)
            if (result) {
              collected += "\n" + result
              push({ kind: "out", text: result })
            }
          }
        },
      )
      setOutcome("success")
    } catch (err) {
      setOutcome("failed")
      if ((err as Error).name !== "AbortError") {
        const message = (err as Error).message
        setError(message)
        push({ kind: "err", text: message })
      }
    } finally {
      setDigests(extractDigests(collected))
      setRunning(false)
      setStopPending(false)
      setConfirmed(false)
      onRunningChange(false)
      cancelRef.current = null
    }
  }

  function stop() {
    if (!cancelRef.current || stopPending) return
    setStopPending(true)
    push({ kind: "info", text: "已请求停止，等待远端终态；请勿立即重试" })
    void cancelRef.current().catch((err) => {
      setStopPending(false)
      push({ kind: "err", text: `停止请求失败：${(err as Error).message}` })
    })
  }

  const current = MODES.find((m) => m.id === mode)!

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b bg-card px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <Boxes aria-hidden="true" className="size-4 text-primary" />
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold text-foreground">镜像工作台</h2>
            <p className="truncate text-xs text-muted-foreground">BuildKit · Skopeo · {target.cluster}/{target.instance}</p>
          </div>
        </div>
        <div className="flex items-center gap-1 rounded-md border bg-background p-0.5" role="group" aria-label="镜像操作">
          {MODES.map((m) => (
            <button
              key={m.id}
              type="button"
              aria-pressed={mode === m.id}
              disabled={running}
              onClick={() => { setMode(m.id); changed() }}
              className={`rounded px-2 py-1 text-xs transition-colors ${mode === m.id ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"}`}
            >
              {m.label}
            </button>
          ))}
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        <form
          className="mx-auto flex w-full max-w-3xl flex-col gap-3"
          onSubmit={(event) => { event.preventDefault(); void run() }}
        >
          <p className="text-xs text-muted-foreground">{current.hint}。私有 Registry 使用 Gateway 为本目标下发的 authfile，界面不接收凭据内容；只有目标把 authfile 放在别处时才需要填写远端路径。</p>
          <fieldset disabled={running} className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2">
            {mode === "build" && (
              <>
                <label className={LABEL_CLASS}>构建上下文目录
                  <input aria-label="构建上下文目录" required value={build.context} onChange={(e) => updateBuild("context", e.target.value)} placeholder="/root/ws/app" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>Dockerfile（可选）
                  <input aria-label="Dockerfile" value={build.dockerfile} onChange={(e) => updateBuild("dockerfile", e.target.value)} placeholder="默认 <上下文>/Dockerfile" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>平台（逗号分隔，可选）
                  <input aria-label="平台" value={build.platform} onChange={(e) => updateBuild("platform", e.target.value)} placeholder="linux/amd64,linux/arm64" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>输出方式
                  <select aria-label="输出方式" value={build.output} onChange={(e) => { const output = e.target.value as ImageBuildInput["output"]; setBuild((c) => ({ ...c, output, push: output === "oci" ? false : c.push })); changed() }} className={INPUT_CLASS}>
                    <option value="oci">OCI 归档</option>
                    <option value="image">镜像</option>
                  </select>
                </label>
                {build.output === "oci" ? (
                  <label className={`${LABEL_CLASS} sm:col-span-2`}>OCI 归档路径
                    <input aria-label="OCI 归档路径" required value={build.outputPath} onChange={(e) => updateBuild("outputPath", e.target.value)} placeholder="/root/ws/app.oci" className={INPUT_CLASS} />
                  </label>
                ) : (
                  <>
                    <label className={LABEL_CLASS}>镜像引用
                      <input aria-label="镜像引用" required value={build.image} onChange={(e) => updateBuild("image", e.target.value)} placeholder="registry.example.com/team/app:v1.2.3" className={INPUT_CLASS} />
                    </label>
                    <label className="flex items-center gap-2 self-end pb-2 text-xs text-muted-foreground">
                      <input type="checkbox" checked={build.push} onChange={(e) => updateBuild("push", e.target.checked)} />
                      构建后经 BuildKit 推送到 Registry
                    </label>
                  </>
                )}
              </>
            )}

            {mode === "copy" && (
              <>
                <label className={`${LABEL_CLASS} sm:col-span-2`}>源镜像（含传输方式）
                  <input aria-label="源镜像" required value={copy.source} onChange={(e) => updateCopy("source", e.target.value)} placeholder="docker://registry.example.com/team/app:v1" className={INPUT_CLASS} />
                </label>
                <label className={`${LABEL_CLASS} sm:col-span-2`}>目标镜像（含传输方式）
                  <input aria-label="目标镜像" required value={copy.destination} onChange={(e) => updateCopy("destination", e.target.value)} placeholder="docker://mirror.example.com/team/app:v1 或 oci-archive:/root/ws/app.oci" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>源 authfile 远端路径（可选）
                  <input aria-label="源 authfile" value={copy.sourceAuthfile} onChange={(e) => updateCopy("sourceAuthfile", e.target.value)} placeholder="默认使用 Gateway 下发的 authfile" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>目标 authfile 远端路径（可选）
                  <input aria-label="目标 authfile" value={copy.destinationAuthfile} onChange={(e) => updateCopy("destinationAuthfile", e.target.value)} placeholder="默认使用 Gateway 下发的 authfile" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>单步超时（秒）
                  <input aria-label="超时" type="number" min={1} step={1} value={copy.timeout} onChange={(e) => updateCopy("timeout", Number(e.target.value))} className={INPUT_CLASS} />
                </label>
                <div className="flex flex-col justify-end gap-1 pb-1 text-xs text-muted-foreground">
                  <label className="flex items-center gap-2"><input type="checkbox" checked={copy.all} onChange={(e) => updateCopy("all", e.target.checked)} />复制全部平台 manifest（--all）</label>
                  <label className="flex items-center gap-2"><input type="checkbox" checked={copy.verify} onChange={(e) => updateCopy("verify", e.target.checked)} />复制后比对源和目标 manifest digest</label>
                </div>
              </>
            )}

            {mode === "sync" && (
              <>
                <label className={LABEL_CLASS}>源 Registry 路径
                  <input aria-label="源 Registry" required value={sync.sourceRegistry} onChange={(e) => updateSync("sourceRegistry", e.target.value)} placeholder="registry.example.com/source/team" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>目标 Registry 路径
                  <input aria-label="目标 Registry" required value={sync.targetRegistry} onChange={(e) => updateSync("targetRegistry", e.target.value)} placeholder="harbor.example.com/mirror/team" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>不可变 tag
                  <input aria-label="tag" required value={sync.tag} onChange={(e) => updateSync("tag", e.target.value)} placeholder="release-20261101" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>单步超时（秒）
                  <input aria-label="超时" type="number" min={1} step={1} value={sync.timeout} onChange={(e) => updateSync("timeout", Number(e.target.value))} className={INPUT_CLASS} />
                </label>
                <label className={`${LABEL_CLASS} sm:col-span-2`}>镜像名（不含 Registry 和 tag，逗号或空格分隔）
                  <input aria-label="镜像名" required value={sync.images} onChange={(e) => updateSync("images", e.target.value)} placeholder="api, worker" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>源 authfile 远端路径（可选）
                  <input aria-label="源 authfile" value={sync.sourceAuthfile} onChange={(e) => updateSync("sourceAuthfile", e.target.value)} placeholder="默认使用 Gateway 下发的 authfile" className={INPUT_CLASS} />
                </label>
                <label className={LABEL_CLASS}>目标 authfile 远端路径（可选）
                  <input aria-label="目标 authfile" value={sync.targetAuthfile} onChange={(e) => updateSync("targetAuthfile", e.target.value)} placeholder="默认使用 Gateway 下发的 authfile" className={INPUT_CLASS} />
                </label>
              </>
            )}
          </fieldset>

          <label className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/5 p-2 text-xs text-foreground">
            <input type="checkbox" checked={confirmed} disabled={running} onChange={(e) => setConfirmed(e.target.checked)} className="mt-0.5" />
            <span>我确认在 {target.cluster}/{target.instance} 上执行本次{current.label}，它会写入目标路径或 Registry。</span>
          </label>
          {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
          <div className="flex items-center gap-2">
            <button type="submit" disabled={!confirmed || running || stopPending} className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">
              <Send aria-hidden="true" className="size-4" />执行{current.label}
            </button>
            {running && (
              <button type="button" onClick={stop} disabled={stopPending} className="inline-flex items-center gap-1.5 rounded-md border px-3 py-2 text-sm text-foreground disabled:opacity-50">
                <Square aria-hidden="true" className="size-4" />{stopPending ? "停止中…" : "停止"}
              </button>
            )}
          </div>
        </form>

        {(outcome || digests.length > 0) && (
          <section aria-label="执行结果" className="mx-auto mt-4 w-full max-w-3xl rounded-md border bg-card p-3 text-xs">
            <p className={outcome === "failed" ? "text-destructive" : "text-foreground"}>{outcome === "failed" ? "执行失败，见下方输出" : "执行完成"}</p>
            {digests.length > 0 && (
              <ul className="mt-2 flex flex-col gap-1">
                {digests.map((digest) => <li key={digest} className="break-all font-mono text-muted-foreground">{digest}</li>)}
              </ul>
            )}
          </section>
        )}

        {lines.length > 0 && (
          <pre aria-label="执行输出" className="mx-auto mt-4 w-full max-w-3xl whitespace-pre-wrap break-all rounded-md border bg-background p-3 font-mono text-xs">
            {lines.map((line, index) => (
              <span key={index} className={line.kind === "err" ? "text-destructive" : line.kind === "info" ? "text-muted-foreground" : "text-foreground"}>
                {line.text}{line.text.endsWith("\n") ? "" : "\n"}
              </span>
            ))}
          </pre>
        )}
      </div>
    </div>
  )
}
