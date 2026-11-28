# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 与语义化版本。

本文件是控制台「更新内容」的数据源：Web 镜像构建时从仓库根打包 `CHANGELOG.md`，控制台读取其中最新带版本号的条目作为当前版本。`[Unreleased]` 收敛尚未发布的变更，发布时整段改成 `## [版本号] — YYYY-MM-DD`，已发布条目不再改写；版本条目按版本号从新到旧排列（控制台按版本号排序读取，文件顺序只影响阅读）；0.1.0–0.2.2 为回填的历史记录。

分类标题只使用 `### Added` / `### Changed` / `### Deprecated` / `### Removed` / `### Fixed` / `### Security`（可选 `### Performance`）；条目以 `- ` 开头，续行缩进；允许行内 `code`、**粗体** 与 Markdown 链接，不要使用表格或 HTML。

## [Unreleased]

### Changed

- 全站前端彻底框架化重构（标准 **shadcn/ui + Radix UI + Tailwind CSS v4 + TanStack Table v8**），消除手写 CSS 组件、任意 `px` 布局与硬编码色值：
  - **框架底座与标准设计令牌**：新增标准 `components.json`，引入 `@radix-ui/react-{slot,select,label,checkbox,switch}` 与 `@tanstack/react-table`；`app/globals.css` 恢复 Tailwind v4 标准 `rem` 字号标尺（仅保留语义 `text-2xs`）并补齐 OKLCH `--sidebar-*` 令牌，移除 250+ 行手写 `.btn`/`.input`/`.segmented`/`.page-tab`/`.checkbox`/`.status-*` CSS 类；`app/console/console.css` 清除全部硬编码 hex 色值与手写侧栏样式，仅保留 `@agent-cloud/agent-ui` 作用域适配；
  - **统一组件原语体系**：全面补齐 `Button`（CVA + `asChild`）、`Input`/`Textarea`/`Label`、`Checkbox`/`RadixCheckbox`/`Switch`、Radix `Select`（替代手算 `getBoundingClientRect` 像素定位并保持 `FormData` / AI 表单辅助兼容）、`Sheet`、`Sidebar`、`DataTable`、`PageHeader`/`PageSection`/`StatCard`/`FilterBar`/`EmptyState`、`Skeleton`、`PortalContainer`，并删除重复的 `components/ui/modal.tsx`；
  - **官网公开门户全页重构（`/`、`/cases`、`/docs`、`/blog`）**：全站统一接入 `<PortalContainer>`、`<Button asChild>` 与 Radix `<Sheet>` 移动端抽屉，统一页头、首页、案例、文档侧栏+正文+目录、博客与页脚左右对齐基准，博客文章页补齐右侧吸顶目录，修复 `/blog` 与 `/docs` 在 `1024px` / `390px` 视口下的换行与横向溢出；
  - **控制台与管理后台全页统一（`/console`、`/console/admin`）**：侧栏与移动端菜单升级为 `<Sidebar>` + Radix `<Sheet>`，修复控制台移动端顶栏折行与溢出；控制台终端/AI 分栏抽离为可键盘调节的 `<SplitResizeHandle>`；全站裸 `<table>`/`<select>`/`<textarea>`/非文件 `<input>` 与手写 `role="switch"` 全部清零，迁移至 `@/components/ui` 原语；核心台账、插件中心、资源监控与平台监控页面全面接入 `<PageHeader>`、`<FilterBar>`、`<EmptyState>`、`<Skeleton>`、Radix `<Switch>` 与 TanStack `<DataTable>`（支持列排序与分页），彻底清除全站任意 `[...px]`/`[...rem|em|%]` 与裸色阶/硬编码 hex，并收紧全站框架契约守卫测试防止回退。

## [0.3.78] — 2026-11-28

### Changed

- 优化 Gateway 公网 Proxy 终态审计落库路径并补齐断链重连与真实 MySQL 并发回归门禁（`GW-01`，零数据库 Schema 变更）：
  - **单次原子终态审计**：Proxy 请求改为通过 `recordProxyFinish` 记录去敏终态并统一交由 `finishActiveOperation` → `FinishExecutionAudit` 在单次数据库事务内完成 `audit_events` 更新、`DOOPS_AUDIT_STORAGE=file` File-First 日志写入与 `execution.finished` Webhook 入队，消除每请求的重复终态写库（保留同步落盘、去敏与终态一致性语义）。
  - **断链重连与真实 MySQL 并发覆盖**：新增 Proxy `-target-reconnect-grace` 重连恢复/超时、在途断链写方法 `outcome_unknown`/读方法 `error`、File-First 单次写入去重以及真实 MySQL 8.4 并发写/读与断链重连集成测试（自动纳入 CI MySQL 8.4 门禁）。

### Fixed

- 修复 Gateway `WebhookDelivery` 结构体缺少 JSON tag 导致 `GET /v1/admin/webhooks/<id>/deliveries` 与 `doops admin webhook deliveries` 返回 PascalCase 字段的后端 API 契约缺陷（`GW-01`）：为 11 个投递字段统一补充稳定 `snake_case` JSON tag，新增 HTTP 契约测试（覆盖 401/403、空列表 `[]`、成功/失败字段集合与脱敏约束）并更新 [DOOPS_API_GUIDE.md](backend/docs/DOOPS_API_GUIDE.md)。
- 修复 Agent 断线重连后旧代际在途 Proxy 写操作与新代际同路由 Proxy 写操作在 `operationsConflict` 触发误判 `409 Conflict (target busy)` 的并发缺陷（`GW-01`），同时严格保留 `ActionAgentUpgrade` / `target:` 升级隔离围栏。

## [0.3.77] — 2026-11-28

### Added

- 新增跨模块零 Mock「真实 CLI ↔ 真实 Gateway ↔ 真实 Agent」三进程 Loopback 端到端契约门禁（`REM-01`）：新增 `backend/test/test_cli_gateway_agent_e2e.py` 并接入 `.github/workflows/ci.yml` `Go tests`（Go 1.25.14），真实编译并启动三端进程覆盖 `doops push --storage gateway`（`--dry-run`、首次推送与增量同步）、`doops exec --raw`/`--quiet` 纯 JSON、`doops pull --storage gateway` 往返、`doops clean` 复用 `-session` 与缺省 fail-closed、以及本地反代注入 `502`/`503` 握手重试与 `401` 零重试校验。
- 舰队版本漂移与离线目标观测治理（`REM-04`，零数据库 Schema 变更）：
  - **Gateway `/v1/platform-monitor`**：`agent_summary` 新增 `gateway_release_commit`、`drifted_online` 与 `offline_total`，节点明细新增 `version_drifted` 派生标记；
  - **CLI `doops targets`**：多目标列表检测到存在不同 `RELEASE_COMMIT` 或 `<unknown>` 时在末尾输出 `VERSION DRIFT:` 汇总提示（单目标输出逐行保持不变以兼容 `deploy-gateway.sh`）；
  - **Web「Agent 运行监控」**：版本一致性卡片展示相对 Gateway 基线 commit 的漂移台数与离线总数，节点列表新增「全部 / 版本漂移 / 离线」快捷筛选与漂移徽标（向后兼容旧 Gateway）。

### Changed

- 协议层分离命令数据流与控制面进度并统一 CLI 纯输出契约（`REM-02`，新旧 Agent/CLI 双向向后兼容）：
  - **控制面进度与流式终态分离**：Agent 心跳改发独立 `kind: "progress"` 通知（`progressNotification`），`doops_shell`/`doops_ssh`/`doops_node_info` 成功终态携带 `structuredContent: {"kind": "doops.command-result", "exitCode": 0, "streamed": true}`（保留 `content[0].text = "Operation complete."` 兼容旧客户端）；Gateway `RunInternalToolCall` 与 CLI `CallAndCapture`/`Call` 优先识别 `kind: "progress"` 与 `structuredContent.streamed` 避免终态文案与心跳污染命令 stdout，同时保留对旧 Agent 的兼容降级。
  - **统一纯输出模式**：为 `doops ssh` 与 `doops k8s` 补齐 `--raw` / `--quiet` 纯输出选项（与 `doops exec` 对齐，抑制 `[TARGETING]` Banner、心跳与壳层结束文案并将错误/调试日志导向 `stderr`）。

### Fixed

- 修复 Git 传输路径（`gateway.go`、`push.go`、`pull.go`）硬编码 `/root/ws` 忽略 `DOOPS_WORKSPACE_ROOT` 以及 `init.defaultBranch=main` 主机上 bare 仓库 `HEAD` 未出生导致 `post-receive` `git checkout -f HEAD` 静默失败的缺陷（`REM-01`）。
- 透明化 `helmPull` 及同名凭据多匹配歧义诊断（`REM-03`）：`authorizedHelmCredential` 完整遍历全部同源匹配项并以确定性顺序列出公开非敏感候选 `name [cred_id]`（严禁回显口令、Token、URL 或 socket 路径），同步补齐 `credential_resolve.go` 授权可见候选清单与 CLI `askCredentialNext` 对 `helm_credential_binding_ambiguous` 的可执行 `--credential` 指引。

### Security

- 公开仓库合规收口与防回流守卫：移除仓库中误留存的内部开发过程台账（`docs/operations/`）、阶段审查与设计草稿（`docs/reviews/`、`backend/docs/reviews/`、`docs/design/`、`HELM_GATE_CLEANUP.md`）以及具体集群割接记录（`backend/deploy/instances/`、`backend/deploy/fleet-20261116/`），清洗保留规范中的本机绝对路径，并在 `.gitignore` 与 `backend/test/test_stale_contracts.py` 中新增防回流 CI 门禁。

## [0.3.76] — 2026-11-28

### Added

- 新增管理员「出站 Webhook」控制台（`WEB-07`）：在「平台管理」接入现有 `/api/admin/*` → `/v1/admin/*` 合同，支持端点列表、创建、编辑启停、删除、Secret 轮换、测试投递与投递记录查询；创建和轮换后的 Secret 仅在弹窗中展示一次，关闭即清除，列表只展示主机与 URL hash。
  - **状态与错误呈现**：投递记录归一化 Gateway 返回的字段并展示等待中、发送中、已送达、失败；尚无 HTTP 响应时响应列显示 `—`，不虚构状态码。失败按稳定错误类别映射为中文提示，同时保留 HTTP 状态，不把上游原始响应正文、完整 URL 或 Secret 写入错误消息。
  - **只读与权限边界**：演示会话仅提供空列表并禁用变更；非管理员由 Gateway 拒绝，页面展示明确的权限提示。见 [Web 控制台说明](backend/docs/DOOPS_WEBUI.md#管理员出站-webhook-页签web-07)。

### Changed

- 重构插件工作台产品形态，从「巡检脚本跑批器」升级为 **内容优先的 3 页签领域工作台（`PLG-04`）**：
  - **去除跑批壳子与内部开发术语**：清理插件页头部的 `系统 L2 插件` / `核心内置 L2+ 插件` / `skill.*` 等内部术语及前置「3 张脚本直跑预设卡 + 执行范围与控制参数 + 暂无历史巡检记录」结构；统一采用 `实时工作台` / `运维操作` / `策略与报告` 三页签布局，顶栏提供节点切换、实时采集、AI 分析与报告导出。
  - **领域专属实时工作台与首次进入自动采集**：为「GPU 算力治理」新增专属 `<GpuGovernanceWorkbench>`（4 列算力 KPI、单卡算力/显存进度条/温度/功耗/ECC 状态卡、活跃占卡进程表与疑似空闲占卡自动标注，点击卡或进程直达运维操作台）；带自声明资产目录的外部插件（如 DNS Zone / 邮件域）在「实时工作台」首屏置顶呈现受管资产目录；零 Token 且无必填凭据的只读插件在首次进入时自动执行一次实时采集，不再呈现空白历史记录框。
  - **收口侧栏冗余巡检脚本**：将 8 个与内置模块重叠的 `inspect-*` 巡检脚本调整为 `sidebar.enabled: false`（保留在自动化、目标页签与首页专项监测中），侧栏「场景与扩展」只呈现真正独立的领域工作台（GPU 算力治理、主机安全治理、外部暴露面扫描、应用挖掘与打标）及管理员按需安装的外部 `.zip` 插件（如 DNS / 邮件）。
- 清理控制台首页冗余文字提示并围绕「全网运行指挥台」重构首页工作台（`WEB-07` / `WEB-08`）：
  - **清理冗余提示与重复入口**：彻底移除首页「按使用场景精简你的工作台 / 默认兼容态 / 选择场景包」引导横幅、与左侧导航重复的「当前场景 · 已启用插件」快捷按钮条及右栏重复纯文字链接，场景与插件管理统一收口至「插件中心」。
  - **单行指挥页头、一键全网体检与均衡 4 列 KPI**：顶栏收敛为 `运行总览` + 实时状态胶囊与 **「一键全网体检」** 快捷触发（直接调度已启用的系统巡检 Skill 并自动刷新指标）；4 列 KPI 清晰区分在线节点运行形态分布（`K8s / Linux / Docker`，不再与「K8s 集群台账」口径打架）、健康与告警、全网资源水位（3 条 CPU / 内存 / 磁盘微进度条，未采样时提供「立即体检」闭环且极简模式自动回退活跃会话）与任务调度；存在异常时按需浮现可折叠的「待处理异常」聚焦区（同目标阈值告警合并实时 CPU/内存快照防重复，默认展前 3 项，健康时零占位）。
  - **高密度「实时节点工作台」与业务上下文融合**：默认提供紧凑高密度节点表并支持「列表 / 分组」视图切换（持久化），融合机器台账业务/技术栈标签与实采 CPU/内存水位，支持按类型与节点/标签/IP 搜索，行内悬停显露「AI / 终端 / 监控」快捷动作；右侧「核心资产规模」按启用模块自适应展示机器台账（含打标覆盖率与待归属提醒）、K8s 集群与命名空间、业务系统及受管网站/数据库数量；「近期动态」支持「操作审计 / 巡检记录」双视图，补齐操作人、`cluster/instance` 与全部记录直达（普通用户全局审计 403 自动默认展示可见巡检记录）。
  - **数据优先的「专项能力监测」**：`<HomePluginWidgetGrid>` 保留无硬上限分批查询与自定义显隐，全未采集时压为紧凑单行快捷进入条，已有实采数据时按严重度排序渲染实时指标卡，彻底消除大片「尚未采集巡检数据 / 暂无数据」空卡片占位。

## [0.3.75] — 2026-11-28

### Fixed

- 修复 `backend/Dockerfile` 在 `PLG-03` 剥离 `dodns`/`doemail` 为外部插件包后仍残留构建期脚本校验（`/app/skills/dodns/...`、`/app/skills/doemail/...`）导致 `doops-agent` 镜像构建失败的缺陷，并在 `backend/test/test_ci_workflow_contract.py` 增加 `backend/Dockerfile` 与 `skills/agent/` 文件存在性自动对账契约守卫，彻底杜绝构建脚本与内置 Skill 目录漂移。

## [0.3.74] — 2026-11-28

### Changed

- 升级 GitHub Actions 与 CNB 双轨 CI/CD 质量门禁、缓存加速与发布预检流程：
  - **三模块 Go 门禁与跨平台编检前置**：`.github/workflows/ci.yml` 的 `gofmt -l .` 与新增 `go vet ./...` 覆盖全部 3 个 Go 模块（`backend/agent`、`backend/gateway`、`backend/cli`），并补齐 `darwin/arm64` 与 `windows/amd64` CLI 交叉编译冒烟及全部 7 个 CI job 的 `timeout-minutes` 保护；顺手修正 `backend/cli/cli/doops/detached_test.go` 未排序 import。
  - **全链路依赖与镜像构建缓存**：Web job 接入 `pnpm store`（通过 `pnpm store path` 权威解析）与 `.next/cache` 缓存，Contract tests 接入 pip 缓存，Skill packages 接入 Docker Buildx GHA 缓存。
  - **CNB 流水线版本对齐与去重**：`.cnb.yml` 移除废弃 `master` 分支冗余流水线，消除硬编码 `pnpm@11.7.0` 漂移并改为统一读取 `package.json` `packageManager`（兼容 Alpine 无 `corepack` 回退），补齐三模块 `gofmt`、`pnpm test:dosql` 与阶段超时。
  - **只读发布预检与契约守卫**：新增 `backend/scripts/release-preflight.sh` 与 `backend/test/test_ci_workflow_contract.py`，同步更新 [backend/docs/BUILD_DEPLOY_SOP.md](backend/docs/BUILD_DEPLOY_SOP.md)。

## [0.3.73] — 2026-11-28

### Added

- 交互终端实时回显 AI 执行命令与会话工作目录对齐（`TERM-09`）：
  - **交互终端实时可见 AI 执行命令与输出**：在「AI 助手」右侧交互终端分栏与「终端 + AI 协同」双入口中默认开启「AI 回显」（支持顶栏一键开关并持久化），AI 执行的脱敏 Shell 命令在运行中于终端顶部显示实时执行状态条（支持一键跳回「执行过程」查看完整轨迹），完成后自动以边框块无损回显到 `xterm.js` 画布并保留/重建提示符与 OSC 133 锚点；严格剥离 OSC/清屏/备用屏等危险控制序列，并在用户正在打字或处于全屏程序（`vim`/`top`）时自动排队待提示符空闲后刷入，不打断人工输入。
  - **Agent PTY 会话工作目录对齐**：`doops_terminal` `open` 在当前会话的 AI 工作区目录（如 `/root/ws/<session>`）已存在时优先直接落在该目录并返回真实初始 `cwd`，使人工接管终端与 AI 会话保持同一工作目录，目录不存在或会话 ID 非法时安全回退默认 `/workspace`。

## [0.3.72] — 2026-11-28

### Added

- 完善全栈配置文件模板与环境变量权威说明（`CONF-01`）：
  - **单一权威环境变量参考与文档中心接入**：新增 [backend/docs/CONFIGURATION.md](backend/docs/CONFIGURATION.md) 并在 Web 文档中心（`/docs/configuration`）、`AGENTS.md`、`README.md` 与 `backend/docs/README.md` 建立索引，逐项写明 Gateway、Agent、Web 控制台、CLI 与内部自动注入变量的默认值、合法取值、生效机制与 fail-closed 安全边界。
  - **开箱即用配置模板与 `.gitignore` 防护**：新增 `backend/deploy/config/doops-gateway.env.example`、`backend/deploy/systemd/doops-gateway.service.example`、`backend/deploy/config/doops-agent.env.example` 与根目录 `.env.example`，补齐 `.gitignore` 对 `.env` / `.env.local` 的防误提交忽略规则（保留 `*.env.example` 模板），并优化 `backend/deploy/helm/doops-agent/values.yaml` 环境变量映射注释。
  - **源码-文档-模板自动化契约守卫**：新增 `backend/test/test_env_config_contract.py`，在 CI 中自动核对所有生产源码读取的环境变量均已收录于 `CONFIGURATION.md`、模板语法与变量名全部有据可查且零私有地址/密钥泄露。
- 增强 CLI 自动化可用性（`CLI-01`）：
  - **`doops exec --raw` / `--quiet` 纯输出模式**：默认关闭以保持交互输出兼容；开启时抑制彩色 `[TARGETING]` Banner、`[agent] ⏳ 命令后台执行中... (耗时 Xs)` 心跳进度及壳层结束文案并将 CLI 错误/调试日志导向 `stderr`，使 `stdout` 仅保留远端命令原始输出，可直接供 `jq` / `json.loads` 消费。
  - **`doops clean` 默认复用显式 `-session`**：未传 `--workspace` 时自动复用显式传入的 `-session`（显式 `--workspace` 仍优先）；既未传 `-session` 也未传 `--workspace` 时继续 fail-closed 报错拒绝，防止误删工作区。

### Fixed

- 修复 `doops push --storage gateway` 推送落盘成功后仍返回非零退出码的缺陷（`CLI-01`）：`waitForWorkspaceReady` 探测 `.doops-ready` 时原先未输出尾随换行，导致 `CallAndCapture` 追加的 `doops_shell` 终态文本 `Operation complete.` 与提交哈希拼接为 `<commit>Operation complete.` 触发提交不匹配；同步修复 `validateWorkspaceCheckoutProof` 对单个尾随 `Operation complete.` 终态行的严格剥离，并将 `doops push` 错误输出统一写入 `stderr`，在保持全部提交哈希与 manifest 校验不削弱的前提下恢复正常退出码。
- 修复 `Gateway /v1/rpc` WebSocket 建连握手遇到反代/重启瞬断返回 `502`/`503`/`504` 或连接重置时零重试直接中断流水线的问题（`CLI-01`）：仅在任何 RPC/tool 尚未发出的幂等握手阶段对 `502`/`503`/`504`（每次重试显式关闭 `resp.Body`）与瞬态 EOF/连接重置执行有界指数退避重试（5 次尝试、总退避窗口约 4.5s）；`400`/`401`/`403`/`404`/`409`/`500` 等非瞬态错误保持零重试直接返回真实状态码，且绝不重放已发出的远端调用。

## [0.3.71] — 2026-11-28

### Changed

- 完成 **真插件化架构解耦与外部全栈插件底座升级（`PLG-03`）**，将 DNS（`dodns`）与 Email（`doemail`）彻底剥离出 DoOps 核心二进制、Agent 基础镜像与核心前端，升级 DoOps Core 通用零 DDL 插件原语使任意外部领域插件免改内核代码即装即用、卸载即清：
  - **零 DDL 声明式插件资产目录（`catalog_schemas[]`）**：`plugin.yaml` 新增 `catalog_schemas[]`（支持 `domain`/`host`/`ip`/`port`/`label`/`enum` 字段强校验，严禁含 `secret`/`password`/`token`/`key` 等敏感命名键），复用 `inventory_resource_catalog` 以 `kind = "plugin:<slug>:<key>"` 动态读写；核心资产目录彻底移除硬编码的 `dns_zone` / `mail_domain`，系统插件执行时自动注入 `DOOPS_PLUGIN_*` 环境变量，前端 `<InlineCatalogManager>` 完全按 `manifest.catalog_schemas` 动态渲染表单与卡片。
  - **插件自带独立 MCP Server（`mcp_server`）与按插件工具隔离**：`plugin.yaml` 新增 `mcp_server` 声明（支持 `${SKILL_DIR}/<rel>` 相对脚本路径，强阻断内核 `doops`/`dosql` 抢占、`sh`/`bash` 命令与路径逃逸），管理员安装、启停或卸载系统插件时由 Gateway 自动在 `platform_mcp_servers` 完成注册、开关同步与清理；Agent 侧安全展开 `${SKILL_DIR}` 并经 `customSyncedMCPToolsForServer(skill)` 将外部插件 `apply` 会话严格隔离至「自身 MCP + 受审计 `Bash`」，`doagent_tool_evidence.go` 同步支持外部插件 MCP 完成与失败证据判定。
  - **插件中心 `.zip` 一键安装/卸载与 `plugins/dodns/`、`plugins/doemail/` 独立插件包**：后端 `GET /v1/plugins` 新增 `builtin` 与 `can_delete` 标记（核心 12 个内置 L2 Skill 受 `isBuiltinSystemSkill` 保护不可删，外部安装的平台插件可由管理员完整卸载并自动清理 `platform_plugin_overrides` 与伴生 MCP）；「插件中心」新增 **「安装插件包 (.zip)」** 与 **「卸载」** 交互；删除内核中的 `dns_mcp*.go`、`mail_mcp*.go`、`skills_builtin/{dns,mail}-governance` 与 `skills/agent/{dodns,doemail}`，改为自包含独立插件包 `plugins/dodns/`（Python stdio MCP）、`plugins/doemail/`（Node stdio MCP）与打包脚本 `plugins/package-plugin.sh`。

## [0.3.70] — 2026-11-28

### Fixed

- 修复 MySQL 后端下资产监控时序详情接口 `GET /v1/assets/monitor`（含 K8s Pod `origin=k8s_top` 等多序列资产）系统性返回 `503 asset_monitor_unavailable` 及前端误报 Agent 离线的问题：
  - **Gateway 时序读取游标冲突修复与诊断日志**：`readAssetMonitor` 在读取 `asset_monitor_series` 后先完整收集序列元数据并显式关闭游标，再逐条执行 `readMonitorSeries`（同时在 `readMonitorSeries` 内先关闭样本游标再查询规则与告警），消除 `go-sql-driver/mysql` 同事务嵌套游标导致的 `driver: bad connection`；`HandleAssetMonitor` 对未归类内部错误补齐服务端错误日志，不再静默吞掉根因；
  - **前端 503 错误精准分流**：`/api/assets/monitor` 代理与 `AgentMonitor` 按错误码区分 `target_offline`、`container_runtime_unavailable` 与 `asset_monitor_unavailable`，不再把 Gateway 监控存储异常笼统提示为「目标 Agent 离线」。
- 修复控制台 `?view=admin` 深链静默回落首页的问题：`lib/console-url.ts` `VALID_VIEWS` 补齐 `"admin"` 并支持 `?view=admin&tab=<adminTab>` 解析，`ConsoleShell` 对平台管理员自动以 `replace` 跳转至 `/console/admin`（保留运行时参数与页签），对非管理员展示明确无权限提示与「返回首页」入口。
- 修复「机器台账」大列表一次全量渲染无分页的问题：新增客户端分页控件（默认每页 50 条，支持切换 20 / 50 / 100 / 200 条、页码跳转与详情返回恢复页码），并改为按当前可见页懒加载主机诊断样本，避免数百至上千台机器时 DOM 与并发请求过载。
- 优化「审计记录」分页状态提示与精确翻页判定：改为每次按 `PAGE_SIZE + 1` 探测下一页，在列表顶部与底部明确区分「已加载全部 N 条匹配记录（无更多数据）」与「还有更早记录（含加载入口）」，消除「每页 50 条，已加载 20 条」时的歧义。

## [0.3.69] — 2026-11-28

### Changed

- 调整 Gateway Agent 注册默认策略为**默认开放注册并允许未登记目标下发模型与受管凭据**（`AUTH-01`），打通新节点与本地开发 Agent 免预签发密钥的接入链路，同时保留可按需开启的严格密钥开关：
  - **默认开放与已登记目标保护**：不传开关时，`doops-gateway serve` 默认允许尚未确认过 enrollment token 的新 target 直接以 `cluster/instance` 接入并正常使用模型与受管凭据；已确认过 enrollment token 的存量 target 仍强制校验其专属密钥（无密钥或错误/跨目标密钥继续返回 `401 agent_enrollment_auth_failed`），防止生产节点被顶替。
  - **显式严格开关与部署脚本对齐**：新增 `DOOPS_REQUIRE_AGENT_ENROLLMENT=1` / `-require-agent-enrollment` 开启全量强制注册密钥校验，以及 `DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1` / `-require-enrolled-credential-delivery` 单独收紧凭据下发门禁（同时兼容旧开关 `DOOPS_ALLOW_OPEN_AGENT_REGISTRATION` / `DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY`，冲突参数或非法开关值 fail-closed 启动拒绝）；同步更新 `backend/scripts/deploy-gateway.sh`、`doops targets` 提示文案及全部权威文档。

## [0.3.68] — 2026-11-28

### Added

- Gateway 审计记录支持 **File-First 文档化存储**（`DOOPS_AUDIT_STORAGE=file`，零数据库 Schema 变更，兼容 SQLite 与 MySQL 5.7/8.0/8.4）：
  - **正文外置 + 每日文档落盘**：开启后自动将普通操作的大体积 `tail` 输出原子写入 `<DOOPS_GATEWAY_DATA_DIR>/audit/docs/YYYY-MM-DD/audit_<id>.txt`（`0600`），数据库 `audit_events.tail` 仅存约 100 字节的 `$audit_doc` 指针（保留 `agent:upgrade` 与维护回执内联）；同时每条终态审计记录实时追加为完整结构化 JSONL 文档至 `<DOOPS_GATEWAY_DATA_DIR>/audit/records/YYYY-MM-DD.jsonl`（启动与写入均 fail-closed）。
  - **透明回读与热冷无缝衔接**：`ListAuditFiltered`（Web 控制台「审计记录」、`GET /v1/audit` 与 CLI `audit list`）自动从本地文档还原 `tail` 正文，并在热库仅保留 24 小时轻量索引并清理后无缝回退读取本地历史 JSONL 文档（完整支持全部筛选与分页条件）。
  - **归档与分级回收增强**：`doops-gateway audit export` 导出时自动还原 `$audit_doc` 正文、在清理前幂等补齐事务内提交的审计行到本地文档层，清理后自动删除已归档单条 `tail` 文件，并新增 `-keep-doc-days` 自动清理过期本地审计文档。
- 升级全栈声明式能力插件底座（L2+，`CAP-01`）并内置整合三大 DoSuite 核心能力插件（`CAP-03`/`CAP-04`/`CAP-06`）、平台级根因评分与正式报告导出（`CAP-08`）、全量 24 Skill 产品设计矩阵与「机器部署分析与自动打标」（`CAP-10`/`CAP-11`）：
  - **L2+ 声明式插件底座与终端联动升级**：扩展 `plugin.yaml`（向后兼容 `doops.sh/v1alpha1`，继续严禁 Q3 自由文本 `text` 输入）建立通用声明式资产目录注入注册表（`plugin_catalog_inputs.go`，`inventory_resource_catalog` 零 DDL 扩展 `dns_zone` / `mail_domain` 并统一支持 `website_urls` / `website_domains` / `dns_zones` / `mail_domains` / `database_refs` / `image_refs` 仅向系统 Skill 单次批量注入，彻底消除按 Skill 硬编码胶水）、`capabilities.companion_agent_skill`、`ui.views` 新增 `resource_table` 与 `findings_list`、`ui.actions` 就地操作台支持「终端分栏协同」与 `<AgentRunOutput>` 命令块「填入终端」联动；`ask --apply` 同步开放受审计与策略约束的 `Bash` 工具并与交互终端共享工作区（Helm/网站/数据库仍严格保留结构化 MCP 变更证明，伴生运维 Skill 如 `dodns`/`doemail` 支持受审计 Bash 变更证据）；重设计首页 L2+ 卡片架构为无硬上限截断的可插拔 `<HomePluginWidgetGrid>`（后端 `/v1/monitor/metrics/latest` 支持 `?pairs=<skill>:<key>` 按对无饥饿查询，单批上限 64 对 / 24 Skill / 96 Key，前端自动分批并发拉取并支持分类筛选、自定义显示隐藏与展开收起）。
  - **受管 Opaque 凭据私有运行时物化与内置三大能力插件**：复用既有 `doops_credential_materialize`/`doops_credential_cleanup` 私有通道新增 `RuntimeEnvOnly`（`runtime-opaque`）一次性 `0600` 运行时凭据文件物化与退出覆写粉碎机制，使 AliDNS 等 API 类 `opaqueSecret` 凭据可直接通过 L2 `credential_ref` 安全注入（零明文入参/提示词/审计/工作区，兼容 Linux/Docker/K8s）；内置「DNS 基础设施治理（`dns-governance` + `skills/agent/dodns/`，零 `dig` 依赖纯 Python DNS Wire 协议 + 受管 AliDNS 运行时文件读取）」、「外部暴露面扫描（`attack-surface-scan` + `skills/agent/dosecurity/`，含 `.git/HEAD`、严格 `2xx` 与抗 HTML/SPA 误报守卫、零正文回显与脱敏版 DoSecurity 契约校验器 `dosecurity_contract.mjs`）」与「邮件服务治理（`mail-governance` + `skills/agent/doemail/`，仅从主干安全抽取、零明文密码、`passwordSecretRef` 强制校验）」；同步清理 `skills/agent/dosql/config/` 历史私有示例元数据并修复 `dosql-agent.test.mjs` 在非 ASCII 路径下的 URL 解码缺陷。
  - **平台级根因归并封顶评分与管理级正式报告导出**：新增纯函数评分引擎（`lib/inspection-scoring.ts`）与单文件自包含中文 HTML / 打印 PDF 报告渲染器（`lib/formal-inspection-report.ts`），支持同根因扣分封顶、非生产封顶、覆盖缺口诚实标注与敏感字段导出前质量审计，挂载至全部 `<SkillPluginHost>` 插件工作台与「自动化」产物报告工具栏。
  - **全量 24 个 Skill 产品设计体系、内置「机器部署分析与自动打标」与 14/14 系统 Skill 全面 L2+ 插件化（`CAP-10`/`CAP-11`）**：新增权威产品设计矩阵 `backend/docs/design/SKILL_PRODUCT_DESIGN_MATRIX.md`（覆盖 14 个平台内置巡检/发现/治理 Skill、9 个 Agent 侧运维 Skill 与 1 个操作员 CLI Skill 的信息架构、页面视图、资源表、交互动作、凭据与终端联动规范）；`plugin.yaml` 新增 `plugin.opt_in: true` 按需启用语义（「插件中心」展示「按需启用 / 按需未启用」标记，各场景默认关闭、管理员一键按需挂载且不阻断 AI 助手与 CLI）；新增内置 L2+ Skill `machine-workload-discovery`（零 Token 纯 Python + `/proc` 兜底交叉核验操作系统、MySQL/PostgreSQL/Redis/MongoDB/ES 等数据库及版本、Java/Python/PHP/Node/Go/.NET/Ruby 运行时及版本、Nginx/中间件及 JAR/`package.json`/`composer.json`/`wp-includes/version.php`/`pyproject.toml` 业务应用指纹，严禁读取 `.env` 等敏感配置并自动产出符合 `inventory_machine_tags` 校验规范的 `suggested_tags[]`，并在 `<SkillPluginHost>` 内置 `<MachineTagSyncCard>` 支持平台管理员预览差异后经受审计 `POST /api/assets/machines` 一键合并同步至机器台账）；同步为其余 8 个基础/专项系统 Skill 补齐 `plugin.yaml`、升级 `gpu-governance` 与 `host-security` 至 L2+，并将 `mail-governance` 设为按需插件（`opt_in: true`、不上首页、不预推每小时定时计划）。
  - **前后端资产目录闭环、批量主机部署挖掘打标与 DoDNS / DoEmail 原生结构化 MCP 通道（`CAP-02`/`CAP-05`/`CAP-07`）**：
    - 前端在 `<SkillPluginHost>` 内嵌声明式 `<InlineCatalogManager>`（当插件声明 `dns_zones` / `mail_domains` 时自动呈现，复用 `inventory_resource_catalog` 零新增侧栏菜单，支持就地登记/编辑/移除 DNS Zone 与邮件域并绑定目标 Agent，实时馈入 `DOOPS_CATALOG_DNS_ZONES` / `DOOPS_CATALOG_MAIL_DOMAINS`，同步覆盖 Demo 模式）；「机器台账」新增「批量部署挖掘与打标」（`<MachineBatchTagPanel>`），对当前筛选的在线机器一次执行 `machine-workload-discovery` 并按机预览标签差异与 20 标签上限门禁，由平台管理员经受审计乐观锁接口批量合并写入 CMDB 标签。
    - 后端将 `doagent_tools.go`、`doagent_tool_binding.go`、`doagent_tool_attestation.go`、`doagent_tool_evidence.go` 收口至统一声明式能力规格表（`builtinCapabilityToolSpecs` 与 `structuredDoopsMutationSpecs`，未声明或含空白的 Skill 严格 fail-closed 零写工具）；在 `doops` MCP 域新增原生结构化工具 `mcp_doops_dns_read` / `mcp_doops_dns`（支持纯 Python Wire 查询、开放递归检测、Zone 渲染/校验、记录增改删带时间戳备份 + SOA 递增 + `named-checkzone` + `rndc reload` + **校验或重载失败自动还原备份**、AliDNS `runtime-opaque` 同步，严禁路径逃逸与内联密钥）与 `mcp_doops_mail_read` / `mcp_doops_mail`（支持 DNS 规划评估、多语言开发者接入指南、K8s 巡检规划、账号/监听计划与工作区清单校验，严格拒收任何明文密码/令牌并强制 `password_secret_ref` Secret URI），接入统一运行时 attestation 签证与 `dns-governance`/`mail-governance` 插件。

### Changed

- 彻底合并控制台「自动化」与「智能巡检」为单一三页签工作台（`AUTO-02`，零数据库 Schema 变更）：
  - **消除跨页面与列表内重复配置**：侧栏移除重复的「智能巡检」入口（同步更新 `plugin_catalog.go` 与 `lib/plugins/builtin-catalog.json`，将 `doops.inspection` 聚焦为「态势感知与监控」、`doops.automation` 升级为「自动化与巡检」并纳入 `app-delivery-web` 预设，同时 100% 兼容旧 `?view=assets&area=inspection` 书签与跨页跳转）；删除冗余包装组件，全站只保留一套定时任务配置表单与即时执行入口，并在「定时任务」页签严格只展示已配置的定时任务（不再混入零散手动即时巡检卡片，解决 `(2)` 与 `5 个任务` 重复冲突）。
  - **清晰三页签与去内部黑话**：「自动化」（`?view=console&tab=tasks[&sub=schedules|runs|bg][&run=<id>]`）收口为「定时任务」「执行记录」「一次性后台执行」三张页签——移除晦涩的「按任务分组 / 全部批次」切换与「批次」术语，「执行记录」按时间倒序展示全部定时调度、即时执行与异常复检记录并支持按来源/状态/目标筛选及原地查看动态产物报告。
  - **修正一次性后台执行徽标语义**：页签更名为「一次性后台执行」并补充 `doops exec --bg` 场景说明，仅在当前目标确实存在运行中命令（`running > 0`）时高亮显示「运行中 N」，消除已结束历史记录在页签上显示 `(1)` 带来的误解。

## [0.3.67] — 2026-11-28

### Fixed

- 补齐 `audit export` 清理路径的 `affected_rows_manifest.json` 登记并完成正式发布。

## [0.3.66] — 2026-11-28

### Fixed

- 还原非改动文件的 Go 1.25 `gofmt` 对齐以完成发布。

## [0.3.65] — 2026-11-28

### Fixed

- 补齐 `audit export` 与 `store backup` Go 源码格式（`gofmt`）以发布完整制品。

## [0.3.64] — 2026-11-28

### Added

- Gateway 新增 `doops-gateway audit export` 与 `doops-gateway store backup` 一体化导出、归档备份与存储空间回收能力（原生兼容 SQLite 与 MySQL 5.7/8.0/8.4，配合宿主 `systemd` timer 可零外部依赖实现定时导出与空间释放）：
  - **`doops-gateway audit export`**：按主键游标恒定内存流式导出 `audit_events`（附带 `agent_resource_audit` 关联元数据）及可选终态 `platform_events` 为 `0600` `.jsonl.gz` 与 `.sha256`；支持通过现有 `DOOPS_COS_*` 自动 S3 Multipart 分片上传并强制 `HeadObject` 字节数校验；`-purge` 严格 fail-closed（仅在本地与 COS 校验全部通过后按 `id <= max_id` 5,000 行/批清理并写入含归档摘要与 COS 键的 `audit:purge` 回执，MySQL 侧强制 `-confirm-database` 防误删）；`-reclaim` 执行 `OPTIMIZE TABLE`（MySQL）或 `VACUUM`（SQLite）将释放页真正归还磁盘，支持 `-keep-local` 与 `-keep-cos-days` 自动轮转；
  - **`doops-gateway store backup`**：在单次 `REPEATABLE READ` 只读快照事务内导出全部基表（自动跳过 MySQL 5.7 虚拟生成列）并与 `DOOPS_GATEWAY_DATA_DIR/{skills,artifacts}` 成对打包校验，支持 COS 分片上传校验与本地/远程过期副本自动轮转；
  - **`doops-gateway audit purge -reclaim`**：支持在清理完成后即时回收表空间。

### Fixed

- 修复 `audit_purge.go` 的 `drainAuditTx` 在批量清理中每轮先调用无界 `deleteCompletedAuditSQLFor` 导致百万级 `audit_events` 表在单事务内全表删除与锁争用的问题，改为优先按 `AuditPurgeBatchSize`（5,000 行）分批清理普通审计行、排空后再清理符合证据保护条件的 `agent:upgrade` 记录。
- 修复 `backend/scripts/deploy-gateway.sh` 在 `DOOPS_GATEWAY_STORE=mysql` 部署模式下仍强校验旧 SQLite 文件存在且非空的问题（MySQL 模式改为校验 `DOOPS_GATEWAY_DATA_DIR`），并在发布成功后自动保留最近 5 个回滚二进制以防止 `/usr/local/bin` 持续膨胀。

## [0.3.63] — 2026-11-28

### Changed

- 整合控制台「资产台账」「资源监控」「Agent 概览」与侧栏导航分组，消除「Agent 资产 / Agent 监控 / 台账与监控」功能重叠与监控数据双轨漂移（`MON-03`）：
  - **统一「资源监控」中心（`?view=assets&area=monitor`）**：将原隐藏在态势感知第三页签的全网「系统资源监控」与原「Agent 监控」整合为双页签中心（「全网资源总览」+「单目标时序与告警」），修复全网「立即全量采集」未传 Skill 列表导致空运行失败的问题并新增「仅看异常 / 离线」筛选与告警/详情抽屉精准下钻定位；支持离线机器详情直达历史监控且不中断当前工作区会话、顶栏切换目标与监控页联动不踢出离线目标、统一态势/运行监控/机器台账返回面包屑；态势感知回归「全局态势总览」「业务→集群→机器拓扑」两张纯态势页签；清理 `AssetMonitorPanel` 死代码并补齐原生采集方式中文标签；保留 `?view=console&tab=monitor` 旧书签兼容。
  - **「Agent 资产」收口进「Agent 概览」**：目标工作区的「Agent 概览」改为三页签（「运行概览」「资源对账清单」「台账分配范围」，支持 `?view=console&sub=manifest|workspace` URL 持久化），原「Agent 资产」不再单独占用一级菜单，保留 `?view=console&tab=assets` 旧书签兼容（即使 `doops.cmdb` 停用仍可访问 Core Shell 的「资源对账清单」），并移除未使用的旧 `?view=assets&area=scope` 路由；平台管理「Agent管理」聚焦跨 Agent 分配范围维护。
  - **正交侧栏分组与「Agent 运行监控」命名**：左侧导航重组为「导航 / 目标运维 / 资产台账 / 监控与巡检 / 场景与扩展 / 发布部署 / 平台管理」，将原「平台监控」更名为「Agent 运行监控」（保留 `?view=assets&area=board` 路由与隧道/并发/调用/Token 定位），将「插件中心」归入「场景与扩展」并与 L2 声明式插件同组；同步 `plugin_catalog.go` 与 `lib/plugins/builtin-catalog.json`（保持 11 项 Core Shell 与 8 个 L1 插件 ID 不变）。
  - **补齐原生采样双向同步、主机快照去重与零在线目标可访问性**：Gateway `POST /v1/assets/monitor` `action=collect` 在落盘 `asset_monitor_samples` 的同一事务内自动将 `host`（附带 CMDB 标签、容量回退与高水位状态）/ `apps` / `containers` / `k8s_pods`（归类至 `app` 避免虚增 K8s 集群数）采样点桥接写入 `monitor_resource_snapshots`，并在 `BuildMonitorOverview` 对同目标 Skill-First 与原生主机快照按时间去重杜绝容量双倍计数；解除控制台在无在线 Agent 时对平台级台账、态势感知、资源监控、智能巡检、Agent 运行监控与插件中心的误拦截（仅「发布工作负载」与「升级 Agent」依赖在线目标）。

## [0.3.62] — 2026-11-28

### Added

- 控制台上线**终端双模式联动与 AI 助手「左对话 + 右执行终端」人机协作工作台**（`TERM-08`）：
  - **Agent 与 BFF 命令白盒化投影**：`publicDoagentUINotification` 在严守 `rawInput`/`rawOutput` 与凭据隔离的前提下，安全提取经 `redactDoagentUIText` 脱敏的 `displayCommand`、`displaySummary`、`cwd` 与 `exitCode`，BFF 实时流与历史水合统一映射为 `shell.execute` / `filesystem.*` 语义键及耗时 `timing`，在对话与历史会话中直接呈现 Agent 执行过的命令与退出状态；
  - **AI 助手可拖拽双栏人机协作**：顶栏新增「终端」开关，原地展开右侧分栏并提供「执行过程」（平铺终端风格实时镜像 Agent 命令与输出、支持「在终端重跑」）与「交互终端」（共享同一会话 PTY、支持 AI 命令原地填入/安全运行及一键「← 发给 AI」将手工操作结果回传左侧对话）；
  - **终端页双模式与极简顶栏**：合并冗余顶栏为单行控件并隐藏空终端占位条；新增「提示命令 | 派给 AI」模式切换及 `@ai` / `##` 速通前缀与 `⌘I` 双出口提交，支持直接拍给右侧 AI 助手自主排查执行，分栏支持拖拽调节宽度。

## [0.3.61] — 2026-11-28

### Changed

- 收敛「目标运维 → 自动化」与「台账与监控 → 智能巡检」功能定位并打通定时任务历史执行记录（`AUTO-01`，零数据库 Schema 变更，兼容 SQLite 与 MySQL 5.7/8.4）：
  - **Gateway 运行记录按目标精确过滤**：`GET /v1/monitor/runs` 新增成对可选参数 `cluster` 与 `instance`（校验目标标识与目标授权，非授权返回 `403 monitor_target_forbidden`，非法/缺项返回 `400 invalid_monitor_target`），基于 `monitor_run_items` 精确筛选命中目标的执行批次并返回 `target_filtered: true`；
  - **自动化升级为统一任务执行器**：默认进入「定时任务与记录」页签（消除默认落入空「后台任务」看不到定时执行记录的问题），在同一视图内融合定时任务配置（支持仅当前目标、多目标勾选与全部在线目标）与按任务分组的历史执行记录、近期状态轨及原地产物详情（多目标批次支持聚焦当前目标，停用巡检插件仍完全可用）；第二页签「后台命令」聚焦一次性 `exec --bg` 长时命令并补充空态引导；
  - **智能巡检收敛为单页巡检策略工作台**：移除冗余的独立「定时计划」页签，与「自动化」共用同一套任务/策略编辑器、执行历史列表与动态产物详情组件，巡检策略底层统一交由自动化执行器按周期调度并支持一键跳转「自动化」执行器展开对应任务历史。
- 优化 Web 控制台「AI 助手」与「凭据」使用逻辑并移除 AI 助手代码模式（`WEB-06`，纯前端改动，完整保留 Gateway 加密与四步安全生命周期）：
  - **AI 助手与发布工作负载移除代码模式**：向 `<AgentWorkspace>` 传入 `allowedModes={["work"]}` 且不再暴露 `codeWorkspace`，消除空态与对话顶栏的「工作 / 代码」切换与代码工作台入口，保留对话时间线及工作区成果列表与下载能力；
  - **AI 助手 ↔ 凭据双向闭环**：顶栏新增懒加载「凭据」状态下拉菜单（仅展示当前目标可用凭据元数据并置顶 `modelProvider` 绑定状态），缺模型或模型连接不可用时顶部展示内联提醒与「配置模型凭据 / 重新检查连接」快捷操作；从 AI 助手跳转凭据页自动预筛选并带返回横幅，激活 `modelProvider` 后点击「返回 AI 助手并重连」自动重连载入新模型；
  - **凭据管理结构化表单与四步阶段引导**：新增 6 类凭据类型筛选条、每行四步阶段徽章与推荐动作高亮（创建 → 自动打开写入 → 自动引导绑定 → 自动打开验证 → 激活）、6 类结构化写入表单（含模型服务商预设、密码掩码与提交/关闭即清空，保留「切换到高级 JSON」入口），且 `modelProvider` 绑定自动隐藏无关的 Kubernetes RBAC `Secret namespace` 输入项。
- 移除交互终端单次 AI 链路的前端与服务端文本自动脱敏（`TERM-07`）：用户提问、右键选中片段、有界历史命令上下文及 `terminal:ai` 审计摘要均原样透传（移除 `lib/terminal-redact.ts` 及 `scrubTerminalAIAuditSummary`），如需文本脱敏后续独立立项设计；Gateway 托管 `modelProvider` 凭据本身的内存清零与上游错误隔离保持不变。
- 目标清单按当前连接确认版本（`RES-01`）：Agent 首次写盘失败后可重试相同版本，离线目标保留上次运行模式和 Kubernetes 可达性；机器、业务部署、网站、数据库与凭据按实际绑定对账，执行审计使用 `credential/<name>` 等资源引用；旧库升级前可用 `backend/docs/migrations/agent_status_capabilities.*.sql` 检查所需能力字段。

### Performance

- 深度优化 Gateway MySQL 后端公网 RDS 运行态性能与连接稳定性：
  - **消除 3× 公网往返并加固连接池**：在 `utf8mb4` 且服务端未开启 `NO_BACKSLASH_ESCAPES` 时自动启用驱动 `InterpolateParams=true`（支持通过 DSN `interpolateParams=false` 显式关闭），将每次单条 SQL 由 `PREPARE+EXECUTE+CLOSE` 3 次公网 RTT 降至 1 次；新增 `CheckConnLiveness=true`、`ConnMaxLifetime=30m` 与默认 `readTimeout/writeTimeout=60s`，消除长连接被云 NAT 静默断开导致的假死超时。
  - **消除高频热路径 N+1 写放大与行锁争用**：新增双引擎通用 `gatewayUpsertBatchSQL`，将 `storeObservationEpoch`（每轮数百个观测资源由 N 次串行 UPSERT 合并为 150 行/批，显著缩短 `agent_status FOR UPDATE` 持锁时间并根治大集群 `target resource sync` 超时）、`TouchAgentsContext`（MySQL 侧按 `(cluster, instance)` 确定性排序后单条 `UPDATE ... CASE` 批量刷写所有在线节点心跳）、`inspection_metric_points`（200 行/批多行 `INSERT`）与 `cluster_inventory_namespaces`（100 行/批）全部批量化；缓存已就绪的 `agentResourceSyncReady`，避免每 30 秒重复扫描 `information_schema.tables`。

### Fixed

- 修复 MySQL 后端下 `enqueuePlatformEventTx` 在 `SELECT webhook_endpoints` 游标未关闭时同事务并发 `INSERT webhook_deliveries` 触发 `commands out of sync` / `driver: bad connection` 的隐患（改为先收敛关闭游标再批量入队）。
- 统一补齐全链路按 UTF-8 rune 边界安全截断与 `U+FFFD` 归一化（`truncateUTF8Bytes` / `normalizeUTF8ForStore`），覆盖 `execution_records`、巡检产物与证据预览、交互终端 `cwd` 与 DoSql 日志/错误截断，根除历史按字节截断中文导致 MySQL `utf8mb4` 报 `Error 1366` 的风险。
- 修复交互终端两处线上复测捕获的交互边界问题（`TERM-06`）：
  - 修正分栏右侧 `AgentExecBar` 空态早退分支顺序，经纯函数 `agentExecBarDisplayMode` 保证在无建议命令且未展开手动执行区时彻底隐藏冗余「执行命令」按钮；
  - 对齐中文提问引导词规则（`hasZhLead && cjkCount >= 4 && shellScore === 0`），使夹带长 URL / 连接串 / 路径的中文自然语言提问（如 `为什么连不上 mysql://... 查一下端口`）在自动识别模式下无需手动加 `#` 也能直走单次 AI。

## [0.3.60] — 2026-11-27

### Fixed

- 修复 `TestGatewayMySQLCopyCommandMovesDataDirs` 在内置 Skill 数量扩展至 10 个后硬编码 `skill_rows=8` 及 `TestMySQL57GatewayEndToEnd` 管理员令牌 ID 遗漏导致的 CI 门禁失败。

## [0.3.59] — 2026-11-27

### Fixed

- 修复 Gateway 在 MySQL 5.7 后端运行期查询机器资产列表、资产拓扑与资产看板返回 `503` 及远程云 RDS 突发心跳超时的问题（`INT-110`）：
  - `gatewayJSONOrderedArraySQL` 改为 `COALESCE(CAST(JSON_ARRAYAGG(...) AS CHAR), '[]')`，规避 MySQL 5.7 相关标量子查询在 0 匹配行时 `COALESCE(JSON_ARRAYAGG(...), JSON_ARRAY())` 误返回 `NULL` 导致 `listInventoryMachines` 扫描报错的引擎缺陷，彻底恢复 `/v1/assets/machines`、`/v1/assets/topology`、`/v1/assets/dashboard` 与 `/v1/assets/coverage`；
  - 将 MySQL 连接池上限由 `8/4` 调整为 `24/8` 并将批量心跳落库超时由 `2s` 调整为 `5s`，消除多 Agent 并发巡检与资源同步高峰期的远程 RDS 连接池排队超时。

### Added

- 控制台与 Gateway 上线**L2 声明式 `plugin.yaml` 全栈热插拔与首批场景插件（Phase 2，`PLG-02`）**：
  - **Gateway `plugin.yaml` 严格解析与隔离**：在 `platform_skills` 新增 `ui_manifest_json`/`ui_manifest_error` 并在 `monitor_schedules` 新增 `inputs_json`（SQLite/MySQL 双库一致 + 运行期幂等补列）；使用 `gopkg.in/yaml.v3` `KnownFields(true)` 强校验 `doops.sh/v1alpha1` 清单（严禁自由文本 `text` 输入、原子拒绝非法上传、磁盘改动按 SHA-256 自动重解析），并在 `syncOneSkillToAgent` 下发前显式剥离 `plugin.yaml`；系统 L2 纳入场景默认集与平台级开关，个人自定义 L2 仅对所有者本人（`owner_is_me`）挂载至侧栏。
  - **L2 受控输入与全集群指标聚合**：`POST /v1/monitor/runs` 与定时计划支持单 Skill 强校验 `inputs`（`number`/`boolean`/`select`/`credential_ref`），经受审计 `DOOPS_INPUT_<KEY>`（`shellSingleQuote` + 脱敏）注入脚本或 `<<DOOPS_INPUTS>>` 注入 AI 提示词；`credential_ref` 失败关闭校验活跃状态、已发布版本与逐目标授权，绝不传递凭据明文；新增 `GET /v1/monitor/metrics/latest`（及 Web BFF `/api/monitor/metrics/latest`）按授权目标单次批量聚合最新指标。
  - **首批 2 个内置 L2 场景插件与 `<SkillPluginHost>`**：内置「GPU 算力治理（`gpu-governance`）」与「主机安全治理（`host-security`）」两个真实可运行 L2 Skill 插件；前端新增 `next/dynamic` 懒加载 `<SkillPluginHost>`，支持左侧「场景扩展」（`?view=plugin&plugin=skill.<slug>`）、目标场景页签（`plugin:skill.<slug>`）、首页 L2 聚合指标卡（上限 6 张，8s 超时独立降级）、一键创建推荐定时计划，以及缺失指标诚实提示「本次运行未输出」绝不伪造 `0`。

## [0.3.58] — 2026-11-27

### Fixed

- 修复 Gateway SQLite → MySQL 割接与运行期审计写入遇到非合法 UTF-8 字节（如历史按字节截断的多字节中文或远端非 UTF-8 命令输出）在 MySQL `STRICT_ALL_TABLES` + `utf8mb4` 下触发 `Error 1366` 失败的问题（`INT-109`）：
  - `store mysql-copy` 会话显式开启 `STRICT_ALL_TABLES` 与 `NO_AUTO_VALUE_ON_ZERO`，对非主键 SQLite `TEXT` 列中的非法 UTF-8 字节按 Unicode `U+FFFD` 规范化后写入与回读校验，并在标准输出显式打印 `utf8-normalized: cells=<N> tables=<...>`；主键 `TEXT` 含非法 UTF-8 仍以 `mysql_copy_invalid_utf8_primary_key` 失败关闭，`BLOB` 列保持逐字节无损；
  - 运行期 `auditTailBuffer.String()`、`StartAudit` 与 `FinishAudit` 同步对 `command_summary`/`error`/`tail` 执行 `U+FFFD` UTF-8 规范化，防止 MySQL 后端运行期因命令二进制或非 UTF-8 输出导致审计落盘失败。
- 修复「AI 助手」历史会话无法列出、切换为空白、刷新/重启/重新登录后丢失以及 24 小时工作区 GC 误删持久化对话记录的问题（`HIST-01`），严格遵循「会话存在 Agent、Gateway 仅做鉴权与转发通道」架构边界：
  - **Agent 侧历史读取与会话列表**（`doops_agent_prompt` `mode=history` / `mode=sessions`，协议版本 `doops.sh/agent-history/v1`）：移除 `handleAgentHistoryWS` 对 2 小时进程内 `sessionMap` 的前置门槛与双重包装、改用脱敏不截断（单字段 128 KiB 前缀上限 + 响应总大小 1 MiB 尾部窗口）并自动剥离内部 `system` 提示与 DoOps 注入上下文；当 `do-agent` SQLite 缺失记录时自动回退读取 `<workspaceRoot>/<sid>/.agent/sessions/<sid>/messages.json`；新增 `mode=sessions` 扫描持久化和活跃会话生成脱敏标题、预览、轮次与时间戳，并将 `mode=history`/`mode=sessions` 映射为并发只读 `info` 审计动作、未识别 `mode` 失败关闭；`session/new` 为非 JSON 对话显式传 `hidden=false`，且 24 小时工作区 GC 与 `doops_clean_workspace` 清理工作区文件时保留 `.agent/sessions/` 30 天。
  - **Web BFF 冷启动历史水合与控制台「历史会话」**：`authorizedAgentSession` 首次打开声明 `history_schema` 的目标会话时（含 30 分钟缓存淘汰、Web 重启或重新登录换令牌）自动拉取 `mode=history` 并投影为合法 `@agent-cloud/agent-ui` 时间线快照，支持直接续聊且历史读取失败时失败关闭；新增 `GET /api/agent-sessions?view=list` 并在「历史会话」下拉展示真实问题摘要、会话 ID、轮次与时间（兼容旧版 Agent 自动回退 `action=ask` 审计并提示升级），同时在工作区「成果」面板过滤 `.agent/` 内部文件。

## [0.3.57] — 2026-11-27

### Fixed

- 修复 Web 控制台在「AI 助手」与网关/控制台短暂重启时的两处异常体验：
  - 工作区成果读取改为仅在会话处于 `connected` 状态时触发，抑制执行中与中断态瞬时失败，并在连接恢复、点击「重新检查连接」或点击「成果读取失败」按钮时自动重试，避免红色「成果读取失败」长期残留；
  - `lib/client.ts` 与 `lib/assets.ts` 的响应解析改用安全 JSON 读取，面对反代或服务滚动重启返回的 HTML `502/503/504` 页面时统一提示中文可用性说明，不再弹出原始 `Unexpected token '<', "<html>..." is not valid JSON`。

## [0.3.56] — 2026-11-27

### Added

- Web 控制台「数据库」浏览器新增直连 Go DoSql 引擎的只读 **「结构治理」** 与 **「SQL 门禁预检」** 视图（`SQL-08`，零 Gateway/Agent 新接口、零 Token 消耗）：选中数据库与凭据后可一键切换 **数据表 / SQL 预检 / 结构治理**，支持查看表/列/索引/外键与 `schema_hash`（`inspect`）、变更版本日志与任意快照/线上库结构漂移对比及建议 DDL（`versions` + `diff`）、全会话/当前会话 SQL 变更流水检索（`history`）、迁移台账与脏版本告警（`status`），以及在 SQL 编辑器中直接执行分段计划与 Policy-as-Code **门禁预检**（`lint`）。

### Fixed

- 补齐交互终端单次 AI 全链路安全与交互收口（`TERM-05`）：
  - **双层提问脱敏**：前端 `submitSingleShotQuestion` 发送 `question` 前同步经 `redactTerminalOutput` 脱敏，Gateway `POST /v1/terminal/ai` 落盘 `audit_events.command_summary` 前增加服务端二次密钥清洗（含 URL 密码 / Bearer / JWT / `sk-` / 私钥 / 敏感键值），并补齐多余尾随 JSON 拒绝（400）及模型返回字符串数组 `commands: ["..."]` 兼容解析。
  - **右键菜单、粘贴缓冲与非 bash 兼容**：交互终端选中文本右键首选「AI 单次解释 / 排查」（直走 `/api/terminal-ai` 不弹右侧栏，保留「转入 AI 协同深入排查」）；右键「粘贴到终端」统一经 `queueInput` 同步乐观行缓冲；无 OSC 133 的 `/bin/sh`/`ash`/`dash` 自动改用 POSIX `Ctrl-U` 清行提交注释，且离线/断连态不再误拦截回车。
  - **审计筛选与残留清理**：「审计记录」动作下拉新增 `terminal:ai` 筛选项；分栏右侧 `AgentExecBar` 隐藏冗余手动输入框并主推「填入终端」；清理 `TerminalPanel` / `console-shell` 无用旧 props 并同步门户与案例文案。
- 修复 DoSql 只读 `status`（`doops sql --mode status`、`mcp_dosql_read(operation=status)` 与控制台「迁移状态」）及策略拒绝的 `migrate` 预检会静默执行 `CREATE TABLE IF NOT EXISTS _dosql_schema_history` 的生产副作用（`SQL-08`）：`migrate.SQLHistoryStore.ListRevisions` 改为只读目录表存在性探测（零 DDL），`sql_action=status` 改用只读数据库连接打开，在只读凭据与从库上可直接返回 `current_version=0` 而不再误建表或权限报错。

## [0.3.55] — 2026-11-27

### Added

- 控制台与 Gateway 上线**场景化插件架构（Phase 1，`PLG-01`）**：
  - **统一内核与 8 个内置 L1 功能插件**：收敛 11 项不可关闭的核心底座（首页、Agent 概览、AI 助手、终端、知识库、凭据、审计、插件中心、首次接入、升级 Agent、平台管理），将智能巡检与态势、Kubernetes、应用发布与交付、数据库、网站、镜像、资产台账与自动化拆分为 8 个可按需装配的 L1 插件；单一事实源 `plugin_catalog.go` 导出 `lib/plugins/builtin-catalog.json` 并受双向一致性测试守卫。
  - **5 套内置场景包与插件中心**：整合原「Skill 管理」与「MCP 管理」为三页签「插件中心」（场景与功能插件 / Skill 能力包 / MCP 工具服务，100% 兼容旧 `?area=skills` 与 `?area=mcp` 书签，且无在线 Agent 时也可访问）；支持 `all-in-one`（升级默认）、`ai-compute-cluster`、`app-delivery-web`、`host-security-ops` 与 `minimal-workbench` 场景一键预览差异（`dry_run`）并切换，以及单项开关覆盖、禁用路由守卫、页内被停用不踢出横幅、跨页跳转守卫与首次场景引导；只控制界面装配，绝不改写 Skill/MCP/定时计划或阻断 CLI。
  - **Gateway 控制面与 SQLite/MySQL 双库持久化**：新增 `platform_plugin_config` / `platform_plugin_overrides` 表及 MySQL 运行期幂等建表，提供 `GET /v1/plugins`（ETag/304 + 依赖就绪度）与 `POST /v1/admin/plugins`（乐观锁 `expected_revision` 409 + 管理员审计摘要 + fail-closed），并新增 Web BFF `GET /api/plugins` 与旧版 Gateway 404 显式兼容提示。

### Changed

- 深度对齐系统/安全/自定义 Skill 与「Agent 资产」「Agent 监控」（`MON-02`）：
  - **`inspect-app-web` 自动接通站点资源目录**：Gateway 调度 `inspect-app-web` 脚本时自动查询该目标在 `inventory_resource_catalog` 中已登记的 `website` 条目（按同套 `http`/`https` 域名路径严格校验、拒绝凭据与查询串、最多 12 个），注入 `DOOPS_INSPECT_URLS` 环境变量，使 HTTP/HTTPS 探活结果无缝桥接至 Agent 监控曲线与告警。
  - **`inspect-k8s-cluster` 补齐按 Pod 实时用量采集与桥接**：`scripts/inspect.sh` 保留 `kubectl top pods -A` 的 Top 40 Pod 实时用量（`pod_usage`，纳入载荷体积裁剪白名单），Gateway 自动将其按命名空间桥接为 `type="app"`、`origin="k8s_top"` 的 CPU/内存时序曲线。
  - **「Agent 监控 → Skill 巡检趋势」开放全类别 Skill 并收敛旧采集器**：不再局限于 `category=inspection`，同步开放安全巡检（如 `inspect-crypto-mining`、`inspect-package-vuln`、`inspect-proxy-evasion`）与自定义 Skill 历史趋势，新增「执行当前 Skill」单 Skill 即时触发按钮；将 4 个旧原生采集按钮收纳至「按需原生采样」折叠区，突出「一键巡检采集」主路径；「Agent 资产」顶部新增一键「前往 Agent 监控」直达入口，并同步操作员 `skills/doops/SKILL.md` 中的 `doops machines` 录入时间说明。

### Performance

- 控制台 8 个 L1 内置功能插件面板（数据库、网站、Kubernetes、镜像、部署包、自动化、台账、巡检与态势、工作负载发布）全面改用 `next/dynamic` 按需懒加载，`/console` 首屏初始脚本体积实测减少 **521.6 KiB**（4927.3 KiB → 4405.7 KiB），停用插件不再下载对应 chunk。

## [0.3.54] — 2026-11-27

### Fixed

- 适配 MySQL 5.7 `innodb_large_prefix=OFF`（767 字节单列索引上限，免改云 RDS 全局参数）：MySQL 5.7 建表自动将 `proxy_routes.host` 与 `edge_routes.hostname` 唯一索引映射为冻结白名单 `VIRTUAL GENERATED` SHA-256 唯一键（完整保留 253 字符无损唯一性），并为 `inventory_node_links` 主键 `node_name` 使用 191 字符索引前缀；MySQL 8.0/8.4 与 SQLite 保持不变，CI 的 `mysql57-backend` 已启用 `--innodb-large-prefix=OFF` 实测覆盖。

## [0.3.53] — 2026-11-27

### Changed

- 重构控制台交互终端 AI 交互为**独立单次模型调用（不走多轮 AI 助手）**（`TERM-04`）：
  - Gateway 新增 `POST /v1/terminal/ai`（Web BFF `POST /api/terminal-ai`）：鉴权并校验目标授权与审计可用性后，直接由 Gateway 使用该目标已激活的 `modelProvider` 凭据发起单次 LLM 调用（兼容 OpenAI 与 Anthropic 协议，禁用重定向、严格脱敏并落盘 `terminal:ai` 审计与 Token 用量），不再拉起 `do-agent` 多轮会话、不污染「AI 助手」历史、不强行弹开右侧「AI 协同」侧栏。
  - **默认开启自然语言自动识别**并扩充中文口语意图规则（在提示符直接输入「我想查一下系统的cpu」等自然语言并回车即可安全转为单次 AI 生成命令，不再误入 bash 报 `command not found`，对合法 Shell 命令/路径/管道/赋值保持零误判，且保留手动关闭开关与 `exit 127`「转 AI 提问」兜底）。
  - **消除同屏重复建议框与窄屏状态栏挤压**：终端底部单次建议卡片与右侧「AI 协同」助手完全解耦（不再同步渲染 `AI 助手` 消息里的命令，杜绝左右两套按钮互相打架），支持流中「取消」、点击「插入 / 运行」后自动收起并归还焦点，仅在主动点击「深入排查 →」时升级进入右侧多轮「AI 协同」；状态栏移除冗长常驻提示语并全项防折行（`shrink-0 whitespace-nowrap`），分栏下不再出现双行竖排错位。

## [0.3.52] — 2026-11-27

### Changed

- 收敛并打通「Agent 资产」与「Agent 监控」前后端能力（`MON-01`）：
  - **Skill-First → Agent 监控自动桥接**：Gateway 持久化巡检结果时自动将 `inspect-vm-host`（CPU 核数、内存字节、物理网卡吞吐、去重挂载点容量及带 UUID 的 NVIDIA GPU 利用率）、`inspect-container-runtime`（容器 CPU/内存/上限）、`inspect-k8s-cluster`（节点 CPU/内存）与 `inspect-app-web`（端点响应时间/错误率）投影写入 `asset_monitor_series`/`asset_monitor_samples` 并参与阈值告警判定（显式 `series_points` 同键优先），彻底消除定时巡检与「Agent 监控」双轨割裂；`inspect-vm-host` 补充采集可选 `nvidia-smi` GPU UUID。
  - **Agent 监控收敛与规则闭环**：新增 `POST /v1/assets/monitor` `action=delete_rule`（管理员可删除告警规则并自动收口关联告警）、在 BFF 开放 `/api/monitor/metrics` 代理，将「Agent 监控」整合为「资产时序与告警」+「Skill 巡检趋势（`1h–30d`）」双视图并复用统一 SVG 折线图与纯函数模块 `lib/agent-monitor.ts`；K8s Pod 采集输入框接入资源清单命名空间自动补全。
  - **Agent 资产收敛**：将实时探测资源清单（`/v1/assets/target-manifest`，含类型/状态筛选、搜索、重新同步与一键登记）与台账分配范围（`/v1/assets/workspace`，含搜索筛选与管理员就地分配）整合进「Agent 资产」双视图；「Agent 概览」改为紧凑摘要卡片并一键跳转，避免数百条资源撑长概览页。

### Fixed

- 修复「Agent 监控」顶栏主按钮（「全策略巡检采集」→「一键巡检采集」）在 Skill-First 退役旧策略后未传 `skills` 导致点击空跑 0 指标写入的缺陷，改为按目标能力显式下发已启用的系统巡检 Skill 并同步刷新双视图。

## [0.3.51] — 2026-11-27

### Added

- 机器台账新增「资产录入时间（`created_at`）」字段：Gateway SQLite/MySQL schema 与启动自动迁移无缝补齐 `inventory_machines.created_at`；`POST /v1/assets/machines`（`create`/`update` 与 CSV 可选 `created_at` 列）支持 `YYYY-MM-DD` 与 RFC3339 时间戳归一化（新建留空默认当前 UTC 时间，更新与不含该列的 CSV 重复导入保留既有值，非法格式返回 `400 invalid_created_at`）；`GET /v1/assets/machines`、`doops machines` 与控制台「机器台账」列表、详情及新增/编辑表单同步支持录入时间展示与维护。
- 升级系统预置主机巡检 Skill `inspect-vm-host`、指标快照桥接与「态势感知」汇总统计（`SF-26`）：在保持零 Token 百毫秒脚本直跑与全量既有契约前提下，补齐物理网卡实时吞吐（`host.network`，与 CPU 同窗 250ms 采样 `/proc/net/dev` 或 `hostPID` 宿主机 `/proc/1/net/dev`）、NVIDIA GPU 算力/显存/温度采集与柱状图展示（`host.gpu`，`hostPID` 容器经只读 `nsenter` 读取宿主 `nvidia-smi`，无 GPU 节点零误报）、真实 `hostPID` 与容器视角识别（`host.view_scope`）、Top 5 CPU/内存短名进程（仅 `comm`）、排除 Snap 只读 `squashfs`（消除 100% 磁盘满误报）与冗余 containerd/kubelet overlay 挂载、以及 >12 KiB 自动紧凑 JSON；同步零 Schema 变更桥接 `cpu_total_cores`/`net_bytes_per_sec`/`disk_io_latency_ms`/`gpu_percent`/`host.os`/`host.kernel` 到 `monitor_resource_snapshots`，并修复 `BuildMonitorOverview` 与 `readMonitorTrendBuckets` 在 Skill-First 退役原生采集器后忽略 `asset_kind=skill` 主机快照导致概览平均水位与总容量归零的缺陷。

### Fixed

- 修复 Gateway SQLite → MySQL 完整数据迁移（`store mysql-copy`、`store audit-scrub` 与 MySQL schema）多项生产割接阻塞（INT-108）：
  - 扩宽 `audit_events.{session,digest,correlation_id}`、`detached_tasks.session`、`release_tickets.plan_digest`、`inspection_receipts.digest` 至 `VARCHAR(191)`，以及 `inventory_import_attempts.receipt` 至 `MEDIUMTEXT`，并在 MySQL 启动路径补齐幂等扩容（消除长 session ID 与 71 字符 `sha256:` 摘要触发的 MySQL `Error 1406`）。
  - 修正源库 `PRAGMA foreign_key_check` 语义：豁免 MySQL DDL 已刻意移除的平台凭据 `credentials.owner_id=''` 历史 SQLite 外键，对其余真实违约（含历史 `tokens.user_id=''`）统一返回退出码 2 并点名违约表；空库 `--dry-run` 同步执行外键校验与排序规则校验，不再误报退出码 0；SQLite 启动迁移自动规范化历史 `kind='agent'` 空串 `user_id` 为 `NULL`。
  - 修复旧绝对路径 `artifact_dir`/`storage_dir` 改写在 MySQL 单事务游标未关闭时执行 `UPDATE` 导致的连接冲突，并统一 `--dry-run` 与正式搬运的路径归属/摘要/缺失表判定；将 `store audit-scrub` SQL 正则由 `(?i)` 改为 MySQL 5.7/8.0/8.4 双兼容写法，同步修正运维手册 §7/§7.1 过时与矛盾条目。
- 修复 DoSql 迁移与数据同步工具缺陷：`migrate.ComputeStatus` 在 `--baseline` 之后执行新版本迁移不再将旧版脚本重新判为 `Pending`；`datadiff` 修复 MySQL FixSQL 反斜杠转义、`[]byte` 整型键解析与未列主键列时的 `INSERT` 拼接；`backfill` 改为键集 `LIMIT` 分批，避免雪花 ID 或稀疏主键空转。

### Performance

- `doops-gateway store mysql-copy` 由逐行单条 `INSERT` 升级为多行批量 `INSERT`（每批上限 500 行 / 60,000 参数 / 2 MiB 封包预算），大幅缩短百万行级审计表的生产割接耗时并保持 MySQL 5.7 `max_allowed_packet` 安全边界。

## [0.3.50] — 2026-11-27

### Added

- 控制台交互终端新增**断线无感自动重连与原地「重新连接」**：网络闪断、Gateway 滚动重启、目标短暂离线（`-32004`）或休眠唤醒（监听 `online`/`visibilitychange` 及目标恢复在线事件）时保持终端不卸载、不丢屏，自动按 `1s → 2s → 4s → 8s → 15s`（最多 8 次）指数退避续接原远端 `term` 与当前 `cursor`（保留原 shell 环境变量与运行态；仅当 Agent 明确确认 PTY 已关闭/回收时自动建立新会话），`401`/`403` 鉴权与权限拒绝保持 fail-closed 不盲重试；断连/失败/退出态顶栏提供一键「重新连接 / 立即重连」按钮免刷新整页；BFF `GatewayWsPool` 5 分钟最大复用周期改为优雅排空轮换（新请求切新 WS、在途长轮询自然结束不报 503，登出与 `-32001` 吊销仍同步即时驱逐排空连接）。

## [0.3.49] — 2026-11-27

### Changed

- 精简控制台冗余且易误导的覆盖率指标：移除「机器台账」内的「探针覆盖率 (100%)」与「纳管覆盖率 (80%)」子页签，改为直接呈现机器清单并新增 **Agent 状态筛选（全部 / Agent 在线 / Agent 离线 / 未接入）**；移除「系统资源监控」资产卡片上的失真 `probe_coverage_pct` 百分比，改为直接展示已采集资产数与在线/离线 Agent 数；移除「态势感知」卡片角标与自定义巡检报告中的覆盖率行（后端 `/v1/assets/coverage` 保持兼容不变）。

### Added

- 控制台「智能巡检 → 执行与记录」支持**按任务分组聚合**（默认「按任务分组」，支持一键切换「全部批次」）：自动按定时计划（`schedule_id`，计划改名不断裂、已删计划显式标注）、关联 Skill、直接命令或提示词首行归组，卡片聚合展示最新状态、最新检查汇总、平均耗时、已完结正常率、状态分布、触发构成与最多最近 10 次可点击状态轨，并支持快捷查看最新产物/复检/立即执行、关键词与状态联动筛选、高频任务挤出全局窗口时按 `schedule_id` 自动/按需补拉该计划专属历史，以及在「定时计划」页签点击「执行记录」一键直达并展开对应任务分组。
- 门户首页与配套文档全面升级为「面向开发者的智能运维中心」：重构 `/` 首页为首屏内完整呈现的交互式工作台（AI 交互终端、K8s & Helm 证据化交付、DoSql 数据治理、Skill-First 智能巡检）、新增「提供给 AI 智能体的受控运维工具箱」专属三层交互区块（IDE Agent Skill + CLI、读写隔离 MCP 矩阵 `mcp_doops_*`/`mcp_dosql_*`、Gateway JSON-RPC 原语与 `gatewayInternal` 分层）、对称式反向隧道交互拓扑、不对称 Bento 能力矩阵与 4+3 安全门栅格；新增内置开源文档中心（`/docs` 与 23 篇 SSG 静态预渲染 `/docs/[slug]`），直接以仓库 `backend/docs/` 为单一权威源并自动重写文档间相对链接、提供 `⌘K` 侧栏检索与滚动联动目录；同步统一 `/cases`、`/blog` 与 `/docs/deploy` 顶栏/页脚及移动抽屉导航，升级 [README.md](README.md) 与 [backend/docs/README.md](backend/docs/README.md)，并在 `Dockerfile.web` 中打包 `backend/docs/` 支持生产独立镜像渲染。

## [0.3.48] — 2026-11-27

### Changed

- 控制台左侧「目标运维 → 后台任务」升级为「自动化」（保留 `tab=tasks` 路由），整合「后台任务」与「定时计划」两张页签：「后台任务」新增命令语义解析器（自动提取中文可读标题、分类、工作目录、执行步骤、跨任务差异参数芯片与时间戳可读提示，支持按需切换原始命令及一键「AI 解释任务」）、结构化新建任务（支持任务名称与工作目录）、状态筛选与关键词搜索、实时耗时、失败报错摘录/退出码展示、左右分栏输出查看（复制/导出/换行/跟随/拆解回填）与行内快捷停止；「定时计划」支持查看命中当前目标的周期计划、立即执行并跳转产物视图，平台管理员可直接为当前目标新建、编辑、启停与删除定时计划。

### Fixed

- 修复「自动化 → 后台任务」列表与详情未映射 Gateway 实际落库状态 `error`、导致失败任务显示为灰色英文 `error` 徽章的问题：统一按语义色渲染为红色「失败」，并将「运行中」对齐为蓝色、「结果未知（`outcome_unknown`）」标为琥珀色警告并提示核对远端状态后再决定是否重新执行。

## [0.3.47] — 2026-11-26

### Added

- Gateway MySQL 存储后端与离线搬运工具（`store mysql-copy`）新增 **MySQL 5.7.22+** 支持（兼容阿里云 RDS MySQL 5.7）：自动检测服务端版本并在 MySQL 5.7 下将 7 个函数唯一索引映射为冻结白名单 `VIRTUAL GENERATED` 唯一索引列、移除运行期 `WITH` CTE / `JSON_TABLE` 依赖与 `utf8mb4_0900_bin` 硬编码，同时保持 MySQL 8.0/8.4 与 SQLite 行为不变；低于 5.7.22 启动 fail-closed。

### Fixed

- DoSql MySQL / PostgreSQL 方言与 `inspect`/`diff` 缺陷修复（SQL-07）：PostgreSQL 建表改用 `CONSTRAINT ... UNIQUE` 并补齐独立普通二级索引与 `ALTER COLUMN` `SET/DROP DEFAULT`/`NOT NULL`、跳过 `*_pkey` 伪差异、`inspect` 修复 `confupdtype`/`confdeltype::text` 并保留外键 `ON UPDATE/DELETE`；MySQL 字符串 `DEFAULT` 自动加引号与建表二级索引补齐；CI 新增真实 MySQL 8.4 + PostgreSQL 16 闭环门禁。

### Security

- 安全纵深加固（SEC-02）：
  - **Web 控制台 CSP 与 BFF 边界**：`next.config.mjs` 升级为完整 `Content-Security-Policy`（收紧 `default-src`/`connect-src`/`base-uri`/`form-action`/`object-src`/`frame-ancestors`，生产态禁用 `'unsafe-eval'`），补齐 `X-DNS-Prefetch-Control`、`Cross-Origin-Opener-Policy` 及 `/api/*` `Cross-Origin-Resource-Policy: same-origin`；封堵 `/api/admin`、`/api/credentials`、`/api/background-tasks` 通配代理的点段路径穿越；为 `/api/terminal-stream` 与 `/api/agent-sessions` 补齐跨站读取拦截与流式 `no-store`/`nosniff`/`X-Accel-Buffering: no` 响应头；统一 `/api/login`、`/api/session` 的 `Cache-Control: no-store` 并限制 `/api/rpc` 并发上限与目标标识校验。
  - **Gateway / Agent 边界加固**：`doops_credential_import` 接入未 enrollment 目标拒绝门禁（`409 credential_target_unenrolled`）与 60s 调用超时；`doagent` SSE 订阅与 HTTP/SSE MCP 探测禁用 HTTP 重定向并封顶多行 SSE `data:` 累计缓冲；MCP URL 校验改用严格 `url.Parse` 拒绝内嵌凭据/空主机/fragment；`POST /v1/skills` 全量变更补齐 `skill:*` 审计事件；直连 TLS 的 WebSocket `CheckOrigin` 拒绝明文 `http://` 降级来源；Agent 健康检查 HTTP 服务补充读写/头超时与最大头体积上限。

## [0.3.46] — 2026-11-26

### Fixed

- 修复 `TestTerminalShellIntegrationEmitsOSC133AndOSC7OnBash44Plus` 在 Linux CI runner 上因输入回显提前匹配导致的竞态失败，并将 `lib/terminal-redact.test.mjs` 中的合成测试金丝雀改为运行时拼接以消除 `gitleaks` 扫描误报。
- DoSql 持久化与治理链路加固（SQL-06）：防止 `dry_run`/`backfill`/`migrate` 污染 `pack`、统一规范库地址与原子落盘、在 `migrate`/`backfill` 接入策略门禁与版本流水、补齐 PostgreSQL 索引/外键探测与运维工作流文档。

## [0.3.45] — 2026-11-26

### Added

- 控制台交互式终端升级为类 Warp AI 终端：Agent 为 bash ≥ 4.4 独立注入 OSC 133/7（自动清理且保留 `_doops_audit`），前端新增命令分块与悬浮操作条（`⌘/Ctrl+Shift+↑↓` 跳转、复制、解释/修复/追问）、`#`/`？` 前缀与 `⌘I`/`Ctrl+Shift+I` 行内自然语言提问（附带上下文预览、三层凭据脱敏与默认关闭的自动意图识别开关），以及终端内 AI 建议卡片（bracketed paste 插入 PTY；安全命令一键运行携带 `source=ai_suggestion` 并强制经过 Gateway 敏感词过滤、Agent `commandDenyRules` 与来源审计，高风险命令仅允许插入后手动回车）。

### Performance

- 控制台交互式终端（`doops_terminal`）新增 BFF 侧 Gateway WebSocket 连接池复用（`/api/terminal-input` + `/api/terminal-stream`）与 Agent 侧有界长轮询（`read` 支持 `wait_ms`，上限 10s，由 `features.long_poll` 声明），大幅降低键入回显延迟与空闲轮询频率；旧版 Agent 自动降级回 250ms 短轮询。

## [0.3.44] — 2026-11-26

### Added

- DoSql 新增统一状态目录 `DOOPS_DOSQL_STATE_DIR`（支持 Helm `dosql.stateDir`，非法相对路径 fail-closed）以及离线幂等项目脚手架 `doops sql --mode init`（自动初始化 `.dosql/{policy.yaml,README.md,schema/,migrations/}` 并在后续命令中自动发现）。

### Fixed

- 补齐 DoSql MCP 参数绑定、`datadiff`、CLI 命令集与文档/Skill 对齐，并同步更新 `web_tool_test.go` 在顶层 `skills/agent/doweb/` 目录迁移后的脚本路径。

## [0.3.43] — 2026-11-26

### Changed

- 运维 Skill 与 Agent 内置 Skill 收敛至顶层 `skills/` 目录（`skills/doops/SKILL.md`、`skills/agent/`），构建脚本与安装器保持向后兼容。

### Fixed

- 修复 `affected_rows_manifest.json` 与 DoSql `runDoSQLStatementOn` 的函数名对齐，并同步更新顶层 `skills/` 目录收敛后的契约测试与 `test:dosql` 路径，恢复完整发布门禁。

## [0.3.42] — 2026-11-26

### Added

- DoSql 升级为统一的 Go 写优先数据治理引擎（`backend/agent/internal/dosql/{lexer,policy,recorder,inspect,diff,datadiff,migrate,backfill}`），修复 P0 阻塞并新增结构对比（`diff`）、分块表内容校验（`datadiff`）、幂等迁移（`migrate`）、主键游标回填（`backfill`）与完整 MCP 读写工具。
- Gateway 新增平台管理员显式退役删除离线实例接口 `POST /v1/admin/instances/delete` 及配套 CLI `doops admin instance <list|archive|unarchive|delete>`、控制台实例页「退役删除」操作：拒绝在线实例（`409`），仅移除 `agent_status` 记录，并返回仍指向该目标的 `active_credential_grants`、`revoking_credential_grants`、`proxy_routes`、`user_grants` 剩余引用计数提示运维按需清理。
- 凭据吊销新增平台管理员显式离线强制终结能力：`doops credential grant-revoke` 与 `doops credential revoke`（及对应 HTTP 接口）支持 `--force-offline --reason <reason>`，仅平台管理员可用、强制单行非空审计原因且拒绝任何在线未清理目标（`409 credential_force_offline_target_online`）；全部待清理目标均离线时在单事务内将未清理物化标为 `abandoned_offline`、推进至 `revoked` 并强制审计落盘（审计失败仍 fail-closed），解决已销毁集群导致凭据永久卡死在 `revoking` 的问题。常规吊销的强清理契约保持不变。
- CLI 新增 `doops sync --prune [--dry-run]` 清理或预览已同步 Gateway 上未在线的本地失效路由（默认 `sync` 保持不删除并提示失效路由，查询失败的 Gateway 绝不清理），以及 `doops list --status` 查询并展示各本地路由的实时在线状态（`online` / `online(busy)` / `offline/unavailable` / `unknown`）。

### Changed

- 离线实例自动淘汰默认窗口 `DOOPS_OFFLINE_INSTANCE_RETENTION` 从 90 天（`2160h`）收紧为 14 天（`336h`），仍支持通过环境变量在 `1s..8760h` 内覆盖。

### Fixed

- 修复 `GET /v1/admin/instances` 默认活跃列表未过滤已归档（`archived=true`）离线实例的问题，使其与文档口径一致：默认只返回在线及 7 天内未归档的离线实例，已归档记录仅在 `include=history` 返回。
- 修复 Agent 凭据清理（`doops_credential_cleanup`）在绑定的 Kubernetes workload 已被删除（`NotFound`）时仍报错 `credential_workload_get_failed` 阻断后续 Secret 删除与凭据吊销的问题：清理阶段改用 `--ignore-not-found` 区分已不存在的 workload（跳过解绑并继续按 UID/resourceVersion 安全删除托管 Secret）与真实权限/接口故障（仍保持 fail-closed）。

## [0.3.41] — 2026-11-26

### Fixed

- 修复「AI 助手」在空会话尚未开始对话时自动弹出右侧「成果 1（`target.json`）」分栏且代码预览显示异常的问题：从工作模式「成果」列表中排除 DoOps 内部自动写入的目标清单文件（`target.json` 及原子写临时文件 `.target-*.tmp`，在「代码」模式下仍可浏览），空会话在产生对话记录前不再拉取或展开成果栏；同时为 `@agent-cloud/agent-ui@0.3.66` 内置的只读 CodeMirror 代码与 Diff 预览补齐深色主题与高对比度深色语法高亮（修复深色模式下左侧行号栏呈刺眼纯白及浅色语法配色发暗的问题）。

## [0.3.40] — 2026-11-26

### Fixed

- 修复 SQLite Gateway 启动未自动初始化资产台账与目标资源同步表，导致全新或升级部署访问 `/v1/assets/machines` 返回 `503 machine_inventory_unavailable`（以及 `target-manifest` 返回 `target_manifest_migration_required`、机器录入/导入与监控巡检全断）的问题。现于 `GatewayStore.migrate()` 内嵌幂等 SQLite 资产 DDL（单事务原子覆盖 `backend/docs/migrations/` 全部 14 份资产、监控、巡检、目录与目标资源同步的 33 张表、索引与触发器），并在启动时自动为老库补齐 `inventory_machines.active` 列及平滑升级早期 `asset_monitor_series` 约束（保留全部历史序列、样本、规则与告警），同时同步补齐 MySQL 建表脚本 `asset_monitor_series` 的设备/磁盘 IO/容器/数据库指标约束；运行期缺失或损坏表时仍保持 fail-closed `503`。

## [0.3.39] — 2026-11-26

### Security

- **破坏性**：自定义 MCP Server（`/v1/mcp-servers`）的创建/修改（`upsert`）、启停（`toggle`）、删除（`delete`）、全量同步（`sync`）与自定义 MCP 探测（`probe`）收紧为**仅平台管理员**可执行（非管理员返回 `403 mcp_admin_required`，且不再允许非管理员探测结果写回平台 `platform_mcp_servers` 全局工具清单）；普通用户保留只读查看已启用 MCP 元数据与探测内置 MCP 的能力。同时，`platform_mcp_servers` 中的 `env` 与 `headers` 改为使用 `DOOPS_GATEWAY_SECRET_KEY`（`v2:` AEAD + AAD）加密落库（未配置主密钥时保存含敏感字段的 MCP 直接返回 `503 mcp_secret_key_unavailable`），HTTP 列表与详情响应不再返回明文值、仅返回 `env_keys` / `header_keys` 键名，编辑省略时保留既有密文。
- 收紧 Skill 仓库的跨用户与定时调度隔离：非管理员查询列表与详情（`GET /v1/skills`）不再暴露其他用户已启用的自定义 Skill；`sync` 批量/单点同步与手动巡检只允许下发/执行系统 Skill 或本人拥有的自定义 Skill（防止管理员批量同步将非管理员自定义脚本推送至全网 Agent）；定时巡检计划（`monitor_schedules`）与 `scheduled` 调度运行仅允许绑定和执行系统 Skill（`scope=system`），引用自定义 Skill 时请求期返回 `400 custom_skill_schedule_forbidden`。同步收紧 Skill 附属文件相对路径校验（逐段强制 `^[A-Za-z0-9][A-Za-z0-9._-]*$`，拒绝 Shell 元字符与隐藏段）并在 `skillScriptCommand` 中对 `slug` 与 `entrypoint` 执行单引号转义与运行前复检。
- Agent 启动读取 `DOOPS_AGENT_ENROLL_TOKEN` 后立即执行 `os.Unsetenv` 清理进程环境变量，同时在 `doops_shell`/`doops_bg`、交互终端 `doops_terminal` 与 `stdio` MCP 探测等全部子进程入口统一剔除 `DOOPS_AGENT_ENROLL_TOKEN` 与 `DO_AGENT_MUTATION_AUTHORITY_SECRET`，防止子进程读取控制面令牌。

### Added
- 新增出站 Webhook 通知（P0）：Gateway 内置 `platform_events` outbox 与 `webhook_endpoints`/`webhook_subscriptions`/`webhook_deliveries` 四张表，事件驱动 worker 按 30s/2m/5m/15m/1h/3h/6h/12h 重试，2xx 视为成功；投递带 `X-DoOps-*` 固定头与 HMAC-SHA256 的 `X-DoOps-Signature`，secret 轮换后旧 secret 保留 24h。HTTPS-only，禁止重定向、代理和自动解压，DNS 解析后逐 IP 校验并直接拨号，拒绝 loopback、RFC1918、CGNAT、link-local、ULA 和云 metadata 地址；端点 URL 与 secret 加密落库，列表只返回 host 与摘要。管理面为仅平台管理员可用的 `/v1/admin/webhooks` 与 `doops admin webhook create|list|show|update|delete|rotate-secret|test|deliveries`，secret 只在创建/轮换时返回一次。已接入 `execution.finished`、`execution.outcome_unknown`、`agent.connected`、`agent.disconnected`、`monitor.alert.opened`、`monitor.alert.resolved`、`inspection.run.finished`。终态投递与事件默认保留 30 天，由 `-webhook-retention` 控制（`0` 永久保留），清理只在 worker 启动或被唤醒时按小时节流执行。完整设计见 `backend/docs/design/DOOPS_WEBHOOKS.md`。

- 控制台「终端」（「交互终端」PTY 与「命令」输出区）新增右键快捷菜单与同屏 **「AI 协同」** 分栏：拖选终端输出或命令后右键支持 **「复制选中内容」**、**「发送到 AI 执行」**（自动复制并在终端右侧展开同屏 AI 助手直接发起排查分析，PTY 自动随分栏宽度重排尺寸且不断连；若当前会话忙碌或模型未就绪则自动保底填入输入框）、**「填入 AI 输入框」** 以及交互终端 **「粘贴到终端」**（按住 `Shift` 右键保留浏览器原生菜单）；同时修复从 AI 助手点击「填入终端」在默认交互终端页签下未自动切至「命令」输入框的问题。
- 新增 `DOOPS_HTTP_SHUTDOWN_TIMEOUT`（默认 10s）：SIGTERM/SIGINT 之后关闭 HTTP server 的预算，与 `DOOPS_SHUTDOWN_DRAIN_TIMEOUT`（默认 2m，等的是在途操作）分开命名。以前这一段时间是代码里的字面量，运维看不到、也调不动。
- 三个「恢复窗口」从私有读取函数并进超时表，可按环境配置并在 `RUNTIME_LIMITS.md` 查到取值范围：`DOOPS_EXECUTION_RESUME_GRACE`（默认 60s，1s..10m，客户端等待重连 Agent 回传终态的宽限）、`DOOPS_EXECUTION_RESUME_TTL`（默认 30m，执行记录在 Gateway 侧的保留期）、`DOOPS_AGENT_EXECUTION_TTL`（默认 30m，Agent 侧执行认领的保留期）。
- 新增 `DOOPS_EXECUTION_RESUME_WAIT_MAX`（默认 5s，100ms..10m）：`GET /v1/executions/<id>?wait_ms=N` 一次请求最多阻塞多久。以前这是代码里的 5s 硬顶，运维把 `DOOPS_EXECUTION_RESUME_GRACE` 调到 10m 也照样在 5s 被截断，看起来像「调了没反应」。现在两者分开命名、分开可调，并且 `RUNTIME_LIMITS.md` 与隧道文档写清三件事的区别：一次请求能阻塞多久（wait_max）、记录为等重连 Agent 保持等待多久（grace）、记录可读多久（TTL）。

### Changed

- 控制台 AI 助手底座 `@agent-cloud/agent-ui` 从 `0.3.52` 升级至 `0.3.66`，移除 SDK 已下线的 `presentation="developer"` 属性，迁移至 `pnpm-workspace.yaml` 维护 `patchedDependencies`（兼容 pnpm 10/11），并在新补丁中延续「工具卡片文本进度展示」与「活动时间线激活后保持展开直至手动折叠」行为。
- 上述三项的超时值现在与其余超时项同一套规则：非法值（拼错的单位、负数、超出范围）**启动即失败**并打印 `gateway_timeout_config_invalid:` 前缀、点名变量与允许范围，不再静默回退默认值——以前写 `DOOPS_EXECUTION_RESUME_GRACE=60x` 会让 Gateway 照常启动、按 60s 跑，与配置写的内容完全相反。同时新增一条顺序约束：`DOOPS_EXECUTION_RESUME_GRACE` 不得大于 `DOOPS_EXECUTION_RESUME_TTL`（否则 Gateway 在它等待的那条记录到期之前就停止等待）。
- `DOOPS_TUNNEL_READ_TIMEOUT`（Agent 侧隧道读死线，默认 90s）与其余超时项同一套规则：非法值**启动即失败**并点名变量与范围，不再静默回落到 90s。同时新增一条顺序约束：读死线不得短于 Agent 租约的三分之一（也就是心跳周期），否则健康的隧道会在两次心跳之间被自己的读死线切断。
- 新增顺序约束：`DOOPS_EXECUTION_RESUME_WAIT_MAX` 不得大于 `DOOPS_EXECUTION_RESUME_GRACE`（一次请求的阻塞上限长于记录本身的等待窗口，客户端就会干等一个早已有答案的请求）。
- 新增 `DOOPS_EDGE_WS_READ_TIMEOUT`（默认 20s，1s..5m）：单条 Edge websocket 的空闲读死线，以前是代码里的 20s 常量、运维调不动。它与 Agent 侧隧道的 `DOOPS_TUNNEL_READ_TIMEOUT`（90s，绑 `DOOPS_AGENT_LEASE`）**不是一个旋钮**：合并会把 Edge 侧的失联检测从 20s 拉到租约量级，`RUNTIME_LIMITS.md` 的 I13 已写明这一条。
- 三个 Edge 预算从「每次调用读环境变量、非法值静默回落」改为启动时解析一次并列入 `RUNTIME_LIMITS.md`：`DOOPS_EDGE_WRITE_STALL`（默认 30s，100ms..10m）、`DOOPS_EDGE_CONN_ID_QUARANTINE`（默认 30s，1s..10m，新增约束：不得短于 `DOOPS_EDGE_DIAL_TIMEOUT` + 1s 的建链余量，否则旧握手的连接 id 会被提前复用）、`DOOPS_EDGE_HELLO_TIMEOUT`（默认 5s，100ms..1m）。非法值现在启动即失败，不再静默用默认值。
- `DOOPS_TOOL_TIMEOUT` 与 `DOOPS_COMPLETED_TASK_MAX_AGE` 改为只从启动快照取值：以前这两个变量每次取值都重新读环境，进程运行中改环境会让两者的生效时机不一致；现在启动后改环境不再影响已运行的流程（要改就重启）。

### Fixed

- 修复控制台顶栏目标切换器（`TargetSwitcher`）在「AI 助手」页面展开时被工作区内容盖住且点击无法切换的问题：`<header>` 的 `backdrop-blur` 形成了无 `z-index` 的层叠上下文，导致 `AI 助手` 命令列表既透字又拦截点击事件。现抬高顶栏层级（`relative z-30`），将目标选择器下拉面板改为 `createPortal` 挂载至 `document.body` 并使用不透明浮层与右对齐防溢出定位，同时优化实例/集群字号层级、支持 `Esc` 关闭与多目标（≥ 6 个）快捷搜索。

## [0.3.38] — 2026-11-24

### Added

- Agent 的命令过滤从「纯文本正则」升级为两层：结构化策略（argv 层）加正则兜底。新增可选策略文件 `DOOPS_COMMAND_POLICY_FILE`（JSON，含 `version` 与 `rules`，每条规则有 `name`、`tool`、`hint` 和 `operand`/`operandAt`/`argument`/`pathUnder` 四种 argv 条件），按解析后的 argv 判定：剥引号、展开 `-rf` 这类合并短选项、识别 `--` 终止符、丢弃重定向，并词法归一 `//`、`/./`、`/..`。内置 argv 规则 `deny_rm_root_filesystem` 让 `rm -rf //`、`rm -rf /*`、`rm -rf /./`、`rm -rf /../`、`rm -rf "/"`、`/bin/rm -rf /`、`/usr/bin/rm -r -f /`、`rm --recursive --force /`、`rm -rf / --no-preserve-root` 都报同一条规则名，`rm -rf /` 也从正则规则名 `delete_root_filesystem` 变为该结构化规则名；两层都命中时结构化 `name` 优先。`DOOPS_COMMAND_DENY_RULES` 与内置正则语义不变，继续兜底 `command rm -rf /` 这类不经 argv 层的 shell 形态。策略文件缺失、损坏、规则重名或超过 1 MiB 时 Agent 启动即失败，不会静默退回只剩正则层。格式与边界见 `backend/docs/design/COMMAND_FILTER.md`。
- Gateway 主密钥（`DOOPS_GATEWAY_SECRET_KEY`）支持原地轮换：同时给出 `DOOPS_GATEWAY_SECRET_KEY_PREVIOUS` 时进入两把钥匙的窗口，加密只用新钥匙、解密先新后旧；`RotateSecretKey` 每 200 行一批、每批一个事务地把凭据版本与机器登录口令库的密文重写为各行的原始 AAD（历史 `v1:` 行保持无 AAD），逐行 CAS 以免覆盖并发写入；全部换完后先自证「没有行仍只能用旧钥匙解开」，再在最后一个事务里把密钥标记（含机器登录库标记）翻到新钥匙。窗口期启动会打印一行 pending 提示（只报待重写载荷数），可在任意批次中断后重跑续做；两把钥匙相同时启动直接失败。运维步骤见 `backend/docs/CREDENTIAL_RESOURCES.md` 的「主密钥轮换」。
- `doops-gateway user create` 与 `user passwd` 新增 `-generate-password`：生成 24 位强随机口令（含大小写字母、数字与符号，排除 `0`/`O`/`1`/`l`/`I` 等易混字符），只向 stdout 打印一次 `password=<value>` 并提示「此口令不会再次显示」；同时新增 `-password-stdin`，从 stdin 读取一行（去掉行尾 CR/LF）作为口令。三个口令入参互斥，同时给出多个会 exit 2 并点名冲突的两个 flag。
- `doops-gateway secret-key rotate` 让主密钥轮换可以在命令行执行：钥匙只从 `DOOPS_GATEWAY_SECRET_KEY`（新的）与 `DOOPS_GATEWAY_SECRET_KEY_PREVIOUS`（旧的）读取，出现 `-key` 一类参数直接拒绝并以 2 退出（argv 会出现在 `ps` 与 shell history 里）；`-dry-run` 只读预览、零写入，输出只有计数与 marker 状态，不打印钥匙与密文。
- `doops-gateway store audit-scrub` 让历史审计行的 URL 清洗可以离线执行：DSN 只从 `DOOPS_GATEWAY_MYSQL_DSN` 读且永不回显，`-confirm-database` 必须与 DSN 库名逐字相同，`-backup` 写 0600 前镜像且先写后改，`-dry-run` 只读事务连 UPDATE 都不发（`CHECKSUM TABLE` 前后一致）；只把 `audit_events.command_summary` 里的 query 换成 `?<redacted>`、把 `error` 里 URL 的 userinfo/query/fragment 删掉，不清空整列不删行，无法解析的行整批拒绝并点名 id 与列，成功写一条 `action='audit:scrub'` 回执。
- 执行断线续接：新增 `GET /v1/executions/<id>?wait_ms=N`（N ≤ 5000）回读在途执行的终态，客户端带 `execution_id` 时重连后仍能取回结果，不再只能收到 `-32008 execution_outcome_unknown`。默认宽限 60s（`DOOPS_EXECUTION_RESUME_GRACE` 可配），超过宽限返回 `410 execution_expired`，未知 ID 返回 `404 execution_not_found`，同一 ID 重复查询从记录回放、不会重跑命令；Agent 侧执行与隧道连接解耦（`context.WithoutCancel`，上界 `maxToolExecutionDuration`，终态保留 30 分钟）。不带 `execution_id` 的调用语义完全不变。

- Gateway 的执行记录现在落库（SQLite 与 MySQL 同一张 `execution_records`）：Gateway 重启后仍能用 `GET /v1/executions/<execution_id>` 读回终态；重启瞬间仍在运行的执行收敛为 `failed`（`execution_result_unavailable`），不谎报成功也不凭空消失。
- 新增 `POST /v1/executions/<execution_id>/cancel`：按执行 ID 取消在途执行；已终态幂等返回当前状态、不改写历史；Agent 离线但记录仍在保留期时返回 `409 execution_agent_offline`（`retryable: true`）；与客户端 `tools/cancel` 共用同一个执行认领，重复取消幂等。

- 运行时超时与限额有了唯一一张表：新增 `backend/docs/RUNTIME_LIMITS.md` 记录 13 个可覆盖项（`DOOPS_TOOL_TIMEOUT`、`DOOPS_TOOL_DEADLINE_MARGIN`、`DOOPS_HELM_SETTLE_RESERVE`、`DOOPS_HELM_SETTLE_TIMEOUT`、`DOOPS_HELM_MAX_WAIT`、`DOOPS_AGENT_LEASE`、`DOOPS_AGENT_HEARTBEAT_FLUSH`、`DOOPS_EDGE_DIAL_TIMEOUT`、`DOOPS_MYSQL_CONNECT_TIMEOUT`、`DOOPS_MCP_PROBE_TIMEOUT`、`DOOPS_CREDENTIAL_DELIVERY_TIMEOUT`、`DOOPS_MONITOR_RETENTION_MIN_INTERVAL`、`DOOPS_COMPLETED_TASK_MAX_AGE`）与它们之间的顺序约束。进程启动即校验每个取值及其顺序（SIGTERM 宽限加结算不超过预留、Helm 预算不超过 MCP 预算不超过工具超时、心跳不超过租约的三分之一），非法值或断链直接启动失败并逐项点名变量、给到的值与允许范围（此前是静默沿用默认值）。默认值与既有行为不变；`DOOPS_TOOL_TIMEOUT` 继续接受裸数字并按秒解释。

- 超时与预算现在真的按环境变量生效。此前 13 个 `DOOPS_*` 超时变量只在启动时被校验，调用点仍各读自己的私有常量，把变量设成非默认值等于什么都没发生；现在 Helm 预留/结算/终止宽限/最大等待、MCP 探测与工具预算、edge 拨号、MySQL 连接、凭据投递、巡检保留最小间隔、工具截止余量都读同一张表，改一个值会同时改动派生的预算（如 Helm 调用截止、向 doagent 公布的 MCP 工具预算）。默认值与报错文案一字未变，非法值仍在启动时直接报错退出。
### Changed
- Gateway 的时间预算再收口 10 项：`-operation-timeout`、`-detached-task-timeout`、`-target-reconnect-grace`、`-shutdown-drain-timeout`、`-monitor-tick` 五个命令行默认值，HTTP 服务器的 `ReadHeaderTimeout`/`IdleTimeout`，离线实例回收周期（`DOOPS_OFFLINE_REAPER_INTERVAL`，默认 1h），离线实例保留窗口（`DOOPS_OFFLINE_INSTANCE_RETENTION`，默认仍是 90 天，此前是常量）与审计清理批间暂停（`DOOPS_AUDIT_PURGE_BATCH_PAUSE`，默认 100ms，此前是常量）都改读同一张超时表。默认值与报错文案一字未变；`DOOPS_TUNNEL_READ_TIMEOUT`（Agent 进程的读死线）与 `DOOPS_AGENT_LEASE`（Gateway 进程的租约，还驱动判活、心跳落库与实例回收）语义不同，保持两个独立变量，差异与后果写在 `backend/docs/RUNTIME_LIMITS.md`。
（`DOOPS_AGENT_LEASE`、`DOOPS_AGENT_HEARTBEAT_FLUSH`、`DOOPS_TARGET_QUEUE_TIMEOUT`，默认仍是 90s / 30s / 10m）。目标排队超时此前的 2 分钟兜底默认值永远不可能生效，现与命令行默认值统一。
- 代理（Proxy）写请求不再排在同一 route 的独占租约上：同一 route 的并发 POST 与读请求一样并行，实测 16 并发同 route 吞吐 43 → 480 rps、p95 360 → 42ms。**行为变化**：proxy 写请求不再有 per-route 排队上限，`-max-queued-per-target` 触发的 `target queue is full`（502）不会再落在代理写请求上；审计仍逐请求同步写入，顺序不受影响；Agent 升级栅栏语义不变（升级前仍须等在途代理请求排空）。
- **破坏性**：`doops-gateway user create` / `user passwd` 不再接受 `-password <value>`，带上它直接 exit 2，并说明 argv 会进入 shell history 与 `ps`。迁移：把 `-password "$PASSWORD"` 改成 `-password-stdin`，例如 `printf '%s\n' "$PASSWORD" | doops-gateway user passwd -db <db> -name <name> -password-stdin`，或改用 `-generate-password` 让 Gateway 生成；README 三种语言的自托管示例已同步为 `-generate-password`。同一批收紧 `doops-gateway user create`：现在必须恰好给一个凭据输入（`-password-stdin` 或 `-generate-password`），缺失时 exit 2 并说明两种合法做法，检查发生在打开数据库之前，不再创建无凭据、永远无法登录的账号。

### Fixed
- `POST /v1/executions/<id>/cancel` 不再泄露某个执行 ID 是否存在：此前对「记录存在但不是你的」返回 `403 execution_forbidden`、对不存在返回 `404`，客户端可以拿它当存在性预言机（同一条 ID 用 GET 问是 404，用 cancel 问就能分辨）。现在「不存在」「不是 owner」「对该 target 的授权已失效」三种情况一律 `404 execution_not_found`，与 GET 逐字节相同。
- Gateway 重启时收敛成 `failed` 的执行记录，用原 `execution_id` 重投不再谎称「终态不可读」：这类记录本来只有 `error_text` 没有 `terminal` 字节，旧实现照样去反序列化空字节、失败后返回 `-32009 the stored terminal state is unreadable`，而同一个 ID 用 `GET /v1/executions/<id>` 明明能读到真实原因。现在没有 terminal 字节时直接回放 `error_text`，只有真的反序列化失败才报不可读。
- 同一 `execution_id` 重投不再重复执行命令：Agent 进程重启后，Gateway 拒绝把在途执行重投给新进程，返回 `409` 与 JSON-RPC 错误码 `-32011 execution_agent_restarted`（`retryable=false`，文案说明命令可能已执行、需人工确认）；Agent 未上报 boot id（旧版本）时保持原有重投行为，并在响应与审计里标注 `agent_boot_unknown`。Gateway 自身重启后的记录恢复仍只回放终态。新错误码的正确性由结构性守卫守着：同一个码或同一个「前缀:」挂两个语义会直接测试失败。
- 凭据重连补投失败现在留下审计事件 `credential:redeliver`（含目标、投递类别与稳定错误分类，不含载荷、摘要或凭据 ID），此前只有一行 Gateway 日志，操作员无法回答「Agent 回来了但凭据为什么没到」。
- 运行期清理失败不再静默：`platform_load_samples` 与 `agent_connection_events` 的过期删除失败改为告警日志；`platform_load_samples` 的保留窗口从硬编码 30 天改为可配 `DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION`（默认仍是 30 天）。
- 审计保留与过期令牌清理拆成两个独立周期（`DOOPS_AUDIT_RETENTION_INTERVAL` 与 `DOOPS_EXPIRED_TOKEN_SWEEP_INTERVAL`，默认都是 6 小时），此前一个 6 小时常量同时驱动两件事，改一个会连带改另一个。
- 有历史 `v1:` 凭据密文的库此前无法用当前主密钥启动：启动自检把旧格式密文按带 AAD 的新格式去解，直接报密钥不匹配；现在按密文前缀选择信封形态，这类库可以正常启动并被轮换覆盖。
- MySQL 后端审计保留回执不再把「值未变」误判为「回执丢失」：连续无过期行的保留周期都成功并各留一条 success 回执；提交过批次后失败时回执改为 `status=error` / `error=audit_purge_failed`，tail 保留真实已删行数，不再出现 success 回执掩盖失败。
- `store mysql-copy` 的 dry-run 与正式运行现在共用同一套「目标端多出的表」分桶：dry-run 打印 `destination_only=40 (native_schema_only=33 legacy_destination_only=7)` 并逐表标注 `reason=native_schema_only` / `reason=legacy_destination_only`，正式运行给出相同的逐表清单与 `destination_only: total=40 (native_schema_only=33 legacy_destination_only=7)` 汇总，不再出现预览报 40、实跑报 33+7 两套数字。

- `doops-gateway store audit-scrub` 重跑或二次回滚不再误报 `audit row <id> column <col> is no longer present`：MySQL 对「写入值已存在」的 UPDATE 返回 0 affected rows，现在受影响行为 0 时在同一事务里复读该列，值相同视为已完成，只有行确实丢失或值不同才失败。

- 工具调用失败时写进审计的错误文本不再带 URL 的 query 与 userinfo：此前只过命名键脱敏，`?customer=<值>` 这类键名不敏感的查询参数会原样留在审计里；现在先按 URL 规则去敏再截断，保留 `scheme://host:port/path` 与错误正文。

- 已安装的 MySQL 库在启动时自动补上 `execution_records` 的 `terminal_truncated` / `terminal_bytes` 两列（缺列才 `ALTER TABLE ... ADD COLUMN`，表不存在时交给首次安装路径，重复启动不重复加列）。此前这类库重启后不会补列，截断的终态在回读时没有标记，长结果看起来像完整结果。
- 后台任务（`doops_bg` 加 `detach`）的输出不再丢开头、也不再漏脱敏：此前每个流式块都被按错误文本口径裁到最后 1200 字节（超过 1200 字节的块只剩尾部），跨越两块的凭据因为两半都不匹配模式而原样留下，64 KiB 上限又按字节下标切可能切断多字节字符，落库时还会再裁到 1200 字节。现在累积原始输出，只在读取与落库边界对整段文本脱敏，上限仍为 64 KiB，裁剪落在字符边界。
### Security

- Gateway 用户 token 的校验不再每个请求跑一次 bcrypt。新签发的 token 以 `sha256:<64 hex>` 存 `tokens.hash`（对完整明文求 SHA-256，常时比较），口令段本身是 128 位随机值，强度不变而单次校验从 62 ms 降到 15 µs；存量 `$2a$/$2b$/$2y$` 行在首次成功校验的同一请求内以 CAS 就地升级为 `sha256:` 形态，无需迁移脚本、不会让已发出的 token 失效，校验失败绝不升级。撤销、过期、禁用、不存在四条判定仍每个请求读库，未引入任何缓存（缓存会推迟撤销生效）。
- Agent 与 Edge 的 **enrollment token** 校验也不再每个请求跑一次 bcrypt：新签发的 token 存 `sha256:<64 hex>` 走常时比较，存量 `$2a$/$2b$/$2y$` 行在首次成功校验的同一请求内 CAS 就地升级，失败绝不升级；校验仍是每请求读库，没有缓存窗口，撤销与失效判定未改。MySQL 后端上 `tokens.hash`、`agent_enrollments.hash`、`edge_enrollments.hash` 三列同步由 `VARCHAR(64)` 扩到 `VARCHAR(191)`，已装旧库在启动时幂等扩容（否则严格模式下写入 71 字符的 `sha256:` 哈希会报 1406 数据过长）。
- 写进 `audit_events` 的 URL 与凭据在做审计落库前统一去敏：识别范围从「只认 http(s)」扩到任意符合 RFC 3986 的 `scheme://`（历史行里的 `postgres://user:secret@host/db`、`redis://:secret@host/0`、`mysql://...` 同样清洗），保留 `scheme://host:port/path` 与错误正文，删除 userinfo、query（对所有 scheme 统一删除，不判断哪个 key 无害）与 fragment，无法解析的 URL 整段替换成 `<unparsable-url>`；代理终态行（`finishProxyAudit`）、内部工具调用（`internal_call.go`）、Git 隧道（`git_tunnel.go`）与升级审计（`upgrade_audit.go`）都在落库前走这一步。不带 scheme 的形态（`user:pass@host:5432`、`git@host:repo`）与 `Authorization` 头回显如实列为已知缺口。

## [0.3.37] — 2026-11-24

### Added

- Gateway 新增 `store mysql-copy` 割接命令：只读快照（`mode=ro` + `query_only`，先跑 `PRAGMA integrity_check`）、DSN 只从 `DOOPS_GATEWAY_MYSQL_DSN` 读取且永不回显、`--confirm-database` 必须与 DSN 库名逐字相同、`--dry-run` 零写入评审（在空库上还会预告正式运行会先装哪份 schema、以及会得到什么判定，不再笼统报「目标为空」）、目标库已有任何数据行即拒绝、单事务搬运失败整体回滚并点名表、成功后逐表行数比对与基线行校验。空库可由命令自行装入与 Gateway 首启相同的 70 表 schema。
- `store mysql-copy` 现在把 **数据目录与库一起搬**，并自动处理自引用外键：`inventory_machines.host_asset_id` 这类父子环先以 NULL 写入、同一事务内回填，外键约束全程保留、搬完约束集合不变，真环则 `mysql_copy_parent_cycle` 点名行失败；`-source-data-dir`/`-target-data-dir` 搬运 `artifacts/` 与 `skills/` 两棵树，逐文件比对 sha256 与字节数，缺文件（点名 `run_id`/`item_id`/`slug`）整笔回滚并清掉已落盘的目标文件；旧库记录绝对路径时改写为相对路径并计数。
- CLI 支持 Windows：发布资产新增 `doops-windows-amd64`（与 darwin/linux 同一校验链进 `checksums.txt` 与发布验证）；`git_transport` 的平台相关部分拆成按 OS 编译的辅助层，Windows 侧用 `LockFileEx` 替代 `flock`、`CREATE_NEW_PROCESS_GROUP` 替代 `Setpgid`、`TerminateProcess`/`GetExitCodeProcess` 替代按进程组的 `kill`，`GIT_CONFIG_GLOBAL` 用 `NUL`。`install.sh` 把 `mingw*`/`msys*`/`cygwin*`（Git Bash）识别为 `windows`，CLI 仍装到规范路径 `~/.local/bin/doops`，另附 `doops.exe` 供 PowerShell/CMD 经 PATHEXT 解析；Windows 上中断清理只终止 git 主进程，不杀整棵进程组树（已写入安装手册边界说明）。`build-cli.sh --all`、`build-cli-release.sh`、`verify-published-cli-release.sh` 与发布契约测试的产物清单同步收录该资产；`x/crypto` 空引用从 go.mod 移除，`x/sys` 提为直接依赖。

### Fixed

- `store mysql-copy` 不再对全新/未迁移的 SQLite 快照误报 schema 不匹配：本版本原生 schema 有、该部署从未创建的表只在目标端为空时逐表记为 `native_schema_only` 并跳过，目标端有行即 `mysql_copy_destination_stale` 点名表与行数；`--dry-run` 与正式运行共用同一判定和退出码，31 表的全新活库可以直接割接到空库。

## [0.3.36] — 2026-11-24

### Added

- Gateway 支持 **MySQL 存储后端**（可选，默认仍为 SQLite）：设 `DOOPS_GATEWAY_STORE=mysql` 与 `DOOPS_GATEWAY_MYSQL_DSN` 后，空库启动时自动建 70 张表并写入 schema 基线台账，Skill 包与巡检产物改由 `DOOPS_GATEWAY_DATA_DIR` 存放；启动期校验数据库字符集与会话排序规则，DSN 覆盖排序规则或库内存在异构排序规则的表时启动即失败并点名。Skill 巡检、指标时序与对比、凭据全生命周期、目标资源清单同步均已在真机 MySQL 8.4 上验证；既有 SQLite 数据可经 copier 搬运，生产割接前仍需从活库重新抓快照并先在空库演练。

### Fixed

- Skill 驱动的巡检不再把「写入 N 条指标」显示成 0：这些运行的指标实际写进了趋势表并能查历史曲线，但运行摘要只统计旧策略快照，控制台在缺少摘要时就会报 0。现在按本轮真正落库的指标行数统计（指标点、桥接快照与策略快照）。
- 巡检保留策略被节流跳过时不再静默：每个 15 分钟窗口的第一次跳过会打一行 `monitor retention deferred`，说明上次扫描距今多久、产物与指标两个窗口各保留多少天，强制扫描后重新计算窗口。此前设置保留天数后，启动后的头一次巡检必然被跳过且不留任何日志，运维只能误判成「保留策略没生效」。
- MySQL 后端下巡检与目标资源同步此前报 `Error 1267 Illegal mix of collations`、控制台目标清单一直停在第 1 版：现按数据库默认排序规则协商会话（JSON_TABLE 派生列取会话排序规则），并在启动期校验会话、每张表、每个文本列的排序规则都等于数据库默认；DSN 覆盖、表级或列级不一致都会启动即失败并点名，驱动不认识的排序规则（如 `utf8mb4_0900_bin`）也会被直接拒绝而不是留到运行期报错。

## [0.3.35] — 2026-11-24

### Fixed

- 交互终端的 **Ctrl-C 恢复真正中断**：Agent 被 systemd/Kubernetes 以忽略 SIGINT 的方式拉起时，PTY 里的 `bash -l` 会继承 `SIG_IGN`，此前按 Ctrl-C 只回显 `^`C、前台 `sleep` 不结束、提示符也不回来。现在 Agent 在启动终端 shell 前把 SIGINT 从「忽略」改成「由 Agent 接管」（被接管的信号在 exec 时会恢复默认处置），子 shell 与嵌套进程都拿回默认 SIGINT；Agent 自身的优雅退出行为不变。

## [0.3.34] — 2026-11-24

### Fixed

- 控制台不再把「未执行任何巡检项」的巡检运行显示为通过：请求了不存在的 Skill 会在提交时直接报 `unknown_skill` 并列出不认识的 slug；在线目标一个巡检项都没执行时，运行结果记为失败并说明原因（请求的 Skill 未解析到可执行包、未指定任何可执行内容或目标无适用资产）。
- 旧 Agent 打开交互终端时不再显示英文 RPC 错误：0.3.33 只识别 Agent 自拒（`unknown doops action for tool: doops_terminal`），现在 Gateway 转发层的 `Unknown tool over WS: doops_terminal` 也归到同一句「该 Agent 版本不支持交互终端（需 Agent ≥ v0.3.32）」提示。

## [0.3.33] — 2026-11-24

### Fixed

- 交互终端不再把「连接交互式终端…」占位提示留在终端首行：连接状态仍在页签标题行显示，终端区只输出真实 PTY 内容。
- 目标 Agent 版本低于 v0.3.32（没有 `doops_terminal`）时，交互终端明确提示「该 Agent 版本不支持交互终端（需 Agent ≥ v0.3.32），请先用「命令」页签或升级该 Agent」，不再原样抛出 RPC 错误文本。

## [0.3.32] — 2026-11-24

### Added

- 控制台「终端」新增 **交互终端**（默认页签，另一页签为保留的「命令」）：直连 Agent 上的真实 PTY（`doops_terminal`），`cd`、`export`、`top`/`vim` 等全屏程序和 Ctrl-C 都按正常终端行为工作，刷新页面后可以用原 `term` 续接同一 shell，不再需要为每条命令启动新进程。终端绑定当前控制台会话，其他会话无法读取、写入或关闭它；Agent 每个会话最多 4 个、全局最多 32 个，空闲 30 分钟自动回收，「断开」会 `close` 掉 shell。旧版 Agent 没有该工具时给出明确失败提示，「命令」页签仍可用单条 `doops_shell` 执行。

## [0.3.31] — 2026-11-24

### Added

- 智能巡检改为由内置 **Skill 包** 驱动：内置 8 个巡检 Skill（虚拟机、容器运行时、Kubernetes 集群、应用站点、软件包漏洞、代理与暴露面、数据库、SSH 主机），每个包含 `SKILL.md` 与 `scripts/inspect.sh`，随镜像以真实目录嵌入并在首次运行时落盘到 `<dbDir>/skills/`，控制台可直接查看脚本内容并在线试跑。
- 「巡检详情」新增 **「与上次对比」** 与 **「历史趋势」** 两个面板：前者展示指标增减与检查项的新增、消失、变化，后者按 Skill 与节点展示时间序列；后端新增 `/v1/monitor/metrics` 时序接口，指标点写入 `inspection_metric_points`。
- 巡检产物改为 **File-First** 落盘：产物写入 `<dbDir>/artifacts/<日期>/<run_id>/<item_id>/`，数据库只保留路径、sha256、字节数与截断标记；单个产物文件超过 256 KiB 会被截断并注明原始字节数。
- 巡检详情在产物已被保留策略清理时不再空白或报错：接口显式返回 `artifact_state=missing`，页面正文区给出「产物缺失」与保留策略说明，可通过调整保留天数或重跑巡检恢复。
- 巡检产物详情新增 **图表可视化**：按产物 `charts[]` 渲染水位条（`bar`）、半圆仪表（`gauge`）与折线（`line`），颜色取该序列 `status` 的最差色调，超过 `max` 的条目标注「超额」；`max` 缺失或非正数时按序列最大值归一化，指标值非数值时跳过不画，未知类型或空数据整块不显示；产物已被清理时只保留指标与检查项记录，不画图表。演示模式（`/console?demo=1`）的机器巡检产物带一份图表，便于直接查看效果。
- 巡检产物与指标点开始按生命周期清理：产物默认保留 30 天、指标点默认保留 90 天（趋势图因此连续），每个巡检计划最近 50 次运行无条件保留，正在运行的巡检不会被清理；清理先删磁盘产物再删索引记录，目录删不掉时记告警并放弃该索引行，不影响下一轮。可用 `DOOPS_MONITOR_ARTIFACT_RETENTION_DAYS`、`DOOPS_MONITOR_ARTIFACT_KEEP_RUNS`、`DOOPS_MONITOR_METRIC_RETENTION_DAYS` 调整。
- 巡检产物顶层 `series_points` 数组现在会桥接进「Agent 监控」时序（`asset_monitor_series` / `asset_monitor_samples`）：Skill 产物按 `MonitorInspectSeriesPoint` 契约声明的点位经与手工上报相同的校验后入库，数据库巡检 Skill 已声明输出 `db_tps`（`tx/s`）与 `db_lock_waits`（`waits`）两项数据库指标，控制台「数据库」类型资产可直接渲染对应曲线。
- 平台管理新增 **「敏感词」** 页签与 Gateway 侧统一过滤器：同一规则表按「全部 / 提示词 / 命令」作用域过滤用户提交的提示词（含 dosql 直接执行的 SQL 正文）及 `doops_shell`、`doops_bg`、`doops_ssh` 原始命令，忽略大小写与连续空白；命中时以 `sensitive_prompt_blocked` / `sensitive_command_blocked` 拒绝并向审计写入 `blocked` 记录，不向终端回显命中词。支持按用户或 Agent 目标（集群 + 可选实例，空实例表示整个集群）配置白名单，规则与白名单持久化于 `<dbDir>/sensitive-filter.json`；过滤器带全局总开关且**默认关闭**（升级后同样保持关闭），管理员可在页签顶部显式启用，关闭时规则与白名单保留但不拦截任何请求（`PATCH /v1/admin/sensitive-filter`）。Agent 侧静态 `commandDenyRules` 继续负责 AI 自发 Bash 与结构化 K8s 命令。

- 「部署包」面板的新建/未保存内容以草稿行显示在列表顶部（带「未保存」标记），可直接打开继续编辑，也可丢弃而不触碰 Agent 上的文件。
- 「部署包」上传新增 **停止** 与 **继续上传**：停止会中断当前分片，未完成的文件单独收集，点「继续上传 N 个」只重试这些文件，不再需要整批重来。

### Changed

- 「部署包」的删除文件/目录、清理工作区改为先弹确认框（清理影响整个工作区与 Git 裸仓库），按钮按危险操作样式呈现；上传、保存、新建、清理互斥，执行中的操作不会再被并发点击打断。
- 「部署包」的文件错误改为中文说明（超出大小限制、二进制/非文本文件、路径越界、文件不存在、权限不足、磁盘已满、shell 退出码），不再直接抛出 Agent 原始报错。

### Fixed

- 修复「部署包」读取失败仍进入编辑态、可能用空内容覆盖原文件的问题：读取报错时保持浏览态并给出原因；切换文件前把未保存内容转成草稿，不再静默丢失。
- 修复没有在线 Agent 目标时点击「资产台账」没有反应、也没有提示的问题：导航被拒绝，并在目标选择器就地显示「当前没有可用的 Agent 目标，请选择在线目标」；部署向导与工作台目录在无目标时仍可正常打开。
- 修复拖拽上传时遮罩偶发不消失的问题（按拖入/拖出计数判断，不再依赖 `dataTransfer.types`）。

- 修复真实链路上点「停止」后终端与 AI 执行栏把取消报成执行失败（`execution_outcome_unknown`）的问题：Gateway 确认取消（`remote_canceled`）后不再显示红色「远端终态未确认」，改为黄色「已停止：Gateway 已确认取消，但没有回传最终输出；命令可能已经执行了一部分，请先核对实际状态」，并立即解锁输入；Gateway 对被打断命令「结果未知」的语义没有改动。

- 手动巡检请求只接受 `skills`，`strategies` 与 `ssh_hosts` 字段已移除并返回明确错误提示，前端同步停止发送这两个字段。

### Removed

- 移除内置原生策略采集器（`vm_host`、`ssh_host`、`k8s_cluster`、`container_runtime`、`app_service`、`database_service`）与 `doops_monitor_inspect` 工具，巡检能力改由对应 Skill 包提供（磁盘 I/O 与 Kubernetes 资源用量两项指标已随 Skill 包补齐，采集口径与降级行为见巡检设计文档）。

### Fixed

- 修复控制台终端「停止」等于没点的问题：`tools/cancel` 的应答（`canceled:true/false`）此前被整条丢弃，停止既不确认也不报错；现在终端依次给出「已请求停止」「远端已确认取消」或「停止没有生效：…」，并保持命令锁定直到真实终态，浏览器侧等待时间由 15 秒放宽到 35 秒（Gateway 侧确认窗口为 30 秒），不再把成功的停止误报成「远端终态未确认」。连接建立前后点停止都不会再被静默丢弃。
- 修复终端成功后硬编码「✓ 完成，退出码 0」而 `doops_shell` 成功并不返回退出码的问题：只有 Agent 真的给出退出码时才显示「✓ 完成，退出码 N」，否则显示「✓ 完成」；流式输出按行缓冲后再追加，不再在分片边界插入假换行，单次命令输出超过 20 万字符会截断并提示。
- 修复「AI 助手」主动停止被记成红色失败的问题：Gateway 确认取消的回合现在以 CANCELLED 结束，不再把会话标成 FAILED 或追加一条重复的错误时间线；未能确认的停止仍如实标为失败并保留「远端终态未确认」。
- 修复「后台运行任务」对已结束任务点「停止」报红的问题：Gateway 返回 404/409 时显示「任务已结束」，演示模式同样返回真实结果；提交后台任务后的提示改为指向「后台任务」页查看或停止。
- 修复「AI 助手」回合在其他标签页无法中断的问题：回合在后台运行时，控制台顶部常驻「AI 助手正在执行任务」提示条，可直接「停止 AI 任务」或「查看」，不再需要在被锁住的切换操作上先切回「AI 助手」才能找到停止按钮。
- 修复 Gateway MySQL 迁移中 `agent_status` 等表在真机导入时报 `Error 1364` / `Error 1048` 的问题：补齐缺失列与默认值，并对 TEXT 列按运行时方言显式赋值。
- MySQL 后端下重复写入的冲突处理此前完全不可执行（SQLite 的 `ON CONFLICT` 语法在 MySQL 上直接报错）：31 处全部改为按原语义生成 `ON DUPLICATE KEY UPDATE`，冲突键沿用原语句目标，`DO NOTHING`、条件更新与 `max()/min()` 各自保持等价语义；SQLite 路径行为不变。
- 修复 MySQL 后端下两处会**静默改坏数据**的写法：审计错误拼接 `error || '; ' || ?` 在 MySQL 默认 `sql_mode` 下是逻辑 OR，会把错误文本记成 0/1，现由方言层按后端生成 `CONCAT()`（NULL 语义与 `||` 一致，不用会跳过 NULL 的 `CONCAT_WS`）；四处 `INSERT OR IGNORE` 改为自赋值 `ON DUPLICATE KEY UPDATE col = col`，已存在的行不再被覆盖（SQLite 路径行为不变）。
- 目标清单卡片的资源类型补齐「容器」中文标签（后端观测到的 `container` 此前回退成英文 `container · Agent 观测`），「最后同步时间」更正为「最后观测时间」（展示的确实是 Agent 观测时间），并去掉「Agent 不支持同步」下与徽章矛盾的「已应用版本」一行。
- Gateway 的 MySQL 离线迁移补齐 JSON 展开与聚合的方言渲染：`json_each` 与 `json_group_array` 语句不再以 SQLite 语法进入运行时审计清单，数组聚合顺序改由读取端统一排序，坏 JSON 仍在解析处被两种数据库拒绝。

## [0.3.30] — 2026-11-24

### Fixed

- 修复 v0.3.29 升级到该版本的 Agent 上 `exec`/`ask`/shell/k8s/helm 等远程操作全部返回 `target_manifest_unavailable` 的问题：数据库观测项的 Key 采用 `命名空间/服务/引擎` 层级格式，但快照校验的元数据令牌规则不允许 `/`，Agent 端 `invalid_agent_observations` 被 Gateway 折叠成统一错误。校验改为按 `/` 分段逐项校验层级 Key；名称与身份的校验规则不变。
- 执行清单钉选降级：资源同步失败（迁移缺失、快照不可用等）不再否决 `exec`/`ask` 等执行调用——目标资源清单只用于审计引用与提示词事实，不是授权门控；同步失败时本次调用不带清单继续执行并在 Gateway 日志记录原因。

## [0.3.29] — 2026-11-24

### Fixed

- 修复审计清理（`audit purge` / 保留期任务）按文本 `started_at` 比较截止时间会把截止点之后近 1 秒内的审计行误删的问题：RFC3339Nano 在整秒处省略小数部分，`.5Z` 字典序小于 `Z`；改为按毫秒整数列 `started_at_ms` 比较，与列表筛选口径一致。
- 修复审计清理回执无法写入终态时可能残留一条 `running` 回执的问题：丢弃回执改为带重试并在失败时把错误一并返回，不再静默留下「看起来仍在执行」的清理记录。

## [0.3.28] — 2026-11-23

### Added

- 新增真正的 **「MCP 管理」**（`?view=assets&area=mcp`、Gateway `/v1/mcp-servers` 与 Agent 内部工具 `doops_mcp_sync` / `doops_mcp_inspect`）：统一管理内置 MCP（`doops`、`dosql`）与用户接入的自定义 `stdio` / `http` / `sse` MCP 服务；支持在在线 Agent 上执行真实 MCP JSON-RPC 2.0 `initialize` + `tools/list` 探测（返回实测延迟、服务端版本与完整工具 Schema），并将自定义 MCP 原子同步至 `/root/.agent/doops-mcp-servers.json` 并合并写入 `/root/.agent/settings.json` 与 `do-agent` 允许工具列表（保留模型配置不变），供「AI 助手」与「智能巡检」直接调用。
- **「Skill 管理」** 升级为共享能力中心：Gateway 侧将 Skill 正文与附属脚本迁移至本地文件目录存储（`<dbDir>/skills/<slug>/`）并在 SQLite 仅保留轻量元数据索引（兼容旧库记录回读）；新增「Agent 运行态 Skill」页签（通过 `doops_skills_inspect` 实时扫描目标 Agent `/root/.agent/skills/`，区分平台托管与镜像内置 Skill）以及卡片内 **「在线试跑验证」**（选节点与可选提示词原地触发真实执行并内联展示动态 Markdown + JSON 产物）。
- 巡检引擎（Gateway `monitor_schedules` / `monitor_runs` / `monitor_run_items`）新增通用的 **Skill + 提示词（`prompt`） + 任意直接命令（`command`）** 执行与产物持久化能力：支持在目标 Agent 上通过 `doops_agent_prompt`、Skill 内置脚本或经审计的 `doops_shell` 执行任意巡检命令，并自动解析与持久化 `artifact_format`（`json` / `markdown+json` / `markdown` / `text`）、`artifact_markdown`、`artifact_json` 与脱敏 `raw_output`。
- 控制台新增无硬编码字段的 **动态巡检产物可视化器**（`InspectionArtifactViewer`）：可对任意结构的 JSON 与 Markdown+JSON 巡检产物自动生成 KPI 指标卡、百分比水位条、状态徽章与动态推断列表格，支持在「可视化 / Markdown / JSON」间切换及一键复制下载。
- 「部署包」页的 Markdown 文件支持 **编辑 / 预览** 切换：预览复用控制台同一套 Markdown 渲染（禁用图片），源文件仍按原文保存。
### Changed

- 一级导航「文件」更名为 **「部署包」**（`?tab=files` 不变），并移除该页没有实际作用的「检查部署」按钮；部署检查仍通过 CLI `doops check` 与 Agent 工具完成。
- 精简 **「智能巡检」** 与 **「态势感知」** 页面并彻底清理冗余配置与演示残留：
  - 删除无后端支撑的 `inspection-mcp-panel`、`inspection-mcp-store` 与 `asset-inspection-panel` 文件；「智能巡检」仅保留「执行与记录」与「定时计划」两张纯巡检页签，移除临时 SSH 表单、硬编码告警阈值、按标签筛选、旧策略复选框及演示 HTML 报表入口，统一聚焦 **目标 + 关联 Skill + 巡检提示词 + 可选直接执行命令**。
  - 登录页移除「进入演示模式」按钮，默认直连真实 Gateway；`HolographicCore3D` 移除硬编码演示默认节点，无在线节点时如实展示空态；「态势感知」与报告生成器全部改为基于 Gateway 真实采样、实时阈值告警与巡检产物输出。

### Fixed

- 修复「部署包」页「上传文件 / 上传文件夹」在部分浏览器与内嵌 WebView 中点击无反应的问题：入口改为原生 `label` + `<input type="file">`，由浏览器直接唤起选择器，不再依赖脚本 `input.click()`；同时支持把文件或文件夹直接拖入页面，执行中拖入会提示原因而不是静默忽略。
- 修复「AI 助手」会话里执行轨迹的工具分组在长链路调用中反复自动展开／收起的闪烁问题：分组一旦开始执行就保持展开，直到用户手动收起（`@agent-cloud/agent-ui` 0.3.52 仓库内 patch），桌面端用户面与本页开发者面行为一致。
- 修复非演示（真实后端）模式下巡检与态势感知错误套用演示规则模板（如硬编码 `83.8%` 内存压力与演示模型文案）的问题；真实模式下仅呈现来自 Gateway 与 Agent 的真实巡检结果与产物。
- 只读 `ask` 不再因一次被恢复的调用而整轮报 `doagent_tool_failed`：`mcp_doops_k8s_read`、`mcp_doops_web_read`、`mcp_doops_kb_read`、`mcp_dosql_read` 因参数无效在协议层被拒绝（`mcp_error`，工具未执行），以及 `Bash` 命令非零退出（`command_failed`），在同名工具的后续成功调用后视为已修正，与 Helm 部署对未执行校验失败的修正规则一致。超时、取消、工具执行失败（`mcp_tool_error`）、被策略拒绝的命令、没有后续成功的失败，以及不同工具的失败仍按失败上报。
- Agent 的 `mcp_doops_k8s_read`、`mcp_doops_web(_read)`、`mcp_doops_kb_read`、`doops_helm` 与 DoSql 工具在拒绝非法 `operation` 时，错误信息现在列出全部合法取值，模型一次重试即可自纠，不再因反复猜测操作名耗尽调用。

## [0.3.27] — 2026-11-23

### Fixed

- 机器台账的「探针覆盖率」在读取失败时显示原因并支持重试。台账读取成功且有可见机器时才显示覆盖率。

### Added

- 控制台新增独立一级入口 **「Skill 管理」**（`?view=assets&area=skills`）及 Gateway `/v1/skills`、Agent 内部控制面同步工具 `doops_skill_sync`：支持同时管理 **系统 Skill**（预置 8 项主机/SSH/K8s/容器/Web/数据库及违规代理与补丁安全巡检 Skill，管理员可在线编辑、启停及一键恢复默认）与 **自定义 Skill**（支持用户上传 `.md` 或包含 `scripts/`、`references/` 的 `.zip` Skill 包，或在线创建编辑，原子同步至在线 Agent 的 `/root/.agent/skills/<slug>/`）；**巡检引擎全面升级为系统驱动 Skill**，定时巡检计划与即时巡检可直接选择系统或自定义 Skill 驱动执行并回传结构化巡检报告。
- 控制台新增独立一级入口 **「态势感知」**（`?view=assets&area=situation`），以 **业务系统 → 应用/数据库 → Kubernetes 集群 → 机器资源** 为整体架构，提供「全局态势大屏」（含可拖拽 3D 态势核心、3D 四层悬浮架构、3D 等轴测资源柱阵及多维度安全报表）、「业务→集群→机器拓扑」（3D 立体分层拓扑）与「机器与集群资源」视图；非演示模式下仅呈现真实 Gateway 接口数据。
- 控制台新增独立一级入口 **「智能巡检」**（`?view=assets&area=inspection`），统一收口「Skill 驱动巡检与记录」「安全巡检 MCP 服务」（支持独立开/关、单项与联合巡检、自定义接入，并区分 DoOps 原生直采与外部安全 MCP）和「周度合规与修复台账」。
- 新增项目验收与功能覆盖操作手册 [DOOPS_ACCEPTANCE_USER_MANUAL.md](backend/docs/DOOPS_ACCEPTANCE_USER_MANUAL.md)。

### Changed

- 精简 **「智能巡检」** 页面结构：移除重复二级标题与重复按钮，将原堆叠视图拆分为「执行与记录」「定时计划」「安全巡检 MCP」「合规台账」四张聚焦页签；默认页收敛为单行发起工具栏（Skill 勾选面板与临时 SSH 按需展开）+ 首屏巡检记录表，定时计划的高级阈值折叠收口。
- 优化 **「态势感知」** 呈现逻辑：重构为「态势定性与风险聚焦 → 业务/应用/集群/机器四层联动影响追踪 → 立体资源水位与根因处置」三段式布局；默认采用高清标准分层（支持一键切换 3D 立体/俯瞰与「仅看异常链路」），点击或悬停节点实时高亮上下游影响链；3D 态势核心新增标签碰撞避让与点击联动，统一真实口径消除集群/探针数字矛盾，空层折叠为紧凑引导条，并去除重复的巡检历史轨迹表。
- **「AI 辅助填写」**收口为仅在资产录入与编辑表单（机器、集群、业务系统、数据库、网站、镜像及个人资产登记）出现，不再挂载于登录、巡检、Skill 管理、平台管理与通用弹窗表单。
- **「平台监控」**回归纯粹的 Agent 维度（保留「平台总览」「Agent 节点」「使用排行」「运行与异常」四项页签）；「机器台账」移除重复的巡检页签、「业务系统」移除重复的拓扑页签、「目标运维 → 后台任务」回归当前目标 `exec --bg` 托管任务视图，彻底消除跨菜单功能重复。

## [0.3.24] — 2026-11-23

### Security

- Gateway 公网 Proxy 和 Git 隧道的审计摘要及活动操作不再记录请求 URL；公网 Proxy 不再回显底层错误。已有审计不变。

### Added

- Gateway 新增优雅停机排空与活动操作观测：支持 `-shutdown-drain-timeout`（默认 `2m`）在收到 `SIGTERM`/`SIGINT` 时等待在途操作结束，排空期间 `/health` 返回 `503`、新操作快速拒绝（RPC `-32010 gateway_draining`，HTTP `503` + `Retry-After: 5`）；`deploy-gateway.sh` 同步配置 systemd `KillSignal=SIGTERM` 与 `TimeoutStopSec=150`。
- Agent 反向隧道在普通断线或拨号失败时启用指数退避与 `[0.5x, 1.5x)` 随机抖动（上限 `30s`，稳定在线超 1 分钟重置），避免 Gateway 重启后所有 Agent 同秒重连冲击。
- 统一监控与策略巡检闭环引擎：Gateway 新增 `monitor_schedules`、`monitor_runs`、`monitor_run_items` 与 `monitor_resource_snapshots` 持久化结构、`-monitor-tick` 定时调度循环与 `/v1/monitor/{overview,resources/history,schedules,runs}` 接口；通过内部工具 `doops_monitor_inspect` 按目标下发资产清单至 Agent，Agent 依据资产类型自动执行 `vm_host`、`ssh_host`、`k8s_cluster`、`container_runtime`、`app_service` 与 `database_service` 六类只读策略，离线目标计入缺口而非通过，采集指标同步桥接至 `/v1/assets/monitor` 触发规则告警；控制台原左侧「后台任务」入口升级为「自动化」，统一承接「定时巡检与记录」与「后台运行任务（`exec --bg`）」，「平台监控」同步提供「系统资源监控」与「巡检任务与记录」页签，「Agent 监控」支持一键全策略巡检采集。
- 控制台表单提供「AI 辅助填写」：本地模型生成建议，可逐项确认；凭据载荷不参与，填入不提交。

- 机器台账标签支持 `key=value` 键值格式与分组交集筛选；「机器台账」新增「探针覆盖率（100%）」视图（含未绑定/离线/超时缺口分类、批量隧道补采与 CSV 导出），并整合「纳管覆盖率 (80%)」「智能巡检」及「业务系统 → 分层拓扑图」入口。
- 应用级监控新增响应时间（`response_time_ms`）、并发连接数（`active_connections`）与错误率（`error_rate`）指标：支持在线 Agent 原生探测已登记站点及回环 nginx `stub_status`（`mode=apps`），宿主采集器同步补齐 `--http-probe` 与 `stub_status` 活跃连接采集。
- Linux 宿主采集器支持按块设备采集读写 IOPS、吞吐和平均延迟；新增 Agent 隧道原生虚拟机/主机（`mode=host`，含 CPU、内存、内核、网卡、挂载点与整盘 I/O）与运行中容器（`mode=containers`，支持 Docker / nerdctl / Podman 的 CPU、内存与 `memory_limit`）监控采集，控制台「Agent 监控」与「机器台账」可直接触发观测、查看内存上限占比并设置阈值。
- 知识库新增 `doops_kb` 工具（`list`/`read`/`write`/`delete`）和只读的 `mcp_doops_kb_read`：普通 ask 会话可以直接查目标上由操作者维护的运维文档，写回仍走控制台或授权路径。删除改为移动到 `.trash/` 并按会话区同一过期时间清理；新增每库 500 篇、256 MiB 和单篇 1 MiB 的配额。

### Changed

- Gateway 消除全局锁内访问单连接 SQLite 的阻塞路径：`AgentRegistry.Register` 改为按目标串行并在锁外写库，`beginExecution` 将 `MarkExecutionDispatched` 移出双锁后复核，`AgentRegistry.Wait` 快路径改用读锁；WAL 模式下新增独立只读 SQLite 连接池承接鉴权/授权/登记/审计/目标查询，心跳落库合并为每 30 秒单事务批量写入。
- 重做控制台「平台监控」并补齐 Gateway 持久化与 `/v1/platform-monitor` 汇总接口：持久化离线 Agent 版本（`release_commit`）、上下线连接事件、分钟级并发排队水位、脱离后台任务（重启未完成任务恢复为 `outcome_unknown`）、`doagent` 大模型 Token/模型消耗与 `audit_events` 毫秒时间窗索引，支持按 `1h`/`24h`/`7d`/`30d` 查看调用与并发趋势、动作/模型分布、目标与账号排行及运行异常追踪。
- 调大控制台和公开页的正文、导航、表单及小字，知识库编辑器和 AI 助手沿用同一字号层级。恢复登录会话时，居中提示改用醒目的状态标题。
- 控制台知识库改用 `doops_kb` 读写，根目录由 Agent 返回而不是写死 `/root/kb`；预览不再加载远程图片，镜像不再下发 `docs/reviews`。
- 凭据列表用中文展示状态、版本和目标；编号与内容摘要收进详情。

### Fixed

- 共享 Gateway 部署可重复指定 `--verify-target`，按路由检查基线和重连身份，避免单个在线 Agent 掩盖其他已列出目标的掉线。
- DoSQL 批次出错时，版本日志保留已确认的 DDL 部分提交；旧日志的版本摘要可回读隐式提交数，无需改写历史。

## [0.3.23] — 2026-11-22

### Fixed

- 修复 `deploy-gateway.sh` 在 `--allow-unenrolled-credential-delivery` 校验中引用未展开变量、导致部署中止的问题；该开关仍要求同时使用 `--allow-open-agent-registration`。

## [0.3.22] — 2026-11-22

### Added

- 管理员可给机器打标签；机器台账与 DoOps Skill 支持按多个标签取交集，仅搜索当前账号可见的机器，不返回机器登录密码。
- 机器台账支持按筛选范围或单台机器发起主机巡检。列表显示采样时间、CPU 和内存状态，可从同一行打开该机器的监控详情。

- AI 助手空会话提供 DoOps 常用提示词：运行巡检、发布与交付、网站与数据库。点击填入草稿，编辑后再发送。
- `/console` 的视图、页签、资产台账范围、目录筛选和发布小节写入 URL：刷新后保留、浏览器前进/后退可恢复、链接可分享，不包含 token 或会话凭据。「平台管理」页签同步到 `?tab=`。
- 数据库、网站和镜像台账页与 Kubernetes 集群台账在已选在线 Agent 时可「直接操作当前目标」进入实时运维面板，演示模式可直达这些面板。
- 「Agent 概览」新增节点信息卡片：读取 uname、负载、内存、磁盘和 kubectl、容器运行时等工具链可用性，以徽标汇总展示。
- AI 助手头部新增「历史会话」菜单：从本人审计记录汇总当前目标的 `console-` 会话，显示最近活跃时间和记录数，点击即切换。
- 文件面板支持 64 MiB 内的任意文件上传（3 MiB base64 分片 + 远端 SHA-256 校验原子落盘）和单文件分块下载；行内提供删除、重命名、新建目录操作，均直接提交。
- `exec --bg` 现在把 `doops_bg` 交给 Gateway 托管（`detach`）：CLI 或浏览器断开后任务仍运行到真实终态；新增 `doops task list|status|stop` 查看和停止本人后台任务，Ctrl-C 只释放本地流并提示查询命令。
- 控制台新增目标页签「后台任务」：列出本人在该目标上的托管任务、运行中自动刷新、可查看输出并直接停止；终端新增「后台运行」按钮，Gateway 不支持 `detachedBackground` 或目标离线时禁用。

### Changed

- 「资产」集中数据库、网站、Kubernetes、镜像和凭据。Kubernetes 复用集群台账；物理机和虚拟机统一从「机器台账」进入，按类型筛选。数据库、网站和镜像用专属字段登记，按管理目标共享，旧个人登记不自动公开。平台管理移至独立页面，统一提供管理员功能和 Agent管理。

- AI 助手空会话页：输入框只保留一层圆角聚焦边框，去掉装饰吉祥物和空白占位；提示词分类改为低调的选中样式，模板卡片两行显示，模板开头直接写检查内容，不再重复标题。会话视图拉开层级：消息正文 15 px，用户消息带浅蓝底；工具行标签改为正文色，命令改用等宽字体，运行中、等待和失败状态用对应的状态色；超宽屏会话列放宽到 1024 px；弹出菜单搜索框不再出现方形聚焦框。
- 控制台把 `@agent-cloud/agent-ui` 从 0.1.4 升级到 0.3.52：AI 助手改用工作区模式（轻量、工作、代码、目标、终端）、统一输入框与「Execution trace」抽屉，工具行继续显示文本进度，执行证据不再在会话中重复渲染。
- 修复模型在 Agent 启动后下发时，AI 助手仍把模型显示为 `unconfigured` 的问题。
- 离线 Agent 保留完整工作区入口，支持查看审计和历史指标，重新连接后自动恢复当前工具。
- 主导航移到左侧，菜单使用深色背景和高对比度文字。桌面常驻侧栏，窄屏从左上角「菜单」打开抽屉。在线与离线 Agent 共用工作区的资产和监控页面。
- 首页集中展示运行统计、集群概览和近期活动，不再重复左侧菜单的五个工具与接入入口。单台机器状态保留在「Agent 概览」。控制面监控支持按目标浏览指标。
- 控制台先选择 Agent，再进入该目标的运维、资产和监控。平台控制面展示汇总监控，并支持管理员按机器、集群或业务系统为多个 Agent 分配资产。
- 放大控制台顶栏的 DoOps 标志。监控页缺少采集目标时显示范围内的机器数，并提供机器台账入口。
- 控制台统一使用主题下拉菜单，看板只有一个可选范围时直接显示范围名称。
- 平台和集群共用资产看板，切换范围时保留当前页签。机器、集群和业务系统归入资产台账。目标运维合并重复快捷入口，AI 助手和终端归入操作台。
- 机器台账支持粘贴带分组的服务器清单，核对管理 IP、内网 IP、SSH 端口与账号后批量导入，可选保存机器登录凭据。
- 本机 3000 端口的控制台登录页默认连接 `http://127.0.0.1:42222`。
- 提高深色登录页的文字对比度，调亮输入提示、错误信息和博客链接。
- 引入 shadcn/ui 风格组件层（Radix Primitives + Tailwind v4，样式沿用现有主题）：布局原语 `Stack`/`Inline`/`Grid`/`Container`/`Spacer`、`Card`、`Badge`、`Separator`、`Tabs`、`Tooltip`、`ScrollArea`、基于 `react-resizable-panels` 的 `ResizablePanelGroup`，以及统一的 `cn()` 类名合并。首页统计卡片、两栏布局和状态标签改用这些组件。
- 控制台导航、目标选择器和 AI 助手的状态与模型标签统一使用 14 px 字号。
- 知识库与文件页切换菜单、目标或会话后保留未保存草稿；文件与知识库操作执行中锁定目标、会话和导航切换。
- 升级控制台知识库与 Markdown 组件：渲染层引入 `Streamdown`（内置 Shiki 多语言高亮、交互式 Mermaid 图表、KaTeX 数学公式、CJK 中文排版、表格导出与提示块），数据库和网站结果继续以纯文本展示外链图片；「知识库」编辑器引入 `CodeMirror 6`，提供快捷格式与运维块插入工具栏、快捷键（`⌘S` 保存等）、可拖拽分栏预览、文档筛选与目录大纲导航。

### Fixed

- 控制台「知识库」保存改为先写同目录临时文件再替换，单篇上限与读取上限对齐为 1 MiB：超限时提示拆分，而不是写入后在会话里读不回来；保存前比对节点上的最新版本，被其他会话改过时可选择覆盖或重新加载；有未保存修改时关页给出提示；预览不再加载远程图片。
- 文件、知识库、数据库、网站、Kubernetes、镜像、节点信息或 AI 助手执行栏有操作在运行时，菜单、页签、会话切换和浏览器后退现在一并锁定，不再只对终端生效；操作结束后提示自动消失。
- session 输入框改为按 Enter 或移开焦点后才切换会话，输入过程中不再逐字重建面板，也不会在目标上按半截名称创建工作区；文件页有操作在运行时该输入框禁用。
- 文件页保存期间继续输入的内容保持为未保存草稿；上传失败时显示失败文件和原因；上传与删除、重命名、下载、新建目录不再同时进行；快速切换目录时不再显示较早请求的结果。
- 「Agent 概览」切换目标后不再显示上一个目标的节点信息；AI 助手「历史会话」最多汇总最近 1000 条审计记录，超出时给出提示。
- 浏览器禁用本地存储时控制台仍可登录和使用，只是不记住 Gateway 地址和引导状态。
- AI 助手在独立 Docker 或 Linux 原生目标上巡检不再因 `mcp_doops_k8s_read` 失败而整轮报错：Agent 先探测目标运行时，Kubernetes API 不可达时不提供 K8s 读取工具，改按主机与容器方式巡检。
- 修复 Agent 升级在排队时仍准入新操作、被持续读流量或 shell/apply 负载拖到 `target queue timeout` 的问题；升级等待期间 target 返回 `target busy`，等待结束后恢复准入。
- 修复终端在输入法选词时按 Enter 误执行、按方向键误切历史的问题；非空命令保留原始空白和引号。
- 修复已选在线 Agent 后打开概览或工具仍提示「当前没有可用的 Agent 目标」的问题。
- 固定 DoAgent 0.2.47：命令执行与回放之间保留原始结果和产物路径，重连、重放或续跑后工具证据不再丢失或被截断。
- Helm 直接回滚或卸载中断后尝试观察 release 状态，原操作结果仍为未知。
- 控制台区分目标 Agent 在线状态与 AI 助手会话连接状态。平台资产看板或业务系统台账读取失败时，提示检查对应的数据库和迁移记录。
### Security

- Agent 注册默认要求 enrollment token，并新增 `DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=1`
  环境开关（等价于 `-allow-open-agent-registration`）供没有 enrollment 的私有部署临时开放连接；
  完全没有 enrollment 的私有 Gateway 可再加 `DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=1`
  恢复旧凭据投递（不安全，仅迁移窗口）；未登记的会话默认不再接收模型密钥或镜像仓库凭据，
  `doops targets` 标出未登记目标。升级前先用 `doops-gateway enroll create` 登记现有
  Agent；确有迁移需要时可临时使用 `-allow-open-agent-registration`。
- Gateway 启动时校验 `DOOPS_GATEWAY_SECRET_KEY` 与数据库中凭据密文匹配；不匹配时拒绝
  启动并给出明确错误，运行期解密失败归类为 `credential_key_mismatch` 或
  `credential_ciphertext_invalid`，不再落入通用凭据错误。
- 控制台登录限流使用带 HMAC 签名的浏览器地址，单个用户输错口令不再锁住其他控制台用户；
  登录成功后清除该（地址，用户名）的失败计数。WebSocket 来源校验同时比较主机和端口，
  控制台增加 `frame-ancestors`、`X-Frame-Options`、`nosniff`、`Referrer-Policy` 和
  `Permissions-Policy`，博客 SVG 响应带沙箱 CSP。

### Changed

- 工具时限改为跟随 Gateway `-operation-timeout`（默认 60 分钟），Agent 的
  `DOOPS_TOOL_TIMEOUT` 作为保险上限；Agent 侧超时返回 `agent_tool_timeout` 并记为审计
  状态 `timeout`，不再误报为取消或 `outcome_unknown`。Helm 新增 `toolTimeout`。
- Agent 输出改为分块转发，取消 1 MiB 单行上限；命令已成功退出但仍有进程持有输出管道时，
  只提示输出不完整，不再把结果改成未知。同一进程组残留的后台进程仍会在宽限期后停止，
  但结果按已知退出码返回。
- RPC、Git 和内部调用在目标重连时最多等待 10 秒（`-target-reconnect-grace`），探测类
  调用保持立即返回；Agent 隧道读超时从 20 秒调整为可配置的 90 秒，并在断线时记录被结束
  的执行数量。
- CI 与构建镜像固定 pnpm 10.18.0（`packageManager` + corepack），Go 测试与发布统一使用
  1.25.14（`toolchain`）。

## [0.3.21] — 2026-11-21

### Fixed

- Web 控制台部署支持显式配置公开 HTTPS origin，并验证登录请求的同源校验。

## [0.3.20] — 2026-11-21

### Fixed

- Helm 直接回滚或卸载中断后尝试观察 release 状态，原操作结果仍为未知。

## [0.3.19] — 2026-11-21

### Fixed

- Git 隧道取消测试为取消请求使用独立 RPC ID，避免把原请求的终态误认成取消回执。

## [0.3.18] — 2026-11-21

### Fixed

- 修正 Linux 上会话文件的相对路径解析，并按凭证类型路由清理，避免普通凭证操作受模型配置目录权限影响。

## [0.3.17] — 2026-11-21

### Removed

- 移除 DoSql 内置的 CockroachDB 镜像识别和专用连接协议别名。

### Fixed

- Edge 注册持久化成功后才调度连接，并在注册时复查会话上限。HTTP 与 Git 隧道在响应提交后的转发错误会中止响应流。
- 工作区文本工具以会话目录限定文件访问。已有工作区采用原子目录替换。DoWeb 的 Docker 安装将自定义目录作为完整挂载参数传递。
- CLI 历史记录省略 ask 和 exec 正文，流式调用拒绝缺失的终态。控制台升级可按操作编号恢复观察。
- 绑定变更或撤销后，受管凭证需要重新验证。模型凭证写入中断后清除待确认配置。Registry 授权文件写入失败时尝试恢复物化前状态。
- DoSql 直接查询和验证拒绝有明显副作用的语句形态，MongoDB 校验解码后的文档键。

## [0.3.16] — 2026-11-21

### Fixed

- DoSql 目标发现支持省略 `--request`，识别常见 Oracle Database 和 CockroachDB 镜像，并从 MySQL、PostgreSQL 的 JDBC URL 提取库名。

- `registry sync` 逐端读取并校验非空 manifest，读取失败或 digest 不一致时返回失败。修正发布 Skill 中的工作区路径和 Kubernetes 参数示例。

- CLI、Skill 与中英俄文档统一了 Gateway 登录、路由发现和 Agent enrollment 的说明。
- 修正工作区文件同步、镜像构建和 Agent 升级示例，区分 COS 快照与远端会话当前文件。

## [0.3.15] — 2026-11-20

### Added

- 数据库页可逐层打开实例、数据库和表，查看表数据，并在当前库运行只读 SQL。

- 机器支持单个账号录入、CSV 批量保存登录凭据，以及从 XLSX 给已有资产补录。管理员可从机器详情查看已保存的账号；预览和回执分别列出待写、实际写入和已有凭据数。

- 数据库详情新增「只读 SQL」，可明确选择受管凭据并在操作区查看实际行、列和执行 ID。

- 资产入口增加平台和集群看板、业务系统及依赖拓扑、双 80% 盘点对账、监控趋势与周检报告。对应迁移、采集源及独立周检执行器的配置入口见 `backend/docs/DOOPS_API_GUIDE.md`。

- 控制台「网站」默认列出当前目标通过 DoWeb 发布的站点，可筛选域名、查看当前及上一发布版本并进入巡检。站点列表需要支持 `doops_web site-list` 的 Agent。

- K8s Skill 提供 `workload-status` 和 `release-status`，可一次读取工作负载及异常 Pod、相关 Warning 事件或 Helm 修订历史。

- 控制台新增「资产」入口，支持物理机和虚拟机手工登记、CSV 预览与批量导入、规格编辑、宿主及 Node 关联。管理员可通过在线 Agent 验证并保存集群或指定命名空间，离线时仍可查看台账。启用前需执行资产表迁移，顺序见 `backend/docs/DOOPS_API_GUIDE.md`。

- 控制台「数据库」标签默认展示当前目标发现的实例，支持筛选、查看发现详情，并将选中资产带入 DoSql 只读查询。

- Kubernetes 资源列表可直接查询指定命名空间的 Pod 和 Deployment。节点列表可查看 Kubernetes 上报的 CPU 与内存容量。

- 控制台的「Kubernetes」标签默认显示 Pod 列表，可切换 Deployment、节点和命名空间，按名称或命名空间筛选，点击资源名称查看详情。

- DoSQL agent 脚本支持 `--help`，按 discover/read、change、journal、timeline、rollback/restore 分组列出全部命令；测试保证清单与实现不会漂移

### Changed

- 主导航按工作范围整理为「目标运维、资产、发布部署、平台管理」。CI/CD 指南并入发布部署。平台及集群统计集中在资产看板，目标运维展示所选 Agent；从资产进入运维后可返回原来的机器详情或集群命名空间。

- 结构化 kubectl 写操作返回非零时，Gateway 回执包含命令退出原因与脱敏后的错误输出。

- CLI 帮助和运维指南明确了 `exec` 可在目标授权范围内执行已确定的原生 `kubectl` 或 `helm` 命令。`doops k8s` 是可选的结构化入口，已有脚本可沿用原调用。

- exec、后台任务、ask 的 Bash 及 K8s/Helm 工具在 Agent 启动命令前执行同一组拒绝规则。可通过 Agent chart 的 `commandDenyRules` 追加规则；拒绝携带规则名和下一步提示。

- Operator Skill 补充 `capabilities` 与 `version` 命令入口，并写明 `upgrade` 在缺少 `upgradeAudit v1` 的旧 Agent 上必然失败、`--dry-run` 只预览参数而不校验活体 release、挂载或权限
- Operator Skill 按渐进引导收敛：红线不再重述命令表中的操作细节，同一主题只留一处入口与文档指针
- Operator Skill 命令表按实际 CLI 校正：补 `uncordon`、`admin user password|disable`、`info`、`history` 与 `-contract`，说明 `k8s` 不开放读取 Secret/ServiceAccount，并写明 `sql --dry-run` 会先执行再回滚

### Fixed

- DoSql 安装说明改为当前登录命令 `doops login --gateway <gateway-url>`。旧文仍要求先 `doops add` 再 `login --target`，那是首次登录尚不能发现 target 时的写法。
- 公众号草稿的上手步骤不再把 user token 写入 `doops add --token`，安装命令补上必需的 `--version`。API 鉴权示例的 `next_action` 与 Gateway 一致，改为 `doops login --gateway`。
- 数据库发现按 StatefulSet 控制者 UID 归并所属 Pod，根据实际 Service 与 selector 展示地址，保留成员资产引用。
- DoSql 只读查询使用当前 Agent 二进制启动数据库 runner，支持从本机目录运行 Agent。

- 新会话首次打开「网站」即可读取目标的站点发布记录，无需预先创建会话工作目录。

- 同一会话的普通 `ask` 和 `exec` 发生冲突时，后到的请求按队列配置等待、超时或取消。
- 普通 `ask` 返回本轮工具失败与 Bash 实际退出码。受管 Bash 等待后台请求的执行终态，保留标准输出和标准错误。Gateway 审计分别记录失败、超时和取消。

- CLI 顶层 help 补上 `guide` 与 `history` 两个实际可用但未列出的命令，并修正 `credential`、`registry` 两行的 tab 缩进
- DoSQL 文档不再声称支持 Oracle：实现只支持 MySQL、PostgreSQL 和 MongoDB

## [0.3.14] — 2026-11-18

### Fixed

- DoSQL 版本日志改为按磁盘上的原始字节校验哈希，并在写入与计算前统一做 UTF-8 规范化：含非法 UTF-8 的语句不再因为写入与读回序列化结果不同而被判为损坏，执行 `begin` 后未正常结束也不再被当成损坏条目，后续 `apply` 可以继续按已用最大版本号递增

## [0.3.13] — 2026-11-17

### Fixed

- Kubernetes 执行按钮保持固定位置，运行时停止按钮独立显示，避免双击执行误触取消或在快速返回后重复提交；窄屏错误提示不再挤压按钮文字
- Web 镜像包含 `public` 静态资源，修复线上品牌图片 404

## [0.3.12] — 2026-11-17

### Fixed

- 受管 Helm 支持 session 工作区根目录的 Chart（`chart="."` 或 `"./"`），避免合法路径被误拒后反复猜测；values 仍须为文件，路径不能逃逸 session

## [0.3.11] — 2026-11-16

### Changed

- Kubernetes 与 DoWeb 明确操作直接执行，移除重复的 `confirm` 参数和 Kubernetes CLI `--confirm`；旧调用须删除这些字段
- 控制台执行与管理按钮直接提交操作，去掉二次确认弹窗和确认框；skill 沿用已有任务继续修复，不逐次索要批准
- 保留 Gateway target 身份认证、凭据隔离、只读工具边界、并发互斥与真实终态；缺失回执不会被改报成功或自动重放
- 将 DoAgent 运行时锁定到已在线验证的 `af62d438` 和对应镜像摘要，保留截止阶段继续修复与进度通知，避免后续构建降回旧运行时

### Fixed

- COS 源码快照支持 `[...path]`、`[[...path]]` 等合法文件名，不再把连续点号误判为父目录穿越

## [0.3.10] — 2026-11-16

### Changed

- 固定 DoAgent `af62d438` 与已验证镜像摘要；构建逐项校验已提交源码，并从该运行时重建匹配的 base-light 和 Agent 镜像
- 增加全部 13 个 Agent 的发布进度与逐目标验收记录；旧协议首升沿用独立控制器、配置备份及失败自动恢复

## [0.3.9] — 2026-11-16

### Fixed

- 缺少 Helm 原始回执时继续保留未知终态；后续同意图成功只能关联历史回执，不能把未知变更改报为已验证成功
- 浏览器 apply watchdog 延长到 Gateway 60 分钟执行期限之后，截止阶段继续暴露已授权的修复工具并发送进度

## [0.3.8] — 2026-11-15

### Fixed

- 延长普通 prompt/apply、Gateway operation 和 CLI 等待窗口，允许 Helm 等长时间工具在同一授权任务内继续执行并持续发送进度
- 受管 Helm 的 `--wait` 上限提高到 25 分钟，完成复核最多允许六次有界修正；仍保留未知终态保护、完整工具回执和明确终态

## [0.3.7] — 2026-11-15

### Fixed

- 发布请求因预算耗尽、取消、复核确认无法完成或复核失败而提前结束时，保留已成功的 Helm 回执与本轮停止原因，整体返回未完成；不再把部分工具成功误报为整个请求成功
- 固定 DoAgent 0.2.46：串行 MCP 调用按真实执行边界依次返回开始与结果事件，避免同一请求内的失败修复被误判为并发执行；保留原始回执和可信证明

## [0.3.6] — 2026-11-15

### Fixed

- Helm 数字和布尔覆盖使用原生 `--set-json`，字符串继续使用 `--set-string`；小数与指数数字不再被 Helm 当成字符串，schema 与模板可读取正确类型
- MCP 注入 session 时保留原始数字 JSON，避免在交给 Helm 前经由 `float64` 舍入

## [0.3.5] — 2026-11-15

### Fixed

- Helm `set_strings` 保留字符串、数字和布尔的原生类型；字符串使用 `--set-string`，数字和布尔使用 `--set`，避免 Chart schema 或模板把 `false` 当成非空字符串

## [0.3.4] — 2026-11-15

### Fixed

- 受管 Helm 的 `set_strings` 接受字符串、数字与布尔值，统一按 `--set-string` 的字符串形式传给原生 Helm。`loki.useTestSchema: true` 这类 Chart 原生写法不再因参数类型在请求校验阶段被拒绝；对象与数组仍会被拒绝

## [0.3.3] — 2026-11-15

### Fixed

- 固定新版 DoAgent 运行时：完成复核的修正建议会进入后续执行，诊断工具调用不再消耗最终答复的修正次数；取消、时间与 token 预算覆盖复核调用
- Helm 的普通 `data` values 不再仅因键名被拒绝；内容中的凭据仍执行原有隔离检查
- Helm 错误保留多行诊断，避免丢失 schema 报错中的缺失配置项；失败与修复后的回执继续完整保留
- 发布诊断使用独立的进度通知，模型说明不会替代最终工具证据；将剩余执行期限传入 doagent 的本轮运行配置
- `ask --apply` 显式启用 doagent 的完成复核；模型过早给出结论时在原授权和同一轮次内继续处理未完成事项，工具失败回执与未知终态仍完整保留
- 工作区同步遵循 `.gitignore` 的有序反向规则，提交中明确放行的无密钥构建契约不会被快照阶段误删
- `pull` 捕获命令输出时排除独立的进度心跳，避免大型工作区已成功生成快照却报缺失 commit
- 清理 skill 与运维文档中遇错立即停止、重复要求用户操作及历史未知结果永久阻塞 target 的旧指导
- 开发 Skill 备份迁出加载目录，避免 `docs.release` 等历史副本继续向 Agent 提供已删除的 Helm 门禁

## [0.3.2] — 2026-11-15

### Changed

- 密码登录 token 的默认有效期从 24 小时改为 30 天，并在使用过程中自动顺延，但距签发时间最长不超过 90 天。可用 `-login-token-ttl` 和 `-login-token-max-lifetime` 调整。管理员签发的服务 token 不受影响
- `doops login` 以 `--gateway <name|url>` 指定 Gateway（登录属于 Gateway，不是 target）；`--target` 仍可作为别名解析到对应 Gateway
- Helm 发布直接执行原生 `upgrade --install --wait`，以实际 release 的 deployed 状态及本次 revision/description 判定成功；取消自设 server dry-run、manifest 类型白名单和工作负载/Ingress 二次验收，无 workload 的 NetworkPolicy 等 Chart 可正常发布
- Helm 调用移除 `expected_image` 与重复 `confirm` 参数，授权沿用 `--apply`；不再强制 `--reset-values`，values 使用 Helm 原生语义，`atomic` 与 `take_ownership` 仍由用户选择。迁移时删除旧参数并复核 values 合并结果，详见 `backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md`
- Helm 文档与 skill 不再要求唯一发布入口、CI 或镜像证明；明确授权的原生 Helm 命令也可经 `doops exec` 执行。保留身份与受管凭据隔离、可信结果及失败/未知终态，Helm 发布成功与业务健康验收分别报告
- 发布规则统一归入业务发布契约，清除重复的 Helm 工作区文档、未使用的固定 target 配置和已退役的安装参数示例；DoOps 自身升级要求不再混入普通业务发布说明

### Security

- 可为单个 target 签发 enrollment token。已在线 Agent 保持连接；该 Agent 用 token 连上一次之后，这个 target 的后续注册才需要它。未签发的 target 仍按原来的 cluster/instance 注册。不要使用 user token
- 控制台的变更请求要求 `Origin` 与控制台自身来源一致
- 密码登录对连续失败限速；未知用户名和错误密码走同一条口令校验
- 登录限速按客户端地址和「地址 + 用户名」分别计数，不会单凭用户名把别人锁在外面；经本机或内网反向代理时按代理传来的客户端地址计数；计数表有上限，被限速的请求不再做口令校验
- 某个 target 确认 enrollment 之后，未带 token 的连接即使已经通过握手，也不能顶替已在线的 Agent
- Agent 注册被 Gateway 以 401/403 拒绝时按指数退避重试（最长约 60 秒），连上后复位，并在日志里提示检查 `DOOPS_AGENT_ENROLL_TOKEN`
- 控制台同源校验在 TLS 反向代理后按 `X-Forwarded-Proto`/`X-Forwarded-Host` 推断来源，也可用 `DOOPS_CONSOLE_ORIGIN` 显式指定
- `adopt` 接管 Agent 时保留通过 Secret 注入的 enrollment token，无法保留的注入方式会直接拒绝接管

### Fixed

- 已绑定私有 Helm 仓库的 target 可以直接下载其他来源的公共 Chart；GitHub Release 的 HTTPS CDN 跳转经过逐跳校验，私有凭据不会发送到公共下载地址
- Helm 参数拒绝保留结构化失败回执和可信证明，避免一次空参数调用把整轮错误误记为 `raw_output_missing`；真正缺失证明的结果仍保持未知
- Helm 失败后可在原任务内定位、修复并继续验证；同一意图的后续可信成功关联原失败，完整保留每次工具回执。模型摘要不再替代执行结果，未知变更不自动重放
- target 绑定多个 Helm 仓库时按 Chart 的实际来源选择唯一凭据，不再因存在无关仓库而阻断公共 Chart 或要求用户重复选择
- Helm 的 `compat` 等普通 values 键不再因结尾包含 `pat` 被误判为凭据字段；实际 PAT 字段与凭据内容仍保持隔离
- 控制台连接成功后不再因 45 秒无输出中断任务；执行采用总期限，取消后继续等待原调用终态，进度通知不会取消总期限
- `k8s` 和 `image-build` 的 apply 会话可以调用 Helm，避免因选择 skill 而丢失已授权的发布工具
- 密码登录会话在持续使用时不再于 24 小时后被强制退出；控制台会刷新登录 Cookie，过期后直接回到登录页
- 同一用户未过期的密码登录 token 超过 10 个时，新登录会撤销最早的旧 token
- Gateway HTTP 服务设置请求头读取超时和空闲连接超时
- CLI 访问 COS 预签名地址时，连接、TLS、响应头以及读写停滞都有时限
- CI 运行 DoSql 的 Node 测试，并在 Go 测试前安装 PyYAML；Go 测试超时放宽到 20 分钟

## [0.3.1] — 2026-11-12

### Added

- 新 DoOps 标识：透明底 mark、横向与竖版 lockup、favicon、应用图标与 Apple 触控图标全部由同一份标识导出；README 提供亮/暗两套 lockup
- 落地页、案例页、博客、部署文档与登录页统一品牌视觉：氛围光主视觉、品牌渐变标题、卡片化内容、手风琴 FAQ 与两行式页脚

### Changed

- 主题色对齐标识：品牌蓝作为主色（填充一律使用浅色文字，禁止蓝底黑字），标识青作为选中与强调色，深色表面改为标识藏青；补充焦点环、选区色与减少动效支持
- 控制台表面统一：仪表盘指标卡、集群与活动面板、Kubernetes 操作胶囊、输入框与进度条改用共享的高架表面与品牌渐变
- 产品名统一写作 DoOps；预览构建可用 `NEXT_PUBLIC_DOOPS_DEMO=1` 打开演示模式，便于无 Gateway 的界面验收
- 控制台复用按钮、表单、分段选择与页面标签，统一主题和键盘/触摸反馈；工作区使用完整宽度与 16px 边距，保留运行中导航保护、权限判断和升级审计恢复
- 固定版本 Agent UI 使用可复现展示补丁，工具记录只显示一份，独立交互回执保持可见

### Fixed

- COS 快照从 Git tree 保存执行位，下载及 Agent 同步在新建和覆盖时恢复 `0755/0644`；缺失元数据的旧快照在写入前明确拒绝并提示重新 push，旧对象保留；按实际连接和选定 Agent 校验协议能力，避免混用旧端静默丢失执行位
- CLI 总帮助明确默认 `pull` 下载已上传的 COS 快照；获取远端当前工作区及生成产物需使用 `pull --storage gateway`
- Agent 最终验收审计写入失败保留实际结果与操作关联，CLI 不再因此回滚健康版本；控制台在业务验收完成后释放操作控件，保留审计告警和恢复记录
- 控制台最终协议或 commit 验收明确失败时自动回滚并核验历史版本；断连、审计故障和停止观察不触发推测性回滚
- 本机 Gateway 审计清理与操作者回执原子提交，回执失败保留原记录；HTTP 管理员权限与未知结果保护规则保持生效

### Removed

- 删除 Gateway 未参与实际准入或公开状态计算的旧 Busy 副本，以及 CLI 无生产调用的会话缓存写入方法；历史会话读取和目标状态接口保持原有行为

## [0.3.0] — 2026-11-11

### Changed

- 构建、发布与安装统一收敛到 GitHub：GitHub Actions 承担 CI 与 tag Release 发布，仓库不再携带镜像发布流水线配置；生产镜像由操作方以显式 registry/repository/tag 和完整 40 位源提交构建推送，安装只有 GitHub Releases 一条路径

### Fixed

- 终端命令执行期间保留输出和停止入口，锁定目标、会话及控制台切换；停止请求失败继续等待终态，退出中禁止启动新命令
- COS push 在创建传输连接前统一解析自动或目的路径推导的 session；低层工作区导出始终打包当前快照，支持 main、旧 master 并存和 detached HEAD
- Edge 隧道正确保留双向半关闭连接，终态释放 socket 与配额；全局连接上限的检查与占位改为同一临界区
- 手工发布历史 tag 时 CI 门禁和产物构建使用同一完整提交 SHA；文档明确远端生成产物需使用 `pull --storage gateway`
- Agent 升级失败不再记为成功：Gateway 持久化结构化结果、失败类别与脱敏证据，区分 Job 受理、运行和阶段终态；操作编号贯通 CLI、控制台、预检、升级与回滚
- 升级或回滚断连后保留未知结果；审计收尾失败保留 Job 信息，客户端继续验收并报告审计失败，不重复变更；手动和自动回滚核对历史回执中的实际 release commit
- 控制台审计支持操作编号、时间范围、游标分页和轮询隐藏；升级来源信息进入审计，最近一次操作可恢复观察
- 审计清理与清理回执原子提交；删除没有写入者的升级孤儿表创建及维护代码，保留现有库历史数据

### Removed

- 删除已退役的 ConfigPanel 及 16 个无调用的内部辅助函数、导出和脚本函数，保留现行配置中心及共享模块

### Security

- CLI 升级历史与调试输出脱敏，并在记录前拒绝带凭据的来源 URL；审计清理保留尚未取得 Job 终态的恢复证据
- K8s 升级脚本统一经 Gateway，不再接受本地 kubeconfig 变更入口；升级派发前检查 `upgradeAudit` v1，缺少结构化审计协议时明确拒绝

## [0.2.6] — 2026-11-11

### Changed

- 安装引导收口：公开线示例更新到当前发布；明确 CNB 默认分支是公开线分发器、CNB `v1.3.x` 必须固定 tag 并使用 `backend/scripts/install.sh`；新机器统一用 `doops login --gateway` 首次发现 target

## [0.2.5] — 2026-11-11

### Changed

- 公开仓库历史清理后重建发布资产：重新生成 CLI、Gateway、Skill 包与 build provenance，恢复 tag/commit 绑定和安装校验链；本次不包含运行时行为变更

## [0.2.4] — 2026-11-11

### Added

- 公开博客 `/blog`：中文 / English / Русский 三语言文章预渲染，含「DoOps 能做什么（业务功能、管理底座与 AI 原生）」与「再见 SSH 之后」两篇长文及 18 张配图；控制台顶栏与落地页可直达
- GitHub Release 现在同时发布 `doops-skill.tar.gz`：按 CNB 资产布局打包 `skills/SKILL.md`、`scripts/install.sh` 与 `docs/*.md`，校验和一并写入 `checksums.txt`，发布后复验包内条目
- DoSQL 在模型运行前于服务端解析唯一数据库：把解析结果作为提示并覆盖模型给出的 credential/database 参数，避免发现循环与超时
- 控制台新增「更新内容」对话框：按 `Added` / `Changed` / `Performance` / `Fixed` / `Security` / `Deprecated` / `Removed` 顺序分组（未知分类归「其他」）聚合多个历史版本，条目带版本徽标与展示范围，手动打开时可切换「自上次查看以来 / 最近 10/20/50/100 个版本」范围，支持行内 `code`、**粗体** 与 http(s) 链接
- 控制台顶栏新增「更新内容」入口与升级后自动弹出：首次访问静默记录当前版本、不弹窗；「今天不再弹出」只抑制当天自动弹出，手动入口始终可用；无数据时入口禁用，有未读版本时显示小圆点。内容来自仓库根 `CHANGELOG.md`，随 Web 镜像构建打包

### Changed

- 公开仓库结构收口：内部设计归档与评审草稿不再随仓库发布，产品文档统一由 `backend/docs/README.md` 索引，对外文章移入 `blog/`；三个 README 的文档入口同步更新
- 离线实例记录不再按 24 小时定时删除：Gateway 在启动时清理一次、之后每小时按 90 天保留窗口清理；7 天活跃窗口只决定默认列表
- 安装文档固定到发布 tag，并区分两条发布线的资产来源：公开发布线（`v0.2.x`，GitHub Releases）支持 `doops login --gateway` 首登发现，CNB 生产线（`v1.3.x`，`install.sh` 默认的 CNB Release Assets）不支持，需四项显式路由；CNB 仓库默认分支 `master` 仍是旧扁平布局，克隆时必须 checkout tag

### Fixed

- 配套文档纠正与实现不符的描述：普通 Agent 反向注册只声明 `cluster/instance`（不存在通用 enrollment token，只有 Edge 隧道使用 `-edge-enroll-token`）；非回环 Gateway 强制 https/wss；`read`/`write`/`push`/`pull` 的 COS 与 workspace 路径限制；`check`、`k8s`、凭据与审计接口的实际参数、过滤维度和错误类别

## [0.2.3] — 2026-11-11

### Added

- 统一 CLI：`login --gateway` 首次登录并自动发现 target，`sync` 补路由、`targets --admission` 回读 do-agent 名额与队列、`check` 比对线上镜像
- `credential` 命令组（create / configure / put / grant / grant-revoke / verify / promote / revoke / preflight / bundle）：凭据元数据存 Gateway，payload 只经 stdin 与私有通道，grant 在 target 上验证通过后才激活
- `sql`、`web` 命令组：经 Gateway/Agent 调用 DoSql、DoWeb skill；`image build|copy` 与 `registry sync` 承接镜像构建与复制
- `k8s` 受限运维入口：get / describe / logs / top / events / rollout status / helm 只读视图，restart / scale / cordon / delete 必须带 `--confirm`，Secret 与 ServiceAccount 不开放
- `ask --apply` 成为业务发布入口，任务内声明 target、范围、原因、Chart 与镜像资产及验收条件；支持 `--credential`、`--repo-ref`、`--database-ref`
- Web 控制台（/console）：Gateway 登录与目标切换，概览 / AI 助手 / 数据库 / 网站 / Kubernetes / 镜像 / 文件 / 知识库 / 凭据 / 审计 / 终端视图，另有部署、CI/CD、管理三个顶栏入口
- Helm Agent 自升级：`upgrade` 由目标集群内 Job 执行，升级前备份、升级后校验并在失败时自动回滚；`--dry-run` 只预览参数
- 发布与安装链：GitHub Actions 由 tag 触发构建不可变 CLI/Gateway 资产，`scripts/install.sh` 校验 sha256、release/commit 与 CLI contract 后安装，并在 receipt 记录 release、commit、skill_commit、contract 与每个 skill 路径
- Agent 镜像内置 shell / k8s / image-build / dosql / doweb 等 skill；`backend/skills/SKILL.md` 与 `backend/docs/*.md` 是项目 Skill 的唯一下发源

### Changed

- 授权单位收敛为 (user, cluster, instance)：一条 target grant 覆盖该 target 上的全部操作与已绑定凭据，旧的 per-action grant 迁移为 target grant
- `exec --bg` 承接原 `bg` 后台任务；`read` / `write` / `push` / `pull` 默认使用 Gateway COS 存储，`--storage gateway` 走本地中继
- 源码树重组为 `backend/`（gateway / agent / cli / docs / deploy / scripts / skills / test），Web 控制台位于仓库根 `app/`、`components/`、`lib/`
- 文档重组到 `backend/docs/` 并按主题单一归属，Skill 只保留红线、命令选择与链接

### Removed

- `doops audit` 命令：审计查询由 Gateway 审计记录与控制台审计视图提供
- Gateway `/metrics` 端点与 `doops-gateway token list|revoke`：token 管理改为 `doops admin token create|list|revoke`
- 命令护栏及 `DOOPS_BLOCK_EXTRA` / `DOOPS_BLOCK_DISABLE` / `DOOPS_BLOCK_OFF` 配置
- `examples/skills/` 示例库与 `install.sh --from-source`：安装只使用校验过的 Release 资产
- 顶层 `doops install`、`doops bg`、`doops build`：分别由 Helm/托管脚本接入、`exec --bg`、`image build` 取代

## [0.2.2] — 2026-11-08

### Added

- gateway `/metrics` Prometheus 端点：在线 agent 数、WS 客户端连接数、活跃操作数、工具调用计数（按 action/status）、护栏拦截计数；回环地址直接放行，非回环需 `Authorization: Bearer $DOOPS_METRICS_TOKEN`
- token 轮换管理：`doops-gateway token list` / `token revoke -id <tok_...>`，吊销后立即失效
- `examples/skills/` 示例库：nginx 巡检、证书到期巡检、日志收集打包（各含 README + 测试说明）

### Changed

- `scripts/install.sh` 默认从 GitHub Release 下载 CLI 二进制与 Skill 包（`doops-skill.tar.gz`），sha256 校验；`--version` 锁定版本，`--from-source` 保留仓库内预构建包路径
- E2E 冒烟新增 `/metrics` 断言（agents_online、exec/success 工具调用计数）

## [0.2.1] — 2026-11-08

### Added

- `doops audit`：审计查询命令，支持 `--blocked`/`--bypassed` 过滤、`--session` 聚合与 `--json` JSONL 导出；agent 侧新增 `doops_audit_query` 工具（只读授权）
- 演示站健康检查 workflow（`/health` + 登录拨测，每 30 分钟）

## [0.2.0] — 2026-11-08

### Added

- agent 命令护栏：`exec` 前拦截 10 类高危命令（递归删根/系统目录、写块设备、mkfs、关机重启、fork 炸弹、远程脚本管道执行等），命中即拒绝并写入审计（`blocked=1 rule=<id>`）；支持 `DOOPS_BLOCK_EXTRA` / `DOOPS_BLOCK_DISABLE` / `DOOPS_BLOCK_OFF` 配置
- CLI 错误分层可操作化：每类失败附下一步建议（401→重新登录、403→授权路径、护栏→规则 ID 与豁免、agent 离线→targets 核查、超时→bg 异步）
- 英文与俄文 README，三语言互链
- 微信公众号图文指南（单文件 standalone HTML）
- 护栏 scope 豁免通道：gateway 转发 exec 时注入调用方 scope（`caller_scopes`，覆盖防伪造），持 `admin`/`exec:privileged` scope 的调用命中护栏规则可放行，审计标记 `bypassed_by_scope=1`；旧 gateway/新 agent 向后兼容
- 本 Release 工作流：tag 触发自动构建 CLI（4 平台）+ agent/gateway（linux amd64/arm64）并发布附件

### Fixed

- gateway-only 校验错位：`exec`/`ask`/`write`/`read`/`info` 对 gateway target 被误拒，已将该校验移回 `bash` 子命令
- 旧 dispatcher 的误伤与混淆绕过：空白/大小写规范化，`rm -rf /tmp/x` 不再被 `rm -rf /` 规则误拦

### Security

- 护栏拦截事件纳入审计（`blocked=1`），可检索、可导出

## [0.1.0] — 2026-11-07

首个公开版本（MIT）：

### Added

- 公网 gateway + 内网 agent 反向注册架构，无需 SSH
- CLI 统一入口：exec / ask / write / read / bg / push / pull / build / upgrade / admin
- token 鉴权与 cluster/instance/action 粒度授权
- 会话工作区隔离与 `.doops-audit-log` 审计
- BuildKit 远端构建、Skills 沙盒、doagent ACP 接入
