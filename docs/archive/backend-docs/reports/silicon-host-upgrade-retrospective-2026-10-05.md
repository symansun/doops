---
name: silicon-host-upgrade-retrospective
scope: standalone-docker-agent-recovery
owner: doops-maintainers
status: runtime-verified
---

# Confirmed Failures

1. The first access check assumed SSH port 22. The real management port is 2022,
   already represented in local known_hosts. Failure on the wrong port was
   incorrectly elevated into a missing-access conclusion. Both user-supplied
   hosts authenticated on 2022; no password is recorded in this document.
2. Silicon's old Agent reports heartbeats but emits notifications without
   request_id. Gateway rejects these frames and disconnects the execution stream.
   The published v1.3.17 Gateway already enforced this check; v1.3.20 did not
   introduce it. Heartbeat-only acceptance concealed an existing broken channel.
3. The native upgrade relies on the old Agent's upgrade/observe interfaces and
   supports only a managed Helm Deployment. A legacy standalone Docker Agent
   cannot install that controller through the same broken interface. The former
   prohibition on host Docker publishing incorrectly forced Kubernetes migration
   as an unrelated prerequisite to Agent recovery.
4. Fast-command execution used Cmd.StdoutPipe/StderrPipe while Cmd.Wait ran in
   parallel. Wait could close the read ends before their readers consumed stdout,
   yielding a successful terminal result with missing output.

# Actual Deployment

- Authorized target: gw-silicon / doops-silicon/vm-49, host 49.51.244.8,
  SSH user ubuntu, port 2022, hostname VM-0-15-ubuntu.
- The second supplied host 43.173.87.215 / VM-0-6-ubuntu was accessed read-only;
  no Agent replacement on that host is included without scope confirmation.
  It is a separate online identity doops-sub2api/vm-43, image v1.2.16 / source
  784bc7ea3f198f7ec88806ff40f43342163421af, not gw-silicon/vm-49. The local
  target inventory had omitted it. Its management port is also 2022.
- Existing container: doops-agent, Docker network host, privileged=true,
  restart unless-stopped, no Compose management labels and no kubectl/helm.
- Existing command uses loopback 127.0.0.1, Agent health port 42223 and the
  original Gateway/cluster/instance. Preserve these values, do not assume 42222.
- Existing Agent home and authoritative doagent settings are already bind-mounted;
  preserve their paths and read-only settings mount without copying payloads.

# Root Repair

- Add an explicit external host Docker release controller, not an automatic
  fallback or an Agent-process self-replacement. Native Helm upgrade is unchanged.
- Require verified host/user/port/container and configured target identity.
  Do not silently default SSH recovery to port 22.
- Pin image digest, image labels and actual Agent binary source/release before
  stopping the old container. Dry-run only inspects preloaded artifacts/config.
- Preserve Docker configuration in memory through the local Engine API; neither
  public command arguments nor receipts contain original environment payloads.
- Retain the old container, serialize the host transaction, write metadata-only
  receipts, and verify running image/version, Gateway takeover and real RPC output.
- Failed startup or acceptance restores the recorded old container externally.
  Old/new ownership changes fail before rollback mutations. Dry-run plus rollback
  is rejected before any operation. A rollback to an old broken protocol is not
  declared a successful new-version upgrade.
- Independent review reproduced two additional recovery defects: the shell
  installed its rollback trap after replacement, and interrupted rollback could
  delete the replacement then fail permanently on its missing container.
  Arm recovery before replacement; persist removal intent in the existing receipt
  and continue only the proven exact-ID removal. Do not swallow other Docker errors.
- Automate foreground double-stream, background output/exit, structured SSH argv
  dispatch and expected failure, and successful follow-up probes. A version and
  capabilities declaration alone must not complete the host release.
- Keep request_id validation and credential/permission boundaries intact. No
  temporary Agent identity, release-runner or Kubernetes installation is introduced.

# Verification

- Actual SSH authentication and exact-host Docker inventory completed on both hosts.
- Actual gw-silicon read-only controller preflight passed for the published
  v1.3.20 image/source; the old container remained running.
- Focused controller transaction, preservation, credential-canary redaction,
  preflight-denial, rollback and ownership tests are machine-checked.
- Controller source 4af6f8512fd48da22b3705e7f78675ad5fccea63 passed PR CI
  cnb-btj-1k45bueu6 and merged as 1354fdf67b8b4f5cf11be1b44892a4bc2ac3ca7b.
- Production replacement completed. Original container remains stopped as
  doops-agent-rollback-silicon-v1320-20261005. The same Gateway identity now
  advertises v1.3.20 source ecd60945; node-info and streaming/background RPCs work.
- Host networking, privileges, restart policy and mounts were preserved.
  Sub2API instances retained their original running state and health.
- Final verification also found an existing raw-execution race: Cmd.Wait closed
  StdoutPipe/StderrPipe readers before fast-command output was consumed.
  This is repaired by explicit reader ownership and bounded drain, not a sleep
  workaround.

## Fixed Runtime Acceptance

- PR 55 merged as a935905ab63e2b08e370df06898906b7765a7031 on main;
  v1.3.21 Agent digest is
  bcfee296a6d76cb8e6a8d25bfe0544283c5ccaf5d7d2c9f9a11a5cffdf1fd5a3.
  Main CI cnb-th1-1k45e86eh and release CI cnb-e7g-1k45eafjt both succeeded.
- On 2026-10-05 the real host container
  003940bb14f62595090bd31e7eaab317b74566be300c9aab36eb7dab1573a546,
  its image labels, Agent binary and Gateway release_commit agree on that source.
  Command, entrypoint, environment, user, working directory, HostConfig and mounts
  were compared with the retained old container and match. Mount order is irrelevant.
- Exact production RPC tests passed 40 fast stdout/stderr calls, same-session
  concurrent output isolation, foreground and background terminal handling.
  All 89 streaming notifications matched each call's Root RequestID; 46 terminal
  frames were observed. Structured SSH dispatched real argv and reported the
  expected connection failure, not successful destination authentication.
- Cancellation of the task owned by this acceptance returned failure/unknown,
  not a false success. Its observed process disappeared and subsequent exec passed.
  Gateway ended idle with active/queued 0. Old unresolved audits remain readable
  but do not participate in admission; no records were deleted or rewritten.
- Browser Console selected vm-49, executed fast stdout/stderr plus binary version
  in session browser-silicon-final-20261005, rendered both streams and the exact
  v1.3.21 source, and displayed Operation complete. No console error/warn was observed.
- Original and v1.3.20 rollback containers remain stopped and available. Business
  Sub2API containers retained their original IDs, start times and healthy state.
- The revised controller's read-only host preflight and its exact live functional
  probe passed on vm-49; 100 local contracts passed, including lost SSH reply,
  incomplete functional acceptance, interrupted rollback retry and ownership denial.
  Controller changes require their own pushed-source CI before another replacement;
  they do not require a new Agent image or protocol version. vm-43 upgrade scope
  remains an explicit question.
