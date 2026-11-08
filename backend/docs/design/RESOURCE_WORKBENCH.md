# 资源工作台

设计日期 2026-11-02。定稿日期 2026-11-03。状态：**已定稿**。行为以本文为准；在资源页按 §6 于真实目标验收之前，不把它写成已验收。
不要把验收结论写进 `SKILL.md` 或使用指南。`DOOPS_WEBUI.md` 只描述已经落地的页面。

工作区里未提交的数据库页、Shell 页、`components/k8s-panel.tsx` 和 `lib/workbench.ts`
不是本设计的实现，也不要在那三份表单上续写。它们没有进入已发布控制台，也没有在真实目标上验收。

## 0. 现状

已发布控制台（`main` 上的页面，线上 Web 与此一致）没有资源管理，也没有 K8s 页。

| 已发布入口 | 实际做法 | 和资源管理的差距 |
|---|---|---|
| 数据库 | 一段提示词，经 `doops_agent_prompt`（`skill=dosql`）做发现或只读查询 | 没有实例表，不能点一行带出 `database_asset_id` |
| 终端 | 自由命令框，工具是 `doops_shell` | 没有主机信息，没有固定检查 |
| K8s | 无页面 | Agent 已有 `doops_k8s`，控制台没有入口 |
| 资源 | 无页面 | 不存在 |

Agent 上已经能用、本设计直接调用的能力：

| 能力 | 位置 | 本设计怎么用 |
|---|---|---|
| `doops_k8s` | 已发布。`buildK8SCommands` 规定 operation、类型和 `confirm=true` | 集群对象的全部读写 |
| `doops_shell` | 已发布 | 主机上的固定检查和一条显式命令 |
| `doops_node_info` | 已发布 | 打开主机范围时的固定探测 |
| `discoverDatabaseCandidates` | 已发布函数，扫 Pod、Service、Deployment、StatefulSet 的非 Secret 元数据 | 数据库实例的数据来源 |
| `doops_database_inventory` | **未发布**。工作区只有一段未验收的转发 | 见 §5.4，按本文重新接入，不把这段 diff 算作完成 |

本地草稿不可用的原因，写进规格里避免再犯：

1. K8s 草稿是「先选操作、再手填名字」的表单。资源管理要的是先看见对象。
2. 草稿对 Deployment、ConfigMap 使用 `output=json`。打开页面就会把 Pod 模板、env 字面量和 ConfigMap 的 data 值拉进浏览器。
3. 演示模式里，除 Node 以外的 `doops_k8s` 一律返回一个 Pod。页面无法按类型验收。
4. 数据库草稿依赖尚未发布的清单工具。旧 Agent 上这个页面不能成立。
5. 三份草稿各自一套左表单右输出，没有同一个选中对象。

## 1. 问题

目标上要管的东西是对象：工作负载、Pod、Service、配置、存储、节点、Helm 发布、数据库实例、这台主机。
已发布控制台要么没有入口，要么要写一段提示词或一条命令。本地草稿把入口拆成三张互不相通的表单，而且没有交付。

资源工作台是当前目标上这些对象的唯一可视化入口。一个页面，先列表，点一行，再对该行做已经允许的动作。

## 2. 已锁定的前提

这些前提不再讨论替代方案。若要改，先改本文。

| # | 前提 | 影响 |
|---|---|---|
| P1 | 授权只有（用户，集群，实例）。本页不新增权限、配额或凭据维度 | 能打开目标就能用本页；拒绝仍是 Gateway 的 401/403 |
| P2 | 集群对象只走已有的 `doops_k8s`。只新增一个只读 operation `configmap-keys`（2026-11-05 确认，见 §5.8），不新增任何变更 operation | 没有 apply、edit、任意 delete、创建对象、Helm upgrade |
| P3 | 变更只有滚动重启、伸缩、删除 Pod、隔离节点、恢复节点。每条必须 `confirm=true`，且页面上先勾选 | 没勾选就不发请求 |
| P4 | Secret、ServiceAccount、Token 不出现。ConfigMap 和 Helm values 不返回值 | 列表用列投影，不用完整 JSON |
| P5 | 不另存资产库。每次打开向目标现查 | 离开页面即丢，刷新就是再查一次 |
| P6 | 名字、命名空间、类型来自刚查到的那一行。手填只允许容器名、副本数、日志行数；Agent 无权列出命名空间时，允许手填一个命名空间作为列表范围（见 §4.6） | 不提供「自己输入 kind/name」的框 |
| P7 | 数据库变更仍只走 `doops sql --mode apply`。本页没有变更按钮 | 查询固定 `operation=ask` |
| P8 | 数据库、终端和 K8s 草稿入口在资源页落地时直接替换，不双轨保留。文件、网站、凭据、审计、AI 助手不动 | 2026-11-03 定稿时确认前端可以整页替换 |
| P9 | 本地三份草稿不合并为产品。实现时按本文重写页面；草稿里可复用的只有解析函数，且要按 §5.3 改 | 不要在 `k8s-panel.tsx` 上加一个标签 |

## 3. 目标与非目标

### 目标

| # | 目标 |
|---|---|
| G1 | 选中目标后，一个「资源」页列出该目标上的对象 |
| G2 | 点一行只选中，不自动发第二次请求 |
| G3 | 动作随对象类型出现或消失，不能对这一行做的按钮不渲染 |
| G4 | 列表响应里没有 Pod 模板、env、ConfigMap data、Helm values、Secret |
| G5 | 变更失败或刷新失败分开显示，不把一次失败说成另一次的结果 |
| G6 | 旧 Agent 没有数据库清单工具时，数据库范围说明要升级，其余范围仍可用 |
| G7 | 加载、空、错误分开。离开页面中止未完成的请求 |

### 非目标

- 不做 kubectl apply、edit、删除 Deployment、创建任何对象。
- 不做 Helm upgrade、install、rollback。发布仍走 `ask --apply`。
- 第一期不浏览 Role、RoleBinding、ClusterRole、CRD、Lease、Endpoint、EndpointSlice、NetworkPolicy、HPA、PDB、ResourceQuota、LimitRange、IngressClass、PriorityClass。工具读得了，但不是本页的目录。
- 事件不做目录，只作为选中对象的一个动作。
- 不把文件、网站、凭据、审计、AI 助手并进这个页面。
- 不在本页吊销凭据，不展示凭据 payload。
- `top` 不能指定单个 Pod，所以不做「这一行的用量」按钮。

## 4. 规格

### 4.1 页面

宽屏三列，窄屏按「范围、对象、动作」上下排列。

| 列 | 内容 |
|---|---|
| 范围 | 三个域：集群、数据库、主机。集群域下列出类型分组；上面是命名空间，含「全部」 |
| 对象 | 当前范围的表。点一行选中，再次点击同一行不重复请求 |
| 动作 | 只渲染当前行合法的按钮。按钮下面是这一次的输出 |

打开「资源」时：

1. 域是集群，类型是 Deployment，命名空间是「全部」。
2. 并行两条只读请求：命名空间列表，以及全部命名空间的 Deployment。两条都不锁标签。
3. 切换目标、session 或离开页面时，中止这两条和任何未完成的动作。

概览里当前机器的卡片增加「资源」入口，与数据库、终端草稿无关。主标签放在「数据库」和「终端」之间，标签文案是「资源」。

### 4.2 集群对象

类型分组写死，不从集群发现。

| 分组 | 类型 | 命名空间 |
|---|---|---|
| 工作负载 | Deployment、StatefulSet、DaemonSet、Job、CronJob | 要 |
| 实例 | Pod | 要 |
| 网络 | Service、Ingress | 要 |
| 配置 | ConfigMap | 要 |
| 存储 | PVC、PV、StorageClass | PV、StorageClass 不要 |
| 集群 | Node、Namespace | 不要 |
| 发布 | Helm release | 列表可跨命名空间。历史、状态、键名必须有命名空间 |

集群级类型忽略左侧选中的命名空间。请求里既不带 `namespace`，也不带 `all_namespaces`。这两个字段同时出现时，工具直接拒绝。

切换命名空间或类型时，中止上一次列表请求，清空选中行和上一次动作输出，再查新列表。查列表不锁标签。

列只来自投影，不来自完整对象。空值在表格里显示为「—」，不显示 `<none>`。

| 类型 | 列 |
|---|---|
| Deployment、StatefulSet、DaemonSet | 命名空间、名称、期望、就绪、可用 |
| Job | 命名空间、名称、完成数、成功数 |
| CronJob | 命名空间、名称、调度、是否暂停 |
| Pod | 命名空间、名称、阶段、就绪、节点 |
| Service | 命名空间、名称、类型、ClusterIP、端口 |
| Ingress | 命名空间、名称、类、主机 |
| ConfigMap | 命名空间、名称、键名（`data` 和 `binaryData` 的键，逗号分隔）。不显示值，来源见 §5.8 |
| PVC | 命名空间、名称、状态、容量、存储类 |
| PV | 名称、状态、容量、绑定的 PVC |
| Node | 名称、Ready、角色、内网 IP、Kubelet |
| Namespace | 名称、状态 |
| Helm | 命名空间、发布、修订、状态、Chart |

ConfigMap 没有「详情」。`describe` 和 `get -o json` 都会把 data 的值打出来。

解析成功就只保留表格。kubectl 或 helm 的报错不是表格时，保留错误原文。不把原始 JSON 留在页面状态里。

### 4.3 对选中行的动作

读动作是用户点的，锁标签。列表刷新不锁。

| 按钮 | operation | 何时出现 |
|---|---|---|
| 详情 | `describe` | 除 ConfigMap 外的已选对象 |
| 事件 | `events`，`resource` 为 `kind/name` | 带命名空间的对象 |
| 日志 | `logs`，默认 200 行 | Pod、Deployment、StatefulSet、DaemonSet、Job。容器名可填，不填则不传 |
| 发布状态 | `rollout-status` | Deployment、StatefulSet、DaemonSet |
| 看 Pod | 先取 `.spec.selector`，转成标签选择器，再 `get pods --selector` | Deployment、StatefulSet、DaemonSet。apps/v1 要求这三类都有选择器，所以按钮常在；选择器不能原样写出时不发 Pod 请求，不猜标签 |
| 滚动重启 | `rollout-restart` | 同上三类 |
| 伸缩 | `scale` | Deployment、StatefulSet。副本数是 0 到 100 的整数 |
| 删除 Pod | `delete-pod` | 仅 Pod |
| 隔离节点 | `cordon` | 仅 Node |
| 恢复节点 | `uncordon` | 仅 Node |
| Helm 历史、状态、键名 | `helm-history`、`helm-status`、`helm-values` | 仅发布行，且该行有命名空间 |

点变更按钮只选中这个动作，不发请求。下面出现警告条，写明目标（`cluster/instance`）、对象（类型、命名空间、名称）和动作；伸缩还写明新副本数。勾选「我确认执行这一次变更」后，「执行」才可用。请求只在动作与警告条一致且已勾选时发出，因此没勾选就不会带 `confirm=true`。改副本数、换动作、换行或点「取消」都会清掉勾选。请求结束后勾选和选中的动作一并清掉。

变更的输出留在动作列。变更完成后刷新当前列表一次。刷新失败单独显示，不改写变更已经返回的输出，也不把变更说成失败。

停止：执行中可以停止。停止后等待这次调用结束，不自动重试。

日志行数是输入框，默认 200，只允许 1 到 1000 的整数。工具允许更大的 tail，页面不暴露。副本数只允许 0 到 100。工具允许到 10000，页面不暴露。超出范围时在提交前拦住，不发请求。

### 4.4 数据库

数据库是同一页的一个域，不是另一张表单。

打开该域时调用 `doops_database_inventory`，无业务参数。表的列是引擎、命名空间、工作负载、端口、库名、状态。可按引擎过滤：全部、mysql、postgres、mongodb、mssql。发现函数不识别 oracle，过滤里不要放 oracle。

点一行只选中该实例，并填入该行已有的 `database_name`。没有库名的行不能查询。用户要自己填写库名，页面不猜测，也不用别的行补全。

查询按钮始终发送：

- 工具 `doops_agent_prompt`
- `skill=dosql`
- `operation=ask`
- `credential_mode=auto`
- `sql` 为用户输入的语句
- `sql_database` 为填写的库名

不发送 `database_asset_id`。`doops_agent_prompt` 的参数里没有这个字段，Gateway 和 Agent 解码时会直接丢掉，传了也不起作用。使用哪个凭据由 Gateway 按目标上绑定的数据库凭据和库名决定，与 `doops sql` 相同。

页面上没有 apply、没有「执行变更」。写入和 DDL 由 Agent 在非 apply 时拒绝；即使 Agent 没拒绝，页面也不提供这条路。

结果能解析成列和行就画表，并显示引擎、库名、凭据资源名。不能解析就保留原文。凭据值不出现。

清单工具不存在或返回 `dosql_discovery_unavailable` 时，数据库域显示「当前 Agent 还没有数据库清单，需要升级 Agent」。不清空成一张空白表，也不把错误文本解析成实例。查询区在没有实例时不可用。

扫描不读 Secret，结果不写入第二份资产库。

### 4.5 主机

主机是同一页的一个域，对象只有当前目标这一台。

打开时调用 `doops_node_info`。按返回的分节显示：系统、负载、内存、磁盘、工具链。没有这些标题时，整段作为「输出」。

五个检查是固定命令，不是用户拼出来的：

| 按钮 | 命令 |
|---|---|
| 运行状态 | `uptime` |
| 内存 | `free -m` |
| 磁盘 | `df -h` |
| 进程 | `ps -eo pid,user,pcpu,pmem,comm --sort=-pcpu \| head -n 20` |
| 监听端口 | `ss -lnt` |

另外保留一个命令输入框。这是主机域仅有的自由输入。不把重启、删文件做成按钮。命令走 `doops_shell`，与今天的终端相同，进入同一条审计。

### 4.6 状态

| 状态 | 表现 |
|---|---|
| 第一次列对象 | 对象区显示「正在读取」，没有表 |
| 刷新列表 | 留下当前表，并标明正在刷新。失败则保留这张表，错误单独显示 |
| 没有对象 | 「这个范围里没有对象」，不是空白 |
| 换了范围或类型 | 清掉上一张表和选中行，再显示「正在读取」 |
| 动作失败 | 只显示在动作区，不清空对象表 |
| 目标正在独占变更 | 只读请求返回 target busy。显示该错误，不自动排队或重试 |
| 旧 Agent 无清单工具 | 只影响数据库域 |
| Agent 不能列出命名空间（kubectl 返回 Forbidden） | 命名空间列表下出现一个输入框，只接受合法的命名空间名。填写后按该命名空间列对象。页面不替用户挑命名空间 |
| 「全部」被拒（ServiceAccount 只有单个命名空间的权限） | 列表区显示 kubectl 的原始错误，并提示选择或填写一个命名空间。不自动改用别的命名空间 |
| 工具失败 | 错误里带上工具在失败前输出的原因（如 kubectl stderr），不只显示 `exit status 1` |

会改状态的动作锁住控制台标签。自动打开时的列表、手动刷新列表，不锁。

## 5. 详细设计

### 5.1 调用链

浏览器不直连 Agent。页面经现有 `/api/rpc`，Gateway 用当前登录身份校验目标授权、记审计，再转发到已连接的 Agent。本页不新增 Gateway 路由。

| 页面动作 | 工具 | 审计动作 | 并发 |
|---|---|---|---|
| 集群列表、详情、日志、事件、发布状态、Helm 读 | `doops_k8s` 的只读 operation | exec。资源键是 `query:k8s:`，但租约走只读计数，不按这个键互斥 | 可与其他只读并行。目标上已有 `target:` 或 `target-mutation:` 时直接 target busy |
| 滚动重启、伸缩、删除 Pod、隔离、恢复 | 同一工具的变更 operation | exec | `target-mutation:`，独占该目标，且 `confirm=true` |
| 数据库清单 | `doops_database_inventory` | info | 只读计数 |
| 只读 SQL | `doops_agent_prompt`，`skill=dosql` 且 `credential_mode=auto` | ask | 已有逻辑返回 `target-mutation:`，独占该目标 |
| 主机信息 | `doops_node_info` | info | 只读计数 |
| 主机命令 | `doops_shell` | exec | `target-mutation:`，独占该目标 |

不为本页修改动作枚举。只读的 `doops_k8s` 今天就是 exec，靠 operation 区分。两个列表请求可以同时发出，不要在页面里排成串行。

### 5.2 列表请求

所有集群列表都带 `session_id`。命名空间来自「全部」时，带命名空间的类型使用 `all_namespaces=true`，不带 `namespace`。选定一个命名空间时只带 `namespace`，不带 `all_namespaces`。

投影使用 `output=custom-columns=...` 或 `output=jsonpath=...`。这是 `doops_k8s` 已经允许的 output。不要用 `json` 或 `yaml` 拉列表。

| 类型 | output |
|---|---|
| Deployment、StatefulSet | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,DESIRED:.spec.replicas,READY:.status.readyReplicas,AVAILABLE:.status.availableReplicas` |
| DaemonSet | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,DESIRED:.status.desiredNumberScheduled,READY:.status.numberReady,AVAILABLE:.status.numberAvailable`。DaemonSet 没有 `spec.replicas` |
| Job | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,COMPLETIONS:.spec.completions,SUCCEEDED:.status.succeeded` |
| CronJob | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,SCHEDULE:.spec.schedule,SUSPEND:.spec.suspend` |
| Pod | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,READY:.status.containerStatuses[*].ready,PHASE:.status.phase,NODE:.spec.nodeName` |
| Service | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,TYPE:.spec.type,CLUSTER-IP:.spec.clusterIP,PORTS:.spec.ports[*].port` |
| Ingress | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,CLASS:.spec.ingressClassName,HOSTS:.spec.rules[*].host` |
| PVC | `custom-columns=NS:.metadata.namespace,NAME:.metadata.name,STATUS:.status.phase,CAPACITY:.status.capacity.storage,CLASS:.spec.storageClassName` |
| PV | `custom-columns=NAME:.metadata.name,STATUS:.status.phase,CAPACITY:.spec.capacity.storage,CLAIM:.spec.claimRef.name` |
| Namespace | `custom-columns=NAME:.metadata.name,STATUS:.status.phase` |
| StorageClass | `custom-columns=NAME:.metadata.name,PROVISIONER:.provisioner`。集群级，列是名称和制备器 |
| ConfigMap | 不用 `get`。`operation=configmap-keys`，带同样的 `namespace` 或 `all_namespaces`，不带 `output`。见 §5.8 |
| Node | `json`。Node 对象不含凭据，沿用现有节点表解析。不要改去请求 allocatable 或镜像列表以外的字段；若实现时发现 JSON 过大，再改成 custom-columns，不在本期扩大字段 |
| Helm 列表 | `helm-list`，已是 JSON，字段只有发布元数据 |

`custom-columns` 的第一行是表头，之后每行用两个以上空格分列。kubectl 的空值是 `<none>`，解析成空，再显示为「—」。选定的字段里没有连续空格，不要写一个通用的空白折叠器去猜别的输出。

kubectl 的 jsonpath 不能遍历 map 的键。`{range $k,$v := .data}` 在解析阶段就报 `unrecognized character in action: U+002C ','`；`{.data}`、`{.data.*}` 和 `custom-columns=KEYS:.data` 都会带出值。因此键名不能靠页面上的投影取，由 Agent 的 `configmap-keys` 在服务端丢掉值后再返回（§5.8），与 `helm-values` 只回键路径同一做法。不要对 ConfigMap 调用 `describe`。

「看 Pod」的选择器来自第二次只读请求，不是来自列表投影：

`get` 单个 Deployment、StatefulSet 或 DaemonSet，`output` 为 `jsonpath={.spec.selector}`。kubectl 把它打印成一行 JSON，例如 `{"matchLabels":{"app":"orders"}}`。

页面把这段 JSON 原样转成标签选择器：

| 选择器里的项 | 写成 |
|---|---|
| `matchLabels` 的 `k: v` | `k=v` |
| `In` | `key in (a,b)` |
| `NotIn` | `key notin (a,b)` |
| `Exists` | `key` |
| `DoesNotExist` | `!key` |

各项用逗号连接。键或值不是合法标签、操作符不认识、`In`/`NotIn` 没有值、`Exists`/`DoesNotExist` 带了值、结果为空，或结果不匹配 `doops_k8s` 的选择器字符集时，整次不发 Pod 请求，动作区说明原因。不丢掉一部分条件再去查，那样会查出更多 Pod。随后的 Pod 列表用上面的 Pod 投影，并带 `selector`。

2026-11-04 在 kubectl v1.34.1 上核对：`k8s-app=kube-dns` 和 `k8s-app in (kube-dns,x),!legacy` 都只列出 coredns 的两个 Pod。

详情、日志、事件、发布状态和全部变更的参数与今天的 `K8SParams` 相同：`kind` 与 `name`，或 `resource=kind/name`，加上该行的 `namespace`。页面不传 `output=json`。

Helm 历史、状态、键名把该行的发布名放到 `name`，命名空间放到 `namespace`。键名继续由 Agent 把 values 收成键路径，页面不要再解析 values 正文。

`helm status -o json` 原样会带出整份 release：`config`（用户 values）、`manifest`（渲染后的对象，可能含 Secret 的 `data`/`stringData`）、`hooks` 和 `info.notes`。2026-11-05 在测试目标上实测，一个发布的 status 有约 100 KB，其中 `config` 有 18 个像令牌或密码的键。因此 Agent 的 `helm-status` 只回 `name`、`namespace`、`version` 和 `info` 里的 `status`、`description`、`first_deployed`、`last_deployed`、`deleted`，`description` 再过一遍脱敏。JSON 解析失败时报错不引用输入。

### 5.3 前端模块

新页面 `components/resource-panel.tsx`。参数与现有面板相同：`session`、`target`、`sessionId`、`onRunningChange`。

调用经 `lib/workbench.ts` 的 `runTargetTool`。不在页面里再写一条 WebSocket。

`lib/workbench.ts` 可以留下并改写，但列表解析按 §5.2 重做。现有的 `k8sTable` 吃完整 JSON，不能用来解析本页的列表。Pod、Node、Helm 的现有解析只在输入确实是对应 JSON 时使用。Node 列表可以继续用现有节点解析。

页面状态：

- `scope`：`cluster`、`database`、`host`
- `kind`、`namespace`、`allNamespaces`
- `rows`：只含表格要画的字符串，外加选择一行所需的 `name`、`namespace`、`kind`
- `selected`：上面三个字段，或数据库的 `database_asset_id`
- `actionOutput`、`actionError`、`listError`
- 一个 `AbortController` 管当前列表，另一个管当前动作。新请求替换旧的并 abort。

不要把 `custom-columns` 原文、jsonpath 原文或完整对象放进 React state。

`components/console-shell.tsx` 增加标签 `resources`，文案「资源」。`key` 带上目标的 cluster、instance 和 `sessionId`，换目标即卸载页面。

`components/dashboard-panel.tsx` 增加入口。不从草稿的三张快捷卡片抄描述。

演示模式 `lib/demo.ts` 按 `operation` 和 `kind` 返回与 §5.2 相同形状的文本。变更在 `confirm !== true` 时返回 `confirm=true is required`。`confirm === true` 时说明「演示模式已接受变更请求，没有改动真实集群」。禁止再把所有类型都返回成一个 Pod。

数据库查询的演示结果保持现有合成行，不含凭据值。

### 5.4 Agent：清单工具

`discoverDatabaseCandidates` 已经存在。缺的是公开工具名。

在已发布的工具分发处增加 `doops_database_inventory`：

- 无业务参数。`session_id` 由 Gateway 调用路径原样带上，函数本身不读它。
- 成功时把 `discoverDatabaseCandidates` 的 JSON 原样返回。
- 失败时返回该函数的错误。没有 kubectl 时就是 `dosql_discovery_unavailable`。
- 不读 Secret，不写文件，不调用模型。

Gateway 把它标成 info、只读，与 `doops_node_info` 同一类。不要新的资源键，不要新的授权判断。

工作区里已有的十行转发没有测试，不算完成。要补的测试：

- 工具名分发到发现函数，返回的 JSON 含 `candidates`，且不含 Secret 字面量。
- kubectl 不可用时错误是 `dosql_discovery_unavailable`。
- Gateway 将该工具视为只读 info，不要求 `confirm`。

不修改发现函数的识别规则、资产 ID 算法或「缺库名则保持该状态」的行为。

### 5.5 草稿怎么处理

实现时：

- 不接 `components/k8s-panel.tsx`、`components/database-panel.tsx`、`components/terminal-panel.tsx`。资源页接上控制台后删除这三份草稿。
- `lib/workbench.ts` 按 §5.3 改，不保留「get -o json 再猜表格」作为列表路径。

### 5.6 实现顺序

1. 本文已于 2026-11-03 定稿。
2. Agent 清单工具的分发和 §5.4 的测试。
3. 集群域：投影、表、选中、只读动作、变更确认。
4. 数据库域、主机域。
5. 标签、概览入口、演示数据。
6. 按 §6 在真实目标上看一遍。页面落地时就把 `DOOPS_WEBUI.md` 改成描述资源页，不再描述已删除的三个入口。验收结论另计，不提前写成已验收。

边界没有变，不改 `UNIFIED_OPERATIONS_WORKBENCH.md`，不改 `SKILL.md`。

### 5.7 文档落地时要写的那一段

仅在第 6 步之后写入 `DOOPS_WEBUI.md`，替换而不是并列：

资源页用 `doops_k8s` 按列投影列出对象，点一行再做详情、日志、事件、发布状态、看 Pod，以及滚动重启、伸缩、删除 Pod、隔离或恢复节点。变更要勾选并带 `confirm=true`。ConfigMap 通过 Agent 的 `configmap-keys` 只显示键名。Secret 和 ServiceAccount 不在目录中。数据库域调用 `doops_database_inventory`，查询走 `doops_agent_prompt`（`skill=dosql`，`operation=ask`）。主机域调用 `doops_node_info` 和 `doops_shell`。

### 5.8 Agent：ConfigMap 键名

`doops_k8s` 增加只读 operation `configmap-keys`。它与 `helm-values` 同一做法：Agent 在服务端拿到完整对象，丢掉值，只把键名交给调用方。

请求：

- 可带 `namespace` 或 `all_namespaces`（互斥），可带 `name` 或 `selector`。
- 不接受 `output`。输出格式固定，调用方不能换成 `json` 或 `yaml`。
- Secret 的禁止规则不变；这个 operation 只读 `configmaps`，不接受 `kind`。

执行：

- Agent 运行 `kubectl get configmaps ... --output json`，解码时 `data` 和 `binaryData` 的值只保留为原始字节，随后丢弃。
- 输出三列表：`NAMESPACE`、`NAME`、`KEYS`。键名按字典序，逗号分隔，包含 `data` 和 `binaryData` 的键。没有键时为 `<none>`。ConfigMap 键名不能含逗号和空格，所以逗号分隔不会有歧义。
- 读入的 JSON 上限 64 MiB。超过时停止 kubectl，报错要求缩小到一个命名空间或加选择器，不截断成半张表。
- kubectl 失败时只返回经 `sanitizeSecretText` 处理的 stderr。JSON 解码失败时报固定文案，不引用输入，因为输入里有值。对象缺名称时整次失败，不输出表。

分类：

- `gatewayK8SReadOnly` 把它算作只读，与其他读操作一样不占变更租约，也不要求 `confirm`。
- doagent 的 `k8s_read` MCP 工具枚举里加入它，与 `events`、`helm-list` 同一条「只需 operation」的 schema 分支。

页面：

- ConfigMap 列表发送 `operation=configmap-keys`，按 §5.2 的对齐列解析，列是命名空间、名称、键名。
- 旧 Agent 返回 `unsupported k8s operation "configmap-keys"` 时，只在 ConfigMap 列表上提示升级 Agent，不退回 `get` 投影，也不改用 `describe`。

测试覆盖：请求参数与只读分类；列表、单个对象、空列表；输出不含合成 canary 值；kubectl 失败、JSON 损坏、缺名称、超过上限时报错不含值。

## 6. 验收

目标使用已授权的测试目标。不碰业务凭据，不吊销任何凭据。

| # | Given | When | Then |
|---|---|---|---|
| A1 | 已登录且有该目标授权 | 打开「资源」 | 只走当前 session 和 `doops_k8s`；第一屏是命名空间和全部命名空间的 Deployment |
| A2 | A1 的响应 | 查看列表内容 | 没有 Pod 模板、env、`data:` 值 |
| A3 | 选中一个 Deployment | 不勾选确认，点滚动重启 | 没有发出请求，审计里没有这次变更 |
| A4 | 勾选确认后滚动重启 | 等待结束 | 动作区留下这次输出；列表再查一次；刷新失败时变更输出还在 |
| A5 | 切到 ConfigMap | 列表返回 | 请求是 `operation=configmap-keys`；表里有键名；响应里没有 data 和 binaryData 的值；没有详情按钮。Agent 未升级时提示升级，不退回别的投影 |
| A6 | 左侧选了某个命名空间 | 改看 Node | 请求不带 `namespace`，也不带 `all_namespaces` |
| A7 | 列表请求未完成 | 离开页面或切换目标 | 请求被中止，不把半截结果画成表 |
| A8 | 选中 Deployment，点「看 Pod」 | 看请求和结果 | 先读 `.spec.selector`，再带转出的选择器查 Pod；只列出该工作负载的 Pod。选择器不能原样写出时，没有发出 Pod 请求 |
| A9 | Agent 没有清单工具 | 打开数据库域 | 明确提示升级；集群域和主机域仍可列出 |
| A10 | 实例没有 `database_name` | 不填写库名就查询 | 不发查询 |
| A11 | 填写库名后查询 | 看请求 | `operation=ask`，`skill=dosql`；页面上没有变更按钮 |
| A12 | 打开主机域 | 点「磁盘」 | 命令就是 `df -h`，走 `doops_shell` |
| A13 | 演示模式 | 列出 Deployment，再对 Pod 做删除且已确认 | 列表不是一个假 Pod；变更文案写明没有改动真实集群 |
| A14 | 副本数填 101，或日志行数填 0 | 点对应按钮 | 提交前被拦住，不发请求 |

A1 到 A8、A12 用真实目标做。A9 可以用未升级的 Agent，或在测试里模拟未知工具。A13 是本地演示，不能代替 A1。
