# Gateway MySQL migration plan

Status: engineering preflight only. This document does not authorize a production cutover.

## Current boundary

The Gateway store is opened by `OpenGatewayStore` with SQLite at
`/var/lib/doops-gateway/gateway.db`. The `database/sql` handle and `*sql.Tx`
are used by the server throughout the store package, and the schema contains
SQLite-specific DDL and queries (WAL/PRAGMA, JSON table functions, `RETURNING`,
`ON CONFLICT`, and date functions). A DSN-only switch to MySQL is therefore
not supported.

## Required implementation gates

1. Define a native MySQL 8.4 schema for every current Gateway table, index and
   trigger. Do not run SQLite DDL against MySQL or silently omit a migration.
2. Add a MySQL dialect/store path, including transaction behavior and all
   query rewrites. Every SQLite-specific query must have a tested MySQL form.
3. Add an offline copier that reads one consistent SQLite snapshot and writes
   an empty MySQL database transactionally. It must validate table sets, row
   counts, foreign keys, generated IDs and a schema/data digest; it must fail
   closed on a non-empty or partially initialized destination.
4. Use TLS with server certificate verification. The RDS connection secret stays
   stdin/in-memory only; it must never be in shell arguments, logs, source,
   audit records or a working tree.
5. Rehearse against disposable MySQL 8.4 and a synthetic Gateway DB before
   attempting the RDS endpoint. Then create and integrity-check a production
   SQLite snapshot without stopping the Gateway.
6. Only after review and a tested rollback path may a maintenance window stop
   the Gateway, make a final snapshot, migrate, switch the systemd environment,
   and verify health, authentication, credentials, target reconnects and audit
   reads. Keep the original SQLite DB and old binary for rollback.

## Current evidence

- Gateway host and `gw-edu-coder` can reach the RDS host on TCP/3306.
- The production SQLite DB is approximately 1.43 GB; the exported schema used for the native conversion now contains 61 tables, 50 indexes and 12 triggers. The original capture held 58 tables; the three tables added on 2026-11-24 (`platform_skills`, `platform_mcp_servers`, `inspection_metric_points`) were re-captured from the runtime migration, not from the production dump (see the coverage section below). Capture a fresh inventory again immediately before any future rehearsal.
- No production files, systemd environment, credentials or Agent targets have
  been changed by this preflight.
- A consistent production snapshot was created on the Gateway host at `/var/lib/doops-gateway/gateway.db.migration-snapshot-20261123T023500Z` (mode `0600`, 1,483,608,064 bytes) and passed SQLite integrity validation.
- The offline copier passed its disposable MySQL 8.4 functional suite, including non-empty destination, schema/type mismatch, foreign-key, allocator, storage-engine, corruption and error-redaction fail-closed cases.
- The native MySQL schema is now present and passed the disposable MySQL 8.4 installer plus copier acceptance tests. It covers the exported 61 tables, 50 indexes, 12 triggers and 38 foreign keys. The export itself is committed at `backend/agent/internal/server/testdata/gateway-sqlite-schema.sql` (it previously sat at the repository root); the parity tests read it from there.
- The runtime audit passed its scanner and representative MySQL syntax tests, but records 120 required query remediations: 29 `ON CONFLICT`, 4 `INSERT OR IGNORE`, 3 `JSON_TABLE` rewrites, 3 `julianday`, 4 `unixepoch`, 3 `CONCAT`, 25 index migration cases, 7 `AUTOINCREMENT` cases, 13 explain/introspection cases and other dialect differences. The audit intentionally does not rewrite production queries. The agent resource sync (merged as `15e75cf1`) adds SQLite-only statements of its own, so the pinned counts move to 31 `ON CONFLICT`, 5 `JSON_TABLE`/`json_each` rewrites and 8 explain/introspection sites, plus one numbered-parameter and one reserved-word site (`agent_resource_store.go`, `agent_resource_execution.go`); `gateway_mysql_runtime_test.go` records the exact locations.
- The audit is an inventory of the SQLite runtime, not a list of live MySQL bugs: no MySQL storage mode is wired, so every counted statement still runs only against SQLite. When `main` adds SQLite-only SQL, the counted set and the expected table in `gateway_mysql_runtime_test.go` are updated in the same change; the audit never selects an alternate path and is never relaxed by deleting a category.
- Coverage gap found and closed on 2026-11-24: after `main` advanced past the export, the live store had added tables the export and the native DDL did not contain — `platform_skills` (`gateway_store_schema.go:355`), `platform_mcp_servers` (`gateway_store_schema.go:375`) and, found while building the guard described next, `inspection_metric_points` (`gateway_store_schema.go:400`). The old parity pair (`TestGatewayMySQLTablesMatchExport`) only compared the frozen export with the native DDL, so a table present only in the runtime migration was invisible to both sides; the offline copier fails closed on an unknown source table, so a cutover would have stopped rather than silently dropping it. The export now carries all three (including the `file_count`/`storage_dir` columns that `ensureColumn` adds to `platform_skills`) and the native DDL converts them, so `platform_skills`, `platform_mcp_servers` and `inspection_metric_points` are part of both sides. The export is still a capture, not a generated dump.
- The drift guard is now two-directional. `TestGatewayMySQLTablesMatchExport` requires the frozen export and the native DDL to contain exactly the same table set (a future table on either side alone fails). `TestGatewayMySQLSchemaCoversRuntimeDDL` scans every non-test Go file in `backend/agent/internal/server` for `CREATE TABLE` and requires each runtime table to be present in both the export and the DDL, so a table added to `main` without a re-captured export fails a test instead of surfacing at cutover. The three `_v2` tables in `gateway_store_grants.go` and `credential_store_migration.go` are renamed or dropped inside their migration; they are listed as transient so they cannot be smuggled into the export. A renamed runtime table cannot be detected this way, only additions.
- Column-level extensions are guarded separately from the table set. `agent_status` is the first table whose runtime migration adds columns the production dump never carried: `mode TEXT NOT NULL DEFAULT ''` and a nullable `kubernetes_available INTEGER` (`gateway_store_schema.go`). The runtime writes `kubernetes_available` from `GatewayAgent.KubernetesAvailable *bool`, so an Agent that does not report a Kubernetes runtime stores NULL rather than a false "no cluster"; the MySQL column is therefore `TINYINT(1) NULL`. `TestGatewayMySQLAgentStatusColumnsMatchExport` compares the frozen export's `pragma_table_info` names with the MySQL DDL body and fails on any extra or missing column, and `TestGatewayMySQLAgentStatusInsertMatchesSchema` replays the exact `MarkAgentOnline` INSERT against MySQL 8.4, requiring every omitted column to be nullable or defaulted and `kubernetes_available` to read back as NULL. The second test was added after the MySQL DDL declared `archived_at TEXT NOT NULL` without a default while `MarkAgentOnline` omits that column, which rejected the live insert with error 1364; `archived_at` now defaults to `('')` (MySQL 8.4 accepts a bare `DEFAULT ''` on `TEXT` with error 1101 only in the parenthesised form). The guard is deliberately scoped to this one table; the other tables stay covered at table level by `TestGatewayMySQLTablesMatchExport` and `TestGatewayMySQLSchemaCoversRuntimeDDL`.
- The column edit is surgical instead of a re-capture. Re-dumping the live schema would drop indexes the frozen export carries and the runtime migration no longer creates (`asset_monitor_one_open`, `cluster_inventory_asset_id`, `idx_git_repos_created`, `idx_release_events_ticket`), so the export keeps those legacy entries and gains only the `agent_status` columns. A full re-capture is only correct together with the production database it captures, and must run immediately before a rehearsal.
- The offline copier keeps failing closed on an unknown source table; the added table-driven case `unknown_source_table_with_rows` covers an unknown table that carries data, so the copier cannot copy the rows it happens to map while ignoring the table it does not know.
- Rehearsal on 2026-11-24 (disposable local MySQL 8.4 container on TCP 13306, throwaway credentials):
  `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13306 go test ./internal/server/ -run 'GatewayMySQL' -count=1`
  passed all 15 top-level and 31 subtests. The end-to-end contract proof installs the native schema into an empty database, builds SQLite from the frozen export and runs the real copier: the full schema is rejected only by `mysql_copy_foreign_key_cycle` (`inventory_machines.host_asset_id` self-reference) and succeeds once that one foreign key is deferred in the destination. A separate data-bearing run seeded `users`, `scheduler_jobs` and `git_repos`, copied them through the production schema and read the values back (row counts, `users.name`, `scheduler_jobs.interval_sec` default and `git_repos.url` all matched). This proves the copier, not that the production export is current: the export is a frozen zero-row schema.
- Offline planning path rehearsed on the same snapshot: `gateway-sqlite-snapshot.py` produced a 692224-byte snapshot of the frozen schema (58 tables, 48 indexes, 12 triggers, 0 foreign-key violations, schema sha256 `605849f9f7836d7140b00c8132f584419acb0a42b4fa1c02500ca98298427026`); `gateway-sqlite-to-mysql.py` fails closed without a reviewed mapping table (exit 2, `mysql_ready=false`, `mysql_schema_mapping_required`, 30 blockers: 15 `conditional_object`, 9 `autoincrement`, 4 `json_constraint`, 2 `without_rowid`) and reports `mysql_ready=true` with `--allow-reviewed-mappings`. The planner only plans schema; it never connects to MySQL and never moves data.
- The export at `backend/agent/internal/server/testdata/gateway-sqlite-schema.sql` is a statement list, not a `sqlite3 .dump`: indexes and triggers precede the tables they reference, which the parity test tolerates (it parses statements without executing them) but `sqlite3 db < file` rejects with `no such table`. To rebuild a runnable database for rehearsal, apply `CREATE TABLE` and `CREATE TRIGGER` statements first and `CREATE INDEX` afterwards (126 statements: 61 tables, 49 plain indexes, 4 unique indexes, 12 triggers).
- Gateway startup remains SQLite-only; no MySQL storage mode is wired. No production cutover is authorized until all runtime queries and startup/migration wiring are implemented and tested; the schema coverage gap no longer blocks a rehearsal, but the export must still be re-captured from the live database immediately before one.
