# 待决策与裁决

记录我暂按默认值推进、或已自行裁决的事项。**09:07 起口径变更（用户 m01597：任务都没完成，要你决策不是我决策）**：能由我决定的就在这里裁决并写理由，只有真的需要凭据、访问权或产品事实时才去找用户。已决事项保留裁决与理由，不删除。

## 已决

| 编号 | 事项 | 决定 | 时间 |
|---|---|---|---|
| D1 | `.worktrees/agent-resource-sync` 半成品（27 文件）怎么处置 | 用户：worktree 整理合并，清理，后边主线开发 → 保全为 `rescue/*` 分支、清 19 条 worktree、18 个 merged 分支 | 00:44 |
| D2 | 是否长期采用三本账（backlog / pm-worklog / pending-decisions） | **09:07 改为采纳并固化**：写进 `AGENTS.md`（Topic Ownership 两行 + 新增 `## Operations Ledgers` 一节）。理由：三本账已经连续 41 轮巡检实际在用，靠记忆维持等于没约定；早先「怕与上游模板冲突」的顾虑不成立——本仓库 `AGENTS.md` 是自有契约文件，不跟随上游 | 09:07（原 00:39 为「先跑一轮」） |
| D3 | 产物 256 KiB 是否还要在 HTTP 响应层二次截断 | 否（落盘已截断，响应直接读文件）；保留观察，若出现响应体过大再议 | 01:10 |
| D4 | 产物 256 KiB 是每文件还是单条合计 | 每文件（artifact.md/artifact.json/raw.txt 各自 ≤256 KiB），spec §3.2 补措辞，不回改 SF-03 代码 | 01:05 |
| D5 | SF-05 前端被另一条会话占用（`files-panel.tsx`、`console-views.ts`、`files-maintenance.ts`、`globals.css`、`CHANGELOG.md` 在写） | 限定接管：只碰 `inspection-artifact-viewer.tsx`、`skill-registry-panel.tsx`、`lib/demo.ts`、新增 `lib/monitor-metrics.ts`(+test)；禁改其余前端文件 | 01:26 |
| D6 | legacy native strategy 双轨（SF-06 报告第 9 行）是保留还是移除 | 移除（SF-09）：spec §1.1 要单一真源；若某采集能力 Skill 包没有，先把缺口写进 spec §2.4 再退役旧路径，不静默删能力 | 01:26 |
| D7 | 用户要求每个任务彻底闭环 | 落成 `docs/operations/closure-standard.md` 七条，所有派活模板加「拥有/禁改文件 + 验收命令 + 四段式提交 + 闭环出口」 | 01:26 |
| D8 | SF-11 真实隧道端到端验收要不要做、用哪个 target | **09:07 改为做，且不再依赖外部 target**：改用本机进程自建 Gateway↔Agent 链路（真 HTTP/WS 编解码，不用真集群、不用 canary 凭据），已派 dev-e2e-local 执行。理由：原先卡在「没有可用 target 和凭据」，但隧道两端都是本仓库代码，自建链路能覆盖同一批契约，真正的缺口只剩「外部集群的网络/凭据差异」，那部分另行标注未覆盖 | 09:07 |
| D9 | ssh_host 巡检能力缺 AI 可见的受管 SSH 通道（`StructuredSSH` 不在 AI 白名单，`tool_registry.go:45`） | **维持不给**：AI 永不直连 SSH，ssh-host 包在未授予受管通道时输出 `status=warning` + reason。理由：开放等于给 AI 一条可执行任意命令的远程通道，属安全边界变更，收益（一个巡检包）与风险不匹配；要开放须单独威胁建模并进 T2 验收 | 09:07 |
| D11 | 巡检改造（Skill-First）随哪个版本对外发布 | **认账**：巡检整批算下个版本内容，切版时把 `## [Unreleased]` 整段收成该版本条目一次写全，**不回改已发布的 0.3.29/0.3.30 条目**（CHANGELOG 约定已发布条目不再改写；0.3.30 仍在 `[Unreleased]` 的是「说明未发、代码已发」，下个版本写清即可）。附加规则：往 CHANGELOG 顶部追加条目前必须 `git tag --list v<版本>` 确认该版本 tag 尚不存在 | 09:07 |
| D12 | MySQL 存储模式还没接线，剩余方言改写要不要继续 | **继续做**，顺序 3A→3D→3C→3B，但每条批次都必须带「接线当天也不能靠运气」的护栏（双库同判的真机断言、反向实验、golden 归零不许放宽）。理由：今天为零执行不等于可以放着，接线那天会一次面对 95 处 + 已改 42 处的回归面，而那时才是没有守卫、没有上下文的时刻；现在做，守卫已在 CI 里跑，边际成本低。INT-17 已按此派出 | 09:07（原 05:05 为「暂停」） |

- D10（02:09 已自行决定，留档）：13 个 `TEXT NOT NULL` 列在 MySQL 侧无法写默认值（表达式默认被判 `DEFAULT_GENERATED`、字面量默认语法错），选择**运行层显式给值**而非改 `VARCHAR`。依据是 `gateway_mysql_schema.sql` 头部既有约定；若将来有列必须允许「未提供」语义，改回 `NULL` 而不是给 `''`（`kubernetes_available` 的先例）。

## 待决

当前没有需要用户拍板的事项。需要用户输入的只有两类：一是凭据或访问权（外部集群 target、canary 凭据），二是产品选择（某功能要不要做、做成什么形态）。其余由我裁决并记录在上面。

## D11 详情与裁决依据

- 现状：外部会话把 `v0.3.29`（`771755ea`，02:44）与 `v0.3.30`（`d7c2bcf1`，03:48）先后切出版本，两版的 tag 祖先都包含巡检改造，但 `CHANGELOG.md` 把这些条目整段留在 `## [Unreleased]`。结果是代码随版本走、更新内容不随版本走，跨了两个版本。
- 裁决（09:07）：按认账处理。巡检改造的更新内容在下个版本条目里一次写全，包括「产物 File-First」「对比与趋势」「保留策略」「8 个内置 Skill 包」等已实现项；0.3.29/0.3.30 的条目保持原样不动。
- 收口（10:39）：外部会话 `74af39e6`（远端最终为 `47946471`）发布 0.3.31，把 `[Unreleased]` 整段收成 `## [0.3.31]`，条目内容正是这批巡检改造（Skill 包驱动、对比与趋势、产物 File-First、`artifact_state`、图表、保留策略、`series_points` 桥接）。D11 的「代码先走版本、说明后补」到此消除，裁决关闭。观察：`git tag --list` 当时只有 `v0.3.3`/`v0.3.30`，`v0.3.31` 尚未打；附加规则（追加条目前先确认该版本 tag 不存在）继续有效。
- 我 04:01 造成、04:45 已修的反向问题（`84b57653`）：把 INT-13 的 Fixed 条目误写进 `## [0.3.30]`，而该版本 tag 早于 INT-13 代码。教训已固化为上面 D11 的附加规则。

## D12 详情与裁决依据

- 事实（已核实）：`backend/agent/internal/server/gateway_store_mysql.go:18-19` 注释明写 `It does not select an alternate store for the running Gateway`；`openGatewayMySQL`（同文件 :75）除测试外无调用方；`OpenGatewayStore`（`backend/agent/internal/server/gateway_store.go:32`）只打开 SQLite。MySQL 目前只服务离线迁移工具。
- 含义：INT-13/14/15 已改的 42 处、INT-16 分类出的剩余 95 处，今天没有一行在 MySQL 上跑。价值是「将来接线时不会出错」。
- 裁决（09:07）：继续做，理由见上表。INT-16 给出的批次顺序与反向实验构想保留为施工图（`docs/reviews/2026-11-24-mysql-dialect-batch-plan.md`）。
- 批次进度：3A 时间/授权（julianday 3 + unixepoch 4）**已关单**（INT-17，`2139e26c` + 我补的 `6a4cbd2e`/`6f978c5a`；真机 139 PASS/2 SKIP）；3D 引号/守卫（reserved_use 12 等）→ 3C 参数重排（numbered_parameter 4 必须单独真机行值断言）→ 3B JSON（语义差最大）待 3A 关单后依次派。`materialized_cte` 单列，不并批。
