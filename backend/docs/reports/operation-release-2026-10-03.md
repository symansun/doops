---
name: operation-lifecycle-release-2026-10-03
scope: release-and-live-acceptance
owner: doops-maintainers
status: release-blocked
---

# Release Objective

Publish the operation lifecycle repair from a reviewed, remotely visible commit.
Use the successful main CI Agent/Gateway artifact and a matching versioned CLI
release. Confirm the rollout scope before changing Agent workloads or the
production Gateway: the executionLifecycle requirement rejects shell, SSH,
background and prompt calls to Agents without the new protocol.

# Completion Evidence

- Agent, Gateway, CLI and contract checks pass on the final source tree.
- Independent review finds no unresolved release-blocking defects.
- Commit, main CI, release tag, image digest and CLI/Skill identity agree.
- Authorized Agents advertise executionLifecycle before the Gateway switches.
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

- Agent rollout scope remains unanswered. The recorded Gateway baseline has 12
  online identities; switching the Gateway before all authorized Agents support
  the protocol would reject their shell, SSH, background and prompt operations.
- Browser acceptance verified the production-build operations table, all four
  states and disabled cancellation controls. The cancellation confirmation dialog
  blocked further in-app browser automation. Native Codex-app control was denied
  by the tool's safety policy; human dismissal is required before that UI path
  can be completed. No successful cancellation interaction is claimed.
- The replacement Git process path passed local macOS functional and race tests.
  Docker daemon inspection did not respond, so Linux/container process cleanup
  has not been verified in this continuation. The current PR's CI job runs
  Python contracts, not the Go suites; do not infer Linux execution acceptance
  from a green ci-contracts result.

Keep the source review branch out of main and do not create a release tag or
switch production services while these release blockers remain.

# Additional Verification

- Exact lifecycle capability forwarding and CLI schema validation pass.
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
