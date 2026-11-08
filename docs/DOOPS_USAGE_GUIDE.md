# DoOps Usage Guide

This guide holds command-level usage details. The skill file is only the entry point.

## Command Choice

| Need | Command | Notes |
| :--- | :--- | :--- |
| List configured targets | `doops list` | Local config only; not live gateway state. |
| List live gateway agents | `doops -session <s> targets --target <gateway-target>` | HTTP control plane; session does not provide isolation. |
| Run deterministic shell | `doops -session <s> exec --target <t> --cmd '<cmd>'` | Best for known checks and deploy scripts. |
| Ask the edge agent to reason | `doops -session <s> ask --target <t> --msg '<scope and acceptance>'` | Delegate the authorized scope explicitly. |
| Push a local directory | `doops -session <s> push --target <t> --src <dir>` | Syncs into `/root/ws/<session>`. |
| Pull a remote workspace | `doops -session <s> pull --target <t> --dest <new-download-dir>` | Commits a remote snapshot; resets existing local Git directories. |
| Read small text | `doops -session <s> read --target <t> --path <file>` | Small text only. |
| Write small text | `doops -session <s> write --target <t> --path <file> --file <local>` | Also supports `--content` and stdin. |
| Clean workspace | `doops -session <s> clean --target <t>` | Cleans `/root/ws/<session>`. |
| Cache token | `doops -session <s> login ...` | Gateway login can exchange username/password for user token. |
| Issue token as admin | `doops -session <s> admin token create ...` | Requires an admin identity and private output capture as described below. |
| Remove cached token | `doops logout --target <t>` | Removes local token cache. |

## Basic Checks

```bash
doops list
doops -session smoke targets --target <gateway-target>
```

`doops list` reads local config. Select the target from that configuration before querying live state.
The global `-session` belongs before the subcommand. It isolates workspace operations, but HTTP
control-plane commands such as `targets` and `admin` do not use it for isolation.
A locally configured target can be offline or hidden from the current user.

`doops targets` separates target-wide busy from normal per-session activity:

- `busy=true` means the whole target is blocked by an exclusive operation or target queue, so new work may return `target busy`.
- `status=active` with `busy=false` means one or more sessions/resources are running, but other sessions can still use the same agent.
- `resources` shows locked resource keys such as `workspace:<session>` or `session:<session>`.

If gateway returns `user operation limit exceeded`, list active gateway operations and cancel stale ones with an admin token:

```bash
doops -session inspect admin operations list --target <gateway-target>
doops -session inspect admin operations cancel --target <gateway-target> --id <operation-id>
```

Only cancel the identified operation within the user's authorized scope. On upgraded agents, cancel
terminates local shell-style commands by process group. For `ask`, treat cancel as stopping the doops
waiting path first; verify the remote session if doagent already started work.

## Gateway Admin Tokens

签发前确认 Gateway、用户、有效期和授权的本地私密交付目录。不要 SSH 修改 Gateway DB。
当前 CLI 会向 stdout 打印完整 token；stdout 和 stderr 必须直接写入私密文件，
不能返回工具输出或读回对话。以下变量是已确认的非敏感输入；示例不包含真实凭据：

```bash
set -eu
umask 077
: "${TOKEN_DELIVERY_DIR:?set an approved private parent directory}"
: "${DOOPS_TARGET:?set the configured gateway target}"
: "${TOKEN_USER:?set the approved recipient}"
: "${TOKEN_TTL:?set the approved expiry}"
delivery_dir=$(mktemp -d "${TOKEN_DELIVERY_DIR%/}/doops-token.XXXXXX")
chmod 700 "$delivery_dir"
if doops -session token-issue admin token create \
  --target "$DOOPS_TARGET" --user "$TOKEN_USER" --name maintenance \
  --expires "$TOKEN_TTL" >"$delivery_dir/issued.txt" 2>&1; then
  chmod 600 "$delivery_dir/issued.txt"
  printf 'Token delivery: %s\n' "$delivery_dir/issued.txt"
else
  printf 'Issuance failed; private diagnostic: %s\n' "$delivery_dir/issued.txt" >&2
  exit 1
fi
```

`--target` 当前身份必须有 `admin` 权限。`--save-as <target-name>` 会覆盖该本地缓存，
仍打印完整 token；只有缓存替换也获授权才附加，并继续捕获输出。
没有私密交付方式时不签发。失败或断连不盲目重试，先核对是否已签发。

## Exec

当前 `exec/ask` 成功后会检查远端 `/root/ws/<session>/deploy.sh`，满足脚本检测时直接写入
本地 `./deploy.sh`，没有关闭或禁止覆盖选项。因此即使远端只读，也要从本任务专用的新目录调用：

```bash
(
  local_task_dir=$(mktemp -d "${TMPDIR:-/tmp}/doops-call.XXXXXX") || exit 1
  cd "$local_task_dir" || exit 1
  printf 'Local output directory: %s\n' "$local_task_dir"
  doops -session ops exec --target jm --cmd 'hostname && df -h && free -h'
)
```

Use `exec` for deterministic commands. Keep any returned script for review; do not automatically execute,
commit or copy it over the project's script. This containment does not change the CLI's overwrite behavior.

## Ask

```bash
(
  local_task_dir=$(mktemp -d "${TMPDIR:-/tmp}/doops-call.XXXXXX") || exit 1
  cd "$local_task_dir" || exit 1
  printf 'Local output directory: %s\n' "$local_task_dir"
  doops -session ops ask --target jm --msg '只读检查当前 target 的 Kubernetes 节点状态；禁止修改、重启、部署和清理；返回对象、证据、结论和未知项'
)
```

The subshell isolates any returned script as described above. The target must have doagent/model configuration.
The current Agent uses automatic tool approval; a natural-language restriction is model guidance, not
an enforced read-only sandbox. Supply target, objects, allowed actions, reason and acceptance criteria.
Reuse existing authorization; return newly discovered impacts to the caller instead of expanding scope.
The Agent also appends a script-generation hint after successful build/deploy actions; review returned
scripts and preserve existing files. Neither the model summary nor `Operation complete.` proves success:
read back actual workload state, output artifacts and health. Unknown outcomes must not trigger automatic replay.

## Push

```bash
doops -session build push --target jm --src .
```

`push` uses Git workspace sync in both direct and gateway modes:

- Direct agent: `/git/<session>.git`
- Gateway: `/v1/git/<cluster>/<instance>/<session>.git`

Do not reimplement push with tar/base64/chunk upload. `doops_workspace_*` chunk tools are low-level compatibility JSON-RPC tools, not the standard CLI/deploy path; seeing 512KB chunk logs means a script is bypassing the supported Git HTTP path. Push respects `.gitignore` and `.doopsignore`; do not hide deploy manifests or scripts behind ignore rules unless intentional.

## Pull

```bash
doops -session build pull --target jm --dest ./doops-build-output
```

Use `pull` for large files, archives, SQL dumps, images, videos, and generated assets. Do not use `read` or `exec base64` for downloads.

`pull` writes a Git snapshot on the remote session before downloading. Use a new dedicated destination:
if it already contains `.git`, the CLI fetches then runs `reset --hard FETCH_HEAD`, discarding tracked
local edits. Reuse requires checking ownership and preserved changes; never point it at a business repo.

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
doops -session login login --target jm --gateway https://gateway.example.com --username alice
doops logout --target jm
```

`auth.json` only affects local CLI token selection. Gateway permissions still come from gateway user scopes.
Use a private interactive terminal for the password prompt. This version's `add --token` and
`login --token` take secrets as arguments and do not implement `--token-stdin`; do not expand real
secrets into an agent tool command. If the required input cannot be delivered privately, report that
limitation before creating or replacing configuration.

## Upgrade

```bash
doops -session upgrade_demo upgrade \
  --target prod \
  --cluster cluster-c \
  --instance node-3 \
  --image <your-registry>/doops:<release> \
  --mode k8s \
  --namespace doops-system \
  --workload deployment/doops-agent \
  --container doops-agent \
  --dry-run
```

Remove `--dry-run` only after reviewing the command. Gateway upgrade requires `agent:upgrade` or `admin`.

## BuildKit Example

```bash
(
  local_task_dir=$(mktemp -d "${TMPDIR:-/tmp}/doops-call.XXXXXX") || exit 1
  cd "$local_task_dir" || exit 1
  printf 'Local output directory: %s\n' "$local_task_dir"
  doops -session build exec --target jm --cmd \
    'cd /root/ws/build && buildctl --addr unix:///run/buildkit/buildkitd.sock build \
     --progress=plain \
     --frontend dockerfile.v0 \
     --local context=. \
     --local dockerfile=. \
     --opt filename=Dockerfile \
     --output type=oci,dest=/root/ws/build/image.tar'
)
```

This example only builds an OCI archive, with the local directory isolation described under Exec.
Verify exit status and the archive's structure/digest. A registry push or deployment requires its
own authorized destination and verification. Use the runtime's exposed task tracker for long builds,
record its task ID and wait for final status/exit code; a log tail is progress only. This CLI does not
expose an `exec --bg` flag, so do not invent one. Do not clean a context still used by a background job.
