# doops Model & Deployment Standards

This document defines the model configuration used by `doops ask` through the
embedded doagent runtime.

## Runtime Configuration

`doops-agent` starts doagent as an ACP HTTP service on `127.0.0.1:$DO_AGENT_PORT`
(default `9000`). When `DO_AGENT_URL` is omitted, it is derived from that same
port so the Agent never starts a second ACP HTTP process on a different port. The
only settings file doagent reads is:

```text
/root/.agent/settings.json
```

No environment variable or command-line flag selects another file.

## Model Provider Delivery

An Agent deploys without any model configuration. At start the entrypoint runs
`doops-agent doagent-settings`, which writes the fixed settings file with the
runtime MCP servers and permissions only; doagent starts and reports
"no model configured" until a model arrives.

The model comes from the Gateway as a `modelProvider` credential bound to the
target. When the Agent connects, the Gateway materializes the active version
into `/root/.agent/settings.json`; doagent reads that file for every new
session, so delivery and rotation take effect without restarting it. Payload
schema, verification (one real model call per binding) and revocation are
defined in [CREDENTIAL_RESOURCES.md](CREDENTIAL_RESOURCES.md#model-provider).
No Kubernetes Secret, ConfigMap or host file holds the model configuration, and
the Chart has no model settings values.

A new cluster is installed with the external controller (cluster management
host or CI) and an explicitly selected Kubernetes identity; the values need no
model settings:

```bash
bash scripts/release-managed-agent.sh \
  --release doops-agent \
  --namespace doops-system \
  --values /path/to/complete-values.yaml \
  --kubeconfig "$KUBECONFIG" \
  --version <vX.Y.Z>
```

`--version` 可以解析 release tag 到镜像 digest，`--image` 也可以直接提供镜像；两者都
不证明 CI 来源。发布者必须在调用前保留精确 source commit、registry digest 和 CI 成功
证据。`--values` 新集群路径是 bootstrap-only，只做 Helm wait 和最终 Deployment 检查，
不做 Gateway 接管或自动回滚；完成后必须继续执行安装验收。

The image must not supply a provider, infer a credential, or reset the
delivered model during an upgrade.

Ordinary workload deployments use the Gateway target's natural-language
apply path:

```bash
doops -session <session> ask --target <target> --apply \
  --msg "<target、服务范围、镜像版本、变更原因和验收条件>"
```

This path does not require a kubeconfig on the operator machine.

For an existing Helm-managed Agent that already contains the self-upgrade
controller, use `doops upgrade`. The Agent must be in its own namespace, use
the standard Deployment, and have an existing matching Helm release; without
`--release` exactly one matching release is required, while `--release`
selects and validates the named release. The in-cluster Job explicitly reuses
the `/root/.kube` or `/root/.kube/config` mount, so its Kubernetes permissions
come from that kubeconfig and the operator machine does not need a kubeconfig:

```bash
doops -session <release-session> upgrade \
  --target <gateway-target> \
  --image <repository>@sha256:<digest>
```

Use `scripts/release-managed-agent.sh` with an operator-side kubeconfig only
for a new cluster without an Agent or an existing Helm release whose Agent
does not contain the self-upgrade controller. An existing workload without a Helm
release is brought under Helm first, as described in BUILD_DEPLOY_SOP.md. The
script also changes Agent `proxy.allowedHosts` or `hostAliases`.
`doops upgrade` changes only the managed Agent image or performs rollback.
Ordinary workload deployments always use
`doops -session <session> ask --target <target> --apply` and do not require a
local kubeconfig.

## Standard Endpoint

```text
https://api.minimaxi.com/anthropic
```

## Standard Model

| Task Type | Model ID |
|-----------|----------|
| Default / Coding / Summary | `minimax/MiniMax-M3` |

## Troubleshooting

If `doops ask` fails:

1. Check `DO_AGENT_URL`, default `http://127.0.0.1:9000`.
2. Verify `/usr/local/bin/do-agent --help` works inside the final image.
3. Run `doops credential list --target <target>` and confirm one active
   `modelProvider` credential is bound and promoted for this target. The Agent
   metadata reports whether a model was delivered; it never shows the key.
4. Run `doops exec` against the same target to confirm the gateway fast path is
   healthy.
