import test from "node:test"
import assert from "node:assert/strict"
import {
  buildBatchMachineTagPlans,
  buildPluginCatalogPayload,
  createEmptyPluginCatalogRow,
  formatPluginCatalogAddress,
  pluginCatalogKind,
  validatePluginCatalogDraft,
} from "./catalog-extensions.ts"

const sampleZoneSchema = {
  key: "zone",
  label: "插件域名目录",
  description: "由插件 plugin.yaml 动态声明的资产目录",
  env_var: "DOOPS_PLUGIN_ZONES",
  primary_field: "zone",
  fields: [
    { key: "zone", label: "域名", placeholder: "example.org", type: "domain" },
    { key: "server", label: "服务地址", placeholder: "10.0.0.53", type: "host", default: "127.0.0.1" },
    { key: "port", label: "端口", type: "port", default: "53" },
    { key: "mode", label: "模式", type: "enum", options: ["authoritative", "recursive"], default: "authoritative" },
  ],
}

test("pluginCatalogKind and schema-driven draft validation enforce every field type generically", () => {
  assert.equal(pluginCatalogKind("ext-plugin", "zone"), "plugin:ext-plugin:zone")

  const row = createEmptyPluginCatalogRow("ext-plugin", sampleZoneSchema, { cluster: "prod", instance: "node-01" })
  assert.equal(row.kind, "plugin:ext-plugin:zone")
  assert.equal(row.details.server, "127.0.0.1")
  assert.equal(row.details.port, "53")
  assert.equal(row.details.mode, "authoritative")

  row.name = "内网主区"
  row.details.zone = "corp.example.org."
  row.details.server = "10.0.0.53"

  const ok = validatePluginCatalogDraft(sampleZoneSchema, row)
  assert.equal(ok.ok, true)
  if (ok.ok) {
    assert.equal(
      formatPluginCatalogAddress(sampleZoneSchema, ok.normalized),
      "corp.example.org. · 服务地址: 10.0.0.53 · 端口: 53 · 模式: authoritative",
    )
    const payload = buildPluginCatalogPayload(ok.normalized, "create")
    assert.equal(payload.op, "create")
    assert.equal(payload.kind, "plugin:ext-plugin:zone")
  }

  // Invalid domain rejected
  assert.equal(
    validatePluginCatalogDraft(sampleZoneSchema, {
      ...row,
      details: { ...row.details, zone: "not a valid domain!!" },
    }).ok,
    false,
  )
  // IP rejected for domain type
  assert.equal(
    validatePluginCatalogDraft(sampleZoneSchema, {
      ...row,
      details: { ...row.details, zone: "10.0.0.1" },
    }).ok,
    false,
  )
  // Out-of-range port rejected
  assert.equal(
    validatePluginCatalogDraft(sampleZoneSchema, {
      ...row,
      details: { ...row.details, port: "70000" },
    }).ok,
    false,
  )
  // Unlisted enum value rejected
  assert.equal(
    validatePluginCatalogDraft(sampleZoneSchema, {
      ...row,
      details: { ...row.details, mode: "evil" },
    }).ok,
    false,
  )
  // URL rejected for host type
  assert.equal(
    validatePluginCatalogDraft(sampleZoneSchema, {
      ...row,
      details: { ...row.details, server: "http://bad.example.org/x" },
    }).ok,
    false,
  )
})

test("buildBatchMachineTagPlans maps multi-target discovery items to machines and computes merge diffs", () => {
  const machines = [
    {
      asset_id: "vm-01",
      name: "订单节点",
      kind: "vm",
      address: "10.0.1.10",
      cluster: "prod",
      instance: "node-1",
      tags: ["env=prod", "db=mysql-8.0"],
      revision: 3,
    },
    {
      asset_id: "vm-02",
      name: "门户节点",
      kind: "vm",
      address: "10.0.1.11",
      cluster: "prod",
      instance: "node-2",
      tags: ["env=prod"],
      revision: 1,
    },
    {
      asset_id: "vm-unbound",
      name: "未接入节点",
      kind: "vm",
      address: "10.0.1.99",
      tags: [],
      revision: 1,
    },
  ]
  const items = [
    {
      cluster: "prod",
      instance: "node-1",
      status: "ok",
      artifact_json: {
        suggested_tags: [
          { tag: "db=mysql-8.0", category: "db", confidence: "high", source: "mysqld" },
          { tag: "lang=java-17", category: "lang", confidence: "high", source: "java" },
          { tag: "app=order-api-2.1", category: "app", confidence: "high", source: "jar" },
        ],
      },
    },
    {
      cluster: "prod",
      instance: "node-2",
      status: "ok",
      artifact_json: {
        suggested_tags: [
          { tag: "lang=php-8.2", category: "lang", confidence: "high", source: "php" },
          { tag: "app=wordpress-6.5", category: "app", confidence: "high", source: "wp" },
        ],
      },
    },
  ]

  const plans = buildBatchMachineTagPlans(machines, items)
  assert.equal(plans.length, 2)
  assert.equal(plans[0].machine.asset_id, "vm-01")
  assert.deepEqual(plans[0].mergePlan.added, ["app=order-api-2.1", "lang=java-17"])
  assert.deepEqual(plans[0].mergePlan.alreadyPresent, ["db=mysql-8.0"])
  assert.equal(plans[1].machine.asset_id, "vm-02")
  assert.deepEqual(plans[1].mergePlan.added, ["app=wordpress-6.5", "lang=php-8.2"])

  const errorPlans = buildBatchMachineTagPlans(machines, [
    {
      cluster: "prod",
      instance: "node-1",
      status: "error",
      reason: "skill_sync_failed",
      checks: [{ status: "critical", detail: "无法将 Skill 同步至目标 Agent" }],
    },
  ])
  assert.equal(errorPlans.length, 1)
  assert.equal(errorPlans[0].runError, "无法将 Skill 同步至目标 Agent")
})
