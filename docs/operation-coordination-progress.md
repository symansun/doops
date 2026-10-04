---
title: Operation coordination remediation
date: 2026-10-03
status: lifecycle-rollout-remediation
---

# Scope

Repair the existing Gateway/Agent execution lifecycle before introducing
release-scoped distributed ownership. The user subsequently requested release
and deployment. Release preparation and live acceptance are tracked in
backend/docs/reports/operation-release-2026-10-03.md; confirm the Agent scope and
protocol readiness before switching the production Gateway. Do not cancel
unrelated production operations or install unreleased binaries.

# Acceptance

- Cancellation retains ownership until the original execution reaches a terminal
  response. Cancellation acknowledgement alone is not completion.
- Missing terminal evidence is persisted as outcome_unknown and blocks subsequent
  target mutations, including after Gateway restart or Agent reconnection.
- unlock refuses active, queued, or unresolved work. Explicit administrator
  reconciliation records confirmation, reason, and evidence as manual evidence.
- Waiting for a resource does not consume a user execution slot.
- Independent structured workspaces can write/synchronize concurrently; operations
  on the same workspace cannot overlap. Unknown shell effects stay conservative.
- Background execution retains its operation until process termination.
- Operations report waiting/running/cancel_requested and blocking ownership.

# Work

- [x] Gateway cancellation, persistent uncertainty guards, safe administration.
- [x] Agent prompt/background cancellation and Gateway-owned terminal state machine.
- [x] Admission order, workspace scope, CLI operation status.
- [x] Documentation, protocol/process tests, independent review.

# Verification Bounds

Run targeted local protocol/process tests, then Agent and CLI package suites and
contract checks. At most three repair passes per failing group before reassessing
scope and root cause. No unknown-outcome production retries. Record
remaining failures and evidence here; do not weaken validators to pass.

Agent, Gateway and CLI suites, Agent go vet, 50 Python contracts, execution-guard
scenarios and targeted race checks pass. Final independent review identified a
release blocker in gitkit's asynchronous process cleanup on abnormal Git
cancellation. The continuation reproduced a surviving process group after an
output-copy failure, then replaced that dependency path with request-owned Git
HTTP execution using the existing synchronous process-group cleanup.
Input/output failure, cancellation during initialization, failure after HTTP 200,
and a real Gateway/Agent Git push with TERM-resistant receive-hook descendants
pass focused functional checks. Independent review then reproduced delayed EOF
being misclassified as failure and missing hooks after interrupted initialization;
both were repaired and their real-Git regression scenarios pass. Final Agent,
Gateway and CLI suites, Agent go vet, 50 Python contracts, compiled CLI help and
two repetitions of Git-focused race checks pass. Independent re-review reports
no new source release blockers.

The user authorized all Agents and explicitly requested main merge/publication.
Linux Agent/Gateway/CLI and Git process race checks passed for fa1a98a in
cnb-gr9-1k3vqigpe; the matching frontend browser recheck has 27 passes. Final
review found ordinary SSH nonzero exits falsely causing quarantine; that repair
must pass exact-commit CI before merge. Runtime preflight also found unmanaged
Agents requiring a separate migration plan. Those runtime constraints block
Gateway cutover, not reviewed source and artifact publication. No Worktree is
used. Evidence and rollout limits are tracked in the release report.

# Deferred Design

Release/database identity across different Agent routes, executor fencing,
atomic multi-resource claims, and priority handoff require a separate protocol
and rollout. Existing application mutation serialization is not removed by this
patch, and this patch must not be described as implementing those capabilities.
