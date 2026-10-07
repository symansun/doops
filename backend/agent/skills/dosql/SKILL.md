---
name: dosql-agent
description: DoSql 数据库变更控制助手。用于通过 doagent/CLI 对 MySQL、PostgreSQL、MongoDB 执行受控查询、变更计划、迁移脚本生成、多环境迁移和审计留痕。
metadata:
  family: executable
  runtime: node
  entrypoint: scripts/dosql-agent.mjs
---

# DoSql Agent

DoSql is a skill inside the DoOps Agent runtime. The user authenticates once to
the DoOps Gateway, selects a Gateway target, and then asks doagent to route the
database task here. DoSql does not implement login, target grants, Gateway
token parsing or a second user authorization system.

The core rule is simple: do not bypass the DoSql proxy path. Every database
query, DDL, DML and MongoDB command must create an operation record before it is
executed.

## Intended Capabilities

- Resolve the target project, environment and database asset.
- Discover candidate databases from target probe output, then ask the user to
  confirm business names and aliases.
- Classify the operation as read, schema change, data change, admin or unknown.
- Decide whether the operation enters the formal change document.
- Generate MySQL/PostgreSQL SQL migration scripts.
- Generate MongoDB migration scripts or controlled command documents.
- Execute read queries and migrations through the DoSql CLI.
- Use `scripts/dosql-agent.mjs` for the first implemented Agent commands:
  `classify`, `query`, `execute`, `discover-databases`, `register-database`, `resolve-database`,
  `compare-databases`, `derive-change-plan-from-comparison`,
  `render-forward-change-artifacts`, `execute-forward-artifacts`,
  `render-forward-evidence-metadata-commit`, `scan`, `record-execution`,
  `inspect-journal`, `migrate-journal`,
  `replay-timeline`, timeline lookup, rollback planning,
  restore-node baseline derivation and `propose-confirmation`.
- Record SQL/MongoDB execution lifecycle into append-only JSONL journals before
  importing the same events into the future control-plane database.
- Return affected rows, schema snapshots, dry-run output and verification
  evidence.
- Explain each mutating change in user-readable language before execution.
- Scan database health, extract SQL logs and detect SQL injection indicators
  through read-only commands.

## Safety Rules

- Read-only queries are audited but do not enter a change document by default.
- Schema changes, data changes, admin operations and unknown operations always
  enter the change document.
- A user may explicitly include a read-only query in a change document.
- A user may not exclude a mutating operation from a change document.
- Production execution requires an approved change request and execution
  evidence. The signed approval is change-control evidence bound to the exact
  operation and verification fingerprints; it is not a second login or a
  replacement for Gateway target/action authorization.
- MongoDB commands must use structured `mongoOperation` documents. Raw
  JavaScript is never executed, and queries require a database read-only role.
- Scanning modules are read-only and must still produce operation records and
  evidence.
- A mutating execution must have JSONL lifecycle events for planned, running,
  succeeded or failed, and verified or verification_failed.
- Users confirm the change summary, impact, risk and version movement. They do
  not confirm raw SQL as the primary approval surface.
- If a database name resolves to multiple assets, ask the user to choose the
  environment or database. Do not guess.
- 不允许用兼容分支或静默降级掩盖真实失败。

## Target And Credential Boundary

- The selected `cluster/instance` comes from the DoOps Gateway target selected
  by the user. Do not read a skill-local default target or invent one from a
  product URL.
- Discover database assets from the selected target and require an explicit
  choice when more than one asset matches. `config/resources.json` is optional
  metadata for labels and examples; it is not a target registry or permission
  source.
- Database credentials are DoOps managed credentials. Pass only credential
  resource names to the Gateway; never put payloads, connection strings or
  passwords in prompts, artifacts or logs.
- Gateway target/action checks happen before the skill runs. DoSql's operation
  journal and signed approval checks protect database change evidence after
  admission and must not duplicate Gateway authentication.

## Detailed Contract

- `docs/install-management.md`
- `docs/agent-cli-contract.md`
- `docs/journal-recovery.md`
- `docs/user-confirmation.md`
- `docs/scanning.md`
- `docs/mysql.md`
- `docs/postgres.md`
- `docs/mongodb.md`
