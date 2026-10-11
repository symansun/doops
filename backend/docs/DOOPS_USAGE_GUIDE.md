# Doops Usage Guide

## Audit Query Maintenance

When filtered audit queries time out because the Gateway's existing SQLite audit
history has no query indexes, a platform administrator explicitly runs:

```bash
doops -session audit-index-maintenance admin audit optimize --target <target>
```

The selected target identifies the Gateway login, not an Agent database. The
operation executes only the fixed repository SQL through the Gateway's audited
control plane. It is idempotent, accepts no custom SQL and preserves every event.
The global audit page provides the same operation. Audit unavailability or denied
authority blocks DDL; application startup and CI do not run it automatically.

This guide holds command-level usage details. The skill file is only the entry point.

## Command Choice

| Need | Command | Notes |
| :--- | :--- | :--- |
| List configured targets | `doops list` | Local config only; not live gateway state. |
| List live gateway agents | `doops -session <s> targets --target <gateway-target>` | First check for gateway troubleshooting. |
| Retire an offline target | `doops -session <s> remove --target <gateway-target>` | Removes only the local route after a successful Gateway query confirms the exact route is not currently visible; online, unreachable, or unauthorized targets are refused. |
| Check Agent protocol capability | `doops -session <s> capabilities --target <t> --require structuredSSH` | Fails closed when the online Agent is too old for structured SSH. |
| Add a Gateway target | `doops add --name <n> --gateway <url> --cluster <c> --instance <i>` | Stores routing only; never stores a target token. |
| Run deterministic shell | `doops -session <s> exec --target <t> --cmd '<cmd>'` | Known checks and explicitly requested operations; standard releases use `ask --apply`. |
| Sync OCI images | `doops -session <s> registry sync --target <t> --source-registry <src> --target-registry <dst> --tag <release> --source-authfile <remote-file> --target-authfile <remote-file> --image <name>` | Generic target-side `skopeo copy --all`, bounded timeout/retry, and manifest digest verification; auth payloads never enter CLI arguments. |
| Build an OCI image | `doops -session <s> image build --target <t> --context <remote-dir> --output oci --output-path <remote-file>` | Generic BuildKit build; supports `--platform` and `--output image --image <ref> --push` for an explicitly authorized registry push. |
| Copy an OCI image | `doops -session <s> image copy --target <t> --source <ref> --destination <ref>` | Generic Skopeo transport-to-transport copy with optional authfiles and raw manifest digest verification. |
| Run nested SSH safely | `doops -session <s> ssh --target <t> --host <user@host> -- <command> [args...]` | Preserves remote argv across nested shells. |
| Run a long build | `doops -session <s> exec --bg --target <t> --cmd '<cmd>'` | One RPC waits for the Agent task's terminal result; no task-status polling or command retries. |
| Ask the edge agent to reason | `doops -session <s> ask --target <t> --msg '<task>'` | Best for diagnostics and multi-step investigation. |
| Run an authorized release | `doops -session <s> ask --target <t> --apply --credential <name> --msg '<facts>'` | Canonical business workload CI/CD entry. |
| DoSql read-only query or scan | `doops -session <s> sql --target <t> --mode query --credential <database-resource> --request '<request>'` | Uses the selected Gateway target and Agent skill; no local connection file is read. |
| DoSql change plan | `doops -session <s> sql --target <t> --mode plan --request '<request>'` | Returns business summary, risk and verification evidence without mutation. |
| DoSql controlled mutation | `doops -session <s> sql --target <t> --mode apply --credential <database-resource> --request '<approved change>'` | Requires Gateway action/credential scope and DoSql change evidence. |
| DoSql target discovery | `doops -session <s> sql --target <t> --mode discover --request '<request>'` | Uses `mcp_dosql_read` to scan non-Secret target metadata; it never stores connection secrets. |
| DoSql repository/resource discovery | `doops -session <s> sql --target <t> --mode discover --root <authorized-repo> --resources-file <assets.json> --project <id> --environment <id> --dosql-script <path>/dosql-agent.mjs` | Scans authorized code/config metadata and persists a metadata-only database inventory. |
| DoWeb read-only inspection | `doops -session <s> web --target <t> --mode check --request '<site/Nginx/TLS/health request>'` | Uses the selected Gateway target and DoWeb evidence path. |
| DoWeb site plan | `doops -session <s> web --target <t> --mode plan --request '<site publish request>'` | Produces SiteSpec, Nginx plan, checks and rollback without mutation. |
| DoWeb controlled publish | `doops -session <s> web --target <t> --mode apply --credential <web-resource> --request '<site, dist, domain, proxy, acceptance>'` | Requires Gateway mutation authority and `nginx -t`/reload/health evidence. |
| Push a local directory | `doops -session <s> push --target <t> --src <dir>` | Syncs into `/root/ws/<session>`. |
| Pull a remote workspace | `doops -session <s> pull --target <t> --dest <dir>` | Use for large files and binary assets. |
| Read small text | `doops -session <s> read --target <t> --path <file>` | Small text only. |
| Write small text | `doops -session <s> write --target <t> --path <file> --file <local>` | Also supports `--content` and stdin. |
| Read system information | `doops -session <s> info --target <t>` | CPU, memory and disk metadata. |
| Use structured Kubernetes operations | `doops -session <s> k8s --target <t> ...` | Restricted operational interface; publishing uses `ask --apply`. |
| Clean workspace | `doops -session <s> clean --target <t> --workspace <s>` | Cleans `/root/ws/<workspace>`. |
| Check an image deployment | `doops check ...` | Compares configured service image with build metadata. |
| Upgrade a managed Helm Agent | `doops -session <s> upgrade --target <t> --image <repository>@sha256:<digest>` | In-cluster Job only. `--dry-run` previews parameters. |
| Release an existing standalone Docker Agent | External `scripts/release-host-agent.sh` | Explicit DockerHost controller, not CLI fallback; real read-only `--dry-run`. See [SOP](BUILD_DEPLOY_SOP.md#独立-dockerhost-发布). |
| Cache token | `doops login ...` | Gateway login can exchange username/password for user token. |
| List target credentials | `doops credential list --target <t>` | Uses the cached gateway login and returns only metadata granted to that user on the selected cluster/instance. |
| Preflight development credentials | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` | Fails closed unless every declared credentialRef has a matching active grant use on the selected target. |
| Configure a credential once | `doops -session <s> credential configure --target <t> --id <id> --namespace <ns> --use <use> <source-and-scope-flags>` | Reuses managed storage, stages, grants to the current user, verifies every active scope and promotes. See `CREDENTIAL_RESOURCES.md`. |
| Manage credential lifecycle | `doops credential <create|put|grant|verify|promote|grant-revoke|revoke>` | See `CREDENTIAL_RESOURCES.md`. |
| Create Gateway user and API key | `doops admin user create --target <t> --user <u> (--password-stdin\|--generate-password)` | Removes bootstrap wildcard access and applies only explicit `--grant-target` scopes. |
| List Gateway users | `doops admin user list --target <t>` | Returns metadata only. |
| Manage target grants | `doops admin grant <list\|add\|delete> ...` | Grants use exact configured `cluster/instance` routes. |
| Issue token as admin | `doops admin token create ...` | Requires a gateway token with `admin` permission. |
| Inspect/cancel active operations | `doops admin operations <list|cancel> --target <target> ...` | Gateway scopes the operation list and cancellation to the selected cluster/instance; cancellation requests termination, not confirmation that work stopped. |
| Manage scheduled issue jobs | `doops admin jobs <list|add|run|enable|disable|rm|issues> ...` | See `DOOPS_SCHEDULED_ISSUES.md`. |
| Reconnect an idle target | `doops unlock --target <t>` | Refuses active or queued work; does not force-release operations. |
| Remove cached token | `doops logout --target <t>` | Removes local token cache. |

DoSql 必须使用 `doops sql` 入口。普通 `doops ask --msg '使用 DoSql'` 不保证注入
`mcp_dosql_read/execute`，也不能替代 DoSql 的 target discovery、credential scope 或
journal/evidence 链。

DoSql 的凭据使用继承本次目标 `ask`/`exec` 权限，不再要求额外 `credential:use` 或逐操作员 grant。
Gateway 直接解析保存的 target/database 绑定和 active version，不让模型生成凭据 scope。
不存在 active version 时先用 `credential configure` 安全导入；该错误明确为 `credential_version_not_found`。

`registry sync` is a thin generic wrapper over the target Agent's audited exec path. The
standard DoOps Agent image provides `buildctl` for image builds and `skopeo` for OCI
copy/inspect. The command performs copy and source/destination manifest digest checks;
it has no AMP-specific path and never installs a missing tool at runtime. If the target
does not advertise `skopeo`, the operation fails with a missing-tool error; upgrade the
DoOps Agent image or use an explicitly authorized target that already provides it.

`image build` and `image copy` are thin generic wrappers over the same audited exec path.
They use standard BuildKit and Skopeo transports, so the command does not depend on Docker,
nerdctl, Harbor, AMP image names, or a project-specific script. For private Registry output,
the target must have an authorized remote authfile; credentials are never accepted inline in
the command arguments.

## Basic Checks

```bash
doops list
doops version
doops -session smoke targets --target <gateway-target>
doops -session smoke exec --target <gateway-target> --cmd 'hostname && date -u +%FT%TZ'
doops -session smoke-capabilities capabilities --target <gateway-target> --require structuredSSH
doops -session image-toolchain-check info --target <gateway-target>
```

`info` 输出中的 `buildctl: OK`、`skopeo: OK` 和 `buildkit-sock: OK` 表示目标具备
DoOps 通用镜像工具链。镜像构建使用明确授权的 `exec` 命令调用 `buildctl`；镜像复制
和源/目标 manifest 校验使用 `image copy` 或 `registry sync`。两者都不需要 AMP 专用脚本或外置同步 Job。

`doops version` 的 `release` 是唯一 DoOps 发布版本，`commit` 是该版本对应的完整
源码 provenance；`doops targets` 的 `RELEASE_COMMIT` 必须与它一致。输出中的
`cli-contract/v3` 只通过 `doops --contract` 检查协议兼容性，不是另一个发布版本。
Agent 未提供完整 `RELEASE_COMMIT` 时只能视为旧运行时，升级验收不会通过。

`doops list` reads local config. `doops targets` asks gateway for live connected agents. A target can be listed locally but unavailable in gateway because it is offline or because the logged-in user lacks the exact target grant; the CLI does not collapse these cases into a definitive offline diagnosis.

To remove a stale local route, use `doops -session <s> remove --target <gateway-target>`. The command first queries the configured Gateway and refuses to modify the config if the Gateway is unreachable, authentication fails, or the exact route is currently visible. It does not delete Gateway grants, audit records, proxy routes, or historical agent status.

`doops targets` separates target-wide busy from normal per-session activity:

- `busy=true` means an Agent self-upgrade holds the target-wide exclusive lease. Ordinary mutations and waiting sessions do not set target-wide busy.
- `status=active` with `busy=false` means one or more sessions/resources are running; ordinary Shell/SSH/bg/apply operations are serialized only within the same target + session, while different sessions can use the same Agent concurrently.
- `resources` shows workspace locks such as `workspace:<session>`.
  Shell/SSH/bg and apply use a session-scoped mutation lease; a session is an ownership key, not an OS or remote-resource sandbox.
- The admin operation view shows both the requested resource and the effective lock. A
  `target-mutation:<target>` request with a session is actually fenced as
  `session-mutation:<target>/<session>`; do not infer target-wide exclusivity from the
  requested resource label.
- Public HTTP proxy requests are classified by method: `GET/HEAD/OPTIONS/TRACE` are read-only
  queries; other methods use a route-scoped resource lock and record `outcome_unknown`
  when the Agent disconnects before a terminal response.

Cancellation uses the same session/resource conflict rules as admission. Audit-only
finalization retries retain evidence, not execution leases; even the same session can
start new work after execution cleanup. Missing-session mutations are rejected,
not silently turned into target-wide operations. A session is not a sandbox: callers
must not concurrently mutate the same release or database from independent sessions.

Existing WebSocket connections recheck their token identity for each new operation.
Disabled accounts and revoked/expired tokens cannot start new work; this does not
add a manual approval step. Target administrators see only their authorized target
operations, instances and proxy routes. Global user/grant/token/repository/job lists
remain platform-admin resources. Attachment uploads verify the live target and ask
scope before storing bytes.

`capabilities --require structuredSSH` performs only the Gateway WebSocket initialize
handshake. It succeeds only when the selected online Agent advertises the exact
structured SSH protocol; it never falls back to `doops exec`.
`capabilities --require executionLifecycle` checks background execution's exact
terminal-response, managed-lifetime and cancellation contract. Only `exec --bg`
requires it before dispatch; foreground exec, SSH and ask do not. Background
execution remains one streaming RPC, not a promise to survive disconnection.
Other protocol names are rejected locally before configuration, authentication
or network access; those argument errors are not evidence of an outdated Agent.
It does not prove that the Agent supports in-cluster self-upgrade. Verify the
installed CLI and target image against the selected release before choosing an
upgrade path; source changes alone do not update either runtime.

Managed update controllers use the Agent node and runtime class. The requested
immutable image remains an exact Deployment/Pod spec requirement; runtime imageID
is independently checked against the controller pinned to that image. Automatic
rollback checks the pre-update runtime identity. Successful complete verification
persists revision/image/runtime evidence in `doops-upgrade-verified-<release>`.
Manual rollback selects only an evidenced superseded revision with a pinned image
and rechecks the admitted revision before mutation; missing evidence fails closed.
Legacy releases without that evidence need an authorized external rollback.

Gateway 控制面发布不走 doops target。必须从 CNB 不可变 Agent 镜像提取 Gateway，
并把同一个 `main` commit 传给发布脚本；脚本会拒绝没有该 commit provenance 的本地旧二进制：

```bash
bash scripts/extract-gateway-from-image.sh \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<verified-ci-digest> \
  --source-commit <verified-main-commit>
bash scripts/deploy-gateway.sh \
  --host <gateway-host> \
  --user <ssh-user> \
  --binary backend/bin/doops-gateway \
  --no-build \
  --source-commit <verified-main-commit> \
  --verify-target <configured-doops-target> \
  --verify-session gateway-release-<release>
```

If gateway returns `user operation limit exceeded`, list active gateway operations and cancel stale ones with an admin token:

```bash
doops -session <s> admin operations list --target <gateway-target>
doops -session <s> admin operations cancel --target <gateway-target> --id <operation-id>
```

Both commands scope operation lookup to the target's exact `cluster/instance`.
The CLI refuses to cancel an operation that is missing, unscoped, or belongs to
another target on the same Gateway.

Cancel returns HTTP `202` with `cancel_requested=true`, meaning the request was
registered, not that the operation stopped. The operation keeps ownership until
the original terminal response. Shell-style commands join process-group
termination; `ask` waits for the matching doagent turn terminal event. Missing
terminal evidence persists `outcome_unknown` and is reported as failure, not success.
This includes interrupted workspace writes, cleanup and clone/commit operations
without a terminal reply. A confirmed local completion after cancellation is
not an unknown outcome. Read-only query failures do not quarantine the target.
Agent progress notifications carry `params.request_id`; Gateway and CLI route them by
request ID so concurrent calls in one session cannot overwrite one another. The Gateway
attempts terminal persistence before returning success. If that write fails, it reports
`audit_completion_failed` and retries the original evidence independently of execution
ownership. Completed execution cleanup releases the lease even while the audit retries.
Interrupted Shell/SSH/bg, apply and Kubernetes mutations also require reconciliation:
local process or turn termination does not fence remote or detached work. Historical
unknown audits do not participate in new-task admission; they are neither deleted nor
changed to success. Active work still uses resource locks, queueing and cancellation;
an audit storage outage is not an execution lock. The operation list shows
`waiting`, `running`, `cancel_requested`, `finalizing`, `outcome_unknown`, `audit_id`,
`resource`, `requested_resource`, `effective_lock` and `blocked_by`. `running` is the admission
state for admission and unguarded reads; it does not prove that no Agent call was sent.
Interrupted `running` rows become terminal `error` after restart. `executing` identifies a
dispatched guarded operation and becomes `outcome_unknown` if its owner is lost. A failed
terminal audit write retries the original status, error, timestamp and output evidence,
not an inferred replacement result. Waiting for a resource
does not consume a user execution slot.
Different sessions can run Shell/SSH/bg/apply concurrently; operations sharing one
session or an explicit shared resource remain serialized. Structured Kubernetes queries and task status remain available during a
mutation or unknown outcome. Agent upgrades remain exclusive. Different routes to
one release/database are not coordinated by this target-local lock; do not bypass it
using another target. There is no safe force-preemption or automatic replay protocol.

CLI streaming and captured output preserve notification text exactly, including JSON,
blank lines and partial chunks. Business output cannot declare an Agent event or task
completion. Shell commands start in the Agent process directory; use an explicit `cd`
when a command needs a session workspace. Agent reconnection waits for old
connection-owned executions to finish cleanup before registering again.
CLI timeouts request cancellation and continue observing the original RPC. Missing
terminal evidence reports `execution_outcome_unknown`; no automatic retry is made.
Captured output is retained on failures for diagnosis.
Shell audit records the actual command and output; it no longer guesses file paths
from `kubectl apply -f` text or reads incidental files into the audit.

An SSH failure with an Agent structured terminal result is an ordinary failed
operation when it has a remote exit code in `1..254`, or when the structured
tool error has no exit code. Exit code `255`, signal termination, cancellation,
disconnection and missing structured terminal evidence still report unknown outcomes. Command
text never determines whether an SSH operation is safe to preempt.

Verified terminal Helm tool errors are failed operations, not unknown outcomes.
One successful tool cannot hide another failed tool in the same apply. Credential
operations lock their exact resource IDs, allowing unrelated credentials to run
concurrently while preserving serialization for the same credential.
Manual execution reconciliation is no longer available: operator declarations
must not rewrite an audit or force-release an active lease. Historical unknown results
remain readable without blocking the whole target. Failure or uncertainty never
authorizes automatic mutation replay. `unlock` only reconnects an idle target and
refuses active or queued operations.

## Gateway Admin Tokens

给用户签发 token 时，优先用 CLI 管理入口，不要 SSH 到 gateway 主机手工跑
`doops-gateway token create`：

```bash
doops admin token create \
  --target jm \
  --user gaojiaqi \
  --name maintenance \
  --expires 720h
```

`--target` 必须是本地配置里的 gateway target，并且它当前使用的 token 要有
`admin` 权限。新 token 只打印一次；需要写入本机缓存时显式使用：

```bash
doops admin token create \
  --target jm \
  --user gaojiaqi \
  --name maintenance \
  --expires 720h \
  --save-as jm
```

查看 token 元数据和撤销旧 token：

```bash
doops admin token list --target jm --kind user
doops admin token revoke --target jm --id <token-id>
```

列表只返回 ID、前缀、归属和状态，不返回 token 明文。管理员凭据只从
`--target` 对应的本地受限存储读取，不得通过命令行参数传入。

## Gateway Users And Target Grants

CLI 已封装用户创建、API key 签发和精确 target grant 管理。管理员 token 只从统一
`auth.json` 读取并放入 Authorization header，不得出现在命令行参数、请求 URL、日志或
文档中。用户密码只通过 stdin、交互式终端或 `--generate-password` 输入，禁止放入命令
参数或自然语言 prompt。

创建新用户并签发 API key：

```bash
printf '%s' '<PASSWORD_FROM_SECURE_INPUT>' | \
  doops admin user create \
  --target <gateway-target> \
  --user <username> \
  --password-stdin \
  --grant-target <allowed-target> \
  --name user-create \
  --expires 720h
```

`--grant-target` 可以重复。没有指定时，CLI 会删除后端 bootstrap grant，不授予任何
target 权限，并明确输出 warning。`--generate-password` 可替代 `--password-stdin`。
CLI 会先查同名用户，拒绝重复创建；创建或授权、签发、最终复核任一步失败都会停用新用户，
若 API key 已签发则同时尝试撤销，命令以失败退出。

用户和授权的只读/维护命令：

```bash
doops admin user list --target <gateway-target>
doops admin grant list --target <gateway-target> --user-id <user-id>
doops admin grant add --target <gateway-target> \
  --scope-target <allowed-target> --user-id <user-id>
doops admin grant delete --target <gateway-target> --id <grant-id>
```

已有用户可以设置或轮换密码：

```bash
printf '%s' '<PASSWORD_FROM_SECURE_INPUT>' | \
  doops admin user password --target <gateway-target> \
  --user-id <user-id> --password-stdin
```

停用或恢复用户：

```bash
doops admin user disable --target <gateway-target> --user-id <user-id> --disabled=true
doops admin user disable --target <gateway-target> --user-id <user-id> --disabled=false
```

先确认请求对应的精确路由，并检查同名用户：

```bash
doops list
doops targets --target <allowed-target>
```

```http
GET /v1/admin/users
Authorization: Bearer <ADMIN_USER_TOKEN>
```

同名用户已存在时停止创建，转为核对或调整现有 grant。创建普通用户必须显式使用
`admin:false`：

```http
POST /v1/admin/users
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{
  "name": "alice",
  "password": "<PASSWORD_FROM_SECURE_INPUT>",
  "admin": false
}
```

当前 Gateway 会随创建请求自动写入一条 `cluster="*"`、`instance="*"` 的标准操作
grant。这是暂时的过宽 bootstrap 行为，不代表已完成授权。账号在以下收窄流程完成前
不得交付：

1. 从创建响应或用户列表取得 `user_id`。
2. `GET /v1/admin/grants?user=<user_id>`，记录该用户全部 grant ID。
3. 对所有 wildcard scope 或 action `"*"` 执行
   `DELETE /v1/admin/grants?id=<grant-id>`。
4. 为每个允许 target 分别创建精确 grant，并显式列出 actions。
5. 重新读取用户和 grants，确认 `is_admin=false`、grant 数量符合预期且 wildcard 为 0。

普通机器操作的精确 grant 示例：

```http
POST /v1/admin/grants
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{
  "user_id": "usr_xxx",
  "cluster": "doops-oilan",
  "instance": "oilan-node",
  "actions": [
    "targets:list", "exec", "ask", "read", "write",
    "push", "pull", "info", "check", "clean"
  ]
}
```

不要省略 `actions`：空列表会由 Gateway 展开为默认权限集合。`credential:*`、
`agent:upgrade`、`admin` 和 action `"*"` 不属于普通机器操作授权，只有用户明确要求且
风险边界清楚时才单独添加。若收窄或精确 grant 创建失败，立即停用用户再退出：

```http
POST /v1/admin/users/disable
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{"user_id":"usr_xxx","disabled":true}
```

### End-to-End Verification

不要用新用户登录覆盖本机管理员的共享 Gateway 登录态。创建一个临时目录，将所需的
routing-only targets 写入其 `config.json`，并让 `DOOPS_CONFIG` 指向该文件；其同目录
`auth.json` 将保存隔离的验证登录态。

```bash
DOOPS_CONFIG=<temporary-dir>/config.json \
  doops login --target <allowed-target> --username alice --name user-create-smoke

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops targets --target <allowed-target>

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops -session user_create_smoke exec --target <allowed-target> --cmd 'hostname'

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops -session user_create_smoke_denied exec --target <denied-sibling> --cmd 'hostname'

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops -session user_create_smoke clean --target <allowed-target> --workspace user_create_smoke
```

允许 target 必须成功，未授权 sibling target 必须非零退出并被 Gateway 拒绝。最后用
`doops admin token list` 按 `user-create-smoke` 标签找到 token ID，再执行
`doops admin token revoke`。`doops logout` 只删除本地缓存，不能替代服务端撤销。安全删除
临时配置目录后，再交付账号。

## Exec

```bash
doops -session ops exec --target jm --cmd 'hostname && df -h && free -h'
```

Use `exec` for deterministic commands, read-only checks, and known deployment scripts.
标准 Agent 镜像提供出站 `ssh` 客户端。多层 SSH 必须使用结构化入口，不能把 SSH
命令拼进 `exec --cmd` 的单个字符串；这样可在 OpenSSH 边界保留每个远端 argv，避免
嵌套引号被远端 shell 重解析。这不会创建 SSH target，也不会启动 `sshd`。SSH 目标、
参数、远端命令和认证材料必须来自当前明确任务，禁止把私钥或密码内联到命令、prompt、
日志或工作区。

```bash
doops -session ops ssh --target jm --host ops@app.internal \
  --option=BatchMode=yes -- bash -lc 'hostname && systemctl status api'
```

`--option` 只接受 `-4`、`-6`、`-p`、`-i`，以及 `BatchMode`、`StrictHostKeyChecking`、
`ConnectTimeout`、`IdentitiesOnly`、`IdentityFile` 和 `Port`。`key=value` 简写会转换为
`-o<key>=<value>`。会改写目标或执行本地命令的选项会被拒绝。

## Ask

```bash
doops -session ops ask --target jm --msg '只读检查 Kubernetes 节点状态，给出结论和风险'
```

Use `ask` for diagnostics that require exploration. The target must have doagent/model configuration available.

## Development Credential Preflight

需要 Git、Registry、Helm、TLS 或 API 凭据的项目，在仓库中提交 metadata-only 清单：

```yaml
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-114-k8s
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
  - credentialRef: cnb-registry
    use: imagePull
```

Agent 在相关开发操作前运行：

```bash
doops -session dev-preflight-<task> credential preflight \
  --manifest .agent/doops-credentials.yaml
```

CLI 严格解析单文档 YAML，拒绝未知字段、重复的 `credentialRef + use`、target 冲突和
没有 `-session` 的远端检查。它只查询当前登录用户在 manifest target 上可见的 active
resource 和 active grant，输出 `ready`、`missing`、`wrong_use` 或 `ambiguous` 元数据。

清单不是授权源，也不包含 payload、secret 文件路径、默认值或本地 fallback。preflight
不会把凭据注入本机命令或远端 workspace；实际使用仍必须经过现有精确 grant 和受控
materialization 路径。

## CI/CD And Apply

普通业务 workload 的标准发布入口是 `doops ask --apply`。完整职责边界、执行顺序、
凭据预物化、验收和自托管组件例外见
[`NATURAL_LANGUAGE_DEPLOYMENT.md`](NATURAL_LANGUAGE_DEPLOYMENT.md)。

```bash
doops -session release-<commit> ask \
  --target <configured-target> \
  --apply \
  --credential <resource-name> \
  --msg '在 <target> 的 namespace <namespace> 发布 deployment/<workload>，镜像 <repository>@sha256:<verified-ci-digest>；使用 chart https://charts.example/<name>.tgz；原因：<reason>；验收：Secret 类型/key、rollout、最终 Deployment spec/status、实际镜像、Service/Endpoint、健康检查和关键功能路径通过。'
```

必须明确提供 target、服务范围、变更原因、授权资产和验收条件。业务镜像版本必须来自
任务或其明确授权的仓库、Chart、values、现有 release 的只读证据，不猜测，也不要求
将已明确资产中的事实再粘贴到 prompt。覆盖遵循 Chart schema，不固定业务镜像键或版本格式。
普通升级使用 `--reset-values`，不再自动保留现有非镜像 values；需要保留的值须显式作为
资产授权。`asset_source` 默认 remote，当前支持 HTTPS `.tgz`，不支持 `oci://`。
完整资产参数、可选 `expected_image` 和迁移说明见
[`NATURAL_LANGUAGE_DEPLOYMENT.md`](NATURAL_LANGUAGE_DEPLOYMENT.md)。
`--credential` 可重复，但只传 resource name，不能内联 payload；受管 Helm 认证、
精确 scope 与包 digest 钉住不变，允许授权的 values overlay 和字符串覆盖。
发布者必须在调用前独立核对精确 CI source commit、registry digest 和构建成功证据；
`ask --apply`、`--image`、`--version` 和 Helm 工具不自动验证 CI provenance。

CNB CI 负责测试、构建和发布制品；Gateway 负责鉴权、审计、并发控制和凭据预物化；
目标 Agent/doagent 在目标运行环境执行业务 mutation。当前没有 EnvironmentRegistry、
`cicd run`、workflow 环境推断、CICDReport 或 `doops_cicd_result`。

普通前台 `exec` 可以执行操作者明确给出的 Helm 命令，但不替代标准发布编排，也不能使用
managed credential references。标准 Helm `doops-agent` 自身升级使用 `doops upgrade`，仅适用于
Agent 自身 namespace 中的标准 Helm-managed Deployment 和已有匹配 Helm release；未给
`--release` 时必须只有一个匹配 release，给出时按名称选择并校验。Agent Pod 必须挂载
可由 Job 共享的 `/root/.kube` 或 `/root/.kube/config`，Job 的 Kubernetes 权限来自该
kubeconfig，不需要操作机 kubeconfig。旧 Helm Agent 使用外部
`scripts/release-managed-agent.sh`；已有独立 Docker 使用外部
`scripts/release-host-agent.sh`，不要求先迁 K8S，不通过旧 Agent `exec` 升级自己。

## Push

```bash
doops -session build push --target jm --src .
doops -session build exec --target jm --cmd 'ls -la /root/ws/build | head'
```

Standard `push` uses Git workspace sync through Gateway:

- Gateway: `/v1/git/<cluster>/<instance>/<session>.git`

Agent 的 Git handler 只接受已授权 Gateway 反向隧道中的进程内调用，不暴露本地
HTTP `/git` 入口。

Do not reimplement push with tar/base64/chunk upload. `doops_workspace_*` chunk tools are low-level compatibility JSON-RPC tools, not the standard CLI/deploy path; seeing 512KB chunk logs means a script is bypassing the supported Git HTTP path. Push respects `.gitignore` and `.doopsignore`; do not hide deploy manifests or scripts behind ignore rules unless intentional.

## Pull

```bash
doops -session build pull --target jm --dest ./doops-build-output
```

Use `pull` for large files, archives, SQL dumps, images, videos, and generated assets. Do not use `read` or `exec base64` for downloads.

## Read And Write

```bash
doops -session ops read --target jm --path deploy.sh
doops -session ops write --target jm --path notes.txt --content 'hello'
doops -session ops write --target jm --path pod.yaml --file ./pod.yaml
cat ./pod.yaml | doops -session ops write --target jm --path pod.yaml
```

File API paths are relative to the named session workspace. The Web file browser
uses the same paths for open, save, new-file and upload, and its navigation stays
inside that workspace. Use explicit authorized Shell operations for host paths.

`read` and `write` are for small text files and short scripts. Use `push/pull` for directories and binary assets.

## Clean

```bash
doops -session build clean --target jm --workspace build
```

Confirm the session name before cleaning. Do not share one session across unrelated tasks.

## Login And Logout

```bash
doops login --target jm --username alice
doops logout --target jm
```

Login and logout apply to the Gateway, not one target. `auth.json` stores one
token under the normalized Gateway URL, and every configured target on that
Gateway uses the same login state. Target configuration contains routing only;
it must not contain a Gateway user token. Gateway permissions still come from
the user's grants.

## Credential Resources

完整凭据模型、payload schema、权限矩阵、验证、轮换、吊销、bundle、审计和发布引用见
[`CREDENTIAL_RESOURCES.md`](CREDENTIAL_RESOURCES.md)。

首次配置优先使用 `credential configure`，不要求用户逐步执行 create/put/grant/verify/promote。
已有资源传 `--id`，避免重复创建；`--from-secret <name> --keys <keys>` 让 platform admin
在 Agent 内导入指定 Secret，无须回传密码。不指定 Secret 时沿用 stdin payload。
数据库 use 不要求 workload；配置失败不会报告成功，后续仍只通过 `--credential <name>` 引用。

安装正式 CLI 并验证不可变构建信息：

```bash
bash scripts/install.sh --version <release-tag> --project "$PWD"
~/.local/bin/doops version
```

The installer downloads a checksummed CNB Release Asset into `~/.local/bin/doops`
and writes a receipt to `~/.local/share/doops/install.txt`. Skill directories contain
instructions and docs only; they are not CLI executable locations.
Skill/docs are read from the exact Git commit embedded in that CLI, not from the
current working tree. A missing commit or required source file stops installation
before replacing the installed CLI or Skill. Fetch the selected release commit
into this repository and retry; the installer does not create a worktree or fetch
another revision automatically. The receipt records matching `commit` and `skill_commit`.

同一个 Gateway 只登录一次，但查询和使用仍按用户及 configured target 的
`cluster/instance` 隔离：

```bash
doops login --target gw-edu-coder --username alice
doops credential create --target gw-edu-coder \
  --name oilan-registry --scope personal --type registry
doops credential list --target gw-edu-coder
doops credential list --target gw-edu-coder --view managed
```

默认 `target` 视图在当前用户能操作该 target 时返回目标绑定且存在 active version
的 active resource；
`managed` 视图返回本人拥有的 personal resource，platform admin 可以管理平台资源。
两个视图都不返回 payload。`managed` 视图只返回 resource metadata，不加载 grant 详情；
CLI 会显示 `grants=not_loaded`，不能把它解释为没有 grant。target 视图也只有在 resource、
active version 和 active grant 同时存在时才返回凭据。

完整 CLI 生命周期：

```bash
# 1. 写入 staged version
printf '%s' '{"server":"registry.example.com","username":"deploy","password":"..."}' |
  doops credential put --target gw-edu-coder --id <credential-id>

# 2. 创建精确 grant
doops credential grant --target gw-edu-coder \
  --id <credential-id> \
  --cluster doops-edu --instance edu-coder \
  --namespace apps --use imagePull \
  --workload-kind Deployment --workload-name oilan \
  --image-repository registry.example.com/oilan \
  --image-digest sha256:<manifest-digest>

# DoSql database grant: Gateway binds target + credential usability only;
# DoSql/Agent validates asset, environment, engine and database name at execution time
doops credential grant --target gw-edu-coder \
  --id <credential-id> \
  --cluster doops-edu --instance edu-coder \
  --namespace agents --use database \
  --required-keys connection_uri

# 3. 在相同 scope 验证 staged version
doops -session credential-verify-<version> credential verify \
  --target gw-edu-coder \
  --id <credential-id> --version-id <version-id> \
  --cluster doops-edu --instance edu-coder \
  --namespace apps --use imagePull \
  --workload-kind Deployment --workload-name oilan \
  --image-repository registry.example.com/oilan \
  --image-digest sha256:<manifest-digest>

doops -session credential-verify-db-<version> credential verify \
  --target gw-edu-coder --id <credential-id> --version-id <version-id> \
  --cluster doops-edu --instance edu-coder --namespace agents --use database \
  --required-keys connection_uri

# 4. 激活已经覆盖全部 active grants 的版本
doops credential promote \
  --target gw-edu-coder \
  --id <credential-id> --version-id <version-id>

# 5. 吊销单条 grant 或整个 resource
doops credential grant-revoke \
  --target gw-edu-coder --id <credential-id> --grant-id <grant-id>
doops credential revoke --target gw-edu-coder --id <credential-id>
```

payload 只能从 stdin 输入并由 Gateway 加密持久化。验证输出只允许状态、
resourceVersion、digest 和稳定错误类别。Grant scope 是全字段精确匹配，不支持通配符或
从 credential name 推断。

数据库凭据的 resource `state=active` 只表示 resource 未吊销，不表示已有可执行版本。
数据库执行前必须完成 `put -> grant -> verify -> promote`；缺少 active version 或精确
grant 时，Gateway 会在物化阶段拒绝执行。遇到 `credential_invalid`，先检查生命周期和
scope，不要换用普通 `opaqueSecret` 或绕过 Gateway。

## Upgrade

CLI 一键安装或更新：

```bash
bash scripts/install.sh --version <vX.Y.Z> --project "$PWD"
```

K8S 新集群首次安装、旧 Helm Agent 首次启用控制器与显式 Helm 配置变更使用本仓库
`scripts/release-managed-agent.sh`，不能重跑外部清单安装器替代 Helm 升级。
首次安装完整命令与验收见 `CLUSTER_INSTALL_GUIDE.md`；这条 bootstrap 路径与普通
业务 `ask --apply` 分开。外部控制器可以位于授权管理主机或 CI，不必在开发机。

已有独立 Docker Agent 用 `backend/scripts/release-host-agent.sh` 和同目录 Python
实现，由外部宿主控制器发布并保持原配置。必须显式指定 SSH 端口，核验主机密钥；
可引用已认证私有 SSH socket，不接收密码/token。完整必填参数、真实只读 `--dry-run`、
宿主锁、停止后保留旧容器、外部回滚以及实际容器/镜像/版本、Gateway
`release_commit`、structuredSSH 协议/argv 派发/预期失败终态、foreground/bg 真实 RPC
流与 Root RequestID 验收见
[主 SOP](BUILD_DEPLOY_SOP.md#独立-dockerhost-发布)。这不是 `doops upgrade` 的 Docker
模式或自动 fallback，也不是无 Agent 主机的 bootstrap，不依赖旧 Agent 的
`doops_agent_upgrade` / `managedHelm`。
候选镜像先由外部准备，dry-run 仅只读检查已有制品，缺失明确失败；候选二进制静态
`--version` 检测仅在正式发布停止旧容器前执行，不启动 Agent 或新 target。
外部 bootstrap 密码只走 stdin，不传给 Agent；外部管理 SSH 成功另记，不能据此宣称
Agent 已成功认证 SSH 目的地主机。

已经改用 Helm chart `deploy/helm/doops-agent`、并且 Agent 镜像包含自升级控制器的节点，
用 `doops upgrade`，不需要本机 kubeconfig。普通业务仍使用 `doops ask --apply`。
发布者须先通过只读检查确认现网采用标准 `/app/sandbox-entrypoint.sh`；
CLI 的参数预览不替代这项检查。Agent 进程不执行这次 Helm mutation；
集群内 Job 复用 Agent 已挂载的 kubeconfig。Chart 先起新 Pod，Gateway
切换会话后才删旧副本；新 Pod 不持续就绪或 `structuredSSH` 探针失败会回滚。
`doops upgrade` 只接受 Agent 镜像和 rollback 操作，不修改 `proxy.allowedHosts`、
`hostAliases` 或其它 Agent Helm 配置。

还没有该控制器的旧 Helm Agent，以及 K8S 新集群第一次接入，仍用
`scripts/release-managed-agent.sh` 和操作机 kubeconfig：

脚本会先验证 Kubernetes API 和 kubeconfig 可达性，再读取 Helm release。`--values` 新集群
模式是 bootstrap-only，只做 Helm wait 和最终 Deployment 检查，不做 Gateway 接管或自动
回滚；完成后必须继续执行安装验收。API 超时、
EOF、鉴权或其它连接失败会直接报告控制器不可达，不会误报为“release 不存在”。新集群
无 Agent 时传完整 `--values` 走安装路径；已有 workload 但 Helm release 缺失时脚本会
停止，不执行 adopt，必须先完成独立的 `deploy/helm/doops-agent` adoption。

```bash
bash scripts/release-managed-agent.sh \
  --target gw-edu-coder \
  --session agent-release-<tag> \
  --cluster doops-edu \
  --instance edu-coder \
  --release doops-agent-live \
  --namespace kz-ops \
  --kubeconfig /secure/path/to/kubeconfig \
  --proxy-allowed-hosts 127.0.0.1,internal.example.com \
  --host-alias internal.example.com=10.0.0.10 \
  --version <vX.Y.Z>
```

以下 `--proxy-allowed-hosts` 和 `--host-alias` 仅属于外部
`release-managed-agent.sh` 路径；`doops upgrade` 不支持这些配置变更。需要开放网站反代时，
通过 `--proxy-allowed-hosts` 显式传入逗号分隔的精确上游
hostname/IP；省略会保留现网值，传空字符串会恢复为拒绝全部。该参数控制 Agent
能访问的内网上游，不是公网访问域名。URL、端口、路径、通配符和空条目会在任何
集群变更前被拒绝；发布后会核对新 Agent 环境与 Helm release 的值。新集群省略
`proxy.allowedHosts` 时安全地默认拒绝全部。

当上游域名必须固定到指定地址时，重复传
`--host-alias <hostname>=<ip>`。该参数会合并到 live Helm
`hostAliases`，同名 hostname 会被显式替换，其他映射保持不变。域名或 IP
格式错误会在接触 target 前失败；发布后会核对 Pod 配置，并从新 Agent
内执行真实解析验证。省略该参数会完整保留现网映射，不存在 DNS fallback。

`doops upgrade` 执行自升级。`--dry-run` 只预览参数，不校验现网 Helm release、挂载、
权限或回滚可行性，不能作为发布就绪证据：

```bash
doops -session agent-upgrade upgrade \
  --target gw-edu-coder \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest> \
  --dry-run

doops -session agent-upgrade upgrade \
  --target gw-edu-coder \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest>
```

镜像必须是 `repository@sha256:<64 hex>`，包括 `--dry-run` 预演。
`--rollback` 回到前一个成功且已被替代的 revision。
`--target` 的 `cluster/instance` 就是升级范围。还没有自升级控制器的旧 Helm Agent 用
`scripts/release-managed-agent.sh`。
`PREVIEW` 不是已部署，`ACCEPTED` 不是已完成。Job 成功后还需核对 Gateway 实际服务
Pod 的 UID、imageID、版本与功能路径；`structuredSSH` 只是协议声明。协议探针失败时
CLI 尝试提交回滚 Job，但 target 不可达时不能保证回滚，必须明确报告并按恢复流程处理。
CLI 的 `OK` 仅说明 Job 与 `structuredSSH` 已核验，输出会提醒上述发布验收仍未完成。
Job 自动校验最终 Deployment 与 selector 匹配的非终止 Pod 的 UID、Ready、spec image
和 imageID；使用 digest 时必须与候选平台镜像 digest 精确匹配，不能把多架构索引 digest
当成已验证的平台 digest。回滚以历史 Helm revision 的 manifest 为期望镜像来源，并重新
检查 Gateway 协议；这些检查仍不替代 Gateway 实际服务 Pod 关联和功能验收。

## BuildKit Availability

Release images are built by CNB CI from the exact source commit. BuildKit in an
Agent is a runtime capability, not permission to build release images on a
business target or to make `push` a prerequisite for `ask --apply`.
Check availability without starting a build:

```bash
doops -session buildkit-check exec --target <target> --cmd 'buildctl --version'
```

Explicitly authorized long-running non-release commands use `exec --bg`, not
`&`, `nohup`, or `setsid` inside a foreground `exec`. The native task service owns
the process, logs and exit status; it does not grant deployment authority.
