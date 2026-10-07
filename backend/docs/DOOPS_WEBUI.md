# Doops WebUI

## Current Gateway Console

The production application is the Next.js console at `/console`. Users sign in
with their existing Gateway account or user token, select an explicit target,
and execute operations under that identity. The console stores the Gateway
session in an HttpOnly cookie, restores and clears it through `/api/session`,
and does not expose the token to React state, localStorage or copied examples.

- AI conversation uses the shared Agent Cloud `AgentWorkspace`, v2 runtime,
  queue and store. The host supplies only Gateway transport, target/session
  routing and authorized deployment forms. The session API requires the Agent's
  explicit structured ACP event capability and never downgrades to the old chat
  engine. This source change needs a new shared package publication and matching
  Agent/Web rollout; local fixture evidence is not production acceptance.
- AI diagnostics query Agent runtime metadata instead of reading settings or API
  keys. Diagnostics use `operation=ask`; the deployment form requires explicit
  authorization and sends `operation=apply` with target, release, namespace,
  remote Chart, controlled image, CI commit/evidence, reason and acceptance.
- Helm success must include the final live Deployment and task acceptance.
  Native MCP `isError`, protocol errors and interrupted streams remain failures.
- Existing managed Agents have a pinned-image upgrade/rollback form. An accepted
  Job is not a completed release; the page observes the same Job and checks the
  Gateway protocol. Verify the serving Pod/imageID/runtime and affected functions.
  First installation still requires an authorized cluster administration context.
- Credential pages manage create, staged payload, exact grants, persisted version
  metadata, verify, promote, grant revocation and resource revocation. Payload
  input uses the credential endpoint only and never enters an AI task.
- The audit page exposes target/session/self/global scopes and action/status
  filters. Users can read their own history; global audit requires platform admin.
  Other users' credential events retain precise `credential:audit` scope checks.
  A platform administrator can explicitly maintain the fixed query indexes from
  the global view. This operation is audited and preserves all history.
- The database page routes through the same `doops_agent_prompt` Gateway RPC and
  invokes the bundled DoSql skill. It reuses the selected target, session and
  Gateway audit; database connections and credentials are never stored in the
  browser. DoSql's signed approval is change evidence, not a second login path.
- The website page routes through the same `doops_agent_prompt` Gateway RPC and
  invokes the bundled DoWeb skill. It reuses the selected target and Gateway
  audit for SiteSpec, Nginx validation, reload, health and rollback evidence.
- Administrator mutations are audited with actor/token IDs, resource metadata,
  HTTP result and stable errors. Credential/configuration bodies are excluded.
  Unavailable audit storage blocks a mutation.

The complete Web/CLI/skill boundary and authorization matrix is documented in
`UNIFIED_OPERATIONS_WORKBENCH.md`.

Deploy Gateway and Web from matching successful CI artifacts with the canonical
host scripts. Record the binary source commit, Web digest, health, reconnects and
rollback artifact. Browser acceptance and current limitations are recorded in
`reports/gateway-console-release-2026-10-02.md`; local fixtures do not establish
live Helm or production-user acceptance.

## Legacy Go WebUI

> 当前生产控制台是仓库根目录的 Next.js Web 应用，通过 `Dockerfile.web` 和 CNB
> `<repo>/web:<tag>` 镜像交付。本文描述的是旧 Go `doops-webui`，只保留给明确审计过的
> legacy 维护场景；日常环境不提供真实 SSH 连接时，不应使用 `scripts/deploy-webui.sh`。

`doops-webui` 是一个轻量 legacy Web 控制台：在浏览器里连接 `doops-gateway`，列出在线
`cluster/instance`，并对选中的实例发起 `ask` 自然语言任务，实时看到 doagent 的
流式执行过程。

## 为什么需要一个后端

浏览器原生 `WebSocket` 无法设置 `Authorization` 请求头，而 gateway 的 `/v1/rpc`
只认 `Bearer token`。`doops-webui` 在服务端注入 token 并把浏览器 WS 双向桥接到
gateway，因此不需要改动 gateway 任何代码。

```
浏览器 SPA ──HTTP/WS──▶ doops-webui ──HTTP/WS(+Bearer)──▶ doops-gateway ──▶ doops-agent
```

## 端点

- `GET /` 内置静态前端（go:embed）
- `POST /api/login` 透传到 gateway `POST /v1/auth/login`，返回 user token
- `GET /api/targets?gateway=<url>`（带 `Authorization: Bearer`）透传 `GET /v1/targets`
- `GET /api/rpc?gateway=<url>&cluster=&instance=&token=`（WS）桥接到 gateway `/v1/rpc`

token 只保存在浏览器内存里；后端不落盘、不缓存密码。

## 构建

```bash
bash scripts/build-webui.sh
# 或本机直接跑：
cd agent && go run ./cmd/webui -port 8088
```

交叉编译同 gateway，用 `GOOS`/`GOARCH` 环境变量控制。

## 运行

```bash
./bin/doops-webui -port 8088 -gateway https://doops.l8ai.cn
```

参数：

- `-port`：监听端口，默认 `8088`
- `-gateway`：可选，预填到前端输入框的默认网关地址（仍可在页面修改）
- `-static`：可选，从指定目录加载前端资源（前端开发时用），默认用内置 embed

打开 `http://localhost:8088`：

1. 填 Gateway 地址，用账号密码登录或直接粘贴 user token，点「连接」。
2. 在左侧选择一个在线实例（绿点空闲 / 黄点活动 / 红点忙）。
3. 在右侧输入运维意图，点「发送 ask」（也可 Cmd/Ctrl+Enter），实时查看
   step / tool / AI 文本 / 完成等事件流。

## 部署建议

- 生产环境使用 `Dockerfile.web` 构建的 Next.js 控制台镜像，不使用本 legacy 二进制。
- Gateway 主机上的正式发布使用 `backend/scripts/deploy-web-console.sh`，只接受
  immutable image digest，先在 `127.0.0.1:3001` 启动候选容器并验证，再替换
  `doops-web`；旧容器保留为 rollback。随后用
  `backend/scripts/deploy-gateway-tls-proxy.sh` 发布版本化 Nginx 配置。
- 生产容器必须设置 `DOOPS_CONSOLE_ALLOWED_GATEWAYS`，值为允许代理的 Gateway
  origin，多个值用英文逗号分隔，例如
  `DOOPS_CONSOLE_ALLOWED_GATEWAYS=https://doops.example.com`。控制台只接受精确
  origin，未配置时生产代理接口拒绝请求；除本机 loopback 外，生产 Gateway 必须使用
  HTTPS。
- `scripts/deploy-webui.sh` 默认禁用；只有显式设置
  `DOOPS_ALLOW_LEGACY_SSH_WEBUI_DEPLOY=1` 且确有真实 SSH 主机时才允许 legacy 维护。
- WS 升级放开了 `CheckOrigin`，公网暴露时应由反向代理/防火墙限制来源。
- 每个浏览器会话使用独立 `session_id`（`webui-<rand>`），与 gateway 的串行/排队
  语义一致。
