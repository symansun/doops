import { create } from "@bufbuild/protobuf"
import type { AgentWorkbenchEventInput } from "@agent-cloud/agent-ui/server"
import {
  ContentBlockSchema, MessageRole, PlanStepSchema, PlanStepStatus,
  TimelineItemAppendedSchema, TimelineItemContentSchema, TimelineItemStatus,
  TimelineItemUpdatedSchema, ToolExecutionSchema, ToolFailureSchema,
  ToolIdentitySchema, ToolPhase, ToolProgressSchema, ToolResultSchema,
  type TimelineItemContent,
} from "@agent-cloud/agent-ui/protocol"

const textFields = [
  "sessionUpdate", "turnId", "toolCallId", "toolName", "status", "title", "text",
  "stopReason", "failureClass", "failureReason", "error", "message", "reason",
]
const numberFields = ["inputTokens", "outputTokens", "totalTokens", "elapsedMs"]
const publicFields = new Set([...textFields, ...numberFields, "content", "entries", "todos", "usage", "replay"])
const toolPhases = new Map<string, ToolPhase>([
  ["pending", ToolPhase.QUEUED], ["in_progress", ToolPhase.RUNNING],
  ["completed", ToolPhase.COMPLETED], ["failed", ToolPhase.FAILED], ["cancelled", ToolPhase.CANCELLED],
])
const planStatuses = new Map<string, PlanStepStatus>([
  ["pending", PlanStepStatus.PENDING], ["in_progress", PlanStepStatus.RUNNING], ["completed", PlanStepStatus.COMPLETED],
])
const terminalStatuses = new Set(["completed", "failed", "needs_input", "interrupted", "cancelled"])

interface ToolProjection {
  name?: string
  title?: string
  phase: ToolPhase
  output?: string
  error?: string
  progress?: string
}

// This maps public wire data only. Gateway result validation owns execution completion.
export class DoopsAgentEventProjection {
  private nativeTurnId?: string
  private terminal?: { status: string; stopReason: string; error?: string }
  private gatewayStatus?: TimelineItemStatus
  private messageText = ""
  private thoughtText = ""
  private messageSeen = false
  private thoughtSeen = false
  private planSeen = false
  private notices = 0
  private readonly tools = new Map<string, ToolProjection>()
  private readonly prefix: string

  constructor(private readonly commandId: string) {
    if (typeof commandId !== "string" || !commandId.trim()) invalid("commandId")
    this.prefix = encodeURIComponent(commandId)
  }

  get finished(): boolean { return this.terminal !== undefined }

  project(update: Record<string, unknown>): AgentWorkbenchEventInput[] {
    const record = object(update, "update")
    keys(record, publicFields, "update_fields")
    for (const field of textFields) optionalText(record, field)
    for (const field of numberFields) unsignedNumber(record, field)
    if (record.replay !== undefined && typeof record.replay !== "boolean") invalid("replay")
    if (record.replay === true) invalid("historical_replay")
    if (record.usage !== undefined) {
      const usage = object(record.usage, "usage")
      keys(usage, new Set(numberFields.slice(0, 3)), "usage_fields")
      for (const field of numberFields.slice(0, 3)) unsignedNumber(usage, field)
    }
    if (this.gatewayStatus !== undefined) invalid("after_gateway_terminal")
    const kind = requiredText(record, "sessionUpdate")
    const nativeTurnId = optionalText(record, "turnId")
    if (nativeTurnId !== undefined && (!nativeTurnId.trim() ||
      (this.nativeTurnId !== undefined && nativeTurnId !== this.nativeTurnId))) invalid("turnId")

    let events: AgentWorkbenchEventInput[]
    switch (kind) {
      case "agent_message":
      case "agent_message_chunk":
        events = this.text(contentText(record.content), false, kind === "agent_message")
        break
      case "agent_thought_chunk":
      case "thought":
        events = this.text(contentText(record.content), true, kind === "thought")
        break
      case "tool_call":
      case "tool_call_update":
      case "tool_call_progress":
        events = this.tool(record, kind === "tool_call_progress")
        break
      case "plan":
        events = this.plan(record.entries)
        break
      case "todos_updated":
      case "update_todos":
        events = this.plan(record.todos)
        break
      case "turn_started":
        requiredText(record, "turnId")
        if (this.finished || (record.status !== undefined && record.status !== "inProgress")) invalid("turn_started")
        events = []
        break
      case "turn_finished": {
        requiredText(record, "turnId")
        const status = requiredText(record, "status"), stopReason = requiredText(record, "stopReason")
        if (!terminalStatuses.has(status)) invalid("turn_finished_status")
        const error = optionalText(record, "error")
        if (this.terminal && (this.terminal.status !== status || this.terminal.stopReason !== stopReason || this.terminal.error !== error)) {
          invalid("turn_finished_conflict")
        }
        this.terminal = { status, stopReason, error }
        events = []
        break
      }
      case "usage_update":
        if (record.usage === undefined) invalid("usage")
        events = []
        break
      case "doops_notice": {
        const text = contentText(record.content)
        if (!text) invalid("notice_text")
        const itemId = `${this.prefix}:notice:${++this.notices}`
        events = [this.timeline(itemId, create(TimelineItemContentSchema, { content: { case: "system", value: {
          content: [create(ContentBlockSchema, { contentId: `${itemId}:text`,
            identity: { namespace: "doops.gateway", semanticKey: "notice", schemaVersion: "1" },
            content: { case: "text", value: { text } } })],
        } } }), false)]
        break
      }
      default:
        invalid("unsupported_sessionUpdate")
    }
    if (nativeTurnId !== undefined) this.nativeTurnId = nativeTurnId
    return events
  }

  // Call only after the Gateway's authoritative final result, including failure/cancellation.
  finishMessage(status: TimelineItemStatus): AgentWorkbenchEventInput[] {
    if (![TimelineItemStatus.COMPLETED, TimelineItemStatus.FAILED, TimelineItemStatus.CANCELLED].includes(status)) invalid("gateway_terminal_status")
    if (this.gatewayStatus !== undefined) {
      if (this.gatewayStatus !== status) invalid("gateway_terminal_conflict")
      return []
    }
    const events: AgentWorkbenchEventInput[] = []
    const unfinished = [...this.tools].filter(([, tool]) => [ToolPhase.QUEUED, ToolPhase.RUNNING, ToolPhase.WAITING_APPROVAL].includes(tool.phase))
    if (status === TimelineItemStatus.COMPLETED && unfinished.length) invalid("tool_terminal_missing")
    for (const [callId, tool] of unfinished) {
      const itemId = `${this.prefix}:tool:${encodeURIComponent(callId)}`
      events.push(this.timeline(itemId, create(TimelineItemContentSchema, { content: { case: "toolExecution", value: {
        executionId: itemId, title: tool.title, phase: ToolPhase.FAILED,
        identity: { namespace: "doagent", semanticKey: "tool.call", schemaVersion: "1", sourceToolName: tool.name },
        failure: { code: "doops_outcome_unconfirmed", message: "会话已结束，工具结果未确认" },
        results: tool.output === undefined ? [] : [{ resultId: `${itemId}:result`, primary: true, blocks: [textBlock(`${itemId}:output`, tool.output)] }],
      } } }), true))
    }
    if (this.messageSeen) events.push(this.textItem(false, this.messageText, status, true))
    if (this.thoughtSeen) events.push(this.textItem(true, this.thoughtText, status, true))
    this.gatewayStatus = status
    return events
  }

  private text(value: string, thought: boolean, complete: boolean): AgentWorkbenchEventInput[] {
    if (complete && !value.trim()) invalid("empty_complete_message")
    if (!value) {
      return []
    }
    const previous = thought ? this.thoughtText : this.messageText
    const next = complete ? value : previous + value
    const seen = thought ? this.thoughtSeen : this.messageSeen
    const event = this.textItem(thought, next, TimelineItemStatus.STREAMING, seen)
    if (thought) { this.thoughtText = next; this.thoughtSeen = true }
    else { this.messageText = next; this.messageSeen = true }
    return [event]
  }

  private textItem(thought: boolean, text: string, status: TimelineItemStatus, seen: boolean): AgentWorkbenchEventInput {
    const itemId = `${this.prefix}:${thought ? "thought" : "assistant"}`
    const content = [textBlock(`${itemId}:text`, text)]
    return this.timeline(itemId, create(TimelineItemContentSchema, {
      content: thought ? { case: "reasoning", value: { content, status } }
        : { case: "message", value: { role: MessageRole.ASSISTANT, content, status } },
    }), seen)
  }

  private tool(update: Record<string, unknown>, heartbeat: boolean): AgentWorkbenchEventInput[] {
    const callId = requiredText(update, "toolCallId"), previous = this.tools.get(callId)
    const wireStatus = optionalText(update, "status")
    if (heartbeat && !previous) invalid("tool_progress_without_call")
    const phase = wireStatus === undefined ? previous?.phase : toolPhases.get(wireStatus)
    if (phase === undefined) invalid("tool_status")
    const name = update.toolName === undefined ? previous?.name : requiredText(update, "toolName")
    const title = update.title === undefined ? previous?.title : requiredText(update, "title")
    if (!previous && name === undefined && title === undefined) invalid("tool_identity")
    if (previous?.name !== undefined && name !== previous.name) invalid("tool_identity_conflict")
    if (previous && [ToolPhase.COMPLETED, ToolPhase.FAILED, ToolPhase.CANCELLED].includes(previous.phase) && phase !== previous.phase) invalid("tool_terminal_conflict")
    const output = update.content === undefined ? previous?.output : contentText(update.content)
    const errors = [optionalText(update, "error"), optionalText(update, "failureReason")].filter((value) => value !== undefined && value.length > 0)
    const error = errors.length > 0 ? errors.join("\n") : previous?.error
    const progress = heartbeat ? requiredText(update, "message") : previous?.progress
    const itemId = `${this.prefix}:tool:${encodeURIComponent(callId)}`
    const execution = create(ToolExecutionSchema, {
      executionId: itemId, title, phase,
      identity: create(ToolIdentitySchema, { namespace: "doagent", semanticKey: "tool.call", schemaVersion: "1", sourceToolName: name }),
      progress: progress === undefined ? undefined : create(ToolProgressSchema, { message: progress }),
      results: output === undefined ? [] : [create(ToolResultSchema, {
        resultId: `${itemId}:result`, primary: true, blocks: [textBlock(`${itemId}:output`, output)],
      })],
      failure: phase === ToolPhase.FAILED ? create(ToolFailureSchema, {
        code: "doagent_tool_failed", message: error === undefined ? (output === undefined ? "" : output) : error,
      }) : undefined,
    })
    const event = this.timeline(itemId, create(TimelineItemContentSchema, { content: { case: "toolExecution", value: execution } }), previous !== undefined)
    this.tools.set(callId, { name, title, phase, output, error, progress })
    return [event]
  }

  private plan(value: unknown): AgentWorkbenchEventInput[] {
    if (!Array.isArray(value)) invalid("plan_entries")
    const steps = value.map((item, index) => {
      const entry = object(item, "plan_entry")
      const fields = ["id", "content", "text", "title", "status", "priority", "activeForm"]
      keys(entry, new Set(fields), "plan_entry_fields")
      for (const field of fields) optionalText(entry, field)
      const status = planStatuses.get(requiredText(entry, "status"))
      if (status === undefined) invalid("plan_status")
      return create(PlanStepSchema, {
        stepId: `${this.prefix}:step:${index}`, title: requiredText(entry, "content"), status,
        detail: optionalText(entry, "activeForm"),
      })
    })
    const event = this.timeline(`${this.prefix}:plan`, create(TimelineItemContentSchema, { content: { case: "plan", value: { steps } } }), this.planSeen)
    this.planSeen = true
    return [event]
  }

  private timeline(itemId: string, content: TimelineItemContent, seen: boolean): AgentWorkbenchEventInput {
    return {
      itemId, turnId: this.commandId, causationCommandId: this.commandId,
      event: seen ? { case: "timelineItemUpdated", value: create(TimelineItemUpdatedSchema, { content }) }
        : { case: "timelineItemAppended", value: create(TimelineItemAppendedSchema, { content }) },
    }
  }
}

function textBlock(contentId: string, text: string) {
  return create(ContentBlockSchema, {
    contentId, identity: { namespace: "doagent", semanticKey: "content.text", schemaVersion: "1" },
    content: { case: "text", value: { text } },
  })
}

function contentText(value: unknown): string {
  if (typeof value === "string") return value
  if (Array.isArray(value)) return value.map(contentText).join("\n")
  const content = object(value, "content")
  if (content.type === "text") {
    keys(content, new Set(["type", "text"]), "text_content_fields")
    const text = optionalText(content, "text")
    if (text === undefined) invalid("content_text")
    return text
  }
  if (content.type === "content") {
    keys(content, new Set(["type", "content"]), "wrapped_content_fields")
    return contentText(content.content)
  }
  return invalid("content_type")
}

function object(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) invalid(field)
  return value as Record<string, unknown>
}

function keys(record: Record<string, unknown>, allowed: ReadonlySet<string>, field: string): void {
  if (Object.keys(record).some((key) => !allowed.has(key))) invalid(field)
}

function optionalText(record: Record<string, unknown>, field: string): string | undefined {
  const value = record[field]
  if (value === undefined) return undefined
  if (typeof value !== "string") invalid(field)
  return value
}

function requiredText(record: Record<string, unknown>, field: string): string {
  const value = optionalText(record, field)
  if (value === undefined || !value.trim()) invalid(field)
  return value
}

function unsignedNumber(record: Record<string, unknown>, field: string): void {
  const value = record[field]
  if (value !== undefined && (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)) invalid(field)
}

function invalid(field: string): never {
  throw new Error(`doops_agent_event_invalid:${field}`)
}
