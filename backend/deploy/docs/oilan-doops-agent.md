# Oilan DoOps Agent Release

The Oilan release has one authoritative declaration chain:

- registry: `deploy/environments.yaml`;
- environment service: `deploy/environments/oilan/services/doops-agent.yaml`;
- service definition: `deploy/services/doops-agent.yaml`;
- build definition: `deploy/deploy.yaml`;
- chart: `backend/deploy/helm/doops-agent`;
- managed Agent values: `deploy/environments/oilan/values.yaml`;
- release runner values: `deploy/environments/oilan/release-runner-values.yaml`.

All declarations use `doops.sh/v3`. Generated workflows and v2
`DeploymentTemplate` files are not release inputs.

## Artifact And Credentials

CNB CI builds the DoOps image with the full source commit as its immutable tag.
The environment service then pins the registry digest used by Helm and the
runtime Deployment.

Agent reverse registration declares only the configured cluster and instance;
it does not mount, read, or send a registration token. Gateway user tokens
remain the authorization boundary for operator actions.

`docker.cnb.cool/l8ai/ai/doops.sh` is public, so Oilan does not declare source,
target, or runtime registry credentials. A private runtime registry must
declare its Kubernetes Secret reference explicitly; the compiler binds it to
the selected workload as an `imagePull` materialization. Private source
checkout and target registry transfer are rejected until their separate
credential uses are defined. Credential prepare rejects partial, unsupported,
or explicitly undeclared references and never infers a Secret from an existing
workload.

## Runtime Contract

The image contains the pinned doagent release. Gateway credential prepare
requires the target Agent to advertise the exact credential-plan capability:

- `schemaVersion=doops.sh/credential-plan/v2`;
- `declarationSchemaVersion=doops.sh/v3`;
- `sourceRevision=true`;
- `targetBinding=true`;
- `sessionBinding=true`;
- `buildDefinition=true`.

The embedded doagent must advertise the exact reconciliation contract:

- `schemaVersion=doops.reconciliation-context/v2`;
- `callContextSchemaVersion=doops.reconciliation-call-context/v1`;
- `mcpMetaKey=doops.sh/reconciliation-context`;
- `credentialRunDigest=true`;
- `sourceWorkspaceCommit=true`.

Any mismatch stops before Ask. The managed `doops-agent-live` Deployment never
executes its own release. The distinct `doops-release-runner` Deployment uses a
different Gateway identity, selector, and Agent home. There is no direct-Agent,
local Helm, self-upgrade, or older-protocol execution branch.

## Execution

```bash
doops -session doops-oilan-release cicd run \
  -f deploy/environments.yaml \
  -target gw-edu-release-runner \
  --dry-run \
  --set environment=oilan \
  --set service=doops-agent
```

The CLI resolves the declared branch to an immutable commit, calls Gateway
`POST /v1/credentials/prepare`, and then submits the bound `$doops-cicd` Ask.
Dry-run must report zero mutations. Apply uses the same declaration and
credential run; it does not accept a locally edited workflow or image tag.

The Helm release is `doops-agent-live` in namespace `kz-ops`. The Deployment
uses rolling update with zero unavailable replicas and becomes ready only
after its Gateway registration is healthy. Model provider configuration remains
in the `doagent-model-settings/settings.json` Secret and is not duplicated in
the environment registry.
