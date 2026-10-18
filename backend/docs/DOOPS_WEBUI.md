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
- Existing managed Agents have an image-reference upgrade/rollback form. An accepted
  Job is not a completed release; the page observes the same Job and checks the
  Gateway protocol. Verify the serving Pod/imageID/runtime and affected functions.
  First installation still requires an authorized cluster administration context.
- Credential pages manage create, staged payload, target bindings, persisted version
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
rollback artifact. Local fixtures do not establish live Helm or production-user acceptance.

## 部署

- 生产控制台是仓库根目录的 Next.js 应用，由 `Dockerfile.web` 构建、CNB 发布为
  `<repo>/web:<tag>` 镜像。
- Gateway 主机上的正式发布使用 `backend/scripts/deploy-web-console.sh`，只接受
  immutable image digest，先在 `127.0.0.1:3001` 启动候选容器并验证，再替换
  `doops-web`；旧容器保留为 rollback。随后用
  `backend/scripts/deploy-gateway-tls-proxy.sh` 发布版本化 Nginx 配置。
- 生产容器必须设置 `DOOPS_CONSOLE_ALLOWED_GATEWAYS`，值为允许代理的 Gateway
  origin，多个值用英文逗号分隔，例如
  `DOOPS_CONSOLE_ALLOWED_GATEWAYS=https://doops.example.com`。控制台只接受精确
  origin，未配置时生产代理接口拒绝请求；除本机 loopback 外，生产 Gateway 必须使用
  HTTPS。
