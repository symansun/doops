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

向目标 AI agent 下发非发布类的诊断或运维任务。生产发布不得使用该通用入口，
必须使用后文的声明式发布协议。

参数：

```json
{
  "session_id": "diagnose_20260514",
  "instruction": "检查目标节点磁盘和容器运行时状态，只返回诊断结果，不修改资源。",
  "model": "openai/gpt-5.4"
}
```

需要权限：`ask`。

适用于诊断、信息收集和用户明确授权的非发布运维任务。它不能替代
`doops.sh/v3 EnvironmentRegistry`、credential prepare、发布 admission 或审计。

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

## 声明式发布 API 对接模式

平台可以对外提供更高层的发布接口，但内部必须收敛到仓库中的
`doops.sh/v3 EnvironmentRegistry`，不能把自然语言指令、工作区上传或直接 Agent
工具调用作为生产发布协议。一个典型的平台接口可以是：

```http
POST /your-platform/deployments
Content-Type: application/json

{
  "repository": "https://cnb.cool/org/project.git",
  "ref": "main",
  "workflow": "deploy/environments.yaml",
  "environment": "oilan",
  "service": "api",
  "dry_run": true
}
```

平台内部必须按以下固定协议执行：

1. 解析并校验指定 revision 中的 `deploy/environments.yaml`、环境和单个服务声明。
2. 由 Gateway `/v1/credentials/prepare` 生成 metadata-only、单次使用且绑定
   source revision、target、session 和 build definition 的 `CredentialRun`。
3. Gateway 校验目标 Agent 精确声明
   `doops.sh/credential-plan/v2`、`doops.sh/v3`、`doops.sh/cicd-contract/v3`
   及全部绑定能力；不一致时返回
   `426 Upgrade Required`，不得继续分发。
4. Gateway 通过已授权反向隧道发送严格的
   `doops.reconciliation-context/v2` apply envelope；Agent 公共 `/ws` 不接受
   credential control-plane 工具或 apply。
5. Agent 仅在当前 session 内临时物化凭据，调用 doagent 执行声明式 dry-run/apply，
   完成后清理；Gateway 消费 `CredentialRun` 并记录审计。
6. 返回发布状态、审计 ID、source revision、目标和健康检查结果。

生产发布必须由 CI/CD、GitOps 或独立 release controller 调用该协议，不能由被升级的
目标 Agent 自升级。

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

默认 user token 拥有完整目标操作权限，除非该用户被显式配置了更窄的 grant。如果调用返回 `403`，检查该用户在具体 `cluster/instance/action` 上的授权。

## 操作规则

- 所有远程操作必须设置有意义的 `session_id`。
- 文件传输使用 `push/pull`，`read/write` 只用于小文本。
- 可重复部署使用 `doops_shell`；首次适配、排障和探索使用 `doops_agent_prompt`。
- Kubernetes 变更必须先做 server-side dry-run，再真实 apply。
- 升级 agent 镜像前，先确认新镜像已能在目标环境拉取。
- 生成的脚本和报告放在 `/root/ws/<session>`，成为产品流程后要拉回源码仓库。
