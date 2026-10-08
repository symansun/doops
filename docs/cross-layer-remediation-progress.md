---
title: DoOps cross-layer remediation and main merge
scope: doops.sh
status: source-verified
---

# Goal

Close the confirmed cross-layer review defects, preserve session-scoped
concurrency, and merge verified source into CNB main without a worktree.
Do not claim production rollout from local fixtures or a source merge.

# Boundaries

- Preserve unrelated DoSql runner changes and local QA/install artifacts.
- Do not rewrite unknown audits, replay uncertain mutations, or add target-wide
  locks, compatibility branches, or manual production hotfixes.
- Helm concurrency remains allowed; acceptance must identify its own revision.

# Steps And Evidence

1. Review existing partial repairs against `cross-layer-review-2026-10-08.md`.
   Check actual code paths and reproduce remaining failures.
2. Repair Agent cleanup, Helm attribution, CLI output, and Web state contracts.
   Run real process/RPC fixtures plus browser success/error/cancel paths.
3. Run Agent, Gateway, CLI, Python contracts, Web tests/build and diff checks.
   Independently review the resulting scoped diff.
4. Commit only reviewed repair files, push, confirm required CI, merge main,
   and fetch to prove remote commit visibility.

# Progress

- HEAD and fetched CNB main have identical trees, despite different merge history.
- Historical unknown audits no longer participate in Gateway task admission.
- Existing partial CLI and Helm repairs still had output/revision races.
- Agent no longer parses shell text for incidental audit file snapshots or reaps
  managed children with a competing global Wait4. Connection shutdown joins
  execution cleanup before reconnect; the Gateway fences surviving ownership
  across connection generations.
- CLI preserves raw output and sends request-scoped cancellation on timeout,
  draining the original RPC without replay. The Web uses the same cancellation
  contract and does not mark a stop request as completed.
- Unknown Web commands release the active slot after their terminal failure;
  their command IDs cannot be replayed. Reference augmentation rebuilds the
  payload digest while retaining retry identity.
- Helm rollback no longer uses unsupported --atomic. Upgrade verification is
  pinned to the mutation's returned revision and unique description, without
  adding a release lock or accepting a concurrent release as its own.
- Audit terminal writes preserve original evidence on retry. Audit-only
  finalization does not keep execution leases. Restart closes abandoned running
  rows as error and guarded executing rows as outcome_unknown.
- Dashboard refresh queries every source, shows failures as unavailable, clears
  disappeared targets, and distinguishes active work from exclusive busy state.
- Local checks passed: CLI and Gateway full Go suites; 42 console tests plus 5
  Gateway policy tests; 103 Python release contracts; Next production build
  including TypeScript. The staged Agent source snapshot passed the full Go
  suite (server package 246 seconds) and targeted race checks (108 seconds).
- The initial full dirty-tree Agent run failed. Its unrelated DoSql runner
  changes remain excluded; the isolated staged Agent tree passed. An archive
  without .git cannot run three installer provenance fixtures, so the passing
  repository run and required CNB CI must supply that evidence, not a weakened
  installer test.
- Browser evidence: production-mode local Web on 127.0.0.1:43109, real Gateway
  and Agent on 127.0.0.1:43110, synthetic account only. JSON and event-shaped
  business output remained visible; the audit-parser no-op did not disconnect
  the Agent; stop displayed pending before an unknown terminal result, and a
  new command in the same session completed. Dashboard refresh showed those
  actual audits; browser error/warning logs were empty. The 390px viewport had
  document scrollWidth 390px.
- Existing live Ali (gw-114-k8s), Tencent (gw-zyj), and edu (gw-edu-coder)
  accepted foreground and background marker commands through the installed
  v1.3.31 CLI. Each background RPC reached ExitCode 0; all three advertised
  executionLifecycle ready. This proves baseline reachability, not rollout of
  this unmerged repair.
- Follow-up browser inspection found absolute paths passed to workspace-only
  file APIs. Open/save/new/upload now use relative paths; navigation is restricted
  to the session root. Four path-contract scenarios pass. A production Web build
  on 127.0.0.1:43209 and explicitly synthetic Gateway on 43108 verified literal
  dollar-expression directory navigation, relative file reads, edits and save
  feedback with no browser errors. This fixture is not production data.
- Independent CLI/Helm and Gateway/Agent reviews found no remaining merge
  blockers after removing lock-label and bare-401 authority guesses. The final
  build passed; cancellation/finalization functional scenarios also passed five
  repetitions.
- Follow-up for PR #69: reproduced Git pre-dispatch cancellation and global/user
  execution-limit rejection receiving a generic error instead of their known
  cause. Both early returns now persist canceled/rate_limited through the existing
  terminal writer. Regression scenarios verify the HTTP result, final audit,
  no Agent dispatch, and no remaining lease or active owner.
- Required Linux CI exposed nondeterministic conflict-owner reporting: iterating
  the active-operation map could report a later waiter instead of the oldest
  conflicting owner. The failure reproduced locally over repeated runs. Selection
  now uses registration time and an ID tie-break, without changing conflict
  eligibility; regression coverage retains the original oldest-owner assertion
  and covers tied timestamps, same-connection owners, finalization, and skipped IDs.
- Non-blocking limits: persistent audit outages retain evidence/retry goroutines
  without execution leases. Live Kubernetes rollback and rollout of these changes
  are not claimed.
- Source verification is complete. CNB required CI and main merge are recorded
  by the pull request and Git history; they are separate from production rollout.
