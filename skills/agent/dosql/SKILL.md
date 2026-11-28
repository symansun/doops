---
name: dosql
description: DoSql 数据库结构治理与数据变更引擎。通过统一 Go 内核与 MCP（mcp_dosql_read / mcp_dosql_exec）对 MySQL、PostgreSQL、MongoDB、Oracle、SQL Server 执行读写、结构探测与对比、表内容比对、规范门禁、分批回填与多环境幂等迁移。
metadata:
  family: executable
  runtime: go
  mcp_domain: dosql
---

# DoSql Agent

DoSql is a built-in database governance and change engine inside the DoOps Agent
runtime. The user authenticates once to the DoOps Gateway, selects a Gateway
target, and routes database tasks through `doops sql` or the `dosql` MCP domain
(`mcp_dosql_read` and `mcp_dosql_exec`). DoSql does not implement a second login
or target authorization system: one Gateway target grant authorizes operations on
that target and its bound database credentials.

The canonical skill name and DoOps domain are both `dosql`. Load `Skill name=dosql`.
Use the bound credential resource name or `credential_id` as `credential_ref`;
`grant_id` and `version_id` are audit metadata, not credential selectors.

Never fall back to `Bash`, `kubectl`, `psql`, `mysql`, `mongosh`, or local
connection files to bypass DoSql's policy gate, timeouts, version journal, and
automatic statement recorder.

## Operation Routing

All CLI (`doops sql`), Web Console, and MCP (`mcp_dosql_read` / `mcp_dosql_exec`)
calls execute directly in-process through the unified Go `internal/dosql` engine.

| User intent | CLI route | MCP route | Behavior |
| --- | --- | --- | --- |
| Apply schema / data changes (DDL, DML, stored routines, mysqldump) | `doops sql --mode apply --sql/--file` | `mcp_dosql_exec operation=apply` | Enforces `.dosql/policy.yaml` (with audited `--override-rule` + `--override-reason`), supports batches up to 4 MiB, MySQL `DELIMITER` / `BEGIN..END` / standalone `/*!40101 SET ... */`, Oracle PL/SQL `/`, MSSQL `GO`, automatic segmentation for mixed MySQL DDL+DML and PG non-transactional `CONCURRENTLY`/`VACUUM`, per-request `--lock-timeout` (sets both row lock and MySQL MDL `lock_wait_timeout`) and `--statement-timeout`, records every statement to history, takes a version in the journal, and persists a `SchemaIR` snapshot. |
| Chunked large-table data backfill | `doops sql --mode backfill --table <t> --set '<expr>'` | `mcp_dosql_exec operation=backfill` | Updates large integer-key tables in small primary-key cursor transactions with optional `--where`, `--batch-size`, `--throttle`, `--start-after`, and `--dry-run`. |
| Multi-environment idempotent migration | `doops sql --mode migrate --dir <path>` | `mcp_dosql_exec operation=migrate` | Executes pending `V00N__desc.sql` / `.mongo.json` files verified by `dosql.sum`, tracks `_dosql_schema_history` (`applied`/`total`), supports `--baseline` for legacy databases and `--resume` after partial failures. |
| Audit SQL against governance policy | `doops sql --mode lint --sql/--file` | `mcp_dosql_read` or `mcp_dosql_exec` `operation=lint` | Runs `.dosql/policy.yaml` checks (`forbid_unbounded_update_delete`, `require_primary_key`, `require_charset_utf8mb4`, `forbid_float_types`, `require_concurrent_index`, `expand_contract_guard`) and previews execution segments. |
| Inspect live database schema | `doops sql --mode inspect` | `mcp_dosql_read operation=inspect` | Runs batched catalog queries (`information_schema` / `pg_catalog`) to return canonical `SchemaIR` (tables, columns, indexes, FKs, estimated rows, deterministic `schema_hash`). Pass `tables` to narrow output on large databases. |
| Compare schemas / detect drift | `doops sql --mode diff [--from latest] [--to live]` | `mcp_dosql_read operation=diff` | Compares versioned `SchemaIR` snapshots (`vN`, `latest`) or live DB, orders incremental DDL by FK topology, flags destructive changes and possible column renames. |
| Compare table row content | `doops sql --mode datadiff --source <src> --database <dst> --table <t>` | `mcp_dosql_read operation=datadiff` | Compares integer-key tables via chunked `COUNT(*) + BIT_XOR(MD5(...))` pushdown, recursive bisection, row merge-join, and optional `fix_sql` generation. |
| Check migration status, version journal, or SQL history | `doops sql --mode status\|versions\|history [--all-sessions]` | `mcp_dosql_read operation=status\|versions\|history` (`all_sessions`) | Inspects `_dosql_schema_history` pending files, hash-chained version journal, and secret-sanitized recorded SQL statement logs. |
| Initialize project governance workspace | `doops sql --mode init [--project-dir <dir>]` | — (local repo scaffold via CLI) | Scaffolds `<project>/.dosql/{policy.yaml,README.md,schema/,migrations/}` idempotently without credentials; CLI auto-discovers `.dosql/policy.yaml` and `.dosql/migrations` when flags are omitted. |
| Pack recorded SQL history into migration file | `doops sql --mode pack [--out <dir>] --desc <slug> [--all-sessions]` | — (local repo write via CLI) | Turns committed `apply` mutating statements from history into `V00N__<slug>.sql` (or `.mongo.json`) and atomically updates `dosql.sum` (excludes `read`, `dry_run`, `backfill`, and `migrate` records; defaults to `.dosql/migrations` when initialized). |
| Copy whole tables between bound databases | `doops sql --mode apply --copy-from <src> --database <dst> --tables a,b` | — | Streams batches directly between two same-engine databases on the target without passing rows through the model. |
| Discover database workloads | `doops sql --mode discover` | `mcp_dosql_read operation=discover` | Scans non-Secret Kubernetes Pod/Service/Deployment/StatefulSet metadata. Never reads Secret values. |
| Read business data | `doops sql --mode query --sql '...'` | `mcp_dosql_read operation=query` | Runs read-only statements in a read-only transaction with bounded row output (200 rows) and audit recording. |

## Standard Playbook for AI Agents

1. **Before writing DDL or DML**: call `mcp_dosql_read operation=inspect` (returns MySQL/PG tables, columns, indexes, and foreign keys) and `operation=versions` / `status` to know live table structure, current version, and pending migrations.
2. **Validate with Policy-as-Code**: run `operation=lint` before executing mutations; fix `error` findings (e.g. missing `WHERE` clause—keywords inside string literals or comments do not bypass checks) rather than overriding them unless the operator provides an explicit audit reason. Policy is enforced on both `apply` and `migrate`.
3. **Prefer safe execution primitives**:
   - Mixed MySQL DDL + DML backfill runs cleanly via automatic segmentation; review per-segment `atomic` and `committed` results.
   - For large-table updates, prefer `operation=backfill` (chunked PK cursor) over a single huge `UPDATE`.
   - For PostgreSQL indexes on live tables, use `CREATE INDEX CONCURRENTLY` (automatically executed outside a transaction block).
4. **MongoDB**: submit structured `mongoOperation` JSON documents (array or consecutive documents). Raw JavaScript (`$where`, `$function`, `$accumulator`) is rejected.
5. **Pack & roll out across environments**: after validating changes in dev, guide the operator to pack committed `apply` history via `doops sql --mode pack --desc <slug>` into `.dosql/migrations/` (`dosql.sum` verified), then roll out idempotently via `status` -> `migrate [--baseline <n>] [--resume] --release-tag <tag>`.
6. **Verify post-change state**: run `operation=diff` (`latest` vs `live` or `vN`) to detect drift, `operation=datadiff` for cross-database table consistency, and read-back `operation=query` against acceptance criteria.
7. **Truthful reporting**: report exact `operation_id`, `version`, `applied`/`total`, `segments`, `schema_hash`, `evidence_path`, and any `partially_committed` note from tool output. Never claim success when a tool returned an error or unknown outcome.

## Target & Credential Boundaries

- Database credentials are DoOps managed credentials (`create -> put -> grant -> verify -> promote`). Pass only resource names or `credential_id`; never print or store passwords, URIs, or tokens.
- Server-side `dosql_resolution` pins `credential_ref`, `engine`, and `database_name` when `--database` is specified; do not guess or switch databases on binding failure.
- MySQL URI/JDBC query parameters (`tls`, `useSSL`, `sslMode`, `charset`, `loc`, `parseTime`) are preserved into the driver DSN; unsupported parameters fail closed instead of silently downgrading TLS.
- Persistent Agent state (`versions/`, `history/`, `snapshots/`, `drivers/`) is unified under `DOOPS_DOSQL_STATE_DIR` (Helm `dosql.stateDir`) when configured as an absolute path, or falls back to legacy `.doops-dosql-versions` / `.doops-dosql-drivers` beside the workspace root.
- Detailed operational contract: [DOOPS_USAGE_GUIDE.md](../../../backend/docs/DOOPS_USAGE_GUIDE.md#dosql-直接执行-sql) and [DOSQL_ENGINE_SPEC.md](../../../backend/docs/design/DOSQL_ENGINE_SPEC.md).
