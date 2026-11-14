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
target. Delivery timing, the no-reprobe rule, and the refusal to copy another
target's model are defined in
[CREDENTIAL_RESOURCES.md](CREDENTIAL_RESOURCES.md#model-provider). doagent reads
`/root/.agent/settings.json` for every new session, so delivery and rotation
take effect without restarting it. Payload
schema, verification (one real model call per binding) and revocation are
defined in [CREDENTIAL_RESOURCES.md](CREDENTIAL_RESOURCES.md#model-provider).
No Kubernetes Secret, ConfigMap or host file holds the model configuration, and
the Chart has no model settings values.

The image must not supply a provider, infer a credential, or reset the
delivered model during an upgrade. Installation and upgrade procedures belong
to [CLUSTER_INSTALL_GUIDE.md](CLUSTER_INSTALL_GUIDE.md) and
[BUILD_DEPLOY_SOP.md](BUILD_DEPLOY_SOP.md); their values contain no model settings.
Business Helm delivery is defined in
[NATURAL_LANGUAGE_DEPLOYMENT.md](NATURAL_LANGUAGE_DEPLOYMENT.md).

## Model Selection

This repository ships **no default provider, endpoint or model**. The model is
chosen per target in the Gateway and delivered as a `modelProvider` credential
(see [CREDENTIAL_RESOURCES.md](CREDENTIAL_RESOURCES.md#model-provider)); `doops ask`
runs with whatever that binding delivers.

Skills, images and Chart values stay model-agnostic: never hard-code a vendor,
endpoint, model ID, API-key shape or "use model X" instruction in a skill, a
document or a deployment value. Configure or rotate the model through the
`modelProvider` lifecycle; inspect what a target currently has:

```bash
doops -session <session> credential list --target <target>
```

## Troubleshooting

If `doops ask` fails:

1. Check `DO_AGENT_URL`, default `http://127.0.0.1:9000`.
2. Verify `/usr/local/bin/do-agent --help` works inside the final image.
3. Run `doops credential list --target <target>` and confirm one active
   `modelProvider` credential is bound and promoted for this target. The Agent
   metadata reports whether a model was delivered; it never shows the key.
4. Run `doops exec` against the same target to confirm the gateway fast path is
   healthy.
