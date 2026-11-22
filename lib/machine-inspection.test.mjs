import test from 'node:test'
import assert from 'node:assert/strict'
import { classifyHost, eachMachine, inspectionKey } from './machine-inspection.ts'

const now = 1_000_000
const sample = { at_ms: now, memory_used_bytes: 45, memory_total_bytes: 100 }
const classify = (s, cpu, gap) => classifyHost({ sample: s, comparison: { cpu_percent: cpu }, gap, evidence_mode: 'tunnel' }, now)
test('machine state is based on fresh evidence, not Agent reachability', () => {
  assert.equal(classify(null, undefined, 'binding_missing').label, '待核验宿主')
  assert.equal(classify(null, undefined, 'sample_missing').label, '近 1 小时无样本')
  assert.equal(classify({ ...sample, at_ms: now - 300001 }, 1).label, '样本已过期')
  assert.equal(classify(sample, undefined).label, '指标不完整')
  assert.equal(classify(sample, 90).label, '资源使用率高')
  assert.equal(classify({ ...sample, memory_used_bytes: 90 }, 0).label, '资源使用率高')
  assert.deepEqual(classify(sample, 0), { label: '资源使用率正常', at: now, metrics: 'CPU 0.0% · 内存 45.0%' })
})
test('results cannot cross machine bindings', () => {
  const m = { asset_id: 'm', cluster: 'dev', instance: 'a' }
  assert.notEqual(inspectionKey(m), inspectionKey({ ...m, instance: 'b' }))
  assert.notEqual(inspectionKey(m), inspectionKey({ ...m, asset_id: 'other' }))
})
test('dispatch is bounded and stops on cancellation', async () => {
  const ctrl = new AbortController(); const started = []
  let release; const pending = new Promise(resolve => { release = resolve })
  const work = eachMachine([1,2,3,4], ctrl.signal, async m => { started.push(m); await pending })
  assert.deepEqual(started, [1,2]); ctrl.abort(); release(); await work
  assert.deepEqual(started, [1,2])
})
