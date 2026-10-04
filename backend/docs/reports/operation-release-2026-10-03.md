---
name: operation-lifecycle-release-2026-10-03
scope: release-and-live-acceptance
owner: doops-maintainers
status: release-in-progress
---

# Release Objective

Publish the operation lifecycle repair from a reviewed, remotely visible commit.
Use the successful main CI Agent/Gateway artifact and a matching versioned CLI
release. On 2026-10-03 the user authorized all components and all online Agents.
Inventory exact identities before changing workloads or the production Gateway:
the pre-repair lifecycle capability gate incorrectly rejected operations based on
an Agent declaration. The repaired contract removes that capability gate and
uses one Gateway-owned terminal state machine for foreground and background
operations, while preserving terminal-evidence guards for mutations.

# Completion Evidence

- Agent, Gateway, CLI and contract checks pass on the final source tree.
- Independent review finds no unresolved release-blocking defects.
- Commit, main CI, release tag, image digest and CLI/Skill identity agree.
- Agents used for release must return request-correlated terminal responses;
  no separate lifecycle capability declaration is required.
- Gateway executable hash and source commit match the verified CI artifact.
- The registered Agent baseline reconnects and existing services stay healthy.
- Live exec, cancellation, background lifetime, operation status and audit checks
  pass. A canceled acknowledgement alone does not prove execution stopped.
- Rollback artifacts and any untested paths are recorded.

# Bounds

Use at most three repair passes per failed group and three attempts per production
artifact. Stop after three unchanged failure results, a provenance mismatch or a
failed rollback. Each CI observation has a 20-minute deadline; the release has a
90-minute execution ceiling before reassessing with the user. Do not weaken
validators or retry a production mutation whose outcome is unknown.

# Current Evidence

- Repository: /Users/wwyz/Documents/l8ai/doops.sh.
- Initial working branch: fix/session-operation-coordination; repair was not
  committed when this release preparation began.
- Current main and released v1.3.8 source:
  0e9b5ef42898aaa94eed0a3f0a669d68b4653999.
- Main CI cnb-vkb-1k3v5kjls and tag CI cnb-nfg-1k3v68fdt succeeded.
- Gateway host 106.54.197.139 reports that source commit and an active systemd
  service. Installed CLI reports the same commit.
- Main Agent artifact provenance reports linux/amd64 digest
  sha256:e0098ebe4664b3296a9366eaf72bf351875ad027158803bacf60b75f9034b6fd.
- Gateway, CLI and 50 Python contracts passed; Agent go vet passed.
- Initial full Agent suite failed: internal execution events omit actor/token
  and add events not accounted for by existing admin audit checks; cancellation
  now returns HTTP 202 rather than the old HTTP 200 contract.
- Repaired those failures without reducing permission or audit assertions. Full
  Agent, Gateway and CLI package suites now pass; 50 Python contracts and Agent
  go vet pass. Four execution-guard regression scenarios pass.
- Independent review identified target mutation/workspace overlap, mismatched
  clean workspace locks, replacement-connection ownership and raw-output keyword
  matching. All four have scoped repairs and regression coverage; final review
  and full race verification remain.
- gw-edu-coder is online and the Gateway active operation list is empty.
- Read-only Gateway inventory reports 12 online Agent identities. Scope question
  is pending: all Agents on doops.l8ai.cn, or gw-edu-coder acceptance only with no
  production Gateway switch.
- No production mutation or new release tag has occurred in this release
  attempt. Source is being prepared for WIP review only. Installed Skill copies
  and .qa-console are excluded from the source commit.

# Release Blockers

- Source: independent review confirmed that ordinary SSH nonzero exits are
  incorrectly quarantined. Repair using structured exit evidence and rerun
  exact-commit CI before merge.
- Runtime: the user authorized all online Agents and then explicitly requested
  main merge and publication. Refreshed inventory has 11 online identities;
  114 is unavailable and another authorized chat is investigating DiskPressure.
  JM, SCU-new and ZYJ have no managed Helm release; silicon has no available
  Kubernetes upgrade endpoint. Do not adopt those workloads or bypass the
  managed release controller without a separate migration plan.
- Do not switch Gateway until every authorized consumer passes terminal-response,
  cancellation, unknown-outcome and request-correlation checks, and every structured
  SSH consumer supports `structuredSSH`. Source merge, release assets and runtime
  deployment are separate acceptance states.

Linux Agent/Gateway/CLI and Git process race acceptance passed in
cnb-gr9-1k3vqigpe for fa1a98a52d081459a4dcecc47f8f004662cde0df.
The production-build browser recheck at
/tmp/doops-frontend-recheck-20261003-yOygLt/browser-results.json reports 27 passes,
including dismiss/accept confirmation and the HTTP 202 cancellation path.
Current frontend hashes match its source-baseline.sha256. This is synthetic
Gateway browser evidence, not proof of production process termination.

# Additional Verification

- Unified Gateway terminal-state and CLI protocol validation pass.
- Original-error delivery during disconnection, undispatched internal rejection,
  actual background stdout/exit evidence and split UTF-8 output pass.
- Targeted race verification for lifecycle, background and Agent Git cancellation
  passes; the resistant-descendant cancellation case passes five repeated race
  runs after bounded permission-probe observation was repaired.
- Full Agent, Gateway and CLI suites pass; 50 Python contracts, 5 Gateway-origin
  checks and 7 console checks pass. The four frontend files passed production
  build. These do not establish safe cancellation of gitkit's abnormal paths.
- Local dev generated malformed type files and returned route 404s. Trace review
  shows build completed before dev started; concurrent build activity is not an
  established cause, and the 404 root cause remains unresolved. The source tree
  was intact. Both local servers were stopped; the
  generated dev tree was preserved at /tmp/doops-next-dev-20261003-0920 and
  TypeScript verification was rerun against the release build's generated types.
- WIP source commit ea4c11aea658880e8f0cbdd3de44d28d8b808b82 was pushed and
  remotely verified on fix/session-operation-coordination. PR 37 targets main and
  is explicitly WIP. CNB run cnb-e8u-1k3vmkjqt reports a failed ci-contracts
  pipeline because the required phrase "共享 mutation 锁" was split across two
  document lines (49 passed, one failed). The wording line wrap was repaired
  without weakening the validator. No merge, tag or deployment
  was performed. Codex could not attach the CNB PR URL as an app artifact because
  the URL was not accepted by its artifact tool; the CNB PR itself exists.

# Current Continuation

- Work continues in the existing checkout on fix/session-operation-coordination;
  no Worktree is created or used, and no production lock is canceled.
- gitkit was removed from Agent/Gateway dependencies rather than retained as an
  alternate execution path. Git's three supported smart-HTTP routes preserve
  repository initialization and the existing workspace post-receive hook.
- A real Git push starts a TERM-resistant post-receive hook and descendant.
  Gateway cancellation preserves cancel_requested ownership until both stop,
  records outcome_unknown, and rejects a subsequent request until reconciliation.
- An execution failure after HTTP 200 now emits an unknown-outcome terminal
  instead of success EOF. Input streams are closeable on command termination.
- The Skill's SSH statement now distinguishes the CLI's direct-prefix check
  from the caller's requirement to use structured SSH for wrapped commands.
- Independent review reproduced two additional defects: normal upload-pack
  could finish before the request EOF and be falsely quarantined, and init
  cancellation could leave an otherwise valid repository without its required
  hook. Both now have real-Git regression evidence. Normal completion waits for
  EOF while remaining cancellable; every admitted request checks repository
  completeness and installs the required hook before serving Git.
- Final Agent/Gateway/CLI suites, Agent go vet, 50 Python contracts, compiled CLI
  help and git diff --check pass. Git-focused race scenarios passed twice on the
  final source. Independent re-review found no new source release blockers.
- This clears the gitkit source blocker, not the deployment blockers above.
  No production mutation, release tag, main merge or installed CLI/Skill
  replacement occurred. Remote visibility and PR CI must be checked after push.

# Authorized Release Run

- Start: 2026-10-03 10:24 Asia/Shanghai. Scope is all DoOps components and online
  Agents, as explicitly authorized by the user. The 90-minute run ceiling and
  per-failure retry limits above still apply.
- First verify Linux execution and browser interaction, then merge and obtain
  main CI artifacts, then upgrade Agents before the Gateway. Install the matching
  versioned CLI/Skill and verify actual runtime identities and affected paths.
- Do not commit pre-existing .agent or backend/agent/.qa-console runtime files,
  use Worktrees, cancel unrelated releases, or weaken protocol checks.

# Runtime Preflight On 2026-10-03

All reads used session release-20261003-inventory and performed no mutation.

| Target | Live workload | Release baseline | Upgrade constraint |
| --- | --- | --- | --- |
| gw-edu-coder | kz-ops/doops-agent-live | Helm rev 16; sha256:731e34e1127eb55ed53772026662f2c82829699e1da4243972680db3aeb7c8e2 | structuredSSH ready; controller still to verify |
| gw-hdu-235 | ai/doops-agent | Helm rev 12; sha256:b72f26ce37d49dc5daf0413ce4136d6e9b15e7a5bfaa636ec708d1ff9ed9791a | old Agent; external controller required if upgrade tool absent |
| doops-235/hdu-235-release-v131 | ai/doops-release-runner-v131 | Helm rev 1; sha256:0402bef2d0a7873fad33a252c89e83ef368c20e4a57fdfe168f18eb66d6ac99b | existing legacy runner, not created by this release |
| gw-zy | default/doops-agent-239 | Helm rev 8; sha256:b72f26ce37d49dc5daf0413ce4136d6e9b15e7a5bfaa636ec708d1ff9ed9791a | old Agent; external controller required if upgrade tool absent |
| gw-jm | doops-system/doops-agent | sha256:b72f26ce37d49dc5daf0413ce4136d6e9b15e7a5bfaa636ec708d1ff9ed9791a | no Helm release |
| gw-scu-new | doops-system/doops-agent | sha256:0402bef2d0a7873fad33a252c89e83ef368c20e4a57fdfe168f18eb66d6ac99b | no Helm release, no Helm managed-by label |
| gw-zyj | ai/doops-agent | sha256:7f8582052367bf54f2ff052e530a8782b1e96a6e26178a4a4fc95a5c0039eb15 | no Helm release, managed-by=bootstrap |
| gw-silicon | hostname VM-0-15-ubuntu | not established | no service-account namespace; Kubernetes endpoint localhost:8080 refused |

Other online identities still to inventory are doops-sub2api/vm-43,
doops-yaml-probe/oilan-1 and keyi/host-10-66-3-109. No broad restart, forced
unlock, workload adoption or production credential change is authorized by this
inventory.
