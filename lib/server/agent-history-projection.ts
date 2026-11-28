import { create } from "@bufbuild/protobuf"
import {
  ContentBlockSchema,
  EventEnvelopeSchema,
  MessageRole,
  TimelineItemContentSchema,
  TimelineItemSchema,
  TimelineItemStatus,
  ToolExecutionSchema,
  ToolFailureSchema,
  ToolIdentitySchema,
  ToolPhase,
  ToolResultSchema,
  type TimelineItem,
  type TimelineItemContent,
} from "@agent-cloud/agent-ui/protocol"
import {
  buildToolDisplayFields,
  extractHistoryToolDisplay,
  resolveAgentToolSemanticKey,
} from "./agent-tool-display"

export const AGENT_HISTORY_SCHEMA = "doops.sh/agent-history/v1"

const SESSION_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const MAX_TOOL_DETAIL_CHARS = 240

export interface AgentHistorySession {
  session_id: string
  title: string
  last_message_preview?: string
  message_count: number
  turn_count: number
  created_at?: string
  updated_at: string
  source?: string
}

export interface AgentHistoryProjectionOptions {
  sessionId: string
  streamEpoch: string
  fallbackTimestamp: string
}

interface RawToolCall {
  id: string
  name: string
  argumentsText?: string
}

interface RawToolResult {
  index: number
  content: string
  isError: boolean
  name?: string
  timestamp?: string
}

export function parseAgentSessionsList(raw: string): AgentHistorySession[] {
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { throw new Error("Agent 会话列表不是合法 JSON") }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("Agent 会话列表格式无效")
  }
  const record = parsed as Record<string, unknown>
  if (record.history_schema !== AGENT_HISTORY_SCHEMA || !Array.isArray(record.sessions)) {
    throw new Error("Agent 会话列表缺少有效协议版本")
  }
  const sessions: AgentHistorySession[] = []
  const seen = new Set<string>()
  for (const item of record.sessions) {
    if (!item || typeof item !== "object" || Array.isArray(item)) continue
    const entry = item as Record<string, unknown>
    const sessionId = typeof entry.session_id === "string" ? entry.session_id.trim() : ""
    if (!sessionId || !SESSION_ID_PATTERN.test(sessionId) || sessionId.includes("..") || seen.has(sessionId)) continue
    seen.add(sessionId)
    const updatedAt = typeof entry.updated_at === "string" ? entry.updated_at.trim() : ""
    const createdAt = typeof entry.created_at === "string" && entry.created_at.trim() ? entry.created_at.trim() : undefined
    const title = typeof entry.title === "string" ? entry.title.trim() : ""
    const preview = typeof entry.last_message_preview === "string" && entry.last_message_preview.trim()
      ? entry.last_message_preview.trim() : undefined
    const source = typeof entry.source === "string" && entry.source.trim() ? entry.source.trim() : undefined
    const messageCount = typeof entry.message_count === "number" && Number.isFinite(entry.message_count) && entry.message_count >= 0
      ? Math.floor(entry.message_count) : 0
    const turnCount = typeof entry.turn_count === "number" && Number.isFinite(entry.turn_count) && entry.turn_count >= 0
      ? Math.floor(entry.turn_count) : 0
    sessions.push({
      session_id: sessionId,
      title,
      ...(preview ? { last_message_preview: preview } : {}),
      message_count: messageCount,
      turn_count: turnCount,
      ...(createdAt ? { created_at: createdAt } : {}),
      updated_at: updatedAt,
      ...(source ? { source } : {}),
    })
  }
  return sessions
}

export function projectAgentHistoryToSnapshotItems(
  raw: string,
  options: AgentHistoryProjectionOptions,
): TimelineItem[] {
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { throw new Error("Agent 历史记录不是合法 JSON") }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("Agent 历史记录格式无效")
  }
  const record = parsed as Record<string, unknown>
  if (record.history_schema !== AGENT_HISTORY_SCHEMA || !Array.isArray(record.messages)) {
    throw new Error("Agent 历史记录缺少有效协议版本")
  }

  const messages = record.messages
  const toolResultsById = new Map<string, RawToolResult>()
  for (let index = 0; index < messages.length; index++) {
    const msg = asRecord(messages[index])
    if (!msg || normalizedRole(msg) !== "tool") continue
    const toolCallId = stringField(msg, "tool_call_id") || stringField(msg, "toolCallId") || stringField(msg, "call_id")
    if (!toolCallId || toolResultsById.has(toolCallId)) continue
    toolResultsById.set(toolCallId, {
      index,
      content: extractMessageText(msg),
      isError: msg.is_error === true || msg.isError === true,
      name: stringField(msg, "name") || stringField(msg, "tool_name") || undefined,
      timestamp: validIsoTimestamp(msg),
    })
  }

  const consumedToolIndexes = new Set<number>()
  const entries: Array<{ itemId: string; turnId: string; createdAt: string; content: TimelineItemContent }> = []
  const fallbackTs = Number.isFinite(Date.parse(options.fallbackTimestamp))
    ? options.fallbackTimestamp
    : new Date().toISOString()

  if (record.truncated === true) {
    entries.push({
      itemId: "history:truncated",
      turnId: "history-turn-0",
      createdAt: fallbackTs,
      content: create(TimelineItemContentSchema, {
        content: {
          case: "system",
          value: {
            content: [create(ContentBlockSchema, {
              contentId: "history:truncated:text",
              identity: { namespace: "doops.gateway", semanticKey: "history-truncated", schemaVersion: "1" },
              content: { case: "text", value: { text: "早期历史消息因体积上限已省略，以下为最近的对话记录。" } },
            })],
          },
        },
      }),
    })
  }

  let turnNumber = 0
  const currentTurnId = () => `history-turn-${turnNumber || 1}`

  for (let index = 0; index < messages.length; index++) {
    const msg = asRecord(messages[index])
    if (!msg) continue
    const role = normalizedRole(msg)
    if (!role || role === "system") continue
    const createdAt = validIsoTimestamp(msg) || fallbackTs

    if (role === "user") {
      const rawText = stripInjectedUserContext(extractMessageText(msg))
      const attachmentNames = extractAttachmentNames(msg)
      const blocks = []
      if (rawText) {
        blocks.push(textBlock(`history:${index}:user:text`, rawText))
      }
      if (attachmentNames.length > 0) {
        blocks.push(textBlock(
          `history:${index}:user:attachments`,
          attachmentNames.map((name) => `[附件: ${name}]`).join("\n"),
        ))
      }
      if (blocks.length === 0) continue
      turnNumber += 1
      entries.push({
        itemId: `history:${index}:user`,
        turnId: currentTurnId(),
        createdAt,
        content: create(TimelineItemContentSchema, {
          content: {
            case: "message",
            value: {
              role: MessageRole.USER,
              status: TimelineItemStatus.COMPLETED,
              content: blocks,
            },
          },
        }),
      })
      continue
    }

    if (role === "assistant") {
      const turnId = currentTurnId()
      const reasoning = (stringField(msg, "reasoning_content") || stringField(msg, "reasoning") || "").trim()
      if (reasoning) {
        entries.push({
          itemId: `history:${index}:reasoning`,
          turnId,
          createdAt,
          content: create(TimelineItemContentSchema, {
            content: {
              case: "reasoning",
              value: {
                status: TimelineItemStatus.COMPLETED,
                content: [textBlock(`history:${index}:reasoning:text`, reasoning)],
              },
            },
          }),
        })
      }

      const text = extractMessageText(msg).trim()
      if (text) {
        entries.push({
          itemId: `history:${index}:assistant`,
          turnId,
          createdAt,
          content: create(TimelineItemContentSchema, {
            content: {
              case: "message",
              value: {
                role: MessageRole.ASSISTANT,
                status: TimelineItemStatus.COMPLETED,
                content: [textBlock(`history:${index}:assistant:text`, text)],
              },
            },
          }),
        })
      }

      const toolCalls = extractToolCalls(msg, index)
      for (let callIndex = 0; callIndex < toolCalls.length; callIndex++) {
        const call = toolCalls[callIndex]
        const matched = call.id ? toolResultsById.get(call.id) : undefined
        if (matched) consumedToolIndexes.add(matched.index)
        const itemId = `history:${index}:tool:${callIndex}`
        const output = matched ? matched.content : undefined
        const failed = matched?.isError === true
        const phase = failed ? ToolPhase.FAILED : ToolPhase.COMPLETED
        const semanticKey = resolveAgentToolSemanticKey(call.name)
        const extracted = extractHistoryToolDisplay(call.name, call.argumentsText)
        const display = buildToolDisplayFields(call.name, extracted)
        const detail = display.detail ?? compactToolDetail(call.argumentsText)
        entries.push({
          itemId,
          turnId,
          createdAt: matched?.timestamp || createdAt,
          content: create(TimelineItemContentSchema, {
            content: {
              case: "toolExecution",
              value: create(ToolExecutionSchema, {
                executionId: itemId,
                title: call.name,
                ...(display.input ? { input: display.input } : {}),
                ...(detail ? { detail } : {}),
                phase,
                identity: create(ToolIdentitySchema, {
                  namespace: "doagent",
                  semanticKey,
                  schemaVersion: "1",
                  sourceToolName: call.name,
                }),
                results: output === undefined || output === "" ? [] : [create(ToolResultSchema, {
                  resultId: `${itemId}:result`,
                  primary: true,
                  blocks: [textBlock(`${itemId}:output`, output)],
                })],
                failure: failed ? create(ToolFailureSchema, {
                  code: "doagent_tool_failed",
                  message: output || "工具执行失败",
                }) : undefined,
              }),
            },
          }),
        })
      }
      continue
    }

    if (role === "tool") {
      if (consumedToolIndexes.has(index)) continue
      const output = extractMessageText(msg)
      if (!output.trim()) continue
      const itemId = `history:${index}:tool-result`
      const toolName = stringField(msg, "name") || stringField(msg, "tool_name") || "tool"
      const failed = msg.is_error === true || msg.isError === true
      entries.push({
        itemId,
        turnId: currentTurnId(),
        createdAt,
        content: create(TimelineItemContentSchema, {
          content: {
            case: "toolExecution",
            value: create(ToolExecutionSchema, {
              executionId: itemId,
              title: toolName,
              phase: failed ? ToolPhase.FAILED : ToolPhase.COMPLETED,
              identity: create(ToolIdentitySchema, {
                namespace: "doagent",
                semanticKey: resolveAgentToolSemanticKey(toolName),
                schemaVersion: "1",
                sourceToolName: toolName,
              }),
              results: [create(ToolResultSchema, {
                resultId: `${itemId}:result`,
                primary: true,
                blocks: [textBlock(`${itemId}:output`, output)],
              })],
              failure: failed ? create(ToolFailureSchema, {
                code: "doagent_tool_failed",
                message: output,
              }) : undefined,
            }),
          },
        }),
      })
    }
  }

  return entries.map((entry, idx) => create(TimelineItemSchema, {
    envelope: create(EventEnvelopeSchema, {
      sessionId: options.sessionId,
      streamEpoch: options.streamEpoch,
      revision: 1n,
      sequence: BigInt(idx + 1),
      itemId: entry.itemId,
      turnId: entry.turnId,
      createdAt: entry.createdAt,
    }),
    content: entry.content,
  }))
}

function textBlock(contentId: string, text: string) {
  return create(ContentBlockSchema, {
    contentId,
    identity: { namespace: "doagent", semanticKey: "content.text", schemaVersion: "1" },
    content: { case: "text", value: { text } },
  })
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function stringField(record: Record<string, unknown>, key: string): string {
  const value = record[key]
  return typeof value === "string" ? value : ""
}

function normalizedRole(record: Record<string, unknown>): string {
  return stringField(record, "role").trim().toLowerCase()
}

function validIsoTimestamp(record: Record<string, unknown>): string | undefined {
  for (const key of ["created_at", "createdAt", "timestamp"]) {
    const value = stringField(record, key).trim()
    if (value && Number.isFinite(Date.parse(value))) return value
  }
  return undefined
}

function extractMessageText(record: Record<string, unknown>): string {
  const parts = Array.isArray(record.parts)
    ? record.parts
    : Array.isArray(record.content) ? record.content : null
  if (parts) {
    const pieces: string[] = []
    for (const part of parts) {
      if (typeof part === "string" && part) {
        pieces.push(part)
        continue
      }
      const partRecord = asRecord(part)
      if (!partRecord) continue
      const partType = stringField(partRecord, "type").trim().toLowerCase()
      if (partType === "text" || (!partType && typeof partRecord.text === "string")) {
        const text = stringField(partRecord, "text")
        if (text) pieces.push(text)
      }
    }
    if (pieces.length > 0) return pieces.join("\n")
  }
  if (typeof record.content === "string") return record.content
  return ""
}

function extractAttachmentNames(record: Record<string, unknown>): string[] {
  const parts = Array.isArray(record.parts)
    ? record.parts
    : Array.isArray(record.content) ? record.content : null
  if (!parts) return []
  const names: string[] = []
  for (const part of parts) {
    const partRecord = asRecord(part)
    if (!partRecord) continue
    const partType = stringField(partRecord, "type").trim().toLowerCase()
    if (partType === "text" || !partType) continue
    const name = stringField(partRecord, "filename") || stringField(partRecord, "name") || stringField(partRecord, "media_type") || partType
    if (name) names.push(name)
  }
  return names
}

function extractToolCalls(record: Record<string, unknown>, messageIndex: number): RawToolCall[] {
  const rawList = Array.isArray(record.tool_calls)
    ? record.tool_calls
    : Array.isArray(record.toolCalls) ? record.toolCalls : null
  if (!rawList) return []
  const calls: RawToolCall[] = []
  for (let idx = 0; idx < rawList.length; idx++) {
    const item = asRecord(rawList[idx])
    if (!item) continue
    const fn = asRecord(item.function)
    const id = stringField(item, "id") || stringField(item, "call_id") || stringField(item, "toolCallId") || `history-call-${messageIndex}-${idx}`
    const name = (fn && stringField(fn, "name")) || stringField(item, "name") || stringField(item, "tool_name") || "tool"
    const rawArgs = fn?.arguments ?? item.arguments ?? item.input
    let argumentsText: string | undefined
    if (typeof rawArgs === "string") argumentsText = rawArgs
    else if (rawArgs && typeof rawArgs === "object") {
      try { argumentsText = JSON.stringify(rawArgs) } catch { /* ignore unserializable args */ }
    }
    calls.push({ id, name, argumentsText })
  }
  return calls
}

function compactToolDetail(raw: string | undefined): string | undefined {
  if (!raw) return undefined
  const trimmed = raw.trim()
  if (!trimmed || trimmed === "{}" || trimmed === "null") return undefined
  const singleLine = trimmed.replace(/\s+/g, " ")
  if (singleLine.length <= MAX_TOOL_DETAIL_CHARS) return singleLine
  return singleLine.slice(0, MAX_TOOL_DETAIL_CHARS - 1) + "…"
}

const INJECTED_USER_MARKERS = [
  "\n\n---\nThe following target facts",
  "\n\n[DoOps Target Facts]",
  "\n\n[Gateway-managed credential bindings",
  "\n\nGateway-managed credential bindings (metadata only",
  "\n\nCurrent session workspace directory:",
]

function stripInjectedUserContext(text: string): string {
  let cleaned = text
  for (const marker of INJECTED_USER_MARKERS) {
    const idx = cleaned.indexOf(marker)
    if (idx !== -1) cleaned = cleaned.slice(0, idx)
  }
  return cleaned.trim()
}
