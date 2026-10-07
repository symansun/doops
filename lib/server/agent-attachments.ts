import { createHash, randomUUID } from "node:crypto"
import { mkdir, readFile, rm, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { basename, join } from "node:path"

export const MAX_AGENT_ATTACHMENT_BYTES = 25 * 1024 * 1024
const ATTACHMENT_TTL_MS = 30 * 60 * 1000
const MAX_PENDING_ATTACHMENTS_PER_SCOPE = 4
const MAX_PENDING_BYTES_PER_SCOPE = 100 * 1024 * 1024
const ATTACHMENT_DIR = join(tmpdir(), "doops-agent-attachments")

export interface AgentAttachmentScope {
  gateway: string
  token: string
  cluster: string
  instance: string
  sessionId: string
}

export interface AgentAttachmentReference {
  id: string
  name: string
  mediaType: string
  bytes: number
}

interface StoredAgentAttachment extends AgentAttachmentReference {
  scopeKey: string
  path: string
  createdAt: number
}

const attachments = new Map<string, StoredAgentAttachment>()

export function agentAttachmentScopeKey(scope: AgentAttachmentScope): string {
  const tokenDigest = createHash("sha256").update(scope.token).digest("hex")
  return JSON.stringify([scope.gateway, tokenDigest, scope.cluster, scope.instance, scope.sessionId])
}

export async function storeAgentAttachment(
  scope: AgentAttachmentScope,
  input: { name: string; mediaType: string; bytes: Uint8Array },
): Promise<AgentAttachmentReference> {
  await pruneExpiredAgentAttachments()
  if (input.bytes.byteLength === 0) throw new Error("附件不能为空")
  if (input.bytes.byteLength > MAX_AGENT_ATTACHMENT_BYTES) throw new Error("附件超过 25 MiB 限制")

  const scopeKey = agentAttachmentScopeKey(scope)
  const pending = [...attachments.values()].filter((record) => record.scopeKey === scopeKey)
  if (pending.length >= MAX_PENDING_ATTACHMENTS_PER_SCOPE || pending.reduce((total, record) => total + record.bytes, 0) + input.bytes.byteLength > MAX_PENDING_BYTES_PER_SCOPE) {
    throw new Error("当前会话最多保留 4 个待发送附件或 100 MiB")
  }

  const id = `att_${randomUUID().replaceAll("-", "")}`
  const name = safeFilename(input.name)
  const mediaType = safeMediaType(input.mediaType)
  const path = join(ATTACHMENT_DIR, id)
  await mkdir(ATTACHMENT_DIR, { recursive: true, mode: 0o700 })
  await writeFile(path, input.bytes, { mode: 0o600 })
  attachments.set(id, {
    id,
    name,
    mediaType,
    bytes: input.bytes.byteLength,
    scopeKey,
    path,
    createdAt: Date.now(),
  })
  return { id, name, mediaType, bytes: input.bytes.byteLength }
}

export async function consumeAgentAttachment(
  scope: AgentAttachmentScope,
  id: string,
): Promise<AgentAttachmentReference & { base64Data: string }> {
  await pruneExpiredAgentAttachments()
  const record = attachments.get(id)
  if (!record || record.scopeKey !== agentAttachmentScopeKey(scope)) {
    throw new Error("附件不存在或已过期")
  }
  const bytes = await readFile(record.path)
  await removeStoredAttachment(record)
  return { id: record.id, name: record.name, mediaType: record.mediaType, bytes: record.bytes, base64Data: bytes.toString("base64") }
}

async function pruneExpiredAgentAttachments(): Promise<void> {
  const cutoff = Date.now() - ATTACHMENT_TTL_MS
  await Promise.all([...attachments.values()].filter((record) => record.createdAt < cutoff).map(removeStoredAttachment))
}

async function removeStoredAttachment(record: StoredAgentAttachment): Promise<void> {
  attachments.delete(record.id)
  await rm(record.path, { force: true })
}

function safeFilename(value: string): string {
  const name = basename(value || "attachment").replace(/[\u0000-\u001f\u007f]/g, "_").trim()
  return (name || "attachment").slice(0, 255)
}

function safeMediaType(value: string): string {
  const mediaType = (value || "application/octet-stream").trim()
  if (!mediaType || mediaType.length > 255 || /[\u0000-\u001f\u007f]/.test(mediaType)) return "application/octet-stream"
  return mediaType
}
