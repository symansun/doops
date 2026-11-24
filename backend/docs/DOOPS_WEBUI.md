# Doops WebUI

从本机 3000 端口打开控制台时，登录页默认填写 Gateway 地址 `http://127.0.0.1:42222`。手动连接其他 Gateway 后，登录页会记住该地址。

登录页使用标准正文字号，表单标签和输入提示采用浅色，博客链接使用浅蓝色并带下划线。恢复会话和断开连接时，居中的主状态使用区分于辅助说明的大字。

登录后的顶栏以 34 px 高显示 DoOps 标志与字标，右上角是目标 Agent 选择器：显示当前目标的集群/实例和在线状态，可切换目标或返回全部 Agent。登录后默认进入「首页」，可查看在线连接、活跃会话、机器和集群规模、正在执行、近期执行，以及集群概览和近期活动。未连接 Agent 时仍可查看首页。首页展示全局运行情况，不重复列出工具入口；单台机器的状态在「目标运维 → Agent 概览」，工具和 CI/CD 接入从左侧菜单打开。

主导航位于左侧。桌面侧栏可独立滚动，窄屏点左上角「菜单」打开左侧抽屉，选择入口或按 Esc 收起。首页、目标运维、资产、台账与监控、发布部署以及平台管理按组排列；当前页用蓝底、亮边和粗体标识。平台管理和「Agent管理」仅管理员可见。目标相关入口共享右上角选择的目标；尚未选定 Agent 时先显示 Agent 目录，选定后直达所点工具。「发布工作负载」和「升级 Agent」需要目标，未选目标时置灰。刷新或断线后，仍获授权的目标会保留。离线时导航仍可使用，可查看登记资产、历史指标、凭据和审计记录；需要 Agent 执行的工具会提示等待连接，当前目标重新上线后自动恢复。

「台账与监控」采用职责正交的七项独立入口：
- **「态势感知」**（`?view=assets&area=situation`）：采用「先定性总览与异常聚焦 → 再业务/应用/集群/机器四层联动影响追踪 → 再立体资源水位与根因处置」三段式呈现逻辑，默认高清标准分层并支持按需切换 3D 立体/俯瞰与「仅看异常链路」，点击或悬停任一节点自动高亮上下游影响面；同时提供防标签重叠的可交互 3D 态势核心、3D 立体拓扑与机器集群资源视图；非演示模式下仅呈现真实台账与实采快照，空层自动折叠为单行引导条。
- **「智能巡检」**（`?view=assets&area=inspection`）：精简为「执行与记录」与「定时计划」两张纯巡检页签（已移除外部 MCP 开关、合规台账、临时 SSH 表单、硬编码告警阈值与旧策略复选配置）。每次即时或定时巡检均仅需配置 **目标 + 关联 Skill（可选） + 巡检提示词（`prompt`） + 任意直接命令（`command`，可选）**，由目标 Agent 实际执行并持久化真实产物（`JSON`、`Markdown + JSON`、`Markdown` 或纯文本）。详情页提供无硬编码结构的动态产物可视化器（自动识别标量指标卡、状态徽章、百分比进度条、对象数组表格及嵌套结构），并支持在「可视化 / Markdown / JSON」之间切换与导出；控制台默认直连真实 Gateway，全站不展示静态演示数据或虚构模型结论。
- **「Skill 管理」**（`?view=assets&area=skills`）：平台级 Skill 仓库与运行态能力中心，采用 **文件目录化存储（`<dbDir>/skills/<slug>/`） + SQLite 轻量索引** 架构。分为「平台 Skill 仓库」（系统 Skill 与用户上传的 `.md`/`.zip` 自定义 Skill，支持编辑、启停、热同步与 **在线试跑验证** 并内联呈现 Markdown + JSON 巡检产物）与「Agent 运行态 Skill」（经内部工具 `doops_skills_inspect` 实时扫描目标 Agent `/root/.agent/skills/`，区分平台托管与镜像内置 Skill）两张页签，供「AI 助手」与「智能巡检」共同调用。
- **「MCP 管理」**（`?view=assets&area=mcp`）：统一管理 Agent 内置 MCP（`doops`、`dosql`）与用户接入的外部自定义 MCP（`stdio` / `http` / `sse`）。支持在目标 Agent 上通过 `doops_mcp_inspect` 发起真实 MCP JSON-RPC 2.0 `initialize` + `tools/list` 探测并展示延迟与完整工具列表/参数 Schema；通过 `doops_mcp_sync` 原子写入 `/root/.agent/doops-mcp-servers.json` 并合并至 `/root/.agent/settings.json`（保留既有模型配置不变），同时将已探测工具注入 `do-agent` 权限允许列表，供「AI 助手」与「智能巡检」直接调用。
- **「平台监控」**（`?view=assets&area=board`）：纯粹面向 **Agent 维度**，按「平台总览」「Agent 节点」「使用排行」「运行与异常」切换同一时间窗（`1h` / `24h` / `7d` / `30d`）与集群筛选下的连接、并发水位、调用与大模型 Token 统计。
- **「机器台账」**（`?view=assets&area=machines`）：负责机器登记、自定义标签、「探针覆盖率 (100%)」与「纳管覆盖率 (80%)」。
- **「业务系统」**（`?view=assets&area=systems`）：负责业务系统及部署、数据库依赖关系台账维护。

「目标运维 → 后台任务」仅承接当前选中目标的 Gateway 托管 `exec --bg` 后台任务。完整验收与指标边界说明见 [智能综合感知、巡检与监控用户手册](DOOPS_ACCEPTANCE_USER_MANUAL.md)。

「资产 → Kubernetes」是集群台账的唯一菜单入口。可登记待接入集群（自动编号、名称、环境、负责人）；创建者可选择自己获授权的在线 Agent 做只读发现、预览并确认关联。未接入时只对创建者可见；关联后按实际来源授权展示。同一集群有多个来源时，修改整份资料需要有权访问所有来源。工作负载操作使用选中行的来源和命名空间。解除来源关联和移出待接入登记不会销毁真实集群；存在 Node、部署或分配关系时须先处理依赖。

数据库按引擎、主机、端口、库名登记；网站按协议、域名、访问路径登记；镜像按仓库地址、镜像路径和 Tag 或 Digest 登记。未绑定目标时是个人待接入资料；显式选择目标后，获授权用户共同维护，操作入口绑定该条记录的 Agent。登记不是连通性验证。旧版个人登记单独只读展示，不自动公开。凭据沿用原管理和授权流程。

「平台管理」跳转到独立页面 `/console/admin`，集中实例、配置中心、用户、权限、令牌、敏感词、代理路由和 Agent管理。仅管理员可见，直接访问也会核对当前会话。

在「机器台账 → 编辑机器」维护标签（支持平面标签及 `key=value` 键值标签，如 `院系=计算机`），列表按标签键分组筛选并取交集。「机器台账」内设「机器清单」「探针覆盖率（目标 100%，支持缺口原因分类、批量隧道补采与 CSV 导出）」和「纳管覆盖率 (80%)」三个页签；全局拓扑位于「态势感知 → 业务→集群→机器拓扑」，即时与定时巡检位于独立入口「智能巡检」。标签是可见元数据，不要输入密码、密钥或连接串。

探针覆盖率按当前账号可见的登记机器计算。台账读取失败时，点「重新读取台账」重试。读取成功且没有可见机器时，页面显示「当前没有可见的登记机器」。

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

「AI 助手」会话中的执行轨迹按轮次和工具分组展示。一组工具调用开始执行后保持展开，直到用户手动收起，长链路巡检不会随每个工具的完成反复自动展开、收起；轮次结束的未确认工具按未确认展示，不据此判定成功。

## AI 助手常用提示词

在「AI 助手」的空会话中，「工作」模式提供 DoOps 自带的运行巡检、发布与交付、网站与数据库模板。点击模板只会把文字填入输入框（已有草稿会保留），可补充信息、编辑后再发送。模板默认用于只读检查或拟定发布方案，不会自动执行。模板卡片最多两行，显示标题和正文的开头。

维护者可在 [DoOps 提示词配置](../../lib/doops-start-prompts.ts) 调整分组、标题和正文；控制台通过 Agent UI 的 `startPromptGroups` 注入，不修改 Agent 的系统提示词。

## 资产台账

在「机器台账」点「发起巡检」检查当前筛选的机器，或点某行的「巡检」只查该机。Agent 在线不等于宿主健康：巡检按资产编号和已绑定的目标读取已核验的宿主样本；隧道模式主动拉取，挂载模式只读已有样本。CPU 或内存使用率达到 90% 时提示偏高，样本超过 5 分钟、指标不完整或无权限时不会判为正常。「监控详情」可查看该机 CPU、内存、进程与采样时间。未接入机器须先关联 Agent 并核验宿主指纹；仅登记 SSH 账号不会启用采集。此入口不创建每周巡检计划。

机器的唯一入口是侧栏「台账与监控 → 机器台账」，默认显示全部机器。「物理机」和「虚拟机」仅为类型筛选；筛选为虚拟机时，手工新增也默认选择虚拟机。即使机器尚未接入 Agent，管理员也可以先登记配置。普通用户看到自己获授权目标关联的资产。

- **机器**支持手工新增、搜索和编辑。物理机登记物理核心数，虚拟机登记 vCPU 数，内存与磁盘容量单位为 GiB。虚拟机可引用已登记的宿主。详情页可反查虚拟机、宿主和手工关联的 K8s Node。修改规格会更新资产记录。
- **文本导入**支持在机器台账或手工新增页粘贴多行清单。机器行格式为 `管理IPv4 账号 密码 [内网IPv4] [SSH端口]`，也支持 `-p 端口`；仅登记资产时可只填写管理 IP。分组名单独占一行（有空格的名称可用方括号包围），作用到下一分组。新机器以管理 IP 生成编号，可选物理机或虚拟机，分组、内网地址和显式端口保存在显示名称中。已有机器按唯一管理 IP 匹配。默认只导入资产，勾选「同时保存登录账号和密码」才会将登录资料送交 Gateway。核对右侧字段后点「预览导入」，再确认写入。
- **CSV 导入**先选择导入内容。选「机器台账」后，使用 UTF-8 模板填写 `asset_id,name,kind,address,cpu_count,memory_gib,disk_gib,host_asset_id`，上传后点「预览 CSV」。必填列为编号、名称和类型。`physical` 表示物理机，`vm` 表示虚拟机。宿主可以出现在同一批次的任意位置。确认前核对可新增、重复和错误行数，只导入可新增记录。每批最多 2,000 行、1 MiB。按批次号回查能看到台账写入结果和本批保存的机器密码数。
- **机器登录凭据**可从已有机器详情点击「添加登录凭据」，填写账号、密码，预览后确认。批量处理时选择「机器和登录凭据」：已有机器可用 `asset_id,username,password` 补录，新增机器在 CSV 中一并填写 `name,kind`。XLSX 可以按资产编号或台账中唯一匹配的管理 IP 关联，预览时请核对跳过的源行。每个账号单独计数。相同凭据再次上传会显示「已有」，密码冲突会阻止确认。管理员可以查看已保存的账号与本批回执。密码由 Gateway 加密保存。
- **集群台账**中点「从 Agent 导入」，填写资产编号、名称和来源 Agent。指定命名空间时仅验证该空间的 Pod 列举权限；留空时读取 Namespace 对象。先预览再确认，Gateway 在确认时重新读取。集群列表保存来源、验证时间和命名空间，断线后仍可查询。展开命名空间可进入对应范围的实时工作负载。
- **凭据**沿用 Gateway 受管凭据。在已关联在线目标的资产详情点凭据按钮，进入「当前目标可用」。具备管理权限的用户可从「我管理的」操作绑定、验证、轮换和吊销。列表只显示名称、状态、版本和目标等日常信息；排查时可点「详情」查看凭据编号、所选版本的编号与校验值、目标绑定编号。适用类型、从已有 Secret 导入的操作见 [凭据说明](CREDENTIAL_RESOURCES.md)。资产文件只填写元数据。

迁移顺序、接口和权限见 [API 指南](DOOPS_API_GUIDE.md#资产台账)。

打开「平台管理 → Agent管理」，选择已登记的 Agent，按机器、集群或业务系统逐项分配。每项资产可以供多个 Agent 查看。撤销仅影响所选目标的显式分配，台账直接关联由对应资产详情维护。登录凭据及目标授权沿用各自的管理流程。

### 平台监控与目标联动

「平台监控」读取 Gateway `/v1/platform-monitor`，展示 Agent 连接状态、注册凭证绑定、离线保留的运行版本（`release_commit`）与上下线闪断事件、分钟级并发/排队水位峰值，以及所选时间窗内的调用趋势、动作分布、大模型 Token 消耗排行、目标与账号使用排行、正在执行的操作、SQLite 持久化的脱离后台任务和近期异常（`outcome_unknown` 以警告态单独标识，提示核对状态后再决定是否重试）。平台管理员查看全局实例、并发水位与账号统计，普通账号仅查看本人授权目标与本人/自有凭据范围内的调用记录。

在线 Agent 行内可点击「准入队列」实时拉取 `do-agent` 名额与排队会话，也可一键进入该目标的工作区、指标、后台任务或审计记录（带 `correlation_id` 的异常自动定位到该操作的审计回执）；顶栏旁的「返回平台监控」可回到原筛选状态。

从机器详情点「进入目标运维」或「查看虚机/主机与容器监控」，或从集群命名空间点工作负载入口，页面会切换到该 Agent 的操作区。目标选择器旁的返回按钮回到原资产，恢复筛选和展开位置。主动切换 Agent 后，操作区采用新目标。

在「业务系统」登记系统编号、负责人和服役状态，再添加来源目标、命名空间、应用与可选数据库。保存表示操作员已核对所填关系。管理员维护记录，其他用户仅能查询自己目标内的部署。

在机器详情中可通过已领取的 Agent 核验主机指纹、获取宿主采样。主机侧按分钟生成带身份的 JSON；页面按采样时间展示系统、内核、进程和资源使用情况。[宿主采样与首次接入](MACHINE_SAMPLING.md)

从主导航或机器详情进入「Agent 监控」，在线目标可直接点击「采集虚拟机 / 主机」「采集运行中容器」（自动识别 Docker / nerdctl / Podman，展示容器 CPU、内存、内存上限及占上限百分比）或填写命名空间「采集 K8s Pod」，也可查看外部采集器上报的历史样本。可按序列查看单位、容量换算提示、数据中断、告警及阈值规则。[设备与容器指标说明](ASSET_MONITOR_DEVICES.md)

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
  ID and time filters, and cursor pagination. Routine `observing` upgrade polls are
  hidden by default; terminal observations remain visible. Each row shows its audit
  ID, operation ID, elapsed time, byte counts and available evidence.
  Users can read their own history; global audit requires platform admin.
  Credential owners also see events that reference their credentials. Audit
  query indexes are created when the Gateway starts.
- Open **数据库** and select an instance. Choose its managed connection to read the database directory. Open a database for its tables and schemas, then select a table to preview rows. The breadcrumb returns to the previous level. Access to each database uses the binding for the selected credential.
- Inventory groups a StatefulSet with Pods whose controller reference matches its UID, kind, name and namespace. Its governing Service supplies the address only when the selector matches its Pods exclusively. Other candidates remain separate. A missing service endpoint is shown as **未确认服务地址**. Previously discovered member IDs can still be used with their existing credential bindings.
- From a database or table choose **执行 SQL**. The editor uses the selected database and credential. From a table it prefills a quoted preview query. Enter a read-only statement and click **执行只读 SQL** for result columns, rows, and the DoSql operation ID. **停止 SQL** requests cancellation and shows the remote outcome.
- The database operations form routes through the same `doops_agent_prompt` Gateway RPC and
  invokes the bundled DoSql skill. It reuses the selected target, session and
  Gateway audit; database connections and credentials are never stored in the
  browser. The page is read-only (query, discover, plan); changes go through
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
  `doops_ssh` commands. Each rule targets all text,
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
  只有离线记录可归档或恢复，点击对应操作直接提交。归档不会删除 target 授权、令牌、凭据绑定或审计历史，
  Agent 重新连接时会自动恢复该记录。离线超过 90 天的实例记录由 Gateway 自动删除，授权仍保留。
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
  target) combines 「定时巡检与记录」 (scheduled/manual Skill-package inspections and
  run reports across authorized targets) and 「后台运行任务」 (the current user's
  Gateway-hosted detached `doops_bg` tasks — running, success, failed, canceled,
  timeout, outcome_unknown — for the selected target, even while the Agent is
  offline). Detail output polls while a background task runs; 停止 submits
  directly without a confirmation dialog and only acknowledges the request — the
  terminal state still arrives through the task record. A finished task reports
  「任务已结束」.
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
- 控制台通过 `next.config.mjs` 为所有路由设置安全响应头：`frame-ancestors 'none'` 与
  `X-Frame-Options: DENY`（禁止被第三方页面嵌入）、`nosniff`、`Referrer-Policy: same-origin`
  和限制摄像头/麦克风/定位的 `Permissions-Policy`。`/blog/images/` 返回的 SVG 额外带
  `sandbox` 与 `default-src 'none'` 的 CSP；其他图片类型不受影响。HSTS 由终止 TLS 的
  Nginx 或入口网关设置，不在 Next.js 应用内设置（避免开发或内网 HTTP 访问被锁定）。
- 模型密钥在凭证管理中录入、验证并启用。配置中心记录模型配置与凭证引用，打开时会清除旧浏览器配置里的 API 密钥字段。
- 「平台监控」除原有的隧道、调用与 Token 视图外，提供「系统资源监控」（跨 Agent 全系统机器/K8s/容器/应用/数据库资源总览、趋势曲线、Top 排行、挂载点/磁盘 I/O/网卡/GPU/K8s 异常 Pod 展开明细与一键全量采集）与「巡检任务与记录」（定时计划管理、按 Skill 包即时下发巡检、历史执行记录、单次明细报告、异常项复检及 CSV/JSON 导出）；「Agent 监控」支持对该目标一键触发全 Skill 包巡检采集。

## Console layout and shared controls

[`DESIGN.md`](../../DESIGN.md) defines console spacing, semantic tokens and control states.
Console views fill the available width with a 16px gutter; only public pages, dialogs
and document previews use bounded reading widths. Desktop and mobile share the same
components. Use `components/ui.tsx` for buttons, inputs, selects, textareas, segmented
controls and page tabs; keep business behavior and permission checks in the host panel.

The pinned `@agent-cloud/agent-ui@0.3.52` display patch is tracked in `patches/`
and applied by pnpm in local and Docker builds through `pnpm.patchedDependencies`
in `package.json`. Tool activity remains in the conversation list, and 0.3 keeps
execution evidence in the "Execution trace" drawer whose trigger counts interactions
and runs, so tool rows are not rendered a second time inline. Titles that repeat the
workspace path or translated tool label are omitted by the SDK; the patch keeps
textual progress stages/messages on the tool row, including progress without a
fraction.
