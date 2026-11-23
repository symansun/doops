# 待办与派活（backlog）

本仓库的任务队列。规则来自 `agentcloud/docs/operations/agent-team-workflow.md`（v4.8）R43–R46：
任务先入「待办」，总控决定归属后移入「进行中」（写派给谁、派出时间），完成移入「已完成」（附提交号）。

「完成」的定义见 `docs/operations/closure-standard.md`（2026-11-24 起强制）：文档同批、测试同批、干净树验证、CHANGELOG、反向实验、零悬空尾巴、推送即完成。

## 进行中

| 编号 | 任务 | 会话 | 文件占用 | 派出 | 状态 |
|---|---|---|---|---|---|
| SF-18 | evidence 记录真实调度结果（声明 `exec_mode` vs 实际 `dispatch_mode`），老 run 兼容 + 前端可读 | integrator-e2e-01 | `internal/server/*.go`（不含 skills_builtin/**）、spec、API 文档 | 02:49 | 运行中（INT-10 后续，由它报的证据不真实问题立项） |
| SF-17 | 前端消费 `artifact_state`：missing 时正文区中文明示、指标/趋势照常、纯函数三态 + 接线锁 | dev-viewer-01 | `components/inspection-artifact-viewer.tsx` 等 viewer 文件 | 02:30 | 运行中 |
| QA-03 | 对抗性复核 6 条反向实验（SF-08 x2 / INT-08 / SF-16 / SF-15 / 审计 cutoff F5），干净树重做、破坏只在 /tmp | qa-recheck-01 | 只新增 `docs/reviews/2026-11-24-adversarial-recheck.md` | 02:38 | 运行中 |

## 待办（排队/待决策）

| 编号 | 任务 | 依赖 | 备注 |
|---|---|---|---|
| SF-11 | 真实 Gateway↔Agent 隧道端到端验收（SF-06 §5 红线5「未覆盖」） | 需要可用 target 与凭据 | 未派：需先确认有可连的 agent 与允许的 canary 凭据，不能编造证据 |
| INT-06 | Console 侧展示 agent_resource 同步状态 | INT-05 | **已阻塞（02:38）**：`target-manifest-card.tsx` 已具备四态同步状态 + 五态资源徽章 + 八类资源（`manifestStatus()`/`resourceStates`/`resourceKinds`），由 `console-shell.tsx:64/:936` 渲染；剩余「TS 校验 + 浏览器实测」需真实 target，与 SF-11/D8 同条件 |
| — | `CHANGELOG.md` [Unreleased] 统一补写（SF-04/SF-05/SF-09/INT-05 的用户可见变化） | SF-05/SF-09 完成 | 该文件被前端会话占用，由我或前端会话统一写，避免共享索引互扫 |

## 已完成

| 编号 | 任务 | 提交 | 结论摘要 |
|---|---|---|---|
| SB-02 | 补回 `host.disk_io`（inspect-vm-host）与 `k8s.metrics`（inspect-k8s-cluster）+ verify.sh 覆盖 + 反向实验 | a7a4a84e、e03d8439、2dfbf26b | `host.disk_io` 读 `/proc/diskstats` 只留整盘（排除分区与 `loop`/`dm-`/`md` 等虚拟设备），延迟阈值 50/200ms 对齐 `DiskLatencyWarnMs`，不可读时 `warning` + detail 说明缺口；`k8s.metrics` 走 `kubectl top`，metrics-server 不可用时该 check 记 `warning` 而集群 `status` 不变；verify.sh 34→39 项全绿（新增 capability 5 个真跑夹具），`SB01_NO_PRIVILEGED=1` 32 通过 7 降级；反向实验两处红：diskstats 字段偏移改错 → `指标 disk_io_reads=0 期望 4000`，吞掉 `kubectl top` 失败 → `检查项 k8s.metrics status='pass' 期望 'warning'`；spec §2.4.1 两行改「已覆盖」 |
| INT-09 | 把 SQLite 迁移独建的表纳入 MySQL：守卫改扫 `backend/docs/migrations/*.sql`（先红，红名单正好 8 表），DDL+冻导补 8 表（69 表/54 索引/22 触发器/43 FK，含 4 个 catalog 部分索引 enforcer 触发器），`gatewayMySQLUnmigratedTables` 清零；`*_v2`/`asset_monitor_series_new` 仅 migrate* 函数内豁免 | ac191ff6 | 红→绿对照：扩扫描后 `export=[8 表] ddl=[同 8 表]`，补表后全绿；census 未变；真机 mysql:8.4 八表逐一真实 INSERT 回读（`catalog_pending_identity` 第二次被 `Error 1644 (45000)` 拒）；4 组反向实验全红（删表/删默认/删列/把 `grants_v2` INSERT 挪出 migrate*）；干净树 build/vet/`-run GatewayMySQL`(真机)/`-run 'Resource\|Agent\|Registry'` 全绿；无运行时 Go 改动；导出仍外科式补（重抓会删生产遗留索引） |
| SF-14 | 把 `verify.sh`（34 项真跑）接进 CI：新增 `skills-packages` job（apt shellcheck + 现场 build `sb01-inspect-test:ci` + 预拉 `alpine:3` + 跑 verify.sh）、`SB01_NO_PRIVILEGED` 显式降级分支（4 个特权场景记 WARN、不删场景）、`BUILD_DEPLOY_SOP.md` 第五节第 8 条 | 686b2076、70a7727e | [CI run 45239179874](https://github.com/symansun/doops/actions/runs/45239179874) 该 job 2m27s 绿（verify step 110s）、34/34 PASS、降级 0；首跑红因 verify.sh 把缺镜像当硬失败而 CI 未预拉 `alpine:3`，补 pull 步骤后绿（反向实验：`docker rmi alpine:3` 后 28/29 复现同一红）；本地干净树 `git archive 686b2076` 34/34、257s；`SB01_NO_PRIVILEGED=1` 30 通过 4 降级 |
| INT-08 | 列级守卫推广到全部导出表 + "NOT NULL 无默认且被真实 INSERT 省略"守卫（AST 抽列，无 allowlist）；当轮修 18 列/10 表：13 个 TEXT 列改在 INSERT 显式写 `''`，5 个可内联默认的列补 DDL 字面默认 | 655fd9de | `TestGatewayMySQLColumnsMatchExport`（61 表列集合双向）+ `TestGatewayMySQLInsertDefaultsMatchSchema`（无列清单的 INSERT 记 skip，零覆盖即失败）；build/vet/`-run GatewayMySQL`（真机 mysql:8.4）/`-run 'Resource\|Agent\|Registry'` 全绿（干净树 `git archive 655fd9de` 复跑）；反向实验：删 `revoked_at` → 红且指名 `credentials.revoked_at`、逐个删 5 个 DDL 默认 → 全红（`skills_registry.go:255`/`mcp_registry.go:81`）、给 TEXT 加 `DEFAULT ('x')` → copier `mysql_copy_schema_mismatch`；真机修复前 `Error 1364 Field 'error' doesn't have a default value`、修复后回读 `''`；10 张未迁移表入 `gatewayMySQLUnmigratedTables` 交 INT-09；导出未重抓（保留 4 个遗留索引） |
| INT-05 | 合入 `rescue/agent-resource-sync-v2`（9ae10d93）+ `agent_status` 两列 + census 刷新 + 真机回读 | 15e75cf1、755e4bab、998e536c | B 方案原子三方写回 `console-shell.tsx`（sha256 二次比对一致）一次成功；`kubernetes_available` 改 `TINYINT(1) NULL`（`NOT NULL` 被真机 `Error 1048` 拒）、`archived_at` 改 `VARCHAR(64)`（`TEXT DEFAULT ('')` 是 `DEFAULT_GENERATED`，被离线 copier fail closed）；真机 mysql:8.4 反向实验三组；未覆盖：浏览器实测、Gateway↔Agent 端到端（INT-07 接手部分） |
| SF-05 | 前端接 SF-04：viewer Diff/Trends + 删死开关 + 停发 strategies/ssh_hosts；补丁 bbe4b9c8 详情页铺开 toInspectionViewerProps + 纯函数映射 + 接线锁测试 | 3e1e6d8a / bbe4b9c8 | tsc/13-13/699-699/build 全绿；反向实验（demo 加回 strategies → 红）；ego-browser 四张截图；**更正**：后端无 actions 字段，只收 skills[] |
| INT-07 | 真 Skill 包执行链路 e2e（inspect.sh 经 doops_shell 回流产物与指标） | 33c7147f | 仅新增 skills_builtin_e2e_test.go 372 行；真容器 python:3.12-slim 跑通；11 个指标落库且 metrics 接口可读回；4 组反向实验全红。**缺口**：只覆盖 vm-host 一包 → 已立项 INT-10 |
| SF-15 | 巡检产物与指标点保留策略（30d/90d、keep-runs 50、running 保护、先删文件后删行、force 兜底） | d979459c | 10 新测试绿；4 组反向实验全红；干净树 build/vet/`-run Monitor|Skill|Retention` ok 57.2s。**取舍**：keep-runs=50 意味着老 schedule 停跑后再跑时历史不被时间窗口清掉 |
| SF-16 | 产物不可读时显式返回 `artifact_state=present|missing`（老数据回退仍 present，truncated 是 present 子状态） | 43e8a8cb | 干净树 build/vet/`-run Monitor|Artifact|Retention` ok 30.2s；真实 HTTP 测试（删产物目录后 run/item 均 missing、结构完整、索引行仍在）；反向实验 2 组红（禁用判定 `/` 去序列化）；spec §3.2 四条 + §3.3 一处 |
| INT-10 | e2e 推广到全部有 `scripts/inspect.sh` 的包（发现式遍历 `builtinSkillPackagesFS`，无硬编码），只断言结构契约（三件产物 sha256 与字节数重算、artifact_format、metrics 键与 `inspection_metric_points`/`/v1/monitor/metrics` 逐项一致、checks 带 id/status/detail、status 域）；「脚本根本没跑」零容忍（PATH bash shim 执行日志 + `CollectorMethod=skill_script` + `evidence.entrypoint` 证明） | 4475fdd6 | 干净树 6/6 包实跑：vm-host 9 指标/6 checks、package-vuln 2/3、proxy-evasion 3/3，container-runtime/app-web/k8s-cluster 各 0 指标/1 checks 且是**诚实降级**（无 runtime、无 ss/netstat）；database（required_mcp: dosql）与 ssh-host 如实 t.Logf 跳过；4 组负控全红（脚本失败→critical、非 JSON→format=text 且 0 指标、声明不存在的 entrypoint→400 `skill_entrypoint_missing`、运行期删 entrypoint→非 pass）；`-run 'BuiltinSkillE2E'` 14.980s 绿。**副产物**：发现 evidence 记录的是声明值而非实际调度（→ SF-18） |
| QA-02 | 全仓回归基线（全包 go test + pytest + verify.sh + 残留扫描） | 93f9e92e、b9229eb8 | 三模块 build/vet rc=0；`internal/server` 三分组 45s/171s/193s 全 ok；pytest 真实检出 32 passed；verify.sh b5765106 20/20、b733e6fd 34/34；残留 grep 51→27、生产代码 0 使用。**F5（审计 cutoff 字符串比较误删）在报告基线 b5765106/b733e6fd 上确定性红，但 main 上 1446b965 已改按 `started_at_ms` 比较，我复跑 `TestAuditTimeRangePreservesSecondBoundary` 0.825s 绿**（第三方补验见 QA-03 第 6 条）。**未覆盖**：`internal/server` 全量 1389s 超 20m 窗口、前端 `pnpm test:console`/`build`、真实端到端 |
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
