"use client"

import { useEffect, useRef, useState } from "react"
import { assetRequest } from "@/lib/assets"
import { classifyHost, eachMachine, inspectionKey, inspectionQuery, type HostEvidence, type InspectionTarget } from "@/lib/machine-inspection"

type Summary = ReturnType<typeof classifyHost>
type Progress = { total: number; done: number; skipped: number; failed: number }
const detail = (e: unknown) => e instanceof Error ? e.message : "请求失败"

// GET only reads verified evidence. A pull through the audited tunnel must be
// an explicit operator action. Neither path sends an address or a password.
export function useMachineInspections(machines: InspectionTarget[] | null, visible?: InspectionTarget[]) {
  const [results, setResults] = useState<Record<string, Summary>>({})
  const [progress, setProgress] = useState<Progress | null>(null)
  const [running, setRunning] = useState(false)
  const active = useRef<AbortController | null>(null)
  const touched = useRef(new Set<string>())
  const loaded = useRef(new Set<string>())
  const read = (m: InspectionTarget, signal: AbortSignal) => assetRequest<HostEvidence>("machine-diagnostics", undefined, inspectionQuery(m), signal)
  function put(m: InspectionTarget, s: Summary, signal: AbortSignal) {
    if (!signal.aborted) setResults(prev => ({ ...prev, [inspectionKey(m)]: s }))
  }
  const bound = (m: InspectionTarget) => !!(m.cluster && m.instance)
  useEffect(() => {
    touched.current.clear()
    loaded.current.clear()
    setRunning(false); setProgress(null); setResults({})
    return () => { active.current?.abort(); active.current = null }
  }, [machines])

  const scope = visible ?? machines ?? []
  const scopeKey = scope.filter(bound).map(inspectionKey).join("\0")
  useEffect(() => {
    const pending = scope.filter((m) => {
      if (!bound(m)) return false
      const key = inspectionKey(m)
      return !touched.current.has(key) && !loaded.current.has(key)
    })
    if (!pending.length) return
    const ctrl = new AbortController()
    void eachMachine(pending, ctrl.signal, async m => {
      const key = inspectionKey(m)
      if (touched.current.has(key) || loaded.current.has(key)) return
      loaded.current.add(key)
      let summary: Summary
      try { summary = classifyHost(await read(m, ctrl.signal), Date.now()) }
      catch (e) {
        if (ctrl.signal.aborted) {
          loaded.current.delete(key)
          return
        }
        summary = { label: "无法读取：" + detail(e) }
      }
      if (ctrl.signal.aborted) {
        loaded.current.delete(key)
        return
      }
      if (!touched.current.has(key)) put(m, summary, ctrl.signal)
    })
    return () => { ctrl.abort() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machines, scopeKey])

  async function inspect(rows: InspectionTarget[], isOnline: (m: InspectionTarget) => boolean) {
    if (active.current || !rows.length) return
    // Initial reads for unrelated rows may continue, but never overwrite a pull.
    for (const m of rows) touched.current.add(inspectionKey(m))
    const ctrl = new AbortController(), { signal } = ctrl
    active.current = ctrl
    setRunning(true); setProgress({ total: rows.length, done: 0, skipped: 0, failed: 0 })
    await eachMachine(rows, signal, async m => {
      let skipped = false, failed = false
      try {
        if (!bound(m)) { skipped = true; put(m, { label: "未接入 Agent" }, signal); return }
        put(m, { label: "巡检中…" }, signal)
        let report = await read(m, signal)
        if (report.gap === "binding_missing") skipped = true
        else if (report.evidence_mode === "tunnel") {
          if (!isOnline(m)) { skipped = true; put(m, { label: "Agent 离线，未取样", at: report.sample?.at_ms }, signal); return }
          await assetRequest("machine-diagnostics", { action: "refresh", asset_id: m.asset_id, cluster: m.cluster, instance: m.instance }, undefined, signal)
          report = await read(m, signal)
        }
        put(m, classifyHost(report, Date.now()), signal)
      } catch (e) { failed = true; put(m, { label: "巡检失败：" + detail(e) }, signal) }
      finally {
        if (!signal.aborted) setProgress(p => p && ({ ...p, done: p.done + 1, skipped: p.skipped + Number(skipped), failed: p.failed + Number(failed) }))
      }
    })
    if (!signal.aborted) { setRunning(false); active.current = null }
  }
  return { results, progress, running, inspect }
}
