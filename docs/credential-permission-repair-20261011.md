# Target Credential Repair

Date: 2026-10-11. Branch: main. No Worktree.

## Verified Failure

The production Gateway is v1.3.28 and edu-coder Agent is v1.3.29.
agentcloud-edu-coder-postgres has two active bindings and no stored versions.
Audit 617843 records a failed internal scope-planning prompt followed by
credential_invalid; audit 617821 records a planning response followed by the same
generic rejection. The caller is platform administrator; adding another action
alone would not restore this request.

## Acceptance

- Given only target ask/exec authority, operators can use its configured credentials
  without credential:use or a separate per-user grant.
- Database bindings resolve from stored metadata, with no model scope-planning call.
- Target mismatch, revoked credentials, absent versions and revoked operation
  authority fail before SQL dispatch and report the underlying error category.
- Secret values never appear in prompts, audit, process arguments or public output.
- Deploy the CI artifact to Gateway, install its CLI/Skill, upgrade edu-coder,
  import the known Secret internally and verify an actual read-only database query.

## Progress

Target permission implementation, CLI/Gateway tests and 103 contract tests pass.
34 real Gateway HTTP/WebSocket scenarios pass with a simulated Agent, including
ask/exec-only operators and queued revocation. They do not prove SQL execution.
Independent review closed query/materialization lock mismatch, name-based refresh,
namespace drift and token revalidation issues. Production rollout and read-only
database acceptance remain pending. No business DDL has been replayed.
