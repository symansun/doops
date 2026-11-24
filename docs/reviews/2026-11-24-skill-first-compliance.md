# SF-06 Skill-First 架构符合性核验报告

- 核验对象：`backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md` §1–§5 的落地情况（HEAD `282d338cd9ad267b3724ef7898264d67e74a4023`）
- 核验方式：**只读**。业务代码一律未改；核验在 `git archive HEAD | tar -x -C /tmp/sf06` 的干净树中执行（避免共享工作区里 SF-04/SF-08 的未提交改动干扰），二进制/embed/测试证据均绑定该 HEAD sha。
- 结论计数：**符合 13 项 · 部分符合 6 项 · 不符合 0 项 · 未实现（进行中）3 项 · 未覆盖 1 项**
- 核验方法概述：源码逐条定位（file:line）+ 真实 `go test`（含自写夹具测试）+ 真实二进制 `go build` 后字节级 embed 清点 + `testdata/skills_builtin_verify/verify.sh` 全量容器实测。

---

## 一、结论

已进 main 的部分整体成立：单一真源（后端）、包结构/entrypoint、执行形态判定、产物 File-First（落盘/索引/回读/回退/清理/脱敏/256 KiB 信封）均与 spec 一致，并有真实测试与容器实测背书。发现 6 处"部分符合"，无"不符合"：其中 2 处是**前端仍保留 `builtin_strategy`/`strategy_id` 死开关**、1 处是 **legacy native strategy 兼容路径仍可达**（均为设计决策面，需 lead 定夺），1 处是 **spec 与实际注入的环境变量名不一致**，1 处是 **proxy-evasion 端口表超集 / ssh-host 未指名受管通道**。§4.2/§4.3 按 lead 说明属 SF-04 在开发，如实记为"未实现（进行中）"。

---

## 二、逐条核验表

| # | Spec 条款 | 代码/文件:行 | 判定 | 证据命令 |
|---|---|---|---|---|
| 1 | §1.1 Skill-First 单一真源 | 后端：`DriverMode`/`StrategyID` 仅剩 `skills_builtin_packages_test.go:229` 的"不存在"断言；`driver_mode`/`strategy_id` 仅 `gateway_store_schema.go:361-362` 历史 DDL 列，无任何 SQL 读写（`skills_registry.go:254/267/309/425/449/541/569/580/601`）。前端/兼容路径见第 9 行 | **部分符合** | `grep -rn "DriverMode\|StrategyID" --include=*.go backend/agent/internal/server`；`grep -rn "builtin_strategy" components lib` |
| 2 | §1.2 MCP 工具层 | `mcp_agent.go:92` 生成 `doops-mcp-servers.json`（与 doagent settings 同目录）；`:278` `probeMCPServer` 真实 JSON-RPC 2.0 `initialize`+`tools/list` 握手（stdio `:400/:441-442`，http `:514-520`）；`mcp_registry.go`、`k8s_mcp_stdio.go:92-104`（同协议 stdio server）配套；`skills_registry.go:606/885` 提供 `reset_system` 恢复内建包 | 符合 | `grep -rn "tools/list\|initialize" mcp_agent.go mcp_registry.go k8s_mcp_stdio.go` |
| 3 | §1.3 File-First（Skill 包本体） | `writeSkillFilesToDisk`（`skills_registry.go:330-360`）0600 + 原子 rename；`writePlatformSkillStorageDir` 落盘并写 `storage_dir` 索引列；`skills_agent.go:105-111` Agent 侧写 0755 脚本 | 符合 | `go test ./internal/server -run TestPlatformSkillsCRUDPermissionsSyncAndInspectionDriver -v` |
| 4 | §1.4 凭据脱敏 + 256 KiB | `monitor_artifact.go` 的 `sanitizeAndCapArtifact`/`sanitizeSecretText` 在落盘与信封层同时生效；`TestSF06ArtifactOwnFixture` 注入 canary 后 md/raw/JSON 三处均无明文 | 符合 | 见第三节 T-2/T-3 |
| 5 | §2.1 目录结构（8 包） | `git ls-tree -r HEAD backend/agent/internal/server/skills_builtin`：8 个 slug 目录，6 包 `SKILL.md+scripts/inspect.sh`，database 仅 `SKILL.md`+frontmatter `required_mcp: dosql`，ssh-host 仅 `SKILL.md`；SKILL.md 均 100644、6 个 inspect.sh 均 100755 | 符合 | `git ls-tree -r HEAD backend/agent/internal/server/skills_builtin` |
| 6 | §2.2 frontmatter/entrypoint/embed | 6 包声明 `entrypoint: scripts/inspect.sh`（k8s-cluster 另有 `required_mcp: doops`）；无包声明 `exec_mode`（默认 auto）；`//go:embed all:skills_builtin`（`skills_builtin_packages.go:21`）；`_embedcheck/README.md` 为流水线证明夹具且 `skills_builtin_packages_test.go:21-23` 显式断言其必须被嵌入 | 符合 | 见第三节 T-1/T-4 |
| 7 | §2.3 执行形态 auto\|script\|agent | `resolveSkillEntrypoint`(:171)/`resolveSkillExecMode`(:186)/`resolveSkillDispatchMode`(:203)：entrypoint 空→agent；`exec_mode=agent`→agent；auto+有自然语言 prompt→agent；否则 script；script 显式但无 entrypoint 时回升探测 `scripts/inspect.sh`，仍无→agent（不静默丢弃）。8 slug 全部产出 item 由 `TestAllBuiltinSkillsProduceRunnableItems` 实证 | **部分符合**（环境变量名不一致） | 见第三节 T-5/T-6 |
| 8 | §2.4 8 包能力与 spec 一致 | 6 脚本包实测产出 status/summary/metrics/checks；database 正文指名 `mcp_dosql_read` AI 驱动；ssh-host 无 `required_mcp`。残留：proxy-evasion 的 `PROXY_PORTS` 含 spec 未列的 20171（超集，正常服务占用会误报）；ssh-host 未在包内指名受管 SSH 通道 | **部分符合** | `grep -rn "PROXY_PORTS\|20171" skills_builtin/inspect-proxy-evasion` |
| 9 | §1.1 前端/兼容路径残留 | 前端：`components/skill-registry-panel.tsx:127/:330/:899-902/:907`、`components/inspection-center-panel.tsx:241`、`lib/demo.ts:1995/2110-2111/2125/2153/2180/2231/2386-2387/2416/2449/2479/2503/2526/2547/2569/2729-2732` 仍读写/模拟 `driver_mode`/`strategy_id`（后端已不读该字段→死开关）。兼容路径：`monitor_engine_agent.go:98-120` `monitorStrategyCatalog` + 6 个原生采集器（:156/:889/:1284/:1755/:1867/:2088）、`monitor_engine_runner.go:508-560`（`plan.Strategies` 非空时经 `dispatchAgentMonitorInspect`+`buildBuiltinStrategyArtifact(:539)`）、`monitor_engine_http.go:140/:344` 暴露 `strategies`、`gateway_store_schema.go:263` DDL 默认值 | **部分符合**（需 lead 决策） | `grep -rn "monitorStrategyCatalog\|strategies_json" *.go` |
| 10 | §3.1 产物目录布局 | `monitor_artifact_store.go:144` `writeMonitorArtifactFiles` 按 日期/run/item 写 `artifact.md`/`artifact.json`/`raw.txt`，0600 + sha256；夹具实测目录布局符合 | 符合 | T-2 |
| 11 | §3.2 SQLite 索引列 | `gateway_store_schema.go:489-496`：`artifact_format/artifact_markdown/artifact_json/raw_output/artifact_dir/artifact_bytes/artifact_sha256/artifact_truncated` | 符合 | `grep -n "artifact_dir\|artifact_sha256" gateway_store_schema.go` |
| 12 | §3.3 写入规则（落盘优先、失败回退） | `monitor_engine_store.go:396` `persistMonitorRunItemArtifact`：落盘成功→写索引列且 SQLite 文本列留空；落盘失败→回落文本列。`TestMonitorArtifactFallsBackWhenStorageDisabled` 覆盖 | 符合 | T-3 |
| 13 | §3.4 回读 + 老行回退 | `GetMonitorRunDetail`(`monitor_engine_store.go:479`) 文件优先、缺文件回退 `artifact_markdown/artifact_json` 文本列。`TestMonitorArtifactMissingFilesFallBackToDatabase` 覆盖 | 符合 | T-3 |
| 14 | §3.5 保留期清理删盘 | `PruneMonitorEngineHistory`(`monitor_engine_store.go:1037`) 30 天同时删行+删盘（`monitor_artifact_store.go:229`）；夹具 `PruneMonitorEngineHistory(now)` 实测目录消失 | 符合 | T-2 |
| 15 | §4.1 输出协议枚举 | 真跑 6 脚本：`status` 仅 ok/warning/critical/error、`summary` 非空字符串、`metrics[]`/`checks[]` 均为数组、无 `skipped`；JSON 可由 python3 解析 | 符合 | 见第三节 T-7 |
| 16 | §4.2 指标时序 | 全仓无 `inspection_metric_points` 表/写入、无指标点 API | **未实现（进行中）** | `grep -rn "metric_points\|metric_deltas" backend` |
| 17 | §4.3 与上次对比 | 无 `previous_run_id/added_checks/resolved_checks` 字段 | **未实现（进行中）** | 同上 |
| 18 | §4.4 前端可视化器 | `components/inspection-artifact-viewer.tsx:348` 具备 visual/Markdown/JSON 三态 + `DynamicJsonNode` 通用渲染 + 导出；但 Diff/Trends 依赖 §4.2/§4.3，charts 内置 SVG 原语未见 | **部分符合**（受 §4.2/§4.3 阻塞） | `grep -rn "DynamicJsonNode\|inspection-artifact-viewer" components` |
| 19 | §5 红线1 真实执行 | 6 脚本包在真实容器 + 真实 HTTP `POST /v1/monitor/runs`→runner→Agent 执行链上全部产出 item（CollectorMethod `skill_script`×6 / `agent_skill`×2），但 Agent 为进程内假 Agent，非真实 Gateway↔Agent 隧道 | **部分符合** | 见第三节 T-5/T-7 |
| 20 | §5 红线2 去重 | `verify.sh` 4 个去重夹具（`dedup_mount/dedup_overlay_shim/dedup_overlay_lowerdir/dedup_virtual_approx`）全 PASS；QA-01 独立夹具亦验证同盘双挂载点不翻倍 | 符合 | T-8 |
| 21 | §5 红线3 File-First | 同上第 10–14 行 | 符合 | T-2/T-3 |
| 22 | §5 红线4 趋势/对比 | 依赖 §4.2/§4.3，不可达 | **未实现（进行中）** | 同第 16 行 |
| 23 | §5 红线5 端到端 | 需真实 Gateway + 真实 Agent 双模式端到端，本环境无隧道/实网 | **未覆盖** | — |

---

## 三、证据记录（命令 + 实际输出摘要）

**T-1 包结构 / mode**
```
$ git -C <repo> ls-tree -r HEAD backend/agent/internal/server/skills_builtin
100644 SKILL.md ×8；100755 scripts/inspect.sh ×6（9a91baff 已修正可执行位）
```
embed 指令：`skills_builtin_packages.go:21  //go:embed all:skills_builtin`；loader `loadBuiltinSkillPackage`(:99-160) 缺 SKILL.md/entrypoint 时 fail closed。

**T-2 自写夹具测试（/tmp/sf06，不入库）**
```
$ cd /tmp/sf06/backend/agent && env -u GOROOT go test ./internal/server -run 'TestSF06' -v
--- PASS: TestSF06EmbedInventory (0.00s)
--- PASS: TestSF06ArtifactOwnFixture (0.56s)
ok github.com/user/doops/agent/internal/server
```
`TestSF06EmbedInventory`：walk `builtinSkillPackagesFS`，断言 8 个 `SKILL.md` + 6 个脚本、无 `_verify/testdata`/隐藏段。
`TestSF06ArtifactOwnFixture`：自造 `maxInspectionArtifactBytes+2048` 的 markdown/JSON/raw，canary=`Bearer QA06CANARYTOKEN123456`；实测 SQLite 三文本列均为空、`artifact_truncated=1`、目录为 日期/run/item 布局、md/raw 截断并脱敏、JSON 为合法信封 `{"truncated":true,"original_bytes":N,"limit_bytes":262144}`、`GetMonitorRunDetail` 透明回读、`PruneMonitorEngineHistory(now)` 同时删行删盘。

**T-3 既有 File-First 测试套件**
```
$ cd /tmp/sf06/backend/agent && env -u GOROOT go test ./internal/server -run \
    'TestBuiltinSkillPackagesEmbedDiskIndexAndReset|TestBuiltinSkillDispatchIsPackageDriven|TestAllBuiltinSkillsProduceRunnableItems|TestMonitorArtifact' -v
--- PASS: TestAllBuiltinSkillsProduceRunnableItems (6.48s)
--- PASS: TestBuiltinSkillDispatchIsPackageDriven (0.00s)
--- PASS: TestBuiltinSkillPackagesEmbedDiskIndexAndReset (0.42s)
--- PASS: TestMonitorArtifactFallsBackWhenStorageDisabled (0.06s)
--- PASS: TestMonitorArtifactFilesFirstEndToEnd (0.05s)
--- PASS: TestMonitorArtifactMissingFilesFallBackToDatabase (0.08s)
--- PASS: TestMonitorArtifactPruneRemovesFiles (0.07s)
--- PASS: TestMonitorArtifactTruncationAndRedaction (0.55s)
--- PASS: TestSF06ArtifactOwnFixture (0.56s)
--- PASS: TestSF06EmbedInventory (0.00s)
ok github.com/user/doops/agent/internal/server 9.252s
```

**T-4 二进制级 embed 清点（不引用 `_embedcheck` 之外的任何声明，自建 Gateway 二进制实测）**
```
$ cd /tmp/sf06/backend/gateway && env -u GOROOT go build -o /tmp/sf06/gatewaybin .   # 38 MB
# 对二进制做字节计数（python3）：
b'skills_builtin/inspect-vm-host/scripts/inspect.sh' = 1
b'skills_builtin/inspect-ssh-host/SKILL.md'          = 1
b'skills_builtin/_embedcheck/README.md'              = 1
b'build-time fixture owned by the Go'                = 1
b'skills_builtin/_verify'                            = 0
b'skills_builtin/testdata'                           = 0
```
另：`cmd/agent` 二进制（47 MB）不含 `skills_builtin` 字符串——该角色不链接 Gateway 存储层，属正常，非缺陷。

**T-5 8 slug 全量 item 实测**
`TestAllBuiltinSkillsProduceRunnableItems` 经真实 `POST /v1/monitor/runs`（`DO_AGENT_URL` 指向死端口、`DOOPS_AGENT_SKILLS_ROOT` 临时目录、假 Agent）传 8 个 slug：8 个 item 全部产出，`AssetKind=skill`、`Strategy=skill:<slug>`、`CollectorMethod=skill_script`(6)/`agent_skill`(2)、evidence 含 `skill_slug`+synced=true、AI 路径 `reason=skill_synced_ai_unavailable`（不静默丢弃）。

**T-6 §2.3 环境变量名比对**
```
$ grep -rn "DOOPS_CLUSTER\|DOOPS_INSTANCE\|DOOPS_TARGET_CLUSTER\|DOOPS_TARGET_INSTANCE" backend | grep -v _test
spec: DOOPS_CLUSTER / DOOPS_INSTANCE
code: monitor_engine_runner.go:938-953 注入 DOOPS_SKILL_DIR / DOOPS_SKILL_SLUG /
      DOOPS_TARGET_CLUSTER / DOOPS_TARGET_INSTANCE / DOOPS_RUN_ID
scripts: 仅 inspect-app-web/scripts/inspect.sh:125 消费 DOOPS_INSPECT_URLS
```
即 spec 表与代码注入名不一致；因脚本未消费这两个变量，实际影响低，但 spec 作为唯一真源应澄清。

**T-7 verify.sh 全量容器实测（后台 job `bash-714c178bdced44d0`，exit 0）**
```
$ cd /tmp/sf06 && sh backend/agent/internal/server/testdata/skills_builtin_verify/verify.sh
SB-01 校验开始：python3 镜像=doops-t2-agent:v0.3.21；无 python3 镜像=alpine:3
8 包 static 全 PASS；6 脚本包容器真跑全 PASS；
红线2 去重夹具 4/4 PASS；运行时异常路径夹具 2/2 PASS（runtime_stats / runtime_stats_fail）
合计 20 项：通过 20，失败 0
SB-01 VERIFY_ALL_PASS
```

**T-8 reboot 后的去重独立复核**
QA-01 已用 loop device + `mount --bind` 独立构造同盘双挂载点，`disk_total_bytes` 未翻倍；本次 `verify.sh` 的 4 个去重夹具再次全 PASS（含 overlay 同源、lowerdir 近似、虚拟文件系统近似）。

**T-9 前端残留 grep（判定缺口）**
```
$ grep -rn "builtin_strategy" components lib
components/inspection-center-panel.tsx:241  # 仅当 driver_mode==="builtin_strategy" 才登记 strategy
components/skill-registry-panel.tsx:127/330/899-902/907  # 表单默认值、提交 driver_mode/strategy_id、文案"内置策略直采（Go 原生高速采集）"
lib/demo.ts: 类型定义与 20+ 处 demo 数据/渲染
```
后端已无读写 → 该下拉为死开关，demo 仍模拟旧双轨。

---

## 四、部分符合项的证据与修复建议（交 lead 派活，本次未改）

1. **前端 `builtin_strategy`/`strategy_id` 死开关**（§1.1）
   - 影响：控制台仍展示"内置策略直采"，但后端不会再按该字段分派；demo 仍模拟旧行为，易误导后续开发者与文档读者。
   - 建议：删除 `skill-registry-panel.tsx` 的表单字段/下拉、`inspection-center-panel.tsx:241` 分支、`lib/demo.ts` 相关类型与样例；spec 第 4 行把 Web 控制台纳入适用范围，前端属整改面。
2. **legacy native strategy 兼容路径仍可达**（§1.1）
   - 影响：当 `monitor_schedules.strategies_json` 非空（历史行或显式传 `strategies`）时，runner 仍走 Go 原生采集器（6 个），而非 Skill 包；`monitorStrategyCatalog` 仍经 HTTP 暴露。
   - 证据：`monitor_engine_runner.go:508-560`、`monitor_engine_agent.go:98-120/156/889/1284/1755/1867/2088`、`monitor_engine_http.go:140/:344`、`gateway_store_schema.go:263`。
   - 建议：明确"兼容期"边界（只读旧行、禁止新建携带 strategies）并加断言/日志；或给出迁移路径后移除 catalog。纯属设计决策，不做判定为"不符合"。
3. **环境变量名 spec↔code 不一致**（§2.3）
   - 建议：以 spec 为唯一真源，把 §2.3 的 `DOOPS_CLUSTER/DOOPS_INSTANCE` 修正为代码实际注入的 `DOOPS_TARGET_CLUSTER/DOOPS_TARGET_INSTANCE`（或反向改代码并让脚本消费），二者取一，消除歧义。
4. **proxy-evasion 端口超集 / ssh-host 通道未指名**（§2.4）
   - `PROXY_PORTS` 含 spec 未列端口 20171，正常服务占用会误报；建议收紧为 spec 列表或注明来源与误报处置。
   - ssh-host SKILL.md 未声明 `required_mcp` 也未指名受管 SSH 通道；若平台唯一受管通道是 `doops_ssh`（QA-01 已记录其不在 AI 可见白名单），需在 spec §2.4 表格注明"依赖 AI 自身 SSH 能力"或补通道声明。
5. **§4.4 第 1/2 层可视化未闭环**：待 SF-04 落地 §4.2/§4.3 后补 Diff/Trends；本次不判不符合。

---

## 五、覆盖不到的范围（如实记录）

- **§5 红线5 真实 Gateway + 真实 Agent 双模式端到端**：本机无隧道/实网环境，未覆盖；本次 E2E 为进程内假 Agent + 真实 HTTP/runner 路径。
- **§4.2/§4.3**：SF-04 开发中，未进 main，未实现。
- **真实 Kubernetes 集群 / 真实远程主机**：容器内只跑到降级/warning 分支，未做真机只读采集。
- **Web 控制台浏览器实测**：严格只读且前端由他人高频提交，未构建/未浏览器验证（前端结论均来自源码 grep）。
- **冻结 HEAD 之后的改动**：本报告证据绑定 `282d338cd9ad267b3724ef7898264d67e74a4023`；之后他人提交未纳入。
- `/tmp/sf06/backend/agent/internal/server/zz_sf06_verify_test.go`（自写核验测试）仅存于临时干净树，**未入库**，业务代码与 skills_builtin 未做任何修改。

---

## 六、提交

- 报告文件：`docs/reviews/2026-11-24-skill-first-compliance.md`
- 提交方式：`git commit -o docs/reviews/2026-11-24-skill-first-compliance.md`（单文件）
- 提交 hash：见本文件所在提交（`.git` 侧生成后回填于交付消息）
- Spec: backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md §1-§5
- Tests: `env -u GOROOT go test ./internal/server -run 'TestSF06|TestBuiltinSkill|TestAllBuiltinSkillsProduceRunnableItems|TestMonitorArtifact' -v` → 10 PASS / 0 FAIL；`sh backend/agent/internal/server/testdata/skills_builtin_verify/verify.sh` → 20 PASS / 0 FAIL（`SB-01 VERIFY_ALL_PASS`）
- Co-Authored-By: qa-skill-01

---

## 七、lead 复核（2026-11-24 03:00，HEAD 2a29ed67）

本报告证据绑定在 282d338c（11-23 傍晚），之后 SF-05/SF-09/SF-17 与外部会话的提交已进 main。逐项在 2a29ed67 上复核：

| 原编号 | 复核结论 | 证据 |
|---|---|---|
| #1 前端 `builtin_strategy`/`strategy_id` 死开关 | **仍在，但只剩一个文件** | `components/skill-registry-panel.tsx`、`lib/demo.ts` 已无命中；仅 `lib/skills-registry.ts:16`:17（类型字段）：57-59（`SKILL_DRIVER_LABELS`）：219-220（upsert payload）。后端 `grep driver_mode backend/agent/internal/server` 零命中 → 纯死代码。已派 **SF-21**（dev-viewer-01）清理并加「旧概念不得复活」静态守卫 |
| #2 legacy native strategy 兼容路径可达 | **已消除** | `monitor_engine_agent.go` 文件不存在；`grep 'plan.Strategies\|monitorStrategyCatalog'` 零命中；只剩 `gateway_store_schema.go:255` 的退役注释、`:264` 的 DDL 默认值，以及 `monitor_engine_store.go` 把 `strategies_json` 当退役列原样读写（不参与分派） |
| #3 spec↔code 环境变量名不一致 | **已修正** | spec 第 83/84 行为 `DOOPS_TARGET_CLUSTER` / `DOOPS_TARGET_INSTANCE`，与注入/脚本消费一致 |
| #4a proxy-evasion 端口超集（20171） | **已消除** | `skills_builtin/inspect-proxy-evasion/scripts/inspect.sh:109` 的 `PROXY_PORTS` = {1080,3128,7890,7891,8118,10808,20170}，与 spec 第 118 行七端口逐项一致 |
| #4b ssh-host 未指名受管通道 | **已补齐** | `skills_builtin/inspect-ssh-host/SKILL.md` 指名 `doops_ssh`（clientCallable）为唯一受管通道、白名单当前仅 `mcp_doops_k8s_read`/`mcp_dosql_read`、退化分支写 `managed_ssh_channel=unavailable`；spec 第 132 行记该能力缺口并注明另立任务 |
| #18 §4.4 可视化器 | **基本闭环** | SF-05 的「与上次对比」「历史趋势」与 SF-17 的 `artifact_state=missing` 已进 main；`components/inspection-artifact-viewer.tsx:546` 有内联 SVG 趋势线。仍缺「charts 原语抽象」这一层，如实保留为欠账，不与本轮混做 |
| #19 §5 红线5 真实隧道 e2e | **本机真实隧道已覆盖**（真集群仍不覆盖） | SF-11 用本机进程把 Gateway store + 真 ws 端口 + 真 Agent 运行时接起来跑通一次 `inspect-vm-host` 巡检，四组反向实验与断言变异验证见 §8.5；需 kubeconfig/RBAC 的 `inspect-k8s-cluster`、受管 SSH/网站巡检与 Helm 凭据链路仍不在覆盖内（§8.2） |

结论修正：原报 6 项「部分符合」中，**4 项（#2/#3/#4a/#4b）在当前 HEAD 已消除**，#1 收敛为单文件死代码并转 SF-21，#18 基本闭环仅剩 charts 抽象。真正未覆盖的只剩「真实 Gateway↔Agent 隧道端到端」一项，卡在凭据而非代码。

---

## 八、SF-11 本机真实隧道 E2E 可行性判定（2026-11-24）

**结论：红线 5 可以只在本机闭合，不需要真实 target、canary 凭据或 Kubernetes 集群。** 缺的不是凭据，而是一段把「真 Gateway 服务端 + 真 Agent 隧道客户端」装到同一台机器上的测试装配；生产代码无需改动。

### 8.1 逐段判定

| 环节 | 生产实现 | 本机可跑 | 依据 |
|---|---|---|---|
| 目标定义 | 目标 = (cluster, instance)，只要求两者非空 | ✅ | `agent_registration.go:55-60`；不要求 kubeconfig、k8s 版本或云凭据 |
| Agent 注册 | `/v1/agent/connect` 带 token 时强制 `VerifyAgentEnrollment(token, cluster, instance)` | ✅ | 同文件 `:73`，token 绑定精确目标；失败 401 `agent_enrollment_auth_failed`（`:157-165`） |
| 用户授权 | `UserCanUseTarget` 在 POST /v1/monitor/runs 校验 | ✅ | `monitor_engine_http.go:452-455`，无权 → 403 `monitor_target_forbidden` |
| 隧道（Gateway 侧） | `AgentRegistrationHandler.ServeHTTP` → `upgrader.Upgrade` → `AgentRegistry` | ✅ | `agent_registration.go:87`、`tunnel_hub.go:290`；真 websocket 升级 |
| 隧道（Agent 侧） | `(*Gateway).ServeGatewayWebSocketConnWithReady` | ✅ | `handler_ws.go:20`；与 `cmd/agent` 的 `runReverseTunnel` 调用同一函数 |
| 下发 | Skill 包经 `doops_skill_sync` 落到 `DOOPS_AGENT_SKILLS_ROOT`，再经 `doops_shell` 执行 | ✅ | `skills_agent.go:40`、`skills_registry.go:1013`；脚本模式不经过 AI |
| 执行 | `inspect-vm-host/scripts/inspect.sh` 只需 `sh` + `python3` | ✅ | 对 `/proc` 缺失降级为 warning，不伪造 0；macOS 实测 exit=0 |
| 回传/读回 | 结果落 SQLite，指标投影 `inspection_metric_points`，产物写 `<dbDir>/artifacts` | ✅ | GET run detail 重算 `artifact_state`（`monitor_engine_store.go:505`）；GET metrics 读投影表 |

Gateway 入口 `backend/gateway/main.go` 只是 `gatewaycmd.Run` 的 11 行包装，没有 e2e 专属逻辑；本机用 `httptest.Server` 承载 hub 的完整路由表（真 TCP 端口、真 HTTP/WS 编解码），不需要另起监听端口。

### 8.2 必须真集群/真外部资源、本测试不覆盖

- `inspect-k8s-cluster`：`scripts/inspect.sh` 调 `kubectl`，需 kubeconfig + RBAC + 可达 apiserver。
- 远程主机类（`doops_ssh` 受管通道 / ssh-host）与网站巡检：需真实网络、真实凭据。
- Helm/exec 变更与 credential materialization：需已部署 target 与 canary 凭据（T2 范围）。
- 容器模式（dispatcher docker）：本机 Docker 可用但测试不依赖；脚本模式不需要容器。

### 8.3 最小可跑组合

1. 临时 SQLite store + `GatewayHubOptions{RequireAgentEnrollment: true}` 的 GatewayHub + `httptest.Server`（真端口）。
2. `CreateAgentEnrollment("dev","local")` 真 token；Agent 侧 `NewGateway()` + `ServeGatewayWebSocketConnWithReady` 带该 token 连 `ws://…/v1/agent/connect?cluster=dev&instance=local`。
3. operator 用户 + `ScopeGrant{Cluster:"dev", Instance:"local"}`；POST `/v1/monitor/runs` `{"targets":[{"cluster":"dev","instance":"local"}],"skills":["inspect-vm-host"]}`。
4. 断言：`collector_method=skill_script`、`evidence.synced=true`、checks/metrics 非空、产物文件与 sha256 匹配、GET detail `artifact_state=present`、GET `/v1/monitor/metrics` 能按 run_id 读回。

依赖只有本机 `sh` 与 `python3`（GitHub `ubuntu-latest` 自带），无外部服务，可进现有 Go 测试范围。

### 8.4 本机承载的边界（如实）

- Gateway 与 Agent 在同一测试进程内，WS 仍是真 socket；跨机网络分区、Agent 重连退避（`cmd/agent` 的 `runReverseTunnel` 职责）不在覆盖范围。
- 不断言宿主数值：`inspect-vm-host` 的 disk 检查可能本来就是 critical；断言只针对结构、状态机与证据链，不针对具体百分比。

### 8.5 实测证据（2026-11-24，macOS 本机）

测试文件：`backend/agent/internal/server/gateway_tunnel_e2e_test.go`（package `server`，与生产代码同包只共享内部测试助手，不绕过隧道）。

命令与真实输出（`env -u GOROOT` 规避 shell 里指向旧 1.26.2 的 GOROOT）：

```
$ cd backend/agent && env -u GOROOT go test ./internal/server -run 'TestGatewayTunnelE2E' -v
=== RUN   TestGatewayTunnelE2ESkillRunRoundTrip
    gateway_tunnel_e2e_test.go:233: run=run_35bc002281c61824 status=critical item=critical checks=5 metrics=9
--- PASS: TestGatewayTunnelE2ESkillRunRoundTrip (0.76s)
=== RUN   TestGatewayTunnelE2EOfflineTunnelFailsFast
    gateway_tunnel_e2e_test.go:275: offline dispatch returned in 65.497833ms with status=failed reason=target_offline
--- PASS: TestGatewayTunnelE2EOfflineTunnelFailsFast (0.46s)
=== RUN   TestGatewayTunnelE2EEnrollmentAndGrantRejection
--- PASS: TestGatewayTunnelE2EEnrollmentAndGrantRejection (0.43s)
=== RUN   TestGatewayTunnelE2EArtifactMissingAfterDeletion
--- PASS: TestGatewayTunnelE2EArtifactMissingAfterDeletion (0.76s)
ok  	github.com/user/doops/agent/internal/server	3.375s
```

正向链路（用例 1）断言的证据链：`run.offline_targets=0`、状态 ∈ {success,warning,critical}（本次 critical，因宿主磁盘 95%）、item 非 offline/error 且 `reason` 为空、asset/strategy 绑定、`collector_method=skill_script`、`evidence.synced=true`、entrypoint 非空、5 条 checks 状态合法、产物文件字节数与 `sha256` 与回传一致、`GET /v1/monitor/metrics` 按 `run_id` 读回 9 个键值、`GET /v1/monitor/runs/<id>` 的 run 与 item `artifact_state=present`、detail 里的产物 payload 指标与 POST 返回逐键相等。

四组反向实验（都能失败，不是走过场）：

| 实验 | 做法 | 期望与实测 |
|---|---|---|
| 隧道断开后下发 | 先注册成功，再 `stopAgent()` 关 WS 并等 hub 注销，然后 POST（客户端超时放宽到 15s 以暴露挂住） | 65.5ms 内返回 `status=failed`、`offline_targets=1`、item `offline`/`target_offline`、存在 `agent.online` critical 检查；断言耗时 < 10s，挂住即失败 |
| 未授权 target | 只有 `dev/local` grant 的用户下发 `dev/other` | HTTP 403 且 body 含 `monitor_target_forbidden`；随后 `GET /v1/monitor/runs` 仍为 0 条（拒绝发生在建 run 之前） |
| 未授权 target 注册 | 用 `dev/local` 的 enrollment token 拨 `dev/other` 的 `/v1/agent/connect` | 握手 HTTP 401、body 含 `agent_enrollment_auth_failed`，hub 中不存在 `dev/other` |
| 产物被删 | 先校验产物落盘，再 `os.RemoveAll` 整个 `item_1` 目录 | detail 的 run 与 item `artifact_state` 均为 `missing`，checks 保留，`/v1/monitor/metrics` 仍能按 `run_id` 找到点（指标落库不被产物丢失抹掉） |

断言可失败的直接证据（变异验证）：把用例 4 的两处 `monitorArtifactStateMissing` 改成 `monitorArtifactStatePresent` 后单跑该用例：

```
--- FAIL: TestGatewayTunnelE2EArtifactMissingAfterDeletion (0.70s)
    gateway_tunnel_e2e_test.go:357: run artifact_state = "missing" after deleting
      .../artifacts/2026-11-24/run_013ee9372f6dd84f/item_1, want "present"
FAIL	github.com/user/doops/agent/internal/server	1.899s
```

改回后全绿，说明断言确实绑定 API 返回值而不是恒真。

CI：`.github/workflows/ci.yml:40-42` 已在 `backend/agent` 跑 `go test -timeout 20m ./...`（ubuntu-latest + Go 1.25.14，`python3` 预装），本用例无外部依赖，自动纳入现有 Go 测试范围，**不新建 job、不改 ci.yml**。

一处如实记录的契约细节（非阻塞、未改生产代码）：Skill 运行的 `monitor_run_items.metrics_json` 恒为空——全仓无代码给 `item.Metrics` 赋值（SF-09 已退役原生采集器，只剩 `monitor_engine_store.go:276`/`:557` 的读写列），前端也无人读该字段（`components/` 下无 `.metrics.` 访问）。Skill 指标的真实回读路径是产物 payload + `inspection_metric_points` + `GET /v1/monitor/metrics`，测试据此断言 detail 内产物 payload 与 POST 返回等价，而不是断言这个 vacuous 字段。

未覆盖（明确保留）：`inspect-k8s-cluster` 的 kubectl/kubeconfig/RBAC、受管 SSH 与网站巡检的真实网络与凭据、Helm/exec 变更与 credential materialization（§8.2）。这些都需要真集群或真凭据，不是本机进程能替的。
