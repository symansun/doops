# 待办与派活（backlog）

本仓库的任务队列。规则来自 `agentcloud/docs/operations/agent-team-workflow.md`（v4.8）R43–R46：
任务先入「待办」，总控决定归属后移入「进行中」（写派给谁、派出时间），完成移入「已完成」（附提交号）。

「完成」的定义见 `docs/operations/closure-standard.md`（2026-11-24 起强制）：文档同批、测试同批、干净树验证、CHANGELOG、反向实验、零悬空尾巴、推送即完成。

## 进行中

| 编号 | 任务 | 会话 | 文件占用 | 派出 | 状态 |
|---|---|---|---|---|---|
| SF-05 | 前端接 SF-04：viewer Diff/Trends + 删 `builtin_strategy` 死开关 | dev-artifact-01 | `components/inspection-artifact-viewer.tsx`、`components/skill-registry-panel.tsx`、`lib/demo.ts`、新增 `lib/monitor-metrics.ts`(+test) | 01:26 | 运行中 |
| SF-08 | 内置巡检包鲁棒性矩阵（10 场景真跑）+ 端口表收紧 + ssh-host 退化分支 + 消费 `DOOPS_TARGET_*` | skill-dev-01 | `skills_builtin/**`、`testdata/skills_builtin_verify/**` | 01:15，01:27 加附录 | 运行中 |
| SF-09 | 彻底移除 legacy native strategy 双轨（`monitorStrategyCatalog`、`plan.Strategies` 分支、HTTP `strategies` 参数） | dev-sf-01 | `monitor_engine_{agent,runner,http}.go`、spec §1.1/§2.3、`DOOPS_WEBUI.md` | 01:26 | 运行中 |
| SF-12 | SF-10 两条低危性能项：diff `ORDER BY` 避 TEMP B-TREE、series `skill_slug` 残差过滤（不改 schema） | integrator-gm | 只 `monitor_metric_store.go` + `monitor_metric_test.go` | 01:35 | 运行中 |
| INT-05 | 合入 `rescue/agent-resource-sync-v2`（9ae10d93）+ `agent_status` 两列 + census 刷新 + 真机回读 | integrator-ars | `gateway_mysql_schema.sql`、`testdata/gateway-sqlite-schema.sql`、`gateway_mysql_runtime_test.go`、`gateway_store_schema.go` | 01:23 | 运行中 |

## 待办（排队/待决策）

| 编号 | 任务 | 依赖 | 备注 |
|---|---|---|---|
| SF-11 | 真实 Gateway↔Agent 隧道端到端验收（SF-06 §5 红线5「未覆盖」） | 需要可用 target 与凭据 | 未派：需先确认有可连的 agent 与允许的 canary 凭据，不能编造证据 |
| INT-06 | Console 侧展示 agent_resource 同步状态（`components/target-manifest-card.tsx` 已随 INT-05 入库） | INT-05 | 无 TS 校验与浏览器实测，属 SF-05 之后的下一刀 |
| — | `CHANGELOG.md` [Unreleased] 统一补写（SF-04/SF-05/SF-09/INT-05 的用户可见变化） | SF-05/SF-09 完成 | 该文件被前端会话占用，由我或前端会话统一写，避免共享索引互扫 |

## 已完成

| 编号 | 任务 | 提交 | 结论摘要 |
|---|---|---|---|
| SF-01 | Skill 包 `//go:embed all:skills_builtin` + `Entrypoint`/exec_mode 派生 + 落盘复用 | 7457bd56、d41bc318 | build 绿，`-run 'Skill\|Monitor'` 12/12 |
| SF-02 | 移除 `DriverMode`/`StrategyID` 双轨，分派只从 SKILL.md 派生 | ee192275、a47ed50d | 8 slug 全产出，20/20 绿（60.3s），主工作区 build rc=0 |
| SF-03 | 产物 File-First 落盘 + 索引列 + 截断脱敏 | daada95c | 5 测试绿；D4 定为"每文件 ≤256 KiB" |
| SF-04 | `inspection_metric_points` 时序 + metrics API + run diff | 0c000b1c | 4 索引、30.16s 绿、两次反向实验；待 SF-10 独立复验 |
| SF-10 | 独立复验 SF-04（§4.2/§4.3） | d311cf33 | **符合 2 · 部分 1 · 不符合 0**；只读产物 JSON、多 target 不串行、三组反向实验均红；两条低危性能项转 SF-12，run 级状态语义进 spec（SF-09） |
| SF-06 | 架构符合性核验（模板 B，只读） | 102711e6 | 符合 13 · 部分 6 · 不符合 0 · 进行中 3 · 未覆盖 1 |
| SF-07 | 内置包真跑 + 静态契约（`testdata/skills_builtin_verify/verify.sh`） | 6 提交至 8371e99a | 20/20 PASS；6 个 inspect.sh 0644→0755 真缺陷 |
| SB-01 | 8 个 Skill 包内容建设（脚本/degraded/去重夹具） | f00e01bf…8371e99a | disk_total 去重后 1837.15 GiB 未翻倍、canary 零命中 |
| QA-01 | 独立基线实测报告 | 7beb4679 | 缺陷进 SF-07/SB-01 |
| INT-01 | Gateway MySQL 迁移主干合入 | 801a73e6、8cf75d87 | 真 mysql:8.4 全链搬运，58 表 |
| INT-04 | 导出/DDL 覆盖补齐 + 全等守卫 | ca443a16 | 61 表/49+4 索引/12 触发器，真机 8.655s |

## 已废弃

| 编号 | 原因 |
|---|---|
| INT-03（`rescue/cnb-ci-split`） | 唯一内容 `.cnb.yml` 已被 main 31115116「publish from GitHub only」主动删除，分支已删 |
