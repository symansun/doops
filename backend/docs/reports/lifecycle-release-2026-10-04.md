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
- [x] Review, test, commit and push c2937a50bd03719c88a8ece50aacb80185374f06.
- [ ] Verify main/tag CI and immutable release assets.
- [ ] Install release CLI/Skill and upgrade Agents with rollback evidence.
- [ ] Deploy matching Gateway only after consumer readiness.
- [ ] Complete live functional and health acceptance.

# Continuation Evidence

- PR 44 contains c2937a50bd03719c88a8ece50aacb80185374f06 and is remotely
  visible. Its Linux CI cnb-kqq-1k42f7sm8 failed the real OpenSSH exit-1 and
  exit-42 scenarios; the earlier PR 43 run failed exit-254. These failures
  must be resolved, not bypassed or relabeled as successful.
- Fresh local Agent/Gateway/CLI suites and all 50 Python contracts pass.
  A separate review found no additional confirmed admission, notification
  correlation or lease-finalization defect in c2937a5.
- The loopback SSH test sent exit-status and immediately closed the underlying
  transport. It now closes the session channel and waits for the client to
  disconnect within the existing 10-second deadline. Exit-code and unknown-outcome
  assertions are unchanged. The same CNB image on Linux/amd64 passes 20 repetitions;
  macOS passes five repetitions. Independent review confirms the missing
  transport-shutdown synchronization; exact-commit CI revalidation is pending.
- Fresh production inventory found 12 connected, idle Agents. Ali and Tencent
  advertise 7e605c48a848373a9992368021fdc3f7f3807a5c; the other 10 have no
  release commit. The shared Gateway still runs 148a97c77c0048504c2f1d6afa486d07dbe1ba27.
  An Ali foreground output canary still returns only "Operation complete.".
  No production operation was canceled, no unknown outcome was unlocked and
  no production binary was replaced during this verification.
