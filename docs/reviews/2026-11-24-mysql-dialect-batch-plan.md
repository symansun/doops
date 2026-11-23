# MySQL 方言第三批：95 个站点的分类与批次计划

日期：2026-11-24 · 任务：INT-16 · 状态：只出判定与计划，不改生产代码

审计基线：`TestGatewayMySQLRuntimeSQLAudit` 当前钉住 16 类、95 个站点（提交 974a7539 之后）。
本文逐个站点回答三件事：**它在哪、谁调用它、MySQL 存储模式今天能不能跑到它**；然后给出批次归属与反向实验构想。

## 0. 方法（可复核）

- 站点清单不是凭类别估出来的：在 `git archive 974a7539` 的**临时导出树**里加一个一次性 dump 测试，
  直接调用生产扫描器 `gatewayMySQLRuntimeSQLLiterals` + `gatewayMySQLRuntimeIssues`，
  打印 `code / file:line / 声明名 / SQL 原文`，得到 95 行，与 golden 的 16 类计数逐类相等（29/13/12/8/8/5/4/4/3/2/2/1/1/1/1/1）。
  该 dump 测试只存在于 /tmp，仓库内未新增任何 `.go`。
- 调用方用 `git grep -n` 追到声明、再追到 handler / ticker / migrate 调用点；
  路由名取自 `backend/agent/internal/server/tunnel_hub.go:137-181` 的注册表。
- 「今天能不能跑到」的判据来自存储装配代码，不来自文件名（见 §1）。

## 1. 前提：MySQL 存储模式没有接线

`backend/agent/internal/server/gateway_store_mysql.go:18-19` 的注释就是结论：

> `gatewayMySQLConfig is used by offline migration tooling. It does not select an alternate store for the running Gateway.`

佐证：

- `OpenGatewayStore`（`gateway_store.go:32`）只打开 SQLite，`store.migrate()`（`gateway_store.go:70`）是唯一的建库入口；
  全仓库对 `gateway_store_mysql` / `gatewayMySQLStore` 的引用只有测试的扫描器跳过清单一处，没有任何运行时选择逻辑。
- 因此今天 95 个站点**全部只可能在 SQLite 上执行**。下文「运行期必改」的准确含义是
  **「MySQL 存储模式一旦接线，这些站点位于每次请求/每次定时的热路径上，必须先改」**，
  不是「今天线上正在出错」。这一点决定了优先级：先改语义会错的，不先改只是语法不兼容的。

## 2. 分类判据

| 分类 | 判据（同时满足） | 后果 |
|---|---|---|
| **运行期必改** | 被 HTTP handler 或定时 ticker 在每次请求/每轮循环里执行；MySQL 语法或语义不同 | 接线前必须改完，否则请求 500 或写入静默错 |
| **一次性迁移（数据搬迁）** | 只在 `migrate()` 内执行一次，改写既有行 | 改之前必须先做回读对照，否则历史口径改变 |
| **诊断路径** | 只为验证索引/查询计划而执行（`EXPLAIN QUERY PLAN`、`PRAGMA index_xinfo`） | 先决定审计是否计入，再决定用 `information_schema` 重写还是标注为 SQLite-only |
| **schema DDL（不翻译）** | 只在 `migrate()`/`migrateEdge()`/`migrateGrantActions()` 里建表建索引 | 已由冻结的 `gateway_mysql_schema.sql` 原生覆盖；运行期不翻译 |

第四类是对任务书三分类的补充，理由：把 `CREATE INDEX`/`AUTOINCREMENT` 归进「一次性迁移」会诱导下一批去改一次性 DDL 的 SQL 文本，
而 MySQL 侧的表结构权威是 `gateway_mysql_schema.sql`（parity 测试 + `TestGatewayMySQLSchemaCoversRuntimeDDL` 双重守卫），改迁移 SQL 收益为负。

## 3. 统计表

| 分类 | 站点数 | 涉及类别 |
|---|---|---|
| 运行期必改 | **32** | json_each 5、unixepoch 4、numbered_parameter 4、reserved use（运行期）8、julianday（运行期）2、json_aggregate 2、sqlite_introspection（运行期守卫）3、update_or_ignore 1、null_safe_parameter 1、aggregate_filter 1、materialized_cte 1 |
| 一次性迁移（数据搬迁） | **1** | julianday（`gateway_store_schema.go:494` 的 audit_events 毫秒回填） |
| 诊断路径 | **14** | sqlite_explain_query_plan 13、sqlite_introspection（`audit_indexes.go:45` 的 `PRAGMA index_xinfo`）1 |
| schema DDL（不翻译） | **44** | create_index_if_not_exists 29、sqlite_autoincrement 8、drop_index_if_exists 2、partial_index 1、reserved use（DDL）4 |
| 迁移期 schema 探查（一次性） | **4** | sqlite_introspection：`gateway_store_schema.go:11,12`、`gateway_store.go:156`、`credential_store_migration.go:164` |
| 合计 | **95** | 16 类 |

## 4. 批次方案

### 批次 3A：运行期时间函数（6 站点）— 先做，因为授权会错

站点：`cluster_inventory.go:206`（`inventoryGrants` 的 `julianday(t.expires_at)>julianday('now')`）、
`machine_diagnostic.go:185`、`asset_inspection_read.go:47/49/77/121`（`unixepoch()`）。

- 改动面：不动 `mysql_runtime_dialect.go`（这是 SQL 文本级替换，不是结构级 upsert），只在调用点按方言选择表达式，
  或在方言层加 `gatewayTimeExpr(kind)` 常量渲染器并配单测。
- 验收：`DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=<port> env -u GOROOT go test ./internal/server/ -run 'GatewayMySQL' -count=1` 绿；
  另加一条真机断言：把 `tokens.expires_at` 写成 1 小时前，MySQL 上同一 `inventoryGrants` 必须判为过期。
- 反向实验：把 `julianday(t.expires_at)` 还原为 SQLite 字面量 → `TestGatewayMySQLRuntimeSQLAudit` 报
  `julianday findings at cluster_inventory.go:206, want 1`（回填站点保留）；把 `unixepoch()` 还原 → `unixepoch findings at asset_inspection_read.go:47, want 0`。
- 风险：`expires_at` 与 `verified_at` 都是 TEXT ISO8601，MySQL 侧必须 `STR_TO_DATE` 或先有毫秒整数列；
  时区按 UTC 解释（SQLite `julianday` 对无时区字符串按 UTC）。**先做 token 过期一条**，它决定授权正确性。

### 批次 3B：JSON 展开与聚合（7 站点）

站点：`agent_resource_execution.go:127`、`agent_resource_store.go:176`、`asset_topology_runtime.go:222/242`、`cluster_inventory_store.go:85`（`json_each`）；
`machine_inventory_store.go:108`、`machine_login_vault.go:19`（`json_group_array`/`json_object`）。

- MySQL 映射：`json_each(?,'$.x')` → `JSON_TABLE(?,'$.x' COLUMNS(...))`；`json_group_array(x ORDER BY y)` → `JSON_ARRAYAGG` 无 `ORDER BY`，
  必须改成「先排序的子查询再聚合」，否则数组顺序不稳定（SQLite 的 `ORDER BY` 在聚合内，MySQL 没有等价语法）。
- 改动面：需要方言层新增 JSON 片段渲染器（`json_each` 的别名 `je.value` 与 `JSON_TABLE` 的列名不同），批内第一步先定渲染器。
- 验收：真机四站点「有数据/空数组/NULL」三种输入的返回值逐字节相等。
- 反向实验：把 `json_group_array` 的 `ORDER BY` 去掉 → 真机聚合顺序断言红；把 `json_each` 还原 → golden 报 `json_each findings ... want 0`。
- 风险：`declaredAgentResources` 里 `json_each` 与 `numbered_parameter`、`reserved use` 同语句出现，3B 会与 3C/3D 交叉，建议先做 3C 再做 3B。

### 批次 3C：参数与控制流（8 站点）— 语义最危险，最需要真机

站点：`agent_asset_scope.go:12/129/186`、`agent_resource_store.go:302`（`?N` 编号参数）；
`asset_catalog.go:230`（`UPDATE OR IGNORE`）、`asset_monitor_store.go:29`（`value IS ?` 空安全比较）、
`asset_monitor_summary.go:78`（`FILTER (WHERE ...)`）、`audit_purge.go:52`（`WITH ... AS MATERIALIZED`）。

- `?3`/`?4` → MySQL 只认 `?`，必须重排调用方参数**顺序**，这是本批唯一会静默错位的改动类型：SQL 语法合法、结果错行。
  因此每个站点都要一条「按具体行值断言」的真机测试，而不是「不报错即通过」。
- `UPDATE OR IGNORE` → INT-13/INT-15 的 `gatewayUpsert{doNothing:true}`；`IS ?` → `<=>`；
  `FILTER (WHERE c)` → `SUM(CASE WHEN c THEN 1 END)`（注意 NULL 行不计数，与 `FILTER` 一致）；
  `AS MATERIALIZED` → MySQL 无此关键字，需确认去掉后 CTE 被 merge 两次求值是否改变 `DELETE ... WHERE id IN (WITH ...)` 的结果。
- 反向实验：把 `?3` 换成 `?` 但参数顺序不动 → 真机 workspace 断言红（`linked OR assigned` 结果集多一行/少一行）；
  把 `IS ?` 换成 `= ?` → NULL 场景红；把 `FILTER` 换成无条件 `COUNT(*)` → 计数偏大。
- 风险：`audit_purge.go:52` 的 CTE 删除是审计保留策略，改错会删多或删少，必须先有「保留窗口内一行不删」的断言。

### 批次 3D：标识符与存在性守卫（11 站点）— 最机械

站点：`agent_resource_store.go:302`、`credential_materialization_store.go:78/89/262/271/289`、
`credential_store_verifications.go:59/66`（`use` 是 MySQL 保留字，需反引号或改名）；
`agent_resource_execution.go:114`、`agent_resource_store.go:74`、`asset_coverage_register.go:34`（`sqlite_schema`/`PRAGMA` 存在性检查 → `information_schema.tables`）。

- 改动面：`mysql_runtime_dialect.go` 已有一条 `reserved_use` 规则，但渲染层没有「给标识符加引号」的能力，需新增 `gatewayMySQLIdentifier` 的正向渲染（不是只报错）。
- 验收：真机 `SELECT ... use ...` 成功，且三条 credential materialization 语句回读字段相等。
- 反向实验：去掉反引号 → MySQL 报 1064；把 `information_schema.tables` 查询写成返回 true 的常量 → 表缺失场景的 fail-closed 测试红。
- 风险：`sqlite_schema` 守卫是「表不存在就拒绝服务」，改写后必须保持**缺表 → false**，不能变成「查不到就假定存在」。

### 批次 3E：不建议现在改

| 站点/组 | 数量 | 理由 |
|---|---|---|
| create_index_if_not_exists、drop_index_if_exists、partial_index、sqlite_autoincrement、reserved use（DDL） | 44 | MySQL 表结构权威是 `gateway_mysql_schema.sql`（61 表，parity + 运行时 DDL 覆盖守卫）；改迁移 DDL 的 SQLite 文本不会改变 MySQL 现有表，收益为负 |
| sqlite_explain_query_plan + `PRAGMA index_xinfo` | 14 | 目的就是让 SQLite 规划器选索引；MySQL 侧等价物是 `information_schema.statistics` 与 `EXPLAIN FORMAT=JSON`，属于「重写诊断实现」而不是「翻译 SQL」 |
| julianday 回填（`gateway_store_schema.go:494`） | 1 | 一次性把 audit_events 从 ISO 文本转毫秒；今天已新写毫秒列，改它只影响历史行，需先有回读对照脚本 |
| 迁移期 schema 探查（`PRAGMA table_info`、`sqlite_schema` 计数） | 4 | 只在首次打开库时执行；MySQL 侧用原生 DDL 建库后这些分支不会命中，改了也无人执行 |

## 5. 小项逐个判定

| 小项 | 数量 | 判定 | 归属 |
|---|---|---|---|
| `reserved_use` | 12 | **可并入 3D**：8 站点运行期、4 站点 DDL。运行期 8 处只是引号问题，无语义歧义；DDL 4 处不建议改 | 3D |
| `numbered_parameter` | 4 | **不能并入 3D**，必须单独成批（3C）：`?N` → `?` 会改变参数绑定顺序，需要逐站点重排与真机行值断言 | 3C |
| `update_or_ignore` | 1 | **可并入 3C**：`asset_catalog.go:230` 与 INT-13 的 upsert 表达同构，冲突键由表主键决定 | 3C |
| `aggregate_filter` | 1 | **可并入 3C**：`asset_monitor_summary.go:78` 的 `FILTER` 改条件聚合即可，NULL 语义一致 | 3C |
| `null_safe_parameter` | 1 | **可并入 3C**：`asset_monitor_store.go:29` 的 `IS ?` → `<=>`，语义完全等价 | 3C |
| `materialized_cte` | 1 | **不能并入 3C，需先解决语义**：`MATERIALIZED` 在 MySQL 8.0 无对应关键字，去掉后 MySQL 可能把 CTE 合并成两次求值，`DELETE ... WHERE id IN (CTE)` 的可见性语义要单独论证 | 3C 内单列子项 |

## 6. 结论与建议顺序

1. 今天没有一条需要紧急修：MySQL 存储模式未接线，95 个站点只在 SQLite 上跑。
2. 真接线前按 **3A（时间/授权）→ 3D（引号/守卫）→ 3C（参数重排）→ 3B（JSON）** 改，风险递增、依赖递增。
3. DDL 44 站点与诊断 14 站点建议永久不翻译，只在计划文档登记「MySQL 侧由冻结 schema / information_schema 承担」。
4. 每批验收命令统一为：`env -u GOROOT go build ./...`、`go vet ./internal/server/`、
   `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=<port> go test ./internal/server/ -run 'GatewayMySQL' -count=1`，
   外加该批自己的真机行值断言与 golden 计数下调。

## 附录：95 个站点逐条清单

（表名由 SQL 首个 `CREATE TABLE/INSERT INTO/UPDATE/FROM/JOIN/ON` 目标推断；声明列为包级常量名或所在函数。）

### create_index_if_not_exists (29)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `gateway_store_edge.go:35` | `migrateEdge` | `edge_routes` | `CREATE UNIQUE INDEX IF NOT EXISTS idx_edge_routes_hostname ON edge_routes(hostname)` |
| 2 | `gateway_store_grants.go:149` | `migrateGrantActions` | `grants` | `CREATE INDEX IF NOT EXISTS idx_grants_user ON grants(user_id)` |
| 3 | `gateway_store_schema.go:35` | `migrate` | `tokens` | `CREATE INDEX IF NOT EXISTS idx_tokens_kind ON tokens(kind)` |
| 4 | `gateway_store_schema.go:45` | `migrate` | `grants` | `CREATE INDEX IF NOT EXISTS idx_grants_user ON grants(user_id)` |
| 5 | `gateway_store_schema.go:73` | `migrate` | `agent_connection_events` | `CREATE INDEX IF NOT EXISTS idx_agent_conn_events_time ON agent_connection_events(occurred_` |
| 6 | `gateway_store_schema.go:74` | `migrate` | `agent_connection_events` | `CREATE INDEX IF NOT EXISTS idx_agent_conn_events_target ON agent_connection_events(cluster` |
| 7 | `gateway_store_schema.go:100` | `migrate` | `detached_tasks` | `CREATE INDEX IF NOT EXISTS idx_detached_tasks_user ON detached_tasks(user_id, audit_id DES` |
| 8 | `gateway_store_schema.go:140` | `migrate` | `proxy_routes` | `CREATE INDEX IF NOT EXISTS idx_proxy_routes_target ON proxy_routes(cluster, instance, enab` |
| 9 | `gateway_store_schema.go:156` | `migrate` | `credentials` | `CREATE INDEX IF NOT EXISTS idx_credentials_owner ON credentials(owner_id, state, created_a` |
| 10 | `gateway_store_schema.go:157` | `migrate` | `credentials` | `CREATE INDEX IF NOT EXISTS idx_credentials_name ON credentials(name, state)` |
| 11 | `gateway_store_schema.go:171` | `migrate` | `credential_versions` | `CREATE INDEX IF NOT EXISTS idx_credential_versions_resource ON credential_versions(credent` |
| 12 | `gateway_store_schema.go:172` | `migrate` | `credential_versions` | `CREATE UNIQUE INDEX IF NOT EXISTS idx_credential_versions_one_active ON credential_version` |
| 13 | `gateway_store_schema.go:193` | `migrate` | `credential_grants` | `CREATE INDEX IF NOT EXISTS idx_credential_grants_authorize ON credential_grants(grantee_id` |
| 14 | `gateway_store_schema.go:211` | `migrate` | `credential_verifications` | `CREATE INDEX IF NOT EXISTS idx_credential_verifications_resource ON credential_verificatio` |
| 15 | `gateway_store_schema.go:231` | `migrate` | `credential_materializations` | `CREATE INDEX IF NOT EXISTS idx_credential_materializations_resource ON credential_material` |
| 16 | `gateway_store_schema.go:254` | `migrate` | `credential_bundle_items` | `CREATE INDEX IF NOT EXISTS idx_credential_bundle_items_resource ON credential_bundle_items` |
| 17 | `gateway_store_schema.go:275` | `migrate` | `monitor_schedules` | `CREATE INDEX IF NOT EXISTS idx_monitor_schedules_due ON monitor_schedules(enabled, next_ru` |
| 18 | `gateway_store_schema.go:296` | `migrate` | `monitor_runs` | `CREATE INDEX IF NOT EXISTS idx_monitor_runs_started ON monitor_runs(started_at_ms DESC, id` |
| 19 | `gateway_store_schema.go:316` | `migrate` | `monitor_run_items` | `CREATE INDEX IF NOT EXISTS idx_monitor_run_items_run ON monitor_run_items(run_id, id)` |
| 20 | `gateway_store_schema.go:317` | `migrate` | `monitor_run_items` | `CREATE INDEX IF NOT EXISTS idx_monitor_run_items_target ON monitor_run_items(cluster, inst` |
| 21 | `gateway_store_schema.go:354` | `migrate` | `monitor_resource_snapshots` | `CREATE INDEX IF NOT EXISTS idx_monitor_snapshots_latest ON monitor_resource_snapshots(clus` |
| 22 | `gateway_store_schema.go:355` | `migrate` | `monitor_resource_snapshots` | `CREATE INDEX IF NOT EXISTS idx_monitor_snapshots_time ON monitor_resource_snapshots(collec` |
| 23 | `gateway_store_schema.go:375` | `migrate` | `platform_skills` | `CREATE INDEX IF NOT EXISTS idx_platform_skills_scope ON platform_skills(scope, category, e` |
| 24 | `gateway_store_schema.go:400` | `migrate` | `platform_mcp_servers` | `CREATE INDEX IF NOT EXISTS idx_platform_mcp_scope ON platform_mcp_servers(scope, enabled)` |
| 25 | `gateway_store_schema.go:415` | `migrate` | `inspection_metric_points` | `CREATE INDEX IF NOT EXISTS idx_inspection_metric_points_series ON inspection_metric_points` |
| 26 | `gateway_store_schema.go:416` | `migrate` | `inspection_metric_points` | `CREATE INDEX IF NOT EXISTS idx_inspection_metric_points_run ON inspection_metric_points(ru` |
| 27 | `gateway_store_schema.go:417` | `migrate` | `inspection_metric_points` | `CREATE INDEX IF NOT EXISTS idx_inspection_metric_points_target ON inspection_metric_points` |
| 28 | `gateway_store_schema.go:418` | `migrate` | `monitor_run_items` | `CREATE INDEX IF NOT EXISTS idx_monitor_run_items_series ON monitor_run_items(cluster, inst` |
| 29 | `gateway_store_schema.go:499` | `migrate` | `audit_events` | `CREATE INDEX IF NOT EXISTS idx_audit_started_ms ON audit_events(started_at_ms DESC, id DES` |

### drop_index_if_exists (2)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `credential_store_migration.go:34` | `migrateCredentialGrantScopes` | `-` | `DROP INDEX IF EXISTS idx_credential_grants_authorize` |
| 2 | `credential_store_migration.go:100` | `migrateCredentialBundleItems` | `-` | `DROP INDEX IF EXISTS idx_credential_bundle_items_resource` |

### partial_index (1)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `gateway_store_schema.go:172` | `migrate` | `credential_versions` | `CREATE UNIQUE INDEX IF NOT EXISTS idx_credential_versions_one_active ON credential_version` |

### sqlite_autoincrement (8)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `gateway_store_edge.go:22` | `migrateEdge` | `edge_routes` | `CREATE TABLE IF NOT EXISTS edge_routes ( id INTEGER PRIMARY KEY AUTOINCREMENT, hostname TE` |
| 2 | `gateway_store_grants.go:121` | `migrateGrantActions` | `grants_v2` | `CREATE TABLE grants_v2 ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL, clus` |
| 3 | `gateway_store_schema.go:36` | `migrate` | `grants` | `CREATE TABLE IF NOT EXISTS grants ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT` |
| 4 | `gateway_store_schema.go:61` | `migrate` | `agent_connection_events` | `CREATE TABLE IF NOT EXISTS agent_connection_events ( id INTEGER PRIMARY KEY AUTOINCREMENT,` |
| 5 | `gateway_store_schema.go:101` | `migrate` | `audit_events` | `CREATE TABLE IF NOT EXISTS audit_events ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TE` |
| 6 | `gateway_store_schema.go:297` | `migrate` | `monitor_run_items` | `CREATE TABLE IF NOT EXISTS monitor_run_items ( id INTEGER PRIMARY KEY AUTOINCREMENT, run_i` |
| 7 | `gateway_store_schema.go:318` | `migrate` | `monitor_resource_snapshots` | `CREATE TABLE IF NOT EXISTS monitor_resource_snapshots ( id INTEGER PRIMARY KEY AUTOINCREME` |
| 8 | `gateway_store_schema.go:401` | `migrate` | `inspection_metric_points` | `CREATE TABLE IF NOT EXISTS inspection_metric_points ( id INTEGER PRIMARY KEY AUTOINCREMENT` |

### sqlite_explain_query_plan (13)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `audit_indexes.go:29` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? ORDER BY id DESC LIMIT 500` |
| 2 | `audit_indexes.go:30` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND session=? ORDER BY id D` |
| 3 | `audit_indexes.go:31` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? ORDER BY id ` |
| 4 | `audit_indexes.go:32` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE action=? ORDER BY id DESC LIMIT 500` |
| 5 | `audit_indexes.go:33` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE status=? ORDER BY id DESC LIMIT 500` |
| 6 | `audit_indexes.go:34` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND action=? ORDER BY id DE` |
| 7 | `audit_indexes.go:35` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND status=? ORDER BY id DE` |
| 8 | `audit_indexes.go:36` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? AND action=?` |
| 9 | `audit_indexes.go:37` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? AND status=?` |
| 10 | `audit_indexes.go:38` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? AND session=` |
| 11 | `audit_indexes.go:39` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE session=? ORDER BY id DESC LIMIT 500` |
| 12 | `audit_indexes.go:40` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE session=? AND action=? ORDER BY id DE` |
| 13 | `audit_indexes.go:41` | `optimizeAuditIndexes` | `audit_events` | `EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE session=? AND status=? ORDER BY id DE` |

### sqlite_introspection (8)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `agent_resource_execution.go:114` | `loadManifestAudit` | `sqlite_schema` | `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='agent_resource_audi` |
| 2 | `agent_resource_store.go:74` | `agentResourceSyncReady` | `sqlite_schema` | `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN (` |
| 3 | `asset_coverage_register.go:34` | `registerSchema` | `sqlite_schema` | `SELECT count(*)=2 FROM sqlite_schema WHERE type='table' AND name IN ('asset_coverage_regis` |
| 4 | `audit_indexes.go:45` | `optimizeAuditIndexes` | `-` | `PRAGMA index_xinfo('` |
| 5 | `credential_store_migration.go:164` | `tableHasColumn` | `-` | `PRAGMA table_info(` |
| 6 | `gateway_store.go:156` | `ensureColumn` | `-` | `PRAGMA table_info(` |
| 7 | `gateway_store_schema.go:11` | `migrate` | `-` | `PRAGMA busy_timeout=5000` |
| 8 | `gateway_store_schema.go:12` | `migrate` | `-` | `PRAGMA journal_mode=WAL` |

### julianday (3)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `cluster_inventory.go:206` | `inventoryGrants` | `users` | `WITH actor AS ( SELECT u.id, u.is_admin FROM users u JOIN tokens t ON t.user_id=u.id WHERE` |
| 2 | `gateway_store_schema.go:494` | `migrate` | `audit_events` | `UPDATE audit_events SET started_at_ms = CAST(ROUND((julianday(started_at) - 2440587.5) * 8` |
| 3 | `machine_diagnostic.go:185` | `diagnosticDeployments` | `inventory_business_deployments` | `SELECT d.asset_id,s.name,d.namespace,d.application,d.database_name,d.verified_at FROM inve` |

### unixepoch (4)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `asset_inspection_read.go:47` | `inspectState` | `-` | `unixepoch()` |
| 2 | `asset_inspection_read.go:49` | `inspectReason` | `-` | `WHEN s.status='pending' AND s.deadline<=unixepoch() THEN 'collection_deadline_elapsed' ELS` |
| 3 | `asset_inspection_read.go:77` | `inspectionRead` | `-` | `AND p.executor=? AND s.status='pending' AND s.opened<=unixepoch() AND s.deadline>unixepoch` |
| 4 | `asset_inspection_read.go:121` | `inspectionRead` | `actor` | `SELECT p.id AS plan_id,p.name,p.owner,p.executor,p.weekday,p.clock,p.timezone,p.next_due,p` |

### json_each (5)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `agent_resource_execution.go:127` | `loadManifestAudit` | `agent_resource_audit` | `SELECT audit_id,manifest_revision,resource_refs_json FROM agent_resource_audit WHERE audit` |
| 2 | `agent_resource_store.go:176` | `storeObservationEpoch` | `agent_observations` | `UPDATE agent_observations SET misses=MIN(misses+1, ?) WHERE cluster=? AND instance=? AND k` |
| 3 | `asset_topology_runtime.go:222` | `applyRuntime` | `asset_runtime_bindings` | `DELETE FROM asset_runtime_bindings WHERE cluster=? AND instance=? AND namespace=? AND work` |
| 4 | `asset_topology_runtime.go:242` | `applyRuntime` | `inventory_business_deployments` | `SELECT EXISTS(SELECT 1 FROM inventory_business_deployments WHERE cluster=? AND instance=? ` |
| 5 | `cluster_inventory_store.go:85` | `saveInventoryObservation` | `cluster_inventory_namespaces` | `DELETE FROM cluster_inventory_namespaces WHERE cluster=? AND instance=? AND name NOT IN (S` |

### json_aggregate (2)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `machine_inventory_store.go:108` | `machineInventoryColumns` | `inventory_machine_tags` | `m.asset_id,m.name,m.kind,m.address,m.cpu_count,m.memory_gib,m.disk_gib, (SELECT json_group` |
| 2 | `machine_login_vault.go:19` | `machineLoginAccountsJSON` | `inventory_machine_login_accounts` | `(SELECT json_group_array(json_object('username', a.username, 'credential_id', a.credential` |

### numbered_parameter (4)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `agent_asset_scope.go:12` | `workspaceCatalog` | `inventory_machines` | `WITH catalog(kind,id,name,address) AS ( SELECT 'machine',asset_id,name,address FROM invent` |
| 2 | `agent_asset_scope.go:129` | `readWorkspaceAssets` | `assets` | `SELECT kind,id,name,address,linked,assigned FROM assets WHERE linked OR assigned OR ?3 ORD` |
| 3 | `agent_asset_scope.go:186` | `applyWorkspaceChange` | `assets` | `SELECT EXISTS(SELECT 1 FROM assets WHERE kind=?3 AND id=?4)` |
| 4 | `agent_resource_store.go:302` | `declaredAgentResources` | `inventory_machines` | `SELECT kind,id,name,key,identity,credential_type,use FROM ( SELECT 'machine' AS kind, m.as` |

### reserved_use (12)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `agent_resource_store.go:302` | `declaredAgentResources` | `inventory_machines` | `SELECT kind,id,name,key,identity,credential_type,use FROM ( SELECT 'machine' AS kind, m.as` |
| 2 | `credential_materialization_store.go:78` | `RecordCredentialMaterializationIntent` | `credential_materializations` | `SELECT status, error_category, request_json, evidence_json FROM credential_materialization` |
| 3 | `credential_materialization_store.go:89` | `RecordCredentialMaterializationIntent` | `credential_materializations` | `INSERT INTO credential_materializations (id, credential_id, version_id, grant_id, use, con` |
| 4 | `credential_materialization_store.go:262` | `MarkCredentialMaterializationCleaned` | `credential_materializations` | `UPDATE credential_materializations SET status = 'cleaned', error_category = '', updated_at` |
| 5 | `credential_materialization_store.go:271` | `MarkCredentialMaterializationContextCleaned` | `credential_materializations` | `UPDATE credential_materializations SET status = 'cleaned', error_category = '', updated_at` |
| 6 | `credential_materialization_store.go:289` | `CredentialMaterializations` | `credential_materializations` | `SELECT id, credential_id, version_id, grant_id, use, context_key, request_json, evidence_j` |
| 7 | `credential_store_migration.go:126` | `migrateCredentialVerifications` | `credential_verifications_v2` | `CREATE TABLE credential_verifications_v2 ( id TEXT PRIMARY KEY, credential_id TEXT NOT NUL` |
| 8 | `credential_store_migration.go:144` | `migrateCredentialVerifications` | `credential_verifications_v2` | `INSERT INTO credential_verifications_v2 (id, credential_id, version_id, grant_id, use, con` |
| 9 | `credential_store_verifications.go:59` | `recordCredentialVerificationWith` | `credential_verifications` | `INSERT INTO credential_verifications (id, credential_id, version_id, grant_id, use, contex` |
| 10 | `credential_store_verifications.go:66` | `CredentialVerifications` | `credential_verifications` | `SELECT id, credential_id, version_id, grant_id, use, request_json, evidence_json, status, ` |
| 11 | `gateway_store_schema.go:195` | `migrate` | `credential_verifications` | `CREATE TABLE IF NOT EXISTS credential_verifications ( id TEXT PRIMARY KEY, credential_id T` |
| 12 | `gateway_store_schema.go:213` | `migrate` | `credential_materializations` | `CREATE TABLE IF NOT EXISTS credential_materializations ( id TEXT PRIMARY KEY, credential_i` |

### update_or_ignore (1)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `asset_catalog.go:230` | `HandleResourceCatalog` | `OR` | `UPDATE OR IGNORE inventory_resource_catalog SET cluster=?,instance=?,name=?,environment=?,` |

### null_safe_parameter (1)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `asset_monitor_store.go:29` | `storeMonitorPoint` | `asset_monitor_samples` | `SELECT EXISTS(SELECT 1 FROM asset_monitor_samples WHERE series_id=? AND at_ms=? AND status` |

### aggregate_filter (1)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `asset_monitor_summary.go:78` | `readMonitorSummary` | `sources` | `SELECT count(*) FILTER(WHERE state='open'),count(*) FILTER(WHERE state='missing'),(SELECT ` |

### materialized_cte (1)

| # | 站点 | 声明 | 表 | SQL 摘要 |
|---|---|---|---|---|
| 1 | `audit_purge.go:52` | `deleteCompletedAuditSQL` | `audit_events` | `WITH terminal AS MATERIALIZED ( SELECT cluster, instance, correlation_id, json_extract(CAS` |
