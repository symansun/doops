# Oilan DoOps Agent Release

The runtime definition is:

- chart: `backend/deploy/helm/doops-agent`;
- values: `backend/deploy/helm/doops-agent/values-oilan.yaml`;
- image identity: immutable `repository@sha256:digest`.

Agent registration uses the configured `gateway.cluster` and `gateway.instance`.
It does not mount or send a registration token. Gateway user tokens authorize
operator calls and are unrelated to Agent connectivity.

The Deployment uses `RollingUpdate` with `maxUnavailable: 0` and `maxSurge: 1`.
The replacement Pod becomes ready only after Gateway initialization,
acknowledgement, and an initial heartbeat. Kubernetes therefore keeps the
previous Pod running until the replacement has a verified management path.

The Agent must not update its own Deployment. A release is applied by an
external release runner or GitOps controller, which sets the immutable digest
and verifies rollout, Gateway registration, heartbeat, and the target health
path. `doops upgrade` is preview-only.

`doagent-model-settings/settings.json` remains the model configuration source.
An Agent image release must not replace or synthesize model credentials.
