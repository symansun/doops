# 待办与派活（backlog）

本仓库的任务队列。规则来自 `agentcloud/docs/operations/agent-team-workflow.md`（v4.8）R43–R46：
任务先入「待办」，总控决定归属后移入「进行中」（写派给谁、派出时间），完成移入「已完成」（附提交号）。

「完成」的定义见 `docs/operations/closure-standard.md`（2026-11-24 起强制）：文档同批、测试同批、干净树验证、CHANGELOG、反向实验、零悬空尾巴、推送即完成。

## 进行中

| 编号 | 任务 | 会话 | 文件占用 | 派出 | 状态 |
|---|---|---|---|---|---|
| QA-02 | 全仓回归：`go test ./...` 全包 + pytest + verify.sh + 残留扫描（SF-09 删 2186 行的保险） | qa-skill-01 | 只新增 `docs/reviews/2026-11-24-main-regression.md` | 01:52 | 运行中 |

## 待办（排队/待决策）

| 编号 | 任务 | 依赖 | 备注 |
|---|---|---|---|
| SF-11 | 真实 Gateway↔Agent 隧道端到端验收（SF-06 §5 红线5「未覆盖」） | 需要可用 target 与凭据 | 未派：需先确认有可连的 agent 与允许的 canary 凭据，不能编造证据 |
| INT-09 | 补 10 张"被 INSERT 引用但 MySQL DDL 与冻结导出都没有"的表（`agent_resource_state`/`agent_observations`/`agent_resource_audit`、`credential_*_v2`、`grants_v2`、`inventory_agent_assets`/`inventory_machine_tags`/`inventory_resource_catalog`、`personal_asset_records`）；**先扩守卫到扫 `backend/docs/migrations/*.sql` 让它先红**，再补 DDL/导出，真机写入回读 | INT-08（同改 `gateway_mysql_*_test.go` 与 schema/导出） | 根因：`TestGatewayMySQLSchemaCoversRuntimeDDL` 只扫包内 .go 的 CREATE TABLE，迁移建的表在范围外；不补则 MySQL 迁移后这些能力无表 |
| SB-02 | 补回 SF-09 删采集器后**真丢的两项能力**：`host.disk_io`（磁盘 I/O 与延迟）进 `inspect-vm-host`、`k8s.metrics`（metrics-server 用量）进 `inspect-k8s-cluster`；先在 spec §2.4.1 核对旧字段（`git show 0226da5a^:backend/agent/internal/server/monitor_engine_agent.go`）再实现，验收进 `verify.sh` | SF-14（同占 `testdata/skills_builtin_verify/**`） | 能力真实丢失，不是文档问题；owner 只能是 skill-dev-01（脚本归属） |
| INT-06 | Console 侧展示 agent_resource 同步状态（`components/target-manifest-card.tsx` 已随 INT-05 入库） | INT-05 | 无 TS 校验与浏览器实测，属 SF-05 之后的下一刀 |
| — | `CHANGELOG.md` [Unreleased] 统一补写（SF-04/SF-05/SF-09/INT-05 的用户可见变化） | SF-05/SF-09 完成 | 该文件被前端会话占用，由我或前端会话统一写，避免共享索引互扫 |

## 已完成

| 编号 | 任务 | 提交 | 结论摘要 |
|---|---|---|---|
| SF-14 | 把 `verify.sh`（34 项真跑）接进 CI：新增 `skills-packages` job（apt shellcheck + 现场 build `sb01-inspect-test:ci` + 预拉 `alpine:3` + 跑 verify.sh）、`SB01_NO_PRIVILEGED` 显式降级分支（4 个特权场景记 WARN、不删场景）、`BUILD_DEPLOY_SOP.md` 第五节第 8 条 | 686b2076、70a7727e | [CI run 45239179874](https://github.com/symansun/doops/actions/runs/45239179874) 该 job 2m27s 绿（verify step 110s）、34/34 PASS、降级 0；首跑红因 verify.sh 把缺镜像当硬失败而 CI 未预拉 `alpine:3`，补 pull 步骤后绿（反向实验：`docker rmi alpine:3` 后 28/29 复现同一红）；本地干净树 `git archive 686b2076` 34/34、257s；`SB01_NO_PRIVILEGED=1` 30 通过 4 降级 |
| INT-08 | 列级守卫推广到全部导出表 + "NOT NULL 无默认且被真实 INSERT 省略"守卫（AST 抽列，无 allowlist）；当轮修 18 列/10 表：13 个 TEXT 列改在 INSERT 显式写 `''`，5 个可内联默认的列补 DDL 字面默认 | 655fd9de | `TestGatewayMySQLColumnsMatchExport`（61 表列集合双向）+ `TestGatewayMySQLInsertDefaultsMatchSchema`（无列清单的 INSERT 记 skip，零覆盖即失败）；build/vet/`-run GatewayMySQL`（真机 mysql:8.4）/`-run 'Resource\|Agent\|Registry'` 全绿（干净树 `git archive 655fd9de` 复跑）；反向实验：删 `revoked_at` → 红且指名 `credentials.revoked_at`、逐个删 5 个 DDL 默认 → 全红（`skills_registry.go:255`/`mcp_registry.go:81`）、给 TEXT 加 `DEFAULT ('x')` → copier `mysql_copy_schema_mismatch`；真机修复前 `Error 1364 Field 'error' doesn't have a default value`、修复后回读 `''`；10 张未迁移表入 `gatewayMySQLUnmigratedTables` 交 INT-09；导出未重抓（保留 4 个遗留索引） |
| INT-05 | 合入 `rescue/agent-resource-sync-v2`（9ae10d93）+ `agent_status` 两列 + census 刷新 + 真机回读 | 15e75cf1、755e4bab、998e536c | B 方案原子三方写回 `console-shell.tsx`（sha256 二次比对一致）一次成功；`kubernetes_available` 改 `TINYINT(1) NULL`（`NOT NULL` 被真机 `Error 1048` 拒）、`archived_at` 改 `VARCHAR(64)`（`TEXT DEFAULT ('')` 是 `DEFAULT_GENERATED`，被离线 copier fail closed）；真机 mysql:8.4 反向实验三组；未覆盖：浏览器实测、Gateway↔Agent 端到端（INT-07 接手部分） |
| SF-05 | 前端接 SF-04：viewer Diff/Trends + 删死开关 + 停发 strategies/ssh_hosts；补丁 bbe4b9c8 详情页铺开 toInspectionViewerProps + 纯函数映射 + 接线锁测试 | 3e1e6d8a / bbe4b9c8 | tsc/13-13/699-699/build 全绿；反向实验（demo 加回 strategies → 红）；ego-browser 四张截图；**更正**：后端无 actions 字段，只收 skills[] |
| INT-07 | 真 Skill 包执行链路 e2e（inspect.sh 经 doops_shell 回流产物与指标） | 33c7147f | 仅新增 skills_builtin_e2e_test.go 372 行；真容器 python:3.12-slim 跑通；11 个指标落库且 metrics 接口可读回；4 组反向实验全红。**缺口**：只覆盖 vm-host 一包 → 已立项 INT-10 |
| SF-15 | 巡检产物与指标点保留策略（30d/90d、keep-runs 50、running 保护、先删文件后删行、force 兜底） | d979459c | 10 新测试绿；4 组反向实验全红；干净树 build/vet/`-run Monitor|Skill|Retention` ok 57.2s。**取舍**：keep-runs=50 意味着老 schedule 停跑后再跑时历史不被时间窗口清掉 |
| SF-16 | 产物不可读时显式返回 `artifact_state=present|missing`（老数据回退仍 present，truncated 是 present 子状态） | 43e8a8cb | 干净树 build/vet/`-run Monitor|Artifact|Retention` ok 30.2s；真实 HTTP 测试（删产物目录后 run/item 均 missing、结构完整、索引行仍在）；反向实验 2 组红（禁用判定 `/` 去序列化）；spec §3.2 四条 + §3.3 一处 |
| SF-13 | legacy strategy 残留清扫：删死码 `buildBuiltinStrategyArtifact` 与 `doops_monitor_inspect` RPC 类型、`strategies_json` DDL 退役注释、遗留列清单入迁移文档、spec §2.4.1 六采集器→Skill 映射（`host.disk_io`/`k8s.metrics` 标待补）、WebUI/API guide 措辞、`retiredStrategySkillSlugs` 加 slug 钉子 | 3e66d588 | build/vet 绿，`-run 'Monitor\|Skill\|Strategy'` 78.0s 绿（干净树 80.0s）；反向实验：映射改成不存在的 slug 时钉子测试 FAIL |
| SF-01 | Skill 包 `//go:embed all:skills_builtin` + `Entrypoint`/exec_mode 派生 + 落盘复用 | 7457bd56、d41bc318 | build 绿，`-run 'Skill\|Monitor'` 12/12 |
| SF-02 | 移除 `DriverMode`/`StrategyID` 双轨，分派只从 SKILL.md 派生 | ee192275、a47ed50d | 8 slug 全产出，20/20 绿（60.3s），主工作区 build rc=0 |
| SF-03 | 产物 File-First 落盘 + 索引列 + 截断脱敏 | daada95c | 5 测试绿；D4 定为"每文件 ≤256 KiB" |
| SF-04 | `inspection_metric_points` 时序 + metrics API + run diff | 0c000b1c | 4 索引、30.16s 绿、两次反向实验；待 SF-10 独立复验 |
| SF-09 | 彻底移除 legacy native strategy 双轨：删 `monitor_engine_agent.go`（6 采集器 + `doops_monitor_inspect`）、`strategies`/`ssh_hosts` 请求 400、老 run 只读 + retest 经 Skill 包重放、spec §1.1/§2.3/§2.4/§4.3 | 0226da5a | build/vet 绿，`-run 'Monitor\|Skill\|Strategy'` 47.2s 绿（干净树 51.9s），两条新测试反向实验变红 |
| SF-12 | diff 查询避 TEMP B-TREE（保持语义重写）、series 判定不改 schema | eb920009 | 拒绝"排序键换 `i.started_at_ms`"的粗方案：item 与 run 的 `started_at_ms` 不等价，重叠运行会改前驱；改走 `idx_monitor_runs_started`+EXISTS 探测，三条新测试（含 EXPLAIN 断言），反向实验 FAIL 证据齐 |
| SF-10 | 独立复验 SF-04（§4.2/§4.3） | d311cf33 | **符合 2 · 部分 1 · 不符合 0**；只读产物 JSON、多 target 不串行、三组反向实验均红；两条低危性能项转 SF-12，run 级状态语义进 spec（SF-09） |
| SF-06 | 架构符合性核验（模板 B，只读） | 102711e6 | 符合 13 · 部分 6 · 不符合 0 · 进行中 3 · 未覆盖 1 |
| SF-07 | 内置包真跑 + 静态契约（`testdata/skills_builtin_verify/verify.sh`） | 6 提交至 8371e99a | 20/20 PASS；6 个 inspect.sh 0644→0755 真缺陷 |
| SB-01 | 8 个 Skill 包内容建设（脚本/degraded/去重夹具） | f00e01bf…8371e99a | disk_total 去重后 1837.15 GiB 未翻倍、canary 零命中 |
| SF-08 | 内置巡检包鲁棒性矩阵（14 场景真跑）+ 端口表收紧 + ssh-host 退化分支 + 消费 `DOOPS_TARGET_*` | 583af310、dd755a7c、6616a0d3、f645012b、5498cb2d | 34/34 PASS（20 旧 + 14 robust）；两个真实缺陷：非 UTF-8 静默丢磁盘（反例 mounts=0）、整条产物 404113 B 超 256 KiB；静态断言锁死 PROXY_PORTS=spec §2.4 |
| QA-01 | 独立基线实测报告 | 7beb4679 | 缺陷进 SF-07/SB-01 |
| INT-01 | Gateway MySQL 迁移主干合入 | 801a73e6、8cf75d87 | 真 mysql:8.4 全链搬运，58 表 |
| INT-04 | 导出/DDL 覆盖补齐 + 全等守卫 | ca443a16 | 61 表/49+4 索引/12 触发器，真机 8.655s |

## 已废弃

| 编号 | 原因 |
|---|---|
| INT-03（`rescue/cnb-ci-split`） | 唯一内容 `.cnb.yml` 已被 main 31115116「publish from GitHub only」主动删除，分支已删 |
