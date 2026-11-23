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
