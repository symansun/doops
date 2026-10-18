# doops Model & Deployment Standards

This document defines the model configuration used by `doops ask` through the
embedded doagent runtime.

## Runtime Configuration

`doops-agent` starts doagent as an ACP HTTP service on `127.0.0.1:$DO_AGENT_PORT`
(default `9000`). When `DO_AGENT_URL` is omitted, it is derived from that same
port so the Agent never starts a second ACP HTTP process on a different port. The
effective runtime settings are stored at:

```text
/root/.agent/runtime-settings.json
```

Kubernetes mounts `/opt/doagent_config/settings.json` from the
`doagent-model-settings` Secret. The entrypoint materializes that mounted
Secret into the runtime path without modifying the Secret. The public
`doops.sh` repository keeps only a placeholder Secret template;
environment-specific deployments must generate the real Secret from their
private secret store. Do not commit real API keys to the public repository.

## Kubernetes Secret Deployment

`doagent-model-settings` is the required model configuration object for
`doops ask`. It must exist before the `doops-agent` Pod starts:

The following bootstrap example runs only on the authorized external controller
(cluster management host or CI) with an explicitly selected Kubernetes identity.
It is not an instruction to find a developer-machine kubeconfig for an ordinary release.

```bash
export KUBECONFIG=<external-controller-kubeconfig>
kubectl create namespace doops-system --dry-run=client -o yaml | kubectl apply -f -

# Public template, suitable only after replacing the placeholder apiKey.
kubectl -n doops-system apply -f agent/agent-config.yaml

# In production, apply the environment-specific Secret from the private store.
# kubectl -n doops-system apply -f /path/to/private/doagent-model-settings.yaml

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

The Secret must provide:

```text
data.settings.json
```

with a configured `provider.minimax.options.apiKey`. The image must not supply
a second provider, infer a credential, or reset the selected model during an
upgrade.

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
3. Verify the mounted `doagent-model-settings/settings.json` has a configured
   provider and non-empty API key without printing it.
4. Run `doops exec` against the same target to confirm the gateway fast path is
   healthy.
