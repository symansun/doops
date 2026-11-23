# 待办与派活（backlog）

本仓库的任务队列。规则来自 `agentcloud/docs/operations/agent-team-workflow.md`（v4.8）R43–R46：
任务先入「待办」，总控决定归属后移入「进行中」（写派给谁、派出时间），完成移入「已完成」（附提交号）。

## 进行中

| 编号 | 任务 | 角色/会话 | 文件占用 | 派出 | 状态 |
|---|---|---|---|---|---|
| SF-01 | 内置巡检 Skill 包 embed/registry 接线、`Entrypoint` 字段、落盘复用、恢复可编译 | dev-sf-01 | `backend/agent/internal/server/*.go`、`backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md` | 2026-11-24 00:39 | 运行中 |
| SB-01 | 8 个内置巡检 Skill 包实战化：docker 真跑、协议断言、同盘去重夹具、补缺脚本包、`_verify/verify.sh` | skill-dev-01 | `backend/agent/internal/server/skills_builtin/**` | 2026-11-24 00:41 | 运行中 |
| QA-01 | 内置巡检 Skill 包基线缺陷实测（独立验证，不改代码） | qa-skill-01 | `docs/reviews/`（仅报告） | 2026-11-24 00:45 | 运行中 |
| SF-03 | 巡检产物 File-First 落盘：`<dbDir>/artifacts/<date>/<run_id>/<item_id>/`，SQLite 只存索引 | dev-artifact-01 | `backend/agent/internal/server/monitor_artifact*.go`、`gateway_store_schema.go`（增量） | 2026-11-24 00:45 | 运行中 |

## 待办（排队，等前置合入）

| 编号 | 任务 | 依赖 | 备注 |
|---|---|---|---|
| SF-02 | 移除 `builtin_strategy`/`strategy_id` 双轨，`monitor_engine_runner.go` 改走 Skill 包 | SF-01 合入 main | 改 `monitor_engine_*.go` 与测试，约 15 处 `builtin_strategy`、29 处 `StrategyID` |
| SF-04 | `inspection_metric_points` 通用时序表 + 跨次 diff + `GET /v1/monitor/metrics` | SF-03 合入 | spec 4.2/4.3 |
| SF-05 | 前端 `InspectionArtifactViewer` 动态渲染 + 趋势图，ego-browser 真实验收 | SF-04 合入 | 改 `components/`、`lib/`，与另一条会话的前端提交错峰 |
| SF-06 | 架构评审核验 SF-01/SB-01/SF-03（SOP-3，模板 B） | 上述提交进 main | 只读核实，不改业务代码 |

## 合入 main（从遗留分支移植）

| 编号 | 任务 | 来源 | 状态 |
|---|---|---|---|
| INT-01 | Gateway MySQL 迁移（store/dialect/schema + 迁移脚本 + 计划文档） | `rescue/gateway-mysql` d32abbfd | 干净合并，隔离 clone 验 build/test 后落 main |
| INT-02 | Agent 资源同步四期（7 个新 go 文件 + SQL 迁移 + target-manifest-card） | `rescue/agent-resource-sync` d5250a6d | 4 处冲突待人工移植，落后 main 70 个提交，须全套 build/test |
| INT-03 | 前端重构挑拣（format / modal / use-loader） | `rescue/dedupe-web-tests` | 见 pending-decisions D3，未开工 |

## 已完成

| 编号 | 任务 | 提交 | 报告 |
|---|---|---|---|
| — | 建立本台账 | 见 docs(ops) 提交 | pm-worklog.md |
| CLEAN-01 | worktree/分支清理 + 4 条遗留分支保全 | 见 docs(ops) 提交 | pm-worklog.md 00:52 |

## 不派

- 安全整改剩余项（B-10…B-13、S-04、S-06、S-08、M-02 与 5 项半程）：另一条活跃会话正在 `audit/r3-*` 上推进（00:04–00:29 仍在合），重复派会撞文件。
- `.worktrees/agent-resource-sync`（ad402c4c 遗留 27 文件半成品）：等用户决定「废弃重做」还是「逐条挑拣」，见 pending-decisions.md D1。
