---
title: Operation coordination remediation
date: 2026-10-03
status: review-required
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
- [x] Agent prompt/background cancellation and lifecycle capability.
- [x] Admission order, workspace scope, CLI operation status.
- [ ] Documentation, protocol/process tests, independent review.

# Verification Bounds

Run targeted local protocol/process tests, then Agent and CLI package suites and
contract checks. At most three repair passes per failing group before reassessing
scope and root cause. No unknown-outcome production retries. Record
remaining failures and evidence here; do not weaken validators to pass.

Agent, Gateway and CLI suites, Agent go vet, 50 Python contracts, execution-guard
scenarios and targeted race checks pass. Final independent review identified a
release blocker in gitkit's asynchronous process cleanup on abnormal Git
cancellation. Source review can proceed, but main merge, release tags and
production deployment must wait for that repair, confirmed Agent rollout scope
and completed live acceptance. Full race and browser evidence limits are tracked
in the release report.

# Deferred Design

Release/database identity across different Agent routes, executor fencing,
atomic multi-resource claims, and priority handoff require a separate protocol
and rollout. Existing application mutation serialization is not removed by this
patch, and this patch must not be described as implementing those capabilities.
