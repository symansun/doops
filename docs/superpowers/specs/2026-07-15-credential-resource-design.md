# DoOps Credential Resource Design

> Updated on 2026-07-17 for the authoritative `doops.sh/v3`
> `EnvironmentRegistry` execution path. Credential payloads remain write-only;
> Git contains only metadata and references.

## Goal

Make credentials a managed DoOps resource rather than hand-authored Kubernetes
Secret YAML. Deployment manifests must reference a credential by name and never
contain a token, password, private key, cookie, or encoded secret payload.

The first release addresses private OCI image pulls, while defining one
resource model for registry, TLS, opaque, Helm repository, and Git credentials.
It extends the existing
`EnvironmentRegistry -> credential prepare -> doops cicd run -> doagent`
release path; it does not introduce an SSH or direct `kubectl` deployment path.

## Scope And Non-Goals

In scope:

- Personal and platform-owned managed credentials.
- Explicit authorization to targets, projects, environments, namespaces, and
  deployment templates.
- Deployment YAML references, target-side materialization, verification, audit,
  revocation, and rotation.
- Kubernetes Secret, image pull secret, TLS Secret, Helm repository
  credential, and Git token materialization.

Out of scope for the first release:

- Importing or displaying arbitrary existing Kubernetes Secret values.
- Storing credentials in Git, CI variables, workspace files, Agent prompts, or
  deployment artifacts.
- A second deployment controller, shell fallback, or silent local Secret path.
- Automatic credential discovery from image names or historical deployments.

## Product Model

```text
CredentialResource
  + CredentialGrant(s)
  + optional CredentialBundle
  -> EnvironmentRegistry / EnvironmentService credential reference
  -> Gateway authorization and target resolution
  -> Agent materialization in the authorized target
  -> non-sensitive verification evidence in DeploymentRun and audit
```

`CredentialResource` owns one secret payload and its non-sensitive metadata.
It has a stable ID, type, ownership, version, lifecycle state, and
materialization policy. Its payload is write-only: no normal API, CLI, issue,
Git, workspace, Agent prompt, audit record, or result artifact can return it.

`CredentialGrant` binds a credential to one or more allowed consumers. A
consumer can be a target, project, environment, namespace, or selected
environment service. A grant also limits permitted uses, such as `imagePull`,
`helmRepository`, `gitCheckout`, or `runtimeMount`.

`CredentialBundle` is a named, versioned list of credential references for one
deployment concern. It does not copy payloads and has no independent secret
value. A bundle is useful when a template needs, for example, an OCI pull
credential and a Helm repository credential under one authorization boundary.

## Ownership And Lifecycle

| Resource scope | Owner | Who may maintain payload | Intended sharing |
| --- | --- | --- | --- |
| `personal` | One DoOps user | Owner; platform credential admin for emergency recovery | Explicit grants made by the owner within permissions |
| `platform` | Platform or administrator-managed service identity | Platform credential admin | Explicit grants to multiple targets, environments, or templates |

“Platform credential admin” means a user with a global `admin` or `*` action
grant scoped to `*/*`. Target-scoped credential actions do not grant platform
administration.

Lifecycle states are `active`, `rotating`, `revoked`, and `retired`.

- `active` credentials can be used only when a matching active grant exists.
- `rotating` accepts a replacement payload, creates a new version, validates it
  in every selected target, and marks the prior version superseded only after
  the requested rollout succeeds.
- `revoked` denies new use immediately and triggers removal of managed target
  materialization on the next reconciliation or explicit revoke operation.
- `retired` preserves metadata and audit history but cannot be reactivated.

The credential store must encrypt payloads at rest with a deployment-provided
key-encryption boundary. Key material is not stored with the credential record.
The implementation must fail closed when the encryption provider is
unavailable; it must not write plaintext locally as a fallback.

## Credential Types And Materialization

The resource type declares a fixed payload schema and permitted target
representations. A caller cannot select arbitrary Secret types or keys.

| Credential type | Target representation | Required verification |
| --- | --- | --- |
| `registry` | Kubernetes Secret type `kubernetes.io/dockerconfigjson` and optional `imagePullSecrets` reference | Secret type/key set and OCI manifest access |
| `tls` | Kubernetes Secret type `kubernetes.io/tls` | Secret type/key set and certificate/key match |
| `opaque` | Kubernetes Secret type `Opaque` with an allowlisted key schema | Secret type/key set only |
| `helmRepository` | Managed Helm repository authentication configuration or Secret | Repository index access |
| `gitToken` | Managed Git credential helper configuration or purpose-bound Secret | Remote metadata access |

The target Secret name is generated deterministically from the credential ID,
version, namespace, and materialization intent. It is never provided by a
deployment manifest and does not expose a credential value. The Gateway
resolves target and namespace from the selected v3 environment service plus the selected
grant; the Agent does not infer them.

## Authorization Model

Every use requires both deployment authorization and credential authorization.
Possession of a deployment permission alone never implies access to a
credential.

| Action | Personal credential | Platform credential |
| --- | --- | --- |
| Create metadata and submit payload | Owner | Credential admin |
| View metadata | Owner; explicitly authorized reader; credential admin | Explicitly authorized reader; credential admin |
| Change payload / rotate | Owner; credential admin | Credential admin |
| Create or change grant | Owner with authority over every target scope; credential admin | Credential admin |
| Use through a deployment | Caller with deployment permission and matching grant | Caller with deployment permission and matching grant |
| Revoke | Owner; credential admin | Credential admin |
| Read payload | Never | Never |

The Gateway evaluates grants against the authenticated user, requested target,
project/environment/template context, namespace, credential state, credential
type, and requested use. Denials identify the failed authorization category,
not the payload or source details.

For personal credentials, an owner may grant only targets and scopes that the
owner is already authorized to deploy to. Platform credentials are grantable
only by a credential admin. Deleting a grant or revoking a credential must
prevent new materialization before any Agent call is made.

## YAML Contract

Credential payloads are provided through a dedicated write-only command
outside Git. Metadata and grants are managed through the Gateway API/CLI. The
deployment repository contains only a reference. For a private runtime OCI
registry, the v3 environment declares the managed credential name:

```yaml
apiVersion: doops.sh/v3
kind: EnvironmentRegistry
metadata:
  name: doops
environments:
  oilan:
    target:
      name: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
      session: doops-oilan-release
    source:
      repository: https://cnb.cool/l8ai/ai/doops.sh.git
      branch: main
      build: deploy/deploy.yaml
    helm:
      release: doops-agent-live
      namespace: kz-ops
      chart: backend/deploy/helm/doops-agent
      values: deploy/environments/oilan/values.yaml
      atomic: true
      wait: true
    artifacts:
      targetRegistry: registry.example.internal/team
      runtimeImagePullSecret:
        kind: KubernetesSecret
        name: cnb-oci-pull
    services:
      doops-agent: deploy/environments/oilan/services/doops-agent.yaml
    healthChecks:
      - https://doops.l8ai.cn/health
```

The manifest must not accept `data`, `stringData`, `password`, `token`,
`auth`, or equivalent secret fields. The compiler derives the namespace,
registry repository, immutable digest, and workload from the selected
environment service; users cannot override those fields in deployment inputs.

The implemented CLI surface is intentionally small:

```text
doops credential create --target <target> --name <name> --scope <personal|platform> --type <type>
doops credential put --target <target> --id <credential-id> < payload.json
doops credential grant --target <target> --id <credential-id> --grantee-id <user-id> \
  --cluster <cluster> --instance <instance> --project <project> \
  --environment <environment> --template <template> --namespace <namespace> \
  --uses <comma-separated-uses>
doops credential verify --target <target> --id <credential-id> --version-id <version-id> \
  --session <session> --workflow-path <path> --workspace-commit <commit> \
  --source-repository <https-repository> --source-branch <branch>
doops credential promote --target <target> --id <credential-id> --version-id <version-id>
doops credential grant-revoke --target <target> --id <credential-id> --grant-id <grant-id>
doops credential revoke --target <target> --id <credential-id>
doops credential bundle-create --target <target> < bundle.json
doops credential bundle-get --target <target> --name <name>
doops -session <session> cicd run -f deploy/environments.yaml -target <target> \
  --set environment=<environment> --set service=<service> [--dry-run]
```

Payload input is read only from stdin, is size-bounded and schema-validated,
and is never printed. The CLI rejects payload values in YAML, `--set`, command
arguments, or workflow inputs instead of attempting compatibility behavior.

## Deployment Resolution

For an apply operation, the Gateway and Agent execute the following ordered
flow:

1. Parse the selected v3 EnvironmentRegistry and EnvironmentService and reject
   secret-like inline fields.
2. Resolve each credential or bundle reference by immutable resource ID and
   current active version.
3. Verify caller deployment permission and an active CredentialGrant that
   covers the target, environment, selected service identity, namespace, and
   requested use.
4. Construct target materialization only from the credential type's fixed
   schema and grant-approved destination.
5. Send payload only through the authenticated Gateway-to-Agent operation
   channel, materialize the target object, and attach it to the allowed
   workload/repository configuration.
6. Run non-sensitive verification. Persist the resulting facts in the
   DeploymentRun and audit records.
7. On failure, report the exact stage and error category, leave the credential
   state explicit, and stop. Do not fall back to an existing unverified Secret,
   plaintext YAML, shell command, or a different target.

Dry-run validates references, grants, target reachability, materialization
schema, and any safe provider metadata. It has mutation count zero and must not
create a Secret or test a value-dependent registry operation.

## Verification Contract

Successful apply or explicit `credential verify` produces evidence limited to:

- credential ID and version identifier;
- target, namespace, Secret name, Secret type, and sorted key names;
- target `resourceVersion`;
- OCI repository reference and manifest digest when the use is `imagePull`;
- certificate fingerprint and expiration for TLS;
- verification phase, timestamp, and error category.

It must never include Secret data, decoded Docker config, `Authorization`
headers, bearer values, cookies, command lines containing values, or response
bodies that can contain values.

For a registry credential, success requires all of:

1. target Secret exists;
2. type is exactly `kubernetes.io/dockerconfigjson`;
3. key set is exactly `.dockerconfigjson`;
4. the authorized OCI manifest request succeeds; and
5. the returned manifest digest is recorded without a credential value.

Failure categories include `reference_not_found`, `grant_denied`,
`target_offline`, `schema_invalid`, `materialization_failed`,
`secret_type_mismatch`, `secret_key_mismatch`, `registry_auth_denied`,
`manifest_not_found`, and `verification_transport_failed`.

## Audit And Redaction

The existing Gateway audit model must gain credential-aware fields:

- actor user/service identity and token ID;
- action: `create`, `metadata_update`, `payload_put`, `grant`, `use`,
  `verify`, `rotate`, `revoke`, or `retire`;
- credential resource ID, version ID, scope, and type;
- grant ID and authorized consumer scope;
- cluster, target instance, environment, namespace, template, and session;
- outcome, error category, resourceVersion, manifest digest, timestamps, and
  correlation ID.

Audit write and read paths must redact secret-bearing fields before persistence.
The same redactor applies to CLI output, Gateway logs, Agent logs, Git
operations, issue bodies/comments, workspace files, `DeploymentRun`, and
remote command output. Redaction is defense in depth, not permission to pass a
payload through those channels.

## API Sketch

The Gateway exposes metadata-only CRUD and purpose-bound operations:

```text
POST   /v1/credentials
GET    /v1/credentials
GET    /v1/credentials/{id}
PUT    /v1/credentials/{id}/payload
POST   /v1/credentials/{id}/grants
POST   /v1/credentials/{id}/verify
POST   /v1/credentials/{id}/rotate
POST   /v1/credentials/{id}/revoke
GET    /v1/credentials/{id}/audit
```

`GET` responses contain only metadata. Payload mutation endpoints accept a
credential type-specific opaque body on the protected request channel and
return metadata plus a version identifier. They must reject `GET`, list, audit,
or error serialization that could reveal payload bytes.

The Agent receives a purpose-bound materialization request, not a general
"read credential" operation. Its request includes the immutable resource and
version identifiers, target destination, allowed use, and correlation ID. It
cannot list, export, or repurpose credentials.

## Acceptance Scenarios

1. Given an active platform registry credential granted to `gw-edu-coder`,
   `oilan`, `kz-ops`, and `oilan-agent-release`, when an authorized deployer
   runs the template, then DoOps materializes the generated pull Secret,
   attaches it to the selected workload, verifies its type/key set and OCI
   manifest access, and returns only status, resourceVersion, and digest.
2. Given the same credential but a deployment to an ungranted namespace, when
   the deployer applies the template, then the Gateway returns `grant_denied`
   before contacting the Agent and no target Secret is created or changed.
3. Given a personal credential, when its owner grants it to a target the owner
   cannot deploy to, then grant creation fails with an authorization error.
4. Given a revoked credential, when any deployment references it, then
   resolution fails before materialization; DoOps does not use a pre-existing
   Secret as fallback.
5. Given a rotation request, when target verification fails, then the result
   records the failed verification category and the new version is not promoted
   for subsequent deployments.
6. Given an audit or DeploymentRun read, when the operation succeeded or
   failed, then no secret value, encoded payload, token, password, cookie, or
   authorization header is present.

## Delivery Sequence

1. Define Gateway schema, encryption provider contract, metadata-only APIs,
   grants, redaction tests, and audit extensions.
2. Add CLI metadata/grant/payload commands and v3 EnvironmentRegistry
   reference validation; reject inline secret fields.
3. Implement Agent materializers beginning with registry image pull Secrets and
   OCI manifest verification.
4. Add TLS, opaque, Helm repository, and Git token materializers only after
   each fixed schema and verifier is defined.
5. Exercise the Oilan CNB private OCI path end-to-end against a non-production
   target before enabling production grants.
