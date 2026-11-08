# Doops WebUI

## Current Gateway Console

The production application is the Next.js console at `/console`. Users sign in
with their existing Gateway account or user token, select an explicit target,
and execute operations under that identity. The console stores the Gateway
session in an HttpOnly cookie, restores and clears it through `/api/session`,
and does not expose the token to React state, localStorage or copied examples.
Each login also creates a fresh session generation. Browser RPC requests carry
the returned `sessionGeneration` in `X-Doops-Session-Generation`; the server
checks it against the current login before opening the upstream connection.
A missing or changed generation returns HTTP 409 with
`code=browser_session_changed`. Existing sessions from before this contract
must sign in again. A batch interrupted by a login change stops its remaining
writes instead of following another tab's new Gateway or account.

When the Gateway returns a token expiry, the cookie lifetime is capped at that
expiry and never outlives the token. A token with no expiry keeps the console's
local seven-day session cap; that cap is only browser storage policy, not token
validity. Gateway `401` responses preserve the structured token reason so the
console can distinguish missing, invalid, expired, revoked and disabled-user
states. A temporary Gateway 5xx keeps the local session cookie so a transient
outage does not force a new login; only an authentication 401 clears it. A 403
means permission denial and keeps the login cookie.

Password login, token import and session restore all validate against
`GET /v1/auth/me`. The Gateway supplies the real username, platform-admin flag,
current token ID, issue time and optional expiry. The console does not infer
identity from target visibility or probe an admin-only endpoint. An absent or
malformed identity endpoint is an explicit error and does not create a cookie.
Token login therefore displays the real username and restores only that user's
target/session selection. The token page marks the current login token and its
revocation confirmation states that the console, CLI and any other clients using
that same token lose access together. Disconnect only clears the browser cookie;
revocation remains a separate explicit action. Neither operation renews a token.

- AI conversation uses the shared Agent Cloud `AgentWorkspace`, v2 runtime,
  queue and store. The host supplies only Gateway transport, target/session
  routing and deployment forms. The session API requires the Agent's
  explicit structured ACP event capability and never downgrades to the old chat
  engine. This source change needs a new shared package publication and matching
  Agent/Web rollout; local fixture evidence is not production acceptance.
- Stopping a running task uses the already authorized hosted session, bound to
  the exact Gateway, caller token, target and session. It does not request
  metadata or a new workspace lease. The session protocol still validates the
  epoch and turn before canceling on the original RPC. A cancellation ACK is
  not completion: the console waits for the original terminal response and
  reports an unknown outcome when that response is unavailable.
  Snapshot/event reconnection to that active hosted operation uses the same
  exact caller binding without a metadata lease; new commands and idle-session
  authorization still query Gateway. A confirmed cancellation records a
  `CANCELLED` command without an error and returns the session to `IDLE`.
  An uncertain apply remains a failure requiring inspection of the actual target.
- AI diagnostics query Agent runtime metadata instead of reading settings or API
  keys. Diagnostics use `operation=ask`; the deployment form requires an explicit confirmation and sends `operation=apply` with target, release, namespace,
  remote Chart, controlled image, CI commit/evidence, reason and acceptance.
- Helm success must include the final live Deployment and task acceptance.
  Native MCP `isError`, protocol errors and interrupted streams remain failures.
- Existing managed Agents have an image-reference upgrade/rollback form. An accepted
  Job is not a completed release; the page observes the same Job and checks the
  Gateway protocol. Verify the serving Pod/imageID/runtime and affected functions.
  First installation runs from the external controller with a cluster kubeconfig.
- Credential pages manage create, staged payload, target bindings, persisted version
  metadata, verify, promote, grant revocation and resource revocation. Payload
  input uses the credential endpoint only and never enters an AI task.
  The `凭据组` button creates a credential bundle and looks one up by name; a
  bundle holds credential IDs only. Steps and limits are in
  [CREDENTIAL_RESOURCES.md](CREDENTIAL_RESOURCES.md#bundle).
- The audit page exposes target/session/self/global scopes and action/status
  filters. Users can read their own history; global audit requires platform admin.
  Credential owners also see events that reference their credentials. The scope
  is applied before the result limit. `outcome_unknown` stays distinct from a
  confirmed failure. Audit query indexes are created when the Gateway starts.
- The resource page lists cluster objects through `doops_k8s` using column
  projections, not full object JSON. A row opens describe, logs, events, rollout
  status, and the pods selected by that workload. Mutations are rollout restart,
  scale, delete pod, cordon and uncordon. Picking one shows the target, object
  and action; the request carries the existing `confirm=true` only after that
  checkbox is ticked. ConfigMaps list namespace, name and data key names through
  the read-only `configmap-keys` operation; the Agent drops the values before
  returning, and an older Agent shows an upgrade notice instead of another
  projection. Secret and ServiceAccount kinds stay unavailable. The database section calls
  `doops_database_inventory` and runs read-only SQL through `doops_agent_prompt`
  (`skill=dosql`, `operation=ask`, `credential_mode=auto`, `sql`, `sql_database`).
  Writes and DDL stay on `doops sql --mode apply`. The host section calls
  `doops_node_info` and `doops_shell`. Live target acceptance is still separate
  from this page existing in the console.
- The website page routes through the same `doops_agent_prompt` Gateway RPC and
  invokes the bundled DoWeb skill. It reuses the selected target and Gateway
  audit for SiteSpec, Nginx validation, reload, health and rollback evidence.
- The admin grant form chooses a user and a registered cluster/instance. Cluster
  and instance start empty, so a blank submit cannot become `*`. `*` is sent
  only when the operator explicitly picks every cluster or every instance in a
  cluster, and the form states that scope before submission. An instance row's
  grant action opens the same form with that instance selected.
- Administrator mutations are audited with actor/token IDs, resource metadata,
  HTTP result and stable errors. Credential/configuration bodies are excluded.
  Unavailable audit storage blocks a mutation.
- 管理员实例页将在线、近期离线、历史和已归档记录分开计数。历史和已归档记录默认折叠；
  只有离线记录可在显式确认后归档或恢复。归档不会删除 target 授权、令牌、凭据绑定或审计历史，
  Agent 重新连接时会自动恢复该记录。离线超过 24 小时的实例记录由 Gateway 自动删除，授权仍保留。
- The configuration library writes files inside the selected session workspace.
  Templates use relative paths such as `nginx/conf.d/doops.conf`; absolute,
  root-only and traversing paths are rejected before dispatch. Existing saved
  templates with absolute paths must be edited explicitly. Writing a workspace
  file does not reload Nginx or activate an Agent model. Those changes use the
  corresponding explicit deployment or managed model operation.
  A successful write marks only the exact template version sent; editing the
  template while a batch is running keeps the newer version unpublished.

The complete Web/CLI/skill boundary and authorization matrix is documented in
`UNIFIED_OPERATIONS_WORKBENCH.md`.

## Console layout

The shared UI tokens, control sizes, states and responsive rules are defined in
[`DESIGN.md`](../../DESIGN.md). Console controls reuse the shared classes in
`app/globals.css`; the host theme is loaded after Agent UI's packaged styles.

The console shell fills the available viewport width. Overview, workbench,
deployment, CI/CD and administrator views use a 16px horizontal gutter; page
roots must not add a centered `max-width`. Split-pane dividers, local table
scrolling and bounded document previews remain inside the page. The public home,
cases and deployment guide keep their shared `max-w-6xl` container; the public
deployment wrapper owns this limit so the same guide body can fill the console.
Below 768px, the file browser stacks above the editor and its toolbar wraps,
so file actions stay inside the viewport instead of widening the page.

Deploy Gateway and Web from matching successful CI artifacts with the canonical
host scripts. Record the binary source commit, Web digest, health, reconnects and
rollback artifact. Local fixtures do not establish live Helm or production-user acceptance.

## 部署

- 生产控制台是仓库根目录的 Next.js 应用，由 `Dockerfile.web` 构建、GitHub 发布为
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
