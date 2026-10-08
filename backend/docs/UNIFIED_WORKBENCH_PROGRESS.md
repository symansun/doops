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

- Current round: 6 of 6. Status: DoSql discovery/reference integration and final verification.

- Source already contains shared CLI prompt transport, DoSql/DoWeb Console
  panels, the bundled DoWeb script and restricted read/mutation MCP tools.
- Real Nginx functional verification passed: deployment A/B/C, repeated
  rollback C -> B -> A, refusal to roll back beyond A, failing health/config
  recovery, evidence-write failure with release-index restoration, effective
  snippet inclusion, snippet-health failure and recovery-reload unknown outcome.
- Session/workspace escape and read-tool mutation checks passed.
- DoSql now has a restricted database execution tool and managed database
  credential lease path. Live target execution is still unverified; this is a
  release verification blocker, not a reason to permit Bash, local connection
  files or secret-bearing prompts.
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
- DoSql now exposes `mcp_dosql_read` and `mcp_dosql_execute` with runtime-bound
  sessions. Read mode supports `operation=discover`, which scans only Pod,
  Service, Deployment and StatefulSet metadata and never reads Secret values.
  It returns stable database candidates and marks candidates missing a name as
  needing an exact credential/selection step.
- Database credentials use a dedicated `use=database` scope while reusing the
  existing encrypted Opaque Secret lifecycle. Scope binds target, database
  asset, environment, engine, database name and canonical Secret keys.
- DoSql execution reuses the existing journal/evidence/signed-approval CLI
  executor. Target-local MySQL, PostgreSQL and MongoDB wrappers validate the
  DoOps-owned Secret metadata before creating an ephemeral client Pod.
- Conversation context can carry metadata-only database asset references and
  repository references (`repo_id@branch`) from the Web context panel or CLI
  `--database-ref` / `--repo-ref`. References provide task context only; they
  do not grant access or carry credentials.
- 2026-10-08: `config/assets.json` and the display projection in
  `config/resources.json` contain the discovered ZYJ PostgreSQL asset
  `db_zhiyong_zyj_agents_postgres_agentcloud` for
  `gw-zyj/doops-zyj/control-plane-1/agents/postgres/agentcloud`. This registers
  metadata only; no Secret, psql connection or schema mutation was performed.
- Independent review confirmed and fixed stale disabled/revoked-token admission,
  anonymous attachment persistence and target-admin global read exposure.
  Store/HTTP/WebSocket checks pass in the local synthetic Gateway.
- The user authorized all source changes to be committed and pushed together by
  the session-concurrency task. DoSql signed change approval and real database
  execution remain separate verification decisions; Gateway concurrency rollout
  does not authorize database mutations or claim unfinished DoSql paths work.
- No production deployment or production mutation performed in this run.

## Pending Verification / Architecture Decisions

- Real target acceptance remains pending: the current workstation has no
  `mysql`, `psql`, `mongosh`, kind/k3d cluster, or registered disposable target.
  The wrappers and fake-kubectl tests pass, but this is not evidence of a live
  database query or mutation.
- The production Agent image must include the DoSql skill and retain `kubectl`
  permissions to create only the short-lived, Secret-bound client Pods for
  exact database scopes. PostgreSQL/MongoDB client image pulls and target RBAC
  must be verified through GitOps/CI before rollout.
- Anonymous Agent registration currently permits identity replacement by
  `cluster/instance`; the repository explicitly retains this design. Closing
  it requires a versioned target-bound enrollment/workload-identity rollout.
- Legacy Agent session IDs are not user-owned. Default owner isolation and
  explicit sharing require an agreed history/migration policy.
- These unresolved boundaries prevent reporting the entire suite as a
  completed secure production merge.

## Evidence

- Full final Agent `go test ./... -count=1 -timeout 360s` passed after the
  DoSql discovery, credential, MCP and conversation-reference integration.
- `go test ./internal/server -run 'TestWeb|TestApplyWeb|TestDoagentAllowedTools|TestNginxLease' -count=1 -v -timeout 120s`
  passed real Nginx and exact domain capability/resource-lease checks.
- Targeted `-race` checks passed before the final token/attachment/admin patches.
- Console production build passed after the form-layout fix. `test:console`
  passed 22 checks; `test:gateway-policy` passed 5 checks before attachment hardening.
- A compiled CLI with an isolated config performed one real Gateway login,
  both domain transport requests, denied credential use and logout rejection.
  The fixture Agent is synthetic; no database-execution claim follows.
- New targeted checks pass: DoSql discovery with fake Kubernetes metadata,
  credential scope exactness, SQL MCP schema/session binding, DoSQL result
  redaction, CLI conversation references, and frontend TypeScript.
- Real disposable PostgreSQL verification passed through the bundled DoSql
  executor and `docker-db-client.sh`: a read-only query returned `verified`,
  wrote journal/evidence under the managed artifact root, and the temporary
  container was stopped and removed afterward. This proves the executor/client
  path, not the full Gateway/Agent target path.
- Existing live Gateway read-only baseline on `gw-114-k8s` confirmed the target
  contains PostgreSQL, MongoDB and MySQL services/pods across `agents`, `dev`,
  `hoj`, `oilan-system` and `shenben`, including service ports 5432, 27017 and
  3306. This was collected with `doops -session ... exec` using metadata-only
  `kubectl get`; no Secret values or database mutations were read. The new
  `mcp_dosql_read operation=discover` is local source only and still needs
  an immutable Agent rollout before the same scan can be claimed through MCP.
- Production Console build passed after adding conversation reference controls.
- Full DoSql standalone CLI suite passed 58 tests; full Gateway suite passed.
  Full DoOps CLI still contains unrelated pre-existing Helm/upgrade contract
  failures in the dirty worktree; the DoSql/domain/reference tests pass.
- Browser screenshots are outside the repository under `/tmp/doops-unified-*`.
  In-app browser invocation failed with `Transport closed`; installed local
  Playwright was used explicitly. Expected 401/403 responses are recorded;
  no unrelated JavaScript runtime failure was observed on the tested panels.

## October 8, 2026 Follow-up Verification

- The live Gateway target `gw-114-k8s` responded to `capabilities` with
  `structuredSSH=ready` and was idle. The live Agent reported
  `release=v1.3.24` with commit
  `6ac87c9b4a16081cd824cfeb5b15424d27c7b06d`.
- A metadata-only remote check confirmed that this live Agent does not yet
  contain `/app/skills/dosql/scripts/dosql-agent.mjs` or the target database
  client wrappers. This is evidence that the immutable DoSql-enabled Agent
  image has not been rolled out to the target, not permission to use the old
  Agent path as a compatibility route.
- A read-only `kubectl get pods,services,deployments,statefulsets --all-namespaces
  -o json` inventory completed through the existing Gateway session. It read
  Kubernetes metadata only; no Secret values, database connections or
  mutations were attempted.
- No production deployment, credential grant change or database mutation was
  performed. Overall completion remains blocked until a separately approved
  immutable Agent rollout and live disposable-target acceptance are available.

## Next Round

Read this file and the current diff first. Complete the independent review,
verify the remaining failure paths and browser workflow, and update this state
with actual tool results. Do not mark the overall merge complete while the
database execution path or required live acceptance is missing.
