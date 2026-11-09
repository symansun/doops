import type { AgentSessionRuntime, AgentSessionSnapshot } from "@agent-cloud/agent-ui"

// Server-side wording for a target whose Agent has no delivered model yet
// (metadata status awaiting_model_provider / unconfigured).
export const AGENT_MODEL_MISSING = "Gateway 还没有可下发到该节点的模型"
// DoAgent's model comes only from the modelProvider credential Gateway materializes
// on the target (backend/docs/CREDENTIAL_RESOURCES.md), so the hint points at credentials.
export const AGENT_MODEL_MISSING_HINT = "该目标还没有绑定 DoAgent 模型。平台管理员可在「凭据」里为该目标绑定并激活 modelProvider 凭据"

// ConnectError prefixes its message with the RPC code, e.g. "[unavailable] ...".
const CONNECT_CODE_PREFIX = /^\[[a-z_]+\]\s*/

// agentErrorMessage turns a DoAgent session error into the text shown to users:
// no transport code prefix, and the missing-model case says what to do next.
export function agentErrorMessage(raw: string | null | undefined): string | null {
  const trimmed = (raw ?? "").trim()
  if (!trimmed) return null
  const message = trimmed.replace(CONNECT_CODE_PREFIX, "").trim()
  if (!message) return "智能体连接失败"
  if (message === AGENT_MODEL_MISSING) return AGENT_MODEL_MISSING_HINT
  return message
}

// Stable categories for a refused doops_agent_prompt: the Gateway classifies doagent's
// session/prompt reply once (backend/agent/internal/server/doagent_prompt_admission.go) and
// returns these literal wire categories. The console classifies them in one place as well, so no
// page has to guess protocol text again.
export type AgentAdmissionKind = "queued" | "queue_cleanup_unknown" | "no_prompt" | "rejected" | "unknown" | "other"

export interface AgentAdmission {
  kind: AgentAdmissionKind
  // Whether the same task may be submitted again: a queued admission provably created no turn, so
  // it may be retried -- preferably in a NEW session, which does not inherit a stale per-session queue.
  retryable: boolean
  // Whether the target may already have changed, so the operator must verify before retrying.
  needsMutationRecheck: boolean
}

// The category can sit anywhere in the text: the same one arrives as "doagent_prompt_queued",
// "remote error: doagent_prompt_queued" (CLI) or "-32008 execution_outcome_unknown: prompt_turn_id_missing".
// The unknown outcome always wins: an older Gateway wraps a pre-admission failure as
// "execution_outcome_unknown: remote error: doagent_prompt_queued", and that fail-closed mutation
// gate must never be downgraded to a retryable admission category.
const ADMISSION_RULES: readonly (AgentAdmission & { pattern: RegExp })[] = [
  // The queue entry may still execute: cleanup was never confirmed, so never retry automatically.
  { kind: "queue_cleanup_unknown", retryable: false, needsMutationRecheck: true, pattern: /\bdoagent_prompt_queue_cleanup_unknown\b/ },
  { kind: "queued", retryable: true, needsMutationRecheck: false, pattern: /\bdoagent_prompt_queued\b/ },
  { kind: "no_prompt", retryable: false, needsMutationRecheck: false, pattern: /\bdoagent_prompt_no_prompt\b/ },
  { kind: "rejected", retryable: false, needsMutationRecheck: false, pattern: /\bdoagent_prompt_rejected\b/ },
]

const OUTCOME_UNKNOWN = /\bexecution_outcome_unknown\b/
const NOT_ADMITTED: AgentAdmission = { kind: "other", retryable: false, needsMutationRecheck: false }
const OUTCOME_UNKNOWN_POLICY: AgentAdmission = { kind: "unknown", retryable: false, needsMutationRecheck: true }

// classifyAgentAdmission maps a session error (message plus, where the caller keeps it, the receipt
// code) to the admission category and its retry policy. "unknown" is the existing outcome-unknown
// contract: the target may already have changed, so nothing may be replayed automatically.
export function classifyAgentAdmission(raw: string | null | undefined, code?: string | null): AgentAdmission {
  const message = (raw ?? "").trim()
  if (code === "doops_apply_outcome_unknown" || OUTCOME_UNKNOWN.test(message)) return { ...OUTCOME_UNKNOWN_POLICY }
  for (const { pattern, ...policy } of ADMISSION_RULES) {
    if (pattern.test(message)) return { ...policy }
  }
  return { ...NOT_ADMITTED }
}

// The console keeps the AI assistant mounted while other tabs are open so a
// running task survives tab switches. createVisibilityScopedRuntime wraps its
// runtime so the hidden assistant does not render its connection error; the
// connection itself is untouched and the error returns when it is shown again.
export function createVisibilityScopedRuntime<Runtime extends AgentSessionRuntime>(
  runtime: Runtime,
  initiallyVisible: boolean,
): { runtime: Runtime; setVisible: (visible: boolean) => void } {
  let visible = initiallyVisible
  const listeners = new Set<() => void>()
  let source: AgentSessionSnapshot | undefined
  let quiet: AgentSessionSnapshot | undefined
  const overrides: Record<string, unknown> = {
    getSnapshot(sessionId: string): AgentSessionSnapshot {
      const snapshot = runtime.getSnapshot(sessionId)
      if (visible || !snapshot.error) return snapshot
      // useSyncExternalStore needs the same object until the source changes.
      if (snapshot !== source || !quiet) {
        source = snapshot
        quiet = { ...snapshot, error: null }
      }
      return quiet
    },
    subscribe(sessionId: string, listener: () => void): () => void {
      listeners.add(listener)
      const unsubscribe = runtime.subscribe(sessionId, listener)
      return () => {
        listeners.delete(listener)
        unsubscribe()
      }
    },
  }
  // Methods are bound once so components that keep them in hook deps see stable references.
  const bound = new Map<PropertyKey, unknown>()
  const scoped = new Proxy(runtime, {
    get(target, property) {
      if (typeof property === "string" && Object.hasOwn(overrides, property)) return overrides[property]
      const value = Reflect.get(target, property, target)
      if (typeof value !== "function") return value
      if (!bound.has(property)) bound.set(property, value.bind(target))
      return bound.get(property)
    },
  })
  return {
    runtime: scoped,
    setVisible(next) {
      if (next === visible) return
      visible = next
      for (const listener of [...listeners]) listener()
    },
  }
}
