---
name: operation-lifecycle-release-2026-10-03
scope: release-and-live-acceptance
owner: doops-maintainers
status: review-required
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

- Actual gitkit v0.4.0 commands are not tied to request cancellation. Early body
  or output-copy failures send SIGTERM and start an asynchronous Wait. A handler
  return therefore does not prove its process group has stopped. The new Git
  terminal-response fix must not ship until command ownership and synchronous
  process-group termination are repaired against this real dependency path.
  This is a release blocker even though ordinary Git push/fetch and custom
  context-aware-handler cancellation tests pass. Keep the source review branch
  out of main and do not create a release tag or switch production services.
- Agent rollout scope remains unanswered. The recorded Gateway baseline has 12
  online identities; switching the Gateway before all authorized Agents support
  the protocol would reject their shell, SSH, background and prompt operations.
- Browser acceptance verified the production-build operations table, all four
  states and disabled cancellation controls. The cancellation confirmation dialog
  blocked further in-app browser automation. Native Codex-app control was denied
  by the tool's safety policy; human dismissal is required before that UI path
  can be completed. No successful cancellation interaction is claimed.

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
- Concurrent local dev/build activity produced malformed generated dev types and
  route 404s. The source tree was intact. Both local servers were stopped; the
  generated dev tree was preserved at /tmp/doops-next-dev-20261003-0920 and
  TypeScript verification was rerun against the release build's generated types.
