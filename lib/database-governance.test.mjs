import assert from "node:assert/strict"
import test from "node:test"
import {
  buildDiffRefOptions,
  describeDiffChangeKind,
  describeGovernanceError,
  describeSegmentKind,
  diffDatabaseSchema,
  filterHistoryRecords,
  getMigrationStatus,
  inspectDatabaseSchema,
  isValidDiffRef,
  lintDatabaseSql,
  listDatabaseHistory,
  listDatabaseVersions,
  summarizeSchema,
} from "./database-governance.ts"
import { buttonNamed, componentHarness, componentStubs, elementText } from "./component-test-harness.mjs"

const baseOpts = {
  session: { gateway: "https://gateway.example.test" },
  target: { cluster: "doops-test", instance: "node-1" },
  sessionId: "gov-test",
  engine: "mysql",
  database: "orders_db",
  credential: { id: "cred-db-1", name: "orders-rw" },
}

function resultResponse(payload) {
  return new Response(JSON.stringify({ type: "result", result: { content: [{ type: "text", text: JSON.stringify(payload) }] } }) + "\n")
}

test("inspectDatabaseSchema sends read-only sql_action=inspect and rejects mismatched identity", async (t) => {
  const bodies = []
  let payload = {
    mode: "inspect",
    credential: "orders-rw",
    schema: {
      engine: "mysql",
      database: "orders_db",
      schema_hash: "sha256:abc123",
      table_count: 2,
      tables: [
        { name: "orders", kind: "TABLE", columns: [{ name: "id", type: "bigint", nullable: false }], indexes: [{ name: "PRIMARY", unique: true, primary: true, columns: ["id"] }] },
        { name: "v_orders", kind: "VIEW", columns: [{ name: "id", type: "bigint", nullable: false }] },
      ],
    },
  }
  t.mock.method(globalThis, "fetch", async (_url, init) => {
    bodies.push(JSON.parse(init.body))
    return resultResponse(payload)
  })

  const report = await inspectDatabaseSchema(baseOpts)
  assert.equal(bodies.length, 1)
  assert.equal(bodies[0].tool, "doops_agent_prompt")
  assert.equal(bodies[0].arguments.operation, "ask")
  assert.equal(bodies[0].arguments.skill, "dosql")
  assert.equal(bodies[0].arguments.credential_mode, "auto")
  assert.equal(bodies[0].arguments.sql_credential, "cred-db-1")
  assert.equal(bodies[0].arguments.sql_database, "orders_db")
  assert.equal(bodies[0].arguments.sql_action, "inspect")
  assert.deepEqual(report.schema.tables[1].indexes, [])
  assert.deepEqual(report.schema.tables[1].foreign_keys, [])
  assert.deepEqual(summarizeSchema(report.schema), {
    baseTableCount: 1,
    viewCount: 1,
    columnCount: 2,
    indexCount: 1,
    foreignKeyCount: 0,
  })

  // Unsupported engine fails closed before network.
  await assert.rejects(inspectDatabaseSchema({ ...baseOpts, engine: "oracle" }), /暂不支持/)
  assert.equal(bodies.length, 1)

  // Mismatched database or credential fails closed.
  payload = { ...payload, schema: { ...payload.schema, database: "wrong_db" } }
  await assert.rejects(inspectDatabaseSchema(baseOpts), /不匹配/)
  payload = { mode: "inspect", credential: "other-cred", schema: { engine: "mysql", database: "orders_db", schema_hash: "sha256:x", tables: [] } }
  await assert.rejects(inspectDatabaseSchema(baseOpts), /凭据/)
})

test("listDatabaseVersions normalizes Go PascalCase summaries and sorts snapshots", async (t) => {
  t.mock.method(globalThis, "fetch", async () =>
    resultResponse({
      mode: "versions",
      engine: "mysql",
      database: "orders_db",
      credential: "orders-rw",
      versions: [
        { Version: 1, OperationID: "dbop_1", Status: "verified", Committed: true, ImplicitCommits: 1 },
        { Version: 3, OperationID: "dbop_3", Status: "unfinished", Committed: false, ImplicitCommits: 0 },
      ],
      snapshots: [3, 1],
    }),
  )
  const report = await listDatabaseVersions(baseOpts)
  assert.deepEqual(report.snapshots, [1, 3])
  assert.deepEqual(report.versions.map((v) => v.version), [3, 1])
  assert.equal(report.versions[0].operation_id, "dbop_3")
  assert.equal(report.versions[1].implicit_commits, 1)
  assert.deepEqual(buildDiffRefOptions(report.snapshots).map((o) => o.value), ["live", "latest", "v3", "v1"])
  assert.deepEqual(buildDiffRefOptions([]).map((o) => o.value), ["live"])
})

test("diffDatabaseSchema validates version refs fail-closed and parses diff receipt", async (t) => {
  const sent = []
  t.mock.method(globalThis, "fetch", async (_url, init) => {
    sent.push(JSON.parse(init.body))
    return resultResponse({
      mode: "diff",
      from: "v1",
      to: "live",
      credential: "orders-rw",
      diff: {
        engine: "mysql",
        from_database: "orders_db",
        to_database: "orders_db",
        from_hash: "sha256:1",
        to_hash: "sha256:2",
        identical: false,
        has_destructive: true,
        changes: [{ kind: "drop_column", table: "orders", object: "legacy", destructive: true }],
        suggested_ddl: ["ALTER TABLE `orders` DROP COLUMN `legacy`;"],
      },
    })
  })
  assert.equal(isValidDiffRef("live"), true)
  assert.equal(isValidDiffRef("latest"), true)
  assert.equal(isValidDiffRef("v12"), true)
  assert.equal(isValidDiffRef("v0"), false)
  assert.equal(isValidDiffRef("../etc"), false)

  await assert.rejects(diffDatabaseSchema({ ...baseOpts, from: "v0", to: "live" }), /引用无效/)
  assert.equal(sent.length, 0)

  const res = await diffDatabaseSchema({ ...baseOpts, from: "v1", to: "live" })
  assert.equal(sent[0].arguments.sql_diff_from, "v1")
  assert.equal(sent[0].arguments.sql_diff_to, "live")
  assert.equal(res.diff.identical, false)
  assert.equal(res.diff.has_destructive, true)
  assert.deepEqual(res.diff.possible_renames, [])
  assert.equal(describeDiffChangeKind("drop_column").tone, "danger")
})

test("listDatabaseHistory, getMigrationStatus, and lintDatabaseSql send exact read-only RPCs", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (_url, init) => {
    const body = JSON.parse(init.body)
    calls.push(body.arguments)
    if (body.arguments.sql_action === "history") {
      return resultResponse({
        mode: "history",
        engine: "mysql",
        database: "orders_db",
        credential: "orders-rw",
        records: [
          { seq: 1, timestamp: "2026-11-27T10:00:00Z", operation_id: "op1", engine: "mysql", database: "orders_db", mode: "apply", kind: "ddl", statement: "CREATE TABLE a (id INT)", stmt_hash: "h1", duration_ms: 5, status: "succeeded" },
          { seq: 2, timestamp: "2026-11-27T10:05:00Z", operation_id: "op2", engine: "mysql", database: "orders_db", mode: "query", kind: "read", statement: "SELECT 1", stmt_hash: "h2", duration_ms: 1, status: "succeeded" },
        ],
      })
    }
    if (body.arguments.sql_action === "status") {
      return resultResponse({
        mode: "status",
        engine: "mysql",
        database: "orders_db",
        credential: "orders-rw",
        status: { current_version: 2, applied: null, pending: null, dirty: null },
      })
    }
    return resultResponse({
      mode: "lint",
      engine: "mysql",
      database: "orders_db",
      statements: 1,
      segments: [{ index: 1, kind: "transaction", atomic: true, statements: [{ index: 1, kind: "dml" }] }],
      policy: {
        passed: false,
        errors: 1,
        warnings: 0,
        findings: [{ rule_id: "forbid_unbounded_update_delete", severity: "error", statement_index: 1, message: "missing WHERE" }],
      },
    })
  })

  const hist = await listDatabaseHistory({ ...baseOpts, allSessions: true })
  assert.equal(calls[0].sql_all_sessions, true)
  const filtered = filterHistoryRecords(hist.records, { mode: "apply", query: "CREATE" })
  assert.equal(filtered.length, 1)
  assert.equal(filtered[0].seq, 1)

  const st = await getMigrationStatus(baseOpts)
  assert.equal(calls[1].operation, "ask")
  assert.equal(calls[1].sql_action, "status")
  assert.deepEqual(st.status.applied, [])
  assert.equal(st.status.current_version, 2)

  await assert.rejects(lintDatabaseSql({ ...baseOpts, sql: "   " }), /待检查/)
  const lint = await lintDatabaseSql({ ...baseOpts, sql: "DELETE FROM orders;" })
  assert.equal(lint.policy.passed, false)
  assert.equal(lint.policy.findings[0].rule_id, "forbid_unbounded_update_delete")
  assert.equal(describeSegmentKind("implicit_commit").label, "隐式提交 DDL")
  assert.match(describeGovernanceError("dosql_action_unsupported: lint"), /升级 Agent/)
})

test("DatabaseGovernance switches across Schema, Versions+Diff, History, and Status without infinite refetch on inline callbacks", async () => {
  const calls = { inspect: 0, versions: 0, diff: 0, history: 0, status: 0 }
  const govMocks = {
    supportsSchemaInspect: () => true,
    buildDiffRefOptions,
    describeDiffChangeKind,
    describeGovernanceError,
    filterHistoryRecords,
    summarizeSchema,
    inspectDatabaseSchema: async () => {
      calls.inspect++
      return {
        mode: "inspect",
        credential: "orders-rw",
        schema: {
          engine: "mysql",
          database: "orders_db",
          schema_hash: "sha256:998877",
          table_count: 1,
          tables: [{
            name: "orders",
            kind: "TABLE",
            engine: "InnoDB",
            columns: [{ name: "id", type: "bigint", nullable: false }],
            indexes: [{ name: "PRIMARY", unique: true, primary: true, columns: ["id"] }],
            foreign_keys: [],
          }],
        },
      }
    },
    listDatabaseVersions: async () => {
      calls.versions++
      return {
        mode: "versions",
        engine: "mysql",
        database: "orders_db",
        credential: "orders-rw",
        versions: [{ version: 2, operation_id: "dbop_v2", status: "verified", committed: true, implicit_commits: 1 }],
        snapshots: [1, 2],
      }
    },
    diffDatabaseSchema: async ({ from, to }) => {
      calls.diff++
      return {
        mode: "diff",
        from,
        to,
        credential: "orders-rw",
        diff: {
          engine: "mysql",
          from_database: "orders_db",
          to_database: "orders_db",
          from_hash: "sha256:1",
          to_hash: "sha256:2",
          identical: false,
          has_destructive: false,
          changes: [{ kind: "add_column", table: "orders", object: "memo", after: "varchar(64)" }],
          suggested_ddl: ["ALTER TABLE `orders` ADD COLUMN `memo` varchar(64);"],
          possible_renames: [],
        },
      }
    },
    listDatabaseHistory: async () => {
      calls.history++
      return {
        mode: "history",
        engine: "mysql",
        database: "orders_db",
        credential: "orders-rw",
        records: [{
          seq: 5, timestamp: "2026-11-27T12:00:00Z", operation_id: "dbop_h5", version: 2,
          engine: "mysql", database: "orders_db", mode: "apply", kind: "ddl",
          statement: "ALTER TABLE orders ADD COLUMN memo VARCHAR(64)", stmt_hash: "hh",
          affected_rows: 0, duration_ms: 14, status: "succeeded",
        }],
      }
    },
    getMigrationStatus: async () => {
      calls.status++
      return {
        mode: "status",
        engine: "mysql",
        database: "orders_db",
        credential: "orders-rw",
        status: {
          current_version: 2,
          applied: [{ version: 1, description: "init", type: "execute", checksum: "sha256:c1", applied: 2, total: 2, status: "succeeded", applied_at: "2026-11-27T11:00:00Z", duration_ms: 10 }],
          pending: [],
          dirty: { version: 2, description: "partial", type: "execute", checksum: "sha256:c2", applied: 1, total: 2, status: "failed", error: "duplicate column", applied_at: "2026-11-27T11:30:00Z", duration_ms: 8 },
        },
      }
    },
  }

  const runningEvents = []
  const props = {
    ...baseOpts,
    asset: { database_asset_id: "db-1", namespace: "prod", service: "mysql", engine: "mysql", host: "mysql", status: "ready", source: "service" },
    disabled: false,
    onRunningChange: (v) => runningEvents.push(v),
  }
  const view = componentHarness("../components/database-governance.tsx", "DatabaseGovernance", props, {
    "lucide-react": componentStubs,
    "./ui": componentStubs,
    "@/lib/database-governance": govMocks,
  })

  await view.flush()
  // Re-render with a fresh inline onRunningChange closure (as DatabasePanel does) — must NOT trigger another inspect!
  props.onRunningChange = (v) => runningEvents.push(v)
  await view.flush()
  assert.equal(calls.inspect, 1)
  assert.match(elementText(view.one((n) => n.props["aria-label"] === "Schema 结构视图")), /sha256:998877/)

  // Switch to Versions + Drift tab and execute diff.
  const tabControl = () => view.one((n) => n.type === "SegmentedControl" && n.props.ariaLabel === "治理视图")
  tabControl().props.onChange("versions")
  await view.flush()
  assert.equal(calls.versions, 1)
  assert.match(elementText(view.one((n) => n.props["aria-label"] === "版本与漂移视图")), /dbop_v2/)
  await view.one(buttonNamed("对比结构漂移")).props.onClick()
  await view.flush()
  assert.equal(calls.diff, 1)
  assert.match(elementText(view.one((n) => n.props["aria-label"] === "结构漂移结果")), /ADD COLUMN `memo`/)

  // Switch to History tab.
  tabControl().props.onChange("history")
  await view.flush()
  assert.equal(calls.history, 1)
  assert.match(elementText(view.one((n) => n.props["aria-label"] === "变更流水视图")), /dbop_h5/)

  // Switch to Migration Status tab and verify dirty migration banner renders.
  tabControl().props.onChange("status")
  await view.flush()
  assert.equal(calls.status, 1)
  assert.match(elementText(view.one((n) => n.props["aria-label"] === "迁移状态视图")), /脏迁移版本 v2/)
  assert.equal(runningEvents[runningEvents.length - 1], false)
})
