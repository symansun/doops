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

## 遗留列清单（保留结构，代码不再读写）

下列列只保留在 DDL 与冻结导出里以兼容既有库升级，运行时代码不再读写。迁移和本计划后续的 schema 对齐都必须原样保留，不得因为「代码已经不用」而删列或改写默认值。

| 表 | 列 | 状态 |
|---|---|---|
| `monitor_schedules` | `strategies_json` | 已无运行期读取：SELECT 列清单与 UPDATE 都不再提及该列（11-22 台账 M-02），仅存于 DDL、冻结导出与 INSERT 的列名——SQLite 侧缺列会取 DDL 默认值，但冻结的 MySQL DDL 该列为 `NOT NULL` 且无默认值，缺列 INSERT 在 MySQL 8.4 严格模式下报 1364，所以 INSERT 仍按名供常量 `[]`；列默认值仍保留历史 strategy 名单，仅用于老库升级 |
| `platform_skills` | `driver_mode`、`strategy_id` | SF-02 拆除 `builtin_strategy` 双轨后不再读写；老行里的历史值原样保留，新行取 DDL 默认值。由 `backend/agent/internal/server/platform_skills_driver_columns_guard_test.go` 的 `TestPlatformSkillsDriverColumnsHaveNoRuntimeSQL` 钉住：本包非测试源码里只允许 `gateway_store_schema.go` 那条 `CREATE TABLE ... platform_skills` 的 DDL 提到这两个名字（Go 注释不算，退役注释因此合法），并且 MySQL 侧 `gateway_mysql_schema.sql` 与冻结导出 `testdata/gateway-sqlite-schema.sql` 仍须声明这两列。**删列前置条件**：先写迁移并验证老库升级，再同时更新该守卫（放开列名与 DDL 断言）与本节，否则守卫会红——这是有意的摩擦，防止「代码不用了就直接删列」 |
| `monitor_run_items` | `strategy` | 历史记录只读展示；新 run 写 `skill:<slug>` / `command` / `custom_prompt` |
| `gateway_schema_migrations` | `version`, `applied_at` | 不保留结构、是新增台账：两方言各一张同名列（MySQL 侧 `version VARCHAR(64) PRIMARY KEY`，SQLite 侧 `version TEXT PRIMARY KEY`），只由 `migrate()` 写一行基线 `('0001_baseline', 启动时刻 UTC RFC3339)`；重启与并发启动都由主键吸收，MySQL 侧的行由 copier 搬运而不是执行 SQLite DDL。 |

### 遗留列的守卫（platform_skills 先行）

`TestPlatformSkillsDriverColumnsHaveNoRuntimeSQL`（`backend/agent/internal/server/platform_skills_driver_columns_guard_test.go`）是本清单里第一条把「保留结构、代码不再读写」变成可证伪断言的守卫，形状照 `monitor_schedules.strategies_json` 的 M-02 守卫：

- 用 `go/ast` 扫本包全部非 `_test.go` 文件，只看 SQL 字符串字面量。`driver_mode` / `strategy_id` 只允许出现在 `gateway_store_schema.go` 那条 `CREATE TABLE IF NOT EXISTS platform_skills` 里，且必须逐字含 `driver_mode TEXT NOT NULL DEFAULT 'agent_prompt'` 与 `strategy_id TEXT NOT NULL DEFAULT ''`；其余任何位置命中都报 `file:line` 与语句开头。Go 注释不进 AST，所以 `skills_registry.go`、`monitor_engine_runner.go` 里的退役注释天然合法。
- 同时按文本检查 `gateway_mysql_schema.sql` 与 `testdata/gateway-sqlite-schema.sql` 仍声明这两列，避免「只在 DDL 里」这句话悄悄失真。
- 扫描文件数 ≤ 100 判失败，防止目录走空时静默变绿。

反向实验（临时往 `skills_registry.go` 加一个运行期读，验证后已删除）：

```
--- FAIL: TestPlatformSkillsDriverColumnsHaveNoRuntimeSQL (0.06s)
    platform_skills_driver_columns_guard_test.go:118: platform_skills.driver_mode / strategy_id are retired from runtime SQL (SF-02); only the frozen DDL and the retirement comments may keep them:
          skills_registry.go:1040 runtime SQL still names driver_mode: SELECT slug, driver_mode FROM platform_skills WHERE slug = ?
FAIL
```

删掉后：`ok github.com/user/doops/agent/internal/server 0.977s`。

## 历史遗留清单：copier 的冻结白名单

INT-26 之前 copier 要求源与目标表集、列集严格相等（`gatewayCopyPlan`/`planColumns`）。这条规则对「真漂移」是必要的，但它把两种合法形态也判成 `mysql_copy_schema_mismatch`：新建库没有已删除功能的遗留表，老生产库还带着已删除功能的遗留列。两者都不是漂移，因此加一份**逐名冻结**的清单，而不是放宽相等。

- `gatewayCopyLegacyTables`（`gateway_mysql_copy_legacy.go`，7 张）：`git_repos`、`release_events`、`release_tickets`、`scheduler_issues`、`scheduler_job_leases`、`scheduler_jobs`、`upgrade_operations`。来源：`refactor(gateway): remove scheduled issue jobs and git repo registry`（98cd6000 / 0b815aaa）删除调度 issue 与 git 仓库注册表；`release_events`/`release_tickets` 属同一代 console release registry，`upgrade_operations` 是 0.3.19 前由 controller 驱动升级取代的记账表，三者自删除后在 `backend/agent` 内零引用。
- `gatewayCopyLegacyColumns`（5 条）：`audit_events.environment/namespace/project/template` 与 `users.account_type`。运行时零读取；冻结导出与 MySQL DDL 保留它们只为老库升级，新行取 DDL 默认值。
- `asset_coverage_baselines` **不在**清单里。它看似「导出有、新库没有」，实际由 `backend/docs/migrations/asset_coverage.sql` 创建且 `asset_coverage.go` 有 5 处运行期读写（见「Tables created only by the operator migrations」）。把它当遗留表会让「根本没执行该迁移」的库静默通过，正是清单必须能指到已删除功能提交的原因：加条目的门槛是有一个删除该功能的提交，而不是一次失败的搬运。

语义（严格双向，且只对清单内的名字生效）：

| 形态 | 行为 | 报告字段 |
|---|---|---|
| 目标有、源无的表，且表在清单内 | 允许；该目标表必须仍为空（有行仍报 `mysql_copy_stale`），不参与复制 | `LegacyTablesMissing` 记表名，`TableRows[表]` 记 0 |
| 清单内的列（无论只在源、只在目标，或两侧都在） | 允许；不进比较、不进 INSERT/SELECT，目标保留 DDL 默认值 | `LegacyColumnsSkipped` 记 `{Table,Column,InSource,InTarget}` |
| 清单以外的任何表/列差异 | 仍报 `mysql_copy_schema_mismatch`，错误文本不变 | 无 |

清单按名字精确匹配，不做前缀/通配/推断；`release_events_x` 这类前缀同名表仍判 mismatch（`TestGatewayMySQLCopyFailsClosed/legacy_prefix_table`）。旧的四条 fail-closed 用例一条未删、一条未降级。真机（MySQL 8.4，TCP 13334）验过：缺 7 张遗留表与 5 列的库整库搬成功并回读 `LegacyTablesMissing` 七项；带 5 列的库搬成功、`LegacyColumnsSkipped` 五项均为 `InSource && InTarget`，目标 `users.account_type` 落到 `person` 默认而非源里的 `admin`；额外未知表仍 `mysql_copy_schema_mismatch`。反向实验：把 `release_events` 从匹配中摘掉 → `copier rejected a schema without the deleted features: mysql_copy_schema_mismatch`；把精确匹配改成前缀 → `legacy table lookup accepted "release_events_x"` 且 `legacy_prefix_table` 用例由 mismatch 变成 `<nil>`。

## Current evidence

- Gateway host and `gw-edu-coder` can reach the RDS host on TCP/3306.
- The production SQLite DB is approximately 1.43 GB; the exported schema used for the native conversion now contains 62 tables, 50 indexes and 12 triggers. The original capture held 58 tables; the three tables added on 2026-11-24 (`platform_skills`, `platform_mcp_servers`, `inspection_metric_points`) were re-captured from the runtime migration, not from the production dump (see the coverage section below). Capture a fresh inventory again immediately before any future rehearsal.
- No production files, systemd environment, credentials or Agent targets have
  been changed by this preflight.
- A consistent production snapshot was created on the Gateway host at `/var/lib/doops-gateway/gateway.db.migration-snapshot-20261123T023500Z` (mode `0600`, 1,483,608,064 bytes) and passed SQLite integrity validation.
- The offline copier passed its disposable MySQL 8.4 functional suite, including non-empty destination, schema/type mismatch, foreign-key, allocator, storage-engine, corruption and error-redaction fail-closed cases.
- The native MySQL schema is now present and passed the disposable MySQL 8.4 installer plus copier acceptance tests. It covers the exported 62 tables, 50 indexes, 12 triggers and 38 foreign keys. The export itself is committed at `backend/agent/internal/server/testdata/gateway-sqlite-schema.sql` (it previously sat at the repository root); the parity tests read it from there.
- The runtime audit passed its scanner and representative MySQL syntax tests, but records 65 inventory entries: 0 `ON CONFLICT`, 1 `INSERT OR IGNORE` (the INT-23 ledger bootstrap in `migrate()`), 3 `JSON_TABLE` rewrites, 1 `julianday` (the one-shot `audit_events` backfill, kept on purpose), 0 `unixepoch`, 0 `CONCAT`, 29 index creation cases, 8 `AUTOINCREMENT` cases, 5 sqlite catalog probes, 4 reserved-word `use` DDL columns, 1 materialized CTE (the SQLite-only audit purge, exemption below) and other dialect differences. The four numbered-parameter, one `UPDATE OR IGNORE`, one `IS ?` and one `FILTER (WHERE ...)` findings left the audit in INT-19; they are rendering results, not relaxed rules, because each category stays a hard rule for a literal anywhere else.
- The identifier batch (INT-18) moved the eight runtime `use` sites out of the audit: the column is quoted by `gatewayIdentifierFor` (`backtick use` on MySQL, bare `use` on SQLite), the two upsert statements quote it in their own column list (SQLite also accepts that spelling), and the cleanup and prior-record statements are rendered per dialect, so the `reserved_use` count fell from 12 to 4 and `sqlite_introspection` from 8 to 5. The four remaining `reserved_use` sites are SQLite-only DDL: MySQL's schema authority is `gateway_mysql_schema.sql` (guarded by parity and the runtime-DDL coverage test), so editing the SQLite migration text would change no MySQL table. The three runtime existence guards now go through `gatewayExistingTableCountSQL`, which asks `information_schema.tables` with `table_schema = DATABASE()` on MySQL and `sqlite_schema` on SQLite and is always compared with equality, so a missing table fails closed. The remaining five introspection probes are the audit-index probe and the one-shot schema migration probes plus the PRAGMAs that configure SQLite itself; all of them run only against the SQLite store. The audit reports runtime SQLite-shaped SQL; it never selects an alternate path, and the pinned table in `gateway_mysql_runtime_test.go` is the exact inventory.
- The parameter and control-flow batch (INT-19) closed the eight remaining rendered sites on 2026-11-24. Numbered placeholders are gone from the source: MySQL only understands `?` and binds by text order, so `workspaceCatalog` (both the list and existence statements) and `declaredAgentResources` spell plain `?` once per occurrence and the callers repeat the target pair in that order (the list statement passes `cluster, instance` four times plus the manage flag; `declaredAgentResources` passes it seven times, once per union/join). This is the one batch where a wrong rendering stays syntactically valid and returns wrong rows, so `TestGatewayMySQLNumberedParametersBindByTextOrder` executes the production text against MySQL 8.4 over machine `m1 (alpha,beta)`, machine `m2 (alpha,gamma)`, system `s1` and their assignments, and asserts the exact rows of the default view (`machine:m1|true/true`, `system:s1|true/true`), the manage view (both machines and the system) and three existence probes; reverse experiment: writing `instance=? AND cluster=?` in the first catalog union selects `machine:m1|false/true` and fails. The other three sites render through the dialect layer: `gatewayUpdateIgnoreSQL` emits `UPDATE IGNORE` on MySQL and `UPDATE OR IGNORE` on SQLite (chosen over `gatewayUpsert{doNothing:true}` because an upsert would insert when the row is missing, changing an UPDATE into an INSERT; both forms skip the conflicting row, and the caller still maps `RowsAffected()==0` to 409), `gatewayNullSafeEqualsSQL` emits `left <=> right` on MySQL and `left IS right` on SQLite, and `gatewayFilteredCountSQL` emits `COALESCE(SUM(CASE WHEN c THEN 1 ELSE 0 END), 0)` on MySQL and `count(*) FILTER(WHERE c)` on SQLite (the `COALESCE` keeps the empty-set 0 that `count()` returns; without it the probe scans NULL and fails). `TestGatewayMySQLControlFlowStatementsExecuteOnMySQL` asserts on MySQL 8.4 that the conflicting UPDATE affects 0 rows and leaves the stored row untouched while a free identity updates exactly one row, that `<=>` matches both the NULL and the `'x'` sample, and that the filtered count is 2/1 with rows and 0 over no rows; reverse experiments are `left = right` (the NULL probe finds nothing) and dropping `COALESCE` (`converting NULL to int is unsupported`). The materialized CTE stays on SQLite and the golden keeps exactly one entry with a location pin on `audit_purge.go`: `deleteCompletedAuditSQL` is not merely missing a keyword, because the caller appends the SQLite-only `INDEXED BY` hint, the `DELETE ... WHERE id IN (SELECT ... FROM audit_events)` shape is MySQL error 1093 (a table selected in a subquery cannot be the delete target) and would need a two-step rewrite, and the statement also uses `json_extract` (batch 3B). `TestGatewayMySQLMaterializedCTEStaysSQLiteOnly` runs it on MySQL 8.4 and requires the 1064 rejection as evidence that the site needs a rewrite rather than a rendering, and the audit location pin fails if a second materialized CTE appears anywhere else. The audit total therefore went from 78 to 71 here, and to 64 after the JSON batch below; the INT-23 migration ledger adds the single `INSERT OR IGNORE` DDL bootstrap, so the current total is 65. Call-site binding order is owned by one constructor, `targetPairArgs(cluster, instance, pairs, extra...)`, which every catalog statement spreads into `Query`/`QueryRow`; `TestGatewayMySQLTargetPairArgsOrder` pins the exact order value by value and `TestGatewayMySQLCatalogCallSitesBindThroughConstructor` parses the sources and rejects any catalog call that assembles its own argument list, passes an empty or non-literal pair count, hands `Instance` where `Cluster` belongs (or the reverse), or binds a placeholder count different from `pairs*2 + len(extra)`. Mutation evidence: swapping the pair inside the constructor fails with list bindings beta/alpha, and swapping the two values at the call site fails with passes t.Instance as the first binding, want the cluster.
- Upserts no longer appear as literal `ON CONFLICT`. The 31 sites in 22 production files (counted at `c850b63e`: 9 bare `DO NOTHING`, 6 with an explicit conflict target, 16 `DO UPDATE SET ... excluded.col`) now build a `gatewayUpsert{insert,target,doNothing,set,where}` value and render it with `gatewayUpsertSQL` in `mysql_runtime_dialect.go`. The SQLite rendering reproduces the original statement (`ON CONFLICT (cols) DO NOTHING` or `DO UPDATE SET ... [WHERE ...]`), so behaviour is unchanged; the MySQL rendering emits `ON DUPLICATE KEY UPDATE`, maps `excluded.col` to `VALUES(col)`, turns `DO NOTHING` into a self-assignment of the first column, and for the one predicate site (`asset_monitor_store.go:91`, `threshold<>excluded.threshold OR duration_seconds<>excluded.duration_seconds`) wraps every assignment in `IF(predicate, new, old)` and assigns the predicate columns last, because MySQL has no per-row `WHERE` on the update branch and writing the predicate columns first would make later assignments see the new values and freeze rows SQLite would update. `VALUES()` is deprecated in 8.4 but the row-alias alternative (`INSERT ... AS new`) makes every unqualified column inside the caller-written predicate ambiguous (`ERROR 1052`), so the predicate would have to be rewritten per site; keeping `VALUES()` keeps the predicate text untouched, and the dialect layer is the single place to change when MySQL removes the function.
- The golden `on_conflict` count is 0, and the audit stays a hard gate for a literal anywhere else (reverse experiment: restoring one literal in `asset_monitor_store.go` fails the audit with `on_conflict: 1`). `TestGatewayMySQLUpsertsRenderForBothDialects` renders every site twice and asserts the SQLite text keeps its exact conflict target, the MySQL text contains no `ON CONFLICT` or `excluded.`, both keep the same placeholder count and assignment count, the predicate site prefixes every assignment with `= IF(` and assigns its predicate columns last, and the rendered MySQL text produces no `on_conflict` or `reserved_use` issue. `TestGatewayMySQLUpsertConflictTargetsMatchUniqueKeys` rebuilds the unique keys of the frozen export in an in-memory SQLite (`PRAGMA index_info` plus `CREATE UNIQUE INDEX`), requires every target to be a unique key of its table, and requires any table with a second unique key to state why in `gatewayMySQLUpsertResidualKeys`; the four entries are `credential_grants`, `credential_materializations` and `credential_verifications` (their `id` is generated per statement, so a primary-key clash is unreachable) and `grants` (its `id` is assigned by `AUTO_INCREMENT` and never supplied by the INSERT), and an unused entry fails. `TestGatewayMySQLUpsertsExecuteOnMySQL` replays all 31 sites against a disposable MySQL 8.4 database built from `information_schema` metadata: per site it asserts the first insert lands, a conflicting insert updates exactly the named columns (or leaves the row untouched for `DO NOTHING`), columns outside the assignment list keep their first value, and a third insert with the predicate false leaves the row untouched; 30 sites execute and `grants_v2` skips because that table exists only inside its migration.
- `INSERT OR IGNORE` and `||` concatenation no longer appear as literals either (closed 2026-11-24). The four ignore sites now build a `gatewayUpsert` with `doNothing: true` and the conflict key the SQLite statement relied on, so the MySQL rendering is `ON DUPLICATE KEY UPDATE <col> = <col>`: `credential_store_migration.go:88` (migration-only table `credential_bundle_items_v2`, primary key `(bundle_id, credential_id)`), `mcp_registry.go:88` and `skills_registry.go:255` (both `slug`, the `platform_mcp_servers`/`platform_skills` primary key; a seeded row must stay untouched because a later `UPDATE` is what refreshes it) and `gateway_store_edge.go:40` (`id`, pinned to 1 by the table `CHECK`). The three `||` sites are the interrupted-execution error appends in `execution_audit.go` (`FinalizeExecution`, plus the `outcome_unknown` and `error` statements of `RecoverInterruptedExecutions`); they render through `gatewayAuditAppendErrorSQL`, which yields `error || '; ' || ?` on SQLite and `CONCAT(error, '; ', ?)` on MySQL.
  - The concatenation rewrite was chosen for NULL equivalence, not convenience. `||` and `CONCAT()` both return NULL when any operand is NULL, so appending to a NULL error stays NULL on both servers; `CONCAT_WS('; ', error, ?)` would drop the NULL and produce a partial string, so it is ruled out. `TestGatewayMySQLConcatKeepsNullSemantics` runs the same `CASE WHEN error = '' THEN ? ELSE <expression> END` update against SQLite and MySQL 8.4 over rows holding `'prior'`, NULL and `''` and requires the same three results; a MySQL probe also asserts `CONCAT('a', NULL) IS NULL` while `CONCAT_WS('; ', 'a', NULL) = 'a'`, so the comparison cannot pass by accident. `TestGatewayMySQLConcatRendersForBothDialects` pins both spellings, requires equal placeholder counts, and requires the `sqlite_concat` audit rule to still match the SQLite form, so the zero finding is a rendering result rather than a relaxed rule.
  - Two translation details are easy to get wrong and are now regression-tested. (1) One ignore site is an `INSERT ... SELECT`; SQLite refuses a trailing `ON CONFLICT` after a `SELECT` that has no `WHERE` clause (the `ON` can also start a join constraint), so the copy carries a constant `WHERE 1` that changes no row. (2) In `INSERT ... SELECT ... ON DUPLICATE KEY UPDATE` a bare column name is ambiguous because the source table carries it too (MySQL `ERROR 1052`), so the no-op assignment names the target table; plain `VALUES` inserts keep the unqualified form. `credential_bundle_items_v2` exists only inside that migration, so `TestGatewayMySQLInsertIgnoreMigrationExecutes` builds both fixture tables by hand and replays the declaration twice on SQLite and MySQL 8.4, requiring exactly two distinct rows out of three source rows; `TestGatewayMySQLUpsertConflictTargetsMatchUniqueKeys` keeps `gatewayMySQLUpsertMigrationTables` as the reviewed list of migration-only tables and fails when an entry stops being used.
- The date batch closed on 2026-11-24 (INT-17): the three `julianday` and four `unixepoch` sites now render through the dialect layer, leaving 0 `unixepoch` and exactly one `julianday` (the excluded backfill, below). The clock/parse pair lives in `mysql_runtime_dialect.go`. `gatewayEpochNowFor` renders `unixepoch()` on SQLite and `UNIX_TIMESTAMP()` on MySQL; both read the database clock in whole UTC seconds and neither depends on the connection time zone. `NOW()` was rejected because MySQL evaluates it in the session time zone, so a host or session left in `Asia/Shanghai` would shift every authorization and inspection boundary by eight hours while SQLite's `unixepoch()` is always UTC. `gatewayEpochSecondsFor` parses the RFC3339 text column as SQLite `CAST(strftime('%s', col) AS INTEGER)` and MySQL `TIMESTAMPDIFF(SECOND, '1970-01-01 00:00:00', STR_TO_DATE(SUBSTRING(col, 1, 19), '%Y-%m-%dT%H:%i:%s'))`. `TIMESTAMPDIFF` over two naive `DATETIME` operands is pure arithmetic, so no `DATETIME`/`TIMESTAMP` session conversion is involved; that is why the pattern is not `UNIX_TIMESTAMP(col)`, which would interpret the parsed value in the session time zone.
  - Precision: both sides truncate to whole seconds. The text columns are written by `formatTime` as RFC3339 to the second (`...T%H:%i:%sZ`), so a truncating render is exact for real rows; a hand-written fractional value is truncated identically on both sides (`2020-01-01T00:00:00.500Z` parses as second 00 on MySQL and SQLite). Nanoseconds can never survive this rendering, because `DATETIME` stops at microseconds and RFC3339Nano at nanoseconds; anything that needs sub-second ordering has to use the integer millisecond columns, not these text columns.
  - Accepted limitation: the parser only accepts the UTC `...Z` form, which is the only form the Gateway writes. A value carrying a non-UTC offset (`+08:00`) would be read as UTC on MySQL while SQLite's `strftime` converts it, so such a row is out of contract; the mitigation is to keep writing `Z` text rather than to make the renderer guess an offset.
  - Boundaries are unchanged on purpose: token freshness keeps the strict `>` (a token whose expiry second equals the clock second is already expired), the work window keeps `opened<=clock AND deadline>clock`, and the overdue marker keeps `next_due+86400<=clock`. `TestGatewayMySQLTimeComparisonsExecuteOnMySQL` runs every predicate on a disposable MySQL 8.4 database and on in-memory SQLite over the same fixture with a pinned clock (`1577836800` = `2020-01-01T00:00:00Z`), and requires identical row sets at the exact boundary (expiry equal to the clock, `deadline == clock`, `next_due+86400 == clock`), plus a wall-clock run of the freshness predicate where both servers must again return the same rows. Pinning the clock is what makes the boundary assertion deterministic instead of dependent on when the suite runs.
  - `gateway_store_schema.go:494` (the `audit_events` millisecond backfill) is deliberately untouched: it is a one-shot SQLite data migration over historical rows, the batch was scoped to runtime SQL, and changing how the historical milliseconds are derived needs a row-by-row comparison against exported data. `TestGatewayMySQLRuntimeSQLAudit` therefore keeps `julianday` at exactly one entry and names that site instead of the zero the batch brief asked for; reaching zero needs an explicit decision to change the backfill, and `gateway_store_schema.go:494` is the only place it appears.
  - Remaining batches, with the obstacle that decides their order: `JSON_TABLE` 3 next, because each `json_each` row shape must be mapped to explicit column paths site by site; introspection and `EXPLAIN QUERY PLAN` 13 were answered by INT-30: the five live `sqlite_master` probes now render through `gatewayTableExistsSQL`, and the 13 `EXPLAIN QUERY PLAN` statements are pinned to `audit_indexes.go`, whose only caller is `migrate()` on the SQLite branch; the 29 index-creation and 8 `AUTOINCREMENT` findings are DDL-shaped and already answered by `gateway_mysql_schema.sql`, so they stay inventory entries that describe the SQLite install path instead of runtime rewrites.
- The six unique keys the execution run found missing are now declared by the native DDL (closed 2026-11-24), and the execution fixture no longer creates them: a key lost from `gateway_mysql_schema.sql` now fails a test instead of being patched in at runtime. The DDL carries `credential_grants_identity (credential_id, grantee_id, (SHA2(scope_json, 256)))`, `idx_credential_versions_one_active ((IF(state='active', credential_id, NULL)))`, `credential_verifications_identity (version_id, grant_id, `use`, context_key)`, `credential_materializations_identity (version_id, grant_id, `use`, context_key)`, `asset_monitor_one_open ((IF(state='open', series_id, NULL)))`, and on `inventory_resource_catalog` the two partial identities `catalog_bound_identity ((IF(cluster<>'', CONCAT(LENGTH(kind), ':', kind, LENGTH(cluster), ':', cluster, LENGTH(instance), ':', instance, identity_key), NULL)))` and `catalog_pending_identity ((IF(cluster='', CONCAT(LENGTH(kind), ':', kind, LENGTH(created_by), ':', created_by, identity_key), NULL)))`. The DDL now declares 15 `UNIQUE KEY` items plus the inline column `UNIQUE` constraints, 18 index-enforced keys in total.
  - `scope_json` is keyed by `SHA2(scope_json, 256)`, not by a 191-character prefix. A prefix would merge two grants whose scopes share their first 191 characters, and the upsert would then update the wrong grant instead of inserting a new one; the hash keeps the identity injective and a 256-bit collision is not a practical concern. The hash is over the exact stored text, so a producer that normalizes scope JSON must do it before the INSERT instead of expecting the key to canonicalize it. Verified on MySQL 8.4: a second row with the same scope is rejected with `ERROR 1062`, a row differing only in `scope_json` is accepted, and a key added over existing duplicates fails with a duplicate-entry error until the duplicate is deleted.
  - Partial uniqueness is expressed with `IF(condition, column, NULL)` index parts. MySQL allows any number of NULLs in a unique index, so `IF(state='open', series_id, NULL)` rejects a second open alert per series while letting every closed row through; the plain-column form would have blocked all but one alert per series. The `catalog_*` forms also concatenate with a length prefix (`LENGTH(col), ':', col`) instead of a bare `CONCAT`, because without it `kind='ab', instance='c'` and `kind='a', instance='bc'` produce the same string and would be treated as the same row.
  - `inspection_runs(plan_id, due, (trigger_kind='manual'))` stays trigger-enforced. `due` is `TEXT`, so a MySQL unique key needs a prefix length, and a prefix would merge distinct `due` values; the `inspection_cycle_insert`/`inspection_cycle_update` triggers already `SIGNAL` that identity, and the parity guard pins this as its single explicit exclusion rather than accepting a silent gap.
  - Upgrading an existing MySQL instance: MySQL refuses to create a unique key over existing duplicate rows, so check each identity before the schema is applied, for example `SELECT credential_id, grantee_id, SHA2(scope_json,256), COUNT(*) FROM credential_grants GROUP BY 1,2,3 HAVING COUNT(*) > 1`, plus the matching `GROUP BY ... HAVING` per table (`credential_versions` by `IF(state='active', credential_id, NULL)`; `credential_verifications` and `credential_materializations` by `(version_id, grant_id, use, context_key)`; `asset_monitor_alerts` by `IF(state='open', series_id, NULL)`; `inventory_resource_catalog` by the two concatenated identities). Resolve every duplicate explicitly - keep the newest row, revoke or re-scope the older ones, re-run the check - and never drop the key to get past the error: the duplicate is a pair of rows the SQLite runtime would also have written.
  - Guards: `TestGatewayMySQLUniqueKeysMatchExport` (`gateway_mysql_columns_test.go`) rebuilds the frozen export in an in-memory SQLite, reads every `UNIQUE` index (`origin='u'` for inline column constraints, `origin='c'` for `CREATE UNIQUE INDEX`, using `PRAGMA index_info` plus the index SQL's `WHERE` for partial keys), and matches them one-to-one against the DDL's `UNIQUE KEY` items and inline `UNIQUE` column constraints in both directions; the one trigger-enforced key is listed in `gatewayMySQLTestUniqueKeyExclusions` with its reason, and an unused translation or exclusion fails. The five translated keys are compared as exact normalized expressions through `gatewayMySQLTestUniqueKeyTranslations`, so a prefix in place of `SHA2`, or a plain column in place of `IF(...)`, fails with the table and key names. `TestGatewayMySQLUniqueKeysEnforceConflicts` proves the keys on MySQL 8.4: it drops the triggers first so the key is what rejects, rejects a second grant with the same `(credential_id, grantee_id, scope_json)` and a second open alert, and shows that adding `asset_monitor_one_open` over two open alerts fails with a duplicate-entry error until the duplicate row is deleted. Four reverse experiments fail as intended: deleting `credential_grants_identity` fails with `credential_grants declares []`, replacing `SHA2(scope_json, 256)` with `SUBSTRING(scope_json, 1, 64)` fails with `credential_grants declares [credential_grants_identity]`, replacing the `IF` part with a bare `series_id` fails with `asset_monitor_one_open: the native MySQL DDL must declare a unique key over (series_id)`, and an unreachable server fails instead of skipping (below).
  - Disposable-server handling is fail-closed. `openGatewayMySQLRuntimeTestDB` and `gatewayMySQLTestOpenEmpty` now `t.Fatalf` with the host, port and dial error when `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT` is set but the server is unreachable, and skip only when the variable is unset; a stale container that answered a ping but closed the TCP connection used to produce a skip-shaped green. Reverse experiment: `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13399` fails with `the disposable MySQL 8.4 server at 127.0.0.1:13399 is unavailable: dial tcp 127.0.0.1:13399: connect: connection refused (unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip this test)`. The full `-run GatewayMySQL` suite on a disposable 8.4 container (TCP 13311) passes all 26 top-level tests, including 31 upsert executions and the new enforcement case.
- The audit is an inventory of the SQLite runtime, not a list of live MySQL bugs: no MySQL storage mode is wired, so every counted statement still runs only against SQLite. When `main` adds SQLite-only SQL, the counted set and the expected table in `gateway_mysql_runtime_test.go` are updated in the same change; the audit never selects an alternate path and is never relaxed by deleting a category.
- Coverage gap found and closed on 2026-11-24: after `main` advanced past the export, the live store had added tables the export and the native DDL did not contain — `platform_skills` (`gateway_store_schema.go:355`), `platform_mcp_servers` (`gateway_store_schema.go:375`) and, found while building the guard described next, `inspection_metric_points` (`gateway_store_schema.go:400`). The old parity pair (`TestGatewayMySQLTablesMatchExport`) only compared the frozen export with the native DDL, so a table present only in the runtime migration was invisible to both sides; the offline copier fails closed on an unknown source table, so a cutover would have stopped rather than silently dropping it. The export now carries all three (including the `file_count`/`storage_dir` columns that `ensureColumn` adds to `platform_skills`) and the native DDL converts them, so `platform_skills`, `platform_mcp_servers` and `inspection_metric_points` are part of both sides. The export is still a capture, not a generated dump.
- The drift guard is now two-directional. `TestGatewayMySQLTablesMatchExport` requires the frozen export and the native DDL to contain exactly the same table set (a future table on either side alone fails). `TestGatewayMySQLSchemaCoversRuntimeDDL` scans every non-test Go file in `backend/agent/internal/server` for `CREATE TABLE` and requires each runtime table to be present in both the export and the DDL, so a table added to `main` without a re-captured export fails a test instead of surfacing at cutover. The three `_v2` tables in `gateway_store_grants.go` and `credential_store_migration.go` are renamed or dropped inside their migration; they are listed as transient so they cannot be smuggled into the export. A renamed runtime table cannot be detected this way, only additions.
- Column-level extensions are guarded separately from the table set. `agent_status` is the first table whose runtime migration adds columns the production dump never carried: `mode TEXT NOT NULL DEFAULT ''` and a nullable `kubernetes_available INTEGER` (`gateway_store_schema.go`). The runtime writes `kubernetes_available` from `GatewayAgent.KubernetesAvailable *bool`, so an Agent that does not report a Kubernetes runtime stores NULL rather than a false "no cluster"; the MySQL column is therefore `TINYINT(1) NULL`. `TestGatewayMySQLAgentStatusColumnsMatchExport` compares the frozen export's `pragma_table_info` names with the MySQL DDL body and fails on any extra or missing column, and `TestGatewayMySQLAgentStatusInsertMatchesSchema` replays the exact `MarkAgentOnline` INSERT against MySQL 8.4, requiring every omitted column to be nullable or defaulted and `kubernetes_available` to read back as NULL. The second test was added after the MySQL DDL declared `archived_at TEXT NOT NULL` without a default while `MarkAgentOnline` omits that column, which rejected the live insert with error 1364; `archived_at` is now `VARCHAR(64) NOT NULL DEFAULT ''`. It stays `VARCHAR` rather than `TEXT`: MySQL only accepts a default on `TEXT` as an expression (`DEFAULT ('')`, a bare `DEFAULT ''` is error 1101), the expression form is flagged `DEFAULT_GENERATED`, and the offline copier refuses any column whose `EXTRA` looks generated. The test therefore also fails on an expression default in this table, so the next person cannot fix a default by reintroducing the form the copier rejects. The guard is deliberately scoped to this one table; the other tables stay covered at table level by `TestGatewayMySQLTablesMatchExport` and `TestGatewayMySQLSchemaCoversRuntimeDDL`.
- The column edit is surgical instead of a re-capture. Re-dumping the live schema would drop indexes the frozen export carries and the runtime migration no longer creates (`asset_monitor_one_open`, `cluster_inventory_asset_id`, `idx_git_repos_created`, `idx_release_events_ticket`), so the export keeps those legacy entries and gains only the `agent_status` columns. A full re-capture is only correct together with the production database it captures, and must run immediately before a rehearsal.
- The offline copier keeps failing closed on an unknown source table; the added table-driven case `unknown_source_table_with_rows` covers an unknown table that carries data, so the copier cannot copy the rows it happens to map while ignoring the table it does not know.
- Rehearsal on 2026-11-24 (disposable local MySQL 8.4 container on TCP 13306, throwaway credentials):
  `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13306 go test ./internal/server/ -run 'GatewayMySQL' -count=1`
  passed all 15 top-level and 31 subtests. The end-to-end contract proof installs the native schema into an empty database, builds SQLite from the frozen export and runs the real copier. Since INT-50 the copier handles the `inventory_machines.host_asset_id` self-reference itself: the deferred column is written NULL first and backfilled in the same transaction, the foreign key stays in place, and a real parent cycle fails closed with `mysql_copy_parent_cycle` naming the row. A separate data-bearing run seeded `users`, `scheduler_jobs` and `git_repos`, copied them through the production schema and read the values back (row counts, `users.name`, `scheduler_jobs.interval_sec` default and `git_repos.url` all matched). This proves the copier, not that the production export is current: the export is a frozen zero-row schema.
- Offline planning path rehearsed on the same snapshot: `gateway-sqlite-snapshot.py` produced a 692224-byte snapshot of the frozen schema (58 tables, 48 indexes, 12 triggers, 0 foreign-key violations, schema sha256 `605849f9f7836d7140b00c8132f584419acb0a42b4fa1c02500ca98298427026`); `gateway-sqlite-to-mysql.py` fails closed without a reviewed mapping table (exit 2, `mysql_ready=false`, `mysql_schema_mapping_required`, 30 blockers: 15 `conditional_object`, 9 `autoincrement`, 4 `json_constraint`, 2 `without_rowid`) and reports `mysql_ready=true` with `--allow-reviewed-mappings`. The planner only plans schema; it never connects to MySQL and never moves data.
- The export at `backend/agent/internal/server/testdata/gateway-sqlite-schema.sql` is a statement list, not a `sqlite3 .dump`: indexes and triggers precede the tables they reference, which the parity test tolerates (it parses statements without executing them) but `sqlite3 db < file` rejects with `no such table`. To rebuild a runnable database for rehearsal, apply `CREATE TABLE` and `CREATE TRIGGER` statements first and `CREATE INDEX` afterwards (139 statements: 70 tables, 51 plain indexes, 6 unique indexes, 12 triggers).
- Gateway startup is no longer SQLite-only: DOOPS_GATEWAY_STORE=mysql is wired and exercised end to end (see 运行期 MySQL 后端 and the D17 acceptance section at the end of this document). The cutover command is delivered: `doops-gateway store mysql-copy` runs the copier against a read-only snapshot, after a `--dry-run` review, a database-name confirmation and a single transaction with per-table verification. The production cutover itself is still not done: the frozen export is not a capture of a live database.

### INT-23：两库同一张基线台账

D13 的裁决把「schema 版本落点」从阻塞项变成一张表和一行数据：两个方言各建同一张 `gateway_schema_migrations(version TEXT/VARCHAR(64) PRIMARY KEY, applied_at TEXT NOT NULL)`，冻结导出与 MySQL 权威 DDL 同步登记，copier 照常搬运，因此不需要给 copier 开白名单、也不依赖 information_schema 注释。`migrate()` 在同一个事务序列里建表并保证基线行 `0001_baseline` 存在：用 `INSERT OR IGNORE ... VALUES` 而不是 `INSERT ... WHERE NOT EXISTS`，因为幂等与并发安全都只由 version 主键一处承担——重启撞主键被忽略，两个进程同时启动也只有一方写入，调用方无需检查错误串或重试。新增 `TestGatewaySchemaMigrationsLedgerBaseline/Idempotent/Rebuilds` 三条测试分别证明：全新库恰好一行且时间戳可解析为 UTC RFC3339；第二次 `migrate()` 不覆盖既有时间戳也不新增行（测试先写入哨兵时间）；整表被删后重新 `migrate()` 会重建表并补回基线行。反向实验：注释掉基线写入 → 基线测试报空清单；把 `version` 的主键去掉 → 幂等测试看到两行；从冻结导出删掉该表 → `TestGatewayMySQLSchemaCoversRuntimeDDL` 报 `gateway_schema_migrations` 缺失。

### INT-25：守卫链三段（真库 → 冻结导出 → MySQL DDL）

此前只有导出一段：`TestGatewayMySQLColumnsMatchExport` 比对冻结导出与 MySQL DDL，两边一起错就永远看不见。`migrate()` 的 `ensureColumn` 会往真库补列（`monitor_run_items` 8 列、`monitor_runs` 3 列、`monitor_schedules` 3 列，另加 `platform_skills` 2 列与 `credential_bundles.scope`），这些列只活在真库里，于是「真库 → 导出」这段完全没有守卫。

`TestGatewaySchemaExportMatchesLiveStore`（`gateway_schema_export_test.go`）补上第一段：临时目录 `OpenGatewayStore` 建真库，逐表与冻结导出比对列集合。两个方向分开判：

- 只在真库的列 → 直接失败，指名表与列，要求用真库重新抓取 `testdata/gateway-sqlite-schema.sql`（本次据此重抓了上述三表，14 列全部来自真库而非手写）。
- 只在导出的列 → 属历史列（生产库仍带、新库不再创建），允许保留，但必须在 MySQL DDL 里可省略：`NOT NULL` 且无 `DEFAULT` 就失败，否则搬运真库行会撞 1364。当前只有 `audit_events.environment/namespace/project/template` 与 `users.account_type` 属此类，DDL 里都有默认值。
- 真库的表必须是导出的子集：导出保留 8 张现代库不再创建的历史表（`release_events`、`scheduler_jobs`、`upgrade_operations` 等）。因此真库整库搬运不是「打开 store 就能 copy」，而要先补齐生产形态：`TestGatewaySchemaLiveStoreCopiesToMySQL` 以 `OpenGatewayStore` 建出的真库为源（非冻结导出），补上导出里那 8 张历史表的表定义与 5 个历史列（`audit_events.environment/namespace/project/template`、`users.account_type`，均有默认值），再跑真实 copier；成功后回读 `monitor_run_items` 的 8 个 artifact 列与 `monitor_runs`/`monitor_schedules` 的 `prompt/command/skills_json`，证明运行期补出来的列能落到 MySQL。反证：flags 缺失时 copier 报 `mysql_copy_schema_mismatch`，正是「新库 ≠ 生产库」的那 5 列差异。

反向实验（先红后复原）：从导出删 `monitor_run_items.artifact_dir` → `monitor_run_items columns exist in the live store but not in the frozen export: [artifact_dir]`；从 DDL 删同列 → `monitor_run_items columns differ between the frozen SQLite export and the MySQL DDL`；复原后两守卫均通过。

## Column and default guards for every exported table

The table-set guards above compare table names. Two further guards compare columns and the values the runtime actually supplies, so a table that exists on both sides cannot drift column by column or reject a real INSERT on MySQL 8.4.

- `TestGatewayMySQLColumnsMatchExport` compares, for every table in the frozen export, the SQLite `pragma_table_info` column names with the native DDL `CREATE TABLE` body. A column present on only one side fails in both directions: missing on MySQL means the runtime INSERT hits error 1364 or the copier drops production data, present only on MySQL means the copier writes a column the SQLite runtime cannot serve. The table list comes from the export, so the guard follows a re-capture. DDL-only tables stay covered by `TestGatewayMySQLTablesMatchExport` and `TestGatewayMySQLSchemaCoversRuntimeDDL`.
- `TestGatewayMySQLInsertDefaultsMatchSchema` extracts the column list of every `INSERT INTO t (...)` in the package SQL literals with the same AST scanner the dialect audit uses, and fails when a column the MySQL DDL declares `NOT NULL` without a default is omitted. The failure names the table, the column, the source file and line, so the fix is either a default in the DDL or the column in the statement - never a relaxed expectation. A statement without a column list binds its values by physical column order, so a reordered table (a SQLite table rebuild or a hand edit of the native DDL) would write every value into the wrong column with nothing failing. `TestGatewayMySQLInsertsNameColumns` (`gateway_mysql_insert_order_test.go`) therefore forbids the form outright: any runtime `INSERT INTO t VALUES (...)` fails with `INSERT without a column list is forbidden; name the columns: <file>:<line>`, and the `gatewayMySQLUnlistedInsertSites` map is kept but must stay empty, so the answer to a failing statement is a column list and never a new exemption. The six statements converted for this (`inspection_receipts` x2, `inspection_scope` x1, `inventory_cluster_profiles` x3) name their columns in the frozen export's physical order, so each value still lands in the column the positional form bound it to; per site the guard asserts that the export's `pragma_table_info` order and the MySQL DDL declaration order are identical column by column, that the named columns equal that export order (a swapped name fails at the first mismatch and names the position the old statement bound), and that the value count equals the column count, so a hand-added or removed placeholder fails here rather than at execution time. `gatewayMySQLPositionalInsertSites` freezes `table -> site count` for exactly those six statements, so a second write to the same table cannot appear unnoticed; another table's column-list INSERT is still covered by the defaults guard. The column-set guard now hard-fails on an unlisted INSERT instead of skipping it. The table name assembled dynamically in `dosql_copy.go` is out of scope because it always carries an explicit column list; a run that guards no statement fails.
- The finding the guard was written for: 18 columns in 10 tables were `NOT NULL`, carried no default in the MySQL DDL, and were omitted by the real INSERT. SQLite accepted all of them because its columns default to `''` or `0`. The fix follows the column type, because MySQL only accepts a literal default on a non-`TEXT` type:
  - `TEXT` columns get an explicit empty string in the INSERT: `audit_events.error`/`tail`/`ended_at`/`scope_json`, `agent_enrollments.revoked_at`/`confirmed_at`, `inspection_items.repair`, `credential_versions.revoked_at`, `edge_enrollments.revoked_at`, `tokens.revoked_at`, `credential_grants.revoked_at`, `credentials.revoked_at`, `platform_mcp_servers.last_probe_error`. This matches the schema header contract that SQLite `TEXT` columns keep no default and the runtime supplies the value.
  - The five columns MySQL can default inline keep a literal DDL default instead of a statement rewrite: `platform_skills.driver_mode DEFAULT 'agent_prompt'`, `platform_skills.strategy_id DEFAULT ''`, `platform_mcp_servers.last_probe_status DEFAULT ''`, `platform_mcp_servers.last_probe_at_ms DEFAULT 0`, `platform_mcp_servers.last_latency_ms DEFAULT 0`. Their statements are seeded inserts whose column list is deliberately stable.
- `TEXT` columns must not gain a default to make this easier. MySQL 8.4 rejects a bare `DEFAULT ''` on `TEXT` with error 1101 and accepts only an expression default, which information_schema reports as `EXTRA=DEFAULT_GENERATED`; the offline copier fails closed on any generated column. Verified: patching `audit_events.tail` to `TEXT NOT NULL DEFAULT ('x')` turns `TestGatewayMySQLInstallThenCopierAccepts` into `mysql_copy_schema_mismatch`. Converting the columns to `VARCHAR` was rejected too, because it would cap audit `tail`/`scope_json` and change the copier's type mapping.
- Reverse experiments that keep the guard honest (disposable MySQL 8.4 on TCP 13306):
  - Removing `revoked_at` from the `machine_login_vault.go` credentials INSERT fails the guard with `credentials.revoked_at is NOT NULL without a default ... but machine_login_vault.go:134 inserts into credentials without it`.
  - Removing each of the five DDL defaults in turn fails the guard against `skills_registry.go:255` or `mcp_registry.go:81`, so every one of the five is load-bearing rather than decorative.
  - Replaying the pre-fix `audit_events` INSERT shape against MySQL 8.4 returns `Error 1364 (HY000): Field 'error' doesn't have a default value`; the current shape is accepted and reads back `''` for `error`, `tail`, `ended_at` and `scope_json`. `information_schema.COLUMNS` read-back shows the five defaults plus `agent_status.archived_at = ''`.
- The export is still edited surgically, not re-captured. A re-dump deletes indexes the frozen export keeps and the runtime migration no longer creates (`asset_monitor_one_open`, `cluster_inventory_asset_id`, `idx_git_repos_created`, `idx_release_events_ticket`), so this change touched no export row at all.
- The gap that list did not cover is closed in the next section: every table the runtime inserts into now exists in the export and the native DDL, so no table is skipped as unmigrated.
- The ten tables found here were inserted into by the runtime but existed in neither the export nor the native DDL: `agent_observations`, `agent_resource_audit`, `agent_resource_state`, `credential_bundle_items_v2`, `credential_verifications_v2`, `grants_v2`, `inventory_agent_assets`, `inventory_machine_tags`, `inventory_resource_catalog`, `personal_asset_records`. Eight of them are created by `backend/docs/migrations/*.sql` alone (the copier does not read that directory); the three `*_v2` names are transient upgrade tables.

## Tables created only by the operator migrations

The schema coverage guard scanned `CREATE TABLE` statements in the package sources, so tables that only `backend/docs/migrations/*.sql` creates were invisible: the native MySQL schema and the frozen export had 61 tables, while the runtime, the migrations and five migration drivers rely on 69.

- The guard now scans `../../../docs/migrations/*.sql` in addition to the package sources (`gatewayMySQLTestRuntimeDDLTables`), and fails when the migrations directory yields no files instead of passing vacuously. Extending it changed a green run into exactly the expected red: `runtime schema tables are missing from the frozen export or the native DDL: export=[agent_observations agent_resource_audit agent_resource_state inventory_agent_assets inventory_cluster_profiles inventory_machine_tags inventory_resource_catalog personal_asset_records] ddl=[same eight]`. The three `*_v2` names and `asset_monitor_series_new` were already classified transient and stayed out of the comparison.
- Eight tables were added to `gateway_mysql_schema.sql` (69 tables, 51 named indexes, 22 triggers, 43 foreign keys) and to the export with the migrations' own `TEXT`/`INTEGER`/`STRICT` types, so the export keeps describing the SQLite side exactly: `agent_resource_state`, `agent_observations`, `agent_resource_audit`, `inventory_cluster_profiles`, `inventory_resource_catalog`, `inventory_agent_assets`, `inventory_machine_tags`, `personal_asset_records`. Columns, primary keys and foreign keys follow the migrations and the real INSERT statements; short text columns that a statement omits keep a `VARCHAR ... DEFAULT ''` (for example `agent_resource_state.mode`), and `TEXT` columns keep no default exactly as the schema header requires.
- `agent_observations` uses `key` as a column name. MySQL spells it \`key\` because `KEY` is reserved; the primary-key comparison helper now strips identifier quoting so the two sides compare equal.
- `agent_resource_audit.audit_id` is SQLite's `INTEGER PRIMARY KEY` (a rowid alias), which the copier requires to be an `AUTO_INCREMENT` column on MySQL. The value is always supplied from `audit_events.id`, so the auto-increment is a type-compatibility declaration only, and the foreign key on the same column keeps every row anchored to an existing audit event.
- `inventory_resource_catalog` carried two partial unique indexes on the SQLite side (`catalog_bound_identity`, `catalog_pending_identity`). MySQL cannot express a conditional unique index, so four `BEFORE INSERT`/`BEFORE UPDATE` triggers enforce the same two identities and `SIGNAL SQLSTATE '45000'` with the index name; the `UPDATE` variants exclude the row itself by primary key. The same two identities are also declared as functional unique keys on the MySQL side, so the triggers stay the readable enforcement of the SQLite semantics while the keys drive the upsert conflict branch; both must keep agreeing with the frozen export's two partial indexes.
- The `gatewayMySQLUnmigratedTables` list is gone. `TestGatewayMySQLInsertDefaultsMatchSchema` now fails when an INSERT names a table missing from the export, and the only remaining exemption is the transient set, scoped to the enclosing declaration: an INSERT into a transient table is skipped only when it sits inside a function or method whose name starts with `migrate`, and the skip is logged once with that function name. MySQL never runs those one-shot SQLite upgrades (the offline DDL plus copier path has no `migrate()`), so the tables must not exist on the MySQL side.
- Read-back against a disposable MySQL 8.4 (TCP 13306) after installing the native schema: every one of the eight tables accepted its real INSERT shape and returned the written values - `agent_resource_state` (`mode="kubernetes"`, `kubernetes_available=1`, `supported=1`, `observation_hash="sha256:obs"`), `agent_observations` (`details={"id":"m1"}`, `generation=7`), `agent_resource_audit` (`manifest_revision=3`, `["machine:m1"]` via `INSERT ... SELECT` joined to `audit_events`/`agent_status`), `inventory_cluster_profiles` (column-less `VALUES` shape, `revision=1`), `inventory_resource_catalog` (`identity_key="k"`, and a second pending identity was rejected with `Error 1644 (45000): catalog_pending_identity`), `inventory_agent_assets`, `inventory_machine_tags`, `personal_asset_records`. The full `-run GatewayMySQL` suite including the install-and-copy contract passed against the same server.
- Reverse experiments (scratch tree, disposable MySQL 8.4):
  - Removing `inventory_machine_tags` from the DDL fails `TestGatewayMySQLSchemaCoversRuntimeDDL` with `ddl=[inventory_machine_tags]`, which is only reachable because the migrations are scanned now.
  - Removing the `DEFAULT ''` of `agent_resource_state.mode` fails the defaults guard with `agent_resource_state.mode is NOT NULL without a default in the MySQL DDL, but agent_resource_store.go:103 inserts into agent_resource_state without it (MySQL error 1364)`.
  - Removing `inventory_agent_assets.kind` from the DDL fails the column guard with `only in export: [kind]`.
  - Extracting the `grants_v2` INSERT into a helper named `carryGrantV2Row` fails the defaults guard with `grants_v2 is a transient one-shot migration table, but gateway_store_grants.go:166 inserts into it from "carryGrantV2Row"; only a migrate* function may do that`, so the transient exemption cannot be reused as a blanket allowlist.
- The export was extended by hand again rather than re-captured, for the same reason as above: a re-dump drops indexes the frozen export keeps.


### INT-20：JSON 展开与聚合（批次 3B）

- `json_each` 五处与 `json_group_array` 两处不再手写。`gatewayJSONEachValuesSQL` 在 MySQL 渲染 `SELECT jt.value FROM JSON_TABLE(<expr>, '$[*]' COLUMNS(value VARCHAR(512) PATH '$')) AS jt`（SQLite 仍是 `SELECT value FROM json_each(<expr>)`）；`gatewayJSONEachKeysSQL` 先 `JSON_KEYS(<expr>)` 再 JSON_TABLE，因为 `json_each` 直接给 `key` 列而 JSON_TABLE 只迭代数组；`gatewayJSONObjectJoinSQL` 一次返回 from/key/kind 三段，MySQL 的 kind 取 `JSON_UNQUOTE(JSON_EXTRACT(JSON_EXTRACT(<expr>, CONCAT('$.' , JSON_QUOTE(<alias>_key))), '$.kind'))`，SQLite 取 `json_extract(<alias>.value,'$.kind')`；`gatewayJSONOrderedArraySQL` 渲染 `COALESCE(JSON_ARRAYAGG(<expr>), JSON_ARRAY())` 对 `json_group_array(<expr> ORDER BY <order>)`。
- 审计总数 71 → 64：`json_each` 5 与 `json_aggregate` 2 退出清单，absent 表写明两条理由（JSON_TABLE/JSON_KEYS 渲染；`JSON_ARRAYAGG` 没有 ORDER BY、顺序归读者）。
- 顺序归读者是这次唯一不能只靠渲染解决的地方：MySQL 的 `JSON_ARRAYAGG` 不接受 ORDER BY，所以 `scanInventoryMachine` 对 `Tags` 排序，`normalizeLoginAccounts` 按 `username` 排序后重新 marshal。`TestGatewayJSONReaderOwnsOrder` 钉住后者；去掉排序后返回 `zeta,alpha` 而不是 `alpha,zeta`。
- 真机（MySQL 8.4，TCP 13316，`TestGatewayMySQLJSONExpansionsExecuteOnMySQL`）：标量 IN 选 `[2 3]`、NOT IN 选 `[1]`、object keys 得 `alpha,beta,w.3`、object probe 四个键与内存 SQLite 逐键相等（`w1` true、`w2` false 因 kind=Pod、`w.3` true 点号键）、聚合 tag 解码后排序得 `alpha,beta,gamma` 且 SQLite 渲染即 `["alpha","beta","gamma"]`、空集 MySQL 返回 `[]`。
- 坏 JSON 两方言都在解析处拒绝（MySQL `Invalid JSON text`，SQLite `malformed JSON`），所以不需要 SQL 级 `json_valid` 前置守卫：`TestGatewayMySQLJSONRejectsMalformedInput` 把这条写死，应用层 Go 校验保持原样。
- 反向实验：把 JSON_TABLE 根路径 `$[*]` 改成 `$[0]` 后 `scalar IN selected [2], want [2 3]`；去掉 `normalizeLoginAccounts` 的排序后 `normalized login accounts = [{"username":"zeta","credential_id":"c2"},{"username":"alpha","credential_id":"c1"}], want [{"alpha"...},{"zeta"...}]`。

## 运行期 MySQL 后端（INT-27）

```text
DOOPS_GATEWAY_STORE=sqlite   # 默认；行为、文件布局、迁移路径不变
DOOPS_GATEWAY_STORE=mysql    # 需要 DOOPS_GATEWAY_MYSQL_DSN
```

选中的是 MySQL 时，`OpenGatewayStore` 走 `openGatewayMySQLStore`：解析 DSN、Ping、空库时装
`gateway_mysql_schema.sql`（70 张表）、写 `gateway_schema_migrations` 的基线行
`0001_baseline`，随后才允许任何请求语句执行。启动日志只写 `backend=mysql address=host:port
database=name data_dir=<路径>`，不写 DSN、用户名或口令。错误分类沿用离线工具：未知后端
`gateway_store_invalid`、缺 DSN `gateway_mysql_dsn_missing`、DSN 非法
`gateway_mysql_dsn_invalid`、连不上 `mysql_connect_failed`、凭据错
`mysql_authentication_failed`。

两条不变量：

1. **失败即停**。未知后端名、缺 DSN、库里有表但没有 D13 台账行，都是启动错误，不回退
   SQLite——回退会让一次拼写错误把生产数据写到另一个后端。
2. **一个进程一个后端，方言随进程**。绕开全局 var 的串方言：方言从普通 var 改为
   `atomic.Value` + `gatewayDialect()` 读取；启动时设定一次。测试与离线工具用
   `GatewayStore.useDialect` / `gatewayUseDialect` 作用域切换，作用域持写锁直到复原，因此
   同进程两个 store 的交错渲染不会串方言（`TestGatewayDialectIsolationPerStore`）。
   包初始化期烘死方言的缺陷已修复：`inspectState`/`inspectReason` 从包级 var 改为
   `inspectStateSQL()`/`inspectReasonSQL()`，在拼语句时取时钟，MySQL 与 SQLite 各用本方言的
   epoch 表达式。`TestGatewayMySQLNoDialectSQLBakedAtInit` 用 `go/ast` 扫 `internal/server` 全部
   非测试 `.go` 的包级 var 初始化表达式，出现方言渲染函数（`gatewayEpochNowSQL`、
   `gatewayConcatFor`、`gatewayUpsertSQL`、`inventoryGrantsSQL` 等）即失败并点名文件行号；
   并设 100 个 var 的下限防「守卫扫了个空目录」式真空通过。
   `TestGatewayMySQLInspectionListExecutes` 在 `mysql:8.4` 用权威 DDL 建表后真执行
   `view=report&run_id=1` 的巡检列表，`TestGatewayInspectionStateBoundary` 把数据库时钟钉在
   deadline 上（`deadline<=clock` 必须判 overdue），两方言逐行相等。

### INT-33：启动种子（一份列表，两个后端都跑）

MySQL 路径原来只装 DDL 和基线台账行，完全不跑 SQLite `migrate()` 尾部的运行期种子，于是：
`edge_route_revision` 没有行（`NewEdgeRegistry` → `Reload()` 读到空结果，`tunnel_hub.go` 直接
panic）、`platform_skills` 与 `platform_mcp_servers` 各 0 行（`GET /v1/skills` 返回
`{"skills":[]}`，`inspect-vm-host/scripts/inspect.sh` 永不落盘，依赖 `mcp_dosql_read` 的包
拿不到通道）。缺一行只是这个类的可见症状，所以修的是接线，不是补那一行。

现在唯一入口是 `gateway_runtime_seed.go` 的 `gatewayRuntimeSeedSteps()`（显式 slice，顺序固定）：

| 步骤 | 内容 | MySQL 行为 |
|---|---|---|
| `bootstrap-rows` | `gateway_schema_migrations` 基线行 + `edge_route_revision (1,1)`，来自 `gatewayStoreBootstrapRows()` | 同一份 INSERT，按方言渲染成 do-nothing upsert |
| `builtin-skills` | `SeedBuiltinSystemSkills`：8 个内置包入库并按 `<data_dir>/skills/<slug>/` 落盘 | 同 SQLite |
| `builtin-mcp-servers` | `SeedBuiltinSystemMCPServers`：`doops`、`dosql` 两条内置 MCP | 同 SQLite |
| `audit-indexes` | `optimizeAuditIndexes`（SQLite-only，INT-32 fail-closed） | 明确跳过并打日志，理由：MySQL 基线 DDL 自带审计索引，且该函数只解析 PRAGMA 查询计划 |

`migrate()`（SQLite）与 `gatewayMySQLPrepareRuntimeSchema`（MySQL）都只调
`store.seedRuntimeDefaults(ctx)`，任一步失败即启动失败，错误形如
`gateway_runtime_seed_failed: <step>: <cause>`；缺 run 函数报
`gateway_runtime_seed_missing: <step>`。每个种子的写入都是可重复的 upsert，重启与并发启动安全。
MySQL 侧在种子之前仍先过「有表但无 D13 台账行 → `mysql_schema_ledger_missing`」这道门，种子之后
再校验台账恰好 1 行并逐个 probe 每个 bootstrap 行（`gateway_bootstrap_row_missing: <table>`），
所以缺行得到的是点名的错误，而不是又一次裸 panic。

守卫：

- `TestGatewayRuntimeSeedStepsMatchContract` 钉住步骤名单（增删改都要显式改测试）；
- `TestGatewayStartupSeedsRunThroughOnePath` 用 `go/ast` 断言两个启动路径都调用
  `seedRuntimeDefaults`，且 `Seed*` / `optimizeAuditIndexes` 的调用只允许出现在
  `gateway_runtime_seed.go`；
- `TestMigrateDoesNotSeedRowsOutsideTheSharedList` 扫所有 `migrate*` 函数里的 `INSERT INTO`，
  种行必须来自共享列表，迁移拷贝须列进白名单并写明理由；
- `TestGatewayBootstrapRowsMatchAcrossBackends` 用真 MySQL 8.4 对比「MySQL 安装后」与
  「SQLite migrate 后」的 bootstrap 行集合（`gatewayCopyTestDestination`，缺
  `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT` 时 skip）；
- `TestGatewayMySQLRuntimeStartup`（真库）断言 `edge_route_revision` 恰 1 行、`platform_skills`
  的 system 行数等于内置包数、内置 MCP slug 集合、以及
  `<DOOPS_GATEWAY_DATA_DIR>/skills/inspect-vm-host/scripts/inspect.sh` 存在且带可执行位；
- `TestGatewayMySQLBinaryBoots` 真建 `backend/gateway` 二进制、真起进程、10 秒存活、HTTP 有响应、
  日志无 `panic:`。

### INT-36：凭据全生命周期在 MySQL 上的等价性与守卫

D17 第 4 条要求「凭据 put / verify / promote 在 MySQL 上跑通（含 `use` 保留字路径与
`credential_*_v2` 表）」才算 MySQL 后端可对外宣称。真机跑这条链路时，DDL 与运行期 SQL 的
三处差异被逐个逼出来；现在的等价性由一份 e2e 文件和一次真进程演练钉住。

#### DDL 上三处与 SQLite 不等价的地方（本批修）

1. `credentials.owner_id → users(id)` 外键。SQLite 从不启用 `PRAGMA foreign_keys`，
   platform scope 的凭据把 `owner_id` 存成空串是合法的；MySQL 强制外键，
   `POST /v1/credentials {"scope":"platform"}` 报
   `Error 1452 (23000): Cannot add or update a child row: a foreign key constraint fails`。
   权威 DDL 去掉这条边，保留 `created_by → users`（它指向真实创建人），并在
   `gateway_mysql_schema.sql` 的 `credentials` 建表处写明理由。守卫：
   `gatewayMySQLTestForeignKeyExclusions`（`gateway_mysql_schema_test.go`）按
   `(表, 被引用表)` 从导出侧期望值里删一次并附理由，导出里不再有这条边、或 DDL 里重新出现，
   两边都会红；条目失效时报
   `gatewayMySQLTestForeignKeyExclusions lists credentials (users) but the export no longer declares that edge; drop the entry`。
2. `credential_materializations.context_key` 与 `credential_verifications.context_key`
   原来是 `VARCHAR(128)`，而运行期的 key 是由 NUL 连接出来的串
   （`credentialMaterializationContextKey`，`credential_runtime.go:372`），最短一条也超 128：
   `verify` 报 `Error 1406 (22001): Data too long for column 'context_key' at row 1`，
   HTTP 是 502 `credential_operation_failed`。两列改为 `TEXT`；唯一键不能直接建在
   TEXT 上，改成身份列 + 哈希函数索引（与 `credential_grants_identity` 同款）：

   ```
   UNIQUE KEY credential_materializations_identity (version_id, grant_id, `use`, (SHA2(context_key, 256)))
   UNIQUE KEY credential_verifications_identity   (version_id, grant_id, `use`, (SHA2(context_key, 256)))
   ```

   `use` 仍是保留字，DDL 里逐处反引号（运行期由 `gatewayMySQLIdentifier()` 包裹，
   bc23b643）。守卫：`gatewayMySQLTestUniqueKeyTranslations`（`gateway_mysql_columns_test.go`）
   增两条翻译（键表达式 `(version_id,grant_id,use,context_key)`），目标表达式是 `version_id, grant_id, (SHA2(context_key, 256))`
   加上逐处反引号包裹的名为 use 的列。
3. 全新库的 `edge_route_revision` 空行是 INT-33 修的：`gatewayRuntimeSeedSteps()` 的
   `bootstrap-rows` 在两个后端都种 `(1,1)`。权威 DDL 保持纯 DDL、**不**种行——copier 要求
   目标库为空（否则 `mysql_copy_destination_stale`），种行只属于启动路径。e2e 夹具因此也不自种，
   它验证的正是「装完 DDL + 跑完共享种子」之后的真实状态。

#### 凭据语义（不是 MySQL 专属，但决定了用例怎么造）

- grant 的身份是 `(credential_id, 目标, use)`，namespace/workload 是执行元数据：同 `dev/local`
  换 namespace 再 `POST /grants` 只会 `UPDATE` 原 grant 的 `scope_json` 并返回同一个 id
  （`credential_store_grants.go:90-160`）。所以「每个 active grant 都 verify」必须用**两个不同 target**
  造，否则永远只有一条 grant。
- `verify` 对请求 scope 命中的每个 grant 各 materialize 一次（`credential_verify_http.go`）；
  scope 里省略 namespace 并不能把 grant 分开，用例必须回传该 grant 的**完整** scope。
- `promote` 要求当前 staged version 对每个 active grant 都有 verification，否则
  `409 credential_verification_required`；历史/已撤销的 grant 既不满足也不阻塞。

#### 守卫：`gateway_mysql_credential_e2e_test.go`

四个用例，全部走 HTTP handler（`httptest.NewServer(hub.RegisterRoutes)`），不直接调 store：

| 用例 | 覆盖 |
|---|---|
| `TestGatewayMySQLCredentialLifecycleE2E` | create → put（payload 走 stdin 语义的请求体）→ 2 个 target 的 grant → 每个 grant verify → promote → 列表/详情/versions → 撤销一个 grant → credential revoke，逐步断言 HTTP 状态、行数与 `state`/`version`/`promoted_at`，撤销后 materialization 全部 `cleaned` |
| `TestGatewayMySQLCredentialPromoteRequiresEveryActiveGrant` | 两个 active grant 只 verify 一个 → promote 409，version 仍 staged，`promoted_at` 为空 |
| `TestGatewayMySQLCredentialPromoteRequiresActiveGrant` | 无 active grant → promote 409 |
| `TestGatewayMySQLCredentialPayloadFailsClosedWithoutMasterKey` | `DOOPS_GATEWAY_SECRET_KEY` 置空 → put 503 `credential_key_unavailable`，versions 0 行，库内无明文 |

环境契约与 4ed9ed07 的 fail-closed 写法一致：

- 未设 `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT` → `t.Skip("unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip this test")`；
- 设了但连不上 → `t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=%s is set but MySQL is unreachable (fail closed, not skip): %v")`，
  **不**跳过；
- 每个用例建独立库 `doops_cred_e2e_<UnixNano>`，结束时 `DROP DATABASE` 并把进程级方言复位成
  `gatewayDialectSQLite`（`gatewaySetDialect` 是全局的）；
- materialize 需要真 Agent，用 `dialEnrolledTestAgent` + 假 kubectl/git（`installCredentialTestCommands`，
  PATH 必须显式带上它返回的 bin 目录）在进程内起真 Agent；`DOOPS_GATEWAY_DATA_DIR` 指到
  `t.TempDir()`（INT-33 的默认 `/var/lib/doops-gateway` 在开发机不可写）。

#### 真进程证据（`DOOPS_GATEWAY_STORE=mysql`）

`backend/gateway` 二进制 + `mysql:8.4.13` + 两个 `backend/agent` 进程（`dev/local`、
`dev-2/local`，PATH 上放假 kubectl），curl 逐请求、`docker exec ... mysql` 逐行取数。逐步证据
（canary 凭据值不在任何输出里）：

| 步骤 | HTTP | DB |
|---|---|---|
| `POST /v1/credentials` (platform, opaque) | 200 | `credentials` 1 行 `active` |
| `PUT /v1/credentials/<id>/payload`（payload 走 stdin） | 200 | `credential_versions` 1 行 `staged`，`promoted_at` 空，`payload_digest` 非空 |
| `POST .../grants` ×2（dev/local、dev-2/local） | 200/200 | `credential_grants` 2 行 `active` |
| `POST .../verify` ×2（每条 grant 的完整 scope） | 200/200 `status=verified` | `credential_verifications` 每 grant 1 行 `verified`；`credential_materializations` 各 1 行 |
| `POST .../promote` | 200 `{"promoted":true}` | credential `active`；version `active`，`promoted_at=2026-11-24T06:19:49.608423Z` |
| `GET /v1/credentials`、`/<id>`、`/<id>/versions` | 200/200/200 | 只回元数据与 `payload_digest` |
| `DELETE .../grants`（dev-2） | 200 `revoked` | 该 grant 的 materialization `cleaned`，另一条仍 `verified` |
| `POST .../revoke` | 200 `{"revoked":true}` | credentials/versions/grants 全 `revoked`，materializations 全 `cleaned` |

#### 反向实验（原文）

1. 跳过某个 active grant 的 verify：在 2 条 active grant 的库上只 verify 一条（HTTP 层无法「跳过」，
   等价做法），verify 200 只写 1 条 verification，随后 promote →
   `promote http=409 body=credential_verification_required`，version 仍 `staged`、`promoted_at` 空。
2. 无 active grant 时 promote：`promote http=409 body=credential_verification_required`，version 仍 staged。
3. 拿掉 `DOOPS_GATEWAY_SECRET_KEY`：
   - 已有 payload 的库启动即拒：`refusing to start: master key check failed for database /private/tmp/int36/gw.db: credential payloads are stored but DOOPS_GATEWAY_SECRET_KEY is not set; restore the original key and restart`（exit 1）；
   - 空库可启动但只打警告 `[gateway] warning: DOOPS_GATEWAY_SECRET_KEY is not set; credential features stay unavailable until it is configured`，create 200（仅元数据），put `503 credential_key_unavailable`，`credential_versions` 0 行，`payload_ciphertext LIKE '%<canary>%'` 0 行——没有明文落库。
4. MySQL 侧 `use` 不加反引号（临时让 `gatewayMySQLIdentifier` 原样返回，验证后已还原）：
   e2e verify 502 `credential_operation_failed`；直接调用 `CredentialVerifications` 得到
   `Error 1064 (42000): You have an error in your SQL syntax; ... near 'use, request_json, evidence_json, status, verified_at' at line 1`；
   裸 SQL `SELECT use FROM credential_verifications LIMIT 1` 同样 1064。恢复后 e2e 全绿，证明 bc23b643 的
   包裹在真库上确实生效。

#### 本轮未覆盖

- 只覆盖 `opaque` + `opaqueSecret` 这一种 type/use；registry、database 等 use 的物化分支没有在 MySQL 上跑。
- 目标侧是假 kubectl（进程内 Agent 与真 Agent 进程都用 `DOOPS_TEST_*` 状态文件），没有真 Kubernetes 集群；
  因此「Secret 真的落进集群」仍属 SQLite 与 MySQL 共用的未验证段落。
- 与凭据无关的 MySQL 缺陷只记录不改：target resource sync 在 MySQL 上报
  `Error 1064 ... near ', ?) WHERE cluster=? ... JSON_TABLE'`（见任务台账 backlog）。

### D18：运行期数据目录

MySQL 模式没有数据库文件可推目录，旧构造的 store 没有 `dbDir`，于是 `skillStorageRoot()` 与
`monitorArtifactRoot()` 返回空串：Skill 包永不落盘，File-First 产物静默退回 DB 文本列——这正是
AGENTS.md 禁止的静默降级。现在：

```text
DOOPS_GATEWAY_DATA_DIR=/var/lib/doops-gateway   # 默认值，仅 DOOPS_GATEWAY_STORE=mysql 生效
```

- 必须是绝对路径，相对路径报 `gateway_data_dir_invalid`，不做 cwd 解析；
- 启动时 `MkdirAll 0700` 建 `<dir>/skills` 与 `<dir>/artifacts`，并在两处各做一次
  CreateTemp+Remove 可写探测，失败报 `gateway_data_dir_unwritable: <path>: <err>` 且启动失败；
- store 以该目录为 `dbDir`，两个 root 都指向它的子目录；启动日志在 `backend=mysql ...` 行加
  `data_dir=<dir>`，并在 `database:` 行按后端打印——MySQL 打
  `mysql host:port/database`，SQLite 仍打数据库文件路径（旧代码在 MySQL 模式打印 sqlite 路径，
  指着一个进程从未打开的文件，误导运维）。

SQLite 行为逐字节不变：数据目录仍由数据库文件所在目录决定，`DOOPS_GATEWAY_DATA_DIR` 被忽略。
守卫：`TestGatewayMySQLDataDirDefault`、`TestGatewayMySQLDataDirRejectsRelativePath`、
`TestGatewayMySQLDataDirRejectsUnwritable`、`TestGatewayStoreLocationFollowsBackend`。

### INT-30：清掉运行期 `sqlite_master` 探测，给 `EXPLAIN` 诊断定界

内省规则的模式原来只匹配 `PRAGMA`/`sqlite_schema`，漏了 `sqlite_master`，所以 golden 报
`sqlite_introspection` 5 条而实际有 10 条。把模式扩为 `PRAGMA|sqlite_schema|sqlite_master` 后，
审计当场红出五处**运行期**站点（不是两处）：`monitor_engine_store.go` 的 `tableExists` 与
`asset_monitor_series` 桥接、`machine_inventory.go` 的 `asset_monitor_samples` /
`monitor_resource_snapshots` 两处、`monitor_metric_store.go` 的 `inspection_metric_points`。
它们在 MySQL 上都会以 1146 失败，因此全部改走
`gatewayTableExistsSQL(gatewayDialect())`：MySQL 用
`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?)`，
SQLite 仍读 `sqlite_master`。golden 的 `sqlite_introspection` 仍是 5，但那 5 条现在是
`audit_indexes.go`、`credential_store_migration.go`、`gateway_store.go`、
`gateway_store_schema.go` 里的 SQLite-only 探测；扩展模式的效力由 golden 里新增的
`sqlite_master probe` 例子钉住。

`EXPLAIN QUERY PLAN` 13 处全部在 `audit_indexes.go` 的 `optimizeAuditIndexes`，它唯一的
调用者是 `migrate()`（`gateway_store_schema.go:559`），而 `migrate()` 只在
`OpenGatewayStore` 的 SQLite 分支被调用（`gateway_store.go:107`）；MySQL 启动走
`installGatewayMySQLSchema`，不跑索引诊断。这不是「先放着以后改」的豁免，而是当前接线下的
不可达路径，所以 golden 保留 13 并加位置钉：任何落在 `audit_indexes.go` 之外的
`EXPLAIN QUERY PLAN` 都失败。
`ensureColumn`、`tableHasColumn`、`optimizeAuditIndexes` 这三个入口现在
不只由位置钉和注释约束：方言为 MySQL 时它们直接返回
`gateway_sqlite_only_path: <函数名> runs only in the SQLite migration path`（包级哨兵
`errGatewaySQLiteOnlyPath`），所以将来接线误调用拿到的是点名契约的错误，而不是 PRAGMA/EXPLAIN
在 MySQL 上抛出的 1064；测试 `TestSQLiteOnlyPathRefusesMySQLDialect` 与
`TestSQLiteOnlyPathStillRunsOnSQLite` 分别钉住两侧，反向实验证明删掉守卫会退回 1064、写成 panic
会让测试崩掉（因此选 fail-closed 而非崩进程）。

真机证据：`TestGatewayMySQLTableExistsProbeExecutes` 在空库上断言四个探针为 false 且直连
`information_schema` 查询不报 1146；装完权威 DDL 后四个都为 true；随后执行桥接写入，
`asset_monitor_series` 与 `asset_monitor_samples` 各 1 行。空库上直接跑桥接写入会报 1146，
说明门不是装饰。SQLite 侧 `TestGatewayMySQLTableExistsProbeOnSQLite` 用同一探针断言
`audit_events` 存在、`asset_monitor_series`（operator migration 才建）不存在。

反向实验（先红后复原）：把 `monitor_engine_store.go` 的探针退回 `sqlite_master` 字面量，审计报
`found 6 sqlite_introspection findings, want 5 at ... monitor_engine_store.go:19`；把方言层的 MySQL
分支改成 `sqlite_master`，空库探针原文为 `Error 1146 (42S02): Table
'gateway_upsert_...sqlite_master' doesn't exist`；在 `monitor_metric_store.go` 塞一条
`EXPLAIN QUERY PLAN` 字面量，位置钉报 `attributes a remaining explain_plan to
monitor_metric_store.go:294, want the SQLite-only audit index maintenance in audit_indexes.go`。
重试只包了单语句写路径：`StartAudit` 的 INSERT 与 `FinishAudit` 的单条 UPDATE（1213 死锁 /
1205 锁等待，最多 3 次、50/100ms 退避）。读查询、多语句事务与显式事务里的 upsert 不重试——
一个已经提交了前半段的事务重试会重复写入。

### INT-35：扫描器覆盖面：规则表 22 类，以及明确不在此列的 DDL 与一次性迁移

`gatewayMySQLRuntimeIssues` 现在注册 23 类规则（INT-35 之前 19 类、INT-40 再加 1 类、INT-44 再加 1 类；任务书里的「16 → 18」是旧计数，
以规则表为准）。容易混淆的是两张表：**类别数**看 `gatewayMySQLRuntimeIssueRules`，**站点数**看
golden 的 `want`/`absent`——一个类别可以为 0 条站点，但必须留在规则表里，否则它再次出现时无人报警
（golden 为此加了「规则表必须仍注册 sqlite_rowid / limit_in_subquery」的断言，否则删掉规则是静默的）。

| 类别 | 命中构造 | MySQL 侧后果 |
|---|---|---|
| `numbered_parameter` | `?1`、`?2` 位置参数 | 参数错位（1161）或静默错值 |
| `on_conflict` | `ON CONFLICT ... DO UPDATE` | 1064 |
| `insert_or_ignore` | `INSERT OR IGNORE` | 1064 |
| `update_or_ignore` | `UPDATE OR IGNORE` | 1064 |
| `json_each` | `json_each(` 表函数 | 1305 |
| `json_aggregate` | `json_group_array(`、`json_group_object(` | 1305 |
| `aggregate_filter` | `FILTER (WHERE ...)` | 1064 |
| `julian_day` | `julianday(` | 1305 |
| `unix_epoch` | `strftime('%s'` | 1305 |
| `materialized_cte` | `MATERIALIZED` | 1064 |
| `sqlite_concat` | `||` 字符串拼接 | `||` 被当成逻辑或，静默错值 |
| `null_safe_parameter` | `? IS ?` | 1064 |
| `sqlite_introspection` | `PRAGMA`、`sqlite_schema`、`sqlite_master` | 1146 / 1064 |
| `sqlite_explain_plan` | `EXPLAIN QUERY PLAN` | 1064 |
| `create_index_if_absent` | `CREATE INDEX IF NOT EXISTS` | 1064 |
| `drop_index_if_absent` | `DROP INDEX IF EXISTS` | 1064 |
| `auto_increment` | `AUTOINCREMENT` | 1064 |
| `partial_index` | `CREATE INDEX ... WHERE ...` | 1064 |
| `reserved_use` | 未加引号的 `use` 列 | 1064 |
| `sqlite_rowid` | 反引号 SQL 里的 `rowid` | 1054 |
| `limit_in_subquery` | `IN/ANY/ALL/SOME (SELECT ... LIMIT ...)` | 1235 |
| `scalar_minmax` | 两个及以上参数的 `MIN(a, b)`/`MAX(a, b)`（INT-40） | 1064 |
| `reserved_bare_identifier` | 语句里裸写的保留字标识符 `key`（INT-44；词表当前只有 `key`） | 1064 near 'key' |

`sqlite_rowid` 之前为什么不红：`rowid` 在本包里只出现在注释（`gateway_mysql_copy.go` 的 rowid 别名
说明）和一条真 SQL——`asset_coverage.go:342` 的 `ORDER BY rowid DESC`。注释里的 `rowid`、
`WITHOUT ROWID` 后缀、字符串字面量里的 `rowid` 都不该报，所以这一条不是单正则能了结的：规则用
`match` 钩子（`gatewayMySQLSQLiteRowid`）先把注释与字符串字面量抹成等长空格，再排除紧跟在
`WITHOUT ` 后面的命中；反引号标识符再单独扫一遍——`rowid` 换成 MySQL 的标识符写法，指向的还是那个不存在的列。`asset_coverage_baselines` 没有插入序列（`id`/`frozen_at`/`frozen_by`/
`snapshot` 四列），所以 `rowid DESC` 的语义只能是「最后冻结的在前」；改成
`ORDER BY frozen_at DESC, id DESC`，去掉排序不可选——那会让「默认选最新基线」随存储引擎漂移。
`formatTime` 是 RFC3339Nano（末位 0 截断），同秒冻结靠小数位比较、同一瞬时靠 `id` 兜底，MySQL 侧
要求库/表是 `utf8mb4_0900_bin`（权威 DDL 头部的约定）。两库同一夹具的等价性由
`TestAssetCoverageBaselineOrderAcrossEngines` 钉住：同一句 `assetCoveragePlatformBaselinesSQL`
在 SQLite 与 `mysql:8.4` 上返回同一个 id 序列，`?summary=1` 的默认基线是同一条。

`limit_in_subquery` 之前为什么不红：保留策略的批删
`DELETE ... WHERE id IN (SELECT id ... LIMIT ?)` 在 MySQL 8.4 报 1235，是 INT-34 真机跑出来的。
单正则匹配不了「同一个子查询里有 LIMIT」：`IN (SELECT ... FROM (SELECT ... LIMIT 1) AS b)` 里的
LIMIT 合法。规则用括号配平（`gatewayMySQLParenthesized` + `gatewayMySQLTopLevelWord`）只对子查询
体顶层的 LIMIT 报警，`IN`/`ANY`/`ALL`/`SOME` 四个关键字都算。

同形的另一处已核、未进计数：`audit_purge.go:61`/`:76` 的 `deleteAuditBatchSQL` 也是
`DELETE ... WHERE id IN (SELECT ... LIMIT ?)`，但它由两个字面量拼成
（`... AND status NOT IN (` + `activeAuditStatusSQL` + `) ... LIMIT ?`），扫描器只收单条字符串
字面量，拿到的是不含 LIMIT 的前缀，所以既不会被计数、也不会被误判成已覆盖——
`TestGatewayMySQLRuntimeSQLAudit` 里有一条断言把这个前缀拆法钉住：谁把它并成单条字面量，
计数就变 2 并在那里失败。MySQL 侧它也轮不到 1235：同一批语句在 `mysql:8.4` 上实测先报
`Error 1064`（`AS MATERIALIZED` 与 `INDEXED BY idx_audit_started_ms`），与
`materialized_cte` 那一条的位置钉依据相同。可达性上它只在操作员显式打开
`-audit-retention` 时才被执行（默认 0，`StartAuditRetention` 直接返回），而打开后 MySQL 后端
确实会把这两条 SQLite-only 语句发到 MySQL 并失败——这是需要另立任务的缺口，不在 INT-35 的
改动面内，本次只判定不修改。

明确不在此列：
- **权威 DDL**：`gateway_mysql_schema.sql` 是 MySQL 的 schema 来源，本身就是 MySQL 语法，由
  `TestGatewayMySQLColumnsMatchExport` 等导出/列守卫覆盖，不归运行时扫描器。
- **SQLite-only 迁移文本**：`gateway_store_schema.go`、`credential_store_migration.go` 里的
  `ALTER TABLE`/`PRAGMA` 只在 SQLite 建库路径执行（MySQL 走 `installGatewayMySQLSchema`）。
- **一次性 operator 迁移**：`backend/docs/migrations/*.sql` 由运维在目标库执行，不进二进制。
- **copier 读源**：`gateway_mysql_copy.go` 读的是 SQLite 源库，SQLite 语法在那里是对的，所以
  `gatewayMySQLRuntimeSQLLiterals` 把它（以及 `mysql_runtime_dialect.go`、`gateway_mysql_schema.go`、
  `gateway_store_mysql.go`、`self_upgrade_scripts.go`）排除在扫描之外。

golden 计数：`sqlite_rowid` 从 1（`asset_coverage.go:342`）降到 0；`limit_in_subquery` 冻结在 1
（`monitor_retention.go:65`，位置钉住；INT-34 修复前的调用点是同一文件的 352 行）：INT-34 给 MySQL 加的是「再点名一次目标表 + `ORDER BY ... LIMIT` 派生表」
的写法，SQLite 侧仍保留 `IN (SELECT ... LIMIT ?)`（SQLite 合法），所以这条计数不会降为 0——它是一条
SQLite-only 站点，MySQL 侧走 `monitorRetentionMetricPruneMySQLSQL`。反向实验：删掉
`sqlite_rowid` 规则后 golden 报 `MySQL runtime rule table lost sqlite_rowid`，三个 rowid 例子也不再
判类；把 `limit_in_subquery` 的冻结数改成 0 后报
`found 1 limit_in_subquery findings, want 0 at monitor_retention.go:65`；把
`assetCoveragePlatformBaselinesSQL` 的 `ORDER BY` 去掉后
`TestAssetCoverageBaselineOrderAcrossEngines` 在 SQLite 侧返回插入序（默认基线变成最旧的那条）。

### INT-34：保留策略的 MySQL 等价写法与守卫

MySQL 后端的每个 tick 都打：

```text
[gateway] monitor retention: metric prune failed: Error 1235 (42000): This version of MySQL doesn't yet support 'LIMIT & IN/ALL/ANY/SOME subquery'
```

即指标清理在 MySQL 上**从未成功**，`inspection_metric_points` 无界增长，而 SQLite 正常。定位方式
是对 `backend/` 全量 grep `DELETE FROM ... IN (SELECT ...)`，命中两条同形语句：

- `internal/server/monitor_retention.go`（修复前第 352 行的 `pruneMonitorRetentionMetrics` 调用点）：`DELETE FROM inspection_metric_points
  WHERE id IN (SELECT id FROM inspection_metric_points WHERE collected_at_ms < ? LIMIT ?)`，外层
  循环 `monitorRetentionMetricBatches=20` 批、每批 `monitorRetentionMetricBatch=500` 行，`RowsAffected`
  不足一批即停。
- `internal/server/audit_purge.go:61` 与 `:76` 的 `deleteAuditBatchSQL` 同形，但只走 SQLite 的审计
  清理路径（`MATERIALIZED` CTE + `INDEXED BY` 本就 SQLite-only，见上文审计规则豁免），不在本次
  改动面内，留作后续。

**为什么不能只把 `LIMIT` 去掉**：这不是「少删几行」的问题，量级差三个数量级——`LIMIT` 是**批次上限**，
循环靠它把一次清理切成最多 20 个各 500 行的事务；去掉后一条 DELETE 会把整个过期窗口（生产上是
90 天的全部指标）放进同一个事务，锁持有时间、undo/binlog 体量与主从延迟一次性放大，且
`RowsAffected < batch` 的终止条件随之失效，节流由「每批 500」退化为「一次全清」。另外原来的
`IN (SELECT ...)` 没有 `ORDER BY`，SQLite 走 rowid 扫描顺序、MySQL 走主键顺序，两库删掉的**行集
不保证一致**，同一条策略在两个后端会得到不同历史，所以排序必须显式写进语句，而不是依赖引擎。

改法是两个方言各写一条完整语句、由 `gatewayMetricPruneBatchFor` 按方言选择（都在
`internal/server/monitor_retention.go`；语句留在这个文件而不是方言层，是因为运行期 SQL 审计跳过
`mysql_runtime_dialect.go` 一类渲染器文件，SQLite 形式的 `limit_in_subquery` 只有留在这里才能
继续被审计按文件名归属到保留策略这一处）：

```text
batch = SELECT id FROM inspection_metric_points WHERE collected_at_ms < ? ORDER BY collected_at_ms, id LIMIT ?
MySQL : DELETE target FROM inspection_metric_points AS target JOIN (<batch>) AS batch ON batch.id = target.id
SQLite: DELETE FROM inspection_metric_points WHERE id IN (<batch>)
```

MySQL 侧必须用**派生表**：同一张表既在 DELETE 目标里又在子查询里，直接写
`DELETE ... WHERE id IN (SELECT ... FROM inspection_metric_points ...)` 会撞
`ERROR 1093 You can't specify target table for update in FROM clause`；派生表带 `ORDER BY ... LIMIT`
被强制物化，1093 不成立，批次上限与排序都保留。SQLite 侧语句保持原形，只加了 `ORDER BY
collected_at_ms, id`：删除集合仍是「过期窗口里最老的 500 行」，只是顺序确定下来，与 MySQL 逐行相同。

**守卫**（`internal/server/monitor_retention_test.go`，`TestMonitorRetentionMetricPruneParityAcrossDialects`）：

- 夹具 620 行过期（>2 批）+ 5 行未过期，时间戳按 `i % 62` 造出十行同毫秒、且插入顺序不是时间顺序，
  这样「按插入顺序删」与「按 (collected_at_ms, id) 删」会得到不同的行集；
- A 段：两方言各执行**一批**渲染后的语句（limit=500），断言删除的 id 集合彼此相等、且等于按
  `(collected_at_ms, id)` 取前 500 行的集合；再把 SQLite 渲染串直接喂给 MySQL，断言
  `*mysql.MySQLError.Number == 1235`；
- B 段：每库建 5 条 run（新近 2 条、过期 2 条、1 条 running），用 `retentionTestPolicy(30, 1, 2)`
  强制清理，断言两库都是 `RunsDeleted=2`、`MetricsDeleted=620`，幸存 run 集合为
  {最新、次新、running}，remaining metric id 集合两库相等——keep-runs 与 running 不可删两条既有规则
  在 MySQL 上同样生效。

真机证据（`mysql:8.4` 容器，`DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13344`，实际 8.4.13）：

- 旧语句直连 MySQL：`ERROR 1235 (42000) ... 'LIMIT & IN/ALL/ANY/SOME subquery'`；新语句在相同数据上
  只删 `id in (1,2)`，留下 `3,4`，无 1093。
- 修复后真 Gateway 二进制（`DOOPS_GATEWAY_STORE=mysql`）跑 60 秒，日志出现
  `[gateway] monitor retention removed runs=0 items=0 metrics=3 dirs=0 dir_failures=0 (artifact=30d metric=90d keep_runs=50)`，
  无 `metric prune failed`；库里只剩未过期的两行。
- 反向实验一：把渲染器的 MySQL 分支改回 `IN (SELECT ... LIMIT)`，测试红在
  `mysql metric batch: Error 1235 (42000): This version of MySQL doesn't yet support 'LIMIT & IN/ALL/ANY/SOME subquery'`；
  另用「同一份修复后工作树、只把该语句还原成 HEAD 版本」重建网关二进制并连真库，20 秒内日志复现
  生产原文 `[gateway] monitor retention: metric prune failed: Error 1235 (42000) ...`，且两条过期行原样留存
  ——证明这条日志确实由该语句产生，不是别处的连带错误。
- 反向实验二：把清理循环改成「先删索引行、后删产物目录」，SF-15 的顺序守卫直接红：
  `the artifact directory must be removed by the same scan, got runs=1 items=1 metrics=0 dirs=0` 与
  `the payload must be removed before the index delete is attempted, got runs=0 items=0 metrics=0 dirs=0 dir_failures=0`
  （此前认为只有 `BEFORE DELETE` 触发器能观测顺序，实际这两条断言已能抓到顺序倒置）。

本轮未覆盖：`audit_purge.go` 的同形语句（SQLite-only 路径，见上）；MySQL 模式下的产物**文件**删除
只由两条后端共用的同一段代码路径覆盖，没有像指标行那样在真机上做双库同集合断言——本轮对齐的是
删除行集与批次语义。

### INT-38：审计保留策略的 MySQL 等价写法与守卫

INT-34 只修了指标清理；审计保留策略的两条语句是同一类故障的剩余部分。`backend/agent/pkg/gatewaycmd/gatewaycmd.go:141`
对每个 Gateway 都调用 `StartAuditRetention`，默认 `-audit-retention 0` 关闭，显式打开后
`StartAuditRetention` 先立即跑一次、之后每 6 小时一次；MySQL 后端每次都会在
`internal/server/audit_purge.go` 的 `deleteCompletedAuditSQL`（第 52 行）或 `deleteAuditBatchSQL`（第 76 行）
上失败，`audit_events` 无界增长，而 SQLite 正常。两条语句都带 SQLite-only 语法
（`WITH terminal AS MATERIALIZED`、`INDEXED BY idx_audit_started_ms`）和
`DELETE ... WHERE id IN (SELECT ... LIMIT ?)`，所以问题不是「换一个后端跑」而是两个方言各写一条完整语句：

- SQLite 侧原样保留 `deleteCompletedAuditSQL` / `deleteAuditBatchSQL`，一个字不改（`MATERIALIZED`
  CTE 与 `INDEXED BY` 只在这里合法）；
- MySQL 侧新增 `deleteCompletedAuditSQLMySQL` / `deleteAuditBatchSQLMySQL`，由
  `deleteCompletedAuditSQLFor` / `deleteAuditBatchSQLFor` 按 `auditPurgeDialect()` 选择
  （`s.dialect` 为空时回落 `gatewayDialect()`，与 INT-34 同法）；
- `drainAuditTx(tx, cutoffMs, dialect)` 多一个方言参数，`deleteBatch` 在事务里解析一次后传给它两次。

**为什么不能只把 `LIMIT` 去掉或把子查询摊平**：

- `LIMIT` 是批次上限（`AuditPurgeBatchSize=5000`），循环靠 `RowsAffected < batch` 判定「没有更多过期行」。
  去掉后一次事务会删掉整个过期窗口，锁持有时间与 undo/binlog 体量一次性放大，终止条件也失效；
- 本题的 `LIMIT` 在 `IN (SELECT ...)` 里，MySQL 8.4 直接报
  `ERROR 1235 (42000): This version of MySQL doesn't yet support 'LIMIT & IN/ALL/ANY/SOME subquery'`；
- 把派生表拆掉、直接写 `DELETE FROM audit_events WHERE id IN (SELECT ... FROM audit_events ...)` 会撞
  `ERROR 1093 You can't specify target table 'audit_events' for update in FROM clause`（同一张表既是删除目标又是子查询来源）。

改法是 MySQL 侧用**多表 DELETE + 派生表**：

```text
batch  = SELECT candidate.id AS id FROM audit_events AS candidate
         WHERE candidate.started_at_ms < ?
           AND candidate.action NOT IN ('agent:upgrade', 'audit:purge')
           AND candidate.status NOT IN ('running', 'executing', 'outcome_unknown')
         ORDER BY candidate.started_at_ms, candidate.id LIMIT ?
MySQL : DELETE target FROM audit_events AS target JOIN (<batch>) AS batch ON batch.id = target.id
SQLite: DELETE FROM audit_events WHERE id IN (SELECT id FROM audit_events INDEXED BY idx_audit_started_ms ...)
```

派生表带 `ORDER BY ... LIMIT` 被强制物化，删除目标和子查询来源分处两个物化层，1093 不成立；批次上限、
oldest-first 顺序与 `RowsAffected < batch` 的终止语义都保留。两个方言都按 `(started_at_ms, id)` 排序，
同一份数据在两库删掉的是同一组行，而不是各按引擎的扫描顺序取「前 N 行」。终态取代清理保持原注释的承诺：
admission 行只有在同一条语句、同一快照里评估出取代它的终态行时才被删——MySQL 版把这条 `EXISTS` 放进
派生表的 `WHERE` 里，CNT 结果与 SQLite 版逐行一致；派生表用 `LIMIT 18446744073709551615`（= 不限行，
但强制物化）以免优化器把它合并回 DELETE。

**守卫**（`internal/server/audit_purge_mysql_test.go`）：

- `TestAuditPurgeParityAcrossDialects`：15 行夹具（普通 completed、`running`/`outcome_unknown`/`executing`
  保护态、有/无终态的 admission、空 correlation admission、终态 outcome 非 succeeded/failed 的 admission、
  `audit:purge` 行、新鲜行、非法 JSON tail、普通 accepted 行），SQLite 与 MySQL 各一份，跑完整
  `PurgeExpiredAudit`，断言删除 id 集合逐条相等、等于期望集合、`Deleted` 计数一致、保护态全部存活；
- `TestAuditPurgeBatchParityAcrossDialects`：5003 行、时间戳倒序（插入顺序 ≠ 时间顺序），两库直接跑一批
  `deleteAuditBatchSQLFor`，断言各删 5000、`complete=false`、删除集合相等且等于最老的 5000 行；
- `TestAuditPurgeMySQLRejectsSQLiteRendering`：SQLite 终态语句喂 MySQL 必须 1064，SQLite 批删语句喂 MySQL
  必须 1064（先撞 `INDEXED BY`）；MySQL 渲染不得含 `MATERIALIZED`/`INDEXED BY`，必须含派生表 JOIN；
  SQLite 渲染与常量逐字节相同。

真机证据（`mysql:8.4.13` 容器，`DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13344`）：`go test -run TestAuditPurge`
双库全绿（13.6s）。真 Gateway 二进制（`DOOPS_GATEWAY_STORE=mysql`、`-audit-retention 1h`）跑 60 秒，
日志原文 `[gateway] audit retention deleted 7 rows in 1 batches older than 2026-11-24T05:13:11Z`，无任何
`audit retention failed` 行；库里 5 条过期 success + 1 对 upgrade admission/终态被删，`outcome_unknown`
（含启动时由 `executing` 恢复成 `outcome_unknown` 的行）与未过期行全部留存。

变异实验（临时改`backend/agent/internal/server/audit_purge.go`，跑完立即还原）：

- ① MySQL 终态分支退回 SQLite 文本：`mysql purge: Error 1064 (42000): ... near 'MATERIALIZED (`
  ——`AS MATERIALIZED`/`INDEXED BY` 在 MySQL 上先是语法错，不是 1235；
- ② MySQL 批删退回 `IN (SELECT ... LIMIT ?)`：`batch (mysql): Error 1235 (42000): This version of MySQL
  doesn't yet support 'LIMIT & IN/ALL/ANY/SOME subquery'`；
- ③ 去掉派生表包装（同时去掉 `LIMIT`）：`batch (mysql): Error 1093 (HY000): You can't specify target
  table 'audit_events' for update in FROM clause`。注意：**只**去掉派生表的 `ORDER BY ... LIMIT`、保留 JOIN
  形态时 MySQL 8.4 仍然执行成功（多表 DELETE 的派生表总会被物化），所以「1093 守卫」实际守的是「派生表被拆掉」
  这个形态，而不是「派生表有没有 ORDER BY」；强制物化是显式声明，不赌优化器行为。

本轮未覆盖：`internal/server/gateway_store_audit.go:319` 的手动/接口触发清理仍直接执行
`deleteCompletedAuditSQL`（SQLite 文本），MySQL 下会 1064——该文件不在本次改动面内，需另开任务；
运行期 SQL 审计的 golden（`gateway_mysql_runtime_test.go`）由 lead 按本渲染器更新。

### INT-41：手动/接口触发清理（`DeleteAuditBefore`）的方言化与守卫

INT-38 的「本轮未覆盖」里列的最后一条已经在 INT-41 关闭：`internal/server/gateway_store_audit.go:318`
的 `DeleteAuditBefore` 仍然直接执行 SQLite 文本 `deleteCompletedAuditSQL`，在 MySQL 上必然
`ERROR 1064`。先把可达性查清，避免修一条没人走的路：

- HTTP：`tunnel_hub.go:172` 注册 `/v1/audit` → `audit_http.go` 的 `HandleAudit` → `audit_http.go:122`
  `h.store.PurgeAuditBefore(auth, before)` → `audit_purge.go:225` `purgeAuditBefore` → `drainAuditTx`，
  INT-38 已经按方言渲染，**这条路 MySQL 正常**；
- CLI：`doops-gateway audit purge`（`pkg/gatewaycmd/gatewaycmd.go:457`）→ `gatewaycmd.go:470`
  `store.PurgeLocalAuditBefore(cutoff)` → `audit_purge.go:207` `purgeAuditBefore`（带 `AuditRetentionActor`），
  同样已方言化；
- `DeleteAuditBefore`（store 级入口）**当前没有生产调用者**，只有测试
  （`execution_audit_test.go:300/404/437`、`upgrade_audit_test.go:388`）；它是导出方法，SQLite 文本在
  MySQL 下会 1064，属于潜在缺陷而不是正在发生的故障。

改法只有一行：`DeleteAuditBefore` 改为执行 `deleteCompletedAuditSQLFor(s.auditPurgeDialect())`。不在这个
入口里另写一套选择逻辑，也不复制方言判断——`auditPurgeDialect()` 就是 INT-38 引入的那一个（`s.dialect`
为空时回落 `gatewayDialect()`），SQLite 侧拿到的仍是原常量、逐字节不变。这样两个清理入口共用同一对方言语句，
以后改选择规则只需要改一处。

守卫在 `internal/server/gateway_store_audit_test.go`：

- `TestDeleteAuditBeforeParityAcrossDialects`：复用 INT-38 的 15 行夹具（含 `running`/`outcome_unknown`/
  `executing` 保护态、有/无终态的 admission、`audit:purge` 行、新鲜行、非法 JSON tail），SQLite 与 MySQL
  各一份，各跑一次 `DeleteAuditBefore`，断言删除 id 集合逐条相等、等于期望的 completed-only 集合
  （index `{0,4,5,8,10,13}`；accepted 无终态的行留给批删语句）、受影响行数相等、保护态行全部存活；
- `TestAuditPurgeHTTPParityAcrossDialects`：走**真实 HTTP 入口**——`hub.RegisterRoutes(mux)` 后
  `DELETE /v1/audit?before=<RFC3339Nano>`（admin 用户 + user token），断言两端都 200、`Deleted` 计数一致且
  等于期望集合、双库删除集合相等、保护态 `{1,2,3,6,7,9,11,12}` 存活。这条测试同时把 INT-38 修好的
  `HandleAudit` → `PurgeAuditBefore` 路线钉在真机上。

真机证据（`mysql:8.4.13`，`DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13344`）：两个测试双库全绿（3.4s）；连同运行期
SQL 审计 golden 一起跑 4.0s 全绿。

反向实验（临时把该行退回 `deleteCompletedAuditSQL`，跑完立即还原）：`TestDeleteAuditBeforeParityAcrossDialects`
失败原文

```text
gateway_store_audit_test.go:55: mysql DeleteAuditBefore: Error 1064 (42000): You have an error in your SQL syntax;
check the manual that corresponds to your MySQL server version for the right syntax to use near 'MATERIALIZED (
    SELECT cluster, instance, correlation_id,
        json_extract(CASE WH' at line 1
```

即该路径在 MySQL 上是硬失败（语法错先于任何语义），守卫能抓住退回。

本轮未覆盖：`DeleteAuditBefore` 没有生产调用者，所以没有独立的「60 秒真 Gateway 日志」——它不在任何
Gateway 启动/定时路径上；HTTP 入口那 60 秒日志由 INT-38 提供。另外在**新建的 MySQL 库**上无法构造完整
`GatewayHub`：`NewEdgeRegistry`（`tunnel_hub.go:127`）在缺少 edge bootstrap 行时 panic
`sql: no rows in result set`，那行属另一会话在做的 bootstrap 改动，因此 HTTP 测试用只含 `store`+`opts` 的
裸 hub 注册真实路由（`HandleAudit` 只需要这两者），待 bootstrap 落地后应改回 `NewGatewayHub`。

### INT-40：标量 `MIN(a, b)`/`MAX(a, b)` 的方言化与守卫

INT-40 之前在真 Gateway（MySQL 后端）每 30 秒一条
`[gateway] target resource sync failed: mx/mx1: Error 1064 (42000): ... near ', ?) WHERE cluster=? AND instance=? AND kind IN (SELECT jt.value FROM JSON_TABLE' at line 1`。
根因是 `backend/agent/internal/server/agent_resource_store.go:175` 的观察计数语句：

    UPDATE agent_observations SET misses=MIN(misses+1, ?) WHERE cluster=? AND instance=? AND kind IN (...)

SQLite 的 `MIN`/`MAX` 有标量两参形式，MySQL 只有聚合单参形式，第二个参数直接 1064。既有规则只认
upsert `SET` 片段里的 `max()`/`min()`（INT-13 已改 `GREATEST`/`LEAST`），运行期普通 SQL 不在其内。

规则 `scalar_minmax`：匹配大小写敏感的 `MIN(`/`MAX(`，再用 `gatewayMySQLParenthesized` 取整对括号、
`gatewayMySQLTopLevelArgumentCount` 数顶层逗号，**两个及以上参数才报**。大小写敏感是有意的：SQL 写大写、
Go 内建 `max(a, b)`/`min(a, b)` 写小写，后者是编译期调用，不该报警。注释与字符串字面量先由
`gatewayMySQLWithoutComments` 抹成等长空格，所以 `-- ... MIN(x, y)` 与 `'...MAX(a,b)'` 不命中。

`gatewayMySQLLooksLikeSQL` 对这条类别单独 `continue`：Go 字符串里的两参 `MAX(` 多数是 upsert `SET`
**片段**（`"online_agents = MAX(platform_load_samples.online_agents, excluded.online_agents)"`），它由
`gatewayUpsertSQL` 渲染成 `GREATEST(...)`，本身不是语句；真语句以 `UPDATE`/`SELECT` 开头，仍由
`gatewayMySQLSQLStart` 收进扫描。不加这个例外，6 条 `gateway_store_agents.go:248` 的片段会变成假阳性。

渲染层：`gatewayScalarLeastSQL(dialect, a, b)` 在 SQLite 返回 `MIN(a, b)`、MySQL 返回 `LEAST(a, b)`；
`gatewayScalarGreatestSQL` 对应 `MAX`/`GREATEST`。`agentObservationMissesSQL(dialect)` 用前者拼出整条
语句，`storeObservationEpoch` 调它。语义写清并断言：`LEAST` 与 SQLite 标量 `MIN` 都是**任参 NULL 则结果
NULL**，与聚合 `MIN` 的「忽略 NULL」不是一回事——这条语句的两个参数是 `misses+1` 与阈值常量，都不为
NULL，所以行为不变，但测试把三组 NULL 输入钉住，避免以后有人拿聚合语义改它。

golden 计数：`scalar_minmax` 落到 `absent`（0），理由写「`agent_observations.misses` 走
`gatewayScalarLeastSQL`」；规则表注册断言把这条类别也钉住，删掉规则会报
`MySQL runtime rule table lost scalar_minmax`。规则刚加、代码未改时的红是 7 条：
`agent_resource_store.go:175` 一条真站点 + `gateway_store_agents.go:248` 六条 `SET` 片段（后者由上面的
`looksLikeSQL` 例外消化，不是规则放宽）。

双库用例（`agent_resource_scalar_minmax_test.go`）：同一批 `agent_observations` 夹具（`machine/host-a`、
`machine/host-b`、`cluster/cluster-a`，初始 `misses=0`）在 SQLite 与 `mysql:8.4` 上跑同一句
`agentObservationMissesSQL(dialect)`，`threshold=2`、report `["machine"]` 连续三轮，断言
`misses`：报告的 kind 每轮 +1 并被阈值截住，未报告的 kind 不动，链路里已不存在的资源随后被 upsert 归零。
输出：

    SQLite misses: [cluster/cluster-a=0 machine/host-a=2 machine/host-b=2]
    MySQL  misses: [cluster/cluster-a=0 machine/host-a=2 machine/host-b=2]

反向实验（干净树 `git archive` 上做，做完立即还原）：

1. 方言层的 MySQL 分支退回 `MIN(a, b)`：
   `--- FAIL: TestAgentObservationMissesAcrossEngines` /
   `agent_resource_scalar_minmax_test.go:95: mysql round 0: Error 1064 (42000): ... near ', ?) WHERE cluster=? AND instance=? AND kind IN (SELECT jt.value FROM JSON_TABLE' at line 1`，
   与真机日志同一条错误；
2. 规则放宽成单参数也报（`>= 1`）：`MySQL runtime audit found 9 scalar_minmax findings at
   asset_monitor_store.go:24, asset_monitor_store.go:101, audit_purge.go:52, audit_purge.go:96,
   machine_diagnostic.go:378, machine_inventory.go:177, machine_inventory.go:187, monitor_engine_store.go:700,
   monitor_metric_store.go:495, want 0`，且 `aggregate_min`/`aggregate_max_of_an_expression` 两个负例失败。

真机（`mysql:8.4.13`，新库 `int40_e2e` 用 `utf8mb4_0900_bin`，真 Gateway + 真 Agent）：修复后每 30 秒的
同步日志里 `Error 1064 ... MIN(...)` 消失，同一条 SQL 在 `mysql:8.4` 上直接执行也返回
`SQLite misses == MySQL misses`。**但整条资源同步链路仍未走绿**，它被两个不属于本任务改动面的缺陷挡住，
按顺序是：

- `Error 1271 (HY000): Illegal mix of collations for operation 'UNION'`：`declaredAgentResourcesSQLFor` 的
  `inventory_resource_catalog` 与 `credentials` 两支把 `JSON_EXTRACT(...)` 与 schema 的
  `utf8mb4_0900_bin` 列 `UNION ALL`，而 `JSON_EXTRACT` 返回 `utf8mb4_bin`。最小复现：
  `CREATE TEMPORARY TABLE t (v VARCHAR(10) COLLATE utf8mb4_0900_bin); SELECT COALESCE(v,'') FROM t
  UNION ALL SELECT JSON_EXTRACT('{"i":"x"}','$.i');` → 1271；给 `JSON_EXTRACT` 加
  `COLLATE utf8mb4_0900_bin` 后正常返回。验证到「1271 消失、链路继续推进」后已回退，留给后续任务
  （需要连带的方言渲染与守卫）。
- 再下一个 `Error 1064 ... near 'key'`：MySQL 侧有一处 `key` 列名未按方言加反引号。

本轮未覆盖：`gateway_store_agents.go:248` 的六条 `SET` 片段依赖 `gatewayUpsertSQL` 的 `MAX`→`GREATEST`
翻译，这条翻译本身没有单独的方言测试；`scalar_minmax` 也不覆盖 `MIN`/`MAX` 出现在 `ORDER BY` 或
`GROUP BY` 的聚合用法（那是合法 MySQL）。

### INT-44：观察表的 `key` 列与 UNION 标量排序规则

INT-40 留给后续任务的两条错误在 INT-44 关闭，它们是同一条 target resource sync 链路上的先后两堵墙：

1. `Error 1064 (42000): ... near 'key' at line 1`。真机 general_log 里 UNION 的 Prepare/Execute 之后直接
   ROLLBACK，且**没有** `ORDER BY kind` 的 Prepare —— 语句在解析期就死了。命中的是
   `agent_resource_store.go:212`（INT-44 前）的观察读取
   `SELECT details,last_seen,misses FROM agent_observations WHERE cluster=? AND instance=? ORDER BY kind,key`。
   `agent_observations.key` 是 MySQL 8.4 保留字，裸写即 1064。upsert 的列清单（`:194`）不在此列：
   `gatewayMySQLUpsertInsert` 早已把列清单整体反引号化，所以它一直是合法的——这解释了为什么只有读取先报错。
   `agent_resource_inventory.go:88` 与 `:132` 同样裸写 `key`，属于同一类潜伏缺陷。
2. `Error 1271 (HY000): Illegal mix of collations for operation 'UNION'`。`declaredAgentResourcesSQLFor` 的
   `inventory_resource_catalog`/凭据两支把 `json_extract(...)` 放进与 canonical schema 的
   `utf8mb4_0900_bin` 列相同的 UNION 列位，而 JSON 值的字符串形态是 `utf8mb4_bin`。最小复现（`mysql:8.4.13`，
   库/表 `utf8mb4_0900_bin`）：
   `CREATE TEMPORARY TABLE t (v VARCHAR(10) COLLATE utf8mb4_0900_bin); SELECT COALESCE(v,'') FROM t UNION ALL SELECT JSON_EXTRACT('{"i":"x"}','$.i');` → 1271。

修法（全部走方言渲染，不就地写字面量）：

- `gatewayKeyColumn()` / `gatewayKeyColumnFor(dialect)`：SQLite 原样 `key`，MySQL 反引号。观察表 upsert 的
  INSERT 列清单、`ON DUPLICATE KEY UPDATE` 目标列（SQLite 侧是 `ON CONFLICT(...)` 的 target 列表）、
  `readAgentObservations` 的 `ORDER BY`、inventory 的两处谓词全部经它渲染。SQLite 渲染与改前逐字节相同，
  `TestAgentResourceKeyRendering` 按整条语句（含原 `ON CONFLICT ... DO UPDATE SET ...` 尾巴）比对。
- `gatewayJSONTextScalarFor(dialect, expression)`：MySQL 返回 `JSON_UNQUOTE(<expr>) COLLATE utf8mb4_0900_bin`，
  SQLite 原样返回。**为什么必须显式加**：JSON 标量的字符串形态是 `utf8mb4_bin`，与列声明的
  `0900_bin` 不同，同列位 UNION 即 1271；把 COLLATE 钉在表达式上，union 的两侧才是同一个规则。
  **为什么不改列定义**：`utf8mb4_0900_bin` 是全库 identity 的比较规则（大小写与尾空格都参与），schema
  安装器按它校验空库，改成 `bin` 会改变所有表的等值语义与索引。**为什么同时要 JSON_UNQUOTE**：
  `JSON_EXTRACT` 转字符串保留 JSON 引号（HEX `227822`），而 SQLite 的 `json_extract` 返回裸 `x`（HEX `78`），
  不脱引号两库返回的 identity 字节不同；COLLATE 不改字节，只解决 1271，所以两者都要。禁止把 COLLATE
  放宽成 `utf8mb4_general_ci` 一类：那会把仅大小写/尾空格不同的 identity 判等，等于悄悄改掉资源身份。
- 守卫：新规则 `reserved_bare_identifier`（词表当前只有 `key`，做成表是为了以后加词不新增类别）。匹配器
  要求字面量以 `SELECT|INSERT|UPDATE|DELETE|REPLACE|WITH` 开头，先经 `gatewayMySQLWithoutComments` 抹掉
  注释与引号段，再按位置判定：`key` 后面必须是 `=`、`<`、`>`、`,`、`)`、行尾或
  `FROM/WHERE/ORDER/GROUP/LIMIT/AS/IS/IN/NOT/AND/OR/DESC/ASC/USING` 才报；紧跟在
  `PRIMARY/FOREIGN/UNIQUE/DUPLICATE/INDEX` 后面的 `KEY` 是关键字不是列，跳过。注释、字符串字面量、
  反引号标识符、`key_hint` 这类更长标识符都不命中（dialect examples 里有对应负例）。位置钉交给 golden：
  `reserved_bare_identifier` 落在 `absent`（0）并写清理由，规则表注册断言把它加进列表，删规则会报
  `MySQL runtime rule table lost reserved_bare_identifier`。规则刚加、代码未改时的红是 2 条：
  `agent_resource_inventory.go:88`、`agent_resource_store.go:207`。
- 1271 无法用单条静态规则判「同列位」——它取决于列定义（运行时才知道排序规则）。所以这条**靠真机断言守住**：
  `TestAgentObservationKeyColumnAcrossEngines` 在 `utf8mb4_0900_bin` 的新库上跑真实
  `reconcileAgentResources`，UNION 一执行就会暴露回归；渲染层再由 `TestAgentResourceKeyRendering` 断言
  MySQL 文本必须含 `COLLATE utf8mb4_0900_bin` 且不得含 `utf8mb4_general_ci`。

双库证据（`mysql:8.4.13`，`DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13334`，新库 `utf8mb4_0900_bin`）：同一夹具
（两代观察，第二代丢一台机器）分别经 SQLite 与 MySQL store 各跑一次 reconcile：

    first sync: revision=1 counts=map[cluster:1 machine:1] states=map[cluster/current:discovered machine/web-1:discovered] observations=[cluster/current=0 machine/web-1=0]
    second sync: revision=2 counts=map[cluster:1] states=map[cluster/current:discovered] observations=[cluster/current=0 machine/web-1=1]

两库 revision、counts、states、`kind/key/misses` 行逐项相等，且第二轮的 `machine/web-1` misses 必须为 1、
revision 从 1 涨到 2。

真机端到端（`mysql:8.4.13`，新库 `doops_int44b` 用 `utf8mb4_0900_bin`，真 Gateway 端口 42460 + 真 Agent
端口 42412，日志 `/tmp/e2e44/gw.log`、`/tmp/e2e44/agent.log`）：Agent 上线前 `agent_resource_state` 没有行；
连上后第一次同步落 `manifest_revision=1, applied_revision=1`；随后插入一台 vm 机器（`inventory_machines'
kind='vm'` + `inventory_agent_assets`）触发摘要变化，30 秒内涨到 `manifest_revision=2, applied_revision=2`，
期间 `target resource sync failed` 一条都没有。回读原文：

    -- 上线前
    mysql> SELECT ... FROM doops_int44b.agent_resource_state;
    Empty set
    -- 上线后
    manifest_revision applied_revision manifest_hash generated_at
    1                 1                sha256:1953551a... 2026-11-24T07:25:25.793522Z
    -- 插入机器后
    2                 2                sha256:6278f5c2... 2026-11-24T07:28:55.843524Z
    mysql> SELECT kind,`key`,misses FROM doops_int44b.agent_observations ORDER BY kind,`key`;
    kind     key      misses
    cluster  current  0

反向实验（干净树里做：`git archive` 到 `/tmp/int44tree`，变异在 `/tmp/mut`，每次都是真 Gateway + 真 Agent
+ 新库，跑完即弃）：

1. `readAgentObservationsSQLFor` 退回裸 `ORDER BY kind,key`：真机日志
   `[gateway] target resource sync failed: mx2/mx1: Error 1064 (42000): ... near 'key' at line 1`（每 30 秒一条），
   `agent_resource_state` 0 行、`manifest_revision` 停在 -1（无行）；
2. `gatewayJSONTextScalarFor` 退回原表达式（无 `JSON_UNQUOTE`、无 `COLLATE`）：
   `[gateway] target resource sync failed: mx2/mx1: Error 1271 (HY000): Illegal mix of collations for operation 'UNION'`，
   同样 0 行；
3. `agent_resource_inventory.go:88` 的 `key=? ` 退回裸写：日志转为
   `[gateway] target resource sync failed: mx2/mx1: target_manifest_unavailable`，`manifest_revision=1` 而
   `applied_revision=0`——这正是「清单发了但应用不了」的现场，说明这条潜伏缺陷也在生产调用点上；
4. 审计规则本身：把上述 1 与 3 的渲染同时退回裸 `key` 后跑 `TestGatewayMySQLRuntimeSQLAudit`：
   `MySQL runtime audit found 2 reserved_bare_identifier findings at agent_resource_inventory.go:88, agent_resource_store.go:207, want 0`；
   还原后回到 0。

golden 计数前后：新增类别前 `want`/absent 集合里没有 `reserved_bare_identifier`；加入后它落在 `absent`（0），
其余类别计数不变（`julianday` 1、`materialized_cte` 1、`sqlite_introspection` 5、`explain_plan` 13、
`create_index_if_absent` 29、`drop_index_if_absent` 2、`auto_increment` 8、`partial_index` 1、
`reserved_use` 4、`limit_in_subquery` 1），规则表 22 → 23 类。

未覆盖：`key` 词表只收了 `key`，MySQL 8.4 还有 `groups`/`rank`/`row_number` 等保留字；它们目前没有作为裸标识符
出现在本包 SQL 里，加词属于扩大改动面，留给后续任务（加词只需在表里追一项，位置钉与 golden 不变）。

## Cutover command and the introspection golden (INT-49 / INT-52)

The cutover command (`doops-gateway store mysql-copy`, `backend/agent/internal/server/gateway_mysql_copy_command.go`)
opens the source as a read-only SQLite snapshot, so its ten SQLite probes (`PRAGMA table_info`,
`pragma_table_list`, the `sqlite_schema`/shadow-table filter) are expected there by contract. The runtime audit
therefore lists 15 `sqlite_introspection` findings instead of 5, and `gateway_mysql_runtime_test.go` froze the split
per file (audit_indexes 1, credential_store_migration 1, copy command 10, gateway_store 1, gateway_store_schema 2)
rather than an allow-list: a probe added to, moved into or duplicated in any file changes a count and fails.

`--dry-run` on an empty database has to answer the same question the real run answers. It projects the schema the
real run would install (tables and `version=gateway-mysql-schema-1`), then decides: a self-referencing foreign key
(only `inventory_machines.inventory_machines_ibfk_1` today) exits 2 with `mysql_copy_destination_foreign_key_cycle`,
which is exactly the refusal the real run hits; without one it exits 0 and prints the full plan. The static scan is
guarded against the installed schema by `TestGatewayMySQLSchemaSelfReferencesMatchInstalledSchema`
(`information_schema.KEY_COLUMN_USAGE`), and reverting it to "empty means usage failure" turns two assertions red.

## Runtime wiring (INT-22)

The rendering work above makes every audited statement acceptable to MySQL, but none of them has ever
executed there: `OpenGatewayStore` (`backend/agent/internal/server/gateway_store.go:32`) still opens only
SQLite, and `migrate()` (`gateway_store_schema.go:9`) runs `PRAGMA journal_mode=WAL` before any table DDL,
which MySQL rejects with 1064. The wiring judgement, minimal slice, and bootstrap constraints are summarized below:

- Entry point, per-store dialect and bootstrap are the three structural blocks; isolation/retry and the
  schema upgrade path are the two semantic ones.
- The minimal runnable slice is audit + grants + users, about 170 lines across five files, with zero changes
  to `gateway_store_audit.go`, `gateway_store_grants.go` or `gateway_store_users.go`.
- `installGatewayMySQLSchema` is the bootstrap, but it only accepts an empty schema and cannot own a version
  table (the copier requires the exact SQLite table set); the migration ledger has to be decided first.
- The audit purge needs a two-statement rewrite: MySQL rejects both `MATERIALIZED` and a subquery over the
  table being deleted (1093), and `INDEXED BY` is SQLite-only.
- The guards stay green while wiring is absent (`-run 'TestGatewayMySQL(RuntimeSQLAudit|ColumnsMatchExport|
  UniqueKeysMatchExport|InsertsNameColumns|SchemaCoversRuntimeDDL)$'` passes), which is exactly why they
  cannot substitute for a real MySQL run.

## 批次计划（第三批及以后）

Third-batch classification of all 95 runtime-audit sites: 32 sites are on request/ticker hot paths, 1 is a one-shot data backfill, 14 are diagnostic (EXPLAIN/PRAGMA), 44 are startup DDL already owned by the frozen schema, and 4 are one-shot schema probes. Batch order is time functions first (authorization), then identifier quoting, then parameter reordering, then JSON; no site needs a change before MySQL storage mode is wired.

## 会话排序规则（session collation，2026-11-24）

MySQL 把 `JSON_TABLE ... COLUMNS(...)` 的派生列按**会话**排序规则（session collation）赋值，而不是按被比较列的排序规则。
方言层用 `JSON_TABLE` 渲染 `json_each`/`json_group_array`（INT-20），go-sql-driver 在 DSN 未指定 `collation` 时协商
`utf8mb4_general_ci`，于是派生列与任何继承数据库默认排序规则（MySQL 8.4 服务端默认 `utf8mb4_0900_ai_ci`）的表列比较时
直接报错：

```
Error 1267 (HY000): Illegal mix of collations (utf8mb4_0900_ai_ci,IMPLICIT) and (utf8mb4_general_ci,IMPLICIT) for operation '='
```

复现路径：全新库（默认 `utf8mb4_0900_ai_ci`）上第一次 target resource sync 即失败，控制台一直显示
`revision 1 / applied_revision 0 / supported false`，manifest 不再推进。手工 SQL 可复现同一对排序规则：

```sql
SET NAMES utf8mb4 COLLATE utf8mb4_general_ci;
SELECT COUNT(*) FROM agent_observations
 WHERE kind IN (SELECT jt.value FROM JSON_TABLE('["cluster"]', '$[*]' COLUMNS(value VARCHAR(512) PATH '$[0]')) AS jt);
-- ERROR 1267；把 SET NAMES 换成 utf8mb4_0900_ai_ci 后返回 0
```

第二个来源是 schema 文件首部曾经的 `SET NAMES utf8mb4 COLLATE utf8mb4_0900_bin;`：它只改**执行安装的那条连接**的会话排序规则，
安装完成后复用该连接的查询会带着 `0900_bin` 去比较 `0900_ai_ci` 列，同样 1267。该语句已删除，安装期连接也不再回到池里（规则 4）。

规则（五条，都是启动期判定，失败即退出，不降级）：

1. `openGatewayMySQLStore` 先读 `@@character_set_database` / `@@collation_database`；字符集不是 `utf8mb4` 时
   `gateway_mysql_charset_unsupported`。
2. 会话排序规则取数据库默认值：`collation` 参数已在 DSN 里且与数据库默认不同时
   `gateway_mysql_collation_mismatch`（DSN 不能覆盖它，因为 JSON_TABLE 比较用的是会话排序规则）。
3. 库内每张表的 `table_collation` 必须等于数据库默认值（安装 DDL **之前**检查），否则 `gateway_mysql_collation_mismatch`
   并点名表名——只对齐会话不够，表级覆盖仍然会在运行期炸。
4. 安装期不保留空闲连接（`SetMaxIdleConns(0)` 只在 `gatewayMySQLPrepareRuntimeSchema` 期间生效）：安装连接用完即关，
   schema 里任何 `SET NAMES` 都无法污染后续会话；安装完成后立刻用池里的连接断言 `@@collation_connection` 等于数据库默认。
5. 逐列检查 `information_schema.columns.collation_name`（安装**之后**）：`ALTER TABLE ... MODIFY c ... COLLATE foreign`
   不会改 `table_collation`，表级检查看不见，只有列级检查挡得住。

数据库默认排序规则还必须是 go-sql-driver **认得**的：驱动对未知排序规则（实测 `utf8mb4_0900_bin`）静默回落到
`utf8mb4_general_ci`，会话因此永远不可能等于数据库默认。这种库会被规则 4/5 直接拒绝，错误里写明「驱动回落到 general_ci、
请改用 MySQL 8 默认的 `utf8mb4_0900_ai_ci`」，而不是等到运行期报 1267。

identity 比较由方言层显式渲染 `COLLATE utf8mb4_0900_bin`（`mysql_runtime_dialect.go:749`），与会话排序规则无关；
schema 文件不再逐列声明排序规则，全部继承数据库默认。

证据（2026-11-24，integrator 与本机真机 mysql:8.4.13）：

- `TestGatewayMySQLSessionCollationMatchesDatabase`：显式 `utf8mb4_0900_ai_ci` 的新库上，会话排序规则等于数据库默认，
  且上面那条 JSON_TABLE 语句返回 0 行不报错。
- `TestGatewayMySQLStoreRejectsColumnWithForeignCollation`：只把 `monitor_runs.summary` 改成 `utf8mb4_general_ci` 后重启，
  启动失败并点名 `monitor_runs.summary=utf8mb4_general_ci`；同一用例断言该表的 `table_collation` 仍是数据库默认，证明表级检查看不到它。
- `TestGatewayMySQLStoreRejectsDatabaseCollationTheDriverCannotSet`：库默认 `utf8mb4_0900_bin` 时启动失败，错误点名该排序规则、
  驱动回落行为与可用的 `utf8mb4_0900_ai_ci`。
- `TestGatewayMySQLSessionCollationAssertionSeesPooledChange`：把一条连接 `SET NAMES ... utf8mb4_general_ci` 后放回池，
  安装后断言必须报 `post-schema session collation is utf8mb4_general_ci`。
- `TestGatewayMySQLStoreRejectsTableWithForeignCollation`：表级 `utf8mb4_general_ci` 时启动失败并点名该表。
- `TestGatewayMySQLStoreRejectsDSNCollationOverride`：DSN 写 `collation=utf8mb4_general_ci` 时启动失败。
- 反向实验：去掉会话对齐 → 会话变 `utf8mb4_general_ci`；恢复 `SET NAMES ... 0900_bin` → 会话变 `utf8mb4_0900_bin`；
  关掉表级守卫 → 错误退化成 `mysql_schema_ledger_missing`。
- 真进程：干净树重建的 Gateway + Agent，全新库 `doops_col_e2e`（默认 ai_ci），`/v1/assets/target-manifest`
  连查三次为 `rev 1 / applied 1 / online true / supported true / counts {"container":1}`，网关日志无 sync 失败。


## hash 列宽与启动扩容（2026-11-24，INT-74）

INT-64 起用户 token 的哈希从 bcrypt（60 字符）改成 `sha256:<64 hex>`（71 字符），INT-69 把同一格式
用到 `agent_enrollments.hash` 与 `edge_enrollments.hash`；DDL 里这三列却仍是 `VARCHAR(64)`。
MySQL 严格模式（默认）下每次签发/登记都失败，而 SQLite 无长度约束，所以两边测试一直不同步。

真机复现（mysql:8.4.13，`DOOPS_GATEWAY_STORE=mysql` + `openGatewayMySQLStore`，走真实代码路径，非手写 SQL）：

```
PROBE token: len(plain)=71 err=Error 1406 (22001): Data too long for column 'hash' at row 1 created=false
PROBE agent_enrollment: len(hash)=71 err=Error 1406 (22001): Data too long for column 'hash' at row 1 created=false
PROBE edge_enrollment: len(hash)=71 err=Error 1406 (22001): Data too long for column 'hash' at row 1 created=false
```

修法两层：

- `gateway_mysql_schema.sql`：`tokens.hash`、`agent_enrollments.hash`、`edge_enrollments.hash`
  改为 `VARCHAR(191)`。选 191 而不是刚好 71 或 128：这份 DDL 其余 hash 列都已是 191；utf8mb4 下 191
  是索引安全宽度；对 71 有 2.69 倍余量，换成 sha512 形态（135 字符）也不用再改表。宽度单点定义在
  `gatewayMySQLHashColumnWidth`，DDL 守卫与启动扩容都读它。
- 已装库不能只靠新 DDL（`installGatewayMySQLSchema` 只在空库建表）：`gatewayMySQLPrepareRuntimeSchema`
  现在先调 `gatewayMySQLWidenHashColumns`，读 `information_schema.columns.character_maximum_length`，
  仅当列比目标窄才发 `ALTER TABLE ... MODIFY COLUMN ... VARCHAR(191) NOT NULL`，否则一条语句都不发。
  因此重复启动幂等，不动其它列、不重写行：legacy bcrypt 行字节不变，下次校验成功时才惰性升级成 fast hash。

守卫（`gateway_mysql_hash_width_test.go`，长度不写死 71）：

- `tokenFastHashHexLength = 2 * sha256.Size`、`tokenFastHashMaxLength = len(tokenFastHashPrefix) + tokenFastHashHexLength`；
  用例先断言 `len(tokenFastHash(...))` 等于该推导值。
- `TestGatewayMySQLDDLHashWidthHoldsStoredForm`：解析权威 DDL，目标列非 VARCHAR 或宽度小于代码最大长度即红并点名 `table.column`。
- `TestGatewayMySQLHashColumnsAcceptIssuedHashes`：真库全新安装后三列宽度为 191，`CreateToken`/`CreateAgentEnrollment`/`CreateEdgeEnrollment` 全部成功，三行 `CHAR_LENGTH(hash)=71`。
- `TestGatewayMySQLHashWidthUpgradeFromLegacyInstall`：全新安装 → 把三列 ALTER 回 64 并造一条 legacy bcrypt 行 → 断言 71 字符写入报 1406 → 重启两次：宽度 191、其余列与行数不变、legacy 行字节不变、`VerifyUserToken` 把该行升级成 fast hash、第二次重启仍幂等。
- `TestSQLiteTokenHashLengthFitsMySQLDDL`：SQLite 上真实写三条记录，读回 `LENGTH(hash)` 断言等于推导值且不超过 MySQL DDL 宽度——SQLite 无长度约束，这条跨库不变量是它唯一能防的回归。

反向实验（原文）：把 `tokens.hash` 改回 `VARCHAR(64)`（只改 DDL）→
`gateway_mysql_hash_width_test.go:99: tokens.hash: DDL declares VARCHAR(64) but gatewayMySQLHashColumnWidth=191; a fresh install and the startup widen would disagree`；
去掉启动扩容调用（已装旧 schema 的库）→
`gateway_mysql_hash_width_test.go:324: tokens.hash: information_schema reports VARCHAR(64), want 191 (startup upgrade of a legacy install)`。

未覆盖（如实记录）：写入是单条 INSERT，严格模式下失败整笔回滚、不留半行，调用方拿到错误也不会签发 token；
但若某库曾以非严格 `sql_mode` 运行，71 字符可能被静默截断成 64，留下不可用的哈希。运营排查语句为
`SELECT id FROM tokens WHERE hash LIKE 'sha256:%' AND CHAR_LENGTH(hash) <> 71`（两张登记表同理）。
本轮没有实现行级修复，也没有生产库可验证，这条"已存在的截断空洞"未覆盖。

## execution_records 的运行期补列（2026-11-24，INT-91）

INT-87 给 `execution_records` 加了 `terminal_truncated`、`terminal_bytes`，用来标记超过 256 KiB 的终态
被截断。SQLite 侧由 `migrate()` 的 `CREATE TABLE IF NOT EXISTS` 与 `ensureColumn` 兜住；MySQL 侧只有
`installGatewayMySQLSchema`（只在空库建表），所以**已安装的库重启后这两列并不存在**：写不进标记，
读不出标记，重启回读会把截断过的 terminal 当成完整文档交给调用方。

真机复现（mysql:8.4.13，端口 13335；先 `ALTER TABLE execution_records DROP COLUMN terminal_truncated,
DROP COLUMN terminal_bytes` 造出旧 schema，再走 `OpenGatewayStore` 的真实启动路径）。探针只用改前
HEAD 已有的符号，因此在改前树上也能编译，不随本次提交：

```
zz_probe_mysql_upgrade_test.go:52: after the reopen execution_records has 0 of the 2 INT-87 columns; the runtime upgrade did not run
--- FAIL: TestProbeExecutionRecordColumnsUpgradeOnMySQL (0.74s)
```

修法与 INT-74 的 hash 列宽同形：`gatewayMySQLPrepareRuntimeSchema` 在 `gatewayMySQLWidenHashColumns`
之后调 `gatewayMySQLEnsureExecutionRecordColumns`。它先读 `information_schema.TABLES`，表不存在
（比 INT-87 还老的库）就直接返回、不报错；存在则逐列读 `information_schema.COLUMNS`，缺才发
`ALTER TABLE ... ADD COLUMN`，已存在则一条语句都不发，返回本轮补了哪些列。列定义单点写在
`gatewayMySQLExecutionRecordColumns`：

| 列 | 定义 | 老行的含义 |
|---|---|---|
| `terminal_truncated` | `TINYINT NOT NULL DEFAULT 0` | 写这行时还没有标记，即没有截断 |
| `terminal_bytes` | `BIGINT NOT NULL DEFAULT 0` | 未记录原始字节数 |

手动等价语句（运维提前补列时与运行期路径等价，两条路径都幂等）：

```sql
ALTER TABLE execution_records
  ADD COLUMN terminal_truncated TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN terminal_bytes BIGINT NOT NULL DEFAULT 0;
```

回滚：`ALTER TABLE execution_records DROP COLUMN terminal_truncated, DROP COLUMN terminal_bytes;`
删列后新旧代码都能启动（新代码下次启动会再次补列，补出来的老行仍是 0/0，即"未截断"形状）；代价是
这期间写入的截断记录退回未标记状态，回读拿到截断前缀而不是包裹对象——**可逆，但会丢标记**。这不是
数据迁移，不需要备份。

口径差异（为什么 `error_text` 与 `terminal` 截法不同）：`error_text` 走 `sanitizeSecretText`，只留尾部
1200 字节——错误文本的价值在末尾的 cause；`terminal` 走 `redactSecretText`，只脱敏不截尾，超过
256 KiB 才截断并写上面两列——终态是给调用方解析的 JSON，截掉开头就不再是合法文档，所以宁可显式
标记也不悄悄删内容。`DEFAULT 0` 只对"标记出现之前写入的行"成立，新行一定带真实值。

守卫（`execution_records_mysql_upgrade_test.go`，需要 `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT`）：
`TestExecutionRecordColumnsUpgradeOnMySQL` 把新装的表 `DROP COLUMN` 回旧 schema、插一条 legacy 行、
重启 store，断言两列出现、老行 0/0、`registry.restore` 回读的 `result` 与库里 terminal 的 result
逐字节相同，并直接再调一次补列函数断言 `added` 为空（幂等）。

反向实验（原文）：把"已存在则 continue"改成空分支（恒 `ADD COLUMN`）→

```
execution_records_mysql_upgrade_test.go:17: open MySQL gateway store: mysql_execution_records_upgrade_failed: execution_records.terminal_truncated: Error 1060 (42S21): Duplicate column name 'terminal_truncated'
--- FAIL: TestExecutionRecordColumnsUpgradeOnMySQL (0.57s)
```

未覆盖：补列只管 `execution_records` 一张表的缺失列；若某库该表由更早的手写 DDL 建成、列类型与
`gateway_mysql_schema.sql` 不一致（例如 `status` 是 `ENUM`），运行期不会纠正类型——那是
`installGatewayMySQLSchema` 的职责。生产库未验证，证据来自本地 mysql:8.4.13。

## D17 验收结论（2026-11-24）

D17 定的五条判据全部在同一个构建上取证通过，MySQL 存储后端自此可以对外宣称（用户可见变更见 CHANGELOG 的 [Unreleased]，随下个版本发布）。

| 判据 | 结论 | 证据 |
|---|---|---|
| 1. 启动种子与运行期默认值 | 通过 | 全新库上 serve 无 panic 起服务；gateway_schema_migrations 基线行与 edge_route_revision 由 gatewayStoreBootstrapRows() 写入；TestGatewayBootstrapRowsMatchAcrossBackends、TestGatewayStartupSeedsRunThroughOnePath 双库相等（真实 MySQL，0.78s/0.06s） |
| 2. 保留策略 | 通过 | 真进程日志 [gateway] monitor retention deferred: last scan 4m33s ago, throttle 15m0s (artifacts=30d metrics=90d before the next scan) 可见；TestMonitorRetentionMetricPruneParityAcrossDialects 在 mysql:8.4 真跑（两方言删同一批行） |
| 3. 真实巡检 | 通过 | 真 Gateway（MySQL，42490）+ 真 Agent 经 WebSocket 连回，POST /v1/monitor/runs 带 skills=[inspect-vm-host] → run_48f5da3b4bbc77be：5 项 checks（cpu_memory pass / disk critical / disk_io warning / processes warning / auth_baseline pass）、artifact_format=markdown+json、artifact_state=present、产物落 /tmp/e2e58/data2/artifacts/2026-11-24/run_48f5da3b4bbc77be/item_2/{artifact.md,artifact.json}、GET /v1/monitor/metrics?skill=inspect-vm-host&target=c9/c9a&range=24h 返 9 条序列（cpu_percent 38.79） |
| 4. 凭据全生命周期 | 通过 | TestGatewayMySQLCredentialLifecycleE2E 在真实 MySQL 上 PASS（3.08s）：create → put → 2 grant → 每条 verify → promote → 撤销一个 grant → revoke，逐步断言行数与状态；canary 不回显 |
| 5. 资源同步 manifest 推进 | 通过 | 同一真进程 GET /v1/assets/target-manifest?cluster=c9&instance=c9a 连查三次：revision 1 / applied_revision 1 / online true / supported true / counts {"container":1}，网关日志无 sync 失败 |

第 5 条此前一直卡在 Error 1267 Illegal mix of collations：JSON_TABLE 派生列取会话排序规则，而 DSN 未钉排序规则时驱动协商的是 utf8mb4_general_ci。修法与守卫见上面的「会话排序规则」一节（d72af4e2）。

仍未覆盖（如实记录，不当成已完成）：

- 生产割接仍未做，但既有 SQLite 活库到 MySQL 的割接已由 `store mysql-copy` 命令与 TestGatewayMySQLCutoverRehearsal 演练：命令从只读快照搬运，`--dry-run` 评审、`--confirm-database` 校库名、单事务失败整体回滚；INT-50 起自引用外键由命令自动 NULL+回填（约束保留、真环点名行），`artifacts/` 与 `skills/` 数据目录随库搬运并逐文件比对，缺文件整笔回滚。演练用例用真 store 播种用户/grant/审计/巡检 run+artifact/技能/凭据后经真 copier 迁入真 MySQL，逐表逐列比值并断言迁移基线仅一行、canary 不外泄；生产割接前仍须抓活库快照、在空库重跑 dry-run，并确认生产数据量下的耗时与锁窗口（仅按 70 表/22 行规模验证过）。INT-53 起全新/未迁移的 31 表 SQLite 快照也可直接割接：目标端多出的原生 schema 表只在其为空时逐表列为 `native_schema_only` 并跳过，有行即 `mysql_copy_destination_stale` 拒绝并点名表与行数；dry-run 与正式运行共用同一判定和退出码，不再出现「评审通过、正式运行报 schema mismatch」。
- 巡检的 inspect-vm-host 在本机是宿主 python3 跑的；容器内路径、k8s.metrics 需要 metrics-server 的降级分支等由 Skill 包自身的 verify.sh 与 CI 覆盖，不是 MySQL 侧结论。

## INT-108：生产实库预检暴露的割接阻塞清零（2026-11-27）

对真实线上 SQLite 库（75 表、116.4 万+ 行、2.8 GB）做只读逐列对照 `gateway_mysql_schema.sql` 后发现 4 类实库必现阻塞与多处 MySQL 5.7/dry-run 边界，统一在本轮清零：

1. **列宽扩容与运行期幂等补宽**：线上 `audit_events.session` 最长 104 字符（2,582 行超 64）、`audit_events.digest` 为 71 字符 `sha256:<64 hex>`（1,008 行超 64）、`inventory_import_attempts.receipt` 达 45,753 B（逼近 `TEXT` 64 KiB 上限）。DDL 将 `audit_events.{session,digest,correlation_id}`、`detached_tasks.session`、`release_tickets.plan_digest`、`inspection_receipts.digest` 扩至 `VARCHAR(191)`，`inventory_import_attempts.receipt` 扩至 `MEDIUMTEXT`，并在 `gatewayMySQLPrepareRuntimeSchema` 增加幂等的 `gatewayMySQLWidenMigrationColumns` 升级既有库。
2. **源库外键预检与空库 dry-run 一致性**：`gatewayCopyCheckSourceForeignKeys` 明确仅豁免 MySQL DDL 已刻意移除的 `credentials(owner_id) -> users(id)`（限 `scope='platform' AND owner_id=''`），对所有真实违约（含损坏 `created_by`、个人凭据失效 `owner_id`、历史 `tokens.user_id=''`）统一返回退出码 2 并点名表名；空库 `--dry-run`、已有 schema `--dry-run` 与正式搬运共用同一函数；SQLite `migrate()` 补齐幂等 `UPDATE tokens SET user_id = NULL WHERE kind = 'agent' AND user_id = ''`。
3. **多行批量搬运、排序规则前置守卫与路径改写安全化**：`gateway_mysql_copy.go` 改为每批上限 500 行 / 60,000 参数 / 2 MiB 的多行 `INSERT`（消除百万行级百万次串行往返，同时兼容 MySQL 5.7 默认 4 MiB `max_allowed_packet`）；`gatewayMySQLCopyOpenDestination` 增加 `gatewayMySQLAssertSessionCollation` 前置校验；`gatewayCopyVerifyArtifactRows`/`gatewayCopyVerifySkillRows` 先读尽并关闭游标再执行 `UPDATE`（修复 MySQL 单事务连接并发读写报错），并与 `gatewayMySQLCopyPreviewDataDirs` 共享同一路径归属与产物摘要规则。
4. **`store audit-scrub` MySQL 5.7 兼容**：将 SQL 预过滤 `REGEXP` 从 `(?i)[a-z]...`（MySQL 5.7 报 `ERROR 1139`）改为 POSIX ERE/ICU 双兼容的 `[A-Za-z][A-Za-z0-9+.-]*://[^[:space:]"<>]+`，并消除运维手册 §7.1 矛盾表述。

- Agent 侧 DOOPS_WORKSPACE_ROOT 默认是 /root/ws（容器内可写）；开发机上不设它会得到 target_manifest_snapshot_write_failed，这是环境路径问题，不是后端缺陷——本轮第一次真机巡检就撞上，设了工作区根后立刻通过。