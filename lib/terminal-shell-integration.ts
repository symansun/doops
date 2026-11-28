import { clipTerminalTextForAi } from "./terminal-ai.ts"

export type BlockState = "prompt" | "typing" | "running" | "done" | "interrupted"

export interface TerminalMarkerLike {
  readonly line: number
  readonly isDisposed?: boolean
  dispose(): void
}

export interface TerminalBufferLineLike {
  readonly isWrapped?: boolean
  translateToString(trimRight?: boolean, startColumn?: number, endColumn?: number): string
}

export interface TerminalBufferLike {
  readonly type: "normal" | "alternate" | string
  readonly baseY: number
  readonly cursorY: number
  readonly cursorX: number
  readonly length: number
  getLine(y: number): TerminalBufferLineLike | undefined
}

export interface TerminalDisposableLike {
  dispose(): void
}

export interface TerminalLike {
  readonly buffer: {
    readonly active: TerminalBufferLike
    onBufferChange?: (listener: (buffer: TerminalBufferLike) => void) => TerminalDisposableLike
  }
  readonly parser: {
    registerOscHandler(ident: number, callback: (data: string) => boolean): TerminalDisposableLike
  }
  registerMarker(cursorYOffset?: number): TerminalMarkerLike | undefined
}

export interface TerminalBlock {
  id: string
  promptMarker?: TerminalMarkerLike
  inputMarker?: TerminalMarkerLike
  commandMarker?: TerminalMarkerLike
  endMarker?: TerminalMarkerLike
  promptLine: number
  inputStartLine: number
  inputStartCol: number
  commandLine?: number
  endLine?: number
  command: string
  cwd: string
  exitCode?: number
  startedAt?: number
  endedAt?: number
  state: BlockState
  isAiPrompt?: boolean
  aiStatus?: "pending" | "answered" | "error"
  outputTrimmedFromBuffer?: boolean
}

export interface ShellIntegrationOptions {
  maxBlocks?: number
  initialCwd?: string
  suppressCallbacks?: boolean
  now?: () => number
  onCommandStart?: (block: TerminalBlock) => void
  onCommandEnd?: (block: TerminalBlock) => void
}

const DEFAULT_MAX_BLOCKS = 500

export function parseOsc7Cwd(data: string): string | null {
  const trimmed = data.trim()
  if (!trimmed.toLowerCase().startsWith("file://")) return null
  const withoutScheme = trimmed.slice(7)
  const slashIdx = withoutScheme.indexOf("/")
  if (slashIdx < 0) return null
  const rawPath = withoutScheme.slice(slashIdx)
  try {
    return decodeURIComponent(rawPath) || "/"
  } catch {
    return rawPath || "/"
  }
}

export class ShellIntegrationTracker {
  private readonly term: TerminalLike
  private readonly maxBlocks: number
  private readonly now: () => number
  private readonly onCommandStart?: (block: TerminalBlock) => void
  private readonly onCommandEnd?: (block: TerminalBlock) => void
  private readonly disposables: TerminalDisposableLike[] = []
  private readonly listeners = new Set<() => void>()
  private _blocks: TerminalBlock[] = []
  private _cwd: string
  private _atPrompt = false
  private _inAlternateScreen = false
  private _seenOsc133 = false
  private _suppressCallbacks = false
  private _dWithoutBCount = 0
  private _promptTrackingDegraded = false
  private _nextSeq = 1

  constructor(term: TerminalLike, opts: ShellIntegrationOptions = {}) {
    this.term = term
    this.maxBlocks = Math.max(10, opts.maxBlocks ?? DEFAULT_MAX_BLOCKS)
    this._cwd = opts.initialCwd ?? ""
    this._suppressCallbacks = opts.suppressCallbacks === true
    this.now = opts.now ?? (() => Date.now())
    this.onCommandStart = opts.onCommandStart
    this.onCommandEnd = opts.onCommandEnd
    this._inAlternateScreen = term.buffer.active.type === "alternate"

    this.disposables.push(
      term.parser.registerOscHandler(133, (data) => {
        this.handleOsc133(data)
        return true
      }),
    )
    this.disposables.push(
      term.parser.registerOscHandler(7, (data) => {
        const parsed = parseOsc7Cwd(data)
        if (parsed && parsed !== this._cwd) {
          this._cwd = parsed
          this.emit()
        }
        return true
      }),
    )
    if (term.buffer.onBufferChange) {
      this.disposables.push(
        term.buffer.onBufferChange((buf) => {
          const isAlt = buf.type === "alternate"
          if (isAlt !== this._inAlternateScreen) {
            this._inAlternateScreen = isAlt
            this.emit()
          }
        }),
      )
    }
  }

  get blocks(): readonly TerminalBlock[] {
    return this._blocks
  }

  get current(): TerminalBlock | undefined {
    return this._blocks[this._blocks.length - 1]
  }

  get atPrompt(): boolean {
    if (this._inAlternateScreen) return false
    if (this._promptTrackingDegraded) return true
    return this._atPrompt
  }

  get promptTrackingDegraded(): boolean {
    return this._promptTrackingDegraded
  }

  get inAlternateScreen(): boolean {
    return this._inAlternateScreen
  }

  get seenOsc133(): boolean {
    return this._seenOsc133
  }

  get cwd(): string {
    return this._cwd
  }

  // 在刷新重放历史环形缓冲期间静音回调，防止旧 `#` 提问被重复发给 AI。
  setSuppressCallbacks(suppressed: boolean): void {
    this._suppressCallbacks = suppressed
  }

  onChange(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  dispose(): void {
    for (const d of this.disposables) {
      try { d.dispose() } catch { /* noop */ }
    }
    this.disposables.length = 0
    for (const b of this._blocks) {
      this.disposeBlockMarkers(b)
    }
    this.listeners.clear()
  }

  markBlockAiStatus(blockId: string, status: "pending" | "answered" | "error"): void {
    const found = this._blocks.find((b) => b.id === blockId)
    if (!found) return
    found.aiStatus = status
    this.emit()
  }

  // GNU Bash 对 `# 注释` 行不会展开 PS0（因此不会发出 OSC 133;C）。
  // 当用户通过 # / ？ / 自动识别在提示符发起 AI 提问时，由前端显式记一条 AI Block。
  recordAiBlock(command: string): TerminalBlock {
    const buf = this.term.buffer.active
    const absLine = buf.baseY + buf.cursorY
    const marker = this.term.registerMarker(0)
    const line = marker ? marker.line : absLine
    const block: TerminalBlock = {
      id: `blk_${this._nextSeq++}`,
      promptMarker: marker,
      inputMarker: marker,
      promptLine: line,
      inputStartLine: line,
      inputStartCol: 0,
      command: command.trim(),
      cwd: this._cwd,
      startedAt: this.now(),
      endedAt: this.now(),
      exitCode: 0,
      state: "done",
      isAiPrompt: true,
      aiStatus: "pending",
    }
    this._blocks.push(block)
    this.enforceLimit()
    this.emit()
    return block
  }

  readCurrentInput(): string {
    if (!this.atPrompt || this._promptTrackingDegraded) return ""
    const active = this.current
    if (!active) return ""
    const buf = this.term.buffer.active
    const startLine = this.resolveMarkerLine(active.inputMarker, active.inputStartLine)
    const cursorAbsLine = buf.baseY + buf.cursorY
    if (startLine < 0 || cursorAbsLine < startLine) return ""
    return this.readRangeText(startLine, active.inputStartCol, cursorAbsLine, buf.cursorX).trim()
  }

  // 读取当前提示符文本与行数，用于在本地 xterm 画布插入只读镜像块后无损重绘提示符。
  readCurrentPromptText(): { text: string; lines: number } {
    const buf = this.term.buffer.active
    if (this._inAlternateScreen) return { text: "", lines: 0 }
    if (this._seenOsc133 && !this._promptTrackingDegraded) {
      const active = this.current
      if (active && (active.state === "prompt" || active.state === "typing")) {
        const promptStart = this.resolveMarkerLine(active.promptMarker, active.promptLine)
        const inputStart = this.resolveMarkerLine(active.inputMarker, active.inputStartLine)
        if (promptStart >= 0 && inputStart >= promptStart) {
          const parts: string[] = []
          for (let y = promptStart; y <= inputStart; y++) {
            const line = buf.getLine(y)
            if (!line) continue
            const endCol = y === inputStart ? active.inputStartCol : undefined
            const piece = line.translateToString(y !== inputStart, 0, endCol)
            parts.push(piece)
          }
          const text = parts.join("\r\n")
          if (text) return { text, lines: Math.max(1, inputStart - promptStart + 1) }
        }
      }
    }
    const cursorAbsLine = buf.baseY + buf.cursorY
    const line = buf.getLine(cursorAbsLine)
    if (!line || buf.cursorX <= 0) return { text: "", lines: 0 }
    const text = line.translateToString(false, 0, buf.cursorX)
    return text ? { text, lines: 1 } : { text: "", lines: 0 }
  }

  readOutput(block: TerminalBlock, maxChars = 16_000): { text: string; trimmed: boolean; truncated: boolean } {
    if (block.commandMarker?.isDisposed || block.endMarker?.isDisposed || block.outputTrimmedFromBuffer) {
      block.outputTrimmedFromBuffer = true
      return { text: "（该命令输出已滚出终端缓冲区）", trimmed: true, truncated: false }
    }
    const startLine = this.resolveMarkerLine(block.commandMarker, block.commandLine)
    if (startLine < 0) return { text: "", trimmed: false, truncated: false }
    const buf = this.term.buffer.active
    const rawEnd = block.state === "running"
      ? buf.baseY + buf.cursorY + 1
      : this.resolveMarkerLine(block.endMarker, block.endLine)
    if (rawEnd < 0 || rawEnd <= startLine) return { text: "", trimmed: false, truncated: false }

    const lines: string[] = []
    for (let y = startLine; y < rawEnd; y++) {
      const line = buf.getLine(y)
      if (!line) continue
      const str = line.translateToString(true)
      if (line.isWrapped && lines.length > 0) {
        lines[lines.length - 1] += str
      } else {
        lines.push(str)
      }
    }
    const raw = lines.join("\n").replace(/^\n+|\n+$/g, "")
    if (!raw) return { text: "", trimmed: false, truncated: false }
    const clipped = clipTerminalTextForAi(raw, maxChars)
    return { text: clipped.text, trimmed: false, truncated: clipped.truncated }
  }

  private handleOsc133(data: string): void {
    if (this._inAlternateScreen) return
    const parts = data.split(";")
    const code = (parts[0] || "").trim().toUpperCase()
    if (!code) return
    this._seenOsc133 = true

    const buf = this.term.buffer.active
    const absLine = buf.baseY + buf.cursorY

    if (code === "A") {
      // 如果上一个 block 还在 prompt 且没执行过，复用或替换
      const last = this.current
      if (last && (last.state === "prompt" || last.state === "typing")) {
        this.disposeBlockMarkers(last)
        this._blocks.pop()
      }
      const marker = this.term.registerMarker(0)
      const promptLine = marker ? marker.line : absLine
      const block: TerminalBlock = {
        id: `blk_${this._nextSeq++}`,
        promptMarker: marker,
        promptLine,
        inputStartLine: promptLine,
        inputStartCol: 0,
        command: "",
        cwd: this._cwd,
        state: "prompt",
      }
      this._blocks.push(block)
      this.enforceLimit()
      this._atPrompt = false
      this.emit()
      return
    }

    if (code === "B") {
      this._dWithoutBCount = 0
      this._promptTrackingDegraded = false
      const inputMarker = this.term.registerMarker(0)
      const inputLine = inputMarker ? inputMarker.line : absLine
      let cur = this.current
      if (!cur || (cur.state !== "prompt" && cur.state !== "typing")) {
        const promptMarker = this.term.registerMarker(0)
        cur = {
          id: `blk_${this._nextSeq++}`,
          promptMarker,
          inputMarker,
          promptLine: promptMarker ? promptMarker.line : absLine,
          inputStartLine: inputLine,
          inputStartCol: buf.cursorX,
          command: "",
          cwd: this._cwd,
          state: "prompt",
        }
        this._blocks.push(cur)
        this.enforceLimit()
      } else {
        try { cur.inputMarker?.dispose() } catch { /* noop */ }
        cur.inputMarker = inputMarker
        cur.inputStartLine = inputLine
        cur.inputStartCol = buf.cursorX
        cur.cwd = this._cwd
      }
      this._atPrompt = true
      this.emit()
      return
    }

    if (code === "C") {
      const cur = this.current
      if (!cur || cur.state === "done" || cur.state === "running") return
      // 多行 PS1 时 A 与 B 不在同一行：必须从 B 所在的 inputMarker 行 + inputStartCol 起读命令。
      const inputStartLine = this.resolveMarkerLine(cur.inputMarker, cur.inputStartLine)
      const cmdEndLine = absLine > inputStartLine ? absLine - 1 : absLine
      cur.command = this.readRangeText(inputStartLine, cur.inputStartCol, cmdEndLine, undefined).trim()
      cur.commandMarker = this.term.registerMarker(0)
      cur.commandLine = cur.commandMarker ? cur.commandMarker.line : absLine
      cur.startedAt = this.now()
      cur.cwd = this._cwd
      cur.state = "running"
      cur.isAiPrompt = cur.command.startsWith("#")
      this._atPrompt = false
      this.emit()
      if (!this._suppressCallbacks) {
        this.onCommandStart?.(cur)
      }
      return
    }

    if (code === "D") {
      const cur = this.current
      if (!cur || cur.state !== "running") {
        // 如果从未收到过 B（例如外部 PS1 框架覆盖了 A/B），连续 >=2 次只收到 D 后降级，避免插入按钮永久置灰。
        if (!this._atPrompt && (!cur || !cur.inputMarker)) {
          this._dWithoutBCount += 1
          if (this._dWithoutBCount >= 2 && !this._promptTrackingDegraded) {
            this._promptTrackingDegraded = true
            this.emit()
          }
        }
        return
      }
      const rawExit = parts[1]
      const parsedExit = rawExit !== undefined && rawExit !== "" && /^-?\d+$/.test(rawExit.trim())
        ? Number(rawExit.trim())
        : undefined
      cur.endMarker = this.term.registerMarker(0)
      cur.endLine = cur.endMarker ? cur.endMarker.line : absLine
      cur.endedAt = this.now()
      cur.exitCode = parsedExit
      cur.state = parsedExit === 130 ? "interrupted" : "done"
      this._atPrompt = false
      this.emit()
      if (!this._suppressCallbacks) {
        this.onCommandEnd?.(cur)
      }
    }
  }

  private resolveMarkerLine(marker: TerminalMarkerLike | undefined, fallback?: number): number {
    if (marker && !marker.isDisposed && marker.line >= 0) return marker.line
    return fallback !== undefined ? fallback : -1
  }

  private readRangeText(
    startLine: number,
    startCol: number,
    endLine: number,
    endCol: number | undefined,
  ): string {
    const buf = this.term.buffer.active
    const parts: string[] = []
    for (let y = startLine; y <= endLine; y++) {
      const line = buf.getLine(y)
      if (!line) continue
      const from = y === startLine ? startCol : 0
      const to = y === endLine ? endCol : undefined
      const piece = line.translateToString(true, from, to)
      if (y > startLine && line.isWrapped && parts.length > 0) {
        parts[parts.length - 1] += piece
      } else {
        parts.push(piece)
      }
    }
    return parts.join("\n")
  }

  private enforceLimit(): void {
    while (this._blocks.length > this.maxBlocks) {
      const removed = this._blocks.shift()
      if (removed) this.disposeBlockMarkers(removed)
    }
  }

  private disposeBlockMarkers(block: TerminalBlock): void {
    try { block.promptMarker?.dispose() } catch { /* noop */ }
    try { block.inputMarker?.dispose() } catch { /* noop */ }
    try { block.commandMarker?.dispose() } catch { /* noop */ }
    try { block.endMarker?.dispose() } catch { /* noop */ }
  }

  private emit(): void {
    for (const listener of this.listeners) {
      try { listener() } catch { /* noop */ }
    }
  }
}
