# DoOps 全量 Skill 与官方外部插件产品设计矩阵

> 本文是 DoOps 平台全部核心内置 Skill（**12 个平台核心内置 L2+ 巡检/发现/治理 Skill** + **7 个 Agent 镜像操作 Skill** + **1 个操作员外部 CLI Skill**）与 **2 个官方独立可安装全栈插件包（`plugins/dodns/` 与 `plugins/doemail/`，零内核耦合，`PLG-03`）** 的权威产品设计文档。
> 底层插件协议与外部插件安装契约见 [PLUGIN_ARCHITECTURE_SPEC.md](PLUGIN_ARCHITECTURE_SPEC.md)，全栈插件演进规范见 [CAPABILITY_PLUGIN_INTEGRATION_SPEC.md](CAPABILITY_PLUGIN_INTEGRATION_SPEC.md)。

---

## 1. 全站产品信息架构（IA）与防过载原则

DoOps 不把 Skill 当作散落脚本，而是按「内核通用能力 vs 外部可插拔领域插件」与「读写风险边界」分层装配进控制台与 CLI，严守四条信息架构准则：

1. **四层能力定位清晰分离**：
   - **Layer 1 · 核心内置巡检、发现与治理 Skill（12/12 全量 L2+ `plugin.yaml` 化）**：全部通过统一 `<SkillPluginHost>` 自动生成独立产品工作台（KPI 卡片、风险发现与根因封顶健康分、可检索领域资产明细表、图表、历史趋势、就地 `ui.actions` 操作台、终端分栏联动、一键中文 HTML/PDF 管理报告导出；含 `suggested_tags` 的发现型 Skill 自动挂载 `<MachineTagSyncCard>` 一键合并同步至 CMDB 机器台账）。
   - **Layer 1B · 官方外部可安装全栈插件包（`plugins/dodns/`、`plugins/doemail/`，零内核硬编码）**：不在 DoOps 核心二进制、基础镜像或核心前端中写死任何 DNS/邮件代码；每个插件包自含 `SKILL.md`、`plugin.yaml`（含 `opt_in: true`、`catalog_schemas[]` 声明式资产目录与 `mcp_server` 独立 MCP 声明）、只读巡检脚本与独立 stdio MCP Server（`mcp/server.py` / `mcp/server.mjs`）。平台管理员在「插件中心 → 安装插件包 (.zip)」一键安装，即可动态获得资产目录 CRUD、侧栏工作台、受隔离独立 MCP 与一键卸载清理。
   - **Layer 2 · Agent 镜像内置操作 Skill（7 个）**：分为「L1 专属高频交互面板（`k8s`/`dosql`/`doweb`/`image-build`）」「Core 内核交互入口（`shell`）」「核心安全伴生只读工具（`dosecurity`）」「内部 AI 支撑能力（`distill`）」，均受 `PermissionEngine`、Gateway 审计与受管凭据通道保护；`ask --apply` 统一开放受审计 `Bash` 并与交互终端联动。
   - **Layer 3 · 操作员外部 CLI Skill（1 个 `skills/doops/SKILL.md`）**：面向外部 AI 编程/运维助手（Claude Code / Codex / OpenCode / Antigravity）的唯一标准版本化接入契约。
2. **场景包按角色精准投放，绝不全量堆砌**：
   - `ai-compute-cluster`（5 个核心 L2+）：`inspect-vm-host`、`machine-workload-discovery`、`gpu-governance`、`inspect-k8s-cluster`、`host-security`。
   - `app-delivery-web`（6 个核心 L2+）：`machine-workload-discovery`、`inspect-k8s-cluster`、`inspect-container-runtime`、`inspect-app-web`、`attack-surface-scan`、`inspect-database`。
   - `host-security-ops`（8 个核心 L2+）：`inspect-vm-host`、`machine-workload-discovery`、`host-security`、`inspect-container-runtime`、`attack-surface-scan`、`inspect-ssh-host`、`inspect-package-vuln`、`inspect-proxy-evasion`。
   - `minimal-workbench`（0 个插件）：纯粹 Core 内核（首页 / AI 助手 / 交互终端 / 知识库 / 凭据 / 审计 / 插件中心）。
   - `all-in-one`（12 个核心 L2+ 全开）：首页 `<HomePluginWidgetGrid>` 通过 `home_widget.enabled` 精选 8 张实时健康概览卡（`machine-workload-discovery` 属资产挖掘工作台、`inspect-ssh-host` 属按需专项、`inspect-package-vuln`/`inspect-proxy-evasion` 已由 `host-security` 首页聚合，均不占首页），支持分类筛选、自定义显隐与渐进展开，无硬上限截断；外部安装的 `dodns` / `doemail` 插件声明 `opt_in: true`，安装后按需一键启用。
3. **只读采集零 Token 优先 + 写操作受控闭环**：
   - 核心 10 个脚本驱动 Skill 默认零 Token 百毫秒直跑；2 个 AI/MCP 驱动 Skill（`inspect-database`、`inspect-ssh-host`）在预设与表单中如实标注 AI/MCP 路径与能力缺口，绝不伪造 0 值。
   - 任意核心或外部插件工作台的「就地受控操作台」均支持 `check` / `plan` / `apply`，支持 **「终端分栏协同」** 与输出命令块 **「填入终端」**；外部插件在 `apply` 模式下通过 `customSyncedMCPToolsForServer(skill)` 严格隔离在「自身独立 MCP + 受审计 `Bash`」，不泄漏核心写 MCP 或其他插件工具。
4. **交付最后一公里统一化**：
   - 全部核心内置与外部安装的 L2+ 工作台自动接入根因归并封顶评分引擎（`lib/inspection-scoring.ts`）与单文件自包含中文管理级 HTML / 打印 PDF 报告导出（`lib/formal-inspection-report.ts`）。

---

## 2. 核心内置（12 个）与官方外部插件（2 个）逐项产品设计

### 2.1 `inspect-vm-host` — 主机性能与内核体检
- **产品定位与用户**：Linux 运维 / SRE / 算力节点管理员的核心单机与机群健康体检中心。
- **挂载位与场景**：Sidebar(`order: 5`) + Target Tab(`order: 5`, `linux/docker/kubernetes`) + Home Widget(`cpu_percent` / `memory_percent` / `disk_percent` / `load_1m`)；场景：`host-security-ops`、`ai-compute-cluster`、`all-in-one`。
- **视图流水线**：`kpi_grid`（CPU / 内存 / 磁盘最高占比 / 1m 负载 / 进程数 / 磁盘平均延迟）→ `charts`（核心资源水位柱图）→ `resource_table`（`mounts`：挂载点、设备源、总容量 GiB、已用 GiB、占比 %，点击行带入排查上下文）→ `checks_table`（M1–M8 内核安全与硬件基线）→ `trend_series`（24h 曲线）→ `artifact_viewer`。
- **操作台与终端联动**：`vm-diag`（`check`，`shell`：只读排查高负载/IO 瓶颈与 Top 进程）、`vm-sysctl-plan`（`plan`，`shell`：生成内核 `sysctl` 安全基线加固脚本与回滚步骤，可一键填入终端）。
- **降级与边界**：自动区分宿主机/容器 `hostPID` 视角；同盘多挂载点去重防重复累加；无 GPU 节点零误报。

### 2.2 `gpu-governance` — GPU 算力治理（L2+ 升级）
- **产品定位与用户**：AI 算力中心 / 高校实验室集群管理员，监控显存水位、温度、ECC 硬件故障与僵尸占卡任务。
- **挂载位与场景**：Sidebar(`order: 10`) + Target Tab(`order: 10`) + Home Widget(`gpu_util_max_pct` / `gpu_mem_max_pct` / `gpu_temp_max_c` / `gpu_count`)；场景：`ai-compute-cluster`、`all-in-one`。
- **参数与预设**：`WARN_TEMP_C`（50–105°C）、`WARN_MEM_PCT`（50–99%）、`INCLUDE_PROCESSES`；预设：快速算力体检 / 严格温控与显存核查 / AI 排查僵尸占卡。
- **视图流水线（L2+ 升级）**：`kpi_grid`（6 项）→ `charts` → **`resource_table`**（`gpus`：卡号、型号、算力 %、显存 %、温度 °C、ECC 错误，点击行带入排查）→ `checks_table` → `trend_series` → `artifact_viewer`。
- **操作台与终端联动（L2+ 新增）**：`gpu-zombie-check`（`check`，`shell`：定位高显存低算力僵尸进程属主与容器）、`gpu-thermal-plan`（`plan`，`shell`：输出限频/节点排空与告警阈值治理方案，支持终端分栏协同）。

### 2.3 `inspect-k8s-cluster` — K8s 集群深度体检
- **产品定位与用户**：集群管理员的定期基线体检与容量审计工作台，与 L1 `doops.k8s`（实时对象操作）形成「日常操作 + 定时体检/趋势/管理报告」闭环。
- **挂载位与场景**：Sidebar(`order: 15`) + Target Tab(`order: 15`, 仅 `kubernetes` 目标显示) + Home Widget(`k8s_nodes_total` / `k8s_nodes_not_ready` / `k8s_pods_abnormal` / `k8s_warning_events`)；场景：`ai-compute-cluster`、`app-delivery-web`、`all-in-one`。
- **视图流水线**：`kpi_grid`（节点总数 / 异常节点 / Pod 总数 / 异常 Pod / Warning 事件 / 有用量 Pod 数）→ `resource_table`（`pod_usage`：命名空间、Pod 名称、CPU m、内存 Bytes，点击行带入排查）→ `checks_table`（Node 压力 / 控制器副本 / PVC 绑定 / Warning 事件）→ `trend_series`（24h 节点与 Pod 异常趋势）→ `artifact_viewer`。
- **操作台与终端联动**：`k8s-triage`（`check`，`k8s`：调用 `mcp_doops_k8s_read` 深入核查异常 Pod 日志与事件）、`k8s-remediate-plan`（`plan`，`k8s`：生成副本扩容、资源限额或驱逐修复方案并联动终端）。

### 2.4 `host-security` — 主机安全与合规治理（L2+ 升级）
- **产品定位与用户**：安全运维管理员的一站式主机内生安全基线工作台（挖矿木马、违规代理、SSH 配置加固、待更新安全补丁）。
- **挂载位与场景**：Sidebar(`order: 20`) + Target Tab(`order: 20`) + Home Widget(`sec_risk_total` / `sec_mining_suspects` / `sec_proxy_suspects` / `sec_pending_updates`)；场景：`ai-compute-cluster`、`host-security-ops`、`all-in-one`。
- **参数与预设**：`CHECK_PROXY`、`CHECK_UPDATES`、`SENSITIVITY`（`standard`/`strict`）；预设：标准安全体检 / 严格合规审计 / AI 安全事件研判。
- **视图流水线（L2+ 升级）**：`kpi_grid`（5 项）→ **`findings_list`**（顶部直接展示根因归并封顶健康分 + 评级 + 风险发现列表）→ `charts` → `checks_table` → `trend_series`（7d）→ `artifact_viewer`。
- **操作台与终端联动（L2+ 新增）**：`sec-triage-check`（`check`，`shell`：只读核查告警进程父进程、网络连接与定时任务）、`sec-harden-plan`（`plan`，`shell`：生成 SSH 加固与补丁升级维护计划）、`sec-contain-apply`（`apply`，`shell`：经审计 Bash 执行明确授权的隔离或加固动作并支持终端分栏复核）。

### 2.5 `inspect-container-runtime` — 容器运行时治理
- **产品定位与用户**：单机 Docker / nerdctl / Podman 宿主运维人员，填补非 K8s 容器节点缺少专属治理工作台的空白。
- **挂载位与场景**：Sidebar(`order: 25`) + Target Tab(`order: 25`, 仅 `docker`/`linux`) + Home Widget(`containers_running` / `container_max_mem_percent`)；场景：`app-delivery-web`、`host-security-ops`、`all-in-one`。
- **视图流水线**：`kpi_grid`（运行容器数 / 最高容器内存占比）→ `charts`（Top 8 容器内存占比柱图）→ `resource_table`（`containers`：容器名、CPU %、内存占比 %、内存用量、PIDs、状态，点击行带入操作台）→ `checks_table` → `trend_series`（24h）→ `artifact_viewer`。
- **操作台与终端联动**：`container-inspect`（`check`，`shell`：排查高内存/频繁重启容器日志与 OOM 线索）、`container-ops-plan`（`plan`，`shell`：生成资源限制调整与安全重启方案）、`container-ops-apply`（`apply`，`shell`：受审计 Bash 执行指定容器重启或日志清理并验证状态）。

### 2.6 `dodns` — DNS 基础设施治理（**独立可安装外部插件包 `plugins/dodns/`**）
- **产品定位与交付形态**：基础网络与域名管理员按需安装的全栈外部插件包（`bash plugins/package-plugin.sh plugins/dodns` 生成 `dodns-plugin.zip`，在「插件中心」一键上传安装/卸载，零 DoOps 内核硬编码）。
- **挂载位与按需策略**：声明 `opt_in: true`（安装后默认未启用，管理员按需开启后挂载至 Sidebar(`order: 30`) + Target Tab(`order: 30`) + Home Widget(`dns_risk_total` / `dns_zones_total` / `dns_latency_max_ms`)）。
- **声明式目录、凭据与独立 MCP**：通过 `catalog_schemas: [zone]` 动态声明 `plugin:dodns:zone` 资产目录（由 `<InlineCatalogManager>` 通用渲染并自动注入 `DOOPS_PLUGIN_DNS_ZONES`），同时复用核心 `website_domains`；可选 `ALIDNS_CREDENTIAL`（`credential_ref` / `opaqueSecret`，由 Gateway 私有通道物化至一次性 `0600` 文件）；通过 `mcp_server` 自动注册随包独立的 Python stdio MCP（`${SKILL_DIR}/mcp/server.py`），提供纯 Python Wire 查询、开放递归检测、Zone 渲染/校验、以及带时间戳备份 + SOA 递增 + `named-checkzone` + `rndc reload` + **失败自动还原备份** 的受控变更工具。

### 2.7 `inspect-app-web` — Web 可用性与证书巡检（伴生 `doweb`）
- **产品定位与用户**：业务交付与站点值班人员，负责已登记站点的 HTTP 探活、响应延迟、TLS 证书剩余天数与本机 Nginx 语法健康（与 `attack-surface-scan` 清晰分工：本工作台聚焦可用性/证书/Nginx，暴露面扫描聚焦安全头与敏感路径）。
- **挂载位与场景**：Sidebar(`order: 35`) + Target Tab(`order: 35`) + Home Widget(`tcp_listen_ports`)；场景：`app-delivery-web`、`all-in-one`；声明 `catalog_inputs: [website_urls]`（取代旧版硬编码胶水）。
- **视图流水线**：`kpi_grid`（TCP 监听端口数）→ `resource_table`（`endpoints`：站点 URL、状态码、响应延迟 ms、证书剩余天数、状态，点击行带入操作台）→ `checks_table`（含 `nginx -t` 与逐站点探活）→ `trend_series`（24h）→ `artifact_viewer`。
- **操作台与终端联动**：`web-avail-check`（`check`，`doweb`：核查 Nginx 配置、上游响应与证书链）、`web-cert-plan`（`plan`，`doweb`：生成证书续期与站点故障恢复方案并衔接 `doops.website`）。

### 2.8 `attack-surface-scan` — 外部暴露面扫描（伴生 `dosecurity`）
- **产品定位与用户**：应用安全工程师与合规负责人，对已登记站点执行低速只读安全头基线、敏感路径暴露（含 `.env`/`.git/config`/`.git/HEAD`/`swagger`/`actuator`，严格 `2xx` + 抗 SPA/HTML 误报 + 零正文回显）与公开登录面评估。
- **挂载位与场景**：Sidebar(`order: 40`) + Target Tab(`order: 40`) + Home Widget(`surface_risk_total` / `surface_exposed_paths` / `surface_missing_csp`)；场景：`host-security-ops`、`app-delivery-web`、`all-in-one`；声明 `catalog_inputs: [website_urls]`。
- **视图流水线与操作台**：`kpi_grid` → `findings_list`（根因封顶分 + 覆盖缺口）→ `resource_table`（`resources`）→ `charts` → `checks_table` → `trend_series` → `artifact_viewer`；操作台严守只读边界（`surface-remediate-plan` / `surface-deep-plan`，无破坏性 `apply`）。

### 2.9 `inspect-database` — 数据库连通与运行体检（伴生 `dosql`）
- **产品定位与用户**：DBA 与后端负责人，定期通过受管 `mcp_dosql_read` 核查目标数据库的只读连通性、活跃连接、TPS 与锁等待，与 L1 `doops.database`（交互式查询与结构治理）互补。
- **挂载位与场景**：Sidebar(`order: 45`) + Target Tab(`order: 45`) + Home Widget(`active_connections` / `db_lock_waits`)；场景：`app-delivery-web`、`all-in-one`；声明 `catalog_inputs: [database_refs]`、`requires_mcp: [dosql]`、`companion_agent_skill: dosql`。
- **执行形态与视图**：无确定性脚本，所有预设诚实标注 `mode: agent`（走 `mcp_dosql_read`，零明文密码）；视图：`kpi_grid` → `findings_list` → `checks_table` → `trend_series`（24h）→ `artifact_viewer`。
- **操作台与终端联动**：`db-health-check`（`check`，`dosql`：只读查询连接数/锁等待/慢查询概况）、`db-schema-plan`（`plan`，`dosql`：只读分析索引缺口与结构漂移建议，严禁直接变更）。

### 2.10 `doemail` — 邮件服务治理（**独立可安装外部插件包 `plugins/doemail/` · 按需启用 `opt_in: true`**）
- **产品定位与交付形态**：企业自建邮件系统运维管理员按需安装的全栈外部插件包（`bash plugins/package-plugin.sh plugins/doemail` 生成 `doemail-plugin.zip`，在「插件中心」一键上传安装/卸载，零 DoOps 内核硬编码）。
- **按需挂载与声明式目录**：声明 `opt_in: true`（安装后默认关闭、**Home Widget 关闭**、不预推每小时定时计划；管理员在「插件中心」启用后挂载至 Sidebar(`order: 50`) + Target Tab(`order: 50`)）；通过 `catalog_schemas: [domain]` 动态声明 `plugin:doemail:domain` 资产目录（由 `<InlineCatalogManager>` 通用渲染并自动注入 `DOOPS_PLUGIN_MAIL_DOMAINS`）。
- **独立 MCP 与零明文红线**：通过 `mcp_server` 自动注册随包独立的 Node stdio MCP（`${SKILL_DIR}/mcp/server.mjs`），提供 DNS 认证规划/评估、多语言开发者接入指南、K8s 邮件运行时检查规划、账号/监听规划与工作区清单校验，递归拒收任何明文密码/令牌并强制使用 `passwordSecretRef` Secret URI。

### 2.11 `inspect-ssh-host` — 无 Agent 远程主机巡检
- **产品定位与用户**：混合云/堡垒机管理员，对尚未部署 Agent 的传统 Linux 主机核查只读 BatchMode SSH 通道就绪度与远程基线。
- **挂载位与场景**：Sidebar(`order: 60`) + Target Tab(`order: 60`) + **Home Widget 关闭**（专项按需工具，不干扰首页主视图）；场景：`host-security-ops`、`all-in-one`。
- **诚实边界设计**：如实声明 AI 驱动与 `doops_ssh` 通道边界——未授予受管 SSH 通道时绝不伪造数据，在 `checks_table` / `findings_list` 显式展示 `managed_ssh_channel=unavailable` 及授权步骤；操作台提供 `ssh-channel-check`（只读核对机器台账与 `doops_ssh` 可用性）与终端分栏协同。

### 2.12 `inspect-package-vuln` — 软件包与补丁合规审计
- **产品定位与用户**：系统安全与等保合规运维人员，深度盘点 OpenSSL / OpenSSH / Nginx / 内核等关键组件版本、本地缓存待升级补丁数与 `/var/run/reboot-required` 重启需求。
- **挂载位与场景**：Sidebar(`order: 65`) + Target Tab(`order: 65`) + **Home Widget 关闭**（首页补丁概览已由 `host-security` 汇总，避免重复卡片）；场景：`host-security-ops`、`all-in-one`。
- **视图流水线**：`kpi_grid`（已盘点核心组件 / 本地待升级包数）→ `resource_table`（`packages`：组件名、当前版本、包管理器，支持按关键字检索组件版本）→ `checks_table`（组件盘点 / 待升级统计 / 重启需求）→ `trend_series`（7d）→ `artifact_viewer`。
- **操作台与终端联动**：`pkg-audit-plan`（`plan`，`shell`：只读分析待升级包影响范围与维护窗口方案）、`pkg-upgrade-apply`（`apply`，`shell`：受审计 Bash 执行明确指定的安全补丁更新与验证，可联动终端复核）。

### 2.13 `inspect-proxy-evasion` — 违规代理与穿透专项排查
- **产品定位与用户**：安全值班与高校/政企合规管理员，专项定位 `clash`/`mihomo`/`v2ray`/`xray`/`frpc`/`trojan`/`ngrok` 等未备案代理进程、常见代理监听端口及代理环境变量。
- **挂载位与场景**：Sidebar(`order: 70`) + Target Tab(`order: 70`) + **Home Widget 关闭**（首页风险数已由 `host-security` 汇总）；场景：`host-security-ops`、`all-in-one`。
- **视图流水线**：`kpi_grid`（疑似代理进程数 / 常见代理监听端口数 / 代理环境变量数）→ `resource_table`（`suspect_processes`：PID、进程名、命中特征、风险状态，点击行带入溯源）→ `checks_table` → `trend_series`（7d）→ `artifact_viewer`。
- **操作台与终端联动**：`proxy-trace-check`（`check`，`shell`：只读核查命中进程的二进制路径、父进程、systemd/crontab 自启项与外联地址）、`proxy-contain-apply`（`apply`，`shell`：受审计 Bash 执行明确授权的进程终止与持久化清理，支持终端分栏协同）。

### 2.14 `machine-workload-discovery` — 机器部署分析与自动打标
- **产品定位与用户**：CMDB 管理员、多机房资产梳理与 SRE 团队，快速摸清每台机器「到底部署了什么」——数据库及版本（MySQL/PostgreSQL/Redis/MongoDB/ES/ClickHouse/达梦等）、语言运行时及版本（Java/Python/PHP/Node/Go/.NET/Ruby）、Web/中间件及版本、业务系统与应用版本，并自动挖掘符合 DoOps 机器台账规范的标签。
- **挂载位与场景**：Sidebar(`order: 8`) + Target Tab(`order: 8`, 「应用与标签」) + **Home Widget 关闭**（属资产梳理与打标工作台，不与实时健康 KPI 争抢首页）；场景：`all-in-one`、`app-delivery-web`、`host-security-ops`、`ai-compute-cluster`。
- **双轨挖掘引擎与参数**：
  - 受控参数（严守 Q3 禁自由文本）：`SCAN_CONTAINERS`（Docker/nerdctl/Podman 镜像与 Tag）、`SCAN_WEBROOTS`（Nginx vhost 与 `/var/www`/`/opt`/`/srv` 下的 `package.json`/`composer.json`/`pyproject.toml`/`wp-includes/version.php`/`META-INF/MANIFEST.MF`）、`INCLUDE_INSTALLED_RUNTIMES`（是否额外探测未常驻的已安装运行时）。
  - 预设：零 Token 部署挖掘与打标（默认只读运行态）、全量栈与已安装运行时盘点、**AI 深度业务系统语义识别**（针对非标准自研业务系统进一步只读归纳名称与版本）。
- **视图流水线与 CMDB 一键打标闭环**：
  - 顶部 `<MachineTagSyncCard>`（当产物含 `suggested_tags` 自动呈现）：自动匹配当前目标 `(cluster, instance)` 的「机器台账」记录，展示「现有标签（完整保留）+ 勾选新标签（支持取消勾选与 20 标签上限实时门禁）」，平台管理员一键经既有受审计 `POST /api/assets/machines`（`action: "update"` + 乐观锁 `expected_revision`）合并写入，普通用户可一键复制标签清单；同时「机器台账」顶栏提供「批量部署挖掘与打标」（`<MachineBatchTagPanel>`），可对当前筛选的全部在线机器一次发起 `machine-workload-discovery` 并按机预览差异后批量合并写入标签。
  - 视图：`kpi_grid`（5 项）→ `resource_table`（`workloads`：分类、组件/业务系统、版本、技术栈、部署形态、端口、识别依据）→ `resource_table`（`suggested_tags`：推荐标签、维度、置信度、来源、证据）→ `charts` → `checks_table` → `artifact_viewer`。
- **安全与合规红线**：绝对只读，严禁读取 `.env`、`application.yml`、`wp-config.php`、`settings.py` 或任何密码/私钥文件；命令行与镜像证据强制脱敏；标签严格按 `machine_inventory_store.go` 规则（小写、`<= 32` 字、`<= 20` 个、不含凭据词）清洗校验，绝不静默篡改台账。

---

## 3. Layer 2：7 个 Agent 镜像核心操作 Skill 产品设计矩阵

| Agent Skill | 所属交互承载形态 | 产品定位与核心工作流 | 权限 / 凭据 / 终端联动边界 |
|---|---|---|---|
| **`k8s`** | L1 专属面板 `doops.k8s` + `inspect-k8s-cluster` 伴生 | 集群工作负载查看、日志/事件排查、Helm 发布/回滚/卸载（`mcp_doops_k8s_read` + `mcp_doops_helm`） | K8s API 不可用时自动隐藏；变更以 `mcp_doops_helm` 签证回执为权威证据 |
| **`dosql`** | L1 专属面板 `doops.database` + `inspect-database` 伴生 | 表数据浏览、SQL 门禁预检（`lint`）、Schema 结构治理/版本漂移/只读 `status`（`SQL-08`）与受控迁移（`mcp_dosql_read` / `mcp_dosql_exec`） | 强制 Gateway 托管 `database` 凭据，零明文连接串；静态历史样本已脱敏清零 |
| **`doweb`** | L1 专属面板 `doops.website` + `inspect-app-web` 伴生 | 声明式 SiteSpec 管理、`nginx -t` 预检、受控重载、健康验收与快照回滚（`mcp_doops_web_read` / `mcp_doops_web`） | 严禁明文 SSH；变更以 `mcp_doops_web` 签证回执为权威证据 |
| **`image-build`** | L1 专属面板 `doops.image` + `doops.delivery` | 多语言 Dockerfile 规范核验、BuildKit (`buildctl`) OCI 镜像构建与 Harbor/Registry 同步 | 仅构建/同步明确授权目标，凭据走 `imagePull` 托管通道 |
| **`shell`** | Core 内核「交互终端」+ 全插件「终端分栏协同」 | 真实 PTY 交互终端、`nsenter` 宿主机身份校验、单次 AI 命令解释与受审计 `doops_shell` / `Bash` | 全量 `ask` 与 `ask --apply` 均开放受策略约束的 `Bash`，并与控制台终端共享 `sessionId` 工作区 |
| **`dosecurity`** | L2+ 工作台 `skill.attack-surface-scan` 伴生 | 低速只读扫描计划编排、资产脱敏归一化、DoSecurity 产物/防泄密契约校验（`dosecurity_contract.mjs`） | 严格只读评估边界，无破坏性 `apply`；命中 `.env`/`.git` 严禁回显正文 |
| **`distill`** | 内部 AI 支撑能力（无独立菜单） | 超长命令输出/日志压缩摘要，保留退出码、关键错误与产物路径，防止上下文溢出 | 纯数据摘要，不执行日志内嵌指令，不替代上游真实终态回执 |

> 注：`dodns`（`plugins/dodns/`）与 `doemail`（`plugins/doemail/`）已于 `PLG-03` 独立封装为自包含可安装外部插件包（自带只读巡检脚本与独立 stdio MCP Server），不再打入 Agent 基础镜像。

---

## 4. Layer 3：操作员外部 CLI Skill（`skills/doops/SKILL.md`）产品设计

- **目标用户**：在本地 IDE / 终端中使用 Claude Code、Codex、OpenCode、Antigravity 等外部 AI Agent 操作远程集群/服务器的研发与运维工程师。
- **产品定位**：作为 DoOps 面向外部 AI Agent 的「操作契约与安全红线入口」，由 `backend/scripts/install.sh --version <tag>` 从精确 Release Commit 校验安装至 `~/.local/bin/doops` 与项目 `.agent/skills/doops/`。
- **核心产品契约**：
  1. 明确命令路由决策树（`ask` / `ask --apply` / `exec` / `sql` / `web` / `k8s` / `image` / `credential preflight` / `upgrade`）；
  2. 所有反引号命令模板直接被 `backend/test/test_sql_skill_contract.py` 解析并在真实编译 CLI 上运行校验，保证文档与 CLI 零漂移；
  3. 严守凭据安全红线（`credential preflight` 先行校验 `.agent/doops-credentials.yaml`，绝不把密钥写入命令参数或提示词）。
