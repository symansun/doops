# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 与语义化版本。

本文件是控制台「更新内容」的数据源：Web 镜像构建时从仓库根打包 `CHANGELOG.md`，控制台读取其中最新带版本号的条目作为当前版本。`[Unreleased]` 收敛尚未发布的变更，发布时整段改成 `## [版本号] — YYYY-MM-DD`，已发布条目不再改写；0.1.0–0.2.2 为回填的历史记录。

分类标题只使用 `### Added` / `### Changed` / `### Deprecated` / `### Removed` / `### Fixed` / `### Security`（可选 `### Performance`）；条目以 `- ` 开头，续行缩进；允许行内 `code`、**粗体** 与 Markdown 链接，不要使用表格或 HTML。

## [0.3.30] — 2026-11-24

### Fixed

- MySQL 后端下重复写入的冲突处理此前完全不可执行（SQLite 的 `ON CONFLICT` 语法在 MySQL 上直接报错）：31 处全部改为按原语义生成 `ON DUPLICATE KEY UPDATE`，冲突键沿用原语句目标，`DO NOTHING`、条件更新与 `max()/min()` 各自保持等价语义；SQLite 路径行为不变。
- 修复 v0.3.29 升级到该版本的 Agent 上 `exec`/`ask`/shell/k8s/helm 等远程操作全部返回 `target_manifest_unavailable` 的问题：数据库观测项的 Key 采用 `命名空间/服务/引擎` 层级格式，但快照校验的元数据令牌规则不允许 `/`，Agent 端 `invalid_agent_observations` 被 Gateway 折叠成统一错误。校验改为按 `/` 分段逐项校验层级 Key；名称与身份的校验规则不变。
- 执行清单钉选降级：资源同步失败（迁移缺失、快照不可用等）不再否决 `exec`/`ask` 等执行调用——目标资源清单只用于审计引用与提示词事实，不是授权门控；同步失败时本次调用不带清单继续执行并在 Gateway 日志记录原因。

## [Unreleased]

### Added

- 智能巡检改为由内置 **Skill 包** 驱动：内置 8 个巡检 Skill（虚拟机、容器运行时、Kubernetes 集群、应用站点、软件包漏洞、代理与暴露面、数据库、SSH 主机），每个包含 `SKILL.md` 与 `scripts/inspect.sh`，随镜像以真实目录嵌入并在首次运行时落盘到 `<dbDir>/skills/`，控制台可直接查看脚本内容并在线试跑。
- 「巡检详情」新增 **「与上次对比」** 与 **「历史趋势」** 两个面板：前者展示指标增减与检查项的新增、消失、变化，后者按 Skill 与节点展示时间序列；后端新增 `/v1/monitor/metrics` 时序接口，指标点写入 `inspection_metric_points`。
- 巡检产物改为 **File-First** 落盘：产物写入 `<dbDir>/artifacts/<日期>/<run_id>/<item_id>/`，数据库只保留路径、sha256、字节数与截断标记；单个产物文件超过 256 KiB 会被截断并注明原始字节数。
- 巡检详情在产物已被保留策略清理时不再空白或报错：接口显式返回 `artifact_state=missing`，页面正文区给出「产物缺失」与保留策略说明，可通过调整保留天数或重跑巡检恢复。
- 巡检产物与指标点开始按生命周期清理：产物默认保留 30 天、指标点默认保留 90 天（趋势图因此连续），每个巡检计划最近 50 次运行无条件保留，正在运行的巡检不会被清理；清理先删磁盘产物再删索引记录，目录删不掉时记告警并放弃该索引行，不影响下一轮。可用 `DOOPS_MONITOR_ARTIFACT_RETENTION_DAYS`、`DOOPS_MONITOR_ARTIFACT_KEEP_RUNS`、`DOOPS_MONITOR_METRIC_RETENTION_DAYS` 调整。

### Changed

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
