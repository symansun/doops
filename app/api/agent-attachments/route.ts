import { gatewayPolicyResponse, readAllowedAuth } from "@/lib/gateway-policy"
import {
  MAX_AGENT_ATTACHMENT_BYTES,
  requireVisibleAgentAttachmentTarget,
  storeAgentAttachment,
} from "@/lib/server/agent-attachments"
import { AgentGatewayError } from "@/lib/server/agent-gateway-rpc"
import { authorizedAgentSession } from "@/lib/server/agent-session"
import { rejectCrossOriginMutation } from "@/lib/server/same-origin"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

const IDENTIFIER = /^(?!.*\.\.)[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/

export async function POST(req: Request) {
  const rejected = rejectCrossOriginMutation(req)
  if (rejected) return rejected
  try {
    const auth = readAllowedAuth(req)
    if (!auth.gateway || !auth.token) throw new AgentGatewayError("未建立 Gateway 会话", 401)
    const form = await req.formData()
    const cluster = requiredIdentifier(form.get("cluster"), "cluster")
    const instance = requiredIdentifier(form.get("instance"), "instance")
    const sessionId = requiredIdentifier(form.get("session_id"), "session_id")
    const scope = { ...auth, cluster, instance, sessionId }
    await requireVisibleAgentAttachmentTarget(scope, req.signal)
    await authorizedAgentSession(scope, req.signal)
    const file = form.get("file")
    if (!(file instanceof File)) return Response.json({ error: "需要上传文件" }, { status: 400 })
    if (file.size === 0) return Response.json({ error: "附件不能为空" }, { status: 400 })
    if (file.size > MAX_AGENT_ATTACHMENT_BYTES) {
      return Response.json({ error: "附件超过 25 MiB 限制" }, { status: 413 })
    }
    const reference = await storeAgentAttachment(
      scope,
      { name: file.name, mediaType: file.type, bytes: new Uint8Array(await file.arrayBuffer()) },
    )
    return Response.json(reference, { headers: { "Cache-Control": "no-store" } })
  } catch (error) {
    const policy = gatewayPolicyResponse(error)
    if (policy) return policy
    if (error instanceof AgentGatewayError) return Response.json({ error: error.message }, { status: error.status })
    const message = error instanceof Error ? error.message : "附件上传失败"
    const status = message.includes("25 MiB") ? 413 : 400
    return Response.json({ error: message }, { status })
  }
}

function requiredIdentifier(value: FormDataEntryValue | null, field: string): string {
  if (typeof value !== "string" || !IDENTIFIER.test(value)) throw new Error(`需要有效 ${field}`)
  return value
}
