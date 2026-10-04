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
| Run nested SSH safely | `doops -session <s> ssh --target <t> --host <user@host> -- <command> [args...]` | Preserves remote argv across nested shells. |
| Run a long build | `doops -session <s> exec --bg --target <t> --cmd '<cmd>'` | One RPC waits for the Agent task's terminal result; no task-status polling or command retries. |
| Ask the edge agent to reason | `doops -session <s> ask --target <t> --msg '<task>'` | Best for diagnostics and multi-step investigation. |
| Run an authorized release | `doops -session <s> ask --target <t> --apply --credential <name> --msg '<facts>'` | Canonical business workload CI/CD entry. |
| Push a local directory | `doops -session <s> push --target <t> --src <dir>` | Syncs into `/root/ws/<session>`. |
| Pull a remote workspace | `doops -session <s> pull --target <t> --dest <dir>` | Use for large files and binary assets. |
| Read small text | `doops -session <s> read --target <t> --path <file>` | Small text only. |
| Write small text | `doops -session <s> write --target <t> --path <file> --file <local>` | Also supports `--content` and stdin. |
| Read system information | `doops -session <s> info --target <t>` | CPU, memory and disk metadata. |
| Use structured Kubernetes operations | `doops -session <s> k8s --target <t> ...` | Restricted operational interface; publishing uses `ask --apply`. |
| Clean workspace | `doops -session <s> clean --target <t> --workspace <s>` | Cleans `/root/ws/<workspace>`. |
| Check an image deployment | `doops check ...` | Compares configured service image with build metadata. |
| Upgrade a managed Agent | `doops -session <s> upgrade --target <t> --image <repository>@sha256:<digest>` | In-cluster Job. `--dry-run` previews. |
| Cache token | `doops login ...` | Gateway login can exchange username/password for user token. |
| List target credentials | `doops credential list --target <t>` | Uses the cached gateway login and returns only metadata granted to that user on the selected cluster/instance. |
| Preflight development credentials | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` | Fails closed unless every declared credentialRef has a matching active grant use on the selected target. |
| Manage credential lifecycle | `doops credential <create|put|grant|verify|promote|grant-revoke|revoke>` | See `CREDENTIAL_RESOURCES.md`. |
| Create Gateway user and API key | `doops admin user create --target <t> --user <u> (--password-stdin\|--generate-password)` | Removes bootstrap wildcard access and applies only explicit `--grant-target` scopes. |
| List Gateway users | `doops admin user list --target <t>` | Returns metadata only. |
| Manage target grants | `doops admin grant <list\|add\|delete> ...` | Grants use exact configured `cluster/instance` routes. |
| Issue token as admin | `doops admin token create ...` | Requires a gateway token with `admin` permission. |
| Inspect/cancel active operations | `doops admin operations <list|cancel|reconcile> ...` | Cancel requests termination; reconcile records human-reviewed stopped state. |
| Manage scheduled issue jobs | `doops admin jobs <list|add|run|enable|disable|rm|issues> ...` | See `DOOPS_SCHEDULED_ISSUES.md`. |
| Reconnect an idle target | `doops unlock --target <t>` | Refuses active, queued or unresolved work; does not force-release operations. |
| Remove cached token | `doops logout --target <t>` | Removes local token cache. |

## Basic Checks

```bash
doops list
doops version
doops -session smoke targets --target <gateway-target>
doops -session smoke exec --target <gateway-target> --cmd 'hostname && date -u +%FT%TZ'
doops -session smoke-capabilities capabilities --target <gateway-target> --require structuredSSH
```

`doops version` 的 `release` 是唯一 DoOps 发布版本，`commit` 是该版本对应的完整
源码 provenance；`doops targets` 的 `RELEASE_COMMIT` 必须与它一致。输出中的
`cli-contract/v3` 只通过 `doops --contract` 检查协议兼容性，不是另一个发布版本。
Agent 未提供完整 `RELEASE_COMMIT` 时只能视为旧运行时，升级验收不会通过。

`doops list` reads local config. `doops targets` asks gateway for live connected agents. A target can be listed locally but unavailable in gateway because it is offline or because the logged-in user lacks the exact target grant; the CLI does not collapse these cases into a definitive offline diagnosis.

To remove a stale local route, use `doops -session <s> remove --target <gateway-target>`. The command first queries the configured Gateway and refuses to modify the config if the Gateway is unreachable, authentication fails, or the exact route is currently visible. It does not delete Gateway grants, audit records, proxy routes, or historical agent status.

`doops targets` separates target-wide busy from normal per-session activity:

- `busy=true` means the whole target is blocked by an exclusive operation or target queue; when target
  queueing is enabled, conflicting mutations wait for the current workspace/resource operation
  instead of consuming another execution slot.
- `status=active` with `busy=false` means one or more sessions/resources are running, but other sessions can still use the same agent.
- `resources` shows workspace locks such as `workspace:<session>`.
  Shell/SSH/bg and apply use a target mutation lock; a session is not a sandbox.
- Public HTTP proxy requests are classified by method: `GET/HEAD/OPTIONS/TRACE` are read-only
  queries; other methods use the target mutation lock and retain `outcome_unknown` protection
  when the Agent disconnects before a terminal response.

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

Cancel returns HTTP `202` with `cancel_requested=true`, meaning the request was
registered, not that the operation stopped. The operation keeps ownership until
the original terminal response. Shell-style commands join process-group
termination; `ask` waits for the matching doagent turn terminal event. Missing
terminal evidence persists `outcome_unknown` and blocks later target mutations.
This includes interrupted workspace writes, cleanup and clone/commit operations
without a terminal reply. A confirmed local completion after cancellation is
not an unknown outcome. Read-only query failures do not quarantine the target.
Agent progress notifications carry `params.request_id`; Gateway and CLI route them by
request ID so concurrent calls in one session cannot overwrite one another. The Gateway
persists the execution terminal state before releasing the resource lease and retries
that persistence while retaining the lease if the store is temporarily unavailable.
Interrupted Shell/SSH/bg, apply and Kubernetes mutations also require reconciliation:
local process or turn termination does not fence remote or detached work. The guard
survives Gateway restart or Agent reconnection. The operation list shows
`waiting`, `running`, `cancel_requested`, `outcome_unknown`, `audit_id`, `resource`
and `blocked_by`. Waiting for a resource does not consume a user execution slot.
Independent structured workspace operations run concurrently; arbitrary Shell does
not. Structured Kubernetes queries and task status remain available during a
mutation or unknown outcome. Agent upgrades remain exclusive. Different routes to
one release/database are not coordinated by this target-local lock; do not bypass it
using another target. There is no safe force-preemption or automatic replay protocol.

An SSH failure with an Agent structured terminal result is an ordinary failed
operation when it has a remote exit code in `1..254`, or when the structured
tool error has no exit code. Exit code `255`, signal termination, cancellation,
disconnection and missing structured terminal evidence remain guarded. Command
text never determines whether an SSH operation is safe to preempt.

After an administrator independently confirms the original execution has stopped,
record the reason and evidence explicitly:

```bash
doops -session reconcile-check admin operations reconcile \
  --target <gateway-target> --id audit-N --confirm-stopped \
  --reason '<reason>' --evidence '<human-reviewed evidence>'
```

This records `resolved_manually`, not machine-verified success, takeover or replay.
`unlock` reconnects only an idle target and refuses active, queued or unresolved
operations; it does not clear these guards.

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

必须明确提供 target、服务范围、镜像版本、变更原因和验收条件。镜像版本必须是任务中的
不可变 digest 或严格的 `release-YYYYMMDD` tag。需要 Helm 时还要明确远程
`https://`/`oci://` chart；可选 values 只能是
HTTP(S) URL，`set_strings` 只允许 `image.repository` 加一个受控版本选择器：
不可变的 `image.digest` 或严格的 `image.tag=release-YYYYMMDD`，两种模式不得混用。
`--credential` 可重复，但只传 resource name，不能内联 payload。
若项目使用完整 Chart 值而不是镜像覆盖，任务应明确要求同时省略 `value_files` 与
`set_strings`，以已发布的 HTTPS Chart 内唯一 `values.yaml` 为权威源。其 `image.tag`
必须为合法 `release-YYYYMMDD`，不存在历史值合并或 digest 覆盖。CNB 受管 Helm 凭据使用
官方 Helm 仓库 HTTPS `.tgz` 下载接口；验证器读取实际授权包而非仓库 `index.yaml`，
发布器按 session/namespace/release/chart 与物化版本校验后消费，认证失败关闭。
发布者必须在调用前独立核对精确 CI source commit、registry digest 和构建成功证据；
`ask --apply`、`--image`、`--version` 和 Helm 工具不自动验证 CI provenance。

CNB CI 负责测试、构建和发布制品；Gateway 负责鉴权、审计、并发控制和凭据预物化；
目标 Agent/doagent 在目标运行环境执行业务 mutation。当前没有 EnvironmentRegistry、
`cicd run`、workflow 环境推断、CICDReport 或 `doops_cicd_result`。

普通前台 `exec` 可以执行操作者明确给出的 Helm 命令，但不替代标准发布编排，也不能使用
managed credential references。`doops-agent` 自身升级使用 `doops upgrade`，但仅适用于
Agent 自身 namespace 中的标准 Helm-managed Deployment 和已有匹配 Helm release；未给
`--release` 时必须只有一个匹配 release，给出时按名称选择并校验。Agent Pod 必须挂载
可由 Job 共享的 `/root/.kube` 或 `/root/.kube/config`，Job 的 Kubernetes 权限来自该
kubeconfig，不需要操作机 kubeconfig。没有这些前置条件时才使用外部
`scripts/release-managed-agent.sh`。

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
doops -session ops read --target jm --path /root/ws/ops/deploy.sh
doops -session ops write --target jm --path /root/ws/ops/notes.txt --content 'hello'
doops -session ops write --target jm --path /root/ws/ops/pod.yaml --file ./pod.yaml
cat ./pod.yaml | doops -session ops write --target jm --path /root/ws/ops/pod.yaml
```

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

默认 `target` 视图只返回当前用户在所选 target 上获得 active grant 且存在 active version
的 active resource；
`managed` 视图返回本人拥有的 personal resource，platform admin 可以管理平台资源。
两个视图都不返回 payload。

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

# 3. 在相同 scope 验证 staged version
doops -session credential-verify-<version> credential verify \
  --target gw-edu-coder \
  --id <credential-id> --version-id <version-id> \
  --cluster doops-edu --instance edu-coder \
  --namespace apps --use imagePull \
  --workload-kind Deployment --workload-name oilan \
  --image-repository registry.example.com/oilan \
  --image-digest sha256:<manifest-digest>

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

## Upgrade

CLI 一键安装或更新：

```bash
bash scripts/install.sh --version <vX.Y.Z> --project "$PWD"
```

新集群首次安装、旧 Helm Agent 首次启用控制器与显式 Agent 配置变更使用本仓库
`scripts/release-managed-agent.sh`，不能重跑外部清单安装器替代 Helm 升级。
首次安装完整命令与验收见 `CLUSTER_INSTALL_GUIDE.md`；这条 bootstrap 路径与普通
业务 `ask --apply` 分开。外部控制器可以位于授权管理主机或 CI，不必在开发机。

已经改用 Helm chart `deploy/helm/doops-agent`、并且 Agent 镜像包含自升级控制器的节点，
用 `doops upgrade`，不需要本机 kubeconfig。普通业务仍使用 `doops ask --apply`。
发布者须先通过只读检查确认现网采用标准 `/app/sandbox-entrypoint.sh`；
CLI 的参数预览不替代这项检查。Agent 进程不执行这次 Helm mutation；
集群内 Job 复用 Agent 已挂载的 kubeconfig。Chart 先起新 Pod，Gateway
切换会话后才删旧副本；新 Pod 不持续就绪或 `structuredSSH` 探针失败会回滚。
`doops upgrade` 只接受 Agent 镜像和 rollback 操作，不修改 `proxy.allowedHosts`、
`hostAliases` 或其它 Agent Helm 配置。

还没有该控制器的旧 Agent，以及新集群第一次接入，仍用
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
`--target` 的 `cluster/instance` 就是升级范围。还没有自升级控制器的旧 Agent 才用
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
