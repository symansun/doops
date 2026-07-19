# Doops Usage Guide

This guide holds command-level usage details. The skill file is only the entry point.

## Command Choice

| Need | Command | Notes |
| :--- | :--- | :--- |
| List configured targets | `doops list` | Local config only; not live gateway state. |
| List live gateway agents | `doops targets --target <gateway-target>` | First check for gateway troubleshooting. |
| Add a Gateway target | `doops add --name <n> --gateway <url> --cluster <c> --instance <i>` | Stores routing only; never stores a target token. |
| Run deterministic shell | `doops -session <s> exec --target <t> --cmd '<cmd>'` | Best for known checks and deploy scripts. |
| Run a long build | `doops -session <s> exec --bg --target <t> --cmd '<cmd>'` | Agent-managed task with status polling; required when the command outlives a normal remote shell call. |
| Ask the edge agent to reason | `doops -session <s> ask --target <t> --msg '<task>'` | Best for diagnostics and multi-step investigation. |
| Run an authorized release | `doops -session <s> ask --target <t> --apply --credential <name> --msg '<facts>'` | Canonical business workload CI/CD entry. |
| Push a local directory | `doops -session <s> push --target <t> --src <dir>` | Syncs into `/root/ws/<session>`. |
| Pull a remote workspace | `doops -session <s> pull --target <t> --dest <dir>` | Use for large files and binary assets. |
| Read small text | `doops -session <s> read --target <t> --path <file>` | Small text only. |
| Write small text | `doops -session <s> write --target <t> --path <file> --file <local>` | Also supports `--content` and stdin. |
| Read system information | `doops -session <s> info --target <t>` | CPU, memory and disk metadata. |
| Use structured Kubernetes operations | `doops -session <s> k8s --target <t> ...` | Restricted operational interface; publishing uses `ask --apply`. |
| Clean workspace | `doops -session <s> clean --target <t>` | Cleans `/root/ws/<session>`. |
| Check an image deployment | `doops check ...` | Compares configured service image with build metadata. |
| Preview Agent upgrade | `doops -session <s> upgrade ... --dry-run` | Never replaces the current Agent. |
| Cache token | `doops login ...` | Gateway login can exchange username/password for user token. |
| List target credentials | `doops credential list --target <t>` | Uses the cached gateway login and returns only metadata granted to that user on the selected cluster/instance. |
| Preflight development credentials | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` | Fails closed unless every declared credentialRef has a matching active grant use on the selected target. |
| Manage credential lifecycle | `doops credential <create|put|grant|verify|promote|grant-revoke|revoke>` | See `CREDENTIAL_RESOURCES.md`. |
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

`doops list` reads local config. `doops targets` asks gateway for live connected agents. A target can be listed locally but offline in gateway.

`doops targets` separates target-wide busy from normal per-session activity:

- `busy=true` means the whole target is blocked by an exclusive operation or target queue, so new work may return `target busy`.
- `status=active` with `busy=false` means one or more sessions/resources are running, but other sessions can still use the same agent.
- `resources` shows locked resource keys such as `workspace:<session>` or `session:<session>`.

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

## Exec

```bash
doops -session ops exec --target jm --cmd 'hostname && df -h && free -h'
```

Use `exec` for deterministic commands, read-only checks, and known deployment scripts.

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
  --msg '在 <target> 的 namespace <namespace> 发布 deployment/<workload>，镜像 <repository>@sha256:<digest>；允许使用 deploy/helm/<name>/；原因：<reason>；验收：Secret 类型/key、rollout、实际镜像、Service/Endpoint、健康检查和关键功能路径通过。'
```

必须明确提供 target、服务范围、镜像版本、变更原因和验收条件。镜像版本可以是任务中的
不可变 digest，也可以由完整 chart values 与显式 `value_files` 收敛出的
`image.tag=release-YYYYMMDD`；后一模式禁止 digest 和任何 `set_strings`。需要 Helm 时还要
明确资产目录。`--credential` 可重复，但只传 resource name，不能内联 payload。

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
doops -session build clean --target jm
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
`--dry-run`. Apply the reviewed doops-agent image change from another isolated
Agent target or an external CI/GitOps controller so the old Agent remains
available until the replacement has registered and produced a heartbeat.

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
