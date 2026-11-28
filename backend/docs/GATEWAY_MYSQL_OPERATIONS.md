# Gateway MySQL 后端运维手册

本文是 Gateway 以 MySQL 作为存储后端的**运维口径**：怎么开、启动时会被哪些检查拦住、数据落在哪、怎么备份、怎么回退。
设计与迁移取舍见 [GATEWAY_MYSQL_MIGRATION_PLAN.md](GATEWAY_MYSQL_MIGRATION_PLAN.md)；发布流程见 [BUILD_DEPLOY_SOP.md](BUILD_DEPLOY_SOP.md)。

## 1. 现状与边界

- 默认后端仍是 SQLite（单文件 + 同目录下的 `skills/`、`artifacts/`）。设 `DOOPS_GATEWAY_STORE=mysql` 才切到 MySQL。
- MySQL 路径已在真机 MySQL 8.4 上验过：启动种子、保留策略、Skill 巡检、凭据全生命周期、目标资源清单同步（见迁移计划「D17 验收结论」）。
- **割接命令已交付，生产割接仍未执行**：`doops-gateway store mysql-copy` 负责把只读快照搬进空库（dry-run 评审、库名校验、单事务、逐表校验，见 §7）。生产环境那一刀没有跑过；离线规划器（`backend/scripts/gateway-sqlite-snapshot.py`、`backend/scripts/gateway-sqlite-to-mysql.py`）只做快照与 schema 规划，不搬数据。

## 2. 前置条件

- MySQL **8.4**、**8.0** 或 **5.7.22+**（含阿里云 RDS MySQL `5.7.44`；低于 `5.7.22` 启动即报 `gateway_mysql_version_unsupported`）。
  - MySQL 8.0 / 8.4 原样安装 `gateway_mysql_schema.sql`；
  - MySQL 5.7 自动将 7 个函数唯一索引与 2 个 `VARCHAR(253)` 域名唯一索引（`proxy_routes.host`、`edge_routes.hostname`）转换为冻结白名单内的 `VIRTUAL GENERATED` 索引列，并为 `inventory_node_links` 主键的 `node_name` 附加 191 字符索引前缀，无需开启 `innodb_large_prefix` 即可直接适配 767 字节单列索引上限（离线搬运器仅放行这 9 个白名单列，任何其他生成列仍 fail-closed），同时在 JSON 标量比较处改用 `utf8mb4_bin`（5.7 无 `utf8mb4_0900_*`）；MySQL 5.7 会解析但忽略表级 `CHECK` 约束，数据校验依赖运行期代码与唯一索引/触发器。
- 一个**专属库**，不要用 `mysql` / `information_schema` / `performance_schema` / `sys`，也不要和别的应用共库：Gateway 用「库内表集合 + schema 基线台账」判断这是不是自己的库。
- 库的默认字符集必须是 `utf8mb4`，默认排序规则必须是驱动能设置的（MySQL 8 推荐 `utf8mb4_0900_ai_ci` 或 `utf8mb4_bin`；MySQL 5.7 推荐 `utf8mb4_bin` 或 `utf8mb4_general_ci`）。
- 连接账号需要有该库的 DDL 与触发器权限：空库首次启动会建 77 张表（含 `platform_plugin_config` 与 `platform_plugin_overrides`）、索引与 22 个触发器，并写入 `gateway_schema_migrations` 的 `0001_baseline` 基线行；存量 MySQL 库升级启动时会自动以幂等方式补建这两张插件配置表。

建库示例：

```sql
CREATE DATABASE doops_gateway
  DEFAULT CHARACTER SET utf8mb4
  DEFAULT COLLATE utf8mb4_0900_ai_ci;
```

## 3. 配置

| 变量 | 必填 | 说明 |
|---|---|---|
| `DOOPS_GATEWAY_STORE` | 切换时必填 | 只接受 `sqlite`（默认）或 `mysql`；写错是启动失败，**不会**静默回落到 SQLite |
| `DOOPS_GATEWAY_MYSQL_DSN` | mysql 时必填 | go-sql-driver DSN，必须是 `tcp(host:port)/dbname` 形态并点名数据库 |
| `DOOPS_GATEWAY_DATA_DIR` | mysql 时必填 | 存 Skill 包与巡检产物的**绝对路径**；空值取 `/var/lib/doops-gateway`，相对路径是启动失败。SQLite 后端忽略该变量 |
| `DOOPS_AUDIT_STORAGE` | 可选（默认 `db`） | `db`（默认）将审计正文留在数据库列；设为 `file` 开启 **File-First 审计文档存储**，将执行输出 `tail` 写入 `<DOOPS_GATEWAY_DATA_DIR>/audit/docs/YYYY-MM-DD/audit_<id>.txt`（库内仅存 ~100B `$audit_doc` 指针）并实时追加完整 JSONL 文档到 `<DOOPS_GATEWAY_DATA_DIR>/audit/records/YYYY-MM-DD.jsonl`（`0600`），查询接口 `/v1/audit` 与控制台透明回读文档并在热库清理后无缝回退查历史文档；非法值或目录不可写均启动失败（fail-closed） |

示例：

```bash
DOOPS_GATEWAY_STORE=mysql \
DOOPS_GATEWAY_MYSQL_DSN='doops:****@tcp(10.0.0.12:3306)/doops_gateway?parseTime=true' \
DOOPS_GATEWAY_DATA_DIR=/var/lib/doops-gateway \
  doops-gateway serve -port 8080 -db /var/lib/doops-gateway/ignored.db -monitor-tick 30s
```

注意三点：

- `-db` 在 mysql 后端不参与存储（该参数仍在，默认值是 SQLite 路径，仅作兼容保留），不用特意改；数据目录由 `DOOPS_GATEWAY_DATA_DIR` 决定。
- DSN 里**不要**写 `collation=` 去覆盖库默认值：会话排序规则必须等于库默认，否则启动即失败（见 §4 的 `gateway_mysql_collation_mismatch`）。
- 驱动与连接池默认参数：启动探测发现 `utf8mb4` 且服务端 `sql_mode` 未启用 `NO_BACKSLASH_ESCAPES` 时自动开启 `InterpolateParams=true`（单次查询由 3 次公网 RTT 降至 1 次；如需关闭可在 DSN 显式写 `interpolateParams=false`），同时启用 `CheckConnLiveness=true`、默认 `readTimeout=60s` / `writeTimeout=60s`（DSN 显式指定则优先）、连接池 `MaxOpenConns=24` / `MaxIdleConns=8` / `ConnMaxIdleTime=2m` / `ConnMaxLifetime=30m`。
- 数据目录会被 `MkdirAll 0700` 并对 `skills/`、`artifacts/` 做可写探测；目录不可写是启动失败，不会退化成「把文件塞进数据库列」。

## 4. 启动会被什么拦住

所有下列情况都是**启动失败**，没有降级模式：

| 错误码 | 含义 | 处置 |
|---|---|---|
| `gateway_store_invalid` | `DOOPS_GATEWAY_STORE` 不是 sqlite/mysql | 改值或删除该变量 |
| `gateway_mysql_dsn_missing` / `_invalid` | DSN 缺失、不可解析、非 tcp(host:port)、未点名库 | 修 DSN；错误信息不回显 DSN 内容 |
| `gateway_mysql_database_reserved` | 指向 `mysql` 等系统库 | 换专属库 |
| `gateway_data_dir_invalid` / `_unwritable` | 数据目录相对路径、不可创建、不可写 | 给绝对路径与权限 |
| `gateway_mysql_charset_unsupported` | 库默认字符集不是 utf8mb4 | 重建库或改库默认字符集 |
| `gateway_mysql_collation_mismatch` | DSN 覆盖了排序规则、某张表或某个文本列与库默认不一致、或安装期会话排序规则被污染 | 按错误里点名的表/列改回库默认；驱动不认识的排序规则（如 `utf8mb4_0900_bin`）会直接拒绝并提示可用默认值 |
| `mysql_schema_ledger_missing` | 库里已有表但没有 `gateway_schema_migrations` 基线行 | 确认是不是别的应用的库；是 Gateway 的库就补基线行或换空库 |
| `gateway_sqlite_only_path` | 运行期误调 SQLite 专有路径（PRAGMA 之类） | 属缺陷，记 issue 并带上错误文本 |

启动成功后日志会有一行：

```text
gateway store: backend=mysql address=10.0.0.12:3306 database=doops_gateway data_dir=/var/lib/doops-gateway
```

## 5. 数据落在哪

- **库内**：全部业务表（用户、授权、审计、巡检计划与运行、指标时序、凭据元数据与密文、资源清单等）与 `gateway_schema_migrations` 台账。
- **磁盘**：`DOOPS_GATEWAY_DATA_DIR` 下的 Skill 包与巡检产物（`artifacts/<日期>/<run_id>/item_<id>/`，单文件上限 256 KiB，内容与库内索引的 sha256/字节数一一对应）。
- **为什么不能只备份库**：产物正文在磁盘、索引在库内。只还原库会让详情页出现 `artifact_state=missing`（保留策略与删除都会这样显式呈现，不会假装正常）；只还原磁盘则索引对不上。库与目录必须成对备份、成对还原。

保留策略（都会打日志）：

| 变量 / 参数 | 默认 | 说明 |
|---|---|---|
| `DOOPS_MONITOR_ARTIFACT_RETENTION_DAYS` | 30 | 巡检产物保留天数；`0` 表示永久保留 |
| `DOOPS_MONITOR_METRIC_RETENTION_DAYS` | 90 | 指标时序保留天数 |
| `DOOPS_MONITOR_ARTIFACT_KEEP_RUNS` | 50 | 每个巡检计划无条件保留的最近运行次数 |
| `-audit-retention` | `0` | 已完成审计行保留时长，`0` 表示永久 |
| `-webhook-retention` | `720h` | 终态 webhook 投递与事件保留时长，`0` 表示永久；清理只在 webhook worker 启动/唤醒时按小时节流执行 |
| `DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION` | 30 天 | `platform_load_samples` 保留窗口，随实例清理一并执行 |

清理是「先删文件、后删索引行」；每 15 分钟窗口的第一次跳过会打一行 `monitor retention deferred` 说明距上次扫描多久与两个窗口值。

在真实 mysql:8.4（库显式 `utf8mb4_bin`）上已端到端验证（`backend/agent/internal/server/monitor_retention_mysql_test.go`）：同一计划播种过期与保留中的 run/item/metric/snapshot 后，首次 `PruneMonitorRetention` 删掉过期 run、产物目录、指标与快照并保留最近一次；随后同一进程连续 4 次运行只打出 1 条 `monitor retention deferred: last scan ... throttle 15m0s`；`PurgeExpiredAudit` 删掉过期审计并留下 1 条 `audit:purge` 回执；`GET /health` 返回 200。同一批验证还暴露并修复了 MySQL 回执的两个缺陷：连续无过期行的保留周期不再把「值未变」误判成「回执丢失」；提交过批次后失败时回执会写成 `status=error`、`error=audit_purge_failed`，并保留真实已删行数，而不是留下 success 回执。生产规模（千万级审计行、长时间高频巡检）的清理耗时与锁竞争尚未压测，割接前应在目标库用实际数据量做一次窗口演练。

## 6. 备份、审计归档与空间回收

### 6.1 内置一致性备份（`doops-gateway store backup`）

无需外部 `mysqldump`，直接在单次 `REPEATABLE READ` 只读事务内导出全部基表（自动跳过 MySQL 5.7 虚拟生成列），并与 `DOOPS_GATEWAY_DATA_DIR` 下的 `skills/`、`artifacts/` 成对打包校验，支持通过已有 `DOOPS_COS_*` 环境变量分片上传至对象存储（`> 8 MiB` 自动 S3 Multipart，上传后强制 `HeadObject` 字节校验）并自动清理过期副本：

```bash
# 每日全量业务表 + 数据目录备份上传 COS（上传校验通过后清理本地临时包，COS 保留 30 天）
doops-gateway store backup \
  -output-dir /var/lib/doops-gateway-backups/daily \
  -exclude-tables audit_events,platform_events \
  -cos-prefix backups/gateway/daily \
  -keep-local 0 \
  -keep-cos-days 30
```

### 6.2 审计日志与平台事件导出归档、分批清理与空间回收（`doops-gateway audit export`）

`audit_events` 与 `platform_events` 往往占库容量的 90% 以上；仅执行 `DELETE` 不会缩减 InnoDB `.ibd` 文件体积。`doops-gateway audit export` 采用主键/索引游标流式导出（恒定内存、不锁写入），写入 `0600` 权限 `.jsonl.gz` 与 `.sha256`，仅在本地落盘与 COS `HeadObject` 校验**全部通过**后才按 5,000 行/批清理已导出区间（`id <= max_id`）并记录含归档摘要的 `audit:purge` 回执，随后通过 `-reclaim` 执行 `OPTIMIZE TABLE` 真正将空间归还 RDS 磁盘：

```bash
# 每日归档 24 小时前的审计日志与终态平台事件到 COS（File-First 文档模式下同时保留近 30 天本地 JSONL 文档供控制台直查），校验通过后分批清理热库并 OPTIMIZE TABLE 释放空间
doops-gateway audit export \
  -output-dir /var/lib/doops-gateway-backups/audit \
  -older-than 24h \
  -include-platform-events \
  -cos-prefix backups/gateway/audit \
  -purge -confirm-database doops_gateway \
  -reclaim \
  -keep-local 0 \
  -keep-doc-days 30 \
  -keep-cos-days 90
```

- **三层分级存储推荐（`DOOPS_AUDIT_STORAGE=file`）**：
  1. **MySQL 热索引层（近 24 小时）**：不含大块 `tail` 正文，只保留事务状态机、外键关联与近 24h 实时监控指标，常驻 `< 5 MB`；
  2. **本地文档层（`<DOOPS_GATEWAY_DATA_DIR>/audit/records/YYYY-MM-DD.jsonl`，保留 30 天）**：完整结构化审计文档（含全量 `tail`），控制台「审计记录」与 `/v1/audit` 无缝跨热库+本地文档检索；
  3. **对象存储冷归档层（COS `backups/gateway/audit/`，保留 90 天+）**：每日流式压缩上传 `.jsonl.gz` + `.sha256` + `manifest.json`，强制 `HeadObject` 校验通过后才执行热库分批清理与过期本地文档回收。

- 首次全量清空释放空间时可改用 `-all -include-unresolved`（仍严格保护进行中的 `running`/`executing` 行与 `audit:purge` 回执，绝不删除未导出的增量行）。
- 建议通过宿主 `systemd` timer 每日凌晨执行上述两条命令（遵循网关架构，不在 Gateway 进程内新增通用备份调度器），并在 `EnvironmentFile=/etc/doops-gateway.env` 下运行，避免 DSN 或 COS 密钥出现在命令行与日志中。

如需手工逻辑备份（或线下核验）：

```bash
# 1) 库（一致性快照，不锁写）
mysqldump --single-transaction --quick --routines --triggers \
  --default-character-set=utf8mb4 doops_gateway > gateway-$(date +%F).sql

# 2) 文件（Skill 包 + 巡检产物）
tar -C /var/lib/doops-gateway -czf gateway-data-$(date +%F).tgz skills artifacts
```

还原顺序：先导入库，再解文件；然后启动 Gateway 并 `GET /v1/monitor/runs/<id>` 抽一条旧运行，确认 `artifact_state=present` 且指标能经 `/v1/monitor/metrics` 读回。

## 7. SQLite → MySQL 割接

顺序固定：只读快照 → 规划评审 → 空库演练 → 停写窗口内正式搬运（库 + 数据目录）→ 启动校验。数据目录里是巡检产物与 Skill 包，只搬库不搬文件会让旧运行报 `artifact_state=missing`。

```bash
# 1) 只读快照（脚本自身只读，源库不停写也能取一致快照）
backend/scripts/gateway-sqlite-snapshot.py --source /var/lib/doops-gateway/gateway.db \
  --destination /secure/gateway-snapshot.db

# 2) schema 规划评审（不连 MySQL，只输出映射清单）
backend/scripts/gateway-sqlite-to-mysql.py --source /secure/gateway-snapshot.db \
  --plan /secure/gateway-plan.json --allow-reviewed-mappings

# 3) 搬运：DSN 只从环境变量读，口令不会出现在参数、输出或日志里
export DOOPS_GATEWAY_MYSQL_DSN='doops:****@tcp(10.0.0.12:3306)/doops_gateway?parseTime=true'

# 3a) 评审计划：逐表源行数、目标是否为空、是否跳过；零写入
doops-gateway store mysql-copy -source /secure/gateway-snapshot.db \
  -confirm-database doops_gateway -dry-run

# 3b) 评审文件搬运（同一命令；两个数据目录必须都给、绝对、不同，目标目录不得已含 artifacts/ 或 skills/）
doops-gateway store mysql-copy -source /secure/gateway-snapshot.db \
  -confirm-database doops_gateway -dry-run \
  -source-data-dir /var/lib/doops-gateway -target-data-dir /var/lib/doops-gateway-mysql

# 3c) 正式搬运（目标必须是空库；目标数据目录必须还没有 artifacts/ 与 skills/）
doops-gateway store mysql-copy -source /secure/gateway-snapshot.db \
  -confirm-database doops_gateway \
  -source-data-dir /var/lib/doops-gateway -target-data-dir /var/lib/doops-gateway-mysql
```

- 前置条件（不满足即退出码 2，一行不写）：快照 `PRAGMA integrity_check` 必须为 `ok`；`--confirm-database` 必须与 DSN 里的库名逐字相同；目标库默认排序规则必须能被驱动精确协商（禁止 MySQL 8 `utf8mb4_0900_bin` 等驱动静默回落的排序规则，搬运入口已复用启动期 `gatewayMySQLAssertSessionCollation` 守卫）；目标库须已装原生 75 表 schema（库内有表但缺 `gateway_schema_migrations` 时提示先用 `DOOPS_GATEWAY_STORE=mysql` 启动一次，或换空库）；目标库任何一张**已有数据行**的表都拒绝——**不覆盖、不合并、不清库**（源库没有、本版本原生 schema 有的模块表只允许在目标端为空时跳过，见下一条）。
- **源库外键预检与旧数据口径**：搬运前（含空目标库 `--dry-run`）统一校验 SQLite `PRAGMA foreign_key_check`，违约时退出码 2 并点名违约表（`mysql_copy_source_foreign_key_failed`）。唯一豁免的是 `credentials.owner_id -> users(id)` 在 `scope='platform' AND owner_id=''` 时的历史 SQLite 外键（MySQL 原生 DDL 已刻意移除该外键）；其他任何外键违约（包括损坏的 `credentials.created_by`、非空 `owner_id` 或旧版 `kind='agent'` 令牌留下的 `tokens.user_id=''`）均失败关闭。新版本 SQLite 启动迁移会自动执行幂等的 `UPDATE tokens SET user_id = NULL WHERE kind = 'agent' AND user_id = ''`，历史库抓快照前可先重启新版本或手工核查 `tokens`。
- **源库缺少的表**：本版本原生 75 表 schema 里有、而该 SQLite 部署从未创建的模块表（全新部署或没跑过资源/资产同步迁移的库很常见）不需要先补建。命令逐表核对：这些表在目标端为空时打印 `table=<name> source=absent destination=empty reason=native_schema_only` 并跳过；目标端只要有 1 行就退出码 2（`mysql_copy_destination_stale`，点名表与行数），绝不覆盖。非原生 schema 的多余表仍按 `mysql_copy_schema_mismatch` 拒绝。因此全新的 SQLite 部署可以直接割接到空库，`--dry-run` 与正式执行使用同一判定、同一退出码。
- **目标端多出的表分桶一致**：这些表统一分为 `native_schema_only`（本版本功能模块，该部署从未创建）与 `legacy_destination_only`（已退役但为兼容保留的历史表），判断只来自同一份内嵌 schema 与冻结 legacy 名单。dry-run 打印 `destination_only=40 (native_schema_only=33 legacy_destination_only=7)` 并逐表给出 `reason=` 桶名；正式运行给出同样的逐表清单与 `destination_only: total=40 (native_schema_only=33 legacy_destination_only=7)` 汇总，两个数字可以直接对账。
- 空库可以直接作目标：命令装入与 Gateway 首启同一份 75 表 DDL（不写种子数据、不写基线行，基线行随快照搬过去）。`--dry-run` 在空库上不执行 DDL，但会按同一份 DDL + 同一外键/数据目录校验预告正式运行的结果：打印 `plan(preview)` 并说明会先装 75 表（版本 `gateway-mysql-schema-1`），退出码 0 表示正式运行会接受这个目标。
- `inventory_machines` 的自引用外键（`inventory_machines.inventory_machines_ibfk_1`）不需要人工删除：搬运在同一事务里先把 `host_asset_id` 写成 NULL，等所有父行落库后再回填，约束全程存在，割接后约束集合与源一致。真正的父链环会被拒绝并点名 `table <t> row <id> loops through host_asset_id`。
- 数据目录随库一起搬：`-source-data-dir` 与 `-target-data-dir` 必须同时给出、都是绝对路径且不同，目标目录不得已含 `artifacts/` 或 `skills/`。命令复制这两棵树并逐文件核对字节数与 sha256；库里的 `artifact_dir`/`storage_dir` 是数据目录相对路径，所以搬根即可，只有旧库写成绝对路径且落在源数据目录内时才改写为相对路径并计数（`paths_rewritten`，dry-run 与正式执行使用同一套路径归属与产物摘要校验规则；若快照不含 `monitor_run_items`/`platform_skills` 表则 dry-run 跳过对应索引检查）。被索引的 artifact 或 Skill 包缺失或摘要不一致时整笔回滚并点名 `run_id`/`item_id` 或 Skill slug——绝不跳过。
- 正式执行是**单事务**逐表多行批量搬运（每批上限 500 行 / 60,000 参数 / 2 MiB，兼顾百万级表吞吐与 MySQL 5.7 默认 `max_allowed_packet`；搬运会话显式启用 `STRICT_ALL_TABLES` 与 `NO_AUTO_VALUE_ON_ZERO`，杜绝静默截断；历史版本 SQLite 非主键 `TEXT` 列里因旧字节截断遗留的非法 UTF-8 字节会按 Unicode 标准 `U+FFFD` 规范化后再送入 MySQL `utf8mb4` 并在输出显式汇报 `utf8-normalized: cells=<N> tables=<...>`，主键列含非法 UTF-8 仍以 `mysql_copy_invalid_utf8_primary_key` 失败关闭，`BLOB` 列原样逐字节保留；数据目录也在同一事务的提交前落盘，失败即清理已写文件）：任何一张表失败整体回滚且错误点名表；成功后逐表比对源/报告/目标行数与逐行类型化 SHA-256 摘要，并断言 `gateway_schema_migrations` 恰好一行且版本为 `0001_baseline`，不一致即非零退出。退出码固定：0 成功、1 运行失败、2 用法或前置条件失败；`--dry-run` 的退出码与「正式运行会得到的判定」一致，不会因为目标还没装 schema 就单独算失败。
- 生产割接前必须先在空库完整演练一次（dry-run → 正式搬运 → 启动 Gateway → 跑 §10 验收），确认每表行数与关键列；正式窗口内先停写再取快照，快照文件保留作回退依据（§8）。
- **生产实做验收记录（INT-51，2026-11-27/28）**：已完成真实云 RDS MySQL `5.7.44-log`（`utf8mb4_bin`，保持 `innodb_large_prefix=OFF` 未改全局参数）的离线停写快照与 `store mysql-copy` 正式割接与全项回读验证：
  - 停写快照 77 表 `1.85 GB`（`PRAGMA integrity_check = ok`）；`store mysql-copy`（`v0.3.58`）在 711.5s 内单事务完成 99 条 DDL 建表、**77 表 / 1,204,094 行**批量搬运与逐行 SHA-256 回读一致（`ledger=0001_baseline`，`verified=yes`），显式汇报历史非主键 `TEXT` 2,394 处非法 UTF-8 字节按 `U+FFFD` 规范化，并完成独立数据目录 `artifacts`（2,178 文件，731 行）与 `skills`（16 文件，9 行）逐文件 sha256 校验（`paths_rewritten=0`）；原 SQLite 文件、旧数据目录与切库前 env 文件均未改动保留作回退依据。
  - 割接后 `v0.3.60`（含 `INT-110` MySQL 5.7 空子查询 `COALESCE(CAST(JSON_ARRAYAGG(...) AS CHAR), '[]')` 修复与远程 RDS 连接池/心跳超时调优）实测启动日志为 `backend=mysql version=5.7.44-log`，D17 五项判据全绿：10/10 在线目标全量重连且无超时报错；历史与新增定时巡检运行详情 `artifact_state=present`（11/11）；`/v1/monitor/metrics` 返回完整时序；`/v1/assets/target-manifest` 正常回读且无同步失败；`/v1/assets/{machines,topology,dashboard,coverage}`（388 台机器全带 `created_at`）、`/v1/plugins`、`/v1/credentials`（194 条）、`/v1/audit`、`/v1/platform-monitor` 与控制台 `/console` 均返回 `200 OK`；`store audit-scrub -dry-run` 在真实 MySQL 5.7.44 上退出码 0（候选 `30,531` 条，零写入）。

### 7.1 割接后清洗历史审计 URL（audit-scrub）

v0.3.24 之前的 Proxy/Git 审计行会把请求 query 写进 `audit_events.command_summary`；Proxy 传输失败还会把含 query 的上游 URL 写进 `audit_events.error`。割接只搬行、不清洗，所以历史库里的 URL 材料仍在。`store audit-scrub` 是配套的一次性迁移工具，不是日常命令。

```bash
export DOOPS_GATEWAY_MYSQL_DSN='doops:****@tcp(10.0.0.12:3306)/doops_gateway?parseTime=true'

# 1) 评审：只读事务，打印逐列候选/改写数与预计行数，零写入
doops-gateway store audit-scrub -confirm-database doops_gateway \
  -backup /secure/audit-scrub-$(date +%F).jsonl -dry-run

# 2) 正式执行：先写旧值 JSONL（0600），再逐批改库
doops-gateway store audit-scrub -confirm-database doops_gateway \
  -backup /secure/audit-scrub-$(date +%F).jsonl

# 3) 回滚：按 backup 逆序恢复旧值（回执行不删）
doops-gateway store audit-scrub -confirm-database doops_gateway \
  -rollback -backup /secure/audit-scrub-$(date +%F).jsonl
```

- 前置（不满足即退出码 2，一行不写）：目标库已有原生 `audit_events`（缺表时先用 `DOOPS_GATEWAY_STORE=mysql` 启动一次）；DSN 只从 `DOOPS_GATEWAY_MYSQL_DSN` 读，任何 `-dsn` 参数都被拒绝；`-confirm-database` 必须与 DSN 库名逐字相同；`-backup` 必填且以 `0600` 新建，已存在的文件不会被覆盖；`-dry-run` 与 `-rollback` 互斥。
- 只改两列：`command_summary` 保留 method 与 path，query 整段换成 `?<redacted>`；`error` 直接调用运行期的 `scrubURLCredentials`（`audit_error_scrub.go`），逐 token 保留 scheme/host/path 并去掉 userinfo、query 与 fragment，URL 之外的字节原样保留。候选正则同步覆盖任意 RFC 3986 `scheme://`，所以 `postgres://user:<凭据>@db:5432/app?<凭据>=…` 这类历史行也会被选中并清洗；query 对所有 scheme 统一删除（统一按凭据处理，宁可少留信息），因为同一位置的无害 key 旁边可能挂着凭据。已知边界：不带 scheme 的 `user:pass@host:5432`、`git@host:repo` 不覆盖，靠猜 token 边界会把散文里的 `a:b@c` 一起吃掉；`Authorization` 之类的请求头回显也不覆盖。历史清洗与运行期新写入共用同一份 URL 解析，不维护第二套规则。不删审计行，也不整列清空；只接受 `proxy`、`pull`、`push`，通配符与别名退出码 2。
- 共用规则的边界：运行期把无法解析的 URL 写成 `<unparsable-url>`；历史命令检测到新增的这个占位符就失败关闭，不把占位符写进历史行。URL token 内尾随的 `).` 属于 token 本身，会随 query/fragment 一起删掉，不当作散文标点。SQL 预过滤使用显式 `[A-Za-z]` 字符类（不使用 MySQL 8 专属 `(?i)` 内联标记），兼容 MySQL 5.7（POSIX ERE）与 MySQL 8.0/8.4（ICU）及大小写敏感的 `utf8mb4_bin`。不带 scheme 的 `user:pass@host`、`git@host:repo` 不在候选内，原样保留；出现这类历史形态时由维护者决定是否扩大规则，命令不会自行改写。
- 不可解析的摘要或 URL 失败关闭：当前批次回滚、错误点名行 id 与列、退出码 1；不会“跳过坏行继续”。已清洗成 `?<redacted>` 或已无 URL 凭证的行不会再次入选，因此第二次 `-dry-run` 的候选数应回到 0。
- 每批 200 行单事务；backup 先写入并 `fsync`，再在同一批事务里 UPDATE。成功与失败都会写一条 `action='audit:scrub'` 回执，tail 记 `updated`/`batches`/`deleted`；失败回执是 `status=error`、`error=audit_scrub_failed`。
- 真机证据（mysql 8.4.13，库显式 `utf8mb4_bin`）：9 行播种中 dry-run 报 `command_summary candidates=3 rewrite=3`、`error candidates=3 rewrite=3`、`estimated rows=5 batches=1`，前后 `CHECKSUM TABLE audit_events` 相同且没有生成 backup；正式执行 `updated=6 batches=1 deleted=0`，backup 为 `0600` 的 6 行 JSONL；第二次 dry-run 候选 0、估算 0；`-rollback` 恢复 6 行后，非回执行的逐字节内容与播种值一致。回执是操作记录，回滚后仍保留，所以整表 `CHECKSUM` 会比清洗前多这一行；对账应排除 `action='audit:scrub'`。
- backup 含旧 query，是敏感文件：放在受控目录、验收后按敏感文件流程销毁，不得提交、进工单或贴日志。生产 `audit_events` 的数据量与候选形态尚未压测，正式执行前必须在生产快照副本上跑 dry-run 评审。
- 反向实验（实现提交的测试里固化）：去掉 `-confirm-database` 比对后错误库名也能写；把 `error` 列清空会破坏未命中行；把解析错误改成跳过会让坏行退出 0。三者都会让对应用例变红。

## 8. 回退到 SQLite

- 处理方式：把 `DOOPS_GATEWAY_STORE` 去掉（或设为 `sqlite`），指回原来的库文件后重启。**MySQL 一侧不会被删除或修改**，没有破坏性收尾步骤。
- 代价要说清：在 MySQL 上产生的新数据（审计、巡检运行、指标、凭据变更）**不会自动回灌 SQLite**，这是设计取舍而不是遗漏——本工具不提供 MySQL→SQLite 的反向搬运，因为反向合并会同时存在两份「真相」。回退保留的是割接前那份 SQLite 文件，割接后写进 MySQL 的行就留在 MySQL 里；真要合并，只能按需人工导出。
- 因此割接窗口要选在能接受这段差异的时候，并在回退决策前先确认 MySQL 上的新数据是否还有用（审计与巡检记录常需要先导出）。

## 9. 排障对照

| 现象 | 多半原因 | 动作 |
|---|---|---|
| 启动报 `Error 1045` | 账号/口令或来源主机不匹配 | 查 DSN 与 `mysql.user` 的 host 部分 |
| 启动报 1267 | 会话/表/列排序规则与库默认不一致 | 用本节 §4 的规则对齐；不要用 DSN 的 `collation=` 硬盖 |
| 巡检或资源同步报 `Error 1235` / `1093` / `1170` | 运行期 SQL 仍有 SQLite 专有写法 | 属缺陷：带上错误原文与端点记 issue，不要手工改库绕过 |
| 目标清单一直不推进 | Agent 未连、或同步失败 | 先看网关日志 `target resource sync failed`，再看 Agent 是否 online |
| 首次巡检成功但没产物文件 | 数据目录不可写或 Agent 侧工作区不可写 | 网关看启动日志 `data_dir=`；Agent 侧设 `DOOPS_WORKSPACE_ROOT` 到可写目录 |

## 10. 巡检与验收清单（每次升级后）

1. 启动日志出现 `backend=mysql` 一行且没有 `gateway store:` 之外的重试/报错。
2. `GET /v1/assets/target-manifest?cluster=<c>&instance=<i>` 的 `revision` 与 `applied_revision` 随 Agent 观测推进。
3. 触发一次 Skill 巡检：`POST /v1/monitor/runs`，再 `GET /v1/monitor/runs/<id>` 校验 `artifact_state=present`、`metrics_written` 大于 0、`checks[]` 非空。
4. `GET /v1/monitor/metrics?skill=<slug>&target=<c>/<i>&range=1h` 有返回。
5. 数据目录下能按 run 找到产物文件，且字节数与详情里的 `artifact_bytes` 一致。
6. 割接后的库跑一遍搬后校验：`users`、`grants`、`audit_events`、`monitor_runs` 的行数与快照一致（`store mysql-copy` 成功时会打印逐表行数并自带比对），且 `gateway_schema_migrations` 恰好一行 `0001_baseline`。
7. 割接后确认 `inventory_machines` 的自引用外键仍在、父子 `host_asset_id` 指向正确；旧 run 的 `GET /v1/monitor/runs/<id>` 为 `artifact_state=present`；`platform_skills` 指向的 Skill 包能执行（`sh -n scripts/inspect.sh` 或按目录跑一次）。
8. MySQL 保留策略：连续两个没有过期行的保留周期都成功，日志不出现 `audit purge receipt ... is no longer present`；需要复验时在同一容器上跑 `DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=<port> go test ./internal/server/ -run 'MonitorRetention|AuditPurge' -count=1`，并确认 `audit:purge` 回执数等于周期数、失败注入时回执为 `status=error`/`error=audit_purge_failed`。
9. 若历史库跨越 v0.3.24（旧 Proxy/Git 审计可能带 query）：先在库副本上 `store audit-scrub -dry-run` 评审候选，再正式执行并保留 0600 backup；执行后第二次 dry-run 候选数为 0，抽查行的 query 为 `?<redacted>`、error 只余 scheme/host/path，回执 `audit:scrub` 为 success。
