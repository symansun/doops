# Agent 资源同步与目标清单：开发任务书

编制日期 2026-11-22。状态：待评审，未实现。本文是开发任务书，不描述已上线行为；各任务落地时
再更新对应的权威文档（见 §8）。

## 1. 背景

2026-11-22 本地控制台对 `dev-agent` 做巡检，AI 助手整轮报
`doagent_tool_failed: mcp_doops_k8s_read: mcp_tool_error`（三次）。该目标是独立 Docker 容器，
挂了一份指向不可用 API Server 的 kubeconfig。

原因不在这一个目标，而在产品模型：

- Agent 记录只有 `cluster`/`instance`、版本和协议能力开关，没有“目标是什么形态”。
- Agent 与资源的关系全部在 Gateway 的 SQLite 台账里（机器、集群来源、业务系统部署、资源目录、
  凭据绑定），以 `(cluster, instance)` 关联；Agent 本地不存，也拿不到。
- AI 助手的工具清单和系统提示词默认目标是 Kubernetes 集群，执行时只能现场探测和猜测。

## 2. 目标与非目标

### 目标

1. 每个 Agent 有明确的接入模式，并自动上报在目标上观测到的资源。
2. Gateway 把观测事实与台账登记事实合并成一份带版本的**目标清单**，自动同步给 Agent。
3. AI 助手、巡检和其它执行路径在执行时引用同一版清单：工具清单、参数和审计都以它为依据。
4. “目标不管理某类资源”与“该资源出故障”在数据上可区分。

### 非目标

- 不改变授权模型：授权单位仍是 `(user, cluster, instance)`，清单只描述有什么，不决定能不能做。
- 不新增按操作、按资源的权限维度。
- 不在 Agent 侧建数据库，不让 Agent 反向查询 Gateway。
- 不把 `deploy/helm/<name>/` 变成环境注册表或目标选择来源。
- 不改“本轮有工具失败则整轮标为失败”的审计规则。

## 3. 概念

| 概念 | 定义 | 权威方 |
| :--- | :--- | :--- |
| 接入模式 | Agent 进程的运行形态：`kubernetes`（集群内 Pod）、`docker`（宿主机上的独立容器）、`linux`（主机原生进程） | Agent 上报 |
| 观测事实 | Agent 在目标上实际看到的资源和可达性 | Agent |
| 登记事实 | 台账中由人或核验流程维护的归属、名称、负责人、绑定 | Gateway |
| 目标清单 | 某个目标的观测事实与登记事实的合并结果，带版本号和内容哈希 | Gateway 生成 |
| 资源引用名 | 清单中每条资源的稳定标识，执行和审计用它指代资源 | Gateway 生成 |

资源状态共五种：

| 状态 | 条件 |
| :--- | :--- |
| 已核验 | 已登记，且最近一次观测到 |
| 已发现未登记 | 观测到，台账没有 |
| 已登记未核验 | 已登记，从未观测到（含不支持上报的老 Agent） |
| 失联 | 已登记，曾观测到，连续 N 次未观测到 |
| 冲突 | 观测与登记的身份不一致（例如集群 `kube-system` UID 变化） |

## 4. 总体方案

### 4.1 上行：观测上报（Agent → Gateway）

- 连接建立后全量上报一次。
- 之后每次心跳携带观测摘要哈希；哈希变化时发送全量。
- Gateway 可主动请求刷新（控制台“重新同步”、巡检前置）。
- Gateway 按 §3 的状态规则对账；不覆盖人工维护的字段；“已发现未登记”不自动写入人工台账。

### 4.2 下行：清单下发（Gateway → Agent）

- Agent 连接时下发，与现有的连接时模型下发同一时机。
- 该目标的台账、凭据绑定或对账结果变化时推送。
- Agent 心跳携带所持清单版本；Gateway 发现落后即补发。
- Agent 原子写入 `/root/.doops/manifest/current.json`，保留上一版。
- 内容未变不升版本。

### 4.3 执行时引用

- 每轮提问或每次执行开始时，把当前清单复制到会话工作区 `target.json`，本轮固定使用这一版。
- 系统提示词只放摘要（接入模式、各类资源数量与名称、清单版本）；细节由模型用 `Read` 读取。
- AI 工具清单由清单决定：清单中没有可用集群时不提供 `mcp_doops_k8s_read`。
- 审计记录写入所用清单版本与引用到的资源引用名。
- Agent 与 Gateway 断连超过阈值时，清单标为“可能过期”并写入提示词；执行不中断。

### 4.4 清单结构（草案）

```json
{
  "target": {"cluster": "local", "instance": "dev-agent", "mode": "docker"},
  "revision": 12,
  "digest": "sha256:...",
  "generated_at": "2026-11-22T08:00:00Z",
  "resources": [
    {
      "ref": "machine/M-0001",
      "kind": "machine",
      "name": "dev host",
      "state": "verified",
      "source": ["declared", "observed"],
      "observed_at": "2026-11-22T07:59:30Z",
      "access": {"tool": "Bash"}
    }
  ]
}
```

引用名与来源：

| 引用名 | 登记来源 | 观测来源 |
| :--- | :--- | :--- |
| `machine/<asset_id>` | `inventory_machines` | 宿主指纹（`machine_diagnostic_bindings`） |
| `cluster/<asset_id>` | `cluster_inventory_sources` | API 可达性、`kube-system` UID |
| `namespace/<cluster>/<name>` | `cluster_inventory_namespaces` | 命名空间列表 |
| `system/<asset_id>` | `inventory_business_deployments` | 无 |
| `database/<id>`、`site/<id>` | `inventory_resource_catalog` | 第四期扩展 |
| `credential/<name>` | `credential_grants` 中绑定到本目标的凭据 | 无 |

### 4.5 安全边界

- 上报与清单只含元数据。禁止出现凭据载荷、Secret 内容、环境变量值、连接串；机器登录账号只到用户名；
  凭据只有资源名、类型和用途。
- 清单范围限于绑定或分配到本目标的资源。
- 所有新增输出路径必须用合成 canary 断言载荷不出现。

## 5. 存储变更

| 位置 | 变更 |
| :--- | :--- |
| Gateway `agent_status` | 新增列：接入模式、清单版本、清单哈希、观测哈希、最后观测时间 |
| Gateway 新表 `agent_observations` | `(cluster, instance, kind, key)` 主键；详情 JSON、首次观测时间、最后观测时间、连续缺失次数 |
| Agent | `/root/.doops/manifest/current.json`、`previous.json` |

人工台账各表不变。清单是这些表加 `agent_observations` 的汇总结果，不单独持久化内容，只持久化版本与哈希。

## 6. 任务分解

验证等级按仓库 `AGENTS.md` 的 T0/T1/T2 定义。清单涉及凭据名称与载荷边界，标 T2 的任务须补全
Given/When/Then 矩阵。

### 第一期：接入模式与工具门控（根治本次报错）

| 编号 | 任务 | 主要位置 | 验收标准 | 等级 |
| :--- | :--- | :--- | :--- | :--- |
| P1-1 | Agent 判定接入模式与 K8s API 可达性 | `backend/agent/internal/server/target_runtime.go`（已有探测草稿） | 三种模式各有单测；可达性探测有超时与缓存 | T1 |
| P1-2 | 注册与心跳上报接入模式、K8s 可达性 | `tunnel_hub.go`、Agent 侧连接代码 | 新老 Agent 与新老 Gateway 四种组合均可连接；老 Agent 显示“未上报” | T1 |
| P1-3 | Gateway 持久化并经 API 返回接入模式 | `gateway_store.go`、目标列表接口 | 重启 Gateway 后仍可读；离线 Agent 保留最后上报值 | T1 |
| P1-4 | AI 工具清单与提示词由上报结果决定 | `doagent_tools.go`、`handler_ws.go`、`skills/system_prompt.md` | 非 K8s 目标的普通 ask 不含 `mcp_doops_k8s_read`；`--apply` 的 mutation 工具不受影响；独立 Docker 目标巡检不再整轮报错 | T1 |
| P1-5 | 控制台展示接入模式 | 目标选择器、Agent 概览 | 徽标显示 K8s / Docker / Linux / 未上报；浏览器实测 | T1 |

### 第二期：清单生成、下发与引用

| 编号 | 任务 | 主要位置 | 验收标准 | 等级 |
| :--- | :--- | :--- | :--- | :--- |
| P2-1 | 清单汇总查询与版本计算 | Gateway 新文件 | 每类资源有汇总单测；内容不变版本不变；大小有上限，超限时给数量与截断标记 | T2 |
| P2-2 | 清单下发协议与 Agent 原子落盘 | `tunnel_hub.go`、Agent 侧 | 连接时下发；心跳版本落后时补发；写入中断不留半个文件 | T2 |
| P2-3 | 台账变更触发推送 | 机器、集群、业务系统、目录、凭据 grant 的写入路径 | 每条写入路径变更后，在线 Agent 的清单版本更新 | T2 |
| P2-4 | 会话快照与提示词摘要 | `handler_ws.go` | 每轮工作区有 `target.json`；摘要含版本；轮中清单更新不影响进行中的轮次 | T1 |
| P2-5 | 工具清单改由清单决定 | `doagent_tools.go` | 取代 P1-4 中基于探测的判断；清单过期时提示词有标注 | T1 |
| P2-6 | 审计记录清单版本与资源引用名 | 审计写入路径、审计查询接口 | ask、exec、巡检的审计记录可查到清单版本 | T2 |
| P2-7 | 载荷脱敏测试 | 测试 | 合成 canary 覆盖清单生成、下发、落盘、提示词、审计五处，均不出现载荷 | T2 |

### 第三期：对账与控制台

| 编号 | 任务 | 主要位置 | 验收标准 | 等级 |
| :--- | :--- | :--- | :--- | :--- |
| P3-1 | 全量观测上报与 `agent_observations` | Agent、Gateway | 连接时全量、哈希变化时全量、Gateway 可请求刷新 | T1 |
| P3-2 | 五种状态的对账逻辑 | Gateway | 每种状态及状态间迁移有单测；失联阈值可配置 | T1 |
| P3-3 | 控制台“资源清单”卡片 | Agent 概览 | 显示同步状态、版本、最后同步时间、“重新同步”；加载、空、错误、无权限四种状态齐全 | T1 |
| P3-4 | 差异列表与一键登记 | 控制台、台账写入接口 | “已发现未登记”可登记进台账；“失联”“冲突”标红并给出原因 | T1 |
| P3-5 | 导航与提示词模板按清单筛选 | 控制台 | 清单中无集群的目标不显示 K8s、Helm 发布入口与对应模板 | T1 |

### 第四期：扩展观测范围

| 编号 | 任务 | 验收标准 | 等级 |
| :--- | :--- | :--- | :--- |
| P4-1 | 容器引擎与容器列表并入上报 | Docker 目标清单含容器引擎及容器数量 | T1 |
| P4-2 | Nginx 站点并入上报 | 与 `site/<id>` 目录项对账 | T1 |
| P4-3 | 数据库发现并入上报 | 复用现有 `doops_database_inventory` 的发现逻辑，与 `database/<id>` 对账 | T2 |

### 依赖关系

- P1-1 → P1-2 → P1-3 → P1-4、P1-5。
- P2-1 依赖 P1-3；P2-2 → P2-3、P2-4 → P2-5、P2-6；P2-7 贯穿第二期。
- 第三期依赖 P2-1、P2-2；第四期依赖 P3-1、P3-2。

## 7. 兼容与发布

- 协议向后兼容：新增字段均可缺省；老 Agent 的清单只含登记事实，资源状态为“已登记未核验”。
- 第一期可独立发布，不依赖后续各期。
- 表结构变更按现有方式在 `backend/docs/migrations/` 新增 SQL，并纳入安装契约测试。
- 每期发布前执行：Agent 与 CLI 包测试、Python 契约测试、`git diff --check`、编译后 CLI 帮助冒烟；
  控制台改动另做生产构建与浏览器实测。

## 8. 文档更新

| 内容 | 权威文档 |
| :--- | :--- |
| 上报、下发协议字段 | `backend/docs/DOOPS_GATEWAY_TUNNEL.md` |
| 接入模式与安装入口的对应 | `backend/docs/CLUSTER_INSTALL_GUIDE.md` |
| AI 工具清单与提示词行为 | `backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md` |
| 清单与审计查询接口 | `backend/docs/DOOPS_API_GUIDE.md` |
| 控制台卡片、差异列表、导航筛选 | `backend/docs/DOOPS_WEBUI.md` |
| 凭据在清单中的呈现边界 | `backend/docs/CREDENTIAL_RESOURCES.md` |
| 用户可见变更 | `CHANGELOG.md` 的 `## [Unreleased]` |

## 9. 待决事项

以下各项按“建议”一栏作为任务书的默认假设，评审时确认或修改。

| # | 事项 | 建议 | 影响的任务 |
| :--- | :--- | :--- | :--- |
| D1 | “已发现未登记”是否自动写入台账 | 不自动，保留人工确认 | P3-2、P3-4 |
| D2 | 失联判定阈值 | 连续 3 次全量上报未观测到 | P3-2 |
| D3 | 工作区中未提交的资产分配改动（`inventory_agent_assets`）是否作为清单输入 | 是，需与该改动的负责人对齐表结构 | P2-1 |
| D4 | 不支持上报的老 Agent 是否接受“只含登记事实”的清单 | 接受，并在控制台提示升级 | P2-1、P3-3 |
| D5 | `linux` 原生模式是否在第一期支持 | 第一期只识别并展示，不提供安装入口 | P1-1、P1-5 |
| D6 | 清单大小上限 | 256 KiB；超限时命名空间等长列表只给数量和前 50 条 | P2-1 |

## 10. 已有工作

工作区中已有一版未提交的第一期草稿：`target_runtime.go`（现场探测接入形态与 K8s 可达性）、
`doagent_tools.go` 的工具门控、`system_prompt.md` 的巡检分支。它只在 Agent 本地探测，不上报
Gateway，可作为 P1-1 与 P1-4 的起点，P1-2、P1-3、P1-5 尚未开始。
