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
- The production SQLite DB is approximately 1.43 GB; the exported schema used for the native conversion contains 58 tables, 48 indexes and 12 triggers. Capture a fresh inventory again immediately before any future rehearsal.
- No production files, systemd environment, credentials or Agent targets have
  been changed by this preflight.
- A consistent production snapshot was created on the Gateway host at `/var/lib/doops-gateway/gateway.db.migration-snapshot-20261123T023500Z` (mode `0600`, 1,483,608,064 bytes) and passed SQLite integrity validation.
- The offline copier passed its disposable MySQL 8.4 functional suite, including non-empty destination, schema/type mismatch, foreign-key, allocator, storage-engine, corruption and error-redaction fail-closed cases.
- The native MySQL schema is now present and passed the disposable MySQL 8.4 installer plus copier acceptance tests. It covers the exported 58 tables, 48 indexes, 12 triggers and 38 foreign keys. The export itself is committed at `backend/agent/internal/server/testdata/gateway-sqlite-schema.sql` (it previously sat at the repository root); the parity tests read it from there.
- The runtime audit passed its scanner and representative MySQL syntax tests, but records 120 required query remediations: 29 `ON CONFLICT`, 4 `INSERT OR IGNORE`, 3 `JSON_TABLE` rewrites, 3 `julianday`, 4 `unixepoch`, 3 `CONCAT`, 25 index migration cases, 7 `AUTOINCREMENT` cases, 13 explain/introspection cases and other dialect differences. The audit intentionally does not rewrite production queries.
- The audit is an inventory of the SQLite runtime, not a list of live MySQL bugs: no MySQL storage mode is wired, so every counted statement still runs only against SQLite. When `main` adds SQLite-only SQL, the counted set and the expected table in `gateway_mysql_runtime_test.go` are updated in the same change; the audit never selects an alternate path and is never relaxed by deleting a category.
- Coverage gap found on 2026-11-24: after `main` advanced past the export, the live store added two tables that the exported schema and the native MySQL DDL do not contain — `platform_skills` (`gateway_store_schema.go:355`) and `platform_mcp_servers` (`gateway_store_schema.go:375`), both used by the runtime upserts counted above. The MySQL parity tests compare against the frozen export, so they cannot see this drift; the offline copier does fail closed on an unknown source table. A fresh export must be captured and those two tables converted before any rehearsal or cutover.
- Gateway startup remains SQLite-only; no MySQL storage mode is wired. No production cutover is authorized until the export is re-captured, the two missing tables are converted, all runtime queries and startup/migration wiring are implemented and tested.
