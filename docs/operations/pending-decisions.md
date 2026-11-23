# 待用户决策

记录需要用户拍板、我暂按默认值推进的事项。默认值写在这里，用户改口就改这里。

## 已决

| 编号 | 事项 | 决定 | 时间 |
|---|---|---|---|
| D1 | `.worktrees/agent-resource-sync` 半成品（27 文件）怎么处置 | 用户："worktree 整理合并，清理，后边主线开发" → 保全为 `rescue/*` 分支、清 19 条 worktree、18 个 merged 分支 | 00:44 |
| D2 | 是否长期采用三本账（backlog / pm-worklog / pending-decisions） | 是，先跑一轮；不写进 AGENTS.md（避免与上游模板冲突），等用户明确要求再固化 | 00:39 |
| D4 | 产物 256 KiB 是每文件还是单条合计 | 每文件（artifact.md/artifact.json/raw.txt 各自 ≤256 KiB），spec §3.2 补措辞，不回改 SF-03 代码 | 01:05 |
| D5 | SF-05 前端被另一条会话占用（`files-panel.tsx`、`console-views.ts`、`files-maintenance.ts`、`globals.css`、`CHANGELOG.md` 在写） | 限定接管：只碰 `inspection-artifact-viewer.tsx`、`skill-registry-panel.tsx`、`lib/demo.ts`、新增 `lib/monitor-metrics.ts`(+test)；禁改其余前端文件 | 01:26 |
| D6 | legacy native strategy 双轨（SF-06 报告第 9 行）是保留还是移除 | 移除（SF-09）：spec §1.1 要单一真源；若某采集能力 Skill 包没有，先把缺口写进 spec §2.4 再退役旧路径，不静默删能力 | 01:26 |
| D7 | 用户要求"每个任务彻底闭环" | 落成 `docs/operations/closure-standard.md` 七条，所有派活模板加"拥有/禁改文件 + 验收命令 + 四段式提交 + 闭环出口" | 01:26 |

## 待决

| 编号 | 事项 | 我的默认做法 | 影响 |
|---|---|---|---|
| D3 | 产物 256 KiB 是否还要在 HTTP 响应层二次截断 | 否（落盘已截断，响应直接读文件） | 目前无问题 |
| D9 | ssh_host 巡检能力缺"AI 可见的受管 SSH 通道"（`StructuredSSH` 不在 AI 白名单，`tool_registry.go:45`） | 保持现状：AI 永不直连 SSH，ssh-host 包在未授予通道时输出 `status=degraded`+reason；要开放须单独威胁建模 | 开放等于给 AI 一条可执行任意命令的远程通道，属安全边界变更，不随 SF-09/SB-02 顺手做 |
| D8 | SF-11 真实隧道端到端验收要不要做、用哪个 target | 不做，除非用户给出可连 target 与 canary 凭据；报告里如实标"未覆盖" | 平台唯一受管 SSH 通道 `doops_ssh` 不在 AI 可见白名单，端到端需人工授权 |

- D10（02:09 已自行决定，留档）：13 个 `TEXT NOT NULL` 列在 MySQL 侧无法写默认值（表达式默认被判 `DEFAULT_GENERATED`、字面量默认语法错），选择**运行层显式给值**而非改 `VARCHAR`。依据是 `gateway_mysql_schema.sql` 头部既有约定；若将来有列必须允许"未提供"语义，改回 `NULL` 而不是给 `''`（`kubernetes_available` 的先例）。

## D11 巡检改造（Skill-First）随哪个版本对外发布？

- 现状（02:46）：外部会话已把 tag `v0.3.29` 打在 `771755ea`，其祖先包含我们整批巡检改造（SF-01…SF-16、INT-05、INT-08、SF-14）；但 `CHANGELOG.md` 把这些条目整段留在 `## [Unreleased]`，0.3.29 条目只写了审计清理两条修复。结果是**代码随版本走、更新内容不随版本走**。
- 需要你定的：① 承认现状——巡检改造算作下个版本内容，0.3.29 的更新内容就只写审计修复（控制台「更新内容」不会提到巡检改版，但功能已在镜像里）；② 或补写进 0.3.29（需要改已发布条目的归属，按 CHANGELOG 约定「已发布条目不再改写」应尽量避免）；③ 或把巡检改造的整体发布推迟到 SF-11（真实 Gateway↔Agent 端到端）与 SF-17（前端 missing 态）都绿之后再切版本。
- 我的倾向：③ 最稳，但这件事已经不完全在我手上——tag 已由外部会话推出，若要维持「更新内容与镜像一致」，最省事的是①加一句说明。
- 未决期间我不改 `CHANGELOG.md`、不动 tag。

