---
name: unified-workbench-progress
scope: doops-dosql-doweb
status: implementation-verification
---

# Unified Workbench Progress

## Objective

Merge DoOps, DoSql and DoWeb into the `doops.sh` runtime: one Console, one
`doops` user CLI, independent domain skills, one Gateway identity and exact
target/action grants. Remove duplicated identity/authorization, not resource
isolation, audit or execution-outcome evidence.

## Completion Checks

- One Gateway login serves the Console, `doops sql` and `doops web`.
- Missing login, denied target/action and wrong credential scope fail closed.
- SQL and website panels retain operation state and invalidate confirmation
  when the operation, target, session or credential reference changes.
- Real disposable Nginx serves A, then B, then A after rollback. Invalid config
  and failed health checks remain failures and leave A available.
- DoSql executes against disposable databases through the actual Gateway
  credential and restricted Agent tool path, with pre-execution journal and
  terminal verification evidence. A prompt wrapper alone does not pass.
- Production build, CLI smoke, browser login/logout, read requests, denied
  actions, empty/error/disabled states and desktop/mobile rendering pass.
- Canonical skills, user docs and runtime packaging describe implemented
  behavior. Production availability requires a separate immutable-artifact
  rollout and live acceptance; local passing checks are not deployment.

## Guardrails

- At most 6 implementation/verification rounds and 3 hours in this run.
- Stop after 3 unchanged implementation/verifier fingerprints; record the
  blocker and ask for the missing decision instead of weakening verification.
- Do not alter existing user grants, production data or credential payloads.
- Preserve all pre-existing workspace edits. Never weaken tests, approvals or
  Gateway policy to make a domain command appear usable.
- Permission-policy changes and production rollout require an explicit user
  decision. Pending decisions do not block independent local verification.

## October 7, 2026 State

- Current round: 5 of 6. Status: authentication hardening and final verification.

- Source already contains shared CLI prompt transport, DoSql/DoWeb Console
  panels, the bundled DoWeb script and restricted read/mutation MCP tools.
- Real Nginx functional verification passed: deployment A/B/C, repeated
  rollback C -> B -> A, refusal to roll back beyond A, failing health/config
  recovery, evidence-write failure with release-index restoration, effective
  snippet inclusion, snippet-health failure and recovery-reload unknown outcome.
- Session/workspace escape and read-tool mutation checks passed.
- DoSql still lacks a restricted database execution tool and an implemented
  managed-credential-to-database adapter path. This is a release blocker, not
  a reason to permit Bash, local connection files or secret-bearing prompts.
- Database panel credential selection and in-flight navigation protection now
  match the website panel. Both panels invalidate confirmation after edits.
- Browser verification passed login rejection/success, shared session,
  HttpOnly-token isolation, both domain request paths, wrong credential
  references, in-flight disabled navigation and logout -> protected API 401.
  The Gateway is real; the browser fixture Agent replies are synthetic and
  never count as real SQL/Nginx execution evidence.
- Desktop and 390px mobile screenshots exposed overlapping form controls;
  non-shrinking content sizing fixed the overlap and geometric checks passed.
- Ordinary apply no longer exposes website mutation tools. Only the exact
  `skill=doweb` path gets Web mutation authority and its shared Nginx lease;
  website requests do not inherit Helm mutation tools. No new user grants.
- Independent review confirmed stale disabled/revoked-token admission,
  anonymous attachment persistence and target-admin global read exposure.
  Scoped patches and real Store/HTTP/WebSocket checks are in progress.
- The user authorized all source changes to be committed and pushed together by
  the session-concurrency task. DoSql signed change approval and real database
  execution remain separate verification decisions; Gateway concurrency rollout
  does not authorize database mutations or claim unfinished DoSql paths work.
- No production deployment or production mutation performed in this run.

## Pending Architecture Decisions

- Database-resource credential binding and read/write scope in the Gateway;
  the restricted Agent database executor must not read local secrets as a
  substitute. Existing signed DoSql change approval remains unchanged.
- Anonymous Agent registration currently permits identity replacement by
  `cluster/instance`; the repository explicitly retains this design. Closing
  it requires a versioned target-bound enrollment/workload-identity rollout.
- Legacy Agent session IDs are not user-owned. Default owner isolation and
  explicit sharing require an agreed history/migration policy.
- These unresolved boundaries prevent reporting the entire suite as a
  completed secure production merge.

## Evidence

- Baseline full Agent and CLI `go test ./...` passed before the final safety
  patches. Full final verification must run again after hardening is integrated.
- `go test ./internal/server -run 'TestWeb|TestApplyWeb|TestDoagentAllowedTools|TestNginxLease' -count=1 -v -timeout 120s`
  passed real Nginx and exact domain capability/resource-lease checks.
- Targeted `-race` checks passed before the final token/attachment/admin patches.
- Console production build passed after the form-layout fix. `test:console`
  passed 22 checks; `test:gateway-policy` passed 5 checks before attachment hardening.
- A compiled CLI with an isolated config performed one real Gateway login,
  both domain transport requests, denied credential use and logout rejection.
  The fixture Agent is synthetic; no database-execution claim follows.
- Browser screenshots are outside the repository under `/tmp/doops-unified-*`.
  In-app browser invocation failed with `Transport closed`; installed local
  Playwright was used explicitly. Expected 401/403 responses are recorded;
  no unrelated JavaScript runtime failure was observed on the tested panels.

## Next Round

Read this file and the current diff first. Complete the independent review,
verify the remaining failure paths and browser workflow, and update this state
with actual tool results. Do not mark the overall merge complete while the
database execution path or required live acceptance is missing.
