---
title: DoOps SQL Skill Remediation
date: 2026-11-06
status: source-verification-complete-release-pending
scope: sql-contracts-and-skill-distribution
---

# SQL Skill Remediation

## Objective

Resolve the eight findings from the SQL/skill audit without adding a second SQL
mutation path, weakening target authority, or replacing unrelated work.
The source baseline is `e589e8506284cfd52d78b27536403f56ce0e2362`.
The isolated branch is `codex/sql-skill-contract-fixes`.

## Acceptance

- Documented SQL modes and credential arguments match the compiled CLI and Gateway.
- MySQL/Oracle DDL is not directed through an unsupported rollback dry-run.
- Removed commands and confirmation fixtures are not exposed as production capabilities.
- Node query retries cannot overwrite evidence or dispatch an already claimed operation.
- The release installer updates the requested project, canonical global skill, and
  existing Codex/Agents DoOps copies from the same CLI commit; config/auth stay intact.
- CLI smoke, real temporary-filesystem behavior, isolated SQL transactions, installer
  functional tests, relevant repository tests and an independent review pass.
- Commit/push visibility and CI are recorded separately from release/install/deployment.
  Unreleased source is never presented as the installed production behavior.

## Execution Limits

At most three repair/review rounds and 90 minutes for local implementation and
verification. Stop a failing path after two rounds with identical failures and no
new evidence; record the blocker and required external action. Do not weaken tests,
change authorization, publish unrelated work or bypass release provenance.
Reconstruct progress from this file after a context transition.

## Progress

| Area | Status | Verification |
| --- | --- | --- |
| F1/F3/F4/F5: command, API, guide and README contracts | Implemented and locally verified | Compiled CLI, real Gateway handler and temporary SQLite commit/read-back |
| F6/F7/F8: embedded DoSql command/evidence behavior | Implemented and locally verified | 91 Node CLI/filesystem checks, including independent processes and response-path conflicts |
| F2: release skill distribution | Implemented and locally verified | 29 installer checks, including whole-install rollback, retained failed-recovery backups and physical-path deduplication |
| Integration and independent review | Source review complete | CLI/Gateway/Agent packages passed; independent reviews closed; 311 Python checks passed with 3 reproduced baseline failures |
| Push and CI | Pushed; PR #85 checks in progress | CNB branch `codex/sql-skill-contract-fixes`; release remains blocked by baseline failures |
| Published artifacts, installation and target rollout | Pending | Matching commit and runtime checks; no manual hotfix |

## Decisions

- Keep natural-language query/plan/discover and direct SQL/file apply as implemented.
- Remove the production route to the confirmation test fixture; do not invent a new
  approval workflow.
- Reuse existing operation claim and immutable audit artifact helpers.
- Preserve the original dirty worktree and keep all remediation changes in this branch.

## Verification And Review Evidence

- CLI `go test ./... -count=1 -timeout=5m`: passed.
- Gateway `go test ./... -count=1 -timeout=5m`: passed.
- Agent SQL/credential/runner tests: passed in 20.536s. Full Agent
  `go test ./... -count=1 -timeout=10m` also passed; the server package took 412.232s.
- SQL documentation/API checks plus stale-contract and link scans: 86 passed.
  The API checks execute the documented UPDATE through the real SQLite executor
  and read committed rows back; no online database is contacted.
- Node skill checks: 91 passed. These now run through the existing Python CI
  entrypoint, including cross-process operation claims and immutable evidence.
- Both Skill frontmatter validations and `git diff --check`: passed.
- The scanning guide additionally had an obsolete path and invalid request
  shape. Its JSON example now runs successfully through the actual Node CLI;
  the guide distinguishes supplied-sample analysis from live database collection.
- Full Python suite before the added Node wrapper: 297 passed, 3 failed. All
  three failures also reproduce from the untouched base commit: the Helm chart
  enables host access while `test_adopt_agent`, `test_managed_agent_release_contract`
  and `test_model_settings_secret_contract` still assert no host mount/hostPID.
  Their deployment behavior and assertions are outside this SQL remediation;
  neither was weakened to obtain a green result.
- Final integrated Python run: 311 passed, the same 3 baseline failures, in
  92.89s. This includes the Node wrapper and all installer failure-injection
  checks. The branch is not release-ready while these existing checks are red.
- Independent installer review found partial replacement and equivalent-path
  deduplication defects. Both are repaired; all 29 installer tests pass, including
  second-destination docs failure, receipt write/replacement failure, first
  installation rollback, symlink restoration and explicit retained backups when
  recovery itself fails. No installed user files were changed by these tests.
- Independent command review found three remaining guidance defects: omitting
  the requested database after binding failure, unconditional MySQL credential
  keys, and conflating successful implicit DDL commits with partial failures.
  The CLI and docs now retain the database, distinguish engine credential
  shapes and distinguish `verified` from `partially_committed`. The Agent DDL
  dry-run error also no longer suggests switching to a PostgreSQL target.
  CLI tests and Agent SQL tests passed again after these corrections.
- Independent Node review found that response publication detected an existing
  output only after execution. A pre-dispatch response-path claim now prevents
  execution on collisions, and publication failures return machine-readable
  failure with the real execution outcome. Cross-journal protection covers
  claimed IDs; pre-upgrade unclaimed history in other journals is not
  automatically indexed, and the docs now state that boundary explicitly.
- Independent follow-up reviews closed the command-guidance and response-path
  findings with local reproductions. There are no remaining actionable review
  findings inside this change's agreed scope.
- CNB draft PR #85 was created after verifying the pushed source commit
  `b56234789515713fa46ebb9c42ef32defa36a891` on the remote. Automated review
  requested a clearer Node/Gateway error boundary: the docs now explicitly
  state that stderr fields are not parsed into a Gateway error schema by the
  existing Go wrapper. This is a documentation correction, not a new protocol.
  CI runs against the PR merge ref; inspect its checks for the final outcome.
