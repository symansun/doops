# Credential Plan Protocol Convergence

## Goal

Use one strict declaration compiler for credential preparation and one
single-use Gateway prompt binding. Declaration schemas are explicitly
versioned, while every Gateway and Agent uses one credential-plan protocol.

## Completion Criteria

- Credential preparation and the prompt binding consume the same declaration
  source identity and immutable revision.
- The v3 registry selects one explicit environment and one explicit service.
- Agent registration advertises `doops.sh/credential-plan/v2`.
- doagent `initialize` advertises the complete
  `doops.reconciliation-context/v2` capability.
- Gateway credential preparation rejects incompatible Agents before tool
  dispatch with a deterministic protocol error.
- Repository contract tests validate every checked-in deployment root.
- Every retained Gateway target runs an Agent with the required protocol.

## Supported Contracts

- Deployment root: `deploy/environments.yaml`,
  `doops.sh/v3 EnvironmentRegistry`.
- Each request selects exactly one declared `environment` and one declared
  `service`.
- Gateway-Agent credential protocol: `doops.sh/credential-plan/v2`.
- Agent-doagent reconciliation capability:
  `doops.reconciliation-context/v2`.

Legacy v2 workflows are not a compatibility path and are rejected.

## Repository Guarantees

- Credential prepare owns the strict workflow compiler. The prompt carries only
  the resulting metadata-only `CredentialRun` and the same source identity.
- v3 required Secret references marked undeclared block during credential
  prepare.
- Registry selection and prompt binding use the same environment and service
  inputs.
- Agents advertise `doops.sh/credential-plan/v2`; Gateway prepare rejects
  offline targets with 503 and incompatible Agents with 426 before dispatch.
- Apply requires `ask` and `credential:use`, a compatible Agent, and one exact
  short-lived prepared run bound to the user, target, session, workflow and
  workspace commit. The binding is consumed once before dispatch.
- CI/CD prompt dispatch rejects doagent before model execution unless its
  capability includes the exact schema, call-context schema, MCP meta key,
  credential-run digest binding, and source workspace-commit binding.
- Credential prepare failures use a versioned JSON error object consumed
  strictly by the CLI.
- Repository and external contract tests compile declared deployment roots.

## Fleet Completion

Repository convergence is necessary but not sufficient. Completion requires
every retained target to reconnect through Gateway with the required protocol,
all environment declarations to compile, required credential references to be
declared, and final dry-run evidence to pass for each environment.
