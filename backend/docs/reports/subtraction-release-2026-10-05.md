---
name: doops-subtraction-release-2026-10-05
scope: gateway-and-affected-agents
owner: doops-maintainers
status: verification
---

# Objective

Restore authorized operations without changing historical audit outcomes or
adding expiry, protocol, compatibility or manual-unlock layers. Remove
project-specific policy from the generic Helm executor.

# Acceptance And Bounds

- Unknown outcomes remain unknown and are never automatically replayed.
- A historical audit does not block a new explicitly authorized request.
- Running/canceling work stays serialized by its actual resource lease.
- Audit write failures remain fail-closed while finalization is still active.
- Chart-specific images, ordered values overlays and non-sensitive overrides
  render with real Helm; managed authentication/scope/digest tests still pass.
- Main and release CI pass; Gateway binary and Agent digests identify the same
  release source. ZYJ and edu exec, structured reads and apply-failure recovery pass.
- Preserve previous Gateway binary and managed Helm revisions for rollback.
  Reassess after two failures of the same rollout step; no blind mutation replay.

# Observed Baseline

- Gateway and affected Agents still serve v1.3.17 / 24531ce.
- v1.3.18 / 4ef51b7 is published and CI-passed but not active on these targets.
- ZYJ audit 598858: session amp-zyj-proxy-fix-20261005c, ended
  2026-10-05T00:33:38Z, outcome_unknown; a fresh exec was rejected by this audit.
- edu audit 598875: session agentcloud-release-20261005, ended
  2026-10-05T01:01:31Z, outcome_unknown.
- ZYJ Agent baseline: namespace ai, release/deployment doops-agent, generation 13,
  Ready 1, image digest ac1ef2968dc9d2a840f8f255440b8cd6eb95b22702ac21b82ac2b3f8b9b39a8e.
- Gateway host ubuntu@106.54.197.139 is reachable using existing SSH authorization.

# Verification

- Real Helm rendering and managed Chart scope/digest checks pass.
- Cancellation, live serialization, unknown-audit persistence and follow-up
  WebSocket execution checks pass. Targeted integration suite passes.
- Python contract suite: 50 passed. CLI package suite passed before final help edit;
  full Agent/Gateway/CLI recheck is running.
- Independent review identified remote values double-fetch and same-repository
  version coupling; both are removed before release.
- No Worktree; unrelated untracked .agent and .qa-console content is excluded.

# Rollout

Pending source commit, main merge, immutable v1.3.19 CI, installation, Gateway
cutover, managed Agent upgrades and live post-cutover evidence. Source tests do
not prove that the online chain has recovered.
