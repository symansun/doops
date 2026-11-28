# DoOps Webhook 出站通知：设计任务书

编制日期 2026-11-24，P0 实现 2026-11-26。状态：P0 的 Gateway API、worker、事件生产者和 CLI 已实现；
P1 与 Web 控制台（§10.3）待做。本文同时是设计任务书和 P0 实现记录；P1 落地时按 §16 逐项拆分，
并同步更新各自的权威文档。已知 P0 边界：`execution.outcome_unknown` 与启动恢复仍非事务/不补事件，
Agent 上下线为 best-effort，详见 §7.3。

## 1. 结论

在 Gateway 内新增一个**出站 Webhook 模块**，把 DoOps 已经发生、且已经写入数据库的状态变化，
以签名 HTTP POST 的方式通知管理员配置的外部端点。

方案由四部分组成：

1. **事务型 outbox**：新增 `platform_events`、`webhook_endpoints`、`webhook_subscriptions`、
   `webhook_deliveries` 四张表。执行终态、告警与巡检把业务状态变更和事件写入放在同一个数据库事务内，
   避免“状态变了、通知没发”；`outcome_unknown` 与 Agent 上下线在 P0 仍是提交后 best-effort（§7.3）。
2. **单一 outbox worker**：只消费自己的投递队列，按持久化的 `next_attempt_at_ms` 唤醒；
   不新增 Gateway 调度器，不使用周期 ticker，不扫描 `monitor_schedules`。
3. **签名投递**：HMAC-SHA256 签名、时间戳、事件 ID、投递 ID、尝试次数；至少一次投递，接收方按事件 ID
   幂等。HTTPS-only、禁重定向、DNS 解析后逐 IP 校验并钉住 IP，防 SSRF 与 DNS rebinding。
4. **平台管理员管理面**：Admin API、CLI、Web 控制台。Webhook URL 与签名密钥写后不可读；列表只显示
   host；密钥只在创建/轮换时返回一次。

v1 只做**出站通知**，不做入站 Webhook 触发 DoOps；不做按用户订阅；不做自定义请求头；不做
“保证一次”或“全局有序”。

## 2. 背景与目标

### 2.1 背景

[监控、巡检与资产感知闭环](MONITORING_INSPECTION_LOOP.md) 的 D5 已经提出：事件闭环需要
“接入 -> 采集 -> 展示 -> 判定 -> 事件 -> 通知 -> 认领整改 -> 复检 -> 关闭 -> 报表”，
其中通知一段由 Gateway 向管理员配置的 Webhook 出站；设计时 Gateway 没有通知出站，这是新增行为，
备选方案是只在控制台/CLI 查事件。P0 已按本文实现四张表、outbox 与管理员 API/CLI（§14）。

P0 之前代码没有任何 Webhook、outbox 或通知出站实现，事件只落在各自的表里；下表仍用于说明
事件源与 P1 的接入位置：

| 事件源 | 现状 | 相关位置 |
| :--- | :--- | :--- |
| 执行终态 | `audit_events` 的 `success/error/outcome_unknown` | `execution_audit.go`、`client_service.go` |
| 后台任务终态 | `detached_tasks` 的 `status` | `detached_tasks.go` |
| Agent 上下线 | `agent_connection_events` | `gateway_store_agents.go` |
| 阈值告警 | `asset_monitor_alerts` 的 `open/recovered/interrupted/reconfigured` | `asset_monitor_store.go` |
| 巡检 run | `monitor_runs` 及计数 | `monitor_engine_store.go` |
| 凭据生命周期 | `audit_events` 的 `credential:*` 动作 | `credential_store*.go` |

这些表各自有保留策略，`audit_events` 还会被清理；直接把审计表当事件源会把“通知”绑死在审计保留期
和审计语义上。因此需要独立的事件表与投递表。

### 2.2 目标

- 状态变化发生后，在秒级内把事件推送到管理员配置的 HTTPS 端点。
- 投递失败可重试、可观察、可人工重放；进程重启不丢待投递事件。
- 接收方可以验证来源与完整性，并按事件 ID 去重。
- 事件只含元数据，不含凭据、命令输出、文件内容、错误正文或完整 URL。
- 不新增 Gateway 调度器，不改变现有授权模型，不引入外部消息队列。
- 管理动作全部走平台管理员权限并进入现有管理审计。

### 2.3 非目标

- **入站 Webhook**：不提供“外部调用 DoOps 触发部署/巡检”的公网入口。那会引入公网鉴权、重放防护、
  操作白名单和目标授权映射，必须单独立项。
- **按用户订阅**：v1 的端点由平台管理员拥有，事件流是平台级的。按用户端点需要把事件可见性映射到
  `(user, cluster, instance)` 授权，等于新增权限维度，另案设计。
- **精确一次、全局有序**：只保证至少一次；顺序不保证。接收方用 `occurred_at` 和事件 ID 自行处理。
- **业务编排**：DoOps 不等待接收方完成业务动作，不解释响应体；2xx 只表示“已接收”。
- **自定义请求头、模板、Slack/飞书/钉钉适配器**：v1 只发标准 JSON；格式化由接收方完成。

## 3. 设计依据的架构事实与约束

### 3.1 Gateway 的角色

Gateway 负责用户认证、目标授权、路由、审计和持久化；Agent 只在目标网络内执行，不持有 Gateway
用户 token。Webhook 是 Gateway 的出站集成，不经过 Agent，也不把 URL 或密钥下发到 Agent。

### 3.2 调度器约束

仓库契约规定：Gateway 唯一被认可的调度器是 monitor engine（`StartMonitorRunner`、
`-monitor-tick`），只用于派发到期的 `monitor_schedules`；不得新增其它 Gateway 调度器或周期 job loop。

本设计的 outbox worker **不是**监控调度器，也不引入新的周期任务：

- 它不读取任何调度计划，只消费自己产生的事件投递行；没有待投递行时 goroutine 阻塞在唤醒信号或
  `ctx.Done()`，不扫描数据库。
- 它只有一个指向“最早一条待投递记录”的**单次** timer；timer 到期是为了发送已经存在的重试行，
  不是按固定间隔触发。没有 `time.Ticker`、没有 cron、没有 5 分钟安全轮询。
- 启动时做一次恢复扫描，接管 `pending` 和租约过期的 `delivering`；多副本靠条件 UPDATE 认领互斥，
  替代副本启动时完成租约回收。
- 保留清理只在启动和 worker 被唤醒时按小时节流执行，不设独立定时器；空闲期不清理，下一次事件或
  管理面访问会补做。
- `webhook*.go` 禁止 `time.Ticker`、`time.Tick` 和固定间隔循环；源码扫描测试固定这一约束。

### 3.3 授权模型

授权单位只有 `(user, cluster, instance)`；有目标授权即可做该目标上的全部操作。Webhook 管理不新增
按操作、按资源、按事件的权限维度：**只有平台管理员**可以创建、修改、删除、轮换、测试端点，以及
查看投递记录。普通用户即使拥有目标授权，也不能配置平台级出站。

### 3.4 审计与凭据边界

- 管理面写操作复用 `auditAdminMutation`（`admin_audit.go`）：GET/HEAD 要求管理员；写方法先读 body、
  写管理审计，再执行 handler。审计元数据只允许 route、method、资源 ID、user ID、enabled/disabled、
  HTTP 状态，不允许 URL、密钥、请求/响应正文。
- 现有 `adminAuditIDPattern` 只接受 `usr_/tok_/repo_/route_/job_/sw_/op-/audit-` 前缀；落地时需要把
  `wh_[a-f0-9]{24}`、`dlv_[a-f0-9]{24}` 加入该模式，否则管理审计里看不到资源 ID。
- Webhook URL 往往把令牌放在 path/query 里（例如 Slack incoming webhook），因此 URL 本身按秘密处理：
  AES-GCM 加密存储，列表只显示 host，不写日志、不进审计、不回显。
- 签名密钥由 Gateway 生成，32 字节随机数，hex 编码；只在创建/轮换响应中返回一次。
- 事件 payload 仅元数据。禁止把 `command_summary`、`tail`、`error` 原文、凭据值、请求/响应正文、
  prompt、文件内容放进事件。

### 3.5 数据库与方言

- Gateway 使用 SQLite 内嵌 schema，同时维护 MySQL 镜像 `gateway_mysql_schema.sql`；两套都要加表，
  并遵守 MySQL 迁移/冻结导出的约束。
- MySQL 镜像明确不使用 JSON 列，JSON 一律存 `TEXT`，运行时校验；本设计沿用。
- 时间同时存 `*_at`（RFC3339 文本，给人看）和 `*_at_ms`（整数，给索引和排序）。
- ID 统一 `VARCHAR(64)`；业务 ID 用 `randomHex(12)` 生成 24 位 hex。
- 现有 upsert 渲染器 `gatewayUpsertSQL`、方言判断 `gatewayDialect()` 可复用；需要避免 MySQL 不支持的
  `RETURNING`、`INDEXED BY` 等写法。

### 3.6 出站 HTTP 的现状

`asset_monitor_collect.go` 的 `collectAgentAppMetricsJSON` 已经有一套安全 client 雏形：5 秒超时、
不跟随重定向、TLS 1.2、`DialContext` 解析后逐 IP 检查。但它的 `isDisallowedProbeIP` 只挡
LinkLocalUnicast/Multicast、Unspecified、Multicast，**不挡 loopback、RFC1918、ULA、CGNAT**，
不能直接用于 Webhook。本设计在它之上补齐拒绝网段、DNS pinning、禁代理、禁压缩等规则（§9）。

### 3.7 入口

Gateway 路由集中在 `tunnel_hub.go` 的 `RegisterRoutes`。管理路由通过：

```go
admin := func(path string, handler http.HandlerFunc) {
    mux.HandleFunc(path, h.auditAdminMutation(path, handler))
}
```

注册。CLI 入口在 `backend/cli/cli/doops/main.go` 的 `admin` 分支；Web 控制台在 `app/`、`components/`、
`lib/`。落地时 `tunnel_hub.go` 当前有其它会话的未提交改动，路由注册要与该会话协调后再改。

## 4. 设计原则

1. **事件与状态同事务**：能在业务事务里写的事件，绝不放到事务外“补写”。核心状态（告警、巡检 run、
   执行终态、凭据）必须原子；连接上下线是 best-effort（见 §7.3）。
2. **事件是审计的派生，不是替代**：投递失败不影响业务状态，也不删除审计；审计仍是权威记录。
3. **元数据最小化**：按事件类型定义字段白名单，用 Go struct 序列化，禁止 map 透传。
4. **出站失败关闭**：URL 解析、DNS、IP 策略任一步不通过就不发请求；策略拒绝是永久失败，不重试。
5. **管理员拥有集成面**：端点、密钥、投递记录只对平台管理员可见；不新增权限维度。
6. **至少一次 + 接收方幂等**：重试复用同一个 `delivery_id` 和 `event_id`；接收方按事件 ID 去重。
7. **无新调度器**：单一 outbox worker、持久化下次尝试时间、事件唤醒；无 ticker、无 cron。
8. **可运营**：队列深度、最老待投递时长、失败分类、最后成功/失败时间可查；日志只含 ID 和状态。

## 5. 总体架构

```
业务事务                          outbox worker                    外部接收方
─────────                         ────────────                     ──────────
状态变更 ──┐
           ├─ platform_events ──┐
           ├─ webhook_deliveries┼─ 认领到期行 ── HTTPS POST ──>  /webhook
           └─ (同事务提交)       │   成功: delivered
                                 │   失败: 退避重试 / failed
                                 └─ 唤醒信号 / 最早到期时间

管理面: Admin API ── CLI / Web 控制台
        webhook_endpoints / webhook_subscriptions
```

一次事件的生命周期：

1. 业务代码在事务内写状态，并调用 `enqueuePlatformEvent`。
2. `enqueuePlatformEvent` 插入 `platform_events`（带 `dedupe_key`），再按订阅关系插入
   `webhook_deliveries`（每个启用端点一条，初始 `pending`）。
3. 事务提交后调用 `Wake()`（非阻塞）。提交失败则事件和投递一起回滚。
4. worker 认领到期投递，构造固定请求，签名并发送。
5. 成功写 `delivered`；失败按分类写 `pending + next_attempt_at_ms` 或 `failed`。
6. 进程重启后，未完成投递仍在表中；过期的 `delivering` 租约被重新认领。

### 5.1 worker 循环与认领

worker 是进程内单实例 goroutine（多副本 Gateway 靠数据库认领互斥，不靠进程内锁）：

```go
func (w *webhookWorker) run(ctx context.Context) {
    w.resume(ctx) // 启动扫描：pending + 过期 delivering；按最早到期时间设置单次 timer
    for {
        w.cleanupIfDue(ctx) // 每个唤醒周期最多清理一次；没有独立清理定时器
        w.dispatchDue(ctx)  // 只认领当前已到期、且有空闲 worker 的行；不空转
        w.resetTimer()      // 指向新的最早到期行；没有待投递行就停表
        select {
        case <-ctx.Done():
            return
        case <-w.wake: // 入队提交后、重试写回后、管理面重放/启停后
        case <-w.timerC: // 最早一条重试到期；单次，不是固定 tick
        case <-w.done: // 某个投递结束，槽位归还；继续尝试到期行
        }
    }
}
```

`wake` 是容量 1 的非阻塞 channel：`enqueue` 提交后、管理面重放/启停端点、worker 自己写完重试时间后
都调用 `Wake()` 唤醒；重复唤醒被合并。没有待投递行时 timer 停掉，worker 只等 `wake`/`ctx.Done()`，
不产生空转查询。有未来重试时，timer 精确指向最早 `next_attempt_at_ms`，由该行自己的到期时间驱动，
不是固定间隔。

`dispatchDue` 先取空闲槽位，再认领已到期行；没有空闲槽位时不认领，`resetTimer` 也不把等待时间设为
0，而是等某个投递完成后的 `done` 信号，避免忙等和无界 goroutine。认领失败（另一个副本抢先）要归还
槽位。`resetTimer` 每次都重新查最早到期时间（`pending.next_attempt_at_ms` 与 `delivering.lease_until_ms`
取较小值），所以重试写回、管理面重放、端点启停都能把 timer 前移或后移；没有待投递行就停表，不保留
固定间隔。`resetTimer` 先 `Stop` 旧 timer 并排空 `timerC`，避免已经触发的旧信号造成一次多余唤醒。

worker 的生命周期挂在整个 Gateway 上，不挂在路由注册上：`gatewaycmd.go` 在 `NewGatewayHub` 之后调用
`hub.StartWebhookDispatcher()`，与现有 `StartAuditRetention` 等启动项同层；紧接着
`defer hub.StopWebhookDispatcher(server.HTTPShutdownTimeout())`，让正常 SIGTERM 和 `ListenAndServe`
提前失败两条返回路径都经过停止逻辑。停止时先拒绝新认领，再等待在途 HTTP 请求结束（受该超时限制），
并且排在更早注册的 `store.Close` defer 之前执行。复用超时表中的 `HTTPShutdownTimeout`（B-13 已引入；
若本模块先于 B-13 落地，则用同一个 10s 常量，不新增关闭专用环境变量）；超时仍未完成的行保持
`delivering`，由租约到期后的替代副本或下次启动回收；
`StopWebhookDispatcher` 可重复调用。

认领必须用条件 UPDATE，不能用“先 SELECT 再 UPDATE”：

```sql
UPDATE webhook_deliveries
SET status = 'delivering', lease_token = ?, lease_until_ms = ?
WHERE id = ?
  AND (status = 'pending'
       OR (status = 'delivering' AND lease_until_ms < ?))
```

`RowsAffected = 1` 才算认领成功；租约 50 秒，HTTP 超时 10 秒，留足写回时间。进程崩溃后替代副本在
启动扫描时回收过期租约；存活副本不轮询其他副本的租约，这是为遵守“不新增周期任务”而做的取舍，
严格的多副本租约回收需要数据库通知或既有调度器提供 wake，列入 §17。发送方无法区分“对方已收到但
写回失败”和“没收到”，所以只能至少一次，接收方按 `event_id` 幂等。

### 5.2 生产者接口

生产者只写 outbox，不发 HTTP：

```go
type PlatformEvent struct {
    Type          string    // 事件类型，必须在事件目录内
    OccurredAt    time.Time // 状态发生时间，不是写库时间
    ActorUserID   string
    Cluster       string
    Instance      string
    CorrelationID string
    Data          any       // 按事件类型定义的固定 struct，禁止 map
    DedupeKey     string    // 可选，生产者幂等键
}

func (s *gatewayStore) enqueuePlatformEvent(ctx context.Context, tx *sql.Tx, ev PlatformEvent) (string, error)
```

- `tx` 必须是调用方的业务事务；函数只做 envelope 序列化、`platform_events` 插入和按订阅 fan-out，
  不 commit、不发请求。每个调用点必须在 `tx.Commit()` 成功之后调用 `Wake()`，不能在 commit 前唤醒；
  提交失败则事件与投递一起回滚。启动恢复、管理面重放和 `webhook.test` 同样遵守这个顺序。
- `Data` 用具体 struct 序列化，事件类型到 struct 的映射集中在一处；未知类型直接报错，防止拼写错误
  产生永远没人订阅的事件。
- 核心生产者把 `enqueuePlatformEvent` 的错误当业务错误处理（回滚）；best-effort 生产者自己开一个短事务，
  失败只记日志。

## 6. 数据模型

所有表同时出现在 SQLite schema 与 `gateway_mysql_schema.sql`。JSON 字段一律 `TEXT`，不建 JSON 列，
不建生成列，不使用 MySQL 不支持的默认值表达式。

### 6.1 `platform_events`

| 列 | 类型 | 说明 |
| :--- | :--- | :--- |
| `id` | VARCHAR(64) PK | `evt_<24hex>` |
| `type` | VARCHAR(64) NOT NULL | 事件类型，如 `monitor.alert.opened` |
| `occurred_at_ms` | BIGINT NOT NULL | 状态发生时间，用于排序和保留 |
| `occurred_at` | VARCHAR(32) NOT NULL | RFC3339Nano UTC 文本 |
| `actor_user_id` | VARCHAR(64) NOT NULL DEFAULT '' | 触发者；系统事件为空 |
| `cluster` | VARCHAR(64) NOT NULL DEFAULT '' | 事件作用域；全局事件为空 |
| `instance` | VARCHAR(64) NOT NULL DEFAULT '' | 同上 |
| `correlation_id` | VARCHAR(64) NOT NULL DEFAULT '' | 关联执行/请求；可为空 |
| `body_json` | TEXT NOT NULL | **完整 envelope 的最终字节**，发送时原样使用 |
| `dedupe_key` | VARCHAR(160) NULL | 生产者幂等键；空存 NULL，允许多行 NULL |
| `created_at_ms` | BIGINT NOT NULL | 入队时间 |

索引：

- `UNIQUE(dedupe_key)`；`dedupe_key` 为空时存 NULL，SQLite/MySQL 都允许多个 NULL。
- `dedupe_key` 冲突表示同一事件已入队：`INSERT ... ON CONFLICT DO NOTHING`（MySQL 用等价 upsert），
  插入 0 行时不重复 fan-out，按 `dedupe_key` 查已有 `id` 返回；调用方视为成功。
- `KEY idx_platform_events_time (occurred_at_ms, id)`
- `KEY idx_platform_events_type_time (type, occurred_at_ms)`

`body_json` 存完整 envelope 而不是只存 `data`，原因：

- 发送字节与落库字节完全一致，签名不会因为重新序列化而漂移；
- 人工重放/重试时使用同一份字节；
- 事件内容不可变，便于契约测试和取证。

body 上限 60 KiB（MySQL `TEXT` 上限 65,535 字节，留出余量）。事件字段都是固定小体积的 ID、枚举、
数字和时间戳，正常 envelope 约 0.5–2 KiB；超过 60 KiB 只能由代码错误（例如把自由文本塞进 `data`）
造成，按编程错误处理：`enqueuePlatformEvent` 返回错误并拒绝写入。核心生产者（告警、巡检、执行、
凭据）让所在事务回滚，不静默丢事件；best-effort 生产者（连接上下线、后台任务）记日志后跳过。两条
路径都有测试覆盖，见 §15。

### 6.2 `webhook_endpoints`

| 列 | 类型 | 说明 |
| :--- | :--- | :--- |
| `id` | VARCHAR(64) PK | `wh_<24hex>` |
| `name` | VARCHAR(128) NOT NULL | 管理员可读名称 |
| `description` | VARCHAR(512) NOT NULL DEFAULT '' | 备注；不含秘密 |
| `url_ciphertext` | TEXT NOT NULL | AES-GCM 加密的完整 URL；AAD 绑定端点 ID |
| `url_host` | VARCHAR(320) NOT NULL | 仅 host（可含端口），用于列表展示 |
| `url_sha256` | CHAR(64) NOT NULL DEFAULT '' | 完整 URL 的 SHA-256，用于重复提示；不唯一 |
| `secret_ciphertext` | TEXT NOT NULL | 当前签名密钥密文 |
| `previous_secret_ciphertext` | TEXT NOT NULL DEFAULT '' | 轮换过渡期的旧密钥密文 |
| `previous_secret_expires_at_ms` | BIGINT NOT NULL DEFAULT 0 | 旧密钥失效时间 |
| `secret_rotated_at` | VARCHAR(32) NOT NULL DEFAULT '' | 最近轮换时间 |
| `enabled` | TINYINT NOT NULL DEFAULT 1 | 0 = 停用 |
| `scope_cluster` | VARCHAR(64) NOT NULL DEFAULT '' | 空 = 全部；非空 = 仅该 cluster |
| `scope_instance` | VARCHAR(64) NOT NULL DEFAULT '' | 空 = 全部；非空 = 仅该 instance |
| `created_by` | VARCHAR(64) NOT NULL | 管理员 user ID |
| `created_at` | VARCHAR(32) NOT NULL | |
| `updated_at` | VARCHAR(32) NOT NULL | |
| `deleted_at` | VARCHAR(32) NOT NULL DEFAULT '' | 软删除；非空即不可见、不投递 |
| `last_success_at_ms` | BIGINT NOT NULL DEFAULT 0 | 运维展示 |
| `last_failure_at_ms` | BIGINT NOT NULL DEFAULT 0 | 运维展示 |
| `last_error_category` | VARCHAR(64) NOT NULL DEFAULT '' | 稳定分类，不含错误正文 |

索引：`KEY idx_webhook_endpoints_enabled (enabled, deleted_at)`、`KEY idx_webhook_endpoints_url (url_sha256)`。

约束：

- `name` 1..128，`description` <= 512，URL <= 2048，端点总数 <= 32。
- URL 与密钥使用 `encryptSecretWithAAD`：URL 的 AAD 为 `webhook:<id>:url`，密钥为
  `webhook:<id>:secret`，旧密钥为 `webhook:<id>:secret:previous`。这样同一行数据被搬到另一个端点
  时无法解密。
- 删除为软删除：`deleted_at` 置位、`enabled=0`、订阅保留；待投递行在同事务置为 `failed`，
  错误分类 `endpoint_disabled`。

### 6.3 `webhook_subscriptions`

| 列 | 类型 | 说明 |
| :--- | :--- | :--- |
| `endpoint_id` | VARCHAR(64) NOT NULL | |
| `event_type` | VARCHAR(64) NOT NULL | 精确匹配，不支持通配符 |
| `created_at` | VARCHAR(32) NOT NULL | |

主键 `(endpoint_id, event_type)`；索引 `KEY idx_webhook_subscriptions_event (event_type)`。
每个端点最多 32 个事件类型。v1 不支持通配符、正则或 JSONPath 过滤，避免把订阅变成查询语言。

scope 过滤语义：端点的 `scope_cluster` 为空表示接收所有事件；非空时只接收 `cluster` 完全相同的事件。
`scope_instance` 只在该 `scope_cluster` 内匹配，因此 **`scope_instance` 非空时 `scope_cluster` 也必须
非空**，否则拒绝 400 `webhook_scope_invalid`，避免同名 instance 跨集群误投。**不带 cluster 的全局事件
不会命中非空 scope 的端点**，这条规则在 API 文档中写明，避免“为什么我的全局事件没收到”。

### 6.4 `webhook_deliveries`

| 列 | 类型 | 说明 |
| :--- | :--- | :--- |
| `id` | VARCHAR(64) PK | `dlv_<24hex>`，见下 |
| `endpoint_id` | VARCHAR(64) NOT NULL | |
| `event_id` | VARCHAR(64) NOT NULL | |
| `status` | VARCHAR(16) NOT NULL | `pending/delivering/delivered/failed` |
| `attempt` | INT NOT NULL DEFAULT 0 | 已发起的尝试次数 |
| `next_attempt_at_ms` | BIGINT NOT NULL | 下次可认领时间 |
| `lease_token` | VARCHAR(64) NOT NULL DEFAULT '' | 认领令牌，防并发重复发送 |
| `lease_until_ms` | BIGINT NOT NULL DEFAULT 0 | 租约到期；过期可被重新认领 |
| `response_status` | INT NOT NULL DEFAULT 0 | 最近一次 HTTP 状态；网络错误为 0 |
| `error_category` | VARCHAR(64) NOT NULL DEFAULT '' | 稳定分类 |
| `manual_retry_count` | INT NOT NULL DEFAULT 0 | 人工重放次数 |
| `created_at_ms` | BIGINT NOT NULL | |
| `updated_at_ms` | BIGINT NOT NULL | |
| `delivered_at_ms` | BIGINT NOT NULL DEFAULT 0 | 成功时间 |

索引：

- `UNIQUE(endpoint_id, event_id)`：同一端点对同一事件最多一条投递行。
- `KEY idx_webhook_deliveries_due (status, next_attempt_at_ms, id)`
- `KEY idx_webhook_deliveries_endpoint_time (endpoint_id, created_at_ms)`
- `KEY idx_webhook_deliveries_event (event_id)`

投递 ID 用确定性派生，而不是随机：

```
dlv_ + first24hex(sha256(endpoint_id + "\x00" + event_id))
```

这样重复 fan-out、两个 Gateway 副本并发 fan-out、人工补发都不会产生第二条投递行；唯一约束是最后
一道防线。

### 6.5 保留与容量

- `platform_events`、`webhook_deliveries` 默认保留 720 小时（30 天），`-webhook-retention 0` 表示永久保留。
- 只删除终态投递；`pending/delivering` 永不删除。删除事件前必须确认没有任何投递行引用它
  （包括终态），因此事件与投递同批清理；否则人工重放会找不到原始 body。
- 清理在 worker 启动时执行一次；之后只在 worker 被唤醒时按“距上次清理 >= 1 小时”执行，批量
  5000 行；**不设独立定时器**。空闲期不清理，下一次事件或管理面访问补做；只删到期数据，不产生
  业务动作。管理面读取队列状态时顺带 `Wake()` 一次，给运维一个手动触发清理/恢复的入口。
- 每端点待投递上限 10000。超过上限时不丢事件：新投递直接写成 `failed`，分类 `queue_overflow`，
  管理员可见、可重放。这样背压不会静默丢通知。
- 粗略容量：事件 body 约 0.5–2 KiB，投递行约 200 B；100 万事件 + 每事件 2 个端点约 1.4 GB。
  30 天保留在常规运维量级下可控；大客户需要按 §17 的待决项调整。

### 6.6 与凭据系统的关系

D5 建议“地址作为受管凭据保存”。本设计**不复用 `credentials` 表**：

- 凭据资源的授权单位是 target，生命周期是 create -> put -> grant -> verify -> promote；Webhook 没有
  target，也不下发到 Agent。
- 把 Webhook URL 塞进 credentials 会制造一个没有 target 的伪凭据，破坏 grant/verify/promote 语义，
  并让凭据列表/授权界面出现无法解释的条目。
- 复用同一套 `secret_box` 加密原语和 AAD 绑定，达到“写后不可读、静态加密、可轮换”的效果，同时
  不引入伪凭据。

## 7. 事件目录与产生点

### 7.1 事件命名与版本

- 类型格式：`<domain>.<object>.<action>`，全小写，点分隔；例如 `execution.finished`。
- envelope 带 `api_version: "1"`；v1 内只做向后兼容的字段追加，接收方必须忽略未知字段。
- 破坏性变更升 `api_version` 或新增事件类型，不静默改字段语义。
- P0 的 `series_id` 仍是数据库自增数字；其余对外 ID（execution/run/schedule/delivery/endpoint）使用字符串，
  避免新增接口出现 JavaScript 大整数精度问题。后续若把 `series_id` 改为字符串，按 v1 兼容字段追加处理。

envelope：

```json
{
  "api_version": "1",
  "id": "evt_9f2c4a1b7d3e5f60718293a4",
  "type": "monitor.alert.opened",
  "occurred_at": "2026-11-24T22:53:00.123Z",
  "data": {}
}
```

`data` 按事件类型固定；不含自由文本。

### 7.2 事件清单

| 事件类型 | 阶段 | 触发点 | `data` 字段（白名单） |
| :--- | :--- | :--- | :--- |
| `execution.finished` | P0 | ClientService 执行终态；`FinalizeExecution` 的 admission 失败 | `execution_id`, `action`, `status`, `cluster`, `instance`, `actor_user_id`, `started_at`, `ended_at`, `duration_ms`, `error_category`（可空） |
| `execution.outcome_unknown` | P0 | `FinalizeExecution` 的已下发分支 | 同上，另加 `reason`：当前固定 `remote_unknown` |
| `agent.connected` | P0 | `MarkAgentOnline` | `cluster`, `instance`, `generation`, `release_commit`, `mode`, `enrolled` |
| `agent.disconnected` | P0 | `MarkAgentOffline` | 同上；不含 remote IP |
| `monitor.alert.opened` | P0 | `storeMonitorPoint` 插入 open 告警 | `series_id`, `cluster`, `instance`, `asset_type`, `asset_id`, `namespace`, `metric`, `device_id`, `state`（`opened`）, `opened_ms`, `ended_ms`（0）, `threshold`, `duration_seconds` |
| `monitor.alert.resolved` | P0 | `closeMonitorAlert` 关闭 open 告警 | 同上；`ended_ms` 为关闭时间，`state` 为 `resolved` |
| `inspection.run.finished` | P0 | `PersistMonitorRunResults` 提交前 | `run_id`, `schedule_id`, `status`, `trigger_kind`, `counts{targets,assets,passed,warning,failed,skipped,offline}`, `started_at`, `finished_at`, `duration_ms` |
| `background_task.finished` | P1 | `finishDetachedTask` | `audit_id`, `task_id`, `status`, `cluster`, `instance`, `actor_user_id`, `duration_ms` |
| `credential.created` | P1 | 凭据 create 事务 | `credential_id`, `name`, `type`, `owner_id`, `actor_user_id` |
| `credential.rotated` | P1 | 凭据 promote 事务 | `credential_id`, `type`, `actor_user_id` |
| `credential.granted` | P1 | 授权 create 事务 | `credential_id`, `target{cluster,instance}`, `grant_id`, `actor_user_id` |
| `credential.revoked` | P1 | 授权删除/资源撤销事务 | `credential_id`, `target{cluster,instance}`, `grant_id`, `actor_user_id` |

`webhook.test` 是内部事件类型，只用于端点自测，不进入订阅目录：它直接为指定端点创建一条投递，
`data` 为 `{endpoint_id, requested_by}`。不带自由文本 `message`，避免测试接口变成任意内容注入通道。
端点已停用或已软删除时，测试请求直接返回 409 `webhook_endpoint_disabled`，不写事件、不写投递；
自测只允许对启用中的端点发起。
测试也在一个事务里写一条 `type=webhook.test` 的 `platform_events`（不参与订阅 fan-out）和一条
`webhook_deliveries`，确保 body、签名、重试路径与真实事件完全一致；测试事件不进订阅目录，也不因为
端点没有订阅而失败。

### 7.3 产生点与原子性

| 来源 | 实现位置 | 策略 |
| :--- | :--- | :--- |
| 阈值告警 | `asset_monitor_store.go` 的 `storeMonitorPoint` / `closeMonitorAlert` | 与样本、告警同一事务；只有实际插入/关闭到行才发事件 |
| 巡检 run | `monitor_engine_store.go` 的 `PersistMonitorRunResults` | 与该 run 的 UPDATE 同一事务 |
| 执行终态 | `client_service.go` 的终态持久化 | 新增 `FinishExecutionAudit`：事务内读行、更新、写事件；只有 `RowsAffected=1` 才写 |
| 执行 outcome_unknown | `client_service.go` 的 `FinalizeExecution` 调用点 | P0 先提交 `outcome_unknown` 状态，再 best-effort 入队；极端窗口内可能丢一条通知，P1 收紧为同事务。`MarkExecutionUncertain` 的回收路径暂不发事件 |
| 启动恢复 | `execution_audit.go` 的 `RecoverInterruptedExecutions` | P0 只收口状态，不补发事件；逐行入队留 P1 |
| 凭据生命周期 | `credential_store*.go` 的 create/promote/grant/revoke 事务 | 同一事务；P1 |
| Agent 上下线 | `gateway_store_agents.go` | 状态写成功后 best-effort 写事件；失败只记日志，不让连接失败 |
| 后台任务终态 | `detached_tasks.go` 的 `finishDetachedTask` | 更新成功后 best-effort；P1 |

为什么连接上下线允许 best-effort：Agent 断线/重连不能因为通知表写入失败而失败；连接事件也不是审计
凭据。代价是极端情况下丢一条通知，审计与 `agent_connection_events` 仍然可查。核心事件（告警、
巡检、执行、凭据）不允许这种妥协。

### 7.4 触发细节

- **告警 opened**：`asset_monitor_alerts` 没有独立的 `alert_id`，主键是 `(series_id, opened_ms)`；
  `INSERT ... doNothing` 后只有真正插入（`RowsAffected=1`）时才发事件。
  `dedupe_key = monitor:<series_id>:<opened_ms>:opened`（与实现的 `monitor:%d:%d:%s` 一致）。
- **告警 resolved**：`closeMonitorAlert` 先查出当前 open 行（`series_id/opened_ms/threshold`），UPDATE
  影响 1 行后用同一行数据发事件。`dedupe_key = monitor:<series_id>:<opened_ms>:resolved`；
  同一 `series_id` 的 open 行只能关闭一次，重新打开会产生新的 `opened_ms`。P0 payload 的 `state`
  固定为 `resolved`，`interrupted/recovered` 区分仍保留在 `asset_monitor_alerts.state`。
- **巡检 run**：在 `UPDATE monitor_runs ... WHERE id=?` 之后、`tx.Commit()` 之前发事件；
  `dedupe_key = inspection.run:<run_id>:finished`。
- **Agent 上下线**：`dedupe_key = agent:<cluster>:<instance>:<generation>:<connected|disconnected>`；同一
  `generation` 的重复调用（重连回调、离线回收）不重复发事件，新连接使用新 `generation`。
- **执行**：`execution.finished` 只覆盖经 ClientService 下发到 Agent 的执行，以及 admission 阶段就失败的
  执行（`FinalizeExecution` 的 `error` 分支）；不覆盖所有 `audit_events` 行（资产盘点、管理操作等不是
  “执行”）。`execution.finished` 用 `dedupe_key = execution:<audit_id>:finished`，
  `execution.outcome_unknown` 用 `execution:<audit_id>:outcome_unknown`。
- **凭据**：只记录 ID、类型、目标、操作者；不记录名称之外的自由文本，不记录版本号/摘要/载荷。
  名称如果被视为敏感，允许在 P1 配置为不发送；默认发送，因为它是凭据管理界面的可读标识。

### 7.5 执行终态的事件边界

`audit_events` 里 `running/executing/success/error/outcome_unknown/resolved_manually` 混在一起，但只有
由 ClientService / execution lifecycle 管理的执行行属于事件目录里的 `execution.*`；资产盘点、管理操作
等共享同一张审计表的行不发声。四条终态路径必须互斥：

| 路径 | 起始状态 | 终态 | 事件 | `reason` / `error_category` |
| :--- | :--- | :--- | :--- | :--- |
| `ClientService.finishExecutionAudit` / `persistOperationFinish`（正常收尾） | `running` 或 `executing` | `success` / `error` | `execution.finished` | 失败时为稳定分类 |
| `MarkExecutionUncertain`（owner 丢失、结果未知） | `executing` | `outcome_unknown` | `execution.outcome_unknown` | `owner_lost` |
| `FinalizeExecution`（审计写失败后的兜底） | 先试 `executing`；不匹配再试 `running` | `outcome_unknown` 或 `error` | 对应上面两个事件 | `owner_lost` / `admission_failed` |
| `RecoverInterruptedExecutions`（Gateway 启动恢复） | `executing` / `running` | `outcome_unknown` / `error` | 对应上面两个事件 | `gateway_restart` |

- `finishExecutionAudit` 是 ClientService 的方法（`client_service.go:221`），最终调用
  `GatewayStore.FinishAudit`（`gateway_store_audit.go:109`）。`FinishAudit` 被 30 多个非执行路径共用，
  不能在里面无条件发事件；新增 `GatewayStore.FinishExecutionAudit(id, finish, ev)`，在事务内复用
  `finishAuditWith` 的 UPDATE，只有 `RowsAffected=1` 时才 `enqueuePlatformEvent` 并提交，只有 ClientService
  调用它。
- `FinalizeExecution` 的顺序不能反：先 `MarkExecutionUncertain`，只有它返回“行已不在 executing”时才把
  `running` 的 admission 行标成 `error`。前者是“已下发、结果未知”，后者是“根本没下发、确定失败”，
  不能混成一个状态。
- `RecoverInterruptedExecutions` 现在是两条批量 UPDATE。要发事件，改成同一事务内先按 `status` 查出
  `id/action/cluster/instance/actor/started_at`，再 UPDATE，再逐行 `enqueuePlatformEvent`；行数超过
  5000 时按 `id` 分页，每页一个事务。恢复在开始接客前完成，事件和终态一起提交。
- 同一个 `audit_events.id` 只发一次终态事件：UPDATE 带 `WHERE status IN (...)`，只有 `RowsAffected=1`
  才入队；已终态的行不会被恢复流程再次改写。`resolved_manually` 是人工对 `outcome_unknown` 的收尾，
  首期不发事件，避免接收方把人工结论误当成原始执行结果；是否补 `execution.resolved_manually` 留在 §17。

## 8. 投递协议

### 8.1 请求

```
POST <endpoint-url> HTTP/1.1
Content-Type: application/json; charset=utf-8
User-Agent: DoOps-Webhook/1
X-DoOps-Event: monitor.alert.opened
X-DoOps-Event-Id: evt_9f2c...
X-DoOps-Delivery-Id: dlv_7a1b...
X-DoOps-Endpoint-Id: wh_3c4d...
X-DoOps-Attempt: 1
X-DoOps-Timestamp: 1795733580
X-DoOps-Signature: v1=5f2c...,v1=9a1b...
Content-Length: <len>

<platform_events.body_json 原样字节>
```

固定头，不支持自定义头，原因：

- 自定义头容易变成第二个秘密通道，且会进入日志/代理；
- 头注入（CRLF）风险；
- HMAC 已经能证明来源与完整性，接收方不需要额外 token。

不发送 `Authorization`、`Cookie`、`X-Forwarded-*`；不跟随重定向；不使用环境代理。

### 8.2 签名

```
signed_payload = timestamp + "." + raw_body
signature      = hex(HMAC_SHA256(secret, signed_payload))
```

- `timestamp` 是 Unix 秒；接收方应拒绝与本地时间相差超过 300 秒的请求。
- 头里可以有多个 `v1=` 值，用逗号分隔：第一个是当前密钥，第二个（如果还在过渡期）是旧密钥。
  接收方逐个做常量时间比较，任意一个匹配即通过。
- 轮换时旧密钥保留 24 小时；`previous_secret_expires_at_ms` 到期后不再签名，后台惰性清空。
- 签名对**原始字节**计算，接收方必须用未解析的 body；不要先 JSON 反序列化再重新序列化。

接收方验证伪代码：

```python
ts = int(headers["X-DoOps-Timestamp"])
if abs(time.time() - ts) > 300:
    reject(401)
raw = request.get_data()
for part in headers["X-DoOps-Signature"].split(","):
    version, sig = part.split("=", 1)
    if version != "v1":
        continue
    expected = hmac.new(secret, f"{ts}.".encode() + raw, hashlib.sha256).hexdigest()
    if hmac.compare_digest(expected, sig):
        accept()
        break
else:
    reject(401)
```

### 8.3 成功、失败与重试

| 结果 | 处理 |
| :--- | :--- |
| 2xx | 成功，`delivered`，记录 `response_status` |
| 3xx | 永久失败 `redirect`；不跟随 |
| 400/401/403/404/405/409/410/413/415/422 等 4xx | 永久失败 `http_4xx` |
| 408、425、429 | 可重试；429/503 的 `Retry-After` 优先，最多 1 小时 |
| 5xx | 可重试 `http_5xx` |
| 连接超时/重置、临时 DNS | 可重试 `timeout/connection_*` |
| TLS 证书错误、NXDOMAIN、策略拒绝 | 永久失败 `tls_error/dns_nxdomain/blocked_destination` |
| 响应体过大 | 成功与否只看状态码；body 最多读 4 KiB 后丢弃，不落库 |

退避（带 ±20% 抖动）：

| 第 N 次尝试失败后 | 下次延迟 |
| :--- | :--- |
| 1 | 30s |
| 2 | 2m |
| 3 | 5m |
| 4 | 15m |
| 5 | 1h |
| 6 | 3h |
| 7 | 6h |
| 8 | 12h |
| 9+ | 不再重试，`failed` |

最多 9 次尝试（首次 + 8 次重试），总窗口约 22.4 小时。永久失败不重试。接收方 2xx 即视为已接收，
不要求业务完成。

### 8.4 至少一次与幂等

- 同一 `(endpoint_id, event_id)` 只有一条投递行；重试复用 `delivery_id`。
- 网络在“接收方已处理、响应未返回”时中断会导致重复投递；这是至少一次的固有语义。
- 接收方应按 `X-DoOps-Event-Id` 去重；同一事件的重复投递 body 完全相同。
- 不保证顺序。状态类事件（告警 opened/resolved、agent 上下线）携带完整状态与 `occurred_at`，
  接收方应按 `occurred_at` 合并，而不是假设到达顺序。
- 人工重放会把 `attempt` 归零、`manual_retry_count+1`，并保留原 `event_id`。

### 8.5 超时与并发

- 单次请求总超时 10 秒；连接 5 秒、TLS 握手 5 秒、响应头 5 秒。
- 全局并发 8；单端点并发 2。事件量很小，优先保证不把 Gateway 拖慢。
- 响应体最多读 4 KiB，随后关闭；不压缩、不解压。
- 租约 = 30 秒（10 秒超时 + 余量）；进程崩溃后租约到期自动可重新认领。

## 9. 出站安全（SSRF 与 URL 策略）

### 9.1 URL 校验（创建/修改时，以及每次投递前）

- 只允许 `https`；拒绝 `http`、`file`、`ftp`、`gopher` 等。
- 必须带 host；拒绝 userinfo（例如 `https://user:<redacted>@host`）、fragment；长度 <= 2048。
- 端口允许 1..65535，拒绝 0；不强制 443，便于自建网关。
- host 为 IP 字面量时直接校验；为域名时先解析。
- 不允许空 host、`localhost` 等依赖 DNS 的绕过；解析后由 IP 规则决定。
- 错误分类：scheme、空 host、userinfo、fragment、解析无结果归 `webhook_invalid_url`；长度超限归
  `webhook_url_too_long`；IP 命中拒绝网段归 `webhook_blocked_destination`。创建/修改阶段只返回这些
  稳定分类，不回显 URL。

### 9.2 DNS 解析与 IP 拒绝表

解析后要求至少一个地址；**任一解析结果命中拒绝网段就拒绝整个 host**（fail closed），不要“挑一个
允许的 IP 用”。通过校验后，`DialContext` 按解析顺序尝试所有已校验 IP，直到一个连接成功；任一地址
命中拒绝网段仍然拒绝整个 host。TLS 的 `ServerName` 仍是原 host，不因拨号 IP 改变。

IPv4 拒绝：

| 网段 | 原因 |
| :--- | :--- |
| `0.0.0.0/8`、`127.0.0.0/8` | 本机/未指定 |
| `10.0.0.0/8`、`172.16.0.0/12`、`192.168.0.0/16` | RFC1918 |
| `100.64.0.0/10` | CGNAT，含部分云元数据地址 |
| `169.254.0.0/16` | link-local，含 `169.254.169.254` |
| `192.0.0.0/24`、`192.0.2.0/24`、`198.18.0.0/15`、`198.51.100.0/24`、`203.0.113.0/24` | 协议分配/测试/基准 |
| `224.0.0.0/4`、`240.0.0.0/4`、`255.255.255.255/32` | 组播/保留/广播 |

IPv6 拒绝：

| 网段 | 原因 |
| :--- | :--- |
| `::/128`、`::1/128` | 未指定/loopback |
| `::ffff:0:0/96` | IPv4-mapped；先 `To4()` 再按 IPv4 规则判断 |
| `64:ff9b::/96`、`64:ff9b:1::/48` | NAT64，可能映射到内网 |
| `100::/64` | discard-only |
| `2001:db8::/32` | 文档 |
| `fc00::/7` | ULA |
| `fe80::/10` | link-local |
| `ff00::/8` | 组播 |

实现上同时使用 `net.IP.IsPrivate/IsLoopback/IsLinkLocalUnicast/IsUnspecified/IsMulticast` 和自定义
网段表；不要只依赖标准库的 `IsPrivate`（它不覆盖 CGNAT、文档段等）。

### 9.3 连接与传输

- `http.Transport` 显式 `Proxy: nil`，忽略 `HTTPS_PROXY/HTTP_PROXY`，否则代理会替我们解析 host，
  IP 校验失效。
- `DialContext` 自行解析并校验，按顺序拨通过的 IP（每个 IP 受拨号超时约束），不做二次 DNS 查询；
  每次新建连接都重新解析、重新校验，防 DNS rebinding。
- `TLSClientConfig{MinVersion: tls.VersionTLS12}`，不开 `InsecureSkipVerify`。
- `CheckRedirect` 返回 `http.ErrUseLastResponse`；3xx 视为永久失败。
- `DisableCompression: true`，请求头 `Accept-Encoding: identity`，避免压缩炸弹。
- `ResponseHeaderTimeout: 5s`，`TLSHandshakeTimeout: 5s`，`ExpectContinueTimeout: 1s`。
- 不读取/记录响应体；只保留状态码和稳定错误分类。

### 9.4 秘密处理

- URL、签名密钥只以密文落库；解密只发生在投递前和轮换时。
- 主密钥不可用时，创建端点、修改 URL、轮换密钥直接 503 `webhook_secret_unavailable`，不落明文、
  不降级；已有端点的投递在解密失败时保持 `pending` 并按退避重试，分类记为
  `secret_unavailable`，运维恢复密钥后由 `status` 读取触发 `Wake()` 继续，不把配置问题记成接收方失败。
- 列表/详情只返回 `url_host` 和 `url_sha256`，不返回 path/query/密钥。
- 日志字段白名单：`endpoint_id`、`event_id`、`delivery_id`、`attempt`、`status_code`、
  `error_category`、`duration_ms`；禁止 URL、host 之外的路径、密钥、body。
- 错误信息只存稳定分类，不存接收方响应正文或 Go 错误原文（可能包含 URL）。
- 管理审计只记 route/method/资源 ID；`adminAuditMutation` 已经不会把 body 写进审计。

## 10. 管理面

### 10.1 Admin API

所有路由用 `admin(...)` 注册，即 `auditAdminMutation` 包装；handler 内再 `requireAdmin`。普通用户 403。

| 方法 | 路径 | 用途 | 返回 |
| :--- | :--- | :--- | :--- |
| GET | `/v1/admin/webhooks` | 列表 | 端点摘要（无密钥/完整 URL） |
| POST | `/v1/admin/webhooks` | 创建 | 端点详情 + **一次性 secret** |
| GET | `/v1/admin/webhooks/detail?id=` | 详情 | 同列表字段 |
| PATCH | `/v1/admin/webhooks?id=` | 改名/描述/URL/事件/scope/enabled | 详情，无 secret |
| DELETE | `/v1/admin/webhooks?id=` | 软删除 + 停用 | `{ok:true}` |
| POST | `/v1/admin/webhooks/rotate-secret?id=` | 轮换 | 新 secret + 旧密钥到期时间 |
| POST | `/v1/admin/webhooks/test?id=` | 自测 | `{event_id, delivery_id}` |
| GET | `/v1/admin/webhooks/deliveries?id=&status=&limit=&before=` | 投递列表 | 投递元数据，无 body |
| GET | `/v1/admin/webhooks/status` | 队列状态 | 各状态计数、最老 pending 年龄 |
| POST | `/v1/admin/webhook-deliveries/retry?id=` | 人工重放 | `{ok:true, delivery_id}` |

表中前七行（列表到自测）是 P0；最后三行（`deliveries`、`status`、`retry`）与 Web 控制台同属 P1。

同一路径的 GET/POST/PATCH/DELETE 由同一个 handler 按 method 分派；现有 `ServeMux` 是路径注册，不依赖
Go 1.22 的方法模式，`auditAdminMutation` 仍然包住整个路径。

`GET /status` 与 `GET /deliveries` 在读取前调用一次 `Wake()`：运维查看积压时顺带触发到期重试和
按小时节流的保留清理，不需要额外的定时器，也不改变响应内容。

请求示例（创建）：

```json
{
  "name": "ops-oncall",
  "description": "值班群机器人",
  "url": "https://hooks.example.com/services/<token>",
  "event_types": ["monitor.alert.opened", "monitor.alert.resolved", "execution.finished"],
  "scope_cluster": "prod",
  "scope_instance": "",
  "enabled": true
}
```

创建与轮换的响应只在这里出现一次；后续任何 GET 都不返回 secret。

```json
{
  "id": "wh_3c4d5e6f708192a3b4c5d6e7",
  "name": "ops-oncall",
  "url_host": "hooks.example.com",
  "event_types": ["monitor.alert.opened", "monitor.alert.resolved", "execution.finished"],
  "enabled": true,
  "secret": "<64-hex>",
  "secret_notice": "This secret is shown once and cannot be retrieved again."
}
```

错误码（稳定字符串，便于 CLI/Web 展示）：

- `webhook_invalid_url`、`webhook_url_too_long`、`webhook_blocked_destination`（400）
- `webhook_not_found`（404）、`webhook_name_invalid`、`webhook_event_unknown`、`webhook_scope_invalid`（400）
- `webhook_limit_exceeded`（端点数/订阅数，409）
- `webhook_endpoint_disabled`（对停用/删除端点做 test，409）
- `webhook_secret_unavailable`（主密钥不可用，503，fail closed）
- `webhook_delivery_not_retryable`（已 delivered 或正在投递，409）

### 10.2 CLI

命令挂在 `doops admin webhook` 下：

```text
doops admin webhook list    --target <gateway-target>
doops admin webhook create  --target <gateway-target> --name <name> --event <type>[,<type>...] --url-file - [--description <text>] [--cluster <c>] [--instance <i>] [--disabled]
doops admin webhook show    --target <gateway-target> --id <wh_...>
doops admin webhook update  --target <gateway-target> --id <wh_...> [--name ...] [--url-file -] [--event ...] [--cluster ...] [--instance ...] [--enable|--disable]
doops admin webhook delete  --target <gateway-target> --id <wh_...>
doops admin webhook rotate-secret --target <gateway-target> --id <wh_...>
doops admin webhook test    --target <gateway-target> --id <wh_...>
doops admin webhook deliveries --target <gateway-target> --id <wh_...> [--limit 50]
```

P0 交付上述全部命令。`retry` / `status` 不在 P0；投递重放与全局队列状态留到 P1。

规则：

- **不提供 `--url`**。URL 可能含令牌，按秘密处理；只接受 `--url-file -`（stdin）或 `--url-file <path>`，
  与现有凭据“payload 只走 stdin”的边界一致，避免 shell history / 进程列表泄露。
- 创建/轮换把 secret 打到 stdout 一次；TTY 下加醒目提示；`--json` 也只输出一次，不写日志。
- `show`/`list` 只显示 host 和事件类型；不提供“查看 URL”命令；要改就重新输入。
- CLI `--help`、README 示例、`backend/skills/SKILL.md` 的命令条目在实现时同步更新；命令模板要能被
  现有契约测试提取执行。

### 10.3 Web 控制台

- 管理员设置页新增 “Webhooks”（建议挂在现有设置/集成区域，与审计页同权限）。
- 列表：名称、host、事件、scope、启用状态、最后成功/失败、待投递数。
- 创建/编辑：URL 使用密文输入框；提交后仅在一次性弹窗显示 secret，提供复制按钮；关闭后不再显示。
- 操作：测试、轮换、停用/启用、删除、查看投递记录、重放失败投递。
- 空/加载/错误/403 状态齐全；secret 不写 localStorage/sessionStorage、不进 URL、不进前端日志。
- 前端只调用 Gateway Admin API；不在浏览器自行判断管理员权限，以 403 为准。

## 11. 状态机与生命周期

端点：

```
create -> enabled -> disabled -> enabled
                       └────> deleted（软删除，终态）
```

- 停用：不再为它创建新投递；同事务把该端点所有 `pending` 投递置 `failed/endpoint_disabled`；
  已在途的请求允许完成。
- 启用：只影响之后的事件，不自动重放历史。
- 删除：停用 + `deleted_at`；列表不可见；历史投递保留到保留期，便于审计。

投递：

```
pending -> delivering -> delivered
   ^          |  (租约过期)
   |          v
   └────── pending
              |
              v
            failed  --(人工重放)--> pending
```

事件不可变；没有“已读/未读”状态。删除事件只发生在保留清理，且必须没有未完成投递。

## 12. 失败、背压与容量

- 失败分类见 §8.3；永久失败不重试，避免对配置错误持续打靶。
- 每个端点最多 10000 条待投递；超过时新投递写 `failed/queue_overflow`，不静默丢弃事件。
- 全局待投递超过 50000 时，worker 降低领取批次并在日志中告警；不阻塞业务事务（事件写入仍继续），
  但管理面 `status` 会显示积压。
- 队列最老 pending 超过 5 分钟视为异常，管理面标红；这是运维信号，不自动停用端点。
- 多 Gateway 副本共享 MySQL 时，租约 + 唯一约束保证不会长期双发；租约过期后的重复由接收方按
  event_id 去重。
- 进程崩溃：`delivering` 行租约到期后可被任何副本重新认领；`pending` 行原样保留。

## 13. 可观测性与审计

- 日志（结构化字段）：`endpoint_id`、`event_id`、`delivery_id`、`attempt`、`status_code`、
  `error_category`、`duration_ms`；成功用 info/debug，永久失败用 warn，队列积压用 error。
- 禁止日志：完整 URL、secret、body、响应体、Go 错误原文。
- 管理面 `/v1/admin/webhooks/status` 返回：`pending`/`delivering`/`delivered`/`failed` 计数，
  最老 pending 年龄，各错误分类计数，最后成功/失败时间。
- 管理审计：创建/修改/删除/轮换/测试/重放都经 `auditAdminMutation`；审计只含资源 ID 和状态。
- 投递记录不是审计：不写 `audit_events`，避免把通知重试混进执行审计和保留策略。
- 如果后续接入指标系统，建议指标：`doops_webhook_deliveries_total{status,event_type}`、
  `doops_webhook_delivery_duration_seconds`、`doops_webhook_pending`、
  `doops_webhook_oldest_pending_seconds`。

## 14. 交付阶段

### P0（MVP，建议先做）

- 四张表 + SQLite/MySQL schema + 迁移/冻结导出。
- `webhook_store.go`：CRUD、加密、订阅、fan-out、claim、终态写回、保留清理。
- `webhook_dispatcher.go`：事件驱动 worker（全局 8/单端点 2）、唤醒、租约、退避、启动恢复、
  按小时节流的保留清理。
- `webhook_http.go`：Admin API。
- `webhook_types.go`：URL 校验、SSRF 网段、签名辅助、事件目录与 envelope。
- P0 事件：`execution.finished`、`execution.outcome_unknown`、`agent.connected/disconnected`、
  `monitor.alert.opened/resolved`、`inspection.run.finished`。
- CLI `doops admin webhook list/create/show/update/delete/rotate-secret/test/deliveries`
  （`retry/status` 放 P1）。
- 设计文档、Admin API 文档、CLI help、契约测试。

### P1

- `background_task.finished`、`credential.*`。
- Web 控制台页面与浏览器验收。
- CLI `retry/status`；Web 控制台中的投递记录、队列状态与失败重放。
- 保留策略的容量报表与调参指引（参数本身在 P0 已有 `-webhook-retention`）。
- 指标接入（如已有指标设施）。

### P2 / 另案

- 入站 Webhook（外部触发 DoOps）：公网入口、HMAC 重放防护、操作白名单、目标授权映射。
- 按用户/按目标订阅：需要把事件可见性映射到现有 grant，属于权限模型变更，单独立项。
- 自定义头、模板、ChatOps 适配器：优先由接收方做格式转换。

## 15. 验收标准

Webhook 涉及 Gateway 行为、CLI 与安全边界。按仓库验证分级，功能与安全按 T1/T2 的 Given/When/Then
矩阵执行；不涉及凭据生命周期，因此不要求在线凭据生命周期验收。

### 15.1 功能

1. **创建与一次性密钥**：管理员 POST 创建 -> 201，响应含 secret；再次 GET 列表/详情 -> 不含 secret、
   不含完整 URL；普通用户 -> 403。
2. **事件 fan-out**：订阅 `monitor.alert.opened` 的端点在告警插入后收到一条投递；未订阅端点不收；
   scope 不匹配不收。
3. **原子性**：模拟 `PersistMonitorRunResults` 事务回滚 -> 没有事件、没有投递；提交 -> 两者都有。
4. **重试分类**：500 后 200 -> 第 2 次成功；400 -> 一次后 failed；429 + `Retry-After` -> 按指定时间重试。
5. **重启恢复**：投递前杀掉 Gateway -> 重启后 pending 行被重新认领并送达；`delivering` 租约过期后
   可被重新认领。
6. **幂等**：重复调用同一生产事件 -> 只有一条 `platform_events`、每端点一条 `webhook_deliveries`。
7. **轮换**：轮换后旧 secret 在 24 小时内仍可通过签名校验；到期后只用新 secret。
8. **人工重放**：failed 投递可重放；delivered 不可重放；重放复用 event_id、body 不变。

### 15.2 安全

1. **SSRF**：`https://127.0.0.1`、`https://10.0.0.1`、`https://169.254.169.254` 被拒绝为
   `webhook_blocked_destination`；`http://...`、带 userinfo 的 URL 被拒绝为 `webhook_invalid_url`；
   以上都没有发出网络请求。
2. **DNS rebinding**：解析器第一次返回公网、第二次返回私网 -> 第二次连接被拒绝；只拨通过校验的 IP。
3. **重定向**：端点返回 302 -> 不跟随，投递永久失败。
4. **代理环境**：设置 `HTTPS_PROXY` 指向一个内网地址 -> 请求仍直连并通过 IP 校验（即代理被忽略）。
5. **秘密不泄露**：创建/轮换后，日志、管理审计、`GET` 响应、CLI 普通输出、投递记录都不含 URL
   path/query 和 secret；数据库列是密文；用仓库约定的测试凭据 canary 断言 payload 不含凭据值。
6. **payload 白名单**：每个事件用固定 struct 序列化；构造含 `command_summary`、`tail`、`error` 原文、
   Secret 值的源数据，断言最终 body 不含这些字段/值。
7. **签名**：正确签名通过；改一个字节失败；时间戳超 300 秒失败；旧密钥在过渡期通过。

### 15.3 约束

1. `webhook*.go` 不出现 `time.Ticker`、`time.Tick`，也不出现固定间隔的 `time.NewTimer` 循环；源码
   扫描测试通过，空闲时不设 timer 的单元测试通过。
2. `monitor_schedules` 相关代码零改动；`StartMonitorRunner` 行为不变。
3. 管理路由都经过 `auditAdminMutation`；审计行含 `wh_/dlv_` 资源 ID，且不含 URL/secret。
4. MySQL 与 SQLite 行为一致；方言测试覆盖 claim、upsert、fan-out、保留清理。
5. 生命周期测试：`StartWebhookDispatcher` 后能立即认领；`StopWebhookDispatcher` 后不再领取新行、
   等待在途请求结束、可重复调用；关闭超时后行仍为 `delivering` 并可被租约回收。
6. `git diff --check` 干净；Agent/CLI 包测试、Python 契约测试、Web 构建按仓库要求通过。

## 16. 代码落点

| 文件/目录 | 变更 |
| :--- | :--- |
| `backend/agent/internal/server/webhook_store.go` | 新表访问、端点 CRUD、订阅、fan-out、claim、终态、保留清理 |
| `backend/agent/internal/server/webhook_dispatcher.go` | worker、唤醒、租约、退避、签名、HTTP client、SSRF 拨号；10s 请求、5s 连接/握手/响应头、30s 租约、全局 8/端点 2 并发为包级常量 |
| `backend/agent/internal/server/webhook_types.go` | URL 策略、SSRF 网段、事件目录、envelope |
| `backend/agent/internal/server/webhook_http.go` | Admin API handlers |
| `backend/agent/internal/server/tunnel_hub.go` | 创建 dispatcher、注册 `/v1/admin/webhooks*`、连接 `webhookWake`；不启动 worker，生命周期归 `gatewaycmd` |
| `backend/agent/pkg/gatewaycmd/gatewaycmd.go` | 注册 `-webhook-retention`（duration，0=永久），默认 720h，校验 >=0；`NewGatewayHub` 后 `hub.StartWebhookDispatcher()` 并 `defer hub.StopWebhookDispatcher(server.HTTPShutdownTimeout())`，覆盖正常关闭与启动失败两条路径 |
| `backend/agent/internal/server/admin_audit.go` | 扩展 `adminAuditIDPattern` 接受 `wh_/dlv_` |
| `backend/agent/internal/server/gateway_store_schema.go` | SQLite 四张表 + 索引 |
| `backend/agent/internal/server/gateway_mysql_schema.sql` | MySQL 镜像四张表 + 索引 |
| `backend/agent/internal/server/asset_monitor_store.go` | 告警 open/resolved 事件 |
| `backend/agent/internal/server/monitor_engine_store.go` | 巡检 run 完成事件 |
| `backend/agent/internal/server/client_service.go` | `execution.finished` 与审计终态同事务；`execution.outcome_unknown` 仍为提交后 best-effort |
| `backend/agent/internal/server/gateway_store_agents.go` | 上下线事件（best-effort） |
| `backend/agent/internal/server/detached_tasks.go` | P1：后台任务终态事件 |
| `backend/agent/internal/server/credential_store*.go` | P1：凭据事件 |
| `backend/cli/cli/doops/admin_webhooks.go`、`admin_webhooks_http.go`、`main.go` | CLI 子命令、HTTP 客户端与 help |
| `app/`、`components/`、`lib/` | P1：Web 控制台页面 |
| `backend/docs/DOOPS_API_GUIDE.md` | 端点管理、签名验证、错误码与 CLI 的权威说明 |
| `backend/docs/RUNTIME_LIMITS.md`、`CHANGELOG.md` | 超时/保留常量与用户可见变更 |
| `backend/skills/SKILL.md` | 只有新增用户可见命令时才加条目 |

P0 测试：`webhook_store_test.go`、`webhook_dispatcher_test.go`、`webhook_fanout_test.go`、
`webhook_types_test.go`，以及 `gateway_mysql_*_test.go` 的 schema 契约；Web 对应用例在 P1。

## 17. 待决问题

| 问题 | 建议 | 需要谁决定 |
| :--- | :--- | :--- |
| P0 事件范围是否只做执行/Agent/告警/巡检 | 是；凭据与后台任务放 P1，先打通闭环 | 产品/运维 |
| 端点是否只允许平台管理员 | 是；按用户订阅会引入权限维度，另案 | 架构 |
| 默认保留 30 天是否可接受 | 是；`-webhook-retention 0` 可永久保留 | 运维 |
| URL 是否允许 http | 否；只允许 https | 安全 |
| 是否允许自定义 header | 否；v1 固定头 + HMAC | 安全 |
| 是否支持入站 Webhook | 另案；公网入口需要独立威胁模型 | 产品/架构 |
| 轮换过渡期 24 小时是否合适 | 是；可配置，默认 24h | 运维 |
| payload 是否包含 `actor_user_id` | 是；不含姓名/邮箱 | 产品/隐私 |
| outbox worker 是否算新增调度器 | 不算：它只消费事件产生的待投递行，没有调度计划、没有周期 tick；若评审按更严格口径要求复用既有调度器，可把 wake 并入 monitor engine，但会让其承担第二职责，另案 | 架构 |
| 多 Gateway 副本部署是否在范围内 | 是，但 v1 只在替代副本启动时回收他人过期租约；存活副本不轮询。若要求秒级接管，需 DB 通知或既有调度器提供 wake，另案 | 架构/运维 |
| Web 控制台是否必须 P0 | 否；CLI/API 先可用，Web 放 P1 | 产品 |
| 人工把 `outcome_unknown` 收尾为 `resolved_manually` 是否发事件 | 首期不发；P1 可加 `execution.resolved_manually` | 产品/运维 |

## 18. 与现有文档的关系

- 本文落实 [MONITORING_INSPECTION_LOOP.md](MONITORING_INSPECTION_LOOP.md) 的 D5 通知段，但不改变其
  事件闭环的其它环节；告警/巡检事件字段以本文为准。
- P0 不新增独立的 `backend/docs/DOOPS_WEBHOOKS.md`：端点管理、签名验证、重试与错误码并入
  `backend/docs/DOOPS_API_GUIDE.md`，本文保留为设计与实现记录；内容增长后再拆运维手册。
- Admin API 的对外说明并入 `backend/docs/DOOPS_API_GUIDE.md`；控制台行为并入
  `backend/docs/DOOPS_WEBUI.md`；超时/重试/保留常量并入 `backend/docs/RUNTIME_LIMITS.md`。
- 用户可见行为上线时，在 `CHANGELOG.md` 的 `## [Unreleased]` 增加条目；本文作为设计文档本身
  不进入 CHANGELOG。
- 不修改 `backend/skills/SKILL.md`，除非新增了用户可见的命令条目；若新增，按现有契约测试要求把
  命令模板放在反引号内，说明文字放在反引号外。
