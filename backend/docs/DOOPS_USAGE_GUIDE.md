# Doops Usage Guide

This guide holds command-level usage details. The skill file is only the entry point.

## Command Choice

| Need | Command | Notes |
| :--- | :--- | :--- |
| List configured targets | `doops list` | Local config only; not live gateway state. |
| List live gateway agents | `doops targets --target <gateway-target>` | First check for gateway troubleshooting. |
| Add a Gateway target | `doops add --name <n> --gateway <url> --cluster <c> --instance <i>` | Stores routing only; never stores a target token. |
| Run deterministic shell | `doops -session <s> exec --target <t> --cmd '<cmd>'` | Best for known checks and deploy scripts. |
| Run nested SSH safely | `doops -session <s> ssh --target <t> --host <user@host> -- <command> [args...]` | Preserves remote argv across nested shells. |
| Run a long build | `doops -session <s> exec --bg --target <t> --cmd '<cmd>'` | Agent-managed task with status polling; required when the command outlives a normal remote shell call. |
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
| Preview Agent upgrade | `doops -session <s> upgrade ... --dry-run` | Never replaces the current Agent. |
| Cache token | `doops login ...` | Gateway login can exchange username/password for user token. |
| List target credentials | `doops credential list --target <t>` | Uses the cached gateway login and returns only metadata granted to that user on the selected cluster/instance. |
| Preflight development credentials | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` | Fails closed unless every declared credentialRef has a matching active grant use on the selected target. |
| Manage credential lifecycle | `doops credential <create|put|grant|verify|promote|grant-revoke|revoke>` | See `CREDENTIAL_RESOURCES.md`. |
| Create Gateway user and API key | `doops admin user create --target <t> --user <u> (--password-stdin\|--generate-password)` | Removes bootstrap wildcard access and applies only explicit `--grant-target` scopes. |
| List Gateway users | `doops admin user list --target <t>` | Returns metadata only. |
| Manage target grants | `doops admin grant <list\|add\|delete> ...` | Grants use exact configured `cluster/instance` routes. |
| Issue token as admin | `doops admin token create ...` | Requires a gateway token with `admin` permission. |
| Inspect/cancel active operations | `doops admin operations <list|cancel> ...` | Gateway administration. |
| Manage scheduled issue jobs | `doops admin jobs <list|add|run|enable|disable|rm|issues> ...` | See `DOOPS_SCHEDULED_ISSUES.md`. |
| Force-release a target lock | `doops unlock --target <t>` | Admin recovery only; inspect operations first. |
| Remove cached token | `doops logout --target <t>` | Removes local token cache. |

## Basic Checks

```bash
doops list
doops targets --target unicom
doops -session smoke exec --target unicom --cmd 'hostname && date -u +%FT%TZ'
```

`doops list` reads local config. `doops targets` asks gateway for live connected agents. A target can be listed locally but unavailable in gateway because it is offline or because the logged-in user lacks the exact target grant; the CLI does not collapse these cases into a definitive offline diagnosis.

`doops targets` separates target-wide busy from normal per-session activity:

- `busy=true` means the whole target is blocked by an exclusive operation or target queue, so new work may return `target busy`.
- `status=active` with `busy=false` means one or more sessions/resources are running, but other sessions can still use the same agent.
- `resources` shows locked resource keys such as `workspace:<session>` or `session:<session>`.

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
doops admin operations list --target <gateway-target>
doops admin operations cancel --target <gateway-target> --id <operation-id>
```

On upgraded agents, cancel terminates local shell-style commands by process group. For `ask`, treat cancel as stopping the doops waiting path first; verify the remote session if doagent already started work.

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

`--option` 也接受完整的 OpenSSH argv（例如 `--option=-oBatchMode=yes`）；`key=value`
简写会在 CLI 内明确转换为 `-o<key>=<value>`，不会把它当成主机或远端命令。

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
  --msg '在 <target> 的 namespace <namespace> 发布 deployment/<workload>，镜像 <repository>@sha256:<digest>；使用 chart https://charts.example/<name>.tgz；原因：<reason>；验收：Secret 类型/key、rollout、最终 Deployment spec/status、实际镜像、Service/Endpoint、健康检查和关键功能路径通过。'
```

必须明确提供 target、服务范围、镜像版本、变更原因和验收条件。镜像版本必须是任务中的
不可变 digest 或严格的 `release-YYYYMMDD` tag。需要 Helm 时还要明确远程
`https://`/`oci://` chart；可选 values 只能是
HTTP(S) URL，`set_strings` 只允许 `image.repository` 加一个受控版本选择器：
不可变的 `image.digest` 或严格的 `image.tag=release-YYYYMMDD`，两种模式不得混用。
`--credential` 可重复，但只传 resource name，不能内联 payload。

CNB CI 负责测试、构建和发布制品；Gateway 负责鉴权、审计、并发控制和凭据预物化；
目标 Agent/doagent 在目标运行环境执行业务 mutation。当前没有 EnvironmentRegistry、
`cicd run`、workflow 环境推断、CICDReport 或 `doops_cicd_result`。

普通前台 `exec` 可以执行操作者明确给出的 Helm 命令，但不替代标准发布编排，也不能使用
managed credential references。`doops-agent` 自身升级必须由另一个隔离 Agent target 或
真实存在的外部 CI/GitOps controller 执行。

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

目标集群上安装或更新 Agent 是同一条命令，必须传入 cluster / instance：

```bash
curl -fsSL https://cnb.cool/l8ai/dowork/-/git/raw/main/doops-k8s/install.sh | sh -s -- <cluster> <instance>
```

当前基线 v1.3.0。重跑会把 `doops-k8s/` reset 到 `main` 再 apply 这一个身份。

已经改用 Helm chart `deploy/helm/doops-agent` 的维护者节点才走
`scripts/release-managed-agent.sh`。现网必须已经是 `/app/sandbox-entrypoint.sh`，
已有集群必须提供操作机 kubeconfig；Helm mutation 由操作机执行，不能通过当前 Agent
执行。Chart 先起新 Pod，Gateway 切换会话后才删旧副本；候选 Pod 或 Gateway 接管失败
会自动回滚到发布前 Helm revision：

脚本会先验证 Kubernetes API 和 kubeconfig 可达性，再读取 Helm release。API 超时、
EOF、鉴权或其它连接失败会直接报告控制器不可达，不会误报为“release 不存在”或引导
执行 adopt；只有 API 可达且 Helm 明确返回 release 缺失时，才需要先完成
`deploy/helm/doops-agent` 接管。

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

需要开放网站反代时，通过 `--proxy-allowed-hosts` 显式传入逗号分隔的精确上游
hostname/IP；省略会保留现网值，传空字符串会恢复为拒绝全部。该参数控制 Agent
能访问的内网上游，不是公网访问域名。URL、端口、路径、通配符和空条目会在任何
集群变更前被拒绝；发布后会核对新 Agent 环境与 Helm release 的值。新集群省略
`proxy.allowedHosts` 时安全地默认拒绝全部。

当上游域名必须固定到指定地址时，重复传
`--host-alias <hostname>=<ip>`。该参数会合并到 live Helm
`hostAliases`，同名 hostname 会被显式替换，其他映射保持不变。域名或 IP
格式错误会在接触 target 前失败；发布后会核对 Pod 配置，并从新 Agent
内执行真实解析验证。省略该参数会完整保留现网映射，不存在 DNS fallback。

`doops upgrade` 只做预演：

```bash
doops -session upgrade_20260519 upgrade \
  --target unicom \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest-from-successful-CI> \
  --mode k8s \
  --namespace doops-system \
  --workload deployment/doops-agent \
  --container doops-agent \
  --dry-run
```

When `--target` is given, its configured `cluster/instance` is the default
preview scope. Direct Agent self-upgrade is prohibited: `upgrade` requires
`--dry-run`. Apply the reviewed image with `scripts/release-managed-agent.sh`.

## BuildKit Example

```bash
doops -session build exec --target jm --cmd \
  'cd /root/ws/build && buildctl --addr unix:///run/buildkit/buildkitd.sock build \
   --progress=plain \
   --frontend dockerfile.v0 \
   --local context=. \
   --local dockerfile=. \
   --opt filename=Dockerfile \
   --output type=image,name=repo.example.com/team/app:latest,push=true'
```

For long builds, use the native task service:

```bash
doops -session build exec --bg --target jm --cmd \
  'cd /root/ws/build && buildctl --addr unix:///run/buildkit/buildkitd.sock build ...'
```

Do not create a shell background process with `&`, `nohup`, or `setsid` through a normal `exec` call: the agent terminates child processes when that call ends. `--bg` submits `doops_bg` and polls `doops_task_status`, so the agent owns the process, logs, and exit status.
