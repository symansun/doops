# DoOps 深度评审（2026-11-22）

本文从 Gateway、Agent、原子运维工具、Skill、CLI、Web 六个方面评审 DoOps，并附供应链与交付流程的观察。
它是开发过程记录，不是产品文档，不随 CLI、Skill 或 Agent 镜像发布。

| 项 | 内容 |
| --- | --- |
| 基线提交 | `85edd83e8c5fa03c1785dd9c92b8d7276d04852c`（`main`，与 `github/main` 同步） |
| 评审对象 | 基线提交加评审时刻的未提交工作区 |
| 评审时间 | 2026-11-22 14:11–14:45（Asia/Shanghai） |
| 本地工具链 | Go 1.26.2 darwin/arm64、Node v24.14.0、Python 3.14.7、pytest 9.0.3 |
| 是否改动代码 | 否。只新增本文件 |
| 去重 | [2026-11-21 整改交付记录](2026-11-21-doops-remediation.md) 中已列的条目不重复报告 |

评审期间工作区有并行改动：`package.json` 和 `pnpm-lock.yaml` 在 14:32 被修改，`next-server` 进程在运行，
`components/` 下多个文件的修改时间落在评审窗口内。Web 部分的结论、行号和行数以评审时刻的快照为准。

## 证据与级别的标注

| 标注 | 含义 |
| --- | --- |
| 已复现 | 运行了测试或命令，得到了文中描述的行为 |
| 代码路径确认 | 代码路径确定地导出该行为，但没有实际运行 |
| 候选 | 有线索，未达到确认门槛，需要进一步验证 |
| 设计事实 | 项目文档已写明的取舍，这里只记录它的后果 |

级别分高、中、低三档。高表示应在下一次发布前处理；中表示应排期处理；低表示顺手清理或记录即可。

## 1. 总体结论

后端安全关键路径的工程质量高：每次工具调用都重新校验 token 和授权，审计失败即拒绝执行，
结构化工具用白名单加 argv 直传，发布流水线有 digest 固定和构建证明。三个 Go 模块的测试、
486 项 Python 契约测试和 92 项 DoSql 测试在工作区上全部通过。

需要优先处理的有两件事：

1. Agent 注册默认不需要任何凭据，而且新会话可以顶替在线会话并把原 Agent 锁在外面（GW-01）。
   这是文档写明的设计取舍，但默认值不安全，后果被 Agent 的高权限运行方式放大。
2. Web 工作区删除了全部 46 个已跟踪的测试文件和两个测试脚本，CI 仍在调用这两个脚本（WEB-01）。
   按评审时刻的状态提交，CI 会失败。

其余问题集中在跨层契约漂移：Web 与 Agent（配置中心）、CLI 与 Skill（断连终态口径）、
文档与安装包（迁移 SQL）、README 与代码。

## 2. 优先级清单

| 编号 | 级别 | 问题 | 证据 |
| --- | --- | --- | --- |
| GW-01 | 高 | Agent 注册默认开放，无 token 可顶替在线 target，原 Agent 被锁在外 | 已复现 |
| WEB-01 | 高（交付） | 工作区删除 46 个 Web 测试和两个测试脚本，CI 仍调用它们 | 已复现 |
| WEB-02 | 中 | 测试清单手工维护，`node --test` 静默跳过不存在的文件 | 已复现 |
| WEB-03 | 中 | 配置中心两类发布都不可用，相对路径会误报"已写入" | 已复现 |
| GW-02 | 中 | Agent 升级准入可被持续读流量饿死，`draining` 是死字段 | 已复现 |
| GW-03 | 中 | 改密不吊销登录 token，密码只要求非空，可停用最后一个管理员 | 代码路径确认 |
| GW-04 | 中 | 控制台登录共用一个限流桶，可被用来锁住其他用户 | 代码路径确认 |
| GW-07 | 中 | 资产台账表靠 14 个手工 SQL，与自动迁移并存 | 代码路径确认 |
| SK-01 | 中 | Operator Skill 的 `name` 与目录不符，description 过薄 | 代码路径确认 |
| SK-02 | 中 | 安装包只带 3 个迁移 SQL，文档引用 14 个 | 已复现（已安装副本） |
| CLI-01 | 中 | 断连返回 `connection lost`，与 Skill 红线的 `outcome_unknown` 口径不一 | 代码路径确认 |
| AG-01 | 中 | Gateway 与 Agent 同在一个包 | 代码路径确认 |
| AG-02 | 中 | 工具注册表散在多处，`doops_docker` 是不可达残留 | 代码路径确认 |
| 其余 | 低 | 见各节：GW-05、GW-06、AG-03、TOOL-02、SK-03、CLI-02～04、WEB-04～07、SC-01～02、DOC-01 | 见各节 |

## 3. 覆盖矩阵

| 方面 | 检查范围 | 探针 | 结果 |
| --- | --- | --- | --- |
| Gateway | 路由注册、用户认证、target 授权、Agent 注册、并发锁、审计、管理接口、公网代理、资产接口鉴权 | 读代码；两个临时探针测试；现有测试 | 2 项已复现，4 项代码路径确认，2 项低 |
| Agent | 工具分发、命令执行、doagent 桥接入口、变更授权签名、Helm Chart | 读代码；`go tool nm` 查链接符号 | 2 项结构问题，1 项候选 |
| 原子运维工具 | 命令过滤、K8s、SSH、DoSQL 直连、文件读写、部署检查 | 读代码；一个临时探针测试 | 无确认缺陷，1 项低 |
| Skill | Operator Skill、Agent 端 6 个 Skill、system prompt、安装与打包脚本、契约测试 | 读文件；对照 Agent Skills 规范；检查已安装副本 | 2 项中，1 项低 |
| CLI | 登录态、配置、RPC 终态、历史、命令分发 | 读代码；现有测试 | 1 项中，3 项低 |
| Web | 服务端代理、会话 cookie、同源校验、Gateway 白名单、组件结构、测试、配置中心 | 读代码；`tsc`；`node --test` 行为实验 | 1 项高，2 项中，4 项低 |
| 供应链与交付 | CI、发版流水线、Dockerfile、Helm 默认值 | 读配置 | 2 项低 |

没有覆盖的范围见第 7 节。

## 4. 详细发现

### 4.1 Gateway

#### GW-01 Agent 注册默认开放，在线 target 可被无凭据顶替

- 级别：高
- 证据：已复现（临时探针测试，见附录 A.1）；另有现有测试断言了其中两步

**现象**

1. 未确认 enrollment 的 target，`/v1/agent/connect` 不要求任何凭据。
   见 [agent_registration.go](../../agent/internal/server/agent_registration.go#L47-L71)。
2. `AgentRegistry.Register` 把同一 `cluster/instance` 的旧会话直接替换，并关闭旧连接。
   见 [agent_registry.go](../../agent/internal/server/agent_registry.go#L52-L102)。
3. 被替换会话的 runtime ID 进入 retired 列表。它重连时只要当前有会话在线，就被拒绝并返回
   `was superseded`。runtime ID 在 Agent 进程启动时生成，进程不重启就不变
   （[handler_ws.go](../../agent/internal/server/handler_ws.go#L613)）。
4. 注册成功后，Gateway 主动向该会话物化这个 target 的模型密钥和 registry 凭据。
   见 [registry_auth_delivery.go](../../agent/internal/server/registry_auth_delivery.go#L62-L68) 和
   [model_provider_delivery.go](../../agent/internal/server/model_provider_delivery.go#L60-L72)。
5. 现有测试 `TestGatewayAgentRegistrationStaysOpenUntilEnrollmentIsConfirmed` 断言了第 1 步和第 4 步：
   不带凭据拨号后，凭据下发回调被调用一次
   （[gateway_protocol_test.go](../../agent/internal/server/gateway_protocol_test.go#L106)）。
6. enrollment token 只能在 Gateway 宿主机上用 `doops-gateway enroll create` 签发
   （[gatewaycmd.go](../../agent/pkg/gatewaycmd/gatewaycmd.go#L172)），HTTP 管理接口和控制台没有入口。
   Helm Chart 默认 `enrollmentTokenSecret: ""`
   （[values.yaml](../../deploy/helm/doops-agent/values.yaml#L35)）。

**探针输出**

```text
PROBE: tokenless session replaced the live agent for prod/node-1
PROBE: original agent reconnect result: agent runtime "legit-host:101:1" was superseded for prod/node-1
```

**滥用路径**

前提有两个：知道某个未确认 enrollment 的 target 的 `cluster/instance`；能访问 Gateway 地址
（`/v1/agent/connect` 与用户接口在同一端口）。满足后，用任意 WebSocket 客户端应答 `initialize` 和心跳
即可成为这个 target。未认证的调用方不能通过 Gateway 列出 target，所以名字需要另行获知或猜到。

**影响**

- 取得 Gateway 下发的模型密钥和 registry 凭据，以及之后用户用 `--credential` 物化的凭据。
- 收到用户发往该 target 的命令和 prompt。
- 可以伪造执行结果和 `release_commit`。
- 真实 Agent 被拒绝重连，直到顶替者断开或真实 Agent 进程重启。

**已有防护**

enrollment 机制本身是完整的：token 用 bcrypt 存储，绑定到具体 `cluster/instance`，确认后强制，
注册时在锁内复查。问题只在于默认不启用，且启用入口在宿主机上。

**修复建议（改动从小到大）**

1. 不带 token 的会话不得替换在线会话。真实 Agent 重启时旧连接会先断开并注销，所以正常重连不受影响；
   半开连接由现有的 90 秒租约兜底。
2. 凭据下发只对已确认 enrollment 的会话执行。这一条直接切断"无认证拿凭据"，但未 enrollment 的存量
   Agent 重启后会拿不到模型，需要迁移窗口。
3. 把签发入口加到管理接口和控制台，并在实例列表标出"未强制 enrollment"的 target。
4. 新 target 默认强制 enrollment，开放注册改为显式开关。

**验证**

把附录 A.1 的探针转成回归测试，并按新语义调整上面提到的现有测试。这属于权限与凭据边界的改动，
按 `AGENTS.md` 的 T2 要求补齐 Given/When/Then 矩阵。

#### GW-02 Agent 升级准入可被持续读流量饿死

- 级别：中
- 证据：已复现（临时探针测试，见附录 A.2）

**现象**

`target:` 独占锁（Agent 升级使用）要求读者和写者同时为 0
（[tunnel_hub.go](../../agent/internal/server/tunnel_hub.go#L2343-L2347)）。
但升级在排队时，新读者照常被准入：`acquireReadOnly` 只检查独占锁是否已被持有，不检查是否有人在等
（[tunnel_hub.go](../../agent/internal/server/tunnel_hub.go#L2518-L2527)）。
本应阻止新准入的 `draining` 字段在 4 处被读取，全仓库没有一处赋值
（[tunnel_hub.go](../../agent/internal/server/tunnel_hub.go#L183)）。

**探针输出**

```text
PROBE: reader 2 admitted while the upgrade is queued: err=<nil>
PROBE: upgrade admission result with overlapping readers: target queue timeout
```

**影响**

读者包括节点信息、K8s 只读查询、公网代理的 GET 请求，以及每个持有工作区槽位的 `ask`。
只要这些操作在时间上持续重叠，`doops upgrade` 就会等到队列超时（默认 10 分钟）后失败。
越忙的 target 越难升级。

**修复建议**

有独占等待者时把 `draining` 置为真，让新准入排队或快速失败；等待者获准或超时后清除。
如果不打算做写者优先，就删除这个字段和 4 处读取，避免误导。

**验证**

把附录 A.2 的探针转成回归测试：升级排队期间新读者不被准入，已有读者释放后升级获准。

#### GW-03 改密不吊销登录 token，密码与管理员缺少基本约束

- 级别：中
- 证据：代码路径确认

**现象**

- `SetUserPassword` 只更新密码哈希
  （[gateway_store.go](../../agent/internal/server/gateway_store.go#L603-L621)）。
  密码登录签发的 token 是 30 天滑动续期、最长 90 天
  （[login_session.go](../../agent/internal/server/login_session.go#L10-L13)）。
  管理员为泄露的账号重置密码后，旧密码签发的 token 仍然有效。
- `hashPassword` 只拒绝空白密码
  （[gateway_store.go](../../agent/internal/server/gateway_store.go#L1229-L1238)）。
- `HandleAdminUserDisable` 不检查被停用的是不是自己或最后一个管理员
  （[admin_http.go](../../agent/internal/server/admin_http.go#L152-L175)）。
  所有管理员都被停用后，HTTP 管理面无法自救。

**修复建议**

1. 改密时吊销该用户经密码登录签发的 token，或提供显式的"同时吊销全部 token"选项。
2. 设置最小密码长度。
3. 拒绝停用最后一个启用状态的管理员。

**验证**

为三条各加一个存储层测试和一个 HTTP 层测试，断言旧 token 在改密后返回 `gateway_auth_revoked`。

#### GW-04 控制台登录共用一个限流桶

- 级别：中
- 证据：代码路径确认

**现象**

登录限流按客户端地址和"地址加用户名"两个维度计数，注释说明它不按用户名单独限流，
以免别人从另一个地址把某个用户锁住
（[login_limit.go](../../agent/internal/server/login_limit.go#L125-L143)）。
但控制台的登录请求由 Next 服务端转发，转发时不带客户端地址
（[login/route.ts](../../../app/api/login/route.ts#L31-L36)）。
Gateway 看到的地址始终是 Next 服务端，于是所有控制台用户落在同一个桶里。

**影响**

默认每个"地址加用户名"每分钟 10 次失败、每个地址每分钟 50 次失败。任何人通过控制台对某个用户名
每分钟提交 10 次错误密码，该用户就无法从控制台登录；每分钟 50 次则所有人都无法从控制台登录。
CLI 直连 Gateway 的登录不受影响。

**修复建议**

Next 登录路由把真实客户端地址通过可信请求头传给 Gateway。Gateway 现有逻辑已经只在对端是回环或
私网地址时才采信这类请求头，需要补的是 Next 一侧的取值和部署文档里的信任链说明。

#### GW-05 公网代理与控制面共用审计和锁

- 级别：低
- 证据：代码路径确认

每个经公网代理的请求都会写一条审计，摘要是方法加完整的 `RequestURI`，包含 query string
（[proxy_tunnel.go](../../agent/internal/server/proxy_tunnel.go#L83-L87)）。
非只读方法使用按路由的单槽资源锁，同一路由的 POST 请求串行执行，排队超过上限后返回 502
（[proxy_tunnel.go](../../agent/internal/server/proxy_tunnel.go#L39-L45)）。
这个功能需要管理员配置路由、Agent 配置允许的上游后才生效。

建议在文档里写明它适合低流量的管理类站点，或者对代理流量改用聚合审计并去掉 query string。

#### GW-06 token 每次请求做 bcrypt 校验

- 级别：低
- 证据：代码路径确认；耗时为推断，未实测

用户 token 是 256 位随机数，却用 bcrypt 默认强度存储，每个请求校验一次
（[gateway_store.go](../../agent/internal/server/gateway_store.go#L718-L735)）。
管理类变更请求先在审计包装里认证一次，再在处理函数里认证一次
（[admin_audit.go](../../agent/internal/server/admin_audit.go#L101-L107)、
[admin_http.go](../../agent/internal/server/admin_http.go#L334-L346)）。
bcrypt 默认强度通常是每次数十毫秒量级的 CPU，这一条没有在本项目上实测。
另外 Gateway 的 SQLite 连接池上限是 1
（[gateway_store.go](../../agent/internal/server/gateway_store.go#L256)），所有数据库访问串行。

高熵随机 token 用 SHA-256 存储即可。迁移时可以双格式并存校验。管理路径可以把第一次认证的结果
放进请求上下文，省掉第二次。

#### GW-07 资产台账表靠手工 SQL，与自动迁移并存

- 级别：中
- 证据：代码路径确认

核心表由 `migrate()` 在启动时创建。资产台账的表只由操作员手工执行 SQL 创建，缺表时接口返回服务错误
（[machine_inventory.go](../../agent/internal/server/machine_inventory.go#L83-L84)）。
[API 接入指南](../DOOPS_API_GUIDE.md) 列了 13 步迁移顺序，`migrations/` 下现有 14 个 SQL 文件。
这和 Operator Skill 红线里"不 SSH 改 Gateway DB"的方向相反，也是 SK-02 的成因。

建议把这些 SQL 纳入带版本表的自动迁移，或提供 `doops-gateway migrate` 子命令统一执行并留回执。

### 4.2 Agent

#### AG-01 Gateway 与 Agent 同在一个包

- 级别：中
- 证据：代码路径确认，另有构建产物和测试耗时

`internal/server` 一个包同时承载 Gateway 控制面和 Agent 运行时，源码约 4.6 万行、测试约 5 万行。
包内 README 专门有一节"命名陷阱"，说明 `Gateway` 类型实际运行在 Agent 进程里
（[README.md](../../agent/internal/server/README.md)）。

可量化的代价：

- 本机构建的 Gateway 二进制 46.8 MB，其中链接了 Oracle、MSSQL、Mongo、MySQL 驱动的 2261 个符号。
  这些驱动只有 Agent 的 DoSQL 使用，却进入了面向用户网络的控制面进程。
  [gateway/go.mod](../../gateway/go.mod) 的间接依赖里能看到这些驱动和 AWS SDK。
- 该包的测试单次运行 760 秒，CI 给整个模块的上限是 20 分钟。包内任何改动都要重跑全部测试。

建议按 README 里已有的文件前缀分类拆包：纯 Gateway、纯 Agent、共享协议三部分。
先移动没有交叉引用的文件。验证方式是 `go tool nm` 确认 Gateway 二进制不再含数据库驱动符号。

#### AG-02 工具注册表散在多处

- 级别：中
- 证据：代码路径确认

增加一个工具需要同时修改：Gateway 的 `actionForTool`、`resourceKeyForTool`、调用摘要和结果守卫
（[tunnel_hub.go](../../agent/internal/server/tunnel_hub.go#L2781-L2833)），Agent 的分发 switch，
以及 CLI 和 Web 各自的调用构造。

漂移已经发生：`doops_docker` 在 Agent 有完整实现
（[handler_ws.go](../../agent/internal/server/handler_ws.go#L805-L841)），Gateway 的映射里没有它，
请求会在 Gateway 被当作未知工具拒绝。这段代码不可达，而且没有走其它分支使用的策略拒绝处理。

建议用一张工具描述表统一声明名称、动作、资源键、摘要和处理函数，并删除 `doops_docker`。

#### AG-03 超大文件和函数

- 级别：低
- 证据：行数统计

| 位置 | 规模 |
| --- | --- |
| `handler_ws.go` | 3262 行 |
| `tunnel_hub.go` | 3033 行 |
| `self_upgrade.go` | 1921 行 |
| `credential_store.go` | 1715 行 |
| `handleToolCallOverWS` | 约 650 行 |
| `handleGatewayToolCall` | 约 510 行 |
| `handleAgentPromptWS` | 约 400 行 |

`tunnel_hub.go` 里的并发锁用字符串前缀区分语义（`target:`、`target-mutation:`、`session-mutation:`、
`workspace:`、`query:`、`nginx:`、`proxy:`），判断散在多个函数里。GW-02 就出在这套逻辑中。

#### AG-04 影响半径

- 级别：设计事实

Agent 的 Helm Chart 默认 `privileged: true`、共享宿主 PID 命名空间、挂载宿主根目录
（[deployment.yaml](../../deploy/helm/doops-agent/templates/deployment.yaml#L43-L61)），
可选绑定 `cluster-admin`
（[rbac.yaml](../../deploy/helm/doops-agent/templates/rbac.yaml#L30)）。
README 已说明 DoOps 不是强隔离沙箱。这里记录它是因为：一个 target 授权等于节点 root，
所以 GW-01 的后果被放大，Gateway 自身的安全等同于所有受管节点的安全。

#### AG-05 `ask` 的只读性依赖外部权限引擎

- 级别：候选

[自然语言业务发布](../NATURAL_LANGUAGE_DEPLOYMENT.md) 写明普通 `ask` 会话提供原生 Bash，
由 doagent 的权限引擎以 `restricted` 模式裁决。Gateway 把 `ask` 归类为非变更操作：
使用工作区级资源键，不加 target 变更锁，也没有结果未知守卫
（[tunnel_hub.go](../../agent/internal/server/tunnel_hub.go#L2883-L2886)）。
如果权限引擎放行了一条有变更效果的 Bash 命令，控制面记录的仍是一次读取。
doagent 是外部组件，本次没有检查它的裁决规则，所以只列为候选。

### 4.3 原子运维工具

抽查没有发现确认缺陷。

| 工具 | 检查点 | 结论 |
| --- | --- | --- |
| `doops_k8s` | 可读 kind 白名单，凭据类 kind 拒绝，参数正则校验，argv 直传 | 未发现问题 |
| `doops_ssh` | 选项白名单，`-o` 赋值逐项校验，远端 argv 逐项引用 | 未发现问题 |
| DoSQL 直连查询 | 首关键字判定；PostgreSQL、MySQL、Oracle 用只读事务；查询模式结束时不提交 | 未发现持久化写入路径 |
| `doops_file_read`、`doops_file_write` | `os.Root` 限定在会话工作区；拒绝绝对路径和 `..`；知识库只允许 `/root/kb/*.md` | 未发现问题（副作用见 WEB-03） |
| 命令过滤 | 一条内置规则加可配置正则 | 文档如实说明它不是安全边界 |
| `doops_check_deployment` | 子进程生命周期 | 见 TOOL-02 |

关于 DoSQL 的一点说明：SQL Server 没有使用只读事务，只读性由首关键字判定加结束时回滚保证。
以 `WITH` 开头的变更语句会被执行然后回滚，期间持有锁。序列自增这类不随事务回滚的副作用不在保证范围内。

#### TOOL-02 部署检查的子进程不受取消控制

- 级别：低
- 证据：代码路径确认

`handleCheckDeployment` 用不带 context 的 `exec.Command("kubectl", ...)`
（[workspace_upload.go](../../agent/internal/server/workspace_upload.go#L668-L695)）。
调用方取消或超时后子进程不会被终止，它也没有经过其它 kubectl 调用使用的 `dispatcher.CommandContext`。
这个函数放在 `workspace_upload.go` 里，位置也不对。建议改用带 context 的统一入口。

#### TOOL-03 命令过滤的边界

- 级别：低（说明）

内置规则只有一条针对 `rm -rf /` 形状的正则
（[dispatcher.go](../../agent/internal/dispatcher/dispatcher.go#L22-L28)）。
[命令过滤设计](../design/COMMAND_FILTER.md) 明确说它只检查命令形状，不承诺识别语义等价的写法。
代码和文档一致。Operator Skill 里"Agent 在执行前统一过滤命令"的表述容易被读成安全保证，
可以补一句指向设计文档的限定。

### 4.4 Skill

#### SK-01 Operator Skill 的 `name` 与目录不符，description 过薄

- 级别：中
- 证据：代码路径确认，对照 [Agent Skills 规范](https://agentskills.io/specification)

规范要求 `name` 与父目录名一致。Skill 的 frontmatter 是 `name: doops-server`
（[SKILL.md](../../skills/SKILL.md#L1-L5)），安装器把它放进 `skills/doops/`。
全仓库只有这一处出现 `doops-server`，改名不影响其它引用。

规范还要求 description 同时说明做什么和何时使用，并包含帮助匹配任务的关键词。
现在的 description 只有一句"用 doops 通过 doops-gateway 连接和操作主动注册的 doops-agent 节点"，
没有发布、排障、数据库、K8s、凭据这些触发场景。宿主 Agent 只凭 description 决定是否加载 Skill，
过薄会导致该用的时候没被加载。

建议改为 `name: doops`，并在 1024 字符内补全触发场景。

#### SK-02 安装包只带 3 个迁移 SQL

- 级别：中
- 证据：已复现（检查本机已安装副本）

[install.sh](../../scripts/install.sh#L183) 和
[package-skill-release.sh](../../scripts/package-skill-release.sh#L26)
都写死只复制 `machine_inventory`、`cluster_inventory`、`inventory_node_links` 三个文件。
源码里的文档现在引用 14 个迁移 SQL。

本机 `~/.agents/skills/doops/` 下的已安装副本：`docs/migrations/` 里有 3 个文件，
同一副本的 API 指南链接了 11 个，其中 8 个打不开。按当前源码发布后缺口会扩大到 11 个。
契约测试 `test_doc_links.py` 只在源码树里检查链接，源码树里文件齐全，所以发现不了。

建议改为复制 `migrations/*.sql`，并加一条契约测试：文档链接到的每个迁移文件都在打包清单里。
根本解法见 GW-07。

#### SK-03 Skill 体积接近上限

- 级别：低

`SKILL.md` 现为 11,327 字节，契约测试的上限是 12,000 字节
（[test_doc_links.py](../../test/test_doc_links.py#L18)），余量 673 字节。
命令选择表已经很密，再加命令就会碰到上限。可以把故障入口一节移到使用指南，只留链接。

#### SK-04 Agent 端 Skill 和 system prompt

- 级别：信息

6 个 Agent 端 Skill 和 system prompt 共 679 行。对证据、终态、授权沿用和交回上层的表述严谨，
没有发现相互矛盾的指令。frontmatter 的 `name` 都与目录一致。
system prompt 里"同一问题连续六次没有新证据就停止"是只写在提示词里的循环上限，运行时没有对应的强制手段。

### 4.5 CLI

#### CLI-01 断连与超时的终态口径不一

- 级别：中
- 证据：代码路径确认，现有测试断言了该行为

Operator Skill 的红线写的是：取消确认、超时或断连缺少终态时报告 `outcome_unknown`，不自动重放变更
（[SKILL.md](../../skills/SKILL.md#L42-L43)）。
CLI 在超时后返回 `execution_outcome_unknown`，但调用中途断连时返回的是
`connection lost: WS connection lost`
（[client.go](../../cli/cli/doops/client.go#L709-L715)，另有第 873 行和第 973 行两处相同逻辑）。
测试 [client_bg_test.go](../../cli/cli/doops/client_bg_test.go#L320) 把这个文本当作预期。

按错误码判断是否可以重放的自动化，会把断连当成普通失败并重试一次变更。

二选一：让变更类工具在断连时返回 `execution_outcome_unknown` 并附断连原因，同步调整测试；
或者在 Skill 的故障入口里把 `connection lost` 明确列为结果未知。前者更稳妥。

#### CLI-02 错误写到标准输出

- 级别：低
- 证据：计数

`main.go` 里有 83 处用 `fmt.Printf("Error...` 或 `fmt.Println("Error...` 把错误写到标准输出，
写标准错误的只有 2 处。退出码是正确的。但 `doops machines` 这类输出 JSON 的命令，
出错时错误文本会混进调用方要解析的输出流。

#### CLI-03 `main()` 一个函数 1115 行

- 级别：低

全部子命令的参数解析和分发都在 `main()` 里（[main.go](../../cli/cli/doops/main.go#L23)）。
每个分支重复同一段流程：解析参数、检查配置、找 target、取 token、建客户端。
`sql`、`web`、`image` 等较新的命令已经拆成独立函数，可以按同样方式逐步拆出其余分支。

#### CLI-04 两处不再使用的本地状态

- 级别：低
- 证据：代码路径确认

- `SessionStore` 只有读取逻辑，没有任何代码写入 `Sessions` 或 `sessions.json`
  （[config.go](../../cli/cli/doops/config.go#L28-L70)）。`Get` 只有在旧版本遗留文件存在时才会返回非空。
- `LLMConfig` 含 `api_key` 字段，加载后被丢弃，保存配置时又原样写回
  （[config.go](../../cli/cli/doops/config.go#L72-L81)）。CLI 不使用它，却让一个明文密钥槽位一直留在配置文件里。

### 4.6 Web

#### WEB-01 工作区删除了全部已跟踪的 Web 测试

- 级别：高（交付）
- 证据：已复现（文件状态）

基线提交里 `lib/` 下有 46 个测试文件，共 9,349 行。评审时刻的工作区：

- 这 46 个文件全部被删除，测试夹具 `lib/component-test-harness.mjs` 也被删除。
- [package.json](../../../package.json) 里的 `test:console` 和 `test:gateway-policy` 两个脚本被删除。
- [ci.yml](../../../.github/workflows/ci.yml#L63-L67) 没有改动，仍然执行 `pnpm test:gateway-policy` 和
  `pnpm test:console`。

按这个状态提交，CI 会因为脚本不存在而失败；发版流水线复用同一个 CI 作为闸门，也会被挡住。
工作区里新增了两个未跟踪的测试：`lib/machine-inspection.test.mjs` 通过；
`lib/markdown-render.test.mjs` 失败，原因是它导入的 `highlightCode` 在 `lib/markdown-render.ts` 里不存在。

工作区正在被并行修改，这可能是迁移的中间状态。提交前需要确认两件事之一：恢复测试和脚本；
或者把 CI 改成新的测试入口，并确认被删掉的覆盖面有替代。被删除的测试里包括同源校验、
Gateway 白名单、会话路由和 RPC 路由这些安全相关的用例。

#### WEB-02 测试清单手工维护，缺失文件被静默跳过

- 级别：中
- 证据：已复现

基线提交的 `test:console` 列了 47 个文件，其中 `lib/ui-kit.test.mjs` 和
`lib/machine-text-import.test.mjs` 在 git 历史里从未存在过。它们是提交 `cb4f9969` 加进清单的。

在 Node v24.14.0 上做的实验：

```text
$ node --test exists.test.mjs missing-file.test.mjs    # 一个存在，一个不存在
ℹ pass 1 ... 退出码 0

$ node --test missing-a.test.mjs missing-b.test.mjs    # 全部不存在
Could not find 'missing-a.test.mjs, missing-b.test.mjs'    退出码 1
```

只要清单里有一个文件存在，不存在的文件就被跳过且退出码为 0。所以这两个测试从未在 CI 里运行过，
CI 却是绿的。同样的机制意味着今后误删测试文件也不会被发现。

建议改用 glob（例如 `node --test "lib/**/*.test.mjs"`），或在运行前检查清单里的每个文件都存在。

#### WEB-03 配置中心两类发布都不可用

- 级别：中
- 证据：已复现（临时探针测试，见附录 A.3）

「平台管理」里的「配置中心」可以从导航进入（[console-views.ts](../../../lib/console-views.ts#L52)）。
它有两类配置，发布逻辑在 [model-configs.ts](../../../lib/model-configs.ts#L155-L172)。

- **模型类**：Provider、模型 ID、Base URL、凭证引用、Temperature、Max Tokens 都可以编辑并保存在浏览器里，
  但发布函数对这一类永远抛出"请在凭证管理中验证并启用目标的模型凭证"。这些字段不会被任何后端读取。
- **文件类**：预置示例的目标路径是 `/etc/nginx/conf.d/doops.conf`。Agent 的文件写入只接受会话工作区内的
  相对路径，绝对路径只允许 `/root/kb/*.md`
  （[workspace_upload.go](../../agent/internal/server/workspace_upload.go#L125-L137)）。
  所以预置示例必然失败。
- **文件类填相对路径**：写入成功，但落在 `/root/ws/<会话>/` 下，而界面显示的是"已写入"加用户填的路径
  （[admin-config-dispatch.tsx](../../../components/admin-config-dispatch.tsx#L182-L184)）。
  用户会以为配置已经生效。

**探针输出**

```text
PROBE: absolute path -> out="" err=unsafe workspace path: "/etc/nginx/conf.d/doops.conf"
PROBE: relative path -> out="Wrote 9 bytes to <工作区根>/cfg-1/nginx/doops.conf" err=<nil>
```

这是 Agent 收紧文件写入路径之后，Web 一侧没有跟进留下的。建议：模型类改成指向凭证管理的只读入口，
或直接移除；文件类要么移除，要么改走有明确路径白名单的专用工具；成功提示改为显示 Agent 返回的实际路径。

#### WEB-04 README 与代码不一致

- 级别：低
- 证据：代码路径确认

| README 位置 | README 的说法 | 代码现状 |
| --- | --- | --- |
| [第 205–217 行](../../../README.md#L205-L217) | `docker run` 启动控制台镜像即可使用 | 生产构建必须设置 `DOOPS_CONSOLE_ALLOWED_GATEWAYS`，否则登录返回"控制台未配置 gateway allowlist"；开发模式只允许本机 Gateway（[gateway-policy.ts](../../../lib/gateway-policy.ts#L62-L84)）。README 全文没有提到这个变量 |
| [第 571 行](../../../README.md#L571) | Next.js 会按请求里的 Gateway 地址做服务端访问，需要额外的出口约束 | 代码已强制白名单和 https |
| [第 567 行](../../../README.md#L567) | 不要把省略 actions 的授权当作普通用户模板 | 按动作授权已移除，授权只有 target 维度 |
| [第 487 行](../../../README.md#L487) | `logout` 不会删除目标配置中保存的 Token | target 配置只存路由，不再保存 token |

第一条影响首次使用：照 README 操作的人会得到一个无法登录的控制台。

#### WEB-05 服务端在进程内存里保存会话状态

- 级别：低
- 证据：代码路径确认

Next 服务端在进程内保存进行中的 RPC（[rpc/route.ts](../../../app/api/rpc/route.ts#L36)）、
AI 会话和待发送附件（[agent-attachments.ts](../../../lib/server/agent-attachments.ts#L11)，
附件落在本机临时目录）。取消请求只有落到发起请求的同一个进程才有效，重启会丢失进行中的会话。
这意味着控制台只能单副本运行，或者需要会话粘性。[WebUI 文档](../DOOPS_WEBUI.md) 没有说明这一点，建议补上。

#### WEB-06 目标列表由两个接口在前端合并

- 级别：候选

控制台把 `/v1/targets`（在线且已授权的 target）和 `/v1/assets/workspace?view=directory`
（已登记的 target）在前端合并，用后者补出离线 target
（[console-refresh.ts](../../../lib/console-refresh.ts#L66-L72)）。
第二个接口失败时只记录错误，列表退化为仅在线 target。两个来源的语义不同，目前没有发现错误行为。
如果 Gateway 能在一个接口里直接返回在线与离线状态，前端就不需要合并。

#### WEB-07 `console-shell.tsx` 集中了过多状态

- 级别：低

`console-shell.tsx` 评审时为 869 行，导入约 25 个面板和 20 个库模块，同时持有会话、target、标签页、
终端、AI 运行时、新手引导和更新日志的状态。各标签页已经是独立的具名组件，这一点做得对。
可以把会话与 target 刷新、终端预填、更新日志这几组状态各抽成一个 hook。
`credential-panel.tsx`（1115 行）和 `admin-console.tsx`（1014 行）也在同一量级。

### 4.7 供应链与交付

#### SC-01 测试与发版使用不同的 Go 工具链

- 级别：低
- 证据：代码路径确认

CI 按 `go.mod` 的声明安装 Go 1.24.0 跑测试
（[ci.yml](../../../.github/workflows/ci.yml#L31)）；
发版流水线固定用 Go 1.25.14 构建
（[release.yml](../../../.github/workflows/release.yml#L114)）；
Agent 镜像用按 digest 固定的 `golang:1.25-alpine`。
发布出去的二进制没有在构建它的工具链上跑过测试。建议让 CI 的测试也使用发版工具链，或加一个矩阵。

#### SC-02 三处按可变标签引用

- 级别：低

- GitHub Actions 全部按主版本标签引用（`actions/checkout@v4`、`gitleaks/gitleaks-action@v2` 等）。
  发版任务持有签发构建证明的权限，建议按提交 SHA 固定。
- [Dockerfile.web](../../../Dockerfile.web#L3) 的基础镜像是 `node:24-bookworm-slim`，没有 digest。
  Agent 的 Dockerfile 已经按 digest 固定，两者做法不一致。
- Agent Chart 的 `image.tag` 默认值是 `latest`。实际发布流程要求 digest，默认值可以改成空并强制填写。

#### DOC-01 评审记录的存放位置与文档治理规则冲突

- 级别：低

[文档索引](../README.md) 的治理规则写明：内部评审、复盘和验收证据不放进 `backend/docs/`，
应留在仓库外的开发归档里。但 `backend/docs/reviews/` 已经存在并被跟踪，本文件也放在这里。
需要二选一：修改治理规则，承认 `reviews/` 是例外；或者把这个目录移出。

## 5. 做得好的地方

这些是评审中确认过、建议保持的做法。

- **每次调用重新鉴权**。长连接上的每个 `tools/call` 都重新校验 token 状态和 target 授权，
  凭据物化前后再各校验一次。吊销 token 或授权后，已建立的连接立即失效。
- **审计失败即拒绝**。审计写不进去就不执行；管理类变更先探测完成写入是否可用；审计清理和它的回执在同一事务里。
- **审计索引有代码级校验**。`audit_indexes.go` 用 `EXPLAIN QUERY PLAN` 断言每种过滤组合都走索引。
- **结构化工具不拼命令行**。K8s 和 SSH 都是校验后的 argv 直传，没有 shell 拼接。
- **终态语义严格**。Gateway、Agent、CLI 三层都区分失败、取消、超时和结果未知，不自动重放变更。
- **Web 服务端代理**。生产环境强制 Gateway 白名单和 https；会话 cookie 是 HttpOnly；变更请求做同源校验；
  组件不直接发请求；没有使用 `dangerouslySetInnerHTML`。
- **登录防护的细节**。不存在的用户也走同样强度的 bcrypt 比较；限流表有容量上限。
- **发布流水线**。Agent 镜像的基础镜像和 doagent 按 digest 固定；发版以完整 CI 为闸门；
  产物有构建证明并在发布后重新下载校验。
- **文档诚实**。README 和设计文档对隔离边界、命令过滤、只读提示词的局限都有明确说明。

## 6. 验证记录

以下命令都在评审时刻的工作区上运行。

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| Go 构建与静态检查 | 在 `backend/agent`、`backend/cli`、`backend/gateway` 各执行 `go build ./...` 和 `go vet ./...` | 全部通过 |
| Agent 测试 | `go test -timeout 20m ./...` | 通过；`internal/server` 用时 760 秒 |
| CLI 测试 | `go test -timeout 20m ./...` | 通过；36 秒 |
| Gateway 测试 | `go test -timeout 20m ./...` | 通过 |
| Web 类型检查 | `tsc --noEmit --incremental false` | 通过 |
| Python 契约测试 | `python3 -m pytest test -q` | 486 项通过，另有 8 个子测试通过 |
| DoSql 测试 | `node --no-warnings --test` 三个 DoSql 测试文件 | 92 项通过 |
| Web 控制台测试 | `pnpm test:console`、`pnpm test:gateway-policy` | 无法运行：脚本和测试文件在工作区里已被删除 |
| 工作区新增的 Web 测试 | `node --test lib/machine-inspection.test.mjs lib/markdown-render.test.mjs` | 前者 3 项通过；后者因导入不存在的 `highlightCode` 失败 |
| 探针测试 | 附录 A 的三个临时测试 | 全部按预期复现 |

没有运行的：`pnpm build`、浏览器测试、任何针对线上或已部署环境的检查。

## 7. 未覆盖范围与残余风险

- `self_upgrade.go`、Edge 隧道（`edge_*`）、Harbor 同步脚本、DoWeb 脚本没有深入阅读。
- 凭据生命周期的内部逻辑没有重新审查，上一份整改记录已覆盖。
- 资产台账相关代码约 1 万行，只抽查了各接口的认证与授权，没有审查业务逻辑。
- doagent 是外部组件，它的权限引擎和工具白名单不在本次范围内（见 AG-05）。
- Web 组件只看了结构和请求边界，没有做交互和视觉层面的检查。
- 所有结论基于静态阅读、单元级探针和本地测试，没有在真实 Gateway 与 Agent 上做端到端验证。
  GW-01 的端到端利用没有实际演示，只复现了注册表层面的替换与锁出，以及现有测试已断言的无凭据注册与下发。

## 8. 建议的整改顺序

1. **提交当前 Web 改动之前**：处理 WEB-01，确认测试和 CI 入口一致。
2. **下一次发布之前**：GW-01 的修复建议第 1、2 条。它们改动小，且直接消除顶替和无认证取凭据。
3. **同一批次的小修**：WEB-02、WEB-03、SK-01、SK-02、CLI-01。每一项都是独立的小改动。
4. **排期处理**：GW-02、GW-03、GW-04、GW-07。
5. **结构性工作**：AG-01、AG-02。建议先做 AG-02，它更小，也能为拆包理清边界。
6. **顺手清理**：其余低级别条目。

GW-01、GW-03 涉及权限和凭据边界，按 `AGENTS.md` 属于 T2，需要完整的 Given/When/Then 矩阵和线上合成凭据生命周期验证。

## 附录 A 探针测试

三个测试都临时放在 `backend/agent/internal/server/zz_review_probe_test.go`，
用 `go test -count=1 -run TestZZReview -v ./internal/server/` 运行，运行后已删除。
这里保留源码，方便转成正式的回归测试。

### A.1 无 token 会话顶替在线 Agent（GW-01）

```go
func TestZZReviewUnenrolledTakeoverLocksOutLegitAgent(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	legit := newGatewayAgentSession("prod", "node-1", "", "10.0.0.5:1", nil, nil)
	legit.RuntimeID = "legit-host:101:1"
	if err := registry.Register(legit); err != nil {
		t.Fatalf("legit register: %v", err)
	}
	intruder := newGatewayAgentSession("prod", "node-1", "", "203.0.113.9:1", nil, nil)
	intruder.RuntimeID = "intruder:1:1"
	if err := registry.Register(intruder); err != nil {
		t.Fatalf("tokenless second session was refused: %v", err)
	}
	if registry.Get("prod", "node-1") != intruder {
		t.Fatal("tokenless second session did not replace the live agent")
	}
	reconnect := newGatewayAgentSession("prod", "node-1", "", "10.0.0.5:2", nil, nil)
	reconnect.RuntimeID = "legit-host:101:1"
	if err := registry.Register(reconnect); err == nil {
		t.Fatal("original agent was allowed back")
	}
	if registry.Get("prod", "node-1") != intruder {
		t.Fatal("registry no longer points at the replacing session")
	}
}
```

修复后这个测试的预期应当反过来：第二次 `Register` 被拒绝，注册表仍指向 `legit`。

### A.2 升级排队期间新读者被准入（GW-02）

```go
func TestZZReviewUpgradeStarvedByReaders(t *testing.T) {
	agent := newGatewayAgentSession("prod", "node-1", "", "10.0.0.5:1", nil, nil)
	if err := agent.acquireReadOnly(); err != nil {
		t.Fatalf("reader 1: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- agent.acquireExclusive(context.Background(), "target:prod/node-1", 10, 600*time.Millisecond)
	}()
	time.Sleep(150 * time.Millisecond)
	err2 := agent.acquireReadOnly() // 升级已在排队，新读者仍被准入
	agent.releaseReadOnly()         // 读者 1 离开，读者 2 仍持有
	err := <-done                   // 升级以 "target queue timeout" 结束
	if err2 != nil || err == nil {
		t.Fatalf("unexpected: reader2=%v upgrade=%v", err2, err)
	}
}
```

### A.3 配置中心文件发布的两种路径（WEB-03）

```go
func TestZZReviewConfigCenterFilePublishPaths(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("DOOPS_KB_ROOT", t.TempDir()+"/kb")
	abs, _ := json.Marshal(map[string]string{
		"session_id": "cfg-1", "path": "/etc/nginx/conf.d/doops.conf", "content": "server {}",
	})
	if _, err := handleFileWrite(abs); err == nil { // 实际返回 unsafe workspace path
		t.Fatal("absolute system path was accepted")
	}
	rel, _ := json.Marshal(map[string]string{
		"session_id": "cfg-1", "path": "nginx/doops.conf", "content": "server {}",
	})
	out, err := handleFileWrite(rel) // 成功，但写在 <工作区根>/cfg-1/nginx/doops.conf
	t.Logf("relative path -> out=%q err=%v", out, err)
}
```

## 附录 B 评审对工作区的影响

- 没有修改任何受版本控制的文件。新增的只有本文件。
- 临时探针测试文件创建过两次，运行后都已删除。
- 在 `backend/cli` 和 `backend/gateway` 执行 `go build ./...` 时，生成或覆盖了两个被 `.gitignore`
  忽略的二进制：`backend/cli/doops` 和 `backend/gateway/gateway`。无法确认它们此前是否存在，所以没有删除。
- 运行 Python 契约测试更新了 `backend/test/__pycache__/` 下被忽略的缓存文件。
