"use client"

import dynamic from "next/dynamic"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { BookOpenText, Check, FileText, ListTree, Plus, RefreshCw, Save, Search, Trash2 } from "lucide-react"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { cn } from "@/lib/cn"
import { TOOLS } from "@/lib/gateway"
import { extractMarkdownToc, kbSessionStore } from "@/lib/markdown-render"
import type { EditorCursorStats } from "./kb-markdown-editor"
import { Markdown } from "./markdown"
import {
  Badge,
  Button,
  Input,
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
  SegmentedControl,
} from "./ui"

const KbMarkdownEditor = dynamic(() => import("./kb-markdown-editor").then((m) => m.KbMarkdownEditor), {
  ssr: false,
  loading: () => (
    <div className="flex h-full items-center justify-center bg-background text-xs text-muted-foreground">
      正在加载 Markdown 编辑器…
    </div>
  ),
})

// 知识库根目录（持久化，不随会话工作区清理）
const KB_ROOT = "/root/kb"

// 知识库文档会被读回会话，所以写上限必须等于 Agent 侧的读上限（maxFileReadBytes）；
// 写进去却读不回来的文档等于丢失。
const KB_MAX_BYTES = 1 << 20

function formatDocSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < KB_MAX_BYTES) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / KB_MAX_BYTES).toFixed(2)} MB`
}

type Mode = "edit" | "split" | "preview"

function DocumentPreviewPane({
  content,
  fullWidth,
  showToc,
}: {
  content: string
  fullWidth: boolean
  showToc: boolean
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const toc = useMemo(() => extractMarkdownToc(content), [content])

  function jumpToHeading(id: string) {
    const container = scrollRef.current
    if (!container) return
    const targetEl = container.querySelector<HTMLElement>(`#${CSS.escape(id)}`)
    if (targetEl) {
      targetEl.scrollIntoView({ behavior: "smooth", block: "start" })
    }
  }

  return (
    <div className="flex h-full min-h-0 min-w-0 bg-background">
      <div ref={scrollRef} className="min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-5 sm:px-7">
        <div className={cn(fullWidth && "mx-auto max-w-4xl")}>
          {content.trim() ? (
            <Markdown content={content} variant="document" allowImages={false} />
          ) : (
            <div className="flex min-h-40 items-center justify-center rounded-lg border border-dashed border-border/70 bg-card/30 px-4 py-10 text-center text-sm text-muted-foreground">
              预览区：在左侧输入 Markdown、代码块或 Mermaid 图表即可实时渲染
            </div>
          )}
        </div>
      </div>

      {showToc && toc.length > 0 && (
        <aside
          aria-label="文档目录大纲"
          className="hidden w-52 shrink-0 flex-col border-l bg-card/35 lg:flex"
        >
          <div className="flex items-center justify-between border-b px-3 py-2">
            <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
              <ListTree aria-hidden="true" className="size-3.5 text-primary" />
              目录大纲
            </span>
            <Badge variant="outline">{toc.length}</Badge>
          </div>
          <nav className="min-h-0 flex-1 space-y-0.5 overflow-y-auto p-2">
            {toc.map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => jumpToHeading(item.id)}
                title={item.text}
                className={cn(
                  "block w-full truncate rounded-md py-1 pr-2 text-left text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                  item.level === 1 && "pl-2 font-medium text-foreground/95",
                  item.level === 2 && "pl-3.5",
                  item.level === 3 && "pl-5 text-2xs",
                  item.level >= 4 && "pl-6.5 text-2xs",
                )}
              >
                {item.text}
              </button>
            ))}
          </nav>
        </aside>
      )}
    </div>
  )
}

export function KnowledgeBase({
  session,
  target,
  sessionId,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  onRunningChange?: (running: boolean) => void
}) {
  const cache = useMemo(() => kbSessionStore.get(session, target.key), [session, target.key])
  const [docs, setDocs] = useState<string[]>(() => cache.docs)
  const [listBusy, setListBusy] = useState(false)
  const [listErr, setListErr] = useState("")
  const [filterQuery, setFilterQuery] = useState("")

  const [openDoc, setOpenDoc] = useState<string | null>(() => cache.openDoc)
  const [content, setContent] = useState(() => cache.content)
  const [dirty, setDirty] = useState(() => cache.dirty)
  const drafts = useRef(cache.drafts)
  drafts.current = cache.drafts
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [mode, setMode] = useState<Mode>(() => cache.mode)
  const [showToc, setShowToc] = useState(true)
  const [status, setStatus] = useState<{ kind: "ok" | "warn" | "err"; text: string } | null>(null)
  const [stats, setStats] = useState<EditorCursorStats | null>(null)
  const [conflict, setConflict] = useState(false)
  // 最近一次确认过的远端内容；名字对不上（新文档、换了文档）时跳过冲突检测。
  const baseline = useRef<{ name: string; content: string } | null>(null)

  useEffect(() => {
    cache.docs = docs
    cache.openDoc = openDoc
    cache.content = content
    cache.dirty = dirty
    cache.mode = mode
  }, [cache, docs, openDoc, content, dirty, mode])

  useEffect(() => {
    onRunningChange?.(busy)
    return () => onRunningChange?.(false)
  }, [busy, onRunningChange])

  // 草稿只存在内存里，刷新或关页会丢，所以有未保存修改时拦住离开。
  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ""
    }
    window.addEventListener("beforeunload", warn)
    return () => window.removeEventListener("beforeunload", warn)
  }, [dirty])

  const nameRef = useRef<HTMLInputElement>(null)
  const [draftName, setDraftName] = useState("")
  const [naming, setNaming] = useState(false)
  const [nameErr, setNameErr] = useState("")
  const [autoFocusEditor, setAutoFocusEditor] = useState(false)

  const documentNames = [...new Set([...docs, ...drafts.current.keys()])].sort((a, b) => a.localeCompare(b))
  const filteredDocumentNames = useMemo(() => {
    const q = filterQuery.trim().toLowerCase()
    if (!q) return documentNames
    return documentNames.filter((name) => name.toLowerCase().includes(q))
  }, [documentNames, filterQuery])

  const tocCount = useMemo(() => extractMarkdownToc(content).length, [content])
  const lineCount = useMemo(() => (content ? content.split("\n").length : 0), [content])
  const byteLength = useMemo(() => new TextEncoder().encode(content).length, [content])
  const overLimit = byteLength > KB_MAX_BYTES

  // 根目录由 doops_kb list 返回：Agent 可以用 DOOPS_KB_ROOT 改变它，控制台不该写死。
  const [kbRoot, setKbRoot] = useState(KB_ROOT)

  const refresh = useCallback(async () => {
    setListBusy(true)
    setListErr("")
    let buf = ""
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.kb,
          arguments: { session_id: sessionId, operation: "list" },
        },
        (ev) => {
          if (ev.type === "output") buf += ev.data
          else if (ev.type === "result") buf += extractText(ev.result)
          else if (ev.type === "error") setListErr(ev.error)
        },
      )
      const listed = JSON.parse(buf) as { root?: string; documents?: { name: string }[] }
      if (listed.root) setKbRoot(listed.root)
      const names = (listed.documents ?? []).map((doc) => doc.name).filter(Boolean)
      setDocs(names.sort((a, b) => a.localeCompare(b)))
    } catch (err) {
      setListErr((err as Error).message)
    } finally {
      setListBusy(false)
    }
  }, [session, target.cluster, target.instance, sessionId])

  useEffect(() => {
    refresh()
  }, [refresh])

  async function openFile(name: string) {
    if (busyRef.current) return
    setAutoFocusEditor(false)
    const draft = drafts.current.get(name)
    if (draft !== undefined) {
      if (baseline.current?.name !== name) baseline.current = null
      setOpenDoc(name)
      setContent(draft)
      setDirty(true)
      setStatus(null)
      setConflict(false)
      return
    }
    busyRef.current = true
    setBusy(true)
    setStatus(null)
    setListErr("")
    let buf = ""
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.kb,
          arguments: { session_id: sessionId, operation: "read", name },
        },
        (ev) => {
          if (ev.type === "output") buf += ev.data
          else if (ev.type === "result") buf += extractText(ev.result)
          else if (ev.type === "error") setStatus({ kind: "err", text: ev.error })
        },
      )
      baseline.current = { name, content: buf }
      setOpenDoc(name)
      setContent(buf)
      setDirty(false)
      setConflict(false)
    } catch (err) {
      const text = (err as Error).message
      setStatus({ kind: "err", text })
      setListErr(text)
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const save = useCallback(
    async (force = false) => {
      if (!openDoc || busyRef.current || !dirty) return
      const bytes = new TextEncoder().encode(content).length
      if (bytes > KB_MAX_BYTES) {
        setStatus({
          kind: "err",
          text: `文档 ${formatDocSize(bytes)}，超过单篇上限 ${formatDocSize(KB_MAX_BYTES)}，请拆分后再保存`,
        })
        return
      }
      busyRef.current = true
      setBusy(true)
      setStatus(null)
      try {
        // 协议没有版本号，保存前读一次远端比对基线，避免静默覆盖别人的修改。
        const known = baseline.current?.name === openDoc ? baseline.current.content : null
        if (!force && known !== null) {
          let remote = ""
          let failure = ""
          await callTool(
            session,
            {
              cluster: target.cluster,
              instance: target.instance,
              tool: TOOLS.kb,
              arguments: { session_id: sessionId, operation: "read", name: openDoc },
            },
            (ev) => {
              if (ev.type === "output") remote += ev.data
              else if (ev.type === "result") remote += extractText(ev.result)
              else if (ev.type === "error") failure = ev.error
            },
          )
          if (failure) {
            setStatus({ kind: "err", text: `无法确认文档的最新内容，未保存：${failure}` })
            return
          }
          if (remote !== known) {
            setConflict(true)
            setStatus({ kind: "warn", text: "文档已被其他会话修改；覆盖会丢掉对方的修改" })
            return
          }
        }
        await callTool(
          session,
          {
            cluster: target.cluster,
            instance: target.instance,
            tool: TOOLS.kb,
            arguments: { session_id: sessionId, operation: "write", name: openDoc, content },
          },
          (ev) => {
            if (ev.type === "error") setStatus({ kind: "err", text: ev.error })
            else if (ev.type === "result") {
              baseline.current = { name: openDoc, content }
              setStatus({ kind: "ok", text: "已保存" })
              setConflict(false)
              drafts.current.delete(openDoc)
              setDirty(false)
            }
          },
        )
        if (!docs.includes(openDoc)) await refresh()
      } catch (err) {
        setStatus({ kind: "err", text: (err as Error).message })
      } finally {
        busyRef.current = false
        setBusy(false)
      }
    },
    [openDoc, dirty, session, target.cluster, target.instance, sessionId, content, docs, refresh],
  )

  async function reloadDoc(name: string) {
    drafts.current.delete(name)
    setConflict(false)
    await openFile(name)
  }

  function beginNewDoc() {
    if (busyRef.current) return
    setNameErr("")
    setNaming(true)
    setTimeout(() => nameRef.current?.focus(), 0)
  }

  function cancelNewDoc() {
    setNaming(false)
    setDraftName("")
    setNameErr("")
  }

  function createNamedDoc(raw: string) {
    if (busyRef.current) return
    let name = raw.trim()
    if (!name) {
      setNameErr("请填写文档名称")
      return
    }
    if (!/\.md$/i.test(name)) name += ".md"
    if (!/^[^\\/\u0000]+\.md$/i.test(name) || name === ".md" || name.includes("..")) {
      setNameErr("文档名只能是单层 Markdown 文件名，不能包含路径")
      return
    }
    cancelNewDoc()
    if (docs.includes(name) || drafts.current.has(name)) {
      openFile(name)
      return
    }
    const draft = `# ${name.replace(/\.md$/i, "")}\n\n`
    drafts.current.set(name, draft)
    setOpenDoc(name)
    setContent(draft)
    setDirty(true)
    setMode("split")
    setAutoFocusEditor(true)
    setStatus({ kind: "ok", text: "编辑后点击保存（或按 ⌘S）即可创建" })
  }

  const handleEditorChange = useCallback(
    (next: string) => {
      if (!openDoc) return
      drafts.current.set(openDoc, next)
      setContent(next)
      setDirty(true)
    },
    [openDoc],
  )

  const newDocForm = (compact: boolean) => (
    <form
      className={compact ? "border-b bg-card/50 p-2.5" : "flex w-full max-w-xs flex-col gap-2.5 text-left"}
      onSubmit={(event) => {
        event.preventDefault()
        createNamedDoc(draftName)
      }}
    >
      <label htmlFor="kb-new-doc" className="block text-xs text-muted-foreground">
        文档名称（自动补全 .md）
      </label>
      <Input
        id="kb-new-doc"
        ref={nameRef}
        scale={compact ? "sm" : "default"}
        value={draftName}
        aria-invalid={Boolean(nameErr)}
        onChange={(event) => {
          setDraftName(event.target.value)
          setNameErr("")
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape" && openDoc) {
            event.preventDefault()
            cancelNewDoc()
          }
        }}
        placeholder="运行手册"
        className="w-full"
      />
      {nameErr && <p className="text-xs text-destructive">{nameErr}</p>}
      <div className="flex justify-end gap-1.5">
        {openDoc && (
          <Button type="button" size="sm" onClick={cancelNewDoc}>
            取消
          </Button>
        )}
        <Button type="submit" variant="primary" size="sm" disabled={busy}>
          <Plus aria-hidden="true" className="size-3.5" /> 创建
        </Button>
      </div>
    </form>
  )

  async function deleteDoc(name: string) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.kb,
          arguments: { session_id: sessionId, operation: "delete", name },
        },
        () => {},
      )
      drafts.current.delete(name)
      if (openDoc === name) {
        baseline.current = null
        setConflict(false)
        setOpenDoc(null)
        setContent("")
        setDirty(false)
        setStats(null)
      }
      await refresh()
    } catch (err) {
      setStatus({ kind: "err", text: (err as Error).message })
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full min-h-0">
      {/* 左侧文档目录栏 */}
      <aside className="flex w-56 shrink-0 flex-col border-r bg-card/40 sm:w-60">
        <div className="flex items-center justify-between gap-1 border-b px-3 py-2">
          <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
            <BookOpenText aria-hidden="true" className="size-3.5 text-primary" />
            知识库
            {documentNames.length > 0 && <Badge variant="outline">{documentNames.length}</Badge>}
          </span>
          <div className="flex items-center gap-0.5">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              icon
              onClick={beginNewDoc}
              disabled={busy}
              title="新建文档"
              aria-label="新建文档"
            >
              <Plus aria-hidden="true" className="size-3.5" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              icon
              onClick={refresh}
              disabled={listBusy}
              title="刷新文档列表"
              aria-label="刷新文档列表"
            >
              <RefreshCw aria-hidden="true" className={cn("size-3.5", listBusy && "animate-spin")} />
            </Button>
          </div>
        </div>

        {openDoc && naming && newDocForm(true)}

        {documentNames.length > 2 && (
          <div className="border-b px-2.5 py-2">
            <div className="relative flex items-center">
              <Search aria-hidden="true" className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground" />
              <Input
                scale="sm"
                value={filterQuery}
                onChange={(e) => setFilterQuery(e.target.value)}
                placeholder="筛选文档…"
                aria-label="筛选知识库文档"
                className="w-full pl-8"
              />
            </div>
          </div>
        )}

        <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto p-1.5">
          {listErr ? (
            <p role="alert" className="px-2 py-3 text-xs text-destructive">
              {listErr}
            </p>
          ) : documentNames.length === 0 ? (
            <p className="px-2 py-3 text-xs text-muted-foreground">
              {listBusy ? "加载中…" : "暂无文档，点击右上角 + 新建"}
            </p>
          ) : filteredDocumentNames.length === 0 ? (
            <p className="px-2 py-3 text-xs text-muted-foreground">无匹配的文档名称</p>
          ) : (
            filteredDocumentNames.map((name) => {
              const hasDraft = drafts.current.has(name)
              const active = openDoc === name
              return (
                <div
                  key={name}
                  className={cn(
                    "group flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm transition-colors",
                    active ? "bg-primary/12 text-foreground" : "text-muted-foreground hover:bg-muted/70 hover:text-foreground",
                  )}
                >
                  <button
                    type="button"
                    onClick={() => openFile(name)}
                    disabled={busy}
                    className="flex min-w-0 flex-1 items-center gap-1.5 text-left"
                  >
                    <FileText aria-hidden="true" className={cn("size-3.5 shrink-0", active && "text-primary")} />
                    <span className="truncate">{name.replace(/\.md$/i, "")}</span>
                    {hasDraft && (
                      <span
                        className="size-1.5 shrink-0 rounded-full bg-warning"
                        title="存在未保存修改"
                        aria-label="存在未保存修改"
                      />
                    )}
                  </button>
                  <button
                    type="button"
                    onClick={() => void deleteDoc(name)}
                    disabled={busy}
                    className="shrink-0 rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100 disabled:opacity-50"
                    title={`删除 ${name}`}
                    aria-label={`删除 ${name}`}
                  >
                    <Trash2 aria-hidden="true" className="size-3.5" />
                  </button>
                </div>
              )
            })
          )}
        </div>
      </aside>

      {/* 右侧主工作区 */}
      <div className="flex min-w-0 flex-1 flex-col">
        {openDoc ? (
          <>
            {/* 顶部文档操作条 */}
            <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-card px-3 py-2">
              <div className="flex min-w-0 items-center gap-2">
                <span className="truncate text-sm font-semibold text-foreground">
                  {openDoc.replace(/\.md$/i, "")}
                </span>
                <code className="hidden rounded bg-muted px-1.5 py-0.5 font-mono text-2xs text-muted-foreground md:inline">
                  {kbRoot}/{openDoc}
                </code>
                {dirty ? <Badge variant="warning">未保存</Badge> : <Badge variant="success">已同步</Badge>}
              </div>

              <div className="ml-auto flex flex-wrap items-center gap-2">
                <SegmentedControl
                  value={mode}
                  onChange={setMode}
                  ariaLabel="知识库编辑模式"
                  options={[
                    { value: "edit", label: "编辑" },
                    { value: "split", label: "分屏" },
                    { value: "preview", label: "预览" },
                  ]}
                />

                {mode !== "edit" && tocCount > 0 && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    aria-pressed={showToc}
                    onClick={() => setShowToc((v) => !v)}
                    title={showToc ? "收起目录大纲" : "显示目录大纲"}
                    className={cn("hidden lg:inline-flex", showToc && "bg-primary/15 text-primary")}
                  >
                    <ListTree aria-hidden="true" className="size-3.5" />
                    <span>大纲</span>
                  </Button>
                )}

                {conflict && dirty ? (
                  <>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => void reloadDoc(openDoc)}
                      disabled={busy}
                      title="放弃本地修改，重新读取节点上的最新版本"
                    >
                      <RefreshCw aria-hidden="true" className="size-3.5" />
                      <span>重新加载</span>
                    </Button>
                    <Button
                      type="button"
                      variant="danger"
                      size="sm"
                      onClick={() => void save(true)}
                      disabled={busy || overLimit}
                      title="忽略冲突，用自己的版本覆盖节点上的文档"
                    >
                      <Save aria-hidden="true" className="size-3.5" />
                      <span>{busy ? "保存中…" : "覆盖保存"}</span>
                    </Button>
                  </>
                ) : (
                  <Button
                    type="button"
                    variant="primary"
                    size="sm"
                    onClick={() => void save()}
                    disabled={busy || !dirty || overLimit}
                    title={`保存到节点 ${kbRoot} (⌘S)，单篇上限 ${formatDocSize(KB_MAX_BYTES)}`}
                  >
                    <Save aria-hidden="true" className="size-3.5" />
                    <span>{busy ? "保存中…" : "保存"}</span>
                  </Button>
                )}
              </div>
            </div>

            {/* 编辑 / 可拖拽分栏 / 预览画布 */}
            <div className="min-h-0 flex-1 overflow-hidden">
              {mode === "edit" && (
                <KbMarkdownEditor
                  value={content}
                  disabled={busy}
                  autoFocus={autoFocusEditor}
                  onChange={handleEditorChange}
                  onSave={() => void save()}
                  onStatsChange={setStats}
                />
              )}

              {mode === "split" && (
                <ResizablePanelGroup id="doops-kb-split" className="h-full w-full">
                  <ResizablePanel defaultSize={50} minSize={25} className="min-h-0 min-w-0">
                    <KbMarkdownEditor
                      value={content}
                      disabled={busy}
                      autoFocus={autoFocusEditor}
                      onChange={handleEditorChange}
                      onSave={save}
                      onStatsChange={setStats}
                    />
                  </ResizablePanel>
                  <ResizableHandle />
                  <ResizablePanel defaultSize={50} minSize={25} className="min-h-0 min-w-0">
                    <DocumentPreviewPane content={content} fullWidth={false} showToc={showToc} />
                  </ResizablePanel>
                </ResizablePanelGroup>
              )}

              {mode === "preview" && <DocumentPreviewPane content={content} fullWidth showToc={showToc} />}
            </div>

            {/* 底部状态与统计栏 */}
            <footer className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t bg-card/60 px-3 py-1.5 text-xs text-muted-foreground">
              <div className="flex items-center gap-3 font-mono text-2xs tabular-nums">
                {mode !== "preview" && stats ? (
                  <span>
                    行 {stats.line}, 列 {stats.col}
                  </span>
                ) : null}
                <span>{lineCount} 行</span>
                <span>{content.length} 字符</span>
                <span
                  className={cn(overLimit && "text-destructive")}
                  title={`单篇上限 ${formatDocSize(KB_MAX_BYTES)}`}
                >
                  {formatDocSize(byteLength)} / {formatDocSize(KB_MAX_BYTES)}
                </span>
              </div>

              <div className="flex items-center gap-3">
                {status && (
                  <span
                    role={status.kind === "err" ? "alert" : "status"}
                    className={cn(
                      "flex items-center gap-1 text-xs",
                      status.kind === "ok" && "text-success",
                      status.kind === "warn" && "text-warning",
                      status.kind === "err" && "text-destructive",
                    )}
                  >
                    {status.kind === "ok" && <Check aria-hidden="true" className="size-3" />}
                    {status.text}
                  </span>
                )}
                <span className="hidden font-mono text-2xs text-muted-foreground/75 sm:inline">⌘S 保存</span>
              </div>
            </footer>
          </>
        ) : (
          <div className="flex flex-1 flex-col items-center justify-center gap-3.5 px-4 text-center text-muted-foreground">
            <div className="flex size-12 items-center justify-center rounded-xl bg-primary/10 text-primary">
              <BookOpenText aria-hidden="true" className="size-6" />
            </div>
            <div className="space-y-1">
              <h2 className="text-base font-semibold text-foreground">节点运维知识库</h2>
              <p className="max-w-sm text-pretty text-sm text-muted-foreground">
                在 <code className="font-mono text-foreground">{target.instance}</code> 上在线维护 Markdown 运行手册，支持代码高亮、Mermaid 架构图、公式与实时分栏预览
              </p>
            </div>
            {newDocForm(false)}
          </div>
        )}
      </div>
    </div>
  )
}
