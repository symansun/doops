# Doops API 对接指南

## Audit Index Maintenance

`POST /v1/admin/audit/optimize` accepts only `{"session_id":"<operation-session>"}`.
It requires platform administration and writable audit storage, then directly
executes the repository's fixed `audit_indexes.sql`. It accepts no arbitrary SQL,
does not delete history, and records the actor/token/session and HTTP result.
The indexes cover user history, user/session history and target history. Startup,
application entrypoints and CI never invoke this production maintenance operation.
The console exposes it in the administrator's global audit view; CLI entry:
`doops -session <s> admin audit optimize --target <configured-gateway-target>`.
Execution has a four-minute server deadline and one DDL transaction; cancellation
or failure rolls back newly created indexes. Success includes verified column/order
definitions and three actual EXPLAIN query plans. A preexisting incompatible index
is a failure, not an implicit replacement. The maintenance button remains reachable
after a query error; Gateway permission checks remain authoritative.

本文档面向需要通过 API 调用 Doops 的平台、CI 系统和内部工具。推荐的公共入口是 Doops Gateway 的 HTTP / WebSocket 服务；目标集群内的 doops-agent 主动连接 Gateway，不要求调用方能直连内网节点。

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

内网和受控测试环境也支持 HTTP / WS。客户端不能因为 Gateway 是 HTTP 就拒绝连接；生产环境仍建议使用 TLS。

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
  "instance": "oilan-node",
  "actions": ["targets:list", "exec", "ask", "read", "write", "push", "pull", "info", "check", "clean"]
}
```

删除 grant：

```http
DELETE /v1/admin/grants?id=<grant-id>
Authorization: Bearer <ADMIN_USER_TOKEN>
```

`actions` 为空时会展开为 Gateway 默认 action 集；最小授权流程必须显式列出 actions。
普通机器操作不得隐式包含 `credential:*`、`agent:upgrade`、`admin` 或 `*`。

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

`busy` 表示 target 独占/变更锁或队列阻塞；target mutation 期间仍允许结构化诊断查询，
Agent 自升级则完全独占。`status=active` 且 `busy=false` 表示有 workspace/resource
正在执行，但独立结构化 workspace 仍可用。`resources` 列出 `workspace:<session>`。
普通 Shell/SSH/bg 不按关键词或 session 推断副作用范围，一律使用 target mutation 锁。

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

需要权限：`exec`。

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

需要权限：`exec`。

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

需要权限：`exec`。

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

需要权限：`exec`。

### `doops_agent_prompt`

向目标 AI agent 下发诊断、运维或明确授权的发布任务。发布任务必须使用
`operation=apply`，并在自然语言中明确 target、服务范围、镜像版本、原因和验收条件。

参数：

```json
{
  "session_id": "diagnose_20260514",
  "instruction": "检查目标节点磁盘和容器运行时状态，只返回诊断结果，不修改资源。",
  "model": "openai/gpt-5.4"
}
```

需要权限：`ask`。

适用于诊断、信息收集和用户明确授权的运维或发布任务。需要凭据时，CLI 只传递
`--credential <resource-name>` 的 metadata-only 引用；Gateway 在绑定用户、target 和
session 后按精确 scope 授权和物化。

### `doops_file_read`

查看小文本文件。

参数：

```json
{"path": "/root/ws/deploy_20260514/deploy.sh"}
```

需要权限：`read`。

不要用它下载大文件、二进制、压缩包、数据库导出或图片。大文件和目录下载使用 `doops pull`。

### `doops_file_write`

写入小文本文件。

参数：

```json
{
  "path": "/root/ws/deploy_20260514/pod.yaml",
  "content": "apiVersion: v1\nkind: Pod\n..."
}
```

需要权限：`write`。

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

返回节点基础信息。

参数：

```json
{"session_id": "smoke"}
```

需要权限：`info`。

### `doops_check_deployment`

检查线上部署镜像是否与最新构建一致。

需要权限：`check`。

### `doops_clean_workspace`

删除 `/root/ws/<session>` 和相关临时 Git 仓库。

参数：

```json
{"workspace": "deploy_20260514"}
```

需要权限：`clean`。

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

需要权限：`agent:upgrade`。

`image` 必须是 `repository@sha256:<64 hex>`，包括 `dry_run: true` 的预演请求。
`dry_run: true` 只返回预演。
`observe_job` 读取 Job 状态。`rollback: true` 创建回滚 Job。namespace 必须是 Agent
自己的 namespace。Job 使用 Agent 已挂载的 kubeconfig，不接收操作机 kubeconfig。
新 Pod 未持续就绪时 Job 回滚；CLI 在成功后检查 `structuredSSH`，探针失败会再创建回滚 Job。

Agent 必须已经包含该控制器，并挂载可共享的 `/root/.kube` 或 `/root/.kube/config`。
`self-upgrade-accepted` 仅表示 Job 已提交；调用方继续以精确 `job + namespace`
观察 `self-upgrade-running/failed/succeeded` 首行状态，不能从日志中的成功字样推断结果。
CLI 的 `-session` 必传；预演不作为升级完成证据，回滚失败也必须单独报告。

## 自然语言发布 API 对接模式

平台通过 `doops ask --apply` 发送明确授权的发布任务。任务必须直接给出 target、
服务范围、镜像版本、变更原因和验收条件，Gateway 不得从 prompt、路径或历史状态猜测
这些字段。需要凭据时，调用方显式提供一个或多个 metadata-only credential resource
name；Gateway 将引用绑定当前用户、target 和 session，Agent 返回精确 workload/image/Helm
scope 后才授权和物化。凭据 payload 不进入公开 prompt、doagent 上下文、CLI 输出、日志或
审计记录；Gateway 仅通过内部凭据物化调用向目标 Agent 短暂传递 payload。

内部 Helm binding 元数据增加 `credential_id`、`grant_id`、`version_id`、`helm_release`
与 `helm_chart`，由 Gateway 从精确授权结果填写；公开调用方不得提交 binding。
Agent 仅在本次 prompt 存续期间建立无 payload 的绑定租约，内部 Helm 检查 exact scope
与 Secret resourceVersion 后消费凭据。公开 MCP 不增加密码/token 参数。
完整 Chart 值模式令 `value_files`、`set_strings` 都为空；HTTPS Chart 的 bundled values
必须包含 `image.repository` 和合法 release tag，不合并旧值，dry-run 和最终镜像验证仍必需。

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

- 普通用户默认只返回自己的全部操作（包括 `credential:*`）；本人事件按 actor `user_id`
  判断，不要求额外 `credential:audit`。`user_id` 可省略、等于当前用户 ID，或使用
  `user_id=self` 显式解析为当前登录用户；普通用户指定其他用户返回 `403`。
- 平台管理员（`*/*` 的 `admin` 或 `*` grant）可查询全部用户和动作；
  `view=global` 显式要求平台管理员，其他角色一律 `403`，即使同时指定 `user_id=self`。
  `view=global` 不取消其他过滤条件，平台管理员也可用 `user_id=self` 只查本人。
- 不传 `view` 时按当前权限返回可见范围。`credential:audit` 保留独立的管理权限：
  可查看获授权精确 target 的其他用户 `credential:*` 记录；他人无 target 的凭据事件仍要求
  覆盖无 target 事件的授权。该权限不授予其他用户的普通操作历史，也不授予 `view=global`。
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

### 执行生命周期与人工核对

`executionLifecycle` 协议采用 `doops.sh/execution-lifecycle/v1`，声明
`terminalResponse`、`backgroundLifetime` 和 `promptCancellation`。Gateway 只对后台任务
要求完整协议；结构化 SSH 由 `structuredSSH` 参数协议保护，前台 shell、SSH、普通 prompt 和 `ask --apply` 统一进入目标
调度并以实际终态响应作为完成证据，不因旧 Agent 缺少能力声明而阻断正常发布。
前台调用被取消、超时或断连时，若没有足够终态证据仍写入 `outcome_unknown`，不能把
本地等待结束当成远端 mutation 已停止。

`GET /v1/admin/operations` 返回 `audit_id`、`state`、`resource` 和 `blocked_by`。
`DELETE /v1/admin/operations?id=<operation-id>` 返回 HTTP `202`：

```json
{"id":"op-example","cancel_requested":true,"state":"cancel_requested"}
```

该响应仅确认取消请求，资源所有权必须保留到原始请求终态。
终态不确定时审计持久化为 `outcome_unknown`，并阻止后续目标 mutation。
管理员在独立核对停止证据后可发送 `POST /v1/admin/operations`：

```json
{"id":"audit-42","confirm_stopped":true,"reason":"operator investigation","evidence":"human-reviewed stopped state"}
```

成功响应为 `{"id":"audit-42","state":"resolved_manually"}`。
缺失确认、reason 或 evidence 会被拒绝；仍在运行的操作不得人工核对解除。
该动作记录人工证据，不表示机器验证成功，也不自动接管或重放操作。
`POST /v1/targets/unlock` 仅重连空闲目标，拒绝活动、排队或未核对操作。

定时巡检 job 使用 Gateway 持久 lease 单飞执行。`tick` 与 `run-now` 同时命中同一
job 时，后到请求返回 HTTP `409`；执行期间自动续租，成功、失败、取消和超时都会在
结束路径释放 lease，Gateway 进程异常退出后由 lease 到期自动接管，不需要人工
unlock。每次运行都登记为可取消的 active operation。

管理员 mutation 审计：

- 管理员 GET 接口不要求 session，也不会产生 mutation 审计；远端 RPC/CLI 仍使用显式 session。
- `/v1/admin/` 下的用户创建、密码、启停、grant 增删、token 签发/撤销、仓库增改删/
  测试/克隆、巡检创建/删除/启停/立即运行、操作取消、代理路由增改删，以及
  `POST /v1/targets/unlock` 均记录调用者 `user_id + token_id`、动作和资源元数据、
  开始/结束时间与成功、拒绝或失败结果。token 签发只有一条审计，不重复记录。
- 此类事件的 `action=admin`；`command_summary` 是白名单 JSON 元数据，包含注册路由、
  HTTP method/status，以及可用的资源 ID、subject user ID、授权动作或启停值。
  请求体、密码、token 明文、仓库 URL/认证内容、配置、巡检命令、响应和原始错误均不进入
  summary/tail；失败只记录稳定分类（如 `http_403`、`http_502`），tail 为空。
- 全局用户创建/密码/启停、grant 增删、token 签发/撤销、仓库配置增改删/连接测试、
  巡检任务创建/删除/启停必须通过 `UserIsPlatformAdmin`；仅有某个 target 的 `admin`
  不得修改这些全局资源或给自己追加平台权限。此限制在 mutation 审计入口执行，
  不改变现有管理员 GET 的权限或 session 约定。
- 管理员身份不替代目标动作授权：仓库克隆检查目标 `pull`，操作取消/解锁/代理路由
  检查对应目标 `admin`；立即巡检检查匹配目标的实际 `ask/exec` 权限，审计型巡检要求平台管理员。
  普通用户查询本人审计不会获得任何 mutation 权限。
  自动巡检沿用已保存任务的目标选择，不因本次审计改动获得新的用户或目标授权，
  也不会伪造一个在线管理员 token 作为自动任务 actor。
- 配置下发使用既有 `doops_file_write` RPC 和目标 `write` 权限，不新增 admin 配置接口。
  继续使用唯一的 RPC 审计，保留 target/session/文件路径元数据，不保存文件内容、
  下游输出或原始错误；下游 tool error 记录为失败。其他 RPC/凭据沿用其既有审计。
- 管理 mutation 与配置写入在执行前检查审计 INSERT 和结果 UPDATE 可写性，失败返回
  `503 audit_unavailable`（RPC 为 `-32603`），不执行 mutation。执行后才发生的审计故障
  返回 `audit_completion_failed; mutation outcome requires verification`，不返回成功响应；
  持久的 pending 记录保留。该错误不表示远端动作已经回滚，调用方必须先核验真实状态，
  不能盲目重试。巡检下游扫描/Issue 创建/结果保存失败也返回失败，不以“零 Issue”冒充成功。

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
- `404`：路由不存在。
- `405`：HTTP 方法不允许。

JSON-RPC 错误：

- `-32601`：未知方法或未知 Doops 工具。
- `-32602`：请求参数非法。
- `-32603`：Gateway 或目标 agent 内部执行失败。
- `-32003`：目标或动作权限不足。
- `-32004`：目标离线。
- `-32005`：目标忙、目标队列满或等待超时。
- `-32006`：全局或单用户并发限制超限。
- `-32007`：操作取消；不能单凭客户端取消 ACK 推断执行停止。
- `-32008`：`execution_outcome_unknown`，缺少终态或外部 mutation 被中断，需要人工核对；
  持久化阻止后续写入，不能通过重连、unlock 或新 session 绕过。

## 权限

目标动作：

```text
targets:list exec ask read write push pull info check clean agent:upgrade
```

管理员动作：

```text
admin
```

Gateway 权限判断本身是默认拒绝：用户没有匹配 grant 时不能操作 target，且
`targets:list` 不隐式授予其它 action。需要注意，当前管理员创建用户 API 会额外写入
一条 `*/*` 默认 action grant；管理员必须先删除该 bootstrap grant，再按明确的
`cluster/instance/action` 收窄权限。如果调用返回 `403`，检查该用户的精确 grant。

## 操作规则

- 所有远程操作必须设置有意义的 `session_id`。
- 文件传输使用 `push/pull`，`read/write` 只用于小文本。
- 可重复部署使用 `doops_shell`；首次适配、排障和探索使用 `doops_agent_prompt`。
- Kubernetes 变更必须先做 server-side dry-run，再真实 apply。
- 升级 agent 镜像前，先确认新镜像已能在目标环境拉取。
- 生成的脚本和报告放在 `/root/ws/<session>`，成为产品流程后要拉回源码仓库。
