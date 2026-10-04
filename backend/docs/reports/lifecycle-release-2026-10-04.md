---
name: lifecycle-release-2026-10-04
scope: doops-release
owner: doops-maintainers
status: preparing
---

# Objective

Retire the executionLifecycle capability without removing terminal evidence,
cancellation, auditing or mutation protection. Publish a single reviewed release
and restore real output and terminal results through CLI, Gateway and Agent.
No Worktree, compatibility routing, forced unlock or unrelated release cancellation.

# Acceptance And Bounds

- Local Agent/Gateway/CLI suites and Python contracts pass; exact-commit Linux CI passes.
- Source is pushed and visible on main; immutable image and CLI assets identify that commit.
- Released CLI and Skill are installed together.
- Inventory every Agent using the shared Gateway before changing that Gateway.
- Ali gw-114-k8s and Tencent gw-zyj return matching release identities and real
  foreground/background output; failures remain failures, queries stay usable.
- Gateway handoff, resource cleanup, request-correlated output, health and rollback
  evidence must pass before claiming completion.
- At most three repairs per failed evidence group, 20 minutes per CI observation
  and 10 minutes per rollout. Never replay an unknown-outcome mutation.

# Observed Baseline

- Installed CLI v1.3.11: 94bd23ff1b9026ac2be136d58c69f52a02d2f38f.
- Ali/Tencent Agent: 7e605c48a848373a9992368021fdc3f7f3807a5c, v1.3.10.
- Gateway host binary reports release and commit
  148a97c77c0048504c2f1d6afa486d07dbe1ba27, and systemd reports active.
- Gateway is older than the lifecycle-gate removal and request-ID routing change.
  Foreground probes return only "Operation complete." with no command output.
  This is not functional acceptance.
- JM and edu-coder do not advertise a release commit. Shared Gateway cutover
  requires checking and upgrading its remaining consumers, not just the two pilots.
- Administrative operation list was empty. No operation was canceled.
- Uncached Agent, Gateway and CLI suites and all 50 Python contracts passed
  before the additional review fixes.
- Final local Agent/Gateway/CLI suites, 50 Python contracts, Agent go vet,
  targeted outcome/routing race checks and independent source review passed.
- Ali rollback baseline: kube-system/doops-agent-gateway, Helm revision 41.
- Tencent rollback baseline: ai/doops-agent, Helm revision 4.
- Both pilots use image digest
  sha256:733277e6afb5ca7eda2e143b55c3e6cc14d91cbfd5db96a1c33f2ff90d8ea17d.
- Other connected consumers lacking a release commit: JM, SCU-new, ZY, HDU,
  silicon and edu-coder. edu-coder has an unrelated active release; it is not
  canceled or preempted. SCU-old is not visible to this Gateway login.

# Progress

- [x] Locate mixed running releases and misleading retired-protocol CLI errors.
- [x] Fix review-confirmed finalization, workspace, proxy and bounded-write gaps.
- [ ] Review, test, commit and push.
- [ ] Verify main/tag CI and immutable release assets.
- [ ] Install release CLI/Skill and upgrade Agents with rollback evidence.
- [ ] Deploy matching Gateway only after consumer readiness.
- [ ] Complete live functional and health acceptance.
