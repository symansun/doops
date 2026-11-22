# 超大文件拆分设计（2026-11-22）

| 项 | 内容 |
| --- | --- |
| 状态 | 设计稿。阶段一（纯搬运）已在仓库副本上完整执行并通过全部测试；阶段二、三只有设计，未执行 |
| 验证基线 | `release/2026-11-22` @ `55e72e6e` 加当时 14 个未提交文件（17:01 快照）；17:26 在 `6c66c0bb` 上复核 |
| 范围 | `backend/agent/internal/server`、`backend/cli/cli/doops`、控制台的 5 个大文件 |
| 不在范围 | 拆包（Gateway 与 Agent 分成两个 Go 包）、DoSql 的 `.mjs` 脚本、Python 脚本 |
| 关联 | [深度评审](../../backend/docs/reviews/2026-11-22-doops-deep-review.md) 的 AG-01、AG-02、AG-03、CLI-01、CLI-03、WEB-07 |

真实工作区没有被改动。本设计新增的只有本文件和被 `.gitignore` 忽略的 `.local/file-split/`（搬运工具与计划文件）。

## 1. 结论

超大文件分三步处理，三步的风险和验证方式不同，不能混在一起做。

1. **阶段一，纯搬运**：只移动顶层声明，不改任何函数体。15 个 Go 源文件（`internal/server` 12 个、CLI 3 个）和 2 个测试文件，
   新增 86 个文件（源文件 62 个、测试文件 24 个）。
   已在副本上执行：构建、`go vet`、三个模块的全部 Go 测试、486 项契约测试全部通过，测试函数集合前后一致。
2. **阶段二，拆巨型函数**：阶段一之后仍有 6 个超过 300 行的函数，需要改代码结构。每次只动一个函数。
3. **阶段三，Web**：5 个文件。必须和依赖源码路径的测试一起改。

阶段一做完后，Go 代码里最大的非测试文件从 3165 行降到 766 行（不含 CLI 的 `main.go`，它属于阶段二）。

## 2. 方法论

### 2.1 先判断文件为什么大

| 成因 | 判断方法 | 处理 |
| --- | --- | --- |
| 多个主题挤在一起 | 顶层符号能按"谁会来改它"分成互不相干的几组 | 阶段一：按主题搬到各自文件 |
| 单个函数过大 | 一个函数占文件三分之一以上 | 阶段二：先搬到自己的文件，再按阶段拆函数 |
| 内容不是代码 | 大段脚本文本、建表 SQL、演示数据 | 阶段一：单独成文件，不再细拆 |
| 测试兜底文件 | 测试名的前缀对应多个被测主题 | 阶段一：测试跟着被测文件走，公共辅助单独成文件 |

行数只是线索。`credential_materializer.go` 有 1217 行但主题单一，拆它是为了把"主流程"和"探测、Secret 操作"分开，不是为了凑行数。

### 2.2 搬运和重构分开

- **搬运**的正确性可以机器证明：声明原文不变，只是换了文件。评审不需要读 diff。
- **重构**的正确性只能靠测试和人工评审。

两者放进同一个提交，机器证明就失效了。所以阶段一的提交里不允许出现任何函数体改动，连重命名也不做。

### 2.3 一个符号该放哪：按顺序判断

1. **只有一个主题用它**：跟使用者走。例：`requestIDFromNotification` 只被转发逻辑调用，随 `relayToolCall` 进 `agent_relay.go`。
2. **已有文件就是这个概念的归属**：追加到那个文件，不新建。例：`contextMutex` 只为凭据锁服务，追加到 `credential_lock.go`；
   `writeSessionCommandAudit` 追加到已有的 `audit_log.go`。
3. **Gateway 和 Agent 两侧都用**：放进不带 `gateway_`、`agent_` 前缀的中性文件。例：`jsonrpc_response.go`、`redaction.go`、`ws_transport.go`。
4. **以上都不是**：按 2.4 的命名规则新建文件。

规则 3 的附带收益：两侧共用的代码被显式标出来，将来拆包时这些文件就是共享包的内容。

### 2.4 命名规则

文件名是 `<领域>_<职责>.go`。领域沿用包内 [README](../../backend/agent/internal/server/README.md) 的前缀索引，职责用固定词汇：

| 后缀 | 含义 | 例 |
| --- | --- | --- |
| `_http` | HTTP 处理函数 | `auth_http.go`、`audit_http.go` |
| `_store` / `_store_<表>` | SQLite 读写 | `gateway_store_tokens.go` |
| `_session`、`_relay`、`_leases` | 一条连接的生命周期、转发、并发准入 | `agent_session.go` |
| `_request`、`_job`、`_cluster` | 一个流程的输入、执行体、环境读取 | `self_upgrade_job.go` |
| `_testkit_test` | 多个测试文件共用的辅助 | `gateway_testkit_test.go` |

测试文件与被测文件同名：`agent_leases.go` 对应 `agent_leases_test.go`。

并行开发已经建了 `tool_dispatch.go`、`tool_handlers_*.go`、`tool_registry.go`、`credential_types.go`、`credential_store_grants.go` 等文件，本设计沿用这些名字，不另起一套。

### 2.5 预算

| 对象 | 目标 | 说明 |
| --- | --- | --- |
| 非测试源文件 | 600 行以内 | 脚本文本、建表 SQL 这类非代码文件除外 |
| 测试文件 | 1000 行以内 | |
| 函数 | 150 行以内 | |

预算用棘轮执行（第 6 节）：现有超限的列入白名单并记下当前值，只许降不许升。不要求一次达标。

### 2.6 阶段一的验收不变量

一次搬运必须同时满足五条，缺一条就不是纯搬运：

1. `go build ./...` 和 `go vet ./...` 通过。
2. 测试函数集合前后完全相同（`go test -list`）。
3. 声明行多重集相同：把目录下所有 `.go` 文件去掉 `package`、`import` 和空行后排序，前后比较，差异只允许是声明块的外壳（`const (`、`var (`、`)`）。
4. 全部 Go 测试通过。
5. 契约测试通过。

第 3 条是关键。它证明没有任何一行函数体、注释或声明被改动或丢失。附录 A 给出了检查脚本。

## 3. 阶段一：纯搬运

### 3.1 工具和计划文件

搬运由一个小工具按计划文件执行，位于 `.local/file-split/`（被忽略目录，未纳入版本库）：

| 文件 | 作用 |
| --- | --- |
| `mover.go` | 按计划移动顶层声明。连同文档注释一起移动；拆开分组的 `const`/`var` 块；按实际引用重算 import；最后 `gofmt` |
| `lister.go` | 列出文件的全部顶层符号及行数，用来写计划和核对 |
| `server.plan` | `internal/server` 源文件的计划，70 行 |
| `cli.plan` | CLI 源文件的计划，10 行 |
| `tests-1.plan`、`tests-2.plan` | 两个大测试文件的计划 |

计划文件每行是 `源文件 目标文件 符号...`。目标前加 `+` 表示追加到已有文件；符号以 `*` 结尾表示前缀匹配。

工具在两种情况下拒绝执行，不会做一半：

- 计划里的符号在源文件中不存在。16:55 就触发过一次：并行开发把 `credential_store.go` 的类型移走了，工具报出缺失的符号，我据此更新了计划。
- 目标文件已存在却没有标 `+`。

这个工具是为本次设计写的，只处理过本包出现的代码形态。被搬运的文件里没有 `iota`、cgo、构建标签和 `init()`，这些形态没有验证过。

### 3.2 结果

| 文件 | 拆分前 | 拆分后 |
| --- | ---: | ---: |
| `tunnel_hub.go` | 3165 | 220 |
| `handler_ws.go` | 2956 | 439 |
| `workspace_upload.go` | 1304 | 475 |
| `gateway_store.go` | 1267 | 140 |
| `credential_materializer.go` | 1217 | 546 |
| `credential_runtime.go` | 1191 | 443 |
| `self_upgrade.go` | 1170 | 561 |
| `doagent_tool_binding.go` | 965 | 445 |
| `credential_store.go` | 881 | 232 |
| `credential_http.go` | 821 | 689 |
| `gateway_protocol_test.go` | 3558 | 删除 |
| `handler_ws_test.go` | 2712 | 286 |
| CLI `push.go` | 1348 | 379 |
| CLI `client.go` | 1068 | 277 |
| CLI `main.go` | 1349 | 1118（阶段二处理） |

文件数：`internal/server` 非测试 163 → 216，测试 196 → 219；CLI 非测试 39 → 48。

`internal/server` 非测试文件的行数分布：

| | 600 行以上 | 401–600 | 201–400 | 200 以下 |
| --- | ---: | ---: | ---: | ---: |
| 拆分前 | 14 | 11 | 55 | 83 |
| 拆分后 | 6 | 24 | 75 | 111 |

拆分后仍超过 600 行的 6 个：`self_upgrade_scripts.go`（766，脚本文本）、`dosql_mongo.go`（715）、`cos_storage.go`（708）、
`credential_http.go`（689）、`client_tool_call.go`（650，阶段二）、`dosql_tool.go`（649）。

下面各表里，"侧"表示运行在哪个进程：G 是 Gateway，A 是 Agent，G+A 是两侧共用。

### 3.3 `tunnel_hub.go` 拆出的文件

| 文件 | 行数 | 侧 | 含义 | 主要内容 |
| --- | ---: | --- | --- | --- |
| `tunnel_hub.go`（保留） | 220 | G | Gateway 的组装根 | `GatewayHub`、`NewGatewayHub`、`RegisterRoutes`、`HandleTargets`、转发到注册表和客户端服务的薄方法、`tunnelKey` |
| `auth_http.go` | 252 | G | 用户认证的 HTTP 面 | `HandleAuthLogin`、`HandleAuthMe`、`authenticateGatewayUser`、`verifyAndRenewUserToken`、`bearerToken`、认证失败的错误码与响应 |
| `admin_tokens_http.go` | 101 | G | 管理员的 token 接口 | `/v1/admin/tokens` 的列出、签发、吊销 |
| `audit_http.go` | 119 | G | 审计的 HTTP 面 | `HandleAudit`（查询与清理）、`auditRecord` |
| `http_response.go` | 28 | G | 所有 HTTP 处理函数共用的响应写法 | `writeJSONHTTP`、`writeDoopsError`、`doopsErrorResponse` |
| `client_rpc.go` | 225 | G | 用户侧 WebSocket 入口 | `HandleClientRPC`：`initialize`、`tools/call`、`tools/cancel` 的消息循环 |
| `client_tool_call.go` | 650 | G | 一次工具调用在 Gateway 的全过程 | `handleGatewayToolCall` 及其审计文本辅助 |
| `agent_session.go` | 479 | G | Gateway 持有的一条 Agent 连接 | `GatewayAgent`、`GatewayTarget`、初始化握手、读循环、心跳、待应答请求表、运行状态快照 |
| `agent_message_queue.go` | 191 | G | Agent 回包的排队与内存预算 | `gatewayMessageQueue`、`gatewayMessageBudget` |
| `agent_relay.go` | 290 | G | 把一次调用转发给 Agent 并等到终态 | `relayToolCall`、超时判定、结果未知判定 |
| `agent_leases.go` | 409 | G | 同一 target 上的并发准入 | 独占锁、只读准入、资源槽位、锁键推导 |
| `tool_resource_keys.go` | 104 | G | 工具调用对应哪个锁 | `resourceKeyForTool`、`canonicalK8SResource` |
| `tool_args.go` | 50 | G+A | 从工具参数里取值 | `extractSession`、`extractStringArg`、`numericID`、`validateSession` |
| `tool_operations.go` | 33 | G+A | K8s 与 DoWeb 的只读、变更操作清单 | `k8sReadOperations`、`webReadOperations`、`webMutationOperations`、`gatewayK8SReadOnly` |

追加到已有文件：

| 已有文件 | 追加内容 | 理由 |
| --- | --- | --- |
| `credential_lock.go` | `contextMutex` | 只有凭据锁使用 |
| `protocol_capabilities.go` | 凭据计划能力声明、`clientCapabilities` | 该文件本来就是能力声明的归属 |
| `client_service.go` | `GatewayActiveOperation`、`operationLease`、`targetReconnectGrace` | 活动操作登记已在此文件 |
| `instance_retention.go` | `StartOfflineInstanceReaper`、`pruneOfflineInstances` | 离线实例保留策略已在此文件 |

`tool_operations.go` 的内容原先分散在 `tunnel_hub.go`、`k8s_mcp_stdio.go`、`web_tool.go` 三处，Gateway 用它们给调用分类，Agent 用它们限制工具。集中后两侧读的是同一份清单。

### 3.4 `handler_ws.go` 拆出的文件

| 文件 | 行数 | 侧 | 含义 | 主要内容 |
| --- | ---: | --- | --- | --- |
| `handler_ws.go`（保留） | 439 | A | Agent 侧隧道连接的消息循环 | `serveWebSocketConn`、进程运行时 ID |
| `ws_transport.go` | 271 | G+A | WebSocket 的传输细节 | `upgrader`、Origin 校验、心跳、带截止时间的读写、读超时配置 |
| `jsonrpc_response.go` | 71 | G+A | JSON-RPC 响应构造 | `buildSuccessResponse`、`buildErrorResponse` 等 5 个 |
| `redaction.go` | 76 | G+A | 输出脱敏 | `sanitizeSecretText`、`redactSecretText`、`redactDoagentValue` |
| `tool_notifications.go` | 54 | A | 工具进度通知 | `notificationEvent` 及其构造函数、慢任务心跳 |
| `command_exec.go` | 116 | A | 启动一条命令 | `executeRawCommand`、`bashCommand`、`startRawCommand`、退出码 |
| `command_process_group.go` | 199 | A | 等待并清理进程组 | `waitProcessGroup`、残留进程的判定与错误类型 |
| `command_output.go` | 231 | A | 读取命令输出 | 输出流分块、UTF-8 边界、排空提示 |
| `doagent_client.go` | 168 | A | 访问 doagent 的 HTTP 客户端 | 地址解析、可用性探测、RPC、取消 |
| `doagent_sse.go` | 469 | A | 订阅 doagent 的事件流 | SSE 读取、事件解析、回合结束判定 |
| `doagent_prompt.go` | 535 | A | 一次 prompt 的编排 | `handleAgentPromptWS`、凭据绑定上下文、模式映射、apply 响应 |
| `doagent_attachments.go` | 98 | A | prompt 附件 | 数量与大小校验、上传 |
| `doagent_metadata.go` | 132 | A | 不执行 prompt 的两种查询 | metadata 模式、history 模式 |
| `doagent_model_status.go` | 211 | A | 模型通道状态 | 读取 settings 判断各通道是否可用 |

追加到已有文件：`doagentSettingsPath` 进 `model_provider.go`（settings 文件由它写入）；`writeSessionCommandAudit` 进 `audit_log.go`。

### 3.5 `workspace_upload.go` 拆出的文件

| 文件 | 行数 | 侧 | 含义 | 主要内容 |
| --- | ---: | --- | --- | --- |
| `workspace_upload.go`（保留） | 475 | A | 分块上传协议 | begin、chunk、commit、解包、上传元数据、过期清理 |
| `workspace_files.go` | 197 | A | 小文本读写与路径解析 | `handleFileWrite`、`handleFileRead`、`workspaceRoot`、`workspacePath` |
| `knowledge_base.go` | 206 | A | 知识库 Markdown 的读写 | 根目录、文件名校验、符号链接拒绝、读写 |
| `workspace_pull.go` | 238 | A | 拉取协议 | bundle 生成、分块读取 |
| `deployment_check.go` | 58 | A | 部署镜像核对 | `handleCheckDeployment`、`imageRefMatches` |
| `runtime_limits.go` | 90 | A（`positiveEnvInt64` 也被 Edge 隧道使用） | 可用环境变量调整的上限 | 上传、读写、消息大小、执行超时的默认值与读取函数 |

这个文件里有 4 处放错位置的内容，本次一并归位：升级请求参数 `agentUpgradeParams` 进 `self_upgrade_request.go`；
`detectAgentUpgradeMode` 进 `self_upgrade_cluster.go`；`handleCleanWorkspace` 进 `cleanup.go`；`validateSession` 进 `tool_args.go`。

### 3.6 `self_upgrade.go` 拆出的文件

| 文件 | 行数 | 侧 | 含义 | 主要内容 |
| --- | ---: | --- | --- | --- |
| `self_upgrade.go`（保留） | 561 | A | 升级流程的入口与编排 | `handleAgentUpgrade`、预览、启动升级 Job、启动回滚 Job、观察 |
| `self_upgrade_request.go` | 97 | G+A | 升级请求的参数与校验 | `agentUpgradeParams`、`validateAgentUpgradeDispatch`、镜像引用解析 |
| `self_upgrade_cluster.go` | 270 | A | 读取 Agent 自身在集群里的现状 | 命名空间、Helm release、Pod 规格、卷 |
| `self_upgrade_job.go` | 290 | A | 升级 Job 本身 | 清单生成、提交、读取、阶段判定、同一时刻只允许一个 |

失败类别常量追加到 `self_upgrade_response.go`。脚本文本已由并行开发移到 `self_upgrade_scripts.go`，本设计不再动它。

### 3.7 `gateway_store.go` 拆出的文件

| 文件 | 行数 | 侧 | 含义 | 主要内容 |
| --- | ---: | --- | --- | --- |
| `gateway_store.go`（保留） | 140 | G | 存储对象本身 | `GatewayStore`、打开与关闭、时间和随机数小工具 |
| `gateway_actions.go` | 35 | G | 动作词表 | `GatewayAction` 及全部 `Action*` 常量 |
| `gateway_store_schema.go` | 287 | G | 建表与迁移 | `migrate()` |
| `gateway_store_users.go` | 136 | G | 用户与密码 | 创建、查找、改密、密码校验 |
| `gateway_store_tokens.go` | 211 | G | 用户 token | 签发、校验、失败原因类型、过期清理 |
| `gateway_store_agents.go` | 201 | G | Agent 在线状态表 | 上线、下线、心跳、归档 |
| `gateway_store_audit.go` | 297 | G | 审计事件 | 开始、结束、过滤查询 |

### 3.8 凭据相关文件

| 文件 | 行数 | 侧 | 含义 | 主要内容 |
| --- | ---: | --- | --- | --- |
| `credential_store.go`（保留） | 232 | G | 凭据资源本身 | 创建、列出、按 ID 读取、错误变量 |
| `credential_store_versions.go` | 246 | G | 凭据版本 | 写入、解析载荷、提升、按状态读取 |
| `credential_store_authorize.go` | 124 | G | 使用授权判定 | `AuthorizeCredentialUse`、`AuthorizeCredentialGrant` |
| `credential_store_revocation.go` | 135 | G | 资源级撤销 | 开始撤销、完成撤销 |
| `credential_store_verifications.go` | 99 | G | 验证记录 | 写入、读取 |
| `credential_runtime.go`（保留） | 443 | G | 向 Agent 物化一个版本 | 物化请求、结果校验、被拒后的恢复 |
| `credential_resolve.go` | 408 | G | 一次调用需要哪些凭据 | 显式引用解析、Helm 与数据库凭据的自动选择、物化准备与回退 |
| `credential_verify_http.go` | 205 | G | 验证接口 | `handleCredentialVerify`、提升失败后的回退 |
| `credential_cleanup.go` | 158 | G | 物化的清理 | 按版本、按授权清理，统计未清理的 target |
| `credential_http_audit.go` | 85 | G | 凭据接口的审计辅助 | 审计的开始与结束 |
| `credential_errors.go` | 59 | G | 错误归类 | 错误到类别、错误到 HTTP 状态 |
| `credential_materializer.go`（保留） | 546 | A | Agent 上的物化主流程 | 物化、清理、快照与恢复 |
| `credential_materializer_secret.go` | 322 | A | Kubernetes Secret 操作 | Secret 与 imagePullSecrets 的读、写、比对 |
| `credential_materializer_probe.go` | 372 | A | 物化前的对外探测 | registry manifest、TLS 证书、Git 远端、出站 HTTP 客户端 |

`credential_store_grants.go` 是并行开发新建的，本设计把 4 个授权查询函数追加进去。

### 3.9 `doagent_tool_binding.go` 拆出的文件

| 文件 | 行数 | 侧 | 含义 | 主要内容 |
| --- | ---: | --- | --- | --- |
| `doagent_tool_binding.go`（保留） | 445 | A | 收集工具调用轨迹 | `doagentToolTraceCollector.collect`、工具名与输入的规整 |
| `doagent_tool_attestation.go` | 213 | A | 校验可信结果证明 | 证明合并、资格判定、变更证明的逐项检查 |
| `doagent_tool_evidence.go` | 322 | A | 从轨迹得出 apply 的结论 | 已验证变更结果、拒绝摘要、未决变更、对外可见结果 |

### 3.10 CLI

| 文件 | 行数 | 含义 | 主要内容 |
| --- | ---: | --- | --- |
| `main.go`（保留） | 1118 | 入口 | `main()`、版本常量 |
| `target_routes.go` | 92 | 本机路由 | 查找 target、别名与路由冲突检测、列表输出 |
| `command_context.go` | 69 | 每个命令开头的公共步骤 | `requireConfig`、`ResolveToken`、`mustResolveToken` |
| `admin_tokens.go` | 59 | `admin token` 子命令 | 列出、吊销 |
| `push.go`（保留） | 379 | push 的流程 | `pushSnapshot`、等待工作区就绪、检出证明 |
| `push_snapshot.go` | 317 | 构造要推送的快照 | 快照目录、快照提交、源清单 |
| `push_ignore.go` | 320 | 哪些文件不推送 | 默认排除、`.doopsignore`、`.gitignore`、子模块 |
| `git_transport.go` | 339 | 隔离的 git 子进程 | 认证环境、信号清理、参数脱敏；push 和 pull 共用 |
| `clean.go` | 28 | `clean` 命令的远端调用 | `Clean` |
| `client.go`（保留） | 277 | RPC 客户端对象 | `MCPClient`、分发循环、待应答表、关闭 |
| `client_connect.go` | 366 | 建立连接 | 握手、重试、协议能力与 Agent 版本校验 |
| `client_call.go` | 444 | 发起调用并处理结果 | `Call`、`callAndCaptureResult`、`CallStructured`、错误类型 |

`printTargets` 追加到已有的 `gateway_targets.go`。

### 3.11 测试文件

`gateway_protocol_test.go`（3558 行，58 个测试）全部迁出后删除。`handler_ws_test.go` 保留 8 个针对连接主循环的测试。

| 文件 | 行数 | 被测对象 |
| --- | ---: | --- |
| `gateway_testkit_test.go` | 383 | Gateway 侧测试共用：假 Agent、RPC 客户端、`requireEventually`（被 31 个测试文件使用） |
| `handler_testkit_test.go` | 179 | Agent 侧测试共用：隧道拨号、`callTool`、tar 包与摘要辅助 |
| `agent_registration_test.go` | 382 | Agent 注册、候选会话、运行时替换 |
| `agent_session_test.go` | 394 | 心跳、读循环、转发超时 |
| `agent_message_queue_test.go` | 74 | 消息队列与预算 |
| `agent_leases_test.go` | 93 | 忙碌快照、会话变更锁 |
| `auth_http_test.go` | 264 | 登录、token 生命周期、管理员签发 |
| `audit_http_test.go` | 97 | 审计查询的可见范围 |
| `audit_tail_test.go` | 19 | 审计尾部缓冲 |
| `client_rpc_test.go` | 572 | RPC 转发、拒绝、通知路由 |
| `client_rpc_concurrency_test.go` | 526 | 同会话、跨会话、只读并发与排队 |
| `tool_resource_keys_test.go` | 82 | 资源键推导 |
| `tunnel_hub_test.go` | 97 | target 列表 |
| `git_tunnel_test.go` | 603 | Git HTTP 隧道端到端 |
| `git_ws_test.go` | 278 | Agent 侧 Git over WebSocket |
| `ws_transport_test.go` | 312 | 心跳与读写的超时行为 |
| `command_exec_test.go` | 84 | 进程组与输出管道 |
| `redaction_test.go` | 25 | 脱敏 |
| `doagent_prompt_test.go` | 677 | prompt 与 apply 的终态 |
| `doagent_sse_test.go` | 376 | 事件流的各种结束方式 |
| `doagent_metadata_test.go` | 163 | metadata、history、doagent 地址 |
| `workspace_files_test.go` | 89 | 文本读写与越界拒绝 |
| `workspace_upload_test.go` | 206 | 上传顺序、摘要、大小限制、过期清理 |
| `workspace_pull_test.go` | 53 | 拉取 bundle |

另有 3 组测试追加到已有文件：动作映射进 `tool_registry_test.go`，凭据计划能力进 `protocol_capabilities_test.go`，知识库往返进 `knowledge_base_functional_test.go`。

### 3.12 验证记录

在 17:01 的仓库副本上依次应用 4 个计划后：

| 检查 | 结果 |
| --- | --- |
| `go build ./...`（agent、cli、gateway） | 通过 |
| `GOOS=linux go build ./...`（agent、cli、gateway） | 通过 |
| `go vet`（含测试文件） | 通过 |
| `internal/server` 测试函数集合 | 1030 个，前后一致 |
| CLI 测试函数数量 | 247 个，前后一致 |
| 声明行残差，`internal/server` | 仅 `)` 8 行、`var (` 5 行、`const (` 3 行 |
| 声明行残差，CLI | 仅 `)` 1 行、`const (` 1 行 |
| `go test ./...`，agent | 通过，`internal/server` 用时 780 秒 |
| `go test ./...`，cli、gateway | 通过 |
| 契约测试 | 486 项通过，另 8 个子测试通过 |

残差里的 16 行和 2 行都是被拆开的 `const`/`var` 分组块多出来的外壳，没有任何其它行差异。

17:26 在 `6c66c0bb` 上复核：4 个计划仍可应用，构建、`go vet`、残差、测试函数集合一致。这次没有重跑全部测试。

### 3.13 提交方式

按计划文件分 4 个搬运提交，再加 1 个跟进提交：

1. `server.plan`
2. `cli.plan`
3. `tests-1.plan`
4. `tests-2.plan`（依赖上一步生成的 `client_rpc_test.go`）
5. 3.14 的跟进事项

之前我建议过"每个源文件一个提交"。细化后发现多个源文件会向同一个新文件供稿（例如 `tool_args.go` 来自两个源文件，
`tool_operations.go` 来自三个），按源文件分提交会让中间状态不完整，所以改为按计划分。

搬运提交的评审依据是 2.6 的不变量，不是逐行读 diff。`git diff --color-moved=plain` 可以辅助人工抽查：我做过对照实验，
纯搬运时未被识别为移动的新增行只有新文件的 `package` 和 `import`；搬运时夹带一处改动，那一行会单独显示出来。

执行前提：`internal/server` 需要一个没有并行改动的窗口。今天下午这个目录每隔几分钟到二十几分钟就有新提交，计划已经因此失效过一次。
失效时工具会拒绝执行并列出缺失的符号，不会产生错误结果，但需要更新计划后重来。

### 3.14 搬运后必须跟进的事项

1. **包内 README 的文件前缀索引**。新增前缀 `auth_`、`client_rpc`、`client_tool_call`、`agent_session`、`agent_relay`、
   `agent_leases`、`command_`、`ws_transport`、`tool_args`、`tool_operations`、`jsonrpc_response`、`redaction`。
2. **一条会静默失效的否定断言**。[test_natural_language_deployment_contract.py](../../backend/test/test_natural_language_deployment_contract.py)
   里有 4 条"某文件不含某段文本"的断言，对象是 `handler_ws.go` 和 `workspace_upload.go`。相关代码搬走后它们永远为真。
   其中 3 条针对的是已退役的标识符，`test_stale_contracts.py` 对整个 `backend/agent` 目录的扫描仍然覆盖；
   剩下针对 prompt 文本 `Current DoOps session_id` 的那一条会失去保护，应改成扫描整个包目录。
3. **一个未使用的变量**。`tunnel_hub.go` 里的 `var lastProbe atomic.Value` 在整个 `backend` 下只有声明这一处。
   计划把它原样搬到 `agent_relay.go`，删除应放在单独的提交里。
4. **`.git-blame-ignore-revs`**。把 4 个搬运提交的哈希写进去，`git blame` 会跳过它们。

## 4. 阶段二：拆巨型函数

阶段一之后仍超过 300 行的函数：

| 函数 | 行数 | 所在文件 |
| --- | ---: | --- |
| CLI `main` | 1098 | `main.go` |
| `handleGatewayToolCall` | 568 | `client_tool_call.go` |
| `serveWebSocketConn` | 402 | `handler_ws.go` |
| `handleAgentPromptWS` | 399 | `doagent_prompt.go` |
| CLI `runCredentialConfigure` | 339 | `credential_configure.go` |
| `subscribeDoagentSSEWithCollectorReady` | 332 | `doagent_sse.go` |

150 到 300 行的还有 15 个，包括 `migrate`（建表 SQL，不拆）、`relayToolCall`、`HandleClientRPC`、`materializeCredential`、
`buildK8SCommands` 等，按预算棘轮逐步处理，不在本设计里逐个展开。

这一阶段只有设计，没有执行过。文件名已核对过不与现有文件冲突。

### 4.1 方法

每个巨型函数用同一个办法拆：

1. 找出函数里的**阶段**。阶段之间只通过少量变量传递状态。
2. 把这些变量收进一个**调用上下文结构体**，阶段变成它的方法。
3. 原函数只剩下按顺序调用各阶段，以及必须留在原地的 `defer`。

`defer`、锁的获取与释放、审计的开始与结束必须留在编排函数里，不能随阶段搬走。它们的顺序是行为的一部分。

每次只拆一个函数，一个提交，跑该包的全部测试。

### 4.2 `handleGatewayToolCall`（568 行）

| 文件 | 含义 |
| --- | --- |
| `client_tool_call.go`（保留） | 编排：建审计、登记操作、取锁、转发、落终态。`defer` 和锁都留在这里 |
| `client_tool_call_prompt.go` | prompt 类调用的参数校验：操作类型、凭据引用、凭据模式、DoSql 的限制 |
| `client_tool_call_credentials.go` | 取锁之后的凭据复核与物化，以及失败时的回退 |
| `client_tool_call_outcome.go` | 把 Agent 的回包和转发错误归类成终态：成功、错误、取消、超时、结果未知 |

先拆 `_prompt` 和 `_outcome`，它们不碰共享状态。`_credentials` 夹在取锁和转发之间，放最后。

### 4.3 `serveWebSocketConn`（402 行）

循环体里有 10 个按 `req.Method` 判断的分支。连接级状态（写函数、进行中的 Git 与代理流、进行中的工具调用）目前是闭包变量。

| 文件 | 含义 |
| --- | --- |
| `handler_ws.go`（保留） | 连接的建立、读循环、按方法名分发、退出时的清理 |
| `handler_ws_streams.go` | 流式方法：`git/http`、`proxy/http` 以及对应的 `body`、`cancel` |
| `handler_ws_tools.go` | `tools/call`、`tools/cancel`，以及进行中调用的登记 |

做法：新增一个连接状态结构体持有上述闭包变量，10 个分支变成它的方法。`initialize` 和 `gateway/registered` 很短，留在主文件。

### 4.4 `handleAgentPromptWS`（399 行）与事件流函数（332 行）

| 文件 | 含义 |
| --- | --- |
| `doagent_prompt.go`（保留） | 编排一次 prompt |
| `doagent_prompt_session.go` | 准备 doagent 会话：新建或复用、注入系统提示词、模型覆盖、权限模式 |
| `doagent_prompt_turn.go` | 跑一个回合：先订阅事件流，再发 prompt，等到回合终态 |
| `doagent_prompt_result.go` | apply 模式下根据工具证据得出最终响应 |
| `doagent_sse.go`（保留） | 事件流的连接与按行读取 |
| `doagent_sse_events.go` | 处理一个已解析的事件：工具调用、助手消息、回合结束、权限更新、错误 |

### 4.5 CLI `main()`（1098 行）

约束：[test_cli_help_contract.py](../../backend/test/test_cli_help_contract.py) 用正则从 `main.go` 里解析帮助文本和一级缩进的 `case "..."`，
核对两者一致。所以**分发 `switch` 和帮助文本必须留在 `main.go`**，只把每个分支的函数体搬走。

拆完后 `main.go` 的每个 `case` 只有一行调用。

| 分支 | 现有行数 | 去向 | 文件状态 |
| --- | ---: | --- | --- |
| `exec`、`ssh`、`ask`、`write`、`read`、`info`（六合一） | 220 | 见下 | |
| `admin` | 113 | `admin.go`：按子命令分发 | 新建 |
| `login`、`logout` | 108 + 25 | `login.go` | 新建 |
| `add`、`remove`、`list` | 68 + 45 + 16 | `target_routes.go` | 阶段一已建 |
| `targets` | 45 | `gateway_targets.go` | 已有 |
| `pull` | 56 | `pull.go` | 已有 |
| `push` | 53 | `push.go` | 已有 |
| `upgrade` | 51 | `upgrade.go` | 已有 |
| `check` | 38 | `check.go` | 已有 |
| `clean` | 36 | `clean.go` | 阶段一已建 |
| `k8s` | 33 | `k8s.go` | 已有 |
| `capabilities` | 32 | `capabilities.go` | 新建 |

六合一分支目前共用一个 `FlagSet`，20 个参数混在一起，每个命令只用其中几个。拆成每个命令自己的函数和自己的参数：

| 文件 | 命令 | 状态 |
| --- | --- | --- |
| `exec.go` | `exec`、`exec --bg` | 新建 |
| `ask.go` | `ask`、`ask --apply` | 新建 |
| `read.go` | `read` | 新建 |
| `info.go` | `info` | 新建 |
| `ssh.go` | `ssh` | 已有 |
| `write.go` | `write` | 已有 |

"检查配置、找 target、取 token、建客户端"这段在 `main.go` 里重复了 14 次。在 `command_context.go` 里加一个函数统一完成，
各命令调用它。评审里的 CLI-02（错误写到标准输出）也在这个函数里一并修正。

需要同步改的测试：[test_natural_language_deployment_contract.py](../../backend/test/test_natural_language_deployment_contract.py)
断言 `--apply` 的参数定义和 `arguments["operation"] = "apply"` 这两段文本在 `main.go` 里。`ask` 搬到 `ask.go` 后要改成读 `ask.go`。同一测试里还有一条针对 `main.go` 的否定断言，要改成覆盖整个 CLI 目录。

### 4.6 CLI `client_call.go`

`Call`（137 行）和 `callAndCaptureResult`（126 行）是两份几乎相同的事件循环，区别只是输出打印到终端还是收集到缓冲区。
合并成一个循环，输出去向作为参数传入。评审里的 CLI-01（断连应返回结果未知）现在要改 3 处，合并后只改 1 处。

## 5. 阶段三：Web

这一阶段只有设计，没有执行过。16:43 时的行数：`credential-panel.tsx` 1115、`admin-console.tsx` 1033、`console-shell.tsx` 1013、
`lib/demo.ts` 978、`lib/credentials.ts` 909。

### 5.1 约束

- **测试按路径加载组件源码**。[component-test-harness.mjs](../../lib/component-test-harness.mjs) 读取指定的 `.tsx` 文件并执行，
  组件的每个 import 都必须出现在测试提供的替身表里，否则报 `Unmocked dependency`。所以移动组件时，
  [admin-views.test.mjs](../../lib/admin-views.test.mjs) 里的文件路径和替身表必须在同一个提交里改。
- **有测试对源码文本做断言**。[interrupted-banner.test.mjs](../../lib/interrupted-banner.test.mjs) 断言 `console-shell.tsx`
  里中断提示的渲染出现在终端标签页分支之前。所以 `console-shell.tsx` 的 JSX 结构和 `interruptedNotices` 这个变量名要保留。
- **`lib/` 下新文件的相对导入带 `.ts` 扩展名**。这些文件会被 `node --test` 直接加载，`credentials.ts` 现在就是这样写的。
- **不改变组件的挂载位置和 key**。终端和 AI 会话的状态挂在组件实例上，重新挂载会丢失。

### 5.2 `admin-console.tsx`

四个视图已经是独立的函数组件，直接各移到一个文件。

| 文件 | 含义 | 内容 |
| --- | --- | --- |
| `admin-console.tsx`（保留） | 平台管理的外壳 | `AdminConsole`、标签切换、`AdminAssetScope` |
| `admin-shared.tsx` | 四个视图共用的小组件 | `Toolbar`、`ErrorBar`、`StatusDot`、`fmtTime` |
| `admin-instances-view.tsx` | 实例管理 | `InstancesView` |
| `admin-users-view.tsx` | 用户管理 | `UsersView` |
| `admin-grants-view.tsx` | 授权管理 | `GrantsView` |
| `admin-tokens-view.tsx` | 令牌管理 | `TokensView` |

### 5.3 `credential-panel.tsx`

`CredentialPanel` 本身约 495 行，后面 530 行是它用到的 9 个子组件。先把子组件移走。

| 文件 | 含义 | 内容 |
| --- | --- | --- |
| `credential-panel.tsx`（保留） | 凭据面板 | `CredentialPanel` |
| `credential-dialogs.tsx` | 面板弹出的对话框 | `DialogFrame`、`VersionDialog`、`VerifyDialog`、`PayloadDialog`、`BindingDialog` |
| `credential-summaries.tsx` | 列表行里的摘要 | `BindingSummary`、`VersionSummary` |
| `credential-create-form.tsx` | 新建凭据表单 | `CreateCredentialForm` |
| `credential-form-fields.tsx` | 表单字段 | `TextField`、`ReadOnlyField` |

### 5.4 `lib/credentials.ts`

文件里有三段互不依赖的功能：基本的增删改查、从 Secret 导入、凭据组。后两段各有自己的类型和校验。
现有测试已经按这个边界命名（`credential-import.test.mjs`、`credential-bundles.test.mjs`、`credential-errors.test.mjs`、
`credential-binding.test.mjs`），新文件名与测试对齐。

| 文件 | 含义 | 内容 |
| --- | --- | --- |
| `credentials.ts`（保留） | 凭据的增删改查，并转出其余文件的导出 | `listCredentials` 到 `revokeCredential`、绑定范围的构造 |
| `credential-types.ts` | 类型定义 | `CredentialType`、`CredentialResource` 等 |
| `credential-request.ts` | 请求与错误归类 | 请求头、`request`、`CredentialRequestError`、错误码到中文说明的映射 |
| `credential-import.ts` | 从 Secret 导入 | 输入校验、请求构造、导入错误说明 |
| `credential-bundles.ts` | 凭据组 | 类型、校验、创建、读取、各项状态说明 |

`credentials.ts` 保留对其余文件的转出，4 个组件和 4 个测试的导入路径不用改。

### 5.5 `lib/demo.ts`

| 文件 | 含义 | 内容 |
| --- | --- | --- |
| `demo.ts`（保留） | 转出入口 | 只做转出，供现有的动态导入使用 |
| `demo-token.ts` | 演示模式的标记 | `DEMO_TOKEN` |
| `demo-data.ts` | 演示数据 | target、审计、目录、文件内容、知识库 |
| `demo-tools.ts` | 模拟工具调用 | `demoCallTool`、shell 与 K8s 的模拟输出、模拟回答 |
| `demo-admin.ts` | 模拟管理接口 | 用户、授权、令牌、实例、代理路由 |
| `demo-tasks.ts` | 模拟后台任务 | 列表、查询、停止 |

单独拆出 `demo-token.ts` 有一个预期收益：`console-shell.tsx` 和 `connect-screen.tsx` 为了一个常量静态导入了整个 `demo.ts`，
而 `lib/admin.ts`、`lib/client.ts` 特意用动态导入来延迟加载它。静态导入存在时动态导入起不到拆包的作用。
这是从打包规则推断的，没有实测过包体积。

### 5.6 `console-shell.tsx`

一个组件里有 33 个 `useState`、11 个 `useEffect`、13 个 `useRef`。只抽状态逻辑，不新增包裹组件，JSX 留在原文件。
命名沿用已有的 `components/use-machine-inspections.ts`。

| 文件 | 含义 | 收纳的状态 |
| --- | --- | --- |
| `console-shell.tsx`（保留） | 控制台外壳的布局与面板挂载 | JSX、各 hook 的组合 |
| `use-console-session.ts` | 登录会话 | `session`、`defaultGateway`、`authLoading`、`disconnecting`、`offline` |
| `use-console-targets.ts` | target 列表与轮询 | `targets`、`registered`、`directoryError`、`loading`、`error`、`targetsError`、`selected`、`sessionId` |
| `use-console-navigation.ts` | 当前看的是哪一页 | `view`、`tab`、`deploySection`、`catalogKind`、`catalogContext`、`assetNamespace`、`assetOrigin`、`assetPage`、`mobileNavOpen` |
| `use-terminal-handoff.ts` | 终端的预填与中断恢复 | `terminalPrefill`、`consumedPrefillNonce`、`skillRunning`、`execRunning`、`restoreNotice`、`interruptedNotices` |
| `use-whats-new.ts` | 更新内容与新手引导 | `whatsNewOpen`、`whatsNewUnread`、`autoOpenPending`、`showGuide` |

第 33 个状态 `openedAgentRuntime` 持有 AI 会话运行时，留在 `console-shell.tsx`。

这一项风险最高，放在最后。hook 之间有依赖（刷新 target 需要会话，恢复选择需要 target），抽取时一次只抽一个，
每次都在浏览器里验证切换目标、终端执行中刷新页面、AI 会话保持这三条路径。

### 5.7 顺序

`admin-console.tsx` → `credential-panel.tsx` → `lib/credentials.ts` → `lib/demo.ts` → `console-shell.tsx`。
前四项是搬运，每项一个提交，跑 `pnpm test:console` 和 `tsc --noEmit`。

## 6. 防止再长回去

仓库已有先例：[test_doc_links.py](../../backend/test/test_doc_links.py) 限制 `SKILL.md` 不超过 12,000 字节。
照此新增一条契约测试 `backend/test/test_source_size_budget.py`：

- 扫描 `backend/agent`、`backend/cli`、`components`、`lib`、`app` 下的手写源文件。
- 非测试文件超过 600 行、测试文件超过 1000 行即失败。
- 现有超限文件列入白名单，白名单记录每个文件当前的行数，文件行数超过记录值即失败。
- 白名单里的数字只能改小。文件降到预算以内后从白名单删除。

这样不要求一次达标，但保证不会变得更糟。

## 7. 没有验证的部分

- 阶段二和阶段三全部是设计，没有执行过，文件名只核对过不冲突。
- 阶段一没有在真实工作区执行。副本验证用的是 17:01 的状态；17:26 复核时没有重跑全部测试。
- 搬运工具只验证过本次涉及的 15 个源文件和 2 个测试文件。
- 没有在 Linux 上跑测试，只验证了 `GOOS=linux` 能构建。
- 5.5 关于包体积的判断是推断。

## 附录 A 阶段一的执行与检查

```bash
# 构建工具
go build -o /tmp/mover .local/file-split/mover.go

# 记录基线（在干净的提交上）
S=backend/agent/internal/server
C=backend/cli/cli/doops
norm() {
  cat "$@" | awk '
    /^import \($/ {skip=1; next}
    skip && /^\)$/ {skip=0; next}
    skip {next}
    /^import "/ || /^package / || /^[[:space:]]*$/ {next}
    {gsub(/[[:space:]]+/," "); print}' | LC_ALL=C sort
}
norm $S/*.go > /tmp/server.before
norm $C/*.go > /tmp/cli.before
(cd backend/agent && go test -list '.*' ./internal/server/ | grep '^Test' | sort) > /tmp/tests.before

# 依次应用，每步之后提交
/tmp/mover $S .local/file-split/server.plan
/tmp/mover $C .local/file-split/cli.plan
/tmp/mover $S .local/file-split/tests-1.plan
/tmp/mover $S .local/file-split/tests-2.plan

# 不变量
(cd backend/agent && go build ./... && go vet ./...)
(cd backend/cli && go build ./... && go vet ./...)
(cd backend/agent && go test -list '.*' ./internal/server/ | grep '^Test' | sort) | diff /tmp/tests.before -
norm $S/*.go | comm -3 /tmp/server.before - | sed 's/^\t//' | sort | uniq -c   # 只应出现 )、const (、var (
norm $C/*.go | comm -3 /tmp/cli.before -    | sed 's/^\t//' | sort | uniq -c
(cd backend/agent && go test -timeout 25m ./...)
(cd backend/cli && go test ./...)
(cd backend/gateway && go test ./...)
(cd backend && python3 -m pytest test -q)
```

## 执行记录（阶段一，已完成）

- 分支：`refactor/phase1-file-split`，基线 `e28d33ac`，当前 `788626db`（实施时的临时工作树已移除）。
- 提交：`c6b2f441` server 声明、`f5832311` CLI、`3d8b2aaa` 协议测试重命名加拆分、`352a284a` RPC 并发测试、`fa01d540` README 索引与契约测试、`788626db` 合并 main 知识库改动。
- `.git-blame-ignore-revs` 记录了前 4 个纯搬运提交；合并提交含冲突接合，不列入。
- 行数校验：相对 `7d997bbc`（合并后基线）剔除 import 后，server 与 CLI 均无代码行删除，新增仅 `const (`、`var (`、`)` 外壳。
- 测试集合：server 1030 个、CLI 247 个测试函数前后一致。
- 验证（合并后）：`backend/agent` vet + 全部测试通过（server 766s）；`backend/cli`、`backend/gateway` 测试通过；`backend/test` 契约测试 486 + 8 子测试通过；`git diff --check` 干净。
- 离线交付包：`.local/file-split/phase1-788626db.bundle`（已 `git bundle verify`，sha256 `40e49aac…47b9e`），需先含 `e28d33ac`。
- 已快进合入 `main`（`7d997bbc → 788626db`），合并与当时未提交的字号、监控等改动零重叠，工作区改动全部保留。
- 未推送：GitHub HTTPS/SSH/API 均不可达；恢复后 `git push github main`。
