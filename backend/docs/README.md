# DoOps 文档索引

本目录是 DoOps 产品、操作员和接入方的**当前权威文档**。根目录 [docs](../docs/README.md)
只保留内部设计记录；开发过程评审、事故复盘、发布 runbook 和验收证据不在这里，也不随 CLI、
Skill 或 Agent 镜像发布。

## 快速入口

| 文档 | 用途 | 状态 |
| --- | --- | --- |
| [构建与部署 SOP](BUILD_DEPLOY_SOP.md) | 构建、镜像、部署和回滚流程 | 现行（强制 SOP） |
| [集群接入指南](CLUSTER_INSTALL_GUIDE.md) | 安装 CLI/Skill、接入 Gateway target | 现行（安装时固定发布 tag） |
| [CLI 使用指南](DOOPS_USAGE_GUIDE.md) | 命令选择、会话、同步和任务管理 | 现行 |
| [API 接入指南](DOOPS_API_GUIDE.md) | 登录、target、RPC、进度流与审计 | 现行 |
| [Gateway 隧道与执行生命周期](DOOPS_GATEWAY_TUNNEL.md) | Agent 反向连接、执行生命周期和并发边界 | 现行 |
| [Credential 资源](CREDENTIAL_RESOURCES.md) | 凭据绑定、导入、验证和撤销 | 现行 |
| [Skill 集成指南](SKILL_INTEGRATION_GUIDE.md) | 业务 Skill 编写与接入 | 现行 |
| [模型规范](MODEL_STANDARDS.md) | 模型配置及使用规范 | 现行 |
| [自然语言业务发布](NATURAL_LANGUAGE_DEPLOYMENT.md) | 业务发布职责边界与验收原则 | 现行 |
| [WebUI](DOOPS_WEBUI.md) | 控制台能力与页面行为 | 现行 |
| [DoWeb 工作台集成](DOWEB_INTEGRATION.md) | DoWeb 在统一工作台中的接入 | 现行 |
| [统一运维工作台](UNIFIED_OPERATIONS_WORKBENCH.md) | 工作台整体模型与边界 | 现行 |
| [物理机访问](PHYSICAL_HOST_ACCESS.md) | 物理机/宿主机访问与硬件检查 | 环境相关 |
| [Helm 工作区契约](HELM_WORKSPACE_CONTRACT.md) | 受管 Helm 工作区契约与资产源 | 现行契约 |
| [Harbor 镜像同步规范](HARBOR_REPLICATION_SPEC.md) | Harbor 镜像同步配置 | 草案 |
| [DoOps Edge 设计](design/DOOPS_EDGE_TUNNEL.md) | Agent 反向代理与 Gateway 动态域名映射 | 设计（未上线） |

## 阅读顺序

- 首次接入：安装/集群接入 → API 或 CLI 使用 → Credential 资源
- 业务接入：Skill 集成 → 自然语言业务发布
- 部署发布：构建部署 SOP → 集群接入 → 对应环境 README
- Web/工作台：WebUI → DoWeb 集成 → 统一运维工作台

## 文档治理

- 新增或改名文档时必须同步更新本索引和根 README 的文档表。
- 草案必须标注“草案”；未上线设计放 `design/` 并标注状态。
- 内部评审、事故复盘、发布 runbook、evidence 和历史进度不放进本目录；它们属于开发过程记录，应留在仓库外的开发归档中。
