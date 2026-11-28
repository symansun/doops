// A machine is identified by its registration AND its current Agent binding.
export type InspectionTarget = { asset_id: string; cluster?: string; instance?: string }
export type HostEvidence = {
  evidence_mode: "mount" | "tunnel"; gap?: string
  sample: null | { at_ms: number; memory_used_bytes?: number | null; memory_total_bytes?: number | null }
  comparison?: { cpu_percent?: number }
}
export const inspectionKey = (m: InspectionTarget) => JSON.stringify([m.asset_id, m.cluster, m.instance])
export const inspectionQuery = (m: InspectionTarget) => new URLSearchParams({
  asset_id: m.asset_id, cluster: m.cluster ?? "", instance: m.instance ?? "", range: "1h",
})

export type HostSummary = { label: string; at?: number; metrics?: string }
export function classifyHost(evidence: HostEvidence, now: number): HostSummary {
  const s = evidence.sample
  if (!s) return { label: evidence.gap === "binding_missing" ? "待核验宿主" : "近 1 小时无样本" }
  if (!Number.isFinite(s.at_ms) || s.at_ms <= 0 || s.at_ms > now) return { label: "无有效采样时间" }
  if (now - s.at_ms > 300_000) return { label: "样本已过期", at: s.at_ms }
  const cpu = evidence.comparison?.cpu_percent
  const mem = s.memory_total_bytes != null && s.memory_total_bytes > 0 && s.memory_used_bytes != null ? 100 * s.memory_used_bytes / s.memory_total_bytes : undefined
  const high = (cpu != null && cpu >= 90) || (mem != null && mem >= 90)
  const metrics = [cpu != null && `CPU ${cpu.toFixed(1)}%`, mem != null && `内存 ${mem.toFixed(1)}%`].filter(Boolean).join(" · ")
  return { label: high ? "资源使用率高" : cpu == null || mem == null ? "指标不完整" : "资源使用率正常", at: s.at_ms, metrics }
}

// Keep one machine's failure from cancelling the others. No more than two reads
// or pulls are in flight; leaving the ledger stops dispatching pending work.
export async function eachMachine<T>(rows: T[], signal: AbortSignal, visit: (row: T) => Promise<void>) {
  let cursor = 0
  async function worker() {
    while (!signal.aborted && cursor < rows.length) await visit(rows[cursor++])
  }
  await Promise.all([worker(), worker()])
}
