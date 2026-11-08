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
