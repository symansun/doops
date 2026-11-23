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
