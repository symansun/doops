import assert from "node:assert/strict"
import test from "node:test"
import {
  buildMachineTagUpdatePayload,
  computeMergedMachineTags,
  isValidMachineTag,
  normalizeSuggestedTags,
} from "./machine-tag-discovery.ts"

test("Given artifact suggested_tags, When normalizing, Then valid CMDB tags pass and secrets or oversized tags are dropped", () => {
  const tags = normalizeSuggestedTags({
    suggested_tags: [
      { tag: "DB=MySQL-8.0", category: "database", confidence: "high", source: "process", evidence: "mysqld 8.0.36" },
      { tag: "runtime=java-17", category: "runtime", confidence: "high", source: "process", evidence: "java 17" },
      { tag: "runtime=java-17", category: "runtime", confidence: "medium", source: "dup", evidence: "dup" },
      { tag: "db=password-123", category: "database", confidence: "high", source: "bad", evidence: "secret" },
      { tag: "token=abc", category: "ops", confidence: "high", source: "bad", evidence: "secret" },
      { tag: "a".repeat(33), category: "ops", confidence: "high", source: "bad", evidence: "too long" },
      { tag: "bad tag with space", category: "ops", confidence: "high", source: "bad", evidence: "invalid" },
    ],
  })
  assert.deepEqual(
    tags.map((t) => t.tag),
    ["db=mysql-8.0", "runtime=java-17"],
  )
  assert.equal(isValidMachineTag("院系=计算机"), true)
  assert.equal(isValidMachineTag("secret=foo"), false)
})

test("Given existing machine tags and mined tags, When merging, Then existing manual tags stay preserved and 20-tag cap is enforced", () => {
  const plan = computeMergedMachineTags(
    ["院系=计算机", "db=mysql-8.0"],
    ["db=mysql-8.0", "runtime=java-17", "app=order-svc-2.1", "web=nginx-1.24"],
  )
  assert.deepEqual(plan.alreadyPresent, ["db=mysql-8.0"])
  assert.deepEqual(plan.added, ["app=order-svc-2.1", "runtime=java-17", "web=nginx-1.24"])
  assert.equal(plan.merged.length, 5)
  assert.equal(plan.exceedsLimit, false)

  const existing18 = Array.from({ length: 18 }, (_, i) => `k${i}=v${i}`)
  const overflow = computeMergedMachineTags(existing18, ["db=mysql-8.0", "runtime=java-17", "runtime=php-8.2"])
  assert.equal(overflow.exceedsLimit, true)
  assert.equal(overflow.overflowCount, 1)
})

test("Given a machine record and merged tags, When building update payload, Then revision lock and existing fields are preserved", () => {
  const payload = buildMachineTagUpdatePayload(
    {
      asset_id: "srv-app-01",
      name: "App Server 01",
      kind: "vm",
      address: "10.0.1.15",
      cpu_count: 8,
      memory_gib: 32,
      disk_gib: 200,
      host_asset_id: "phy-01",
      cluster: "prod",
      instance: "app-01",
      created_at: "2026-11-20T00:00:00Z",
      tags: ["env=prod"],
      revision: 4,
    },
    ["db=postgresql-16", "env=prod", "runtime=python-3.11"],
  )
  assert.equal(payload.action, "update")
  assert.equal(payload.expected_revision, 4)
  assert.equal(payload.machine.asset_id, "srv-app-01")
  assert.equal(payload.machine.cpu_count, 8)
  assert.equal(payload.machine.host_asset_id, "phy-01")
  assert.deepEqual(payload.machine.tags, ["db=postgresql-16", "env=prod", "runtime=python-3.11"])
})
