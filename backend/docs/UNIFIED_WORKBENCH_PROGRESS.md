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

- Source already contains shared CLI prompt transport, DoSql/DoWeb Console
  panels, the bundled DoWeb script and restricted read/mutation MCP tools.
- Real Nginx functional verification passed in this run: deployment A/B,
  rollback, failing health/config recovery and structured snippet update.
- Session/workspace escape and read-tool mutation checks passed.
- DoSql still lacks a restricted database execution tool and a implemented
  managed-credential-to-database adapter path. This is a release blocker, not
  a reason to permit Bash, local connection files or secret-bearing prompts.
- Database panel credential selection and in-flight navigation protection are
  being aligned with the existing website panel.
- Independent reviewers are checking Gateway authorization and DoWeb failure
  handling. CLI tests and production Console build are running.
- The user authorized all source changes to be committed and pushed together by
  the session-concurrency task. DoSql signed change approval and real database
  execution remain separate verification decisions; Gateway concurrency rollout
  does not authorize database mutations or claim unfinished DoSql paths work.
- No production deployment or production mutation performed in this run.

## Evidence

- `go test ./internal/server -run 'TestWeb|TestApplyWeb|TestPromptCredential|Test.*Session.*Concurrency' -count=1 -v -timeout 120s`
  passed the four matched DoWeb checks, including real local Nginx. The pattern
  matched no separate prompt/concurrency test; those need explicit/full checks.

## Next Round

Read this file and the current diff first. Complete the independent review,
verify the remaining failure paths and browser workflow, and update this state
with actual tool results. Do not mark the overall merge complete while the
database execution path or required live acceptance is missing.
