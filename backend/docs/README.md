# DoOps 权威文档索引

本目录是 DoOps 产品、开发者、操作员与接入方的**当前权威文档单一来源（Single Source of Truth）**。
Web 控制台内置的文档中心（`/docs`）直接渲染本目录的现行文档；CLI 安装器也会把本目录随 Skill 一并同步到业务项目中。

内部开发过程评审、任务台账、事故复盘与一次性客户集群验收证据不随公开代码仓库发布，也不作为产品权威文档。

---

## 按角色快速导航

| 你的角色 | 推荐阅读顺序 |
| --- | --- |
| **开发者（5 分钟接入）** | [集群接入指南](CLUSTER_INSTALL_GUIDE.md) → [CLI 使用指南](DOOPS_USAGE_GUIDE.md) → [自然语言业务发布](NATURAL_LANGUAGE_DEPLOYMENT.md) |
| **后端 / DBA（数据治理）** | [统一运维工作台](UNIFIED_OPERATIONS_WORKBENCH.md) → [DoSql 引擎规范](design/DOSQL_ENGINE_SPEC.md) → [受管凭据资源](CREDENTIAL_RESOURCES.md) |
| **平台管理员（部署与纳管）** | [构建与部署 SOP](BUILD_DEPLOY_SOP.md) → [配置文件与环境变量参考](CONFIGURATION.md) → [Gateway 隧道与生命周期](DOOPS_GATEWAY_TUNNEL.md) → [Gateway MySQL 运维手册](GATEWAY_MYSQL_OPERATIONS.md) |
| **安全与架构审查** | [受管凭据资源](CREDENTIAL_RESOURCES.md) → [Agent 双层命令过滤](design/COMMAND_FILTER.md) → [运行时超时与限额](RUNTIME_LIMITS.md) |
| **巡检与扩展开发** | [Skill-First 智能巡检规范](design/SKILL_FIRST_INSPECTION_SPEC.md) → [业务 Skill 编写与集成](SKILL_INTEGRATION_GUIDE.md) → [出站 Webhook 通知](design/DOOPS_WEBHOOKS.md) |

---

## 全部文档目录

### 一、入门、接入与工作台

| 文档 | 用途 | 状态 |
| --- | --- | --- |
| [集群接入指南](CLUSTER_INSTALL_GUIDE.md) | 安装 CLI/Skill、首次登录发现目标及 Agent 接入入口选择 | 现行权威 |
| [统一运维工作台](UNIFIED_OPERATIONS_WORKBENCH.md) | Web、CLI 与各领域 Skill 的统一身份、授权矩阵与执行边界 | 现行权威 |
| [Web 控制台指南](DOOPS_WEBUI.md) | AI 交互终端、AI 助手协同、资产台账、巡检中心与平台管理页 | 现行权威 |

### 二、日常使用与领域能力

| 文档 | 用途 | 状态 |
| --- | --- | --- |
| [CLI 使用指南](DOOPS_USAGE_GUIDE.md) | 命令选择、session 工作区、代码同步、后台任务、DoSql/DoWeb 与管理命令 | 现行权威 |
| [自然语言业务发布](NATURAL_LANGUAGE_DEPLOYMENT.md) | `ask --apply` 结构化 Helm 交付契约、凭据隔离与实测回读验收 | 现行权威 |
| [DoSql 统一 Go 数据治理引擎规范](design/DOSQL_ENGINE_SPEC.md) | 五引擎数据库 `inspect`/`diff`/`datadiff`/`migrate`/`backfill` 与策略门禁 | 生效中规范 |
| [DoWeb 工作台集成](DOWEB_INTEGRATION.md) | SiteSpec 驱动的 Nginx 配置渲染、校验、重载、健康检查与回滚 | 现行权威 |
| [业务 Skill 编写与接入](SKILL_INTEGRATION_GUIDE.md) | 编写团队专属 `SKILL.md`，固化发布规范与排障 SOP | 现行权威 |
| [模型规范](MODEL_STANDARDS.md) | 通过 `modelProvider` 受管凭据安全下发模型配置及使用规范 | 现行权威 |
| [物理机与宿主机访问](PHYSICAL_HOST_ACCESS.md) | 通过 `nsenter` 与结构化入口检查容器宿主机与物理机硬件 | 现行（环境相关） |

### 三、架构、协议与安全

| 文档 | 用途 | 状态 |
| --- | --- | --- |
| [Gateway 隧道与执行生命周期](DOOPS_GATEWAY_TUNNEL.md) | Agent 反向连接、enrollment token、排队、取消与 `execution_id` 断线续接 | 现行权威 |
| [受管凭据资源](CREDENTIAL_RESOURCES.md) | 凭据创建、授权、验证、提升、主密钥原地轮换、离线强制终结与开发预检 | 现行权威 |
| [Agent 双层命令过滤](design/COMMAND_FILTER.md) | 结构化 argv AST 策略文件（`DOOPS_COMMAND_POLICY_FILE`）与正则兜底拦截 | 生效中规范 |
| [配置文件与环境变量参考](CONFIGURATION.md) | Gateway、Agent、Web 控制台与 CLI 全量配置文件模板、环境变量说明与契约校验 | 现行权威 |
| [运行时超时与限额](RUNTIME_LIMITS.md) | 全部 `DOOPS_*` 超时配置、取值范围与顺序不等式（代码测试逐项比对） | 现行权威 |
| [API 接入指南](DOOPS_API_GUIDE.md) | 面向平台、CI 与内部工具的登录、Target、JSON-RPC、进度流与审计 API | 现行权威 |

### 四、巡检、监控与平台运维

| 文档 | 用途 | 状态 |
| --- | --- | --- |
| [Skill-First 智能巡检规范](design/SKILL_FIRST_INSPECTION_SPEC.md) | 8 大内置巡检 Skill 包、File-First 产物、图表可视化、趋势对比与保留清理 | 权威架构规范 |
| [出站 Webhook 通知](design/DOOPS_WEBHOOKS.md) | Outbox 事件投递、HMAC-SHA256 签名、SSRF 防护与管理员配置接口 | P0 已实现 / 设计 |
| [集群盘点底册与覆盖率对账](ASSET_COVERAGE.md) | 在役资产底册导入、归属核验、缺口统计与批次冻结导出 | 运维手册 |
| [宿主采样与设备监控曲线](ASSET_MONITOR_DEVICES.md) | 宿主 CPU、磁盘 I/O、网卡、挂载点与 GPU 时序指标采集 | 运维手册 |
| [宿主机采样与机器详情](MACHINE_SAMPLING.md) | 只读宿主采样服务部署、`machine-id` 指纹核验与机器详情绑定 | 运维手册 |
| [机器详情的宿主试采](MACHINE_DIAGNOSTICS.md) | 宿主诊断绑定、区间 CPU/内存计算口径与只读挂载兼容说明 | 运维手册 |
| [构建与部署 SOP](BUILD_DEPLOY_SOP.md) | CLI、Gateway、Agent 与 Web 的不可变镜像构建、发布与回滚流程 | 强制 SOP |
| [Gateway MySQL 后端运维手册](GATEWAY_MYSQL_OPERATIONS.md) | MySQL 8.4 存储后端开启、启动门禁、`store mysql-copy` 割接与备份回退 | 运维手册 |
| [Gateway MySQL 迁移方案](GATEWAY_MYSQL_MIGRATION_PLAN.md) | 离线 schema、数据搬运器与运行期 SQL 方言审计计划 | 迁移参考 |
| [综合感知运营与巡检用户手册](DOOPS_ACCEPTANCE_USER_MANUAL.md) | 综合感知运营、智能巡检与系统监控控制台操作手册 | 验收参考 |

### 五、草案与演进中设计

| 文档 | 用途 | 状态 |
| --- | --- | --- |
| [Harbor 镜像同步规范](HARBOR_REPLICATION_SPEC.md) | Harbor 镜像同步与收敛配置 | 草案 |
| [DoOps Edge 隧道设计](design/DOOPS_EDGE_TUNNEL.md) | Agent 反向代理与 Gateway 动态域名映射（默认关闭） | 设计（未上线） |
| [监控与巡检闭环产品设计](design/MONITORING_CLOSED_LOOP.md) | 核心闭环设计提案（权威行为见 API / WebUI 文档） | 提案参考 |
| [监控、巡检与资产感知闭环任务书](design/MONITORING_INSPECTION_LOOP.md) | 采集通道、观测模型与事件闭环总体设计 | 设计（待评审） |
| [Agent 资源同步任务书](design/AGENT_RESOURCE_SYNC.md) | 目标清单与资源同步设计 | 设计（待评审） |

---

## 文档治理原则

- **单一主题归属**：每个主题只在其权威文档展开，其他文档直接链接，不复制粘贴规则段落。
- **同步更新**：新增或改名文档时同步更新本索引、根 `README.md` 与 Web 文档目录（`lib/docs-catalog.ts`）。
- **状态透明**：草案必须标注「草案」，未上线设计放 `design/` 并显式标注实现边界。
