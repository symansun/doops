# Credential Plan Protocol Convergence

## Goal

Use one strict declaration compiler for credential preparation and trusted
CI/CD admission. Declaration schemas are explicitly versioned, while every
Gateway and Agent uses one credential-plan control-plane protocol.

## Completion Criteria

- Credential preparation and trusted reconciliation consume the same parsed
  workflow and configuration context.
- `SemanticRelease` catalog entries are resolved relative to the composition
  document and validated as `ServiceRelease`.
- v2 and v3 declaration roots, referenced documents, and path semantics are
  documented and tested.
- Agent registration advertises `doops.sh/credential-plan/v2`.
- Gateway credential preparation rejects incompatible Agents before tool
  dispatch with a deterministic protocol error.
- Repository contract tests validate every checked-in deployment root.
- Every retained Gateway target runs an Agent with the required protocol.

## Supported Contracts

- v2 execution roots: `DeploymentTemplate` and `SemanticRelease`.
- v2 configuration source: `EnvironmentRegistry`.
- v2 referenced release document: `ServiceRelease`.
- v3 deployment root:
  `deploy/environments/<environment>/environment.yaml`
  `EnvironmentDefinition`.
- v3 referenced documents: `EnvironmentService` and `ServiceDefinition`.
- Gateway-Agent protocol: `doops.sh/credential-plan/v2`.

The v2 and v3 declaration paths are explicit public contracts, not fallback or
silent compatibility paths. Mixed versions and v2 `EnvironmentDefinition`
configuration sources are rejected.

## Repository Guarantees

- Credential prepare and trusted reconciliation share one workflow compiler and
  one environment context.
- Semantic roots load and validate every referenced `ServiceRelease`.
- v3 roots strictly validate every referenced `EnvironmentService` and
  `ServiceDefinition`.
- v3 required Secret references marked undeclared block during credential
  prepare.
- v2 configuration sources require an explicit `EnvironmentRegistry`.
- Agents advertise `doops.sh/credential-plan/v2`; Gateway prepare rejects
  offline targets with 503 and incompatible Agents with 426 before dispatch.
- Apply requires `ask` and `credential:use`, a compatible Agent, and one exact
  short-lived prepared run bound to the user, target, session, workflow and
  workspace commit. The binding is consumed once before dispatch.
- Credential prepare failures use a versioned JSON error object consumed
  strictly by the CLI.
- Repository and external contract tests compile declared deployment roots.

## Fleet Completion

Repository convergence is necessary but not sufficient. Completion requires
every retained target to reconnect through Gateway with the required protocol,
all environment declarations to compile, required credential references to be
declared, and final dry-run evidence to pass for each environment.
