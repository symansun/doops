# Semantic CI/CD

DoOps CI/CD has one user-facing execution path:

```text
v2 DeploymentTemplate | SemanticRelease ┐
                                        ├-> Push -> credential prepare
v3 EnvironmentDefinition ────────────────┘        -> Ask($doops-cicd) -> DeploymentRun
```

The repository workflow declares intent. `doops cicd run` validates the
invocation, pushes the exact Git workspace to the selected Agent, and asks
doagent to execute the `$doops-cicd` Skill. There is no second CI/CD RPC,
generated shell pipeline, or hidden deployment fallback.

## Declaration

Each execution has one versioned root:

- v2 uses `DeploymentTemplate` or `SemanticRelease`; `SemanticRelease` closes
  over referenced `ServiceRelease` documents.
- v3 uses
  `deploy/environments/<environment>/environment.yaml`
  `EnvironmentDefinition`; it closes over every referenced
  `EnvironmentService` and `ServiceDefinition`.

Example v2 root:

```yaml
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: example-release
spec:
  parameters:
    releaseId:
      required: true
  application: example
  release:
    source:
      repository: https://example.test/app.git
      revision: ${inputs.releaseId}
      branch: main
  environment: oilan
  configurationSource: backend/deploy/environments.yaml
```

The declaration contains logical release intent. Physical targets, credentials,
Helm values, registry bindings, and verification requirements remain in the
versioned environment and deployment configuration referenced by the workflow.
For v2, `configurationSource` is repository-root-relative and must explicitly
declare `apiVersion: doops.sh/v2` and `kind: EnvironmentRegistry`. A v3
`EnvironmentDefinition` is a deployment root, not a v2 configuration source.
Missing, mixed-version, unknown, incomplete, or path-escaping declarations are
rejected.

## Execution

`doops cicd run` requires:

- an explicit `-session`;
- an explicit configured Gateway `-target`;
- a workflow file inside a clean Git workspace;
- declared `--set key=value` inputs.

The command performs these steps:

1. Push the repository snapshot to `/root/ws/<session>`.
2. Capture the immutable workspace commit returned by the Agent.
3. Ask the Gateway credential control plane to compile the same root and its
   complete referenced declaration chain.
4. Require the target Agent to advertise
   `doops.sh/credential-plan/v2`; incompatible Agents are rejected before tool
   dispatch.
5. Reject v3 environments whose required source, target, or runtime Secret
   reference is undeclared.
6. Send one structured `doops_agent_prompt` request selecting `$doops-cicd`.
7. Bind the request to the pushed workspace commit.
8. Use ordinary Ask for dry-run and the explicitly validated apply operation
   for mutation.
9. Wait for doagent's authoritative `turn_finished` event.

Apply is accepted only for the structured `$doops-cicd` instruction generated
by the CLI. Ordinary prompts cannot request the mutation-capable mode.

## Result

doagent emits exactly one JSON object as its final response:

```json
{
  "apiVersion": "doops.sh/v2",
  "kind": "DeploymentRun",
  "metadata": {
    "workflow": "deploy/environments/oilan/environment.yaml",
    "runId": "run-example",
    "workspaceCommit": "0123456789abcdef0123456789abcdef01234567"
  },
  "spec": {
    "mode": "dry-run"
  },
  "status": {
    "phase": "planned",
    "mutationCount": 0,
    "capabilities": {
      "ObserveWorkspaceSource": {
        "available": true
      }
    },
    "evidence": [
      {
        "subject": "source-identity",
        "observedAt": "2026-07-17T00:00:00Z",
        "toolRef": {
          "tool": "plan.ObserveWorkspaceSource",
          "input": {}
        }
      }
    ]
  }
}
```

The Gateway rejects a missing, Markdown-wrapped, non-object, or multiple-object
final response. Agent-authored evidence uses `toolRef` with an exact tool name
and exact JSON input. Trusted DoOps plan references use the model-visible
`plan.<Tool>` name and `{}` input because trusted context is injected outside
model arguments. The Gateway rejects model-authored runtime payloads, resolves
every reference to one completed tool call from the accepted `turnId`, and
injects `toolCall` with the exact ID, runtime tool name, input and attested
result after removing transport-only `_meta`.
Conflicting duplicate IDs, replayed events, missing calls or ambiguous
references fail. Image generation belongs to doagent multi-agent execution;
DoOps does not expose or invoke it.

The CLI accepts only a `DeploymentRun` whose apiVersion exactly matches the
root declaration and whose:

- workspace commit matches the pushed workspace;
- mode matches the requested dry-run or apply operation;
- phase is `planned`, `converged`, `blocked`, `failed`, or `outcome-unknown`;
- mutation count is present and non-negative;
- dry-run mutation count is zero;
- capability snapshot and evidence are present.

Text such as `admitted`, `completed`, or `looks healthy` is not deployment
evidence.

## CLI Artifacts

`scripts/build-cli.sh --all` builds every supported CLI artifact with
deterministic Go build settings and writes
`skills/doops-cli/bin/checksums.txt`.

`scripts/install.sh` verifies the selected prebuilt binary against that
manifest before installation. `scripts/verify-cli-artifacts.sh` rebuilds and
byte-compares every checked-in CLI artifact.
