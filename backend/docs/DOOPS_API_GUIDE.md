# Doops API 对接指南

本文档面向需要通过 API 调用 Doops 的平台、CI 系统和内部工具。推荐的公共入口是 Doops Gateway 的 HTTP / WebSocket 服务；目标集群内的 doops-agent 主动连接 Gateway，不要求调用方能直连内网节点。

## 受管凭据配置

运行时引用凭据只要求该 target 的 grant，不要求额外凭据权限或逐用户绑定。
目标视图也继承操作权限；返回无 payload 的目标绑定元数据。创建、轮换、吊销仍检查 owner/admin。
`skill=dosql` 请求直接用服务器保存的 database 绑定解析 namespace/version，不运行 scope 规划 prompt。
DoSql 使用自动凭据模式：Gateway 按资源 ID 把当前 target 上全部 active 的 database credential bindings
传给 DoSql。`skill=dosql` 携带 `credential_refs` 时返回 `-32602 dosql_credential_refs_unsupported`。
多个绑定时由 Agent 按 `sql_database` 与凭据指向的库名匹配选择。`sql_credential`（资源名或 ID）
把这个集合收窄为一个已绑定凭据；只接受 `skill=dosql` + `credential_mode=auto`，否则返回 `-32602`。
未绑定返回 `dosql_credential_not_bound`，同名多 owner 返回 `credential_reference_ambiguous`。
缺少版本、目标绑定、格式不合法或目标离线分别返回真实错误类别，不合并为 `credential_invalid`。

CLI `credential configure` 顺序调用既有的 managed list/create、payload/import-secret、
grants、verify 和 promote；不增加另一套凭据资源或提升权限的 API。
新增 `POST /v1/credentials/<id>/import-secret` 接受 metadata-only JSON：

```json
{
  "session_id": "credential-setup",
  "cluster": "exact-cluster",
  "instance": "exact-instance",
  "namespace": "apps",
  "secret_name": "database",
  "keys": ["DB_PASSWORD"],
  "key_map": {"DB_PASSWORD": "SPRING_DATASOURCE_PASSWORD"},
  "connection_uri": "postgresql://app@database:5432/app",
  "password_key": "DB_PASSWORD"
}
```

`key_map` 可将 source Secret key 映射为运行时 key；每个 source 必须出现在 `keys` 中，
runtime key 不能重复。未映射的 key 原样保留。`connection_uri` 和 `password_key` 一起使用或一起省略。URI 不得含密码、query 或 fragment，
必须是含用户名、host、数据库路径的 postgres/postgresql/mongodb URI；组合时只允许选中密码 key。
该 endpoint 要求对应 target 的 grant 和可管理的 active opaque resource，沿用凭据资源锁、
加密主密钥和审计。它只在当前精确 target 内读取指定 Opaque Secret，不修改源 Secret。
Agent 通过内部 `doops_credential_import` 控制通道返回选中 payload，Gateway 加密为 staged
version 后只向 HTTP 返回版本元数据。公开 RPC 禁止调用该工具，流式通知不会进入导入结果。
错误只返回稳定类别；无权限、无加密主密钥或审计不可用时不读取 Secret。内部调用失败的类别与状态见
`CREDENTIAL_RESOURCES.md` 的 `--from-secret` 边界。
导入本身不建立 grant 或激活；configure 继续走既有 grant/verify/promote。

## 架构

```text
调用方 / CI / 平台
  -> doops-gateway
     -> 目标 cluster/instance 内的 doops-agent
        -> shell / kubectl / buildkit / doagent AI
```

Gateway 负责鉴权、授权、目标路由、单目标 busy 隔离、审计记录、WebSocket JSON-RPC 转发和 Git HTTP 反向隧道。

## 基础地址和鉴权

生产环境建议使用 HTTPS / WSS：

```text
https://gateway.example.com
wss://gateway.example.com
```

生产 Gateway 使用 HTTPS / WSS。CLI 默认仅允许 loopback HTTP / WS；受控内网测试须显式
设置 `DOOPS_ALLOW_INSECURE_GATEWAY=1`，该选项不应作为生产 TLS 故障的绕过方式。

所有用户侧请求都必须使用 Gateway user token 鉴权：

```http
Authorization: Bearer <DOOPS_GATEWAY_USER_TOKEN>
```

Gateway 鉴权失败仍返回 HTTP `401`，但响应体包含稳定错误分类，避免与目标业务服务
或受管凭据的认证失败混淆：

```json
{
  "error": "unauthorized",
  "code": "gateway_auth_failed",
  "auth_domain": "gateway",
  "message": "Gateway user token is missing, invalid, expired, or revoked",
  "next_action": "Run doops login for this Gateway target; do not replace the Gateway token with a managed credential or business API key"
}
```

`gateway_auth_failed` 只表示 DoOps Gateway 登录态问题，不表示受管凭据
`credential_invalid`。如果响应只有裸 `401 Unauthorized`，调用方必须将认证域视为
未确定，不得直接把错误归因于受管凭据。

`/v1/edge/connect` 使用独立的 enrollment token。失败时返回
`code=edge_enrollment_auth_failed` 和 `auth_domain=edge_enrollment`，不能使用 Gateway
user token 或受管凭据替代。

`X-Doops-Key` 不属于当前协议，Gateway 不接受该字段。

## 健康检查

```http
GET /health
```

响应：

```text
ok
```

## 用户登录换取 Token

密码登录是可选能力，返回短期 user token。长期自动化任务应使用 Gateway 管理员签发的服务 token。

```http
POST /v1/auth/login
Content-Type: application/json

{
  "username": "alice",
  "password": "secret",
  "name": "ci deploy token"
}
```

响应：

```json
{
  "token": "dgw_user_xxx",
  "token_id": "tok_xxx",
  "token_type": "user",
  "username": "alice"
}
```

常见错误：

- `401 Unauthorized`：用户名或密码错误。
- `400 Bad Request`：请求体不是合法 JSON。

## 管理员创建和维护用户

这些接口要求调用方持有 `admin` 权限。用户密码只允许出现在 HTTPS JSON request body，
不得写入 URL、argv、日志或文档。

列出或创建用户：

```http
GET /v1/admin/users
Authorization: Bearer <ADMIN_USER_TOKEN>
```

```http
POST /v1/admin/users
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{"name":"alice","password":"<PASSWORD_FROM_SECURE_INPUT>","admin":false}
```

当前 `POST /v1/admin/users` 会在创建成功后自动添加一条 `*/*` grant。普通用户创建流程
必须在交付账号前删除该 bootstrap grant，并创建精确 `cluster/instance/actions` grant。
如果收窄失败，立即停用用户。

设置密码和启用/停用用户：

```http
POST /v1/admin/users/password
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{"user_id":"usr_xxx","password":"<PASSWORD_FROM_SECURE_INPUT>"}
```

```http
POST /v1/admin/users/disable
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{"user_id":"usr_xxx","disabled":true}
```

## 管理员维护 Target Grants

列出用户 grant 时，`user` 查询参数接收 `user_id`：

```http
GET /v1/admin/grants?user=usr_xxx
Authorization: Bearer <ADMIN_USER_TOKEN>
```

创建精确 grant：

```http
POST /v1/admin/grants
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{
  "user_id": "usr_xxx",
  "cluster": "doops-oilan",
  "instance": "oilan-node"
}
```

删除 grant：

```http
DELETE /v1/admin/grants?id=<grant-id>
Authorization: Bearer <ADMIN_USER_TOKEN>
```

grant 覆盖整个 target，没有 action 维度；`actions` 可省略，只接受 `["target:use"]`，其它值返回 `400`。
平台管理员是用户的 `is_admin` 标记，创建用户时用 `"admin": true` 设置。

## 管理员签发用户 Token

管理员可通过 gateway API 给指定用户签发新的 user token。调用方必须使用拥有
`admin` 权限的 gateway user token。返回的明文 token 只出现一次；gateway
数据库只保存哈希。

```http
POST /v1/admin/tokens
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{
  "user": "alice",
  "name": "ci deploy token",
  "expires": "720h"
}
```

响应：

```json
{
  "token": "dgw_user_xxx",
  "token_id": "tok_xxx",
  "token_type": "user",
  "username": "alice"
}
```

CLI 推荐入口：

```bash
doops admin token create \
  --target jm \
  --user alice \
  --name ci-deploy \
  --expires 720h
```

常见错误：

- `401 Unauthorized`：管理员 token 缺失、无效、过期或已撤销。
- `403 Forbidden`：token 有效，但没有 `admin` 权限。
- `404 Not Found`：目标用户不存在。
- `400 Bad Request`：请求体非法，或 `expires` 不是合法正数 duration。

## 查询在线目标

```http
GET /v1/targets
Authorization: Bearer <USER_TOKEN>
```

响应：

```json
{
  "targets": [
    {
      "cluster": "doops-oilan",
      "instance": "oilan-node",
      "key": "doops-oilan/oilan-node",
      "release_commit": "0123456789abcdef0123456789abcdef01234567",
	      "remote": "10.0.0.12:47128",
	      "connected_at": "2026-05-14T12:00:00Z",
	      "last_seen": "2026-05-14T12:00:10Z",
	      "busy": false,
	      "status": "active",
	      "active_ops": 1,
	      "queued_ops": 0,
	      "resources": ["workspace:deploy-20260519"],
	      "sessions": ["deploy-20260519"]
	    }
	  ]
	}
```

常见错误：

- `401 Unauthorized`：token 缺失、无效、过期或已撤销。
- `403 Forbidden`：token 有效，但无权查询目标列表。

`busy` 只表示 Agent 自升级持有 target 级独占 lease；普通变更和排队 session 使用
`target + session` 作用域的共享 lease，同一 session 内的 Shell/SSH/bg/apply 也可并发，
只有该 session 的 workspace 写入要等它们结束。`status=active` 且 `busy=false` 表示有
workspace/resource 正在执行，但其他 session 仍可使用同一 Agent。`resources` 列出
`workspace:<session>` 等资源锁；session 是并发归属，不是操作系统沙箱。
取消和审计收尾复用同一冲突规则，不阻塞其它 session；普通 mutation 缺少有效 session
时直接拒绝，不回退到 target 独占。历史 `outcome_unknown` 只保留事实，不参与新任务准入。

## Git 工作区同步

`push/pull` 使用 Git HTTP，不使用 stdout、base64 或 tar 分块传输。标准公共 API 只通过 Gateway 访问：

```http
GET/POST /v1/git/<cluster>/<instance>/<session>.git/...
Authorization: Basic doops:<USER_TOKEN>
```

CLI 会自动把 user token 放到 Git Basic Auth 的密码字段。Gateway 验证 token 后，按 `<cluster>/<instance>` 找到在线 agent，并通过该 agent 已建立的 `/v1/agent/connect` WebSocket 连接反向透传 Git HTTP 请求。Agent 在本地调用自己的 `/git/<session>.git/...` handler，完成 Git push 或 fetch。

权限映射：

- `git-receive-pack`：需要 `push` 权限。
- `git-upload-pack`：需要 `pull` 权限。

典型 API 路径：

```text
GET  /v1/git/doops-oilan/oilan-node/deploy_20260514.git/info/refs?service=git-upload-pack
POST /v1/git/doops-oilan/oilan-node/deploy_20260514.git/git-upload-pack
GET  /v1/git/doops-oilan/oilan-node/deploy_20260514.git/info/refs?service=git-receive-pack
POST /v1/git/doops-oilan/oilan-node/deploy_20260514.git/git-receive-pack
```

外部系统通常不需要手写 Git HTTP 报文，直接调用 CLI 更安全：

```bash
doops -session deploy_20260514 push --target oilan --src .
doops -session deploy_20260514 pull --target oilan --dest ./deploy-output
```

## 执行操作

所有操作通过 WebSocket JSON-RPC 调用：

```text
WS /v1/rpc?cluster=<cluster>&instance=<instance>
Authorization: Bearer <USER_TOKEN>
```

连接示例：

```bash
websocat \
  -H "Authorization: Bearer ${DOOPS_GATEWAY_USER_TOKEN}" \
  "wss://gateway.example.com/v1/rpc?cluster=doops-oilan&instance=oilan-node"
```

初始化：

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
```

Gateway 和 Agent 的 `serverInfo.version` 使用同一个 DoOps 发布标签，
`serverInfo.releaseCommit` 使用该发布对应的完整 40 位源码 commit。两者是同一个发布
身份的版本与 provenance，不能把 commit 当作版本号；旧 Agent 缺少完整
`releaseCommit` 时不会被当作当前发布验收通过。`cli-contract/v3` 只表示 CLI 协议
兼容性，由 `doops --contract` 单独检查。

`executionLifecycle` 只限制 `doops_bg`，不限制普通前台 shell、SSH、prompt/apply 或查询。
Agent 在 `capabilities.protocols.executionLifecycle` 声明
`schemaVersion=doops.sh/execution-lifecycle/v1`，且 `terminalResponse`、
`backgroundLifetime`、`promptCancellation` 必须全部为布尔 `true`，不得缺字段或额外字段。
CLI 和 Gateway（包括内部调度调用）在后台任务派发前校验，不满足则拒绝且不占执行锁。
`backgroundLifetime` 表示任务受控并等待原始 RPC 终态，不承诺跨断连存活。
终态、取消和未知结果仍由 Gateway 统一处理。workspace 写入等本地操作缺失终态时同样
如实保留未知结果；执行结束及连接清理后释放 lease。审计持久化失败报告
`audit_completion_failed` 并独立重试原始终态，不保留执行锁；历史未知审计不参与新任务准入。

工具调用格式：

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "doops_shell",
    "arguments": {
      "session_id": "deploy_20260514",
      "command": "hostname && kubectl get nodes"
    }
  }
}
```

流式输出通过通知返回：

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/message",
  "params": {
    "sessionID": "deploy_20260514",
    "data": "一行 stdout 或 stderr\n"
  }
}
```

最终响应会使用与请求相同的 `id`。

## 工具列表

### `doops_shell`

在目标 agent 上执行确定性的 Shell 命令。

参数：

```json
{
  "session_id": "deploy_20260514",
  "command": "cd /root/ws/deploy_20260514 && ./deploy.sh"
}
```

需要该 target 的 grant（审计动作记为 `exec`）。

适用于 CI/CD、固定部署脚本、健康检查、BuildKit 构建和 Kubernetes 命令。

### `doops_ssh`

通过 Agent 的出站 SSH 客户端执行结构化远端 argv。`host`、SSH `options` 和远端
`command` 在 JSON 中分别传输；Agent 只在 OpenSSH 的远端命令边界把 `command` 的
每个元素做 POSIX 单引号编码，避免多层 SSH 的嵌套引号被本地或远端 shell 重解析。

Agent 在 `initialize` 响应的 `capabilities.protocols.structuredSSH` 中声明
`doops.sh/structured-ssh/v1`。Gateway 只把该能力转发给已声明的 target，并在能力
缺失时拒绝调用；CLI 也会在发送 `tools/call` 前失败关闭。旧 Agent 不会再以
`Unknown tool over WS: doops_ssh` 作为能力不匹配的唯一提示，必须先按受管 Agent
升级流程替换。

能力声明格式：

```json
{
  "schemaVersion": "doops.sh/structured-ssh/v1",
  "tool": "doops_ssh",
  "structuredArgv": true
}
```

参数：

```json
{
  "session_id": "ops_20260927",
  "host": "ops@app.internal",
  "options": ["-o", "BatchMode=yes"],
  "command": ["bash", "-lc", "hostname && systemctl status api"]
}
```

需要该 target 的 grant（审计动作记为 `exec`）。

CLI 对应入口：

```bash
doops -session ops ssh --target jm --host ops@app.internal \
  --option=BatchMode=yes -- bash -lc 'hostname && systemctl status api'
```

CLI 的 `--option` 只接受不会改写连接目标的旗标：`-4`、`-6`、`-p`、`-i`，以及
`BatchMode`、`StrictHostKeyChecking`、`ConnectTimeout`、`IdentitiesOnly`、
`IdentityFile` 和 `Port`。`key=value` 简写编码为 `-o<key>=<value>`。位置参数、
`ProxyCommand`、`ProxyJump`、端口转发、`HostName` 和 `KnownHostsCommand` 会被拒绝。

不要把嵌套 SSH 写进 `doops_shell.command`。原始 Shell 仍按单个
`bash -c` 字符串执行，无法替结构化 SSH argv 推断用户想保留的引号层级。

### `doops_bg`

提交由 agent 管理的后台任务。进度通知先返回任务 ID、PID 和日志路径，原始 RPC
在进程及其进程组终止后才返回最终结果；CLI 使用一次流式 RPC，不轮询或重试任务。
`doops_task_status` 可独立查询已有任务日志；终态无法确认时返回
`execution_outcome_unknown`，状态查询显示 `unknown`，不得把提交成功当成执行完成。

参数：

```json
{
  "session_id": "deploy_20260514",
  "command": "cd /root/ws/deploy_20260514 && buildctl build ..."
}
```

需要该 target 的 grant（审计动作记为 `exec`）。

不要把 `doops_shell` 里的命令用 `&`、`nohup` 或 `setsid` 转为后台任务。普通 shell 调用结束时，agent 会终止其子进程；长构建必须使用 `doops_bg`。

### `doops_task_status`

查询 `doops_bg` 提交任务的状态和日志尾部。

参数：

```json
{
  "session_id": "deploy_20260514",
  "task_id": "task_abc123",
  "lines": "100"
}
```

需要该 target 的 grant（审计动作记为 `exec`）。

### `doops_agent_prompt`

The unified DoWeb domain uses `skill=doweb` and the same target/session and
Gateway account. `mcp_doops_web_read` accepts only `render`, `check`,
`config-get` and `domain-list`. The mutating `mcp_doops_web` requires
`confirm=true` and a runtime-authorized apply. A read tool cannot stand in for
mutation evidence. The deterministic `doops_web` RPC maps reads to `ask` and
mutations to `exec`; website writes hold a target-wide lease because Nginx
activation/reload is shared. See `DOWEB_INTEGRATION.md` for parameters and mounts.

向目标 AI agent 下发诊断、运维或发布任务。发布任务必须使用
`operation=apply`，并在自然语言中明确 target、服务范围、原因、任务指定的资产和验收条件。
业务镜像版本来自任务或任务指定的仓库、Chart、values、现有 release 的只读证据，
不得猜测，也不要求已明确资产中的事实再粘贴到 prompt。

参数：

```json
{
  "session_id": "diagnose_20260514",
  "instruction": "检查目标节点磁盘和容器运行时状态，只返回诊断结果，不修改资源。",
  "skill": "doweb",
  "model": "openai/gpt-5.4"
}
```

需要该 target 的 grant。审计动作：普通诊断或只读任务记为 `ask`，`operation=apply` 记为 `exec`。

适用于诊断、信息收集和用户明确要求的运维或发布任务。需要凭据时，CLI 只传递
`--credential <resource-name>` 的 metadata-only 引用；Gateway 在绑定用户、target 和
session 后按 `cluster/instance/use` 授权和物化，namespace、workload、image、Helm 和
required keys 只作为执行元数据。普通发布使用 `operation=apply`；DoSql 只读查询或
计划可以使用 `operation=ask` 搭配 `skill=dosql` 和精确 credential reference；CLI
用户应优先使用 `doops sql`，它会设置正确的 DoSql skill/tool boundary。通用
`ask` 不会自动注入 DoSql 工具，也不能附带数据库凭据。payload 永远不进入 prompt、日志或返回事件。

组件对话的结构化事件消费者先发送 `mode=metadata`。支持本协议的 Agent 返回
`event_schema=doops.sh/acp-events/v1` 和 `event_formats=["acp"]`。
消费者显式发送 `event_format=acp` 时，`notifications/message` 的
`kind=acp_update`，`data` 是已投影并脱敏的原生 ACP JSON；通知保留匹配的
`sessionID` 和 Gateway 根请求 `request_id`。任意其他非空 event format 被拒绝。
显示事件不携带凭据 payload、mutation proof 或执行授权。`turn_finished` 不替代
Gateway 最终 RPC 结果及既有 Helm 证据验签；组件只在两者均满足时显示完成。
验签后的 Helm 成功若伴随模型收尾失败，桥接以 `sessionUpdate=doops_notice`
发送结构化警告，并保留 Gateway 已验签的成功结果，不把旧格式警告混入 ACP 流。
未声明该协议的 Agent 必须升级，组件不自动转回旧事件消费者。未请求此格式的
CLI 保持原有输出契约，不能以该路径绕过组件的版本检查。

### `doops_file_read`

查看小文本文件。

参数：

```json
{"session_id": "deploy_20260514", "path": "deploy.sh"}
```

需要该 target 的 grant（审计动作记为 `read`）。

不要用它下载大文件、二进制、压缩包、数据库导出或图片。大文件和目录下载使用 `doops pull`。

### `doops_file_write`

写入小文本文件。

参数：

```json
{
  "session_id": "deploy_20260514",
  "path": "pod.yaml",
  "content": "apiVersion: v1\nkind: Pod\n..."
}
```

需要该 target 的 grant（审计动作记为 `write`）。

### `doops_workspace_read`

为 Agent UI 代码工作台提供当前 session 工作区的只读文件树、源码、变更、Diff、搜索和 Git 信息。
该工具只需要目标 `read` 权限，不执行 shell，也不修改工作区。
结构化文件与工作区 `read` 使用并发只读通道；同一 session 的多个读取可同时执行，
也可查看正在执行的 session。写入/执行仍互斥，Agent 升级期间的维护隔离仍生效。

```json
{"session_id":"console_abc123","operation":"list","path":"src"}
```

`operation` 支持 `list`、`read`、`changes`、`diff`、`search`、`git-summary` 和 `git-log`；
`path`、`query`、`ref` 按操作使用。成果面板中的工作区产物仍通过 session-scoped `read` 边界加载。
读取以当前 session 目录为文件系统根，拒绝越界符号链接。尚未创建的会话工作区显示为空；
没有 Git 仓库时仍返回实际文件供成果查看，并明确标记 `gitStatus=no_repository`。
`read` 返回 `content`、`encoding`（`utf-8` 或 `base64`）、`contentType`、`bytes` 和 `binary`；
预览沿用 `DOOPS_MAX_FILE_READ_BYTES` 上限（默认 1 MiB），大资产仍使用 `doops pull`。
搜索直接遍历当前工作区，不依赖宿主机 grep；跳过 `.git`、`node_modules`、符号链接和二进制，
遇到超过预览上限的文件返回 `truncated=true`，实际读取错误明确失败。

### Workspace 同步协议

通用文件同步 CLI 使用 Git HTTP：

```text
doops push -> /v1/git/<cluster>/<instance>/<session>.git -> agent /git/<session>.git
doops pull <- /v1/git/<cluster>/<instance>/<session>.git <- agent /git/<session>.git
```

`doops_workspace_begin`、`doops_workspace_chunk`、`doops_workspace_commit`、`doops_workspace_pull_begin`、`doops_workspace_pull_chunk` 是低层 JSON-RPC 文件传输工具，不是声明式发布协议的一部分。

普通文件同步应直接执行：

```bash
doops -session <session> push --target <target> --src <dir>
doops -session <session> pull --target <target> --dest <dir>
```

### `doops_node_info`

返回节点基础信息和通用镜像工具链状态，包括 `buildctl`、`skopeo` 和 BuildKit
socket。镜像构建使用 CLI 的通用 `image build`，高级场景仍可通过 `doops exec` 调用
明确给出的 `buildctl` 命令；OCI 镜像复制和 manifest 校验使用 `image copy` 或
`registry sync`，不依赖业务项目脚本。

参数：

```json
{"session_id": "smoke"}
```

需要该 target 的 grant（审计动作记为 `info`）。

### `doops_check_deployment`

检查线上部署镜像是否与最新构建一致。

需要该 target 的 grant（审计动作记为 `check`）。

### `doops_clean_workspace`

删除 `/root/ws/<session>` 和相关临时 Git 仓库。

参数：

```json
{"workspace": "deploy_20260514"}
```

需要该 target 的 grant（审计动作记为 `clean`）。

### `doops_agent_upgrade`

由目标 Agent 创建集群内 Job，升级承载自己的 Helm Deployment。Agent 进程不执行 Helm mutation。

参数：

```json
{
  "image": "docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest>",
  "mode": "k8s",
  "release": "doops-agent-live",
  "dry_run": false
}
```

需要该 target 的 grant；升级期间 Agent 独占 target。

`image` 接受 `repository:tag` 或 `repository@sha256:<64 hex>`，包括 `dry_run: true` 的预演请求。
tag 使用 `Always` 拉取并校验实际 `imageID`；回滚仍只接受历史已验证的 digest 镜像。
`dry_run: true` 只返回预演。
`observe_job` 读取 Job 状态。`rollback: true` 创建回滚 Job。namespace 必须是 Agent
自己的 namespace。Job 使用 Agent 已挂载的 kubeconfig，不接收操作机 kubeconfig。
新 Pod 未持续就绪时 Job 回滚；CLI 在成功后检查 `structuredSSH`，探针失败会再创建回滚 Job。

Agent 必须已经包含该控制器，并挂载可共享的 `/root/.kube` 或 `/root/.kube/config`。
`self-upgrade-accepted` 仅表示 Job 已提交；调用方继续以精确 `job + namespace`
观察 `self-upgrade-running/failed/succeeded` 首行状态，不能从日志中的成功字样推断结果。
CLI 的 `-session` 必传；预演不作为升级完成证据，回滚失败也必须单独报告。

Controller Job 固定在当前 Agent 节点并保留 runtime class，仍为非特权且只共享
kubeconfig。Deployment/Pod 的 spec 镜像必须精确匹配请求的不可变引用；实际 imageID
与同一 pin 的 controller 运行身份比较，不假设 CRI 返回 registry manifest digest。
自动回滚核对升级前捕获的运行身份。完整验证后持久化 revision/image/Pod UID/runtime
证据到自身 namespace 的 `doops-upgrade-verified-<release>` ConfigMap；写入失败不报告成功。
手动回滚只选有该证据且仍 superseded 的具体 revision，不推断所有 superseded 都成功。
Job 使用选定 revision 镜像并复核 revision、namespace 和镜像；无证据的旧 release
必须经外部控制器回滚，不能在新控制器里跳过验证。

## 自然语言发布 API 对接模式

平台通过 `doops ask --apply` 发送明确的发布任务。任务必须明确 target、
服务范围、变更原因、任务指定的资产和验收条件；业务镜像版本必须来自任务或任务指定的仓库、Chart、values、现有 release 的只读证据，不猜测，也不要求重复粘贴已明确资产中的事实。
需要凭据时，调用方显式提供一个或多个 metadata-only credential resource
name；Gateway 根据用户对该 target 的 grant 直接读取 target/use 绑定并物化，
不调用模型生成授权 scope。凭据 payload 不进入公开 prompt、doagent 上下文、CLI 输出、日志或
审计记录；Gateway 仅通过内部凭据物化调用向目标 Agent 短暂传递 payload。

内部 Helm binding 元数据增加 `credential_id`、`grant_id`、`version_id`、`helm_release`
与 `helm_chart`，由 Gateway 从运行位置元数据填写；公开调用方不得提交 binding。
Helm target binding 只绑定 target 的 cluster/instance 和 use=helmPull；namespace、workload、release
和 Chart URL 不再参与 Gateway 或 Agent 授权匹配。认证只发送给配置的 HTTPS origin，
仓库路径不作权限条件。Agent 仅在本次 prompt 存续期间建立无 payload 的绑定租约，
检查 session、凭据身份与 resourceVersion 后消费凭据。公开 MCP 不增加密码/token 参数。
受管 Helm 发布允许任务指定的 `value_files` 和非敏感 `set_strings`，不强制 bundled-only，
认证、target 权限、包校验与单次 staging 保留；失败不改走匿名或旧登录态。

`mcp_doops_helm upgrade-install` 显式提供 `release`、`namespace`、`chart` 和 `confirm=true`；
`session_id` 由运行时绑定。`asset_source` 默认 `remote`（HTTPS `.tgz`，当前不支持 OCI），
也可显式使用 `workspace`。`value_files` 是可选有序列表，`set_strings` 是非敏感
Chart-specific 字符串覆盖；`expected_image` 可选且只接受显式输入，不从 values 推导。
普通升级统一 `--reset-values`，取消隐式保留历史 values，自动执行 server dry-run 和最终
Deployment 校验。完整参数、安全边界与迁移说明见
[`NATURAL_LANGUAGE_DEPLOYMENT.md`](NATURAL_LANGUAGE_DEPLOYMENT.md)。

业务服务的生产发布由目标 Agent 执行。`doops-agent` 自身镜像替换不走 `ask --apply`，
走 `doops upgrade`。调用方必须以同一 session 的真实健康检查和关键业务验收作为完成证据。

## 进展和可追溯性

Doops 有三层进展数据：

- WebSocket 流：实时 `notifications/message` 输出。
- Gateway 审计库：按 user、token、target、action、session、status、命令摘要、输出尾部和时间记录持久事件。
- 目标工作区审计：`/root/ws/<session>/.doops-audit-log` 记录该 session 内执行过的 shell 命令。

已登录用户通过 Gateway API 查询审计：

```http
GET /v1/audit?cluster=doops-oilan&instance=oilan-node&session=deploy_20260514&limit=50
Authorization: Bearer <USER_TOKEN>
```

查询权限：

- 普通用户返回自己的全部操作，以及引用其拥有的凭据的事件。`user_id` 可省略、等于当前
  用户 ID 或 `self`；指定其他用户返回 `403`。
- 平台管理员可查询全部用户；`view=global` 要求平台管理员，否则 `403`。`view=global`
  不取消其他过滤条件，平台管理员也可用 `user_id=self` 只查本人。
- 过滤和权限检查在 `limit` 截断前完成；`limit` 为 1–500，省略或超范围时为 100。
  审计存储读取失败返回 `503 audit_unavailable`，不伪装成空列表。

响应：

```json
{
  "events": [
    {
      "id": 123,
      "user_id": "usr_xxx",
      "token_id": "tok_xxx",
      "cluster": "doops-oilan",
      "instance": "oilan-node",
      "action": "ask",
      "session": "deploy_20260514",
      "command_summary": "检查仓库，生成部署计划...",
      "status": "success",
      "tail": "rollout complete\n",
      "bytes_in": 268,
      "bytes_out": 4096,
      "started_at": "2026-05-14T12:00:00Z",
      "ended_at": "2026-05-14T12:03:21Z"
    }
  ]
}
```

过滤示例：

```http
GET /v1/audit?action=ask&status=error&limit=100
GET /v1/audit?user_id=usr_xxx
GET /v1/audit?session=deploy_20260514
GET /v1/audit?user_id=self
GET /v1/audit?view=global
```

### 执行生命周期

Gateway 对前台、后台、SSH、prompt 和 `ask --apply` 统一维护执行生命周期。只有
后台 `doops_bg` 在派发前要求 Agent 声明精确的 `executionLifecycle` 能力；前台调用不受
该能力门禁影响。每个 `tools/call` 都必须收到对应的终态响应；
`notifications/message` 必须在 `params.request_id` 携带本次请求 ID，Gateway 和 CLI
按请求 ID 路由流式事件，不按 session 覆盖并发请求。取消 ACK 不是原始调用的终态；
调用被取消、超时或断连且没有足够终态证据时写入 `outcome_unknown`，不能把本地等待
结束当成远端 mutation 已停止。结构化 SSH 仍由 `structuredSSH` 参数协议保护。

CLI/Web 可在原 `/v1/rpc` WebSocket 上取消自己的请求，不需要管理员权限：

```json
{"jsonrpc":"2.0","id":"cancel-2","method":"tools/cancel","params":{"id":2}}
```

`params.id` 必须是该连接尚未结束的原始请求 ID，不能取消另一连接的任务。
返回 `result.canceled=true` 仅表示已请求取消；原请求仍通过其原 ID 返回实际终态。
连接内重复的活动请求 ID 会被拒绝，不允许覆盖已有执行的所有权。

取消只确认请求，资源所有权保留到原始请求终态。
终态不确定时审计持久化为 `outcome_unknown`，仍报告失败，不自动重放 mutation。
`running` 包含准入等待及无需变更结果保护的只读执行，不能据此认定请求尚未派发。
Gateway 重启时遗留的 `running` 落为 `error`；已派发且需要变更结果保护的 `executing`
在失去终态证据时落为 `outcome_unknown`。终态落盘失败重试原始结果，不用推测结果替换。
历史未知审计不参与新任务准入，不改为 success，也不删除；活动任务仍受资源锁、排队和
取消机制约束，执行结束及清理完成后释放 lease。`finalizing` 仅表示审计待落盘，
不参与锁冲突；持久化失败不能把已结束任务变成永久阻塞。
已验签的 Helm 工具明确失败按失败终态处理，不封锁后续操作；同轮任何明确失败
不得被另一条成功调用掩盖。工具请求错误与执行失败保持区分，执行失败返回 MCP
`result.isError=true`，取消或缺失终态不转换成普通失败。

管理员 mutation 审计：

- 管理员 GET 接口不要求 session，也不会产生 mutation 审计；远端 RPC/CLI 仍使用显式 session。
- `/v1/admin/` 下的用户创建、密码、启停、grant 增删、token 签发/撤销、实例归档/恢复、代理路由增改删均记录调用者 `user_id + token_id`、动作和资源元数据、
  开始/结束时间与成功、拒绝或失败结果。token 签发只有一条审计，不重复记录。
- 此类事件的 `action=admin`；`command_summary` 是白名单 JSON 元数据，包含注册路由、
  HTTP method/status，以及可用的资源 ID、subject user ID、授权动作或启停值。
  请求体、密码、token 明文、配置、响应和原始错误均不进入
  summary/tail；失败只记录稳定分类（如 `http_403`、`http_502`），tail 为空。
- 全局用户创建/密码/启停、grant 增删、token 签发/撤销必须通过 `UserIsPlatformAdmin`；仅有某个 target 的 `admin`
  不得修改这些全局资源或给自己追加平台权限。此限制在 mutation 审计入口执行，
  不改变现有管理员 GET 的权限或 session 约定。
- 管理员身份不替代目标授权：代理路由检查对应目标。
  普通用户查询本人审计不会获得任何 mutation 权限。
- 配置下发使用既有 `doops_file_write` RPC 和目标 `write` 权限，不新增 admin 配置接口。
  继续使用唯一的 RPC 审计，保留 target/session/文件路径元数据，不保存文件内容、
  下游输出或原始错误；下游 tool error 记录为失败。其他 RPC/凭据沿用其既有审计。
- 管理 mutation 与配置写入在执行前检查审计 INSERT 和结果 UPDATE 可写性，失败返回
  `503 audit_unavailable`（RPC 为 `-32603`），不执行 mutation。执行后才发生的审计故障
  返回 `audit_completion_failed; mutation outcome requires verification`，不返回成功响应；
  持久的 pending 记录保留。该错误不表示远端动作已经回滚，调用方必须先核验真实状态，
  不能盲目重试。

### 实例归档

平台管理员可将已离线的实例记录从控制台的常规历史视图中归档，也可恢复归档。归档仅标记
`agent_status` 记录，绝不删除或更改该实例的用户授权、令牌、凭据绑定或审计历史。Agent 下次
成功连接时，Gateway 自动清除归档标记。

```http
POST /v1/admin/instances/archive
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{"cluster":"doops-oilan","instance":"oilan-node"}
```

恢复使用相同请求体：

```http
POST /v1/admin/instances/unarchive
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{"cluster":"doops-oilan","instance":"oilan-node"}
```

成功响应为实例元数据，例如：

```json
{
  "cluster":"doops-oilan",
  "instance":"oilan-node",
  "status":"offline",
  "busy":false,
  "active_ops":0,
  "queued_ops":0,
  "archived":true,
  "archived_at":"2026-11-01T06:00:00Z",
  "archived_by":"usr_xxx"
}
```

`GET /v1/admin/instances` 返回全部已知记录，以便管理员能发现并恢复归档实例；每条记录新增
`archived`、可选的 `archived_at` 和 `archived_by`。归档在线实例返回 `409`；未知实例返回
`404`。重复归档和对已恢复记录再次恢复都是幂等的成功操作。

审计清理也是管理员 API：

```http
DELETE /v1/audit?before=2026-01-01T00:00:00Z
Authorization: Bearer <ADMIN_USER_TOKEN>
```

响应：

```json
{
  "deleted": 42
}
```

## 错误码

HTTP 错误：

- `400`：查询参数或请求体缺失、非法。
- `401`：token 缺失、无效、过期或已撤销。
- `403`：token 有效，但缺少目标或动作权限。
- `404`：路由或请求的资源不存在。
- `405`：HTTP 方法不允许。
- `409`：请求与当前资源状态冲突，例如尝试归档在线实例。

JSON-RPC 错误：

- `-32601`：未知方法或未知 Doops 工具。
- `-32602`：请求参数非法。
- `-32603`：Gateway 或目标 agent 内部执行失败。
- `-32003`：目标或动作权限不足。
- `-32004`：目标离线。
- `-32005`：目标忙、目标队列满或等待超时。
- `-32006`：全局或单用户并发限制超限。
- `-32007`：操作取消；不能单凭客户端取消 ACK 推断执行停止。
- `-32008`：`execution_outcome_unknown`，本次调用缺少可信终态或外部 mutation 被中断，
  不代表成功，不自动重放；历史未知审计不阻断新任务，活动任务仍受实际资源锁约束。
  message 为 `execution_outcome_unknown: <原因>` 时，原因是 Agent 无法证明的那一项
  （如 `process group did not terminate`、`command output drain timed out`），按原因核对；
  prompt 审计只保留稳定类别，自由文本原因在审计里收敛为裸错误码。

## 权限

用户有匹配 `cluster/instance` 的 grant 才能操作 target，grant 覆盖该 target 的全部操作。
管理接口要求平台管理员（用户 `is_admin`）。新建用户没有任何 grant。返回 `403` 时检查该用户的 grant。

## 操作规则

- 所有远程操作必须设置有意义的 `session_id`。
- 文件传输使用 `push/pull`，`read/write` 只用于小文本。
- 可重复部署使用 `doops_shell`；首次适配、排障和探索使用 `doops_agent_prompt`。
- Kubernetes 变更必须先做 server-side dry-run，再真实 apply。
- 升级 agent 镜像前，先确认新镜像已能在目标环境拉取。
- 生成的脚本和报告放在 `/root/ws/<session>`，成为产品流程后要拉回源码仓库。
