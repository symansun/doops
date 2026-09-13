# Doops API 对接指南

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

所有用户侧请求都必须使用 user token 鉴权：

```http
Authorization: Bearer <DOOPS_GATEWAY_USER_TOKEN>
```

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

`busy` 只表示 target-wide 不可用，例如独占升级、无 session 的旧式操作或 target 队列阻塞。`status=active` 且 `busy=false` 表示有 session/resource 正在执行，但同一 agent 仍可接收其它 session 的操作。`resources` 用于说明当前被锁的 `workspace:<session>`、`session:<session>` 或 `path:<path>`。

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

### `doops_bg`

提交由 agent 管理的后台任务。调用会立即返回任务 ID；使用 `doops_task_status` 查询日志和最终退出码。

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

仅预览外部发布控制器需要执行的 Agent 镜像变更。Agent 不允许修改承载自身的工作负载。

参数：

```json
{
  "image": "docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest-from-successful-CI>",
  "mode": "k8s",
  "namespace": "doops-system",
  "workload": "deployment/doops-agent",
  "container": "doops-agent",
  "dry_run": true
}
```

需要权限：`agent:upgrade`。

`dry_run` 必须为 `true`；非 dry-run 请求会在 Gateway 分发前被拒绝。正式升级必须由
目标 Agent 之外的 CI/CD、GitOps 或 release controller 执行，并负责新实例心跳验证与
失败回滚。

## 自然语言发布 API 对接模式

平台通过 `doops ask --apply` 发送明确授权的发布任务。任务必须直接给出 target、
服务范围、镜像版本、变更原因和验收条件，Gateway 不得从 prompt、路径或历史状态猜测
这些字段。需要凭据时，调用方显式提供一个或多个 metadata-only credential resource
name；Gateway 将引用绑定当前用户、target 和 session，Agent 返回精确 workload/image/Helm
scope 后才授权和物化。凭据 payload 不进入公开 prompt、doagent 上下文、CLI 输出、日志或
审计记录；Gateway 仅通过内部凭据物化调用向目标 Agent 短暂传递 payload。

生产发布不能由被升级的目标 Agent 自升级。调用方必须以同一 session 的真实健康检查和
关键业务验收作为完成证据。

## 进展和可追溯性

Doops 有三层进展数据：

- WebSocket 流：实时 `notifications/message` 输出。
- Gateway 审计库：按 user、token、target、action、session、status、命令摘要、输出尾部和时间记录持久事件。
- 目标工作区审计：`/root/ws/<session>/.doops-audit-log` 记录该 session 内执行过的 shell 命令。

管理员通过 Gateway API 查询审计：

```http
GET /v1/audit?cluster=doops-oilan&instance=oilan-node&session=deploy_20260514&limit=50
Authorization: Bearer <ADMIN_USER_TOKEN>
```

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
```

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
