# MySQL 存储接线：判定、最小切片与迁移工具计划（INT-22）

- 日期：2026-11-24
- 范围：只判定与计划，不改运行代码；不触碰 `backend/agent/**/*.go`、`gateway_mysql_schema.sql`
- 基线：main `874ab8ef`；审计 golden 64 条；MySQL 8.4.13（容器 `int-gm21`，TCP 13316）
- 一句话结论：今天 64 条方言渲染一条都没有在 MySQL 上跑过；接线的第一道墙不是它们，而是 schema bootstrap（`migrate()` 的 PRAGMA/DDL 在 MySQL 上直接 1064）。最小可运行切片约 170 行、集中在 5 个文件，业务 store 文件（audit/grants/users）无需改动；audit purge 的两步改写是切片之后的独立批次。

## A. 接线卡在哪

| 环节 | 结论 | 证据 |
|---|---|---|
| 存储选择入口 | **阻塞（结构性）** | `backend/agent/internal/server/gateway_store.go:32` 的 `OpenGatewayStore` 只开 SQLite；`gateway_store_mysql.go:17-19` 自述「used by offline migration tooling. It does not select an alternate store」；`openGatewayMySQL`（`gateway_store_mysql.go:75`）除测试外无调用方（`grep -rn "openGatewayMySQL(" backend/agent --include=*.go` 仅 `gateway_store_mysql.go:75` 定义与测试） |
| 方言开关 | **阻塞（并发安全）** | `mysql_runtime_dialect.go:183` 是包级 `var gatewayDialectStore = gatewayDialectSQLite`，所有默认渲染（:218、:231、:246）都读它；接线必须设置它，但进程内多 store（测试常用）会互相串 |
| schema bootstrap | **硬阻塞** | `gateway_store_schema.go:9` 的 `migrate()` 执行 SQLite DDL：:12 `PRAGMA busy_timeout=5000`、:13 `PRAGMA journal_mode=WAL`，之后 29 条 `CREATE TABLE IF NOT EXISTS`（24 条在本文件）含 `AUTOINCREMENT`、`TEXT PRIMARY KEY`、`STRICT` 等。真机逐条探测见 E 节，首个失败在第二条语句 |
| 权威 schema 与运行时 | **阻塞（无接线、无升级路径）** | `gateway_mysql_schema.sql` 是 69 个 `CREATE TABLE` 的权威转换（`grep -c "CREATE TABLE" gateway_mysql_schema.sql` = 69；INT-04 记载 126 条 CREATE 含索引）；`gateway_mysql_schema.go:26-27` embed 该文件，`installGatewayMySQLSchema`（:118）能安装，但调用方只有测试；它 **fail-closed 于非空 schema**（:128 `existing != 0`），且 :184 注释明确「copier 要求表集合与 SQLite 源完全一致，所以不允许 installer 拥有 metadata 表」——即今天没有「版本记录 + 增量升级」的落点 |
| 事务与并发 | **阻塞（语义）** | SQLite：`gateway_store.go:74` `SetMaxOpenConns(1)` 单写者 + `busy_timeout`；`BEGIN IMMEDIATE` 全仓 0 处（`grep -rn "BEGIN IMMEDIATE" backend/agent` 空），事务是默认 DEFERRED。MySQL：`openGatewayMySQL` 设 8 连接（`gateway_store_mysql.go:88`）、不设隔离级别，服务端默认 REPEATABLE-READ、`innodb_lock_wait_timeout=50`（真机 `SELECT @@transaction_isolation, @@innodb_lock_wait_timeout, @@version` → `REPEATABLE-READ 50 8.4.13`）。全仓无 `1213`/`1205`/deadlock 重试（`grep -rn "1213\|1205\|deadlock" backend/agent --include=*.go` 空）。离线 copier 反而显式用 `LevelSerializable`（`gateway_mysql_copy.go:51,67`），运行期没有对应决策 |
| RETURNING / LastInsertId | **非阻塞，但需契约** | 全仓 0 处 SQL `RETURNING`；`gateway_store_write.go:8` 注释说明用 `RowsAffected` 替代。`LastInsertId` 4 处：`gateway_store_audit.go:106`、`gateway_store_write.go:35`、`gateway_store_edge.go:193`、`machine_inventory_store.go:313`；MySQL 侧 id 是 `BIGINT PRIMARY KEY AUTO_INCREMENT`（`gateway_mysql_schema.sql:336` audit_events、:831 grants），纯 INSERT 路径语义一致；但 `ON DUPLICATE KEY UPDATE` 命中时 `LastInsertId` 不返回既有行 id，含 upsert 的路径不能依赖它 |
| `WHERE 1` 恒真补丁 | **非阻塞** | `credential_store_migration.go:89-94`：`INSERT INTO credential_bundle_items_v2 ... SELECT ... WHERE 1` + `ON CONFLICT DO NOTHING` 经 `gatewayUpsertSQL` 渲染；MySQL 接受 `WHERE 1`，且该语句所在的一次性迁移（`CREATE TABLE ... RENAME TO`）在 MySQL 上永不执行 |
| `julianday` 回填 | **非阻塞（MySQL 不执行）** | `gateway_store_schema.go:469-471` 的 `UPDATE audit_events SET started_at_ms = ... julianday(started_at) ...` 在 `migrate()` 内；MySQL 分支跳过 `migrate()` 后不会执行。真机直接执行该函数报 `ERROR 1305 FUNCTION mysql.julianday does not exist`。若将来要跑，INT-17 的 `gatewayEpochSecondsFor` 已有成对渲染可复用 |

小结：A 里 5 条阻塞中有 3 条是「没有入口」（选择入口、方言开关、bootstrap），2 条是「语义未决策」（隔离/重试、升级路径）。64 条渲染不是阻塞项，它们是接线成功后才开始被执行的负载。

## B. 最小可运行切片

目标链路：gateway 的 **audit + grants + 最小登录（users）**。选它的理由：这三块是「请求进来就写审计、授权判定、登录」的地基，表少（users、tokens、grants、audit_events）、无后台循环、且三个 store 文件已经零 SQLite 构造（`grep -n "strftime\|julianday\|ON CONFLICT\|INSERT OR\|INDEXED BY\|AUTOINCREMENT" gateway_store_audit.go gateway_store_grants.go gateway_store_users.go` 无输出），改动集中在打开/引导层。

| 文件 | 现状 | 切片改动 | 估计 |
|---|---|---|---|
| `gateway_store.go` | `OpenGatewayStore` 只开 SQLite（:32），store 结构体无方言字段 | 新增 `OpenGatewayStoreConfig`（或环境变量分支），默认仍是 SQLite；store 增加 `dialect` 与 MySQL 分支的 `rdb=db` | ~50 行 |
| `gateway_store_mysql.go` | `openGatewayMySQL` 已可用（:75），连接错误已分类（:100 起） | 新增 `newGatewayMySQLStore(ctx, cfg)` 复用 open + 构造 store | ~30 行 |
| 新文件 `gateway_store_mysql_bootstrap.go` | 无 | 调 `installGatewayMySQLSchema`（空库）或「已安装则校验版本」，并把版本落在 **不被 copier 视为业务表** 的位置（见 C 的决策） | ~70 行 |
| `gateway_store_schema.go` | `migrate()` 无条件跑 SQLite DDL（:9） | 首行 `if dialect == mysql { return nil }`（或拆 `migrateSQLite` / `migrateMySQL`） | ~8 行 |
| `mysql_runtime_dialect.go` | `gatewayDialectStore` 包级 var（:183） | 二选一：store 字段 + 方法化（调用点多，约 100+），或进程级 `sync.Once` + 第二次不同方言即报错（切片期推荐，~15 行） | ~15 行 |

合计约 170 行，且 `gateway_store_audit.go`/`gateway_store_grants.go`/`gateway_store_users.go` **0 行改动**——它们的语句要么是普通 SQL，要么已经过 `gatewayUpsertSQL` 等成对渲染。

切片内必须一起做（否则跑不起来）：入口、方言设置、bootstrap、`migrate()` 跳过。可以后置：audit purge 两步改写（D）、`deleteAuditBatchSQL`、julianday 回填清理、1213/1205 重试。

切片验收门（建议新增一个测试文件 `gateway_store_mysql_slice_test.go`，只在 `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT` 设置时跑）：
1. 空库 → 打开 store → 建用户/校验口令（`CreateUser`/`VerifyUserPassword`）；
2. `StartAudit` → `FinishAudit` → `ListAuditFiltered` 回读同一行；
3. `GrantUser` → `UserCanUseTarget` 为 true、无授权目标为 false；
4. 同一组操作在内存 SQLite 上跑一遍，行集/布尔值逐项相等；
5. 不设置环境变量时，默认路径仍只开 SQLite（回归）。

不做：不切换默认存储、不动 `gateway_mysql_copy.go`、不改 64 条审计的渲染文本、不接 monitor/upgrade。

## C. 迁移工具：扩成 bootstrap，而不是另写

- 已有能力足够复用：`gateway_mysql_schema.go:26-27` embed 权威 DDL；`:118` `installGatewayMySQLSchema` 负责分割、执行、用 `information_schema` 校验对象清单，并在 :136 设定 `sql_mode='STRICT_ALL_TABLES,...'`；`gateway_mysql_copy.go:51,67` 用 Serializable 事务按表复制，未知源表 fail-closed。**不要**在网关启动时拷贝数据：数据搬迁仍是离线工具（`backend/scripts/gateway-sqlite-to-mysql.py` + copier）的职责。
- 缺口 1（版本记录）：installer 只接受空 schema（`gateway_mysql_schema.go:128`），且为了 copier 的表集合相等（:184）不能自建 metadata 表。二选一：**（推荐）** 让 copier 比较时排除一张显式的 `gateway_schema_migrations` 白名单表，运行期用它记录 `gatewayMySQLSchemaVersion`；或把版本写进数据库注释/信息 schema（不改表集合，但升级时难查询）。这件事必须先于切片 3 决定。
- 缺口 2（增量 DDL）：`gatewayMySQLSchemaVersion = "gateway-mysql-schema-1"`（`gateway_mysql_schema.go:32`）是整体版本，没有从 v1 到 v2 的语句清单；接线后 any schema 变更都需要一个只追加的迁移列表，否则只能「空库安装」。
- 缺口 3（已知数据缺口）：INT-04/INT-20 记录 `platform_skills`、`platform_mcp_servers` 在冻结导出与 `gateway_mysql_schema.sql` 的 58/69 表中缺失，parity 只对冻结导出所以看不见；切换演练前必须重抓导出并重新转换，copier 对未知源表 fail-closed 会挡住。
- 运行时 sql_mode 决策：installer/copier 用 `STRICT_ALL_TABLES`；运行期连接目前不设（`openGatewayMySQL` 无 `SET SESSION`），严格模式影响静默截断与 `1062` 之外的行为，需要在切片里显式设定并与 installer 一致。

## D. audit purge 的两步改写

现状（`audit_purge.go`）：一条语句 `WITH terminal AS MATERIALIZED (...) DELETE ... WHERE id IN (SELECT audit_events.id FROM audit_events INDEXED BY idx_audit_started_ms ...)`（:52-73），`INDEXED BY` 由常量拼进文本（:41、:62）；调用点两处：`DeleteAuditBefore`（`gateway_store_audit.go:319`，单语句、无显式事务）与 `drainAuditTx`（`audit_purge.go:274,278`，批量 + 回执同一事务）。

MySQL 三个不兼容点（真机 8.4.13 实测）：
1. `MATERIALIZED` 关键字 → `ERROR 1064 ... near 'MATERIALIZED ...'`；
2. `DELETE ... WHERE id IN (SELECT ... FROM audit_events)` → `ERROR 1093 You can't specify target table 'audit_events' for update in FROM clause`；
3. `INDEXED BY` → `ERROR 1064`。

改写方案（两步，选定）：
- `selectCompletedAuditIDsSQL(dialect)`：保留原 WHERE 结构，把 `terminal` 改成派生表（SQLite 可继续 `MATERIALIZED`，MySQL 用普通派生表），只 `SELECT audit_events.id`，`INDEXED BY` 只在 SQLite 分支拼入；
- `deleteAuditIDsSQL(n)`：`DELETE FROM audit_events WHERE id IN (?,...)` 按 500/1000 分块；
- 批量语句同理：`SELECT id ... ORDER BY started_at_ms, id LIMIT ?` 后再按 id 删。
- 两条语句放进同一事务；MySQL 在 SELECT 末尾加 `FOR UPDATE` 并对候选 id 升序删除，SQLite 走现有 DEFERRED→写锁升级（WAL 下 `SQLITE_BUSY_SNAPSHOT` 由既有 busy_timeout/重试处理）。

可见性等价论证：原语句的价值是「admission 行只有在同一快照里存在更晚的 terminal 行时才被删」。两步版把「选 id」与「删 id」放在同一事务：ids 由一次一致性读选出（SQLite WAL 快照 / MySQL RR 快照 + FOR UPDATE 锁住候选行），terminal 判定在选出瞬间冻结，随后只删这份固定 id 列表——等价于原来「材料化一次、按同一结果删」。并发新增 terminal 只会让更多行受保护（保守，不会误删）；并发把 status 改成 running 的行被 FOR UPDATE 挡住（MySQL）或在写锁升级时由 SQLite 串行化，不会出现「选出后被改状态仍被删」。同一 id 重复删除是幂等的（第二次 RowsAffected 0），分块之间不跨事务则放弃原子性，所以分块必须在同一事务内完成，或把单批上限提高到一次选完（切片期建议单事务 + 每批 500，事务总时长用 `AuditPurgeBatchPause` 之外的方式约束）。

测试计划（沿用 `upgrade_audit_test.go:389` `TestAuditTimeRangePreservesSecondBoundary`，它已断言 cutoff 前一秒的行被删、cutoff 内两行保留）：
- 新增：无 terminal 的 admission 不删；terminal id 更小的不删；空 correlation 不删；>1000 行分块；两方言同 fixture 删除 id 集相等；
- 反向实验：去掉 `terminal.id > audit_events.id` → 该测试应报 `purge crossed the cutoff` 或误删 admission；把 FOR UPDATE 去掉并在并发注入后应能观察到窗口（作为已知风险记录）。

## E. 今天能证明的红

命令 1（绿，证明现有守卫不碰接线）：

```
DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=13316 env -u GOROOT go test ./internal/server/ \
  -run 'TestGatewayMySQL(RuntimeSQLAudit|ColumnsMatchExport|UniqueKeysMatchExport|InsertsNameColumns|SchemaCoversRuntimeDDL)$' -count=1
ok  github.com/user/doops/agent/internal/server  1.492s
```

这 5 个守卫（64 条审计 + 4 个 schema/列/唯一键/INSERT 清单）全绿，但 `OpenGatewayStore` 仍只开 SQLite，所以它们只证明「渲染文本成对」，不证明任何一条语句在 MySQL 上执行过。

命令 2（红，接线即死）：把 `migrate()` 的头几条语句直接喂给 MySQL 8.4.13（`docker exec int-gm21 mysql`）：

| 语句 | MySQL 结果 |
|---|---|
| `PRAGMA journal_mode=WAL` | `ERROR 1064 ... near 'PRAGMA journal_mode=WAL'` |
| `CREATE TABLE grants (id INTEGER PRIMARY KEY AUTOINCREMENT, ...)` | `ERROR 1064 ... near 'AUTOINCREMENT, ...'` |
| `CREATE TABLE t (id TEXT PRIMARY KEY)` | `ERROR 1170 BLOB/TEXT column 'id' used in key specification without a key length` |
| `INSERT OR IGNORE INTO t2 ...` | `ERROR 1064 ... near 'OR IGNORE INTO ...'` |
| `BEGIN IMMEDIATE` | `ERROR 1064 ... near 'IMMEDIATE'` |
| `CREATE TABLE t3 (...) STRICT` | `ERROR 1064 ... near 'STRICT'` |
| `SELECT julianday('2026-01-01T00:00:00Z')` | `ERROR 1305 FUNCTION mysql.julianday does not exist` |
| `WITH terminal AS MATERIALIZED (...)` | `ERROR 1064 ... near 'MATERIALIZED ...'` |
| `DELETE FROM audit_events WHERE id IN (SELECT id FROM audit_events ...)` | `ERROR 1093 You can't specify target table ...` |

推论：接线后的第一个失败点是 bootstrap（第二条 PRAGMA），不是 64 条渲染；即使手工跳过 bootstrap，audit purge 仍会 1064/1093，julianday 回填仍 1305。

## F. 实施顺序

1. C 的版本记录决策 + bootstrap runner（installer 复用 / 已安装校验 / 增量列表雏形）——1.5 天
2. B 的 5 文件切片 + 真机切片测试——1 天
3. 方言 per-store 化或 `sync.Once` 决策 + 并发回归——0.5 天（推荐先 Once）
4. D 的 audit purge 两步改写 + 两方言测试——1 天
5. 1213/1205 重试包装 + 并发/死锁测试——1 天
6. sql_mode、开关与回滚文档、plan 文档收口——0.5 天

## 未覆盖

- 未在真实 Kubernetes 集群/生产规模数据上运行；行数假设来自测试 fixture。
- monitor/upgrade/asset 等链路未评估（本报告只到 slices 边界）。
- 冻结导出缺 `platform_skills`、`platform_mcp_servers` 的影响只在 INT-04/INT-20 文档里有记录，未在本次重新抓取。

