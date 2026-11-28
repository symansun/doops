# Doops WebUI

从本机 3000 端口打开控制台时，登录页默认填写 Gateway 地址 `http://127.0.0.1:42222`。手动连接其他 Gateway 后，登录页会记住该地址。

登录页使用标准正文字号，表单标签和输入提示采用浅色，博客链接使用浅蓝色并带下划线。恢复会话和断开连接时，居中的主状态使用区分于辅助说明的大字。

登录后的顶栏以 34 px 高显示 DoOps 标志与字标，右上角是目标 Agent 选择器：显示当前目标的集群/实例和在线状态，展开为挂在顶层的不透明面板（支持 `Esc` 关闭，目标数 ≥ 6 时提供实例/集群快捷搜索），可随时在任意页面切换目标或返回全部 Agent。登录后默认进入「首页」，首页以「全网运行指挥台」为定位（不展示冗余场景提示横幅、重复插件按钮条或与左栏重复的纯文字导航）：顶栏为单行紧凑指挥条（`运行总览`、实时态势胶囊与「一键全网体检 / 接入 Agent / 刷新」动作）；下方一行 4 列均衡 KPI 卡片分别展示在线节点与运行形态分布（`K8s / Linux / Docker`，与 K8s 集群台账口径解耦）、健康与告警、全网资源水位（3 条 CPU / 内存 / 磁盘微进度条，未采样时显式标明「尚未采样」并提供「立即体检」一键触发，极简模式回退为活跃会话）与任务调度；存在阈值告警（自动合并同目标实时 CPU/内存快照防重复行）、异常资源、失败巡检或离线目标时，在主区上方按需浮现「待处理异常」聚焦区（默认展前 3 项可展开，全网健康时零占位）；主区采用左右双栏——左侧承载默认高密度「实时节点工作台」（支持「列表 / 分组」视图切换、按类型与关键词搜索节点/IP/业务标签、内联展示机器台账业务/技术栈标签与实时水位，悬停呈现「AI / 终端 / 监控」快捷操作），以及数据优先的「专项能力监测」（全未采集时压为紧凑单行快速进入条，已有实采数据时按严重度展开实时指标卡）；右侧聚合按启用模块自适应的「核心资产规模」（机器台账含打标覆盖率与待归属提醒、K8s 集群与命名空间、业务系统、网站 / 数据库受管目录）与支持切换「操作审计 / 巡检记录」的「近期动态」（展示操作人、目标、相对时间与全部记录入口；普通用户在全局审计 403 时自动默认展示本人可见的巡检记录）。未连接 Agent 时仍可查看首页；单台机器的完整状态在「目标运维 → Agent 概览」，工具和 CI/CD 接入从左侧菜单打开。

主导航位于左侧。桌面侧栏可独立滚动，窄屏点左上角「菜单」打开左侧抽屉，选择入口或按 Esc 收起。导航、目标运维、资产台账、监控与巡检、场景与扩展、发布部署以及平台管理按组排列；当前页用蓝底、亮边和粗体标识。平台管理和「Agent管理」仅管理员可见。目标相关入口共享右上角选择的目标；尚未选定 Agent 时先显示 Agent 目录，选定后直达所点工具。「资产台账」「监控与巡检」「插件中心」等平台级视图读取 Gateway 持久化数据，无在线 Agent 目标时也可直接进入；仅「发布工作负载」和「升级 Agent」依赖在线目标，未选在线目标时置灰并给出提示。刷新或断线后，仍获授权的目标会保留。离线时导航仍可使用，可查看登记资产、历史指标、凭据和审计记录；需要 Agent 执行的工具会提示等待连接，当前目标重新上线后自动恢复。

「监控与态势」「资产台账」与「场景与扩展」采用职责正交的独立入口（原单目标「Agent 资产」已整合进「目标运维 → Agent 概览」，原全网「系统资源监控」与单目标「Agent 监控」已统一整合进「资源监控」，原独立「智能巡检」入口已彻底合并进「目标运维 → 自动化」，原「Skill 管理」与「MCP 管理」已统一整合进「场景与扩展 → 插件中心」）：
- **「态势感知」**（`?view=assets&area=situation`）：聚焦「全局态势总览」与「业务→集群→机器拓扑」两张纯态势页签，采用「先定性总览与异常聚焦 → 再业务/应用/集群/机器四层联动影响追踪」呈现逻辑，默认高清标准分层并支持按需切换 3D 立体/俯瞰与「仅看异常链路」，点击或悬停任一节点自动高亮上下游影响面；同时提供防标签重叠的可交互 3D 态势核心与 3D 立体拓扑，并支持从顶部或任一节点一键跳转至「资源监控」查看对应目标的资源水位与告警曲线；非演示模式下仅呈现真实台账与实采快照，空层自动折叠为单行引导条。
- **「资源监控」**（`?view=assets&area=monitor`，兼容旧单目标书签 `?view=console&tab=monitor`）：统一承载**「全网资源总览」**（跨 Agent 机器/集群/容器/应用/数据库资源快照聚合、趋势对比、Top 排行与全量一键巡检）与**「单目标时序与告警」**（面向选定在线或离线目标的设备/挂载点/网卡/GPU/容器/Pod/应用曲线、管理员阈值告警规则保存与删除、全类别 Skill 巡检趋势及折叠区「按需原生采样」）；Gateway 在写入单目标原生采样时同事务桥接同步至全网快照表，消除两套监控口径漂移。
- **「插件中心」**（位于侧栏「场景与扩展」，`?view=assets&area=plugins`，完全兼容旧书签 `?area=skills` 与 `?area=mcp`，且在无在线 Agent 目标时也可直接访问）：分为三张页签——**「场景与功能插件」**（平台管理员可点击顶栏 **「安装插件包 (.zip)」** 上传并安装含 `plugin.yaml` 的外部自包含全栈插件包（如 `dodns` / `doemail`）、自动注册其声明的独立 MCP 服务与动态资产目录 Schema、卸载非核心内置外部插件并自动清理覆盖项与伴生 MCP，同时可先预览差异再一键切换 5 套内置场景包、对 8 个 L1 内置功能插件及核心/外部 L2 声明式插件做单项启停，并查看 Skill/MCP 依赖就绪度与清理陈旧覆盖；普通用户只读展示当前场景与启用列表；完整规则见 [插件架构规范](design/PLUGIN_ARCHITECTURE_SPEC.md)）、**「Skill 能力包」**（原 Skill 管理：文件目录化存储 `<dbDir>/skills/<slug>/` + 轻量索引，支持系统 Skill 与自定义 `.md`/`.zip` 编辑、启停、热同步、在线试跑、L2 `plugin.yaml` 状态/错误提示及 Agent 运行态扫描）与 **「MCP 工具服务」**（原 MCP 管理：统一管理内置 `doops`/`dosql` 与外部/插件自带 `stdio`/`http`/`sse` MCP，写/同步/探测限管理员，敏感 `env`/`headers` 走 `DOOPS_GATEWAY_SECRET_KEY` AEAD+AAD 加密落库永不回显明文，支持真实 JSON-RPC 2.0 探测与热同步）。
- **「场景与扩展」与领域插件工作台（`<SkillPluginHost>`）**：12 个核心内置系统 Skill 中，仅 **4 个独立领域工作台**（应用挖掘与自动打标、GPU 算力治理、主机安全治理、外部暴露面扫描）以及管理员通过 `.zip` 安装并启用的外部全栈插件（如 `plugins/dodns/`、`plugins/doemail/`）与本人上传的自定义插件挂载到左侧「场景与扩展」（`?view=plugin&plugin=skill.<slug>`），其余 8 个与 L1 内置模块职责重叠或专项巡检类 Skill（主机性能/K8s/容器/Web/数据库/SSH/补丁/代理）不再冗余平铺于侧栏，保留在「自动化」、目标页签与首页「专项能力监测」中按需使用。所有插件工作台统一采用**内容优先的 3 页签领域工作台形态**（不再展示内部 `L2`/`skill.*` 术语或前置脚本跑批表单）：顶栏提供节点切换、实时状态刷新、AI 分析与报告导出；**「实时工作台」**页签直接呈现插件专属实时视图——例如「GPU 算力治理」内置 `<GpuGovernanceWorkbench>` 展示单卡算力/显存/温度/功耗/ECC 进度卡及活跃占卡进程（自动标出疑似空闲占卡），带 `catalog_schemas` 的外部插件（如 DNS Zone / 邮件域）将受管资产目录 `<InlineCatalogManager>` 置顶展示，且对零 Token 无必填凭据的只读插件在首次进入时自动执行一次实时采集；**「运维操作」**页签常驻开放 AI 协同排查快捷入口与受控操作台（支持 `check`/`plan`/`apply`、终端分栏协同与命令填入终端，点击任一 GPU/进程/域名行直接带入）；**「策略与报告」**页签收口采集阈值参数、快捷预设、推荐定时计划、历史趋势曲线、检查明细、原始产物与单文件中文 HTML/打印 PDF 正式报告导出。完整设计见 [Skill 产品设计矩阵](design/SKILL_PRODUCT_DESIGN_MATRIX.md)。
  - 首页不再弹出「选择场景包」引导条或重复展示插件快捷按钮条；场景包切换、单项插件启停与外部 `.zip` 插件包安装统一收口在「插件中心」。
  - 直接访问已停用插件的 URL 或书签时会展示停用提示页（管理员可一键启用，不下载已停用插件的 JS chunk）；正在某插件页操作时被管理员中途停用不会强制踢出，仅在顶部提醒「离开后不可再进入」；对接旧版 Gateway（`/v1/plugins` 返回 404）时显式按全功能模式兼容展示并向管理员提示升级。
- **「Agent 运行监控」**（原「平台监控」，`?view=assets&area=board`）：纯粹面向 **Agent 隧道与控制面维度**，按「运行总览」「Agent 节点」「使用排行」「运行与异常」切换同一时间窗（`1h` / `24h` / `7d` / `30d`）与集群筛选下的连接、版本漂移与离线聚合、并发水位、调用与大模型 Token 统计，并支持一键跳转至「资源监控」。
- **「机器台账」**（`?view=assets&area=machines`）：负责机器登记、自定义标签、Agent 在线/离线/未接入状态筛选与快捷主机采样。
- **「业务系统」**（`?view=assets&area=systems`）：负责业务系统及部署、数据库依赖关系台账维护。

「目标运维 → 自动化」（`?view=console&tab=tasks[&sub=schedules|runs|bg][&run=<id>]`，并完整兼容旧书签 `?view=assets&area=inspection[&run=<id>]`）彻底合并原「自动化」与「智能巡检」为单一工作台，提供三张清晰页签：
- **「定时任务」**（默认页签）：全站唯一的周期任务与巡检策略配置入口，严格只展示已配置的定时任务（消除与零散即时巡检混排造成的重复卡片），页签计数与卡片数量 1:1 对应；支持按「当前目标 / 全部目标」筛选（经 `GET /v1/monitor/runs?cluster=...&instance=...` 精确过滤），平台管理员可直接新建、编辑、启停、删除与立即执行（支持仅当前目标、多目标勾选与全部在线目标、系统 Skill / Shell 命令 / AI 提示词），同时提供可折叠的「即时执行 / 巡检」入口（支持系统与自定义 Skill、提示词或命令）。展开任意任务即可查看其历次执行记录（以人类可读时间戳为主列）与近期状态轨迹，点击原地打开动态产物详情（多目标默认聚焦当前目标，支持「可视化 / Markdown / JSON」切换、导出与复检异常项），分组与过滤规则见 [Skill-First 巡检规范](design/SKILL_FIRST_INSPECTION_SPEC.md#45-自动化与巡检统一工作台sf-25--auto-01--auto-02)。
- **「执行记录」**：按时间倒序展示全部定时调度、即时执行与异常复检历史记录，支持「当前目标相关 / 全部记录」、按来源（定时任务 / 即时执行 / 异常复检）、按状态及关键词筛选，点击任意记录原地查看动态产物报告与复检异常项。
- **「一次性后台执行」**：承接当前选中目标上通过 CLI `doops exec --bg` 或本页发起的一次性长耗时命令（如压测、备份、迁移，关闭页面不中断，终态保留 24 小时）；页签仅在存在运行中命令（`running > 0`）时高亮显示「运行中 N」（不以已结束历史条数误导），页内自动将原始 Shell 命令解析为中文可读标题、任务分类、工作目录与跨任务差异参数芯片（支持按需切换原始命令预览），支持结构化新建、状态筛选、关键词搜索、实时耗时、分栏执行内容解析（步骤清单、环境变量键值表、失败报错摘录与退出码、一键「AI 解释任务」、复制/导出、跟随底部、自动换行、拆解回填）及一键停止。完整验收与指标边界说明见 [智能综合感知、巡检与监控用户手册](DOOPS_ACCEPTANCE_USER_MANUAL.md)。

「资产 → Kubernetes」是集群台账的唯一菜单入口。可登记待接入集群（自动编号、名称、环境、负责人）；创建者可选择自己获授权的在线 Agent 做只读发现、预览并确认关联。未接入时只对创建者可见；关联后按实际来源授权展示。同一集群有多个来源时，修改整份资料需要有权访问所有来源。工作负载操作使用选中行的来源和命名空间。解除来源关联和移出待接入登记不会销毁真实集群；存在 Node、部署或分配关系时须先处理依赖。

数据库按引擎、主机、端口、库名登记；网站按协议、域名、访问路径登记；镜像按仓库地址、镜像路径和 Tag 或 Digest 登记。未绑定目标时是个人待接入资料；显式选择目标后，获授权用户共同维护，操作入口绑定该条记录的 Agent。登记不是连通性验证。旧版个人登记单独只读展示，不自动公开。凭据沿用原管理和授权流程。

「平台管理」跳转到独立页面 `/console/admin`，集中实例、配置中心、用户、权限、令牌、敏感词、代理路由、出站 Webhook 和 Agent管理。仅管理员可见，直接访问也会核对当前会话；直接访问 `/console?view=admin`（支持携带 `?tab=<adminTab>`）时，管理员会自动跳转至 `/console/admin`（保留 `demo`/`role` 与页签参数），非管理员展示明确的权限提示与「返回首页」入口，不再静默回落首页。

### 管理员「出站 Webhook」页签（WEB-07）

平台管理员在「平台管理 → 出站 Webhook」页签（`/console/admin?tab=webhooks`；从 `/console?view=admin&tab=webhooks` 进入时会保留页签并跳转）管理 Gateway 的出站通知端点。页签请求经控制台同源 BFF `/api/admin/*` 代理到 Gateway 的 `/v1/admin/*`；非管理员请求由 Gateway 拒绝，演示会话只提供空的只读列表，不执行变更。

页签对应的已实现管理操作如下：

- **查看端点**：读取 `GET /v1/admin/webhooks`（列表）或 `GET /v1/admin/webhooks/<id>`（详情）。返回字段包括端点 ID、名称、描述、启用状态、集群/实例作用域、订阅事件、创建/更新时间，以及最近成功/失败时间和错误类别；URL 只返回 `url_host` 与 `url_sha256`，不会返回路径、查询参数或密文。
- **创建端点**：提交 `POST /v1/admin/webhooks`，字段为 `name`、`description`、HTTPS `url`、`event_types`、可选 `scope_cluster`/`scope_instance` 与 `enabled`。成功返回 HTTP 201；生成的 secret 只在创建响应中返回一次，控制台和日志不得持久化或回显它。
- **编辑、启停和删除**：通过 `PATCH /v1/admin/webhooks/<id>` 更新名称、描述、URL、事件、作用域或启用状态；通过 `DELETE /v1/admin/webhooks/<id>` 删除端点。更新时省略字段表示保留原值。
- **轮换 secret**：通过 `POST /v1/admin/webhooks/<id>/rotate-secret` 取得新 secret；新 secret 只返回一次，旧 secret 仍有效 24 小时。响应带 `Cache-Control: no-store`，必须由管理员安全保存。
- **发送测试事件与查看投递**：通过 `POST /v1/admin/webhooks/<id>/test` 排队一个 `webhook.test` 事件并返回 `event_id`；通过 `GET /v1/admin/webhooks/<id>/deliveries?limit=N` 查看投递元数据（状态、尝试次数、响应状态、错误类别及时间戳，不含事件正文）。投递状态展示为等待中、发送中、已送达或失败；尚未收到 HTTP 响应时响应列显示 `—`，不显示虚构的 `0`。

订阅事件是后端闭合目录中的 `execution.finished`、`execution.outcome_unknown`、`agent.connected`、`agent.disconnected`、`monitor.alert.opened`、`monitor.alert.resolved` 和 `inspection.run.finished`；`webhook.test` 只用于测试接口，不能作为订阅事件。Gateway 校验 HTTPS URL、URL 长度（最多 2048 字符）、名称/描述长度、作用域关系（填写实例时必须填写集群），并拒绝 loopback、私网、链路本地、ULA、CGNAT 和云 metadata 等地址。端点数上限为 32，每个端点订阅数上限为 32。

创建或更新失败时，界面按 Gateway 返回的稳定错误类别映射为中文提示并保留 HTTP 状态；不会把上游原始响应正文、完整 URL 或 Secret 写入错误消息或日志。常见类别包括 `webhook_invalid_url`、`webhook_blocked_destination`、`webhook_event_unknown`、`webhook_scope_invalid`、`webhook_endpoint_disabled`、`webhook_secret_unavailable` 和 `webhook_limit_exceeded`。完整请求/响应与 CLI 对照见 [API 对接指南](DOOPS_API_GUIDE.md#出站-webhook)。

在「机器台账 → 编辑机器」维护标签（支持平面标签及 `key=value` 键值标签，如 `院系=计算机`）与「录入时间」（可选填写日期，新建留空默认当前时间，编辑不改则保留原值），机器清单与详情页均展示「录入时间」，列表支持按机器类型、Agent 状态（在线 / 离线 / 未接入）与标签键分组筛选（多选求交集），并内置分页控件（默认每页 50 条，支持切换 20 / 50 / 100 / 200 条、页码跳转与按当前可见页按需拉取主机诊断样本，避免大台账全量渲染卡顿）；全局拓扑位于「态势感知 → 业务→集群→机器拓扑」，即时与定时巡检统一位于「自动化」。标签是可见元数据，不要输入密码、密钥或连接串。

台账读取失败时，点「重新读取台账」重试。读取成功且没有可见机器时，页面显示空态提示。

## 表单辅助填写

仅在资产录入与编辑表单（机器台账新增/编辑、集群台账登记/编辑/导入、业务系统登记/编辑、数据库/网站/镜像资产登记及个人资产登记）内提供「AI 辅助填写」，只描述非敏感字段；登录、巡检调度、Skill 管理、平台管理与常规操作弹窗不显示该入口。选择「使用本地模型生成」才会在浏览器下载模型并通过 WebGPU 推理；网络或设备不支持时仍可手填，或按「字段名：值」使用无模型的键值解析。建议先预览，已有值默认不勾选。填入后仍须自行保存或执行。密码、令牌、私钥、凭据载荷和任意命令不作为填写目标。

## 前端组件层

控制台的界面由 shadcn/ui 风格的基础组件组成，底层是 Radix Primitives，样式仍用 Tailwind v4 与仓库既有主题令牌，不引入自带设计语言的整套 UI 框架。完整视觉与组件规范见 [DESIGN.md](../../DESIGN.md)，页面统一从 `@/components/ui`（`components/ui.tsx`）导入原语与控件：

- 布局：`components/ui/layout.tsx` 提供 `Stack`、`Inline`、`Grid`、`Container`、`Spacer`，页面用它们表达纵向节奏、工具行和卡片墙，不再手写 `grid-cols-*` 字符串
- 表单与操作：`Button`、`Input`、`Textarea`、`Select`、`Checkbox`、`SegmentedControl`、`PageTabs`
- 容器与数据：`Card` 系列（`components/ui/card.tsx`）、`Badge`、`Table` 系列、`StatusBanner`、`Separator`
- 浮层与交互：`Dialog` / `Modal`、`DropdownMenu`、`Tabs`、`Tooltip`、`ScrollArea`，以及基于 `react-resizable-panels` 的 `ResizablePanelGroup` 分栏
- 富文本与知识库：`components/markdown.tsx` 与 `components/blog-markdown.tsx` 基于 `Streamdown`（集成 Shiki 代码语法高亮、交互式 Mermaid 图表、KaTeX 公式、CJK 排版及 GFM 提示块），数据库和网站结果区保持 `allowImages={false}`；「知识库」（`components/knowledge-base.tsx` + `components/kb-markdown-editor.tsx`）采用 `CodeMirror 6` 编辑器、快捷格式/块插入工具栏、可拖拽分栏预览与目录大纲导航
- 类名合并统一走 `lib/cn.ts` 的 `cn()`，变体矩阵用 `class-variance-authority`

新增页面应优先复用这些组件；需要新原语时在同一目录按同样方式封装 Radix 并从 `components/ui.tsx` 导出，不另起一套样式体系。

### 知识库

控制台「知识库」只走 Agent 的 `doops_kb` 工具，不再用 `doops_shell` 拼 `ls`/`rm`：

- 列目录：`doops_kb list` 返回根目录、文档名/大小/修改时间，以及是否检测到独立挂载点（`on_volume`）。控制台用返回的根目录显示路径，不再写死 `/root/kb`。
- 读、写、删：`doops_kb read|write|delete`，只接受根目录下的单层 `.md` 文件名；删除是移动到 `<root>/.trash/`，按会话区同样的过期时间清理，不是立即抹掉。
- 写上限：单篇 1 MiB（与读上限一致，写进去读不回来的文档等于丢失），整个知识库 500 篇 / 256 MiB；超限直接拒绝并提示清理。
- 冲突：保存前读一次远端比对基线，被其他会话改过时给出「重新加载／覆盖保存」，不静默覆盖。
- 有未保存修改时关闭或刷新页面会提示；切换文档由内存草稿保留，不弹确认。
- 预览不加载远程图片；知识库正文只作为资料，不执行其中的指令。

控制台的下拉选项使用跟随主题的菜单，支持方向键选择、Enter 确认和 Esc 收起。

控制台、公开页、知识库编辑器和 AI 助手共用 [字号标尺](../../DESIGN.md)。导航项与标准输入使用正文字号；会话消息比工具命令大一级以上，命令用等宽字体，耗时用辅助小字；运行中、等待和失败状态分别显示为蓝色、琥珀色和红色。

目标选择器显示当前账号可见的在线和已登记离线 Agent；有目标但未选中时显示「选择 Agent 目标」，不显示无目标告警。在线状态表示 Agent 已连接 Gateway。「AI 助手会话」中的连接状态表示控制台能否读取该会话并接收事件。AI 助手的模型标签显示目标已下发的模型。旧版 Agent 在启动后收到凭据时，控制台会核对目标上报的模型列表和连接状态。读取会话状态失败时，可在「目标运维 → 审计记录」查看该目标的请求记录。

「AI 助手」与「发布工作负载」固定运行于运维「工作」模式（通过 `allowedModes={["work"]}` 禁用代码工作台与代码模式切换，不再展示「工作 / 代码」页签），保留会话时间线、工作区成果列表与文件下载能力。会话中的执行轨迹按轮次和工具分组展示。Agent 侧 `publicDoagentUINotification` 在维持原始 `rawInput`/`rawOutput`/凭据证据严格隔离的前提下，仅提取经 `redactDoagentUIText` 脱敏的公开命令行（`displayCommand`）、安全元数据摘要（`displaySummary`）、`cwd` 与 `exitCode`，由 BFF 映射为 `shell.execute` / `filesystem.*` 语义键及耗时 `timing`，使实时对话与历史水合均可直接显示 Agent 执行的命令与输出。一组工具调用开始执行后保持展开，直到用户手动收起，长链路巡检不会随每个工具的完成反复自动展开、收起；轮次结束的未确认工具按未确认展示，不据此判定成功。顶栏右侧提供「终端」开关，可在「AI 助手」页原地展开可拖拽调宽的右侧终端分栏（内含「执行过程」平铺实时命令流与复用同一会话 PTY 句柄的「交互终端」两张页签）：点击左侧命令可在右侧高亮完整输出或填入交互终端重跑，AI 建议命令在「AI 助手」页点击「填入终端」/「终端运行」直接落地右侧交互终端而不再跳离对话页（高风险命令仍强制仅填入不回车），在右侧交互终端操作完成后可一键「← 发给 AI」将最近命令块或终端缓冲输出回传左侧继续人机协作。此外，「交互终端」在「AI 助手」右侧分栏与「终端 + AI 协同」双入口下均默认开启 **「AI 回显」**（可一键开关，状态持久化至 `doops.terminal.aiMirror`）：AI 执行的脱敏 Shell 命令在运行中会于终端顶部展示实时状态条（支持一键跳转「执行过程」），完成后自动将命令与有界安全输出（剥离 OSC/清屏/备用屏等控制序列）无损回显到交互终端画布并重建当前提示符锚点（OSC 133 终端保留 `133;A/B` 锚点，普通 shell 保留原样提示符），当用户正在输入或处于 `vim`/`top` 等备用屏时自动排队等待提示符空闲后刷入，绝不打断人工操作；Agent 侧 `doops_terminal` 打开新 PTY 时若当前会话工作区目录（如 `/root/ws/<session>`）已存在则优先落在该目录，与 AI 会话保持同一工作现场，否则回退至 `/workspace`。工作区成果列表仅在会话已建立连接后刷新，并在重连成功或点击「重新检查连接」/「成果读取失败」时自动重试，不因执行中或短暂重启残留过期失败告警；控制台在反代或服务重启返回非 JSON 的 HTTP 502/503/504 页面时，统一展示中文可用性提示，不暴露原始 `Unexpected token '<'` 解析异常。

### AI 助手与凭据联动

- **顶栏「凭据」摘要菜单**：「AI 助手」顶栏提供面向当前目标的「凭据」下拉菜单，点击时懒加载目标可用凭据元数据（不回显密钥，优先置顶展示 `modelProvider` 绑定状态「已绑定 / 未配置」及业务凭据清单），平台管理员可一键直达「配置/更换模型凭据」或「打开凭据管理」，普通用户也可直接查看目标凭据与管理个人凭据。
- **缺模型内联引导与自动重连**：当目标尚未绑定 `modelProvider` 或模型连接不可用时，「AI 助手」顶部展示内联提醒横幅，提供「配置模型凭据」（管理员可见）与「重新检查连接」操作；跳转至凭据页时自动切至「我管理的」并预筛选「模型服务」，激活模型凭据后顶部展示「返回 AI 助手并重连」，点击返回后自动触发一次会话重连以立即载入新下发的模型。

### AI 助手历史会话

对话记录持久化在目标 Agent 上（Gateway 仅作为鉴权、授权与审计通道）。点击「AI 助手」顶栏的「历史会话」时，控制台经 `GET /api/agent-sessions?view=list` 向目标 Agent 读取按最近对话时间排序的会话清单，展示首条问题摘要、会话 ID、轮次与更新时间（同时覆盖控制台与 CLI 会话）。切换任意历史会话、刷新页面、重新登录（新令牌）或控制台重启后，BFF 在首次打开该会话时若目标 Agent 声明 `history_schema: "doops.sh/agent-history/v1"`，会通过 `mode=history` 将 Agent 持久化的用户消息、思考过程、工具调用/结果与助手回复完整水合到会话时间线，并支持在原上下文中继续追问；Agent 侧 24 小时工作区清理保留 `.agent/sessions/` 30 天，且工作区「成果」面板自动过滤 `.agent/` 内部文件。若目标 Agent 尚未升级至支持 `mode=sessions` 的版本，「历史会话」自动回退至本人 `action=ask` 审计记录列表并提示升级。

## AI 助手常用提示词

在「AI 助手」的空会话中，「工作」模式提供 DoOps 自带的运行巡检、发布与交付、网站与数据库模板。点击模板只会把文字填入输入框（已有草稿会保留），可补充信息、编辑后再发送。模板默认用于只读检查或拟定发布方案，不会自动执行。模板卡片最多两行，显示标题和正文的开头。

目标选择器和 Agent 概览按 `mode` 显示 K8s、Docker、Linux 或「未上报」。菜单和模板先看 `kubernetes_available`：探测为 `false` 就按无可用集群处理。其余情况下，有已确认清单时使用在线目标的 `manifest_cluster_available`；首次确认前该字段缺省，使用 `kubernetes_available`。两者都未上报时，保留入口和模板。

判断结果为 `false` 时，隐藏 K8s 与「发布工作负载」入口，移除 Kubernetes 排障模板和「发布与交付」组。直接进入相应页面会提示该目标没有可用的 Kubernetes 集群。例如，API 可达但已确认清单中只有冲突集群时，也会隐藏这些入口。

维护者可在 [DoOps 提示词配置](../../lib/doops-start-prompts.ts) 调整分组、标题和正文；控制台通过 Agent UI 的 `startPromptGroups` 注入，不修改 Agent 的系统提示词。

## 资产台账

在「机器台账」点「发起巡检」检查当前筛选的机器，或点某行的「巡检」只查该机。Agent 在线不等于宿主健康：巡检按资产编号和已绑定的目标读取已核验的宿主样本；隧道模式主动拉取，挂载模式只读已有样本。CPU 或内存使用率达到 90% 时提示偏高，样本超过 5 分钟、指标不完整或无权限时不会判为正常。「详情与诊断」可查看该机 CPU、内存、进程与采样时间。未接入机器须先关联 Agent 并核验宿主指纹；仅登记 SSH 账号不会启用采集。此入口不创建每周巡检计划。

机器的唯一入口是侧栏「资产台账 → 机器台账」，默认显示全部机器。「物理机」和「虚拟机」仅为类型筛选；筛选为虚拟机时，手工新增也默认选择虚拟机。即使机器尚未接入 Agent，管理员也可以先登记配置。普通用户看到自己获授权目标关联的资产。

- **机器**支持手工新增、搜索和编辑。物理机登记物理核心数，虚拟机登记 vCPU 数，内存与磁盘容量单位为 GiB。虚拟机可引用已登记的宿主。详情页可反查虚拟机、宿主和手工关联的 K8s Node。修改规格会更新资产记录。
- **文本导入**支持在机器台账或手工新增页粘贴多行清单。机器行格式为 `管理IPv4 账号 密码 [内网IPv4] [SSH端口]`，也支持 `-p 端口`；仅登记资产时可只填写管理 IP。分组名单独占一行（有空格的名称可用方括号包围），作用到下一分组。新机器以管理 IP 生成编号，可选物理机或虚拟机，分组、内网地址和显式端口保存在显示名称中。已有机器按唯一管理 IP 匹配。默认只导入资产，勾选「同时保存登录账号和密码」才会将登录资料送交 Gateway。核对右侧字段后点「预览导入」，再确认写入。
- **CSV 导入**先选择导入内容。选「机器台账」后，使用 UTF-8 模板填写 `asset_id,name,kind,address,cpu_count,memory_gib,disk_gib,host_asset_id`，上传后点「预览 CSV」。必填列为编号、名称和类型。`physical` 表示物理机，`vm` 表示虚拟机。宿主可以出现在同一批次的任意位置。确认前核对可新增、重复和错误行数，只导入可新增记录。每批最多 2,000 行、1 MiB。按批次号回查能看到台账写入结果和本批保存的机器密码数。
- **机器登录凭据**可从已有机器详情点击「添加登录凭据」，填写账号、密码，预览后确认。批量处理时选择「机器和登录凭据」：已有机器可用 `asset_id,username,password` 补录，新增机器在 CSV 中一并填写 `name,kind`。XLSX 可以按资产编号或台账中唯一匹配的管理 IP 关联，预览时请核对跳过的源行。每个账号单独计数。相同凭据再次上传会显示「已有」，密码冲突会阻止确认。管理员可以查看已保存的账号与本批回执。密码由 Gateway 加密保存。
- **集群台账**中点「从 Agent 导入」，填写资产编号、名称和来源 Agent。指定命名空间时仅验证该空间的 Pod 列举权限；留空时读取 Namespace 对象。先预览再确认，Gateway 在确认时重新读取。集群列表保存来源、验证时间和命名空间，断线后仍可查询。展开命名空间可进入对应范围的实时工作负载。
- **凭据**沿用 Gateway 受管凭据。在已关联在线目标的资产详情点凭据按钮，进入「当前目标可用」；具备管理权限的用户可在「我管理的」中按类型筛选（全部 / 模型服务 / 镜像仓库 / 通用密钥 / Helm 仓库 / Git 令牌 / TLS 证书），并按每行展示的四步阶段指引（`1/4 待写内容 → 2/4 待绑目标 → 3/4 待验证 → 4/4 待激活 → 已生效`）顺畅完成创建、结构化表单写入（支持按需切换高级 JSON，提交或关闭即清空敏感字段）、绑定目标（`modelProvider` 直接下发至 Agent `settings.json`，免填 K8s Secret namespace）、验证暂存、激活与吊销。列表只显示名称、状态、阶段、版本和目标等日常信息；排查时可点「详情」查看凭据编号、所选版本的编号与校验值、目标绑定编号。适用类型、从已有 Secret 导入的操作见 [凭据说明](CREDENTIAL_RESOURCES.md)。资产文件只填写元数据。

迁移顺序、接口和权限见 [API 指南](DOOPS_API_GUIDE.md#资产台账)。

打开「平台管理 → Agent管理」，选择已登记的 Agent，按机器、集群或业务系统逐项分配。每项资产可以供多个 Agent 查看。撤销仅影响所选目标的显式分配，台账直接关联由对应资产详情维护。登录凭据及目标授权沿用各自的管理流程。

### Agent 运行监控、资源监控与目标联动

「Agent 运行监控」（原「平台监控」，`?view=assets&area=board`）读取 Gateway `/v1/platform-monitor`，展示 Agent 连接状态、注册凭证绑定、离线保留的运行版本（`release_commit`）与上下线闪断事件、分钟级并发/排队水位峰值，以及所选时间窗内的调用趋势、动作分布、大模型 Token 消耗排行、目标与账号使用排行、正在执行的操作、SQLite/MySQL 持久化的脱离后台任务和近期异常（`outcome_unknown` 以警告态单独标识，提示核对状态后再决定是否重试）。Gateway 同步返回派生字段 `gateway_release_commit`（当 Gateway 构建包含合法 40 位 hex commit 时）、`drifted_online`（在线 Agent 中与基线不一致或未上报提交的台数；开发构建缺省合法 commit 时按非主版本/未上报统计）、`offline_total`（`recent_offline + history`，全部已知离线未归档目标数）与节点级 `version_drifted`（对接旧版 Gateway 缺省这些字段时由控制台前端自动回退推导）。当存在相对 Gateway 基线的在线漂移时，顶栏与「Agent 运行与版本状态」卡片明确展示当前基线 commit（前 8 位）与漂移台数（如 `基线 b5689222 · 10 台在线 Agent 版本漂移`）；「Agent 节点」列表过滤区提供「全部 / 版本漂移 / 离线」快捷筛选芯片，并在漂移在线节点的 `release_commit` 旁显示 `warning` 态「版本漂移」徽标，方便管理员一键筛出待升级节点与未归档离线目标清单。平台管理员查看全局实例、并发水位与账号统计，普通账号仅查看本人授权目标与本人/自有凭据范围内的调用记录。

在线 Agent 行内可点击「准入队列」实时拉取 `do-agent` 名额与排队会话，也可一键进入该目标的工作区、「资源监控」单目标视图、自动化或审计记录（带 `correlation_id` 的异常自动定位到该操作的审计回执）；顶栏旁的「返回 Agent 运行监控」可回到原筛选状态。

从机器详情点「进入目标运维」，或从集群命名空间点工作负载入口，页面会切换到该 Agent 的操作区；点「查看资源监控与告警」（离线机器显示「查看历史监控与告警」）可直接进入该目标的统一「资源监控」单目标视图且不中断或重置当前工作区会话。目标选择器旁的返回按钮回到原资产/态势/运行监控页面，恢复筛选和展开位置；从侧栏主动切换入口会自动清除历史返回上下文。

在「业务系统」登记系统编号、负责人和服役状态，再添加来源目标、命名空间、应用与可选数据库。保存表示操作员已核对所填关系。管理员维护记录，其他用户仅能查询自己目标内的部署。

在机器详情中可通过已领取的 Agent 核验主机指纹、获取宿主采样。主机侧按分钟生成带身份的 JSON；页面按采样时间展示系统、内核、进程和资源使用情况。[宿主采样与首次接入](MACHINE_SAMPLING.md)

从侧栏「监控与巡检 → 资源监控」（`?view=assets&area=monitor`）或旧书签 `?view=console&tab=monitor` 进入统一监控中心，可在「全网资源总览」（跨 Agent 机器/K8s/容器/应用/数据库快照总览、Top 排行、趋势、「仅看异常 / 离线」筛选及按已启用系统巡检 Skill 显式触发的「立即全量采集」）与「单目标时序与告警」两张页签间自由切换；点击全网超阈值告警或资源详情抽屉会直接定位至对应资产与指标卡片，在「资源监控」内切换右上角目标（含离线目标）会就地切换单目标曲线而不离开监控页。单目标页签支持选定任意在线或离线 Agent，收敛展示「资产时序与告警」（`/api/assets/monitor`，自动接收 Skill-First 定时/手动巡检桥接样本、按需原生采样与外部采集器样本，支持管理员保存或删除阈值告警规则）与「Skill 巡检趋势」（`/api/monitor/metrics`，覆盖全部已启用的基础巡检、安全巡检与自定义 Skill，按 `1h–30d` 窗口展示全量巡检数值曲线，并支持直接点击「执行当前 Skill」立即采样）。在线目标默认以顶栏「一键巡检采集」（按目标能力显式下发已启用的系统巡检 Skill）为主路径，旧版「采集虚拟机 / 主机」「采集运行中容器」「采集应用指标」「采集 K8s Pod」（命名空间输入框自动提供资源清单已发现的命名空间补全建议）收纳在折叠区「按需原生采样」中保留兼容，且原生采样写入 `asset_monitor_samples` 时会同事务桥接同步写入 `monitor_resource_snapshots`（同目标与 Skill-First 主机快照在全网总览按时间去重、绝不双算容量；外部推送 `action=ingest` 仍仅写时序样本）。[设备与容器指标说明](ASSET_MONITOR_DEVICES.md)

在「目标运维 → Agent 概览」（支持 `?view=console&sub=manifest|workspace` 持久化子页签并兼容旧书签 `?view=console&tab=assets`）内置「运行概览」「资源对账清单」「台账分配范围」三张页签：「运行概览」展示目标机器状态、紧凑摘要版资源清单与节点运行时能力；「资源对账清单」属于 Core Shell 能力，展示全量实时发现资源（复用 `/v1/assets/target-manifest`，支持按类型/核验状态筛选、关键词搜索、重新同步，平台管理员可一键登记待登记资源，普通用户只读核对差异）；「台账分配范围」（由 `doops.cmdb` 插件提供）展示该 Agent 获分配的机器/集群/业务系统台账（`/v1/assets/workspace`，支持搜索筛选，平台管理员可就地切换「管理分配范围」完成分配与撤销）；概览顶栏亦提供「查看资源监控」直达入口。

## Current Gateway Console

The production application is the Next.js console at `/console`. Users sign in
with their existing Gateway account or user token, select an explicit target,
and execute operations under that identity. The console stores the Gateway
session in an HttpOnly cookie, restores and clears it through `/api/session`,
and does not expose the token to React state, localStorage or copied examples.
When the Gateway returns a token expiry, the cookie lifetime is capped at that
expiry and never outlives the token. A token with no expiry keeps the console's
local 90-day session cap; that cap is only browser storage policy, not token
validity. `GET /api/session` refreshes the cookie `Max-Age` after a successful
identity check, so a long-lived or sliding token stays in the browser. An
authentication 401 (expired, revoked, invalid) clears the cookie and returns
HTTP 200 with `session: null`, so the console returns to the login page instead
of showing a restore failure. A temporary Gateway 5xx keeps the local session
cookie so a transient outage does not force a new login. A 403 means permission
denial and keeps the login cookie.

Password login, token import and session restore all validate against
`GET /v1/auth/me`. The Gateway supplies the real username, platform-admin flag,
current token ID, issue time and optional expiry. The console does not infer
identity from target visibility or probe an admin-only endpoint. An absent or
malformed identity endpoint is an explicit error and does not create a cookie.
Token login therefore displays the real username and restores only that user's
target/session selection. The token page marks the current login token; revoking
it removes access for the console, CLI and any other clients using that same
token. Disconnect only clears the browser cookie;
revocation remains a separate explicit action. Password-login tokens are renewed
by the Gateway on authenticated use, and the console cookie is refreshed when
`GET /api/session` succeeds; administrator-issued service tokens are not renewed.

- AI conversation uses the shared Agent Cloud `AgentWorkspace`, v2 runtime,
  queue and store. The host supplies only Gateway transport, target/session
  routing and deployment forms. The session API requires the Agent's
  explicit structured ACP event capability and never downgrades to the old chat
  engine. This source change needs a new shared package publication and matching
  Agent/Web rollout; local fixture evidence is not production acceptance.
- AI diagnostics run on the Agent and query runtime metadata instead of reading
  settings or API keys. The console sends them, and the database and website pages
  use the same prompt RPC with `operation=ask` (the website page sends
  `operation=apply` when the user submits a change). The deployment form sends
  `operation=apply` with target, release, namespace, Chart, optional values, task
  text and managed credential references.
- After Gateway initialization succeeds, the console clears the 45-second
  connection timer. Gateway owns the normal prompt/apply execution deadline
  (currently 60 minutes); the console keeps a 65-minute watchdog margin so it
  does not cancel an authorized repair before Gateway returns its terminal receipt;
  waiting for its first output or a quiet tool does not cause an earlier timeout,
  and progress events do not reset the deadline. Metadata requests retain their
  separate 45-second limit. At the execution deadline, the console requests
  cancellation and waits up to 15 seconds for the original RPC's terminal result.
  A cancellation ACK is not completion; if no terminal result arrives, the outcome
  remains unknown and the mutation is not automatically replayed. See the
  [execution lifecycle](DOOPS_API_GUIDE.md#执行生命周期).
- Tool start/end status is progress only. Apply success or failure comes from the
  trusted structured tool receipt, not the model's final wording or an unrelated
  successful tool call. An explicitly unexecuted Helm validation/preparation
  failure can be followed by a corrected call for the same target, release and
  namespace; a real execution failure or unknown outcome remains unresolved.
- Helm success is the actual release terminal state: the release must be
  `deployed` and the revision and description must belong to that mutation.
  Business health and functional acceptance remain task-level checks. Native MCP
  `isError`, protocol errors and interrupted streams remain failures. Helm
  parameters, values behavior, credential origin selection and failure handling
  are defined in [NATURAL_LANGUAGE_DEPLOYMENT.md](NATURAL_LANGUAGE_DEPLOYMENT.md).
- In the terminal, Enter submits and Shift+Enter inserts a newline. While composing text
  with an IME, Enter and the arrow keys select candidates instead of running commands or
  browsing history. Nonblank commands are submitted as typed; the console does not repair shell quoting.
- While a terminal command is running, target/session and in-console navigation
  remain locked so the original output and Stop control stay available. A failed
  stop request keeps the lock until a remote terminal result arrives. Background
  target refreshes do not replace a running terminal's target. Disconnecting an
  idle session displays a pending screen so a new command cannot start mid-logout.
- Open **Kubernetes** in the console to browse Pods, Deployments, nodes and namespaces on the selected target. The initial view lists Pods across namespaces. Select a kind or filter by name or namespace to find a resource. Click its name to run a read-only `describe` in the operations panel. Refresh reloads the lists through `doops_k8s` and shows read errors for the affected kind.
- To query workloads in a single namespace, type its name in **查询命名空间** and select **应用范围**, or select **查看工作负载** on a namespace row. In this scope the console requests Pods and Deployments directly in that namespace. Use **返回整个集群** to restore the cluster view. The node list shows CPU and memory as reported by the Kubernetes Node capacity fields.
- Kubernetes Execute stays in the same position and is disabled during a call.
  Stop is a separate control, so a double-click cannot cancel the submitted
  operation. Later clicks in the same double-click gesture cannot resubmit even
  after a fast result. Errors wrap below the controls without compressing their labels.
- Agents advertising `upgradeAudit` v1 have an image-reference upgrade/rollback form.
  The console runs preflight before apply, sends the source commit and CI URL, and
  joins all stages with one operation ID. Accepted is not completed: the page
  observes that Job, verifies the Gateway protocol and the expected release commit.
  A rollback uses the commit recorded in its historical receipt. Audit completion
  errors retain the accepted Job for verification and remain visible failures,
  including failures from the final initialize probe. Completed version verification
  releases the execution controls while the audit warning stays visible; the page
  does not wait for audit storage to recover. An unresolved Job still requires
  observation before another mutation. A definite final protocol or commit rejection
  starts one rollback and verifies the historical commit. Audit-only failures,
  network uncertainty and stopped observation do not start a rollback.
  The latest operation is kept in sessionStorage, scoped to Gateway, user and
  target, for resuming observation without resubmitting a mutation. The operation's
  audit records are available from the upgrade panel. First-upgrade and installation
  limits are defined in [BUILD_DEPLOY_SOP.md](BUILD_DEPLOY_SOP.md).
- Credential pages manage create, staged payload, target bindings, persisted version
  metadata, verify, promote, grant revocation and resource revocation. Payload
  input uses the credential endpoint only and never enters an AI task.
  The `凭据组` button creates a credential bundle and looks one up by name; a
  bundle holds credential IDs only. Steps and limits are in
  [CREDENTIAL_RESOURCES.md](CREDENTIAL_RESOURCES.md#bundle).
- The audit page exposes target/session/self/global scopes, action/status, operation
  ID and time filters, and cursor pagination (querying `PAGE_SIZE + 1` rows per page so
  the top summary and footer explicitly distinguish "all N matching records loaded" from
  "older records available" with a load-more control). Routine `observing` upgrade polls are
  hidden by default; terminal observations remain visible. Each row shows its audit
  ID, operation ID, elapsed time, byte counts and available evidence.
  Users can read their own history; global audit requires platform admin.
  Credential owners also see events that reference their credentials. Audit
  query indexes are created when the Gateway starts.
- Open **数据库** and select an instance. Choose its managed connection to read the database directory. Open a database for its tables and schemas, then select a table to preview rows. The breadcrumb returns to the previous level. Access to each database uses the binding for the selected credential.
- Inventory groups a StatefulSet with Pods whose controller reference matches its UID, kind, name and namespace. Its governing Service supplies the address only when the selector matches its Pods exclusively. Other candidates remain separate. A missing service endpoint is shown as **未确认服务地址**. Previously discovered member IDs can still be used with their existing credential bindings.
- From a database or table switch between **数据表**, **SQL / 预检**, and **结构治理** using the selected database and credential:
  - **SQL / 预检** prefills a quoted preview query from a table. Click **执行只读 SQL** for result columns, rows, and the DoSql operation ID (**停止 SQL** requests cancellation and shows the remote outcome), or click **门禁预检** (`sql_action=lint`) to inspect execution segmentation and Policy-as-Code findings without touching the database.
  - **结构治理** exposes four strictly read-only DoSql governance views over the same `doops_agent_prompt` RPC (`operation=ask`): **Schema 结构** (`inspect`, canonical `schema_hash` plus tables, columns, indexes, and foreign keys), **版本与漂移** (`versions` + interactive `diff` between snapshots/live with suggested DDL), **变更流水** (`history` across all sessions or the current session with mode/keyword filtering), and **迁移状态** (`status`, read-only `_dosql_schema_history` inspection with dirty-revision alerting and zero DDL side effects).
- The database operations form routes through the same `doops_agent_prompt` Gateway RPC and
  invokes the bundled DoSql skill. It reuses the selected target, session and
  Gateway audit; database connections and credentials are never stored in the
  browser. The page is read-only (query, discover, plan, inspect, versions, diff, history, status, lint); changes go through
  `doops sql --mode apply --sql/--file`.
- The **网站** tab loads DoWeb-published sites using `doops_web` with `operation=site-list`. Filter by site ID or domain. Select a site to prefill a read-only inspection with its ID and domain. The listing reads release records from the target on the first visit, before any files have been uploaded to the session workspace. It requires an Agent with `site-list` support.
- The website operations form routes through the same `doops_agent_prompt` Gateway RPC and
  invokes the bundled DoWeb skill. It reuses the selected target and Gateway
  audit for SiteSpec, Nginx validation, reload, health and rollback evidence.
- Administrator mutations are audited with actor/token IDs, resource metadata,
  HTTP result and stable errors. Credential/configuration bodies are excluded.
  Unavailable audit storage blocks a mutation.
- 平台管理的「敏感词」页签维护一 Gateway-side filter shared by prompts and raw
  commands: one rule table applies to `doops_agent_prompt` instructions (including
  the SQL body of a direct dosql prompt) and to `doops_shell` / `doops_bg` /
  `doops_ssh` commands (and `doops_terminal` inputs marked `source=ai_suggestion`,
  decoded after stripping bracketed-paste framing). Each rule targets all text,
  prompts only, or commands only; matching ignores letter case and collapses
  consecutive whitespace. A hit rejects the call with
  `sensitive_prompt_blocked` or `sensitive_command_blocked` and records a
  `blocked` audit entry without echoing the matched word to the terminal. A
  whitelist entry exempts one user or one Agent target (cluster plus optional
  instance; an empty instance means the whole cluster). A global switch controls
  the whole filter and is **off by default**, including after an upgrade: rules
  and whitelist entries persist and stay editable, but nothing is intercepted
  until an administrator turns the switch on. The page shows the current state
  and warns while it is off. Toggling it uses a PATCH request to
  `/v1/admin/sensitive-filter` with an `enabled` boolean; the change applies to
  new requests immediately. Rules and whitelist
  entries are stored in `<dbDir>/sensitive-filter.json` with an atomic 0600
  write; each rule is at most 128 characters and each list holds at most 500
  entries. The filter only sees text users submit directly. The Agent-side static
  `commandDenyRules` still covers Bash and structured Kubernetes commands that
  the AI starts itself, so the two layers complement each other; see
  [design/COMMAND_FILTER.md](design/COMMAND_FILTER.md). A damaged filter file is
  logged and treated as empty so a moderation setting cannot take the Gateway's
  AI and terminal paths offline.
- 管理员实例页将在线、近期离线、历史和已归档记录分开计数。历史和已归档记录默认折叠；
  只有离线记录可归档、恢复或退役删除，点击对应操作直接提交。归档和退役删除都不会自动删除 target 授权、令牌、凭据绑定或审计历史，
  退役删除后会提示仍指向该目标的关联引用数量，Agent 重新连接时会自动恢复记录。离线超过 14 天的实例记录由 Gateway 自动删除，授权仍保留。
- The console mirrors its `/console` view state (view, tab, asset area, catalog
  kind, deploy section) in the URL query and the `/console/admin` page syncs its
  active tab to `?tab=`; reloads and Back/Forward restore the state and links are
  shareable. Tokens and session IDs are never serialized into the URL.
- The database, website and image catalog ledgers and the Kubernetes cluster
  ledger can jump straight into the selected online Agent's live panels via
  直接操作当前目标. Demo mode reaches the same panels.
- The Agent overview shows a node info card that streams `doops_node_info`
  output (uname, uptime, memory, disk and tool capability lines), rendered in a
  scrollable block with capability badges for OK/runtime and MISSING tools.
- The Files panel uploads text files up to 256 KiB with one `doops_file_write`;
  any other file up to 64 MiB uploads as 3 MiB base64 chunks written under
  `.doops-upload/` and assembled by one `doops_shell` command that verifies the
  SHA-256, renames atomically and cleans staged parts. Single-file download reads
  size + SHA-256 first, then fetches 3 MiB base64 ranges via `doops_shell` and
  verifies the assembled bytes before saving. 新建目录 and 重命名 read the new name in
  an input dialog; 删除 (file or directory) and 清理工作区 (wipe the whole workspace
  and its bare repo) always ask for confirmation first and are rendered as
  destructive. Editing is capped by the Agent's `doops read` limit (1 MiB by default),
  so an oversized or binary file opens as a read error instead of an empty editor;
  single-file upload is capped at 64 MiB and points at `doops push` beyond that.
- The Files section is labeled 部署包 in the sidebar (the tab id stays `files`).
  Its 「上传文件」/「上传文件夹」 entries are native `label` + `<input type="file">`
  pairs, so the browser opens the chooser itself instead of a scripted
  `input.click()` that mobile browsers and some WebViews ignore. The picker's
  selection is copied to a plain array before the inputs are cleared, because
  Chromium empties the live `FileList` as soon as `input.value` is reset — without
  the copy a pick uploads as "0/0" with no error. While another
  operation runs the labels lose hover, the input is disabled, and a skipped drop
  says why. Files and folders can also be dropped anywhere on the panel and land
  in the current directory with the same progress block. 停止 aborts the in-flight
  chunk through an AbortController, and every file that did not finish is offered
  again as 继续上传 N 个, which retries only those. Failures are translated into
  Chinese (size limit, binary file, missing path, permission, disk full, shell exit
  code) instead of showing the raw Agent error.
- New files and unsaved edits are kept in an in-memory draft map. A draft with no
  remote counterpart shows as a 未保存 row above the listing, opens directly into
  the editor, and can be discarded without touching the Agent.
- Markdown files (`.md`, `.markdown`, `.mdx`) open with an 编辑/预览 switch; the
  preview renders through the shared Markdown component with images disabled, and
  the editor keeps the plain-text source for saving.
- The console-side 「检查部署」 action is removed from the Files panel; deployment
  checks stay in the CLI (`doops check`) and the Agent tool.
- Knowledge-base and Files panels keep unsaved drafts across tab, target and
  session switches, and lock target/session/navigation while an operation runs.
- While a panel operation (Files, Knowledge base, Database, Website, Kubernetes,
  Image, node info) or an AI assistant exec-bar command runs, menu navigation,
  tab and session switches and browser Back/Forward are locked, the same as for
  a running terminal command. A running AI task additionally blocks a session
  switch; its panel stays mounted, so other tabs remain reachable.
- The session field switches the session on Enter or blur, not per keystroke;
  Escape restores the current session and an invalid name is not applied.
- The AI assistant header lists recent `console-` sessions for the selected
  target, derived from the user's latest 1000 audit records, and switches to
  one on selection.
- The sidebar's 「自动化」 entry (`tab=tasks`, accessible even before selecting a
  target, and also backing legacy `?view=assets&area=inspection` links) provides
  three tabs: 「定时任务」 (configured `monitor_schedules` only — no mixed-in
  ad-hoc duplicate cards — with target/all scope toggle, immediate execution
  drawer, inline per-schedule history, timeline dots, in-place artifact detail,
  and platform-admin create/edit/toggle/delete), 「执行记录」 (chronological log
  of all scheduled, ad-hoc, and retest executions with source/status/target
  filtering and in-place report detail), and 「一次性后台执行」 (readable Chinese
  command parsing with working directory, step breakdown, cross-task diff
  parameter chips, optional raw command toggle, structured creation with
  optional title/workdir, status filtering, search, live duration, failure
  excerpt & exit code, split-pane redacted output viewer with AI explain
  handoff, copy/export/refill, and one-click cancel for the current user's
  Gateway-hosted detached `doops_bg` tasks — `running`, `success`,
  `error`/`failed`, `canceled`, `timeout`, `outcome_unknown` — for the selected
  target, even while the Agent is offline; the tab badge shows `运行中 N` only
  when `running > 0`). Detail output polls every 3s while a background task runs;
  停止 submits directly without a confirmation dialog and only acknowledges the
  request — the terminal state still arrives through the task record. A finished
  task reports 「任务已结束」; `outcome_unknown` is marked with a warning banner
  prompting verification before refilling and retrying.
- The terminal's 「后台运行」 button submits `doops_bg` with `detach: true` and
  does not take the terminal running lock: leaving the page only ends the
  browser stream. It is disabled with 「Gateway 未启用后台任务」 when the
  Gateway lacks the `detachedBackground` capability or the target is offline.
- The terminal's 「停止」 submits `tools/cancel` for the running `doops_shell`
  call and never assumes it worked: the panel reports 「已请求停止」, then
  「远端已确认取消」 when the Gateway acknowledges, or
  「停止没有生效：…」 when the Gateway has no such call. The command stays
  locked until real terminal evidence arrives — the Gateway gets 30 seconds to
  confirm and the browser waits 35 — so an unconfirmed stop ends as
  「远端终态未确认」 instead of a silent success. 「✓ 完成，退出码 N」 is printed
  only when the Agent actually returned an exit code; a plain `doops_shell`
  success reports 「✓ 完成」.
- The terminal has two tabs: 「交互终端」 (default) and 「命令」. 交互终端 drives a real
  PTY on the Agent through the `doops_terminal` tool, so `cd`, exported environment,
  full-screen programs such as `top` and Ctrl-C behave normally, and a page refresh
  reattaches to the same shell by `term` id. Console requests reuse a pooled Gateway
  WebSocket per `(gateway, token, cluster, instance)` via `/api/terminal-input` and
  `/api/terminal-stream` (up to 200 pooled connections per BFF instance, 60s idle close,
  5m max lifetime graceful rotation so in-flight long-poll reads settle cleanly while new calls open a fresh WS; immediately evicted—including draining connections—on logout or Gateway `-32001` token revocation).
  When the Agent advertises `features.long_poll=true`, `/api/terminal-stream` pushes
  NDJSON output chunks using bounded `read` long-polling (`wait_ms` up to 10s); older
  Agents without `features.long_poll` automatically fall back to 250ms polling. A dropped
  browser still never kills the remote shell. On recoverable disconnects (network drops,
  502/503/504, Gateway restarts, target temporary offline `-32004`, or laptop sleep wake via
  `online`/`visibilitychange` and `targetOnline` recovery), the terminal stays mounted,
  enters 「重连中…」 and retries with bounded exponential backoff (`1s → 2s → 4s → 8s → 15s`,
  up to 8 attempts), resuming the same remote `term` at the current `cursor` without
  duplicating rendered history or automatically opening a new shell only when the Agent
  explicitly reports the PTY gone; auth/permission errors (`401`/`403`) fail closed without
  retrying. In disconnected, error, or reconnecting states, the status bar exposes
  「重新连接」/「立即重连」 to restore in place without reloading the page. The terminal belongs to the console session that opened it: another session cannot read,
  write, resize or close it. Before starting a shell the Agent normalizes SIGINT: if it
  inherited `SIG_IGN` from systemd/Kubernetes, it takes SIGINT over itself so the PTY
  shell and its foreground jobs get the default disposition back — otherwise an
  interactive bash keeps ignoring it and Ctrl-C only echoes `^`C. The Agent keeps at most 4 terminals per session and 32
  overall and reaps an idle one after 30 minutes; 「断开」 sends `close`, which hangs up
  the process group. 「命令」 keeps the single `doops_shell` execution with exit code,
  「后台运行」 and 「停止」 for deterministic one-shot commands, and is the fallback when
  an Agent predates `doops_terminal`.
- 在终端的「交互终端」或「命令」输出区拖选文本后点击鼠标右键（按住 `Shift` 右键保留浏览器原生菜单），弹出跟随主题的终端右键菜单：
  - **「复制选中内容」**：将选中文本写入剪贴板（兼容非 HTTPS 内网控制台环境）。
  - **「AI 单次解释 / 排查」**（交互终端首选）：将选中文本直走 `POST /api/terminal-ai` 单次模型调用，快速在终端底部返回中文解释与只读排查命令，不弹开右侧「AI 协同」侧栏。
  - **「转入 AI 协同深入排查 / 发送到 AI 执行」**：复制选中文本的同时，在终端右侧自动展开同屏 **「AI 协同」** 分栏（窄屏下为上下分栏），携带当前目标、终端类型与工作目录上下文直接向当前会话的 DoAgent 发起多轮分析排查；交互终端通过 `ResizeObserver` 自动随分栏宽度调整行列尺寸，保持当前 PTY 会话不中断、不丢屏。若当前会话已有运行中的 AI 回合或目标模型尚未就绪，选中内容会自动保底填入 AI 输入框，不会丢失或触发并发报错。
  - **「填入 AI 协同输入框 / 填入 AI 输入框」**：展开右侧同屏「AI 协同」分栏并将选中终端片段追加到输入框，便于补充说明后再回车提交。
  - **「粘贴到终端」**（仅「交互终端」）：在浏览器允许读取剪贴板时将文本经统一输入队列写入当前 PTY 并同步更新乐观行缓冲。
  终端采用合并后的单行极简顶栏（整合「交互终端 / 命令」切换、连接状态与当前目录、**「提示命令 | 派给 AI」双模式切换**、`AI 提问`、**「AI 协同」** 可拖拽分栏开关、重连/断开与会话选择；空终端隐藏静态引导条，仅在有命令记录或全屏程序时按需展示最近命令块）：
  - **模式 1 · 「提示命令」（单次快答，不污染 AI 助手）**：默认模式或输入 `# <问题>` / `? <问题>` / `？<问题>` 固定触发单次模型调用，经 BFF `POST /api/terminal-ai` 直调 Gateway `POST /v1/terminal/ai`，复用该目标已激活的 `modelProvider` 凭据并记录 `terminal:ai` 审计与 Token 用量（提问正文与有界最近命令上下文原样透传，不做终端文本自动脱敏，Gateway 托管 `modelProvider` 凭据本身仍严格隔离不回传），在终端底部紧凑展示建议命令，支持「插入」「运行」（安全命令携带 `source=ai_suggestion` + `suggestion_id` 经敏感词与 `commandDenyRules` 校验，高风险命令或旧版 Agent 仅允许「插入」）「复制」及「派给 AI →」。
  - **模式 2 · 「派给 AI」（直接拍给 AI 助手自主执行）**：切换顶栏为「派给 AI」后直接输入自然语言，或在任意模式下输入 `@ai <任务>` / `## <任务>`，或在 `⌘I` / `Ctrl+Shift+I` 提问框点击「派给 AI」（`⌘+Enter` / `Ctrl+Enter`），自动附带当前目标、目录与最近命令块上下文并展开右侧「AI 协同」分栏直接交由 DoAgent 多轮排查执行。
  - 交互终端在 bash ≥ 4.4 目标上由 Agent 为每个终端生成独立 rcfile 注入 **OSC 133（A/B/C/D）与 OSC 7（动态 cwd）**（不改全局 `/root/.bashrc`，保留 `_doops_audit`，终端关闭时自动清理；`/bin/sh` 或旧版 bash/Agent 自动降级，无 OSC 133 时用 POSIX `Ctrl-U` 清行提交注释并支持 `⌘+↑↓` / `Ctrl+Shift+↑↓` 块跳转、`exit 127` 兜底「转 AI 提问」与备用屏放行）；侧栏头部支持一键切换到独立「AI 助手」完整视图，分栏宽度可通过中间拖拽分隔线自由调节；在分栏态下 AI 助手底部隐藏冗余手动命令输入框，建议命令优先「填入终端」/「终端运行」（安全命令经 `source=ai_suggestion` 过滤，高风险命令仅填入等待人工回车确认）。

Explicit execution and management buttons submit their operation directly,
without an extra confirmation dialog or checkbox. Invalid inputs, unavailable
targets and operations still running retain their actual state restrictions.
Creating a new session does not replay a previous command or resolve an unknown
outcome. The original receipt stays visible.

The complete Web/CLI/skill boundary and authorization matrix is documented in
`UNIFIED_OPERATIONS_WORKBENCH.md`.

Deploy Gateway and Web from matching published artifacts — GitHub Release assets for
Gateway and explicit operator-built image digests for Web — with the canonical host
scripts. Record the binary source commit, Web digest, health, reconnects and
rollback artifact. Local fixtures do not establish live Helm or production-user acceptance.

## What's New in the Console

The console answers "what changed in this release" from the repository's
`CHANGELOG.md`, not from a Gateway feed. `Dockerfile.web` copies that file into
the Web image; `lib/server/changelog.ts` reads it while the page is rendered and
`lib/changelog.ts` parses it, so `app/console/page.tsx` hands plain release
data to the shell. The dialog needs no Gateway RPC and no runtime HTTP request,
so it keeps working with an air-gapped Gateway.

- The current version is the newest versioned entry in `CHANGELOG.md`.
  `[Unreleased]` is parsed but never shown: it belongs to a release that does
  not exist yet. Empty releases, repeated version sections and headings without
  a version are ignored. A pending change becomes visible by renaming the
  heading to `## [<version>] — YYYY-MM-DD`; the date stays optional.
- Content is merged across the releases in range: sections are ordered as
  Added, Changed, Performance, Fixed, Security, Deprecated and Removed, and an
  unknown `###` heading falls into 其他. Every item keeps the version it
  arrived in, and an item repeated across releases is shown once.
- Inline formatting is limited to `code`, **bold** and http(s) links. Other
  Markdown, including raw HTML, is rendered as text; a non-http(s) link target
  stays plain text.
- The header entry is always available when releases exist, and disabled when
  the image carries no changelog. It shows a dot while the browser's last seen
  version is older than the current one; a first visit has no remembered
  version and therefore no dot.
- The dialog shows one merged view: a "v<current> 更新内容" title, a subtitle with
  the version count and range, one heading per non-empty category with an item
  count, and every item tagged with the version it arrived in. The automatic
  popup shows the releases since the browser's last visit. The manual entry can
  switch between "since last visit" and the latest 10, 20, 50 or 100 releases;
  when nothing is new since the recorded version it falls back to the latest 10
  releases and says so. It closes through the Close button, Escape or the
  overlay.
- After an upgrade the dialog opens once by itself, unless the browser asked not
  to be popped up again today ("今天不再弹出"). A first visit only records the
  current version, so a new user is not shown history. The automatic popup waits
  until the session is restored and the onboarding guide is closed, and opens at
  most once per page load. Opening the dialog, automatic or manual, records the
  current version and clears the dot, so a reload does not open it again. The
  quiet choice suppresses automatic popups for the current local day only; it
  never disables the header entry.
- State lives in browser localStorage only, under `LAST_VERSION_KEY` and
  `QUIET_DAY_KEY` (defined in `lib/whats-new.ts`). It stores version strings and
  a local day, never a token or identity, and nothing is reported to the
  Gateway. A browser that blocks storage behaves like a first visit and can
  still open the dialog manually.
- A missing, unreadable or empty `CHANGELOG.md` is not an error: the console
  renders, the dialog has no notes and its entry is disabled.
- A user-visible change updates `CHANGELOG.md` in the same change and moves its
  `[Unreleased]` entries under the release heading when the version is cut.

## 部署

- 生产控制台是仓库根目录的 Next.js 应用，由 `Dockerfile.web` 构建，由操作方以显式
  registry/repository/tag 推送，例如 `registry.example.com/doops-web:<tag>`。
- Gateway 主机上的正式发布使用 `backend/scripts/deploy-web-console.sh`，只接受
  immutable image digest，先在 `127.0.0.1:3001` 启动候选容器并验证，再替换
  `doops-web`；旧容器保留为 rollback。随后用
  `backend/scripts/deploy-gateway-tls-proxy.sh` 发布版本化 Nginx 配置。
- 生产容器必须设置 `DOOPS_CONSOLE_ALLOWED_GATEWAYS`，值为允许代理的 Gateway
  origin，多个值用英文逗号分隔，例如
  `DOOPS_CONSOLE_ALLOWED_GATEWAYS=https://doops.example.com`。控制台只接受精确
  origin，未配置时生产代理接口拒绝请求；除本机 loopback 外，生产 Gateway 必须使用
  HTTPS。
- 控制台的变更请求（POST/PUT/PATCH/DELETE）要求浏览器 `Origin` 与控制台同源，并拒绝
  `Sec-Fetch-Site` 为跨站的请求。同一可注册域下的兄弟站点也会被拒绝。
  默认根据 Next.js 接收到的 URL 和 Host 校验来源。TLS 在反向代理终止、公开地址与服务地址不同或有多个公开地址时，
  设置 `DOOPS_CONSOLE_ORIGIN=https://doops.example.com`（多个用英文逗号分隔）。设置后只接受这些 origin。使用正式部署脚本发布单一 HTTPS 入口时，传入 `--console-origin https://doops.example.com`。脚本会验证候选容器及切换后的容器接受该来源并拒绝跨源登录请求。
- 控制台登录限流按真实浏览器地址计数：控制台从 `X-Forwarded-For` 取地址
  （`DOOPS_CONSOLE_TRUSTED_PROXY_HOPS=0` 关闭转发，一层 Ingress 用 `1`，CDN + Ingress 用 `2`），
  再用 `DOOPS_CONSOLE_FORWARD_SECRET` 对 `时间戳\n地址` 做 HMAC-SHA256，通过
  `X-DoOps-Client-IP` / `X-DoOps-Client-IP-Signature` 转发。Gateway 用同一密钥验证签名
  （误差不超过 2 分钟）后才采用该地址。两端都配置才生效；未配置时保持原行为：所有控制台
  登录共享控制台主机的限流桶。密钥只从环境变量注入，不写命令行参数。
- 控制台通过 `next.config.mjs` 为所有路由设置纵深防御安全响应头：完整 `Content-Security-Policy`
  （`default-src 'self'`、`base-uri 'self'`、`form-action 'self'`、`frame-ancestors 'none'`、
  `object-src 'none'`、`connect-src 'self'`、`worker-src 'self' blob:`、`img-src 'self' data: blob:`、
  `font-src 'self' data:`、`style-src 'self' 'unsafe-inline'`，生产态 `script-src 'self' 'unsafe-inline'`
  且不含 `'unsafe-eval'`）、`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff`、
  `Referrer-Policy: same-origin`、`X-DNS-Prefetch-Control: off`、`Cross-Origin-Opener-Policy: same-origin`
  与限制摄像头/麦克风/定位的 `Permissions-Policy`，并对 `/api/*` 附加
  `Cross-Origin-Resource-Policy: same-origin`。`/blog/images/` 返回的 SVG 额外带
  `sandbox` 与 `default-src 'none'` 的 CSP；其他图片类型不受影响。HSTS 由终止 TLS 的
  Nginx 或入口网关设置，不在 Next.js 应用内设置（避免开发或内网 HTTP 访问被锁定）。
- 控制台 BFF 对所有通配代理路由（`/api/admin/*`、`/api/credentials/*`、`/api/background-tasks/*`）强制校验路径段，
  拒绝 `.`/`..`/百分比编码点段、斜杠/反斜杠、控制字符与超深路径穿越；登录/会话与流式响应（`/api/login`、
  `/api/session`、`/api/rpc`、`/api/terminal-stream`、`/api/agent-sessions`）统一携带 `Cache-Control: no-store`
  与 `X-Content-Type-Options: nosniff`，流式读端点同时拒绝跨站 `Sec-Fetch-Site` 与外源 `Origin` 请求，
  `/api/rpc` 对并发活动连接施加有界上限。
- 模型密钥在凭证管理中录入、验证并启用。配置中心记录模型配置与凭证引用，打开时会清除旧浏览器配置里的 API 密钥字段。
- **L2+ 能力插件工作台与机器部署打标**：当内置或自定义 L2+ 插件声明 `capabilities.catalog_inputs` 包含 `dns_zones` / `mail_domains` 时，`<SkillPluginHost>` 自动在工作台内嵌「DNS Zone / 邮件域资产目录」管理器（支持就地登记、编辑、移除并关联目标 Agent，零侧栏膨胀、零敏感字段），保存后自动注入下次巡检环境变量；「机器台账」顶栏提供「批量部署挖掘与打标」，可对当前筛选的在线机器批量运行 `machine-workload-discovery` 并按机预览标签差异，经平台管理员确认后通过受审计乐观锁接口合并写入 CMDB 标签（单台结果页亦内嵌 `<MachineTagSyncCard>`）。
- 「监控与态势」由职责清晰的三项独立入口组成：「态势感知」（全局态势与四层影响拓扑）、「资源监控」（整合跨 Agent 全网资源总览与单目标细粒度时序、阈值告警规则及 `/api/monitor/metrics` 全类别 Skill 趋势）与「Agent 运行监控」（隧道状态、并发水位、调用与大模型 Token 消耗）；定时任务、即时巡检与执行报告统一收口在「目标运维 → 自动化」。

## Console layout and shared controls

[`DESIGN.md`](../../DESIGN.md) defines console spacing, semantic tokens and control states.
Console views fill the available width with a 16px gutter; only public pages, dialogs
and document previews use bounded reading widths. Desktop and mobile share the same
components. Use `components/ui.tsx` for buttons, inputs, selects, textareas, segmented
controls and page tabs; keep business behavior and permission checks in the host panel.

The pinned `@agent-cloud/agent-ui@0.3.66` display patch is tracked in `patches/`
and applied by pnpm in local and Docker builds through `patchedDependencies`
in `pnpm-workspace.yaml`. Tool activity remains in the conversation list, and 0.3 keeps
execution evidence in the "Execution trace" drawer whose trigger counts interactions
and runs, so tool rows are not rendered a second time inline. Titles that repeat the
workspace path or translated tool label are omitted by the SDK; the patch keeps
textual progress stages/messages on the tool row (including progress without a
fraction), keeps `ActivityTimelineRun` expanded once active until the user
manually toggles it, and applies DoOps's dark surface/syntax theme to the
read-only CodeMirror artifact and diff viewers. Internal session metadata files
written by the Agent (`target.json` and `.target-*.tmp`) are excluded from
work-mode deliverables, and empty sessions do not load or open the deliverables
pane before a conversation starts.

## Agent 概览中的资源清单

选择具体目标后，在「目标运维 → Agent 概览」（「运行概览」摘要卡或「资源对账清单」全量页签，兼容旧书签 `?view=console&tab=assets`）查看 Gateway 清单版本、Agent 已应用版本和最后观测时间。最后观测时间来自 `last_observed_at`。卡片按以下顺序显示同步状态。

| 显示 | 判断 | 操作员可以做什么 |
| :--- | :--- | :--- |
| Agent 离线 | `online=false` | 查看登记信息；上次握手支持同步时还可查看历史观测。旧 Agent 离线后仍只显示登记事实。重连后再刷新 |
| Agent 不支持同步 | 在线且 `supported=false` | 查看已登记的条目，卡片提示升级并隐藏已应用版本 |
| 等待首次观测 | 握手已记录 `supported=true`，本连接还没有 `last_observed_at` | 此时已应用版本为零，即使列表里有历史条目，也要等首次观测，或点「重新同步」 |
| 等待同步 | 已有观测，但尚无清单或 `applied_revision < manifest.revision` | 查看两端版本，等待确认或点「重新同步」 |
| 已同步 | 在线、支持且已应用版本追上清单 | 对照最后观测时间查看资源和差异 |

每行显示资源名称、类型、来源和核验状态。五态为「已核验」「已发现未登记」「已登记未核验」「失联」「冲突」。后两项用红色标记并显示原因，可核对资源身份和最近的完整观测。清单超出 256 KiB 预算时，卡片用 `manifest.counts` 显示总数、已显示条数及各类总数。每类最多保留前 50 条。

在线且支持同步时可点「重新同步」。「已发现未登记」行的 `can_register=true` 时，点「登记到台账」即明确确认该条目。Gateway 校验管理员导入权限和当前观测，只接收集群来源，以及已确认集群下的命名空间。发现的站点、数据库须到资产目录补齐字段登记。业务部署或机器分配出现在清单中只表示已关联，不能据此认定已核验。登记失败时卡片显示服务端错误。首次读取显示加载状态，空结果显示「此目标暂无资源记录」，读失败可重试，403 显示权限提示。字段和状态定义见 [API 对接指南](DOOPS_API_GUIDE.md#目标资源清单)。

## 本机自建链路验收（Gateway + Agent + Console）

1. 从待验源码构建。工作目录为 `backend/gateway` 时执行 `env -u GOROOT go build -o /tmp/webui-gw .`，工作目录为 `backend/agent` 时执行 `env -u GOROOT go build -o /tmp/webui-agent ./cmd/agent`。
2. 建立独立的测试目录 `/tmp/webui`，为本次 Gateway 设置 `DOOPS_GATEWAY_SECRET_KEY`，启动 `/tmp/webui-gw serve -port 42322 -db /tmp/webui/gw.db -monitor-tick 0`。在同一测试库中为 `local/local-1` 签发 enrollment token，创建测试用户并授权。登记场景另外需要管理员账号。账号、grant 和 enrollment 命令见 [Gateway 隧道指南](DOOPS_GATEWAY_TUNNEL.md)。
3. 在 Agent 进程环境设置该目标的 `DOOPS_AGENT_ENROLL_TOKEN`，使用可写的 `DOOPS_WORKSPACE_ROOT`，启动 `/tmp/webui-agent -port 42323 -listen 127.0.0.1 -gateway-url ws://127.0.0.1:42322 -cluster local -instance local-1`。
4. 在仓库根目录运行 `pnpm dev --port 3014`，打开 `http://localhost:3014` 并登录 `http://127.0.0.1:42322`。在资源清单卡触发刷新，将画面、网络响应中的两端版本与 Agent 的 `current.json` 对照。断开测试 Agent 可核对离线显示；旧版本协商、K8s 身份及登记权限须用相应版本、目标及账号分别核对。

本地调试若停在「正在恢复登录会话...」，检查 `/_next/webpack-hmr` 请求。历史验收环境从 `127.0.0.1:3014` 访问时遇到 Next dev 跨源拒绝，改用 `localhost:3014` 可继续。
