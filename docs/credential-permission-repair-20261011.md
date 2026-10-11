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
namespace drift and token revalidation issues. No business DDL has been replayed.

## Production Findings And Full Repair

- v1.3.35 / 2610cf90804e165ca84190ff61d250ba30fdf2a4 passed CNB main/tag CI and
  was installed for CLI/Skill, Gateway and edu-coder. Gateway health, Agent Job,
  serving Pod/image and generation 105 were verified.
- The PostgreSQL resource had no versions. Removed its unused opaqueSecret binding,
  imported DB_PASSWORD from agents/agentcloud-secrets on the private control channel,
  built the connection URI internally, verified and promoted credver_c8fb251041584e9560937fde.
- A temporary user with only edu-coder targets:list/ask can list and materialize this
  credential without credential actions or a credential grant. Browser login succeeded.
- Actual SELECT 1 failed in the old DoSql adapter before SQL dispatch. Its persisted
  evidence is failed. CLI exit 0 meant only the model turn completed, not SQL success.
- Full repair removes model scope planning for every credential reference and matches
  authorization by target/use. Deployment dimensions remain runtime metadata.
- Helm binds native authentication without downloading an older Chart. Agent lease
  admission no longer compares namespace/release/chart; current Chart is validated
  and staged once for dry-run and mutation. Origin protection remains for secret delivery.
- Integrate the existing Go Job database runner, select the client image by engine,
  resolve optional database metadata from live target inventory, return bounded query
  output, and reject a failed SQL tool as a failed RPC/CLI operation.
- Next checks: focused/full tests, self-review, main commit/push, immutable release,
  Gateway/edu upgrade, ordinary-user SELECT 1 with verified journal/evidence, denied
  revoked target access, browser credential view, and temporary-account cleanup.
- Browser reproduced an independent blocker: target view coupled target credentials
  to managed-list permission and erased all rows on its HTTP 403. Removed the extra
  request and kept target view read-only with its own loading/error/empty states.
- Local focused permission/configure/import/Helm/DoSql tests pass. CLI suite,
  Gateway suite, 103 Python contracts and 63 DoSql Node tests pass. Mac full Agent
  run hit an unrelated SQLite BUSY lifecycle fixture; that test passed three isolated
  repeats. Exact-commit Linux CI remains a release requirement.
