# DoOps 配置文件与环境变量权威参考

本文件是 DoOps 全栈（**Gateway 控制面 / Agent 节点 / Web 控制台 / CLI 工具**）配置文件与环境变量的**单一权威参考**。仓库源码中所有面向运维与开发的环境变量必须在本文件写明**默认值、合法取值、生效方式与安全边界**，并由 `backend/test/test_env_config_contract.py` 与生产源码自动逐项核对（代码新增变量未同步本文件会阻断 CI）。

超时与时间保留窗口的精确合法范围与启动顺序不等式由 [运行时超时与限额](RUNTIME_LIMITS.md) 持有并由 Go 单测直接比对；本文完整索引全部超时变量并链接至该表。

---

## 1. 配置文件清单与生效原则

| 组件 | 推荐配置文件路径 | 仓库模板 | 生效机制 |
| --- | --- | --- | --- |
| **Gateway 控制面** | `/etc/doops-gateway.env`（`chmod 0600`）+ `/etc/systemd/system/doops-gateway.service` | `backend/deploy/config/doops-gateway.env.example`、`backend/deploy/systemd/doops-gateway.service.example` | 经 systemd `EnvironmentFile` 注入，修改后须 `systemctl restart doops-gateway` |
| **Agent（Kubernetes）** | 集群 GitOps 仓库的 Helm `values.yaml` + Kubernetes Secret | `backend/deploy/helm/doops-agent/values.yaml` | 经 Helm values 与 `secretKeyRef` 渲染到 Pod `env`，不可通过明文写入 Git |
| **Agent（独立 Docker / 宿主机）** | `/opt/doops-agent/enrollment.env` / `/etc/doops-agent.env`（`chmod 0600`） | `backend/deploy/config/doops-agent.env.example` | 经 Docker Compose `env_file` 或 systemd `EnvironmentFile` 注入，修改后重启容器/服务 |
| **Web 控制台（Next.js）** | 本地 `.env.local` 或容器运行环境 | `.env.example` | 启动 Node/Next.js 进程时读取（`NEXT_PUBLIC_*` 在构建期注入） |
| **`doops` CLI** | `~/.agent/skills/doops/config.json` 与相邻 `auth.json` | 首次登录自动生成 | 每次执行命令行读取；仅 `DOOPS_CONFIG` 等少数环境变量可覆盖 |

### 通用规则

1. **优先级**：同时支持命令行参数（flag）与环境变量的项，**显式命令行参数优先于环境变量，环境变量优先于内置默认值**；显式传入互相冲突的开关组合会直接启动失败（fail-closed）。
2. **禁止热加载**：Gateway 与 Agent 的环境变量仅在进程启动时读取或按启动快照定型，修改配置后必须重启对应服务才能生效。
3. **凭据与权限红线**：含密钥、DSN、Token 的环境文件必须设置 `0600` 权限且**绝不提交到 Git 仓库**（`.gitignore` 已默认忽略 `.env` 与 `.env.local`，仅跟踪 `*.env.example` 模板）。
4. **MySQL 模式下执行宿主机离线命令**：当 Gateway 使用 `DOOPS_GATEWAY_STORE=mysql` 时，在宿主机手工执行 `doops-gateway enroll`、`user`、`token`、`grant`、`audit`、`store` 等子命令前，**必须先加载 `/etc/doops-gateway.env`**（例如 `set -a; source /etc/doops-gateway.env; set +a`），否则命令会按默认行为打开旧的 SQLite 文件而非线上 MySQL 库。

---

## 2. Gateway 控制面环境变量（`doops-gateway`）

对应配置文件模板：`backend/deploy/config/doops-gateway.env.example` 与 `backend/deploy/systemd/doops-gateway.service.example`。

### 2.1 存储后端、数据目录与审计存储

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_GATEWAY_STORE` | 否 | `sqlite` | `sqlite` \| `mysql` | 选择 Gateway 控制面存储引擎。未设置时默认 `sqlite`（路径由 `-db` 指定，默认 `/var/lib/doops-gateway/gateway.db`）；填写其它值启动直接报错 `gateway_store_invalid`，绝不静默回退。详见 [MySQL 运维手册](GATEWAY_MYSQL_OPERATIONS.md)。 |
| `DOOPS_GATEWAY_MYSQL_DSN` | MySQL 必填 | 空 | Go `go-sql-driver/mysql` DSN，如 `user:pass@tcp(db.example.com:3306)/doops?parseTime=true` | 当 `DOOPS_GATEWAY_STORE=mysql`、以及执行 `store mysql-copy` / `audit-scrub` / `audit export` / `store backup` 时必须配置。缺失或格式错误时 fail-closed 拒绝启动，口令不会写入日志。 |
| `DOOPS_GATEWAY_DATA_DIR` | 否 | `/var/lib/doops-gateway` | 绝对路径目录（启动会自动创建 `skills/` 与 `artifacts/`，`0700`） | **仅在 `DOOPS_GATEWAY_STORE=mysql` 时生效**，保存落盘的 Skill 包、File-First 巡检产物及文件化审计目录（SQLite 模式自动沿用 `-db` 所在目录）。相对路径报 `gateway_data_dir_invalid`，目录不可写报 `gateway_data_dir_unwritable` 并拒绝启动。 |
| `DOOPS_AUDIT_STORAGE` | 否 | `db` | `db` \| `file` | 控制审计大正文存储方式：`db` 全量内联存在数据库；`file` 开启 File-First 审计文档存储（普通操作 `tail` 写入 `<dataDir>/audit/docs/YYYY-MM-DD/`、终态记录追加到 `<dataDir>/audit/records/YYYY-MM-DD.jsonl`，热库仅存轻量指针，支持热库清理后无缝回读）。非法值报 `gateway_audit_storage_invalid`、目录不可写报 `gateway_audit_doc_dir_unwritable` 并拒绝启动。 |

### 2.2 受管凭据加密主密钥与轮换

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_GATEWAY_SECRET_KEY` | 启用凭据时必填 | 空 | 至少 32 字节的高熵随机密钥（建议 `python3 -c 'import secrets; print(secrets.token_hex(32))'` 生成的 64 位十六进制串） | 用于 AEAD 加密受管凭据载荷、自定义 MCP 敏感头/环境变量与 Git 密码。未配置时凭据写入与解密功能 fail-closed 不可用；若库中已存在密文且密钥缺失或不匹配，Gateway 启动直接失败并退出。请独立备份，详见 [受管凭据资源](CREDENTIAL_RESOURCES.md)。 |
| `DOOPS_GATEWAY_SECRET_KEY_PREVIOUS` | 仅主密钥轮换期 | 空 | 旧的 32+ 字节主密钥（不能与 `DOOPS_GATEWAY_SECRET_KEY` 相同） | 仅用于 `doops-gateway secret-key rotate` 原地重加密与过渡回读；轮换全部密文完成后应从 `/etc/doops-gateway.env` 删除并重启。 |

### 2.3 Agent 注册认证与凭据下发策略

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_REQUIRE_AGENT_ENROLLMENT` | 否 | `0`（默认开放注册） | `1`/`true`/`yes`/`on` 或 `0`/`false`/`no`/`off` | **主开关（对应 `-require-agent-enrollment`）**。默认关闭：尚未确认过 enrollment token 的新 target 可直接免密钥注册，且可正常接收模型与受管凭据；**已确认过 enrollment token 的存量 target 始终强制校验其专属密钥**（无 token 或错误/跨目标 token 一律返回 `401 agent_enrollment_auth_failed`）。设为 `1` 后，所有 Agent（含全新 target）必须提前通过 `doops-gateway enroll create` 签发密钥才能连入，且自动禁止向未登记会话下发凭据。非法布尔值或与 `-allow-open-agent-registration` 冲突时 fail-closed 拒绝启动。 |
| `DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY` | 否 | `0`（跟随主开关） | `1`/`true`/`yes`/`on` 或 `0`/`false`/`no`/`off` | **细粒度凭据门禁（对应 `-require-enrolled-credential-delivery`）**。仅在默认开放注册模式下有意义：设为 `1` 时仍允许无密钥新 Agent 连入执行命令，但拒绝向其自动投递或物化/导入任何模型与受管凭据（显式用凭据报 `409 credential_target_unenrolled`）。非法值或与 `-allow-unenrolled-credential-delivery` 冲突时拒绝启动。 |
| `DOOPS_ALLOW_OPEN_AGENT_REGISTRATION` | 否（兼容旧变量） | 继承主开关（默认开放） | `1`/`true`/`yes`/`on` 或 `0`/`false`/`no`/`off` | 历史兼容开关（对应 `-allow-open-agent-registration`）。仅在未设置 `DOOPS_REQUIRE_AGENT_ENROLLMENT` 时生效；显式设为 `0`/`false` 等价于开启强制密钥校验。新部署请统一使用 `DOOPS_REQUIRE_AGENT_ENROLLMENT`。 |
| `DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY` | 否（兼容旧变量） | 继承主开关（开放模式为 `1`，严格模式强制 `0`） | `1`/`true`/`yes`/`on` 或 `0`/`false`/`no`/`off` | 历史兼容开关（对应 `-allow-unenrolled-credential-delivery`）。仅在开放注册模式且未设置 `DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY` 时生效；若同时设置了 `DOOPS_REQUIRE_AGENT_ENROLLMENT=1`，该变量会被强制忽略并打印启动告警（CLI 显式混用两项参数直接报错退出）。 |

### 2.4 对象存储（COS / S3 兼容，用于工作区快照与审计/库归档）

工作区 `push`/`pull` 默认走 COS 快照，以及 `doops-gateway audit export` / `store backup` 的远程分片归档均读取以下 5 项；五项须成套配置，缺失时对应 COS 路径返回明确配置错误，不静默泄露或回退：

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_COS_ENDPOINT` | 启用 COS 时必填 | 空 | HTTPS S3/COS 端点 URL，如 `https://cos.ap-shanghai.myqcloud.com` | S3 兼容对象存储接入点；生产环境须使用 HTTPS。 |
| `DOOPS_COS_REGION` | 启用 COS 时必填 | 空 | 区域标识，如 `ap-shanghai` | 对象存储签名区域。 |
| `DOOPS_COS_BUCKET` | 启用 COS 时必填 | 空 | 存储桶名称，如 `doops-artifacts-example` | 工作区快照、审计归档与库备份写入的目标桶。 |
| `DOOPS_COS_ACCESS_KEY_ID` | 启用 COS 时必填 | 空 | 最小权限访问密钥 ID | 仅授予目标桶读写/分片上传权限，禁止使用主账号根密钥。 |
| `DOOPS_COS_SECRET_ACCESS_KEY` | 启用 COS 时必填 | 空 | 对应访问密钥 Secret | 仅留存在 `/etc/doops-gateway.env`（`0600`），严禁写入日志、代码或 Agent 环境。 |

### 2.5 Web 控制台联动与 WebSocket 安全

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_CONSOLE_FORWARD_SECRET` | Web 反代登录限流时必填 | 空 | 高熵共享随机串（须与 Web 控制台侧完全一致，不可作为 CLI flag 传入） | 校验 Web 控制台登录代理附带的 `X-DoOps-Client-IP` / `X-DoOps-Client-IP-Signature` HMAC 签名，避免所有浏览器登录共享同一个反代 IP 限流桶。未配置时忽略转发头、按直接对端 IP 限流。 |
| `DOOPS_ALLOWED_WS_ORIGINS` | 反向代理跨端口/多域名时配置 | 空（仅允许与请求 `Host` 同主机同端口） | 逗号分隔的来源列表，如 `https://console.example.com,https://doops.example.com:8443` | 浏览器访问 Gateway WebSocket 时的 `Origin` 白名单（非浏览器客户端不带 `Origin` 默认放行；直连 TLS 拒绝明文 `http://` 降级来源）。 |
| `DOOPS_ALLOW_ALL_WS_ORIGINS` | 仅本地开发 | `0` | `1`（放开所有 `Origin`） | **仅限本地调试**，生产环境禁止开启。 |

### 2.6 巡检留存、目标资源同步与宿主机诊断

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_MONITOR_ARTIFACT_RETENTION_DAYS` | 否 | `30` | 正整数（天） | File-First 巡检产物目录与记录保留天数；非法/非正整数记日志告警并回退默认值 `30`。 |
| `DOOPS_MONITOR_METRIC_RETENTION_DAYS` | 否 | `90` | 正整数（天） | `inspection_metric_points` 趋势指标保留天数（通常大于产物保留期，保证曲线连续）。 |
| `DOOPS_MONITOR_ARTIFACT_KEEP_RUNS` | 否 | `50` | 正整数（条） | 每个定时巡检计划至少保留最近 N 次运行产物，不受过期天数影响。 |
| `DOOPS_AGENT_RESOURCE_MISS_THRESHOLD` | 否 | `3` | 整数 `1..10000` | 目标资源同步连续缺失多少次完整观测后标记为失联/清理。非法值导致同步事务报错 `invalid DOOPS_AGENT_RESOURCE_MISS_THRESHOLD`。 |
| `DOOPS_MACHINE_DIAGNOSTICS_ROOT` | 否 | 空 | 本地只读宿主诊断样本根目录（绝对路径） | 仅用于网关侧挂载只读宿主诊断目录的场景；非空时 `bind`/`refresh` 远程采集动作返回 `409 diagnostic_mount_read_only`。 |

### 2.7 Edge 隧道限额与缓冲（仅启用 `-edge-listen` 时生效）

默认 `-edge-listen` 为空（Edge 隧道关闭），详见 [Edge 隧道设计](design/DOOPS_EDGE_TUNNEL.md)：

| 变量 | 默认值 | 合法取值 | 说明 |
| --- | --- | --- | --- |
| `DOOPS_EDGE_FRAME_BYTES` | `32768`（32 KiB） | 正整数（字节） | 单个 Edge 二进制帧最大载荷字节数。 |
| `DOOPS_EDGE_HELLO_BYTES` | `4096`（4 KiB） | 正整数（字节） | TLS ClientHello SNI 嗅探最大缓存字节数。 |
| `DOOPS_EDGE_CONN_QUEUE` | `8` | 正整数 | 单条 Edge 连接发送队列深度。 |
| `DOOPS_EDGE_QUANTUM` | `2` | 正整数 | Edge 调度器轮询配额（每轮连续发送帧数）。 |
| `DOOPS_EDGE_MEM_BUDGET` | `536870912`（512 MiB） | 正整数（字节） | Edge 缓冲全局内存上限（75% 暂停、40% 恢复背压）。 |
| `DOOPS_EDGE_MAX_CONNS` | `4096` | 正整数 | Edge 监听器全局最大并发连接数。 |
| `DOOPS_EDGE_MAX_CONNS_PER_IP` | `64` | 正整数 | 单客户端 IP 最大并发 Edge 连接数。 |

### 2.8 Gateway 侧运行时超时与保留窗口（启动即校验，非法值 fatal）

以下全部变量均为 Go duration 字符串（如 `90s`、`15m`、`24h`、`336h`），仅在进程启动时读取一次并做范围与不等式校验；精确范围与不等式以 [运行时超时与限额](RUNTIME_LIMITS.md) 为唯一权威源：

| 变量 | 默认值 | 简要说明 |
| --- | --- | --- |
| `DOOPS_AGENT_LEASE` | `90s` | Agent 租约上限（心跳 ping 周期 = 租约 / 3） |
| `DOOPS_AGENT_HEARTBEAT_FLUSH` | `30s` | Agent 心跳批量写库周期（须 `≤ DOOPS_AGENT_LEASE / 3`） |
| `DOOPS_OPERATION_TIMEOUT` | `60m` | 单个转发操作上限（`-operation-timeout` 默认值） |
| `DOOPS_DETACHED_TASK_TIMEOUT` | `24h` | 分离式后台任务生存期（`-detached-task-timeout` 默认值） |
| `DOOPS_TARGET_QUEUE_TIMEOUT` | `10m` | 忙目标排队等待上限（`-target-queue-timeout` 默认值） |
| `DOOPS_TARGET_RECONNECT_GRACE` | `10s` | RPC/内部调用等待 Agent 重连宽限（`-target-reconnect-grace` 默认值） |
| `DOOPS_SHUTDOWN_DRAIN_TIMEOUT` | `2m` | 收到 `SIGTERM`/`SIGINT` 后等待在途操作排空上限（`-shutdown-drain-timeout` 默认值） |
| `DOOPS_HTTP_SHUTDOWN_TIMEOUT` | `10s` | 排空完成后关闭 HTTP 连接的超时预算 |
| `DOOPS_HTTP_READ_HEADER_TIMEOUT` | `10s` | Gateway HTTP 请求头读取超时 |
| `DOOPS_HTTP_IDLE_TIMEOUT` | `2m` | Gateway HTTP keep-alive 空闲连接超时 |
| `DOOPS_EXECUTION_RESUME_GRACE` | `60s` | 断线续接记录未落定时等待 Agent 恢复的宽限 |
| `DOOPS_EXECUTION_RESUME_WAIT_MAX` | `5s` | 单次 `GET /v1/executions/<id>?wait_ms=N` 长轮询阻塞硬顶 |
| `DOOPS_EXECUTION_RESUME_TTL` | `30m` | Gateway 侧终态执行记录可寻址保留时长 |
| `DOOPS_CREDENTIAL_DELIVERY_TIMEOUT` | `3m` | Agent 连入后模型与镜像仓库凭据投递等待上限 |
| `DOOPS_MYSQL_CONNECT_TIMEOUT` | `8s` | MySQL 拨号与 Ping 连接超时 |
| `DOOPS_MONITOR_TICK` | `15s` | 定时巡检计划扫描周期（`-monitor-tick` 默认值；flag 传 `0` 禁用） |
| `DOOPS_MONITOR_RETENTION_MIN_INTERVAL` | `15m` | 巡检产物自动清理触发节流间隔 |
| `DOOPS_AUDIT_RETENTION_INTERVAL` | `6h` | 审计保留清理巡检周期（保留天数由 `-audit-retention` 决定） |
| `DOOPS_EXPIRED_TOKEN_SWEEP_INTERVAL` | `6h` | 过期登录 Token 定期清理周期 |
| `DOOPS_OFFLINE_REAPER_INTERVAL` | `1h` | 离线实例清理巡检周期 |
| `DOOPS_OFFLINE_INSTANCE_RETENTION` | `336h`（14 天） | 离线实例在 `agent_status` 中的保留窗口 |
| `DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION` | `720h`（30 天） | `platform_load_samples` 平台负载采样保留窗口 |
| `DOOPS_AUDIT_PURGE_BATCH_PAUSE` | `100ms` | 审计分批清理批间暂停，降低数据库写锁竞争 |
| `DOOPS_EDGE_WRITE_STALL` | `30s` | Edge 隧道单连接无写进展挂起阈值（兼容裸秒） |
| `DOOPS_EDGE_CONN_ID_QUARANTINE` | `30s` | Edge 已释放 `conn_id` 隔离冷却期（兼容裸秒） |
| `DOOPS_EDGE_HELLO_TIMEOUT` | `5s` | Edge TLS ClientHello 握手读取死线（兼容裸秒） |

---

## 3. Agent 节点环境变量（`doops-agent`）

对应模板：`backend/deploy/config/doops-agent.env.example` 与 Helm Chart `backend/deploy/helm/doops-agent/values.yaml`。

### 3.1 隧道身份、注册密钥与集群连接

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_GATEWAY_URL` | Helm/容器模板使用 | 空 | `https://gateway.example.com`（本地回环可 `http://127.0.0.1:42222`） | Helm `templates/deployment.yaml` 将其传给 `-gateway-url`。**注意**：直接运行裸二进制 `doops-agent` 时须显式传 `-gateway-url` 参数。 |
| `DOOPS_GATEWAY_CLUSTER` | Helm/容器模板必填 | 空 | `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$` | 目标所属集群标识；同时被自升级预检与 DoSql 资产上下文读取。直接运行裸二进制须显式传 `-cluster`。 |
| `DOOPS_GATEWAY_INSTANCE` | Helm/容器模板必填 | 空 | `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$` | 集群内唯一实例标识；同一 `cluster/instance` 不可被两台机器同时复用。直接运行裸二进制须显式传 `-instance`。 |
| `DOOPS_AGENT_ENROLL_TOKEN` | 否（严格模式或已锁定 target 时必填） | 空 | `dga_aen_aen_<24hex>_<64hex>`（由 `doops-gateway enroll create` 签发） | Agent 专属注册密钥。**必须通过环境变量或 K8s `secretKeyRef`（`gateway.enrollmentTokenSecret`）注入，禁止放在命令行 `-enroll-token` 参数中**；不要填 Gateway user token。首次以此密钥连通过后，该 target 将永久锁定校验此密钥。 |
| `DOOPS_ALLOW_INSECURE_GATEWAY` | 否（仅开发） | `0` | `1`（放开非回环明文 `http://`/`ws://` Gateway） | 默认仅允许 `https://`/`wss://` 及回环地址 `127.0.0.1`/`localhost`；生产环境严禁设为 `1`。 |
| `DOOPS_KUBECONFIG_SOURCE` | 否（容器入口） | `mounted` | `mounted` \| `serviceAccount` | 由 `sandbox-entrypoint.sh` 读取（对应 Helm `kubeconfig.source`）：`mounted` 使用挂载的 `/root/.kube/config`；`serviceAccount` 使用 Pod 专用 ServiceAccount 短期投射 Token（拒绝 `default` SA）。 |

### 3.2 反向代理与命令安全过滤

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_PROXY_ALLOW_HOSTS` | 使用公网代理时必填 | 空（拒绝所有上游） | 逗号分隔的精确上游主机名，如 `127.0.0.1,internal.example.com` | Agent 侧公网网站代理出站主机名白名单（对应 Helm `proxy.allowedHosts`）；默认空值拒绝全部代理出站，且不跟随重定向。 |
| `DOOPS_COMMAND_POLICY_FILE` | 否 | 空（仅内置结构化规则） | YAML 策略文件绝对路径（上限 256 KiB） | 加载额外的结构化 argv AST 拒绝策略，详见 [双层命令过滤](design/COMMAND_FILTER.md)。**Fail-closed**：文件不存在、超大、语法错误或规则名冲突时拒绝执行命令，绝不静默降级。 |
| `DOOPS_COMMAND_DENY_RULES` | 否 | 空（仅内置正则规则） | JSON 数组：`[{"name":"...","match":"...","hint":"..."}]` | 追加正则兜底命令拦截规则（对应 Helm `commandDenyRules`）；格式错误、正则非法或规则名冲突时 fail-closed 报错。 |

### 3.3 工作区、知识库、Skill、DoSql 与宿主诊断目录

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_WORKSPACE_ROOT` | 否 | `/root/ws` | 目录路径 | Agent 会话工作区根目录（每个 session 对应 `<root>/<session>`）。 |
| `DOOPS_KB_ROOT` | 否 | `/root/kb` | 非根绝对路径 | Agent 本地知识库文档根目录；相对路径或 `/` 将被拒绝。 |
| `DOOPS_AGENT_SKILLS_ROOT` | 否 | `/root/.agent/skills` | 目录路径 | Gateway 同步下发的平台/自定义 Skill 在 Agent 内的落盘根目录。 |
| `DOOPS_DOSQL_STATE_DIR` | 否（生产建议配 PVC） | `<dirname(DOOPS_WORKSPACE_ROOT)>/.doops-dosql-versions` | 绝对路径（如 `/var/lib/doops-dosql`） | 统一存放 DoSql 哈希链版本日志、SQL 历史、SchemaIR 快照与外置驱动（对应 Helm `dosql.stateDir`）。设相对路径报 `dosql_state_dir_invalid`。 |
| `DOOPS_DOSQL_DRIVER_DIR` | 否 | `<DOOPS_DOSQL_STATE_DIR>/drivers` 或 `<dirname(workspaceRoot)>/.doops-dosql-drivers` | 目录路径 | 单独覆盖 DoSql 外置数据库驱动可执行文件目录（`dosql-driver-<scheme>`）。 |
| `DOOPS_AGENT_MACHINE_DIAGNOSTICS_ROOT` | 否 | 空（禁用宿主样本采集） | 目录路径（如 `/var/lib/doops-host-samples`） | 宿主机只读定时采样服务（`doops-host-sample.service`）输出目录挂载点，详见 [宿主机采样](MACHINE_SAMPLING.md)。 |

### 3.4 传输体积与消息上限

| 变量 | 默认值 | 合法取值 | 说明 |
| --- | --- | --- | --- |
| `DOOPS_MAX_WORKSPACE_UPLOAD_BYTES` | `536870912`（512 MiB） | 正整数（字节） | 单次工作区上传/同步体积上限。 |
| `DOOPS_MAX_FILE_READ_BYTES` | `1048576`（1 MiB） | 正整数（字节） | `doops read` 单文件读取上限。 |
| `DOOPS_MAX_FILE_WRITE_BYTES` | `16777216`（16 MiB） | 正整数（字节） | `doops write` 单文件写入上限。 |
| `DOOPS_WORKSPACE_PULL_CHUNK_BYTES` | `524288`（512 KiB） | 正整数（字节） | `doops pull --storage gateway` 分块拉取单块字节数。 |
| `DOOPS_MAX_WS_MESSAGE_BYTES` | `33554432`（32 MiB） | 正整数（字节） | Gateway ↔ Agent WebSocket 单条消息字节上限（两侧须匹配）。 |

### 3.5 AI 引擎（`doagent`）与工具集成覆盖

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DO_AGENT_PORT` | `9000` | 容器内 `do-agent acp-http` 监听端口（仅监听回环，勿暴露公网）。 |
| `DO_AGENT_URL` | `http://127.0.0.1:<DO_AGENT_PORT>` | Agent 访问内部 `doagent` ACP HTTP 服务的完整地址；自定义时须与 `DO_AGENT_PORT` 一致。 |
| `DOWEB_SCRIPT` | 内置 `/app/skills/doweb/scripts/doweb.mjs` | 覆盖 DoWeb 脚本路径（调试/自定义安装使用）。 |
| `DOWEB_NODE` | `node` | 覆盖执行 DoWeb 脚本的 Node.js 可执行文件路径。 |
| `DOWEB_RUNTIME_CONFIG` | 空 | 指定 DoWeb 运行时配置文件路径（可选覆盖）。 |
| `DOSQL_NODE` | 空 | 仅在回落外部 Node 脚本驱动模式时指定 Node 路径（现行默认走原生 Go 引擎）。 |
| `DOSQL_SCRIPT` | 空 | 仅在回落外部 Node 脚本驱动模式时指定 DoSql 脚本路径。 |
| `KUBECTL` | `kubectl` | 覆盖 DoSql / Agent 调用 `kubectl` 的二进制路径。 |

### 3.6 容器入口与旧版可选开关（`sandbox-entrypoint.sh` / `agent-entrypoint.sh`）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DOOPS_ENABLE_WEBIDE` | `0`（关闭） | 仅旧版 `agent-entrypoint.sh` 识别；设为 `1` 才拉起容器内 WebIDE，默认关闭以收敛暴露面。 |
| `DOOPS_ENABLE_SSHD` | `0`（关闭） | 仅旧版 `agent-entrypoint.sh` 识别；设为 `1` 才拉起 `sshd`（现行正式镜像已彻底移除 `sshd`）。 |
| `PUBLIC_PORT` | `8080` | `sandbox-entrypoint.sh` 透传给底层沙箱服务的公共端口。 |
| `CODE_SERVER_PORT` | `8200` | `sandbox-entrypoint.sh` 透传给底层 code-server 的端口（若存在）。 |
| `WORKSPACE` | `/root/ws` | `sandbox-entrypoint.sh` 透传给底层沙箱服务的工作区路径。 |
| `WAIT_PORTS` | 空 | `sandbox-entrypoint.sh` 透传给底层沙箱服务的等待端口列表。 |

### 3.7 Agent 侧运行时超时覆盖（启动即校验，非法值 fatal）

精确合法范围与不等式（I1、I2、I10 等）见 [运行时超时与限额](RUNTIME_LIMITS.md)：

| 变量 | 默认值 | 简要说明 |
| --- | --- | --- |
| `DOOPS_TOOL_TIMEOUT` | `30m` | 单次工具执行上限（对应 Helm `toolTimeout`，兼容裸秒如 `1800`）；显式设置时硬顶 Gateway 广告超时 |
| `DOOPS_COMPLETED_TASK_MAX_AGE` | `24h` | 已完成后台任务记录的惰性淘汰窗口（兼容裸秒） |
| `DOOPS_TOOL_DEADLINE_MARGIN` | `5s` | Agent 提前于 Gateway 截止的余量 |
| `DOOPS_HELM_SETTLE_RESERVE` | `90s` | `doops_helm` 尾部预留给 SIGTERM 宽限与 settle 的余量 |
| `DOOPS_HELM_SETTLE_TIMEOUT` | `45s` | 读 Helm history / 解锁 pending release 的上限 |
| `DOOPS_HELM_MAX_WAIT` | `25m` | 单次 `doops_helm` `--wait` 上限 |
| `DOOPS_TUNNEL_READ_TIMEOUT` | `90s` | Agent 侧隧道 socket 空闲读死线（兼容裸秒，须 `≥ DOOPS_AGENT_LEASE / 3`） |
| `DOOPS_AGENT_EXECUTION_TTL` | `30m` | Agent 侧断线续接执行记录保留时间 |
| `DOOPS_MCP_PROBE_TIMEOUT` | `12s` | 自定义 MCP Server 连通探测超时 |
| `DOOPS_EDGE_DIAL_TIMEOUT` | `5s` | Edge 代理拨号上游超时预算 |
| `DOOPS_EDGE_WS_READ_TIMEOUT` | `20s` | Edge WebSocket 空闲读死线 |

---

## 4. Web 控制台环境变量（Next.js BFF）

对应配置文件模板：仓库根目录 `.env.example`（生产部署脚本：`backend/scripts/deploy-web-console.sh`）。

| 变量 | 必填 | 默认值 | 合法取值 / 格式 | 说明与失败行为 |
| --- | --- | --- | --- | --- |
| `DOOPS_CONSOLE_ALLOWED_GATEWAYS` | **生产必填** | 空（开发仅放行本机回环；生产 fail-closed 拒绝所有请求） | 逗号分隔的 Gateway origin，如 `https://gateway.example.com`（不得含路径、查询串或认证信息） | 控制台服务端代理允许访问的 Gateway 白名单（防止 SSRF）。`NODE_ENV=production` 下未配置时所有请求返回 `403 控制台未配置 gateway allowlist`，且非回环目标必须为 `https://`。 |
| `DOOPS_CONSOLE_ORIGIN` | 反向代理改变对外来源时必填 | 空（从请求 URL 与 `Host` 推导） | 小写 HTTPS origin，如 `https://doops.example.com`（不含路径） | 控制台变更接口的同源 CSRF 校验基准；当 Nginx/Ingress/CDN 终止 TLS 且改变上游 `Host` 或协议时必须显式配置。 |
| `DOOPS_CONSOLE_TRUSTED_PROXY_HOPS` | 反代登录限流时配置 | `0`（禁用转发） | 整数 `1..16`（如 Nginx 单层反代填 `1`，CDN + Nginx 填 `2`） | 指定浏览器到 Next.js 之间的可信反向代理层数，从 `X-Forwarded-For` 右侧取第 N 个合法 IP 供登录限流使用；非法值或 `0` 关闭转发。 |
| `DOOPS_CONSOLE_FORWARD_SECRET` | 反代登录限流时配置 | 空 | 与 Gateway 侧 `DOOPS_CONSOLE_FORWARD_SECRET` 完全一致的高熵随机串 | 对 `X-DoOps-Client-IP` 做 HMAC-SHA256 签名后传给 Gateway；与 `DOOPS_CONSOLE_TRUSTED_PROXY_HOPS` 成对配置。 |
| `NEXT_PUBLIC_DOOPS_DEMO` | 否 | 未设置（生产关闭；开发模式默认允许） | `1`（在生产构建中显式开放只读演示模式） | 决定浏览器端是否允许进入无后端的演示模式；**构建期变量**，需在 `pnpm build` 时传入才会在静态包中生效。 |
| `NODE_ENV` | 容器镜像已置为 `production` | 开发 `development` / 生产镜像 `production` | `development` \| `production` | 控制 Cookie `Secure` 属性、严格 CSP（生产禁用 `'unsafe-eval'`）、HTTPS 强校验与 Gateway 白名单 fail-closed 开关。 |
| `HOSTNAME` / `PORT` | 容器镜像默认 `0.0.0.0` / `3000` | `0.0.0.0` / `3000` | 绑定地址 / 端口号 | Next.js standalone 服务监听地址与端口。 |

---

## 5. `doops` CLI 与运维脚本环境变量

| 变量 | 适用入口 | 默认值 | 说明与注意事项 |
| --- | --- | --- | --- |
| `DOOPS_CONFIG` | `doops` CLI | `~/.agent/skills/doops/config.json` | 覆盖 CLI 配置文件路径（同时改变相邻 `auth.json` 登录态位置）；仅用于隔离测试，日常使用请保留默认路径。 |
| `DOOPS_CLI_CALL_TIMEOUT` | `doops` CLI | `60m` | 单次 CLI 工具调用超时（Go duration，如 `15m`、`2h`）；非法值回退 `60m`。 |
| `DOOPS_ALLOW_INSECURE_GATEWAY` | `doops` CLI | `0` | 设为 `1` 允许连接非回环明文 `http://`/`ws://` Gateway；仅限内网调试，生产禁开。 |
| `DOOPS_RELEASE_ASSET_BASE` | `backend/scripts/install.sh`、`fetch-gateway-release.sh` | `https://github.com/symansun/doops/releases/download` | 将发布制品下载源指向私有镜像站；sha256、提交身份与契约校验不变。 |
| `DOOPS_RELEASE_REPO` | `backend/scripts/fetch-gateway-release.sh` | `symansun/doops` | 覆盖 GitHub Release 仓库标识（`<owner>/<repo>`）。 |
| `DOOPS_AGENT_IMAGE_REPOSITORY` | `backend/scripts/bootstrap-host-agent.sh` | 无（必填） | 空服务器首次 Docker 安装时显式指定的 Agent 镜像仓库（如 `registry.example.com/doops-agent`）。 |
| `DO_AGENT_IMAGE` / `DOOPS_AGENT_BASE_IMAGE` | 镜像构建脚本（见 `backend/runtime-versions.env`） | 无（构建时必填） | 不可变 digest 形式的 `doagent` 与 `base-light` 源镜像地址；缺失或与 `DO_AGENT_INDEX_DIGEST` 不匹配时构建脚本 fail-closed 失败。 |

---

## 6. 系统内部自动注入变量（禁止手工写入配置文件）

以下变量由 Gateway、Agent 或 CLI 在单次任务执行、容器启动或升级 Job 期间**动态生成并仅注入到对应子进程**，严禁运维手动硬编码到 `/etc/doops-gateway.env`、Helm `values.yaml` 或 Git 中：

| 变量 / 前缀 | 注入方 | 作用与安全边界 |
| --- | --- | --- |
| `DO_AGENT_MUTATION_AUTHORITY_SECRET`、`DO_AGENT_MUTATION_AUTHORITY_TOKEN`、`DO_AGENT_SHELL`、`DO_AGENT_BASH_WAIT_FOR_EXIT` | `sandbox-entrypoint.sh` / Agent | Agent 进程与 `doops-shell` 之间的本地变更鉴权密钥、短期令牌与受控 Shell 包装参数；容器启动自动生成 64 位随机十六进制密钥并从通用环境清除导出。 |
| `DOOPS_UPGRADE_*`（含 `DOOPS_UPGRADE_OPERATION_ID`、`DOOPS_UPGRADE_IMAGE`、`DOOPS_UPGRADE_DIGEST`、`DOOPS_UPGRADE_REPOSITORY`、`DOOPS_UPGRADE_TAG`、`DOOPS_UPGRADE_RELEASE`、`DOOPS_UPGRADE_NAMESPACE`、`DOOPS_UPGRADE_WORKLOAD`、`DOOPS_UPGRADE_REVISION`、`DOOPS_UPGRADE_BASELINE`、`DOOPS_UPGRADE_KUBECONFIG_SOURCE`、`DOOPS_UPGRADE_EXPECTED_RELEASE_COMMIT`、`DOOPS_UPGRADE_VERIFICATION_STAGE`、`DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE`、`DOOPS_UPGRADE_ROLLBACK_OUTCOME`） | `doops upgrade` 控制器 Job | 独立 Kubernetes 升级/预检/回滚 Job 的只读执行上下文，由升级控制器自动拼装。 |
| `DOOPS_SKILL_DIR`、`DOOPS_SKILL_SLUG`、`DOOPS_TARGET_CLUSTER`、`DOOPS_TARGET_INSTANCE`、`DOOPS_RUN_ID`、`DOOPS_CATALOG_DATABASES`、`DOOPS_CATALOG_IMAGES`、`DOOPS_INSPECT_URLS`、`DOOPS_INSPECT_DOMAINS`、`DOOPS_PLUGIN_*`、`DOOPS_INPUT_*` | Gateway 巡检调度器 | 运行系统 Skill 或插件巡检脚本时，由 Gateway 按当前目标授权目录、运行上下文与受控输入自动单引号安全注入。 |
| `DOOPS_CRED_FILE_*` | Agent `runtime-opaque` 物化器 | 指向一次性 `0600` 运行时凭据文件的临时路径，任务结束后自动覆写粉碎。 |
| `DOOPS_GIT_TOKEN_FILE`、`DOOPS_GIT_USERNAME`、`DOOPS_GIT_SECRET` | `doops push` / `doops pull` 与 Agent 凭据探测 | CLI 同步工作区或 Agent 探活 Git 凭据时传递给临时 `GIT_ASKPASS` helper 的一次性令牌路径/内存变量，退出即清理，不落盘、不进日志。 |
| `DOOPS_SESSION` | Agent Shell 审计钩子 | 标识当前 bash 会话审计日志文件（`/root/.doops/audit/<session>.log`）。 |
| `DOSQL_DRIVER_SCHEME`、`DOSQL_CONNECTION_URI`、`DOSQL_DATABASE`、`DOSQL_MONGODB_URI`、`DOSQL_MONGODB_DATABASE`、`DOSQL_MONGODB_REQUIRE_READ_ONLY_ROLE`、`DOSQL_MONGODB_OPERATION` | Agent DoSql 运行器 | 内部调用 `dosql-driver` 或数据库子进程时传入的单次物化连接与操作上下文，不落盘、不进 prompt。 |
| `DOOPS_UPLOAD_OK` | Web 控制台文件上传脚本 | 控制台大文件上传流式回执的内部状态标记字面量，非配置项。 |
