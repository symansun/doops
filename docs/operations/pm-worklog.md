# 总控工作日志（pm-worklog）

## 2026-11-24 00:39 巡检

### 研判
- 工作目录 = `/Users/wwyz/Documents/l8ai/doops.sh`；Antigravity 三条用户会话（0ac63dc6 设计+规范、ad402c4c worktree 半成品、bc2a6760 安全整改）全部未完成。
- skill-first 线断口三处：`skills_builtin_packages.go`（49 KB，untracked）与 `skills_registry.go:73` 重复声明 `builtinSystemSkills` 且 raw string 语法错误；`skills_builtin/` 下 8 个包从未执行、未入库；产物/时序/diff 下游为空（`gateway_store_schema.go:275/296` 只有 `monitor_runs`/`monitor_run_items`，无 `inspection_metric_points`）。
- main = 53bbd118（领先 origin 14 个提交），另一条会话 23:40–00:29 在 `components/`/`lib/`/`package.json`/`pnpm-lock.yaml`/`CHANGELOG.md` 上高频提交，派活必须避让这批文件。

### 决策
- 按「Go 打包」与「Skill 包内容」拆分所有权：`dev-sf-01` 独占 `internal/server/*.go`，`skill-dev-01` 独占 `skills_builtin/**`，用 `//go:embed all:skills_builtin` 对接，两线文件零重叠可并行。
- 环境坑固化：shell 里 `GOROOT=/Users/wwyz/.local/share/go` 是旧 go1.26.2，一律 `env -u GOROOT go ...`；模块根在 `backend/agent`；macOS 无 `timeout`。
- 不与 `audit/r3-*` 那条会话抢文件，安全整改剩余项不派。

### 分派
- 00:39 `dev-sf-01` → SF-01（Go 侧接线）。
- 00:41 `skill-dev-01` → SB-01（8 个包实战化：docker 实跑、协议断言、同盘去重夹具、补 `inspect-database`/`inspect-ssh-host` 脚本、`_verify/verify.sh`）。
- 00:45 `qa-skill-01` → QA-01（独立实测缺陷清单，不改代码）。
- 00:45 `dev-artifact-01` → SF-03（产物 File-First 落盘 + 轻量索引）。

### 更正
- 00:39 按 R75 调整 `dev-sf-01` 边界：不再写 `inspect-vm-host` 正文，改用 `skills_builtin/_embedcheck/` 验证 embed 管线。

### 待用户决策
- D1 `.worktrees/agent-resource-sync` 27 文件半成品（落后 main 53 提交）：废弃重做 / 逐条挑拣。
- D2 本仓库是否长期采用 l8ai 三本账（本文件 + backlog.md + pending-decisions.md）。

## 2026-11-24 00:52 worktree 整理与清理（用户 m00245 "worktree整理合并，清理，后边主线开发"）

### 研判
- `git worktree list` 共 19 条：主工作区 + `.worktrees/` 3 条 + `/private/tmp`、`/private/var/folders` 下 15 条（多数 prunable 或目录已不存在）。
- 未入库工作量 4 处：agent-resource-sync（36 文件，含 7 个新 go 文件）、gateway-mysql（12 个未跟踪文件，整套 MySQL 迁移）、游离 commit f616beee（`.cnb.yml` CI 拆分）与 2cca31bf（删 lib 测试 + 两个前端重构）。
- 本地 18 个分支全部已合入 main（ahead=0），可直接删。

### 决策
- 先保全再清理：所有未入库工作量先落成 `rescue/*` 分支并提交，再删 worktree，避免直接丢文件。
- `rescue/cnb-ci-split` 判为废弃：唯一改动 `.cnb.yml` 已被 main 的 31115116「publish from GitHub only」删除，合回等于复活已撤销的发布链路。
- 干净合并的先做：`rescue/gateway-mysql` 在 `/tmp` 隔离 clone 验 build/test，不污染 4 条在跑的会话工作区。

### 执行
- 保全：`rescue/agent-resource-sync` d5250a6d、`rescue/gateway-mysql` d32abbfd、`rescue/cnb-ci-split` f616beee（后删）、`rescue/dedupe-web-tests` 2cca31bf。
- 清理：10 条实存 worktree 全部 `git worktree remove --force`，其余 `git worktree prune`；`.worktrees/` 目录删除；18 个已合入分支 `git branch -D`。现存 worktree 仅主工作区，分支 5 条（main + 4 rescue）。
- 防误提交：把 `outputs/`、`.agentcloud/`、`Agentfile`、`.agentfile-import.lock`（会话运行时产物，含 MCP token）写进 `.git/info/exclude`。

## 2026-11-24 00:55 巡检
- 在跑 6 个会话，已达并发上限，本轮不派新活：`skill-dev-01`(SB-01)、`dev-artifact-01`(SF-03)、`qa-skill-01`(QA-01)、`integrator-gm`(INT-01)、`integrator-ars`(INT-02 隔离移植)、`dev-sf-01`(SF-02 重派)。
- 已落 main：`801a73e6` 合入 `rescue/gateway-mysql`（INT-01 的 merge 段完成，golden 断言与新测试在改）；`7beb4679` QA-01 基线报告。
- 阻塞：github 仍不可达（推送循环 `bash-6174c5a7a3054908` 每 40 s 重试），本地领先 origin 138 个提交；`rescue/cnb-ci-split` 已删（内容被 31115116 主动撤销）。
- 01:05 巡检：5 会话在跑（dev-sf-01/SF-02、skill-dev-01/SB-01+SF-07、dev-artifact-01/SF-04、integrator-gm/INT-01、integrator-ars/INT-02），并发未满；SF-03 已入 main（daada95c），台账 b8e8bcce 已推，远端 ahead=0/behind=0；推送重试 job 全部结束（已恢复）；工作区 19 改 + 1 未跟踪目录为两条进行中会话的半成品，编译暂不可用，符合预期；未派新活（依赖未解锁）。
- 01:10 INT-01 收尾（integrator-gm，证据提交 8cf75d87 已推）：MySQL 迁移 31 子用例全绿、docker 8.4 真迁移演练完成（58 表/带数据回读一致/规划器 exit 2→加 reviewed mappings 后 exit 0）、`production-schema.sqlite.sql` 移入 testdata。遗留 2 缺口（platform_skills、platform_mcp_servers 不在冻结导出与原生 DDL，parity 只比子集）→ 新派 **INT-04**（parity 改全等断言 + 未知源表 fail closed + 重抓导出 + 容器真回读）。
- 01:10 教训（写入派活规则）：共享工作区共用一个 git index，任何会话 `git add` 后若未立刻提交，会被别的会话的提交"扫走"（INT-01 的 docs 改动被 patrol 连扫 3 次）。对策：一律 `git commit -o <自己的路径>` 或「暂存后立刻提交」；总控只 add 自己路径，永不用 `-a`/`-A`。
- 01:15 SB-01 + SF-07 完成（skill-dev-01，6 个提交 f00e01bf/adb1286c/fe65de7c/cf20d2bb/9a91baff/8371e99a，远端 8371e99a）：verify.sh 全量 20/20（静态 8 + 脚本 6 + 夹具 6），D4 去重后 1837.15GiB 未翻倍、D3 回归覆盖、全树无 skipped、单条最大 5408B、凭据 canary 0 命中；**额外发现**：6 个 inspect.sh 在库里是 0644（spec 要求 0755），已 chmod 并加静态断言（这是 SF-02 分派落地的前提）。database/ssh-host 判为 AI 驱动、不补脚本。→ 本轮派 **SF-08**（鲁棒性矩阵：超长行/非 UTF-8/locale/时区/命令缺失/proc 不可读/符号链接环，10 场景真跑）。
- 01:16 SF-02 完成（dev-sf-01，ee192275 已推，+a47ed50d spec 措辞）：分派只从 SKILL.md frontmatter 派生（resolveSkillEntrypoint/resolveSkillExecMode，auto|script|agent），DriverMode/StrategyID 从 spec 与代码消失（只剩 gateway_store_schema.go:361-362 历史列，无读写），8 slug 全部产出 item、无静默丢弃，20/20 测试绿；旧 Strategy=vm_host 数据兼容 200。
- 01:16 依赖解锁 → 派 **SF-06 架构符合性核验** 给 qa-skill-01（SOP-3 模板 B，只读，git archive 干净树，逐条 §1–§5，SF-04 未落地部分如实记"进行中"）。dev-sf-01 本轮转空闲（下一步观察项：doops_ssh 是否进 AI 白名单属安全决策，需我先给口径再派活，不擅自开工）。
- 01:18 INT-02 移植完成（integrator-ars，分支 `rescue/agent-resource-sync-v2` = 9ae10d93，父 801a73e6，44 文件 +2352/−195）：rescue 基线落后 70 提交、internal/server 已被 main 大拆分（gateway_store.go→5 个文件、tunnel_hub.go→agent_session/agent_relay/client_tool_call/doagent_prompt），冲突全是结构迁移而非重复实现；4 处冲突按 main 新结构重写，build/vet 绿，`-run 'Resource|Agent|Registry'` 113.9s 绿，新增 3 个锁行为测试（状态机/清单截断/对账发布），pytest 44 passed。**结论：值得合入**，但需先补 MySQL 普查计数（numbered_parameter 4、on_conflict 31、json_each 5、sqlite_introspection 8、reserved_use 12）+ agent_status 补 mode/kubernetes_available 列与 testdata 导出，否则 TestGatewayMySQLRuntimeSQLAudit 会红。
- 01:18 合入排序决策：**不抢跑**。INT-04（integrator-gm，正在做 parity 全等断言与重抓导出）与本合入都会改 `gateway_mysql_schema.sql`/`testdata/gateway-sqlite-schema.sql`，同一批文件同时只允许一个会话（R75）。故 INT-05（合并 v2 + 补 census/DDL）置为待办、阻塞于 INT-04，待其推送后立即派给 integrator-ars（它已有 /tmp/doops-int-ars 与全部冲突处置上下文）。
- 01:20 INT-04 完成（integrator-gm，ca443a16 已推，远端 tip 748828e3）：缺口比点名多一张——除 platform_skills、platform_mcp_servers 还有 inspection_metric_points；导出补到 61 表/49 普通索引/4 唯一索引/12 触发器，DDL 58→61。核心不是手抄清单而是新守卫 `TestGatewayMySQLSchemaCoversRuntimeDDL`：扫包内非测试 .go 的 CREATE TABLE，要求每张运行时表同时出现在冻结导出与原生 DDL（缺失即 Fatal 并提示重抓），迁移内瞬时建后 RENAME/DROP 的 `grants_v2`/`credential_bundle_items_v2`/`credential_verifications_v2` 显式排除且反向要求不得出现。真机 8.655s 全绿 + 三表插行回读一致 + 两个反向实验（删 DDL 表 / 删导出表各自 FAIL）。
- 01:20 INT-05 仍阻塞：SF-04（dev-artifact-01）正在改 `gateway_store_schema.go`（inspection_metric_points 建表+4 索引，工作区已可见），与 v2 的 `ensureColumn` 改动同文件，且 SF-04 提交时必须把 MySQL 普查期望刷成 29/8——两件事必须串行。待 SF-04 提交后立即派 INT-05。注：`CHANGELOG.md`/`app/globals.css` 出现改动，前端会话已重新活动，禁区不变。
- 01:23 SF-04 完成（dev-artifact-01，0c000b1c 已推）：inspection_metric_points（12 列 + 4 索引，幂等建表）＋ metrics[] 提取（扁平数值兜底、语义键排除）＋ 标准键桥接 monitor_resource_snapshots（按 cluster/instance 去重、已有快照不双写）＋ `GET /v1/monitor/metrics`（30d 只在新接口）＋ run 详情 diff（并集、previous=0 时 delta_pct 为 null、checks 按 id:/name: 归一、走 readDB 只读池规避 MaxOpenConns=1 死锁）；8 文件 +1218/−26，30.16s 绿，两个反向实验各自 FAIL（return nil / 交换 deltas 入参）。**自称越界**：在 tunnel_hub.go 加了一行路由（不在清单也不在禁改清单），已记录。
- 01:23 依赖解锁 → 派 **INT-05** 给 integrator-ars（合并 v2 + agent_status 两列 + 重抓导出 + census 以实测刷新 + 真机回读 + 前端 TS 类型校验）。这是 `rescue/*` 回归 main 的最后一块。
- 01:23 **SF-05 前端仍不能派**：另一条会话正在活动（CHANGELOG.md 01:15、app/globals.css 01:19、components/files-panel.tsx、lib/console-views.ts、lib/files-maintenance.ts 均未提交改动），与 SF-05 的 components/、lib/ 领地直接重叠，属 R75 冲突。需用户定：接管前端还是等它收尾。
- 01:25 巡检：在跑 3（skill-dev-01/SF-08、qa-skill-01/SF-06、integrator-ars/INT-05），空闲 3（dev-sf-01、dev-artifact-01、integrator-gm）；main tip 9a55b31e（含他人 f9d6673d 合并 deny-rule 上下文），ahead=0/behind=0；SF-05 因 D5 未决不派，本轮无新派活。

## 01:35 巡检（定时，第 6 轮）

- 在跑 4：dev-sf-01/SF-09（正在删 `monitor_engine_agent.go`、改 runner/http/types/store）、skill-dev-01/SF-08（6 个 inspect.sh 在改）、dev-artifact-01/SF-05（`lib/monitor-metrics.ts`+`components/` 在写）、integrator-ars/INT-05。
- 空闲 2：qa-skill-01（SF-10 已交付 d311cf33）、integrator-gm（INT-04 已交付 ca443a16）。
- 后台 job 4 条全部 completed（都是历史推送重试与隔离验证，已无用，不再保留）。
- 主干：HEAD `eee5902b`，`ahead=0 behind=0`（用显式 refspec 刷新后再判，避免假"落后"）。工作区 19 改 + 4 新增 = 三条进行中会话的半成品，符合预期。
- 派活：**SF-12** → integrator-gm（SF-10 的两条性能项，只允许改 `monitor_metric_store.go`+`monitor_metric_test.go`，禁碰 schema / monitor_engine_* / 前端 / skills_builtin）。台账已更新。
- 判断：并发 5 < 上限 6；无新任务可派（SF-11 卡真实 target，INT-06 卡 SF-05），不再加派。
- 提醒：`package.json` 出现改动，归属另一条前端会话（非 SF-05 范围，SF-05 已被明确禁改），无需干预。


## 01:45 巡检（定时，第 7 轮）

- 在跑 6：dev-sf-01/SF-09、skill-dev-01/SF-08、dev-artifact-01/SF-05、integrator-gm/SF-12、integrator-ars/INT-05（决策已下发）；qa-skill-01 空闲。无后台 job。
- 主干大推进：INT-05 已合入（`15e75cf1`，B 方案成功，`console-shell.tsx` 三方写回未被抢写）、SF-12 已落（`eb920009`）、SF-09 已落（`0226da5a`）、SF-08 分批在落（`6616a0d3`）。
- 推送前做了**独立编译验证**：`git archive eb920009` → /tmp 干净树 `go build ./...` rc=0、`go vet` rc=0，然后 push（`eb920009..0226da5a`），现 `ahead=0 behind=0`。
- 质量亮点：SF-12 拒绝了我建议的"排序键换成 `i.started_at_ms`"——它核实写路径里 item 与 run 的 `started_at_ms` 不是同一值，两次运行重叠时会选出另一个前驱、改变 `previous_run_id`；改为在保持 `(run.started_at_ms DESC, item.id DESC)` 语义前提下重写查询走 `idx_monitor_runs_started`+`EXISTS` 探测，EXPLAIN 断言无 TEMP B-TREE，并做了反向实验。D2 判定"不改 schema"，把代价写进函数注释。这正是闭环标准要的行为。
- INT-05 的 `archived_at` 缺默认值已修（`gateway_mysql_schema.sql:308` `DEFAULT ('')`），列级守卫与新增真实 INSERT 形状测试待其回报确认。


## 01:55 巡检（定时，第 8 轮）

- 在跑 5：dev-sf-01/SF-13、skill-dev-01/SF-08、dev-artifact-01/SF-05、qa-skill-01/QA-02、integrator-ars/INT-05 收尾；integrator-gm 空闲（SF-12 已交付 243be1c7）；无后台 job。
- 主干：`998e536c fix(gateway-mysql): default archived_at as VARCHAR so the copier accepts it`（INT-05 收尾提交）本地领先 1，先 `git archive 998e536c` 干净树 `go build ./...` rc=0，再 push，现 `ahead=0`。
- 工作区 14 项在改 = SF-05（前端 3+2 文件）与 SF-08（6 个 inspect.sh）半成品，符合预期。
- 未派新活：SF-11 卡真实 target/凭据、INT-06 卡 SF-05、CHANGELOG 被前端会话占用；并发 5，有 1 个空位但无满足依赖的任务，按规则不空转派活。


## 01:59 收口：SF-08 闭环 · INT-05 闭环并升出 INT-06

- **SF-08 完成**（skill-dev-01，6 提交至 `2123551c`）：`verify.sh` 34/34（20 旧 + 14 robust）。修两个**真缺陷**：①非 UTF-8 命令输出被整段丢弃（6 个 `inspect.sh` 的 run 助手缺 `encoding="utf-8", errors="replace"`，反向实验 `mounts=0/unique=0` 数据全丢）；②256 KiB 只约束 JSON payload、Markdown 正文未裁剪（新增 `HARD_CAP_BYTES` 与 `emit_capped()`，反向实验 `longline` 404113 B 超限）。另：`PROXY_PORTS` 静态断言锁七端口、ssh-host SKILL.md 必含 `doops_ssh`/`managed_ssh_channel`、缺依赖一律 warning 不静默。它按 spec §4.1 拒绝我建议的 `status=degraded`（status 仅 pass|warning|critical）——spec 胜，接受。
- **INT-05 完成**（integrator-ars）：B 方案（`git merge-file` 原子三方写回 `components/console-shell.tsx`，sha256 `a221004a…` 二次比对一致）一次成功未降级；`rescue/agent-resource-sync-v2` 9ae10d93 合并为 `15e75cf1`；MySQL 配套 `755e4bab`+`998e536c`。真机反向实验三组：`kubernetes_available NOT NULL` → `Error 1048`；`archived_at TEXT DEFAULT ('')` → `DEFAULT_GENERATED` 被 copier 判 generated；无默认 → `Error 1364`。关键取舍：`archived_at` 用 `VARCHAR(64)`（TEXT 的表达式默认会被离线 copier fail closed）；不全量重抓导出（会删 4 个生产遗留索引），只外科式补列 + `TestGatewayMySQLAgentStatusColumnsMatchExport` 列级守卫。
- **INT-06 派 integrator-ars**：把列级守卫从 `agent_status` 推广到**全部导出表**，并加"NOT NULL 无默认且 INSERT 会省略该列"的守卫（从源码 AST 抽真实 INSERT 列，不手工维护清单）；跑出的真实漂移当轮修。反向实验三组必跑（删默认值/改名/删列）。理由：1048 与 1364 已在 `agent_status` 上各爆一次，其余 60 张表仍是 table 级盲区。
- 在主工作区未覆盖项（继续挂 backlog）：INT-05 的前端浏览器实测、Gateway↔Agent 端到端、其余表列漂移（INT-06 接手）。


## 02:01 派 SF-14（CI 接线）· 记 open item O-1

- **SF-14 派 skill-dev-01**：`.github/workflows/ci.yml` 四个 job（Go tests / Web / Contract tests / Secret scan）**都不跑 `verify.sh`**，SF-07/SF-08 的 34 项真跑证据在 push 后无人复验 → 违反闭环标准②的持续性。要求：新增 `skills-packages` job（照抄 `ref: ${{ inputs.source_commit || github.sha }}`）、CI 用 `SB01_IMG_PYTHON=python:3.12-slim` + `alpine:3` 并写明"CI 验脚本逻辑，镜像一致性由发布流程保证"、`--privileged` 夹具不可跑时走显式 `warning + reason=no_privileged_docker`（不许删场景、不许静默跳过）、本地干净树用 CI 同命令跑通 34 项、push 后 `gh run list/watch` 自己核对并给出绿/红结论。
- **O-1（open item，未决）**：主工作区存在未跟踪 `backend/agent/internal/server/dbg_test.go`（47 行，`TestDebugPurgeCutoff` + `openCredentialTestStore`，凭证审计清理的调试残留），非本轮任何会话声明所有；不删、不提交，待确认归属后清理，已记此处避免被误当成品入库。
- 并发：SF-13、SF-05、QA-02、INT-06、SF-14 = 5 个在跑，integrator-gm 空闲。


## 02:04 SF-13 闭环 · 派 INT-07（真执行链路）· 记能力缺口 SB-02 与安全决策 D8

- **SF-13 完成**（dev-sf-01，`3e66d588` 8 文件 +41/−112，`0fdab05f` 台账）。删死码不留 stub：`monitor_artifact.go:246-336` `buildBuiltinStrategyArtifact`（SF-09 后无调用者）、`backend/agent/api/mcp.go` 的 `MonitorInspectParams/MonitorInspectResponse`（`doops_monitor_inspect` 已随 `monitor_engine_agent.go` 下线）。`strategies_json` 只加注释不删列（删列会连带改 `gateway_store_schema.go` SQL 本体与 testdata，越界），并在 `GATEWAY_MYSQL_MIGRATION_PLAN.md` 新增「遗留列清单」。反向实验：映射表塞假 slug → `FAIL monitor_engine_test.go:256: unexpected legacy mapping` / `:278 ... not a builtin Skill package`。测试 `ok 78.063s`（共享树）/ `ok 80.020s`（干净树）。披露越界：另改了 `backend/docs/DOOPS_API_GUIDE.md`（:1235/:1240 仍描述已下线 `doops_monitor_inspect`），判为同批文档修复，保留。
- **能力缺口（已入 spec §2.4.1，owner skill-dev-01）→ backlog 新任务 SB-02**：旧采集器有、现 Skill 包没有的两项能力——`host.disk_io`（磁盘 I/O 与延迟）、`k8s.metrics`（metrics-server 用量）。SF-09 删了 24 个采集函数后这两项**能力真的丢了**，不是文档问题。SB-02 必须补进对应包（`inspect-vm-host`、`inspect-k8s-cluster`）而不是改文档；补之前先按 spec §2.4.1 核实旧实现字段（`git show 0226da5a^:backend/agent/internal/server/monitor_engine_agent.go` 的 check ID：vm_host 7、ssh_host 5、k8s_cluster 6、container_runtime 2、app_service 5、database_service 2）。**SB-02 等 SF-14 让出 `testdata/skills_builtin_verify/**` 后开工**（R75 同片文件）。
- **INT-07 派 integrator-gm**：`monitor_engine_test.go` 的 `monitorEngineFixture` 已有真 e2e 骨架（hub 路由 + `inventoryTestAgent` + `f.http`），但内置 Skill 包的 `scripts/inspect.sh` **从未被后端流水线执行过**——脚本正确性由 verify.sh 单独验、产物落盘与指标桥接由 Go 单测用构造 payload 验，两段之间无接口证明。任务：新增 `skills_builtin_e2e_test.go`，从 `SKILL.md` frontmatter 取 entrypoint（不硬编码），docker（debian:12）与本机 sh 双路径且打印所用路径，断言 `artifact_dir`/`artifact_sha256`/`artifact_bytes` 与实测一致、`metrics[]` 落 `inspection_metric_points` 并能从 `/v1/monitor/metrics` 读回、连跑两次 `previous_run_id`+`metric_deltas`；反向实验两组（entrypoint 改不存在必须非 pass、脚本输出非法 JSON 必须被拒）。禁碰 `skills_builtin/**` 与 `ci.yml`（SF-14 占用）。
- **D8（新决策待定，入 pending-decisions）**：ssh_host 能力缺的是"平台 AI 可见的受管 SSH 通道"——`StructuredSSH` 不在 AI 可见白名单（`tool_registry.go:45`），属安全边界，不能顺手放宽。留给用户拍板：是保持"AI 永不直连 SSH、只能走 Skill 包 + 平台已授权通道"，还是新开受管通道面向 AI 开放（需单独威胁建模）。
- 并发 5：SF-05、QA-02、INT-06、SF-14、INT-07。


## 02:06 INT-08 探针实测：18 列丢默认值、10 张表整体缺失

- integrator-ars 探针（干净树 bcc8e72a，61 张导出表 vs `gateway_mysql_schema.sql`）：列名漂移 **0 张**（此前的 9 张差异是它探针解析伪影：`--` 注释行、CHECK 续行 OR、类型后逗号）；**18 列/10 张表**"NOT NULL 无默认 + 被真实 INSERT 省略"，这 18 列在 SQLite 导出里**都有默认值** → MySQL 侧丢默认 = 真机 1364（审计 audit_events 4 列、platform_mcp_servers 4、platform_skills 2、agent_enrollments 2、inspection_items 1，tokens/credentials/credential_grants/credential_versions/edge_enrollments 各 1 的 revoked_at）。决定：当轮修，且**逐列照 SQLite 导出抄默认值**（`revoked_at` 填 `''` 与填 `NULL` 语义不同，不统一填 `''`）；守卫跳过项必须 `t.Logf` 打印 + 具名常量 `gatewayMySQLUnmigratedTables`，"可见欠账"而非注释里埋。
- **更严重的缺口**：10 张表被运行时 INSERT 引用，但 MySQL DDL 与**冻结导出都没有**：`agent_resource_state`/`agent_observations`/`agent_resource_audit`（INT-02 分支的 `docs/migrations/agent_resource_sync.sql`）、`credential_bundle_items_v2`/`credential_verifications_v2`/`grants_v2`、`inventory_agent_assets`/`inventory_machine_tags`/`inventory_resource_catalog`、`personal_asset_records` —— 它们由 `backend/docs/migrations/*.sql` 在 SQLite 侧建表。**根因**：INT-04 的 `TestGatewayMySQLSchemaCoversRuntimeDDL` 只扫包内非测试 .go 的 `CREATE TABLE`，迁移脚本建的表在扫描范围外，所以守卫一直绿。→ 新任务 **INT-09**：先把守卫扩到扫 `backend/docs/migrations/*.sql` 让它**先红**，再补 10 张 DDL + 导出 + 真机写入回读。
- 未派 INT-09：等 INT-08 收官（同改 `gateway_mysql_*_test.go` 与 schema/导出，R75）。

## 02:08 巡检（定时，第 9 轮）

- 在跑 5：skill-dev-01/SF-14、dev-artifact-01/SF-05、qa-skill-01/QA-02、integrator-gm/INT-07、integrator-ars/INT-08；dev-sf-01 空闲（SF-13 已闭环）；无后台 job。
- 主干 `bd945415` = origin/main，工作区 12 项半成品（SF-05 前端 5、SF-14 `verify.sh`、INT-07/INT-08 在写）。
- **新发现缺口并派 SF-15 给 dev-sf-01**：`grep -rn "retention|prune|cleanup" monitor_*.go` **零命中**，spec（17 KB）也没有任何保留/清理条款 → 每条 item 最多 768 KiB 产物 + 每次 run 数十行指标点**只增不减**，定时巡检会把 agent 宿主机磁盘吃满，并让 `/v1/monitor/metrics` 全表扫描变慢。要求：先写 spec 新节（产物 30 天 / 每 schedule 最近 50 次 run 保护；指标 90 天；**先删文件后删索引行**，避免索引指向空文件；不删 running 与最新一次），实现放新文件 `monitor_retention.go`，三组反向实验。禁改 schema（INT-08 占用）。
- 其余按规则不空转：SB-02 等 SF-14 让出 `testdata/skills_builtin_verify/**`、INT-09 等 INT-08、CHANGELOG 仍被前端会话占用。


## 02:09 INT-08 卡点决策：TEXT 列默认值走 A（运行层显式给值）

- integrator-ars 实测卡点：18 列里只有 5 列（`VARCHAR`/`BIGINT`：`platform_skills.driver_mode='agent_prompt'`、`strategy_id=''`、`platform_mcp_servers.last_probe_status=''`、`last_probe_at_ms=0`、`last_latency_ms=0`）能在 DDL 层补默认；另 13 列全是 `TEXT`（`audit_events.error/tail/ended_at/scope_json`、`agent_enrollments.revoked_at/confirmed_at`、`inspection_items.repair`、5 张表的 `revoked_at`、`platform_mcp_servers.last_probe_error`）**DDL 层修不了**：MySQL 8.4 的 TEXT 只接受表达式默认 `DEFAULT ('')` → `information_schema.EXTRA=DEFAULT_GENERATED` → `gateway_mysql_copy.go:336` 的 `strings.Contains(extra,"generated")` 直接 `errGatewayCopySchema` 拒整表；字面量 `DEFAULT ''` 则 `Error 1101` 语法错。
- **决定 A**（改 9 处运行时 INSERT 显式给值），驳回 B（TEXT→VARCHAR 会截断 `audit_events.tail/error`、`scope_json`，且改 copier 互操作面）与 C（守卫降级成打印欠账，真机仍 1364）。A 的依据是 `gateway_mysql_schema.sql` 头部既有约定本身：**"SQLite TEXT columns keep no DEFAULT … The runtime dialect layer must supply values explicitly"** —— 代码补到与文档一致，不是新发明。
- 给它的硬约束：逐列核对导出默认值（只补与导出一致的值，不一律 `''`）、SQLite 侧语义必须不变并有测试证明、numbered-parameter/on_conflict 两套变体同步改、`TestGatewayMySQLRuntimeSQLAudit` census 按实测刷新并写明哪几条语句新增了列、守卫最终**无 allowlist fail-closed** 且错误信息指名"表.列"、反向实验三组（删列 → 红；删 DDL 默认 → 红；把 `DEFAULT ('')` 加回去 → copier 必须拒，证明没走回死路）、真机 mysql:8.4 上跑一次改动前会 1364 的真实 INSERT。

