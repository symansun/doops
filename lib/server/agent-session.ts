import { createHash, randomUUID } from "node:crypto"
import { create } from "@bufbuild/protobuf"
import { AgentWorkbenchSessionSource, type AgentWorkbenchEventInput } from "@agent-cloud/agent-ui/server"
import {
  CommandReceiptSchema, CommandReceiptState, MessageRole, SessionSnapshotSchema,
  SessionStatus, TimelineItemStatus, AgentEventSchema, type CommandEnvelope, type CommandReceipt,
} from "@agent-cloud/agent-ui/protocol"
import { deploymentRequest, type DeploymentInput } from "../deployment"
import { AgentGatewayError, agentGatewayCall, type AgentGatewayContext } from "./agent-gateway-rpc"
import { DoopsAgentEventProjection } from "./agent-event-projection"

type ActiveOperation = { command: CommandEnvelope; controller: AbortController; done: Promise<void>; uncertain: boolean }
type HostedSession = { source: AgentWorkbenchSessionSource; touchedAt: number; subscribers: number; active?: ActiveOperation }
const sessions = new Map<string, HostedSession>()

export async function authorizedAgentSession(context: AgentGatewayContext, signal: AbortSignal): Promise<HostedSession> {
  const raw = await agentGatewayCall(context, { mode: "metadata" }, signal)
  let metadata: Record<string, unknown>
  try { metadata = JSON.parse(raw) } catch { throw new AgentGatewayError("DoAgent metadata 不是合法 JSON") }
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) throw new AgentGatewayError("DoAgent metadata 无效")
  if (metadata.event_schema !== "doops.sh/acp-events/v1" || !Array.isArray(metadata.event_formats) || !metadata.event_formats.includes("acp")) {
    throw new AgentGatewayError("目标 Agent 不支持结构化 ACP 事件协议，必须升级 Agent", 412)
  }
  if (metadata.status !== "connected" || typeof metadata.model !== "string" || !metadata.model.trim()) {
    throw new AgentGatewayError(metadata.status === "unconfigured" ? "AI 助手尚未连接大模型" : "DoAgent 模型连接不可用", 503)
  }
  const now = Date.now()
  for (const [key, value] of sessions) {
    if (!value.active && !value.subscribers && now - value.touchedAt > 30 * 60_000) sessions.delete(key)
  }
  const key = createHash("sha256").update(JSON.stringify([context.gateway, context.token, context.cluster, context.instance, context.sessionId])).digest("hex")
  let hosted = sessions.get(key)
  if (!hosted) {
    if (sessions.size >= 100) throw new AgentGatewayError("控制台会话容量已满", 429)
    hosted = { touchedAt: now, subscribers: 0, source: new AgentWorkbenchSessionSource({ snapshot: create(SessionSnapshotSchema, {
      sessionId: context.sessionId, streamEpoch: randomUUID(), status: SessionStatus.IDLE,
      configuration: { model: metadata.model },
      // Metadata was authorized by Gateway for this caller; interruption is limited to this hosted operation.
      grants: [{ grantId: randomUUID(), issuer: context.gateway, subject: key, sessionId: context.sessionId,
        issuedAt: new Date(now).toISOString(), actions: ["session.send_prompt", "session.interrupt"] }],
      capabilities: {
        protocolVersion: "2", history: false, models: [metadata.model],
        commandSchemas: ["send_prompt", "interrupt"].map((semanticKey) => ({ namespace: "agentcloud", semanticKey, schemaVersion: "1", actions: [`session.${semanticKey}`] })),
        extensions: [{ namespace: "doops.operations", semanticKey: "apply", schemaVersion: "1" }],
      },
    }) }) }
    sessions.set(key, hosted)
  }
  const previous = hosted.source.getSnapshot()
  if (previous.configuration?.model !== metadata.model) {
    const capabilities = previous.capabilities
    if (!capabilities) throw new AgentGatewayError("会话缺少协议能力", 500)
    capabilities.models = [metadata.model]
    await hosted.source.emit([
      { itemId: "configuration", event: create(AgentEventSchema, { event: { case: "configurationChanged", value: { configuration: { model: metadata.model } } } }).event },
      { itemId: "capabilities", event: create(AgentEventSchema, { event: { case: "capabilitiesChanged", value: { capabilities } } }).event },
    ], previous.streamEpoch)
  }
  hosted.touchedAt = now
  return hosted
}

export async function executeAgentCommand(hosted: HostedSession, context: AgentGatewayContext, command: CommandEnvelope): Promise<CommandReceipt> {
  return hosted.source.execute(command, async (owned) => {
    if (owned.command.case === "interrupt") {
      const active = hosted.active
      if (!active) throw new AgentGatewayError("当前没有运行中的任务", 409)
      if (active.uncertain) throw new AgentGatewayError("远端结果未确认，不能复用此会话；请先核对远端状态", 409)
      if (owned.command.value.turnId && owned.command.value.turnId !== active.command.commandId) {
        throw new AgentGatewayError("中断目标不是当前任务", 409)
      }
      active.controller.abort()
      await active.done
      const canceled = hosted.source.getSnapshot().commandReceipts.find((receipt) => receipt.commandId === active.command.commandId)?.state === CommandReceiptState.CANCELLED
      const receipt = commandReceipt(owned, canceled ? CommandReceiptState.SUCCEEDED : CommandReceiptState.FAILED,
        canceled ? undefined : active.uncertain ? "已请求停止，远端终态未确认" : "任务已结束，取消未生效")
      await hosted.source.emit([receiptEvent(receipt)], owned.streamEpoch)
      return receipt
    }
    if (hosted.active) throw new AgentGatewayError(hosted.active.uncertain ? "远端结果未确认，不能复用此会话；请先核对远端状态" : "当前会话仍有运行中的任务", 409)
    const args = promptArguments(context, owned)
    const controller = new AbortController()
    let complete!: () => void
    const done = new Promise<void>((resolve) => { complete = resolve })
    hosted.active = { command: owned, controller, done, uncertain: false }
    const receipt = commandReceipt(owned, CommandReceiptState.ACCEPTED)
    try {
      await hosted.source.emit([
        receiptEvent(receipt),
        { itemId: `status:${owned.commandId}`, turnId: owned.commandId, causationCommandId: owned.commandId,
          event: create(AgentEventSchema, { event: { case: "sessionStatusChanged", value: { status: SessionStatus.RUNNING } } }).event },
        { itemId: `user:${owned.commandId}`, turnId: owned.commandId, causationCommandId: owned.commandId,
          event: create(AgentEventSchema, { event: { case: "timelineItemAppended", value: { content: { content: { case: "message", value: {
            role: MessageRole.USER, status: TimelineItemStatus.COMPLETED,
            content: [{ contentId: "text", content: { case: "text", value: { text: args.instruction as string } } }],
          } } } } } }).event },
      ], owned.streamEpoch)
    } catch (error) { hosted.active = undefined; complete(); throw error }
    void runOperation(hosted, context, owned, args, controller.signal).catch(async () => {
      controller.abort()
      if (hosted.active?.command.commandId === owned.commandId) hosted.active.uncertain = true
      const failed = commandReceipt(owned, CommandReceiptState.FAILED, "会话状态发布失败，远端结果未确认")
      await hosted.source.emit([receiptEvent(failed), { itemId: `status:${owned.commandId}`, turnId: owned.commandId,
        event: create(AgentEventSchema, { event: { case: "sessionStatusChanged", value: { status: SessionStatus.FAILED, error: failed.error } } }).event }], owned.streamEpoch)
    }).finally(() => {
      if (hosted.active?.command.commandId === owned.commandId && !hosted.active.uncertain) hosted.active = undefined
      hosted.touchedAt = Date.now()
      complete()
    }).catch(() => {
      // A retired epoch must never receive a replacement failure event.
      controller.abort()
      console.error("doops_session_state_publication_failed")
    })
    return receipt
  })
}

async function runOperation(hosted: HostedSession, context: AgentGatewayContext, command: CommandEnvelope, args: Record<string, unknown>, signal: AbortSignal) {
  const projection = new DoopsAgentEventProjection(command.commandId)
  let updates: Promise<void> = Promise.resolve()
  let failure: string | undefined
  let uncertain = false
  let canceled = false
  let terminalEvents: AgentWorkbenchEventInput[] = []
  try {
    await agentGatewayCall(context, { ...args, event_format: "acp" }, signal, (update) => {
      updates = updates.then(async () => {
        if (update.kind === "error") throw new AgentGatewayError(update.data)
        if (update.kind !== "acp_update") throw new AgentGatewayError("Agent 返回了非结构化事件，协议不匹配")
        const data: unknown = JSON.parse(update.data)
        if (!data || typeof data !== "object" || Array.isArray(data)) throw new AgentGatewayError("ACP 事件无效")
        const events = projection.project(data as Record<string, unknown>)
        if (events.length) await hosted.source.emit(events, command.streamEpoch)
      })
      // Abort on a broken projection; never let a rejected update become a successful task.
      void updates.catch(() => hosted.active?.controller.abort())
    })
    await updates
    if (!projection.finished) throw new AgentGatewayError("缺少 DoAgent turn_finished 终态，执行结果未确认")
    terminalEvents = projection.finishMessage(TimelineItemStatus.COMPLETED)
  } catch (error) {
    failure = error instanceof Error ? error.message : "智能体执行失败"
    uncertain = !(error instanceof AgentGatewayError) || error.outcome === "unknown"
    canceled = error instanceof AgentGatewayError && error.canceled
    try { await updates } catch (updateError) { failure = updateError instanceof Error ? updateError.message : "ACP 事件无效" }
    terminalEvents = projection.finishMessage(canceled ? TimelineItemStatus.CANCELLED : TimelineItemStatus.FAILED)
  }
  if (hosted.active?.command.commandId === command.commandId) hosted.active.uncertain = uncertain
  const status = failure ? SessionStatus.FAILED : SessionStatus.COMPLETED
  const receipt = commandReceipt(command, canceled ? CommandReceiptState.CANCELLED : failure ? CommandReceiptState.FAILED : CommandReceiptState.SUCCEEDED, failure)
  if (uncertain) {
    const capabilities = hosted.source.getSnapshot().capabilities
    if (!capabilities) throw new AgentGatewayError("会话缺少协议能力", 500)
    capabilities.commandSchemas = []
    capabilities.extensions = []
    terminalEvents.push({ itemId: "capabilities", event: create(AgentEventSchema, { event: { case: "capabilitiesChanged", value: { capabilities } } }).event })
  }
  await hosted.source.emit([
    ...terminalEvents,
    receiptEvent(receipt),
    { itemId: `status:${command.commandId}`, turnId: command.commandId, causationCommandId: command.commandId,
      event: create(AgentEventSchema, { event: { case: "sessionStatusChanged", value: { status, ...(receipt.error ? { error: receipt.error } : {}) } } }).event },
    ...(receipt.error ? [{ itemId: `error:${command.commandId}`, turnId: command.commandId,
      event: create(AgentEventSchema, { event: { case: "timelineItemAppended", value: { content: { content: { case: "error", value: { error: receipt.error } } } } } }).event }] : []),
  ], command.streamEpoch)
}

function promptArguments(context: AgentGatewayContext, command: CommandEnvelope): Record<string, unknown> {
  const payload = command.command
  if (payload.case === "sendPrompt") {
    if (!payload.value.text.trim() || payload.value.text.length > 64_000 || payload.value.attachments.length) {
      throw new AgentGatewayError("需要有效文本，当前接口不支持对话附件", 400)
    }
    return { instruction: payload.value.text, operation: "ask" }
  }
  if (payload.case !== "extension" || payload.value.namespace !== "doops.operations" || payload.value.semanticType !== "apply" || payload.value.schemaVersion !== "1") {
    throw new AgentGatewayError("不支持该会话命令", 400)
  }
  if (payload.value.payload?.mediaType !== "application/json" || payload.value.payload.data.length > 64_000) {
    throw new AgentGatewayError("部署命令必须使用受控 JSON 表单", 400)
  }
  let input: unknown
  try { input = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(payload.value.payload.data)) } catch {
    throw new AgentGatewayError("部署表单不是合法 JSON", 400)
  }
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new AgentGatewayError("部署表单无效", 400)
  const envelope = input as Record<string, unknown>
  if (envelope.confirmed !== true || Object.keys(envelope).some((key) => !["input", "confirmed"].includes(key)) ||
      !envelope.input || typeof envelope.input !== "object" || Array.isArray(envelope.input)) {
    throw new AgentGatewayError("部署必须提交已确认的发布表单", 400)
  }
  const record = envelope.input as Record<string, unknown>
  const fields = ["services", "release", "namespace", "chart", "values", "repository", "selector", "version", "commit", "ci", "reason", "acceptance", "credentials"]
  if (Object.keys(record).some((key) => !fields.includes(key)) || fields.some((key) => typeof record[key] !== "string") || !["digest", "tag"].includes(record.selector as string)) {
    throw new AgentGatewayError("部署表单字段无效", 400)
  }
  try {
    return deploymentRequest({ cluster: context.cluster, instance: context.instance, key: `${context.cluster}/${context.instance}` }, record as unknown as DeploymentInput)
  } catch (error) { throw new AgentGatewayError(error instanceof Error ? error.message : "部署表单无效", 400) }
}

function commandReceipt(command: CommandEnvelope, state: CommandReceiptState, failure?: string) {
  return create(CommandReceiptSchema, {
    sessionId: command.sessionId, commandId: command.commandId, payloadDigest: command.payloadDigest, state,
    receivedAt: command.issuedAt, updatedAt: new Date().toISOString(),
    ...(command.command.case === "extension" ? { interactionKind: "extension" } : {}),
    ...(failure ? { error: { code: "doops_operation_failed", message: failure } } : {}),
  })
}

function receiptEvent(receipt: CommandReceipt): AgentWorkbenchEventInput {
  return { itemId: `receipt:${receipt.commandId}`, causationCommandId: receipt.commandId,
    event: create(AgentEventSchema, { event: { case: "commandReceiptChanged", value: { receipt } } }).event }
}
