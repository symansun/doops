# DoSql Unified Go Engine & Data Governance Specification

> **Status**: Active (SQL-01)
> **Scope**: `backend/agent/internal/dosql/**`, `backend/agent/internal/server/dosql_*.go`, `mcp-stdio --domain dosql`, `doops sql`

## 1. Architecture: One Go Engine, Three Entrypoints

All DoSql operations—CLI (`doops sql`), Web Console (`lib/database-browser.ts`), and MCP (`mcp-stdio --domain dosql`)—share one in-process Go engine under `backend/agent/internal/dosql/`:

- `lexer`: Full SQL lexical splitter & segment planner (MySQL `DELIMITER`, `BEGIN...END` compound blocks, standalone mysqldump `/*!40101 SET ... */` comments, PG dollar-quoting & non-transactional `CONCURRENTLY`/`VACUUM`, Oracle PL/SQL `/` blocks, MSSQL `GO` batches, parenthesized queries, PG data-modifying CTE classification, 4 MiB batch byte cap).
- `policy`: Executable Policy-as-Code governance (`.dosql/policy.yaml`) with `error` / `warn` / `off` severities, literal/comment stripping (so keywords inside quotes/comments cannot bypass rules), and audited `--override-rule` + `--override-reason` escapes. Enforced on both `apply` and `migrate`.
- `recorder`: Append-only per-database automatic statement recorder (`history/<db-identity>.jsonl`, canonicalized endpoint identity matching version journal, secret-sanitized) capturing full statements, hashes, segment index, atomicity, affected rows, duration, release tag, mode (`apply`/`dry_run`/`migrate`/`backfill`), and status; supports `--all-sessions` (up to 2000 records).
- `inspect`: Batched catalog metadata collector for MySQL (`information_schema`) and PostgreSQL (`pg_catalog`, including tables, views, columns, `pg_index` indexes/PKs, and `pg_constraint` foreign keys) producing noise-stripped canonical `SchemaIR`, deterministic `schema_hash`, and atomically written versioned on-disk `SnapshotStore` (`snapshots/<db-identity>/vNNNNNN.json` + `latest.json`).
- `diff`: Database & table structural diff with Kahn FK topological sorting, MySQL type normalization (`int(11)` == `int`), possible-rename detection, and ordered incremental DDL planning.
- `datadiff`: Integer-key chunked table content comparison with database-side `COUNT(*) + BIT_XOR(MD5(...))` pushdown, recursive bisection, row-level merge join, and fix SQL generation.
- `migrate`: Numbered migration directory (`V00N__desc.sql` / `.mongo.json`) with atomically written `dosql.sum` integrity manifest, `PackFromHistory` (auto-recorded committed `apply` SQL → migration file, strictly excluding `read`, `dry_run`, `backfill`, and `migrate` records), in-database `_dosql_schema_history` (strictly read-only existence check in `ListRevisions`/`status` so read-only status checks and policy-rejected `migrate` runs never create `_dosql_schema_history` or require write privileges), per-file hash-chained version journal reservation (preventing snapshot version collisions with `apply`), `--baseline`, and `--resume` from `Applied/Total` statement checkpoints.

The legacy 4-hop `Go -> Node (dosql-agent.mjs) -> Go (database-runner) -> K8s Job` path is bypassed by default in `mcp_dosql_read` / `mcp_dosql_exec`.

## 2. Execution & Transaction Semantics

1. **Automatic Segmentation**: Mixed DDL + DML + session `SET` batches (e.g. `ALTER TABLE ...; UPDATE ...;` or Navicat/mysqldump exports) are split into ordered segments (`session`, `implicit_commit`, `non_transactional`, `transaction`) and executed sequentially on one connection. Each segment reports `atomic` and `committed` truthfully, along with `applied` / `total` statement progress.
2. **Dry-Run Guard**: `--dry-run` still rejects batches containing implicit-commit DDL or PG non-transactional statements before opening a connection, because rollback cannot be guaranteed.
3. **Lock & Statement Timeouts**:
   - Defaults: `--lock-timeout 5s` (max `5m`), `--statement-timeout 60s` (max `30m`).
   - MySQL sets **both** `SET SESSION innodb_lock_wait_timeout` and `SET SESSION lock_wait_timeout` (preventing unbounded metadata lock waits).
4. **MySQL URI/JDBC Parameter Fidelity**: `tls`, `useSSL`, `sslMode`, `charset`, `characterEncoding`, `collation`, `loc`, `serverTimezone`, `parseTime`, `interpolateParams` are preserved into the Go MySQL DSN; unknown parameters fail closed with `dosql_connection_uri_param_unsupported`.

## 3. MCP Contract (`mcp-stdio --domain dosql`)

| Tool | Operations | Availability |
|---|---|---|
| `mcp_dosql_read` | `discover`, `query`, `inspect`, `diff`, `datadiff`, `status`, `versions`, `history`, `lint` | Always available in `dosql` sessions |
| `mcp_dosql_exec` | `apply`, `migrate`, `backfill`, `lint` | Available in `dosql` apply sessions; enforces Policy-as-Code, timeouts, segmentation, version journal, schema snapshot, and automatic statement recording |

## 4. Directory Layout & Persistence

DoSql separates **Git-tracked project governance assets** (client/repository side) from **runtime persistent state** and **per-session ephemeral artifacts** (Agent side):

### 4.1 Project Workspace (`<project>/.dosql/`)

Scaffolded locally via `doops sql --mode init [--project-dir <dir>]` (offline, idempotent; never overwrites an existing `policy.yaml`):

```text
<project>/.dosql/
  policy.yaml          # Policy-as-Code rules (dosql.policy.v1); auto-discovered when --policy is omitted
  README.md            # Team convention and credential boundary reminder
  schema/              # Optional reviewed baseline SchemaIR snapshots
  migrations/          # Versioned V00N__desc.sql / .mongo.json + dosql.sum; auto-discovered when --dir / --out is omitted
```

Multi-database repositories may create subdirectories such as `.dosql/migrations/<db>/` and pass `--dir` / `--out` explicitly. `.dosql/` must **never** store passwords, DSNs, `connection_uri`, or tokens; database credentials live exclusively in DoOps Gateway managed credentials.

### 4.2 Agent Persistent State (`DOOPS_DOSQL_STATE_DIR`)

When `DOOPS_DOSQL_STATE_DIR` (Helm value `dosql.stateDir`) is set to an absolute path (for example `/var/lib/doops-dosql` mounted on a PVC), all persistent DoSql assets live under one mountable root:

```text
$DOOPS_DOSQL_STATE_DIR/
  versions/v2/<db-identity>.jsonl       # Hash-chained per-database version journal
  history/<db-identity>.jsonl           # Append-only automatic statement recorder
  snapshots/<db-identity>/vNNNNNN.json  # Versioned SchemaIR snapshots (+ latest.json)
  drivers/dosql-driver-<scheme>         # Optional external database drivers (overridable via DOOPS_DOSQL_DRIVER_DIR)
```

A relative `DOOPS_DOSQL_STATE_DIR` fails closed with `dosql_state_dir_invalid`. When `DOOPS_DOSQL_STATE_DIR` is unset, legacy paths beside `workspaceRoot()` (`$(dirname workspaceRoot)/.doops-dosql-versions/{v2,history,snapshots}` and `.doops-dosql-drivers`) remain active for backward compatibility.

### 4.3 Agent Ephemeral Session Artifacts

Session-scoped files stay inside the session workspace and binding cache and do not require PVC persistence:

- `<workspace>/.dosql/operations/<operation_id>.evidence.json` — per-operation execution evidence (retrievable via `pull --storage gateway`).
- `<workspace>/.dosql/resolution.json` — session database target pin.
- `$(dirname workspaceRoot)/.doops-dosql-bindings/<session>.json` — short-lived session credential lease.
