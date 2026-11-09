<p align="right"><a href="README.md">中文</a> | <b>English</b> | <a href="README.ru.md">Русский</a></p>

# DoOps

<p align="center">
  <img src="docs/images/slogan-en.svg" alt="Manage any machine — no SSH required." width="760">
</p>

> This project is open source under the [MIT License](LICENSE).

**Core contributors: Syman, 高家祺 (Gao Jiaqi), 宋晨宇 (Song Chenyu), 王振普 (Wang Zhenpu).** See [Contributors](#contributors) for the exact attribution order.

**Still paying the price for SSH ports, jump hosts, key distribution, NAT, and private-network isolation?** `doops` removes all of that:

- **One command straight to any private-network machine**: the agent dials out to the public gateway over a long-lived connection — no public IP, VPN, jump host, or bastion required
- **Zero SSH asset burden**: no more maintaining SSH users, ports, passwords, and key inventories; reconnects and network isolation are no longer obstacles
- **Built for agents and automation**: the CLI, the console, and multi-domain skills share a single auditable execution path, so AI can safely troubleshoot, inspect, and deploy instead of relying on manual copy-paste
- **Secure by default**: unified authentication, per-target/action authorization, end-to-end audit logging, and high-risk operations denied by default

Troubleshooting, inspection, deployment, file sync — every remote operation goes through one entry point, every step is recorded, and every execution can be reviewed.

## UI Preview

![DoOps home page](docs/images/doops-home.jpg)

![DoOps Console overview](docs/images/doops-console.png)

DoOps is a unified entry point: it combines agents, a console, a CLI, and multi-domain skills into one ops toolkit covering servers, Kubernetes, websites, databases, files, and inspection workflows — all under the same authentication, authorization, and audit stack for day-to-day operations.

## Repository Layout

| Path | Description |
| :--- | :--- |
| `agent/` | Source of the edge executor `doops-agent` (Go) |
| `gateway/` | Source of the public control plane `doops-gateway` (Go) |
| `skills/` | doops CLI source, prebuilt binaries, and ops skills |
| `scripts/` | Install, build, and release scripts |
| `docs/` | Protocol, API, deployment, and operations docs |
| `test/` | Test and acceptance cases |
| `Dockerfile*` | Build entry points for the base image and the light-update image |

This repo neither contains nor publishes the `do-agent` binary; to run `doops ask` you must provide an image that includes `do-agent` yourself (see "Base image" below).

## Architecture

<p align="center">
  <img src="docs/images/architecture-en.svg" alt="DoOps architecture diagram" width="1100">
</p>

The system has two orthogonal dimensions:

- Connection mode: the CLI/skill connects to the gateway; private-network agents register themselves with the gateway
- Execution mode: the fast path `exec/read/write/push/pull/info/check/clean`, and the slow path `ask` / follow-up `chat`

Component roles:

- Client: `doops` CLI + doops skill
- Public control plane: `gateway/` builds `doops-gateway`, handling RBAC, audit, queueing, and routing to private-network agents
- Edge executor: `agent/` builds `doops-agent`, handling exec, push/pull, files, and AI tasks on target machines
- Runtime security boundary: `doops-agent` starts only `doops-agent`, `do-agent`, and `buildkitd` by default; SSH and WebIDE are not started by default
- Connectivity: the CLI/skill reaches the gateway on `42222`; the gateway reaches private-network agents through the already-established agent WebSocket
- Authentication: users connect to the gateway with a gateway user token; agents register with the gateway without a token — they only need `gateway-url`, `cluster`, and `instance`
- Code sync: the gateway uses Git HTTP; it only relays Git requests through the reverse tunnel of the established agent WebSocket, and no longer uses tar chunk uploads as the `push/pull` path
- Image builds: standardized on `buildctl + buildkitd`

## Target Naming Convention

`cluster/instance` is the gateway's routing key; the names are defined by your own deployment. Examples:

- `prod / master-1`
- `staging / worker-1`
- `edge / gpu-node-1`

Only one agent should be online for a given `cluster/instance` at any time; a duplicate registration replaces the old connection.

## Protocol and Endpoints

- CLI to gateway: `WS /v1/rpc?cluster=<cluster>&instance=<instance>`
- Private-network agent to gateway: `WS /v1/agent/connect?cluster=<cluster>&instance=<instance>`
- Code sync: `Git HTTP /v1/git/<cluster>/<instance>/<session>.git`
- Health check: `GET /health`
- The OpenAI-compatible model endpoint is configured via `API_BASE_URL` (default example: `https://llm.example.com/v1`)

## Base Image and Build

The agent image has two layers:

| Image | Dockerfile | Contents | Change frequency |
| :--- | :--- | :--- | :--- |
| Base image | `Dockerfile.base` / `Dockerfile.base.light` | sandbox, do-agent, BuildKit, system tools | Low |
| Light-update image | `Dockerfile` / `agent/Dockerfile` | `doops-agent`, skills, docs, entrypoint | High |

Required before building:

- `DO_AGENT_IMAGE`: an image containing `/usr/local/bin/do-agent`. This repo does not ship `do-agent`; provide your own.
- `DOOPS_AGENT_BASE_IMAGE`: the base image tag, default `doops-base:latest`.

```bash
# 1. 构建基础镜像（推荐 base-light，纯净 agent 运行时）
docker build -f Dockerfile.base.light \
  --build-arg DO_AGENT_IMAGE=<your-doagent-image> \
  -t doops-base:latest .

# 2. 构建轻更新镜像
docker build \
  --build-arg DOOPS_AGENT_BASE_IMAGE=doops-base:latest \
  -t <your-registry>/doops:dev .
```

Day-to-day upgrades only require replacing the light-update image; rebuild the base image only when the sandbox, do-agent, BuildKit, or system packages change.

## Gateway Source Entry Point

`doops-gateway` is a first-class component; its source lives at the top level of the repo:

```bash
bash scripts/build-gateway.sh
./bin/doops-gateway serve -db /var/lib/doops-gateway/gateway.db -port 42222
```

The compatibility entry `agent/cmd/gateway` is still kept, but only as a wrapper for the legacy build path; new docs and new deployments should use `gateway/`.

See [backend/docs/DOOPS_API_GUIDE.md](backend/docs/DOOPS_API_GUIDE.md) for the API guide aimed at external systems, covering login, target queries, WebSocket JSON-RPC, natural-language deployment, progress streaming, and audit queries.

## Verifying a Deployment

After deploying, verify the chain in this order:

```bash
doops list
doops targets --target <gateway-target>
doops -session smoke exec --target <gateway-target> --cmd 'hostname'
doops -session smoke push --target <gateway-target> --src <small-dir>
doops -session smoke ask --target <gateway-target> --msg '<read-only task>'
```

Only when `targets` shows the `cluster/instance` online has the agent actually registered with the gateway; if `ask` fails, first check whether `settings.json` in `doagent-config` has a valid API Key and `API_BASE_URL` configured.

## Fixed Paths

- CLI: `~/.local/bin/doops`
- Authoritative skill source: `skills/SKILL.md`
- Skill install target: `<project>/.agent/skills/doops/SKILL.md`
- The CLI's only default config: `~/.agent/skills/doops/config.json`
- CLI token cache: `~/.agent/skills/doops/auth.json`
- Prebuilt CLI: `skills/doops-cli/bin/doops-<os>-<arch>`
- Agent local token: `/root/.doops/agent-token` on the server side; only for legacy direct connections or local `/ws` protection, not for gateway registration

`~/.config/doops/config.json`, `~/.doops/auth.json`, and a project's `.doops/` are legacy paths or runtime leftovers and must not be treated as current configuration. `DOOPS_CONFIG=/path/to/config.json` is only for testing and emergency explicit overrides.

## Read This First: The Installation Model

Two things are easy to confuse in DoOps, so the docs separate them up front:

1. **Agent deployment form**: how `doops-agent` runs on the target machine.
2. **Connection mode**: how the CLI/skill connects to that agent.

### Agent Deployment Forms

| Form | How to identify | When to use | Containerized |
| :--- | :--- | :--- | :--- |
| K8s/DaemonSet | `kubectl get pod -A -l app=doops-agent` shows a Pod | K8s/K3s cluster nodes; recommended standard install | Yes |
| Docker/nerdctl container | `docker ps` or `nerdctl ps` shows a container named `doops-agent` | Single machines or nodes without DaemonSet; recommended standard install | Yes |
| Bare binary/systemd | `ps -ef | grep doops-agent` shows `/usr/local/bin/doops-agent -port ...`, but no Pod/container | Temporary bootstrap, legacy node compatibility | No |

For example, if you see this on a machine:

```text
/usr/local/bin/doops-agent -port 42222 ...
```

while `kubectl get pod -A -l app=doops-agent` shows no Pod and `docker ps` shows no `doops-agent` container, then it is a **bare-binary direct install**, not a K8s/K3s image install. A bare binary does not automatically get the doagent, BuildKit, skills, and self-healing capabilities bundled in the container image; for production, upgrade to K8s/DaemonSet or container mode.

### Gateway Connection

A standard target only configures the gateway:

| Where the CLI/skill connects | How the target agent must run |
| :--- | :--- |
| `ws(s)://<gateway>/v1/rpc` | The private-network agent actively connects to the public gateway with `-gateway-url ... -cluster ... -instance ...` |

Whether a node "can be operated on" depends on whether its agent has actively registered with the gateway. Only when `doops targets --target <gateway-target>` shows the corresponding `cluster/instance` online will subsequent `exec/ask/push/pull` succeed. If a node is just a local bare-binary install without `-gateway-url`, it will not automatically appear in the gateway.

## Client Installation and Connection

### 1. Install the CLI and the Skill

The authoritative source of the doops skill is `skills/SKILL.md` in this repo. Do not hand-write or reverse-maintain the skill in user projects; the install script only copies `skills/SKILL.md` from this repo into the business project's `.agent/skills/doops/SKILL.md`.

```bash
git clone https://github.com/symansun/doops.git
cd doops
bash scripts/install.sh --project /absolute/path/to/your/project
```

By default the script downloads the CLI binary and skill bundle for your platform from GitHub Releases (sha256-verified against the `checksums.txt` published with each release); use `--version vX.Y.Z` to pin a version. The `--from-source` mode keeps using the in-repo prebuilt CLI from `skills/doops-cli/bin/doops-<os>-<arch>` and requires a full repo checkout.

This step does the following:

1. Installs the CLI for your platform to `~/.local/bin/doops`
2. Installs `skills/SKILL.md` to `/absolute/path/to/your/project/.agent/skills/doops/SKILL.md`

If `~/.local/bin` is not yet on your `PATH`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

You can also write the gateway target configuration directly during installation.

```bash
bash scripts/install.sh \
  --project /absolute/path/to/your/project \
  --target-name prod \
  --target-gateway https://gateway.example.com \
  --target-cluster prod \
  --target-instance master-1 \
  --target-aliases p \
  --target-token '<GATEWAY_USER_TOKEN>' \
  --use 'prod via gateway'
```

The skill is synced into the project directory during installation. A standard target's config must include `gateway`/`cluster`/`instance`; `token` is the gateway user token.

- Targets support short names and aliases — e.g., primary name `prod` with alias `p`. Later, `doops -session smoke exec --target p --cmd hostname` resolves to the same `prod/master-1`.

## Gateway User Administration

Users, passwords, tokens, authorization, and audit logs are all maintained centrally by the gateway — never stored in business projects.

The product layer exposes only `doops` as the day-to-day entry point. Issuing accounts, issuing tokens, granting permissions, audit queries, and audit cleanup are admin capabilities and should be consolidated under the `doops admin ...` entry; business projects and skills should not guide users to call the gateway's internal maintenance commands directly.

Authorization model:

- A regular user token has full target operation capability by default; legacy scopes granting only `targets:list` are also treated as full-featured target access for compatibility, avoiding "can see but can't operate". When isolation is needed, explicitly narrow `cluster/instance/action`.
- Global administrators must be explicitly granted the `admin` permission.
- Audit records are traceable by `user_id`, `token_id`, `cluster`, `instance`, `action`, `session`, and `status`.

Troubleshooting:

- `401 Unauthorized`: the token is invalid or expired; authentication failed
- `403 Forbidden`: the token is valid, but the user has been explicitly narrowed and lacks permission for the current `cluster/instance/action`, or lacks the gateway admin `admin` permission

### 2. Add a Private-Network Cluster Target via the Gateway

The gateway's users, user tokens, and authorization are maintained by an administrator. Agents do not need a pre-issued token to register with the gateway; the business-side config only consumes user tokens issued by the admin and never touches the gateway database directly.

The gateway supports username/password login: exchange credentials for a user token first, then operate with the token:

```bash
doops login --target prod-master --gateway https://gateway.example.com --username alice
```

A private-network agent actively connects to the public gateway:

```bash
doops-agent \
  -gateway-url https://gateway.example.com \
  -cluster prod \
  -instance master-1 \
```

Configure the gateway target on the client:

```bash
doops add \
  --name prod-master \
  --gateway https://gateway.example.com \
  --cluster prod \
  --instance master-1 \
  --aliases prod,master \
  --token '<USER_TOKEN>' \
  --use 'prod cluster via gateway'
```

doops supports `http` / `https` gateways; `http` is automatically converted to the `ws` scheme used by the WebSocket tunnel.
For public-network production, `https` / `wss` is recommended, but neither the CLI nor the agent will refuse to connect just because the gateway is HTTP.

Verify:

```bash
doops targets --target prod-master
doops -session smoke exec --target prod-master --cmd 'hostname'
```

### 3. Verify the Connection

```bash
doops list
doops targets --target prod-master
doops -session smoke exec --target prod-master --cmd 'hostname'
doops -session smoke ask --target prod-master --msg '用一句话说明当前机器状态'
```

## Installing the Agent on Servers

If `doops-agent` is already running on the target machine and registered with the gateway, you only need to configure the gateway target and gateway user token on the client — no SSH required.

SSH is only used to install the agent onto a machine for the first time, or for self-recovery when the agent is completely unavailable; it is not the day-to-day connection method.

### Identify the Current Deployment Form First

```bash
kubectl get pod -A -l app=doops-agent -o wide 2>/dev/null || true
docker ps --format '{{.Names}} {{.Image}} {{.Ports}}' 2>/dev/null | grep doops-agent || true
nerdctl ps --format '{{.Names}} {{.Image}} {{.Ports}}' 2>/dev/null | grep doops-agent || true
ps -ef | grep '[d]oops-agent' || true
ss -lntp | grep ':42222' || true
```

Decision rules:

- Pod present: manage as K8s/DaemonSet.
- A container named `doops-agent` present: manage as container mode.
- Only a `/usr/local/bin/doops-agent -port ...` process: this is a bare-binary direct install, not a containerized install.
- Port listening but WebSocket EOF: usually an old agent, mismatched startup flags, or port 42222 taken by the wrong process.

### Option A: Standard K8s/DaemonSet Install

Suitable for this repo's default K3s/containerd layout.

```bash
kubectl create namespace doops-system --dry-run=client -o yaml | kubectl apply -f -
kubectl -n doops-system create secret generic registry-credentials \
  --from-literal=username="$REGISTRY_USER" \
  --from-literal=password="$REGISTRY_PASS" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n doops-system apply -f agent/agent-config.yaml
kubectl -n doops-system apply -f agent/agent.yaml
```

### Option B: Standard Docker/nerdctl Container Install

For nodes that have no DaemonSet but can run containers on the host. The container must use the standard name `doops-agent`.

```bash
docker run -d --name doops-agent \
  --privileged --pid=host --network=host \
  --restart=unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /root/.kube:/root/.kube:ro \
  -v /etc/kubernetes:/etc/kubernetes:ro \
  -e DO_AGENT_MODEL='openai/gpt-5.4' \
  <your-registry>/doops:<release> \
  -port 42222 \
  -gateway-url https://gateway.example.com \
  -cluster prod \
  -instance master-1 \
```

If the target environment uses containerd/nerdctl, replace `docker run` with `nerdctl run` and keep the arguments identical. Verify after startup:

```bash
docker ps --filter name=doops-agent
docker logs --tail=100 doops-agent
curl -fsS http://127.0.0.1:42222/health
```

### Option C: One-Shot SSH Bootstrap of a Bare Binary

Use only when the target machine has no `doops-agent` yet and you need SSH to bootstrap a bare process:

```bash
doops install \
  --name prod-node \
  --ip 192.0.2.20 \
  --ssh-user root \
  --ssh-password '<SSH_PASSWORD>' \
  --ssh-port 22 \
  --agent-token '<OPTIONAL_AGENT_TOKEN>'
```

`--agent-token` only protects the local `/ws` of legacy bare processes; it is not a gateway registration credential. After bootstrapping, still join the gateway with `-gateway-url ... -cluster ... -instance ...` and configure the gateway target on the client.

Note: `doops install` is currently an SSH bootstrap/compatibility entry and may launch the agent as a bare binary or as a container, depending on the target machine's runtime capabilities and the flags. For production standardization, prefer **Option A (K8s/DaemonSet)** or **Option B (container mode)**.

Starting from the current version, `doops install` no longer treats "the process is up" as a successful install. After installation it actively connects back to the newly installed agent and runs a capability self-check, verifying at least:

- The agent itself responds
- A container runtime is visible (`nerdctl` / `docker` / `podman`)
- Whether `kubectl` or a kubeconfig is visible
- Whether `buildctl` and `/run/buildkit/buildkitd.sock` are visible

Of these, `agent responds` and `container runtime` are required — a failure there fails the install outright; `kubectl` and `buildkit` are shown explicitly as `OK` or `MISSING`, so a half-working environment is never misreported as "ready".

### Option D: Maintainer Release of `doops-agent`

`deploy.sh` is the maintainer release entry point: it builds the agent image from this repo, pushes it, and rolls it out to the cluster:

```bash
DOOPS_IMAGE=<your-registry>/doops:<release> bash deploy.sh prod-node
```

`DOOPS_IMAGE` specifies the target image to release; if unset, a placeholder address is used and you must replace it yourself. It prefers to release over an existing doops connection. Only when the target agent is unavailable does it read `ssh_user`, `ssh_port`, and `ssh_password` from the config for a one-shot SSH recovery; the agent connection credential is always `token`.

## Common Commands

```bash
doops list
doops -session demo exec --target prod-node --cmd 'uptime && df -h'
doops -session demo ask --target prod-node --msg '检查磁盘和内存压力，给出结论'
doops -session demo push --target prod-node --src .
doops -session demo pull --target prod-node --dest ./demo-output
doops -session demo write --target prod-node --path /root/ws/demo/pod.yaml --file ./pod.yaml
doops -session demo exec --target prod-node --cmd 'ls -la /root/ws/demo'
```

Command reference:

| Command | Description |
| :--- | :--- |
| `doops add --gateway --cluster --instance --token` | Add a gateway target |
| `doops list` | List nodes configured on the client |
| `doops exec` | Run deterministic commands inside the remote agent container |
| `doops ask` | Hand off to the remote doagent for autonomous analysis and execution |
| `doops push` | Sync local code to `/root/ws/$SESSION`, respecting `.gitignore`/`.doopsignore` |
| `doops pull` | Pull the remote `/root/ws/$SESSION` workspace locally, Git-based |
| `doops read` | Read small remote text files; not for downloading large files or binaries |
| `doops write` | Write small remote text files; supports `--content`, `--file`, and stdin |
| `doops clean` | Clean the remote workspace |
| `doops upgrade` | Push an image upgrade to online agents via the gateway |

### Batch-Upgrading Agents via the Gateway

The user must have the `agent:upgrade` or `admin` permission. Dry-run first, then roll out cluster by cluster:

```bash
doops -session upgrade_demo upgrade \
  --target prod \
  --cluster prod \
  --image <your-registry>/doops:<release> \
  --mode k8s \
  --namespace doops-system \
  --workload daemonset/doops-agent \
  --container doops-agent \
  --dry-run
```

Without `--dry-run`, the upgrade is pushed to the matching online `cluster/instance`s. K8s/DaemonSet mode runs `kubectl set image` and `rollout status`; bare-binary agents explicitly refuse image upgrades.

## Maintainer Notes

Rebuild and commit the multi-platform CLIs:

```bash
bash scripts/build-cli.sh --all
ls -lh skills/doops-cli/bin/
```

Prebuilt artifacts are committed to `skills/doops-cli/bin/`; the install script copies from there to `~/.local/bin/doops`.

## Boundaries

- Day-to-day `doops` connections do not rely on SSH.
- In a standard target config, `token` means only the gateway user token.
- Agents need no token to register with the gateway; the `doops-agent token` is only for legacy direct connections or local `/ws` protection.
- `ssh_password` is retained only as an SSH bootstrap parameter.
- The `bash` subcommand is still a legacy SSH interactive entry; prefer `doops exec` and `doops ask`.

## Agent Command Guardrails

On the agent side, every `exec` passes through a built-in command guardrail ([dispatcher](backend/agent/internal/dispatcher/dispatcher.go)) before running; a match refuses execution and is written to the session audit log (`.doops-audit-log`, marked with `blocked=1` and the rule ID). Built-in rules cover: recursive deletion of root/system directories, `dd` writes to block devices, `mkfs`/partitioning tools, shutdown/reboot, fork bombs, recursive permission changes on root, overwriting critical `/etc` files, downloading and directly executing remote scripts, and killing all processes. Matching first collapses whitespace and lowercases the command to defeat obfuscation such as `rm  -rf  /` or `RM -RF /`.

The guardrail can be tuned via agent environment variables:

- `DOOPS_BLOCK_EXTRA`: append custom regexes, semicolon-separated (applied to the normalized command)
- `DOOPS_BLOCK_DISABLE`: disable built-in rules by rule ID, comma-separated, e.g. `net.pipe_shell`
- `DOOPS_BLOCK_OFF=1`: turn the guardrail off completely (emergencies only)

The guardrail is one layer of defense in depth, not a security boundary: it cannot cover every obfuscation (encoding, variable expansion, indirect execution via script files). Day-to-day operating discipline still rests on skill discipline and least privilege.

## Contributors

Core contributors to this project, in attribution order:

1. Syman
2. 高家祺 (Gao Jiaqi)
3. 宋晨宇 (Song Chenyu)
4. 王振普 (Wang Zhenpu)

Contributions via Issues and Pull Requests are welcome.

## License

This project is open source under the [MIT License](LICENSE).
