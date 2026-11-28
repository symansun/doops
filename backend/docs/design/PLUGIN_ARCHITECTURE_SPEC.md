# DoOps 全栈场景化插件架构规范（Spec）

> 状态：权威架构规范（Phase 1 与 Phase 2 均已实施生效；Phase 3 标记为后续按需规划边界）。
> 适用范围：Gateway 插件目录与配置存储（SQLite / MySQL 双引擎）、`/v1/plugins` 与 `/v1/admin/plugins` 控制面、L2 声明式 `plugin.yaml` 热插拔全链路、`GET /v1/monitor/metrics/latest` 聚合接口、Web 控制台「插件中心」与 L1/L2 插件运行时。
> 相关文档：[SKILL_FIRST_INSPECTION_SPEC.md](SKILL_FIRST_INSPECTION_SPEC.md)、[SKILL_PRODUCT_DESIGN_MATRIX.md](SKILL_PRODUCT_DESIGN_MATRIX.md)、[DOOPS_WEBUI.md](../DOOPS_WEBUI.md)、[DOOPS_API_GUIDE.md](../DOOPS_API_GUIDE.md)、[GATEWAY_MYSQL_OPERATIONS.md](../GATEWAY_MYSQL_OPERATIONS.md)。

---

## 1. 核心定位与架构红线

DoOps 采用 **极薄平台内核（Core Shell）+ 场景化全栈插件（Plugins）** 架构，使不同运维场景按需装配控制台能力而不破坏平台安全与开放边界：

1. **授权模型不变**：目标授权仍是 `(user, cluster, instance)` 与平台管理员标记 `is_admin`。插件开关只控制控制台 UI 装配与路由可见性，**绝不**演化为按操作/按插件权限门禁。`TestPluginTablesOnlyReadByPluginFiles` 强制阻止授权、RPC、执行和巡检调度路径引用 `platform_plugin_*` 表。
2. **绝不触碰后端运行态与 CLI**：切换场景包或隐藏插件**绝不**修改 `platform_skills.enabled`、`platform_mcp_servers.enabled` 或 `monitor_schedules.enabled`，不删除任何台账/巡检/凭据历史数据，也不阻断 CLI（`doops`）与自动化脚本。
3. **单一事实源**：内置 L1 插件与 5 套场景预设只在 `backend/agent/internal/server/plugin_catalog.go` 定义一次，导出为 `lib/plugins/builtin-catalog.json` 入库；Go 侧 `TestPluginCatalogJSONInSync` 与前端 `lib/plugins/plugins.test.mjs` 双向守卫零漂移。
4. **严格 CSP 不降级**：遵守 `next.config.mjs` 的生产安全响应头（无 `'unsafe-eval'`、`frame-ancestors 'none'`、`connect-src 'self'`），绝不支持在主窗口执行未受信任的第三方 JS。
5. **双库一致与显式兼容**：SQLite 与 MySQL（8.4 / 8.0 / 5.7.22+）共享完全一致的表结构与迁移保障；新版控制台对接旧版 Gateway（`/v1/plugins` 返回 404）时显式按 `all-in-one` 兼容渲染并向管理员展示升级提示，不做静默降级。

---

## 2. 内核与内置插件归属

### 2.1 平台内核 Core Shell（11 项，不可关闭）

`home`（首页）、`overview`（Agent 概览）、`ask`（AI 助手）、`terminal`（终端）、`kb`（知识库，AI 助手默认启用 `mcp_doops_kb_read`）、`credentials`（凭据）、`audit`（审计记录）、`plugin-center`（插件中心，整合「场景与功能插件」「Skill 能力包」「MCP 工具服务」，兼容旧 `?area=skills` / `?area=mcp`）、`deploy-onboard`（首次接入）、`deploy-upgrade`（升级 Agent，[AGENTS.md](../../../AGENTS.md) 强制保留的安全升级通道）、`platform-admin`（平台管理，仅管理员）。

### 2.2 8 个 L1 第一方内置插件（`next/dynamic` 懒加载）

| 插件 ID | 名称 | 接管入口 | 目标页签 | 依赖能力（仅展示就绪态） | 能力过滤 |
|---|---|---|---|---|---|
| `doops.inspection` | 态势感知与监控 | 态势感知、资源监控、Agent 运行监控 | `monitor` | Skill `inspect-vm-host` | — |
| `doops.k8s` | Kubernetes 治理 | Kubernetes（集群台账） | `k8s` | Skill `inspect-k8s-cluster`、MCP `doops` | `kubernetes_available !== false` |
| `doops.delivery` | 应用发布与交付 | 发布工作负载、部署包、CI/CD 接入 | `files` | MCP `doops` | 发布工作负载需 K8s |
| `doops.database` | 数据库治理 | 数据库 | `database` | Skill `inspect-database`、MCP `dosql` | — |
| `doops.website` | 网站与服务 | 网站 | `website` | Skill `inspect-app-web`、MCP `doops` | — |
| `doops.image` | 镜像构建与仓库 | 镜像 | `image` | Skill `inspect-container-runtime` | — |
| `doops.cmdb` | 资产台账（CMDB） | 机器台账、业务系统（并向 Agent 概览提供「台账分配范围」页签） | — | Skill `inspect-vm-host` | — |
| `doops.automation` | 自动化与巡检 | 自动化（含兼容旧 `?area=inspection` 书签） | `tasks` | — | — |

### 2.3 核心内置 L2+ 声明式能力插件（12 个 Core Builtin System Skills + `plugin.yaml`）

全部 12 个平台核心内置系统 Skill 均具备完整 L2+ `plugin.yaml` 产品清单（逐 Skill 详细页面与交互设计见 [SKILL_PRODUCT_DESIGN_MATRIX.md](SKILL_PRODUCT_DESIGN_MATRIX.md)）。其中 **4 个独立领域工作台**（`machine-workload-discovery`、`gpu-governance`、`host-security`、`attack-surface-scan`）开启侧栏挂载（`sidebar.enabled: true`），其余 8 个与 L1 内置模块职责重叠或专项巡检类 Skill（`inspect-*`）收口为 `sidebar.enabled: false`（继续通过「自动化」、目标页签与首页「专项能力监测」提供服务，避免侧栏被纯巡检脚本刷屏）；全部核心内置 Skill 受 `isBuiltinSystemSkill` 保护（`builtin: true`、`can_delete: false`，不可删除仅可覆盖或重置）：

| 插件 ID | 关联 Skill（伴生 Agent Skill） | 默认所属场景 | 首页卡 | 核心视图与交互能力 |
|---|---|---|---|---|
| `skill.inspect-vm-host` | `inspect-vm-host`（`shell`） | `host-security-ops`、`ai-compute-cluster`、`all-in-one` | ✓ | 主机 CPU/内存/去重磁盘/I/O/M1–M8 基线、`mounts` 资源表、`vm-diag`/`vm-sysctl-plan` 操作台 |
| `skill.machine-workload-discovery` | `machine-workload-discovery`（`shell`） | `all-in-one`、`app-delivery-web`、`host-security-ops`、`ai-compute-cluster` | — | 零 Token 挖掘数据库/运行时（Java/Python/PHP/Node/Go）/业务系统及版本、`workloads` + `suggested_tags` 双表、一键合并同步至机器台账 |
| `skill.gpu-governance` | `gpu-governance`（`shell`） | `ai-compute-cluster`、`all-in-one` | ✓ | NVIDIA GPU 利用率/显存/温度/ECC、阈值表单、`gpus` 单卡资源表、`gpu-zombie-check`/`gpu-thermal-plan` |
| `skill.inspect-k8s-cluster` | `inspect-k8s-cluster`（`k8s`） | `ai-compute-cluster`、`app-delivery-web`、`all-in-one` | ✓ | 仅 K8s 目标可见；Node/Pod/PVC/事件、`pod_usage` Top40 资源表、`k8s-triage`/`k8s-remediate-plan` |
| `skill.host-security` | `host-security`（`shell`） | `ai-compute-cluster`、`host-security-ops`、`all-in-one` | ✓ | 挖矿/代理/SSH/补丁综合治理、灵敏度枚举、`findings_list` 根因归并、`sec-triage/harden/contain` |
| `skill.inspect-container-runtime` | `inspect-container-runtime`（`shell`） | `app-delivery-web`、`host-security-ops`、`all-in-one` | ✓ | 仅 Docker/Linux 目标；`containers` 资源表、Top8 内存图、`container-inspect/ops-plan/ops-apply` |
| `skill.inspect-app-web` | `inspect-app-web`（`doweb`） | `app-delivery-web`、`all-in-one` | ✓ | 联动 `website_urls` 探活 HTTP/TLS 证书与 `nginx -t`、`endpoints` 资源表、`web-avail-check`/`web-cert-plan` |
| `skill.attack-surface-scan` | `attack-surface-scan`（`dosecurity`） | `host-security-ops`、`app-delivery-web`、`all-in-one` | ✓ | 联动 `website_urls` 低速只读扫描 OWASP 安全头、8 类敏感路径（抗 SPA 误报零回显）、根因评分与报告导出 |
| `skill.inspect-database` | `inspect-database`（`dosql`） | `app-delivery-web`、`all-in-one` | ✓ | AI 驱动 + `required_mcp: dosql` + `database_refs`、零明文凭据只读体检、`db-health-check`/`db-schema-plan` |
| `skill.inspect-ssh-host` | `inspect-ssh-host`（`shell`） | `host-security-ops`、`all-in-one` | — | AI 驱动按需核查受管 `doops_ssh` 通道授权边界，未授权如实报 `managed_ssh_channel=unavailable` 绝不伪造 |
| `skill.inspect-package-vuln` | `inspect-package-vuln`（`shell`） | `host-security-ops`、`all-in-one` | — | 零 Token 盘点 dpkg/rpm/apk 核心组件与待升级补丁、`packages` 资源表、`pkg-audit-plan`/`pkg-upgrade-apply` |
| `skill.inspect-proxy-evasion` | `inspect-proxy-evasion`（`shell`） | `host-security-ops`、`all-in-one` | — | 零 Token 扫描代理进程/端口/脱敏环境变量、`suspect_processes` 资源表、`proxy-trace-check`/`proxy-contain-apply` |

### 2.4 外部可安装全栈插件（零内核代码耦合，`PLG-03`）

DNS（`plugins/dodns/`）与 Email（`plugins/doemail/`）等可选运维领域能力已彻底解耦为**外部自包含可安装插件包**，不编译进 `doops-gateway`/`doops-agent` Go 二进制、不打入 Agent 基础镜像、不在核心前端硬编码字段或菜单；管理员可通过「插件中心 → 安装插件包 (.zip)」或 `POST /v1/skills`（`scope: "system"`）一键安装、按需启用与彻底卸载（`builtin: false`、`can_delete: true`）：

- **自声明资产目录（`catalog_schemas[]`）**：复用 `inventory_resource_catalog`（零 DDL），以 `kind = "plugin:<slug>:<key>"` 动态存储并校验（字段类型仅限 `domain | host | ip | port | label | enum`，严格拒收含 `secret`/`token`/`password`/`key` 等敏感命名键）；巡检执行时自动批量注入 `DOOPS_PLUGIN_*` 环境变量，前端 `<InlineCatalogManager>` 完全按 `manifest.catalog_schemas` 动态渲染表单与列表。
- **插件自带独立 MCP Server（`mcp_server`）**：插件包内可随附独立 stdio MCP 脚本（如 `${SKILL_DIR}/mcp/server.py` / `server.mjs`），管理员安装/启停/卸载系统插件时由 Gateway 自动向 `platform_mcp_servers` 完成注册、同步开关与清理（严禁覆盖内核 `doops`/`dosql`、严禁 `sh`/`bash` 命令与 `${SKILL_DIR}` 路径逃逸）；Agent 侧 `mcp_agent.go` 安全展开 `${SKILL_DIR}` 并经 `customSyncedMCPToolsForServer(skill)` 将外部插件的 `ask --apply` 工具边界严格隔离在「自身 MCP + 受审计 `Bash`」，不泄漏任何核心写 MCP 或其他插件工具。

---

## 3. 场景包与生效规则

### 3.1 内置 5 套场景预设

| 场景 ID | 名称 | 默认启用的 L1 插件 | 默认启用的核心内置 L2+ 插件 |
|---|---|---|---|
| `all-in-one` | 全功能模式（存量升级默认） | 全部 8 个 L1 插件 | 全部 12 个核心内置 L2+ 插件（含 8 个首页卡片）；已安装的外部 `opt_in: true` 插件默认关闭、按需启用 |
| `ai-compute-cluster` | AI 算力 / 高校集群 | `doops.inspection`、`doops.k8s`、`doops.cmdb`、`doops.automation` | `skill.inspect-vm-host`、`skill.machine-workload-discovery`、`skill.gpu-governance`、`skill.inspect-k8s-cluster`、`skill.host-security`（5 个） |
| `app-delivery-web` | 应用交付与 Web 运维 | `doops.inspection`、`doops.k8s`、`doops.delivery`、`doops.database`、`doops.website`、`doops.image`、`doops.automation` | `skill.machine-workload-discovery`、`skill.inspect-k8s-cluster`、`skill.inspect-container-runtime`、`skill.inspect-app-web`、`skill.attack-surface-scan`、`skill.inspect-database`（6 个） |
| `host-security-ops` | 主机与安全合规运维 | `doops.inspection`、`doops.cmdb`、`doops.automation` | `skill.inspect-vm-host`、`skill.machine-workload-discovery`、`skill.host-security`、`skill.inspect-container-runtime`、`skill.attack-surface-scan`、`skill.inspect-ssh-host`、`skill.inspect-package-vuln`、`skill.inspect-proxy-evasion`（8 个） |
| `minimal-workbench` | 极简 AI + 终端工作台 | 无（仅保留 Core Shell） | — |

### 3.2 生效公式与写入语义

- **L1 内置插件 `P`**：`effective(P) = (P ∈ overrides) ? overrides[P].enabled : (P ∈ scenario(active).defaults)`。
- **系统 L2 插件 `skill.S`（`scope == "system"`）**：`effective(skill.S) = skill(S).enabled ∧ ui_manifest_valid(S) ∧ ((skill.S ∈ overrides) ? overrides[skill.S].enabled : (!manifest.plugin.opt_in ∧ (manifest.scenarios 为空 ∨ active ∈ manifest.scenarios)))`。当 `plugin.opt_in: true` 时不允许同时声明 `scenarios[]`，默认在所有场景下 `scenario_default = false`，仅当管理员通过「插件中心」显式打开开关（写入覆盖）后挂载。
- **自定义 L2 插件 `skill.S`（`scope == "custom"`）**：`effective(skill.S) = skill(S).enabled ∧ ui_manifest_valid(S)`，不进入 `platform_plugin_overrides`，且仅向所有者本人（`owner_is_me === true`）返回与挂载至侧栏「场景扩展」（绝不污染管理员或其他用户的导航；管理员仍可在「Skill 能力包」查看全站自定义 Skill）。
- **`apply_scenario`**：更新 `scenario_id`，清空全部 `platform_plugin_overrides`，`revision + 1`。
- **`set_plugin`**：接受内置 L1 `doops.*` 或已存在且具备有效 `plugin.yaml` 的系统 L2 `skill.<slug>`（拒绝 Core ID 与自定义 L2 Skill）。若目标值等于当前场景默认值则删除覆盖行，否则写入覆盖行，`revision + 1`。
- **`clear_override`**：管理员清理 `stale_overrides` 中的陈旧 ID，`revision + 1`。
- **`reset`**：重置为 `all-in-one` 并清空全部覆盖行，`revision + 1`。

---

## 4. L2 声明式 `plugin.yaml` 热插拔规范（Phase 2 生效）

### 4.1 唯一解析端与同步过滤

- Gateway 使用 `gopkg.in/yaml.v3`（`KnownFields(true)` 严格模式 + 单文档检查）解析 Skill 包根目录可选 `plugin.yaml`，校验通过后将规范 JSON 写入 `platform_skills.ui_manifest_json`（错误写入 `ui_manifest_error`）；**浏览器只消费 JSON，不引入 YAML 解析器**。
- `POST /v1/skills` `action=upsert` 遇到非法 `plugin.yaml` 时直接返回 `400 invalid_plugin_manifest`（含字段路径）并原子拒绝落盘。
- 手工修改磁盘 `plugin.yaml` 时，读取路径比对包 `sha256` 漂移自动触发重解析并刷新数据库索引。
- `syncOneSkillToAgent` 下发 `doops_skill_sync` 前通过 `filterAgentSyncSkillFiles` **显式剥离 `plugin.yaml`**，避免前端 UI 声明进入 `/root/.agent/skills/<slug>/`。

### 4.2 `plugin.yaml` 校验红线

- 文件大小 `<= 32 KiB`；`apiVersion: doops.sh/v1alpha1`、`kind: SkillPlugin`；`plugin.id` 省略或严格等于 `skill.<slug>`；文本字段纯文本无控制字符（`name/label/title <= 24` rune、`description <= 200` rune）；图标归一化至 20 项白名单 Lucide 图标（越界回退 `layers`）；`scenarios[]` 必须命中 5 套内置场景 ID。
- **`ui.inputs[]`（上限 16 项）**：键匹配 `^[A-Z][A-Z0-9_]{0,31}$`；类型严格限定 `number | boolean | select | credential_ref`，**严禁自由文本 `text` 类型**（直接 `400 invalid_plugin_manifest`）。
- **`ui.presets[]`（上限 8 项）**：`mode` 仅限 `script | agent`；`inputs` 键必须全部已在 `ui.inputs` 声明且值合法；`agent` 模式必须提供非空 `prompt`（`<= 2000` rune）。
- **`ui.views[]`（上限 8 项）**：支持 `kpi_grid | checks_table | charts | trend_series | artifact_viewer | resource_table | findings_list`；指标键匹配 `^[a-z0-9_.-]{1,96}$`。`resource_table` 须声明合法 `source_key` 与 1..8 列（`text | badge | code | number`），可选绑定已声明的 `row_action_id`；`findings_list` 自动接入平台根因封顶评分引擎与覆盖缺口提示。
- **`ui.actions[]`（上限 6 项）**：声明就地受控操作面板动作（`mode: check | plan | apply`，绑定 `skill`、可选 `credential_use` 与 `prompt_template`），通过既有受审计 `doops_agent_prompt` 流式返回真实执行证据，绝不绕过 `doagentAllowedTools` 变更白名单。
- **`capabilities.catalog_inputs[]`（仅系统 Skill 生效）**：由 Gateway 通用声明式注册表（`plugin_catalog_inputs.go`）统一校验并单次批量查询核心 `inventory_resource_catalog`（`website`/`database`/`image`），白名单支持 `website_urls`（`DOOPS_INSPECT_URLS`）、`website_domains`（`DOOPS_INSPECT_DOMAINS`）、`database_refs`（`DOOPS_CATALOG_DATABASES`）与 `image_refs`（`DOOPS_CATALOG_IMAGES`），自定义 Skill 绝不触发目录自动注入；可选 `capabilities.companion_agent_skill` 声明伴生 Agent 运维 Skill。
- **`catalog_schemas[]`（上限 4 组，每组 1..6 字段）**：允许任意外部插件自声明领域资产目录 Schema，零 DDL 存储于 `inventory_resource_catalog`（`kind = "plugin:<slug>:<key>"`），`env_var` 须匹配 `^DOOPS_PLUGIN_[A-Z0-9_]{1,40}$`，字段类型仅限 `domain | host | ip | port | label | enum` 且强制拒收含 `secret`/`password`/`token`/`key`/`credential` 等敏感命名键，安装后由 `<InlineCatalogManager>` 动态渲染表单并在系统插件执行时自动注入 `DOOPS_PLUGIN_*`。
- **`mcp_server`（可选，单插件最多 1 个）**：允许外部插件声明随包附带的独立 stdio MCP（如 `command: python3` / `node`，`args: ["${SKILL_DIR}/mcp/server.py"]`）或受控 HTTP/SSE MCP；严格禁止使用内核保留标识 `doops`/`dosql`、禁止 `sh`/`bash` 等 shell 解释器命令、禁止 `${SKILL_DIR}` 路径逃逸与内联密钥参数。管理员安装、启停或卸载系统插件时由 Gateway 自动同步至 `platform_mcp_servers`。

### 4.3 `inputs` 全链路执行与 `credential_ref` 安全边界（T2）

- `POST /v1/monitor/runs` 与 `POST /v1/monitor/schedules`（`create`/`update`，持久化至 `monitor_schedules.inputs_json`）均支持可选 `inputs`：
  - **单 Skill 硬约束**：传入非空 `inputs` 但 `len(skills) != 1` 时返回 `400 inputs_require_single_skill`。
  - **强校验与防泄露**：未声明键返回 `400 unknown_plugin_input`；类型/范围/枚举违规或疑似内联凭据明文返回 `400 invalid_plugin_input`。
  - **`credential_ref` 失败关闭**：名称须为合法小写资源名（否则 `400 plugin_credential_ref_invalid`），必须已激活、有已发布 active 版本且已授权绑定到本次运行范围内的每一个目标（否则 `400 plugin_credential_ref_unbound` 或 `403 monitor_target_forbidden`）；定时计划在运行期按目标复检绑定状态；全链路**只传递凭据资源名，绝不解密或传递凭据载荷**。
- **注入方式**：脚本直跑经 `skillScriptCommand` 以 `shellSingleQuote` 注入 `export DOOPS_INPUT_<KEY>='<escaped>'`（经 `sanitizeSecretText` 脱敏并记录在受审计 `doops_shell` 与 `evidence.inputs`）；AI 驱动模式以定界块 `<<DOOPS_INPUTS>>...<<END_DOOPS_INPUTS>>` 附加至提示词末尾。

---

## 5. 数据存储与控制面 API

### 5.1 双库 Schema（SQLite & MySQL）

- `platform_plugin_config`：单行表（`id='default'`），保存 `scenario_id`、`revision`、`updated_by`、`updated_at_ms`。
- `platform_plugin_overrides`：保存偏离当前场景默认值的覆盖行（`plugin_id`、`enabled`、`updated_by`、`updated_at_ms`）；删除自定义 Skill 时自动清理残留覆盖行。
- Phase 2 扩展列：`platform_skills.{ui_manifest_json, ui_manifest_error}` 与 `monitor_schedules.inputs_json`（SQLite `TEXT NOT NULL DEFAULT ''/'{}'`，MySQL 无 DEFAULT 的 `MEDIUMTEXT NOT NULL`，存量库启动由 `gatewayMySQLEnsurePluginPhase2Columns` 幂等补齐，全部 `INSERT` 显式传列值）。

### 5.2 HTTP 接口与审计

- **`GET /v1/plugins`**（任意已登录用户；BFF `GET /api/plugins`）：返回 `supported`、`configured`、`scenario_id`、`revision`、`customized`、`scenarios[]`、`plugins[]`（含 8 个 L1 与可见 L2 声明式插件及 `manifest` / `manifest_error` / `owner_is_me`）与 `stale_overrides[]`，支持 `ETag` / `If-None-Match` `304`。
- **`POST /v1/admin/plugins`**（仅平台管理员；挂 `auditAdminMutation`；BFF 复用 `/api/admin/plugins`）：严格 `DisallowUnknownFields`、`expected_revision` 409 乐观锁、`dry_run` 差异预览、审计不可用 503 fail-closed。
- **`GET /v1/monitor/metrics/latest`**（任意已登录用户；BFF `GET /api/monitor/metrics/latest`）：支持精确按对查询 `?pairs=<skill>:<key>,...`（单批上限 64 对）与向后兼容 `?skills=<csv>&keys=<csv>`（单批上限 24 Skill / 96 Key），按 `(skill_slug, metric_key)` 独立有限扫描彻底消除高频 Skill 饥饿效应，并按调用者已授权目标聚合每台目标最新采样点；前端 `<HomePluginWidgetGrid>` 无硬上限截断，自动按 32 对/批并发拉取并支持分类筛选、自定义显隐与渐进展开。

---

## 6. 前端运行时契约

- **防闪烁启动与同步**：登录态就绪后优先读取 `sessionStorage` 缓存渲染侧栏，携带 `If-None-Match` 增量刷新（window focus + 60s 轮询），登出清缓存。
- **路由与插槽**：侧栏「场景扩展」（`extensions`）挂载已启用的系统 L2+ 与本人自定义 L2 插件（`?view=plugin&plugin=skill.<slug>`）；目标概览与目标页签支持按 `capabilities.target_modes` 过滤的 `plugin:skill.<slug>`；首页可插拔 `<HomePluginWidgetGrid>` 无硬上限截断（默认展示前 6 张并可一键展开全部，支持分类筛选与自定义显示/隐藏），按 32 对/批并发拉取 `/api/monitor/metrics/latest?pairs=...`（8s 超时独立降级）。
- **`<SkillPluginHost>` 渲染与诚实空态**：由 `next/dynamic` 懒加载并包裹 `<PluginErrorBoundary>`；当 `kpi_grid` 声明的指标键在本次运行中未产出时，明确显示「本次运行未输出 `<key>`」，绝不伪造 `0`。
- **停用态与跨页守卫**：访问已停用 L1/L2 URL 时拦截渲染 `<PluginDisabledState>`；页内中途被停用保留当前页并顶部提醒；所有跨视图跳转经 `canNavigateDestination` 统一守卫。

---

## 7. 后续规划边界（Phase 3，尚未实现）

- **Phase 3（按需规划）**：若确有第三方复杂大屏/自定义交互视图需求，再引入 L3 `sandbox="allow-scripts"` 受控 `iframe` 协议（`doops.plugin.v1`）。
