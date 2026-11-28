"use client"

import { Button, buttonVariants, SegmentedControl, StatusBanner, Textarea } from "./ui"
import { Markdown } from "./markdown"
import { cn } from "@/lib/cn"
import { SessionInput } from "./session-input"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { callTool, extractText, type Session, type Target } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import {
  joinWorkspacePath,
  workspaceRelativePath,
  workspaceRootPath,
} from "@/lib/workspace-path"
import { listDirectoryCommand, parseDirectoryListing } from "@/lib/files-listing"
import {
  assembleReadChunkCommand,
  assembleStatCommand,
  assembleUploadCommand,
  decodeBase64,
  encodeBase64Chunk,
  MAX_TRANSFER_BYTES,
  parseSentinelBlock,
  parseStatBlock,
  parseUploadReceipt,
  partFileName,
  planUploadParts,
  randomUploadId,
  sha256Hex,
  shq,
  TRANSFER_CHUNK_BYTES,
  UPLOAD_PARTS_DIR,
  B64_BEGIN,
  B64_END,
} from "@/lib/files-transfer"
import { appendShellOutput, shellResultOutcome } from "@/lib/shell-blocks"
import {
  cleanResultText,
  cleanWorkspaceRequest,
  EMPTY_MAINTENANCE_LOG,
  maintenanceFailure,
  maintenanceOutput,
  type MaintenanceLog,
} from "@/lib/files-maintenance"
import { formatBytes, friendlyFileError } from "@/lib/files-errors"
import {
  FileIcon,
  FolderIcon,
  SaveIcon,
  RefreshIcon,
  PlusIcon,
  ChevronRightIcon,
  UploadIcon,
  FolderUpIcon,
  CopyIcon,
  CheckIcon,
  TrashIcon,
  DownloadIcon,
  PencilIcon,
  FolderPlusIcon,
} from "./icons"
import { useAppDialog } from "./app-dialog"

interface Entry {
  name: string
  isDir: boolean
}

interface UploadProgress {
  total: number
  done: number
  current: string
  detail: string
  failed: number
  active: boolean
  stopping: boolean
}

interface UploadItem {
  file: File
  rel: string
  path: string
}

function parentDir(dir: string) {
  const trimmed = dir.replace(/\/+$/, "")
  const idx = trimmed.lastIndexOf("/")
  return idx <= 0 ? "/" : trimmed.slice(0, idx)
}

// 256 KiB 以内的文本文件直接 file_write 一次写入
const MAX_INLINE_BYTES = 256 * 1024
const TEXT_EXT = /\.(txt|md|json|ya?ml|toml|ini|conf|cfg|env|sh|bash|zsh|py|js|ts|tsx|jsx|go|rs|java|c|h|cpp|cs|rb|php|sql|html|css|scss|xml|csv|log|gitignore|dockerfile|properties|lock)$/i

function looksTextual(file: File) {
  if (file.size > MAX_INLINE_BYTES) return false
  if (TEXT_EXT.test(file.name)) return true
  // 无扩展名的小文件（如 Dockerfile、Makefile）也按文本尝试
  return !/\.[a-z0-9]+$/i.test(file.name) || file.type.startsWith("text/") || file.type === "application/json"
}

interface FilesWorkspaceCache {
  cwd: string
  openPath: string | null
  content: string
  dirty: boolean
  drafts: Map<string, string>
}

const filesCacheBySession = new WeakMap<object, Map<string, FilesWorkspaceCache>>()

function getFilesWorkspaceCache(session: Session, targetKey: string, sessionId: string): FilesWorkspaceCache {
  let byScope = filesCacheBySession.get(session)
  if (!byScope) {
    byScope = new Map()
    filesCacheBySession.set(session, byScope)
  }
  const key = `${targetKey}|${sessionId}`
  let cache = byScope.get(key)
  if (!cache) {
    cache = { cwd: workspaceRootPath(sessionId) || "", openPath: null, content: "", dirty: false, drafts: new Map() }
    byScope.set(key, cache)
  }
  return cache
}

export function FilesPanel({
  session,
  target,
  sessionId,
  onSessionChange,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  onSessionChange?: (id: string) => void
  onRunningChange?: (running: boolean) => void
}) {
  const cache = useMemo(() => getFilesWorkspaceCache(session, target.key, sessionId), [session, target.key, sessionId])
  const [cwd, setCwd] = useState(() => cache.cwd || workspaceRootPath(sessionId) || "")
  const workspaceRoot = workspaceRootPath(sessionId)
  const [entries, setEntries] = useState<Entry[]>([])
  const [listBusy, setListBusy] = useState(false)
  const [listErr, setListErr] = useState("")

  const [openPath, setOpenPath] = useState<string | null>(() => cache.openPath)
  const [content, setContent] = useState(() => cache.content)
  const [dirty, setDirty] = useState(() => cache.dirty)
  const drafts = useRef(cache.drafts)
  drafts.current = cache.drafts
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<{ kind: "ok" | "err"; text: string } | null>(null)

  const [upload, setUpload] = useState<UploadProgress | null>(null)
  const [showCmd, setShowCmd] = useState(false)
  const [copied, setCopied] = useState(false)
  const [maint, setMaint] = useState<({ running: boolean } & MaintenanceLog) | null>(null)
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const [preview, setPreview] = useState(false)
  const [retryItems, setRetryItems] = useState<UploadItem[]>([])
  const uploadAbort = useRef<AbortController | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dirInputRef = useRef<HTMLInputElement>(null)
  const { prompt, confirm, dialog } = useAppDialog()

  useEffect(() => {
    cache.cwd = cwd
    cache.openPath = openPath
    cache.content = content
    cache.dirty = dirty
  }, [cache, cwd, openPath, content, dirty])

  const activeOp = Boolean(busy || upload?.active || maint?.running)
  // Uploads, file operations and workspace cleanup write the same tree; only one runs at a time.
  const locked = busy || Boolean(upload?.active) || Boolean(maint?.running)
  // 草稿只存在内存里的 Map，改完要手动触发一次重渲染才能出现在列表里。
  const [draftTick, setDraftTick] = useState(0)
  const touchDrafts = useCallback(() => setDraftTick((tick) => tick + 1), [])
  // What the editor shows now, for a save that returns after more typing.
  const editor = useRef({ openPath, content })
  editor.current = { openPath, content }
  const listRequest = useRef(0)
  useEffect(() => {
    onRunningChange?.(activeOp)
    return () => onRunningChange?.(false)
  }, [activeOp, onRunningChange])

  const list = useCallback(
    async (dir: string) => {
      const request = ++listRequest.current
      const stale = () => request !== listRequest.current
      setListBusy(true)
      setListErr("")
      let buf = ""
      try {
        const safeDir = workspaceRelativePath(sessionId, dir)
        const safeRoot = workspaceRootPath(sessionId)
        if (safeDir === null || !safeRoot) {
          setEntries([])
          setListErr("工作区路径无效")
          return
        }
        const remoteDir = safeDir ? `${safeRoot}/${safeDir}` : safeRoot
        await callTool(
          session,
          {
            cluster: target.cluster,
            instance: target.instance,
            tool: TOOLS.shell,
            arguments: { session_id: sessionId, command: listDirectoryCommand(safeRoot, remoteDir) },
          },
          (ev) => {
            if (ev.type === "output") buf += ev.data
            // doops_shell streams stdout as output; the final result is only a
            // generic completion receipt and must not become a directory entry.
            else if (ev.type === "error" && !stale()) setListErr(ev.error)
          },
        )
        if (stale()) return
        const parsed = parseDirectoryListing(buf)
        if (parsed.error) setListErr(parsed.error)
        setEntries(parsed.entries)
        setCwd(remoteDir)
      } catch (err) {
        if (!stale()) setListErr(friendlyFileError((err as Error).message))
      } finally {
        if (!stale()) setListBusy(false)
      }
    },
    [session, target, sessionId],
  )

  useEffect(() => {
    list(cache.cwd || workspaceRoot || "")
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId])

  async function openFile(path: string) {
    const savedDraft = drafts.current.get(path)
    if (savedDraft !== undefined) {
      setOpenPath(path)
      setContent(savedDraft)
      setDirty(true)
      setStatus(null)
      return
    }
    if (locked) return
    setBusy(true)
    setStatus(null)
    let buf = ""
    // 读取失败时不能进入编辑态：否则会用空/半截内容覆盖远端原文件。
    let readError = ""
    try {
      const relativePath = workspaceRelativePath(sessionId, path)
      if (relativePath === null || !relativePath) {
        setStatus({ kind: "err", text: "文件路径必须位于当前工作区内" })
        return
      }
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.fileRead,
          arguments: { session_id: sessionId, path: relativePath },
        },
        (ev) => {
          if (ev.type === "output") buf += ev.data
          else if (ev.type === "result") buf += extractText(ev.result)
          else if (ev.type === "error") readError = ev.error
        },
      )
      if (readError) {
        setStatus({ kind: "err", text: friendlyFileError(readError) })
        return
      }
      const previous = editor.current
      if (previous.openPath !== path && drafts.current.get(path) === undefined && dirty && previous.openPath) {
        // 带未保存修改切换文件时，先把当前编辑内容存成草稿，避免静默丢失。
        drafts.current.set(previous.openPath, previous.content)
        touchDrafts()
      }
      setOpenPath(path)
      setContent(buf)
      setDirty(false)
    } catch (err) {
      setStatus({ kind: "err", text: friendlyFileError((err as Error).message) })
    } finally {
      setBusy(false)
    }
  }

  async function save() {
    if (!openPath || locked) return
    const savedPath = openPath
    const saved = content
    setBusy(true)
    setStatus(null)
    try {
      const relativePath = workspaceRelativePath(sessionId, openPath)
      if (relativePath === null || !relativePath) {
        setStatus({ kind: "err", text: "文件路径必须位于当前工作区内" })
        return
      }
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.fileWrite,
          arguments: { session_id: sessionId, path: relativePath, content },
        },
        (ev) => {
          if (ev.type === "error") setStatus({ kind: "err", text: friendlyFileError(ev.error) })
          else if (ev.type === "result") {
            // Text typed while the write was in flight stays an unsaved draft.
            const current = editor.current
            const unchanged = current.openPath !== savedPath || current.content === saved
            if (drafts.current.get(savedPath) === saved) drafts.current.delete(savedPath)
            if (current.openPath === savedPath && unchanged) setDirty(false)
            setStatus({ kind: "ok", text: unchanged ? "写入成功" : "已写入保存时的内容，之后的修改尚未保存" })
          }
        },
      )
    } catch (err) {
      setStatus({ kind: "err", text: friendlyFileError((err as Error).message) })
    } finally {
      setBusy(false)
    }
  }

  async function newFile() {
    if (locked) return
    const name = await prompt({
      title: "新建文件",
      message: "文件名相对于当前目录，保存后才会真正创建。",
      label: "文件名",
      placeholder: "notes.txt",
      confirmLabel: "创建",
    })
    if (!name) return
    const path = joinWorkspacePath(sessionId, cwd, name)
    if (!path) {
      setStatus({ kind: "err", text: "文件名必须位于当前工作区内" })
      return
    }
    drafts.current.set(path, "")
    touchDrafts()
    setOpenPath(path)
    setContent("")
    setDirty(true)
    setStatus({ kind: "ok", text: "草稿已创建，输入内容后点击保存才会写入远端" })
  }

  // 返回是否成功；callTool 的 error 事件和随后抛出的同一错误只记一次。
  async function runClean(args: Record<string, string>): Promise<boolean> {
    setMaint({ ...EMPTY_MAINTENANCE_LOG, running: true })
    let failed = false
    const fail = (raw: string) => {
      failed = true
      setMaint((p) => (p ? { ...p, ...maintenanceFailure(p, raw) } : p))
    }
    try {
      await callTool(
        session,
        {
          cluster: target.cluster,
          instance: target.instance,
          tool: TOOLS.cleanWorkspace,
          arguments: args,
        },
        (ev) => {
          if (ev.type === "output") {
            setMaint((p) => (p ? { ...p, ...maintenanceOutput(p, ev.data) } : p))
          } else if (ev.type === "result") {
            const text = cleanResultText(extractText(ev.result))
            setMaint((p) => (p ? { ...p, ...maintenanceOutput(p, text) } : p))
          } else if (ev.type === "error") {
            fail(ev.error)
          }
        },
      )
    } catch (err) {
      fail((err as Error).message)
    } finally {
      setMaint((p) => (p ? { ...p, running: false } : p))
    }
    return !failed
  }

  async function cleanWorkspace() {
    if (locked) return
    const confirmed = await confirm({
      title: "清理整个工作区？",
      message: `会永久删除 ${workspaceRoot || "当前工作区"} 下的全部文件、Git 裸仓库和未保存草稿，不能撤销。`,
      confirmLabel: "清理工作区",
      danger: true,
    })
    if (!confirmed) return
    let args: Record<string, string>
    try {
      args = cleanWorkspaceRequest(sessionId)
    } catch (err) {
      setMaint({ lines: [], errors: [(err as Error).message], running: false })
      return
    }
    if (await runClean(args)) {
      // 工作区已整体删除：不再展示旧目录和已打开的文件，也不重新列举（列根目录会重建空工作区）。
      drafts.current.clear()
      setEntries([])
      setListErr("")
      setCwd(workspaceRoot || "")
      setOpenPath(null)
      setContent("")
      setDirty(false)
      setStatus(null)
    } else {
      list(cwd)
    }
  }

  // 收集一次 doops_shell 调用的 stdout 与错误；result 里的启动失败也算失败。
  async function runShell(command: string, signal?: AbortSignal): Promise<{ output: string; error: string }> {
    const state = { output: "", progress: "" }
    let error = ""
    await callTool(
      session,
      {
        cluster: target.cluster,
        instance: target.instance,
        tool: TOOLS.shell,
        arguments: { session_id: sessionId, command },
        signal,
      },
      (ev) => {
        if (ev.type === "output") {
          const next = appendShellOutput(state, ev.data)
          state.output = next.output
          state.progress = next.progress
        } else if (ev.type === "result") {
          const outcome = shellResultOutcome(extractText(ev.result))
          if (!outcome.ok) error = outcome.text
        } else if (ev.type === "error") {
          error = ev.error
        }
      },
    )
    return { output: state.output, error }
  }

  // 通过 doops_file_write 逐个写入文件，子目录先 mkdir -p
  async function writeRemoteFile(path: string, text: string, signal?: AbortSignal) {
    const relativePath = workspaceRelativePath(sessionId, path)
    if (relativePath === null || !relativePath) throw new Error("上传路径必须位于当前工作区内")
    let err: string | null = null
    await callTool(
      session,
      {
        cluster: target.cluster,
        instance: target.instance,
        tool: TOOLS.fileWrite,
        arguments: { session_id: sessionId, path: relativePath, content: text },
        signal,
      },
      (ev) => {
        if (ev.type === "error") err = ev.error
      },
    )
    if (err) throw new Error(err)
  }

  async function mkdirRemote(dir: string, signal?: AbortSignal) {
    const safeDir = workspaceRelativePath(sessionId, dir)
    const safeRoot = workspaceRootPath(sessionId)
    if (safeDir === null || !safeRoot) throw new Error("上传目录必须位于当前工作区内")
    const remoteDir = safeDir ? `${safeRoot}/${safeDir}` : safeRoot
    let err = ""
    await callTool(
      session,
      {
        cluster: target.cluster,
        instance: target.instance,
        tool: TOOLS.shell,
        arguments: { session_id: sessionId, command: `mkdir -p ${shq(remoteDir)}` },
        signal,
      },
      (ev) => {
        if (ev.type === "error") err = ev.error
      },
    )
    if (err) throw new Error(err)
  }

  // 大文件/二进制：base64 分片经 doops_file_write 暂存，再由 doops_shell 组装并校验 SHA-256
  async function uploadChunked(file: File, dest: string, signal?: AbortSignal) {
    const safeRoot = workspaceRootPath(sessionId)
    if (!safeRoot) throw new Error("工作区路径无效")
    const buffer = await file.arrayBuffer()
    const sha256 = await sha256Hex(buffer)
    const bytes = new Uint8Array(buffer)
    const uploadId = randomUploadId()
    const partsDir = `${safeRoot}/${UPLOAD_PARTS_DIR}/${uploadId}`
    const total = planUploadParts(file.size)
    for (let i = 0; i < total; i++) {
      if (signal?.aborted) throw new Error("已取消")
      setUpload((p) => (p ? { ...p, detail: `分片 ${i + 1}/${total}` } : p))
      const chunk = encodeBase64Chunk(bytes.subarray(i * TRANSFER_CHUNK_BYTES, Math.min((i + 1) * TRANSFER_CHUNK_BYTES, bytes.length)))
      await writeRemoteFile(`${partsDir}/${partFileName(i)}`, chunk, signal)
    }
    if (signal?.aborted) throw new Error("已取消")
    setUpload((p) => (p ? { ...p, detail: "校验中…" } : p))
    const res = await runShell(assembleUploadCommand({ root: safeRoot, partsDir, target: dest, sha256 }), signal)
    if (res.error) throw new Error(res.error)
    const receipt = parseUploadReceipt(res.output, sha256)
    if (!receipt.ok) throw new Error(receipt.error)
  }

  // 一键上传：小文本直接 file_write，其余文件（≤64 MiB）走 base64 分片；失败或中止的留在 pending 里可重试。
  async function handleUpload(fileList: FileList | null) {
    if (!fileList || fileList.length === 0) return
    // The picker keeps its selection; reset it so the same files can be chosen again.
    // Copy first: clearing input.value empties the live FileList in Chromium, so reading
    // Array.from(fileList) afterwards would yield zero files.
    const picked = Array.from(fileList)
    if (fileInputRef.current) fileInputRef.current.value = ""
    if (dirInputRef.current) dirInputRef.current.value = ""
    if (locked) return
    const items: UploadItem[] = []
    for (const file of picked) {
      const rel = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
      const path = joinWorkspacePath(sessionId, cwd, rel)
      if (!path) {
        setStatus({ kind: "err", text: "上传路径必须位于当前工作区内" })
        return
      }
      items.push({ file, rel, path })
    }
    setRetryItems([])
    await uploadBatch(items)
  }

  function stopUpload() {
    uploadAbort.current?.abort()
    setUpload((p) => (p ? { ...p, stopping: true, detail: "正在停止…" } : p))
  }

  async function retryUpload() {
    if (locked || retryItems.length === 0) return
    const items = retryItems
    setRetryItems([])
    await uploadBatch(items)
  }

  // 逐批上传：目录先建，单个文件失败不影响其他文件，没完成的部分留给「继续上传」。
  async function uploadBatch(items: UploadItem[]) {
    const controller = new AbortController()
    uploadAbort.current = controller
    const signal = controller.signal
    setStatus(null)
    setUpload({ total: items.length, done: 0, current: "", detail: "", failed: 0, active: true, stopping: false })

    const dirsToMake = new Set<string>()
    for (const item of items) {
      const slash = item.rel.lastIndexOf("/")
      if (slash > 0) {
        const dir = joinWorkspacePath(sessionId, cwd, item.rel.slice(0, slash))
        if (dir) dirsToMake.add(dir)
      }
    }
    // 浅的先建，深目录的父目录一定已经存在
    for (const dir of Array.from(dirsToMake).sort((a, b) => a.length - b.length)) {
      if (signal.aborted) break
      try {
        await mkdirRemote(dir, signal)
      } catch {
        /* 写文件时若目录仍然缺失会再报一次，并计入失败 */
      }
    }

    let done = 0
    let ok = 0
    let failed = 0
    const reasons: string[] = []
    const pending: UploadItem[] = []
    for (const item of items) {
      if (signal.aborted) {
        pending.push(item)
        continue
      }
      setUpload((p) => (p ? { ...p, current: item.rel, detail: "" } : p))
      if (!looksTextual(item.file) && item.file.size > MAX_TRANSFER_BYTES) {
        pending.push(item)
        failed++
        done++
        reasons.push(`${item.rel}：超过 ${formatBytes(MAX_TRANSFER_BYTES)}，请用「上传命令」走 doops push`)
        setUpload((p) => (p ? { ...p, done, failed } : p))
        continue
      }
      try {
        if (looksTextual(item.file)) {
          await writeRemoteFile(item.path, await item.file.text(), signal)
        } else {
          await uploadChunked(item.file, item.path, signal)
        }
        ok++
      } catch (err) {
        pending.push(item)
        // 用户主动停止不算失败，也不必再报一次错
        if (!signal.aborted) {
          failed++
          reasons.push(`${item.rel}：${friendlyFileError((err as Error).message)}`)
        }
      }
      done++
      setUpload((p) => (p ? { ...p, done, failed } : p))
    }

    uploadAbort.current = null
    setRetryItems(pending)
    setUpload((p) => (p ? { ...p, active: false, stopping: false, current: "", detail: "" } : p))
    const summary = reasons.slice(0, 3).join("；") + (reasons.length > 3 ? ` 等 ${reasons.length} 个` : "")
    if (signal.aborted) {
      setStatus({
        kind: pending.length === 0 ? "ok" : "err",
        text: `已停止上传：成功 ${ok}，未完成 ${pending.length}。${pending.length > 0 ? "可点「继续上传」重试。" : ""}`,
      })
    } else if (pending.length > 0) {
      setStatus({
        kind: ok > 0 ? "ok" : "err",
        text: `上传结束：成功 ${ok}，失败 ${failed}。${summary}`,
      })
    } else {
      setStatus({ kind: "ok", text: `上传完成：成功 ${ok}。` })
    }
    await list(cwd)
  }

  // 下载：先取大小和 SHA-256，再分块拉 base64，拼装校验一致才落盘。
  async function downloadFile(path: string, name: string) {
    if (locked) return
    setBusy(true)
    setStatus(null)
    try {
      const statRes = await runShell(assembleStatCommand(path))
      if (statRes.error) throw new Error(statRes.error)
      const stat = parseStatBlock(statRes.output)
      if (!stat) throw new Error("无法读取远端文件信息")
      if (stat.size > MAX_TRANSFER_BYTES) {
        setStatus({ kind: "err", text: `${name} 超过 64 MiB，请用 doops pull 下载` })
        return
      }
      const bytes = new Uint8Array(stat.size)
      for (let offset = 0; offset < stat.size; offset += TRANSFER_CHUNK_BYTES) {
        const length = Math.min(TRANSFER_CHUNK_BYTES, stat.size - offset)
        const res = await runShell(assembleReadChunkCommand(path, offset, length))
        if (res.error) throw new Error(res.error)
        const block = parseSentinelBlock(res.output, B64_BEGIN, B64_END)
        if (block === null) throw new Error("下载数据块缺失")
        const chunk = decodeBase64(block)
        if (chunk.length !== length) throw new Error("下载数据块长度不一致")
        bytes.set(chunk, offset)
      }
      const sha256 = await sha256Hex(bytes)
      if (sha256 !== stat.sha256) throw new Error("下载内容与远端 SHA-256 不一致，已取消保存")
      const url = URL.createObjectURL(new Blob([bytes]))
      const anchor = document.createElement("a")
      anchor.href = url
      anchor.download = name
      anchor.click()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
      setStatus({ kind: "ok", text: `已下载 ${name}` })
    } catch (err) {
      setStatus({ kind: "err", text: friendlyFileError((err as Error).message) })
    } finally {
      setBusy(false)
    }
  }

  // 打开文件/草稿在删除或改名后同步失效或迁移
  function closeIfUnder(path: string) {
    if (openPath === path || openPath?.startsWith(`${path}/`)) {
      setOpenPath(null)
      setContent("")
      setDirty(false)
    }
  }

  function moveDraftKey(from: string, to: string) {
    for (const key of Array.from(drafts.current.keys())) {
      if (key === from || key.startsWith(`${from}/`)) {
        drafts.current.set(to + key.slice(from.length), drafts.current.get(key)!)
        drafts.current.delete(key)
      }
    }
  }

  async function deleteEntry(path: string, name: string, isDir: boolean) {
    if (locked) return
    const relative = workspaceRelativePath(sessionId, path)
    if (relative === null || !relative) {
      setStatus({ kind: "err", text: "只能删除当前工作区内的文件或目录" })
      return
    }
    // rm -rf 不可恢复，删除前必须显式确认。
    const confirmed = await confirm({
      title: isDir ? `删除目录 ${name}？` : `删除文件 ${name}？`,
      message: isDir
        ? "目录及其全部内容会被永久删除，这个操作不能撤销。"
        : "文件会被永久删除，这个操作不能撤销。",
      confirmLabel: "删除",
      danger: true,
    })
    if (!confirmed) return
    setBusy(true)
    setStatus(null)
    try {
      const res = await runShell(`rm -rf -- ${shq(path)}`)
      if (res.error) throw new Error(res.error)
      for (const key of Array.from(drafts.current.keys())) {
        if (key === path || key.startsWith(`${path}/`)) drafts.current.delete(key)
      }
      touchDrafts()
      closeIfUnder(path)
      setStatus({ kind: "ok", text: `已删除 ${name}` })
      await list(cwd)
    } catch (err) {
      setStatus({ kind: "err", text: friendlyFileError((err as Error).message) })
    } finally {
      setBusy(false)
    }
  }

  // 丢弃本地草稿：只清内存里的内容，不碰远端文件。
  function discardDraft(path: string, name: string) {
    drafts.current.delete(path)
    touchDrafts()
    closeIfUnder(path)
    setStatus({ kind: "ok", text: `已丢弃草稿 ${name}` })
  }

  async function renameEntry(path: string, name: string) {
    if (locked) return
    const input = await prompt({
      title: "重命名",
      message: "输入新的名称（单个路径段，不含 /）。",
      label: "新名称",
      defaultValue: name,
      confirmLabel: "重命名",
    })
    const newName = input?.trim()
    if (!newName || newName === name) return
    const newPath = joinWorkspacePath(sessionId, cwd, newName)
    if (!newPath || newName.includes("/")) {
      setStatus({ kind: "err", text: "名称必须是当前目录下的单个路径段" })
      return
    }
    setBusy(true)
    setStatus(null)
    try {
      const res = await runShell(
        `if [ -e ${shq(newPath)} ]; then echo "rename failed: target exists"; exit 1; fi; mv -n -- ${shq(path)} ${shq(newPath)}`,
      )
      if (res.output.includes("rename failed: target exists")) throw new Error(`目标 ${newName} 已存在`)
      if (res.error) throw new Error(friendlyFileError(res.error))
      if (openPath === path || openPath?.startsWith(`${path}/`)) {
        setOpenPath(newPath + (openPath === path ? "" : openPath.slice(path.length)))
      }
      moveDraftKey(path, newPath)
      touchDrafts()
      setStatus({ kind: "ok", text: `已重命名为 ${newName}` })
      await list(cwd)
    } catch (err) {
      setStatus({ kind: "err", text: friendlyFileError((err as Error).message) })
    } finally {
      setBusy(false)
    }
  }

  async function newFolder() {
    if (locked) return
    const input = await prompt({
      title: "新建目录",
      message: "目录名相对于当前目录。",
      label: "目录名",
      placeholder: "docs",
      confirmLabel: "创建",
    })
    const name = input?.trim()
    if (!name) return
    const path = joinWorkspacePath(sessionId, cwd, name)
    if (!path || name.includes("/")) {
      setStatus({ kind: "err", text: "目录名必须是当前目录下的单个路径段" })
      return
    }
    setBusy(true)
    setStatus(null)
    try {
      const res = await runShell(`mkdir -p -- ${shq(path)}`)
      if (res.error) throw new Error(friendlyFileError(res.error))
      setStatus({ kind: "ok", text: `已创建目录 ${name}` })
      await list(cwd)
    } catch (err) {
      setStatus({ kind: "err", text: friendlyFileError((err as Error).message) })
    } finally {
      setBusy(false)
    }
  }

  const pushCommand = `doops -session ${sessionId} push \\
  --target <configured-target> \\
  --src ./your-folder`

  async function copyCommand() {
    try {
      await navigator.clipboard.writeText(pushCommand.replace(/\\\n\s*/g, " "))
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    } catch {
      setStatus({ kind: "err", text: "复制失败，请手动选择命令复制" })
    }
  }

  // Markdown 文件额外提供预览：编辑器里的原文和渲染结果可随时切换。
  const isMarkdown = Boolean(openPath && /\.(md|markdown|mdx)$/i.test(openPath))
  useEffect(() => {
    setPreview(false)
  }, [openPath])

  // 整页拖放上传：拖到面板任意位置都落到当前目录。
  function hasFiles(e: React.DragEvent) {
    return Array.from(e.dataTransfer.types).includes("Files")
  }
  function onDragEnter(e: React.DragEvent) {
    if (!hasFiles(e)) return
    e.preventDefault()
    dragDepth.current += 1
    setDragging(true)
  }
  function onDragOver(e: React.DragEvent) {
    if (!hasFiles(e)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = locked ? "none" : "copy"
  }
  function onDragLeave() {
    // dragleave 的 dataTransfer.types 常常是空的，不能用它判断，只能按计数递减。
    if (dragDepth.current === 0) return
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) setDragging(false)
  }
  function onDrop(e: React.DragEvent) {
    if (!hasFiles(e)) return
    e.preventDefault()
    dragDepth.current = 0
    setDragging(false)
    if (locked) {
      setStatus({ kind: "err", text: "正在执行其他操作，完成后再拖入文件" })
      return
    }
    void handleUpload(e.dataTransfer.files)
  }

  const crumbs = (workspaceRelativePath(sessionId, cwd) || "").split("/").filter(Boolean)
  // 只在本文件里新建、还没保存的草稿，远端目录里没有它，要单独列出来。
  const normalizedCwd = cwd.replace(/\/+$/, "") || "/"
  const listingNames = useMemo(() => new Set(entries.map((e) => e.name)), [entries])
  const draftRows = useMemo(() => {
    void draftTick
    const rows: { name: string; path: string }[] = []
    for (const path of drafts.current.keys()) {
      if (parentDir(path) !== normalizedCwd) continue
      const name = path.slice(path.lastIndexOf("/") + 1)
      if (!name || listingNames.has(name)) continue
      rows.push({ name, path })
    }
    return rows.sort((a, b) => a.name.localeCompare(b.name))
  }, [draftTick, drafts, normalizedCwd, listingNames])

  return (
    <div
      className="relative flex h-full min-h-0 flex-col"
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
    >
      {/* 上传工具条（含 session） */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-card/60 px-4 py-2">
        {onSessionChange ? (
          <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
            session
            <SessionInput value={sessionId} disabled={activeOp} onCommit={onSessionChange} className="w-40 font-mono" />
          </label>
        ) : (
          <span className="text-xs font-medium text-muted-foreground">上传到</span>
        )}
        <code className="hidden rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground sm:inline">
          {cwd}
        </code>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          {/* label + 原生 file input：点击区域由浏览器自己激活，不依赖脚本调用 input.click() */}
          <label
            className={cn(buttonVariants({ size: "sm" }), "relative focus-within:ring-2 focus-within:ring-ring", locked && "pointer-events-none opacity-50")}
            aria-disabled={locked ? "true" : undefined}
            title="选择文件上传，也可以把文件直接拖进本页"
          >
            <UploadIcon width={14} height={14} /> 上传文件
            <input
              ref={fileInputRef}
              type="file"
              multiple
              aria-label="上传文件"
              className="sr-only"
              disabled={locked}
              onChange={(e) => handleUpload(e.target.files)}
            />
          </label>
          <label
            className={cn(buttonVariants({ size: "sm" }), "relative focus-within:ring-2 focus-within:ring-ring", locked && "pointer-events-none opacity-50")}
            aria-disabled={locked ? "true" : undefined}
            title="选择一个文件夹，按原有目录结构上传"
          >
            <FolderUpIcon width={14} height={14} /> 上传文件夹
            <input
              ref={dirInputRef}
              type="file"
              aria-label="上传文件夹"
              className="sr-only"
              // @ts-expect-error 非标准但被主流浏览器支持
              webkitdirectory=""
              directory=""
              multiple
              disabled={locked}
              onChange={(e) => handleUpload(e.target.files)}
            />
          </label>
          <Button
            onClick={() => setShowCmd((v) => !v)}
            size="sm"
            className={showCmd ? "border-primary/50" : ""}
          >
            <CopyIcon width={14} height={14} /> 上传命令
          </Button>
          <span className="mx-1 h-4 w-px bg-border" aria-hidden />
          <Button
            onClick={cleanWorkspace}
            disabled={maint?.running}
            title="删除本会话的整个工作区和 Git 裸仓库"
            size="sm"
          >
            <TrashIcon width={14} height={14} /> 清理工作区
          </Button>
        </div>
      </div>

      {/* 维护动作结果 */}
      {maint && (
        <div className="shrink-0 border-b bg-muted/30 px-3 py-3">
          <div className="mb-1.5 flex items-center justify-between">
            <p className="flex items-center gap-1.5 text-xs font-medium text-foreground">
              <TrashIcon width={13} height={13} className="text-primary" />
              工作区清理
              {maint.running && <span className="text-muted-foreground">· 执行中…</span>}
            </p>
            {!maint.running && (
              <Button
                onClick={() => setMaint(null)}
                size="sm"
              >
                关闭
              </Button>
            )}
          </div>
          <pre className="max-h-44 overflow-y-auto whitespace-pre-wrap rounded-lg border bg-background p-3 font-mono text-xs leading-relaxed text-foreground">
            {maint.lines.join("") || (maint.running ? "正在执行…" : maint.errors.length ? "" : "已完成，无输出")}
            {maint.errors.map((e, i) => (
              <span key={i} className="block text-destructive">
                错误：{e}
              </span>
            ))}
          </pre>
        </div>
      )}

      {/* push 命令面板 */}
      {showCmd && (
        <div className="shrink-0 border-b bg-muted/30 px-3 py-3">
          <div className="mb-1.5 flex items-center justify-between">
            <p className="text-xs text-muted-foreground">
              大文件 / 整个目录用 CLI 极速同步（Git HTTP），上传到 <code className="font-mono">{workspaceRootPath(sessionId) || "—"}</code>
            </p>
            <Button
              onClick={copyCommand}
              variant="primary" size="sm"
            >
              {copied ? <CheckIcon width={13} height={13} /> : <CopyIcon width={13} height={13} />}
              {copied ? "已复制" : "复制命令"}
            </Button>
          </div>
          <pre className="overflow-x-auto rounded-lg border bg-background p-3 font-mono text-xs leading-relaxed text-foreground">
            {pushCommand}
          </pre>
        </div>
      )}

      {/* 上传进度 */}
      {upload && (
        <div className="shrink-0 border-b bg-card px-3 py-2">
          <div className="flex items-center justify-between gap-2 text-xs">
            <span className="text-foreground">
              {upload.active ? (upload.stopping ? "正在停止…" : "上传中…") : "上传结束"} {upload.done}/{upload.total}
              {upload.failed > 0 && <span className="ml-1 text-destructive">（{upload.failed} 失败/跳过）</span>}
            </span>
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate font-mono text-muted-foreground">
                {upload.current}
                {upload.detail && ` ${upload.detail}`}
              </span>
              {upload.active && (
                <Button onClick={stopUpload} disabled={upload.stopping} variant="ghost" size="sm">
                  {upload.stopping ? "停止中…" : "停止"}
                </Button>
              )}
              {!upload.active && retryItems.length > 0 && (
                <Button onClick={() => void retryUpload()} disabled={locked} variant="ghost" size="sm">
                  继续上传 {retryItems.length} 个
                </Button>
              )}
            </span>
          </div>
          <div className="progress-track mt-1.5 h-1.5">
            <div
              className="progress-fill"
              style={{ width: `${upload.total ? (upload.done / upload.total) * 100 : 0}%` }}
            />
          </div>
        </div>
      )}

      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        {/* 文件浏览器 */}
        <aside className="flex max-h-52 w-full shrink-0 flex-col border-b bg-card/40 md:max-h-none md:w-64 md:border-r md:border-b-0">
          <div className="flex items-center justify-between gap-1 border-b px-3 py-2">
            <span className="text-xs font-medium text-muted-foreground">文件浏览器</span>
            <div className="flex items-center gap-1">
              <Button
                onClick={newFile}
                title="新建文件"
                aria-label="新建文件"
                variant="ghost"
                size="sm"
                icon
              >
                <PlusIcon width={15} height={15} />
              </Button>
              <Button
                onClick={newFolder}
                title="新建目录"
                aria-label="新建目录"
                variant="ghost"
                size="sm"
                icon
                disabled={locked}
              >
                <FolderPlusIcon width={15} height={15} />
              </Button>
              <Button
                onClick={() => list(cwd)}
                title="刷新"
                aria-label="刷新"
                variant="ghost"
                size="sm"
                icon
              >
                <RefreshIcon width={15} height={15} className={listBusy ? "animate-spin" : ""} />
              </Button>
            </div>
          </div>

          <div className="flex items-center gap-0.5 overflow-x-auto border-b px-2 py-1.5 text-xs text-muted-foreground">
            <button onClick={() => list(workspaceRoot || "")} className="shrink-0 hover:text-foreground">
              {sessionId}
            </button>
            {crumbs.map((c, i) => {
              const path = `${workspaceRoot}/${crumbs.slice(0, i + 1).join("/")}`
              return (
                <span key={path} className="flex shrink-0 items-center">
                  <span className="px-0.5">/</span>
                  <button onClick={() => list(path)} className="hover:text-foreground">
                    {c}
                  </button>
                </span>
              )
            })}
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto p-1">
            {workspaceRoot && cwd !== workspaceRoot && (
              <button
                onClick={() => list(parentDir(cwd))}
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted"
              >
                <FolderIcon width={15} height={15} /> ..
              </button>
            )}
            {listErr && <p className="px-2 py-2 text-xs text-destructive">{listErr}</p>}
            {!listErr && entries.length === 0 && !listBusy && (
              <p className="px-2 py-2 text-xs text-muted-foreground">空目录</p>
            )}
            {draftRows.map((d) => (
              <div key={`draft:${d.path}`} className="group flex items-center">
                <button
                  onClick={() => void openFile(d.path)}
                  className={`flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                    openPath === d.path ? "bg-primary/15 text-foreground" : "text-foreground/90 hover:bg-muted"
                  }`}
                >
                  <FileIcon width={15} height={15} className="shrink-0 text-muted-foreground" />
                  <span className="truncate">{d.name}</span>
                  <span className="ml-auto shrink-0 rounded bg-warning/15 px-1.5 py-0.5 text-2xs text-warning">未保存</span>
                </button>
                <span className="flex shrink-0 items-center opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    onClick={() => discardDraft(d.path, d.name)}
                    className="text-muted-foreground hover:text-destructive"
                    title="丢弃草稿"
                    aria-label={`丢弃草稿 ${d.name}`}
                  >
                    <TrashIcon width={13} height={13} />
                  </Button>
                </span>
              </div>
            ))}
            {entries.map((e) => {
              const full = joinWorkspacePath(sessionId, cwd, e.name)
              if (!full) return null
              const active = openPath === full
              return (
                <div key={e.name} className="group flex items-center">
                  <button
                    onClick={() => (e.isDir ? list(full) : openFile(full))}
                    className={`flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                      active ? "bg-primary/15 text-foreground" : "text-foreground/90 hover:bg-muted"
                    }`}
                  >
                    {e.isDir ? (
                      <FolderIcon width={15} height={15} className="shrink-0 text-primary" />
                    ) : (
                      <FileIcon width={15} height={15} className="shrink-0 text-muted-foreground" />
                    )}
                    <span className="truncate">{e.name}</span>
                    {e.isDir && (
                      <ChevronRightIcon width={13} height={13} className="ml-auto shrink-0 text-muted-foreground" />
                    )}
                  </button>
                  <span className="flex shrink-0 items-center opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
                    {!e.isDir && (
                      <Button
                        type="button"
                        size="icon-xs"
                        variant="ghost"
                        onClick={() => void downloadFile(full, e.name)}
                        disabled={locked}
                        className="text-muted-foreground"
                        title="下载"
                        aria-label={`下载 ${e.name}`}
                      >
                        <DownloadIcon width={13} height={13} />
                      </Button>
                    )}
                    <Button
                      type="button"
                      size="icon-xs"
                      variant="ghost"
                      onClick={() => void renameEntry(full, e.name)}
                      disabled={locked}
                      className="text-muted-foreground"
                      title="重命名"
                      aria-label={`重命名 ${e.name}`}
                    >
                      <PencilIcon width={13} height={13} />
                    </Button>
                    <Button
                      type="button"
                      size="icon-xs"
                      variant="ghost"
                      onClick={() => void deleteEntry(full, e.name, e.isDir)}
                      disabled={locked}
                      className="text-muted-foreground hover:text-destructive"
                      title="删除"
                      aria-label={`删除 ${e.name}`}
                    >
                      <TrashIcon width={13} height={13} />
                    </Button>
                  </span>
                </div>
              )
            })}
          </div>
        </aside>

        {/* 编辑器 */}
        <section className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div className="flex shrink-0 items-center justify-between gap-2 border-b bg-card px-3 py-2">
            <span className="truncate font-mono text-xs text-muted-foreground">
              {openPath || "未打开文件"}
              {dirty && <span className="ml-1 text-warning">●</span>}
            </span>
            <span className="flex shrink-0 items-center gap-1">
              {isMarkdown && (
                <SegmentedControl
                  value={preview ? "preview" : "edit"}
                  options={[
                    { value: "edit", label: "编辑" },
                    { value: "preview", label: "预览" },
                  ]}
                  onChange={(next) => setPreview(next === "preview")}
                  ariaLabel="查看方式"
                />
              )}
              {openPath && (
                <Button
                  onClick={() => void downloadFile(openPath, openPath.split("/").pop() || "file")}
                  disabled={locked}
                  size="sm"
                  title="下载"
                >
                  <DownloadIcon width={15} height={15} />
                </Button>
              )}
              <Button
                onClick={save}
                disabled={!openPath || busy || !dirty}
                variant="primary"
                size="sm"
              >
                <SaveIcon width={15} height={15} /> 保存
              </Button>
            </span>
          </div>

          {status && (
            <StatusBanner
              tone={status.kind === "ok" ? "success" : "error"}
              className="mx-3 mt-2"
            >
              {status.text}
            </StatusBanner>
          )}

          <div className="relative min-h-0 flex-1 p-3">
            {openPath ? (
              isMarkdown && preview ? (
                <div className="h-full overflow-auto rounded-lg border bg-background px-5 py-4">
                  {content.trim() ? (
                    <Markdown content={content} variant="document" allowImages={false} />
                  ) : (
                    <p className="text-xs text-muted-foreground">空文件</p>
                  )}
                </div>
              ) : (
                <Textarea
                  value={content}
                  onChange={(e) => {
                    const next = e.target.value
                    drafts.current.set(openPath, next)
                    setContent(next)
                    setDirty(true)
                  }}
                  spellCheck={false}
                  className="h-full w-full resize-none p-3 font-mono leading-relaxed"
                />
              )
            ) : (
              <div className="flex h-full flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed border-border text-muted-foreground">
                <FileIcon width={28} height={28} />
                <p className="text-pretty px-6 text-center text-sm">从左侧选择文件打开编辑，或用上方「上传文件/上传文件夹」选取</p>
                <p className="px-6 text-center text-xs">也可以把文件或文件夹直接拖进本页，落到当前目录</p>
                <p className="px-6 text-center text-xs">小文本走 file_write 一键上传 · 64 MiB 内自动分块校验上传 · 更大文件/目录请复制 doops push 命令</p>
              </div>
            )}
          </div>
        </section>
      </div>
      {dragging && (
        <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-background/80">
          <div className="rounded-xl border-2 border-dashed border-primary/60 bg-card px-6 py-4 text-center shadow-sm">
            <UploadIcon width={22} height={22} className="mx-auto text-primary" />
            <p className="mt-2 text-sm font-medium">
              {locked ? "正在执行其他操作，稍后再拖入" : "松开鼠标，上传到当前目录"}
            </p>
            <p className="mt-0.5 font-mono text-xs text-muted-foreground">
              {cwd} · 小文本直接写入 · ≤64 MiB 自动分块
            </p>
          </div>
        </div>
      )}
      {dialog}
    </div>
  )
}
