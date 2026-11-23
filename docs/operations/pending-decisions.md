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
- 更新（04:45，事实已变）：v0.3.30 已于 03:48 打在 d7c2bcf1，Release 产物 04:03:47+08 发布（annotated tag，v0.3.30^{} = d7c2bcf1）。这一版把 Devin 的两条修复（分层观测键 2d5e0492、清单钉选降级 d7c2bcf1）写进 ## [0.3.30]，两条都是 tag 祖先，写法正确；但巡检整批仍在 ## [Unreleased]，即 0.3.30 的镜像与 CLI 已含巡检改造而更新内容不提，D11 的原问题再次成立，且现在跨了两个版本（0.3.29、0.3.30）。
- 同步发现并已修的反向问题（84b57653）：我 04:01 给 INT-13 补的「MySQL 冲突处理改 ON DUPLICATE KEY UPDATE」那条 Fixed，被我写进当时最上面的 ## [0.3.30]，而 0.3.30 的 tag（03:48）与 INT-13 的代码（524a69bd，04:16）时间错位——那条说明描述的是**不在 0.3.30 里**的代码，下一个版本还会静默继承它。已原样移回 ## [Unreleased]。教训：往 CHANGELOG 顶部追加条目前必须确认该版本标题对应的 tag 尚不存在（git tag --list v<版本>、git tag --contains），否则整条都应进 [Unreleased]。
- 因此选项 ③ 已部分失效（0.3.30 已发、巡检代码已在两版镜像里）。剩下的现实选择是①（认账，巡检改造算下个版本内容，并在下个版本条目里一次写全）；② 已不可能（0.3.30 已发布，不能回改其条目）。建议按①，切下个版本时把 [Unreleased] 整段收成该版本条目——仍需你点头。

## D12 MySQL 存储模式还没接线：剩余的方言改写要不要继续做？

- 事实（已核实，不是推测）：`backend/agent/internal/server/gateway_store_mysql.go:18-19` 的注释明写 `It does not select an alternate store for the running Gateway`；`openGatewayMySQL`（同文件 :75）除测试外没有任何运行期调用方；`OpenGatewayStore`（`backend/agent/internal/server/gateway_store.go:32`）只打开 SQLite。也就是说 MySQL 目前只服务**离线迁移工具**（导出/搬运/schema 校验），运行中的 Gateway 永远走 SQLite。
- 含义：INT-13/14/15 已改的 42 处、以及 INT-16 分类出的剩余 95 处，**今天没有一行在 MySQL 上跑**。这些改动的价值是「将来接线时不会出错」，不是「现在正在出错」。INT-16 的 95 条分类里真正今天会执行的是 0 条。
- 因此我暂停了 INT-16 计划里的四批（3A 时间/授权 7 处、3D 引号/守卫、3C 参数重排、3B JSON），没有派活——避免在没有落点的路径上继续堆改动与测试。
- 需要你定的：① 继续做（认为 MySQL 存储迟早接线，先把 dialect 层铺平，成本是每批 0.5–1 小时）；② 暂停方言层，只在真接线时再做（风险：接线那天会一次面对 95 处 + 已改 42 处的回归面）；③ 干脆不做 MySQL 存储，把 `gateway_store_mysql.go` 与相关迁移脚本标注为实验性/冻结，并从 backlog 移除这类任务。
- 我的建议：②，但加一个低成本护栏——保留现有 golden 审计与 parity 守卫（它们已在 CI 里，零额外成本），不再新增方言改动。理由是已经改的 42 处已经有守卫兜住，剩下 95 处接线时可一次性处理，而且接线本身会带来一轮必须做的真机验收（那时才是有意义的验证时机）。
- 未决期间：不再派 MySQL 方言批次；`docs/reviews/2026-11-24-mysql-dialect-batch-plan.md` 保留作为接线时的施工图。

