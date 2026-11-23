# 待办与派活（backlog）

本仓库的任务队列。规则来自 `agentcloud/docs/operations/agent-team-workflow.md`（v4.8）R43–R46：
任务先入「待办」，总控决定归属后移入「进行中」（写派给谁、派出时间），完成移入「已完成」（附提交号）。

## 进行中

| 编号 | 任务 | 角色/会话 | 文件占用 | 派出 | 状态 |
|---|---|---|---|---|---|
| SF-01 | 内置巡检 Skill 包 embed/registry 接线、`Entrypoint` 字段、落盘复用、恢复可编译 | dev-sf-01 | `backend/agent/internal/server/*.go`、`backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md` | 2026-11-24 00:39 | 运行中 |
| SF-08 | 内置巡检包鲁棒性矩阵（畸形环境 10 场景真跑，治崩溃/非法 JSON/静默 fallback） | skill-dev-01 | `skills_builtin/**`、`testdata/skills_builtin_verify/**` | 2026-11-24 01:15 | 运行中 |
| SF-04 | `inspection_metric_points` 通用时序表 + 标准键桥接快照 + `GET /v1/monitor/metrics` + 跨次 diff | dev-artifact-01 | `monitor_metric*.go`、`monitor_engine_{store,types,http}.go`、`gateway_store_schema.go` | 2026-11-24 01:05 | 运行中 |
| INT-04 | MySQL 迁移覆盖率补齐：`platform_skills`/`platform_mcp_servers` 纳入导出与原生 DDL，parity 改全等断言 | integrator-gm | `testdata/gateway-sqlite-schema.sql`、`gateway_mysql_schema*.go`、`gateway_mysql_copy*.go`、`GATEWAY_MYSQL_MIGRATION_PLAN.md` | 2026-11-24 01:10 | 运行中 |
| INT-02 | Agent 资源同步移植（**隔离 /tmp 目录**解冲突，产出 `rescue/agent-resource-sync-v2`） | integrator-ars | 隔离目录，不动主工作区 | 2026-11-24 00:49 | 运行中 |

## 待办（排队，等前置合入）

| 编号 | 任务 | 依赖 | 备注 |
|---|---|---|---|
| SF-05 | 前端 `InspectionArtifactViewer` 动态渲染 + 趋势图，ego-browser 真实验收 | SF-04 合入 | 改 `components/`、`lib/`，与另一条会话的前端提交错峰 |
| SF-06 | 架构符合性核验（SOP-3 模板 B，只读，产出 docs/reviews/2026-11-24-skill-first-compliance.md） | qa-skill-01 | 只新增报告文件 | 2026-11-24 01:16 | 运行中 |

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
| QA-01 | 内置巡检 Skill 包基线缺陷实测（D1–D10，含 D4/D9 高危） | `7beb4679` | `docs/reviews/2026-11-24-skill-package-baseline.md` |
| SF-02 | 执行分派单一真源（移除 builtin_strategy/strategy_id 双轨，修复 D9） | `ee192275`（+`a47ed50d` spec 措辞） | 20/20 测试绿；8 slug 全产出 item |
| SB-01 | 8 个内置巡检包实战化（协议断言/去重夹具/补缺脚本判定/verify.sh 20 项全绿） | `f00e01bf` `adb1286c` `fe65de7c` `cf20d2bb` `9a91baff` `8371e99a` | 静态 8/8 + 全量 20/20；额外发现脚本 0644 → chmod 0755 |
| SF-07 | 修 QA-01 缺陷 D1/D2/D3/D4/D5/D7/D8/D10 | 同上 6 个 sha | D4 去重后 1837.15GiB 未翻倍；全树无 skipped |
| INT-01 | Gateway MySQL 迁移合入 main（合并 801a73e6 + golden 断言按真实值收紧 + docker 真迁移演练） | `801a73e6` / 证据 `8cf75d87` | 31 子用例全绿；遗留 2 缺口转 INT-04 |
| SF-03 | 巡检产物 File-First 落盘（4 索引列 + 透明回读 + 清理删盘） | `daada95c` | 5 测试绿；256 KiB 按每文件口径（见 D4） |

## 不派

- 安全整改剩余项（B-10…B-13、S-04、S-06、S-08、M-02 与 5 项半程）：另一条活跃会话正在 `audit/r3-*` 上推进（00:04–00:29 仍在合），重复派会撞文件。
- （D1 已决并执行，见 pending-decisions.md）
