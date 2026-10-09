# Credential Setup Repair

Date: 2026-10-09
Branch: main

## Scope

Reuse DoOps credential storage and lifecycle. One CLI invocation configures a
credential from stdin or an explicitly selected target-local Kubernetes Secret.
Do not transfer secret values through shell output, prompts, or local files.
Existing unrelated DoSql runner changes are not owned by this repair.

## Acceptance

- Database grant and verification do not require workload or database asset flags.
- Configure imports/puts, grants, verifies all active scopes, and promotes.
- Failed import or verification never reports readiness or promotes.
- Source Secret import requires platform administration and an exact target,
  namespace, Secret and selected keys. It never changes the source Secret.
- Synthetic functional tests cover the HTTP/tunnel path and secret non-disclosure.
- Verify main branch, focused/full tests, diff, commit and remote visibility.

## Evidence

- Removed CLI workload requirements for database grants and verification.
- Added a private target-local Secret import endpoint using existing encrypted storage.
- Configure reuses existing resources and lifecycle, verifies all active scopes, and
  does not require database credentials to have an artifact digest.
- Import rechecks token and platform administrator authority after waiting and
  before persistence. Public RPC access to the import tool is rejected.
- Switched to main without losing pre-existing uncommitted changes.
- Live edu-coder target responded online and idle. No Secret value or SQL read.
- Live managed metadata contains agentcloud-edu-coder-postgres; the selected target
  usable view does not contain it. No new production resource was created.
- Real compiled CLI -> Gateway HTTP -> Agent WebSocket functional tests pass for
  import/stdin configuration, failed verification, revoked authority, missing source
  keys, missing encryption key, unavailable audit and secret non-disclosure.
  Kubernetes is simulated with synthetic Secret data; this is not a live rollout.
- Agent, Gateway and CLI full Go suites pass; 103 Python release contract tests pass.
- Credential import/configure and resource-lock race tests pass; compiled CLI help
  and invalid-argument checks pass. No test expectations were weakened to bypass
  the database Secret/artifact digest mismatch.
- Deterministic lock-wait tests reproduced revoked-token payload/verify and
  revoked-target verify requests returning HTTP 200. Post-lock authorization
  rechecks now reject all three with HTTP 403; the full credential test group passes.
- Independent review closed all three identified defects. Production acceptance
  remains pending; source delivery is recorded in main Git history and CNB CI.
- Installed CLI remains v1.3.31. No new release or production configuration has
  been applied by this repair. CNB main fetch succeeds. GitHub HTTPS fetch recovered
  after a transient TLS failure; its main branch is 660 commits behind CNB main.
