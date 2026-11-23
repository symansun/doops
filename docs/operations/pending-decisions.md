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
