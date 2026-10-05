import type {
  AgentAttachmentReference,
  AgentSessionRuntime,
  AgentSessionSnapshot,
  AgentTimelineItem,
} from "@agent-cloud/agent-ui"
import { callTool, extractText, type Session, type Target } from "./client"
import { TOOLS } from "./gateway"

interface PromptRequest {
  instruction: string
  operation: "ask" | "apply"
  credential_refs?: string[]
}

const CAPABILITIES = {
  sendMessage: false,
  interrupt: false,
  resolvePermission: false,
  updateConfiguration: false,
  terminal: false,
  executeToolAction: false,
  extensions: [],
}

export class DoopsAgentUiRuntime implements AgentSessionRuntime {
  private readonly listeners = new Set<() => void>()
  private readonly options: { session: Session; target: Target; sessionId: string }
  private snapshot: AgentSessionSnapshot
  private metadataRequest: AbortController | null = null
  private active: { commandId: string; controller: AbortController } | null = null
  private generation = 0
  private ready = false
  private taskStatus: "idle" | "completed" | "failed" = "idle"

  constructor(options: { session: Session; target: Target; sessionId: string }) {
    this.options = options
    this.snapshot = {
      sessionId: options.sessionId,
      title: "DoAgent",
      agentLabel: "DoAgent",
      status: "launching",
      connection: "connecting",
      interactionMode: "acp",
      capabilities: CAPABILITIES,
      interactions: [],
      items: [],
      plan: [],
      permissions: [],
      terminals: [],
      hasOlderItems: false,
      metadata: [{ id: "target", label: "目标", value: `${options.target.cluster}/${options.target.instance}` }],
      error: null,
    }
  }

  getSnapshot(sessionId: string): AgentSessionSnapshot {
    this.requireSession(sessionId)
    return this.snapshot
  }

  subscribe(sessionId: string, listener: () => void): () => void {
    this.requireSession(sessionId)
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  async open(sessionId: string): Promise<void> {
    this.requireSession(sessionId)
    if (this.active) throw new Error("当前已有智能体任务正在执行")
    this.metadataRequest?.abort()
    const controller = new AbortController()
    this.metadataRequest = controller
    const generation = ++this.generation
    this.ready = false
    this.set({ connection: "connecting", capabilities: CAPABILITIES, metadata: [this.snapshot.metadata![0]] })
    let raw = ""
    try {
      await this.call({ session_id: sessionId, mode: "metadata" }, controller.signal, (event) => {
        if (event.type === "result") raw = extractText(event.result)
      })
      if (generation !== this.generation) return
      const metadata: unknown = JSON.parse(raw)
      if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) throw new Error("Agent 模型元数据无效")
      const value = metadata as { status?: unknown; model?: unknown; channels?: { name?: string; message?: string }[] }
      if (value.status === "unconfigured") throw new Error("AI 助手尚未连接大模型，请在管理页面配置该节点后重新检查。")
      if (value.status === "unavailable") {
        const channel = Array.isArray(value.channels) ? value.channels.find((item) => item.name === "acp") : undefined
        throw new Error(`DoAgent 不可用：${channel?.message || "doagent_unavailable"}`)
      }
      if (value.status !== "connected" || typeof value.model !== "string" || !value.model.trim()) throw new Error("Agent 模型元数据无效")
      this.ready = true
      this.set({
        connection: "connected",
        status: this.taskStatus,
        error: null,
        capabilities: { ...CAPABILITIES, sendMessage: true },
        metadata: [this.snapshot.metadata![0], { id: "model", label: "模型", value: value.model }],
      })
    } catch (error) {
      if (generation !== this.generation) return
      this.set({ connection: "disconnected", status: "failed", error: errorMessage(error), capabilities: CAPABILITIES })
    } finally {
      if (this.metadataRequest === controller) this.metadataRequest = null
    }
  }

  close(sessionId: string): void {
    this.requireSession(sessionId)
    ++this.generation
    this.ready = false
    this.metadataRequest?.abort()
    if (this.active) {
      const { commandId, controller } = this.active
      this.active = null
      controller.abort()
      this.finish(commandId, "已停止等待，远端终态未确认")
    }
    this.set({ connection: "disconnected", capabilities: CAPABILITIES })
  }

  async sendMessage(sessionId: string, commandId: string, input: { text: string; attachments?: AgentAttachmentReference[] }): Promise<void> {
    if (input.attachments?.length) throw new Error("DoOps 对话尚不支持附件，请使用文件页面上传。")
    await this.sendPrompt(sessionId, commandId, { instruction: input.text, operation: "ask" })
  }

  async sendPrompt(sessionId: string, commandId: string, request: PromptRequest): Promise<void> {
    this.requireSession(sessionId)
    if (this.active) throw new Error("当前已有智能体任务正在执行")
    if (!this.ready) throw new Error(this.snapshot.error || "智能体会话尚未就绪")
    const instruction = request.instruction.trim()
    if (!instruction) throw new Error("请输入要发送给智能体的内容")
    const generation = this.generation
    const active = { commandId, controller: new AbortController() }
    this.active = active
    this.set({ status: "running", error: null, latestUserCommandId: commandId, capabilities: { ...CAPABILITIES, sendMessage: true, interrupt: true } })
    this.upsert({ id: `user-${commandId}`, kind: "message", role: "user", text: instruction, status: "completed" })
    try {
      await this.call({ ...request, instruction, session_id: sessionId }, active.controller.signal, (event) => {
        if (generation !== this.generation || this.active !== active) return
        if (event.type === "output" && event.data) {
          const assistant = event.kind === "assistant_delta"
          const id = `${assistant ? "assistant" : "process"}-${commandId}`
          const previous = this.snapshot.items.find((item) => item.id === id)
          if (assistant) {
            const text = previous?.kind === "message" ? previous.text : ""
            this.upsert({ id, kind: "message", role: "assistant", text: text + event.data, status: "streaming" })
          } else {
            const detail = previous?.kind === "system" ? previous.detail || "" : ""
            this.upsert({ id, kind: "system", title: "执行过程", detail: detail + event.data, status: "running" })
          }
        } else if (event.type === "result") {
          const text = extractText(event.result)
          // Prompt results are transport receipts; the answer arrives in assistant_delta events.
          this.upsert({ id: `receipt-${commandId}`, kind: "system", title: "远端执行已结束", detail: text, status: "completed" })
        }
      })
      if (generation !== this.generation || this.active !== active) throw new Error("会话已关闭，远端终态未确认")
      this.finish(commandId)
    } catch (error) {
      const message = active.controller.signal.aborted ? "已停止等待，远端终态未确认" : errorMessage(error)
      if (generation === this.generation && this.active === active) this.finish(commandId, message)
      throw new Error(message)
    } finally {
      if (this.active === active) this.active = null
    }
  }

  async interrupt(sessionId: string, _commandId: string): Promise<void> {
    this.requireSession(sessionId)
    this.active?.controller.abort()
  }

  async loadOlder(sessionId: string): Promise<void> {
    this.requireSession(sessionId)
    throw new Error("DoOps 尚未提供历史会话查询接口")
  }

  async resolvePermission(): Promise<void> {
    throw new Error("DoOps 权限由 Gateway 管理")
  }

  async updateConfiguration(): Promise<void> {
    throw new Error("DoOps 模型配置由管理页面维护")
  }

  private call(args: Record<string, unknown>, signal: AbortSignal, onEvent: Parameters<typeof callTool>[2]) {
    return callTool(this.options.session, { cluster: this.options.target.cluster, instance: this.options.target.instance, tool: TOOLS.prompt, arguments: args, signal }, onEvent)
  }

  private finish(commandId: string, error?: string): void {
    const status: "failed" | "completed" = error ? "failed" : "completed"
    this.taskStatus = status
    const items = this.snapshot.items.map((item) =>
      (item.id === `assistant-${commandId}` && item.kind === "message") ||
      (item.id === `process-${commandId}` && item.kind === "system") ? { ...item, status } : item,
    )
    if (error) items.push({ id: `error-${commandId}`, kind: "error", title: "任务失败", detail: error, status: "failed" })
    this.set({ items, status, error: error || null, capabilities: { ...CAPABILITIES, sendMessage: this.ready } })
  }

  private upsert(item: AgentTimelineItem): void {
    const items = this.snapshot.items.slice()
    const index = items.findIndex((previous) => previous.id === item.id)
    if (index < 0) items.push(item)
    else items[index] = item
    this.set({ items })
  }

  private set(patch: Partial<AgentSessionSnapshot>): void {
    this.snapshot = { ...this.snapshot, ...patch }
    for (const listener of this.listeners) listener()
  }

  private requireSession(sessionId: string): void {
    if (sessionId !== this.options.sessionId) throw new Error("agent_workbench_runtime_session_mismatch")
  }
}

function errorMessage(value: unknown): string {
  return value instanceof Error ? value.message : String(value)
}
