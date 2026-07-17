# Semantic CI/CD

DoOps CI/CD has one user-facing execution path:

```text
deploy/environments.yaml (doops.sh/v3 EnvironmentRegistry)
  -> select explicit environment and service
  -> resolve declared HTTPS repository and branch to an immutable commit
  -> Gateway credential prepare
  -> metadata-only CredentialRun
  -> Gateway-bound Ask($doops-cicd)
  -> DeploymentRun
```

There is no v2 `DeploymentTemplate`, generated workflow, pushed-snapshot,
direct-Agent, SSH, or manual Helm compatibility path.

## Declaration

`doops cicd run` accepts only `deploy/environments.yaml` with
`apiVersion: doops.sh/v3` and `kind: EnvironmentRegistry`. The registry selects
one environment and one service document. The selected declaration chain owns
the source repository and branch, Gateway target, build and Helm manifests,
credential references, and health checks.

Missing, mixed-version, unknown, incomplete, duplicate, path-escaping, or
secret-bearing declarations are rejected. Public repositories and registries
omit credential references. A private runtime registry must declare its
Kubernetes Secret reference; the compiler turns it into an `imagePull`
materialization bound to the selected workload. Private source checkout and
target registry transfer are rejected until their distinct execution uses are
defined; they are never accepted and silently ignored.

## Execution

```bash
doops -session <session> cicd run \
  -f deploy/environments.yaml \
  -target <gateway-target> \
  --dry-run \
  --set environment=<environment> \
  --set service=<service>
```

The command requires a clean Git workspace, an explicit Gateway target and
session, and explicit `environment` and `service` inputs. It resolves the
declared branch with `git ls-remote` and binds execution to one immutable 40- or
64-character commit.

Before Ask, Gateway credential prepare compiles the same selected declaration
chain and returns only metadata in a `CredentialRun`. The authorization binds
the authenticated user, target, session, manifest, source, environment,
service, mode, workspace commit, and complete `CredentialRun` digest. It is
consumed once after operation limits and the target lock succeed.

The target Agent must advertise the exact credential-plan capability:

- `schemaVersion=doops.sh/credential-plan/v2`
- `declarationSchemaVersion=doops.sh/v3`
- `sourceRevision=true`
- `targetBinding=true`
- `sessionBinding=true`
- `buildDefinition=true`

Legacy string capabilities are incompatible. Before model execution, doagent
`initialize` must advertise the exact trusted reconciliation contract:

- `schemaVersion=doops.reconciliation-context/v2`
- `callContextSchemaVersion=doops.reconciliation-call-context/v1`
- `mcpMetaKey=doops.sh/reconciliation-context`
- `credentialRunDigest=true`
- `sourceWorkspaceCommit=true`

Any mismatch fails with a deterministic protocol error. It is never retried
through an older protocol.

## Result

doagent emits exactly one JSON `DeploymentRun`. Gateway rejects missing,
Markdown-wrapped, multiple-object, source-mismatched, mode-mismatched, or
workspace-mismatched results. Dry-run must report zero mutations. Apply is
accepted only for the structured instruction produced by the CLI and bound to
the prepared authorization.

Trusted capability and evidence fields come from completed, attested tool calls,
not model-authored claims. Plain text such as `completed` or `looks healthy` is
not deployment evidence.

## CLI Artifacts

`scripts/build-cli.sh --all` builds every supported CLI artifact with
deterministic Go build settings and writes a temporary `dist/cli/checksums.txt`.

On `tag_push`, CNB uploads the four binaries and checksum manifest to the
same-version Release Assets, then downloads them again to verify checksums.
`scripts/install.sh --version <tag>` downloads the selected platform asset and
verifies it against that manifest before installation. CLI binaries are not
checked into Git.
