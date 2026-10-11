---
name: dosql
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

The canonical skill name and DoOps domain are both `dosql`. Load `Skill name=dosql`.
Use the bound resource name or `credential_id` as `credential_ref`; `grant_id` and
`version_id` are audit metadata, not credential selectors.

The supported CLI entrypoint is `doops sql`. A generic `doops ask` prompt that
mentions DoSql does not guarantee the `dosql` skill or `mcp_dosql_*` tools are
bound. If the target is not upgraded/configured for DoSql, fail explicitly;
never replace it with `mcp_doops_k8s_read`, `kubectl`, `psql`, or a local
connection file.

The core rule is simple: do not bypass the DoSql proxy path. Every database
query, DDL, DML and MongoDB command must create an operation record before it is
executed.

## Operation Routing

Use exactly one route for each database task. Do not silently switch routes:

| User intent | Public route | What it actually does |
| --- | --- | --- |
| Find database instances/assets | `doops sql --mode discover` or `mcp_dosql_read operation=discover` | Reads target Kubernetes Pod/Service/Deployment/StatefulSet metadata only; never reads Secret values or connects to the database. |
| Inspect live tables/columns/indexes/schema | `doops sql --mode query` or `mcp_dosql_read operation=query` | Runs one explicitly read-only, asset-bound schema query through the database client. `discover` is not a schema query. |
| Read business data | `doops sql --mode query` or `mcp_dosql_read operation=query` | Runs one read-only statement and records bounded result/evidence. |
| Plan a change | `doops sql --mode plan` | Produces a human-readable change plan, impact, risk and verification steps; it must not connect for mutation. |
| Change data/schema/admin state | `doops sql --mode apply` or `mcp_dosql_execute operation=execute` | Requires target exec authority, its bound credential, explicit confirmation, signed approval, one recognized mutation and a mandatory read-only verification. |

`register-database`, `discover-resources`, `compare-databases` and structure
snapshot commands consume supplied probe/snapshot/repository metadata. They are
not a live database schema inspection fallback. A standalone `inspect-schema`
command is not currently implemented; do not claim it exists or invoke an
unregistered MCP operation. If the user asks for structure, use a read-only
`query` with the correct engine catalog/information-schema statement and the
selected `database_asset_id`.

For PostgreSQL/MySQL schema reads, the request must be a single read-only SQL
statement. For MongoDB, use a structured `mongoOperation`; raw JavaScript is not
accepted. Mutation requests must not be sent through `query`, `discover`, a
generic `doops ask`, Bash, `psql`, or a local connection file.

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
  `classify`, `query`, `execute`, `discover-databases`, `discover-resources`, `register-database`, `resolve-database`,
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
- `config/assets.json` is the metadata-only discovered asset inventory. It is
  generated by `discover-resources` and is not an authorization source; exact
  target, database credential scope and Gateway action checks remain required.
- `mcp_dosql_read operation=discover` scans non-secret Kubernetes metadata
  (Pods, Services, Deployments and StatefulSets) and returns database
  candidates with a stable `database_asset_id`, engine, namespace, service,
  port and any database name visible in non-Secret configuration. It never
  reads Secret values. Candidates without a database name remain unresolved
  until the user selects the asset and provides its bound database credential.
- During a conversation, a user may reference selected database assets and
  code repositories by metadata-only IDs or `repo_id@branch` references. The
  references are task context, not authorization; Gateway target/action and
  credential scope checks remain authoritative.
- `discover-resources` may scan an explicitly authorized repository workspace
  including application code, connection references, Helm values and Kubernetes
  manifests. It merges PostgreSQL, MySQL and MongoDB metadata into the DoSql
  inventory, adopts an explicit Gateway `database_asset_id` from target
  discovery, deduplicates by database identity, and can project the same asset
  into `config/resources.json`. It ignores credential-like filenames and never
  persists URI userinfo, passwords, tokens or other secret payloads.
- `mcp_dosql_execute` is the only structured mutation executor. It requires
  `confirm=true`, an approved change request, a Gateway-bound database credential,
  verification input and the DoSql journal/evidence chain.
- Database credentials are DoOps managed credentials. Pass only credential
  resource names to the Gateway; never put payloads, connection strings or
  passwords in prompts, artifacts or logs.
- `state=active` on a credential resource does not mean it has an active
  credential version or an active target grant. The required lifecycle is
  `create -> put (staged) -> grant -> verify -> promote`. Query/execute is
  admitted only after all four are complete. The payload for PostgreSQL and
  MongoDB is opaque JSON with `data.connection_uri`; MySQL uses the exact
  datasource keys in `CREDENTIAL_RESOURCES.md`.
- `credential list --view managed` reports resource metadata only and does not
  load grant details. Its grant field must not be interpreted as proof that a
  target grant is absent; use the target view after an active version exists or
  inspect the explicit grant response.
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
