# Oilan Agent On EDU 244

This directory contains runtime assets, not an environment registry or deployment
authorization source. The canonical runtime chart is
`backend/deploy/helm/doops-agent`.

## Identity And Placement

- Kubernetes cluster: the existing EDU cluster accessed through `gw-edu-coder`.
- Authorized public node: `244`; Kubernetes node: `gpu-ampere01`,
  InternalIP `192.168.0.122`.
- Namespace / release / workload: `doops-oilan / oilan-agent / oilan-agent`.
- Gateway identity: `doops-oilan/oilan-node`; local CLI route: `gw-oilan-node`.
- Separate Agent home: `/var/lib/doops-oilan-agent-home`.
- Released source: `b96c7a46259798babd2701abf07330121b4fe9da`, `v1.3.23`.
- Pinned image: the linux/amd64 manifest in the verified v1.3.23 image index.

The hostname selector is mandatory. Neither a taint bypass nor another node is
an acceptable substitute when 244 is unavailable. This does not replace the
existing `kz-ops/doops-agent-live` Agent.

## Bootstrap And Secrets

Bootstrap through the external controller or existing EDU Agent,
using the canonical chart and these values after server-side dry-run. Apply
`rbac.yaml` and the token Secret metadata before installing the new release.

The kubeconfig Secret `doops-oilan-kubeconfig` must contain a `config` key for
ServiceAccount `doops-oilan/doops-oilan-agent`, not a copied cluster-admin
credential. No model Secret is mounted: the Agent receives its model from the
Gateway as the `modelProvider` credential bound to `doops-oilan/oilan-node`
(see `backend/docs/CREDENTIAL_RESOURCES.md`). Do not print, commit, or transfer
credential payloads through public prompts or workspaces.

The ServiceAccount token is persistent bootstrap material. Rotate it and
regenerate the kubeconfig Secret through an authorized secret-control path;
these files do not implement automatic token rotation. Kubernetes grants are
cluster read access without general Secret access and namespace write access
to `doops-oilan`, `oilan`, `oilan-middleware`, and `ecp`. The Agent remains
privileged for the existing runtime contract, so RBAC alone does not isolate it
from host access. Gateway target grants still control user requests.

The public Agent image does not depend on a registry pull Secret. Keep payloads
out of values and never use registry credentials as a substitute for diagnosing
an entrypoint failure.

## Update Acceptance

After bootstrap, use the released CLI's managed update path:

```sh
doops -session oilan-update upgrade --target gw-oilan-node \
  --namespace doops-oilan --release oilan-agent \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:7b3e213db1cdb4e4576e2f985b3edef2a8c12fb4ce773840e8e67d4df5ea096b
```

This means an operator-authorized image update executed automatically by an
independent in-cluster Job. It is not a schedule that follows mutable tags.
The Job reuses the mounted kubeconfig; do not run Helm in the current Agent
process to replace that Agent.

- Given the bootstrap is healthy, when the managed update runs, then the Job
  reaches Succeeded and the CLI verifies structuredSSH and release provenance.
- Given the image specification changes, when rollout completes, then there
  is one Ready replacement Pod with a new UID on `gpu-ampere01`; its
  spec/imageID matches the requested immutable image.
- Given the replacement serves the Gateway, when exec, background exec and
  read-only Kubernetes prompts run, then actual output and terminal evidence
  are returned, not only heartbeats.
- Given the Agent kubeconfig is used, when querying unrelated namespace
  Secrets, then the request is denied.

## Rollback

Retain Helm history and the last successful image. An initial failed revision
is not a rollback baseline. After revision 2 bootstrapped successfully, managed
rollback is available via:

```sh
doops -session oilan-rollback upgrade --target gw-oilan-node --rollback \
  --namespace doops-oilan --release oilan-agent
```

If the Gateway target is unreachable, use the approved external Kubernetes
controller to roll the recorded release back and verify Pod, image and Gateway
identity. Automatic recovery cannot be promised for a total control-path outage.
