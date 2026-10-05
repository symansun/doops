---
title: Minimal execution concurrency repair
date: 2026-10-05
status: implementation
---

# Scope

- Separate verified terminal Helm failure from missing execution evidence.
- Replace the Gateway-wide credential mutex with sorted credential-ID locks.
- Remove human-declared execution reconciliation without rewriting historical audits.
- Keep opaque Shell mutations and genuine unknown outcomes conservative.
- No new protocol, environment switch, TTL unlock, or deployment registry.

# Acceptance

- A trusted failed Helm result ends the apply as failure, never success or unknown.
- A successful sibling tool cannot hide a failed or unfinished mutation.
- Missing output, mismatched evidence and interrupted mutations remain guarded.
- Different credentials can run concurrently; shared credentials remain serialized.
- Cancellation while acquiring multiple credentials releases every acquired lock.
- Manual reconciliation sends no CLI HTTP request; the HTTP POST cannot change audits.
- Live pilot passes exec, structured queries, and a controlled apply failure followed
  by another command. Existing business workloads must remain unchanged.

# Delivery

Functional verification precedes commit, CNB main merge, immutable release build,
CLI installation, Agent pilot, and Gateway cutover. Retain prior image digests and
Gateway binary backup. Do not interrupt unrelated work to deploy this patch.
Reassess after two failed repair attempts for the same scenario. Stop production
cutover on a failed pilot, provenance mismatch, or unverified operation outcome.

# Progress

- Verified source and live Gateway/ZYJ Agent baseline: v1.3.17, 24531ce.
- Verified known Helm failure remains a failed terminal response and permits a
  follow-up command in the Agent WebSocket functional fixture.
- Implementation, independent review, CI and live rollout are not yet complete.
