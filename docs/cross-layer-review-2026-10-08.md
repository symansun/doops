---
title: DoOps cross-layer code review
date: 2026-10-08
scope: doops.sh
status: findings-only
baseline: 89fed13f348868de07cc19f0963eb0bcb9371db6
---

# Scope And Evidence

Reviewed the current `fix/credential-guidance-20261008` working tree, including
pre-existing uncommitted changes. No worktree, implementation changes, production
mutation, commit, push, or release was performed. This report is the only added
repository artifact.

Scope: canonical Skill and usage contracts, CLI, Gateway admission and leases,
Agent execution and terminal evidence, and Web transport/state/UI.

Evidence labels:

- **Reproduced:** a local command, functional fixture, or browser observation.
- **Code-path proof:** a reachable producer/consumer mismatch with precise source
  locations; not a claim that the failure was injected into production.
- Existing synthetic fixtures are not evidence of production availability.

# High-Priority Findings

## R1 [P1] Shell Audit Parsing Can Crash The Agent

**Code-path proof; harmless shell portion reproduced.**

`backend/agent/internal/server/handler_ws.go:759-763` detects a substring in the
command and indexes `strings.Fields(parts[1])[0]` without checking its length.
`: # kubectl apply -f -f x` is a successful no-op shell command, but the split
produces an empty `parts[1]`. The execution goroutine at `handler_ws.go:611-618`
has no panic recovery, so this audit convenience can terminate the entire Agent,
interrupting unrelated requests and losing their terminal responses.

Minimal direction: remove command-text-based configuration extraction, or make
this optional audit parsing total and incapable of affecting execution delivery.
Validation: run the exact no-op through a disposable real Agent and verify that
it and an independent request both complete and the Agent remains connected.

## R2 [P1] Browsing A Directory Can Execute Its Name As Shell

**Code-path proof; harmless command substitution reproduced by the Web reviewer.**

`components/files-panel.tsx:101` interpolates a directory into
`ls -1Ap "${dir}"`. Directory names returned by the target become clickable paths
at line 561. Double quotes still permit command substitution, so a name such as
`$(printf READ_ONLY_MARKER)` is evaluated by the Agent's `bash -c` at
`backend/agent/internal/server/handler_ws.go:2723`.

This is an execution boundary violation: browsing a directory from an untrusted
checkout can execute commands with the operator's remote execution authority.
Minimal direction: treat paths as data through a structured listing API or the
existing literal-shell-argument quoting helper, not double-quote interpolation.
Validation: list and enter literal names containing `$()`, backticks, quotes,
spaces, and newlines without executing their contents.

## R3 [P1] The Zombie Reaper Competes With Command Owners

**Code-path proof; wait-status theft reproduced at syscall level by the reviewer.**

`backend/agent/internal/server/gateway.go:73` starts a reaper which calls
`Wait4(-1, WNOHANG)` every 30 seconds at line 125. It does not distinguish orphaned
children from processes owned by `exec.Cmd`. For example,
`backend/agent/internal/server/k8s_tool.go:306-317` waits for streamed output before
calling `cmd.Wait()`, leaving an exited child available to the global waiter.

If the reaper wins, the actual owner loses the exit status and can receive
`ECHILD`, reporting an incorrect failure or exit code. The syscall reproduction
returned exit code 17 to the global waiter and `No child processes` to the owner.
Minimal direction: use one wait owner per managed process; separate orphan
reaping from managed execution rather than patching the reported exit code.
Validation: test the production `NewGateway()` path across a reaper tick with
successful and nonzero-exit commands and delayed output consumers.

## R4 [P1] Reconnection Can Reopen A Session Before Cleanup Finishes

**Code-path proof.**

`backend/agent/internal/server/handler_ws.go:290-298` cancels calls on disconnect
but does not join them. `backend/agent/cmd/agent/main.go:172-177` then reconnects,
with a default delay of one second. Process cleanup gives SIGTERM-resistant
children two seconds before SIGKILL (`handler_ws.go:2822-2829`). Meanwhile,
`backend/agent/internal/server/tunnel_hub.go:775-777` creates fresh lease maps for
the replacement connection.

A same-session mutation can therefore enter through the replacement connection
while the previous execution is still cleaning up. This is distinct from
retaining historical unknown audits as global locks.
Minimal direction: join connection-owned execution cleanup, or retain active
execution ownership across connection generations until its bounded cleanup
completes. Do not restore historical target-wide audit quarantine.
Validation: disconnect a disposable Agent during a TERM-resistant task and
attempt a same-session request during reconnection; execution must not overlap.

## R5 [P1] Structured Helm Rollback Always Uses An Invalid Flag

**Reproduced argument rejection; code-path proof of generated command.**

`backend/agent/internal/server/helm_tool.go:172` constructs
`helm rollback <release> <revision> --atomic`. Local execution of
`helm rollback --atomic` exits 1 with `Error: unknown flag: --atomic`, without
contacting a cluster. The reviewer also checked the pinned Helm 3.14.4 flag
registration. `runHelmCommand` reduces this to `helm_mutation_failed`.

Minimal direction: remove the invalid rollback flag and retain supported wait,
timeout, and result verification. Validation must use the shipped Helm binary's
argument parser, then a disposable release rollback, not an always-successful
fake `helm` executable. This finding concerns the structured workload rollback,
not every separate Agent-upgrade rollback controller.

## R6 [P1] Helm Ownership Does Not Cover Mutation Through Verification

**Code-path proof; previously recorded design gap revalidated.**

`backend/agent/internal/server/tunnel_hub.go:2455-2457` maps apply to a generic
target mutation and `exclusiveLockKey` at line 1915 narrows it to session.
Different sessions may then enter `handleHelmTool` concurrently. That function
(`helm_tool.go:41-68`) does not claim namespace/release ownership.

After mutation, `verifyHelmDeploymentState` (`helm_tool.go:561-576`) fetches the
current release manifest without pinning this operation's revision. A second
session may change the release between the first mutation and its verification.
Without an expected image, the first session can verify the second session's
current state; even with an expected image, other spec changes are not bound to
the first operation. Helm's pending-operation checks do not close this
post-mutation verification window.

Minimal direction: acquire exact namespace/release ownership at the structured
Helm execution boundary and hold it through verification of this operation's
result. Do not parse the natural-language prompt or reinstate cluster-wide
exclusion. Validation: same release serializes, different releases proceed, and
one session cannot accept another session's manifest as its own result.

## R7 [P1] CLI Silently Discards Raw JSON Output

**Code-path proof confirmed independently by two reviewers.**

`backend/skills/doops-cli/cli/doops/client.go:527-540` treats any output suffix
that parses as a JSON object as a legacy Agent event, ignoring notification
`kind`. `{"ok":true}` has no recognized event type, so `formatAgentEvent` prints
nothing. The code still sets `streamedAny=true`; final result text is then skipped
and the CLI exits successfully.

A business log shaped like a `step_finish` event can also produce an erroneous
human-visible completion message. This affects deterministic exec/bg output and
prompt output flowing through the same formatter.
Minimal direction: follow the typed notification kind; raw bytes must not be
reinterpreted as Agent events. Validation: complete and fragmented JSON lines,
prefixed JSON, and event-shaped business JSON remain byte-visible.

## R8 [P1] Web Stop Claims Completion Before Remote Terminal Evidence

**Code-path proof.**

`components/terminal-panel.tsx:73-75` immediately prints "已中断" after aborting
the browser request, and its finally block clears running state.
`app/api/rpc/route.ts:199-207` sends `tools/cancel` and immediately closes the
WebSocket, abandoning the original terminal response. Database and website
panels use the same aborting transport.

The Gateway may still be waiting for cancellation or may record unknown. The UI
therefore suggests a stronger result than the execution owner knows, encouraging
unsafe retries. Minimal direction: distinguish stop requested, confirmed stopped,
and outcome unknown, and retain the original terminal observation. Validation:
slow cancellation, ACK-only cancellation, and cancellation racing completion.

# Cross-Layer Contract Findings

## R9 [P2] Known Admission Rejection Becomes Unknown

**Code-path proof.**

`handler_ws.go:1576-1578` recognizes `doagentPromptRejected` but returns
`-32603 doagent_prompt_failed`. The rejection classifier at lines 2235-2250
explicitly identifies pre-admission errors. Gateway
`execution_lifecycle.go:118-120` does not classify the rewritten code as
pre-admission, and `tunnel_hub.go:1828-1830` converts it to unknown for apply.

No mutation started, but the client receives uncertainty instead of the known
rejection. Preserve a definitive rejected/not-started terminal classification.
Validate apply, not only ordinary ask, through the complete Gateway/Agent path.

## R10 [P2] Web References Invalidate The Command Digest

**Reproduced by the Web reviewer in an in-memory functional fixture.**

`lib/agent-session-transport.ts:65-72` clones an already-digested command and
changes its prompt when database/repository references are present, leaving
`payloadDigest` unchanged. The installed SDK's server command verifier rejects
this as `command_payload_digest_invalid` before Gateway dispatch.

No-reference prompt succeeded; adding `db-synthetic` caused rejection with no
new Gateway dispatch. Incorporate references before constructing the command
envelope, preserving command ID, digest, and retry identity as one contract.

## R11 [P2] Web Retains An Unknown Operation Forever

**Reproduced by the Web reviewer in a synthetic Gateway fixture.**

`lib/server/agent-session.ts:107` retains uncertain `active`, line 81 rejects all
subsequent commands, and lines 155-156 remove command capabilities. A healthy
metadata refresh does not release this historical operation. The session TTL
at line 30 also excludes active sessions, so these entries do not expire.

Gateway releases the finished operation after recording its unknown result
(`tunnel_hub.go:1372`), and direct same-SID requests can succeed while Web still
rejects the SID. New Web SIDs work, but consume another entry in the 100-session
process-local map.

Separate a historical unknown result from an active in-flight operation.
Preserve the error and prohibit automatic mutation replay without permanently
locking the conversation or leaking its active entry.

## R12 [P2] Web Reintroduces A Business-Specific Tag Gate

**Reproduced by the Web reviewer.**

`lib/deployment.ts:35-38` only permits `release-YYYYMMDD`. Both the UI and Web
server call this function. Agent `helm_tool_test.go:244` explicitly accepts
ordinary tags such as `v2.3.1`, and the canonical Skill prohibits a global
business-version-format gate.

A complete valid form using `v2.3.1` fails before RPC. Remove the date convention,
keeping image-reference validation and publisher provenance responsibilities.

## R13 [P2] Dashboard Errors And Refresh Do Not Reflect Current Data

**Code-path proof; isolated render/query-count reproduction by the Web reviewer.**

`components/dashboard-panel.tsx:81-110` turns jobs errors into an empty list,
instance errors into null, and ignores non-403 audit errors. These can render as
"no jobs/no records", rather than unavailable or denied.

The loading effect at line 114 depends on `targets.length`, while the refresh
button at line 143 only refreshes targets. If target count stays constant,
audit/jobs/managed-instance data remains stale after explicit refresh.
Preserve each API's loading/error/permission state and refresh its actual source.

## R14 [P2] Target And Audit Status Projections Are Inconsistent

**Code-path proof; status-option/render observations by the Web reviewer.**

- `components/console-shell.tsx:135-138` retains the old selected target when it
  disappears from the authoritative visible list; the current-machine card
  still says "online connection 1" at `dashboard-panel.tsx:300`.
- `dashboard-panel.tsx:560` treats `busy=false` as idle, despite Gateway allowing
  active session operations with `busy=false`.
- `lib/gateway.ts:75` omits `executing` and `outcome_unknown` from audit filters;
  `components/audit-panel.tsx:169` treats only `running` as in progress.

Use the authoritative target list and distinct online/active/target-exclusive
states, and align the audit status projection with the Gateway enum.

## R15 [P2] Unguarded Audit Finalization Can Leave Permanent Running Rows

**Code-path proof.**

Ordinary ask/read-only operations can remain in audit status `running`
(`execution_lifecycle.go:50-54`). If their `FinishAudit` fails,
`client_service.go:134` treats `MarkExecutionUncertain` returning `sql.ErrNoRows`
as finalized. That update only matches `executing` (`execution_audit.go:35`).
The owner is released without retrying the real terminal record. Restart
recovery likewise only handles `executing`.

Zero affected rows is not proof of a terminal audit. Retain bounded, accountable
terminal persistence for both dispatched guarded and ordinary running records;
validate a read-only completion with injected audit-write failure.

# Skill And CLI Documentation Drift

The reviewer additionally traced these concrete mismatches. They are secondary
to the execution defects and should be corrected with the corresponding owning
behavior, not by adding compatibility paths:

- `backend/skills/doops-cli/cli/doops/main.go:51` promises session-workspace
  execution, but foreground `executeRawCommand` does not set `cmd.Dir`; the
  background execution path also inherits the Agent process directory.
- `backend/docs/DOOPS_USAGE_GUIDE.md:516` and `DOOPS_API_GUIDE.md:542` demonstrate
  absolute file paths while `workspace_upload.go:169` rejects absolute paths.
- `backend/docs/DOOPS_API_GUIDE.md:514` says ask permission for a section including
  apply, while `actionForTool` maps apply to exec permission.
- `backend/docs/DOOPS_API_GUIDE.md:41` says HTTP is not rejected by clients, while
  CLI `gateway_targets.go:169` rejects non-loopback insecure Gateway access
  unless explicitly enabled.
- README still describes unknown outcomes as blocking subsequent mutations,
  unlike the current canonical Skill and Gateway implementation.

# Verification And Non-Findings

Executed in the main review:

- Gateway/Agent targeted session, Nginx, concurrency, internal-finalization and
  proxy tests passed.
- All `TestExecutionGuard`, `TestExecutionLifecycle`, `TestSession`, and
  `TestNginx` scenarios passed.
- Console suite: **33 passed, 0 failed**. Some current tests explicitly assert
  the divergent Web unknown-session behavior and date-tag restriction; passing
  these tests does not establish cross-layer agreement.
- `git diff --check` passed before the report was added.
- Local Helm parser rejected `--atomic` on rollback without a cluster call.
- The shell part of R1 exited zero locally; the Agent crash was not injected
  into an online target.
- Browser: local admin login, dashboard, AI/deployment navigation and unavailable
  state were inspected. The available synthetic Agent lacked the ACP event
  metadata required by the current Web, correctly leaving the composer disabled.
  No successful real AI conversation or production deployment is claimed.

Additional reviewers ran Agent/Gateway suites and protocol/CLI fixtures; those
results are supporting evidence, not production acceptance.

Confirmed non-findings:

- Historical unknown audit rows no longer block new Gateway admission.
- Different sessions execute concurrently; same-session operations and Nginx
  resource operations retain their respective mutual exclusion.
- The background-only executionLifecycle gate is consistent across ordinary
  CLI/Gateway dispatch. It is not the general cause of current foreground blocks.
- The normal finalizer does not overwrite persisted success with unknown:
  `MarkExecutionUncertain` only matches still-executing records.
- Production Web data paths proxy Gateway. No automatic switch to demo/mock was
  found; misleading empty/stale displays are a separate defect.
- Current uncommitted changes already populate and display `effective_lock`;
  the earlier missing-field report should not be repeated against this tree.

Release recommendation: address the confirmed P1 execution, output-integrity and
rollback defects before treating the chain as production-verified. Do not use
manual unlock, compatibility fallback, or a new global audit lock as a repair.
