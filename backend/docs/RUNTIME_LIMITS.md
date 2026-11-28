# 运行时超时与限制（Runtime Timeouts And Limits）

本文件是 DoOps Agent / Gateway 超时与时间限制的**权威表**。代码里的默认值必须与本文一致，
由 `backend/agent/internal/server/timeout_defaults_test.go` 的 `TestTimeoutDefaultsMatchDoc` 逐项比对：
改代码不改本文会红，改本文不改代码也会红。

状态（B-13 第二步，2026-11-24）：**已接线**。`backend/agent/internal/server/timeout_defaults.go`
持有唯一的默认值表与校验逻辑，各模块的调用点（Helm、MCP、MySQL、Edge、巡检、凭证投递、
工具截止、保留任务淘汰）都在运行时读这份快照，源码里不再有第二份同样的字面量。接线点逐条列在
下面的「接线点清单」；`backend/agent/internal/server/timeout_wiring_test.go` 用行为断言钉住
"改表就改行为"，并扫描源码禁止旧常量名回到生产文件。
**接线没有改变任何默认值，也没有改变任何报错文案**：未设环境变量时每个调用点拿到的值与接线前
一模一样，由 `TestTimeoutDefaultsKeepPreWiringValues` 的字面量表逐项钉住。

## 表 1：带环境变量覆盖的超时

时间段一律写 Go duration 字符串（`90s`、`25m`、`24h`），只有 `DOOPS_TOOL_TIMEOUT` 与
`DOOPS_COMPLETED_TASK_MAX_AGE` 例外，它们沿用今天的"裸数字按秒解释"兼容行为（见下文第 2 节）。

| 变量 | 默认值 | 允许范围 | 作用的语义 | 依赖 / 断言 |
|---|---|---|---|---|
| `DOOPS_TOOL_TIMEOUT` | 30m | 1s..24h | Agent 侧单次工具执行上限；显式设置时同时硬顶 Gateway 广告的超时 | I2 |
| `DOOPS_COMPLETED_TASK_MAX_AGE` | 24h | 1ms..720h | 已完成任务记录的惰性淘汰窗口 | — |
| `DOOPS_TOOL_DEADLINE_MARGIN` | 5s | 1ms..1h | Agent 自己的截止时间比 Gateway 早多少，保证回一个确定的超时而不是被对端放弃 | I2 |
| `DOOPS_HELM_SETTLE_RESERVE` | 90s | 1s..30m | MCP 工具超时尾部留给 SIGTERM 宽限与 settle 步骤的余量 | I1 |
| `DOOPS_HELM_SETTLE_TIMEOUT` | 45s | 1s..30m | 读 release history、解锁 pending release 的上限 | I1 |
| `DOOPS_HELM_MAX_WAIT` | 25m | 1s..2h | 单次 `doops_helm` 调用的 --wait 上限 | I1 |
| `DOOPS_AGENT_LEASE` | 90s | 1s..10m | Agent 租约；心跳 ping 周期 = 租约/3 | I3 |
| `DOOPS_AGENT_HEARTBEAT_FLUSH` | 30s | 1s..10m | 心跳批量落库周期 | I3 |
| `DOOPS_TUNNEL_READ_TIMEOUT` | 90s | 1s..10m | Agent 侧隧道的读死线：Agent 多久没收到 Gateway 的帧就断开这条连接并终止其上的在途调用。D43 决定它与 `DOOPS_AGENT_LEASE` 保持两个旋钮（租约驱动 ping/心跳落库/判活/reaper，这个只管单条 socket 的沉默），但读死线不得短于 ping 周期（租约/3）。原来的 `defaultAgentTunnelReadTimeout` 与每次调用读 env + 静默回落的写法已删除 | I10 |
| `DOOPS_EDGE_DIAL_TIMEOUT` | 5s | 100ms..5m | Edge 拨号与握手预算（实际预算另加 `edgeOpeningAckSlack` 1s） | I6 |
| `DOOPS_EDGE_WRITE_STALL` | 30s | 100ms..10m | Edge 连接多久没有写进展就被调度器挂起；原来的 `defaultEdgeWriteStall` 与每次调用读 env + 静默回落的写法已删除 | — |
| `DOOPS_EDGE_CONN_ID_QUARANTINE` | 30s | 1s..10m | 被释放的 Edge 连接 id 多久不可复用，避免旧握手的应答落到 id 的新主人；原来的 `defaultEdgeConnIDQuarantine` 已删除 | I12 |
| `DOOPS_EDGE_HELLO_TIMEOUT` | 5s | 100ms..1m | websocket 建立后 Edge hello 交换的死线；原来的 `defaultEdgeHelloTimeout` 已删除 | — |
| `DOOPS_EDGE_WS_READ_TIMEOUT` | 20s | 1s..5m | 单条 Edge websocket 的空闲读死线（Edge 侧每 5s ping 一次）。它与 `DOOPS_TUNNEL_READ_TIMEOUT` 是两个旋钮：后者是 Agent 侧隧道死线并绑定 `DOOPS_AGENT_LEASE`，合并会把 Edge 侧失联检测从 20s 拉到租约量级；原来的 `edgeWSReadTimeout` 常量已删除 | I13 |
| `DOOPS_MYSQL_CONNECT_TIMEOUT` | 8s | 100ms..2m | MySQL 连接超时（驱动 `cfg.Timeout`，三处各写一份值） | — |
| `DOOPS_MCP_PROBE_TIMEOUT` | 12s | 100ms..5m | MCP 服务探测上下文与 HTTP client 超时 | — |
| `DOOPS_CREDENTIAL_DELIVERY_TIMEOUT` | 3m | 1s..30m | 模型供应商与仓库凭证的投递等待 | D4 |
| `DOOPS_MONITOR_RETENTION_MIN_INTERVAL` | 15m | 1s..24h | 巡检产物清理的后台触发节流窗口（小时兜底扫描不受影响） | — |
| `DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION` | 720h | 1h..8760h | `platform_load_samples` 的保留窗口（资源趋势图的数据源，随实例清理一起删） | — |
| `DOOPS_TARGET_QUEUE_TIMEOUT` | 10m | 1s..24h | 请求在忙目标上排队等待的上限；同时是 `-target-queue-timeout` 的默认值 | — |
| `DOOPS_AUDIT_RETENTION_INTERVAL` | 6h | 1s..24h | 审计保留清理的巡检周期（保留窗口本身仍由 `-audit-retention` 决定） | — |
| `DOOPS_EXPIRED_TOKEN_SWEEP_INTERVAL` | 6h | 1s..24h | 过期会话 token 的清理周期，与审计清理各自独立 | — |
| `DOOPS_OPERATION_TIMEOUT` | 60m | 1s..24h | 单个转发操作的运行上限；同时是 `-operation-timeout` 的默认值 | — |
| `DOOPS_DETACHED_TASK_TIMEOUT` | 24h | 1s..720h | 分离式后台任务（`doops_bg detach`）的生存期；同时是 `-detached-task-timeout` 的默认值 | — |
| `DOOPS_TARGET_RECONNECT_GRACE` | 10s | 1s..5m | RPC/内部调用等待目标 Agent 重连的上限；同时是 `-target-reconnect-grace` 的默认值（flag 传 0 仍然立即失败） | I5 |
| `DOOPS_SHUTDOWN_DRAIN_TIMEOUT` | 2m | 1s..30m | SIGTERM/SIGINT 后等待在途操作结束的排空窗口；同时是 `-shutdown-drain-timeout` 的默认值 | — |
| `DOOPS_HTTP_SHUTDOWN_TIMEOUT` | 10s | 100ms..5m | 排空窗口结束之后，HTTP 服务器的 `Shutdown` 还有多少时间关掉连接；与排空窗口是两件事（排空等操作，这个等连接），原来的 `10 * time.Second` 字面量已删除 | — |
| `DOOPS_EXECUTION_RESUME_GRACE` | 60s | 1s..10m | 凭据/执行恢复记录尚未落定时，Gateway 再等它多久才算"没回来"；原来的 `executionResumeGraceDefault` 已删除 | I9 |
| `DOOPS_EXECUTION_RESUME_WAIT_MAX` | 5s | 100ms..10m | 单个 `GET /v1/executions/<id>?wait_ms=N` 的阻塞上限，即"客户端一次能等多久"；记录能活多久由 `DOOPS_EXECUTION_RESUME_TTL` 决定，两者不是一件事。原来的 `executionResumeWaitMax` 常量已删除 | I11 |
| `DOOPS_EXECUTION_RESUME_TTL` | 30m | 1m..24h | 恢复记录自身的有效期：过了这个点记录被回收，等待也就没有意义；原来的 `executionResumeTTLDefault` 已删除 | I4、I9 |
| `DOOPS_AGENT_EXECUTION_TTL` | 30m | 1m..24h | Agent 侧 detached 执行记录的 TTL；原来的 `agentExecutionTTLDefault` 已删除，包级 store 不再在 var 初始化里读 env | I4 |
| `DOOPS_MONITOR_TICK` | 15s | 1s..1h | 到期巡检/监控计划的检查周期；同时是 `-monitor-tick` 的默认值（flag 传 0 仍然禁用） | — |
| `DOOPS_HTTP_READ_HEADER_TIMEOUT` | 10s | 1s..5m | Gateway HTTP 服务器的 `ReadHeaderTimeout`（只约束升级前的普通请求头） | — |
| `DOOPS_HTTP_IDLE_TIMEOUT` | 2m | 1s..1h | Gateway HTTP 服务器的 `IdleTimeout`（keep-alive 空闲连接） | — |
| `DOOPS_OFFLINE_REAPER_INTERVAL` | 1h | 1s..24h | 离线实例回收器（`StartOfflineInstanceReaper`）的巡检周期，启动时先扫一次 | I8 |
| `DOOPS_OFFLINE_INSTANCE_RETENTION` | 336h | 1s..8760h | 离线实例记录在 `agent_status` 里保留多久（14 天）；原来的 `OfflineInstanceRetention` 常量已删除 | I8 |
| `DOOPS_AUDIT_PURGE_BATCH_PAUSE` | 100ms | 1ms..1m | 审计清理批与批之间的暂停，让其他写入能用数据库；原来的 `AuditPurgeBatchPause` 常量已删除（批量条数 `AuditPurgeBatchSize` 不是时间，仍在代码里） | — |

`-webhook-retention`（默认 `720h`，`0` 永久）是 `platform_events` 与终态 `webhook_deliveries` 的保留窗口；只删没有 `pending/delivering` 投递的事件，清理在 webhook worker 启动或被唤醒时按一小时节流执行，不新增独立定时器。单次请求 10s、租约 30s、全局并发 8/单端点 2 的常量见 `backend/docs/design/DOOPS_WEBHOOKS.md` §8.5。

## 表 2：从表 1 派生的预算（不是第二份默认值）

这些值没有独立的环境变量，也不是独立常量：它们是表 1 的字段按固定公式算出来的，唯一的输入
仍然是表 1。`TestTimeoutDefaultsKeepPreWiringValues` 钉住派生结果与接线前的数值相等。

| 派生量 | 值 | 来源 | 说明 |
|---|---|---|---|
| `helmToolDeadline()` | 28m30s | `MCPToolTimeout − HelmSettleReserve`（`helm_release.go:28`） | 单个 `doops_helm` 调用的预算，比 MCP 预算更紧 |
| MCP 工具调用预算 | 29m30s | `MCPToolTimeout − MCPCallSettle`（`k8s_mcp_stdio.go:208`） | Agent 侧每个 MCP 工具调用的上限 |
| `timeouts.HelmTerminationGrace` | 30s | 表内固定字段（无 env，`timeout_defaults.go`） | Helm 收到 SIGTERM 后记录终态的宽限 |
| `mcpToolTimeoutSeconds()` | 1800（30m） | `MCPToolTimeout / time.Second`（`timeout_defaults.go:227`） | doagent 侧 MCP 工具超时的秒数形式；旧的 `doopsMCPToolTimeoutSeconds` 常量已删除，表是唯一来源 |


## 接线点清单

每一行都是"运行期真的读表"的位置；表里改了值，这些调用点下一次调用就用新值。
`TestRetiredTimeoutConstantsStayOutOfProductionSources` 扫描生产源码，禁止
`helmSettleReserve`、`helmSettleTimeout`、`maxHelmWaitTimeout`、`helmTerminationGrace`、
`mcpToolCallDeadline`、`mcpProbeTimeout`、`defaultEdgeDialTimeout`、
`monitorRetentionMinInterval`、`modelProviderDeliveryTimeout`、`registryAuthDeliveryTimeout`、
`toolDeadlineMargin`、`doopsMCPToolTimeoutSeconds`、`defaultEdgeWriteStall`、
`defaultEdgeConnIDQuarantine`、`defaultEdgeHelloTimeout` 这些旧常量名重新出现。
`TestRetiredEnvReadersStayOutOfTheSources` 同时禁止 `positiveEnvDuration` 与 `durationOverride`
两个"每次调用读 env、写错静默回落"的读取函数回来，
`TestEveryEnvSpecIsReadThroughTheSnapshot` 则要求表里每个带 env 的字段都被某个运行期
`timeouts.<字段>` 读取——一个"解析了但从不使用"的行会让它红。
`OfflineInstanceRetention` 与 `AuditPurgeBatchPause` 现在分别是导出访问器与表字段，名字本身还
在使用，所以这条旧名扫描不覆盖它们；它们由 `backend/agent/pkg/gatewaycmd` 的
`TestGatewayServeDurationsComeFromTheTimeoutTable`（默认值」必须是 `server.*()` 表达式）
与本节的行为证据测试兜住。

| 调用点 | 读取的字段 |
|---|---|
| `backend/agent/internal/server/helm_release.go:28` | `MCPToolTimeout` − `HelmSettleReserve`（`helmToolDeadline()`） |
| `backend/agent/internal/server/helm_release.go:212` | `HelmTerminationGrace` |
| `backend/agent/internal/server/helm_release.go:225` | `HelmSettleTimeout` |
| `backend/agent/internal/server/helm_tool.go:117-118` | `HelmMaxWait`（校验与错误文案同一个值） |
| `backend/agent/internal/server/k8s_mcp_stdio.go:208` | `MCPToolTimeout` − `MCPCallSettle` |
| `backend/agent/internal/server/k8s_mcp_stdio.go:239` | 同上（超时错误的文案） |
| `backend/agent/internal/server/mcp_agent.go:297` | `MCPProbeTimeout`（探测 context） |
| `backend/agent/internal/server/mcp_agent.go:453` | `MCPProbeTimeout`（HTTP client） |
| `backend/agent/internal/server/mcp_agent.go:593` | `mcpToolTimeoutSeconds()` |
| `backend/agent/internal/server/mcp_registry.go:53`、`:67` | `mcpToolTimeoutSeconds()` |
| `backend/agent/internal/server/model_provider.go:144` | `mcpToolTimeoutSeconds()`（JSON schema） |
| `backend/agent/internal/server/tunnel_hub.go:82` | `AgentLease`（hub 选项默认值） |
| `backend/agent/internal/server/tunnel_hub.go:88` | `TargetQueueTimeout`（hub 队列等待兜底） |
| `backend/agent/internal/server/tunnel_hub.go:232` | `AgentHeartbeatFlush`（flusher 无租约可除时的周期） |
| `backend/agent/internal/server/tunnel_hub.go:85` | `OperationTimeout`（零值 hub 选项的兜底，不再是 60m 字面量） |
| `backend/agent/internal/server/tunnel_hub.go:96` | `TargetReconnectGrace`（零值 hub 选项的兜底） |
| `backend/agent/internal/server/tunnel_hub.go:109` | `DetachedTaskTimeout`（零值 hub 选项的兜底，不再是 24h 字面量） |
| `backend/agent/internal/server/tunnel_hub.go:112`、`:115` | `executionResumeGrace()` / `executionResumeTTL()`（原 `executionResumeGraceFromEnv()` / `executionResumeTTLFromEnv()` 已删除，非法 env 现在启动即失败） |
| `backend/agent/internal/server/execution_resume.go:225` | `executionResumeTTL()`（`newExecutionRegistry` 的零值兜底） |
| `backend/agent/internal/server/execution_resume.go:519` | `executionResumeGrace()`（`Resume` 的 grace 兜底） |
| `backend/agent/internal/server/agent_execution_resume.go:193` | `agentExecutionTTL()`（`newAgentExecutionRegistry` 的零值兜底） |
| `backend/agent/internal/server/agent_execution_resume.go:222` | `agentExecutionTTL()`（`agentExecutionStore` 的 TTL，包初始化时取表快照） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:236`、`:245` | `server.HTTPShutdownTimeout()` → `shutdownHTTPServer(srv, budget)`（原 `10 * time.Second` 字面量已删除） |
| `backend/agent/internal/server/instance_retention.go:99` | `PlatformLoadSampleRetention`（`platform_load_samples` 清理窗口） |
| `backend/agent/internal/server/audit_purge.go:481`、`:516` | `AuditRetentionInterval()` / `ExpiredTokenSweepInterval()` |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:69` | `TargetQueueTimeout()`（`-target-queue-timeout` 默认值） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:146` | `AgentHeartbeatFlush()`（flusher 周期） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:149-150`、`:169`、`:173` | `AuditRetentionInterval()` / `ExpiredTokenSweepInterval()`（启动与日志） |
| `backend/agent/internal/server/edge_agent.go:31` | `EdgeDialTimeout` |
| `backend/agent/internal/server/edge_conn.go:129`、`:389` | `EdgeDialTimeout` + `edgeOpeningAckSlack` |
| `backend/agent/internal/server/edge_sched.go:116` | `EdgeDialTimeout` + `edgeOpeningAckSlack` |
| `backend/agent/internal/server/monitor_retention.go:332` | `MonitorRetentionMinInterval` |
| `backend/agent/internal/server/tool_deadline.go:37` | `ToolDeadlineMargin` |
| `backend/agent/internal/server/model_provider_delivery.go:61` | `CredentialDeliveryTimeout` |
| `backend/agent/internal/server/registry_auth_delivery.go:95` | `CredentialDeliveryTimeout` |
| `backend/agent/internal/server/gateway_store_mysql.go:67` | `MySQLConnectTimeout` |
| `backend/agent/internal/server/gateway_store_mysql_runtime.go:155` | `MySQLConnectTimeout` |
| `backend/agent/internal/server/gateway_mysql_copy_command.go:313` | `MySQLConnectTimeout` |
| `backend/agent/internal/server/gateway_store_mysql.go:88` | `MySQLConnectTimeout`（`mysqlPingTimeout()`：ping 与拨号共用一个旋钮，ping 不许比拨号更短） |
| `backend/agent/internal/server/gateway_store_mysql_runtime.go:164`、`:376` | `mysqlPingTimeout()` |
| `backend/agent/internal/server/gateway_mysql_copy_command.go:322` | `mysqlPingTimeout()` |
| `backend/agent/internal/server/audit_scrub.go:170` | `mysqlPingTimeout()` |
| `backend/agent/internal/server/runtime_limits.go:57`、`:61` | `timeouts.ToolExecutionTimeout`、`timeouts.CompletedTaskMaxAge`（原来每次调用读 env 的 `durationOverride` 第二轨已删除，两个第二份默认值常量一并删除） |
| `backend/agent/internal/server/edge_config.go:35`、`:39`、`:43` | `timeouts.EdgeWriteStall` / `timeouts.EdgeConnIDQuarantine` / `timeouts.EdgeHelloTimeout`（原来的 `positiveEnvDuration` 每调用读取已删除） |
| `backend/agent/internal/server/edge_listener.go:63` | `edgeHelloTimeout()`（握手阶段给整个 hello 交换设的 socket 死线） |
| `backend/agent/internal/server/edge_sched.go:108`、`:112` | `edgeWriteStall()` / `edgeConnIDQuarantine()`（调度器把一个连接挂起、把 id 放进隔离表时取的值） |
| `backend/agent/internal/server/edge_conn.go:386` | `defaultTimeouts().EdgeConnIDQuarantine`（连接 id 表的零值兜底，不再引用已删常量） |
| `backend/agent/internal/server/ws_transport.go:147` | `timeouts.EdgeWSReadTimeout`（`edgeWSReadTimeout()`；Edge websocket 的空闲读死线，与隧道死线分开） |
| `backend/agent/internal/server/edge_ws.go:28`、`:79`、`:82` | `edgeWSReadTimeout()`（pong 处理器、readLoop 起始、每条消息读之前各刷一次死线） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:64` | `OperationTimeout()`（`-operation-timeout` 默认值） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:65` | `DetachedTaskTimeout()`（`-detached-task-timeout` 默认值） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:71` | `TargetReconnectGrace()`（`-target-reconnect-grace` 默认值） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:72` | `ShutdownDrainTimeout()`（`-shutdown-drain-timeout` 默认值） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:75` | `MonitorTick()`（`-monitor-tick` 默认值） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:147` | `OfflineReaperInterval()`（离线实例回收器周期） |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go:258-259` | `HTTPReadHeaderTimeout()` / `HTTPIdleTimeout()`（`gatewayHTTPServer`） |
| `backend/agent/internal/server/instance_retention.go:60`、`:105`、`:132`、`:142` | `offlineInstanceRetention()` → `timeouts.OfflineInstanceRetention`（删除截止时间、日志、连接事件清理） |
| `backend/agent/internal/server/instance_retention.go:121` | `timeouts.OfflineReaperInterval`（reaper 无参兜底） |
| `backend/agent/internal/server/audit_purge.go:209` | `timeouts.AuditPurgeBatchPause`（批间暂停） |
| `backend/agent/internal/server/audit_purge.go:267` | `timeouts.AuditPurgeBatchPause`（`discardReceipt` 的退避单位：`attempt × 该值`，单位与批间暂停同一个"清理器能等多久"的旋钮） |
| `backend/agent/internal/server/ws_transport.go:141` | `TunnelReadTimeout`（`agentTunnelReadTimeout()`；每个连接接进来时取一次快照，读死线与 pong 处理器共用） |
| `backend/agent/internal/server/execution_resume.go:758` | `ExecutionResumeWaitMax`（`executionWaitBudget()` 对 `?wait_ms` 的上限） |

行为证据（非默认值真的改变动作，`backend/agent/internal/server/timeout_wiring_test.go`）：
`TestToolDeadlineMarginComesFromTheTable`（10s 预算 + 1s margin → 9s）、
`TestMonitorRetentionThrottleComesFromTheTable`（1s 窗口下 500ms 被节流、1.5s 放行）、
`TestMCPProbeTimeoutComesFromTheTable`（30ms 超时在 300ms 服务器上快速失败，2s 超时成功）、
`TestMySQLConfigTakesConnectTimeoutFromTheTable`（driver `cfg.Timeout` 跟随表）、
`TestMonitorTickDrivesScheduledRuns`（50ms tick 下把计划连续改回到期，2s 内跑出两次 run）、
`TestOfflineInstanceRetentionComesFromTheTimeoutTable`（1s 保留窗口删掉 5s 前的离线实例，
默认 90 天保留）、`TestGatewayServeTimeoutTableReachesStartupLogs`（真实 serve 进程的启动日志
打出 `tick=1s`、`retention=1m30s, interval=2s`）、
`TestGatewayServeShutdownDrainTimeoutBoundsShutdown`（1s 排空预算 + 1 个永不完成的在途操作，
1.14s 内退出并打出 `waiting up to 1s for 1 active`）、
`TestGatewayServeDurationsComeFromTheTimeoutTable`（扫描 `gatewaycmd.go`，五个 flag 的默认值必须
是 `server.*()`，reaper 与 HTTP 两处同理）。

`TestMySQLPingBudgetComesFromTheTable`（50ms 表值 ping 一个不说话的口，51ms 失败；
旧 10s 字面量要等 10s）、`TestHubZeroOptionsTakeBudgetsFromTheTable`（env 7m/3h/17s 经
`loadTimeouts` 后，零值 opts 的 hub 取到 7m/3h/17s，而不是 60m/24h/10s）、
`TestWiredTimeoutSitesKeepNoHandWrittenLiteral`（六个站点文件里回写 `10 * time.Second`、
`60 * time.Minute`、`24 * time.Hour`、`50 * time.Millisecond` 就点名 file:line）。

## 有意不进表的常量

这些值不是"第二份默认值"：它们要么是派生量，要么是产品/安全策略，所以留在代码里，表改了它们也
不会跟着漂移。它们不算漏接线，改它们需要单独的理由，而不是一个 env。

| 常量 | 位置 | 为什么不进表 |
|---|---|---|
| `edgeOpeningAckSlack = 1s` | `backend/agent/internal/server/edge_config.go:18` | 是加在 `EdgeDialTimeout` 之上的握手余量（`EdgeDialTimeout + edgeOpeningAckSlack`），属于派生量而不是第二个默认值。若它单独进表，运维把拨号预算调大后余量仍是独立默认值，两个旋钮会各自漂移；保持一致的方式是永远跟着拨号预算算。使用点 `edge_conn.go:129`、`:389`、`edge_sched.go:116`。 |
| `ActiveInstanceWindow = 7 * 24h` | `backend/agent/internal/server/instance_retention.go:16` | 决定默认实例列表的可见范围（"最近 7 天活跃"），是产品可见性策略，不是"一次操作能等多久"；记录保留窗口本身由 `OfflineInstanceRetention` 表字段驱动。 |
| `DefaultLoginTokenTTL = 30 * 24h` 与 `30d` 查询窗口 | `backend/agent/internal/server/login_session.go:11`、`monitor_metric_store.go:106`、`platform_monitor_http.go:23` | 登录令牌有效期与指标查询区间是认证/查询语义，改它等于改安全策略与可见范围，需要单独评审；它们不描述"等待多久"，不应藏在超时表的 env 后面。 |
| `terminalReadWaitMax = 10s` | `backend/agent/internal/server/terminal_session.go` | `doops_terminal op=read` 单次长轮询 `wait_ms` 的协议级硬顶，远低于 Gateway `operation-timeout`，防止单次读调用触发 `toolDeadline`；与同文件的 `terminalIdleLimit`/`terminalCloseGrace` 同属终端内部固定边界，不作为运维可调 env。 |

## 1. 非法值就是启动失败（fatal，不回落）

进程启动时 `loadTimeouts` 读一次环境、逐项校验（可解析、> 0、在上表范围内），并校验第 4 节的
不等式；任何一项不满足都 `log.Fatalf`，进程**起不来**，错误文本点名变量、给到的值与合法范围：

```
gateway_timeout_config_invalid: DOOPS_HELM_MAX_WAIT="3h" is outside the allowed range 1s..2h (bounds one doops_helm call)
gateway_timeout_config_invalid: DOOPS_EDGE_DIAL_TIMEOUT="soon" is not a Go duration such as 30m or 90s; allowed range 100ms..5m (bounds the Edge dial and handshake budget)
gateway_timeout_config_invalid: helmSettleReserve (DOOPS_HELM_SETTLE_RESERVE=90s) must be at least helmTerminationGrace (30s) + helmSettleTimeout (DOOPS_HELM_SETTLE_TIMEOUT=45s) = 1m15s
```

为什么 fatal：静默回落是今天误配不可见的根因——值写错时进程照常跑，只是用默认值在真机上
按另一套预算工作，事后从日志里查不出来。起不来是可见、可修的；跑着用错值是隐性事故。

## 2. 六个"裸秒"兼容变量

`DOOPS_TOOL_TIMEOUT`、`DOOPS_COMPLETED_TASK_MAX_AGE`、`DOOPS_TUNNEL_READ_TIMEOUT`、
`DOOPS_EDGE_WRITE_STALL`、`DOOPS_EDGE_CONN_ID_QUARANTINE` 与 `DOOPS_EDGE_HELLO_TIMEOUT` 一直经由
`positiveEnvDuration` 读取，接受裸数字并按**秒**解释（`DOOPS_TOOL_TIMEOUT=1800` 等于 `30m`，
`DOOPS_TUNNEL_READ_TIMEOUT=90` 等于 `90s`）。这段兼容**保留**，
新增变量只接受 duration 字符串。若把裸数字改成"解析失败就回落默认值"，会让现有部署与测试
"数值巧合地绿、语义已错"（1800 恰好等于 30m 的秒数），因此必须有一条测试同时钉住
`1800` 与 `30m` 都等于 30m。

这六个变量也**只在进程启动时读一次**：INT-106 之后 `positiveEnvDuration` 已删除，
`TestRetiredEnvReadersStayOutOfTheSources` 禁止它和 `durationOverride` 回到生产源码。
启动后 `t.Setenv` 不再影响任何 getter，值写坏也不可能在运行中回落——它在启动时就让进程起不来。

## 3. 禁止热改

所有值在进程启动时定型，运行中不重新读取、不提供 rescale 入口：

- `helmToolDeadline()` 与 `MCPToolTimeout − MCPCallSettle` 是从这些值**派生**的预算，热改会让同一个请求的两个阶段
  拿到两套预算，证据自相矛盾；
- `doSQLDirectStatementT` 会被拼进 SQL 的 `SET statement_timeout`，热改等于在会话中途改数据库侧预算；
- 热改需要额外的锁与同步，收益为零：改预算本来就该走重启。

表 1 里不存在任何"每调用读 env"的取值路径（第 2 节）；`t.Setenv` 在启动之后不再影响任何
getter，热改只能通过重启生效。

## 4. 隐式不等式清单

这些关系今天多数只靠注释与巧合维持：既有的是 Helm 预算的两条测试断言
（`helm_interrupt_test.go:317-326`，`HelmMaxWait < helmToolDeadline()` 且
`helmToolDeadline()+HelmTerminationGrace+HelmSettleTimeout ≤ MCPToolTimeout`，现在两者都从表取），
它跑在测试里、不在启动路径上。B-13 把 I1 / I2 / I3 落成**启动校验**（比那两条测试更严：多出
reserve 余量与 MCP 预算的比较），其余标注原因。

| 编号 | 不等式 | 状态 |
|---|---|---|
| I1 | `HelmTerminationGrace(30s) + HelmSettleTimeout(45s) ≤ HelmSettleReserve(90s)`；`HelmMaxWait(25m) + reserve(90s) ≤ helmToolDeadline()(28m30s)`；`helmToolDeadline() ≤ MCPToolTimeout−MCPCallSettle(29m30s) ≤ DOOPS_TOOL_TIMEOUT(30m)` | **已断言**：启动校验 `validateTimeoutOrder`（本文件第 1 节第三条错误样例就是它）；测试 `helm_interrupt_test.go:317-326` 另有一份更弱的既有断言 |
| I2 | Agent 硬顶 `DOOPS_TOOL_TIMEOUT` 对 Gateway 广告超时的方向性：`margin < 硬顶`，且 margin 只在 Gateway 更短时生效 | **部分断言**：`margin < DOOPS_TOOL_TIMEOUT` 与 `margin < MCPToolTimeout−MCPCallSettle`；"Gateway 更短才生效"的分支逻辑在 `tool_deadline.go:34-51`，margin 本身已接表（`TestToolDeadlineMarginComesFromTheTable`），分支方向性仍只有单元级证据 |
| I3 | 心跳：ping 周期 = 租约/3，Gateway 读超时 = 租约，flusher 周期 30s | **已接线 + 断言**：`DOOPS_AGENT_HEARTBEAT_FLUSH ≤ DOOPS_AGENT_LEASE/3`；租约、flusher 周期与队列等待都读表，行为断言见 `timeout_wiring_int96_test.go` |
| I4 | 重连续接的 grace / TTL / 回放等待 / 取消确认四个值互不关联 | **部分断言**：grace、TTL 与 Agent 侧 TTL 三条已进表并由 I9 约束；"回放等待"与"取消确认"仍各自是独立常量，没有不等式，也没有行为断言 |
| I5 | `tunnel_hub` 的 `TargetQueueTimeout` 默认 2m 是死默认值（flag 总覆盖它）；`DetachedTaskTimeout` 24h 有两份 | **已接线 + 断言**：`-operation-timeout` / `-detached-task-timeout` 的默认值读表（`gatewaycmd.go:64-65`），hub 的零值兜底也改成读表（`tunnel_hub.go:85`、`:96`、`:109`，不再是 60m / 10s / 24h 字面量），行为断言见 `timeout_wiring_int101_test.go` 的 `TestHubZeroOptionsTakeBudgetsFromTheTable`；第二份 24h（`defaultDetachedTaskTimeout`）已删除 |
| I6 | Edge：dial 5s 与 `-edge-dial-timeout` 5s 两份；预算另加 `edgeOpeningAckSlack` 1s | **未断言**：`edgeOpeningAckSlack` 在本轮仍**不进表**（它是握手预留余量而不是可运维预算，进表需要同时定义它与 dial 的不等式）；两份 5s 的归一化依赖 flag 解析 |
| I7 | `AuditRetentionInterval` 6h 同时驱动审计清理与过期 token 清理；`-audit-retention` 只管"删多旧"不管"多久跑一次" | **已拆分**：清理周期拆成 `AuditRetentionInterval` 与 `ExpiredTokenSweepInterval` 两个字段、两个 env，各自独立；`-audit-retention` 语义不变；独立性只有 getter 级证据，没有执行级用例 |
| I8 | `OfflineInstanceRetention=90d` 旁边，`platform_load_samples` 删除窗口写死 `30*24h` 字面量 | **已接线 + 断言**：两条保留窗口（`DOOPS_OFFLINE_INSTANCE_RETENTION` 90 天、`DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION` 30 天）都读表；行为断言见 `timeout_wiring_int96_test.go`（1h 表值只留 30m 前的样本） |
| I9 | `ExecutionResumeGrace ≤ ExecutionResumeTTL`：等待窗口不能超过它所等待的记录的有效期，否则 Gateway 会在记录已被回收之后还在等 | **已断言**：启动校验 `validateTimeoutOrder`（第 1 节错误样例）；反向实验把 grace 设到 TTL 之上即 `gateway_timeout_config_invalid: executionResumeGrace ... must not exceed executionResumeTTL` |
| I10 | `TunnelReadTimeout ≥ AgentLease/3`：读死线不能短于 ping 周期，否则一条健康的隧道会在两次 ping 之间被切断 | **已断言**：启动校验 `validateTimeoutOrder`（`DOOPS_TUNNEL_READ_TIMEOUT=2s` 对缺省租约报 `tunnelReadTimeout ... must be at least the heartbeat ping period`）；行为断言 `TestTunnelReadTimeoutComesFromTheTable` |
| I11 | `ExecutionResumeWaitMax ≤ ExecutionResumeGrace`：一次 HTTP 阻塞等待不能长过它等待的记录窗口 | **已断言**：启动校验 `validateTimeoutOrder`；行为断言 `TestExecutionResumeWaitBudgetComesFromTheTable`（50ms 表值截断 5000ms 请求）、`TestExecutionResumeHandlerUsesTheBudgetHelper`（call site 仍走 helper） |
| I12 | `EdgeConnIDQuarantine ≥ EdgeDialTimeout + edgeOpeningAckSlack`：隔离窗口必须覆盖一次仍在途的握手，否则 id 会被复用、旧握手的应答落到新主人 | **已断言**：启动校验 `validateTimeoutOrder`；行为断言 `TestEdgeTimeoutOrderingIsChecked`（5s 拨号 + 1s 余量下 3s 隔离被拒） |
| I13 | `EdgeWSReadTimeout ≥ agentWSPingInterval(5s)`：读死线不能短于 Edge 自己的 ping 周期，否则健康会话会在两次 ping 之间被切断 | **已断言**：启动校验 `validateTimeoutOrder`；行为断言 `TestEdgeTimeoutOrderingIsChecked`（2s 读死线被拒） |

## 5. 验收命令

```
cd backend/agent && env -u GOROOT go build ./... ; echo rc=$?
cd backend/agent && env -u GOROOT go vet ./internal/server/
cd backend/agent && env -u GOROOT go test ./internal/server/ -run 'Timeout|RuntimeLimit|ToolDeadline|Helm|MCP|Retention|Edge|Credential' -count=1
```

反向实验（必须真红）：

1. 把表里某个默认值改 1 秒（例如 `ToolExecutionTimeout` 30m → 1s）→ `TestTimeoutDefaultsMatchDoc` 红。
2. 删掉 `validateTimeoutOrder` 里的 I1 检查并给一组违反链的值 → 启动校验用例红。
3. 把非法 duration 改成"回落默认值" → `TestTimeoutDefaultsRejectInvalidValues` 红。
4. 往任一生产文件加回旧常量（例如 `helm_tool.go` 里 `const helmSettleTimeout = 90 * time.Second`）→ `TestRetiredTimeoutConstantsStayOutOfProductionSources` 红并点名 file:line。
5. 把 `tool_deadline.go` 的 margin 改回字面量 `5 * time.Second` → `TestToolDeadlineMarginComesFromTheTable` 红（期望 9s，得到 5s）。
6. 把 `monitor_retention.go` 的 throttle 改回字面量 `15 * time.Minute` → `TestMonitorRetentionThrottleComesFromTheTable` 红（1.5s 的重试仍被节流）。
7. 把 `gatewaycmd.go` 的 `-monitor-tick` 默认值改回 `15*time.Second` → `TestGatewayServeDurationsComeFromTheTimeoutTable` 红：`flag "monitor-tick" defaults to "15*time.Second", want "server.MonitorTick()"`。
8. 把 `gatewaycmd.go` 的 `-shutdown-drain-timeout` 默认值改回 `2*time.Minute` → 同一用例红：`flag "shutdown-drain-timeout" defaults to "2*time.Minute", want "server.ShutdownDrainTimeout()"`。
9. 把 `instance_retention.go` 的 `offlineInstanceRetention()` 改回 `90 * 24 * time.Hour` → `TestOfflineInstanceRetentionComesFromTheTimeoutTable` 红：`offline row survived a 1s retention window`。
10. 把 `edge_config.go` 任一访问器改回 `positiveEnvDuration(...)`（或重新引入该函数）→ `TestRetiredEnvReadersStayOutOfTheSources` 红并点名 `file:line`。
11. 把某个访问器改成恒返回 `defaultTimeouts().<字段>`（即解析了表值却不用）→ `TestEveryEnvSpecIsReadThroughTheSnapshot` 红：`<ENV> resolves into <字段> but no runtime source reads timeouts.<字段>`。
12. 删掉 I12 或 I13 的 `validateTimeoutOrder` 分支 → `TestEdgeTimeoutOrderingIsChecked` 红（3s 隔离 / 2s 读死线被接受）。
13. `DOOPS_EDGE_WRITE_STALL=5q` 启动 Gateway → 进程起不来，stderr 打 `gateway_timeout_config_invalid: DOOPS_EDGE_WRITE_STALL="5q" ... allowed range 100ms..10m`，不再是静默回落。