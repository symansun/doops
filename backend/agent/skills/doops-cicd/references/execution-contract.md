# Execution Contract

## Native Module Delegation

Discover runtime capabilities before planning. Map each declared responsibility
to an available DoOps module or installed Skill:

- source and repository inspection;
- artifact build, resolution, transfer and readback;
- deployment executor;
- authorization and credential-reference checks;
- workload, endpoint, log and release verification;
- rollback observation or execution when declared.

The coordinator may use multiple native subagents for independent work. It must
not invent module names or assume a module is installed because this reference
mentions a responsibility.

## Modes

### dry-run

- Perform no mutation.
- Observe source identity, artifacts, target state, permissions and declared
  verification endpoints.
- Report each mutation that apply would require.
- Return blocked when a required fact cannot be observed.

### apply

- Require explicit mutation authorization.
- Recheck source and target identity immediately before mutation.
- Execute through runtime modules selected for the declared executor.
- Stop later mutations after any failed gate.
- Observe deployment state after mutation.
- Execute rollback only when the declaration provides a rollback capability and
  the run has actually mutated state.

## Self-Hosted Agent Release

An executor declaring `lifecycle: detached-kubernetes-job` must run Helm only
inside the generated Kubernetes Job. The coordinator may create that one Job
but must not invoke `helm upgrade`, `helm install` or equivalent mutation in
its own process. The Job uses a separately declared, previously verified
executor image and passes the immutable candidate digest only to Helm for the
target workload. It uses the control Agent's declared host paths and exact
workspace chart, then runs `helm upgrade --install --atomic --wait`. Its active
deadline must exceed the Helm timeout by the declared rollback buffer. This
keeps Helm alive while Kubernetes replaces the Agent Pod.
The workflow's control target must use a different Gateway cluster/instance
identity from the workload being replaced. If those identities match, stop
before creating the Job.

## Shared Release Convergence

For a semantic composition, selected-service artifact resolution, target
availability and verification may run concurrently when their declared
dependencies permit. Deployment mutation is different: services sharing one
release require exactly one shared-release convergence.

Before convergence, observe the current image identity for every unselected
service and bind it as `preserve-observed`. Aggregate those identities with the
selected target digests and invoke the deployment executor once.

Artifact mismatch, unreadable target artifact, missing selected target digest,
or missing unselected service identity requires zero deployment executor
invocations. Return `blocked` with the missing or mismatched subject. Do not
silently select all services and do not let a `ServiceRelease` invoke Helm or
another shared-release executor independently.

After convergence, re-observe selected workload image IDs and every unselected
service identity. A changed unselected identity is a failed preservation gate,
not a successful partial release.

## Run Evidence

For an Ask request with `response_format=json`, the Gateway supplies the exact
session-local result file path. Write exactly one JSON object to that path using a
temporary file in the same directory followed by an atomic rename. The terminal
response is not the machine result. Do not create a YAML result or a second
result file.

```json
{
  "apiVersion": "doops.sh/v2",
  "kind": "DeploymentRun",
  "metadata": {
    "workflow": "example-release",
    "runId": "generated-by-runtime",
    "workspaceCommit": "immutable-pushed-workspace-identity"
  },
  "spec": {
    "mode": "dry-run",
    "inputs": {
      "version": "release-20260714"
    }
  },
  "status": {
    "phase": "blocked",
    "mutationCount": 0,
    "capabilities": {
      "source-observer": {
        "status": "available",
        "version": "runtime-reported-version"
      },
      "deployment-executor": {
        "status": "missing"
      }
    },
    "evidence": [
      {
        "subject": "source",
        "toolRef": {
          "tool": "Read",
          "input": {
            "path": "release/source.json"
          }
        },
        "observedAt": "2026-07-14T00:00:00Z"
      }
    ],
    "failures": [
      {
        "code": "required-capability-unavailable",
        "subject": "deployment-executor"
      }
    ]
  }
}
```

Evidence must contain observation time, a declared subject and a model-visible
`toolRef`. `toolRef.tool` is the exact runtime tool name and `toolRef.input` is
the exact JSON input used for that call. Do not store Secret values in tool
inputs. The Agent-authored evidence object contains exactly `subject`,
`observedAt`, and `toolRef`. `credentialRun` is instruction-only preparation
context and must not be copied into the result.
The Gateway resolves each exact reference to one completed call from the
accepted doagent turn. Only calls carrying `doops.tool-attestation/v1` from
the trusted reconciliation context are eligible; generic Bash, filesystem,
browser and unattested MCP calls are rejected. The resolved call's `id`,
`tool`, exact JSON input and exact result are injected without rewriting.

`metadata.workspaceCommit` must equal the commit returned by the preceding
workspace push. The Gateway resolves each exact tool-plus-input reference to
one completed ACP call, removes `toolRef`, and injects:

```json
{
  "subject": "source",
  "observedAt": "2026-07-14T00:00:00Z",
  "toolCall": {
    "id": "runtime-call-id",
    "tool": "Read",
    "input": {
      "path": "release/source.json"
    },
    "result": {
      "revision": "immutable-identity"
    }
  }
}
```

Only updates whose `turnId` equals the accepted prompt turn are eligible.
Replayed events, conflicting duplicate IDs, unknown, failed, non-terminal or
ambiguous references are rejected. A status message or admission text is never
evidence. `status.capabilities` must be a non-empty snapshot of the
runtime-discovered capabilities, including availability and version or identity
where reported; missing required capabilities must be represented as blocked
evidence, not hidden by a fallback.

The result is an execution report, not an admission acknowledgement. It must
not use `admitted` as a terminal phase. A dry-run result must set
`mutationCount: 0`; an apply result must report the observed mutation count
and current post-mutation evidence. Image generation and Agent delegation belong
to doagent multi-agent execution, not to the DoOps command or module surface.
Deployment evidence should cite the calls that observe, build, deploy, or verify
the declared release.

Allowed phases are `planned`, `converged`, `blocked`, `failed` and
`outcome-unknown`.

## Failure Rules

- Missing declaration: stop and report the exact path.
- Missing runtime module: stop; no fallback to another executor.
- Permission or credential reference unavailable: stop before mutation.
- Artifact mismatch: do not invoke the deployment executor.
- Deployment failure after mutation: collect actual state, then use only the
  declared rollback capability.
- Unknown mutation outcome: report `outcome-unknown`; do not retry or claim
  success without external state evidence.
