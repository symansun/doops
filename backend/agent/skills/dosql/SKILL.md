---
name: dosql-agent
description: DoSql 数据库变更控制助手。用于通过 doagent/CLI 对 MySQL、PostgreSQL、MongoDB 执行受控查询、变更计划、迁移脚本生成、多环境迁移和审计留痕。
metadata:
  family: executable
  runtime: node
  entrypoint: scripts/dosql-agent.mjs
---

# DoSql Agent

DoSql follows the DoOps interaction model: connect to gateway, select a target,
then ask doagent to operate databases through a controlled CLI.

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
  evidence. Approval is a signed artifact bound to the exact operation and
  verification fingerprints; a request boolean is not authorization.
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

## Local Resource Bootstrap

- Before resolving a database request, read `config/resources.json` for the
  local logical database assets and default doops target.
- The resource file contains no plaintext credentials. Resolve real connections
  only through the selected doagent target and DoSql approval policy.

## Detailed Contract

- `docs/install-management.md`
- `docs/agent-cli-contract.md`
- `docs/user-confirmation.md`
- `docs/scanning.md`
- `docs/mysql.md`
- `docs/postgres.md`
- `docs/mongodb.md`
