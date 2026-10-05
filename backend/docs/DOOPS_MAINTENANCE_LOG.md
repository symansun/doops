# Doops Maintenance Log

This file records release hygiene decisions for doops CLI, gateway, agent, and skills.

## 2026-10-05 External Host Release Failure Recovery

- Install the external rollback trap before the first host replacement call,
  including SSH transport failures whose remote outcome is unknown.
- Resume partial rollback from persisted progress while preserving exact old/new
  container ownership checks. A removed replacement must not strand the old Agent.
- Acceptance requires real foreground stdout/stderr, background output and exit,
  structured SSH argv/failure and follow-up execution. Version and capability
  declarations alone cannot complete a release; do not replay a failed probe.
- Live v1.3.21 Silicon acceptance passed 40 fast dual-stream calls, same-session
  concurrent routing, 89 correctly correlated notifications, background execution
  and post-cancellation execution. Its old containers remain stopped and available.

## 2026-10-05 Preserve Fast Command Output

- Live Silicon recovery exposed short-command stdout loss: the RPC could finish
  successfully before its output readers consumed the bytes.
- Raw execution now owns its pipe readers independently of Cmd.Wait. Parent
  write descriptors close after process start; output is drained before the
  terminal response, without command sleeps or protocol fallback.
- Keep the existing process-group cancellation/watchdog and bounded pipe grace.
  Read failures and escaped writers are reported honestly, never as success.
- Verify fast stdout/stderr, delayed readers, large output, signal exits,
  foreground RPC ordering, background completion and cancellation before release.

## 2026-10-05 Remove Audit Admission And Business Helm Policy

- Remove historical unresolved-audit queries from mutation admission. Preserve
  `outcome_unknown` records and failure responses; do not replay mutations or
  declare an uncertain result successful. Live operations, resource leases,
  cancellation and audit-persistence failure protection remain authoritative.
- No TTL, extra protocol, configuration flag or manual reconciliation mechanism.
- Remove fixed image values keys, date-based tags, bundled-only managed Charts,
  project-directory assumptions and implicit historical values rewriting.
  Chart/values define business configuration; remote values are staged once for
  both preflight and mutation. Authentication, exact scope, artifact digest,
  session isolation, dry-run and final Deployment verification remain enforced.
- Restore Gateway and the affected ZYJ/edu Agents from one immutable CI release.
  Evidence and rollback bounds: `reports/subtraction-release-2026-10-05.md`.

## 2026-10-05 Minimal Concurrency Repair

- Reuse the existing MCP tool-error response for execution failures. RPC request
  errors, canceled execution and missing terminal evidence remain distinct.
- Validate terminal failed Helm evidence with the same runtime/session/digest and
  authorization bindings as successful results; failed apply releases ownership
  without becoming success or an unresolved execution.
- Replace the global credential mutex with sorted credential-resource-ID locks.
  Unrelated credentials can proceed; shared credential rotation and revocation
  remain serialized with apply.
- Remove manual operation reconciliation from CLI, HTTP and audit mutation code.
  Historical records stay readable; operator claims cannot clear unknown outcomes.
- No new protocol, configuration switch or time-based unlock. Source, verification
  and release progress are tracked in `../../docs/concurrency-remediation-2026-10-05.md`.

## 2026-10-04 Target-Scoped Operation Administration And Registry Sync

- Gateway admin operation list, cancellation, and manual reconciliation now require
  an explicit `cluster`/`instance` pair and reject cross-target operation IDs.
- CLI admin calls pass the selected target to the Gateway; client-side filtering remains
  an additional display and cancellation guard.
- The generic `doops registry sync` foreground command does not require the background
  `executionLifecycle` capability. Only `exec --bg` keeps that admission requirement.
- Registry sync remains a DoOps-owned generic OCI flow; AMP-specific image names and
  sync scripts are not part of the implementation or release surface.

## 2026-10-04 Scoped Background Lifecycle Admission

- The current goal retains the existing lifecycle capability for background
  execution. This supersedes the full-retirement behavior in the entries below.
- Agent advertises the exact existing four-field v1 contract. CLI `ExecBg`,
  Gateway RPC and internal background dispatch require it; foreground
  exec/SSH/ask/apply and queries do not.
- Missing background capability is rejected before execution/lock acquisition,
  with a terminal blocked audit. Cancellation ACK remains non-terminal; all
  terminal persistence, request-ID routing and unknown-outcome protection stay.
- Live verification found CLI and Ali/Tencent Agents on `592dad8`, but the
  actual Gateway executable still on `148a97c`. Its old background gate and
  notification routing reproduce rejection and missing command output.
- Scope, bounded rollout and evidence are tracked in
  `reports/lifecycle-release-2026-10-04.md`. Passing local tests is not release.

## 2026-10-04 Lifecycle Finalization And Rollout Repair

- The real OpenSSH lifecycle fixture closes the SSH session before waiting for
  client disconnect, rather than immediately closing the transport after
  exit-status. All remote-exit and unknown-outcome assertions remain unchanged.
- Reject retired capability names at the CLI boundary, before network access;
  an invalid `executionLifecycle` request must not recommend upgrading the Agent.
- Missing workspace-write terminal evidence remains unknown. A confirmed local
  completion after cancellation is distinct from an absent terminal response.
- A failed internal mutation audit propagates uncertainty. Finalizing operations
  are no longer counted as live executions while persistence is retried.
- Upstream transfer failures after proxy writes retain unknown-outcome protection;
  downstream output errors cannot ignore the cancellation result.
- Client WebSocket writes use the existing bounded transport writer. A stalled
  consumer can no longer hold a mutation lease indefinitely through an unbounded write.
- Gateway serve locks its canonical database path before opening/recovering state.
  A second process must fail without changing live Agent or execution records.
- Mixed running releases and rollout acceptance are recorded in
  `reports/lifecycle-release-2026-10-04.md`; source verification is not deployment.

## 2026-10-04 Remove Retired Lifecycle Classification

- Scheduler scan failures no longer classify the retired `executionLifecycle` capability
  as a live protocol category. The Gateway-owned terminal state machine remains the only
  execution lifecycle contract; stale Agent errors are reported as generic scan failures.

## 2026-10-03 Unified Gateway Execution Lifecycle

- Retired the `executionLifecycle` Agent capability and its CLI/Gateway admission gate.
  All tools now use the Gateway-owned terminal state machine; `doops_bg` is not a
  separate protocol path.
- The Gateway persists the terminal audit state before releasing the resource lease.
  If persistence fails, the active operation remains held and retries automatically.
- Agent notifications are correlated by `request_id`, so concurrent calls cannot
  overwrite each other's session routing.

## 2026-10-03 Unified DoOps release identity

- The CLI, Gateway and Agent now share one release tag. They also report the same full
  40-character `CNB_COMMIT` as immutable provenance; `doops targets` exposes it as
  `RELEASE_COMMIT`, and a managed Agent upgrade fails verification when the new Agent
  does not advertise the same commit as the published CLI.
- `cli-contract/v3` remains a protocol compatibility marker and is checked separately
  by `doops --contract`; it is no longer mixed into normal `doops version` output.
- The pinned `doagent` version remains image provenance only. It is not a second DoOps
  release version shown to operators.

## 2026-10-03 Execution Lifecycle Release Preparation

- Final source review confirmed an SSH ordinary-exit regression: a remote
  nonzero exit was classified as unknown and blocked later target mutations.
  The repair accepts Agent structured terminal errors and remote exit codes
  1..254 as ordinary failures; 255, interruption, disconnection and missing
  terminal evidence remain guarded. CI installs OpenSSH for real loopback
  client/server functional acceptance.
- Linux Agent/Gateway/CLI and Git race acceptance passed in
  cnb-gr9-1k3vqigpe for fa1a98a52d081459a4dcecc47f8f004662cde0df.
  Browser recheck has 27 passes with matching frontend source hashes, including
  cancellation confirmation and HTTP 202. These replace the earlier missing
  Linux/browser evidence; the SSH repair needs its own exact-commit CI.
- Runtime preflight found unmanaged Agents on JM, SCU-new and ZYJ, and no
  usable Kubernetes upgrade entry on silicon. Full Gateway cutover remains
  blocked until legacy Agent migration has an explicit plan and verification.
  Source merge and immutable artifact publication do not imply runtime cutover.
- Repair cancellation ownership, process-group and prompt termination evidence,
  persisted unknown outcomes, per-workspace admission and operator-visible states.
- The initial capability-gated lifecycle plan was superseded by the unified Gateway
  state machine above; structured SSH remains the only separate argument protocol.
- Add persistent scheduler job leases with automatic terminal-path and expiry
  cleanup so tick/run-now cannot duplicate a job or require manual unlock.
- Independent review found and repaired target mutation/workspace overlap,
  mismatched clean workspace locks, replacement-connection ownership and raw-output
  keyword matching. Regression scenarios now check those boundaries.
- Agent, Gateway and CLI package suites, Agent go vet and 50 Python contracts
  passed; targeted race scenarios passed. Full race verification is not a final
  release pass. This is local evidence;
  it does not establish a live upgrade or authorize unresolved mutation retries.
- Track source, CI, installation, rollout, rollback and live acceptance evidence in
  reports/operation-release-2026-10-03.md.
- The gitkit process-cleanup blocker was repaired and independently re-reviewed
  in 789db3db5972a6904d3c7a1cb6e7754f9f80c008, including delayed EOF and interrupted
  hook initialization regressions.
- On 2026-10-03 the user authorized committing and releasing all DoOps components
  and all online Agents. Inventory exact identities and rollback baselines before
  mutation; upgrade Agents before switching the Gateway.
- PR checks now include Linux Agent/Gateway/CLI execution tests, not only Python
  contracts. Linux process cleanup, browser interaction and exact-commit CI
  artifacts remain mandatory gates, not implied by local macOS test success.

## 2026-10-02 Deployment Contract Alignment (Local, Not Released)

- Scope: reconcile ordinary workload `ask --apply`, managed Agent image-only
  `upgrade`, external bootstrap/runtime-value changes, and independent Gateway
  delivery. Remove the stale source-workspace prerequisite and distinguish
  bootstrap resource readiness from Gateway acceptance.
- CLI upgrade now requires an explicit session. A preview or a Job acceptance
  is not a successful release; missing acceptance, mismatched observations, and
  success words in failed Job logs must fail closed.
- Chart references enforce the documented HTTPS/OCI boundary; value files retain
  their separate HTTP(S) contract. External bootstrap requires an explicit
  kubeconfig; ordinary workload apply and controller-ready Agent image upgrades
  do not require an operator kubeconfig.
- Live read-only checks on 2026-10-02: `gw-edu-coder` resolved to
  `doops-edu/edu-coder`, was online/idle, and passed `structuredSSH`.
  `do-agent --version` returned `0.2.26`, while this repository pins `0.2.35`.
  This is version drift, not evidence that the target is offline.
- Installed CLI was `v1.3.6`, commit
  `c9a60d77ebce051d2443c4c6c47559f9ffaa7a87`; its upgrade help was still preview-only.
  These working-tree changes have not been published, installed, or deployed.
  Do not copy an unreleased local binary or Skill over the released installation
  and call the release complete.
- Release gate remains: reviewed commit/push, successful exact-commit CNB
  artifacts, versioned CLI/Skill installation, explicit-target deployment,
  live Deployment/imageID/health and affected user-path acceptance. Local
  command tests do not replace that gate.
- Follow-up alignment: the installer previously paired a versioned CLI with
  Skill/docs copied from the current working tree. Stage Skill/docs from the
  CLI's exact embedded commit before replacement, and record `skill_commit`.
  Missing Git objects or required release files must leave the installed
  CLI, Skill and config unchanged; never substitute uncommitted instructions.
- Removed temporary privileged-Pod/hand-started Agent examples from the SOP
  and legacy Docker/DaemonSet recommendations from the README. Mount changes
  remain tracked Helm configuration changes; bootstrap, image upgrade and
  Gateway delivery are distinct operations.
- Documented evidence limits: `--dry-run` only previews parameters;
  `structuredSSH` does not advertise a self-upgrade controller or prove that
  the replacement Pod serves the Gateway. Final Pod UID/imageID/runtime
  version and functional acceptance are still required. A CLI-triggered
  rollback Job requires a reachable Agent, so Gateway loss is not guaranteed
  to recover automatically.
- Review follow-up: the Job now checks active selector-matched Pod UID,
  readiness, spec image and imageID in addition to Deployment convergence;
  digest comparisons follow the selected immutable platform image. Manual
  rollback reads the expected image from the selected historical Helm manifest,
  not from the post-rollback live Deployment. Both rollback paths recheck the
  Gateway protocol in the CLI, and a failed recovery probe does not recursively
  request more rollbacks. The preflight rejects a surge other than one.
- Final guidance audit found stale image-build and shell Skills directing source
  archive/SCP and release-image builds on a business target. Removed that route,
  aligned the file-sync guide and CLI usage with CI artifact delivery, and made
  bootstrap examples select an external-controller kubeconfig explicitly.
- CLI upgrade success output now names the Job/protocol evidence it actually
  verifies and leaves Gateway-serving Pod/version/function acceptance explicit.
  The legacy-DaemonSet test now asserts the explicit migration/no-adoption error.
- Verification: Agent `go test ./...`, CLI `go test ./cli/doops`, all 50 backend
  Python contract tests, shell syntax checks, compiled CLI help/session rejection,
  canonical Skill validation, and `git diff --check` passed. Upgrade protocol
  scenarios exercised missing acceptance, mismatched Job/namespace, failed logs
  containing success text, preview, success, rollback, and failed recovery probes.
  These are local/synthetic tests, not a live upgrade acceptance.
- Rechecked the installed CLI and `gw-edu-coder` through read-only session
  `deploy-contract-20261002`: CLI remains v1.3.6, target is online/idle,
  `structuredSSH` is ready, and `do-agent --version` remains 0.2.26.
  No commit/push, installer replacement, or production mutation was performed
  by this alignment verification.
- Independent Skill scenario review confirmed that dirty local source and a
  missing local kubeconfig do not block ordinary apply, and `structuredSSH`
  alone does not establish self-upgrade readiness. Scoped the system prompt's
  Helm-only mutation rule to ordinary workloads so it does not contradict the
  separate Agent upgrade Job path.

## 2026-09-28 In-cluster Agent self-upgrade

- `doops upgrade` no longer rejects apply. A pinned `repository@sha256:<64 hex>`
  image makes the target Agent create a Kubernetes Job. The Job reuses the
  kubeconfig volume the Agent already mounts and runs Helm outside the Agent
  process, so the rollout cannot kill the rollback controller.
- The Job keeps the chart handoff at maxSurge=1 and maxUnavailable=0, preserves
  the live selector, and rolls back unless the new Pod stays ready. The CLI then
  requires `structuredSSH` and starts a rollback Job if that probe fails.
- `scripts/release-managed-agent.sh` remains the bootstrap for a cluster that
  does not yet have this controller. It still requires an operator kubeconfig.

## 2026-09-28 Structured SSH option boundary

- `doops_ssh` options were copied into the OpenSSH argv after only an empty/NUL
  check. A positional value in `options` could become the destination, and
  `ProxyCommand`, forwarding, `HostName`, or `KnownHostsCommand` could retarget
  the connection.
- The Agent now accepts only `-4`, `-6`, `-p`, `-i`, and the config keywords
  `BatchMode`, `StrictHostKeyChecking`, `ConnectTimeout`, `IdentitiesOnly`,
  `IdentityFile`, and `Port`. The CLI applies the same allowlist before sending
  the tool call. Rejected options never start `ssh`.

## 2026-09-28 doagent 0.2.35 runtime pin

- Published `do-agent v0.2.35` from `4c1e7e8a4e7f89b917dc4cce152cd73c36c32d68`.
  Index `sha256:bfe560f24c6e679f38fe2e880b9fa3e0078fcb3cac1e2631e11d1d3093bf0b12`.
  `linux/amd64` manifest `sha256:f1c8dc743cb5081d59566ec6cd6a241cb2a3e0f88a8c20178896e9976053ba91`.
- v0.2.27 through v0.2.34 never received a versioned image. The v0.2.34 release
  gate received SIGTERM because Debian procps parses `kill -TERM -<pid>` as a
  short option and can issue `kill(-1, SIGTERM)`.
- v0.2.35 signals the child process group through the raw syscall and passed the
  CNB release gate. `runtime-versions.env` now locks that image.
- Oilan `doops-system/doops-agent` was handed off by the external controller to
  DoOps v1.3.5, Helm revision 30, image
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:d71733994bda0fa4c4288cdb8517d432abcdac2ff69147f60ae150d258d31bea`.
  The replacement Pod was Ready, `structuredSSH` was ready, and `do-agent --version`
  returned `0.2.35`.

## 2026-09-27 Legacy Helm selector migration

- Root cause: `release-managed-agent.sh` treated `workload.selectorLabels` from
  `helm get values` as the selector source, but older live releases stored the
  selector only in `Deployment.spec.selector.matchLabels`.
- The external controller now reads and validates the live Deployment selector
  against template labels, then writes that selector into the candidate values
  before the server-side Helm dry-run and handoff.
- Verified the `doops-edu/edu-coder` release upgraded from the legacy values
  schema to the v1.3.4 Agent image without changing its selector.

## 2026-09-27 Agent protocol capability gate

- Root cause: the v1.3.3 CLI sent structured `doops_ssh` to an older online Agent,
  which returned `Unknown tool over WS: doops_ssh`.
- The Agent now advertises `structuredSSH` during initialize, Gateway records and
  exposes that capability per target, and both Gateway and CLI fail closed when it
  is absent.
- The managed-Agent release script now checks that the local CLI supports the
  `capabilities` probe before reading or mutating Helm state. An older CLI cannot
  start an Agent handoff and only fail after the new Pod is running.
- `release-managed-agent.sh` verifies the capability after handoff and rolls back
  before release completion if the replacement Agent is too old.

## 2026-09-27 Structured SSH argv boundary

- Root cause: `doops exec` accepted one shell command string and started it with
  `bash -c`; a nested `ssh ... bash -lc ...` therefore crossed multiple shell
  parsers without a typed remote argv boundary.
- Added `doops_ssh` with separate `host`, `options[]`, and `command[]` fields. The
  Agent quotes only the final remote argv at the OpenSSH boundary and executes the
  local client with `exec.Command`, preserving nested quotes without a fallback path.
- Direct `doops exec` commands beginning with `ssh` now fail with an explicit
  migration message. Functional tests cover spaces, single quotes, dollar signs,
  invalid boundary values, Gateway authorization mapping, and CLI guidance.

## 2026-09-27 Gateway binary provenance release

- Root cause: `bin/`, `backend/bin/`, and Agent binaries are ignored build outputs,
  not repository state. A local `doops-gateway` from September 22, 2026 could
  therefore survive across source changes while CNB built a newer Agent image.
- The Agent image now embeds the exact source commit into Gateway, labels the
  image with the same revision, and verifies both during the CNB build.
- Gateway extraction and host deployment now require the exact source commit and
  reject binaries without matching provenance. The standalone Gateway host is
  not updated by an ordinary Agent workload release.
- The provenance check must consume the complete `strings` output before testing
  the exact commit. `grep -q` under `pipefail` exits early and turns a valid
  extracted CI binary into a false rejection through `SIGPIPE`.

## 2026-09-21 Managed Agent host alias release

- Existing Helm releases can now use repeatable `--host-alias HOST=IP` arguments
  when an Agent must reach a named HTTPS upstream through an explicit address.
- The release script validates exact hostnames and IP addresses before target
  contact, merges the requested mappings into the live Helm `hostAliases`, and
  preserves unrelated mappings.
- Replacement verification records the pre-release Pod identity, requires a
  different Ready Pod to register with Gateway, compares that Pod's host aliases
  with the Helm release, and resolves every requested hostname inside it. The
  release rolls back on mismatch; there is no DNS fallback.
- Proxy allowlist verification reads the replacement Pod spec. The release
  controller uses Kubernetes to inspect the candidate Pod because a `doops exec`
  command may be routed to the still-serving old Pod during a handoff.

## 2026-09-13 Managed Agent proxy allowlist release

- Existing Helm releases now require an operator-side kubeconfig plus the exact
  Gateway `cluster/instance`. The script performs Helm outside the current Agent,
  uses server-side dry-run, diagnoses candidate Pod failures, and automatically
  rolls back to the previous Helm revision when handoff or verification fails.
- `scripts/release-managed-agent.sh` now accepts an explicit
  `--proxy-allowed-hosts` value for existing Helm releases and verifies the
  replacement Agent inherited the exact `DOOPS_PROXY_ALLOW_HOSTS` value.
- Omitting the option preserves the live Helm value; an explicit empty value
  restores deny-all. URL, port, path, wildcard, and empty-list entries fail
  before mutation. New-cluster installs default to deny-all and configure
  allowed hosts through their complete values file when needed.

## 2026-09-07 Single agent handoff release

- `deploy/helm/doops-agent` now uses `maxSurge=1` and `maxUnavailable=0` so the
  live Agent keeps serving until the replacement Pod is Ready. Gateway Register
  switches the session, then Kubernetes deletes the old replica.
- There is one operator command: `scripts/release-managed-agent.sh`. Existing
  releases use an operator-side kubeconfig; `hostNetwork` and custom entrypoints
  are refused. `install-managed-agent.sh` and `upgrade-managed-agent.sh` are
  removed.
- A 30s Gateway offline window during handoff is a release failure.

## 2026-09-06 One-click Helm agent install and upgrade

- Added laptop Helm scripts; superseded on 2026-09-07 by the single handoff
  release command.

## 2026-08-06 Server-Side Helm Preflight

- Helm `upgrade-install` preflight now uses `--dry-run=server` so charts that depend on `lookup`
  validate against the same live cluster objects used by the real mutation.
- Client-side dry-run previously returned empty `lookup` results and falsely rejected valid
  ConfigMap-backed releases before any mutation.
- The preflight remains fail-closed and still runs with the exact release arguments before the
  real Helm command.

## 2026-07-22 Agent SSH Client Runtime Contract

- Root cause: `Dockerfile.base.light` explicitly removed `openssh-client` together with the
  intentionally forbidden SSH server, so every standard Agent image lacked the `ssh` command.
- The light base now installs the outbound SSH client, continues to remove `openssh-server`, and
  verifies both `ssh -V` success and `sshd` absence before publication.
- When the base definition changes, the `main` pipeline builds and publishes the base first, passes
  its immutable digest to the final Agent build, and verifies the final image still contains the
  client. This prevents an old pinned base from producing a false-success Agent image.
- An online Agent may proxy any user-explicit SSH client command through normal `doops exec` or
  background exec semantics; SSH is not limited to first-install or recovery use. This does not
  restore SSH targets, direct Agent connections, an SSH daemon, or inline credential handling.

## 2026-07-21 Remove Percentage-Based Runner Disk Guard

- Removed the root-filesystem percentage threshold from release-runner bootstrap.
- Bootstrap still requires the managed Agent to be Ready, the selected node to
  report `DiskPressure=False`, and at least 5 GiB of root filesystem space.
- The fixed threshold avoids blocking large disks that retain ample absolute
  capacity while remaining below an arbitrary percentage.

## 2026-07-19 Natural-Language Release Safety

- CNB build `cnb-vlt-1jts11c6p` successfully published the HDU managed Agent
  through canonical `doops ask --apply` and `mcp_doops_helm`.
- Verified HDU Helm revision 11 with image
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:7f8582052367bf54f2ff052e530a8782b1e96a6e26178a4a4fc95a5c0039eb15`,
  Deployment `1/1` Ready, zero restarts on the current Pod, reverse-tunnel
  reconnection, and embedded `do-agent 0.2.26`.
- Removed the isolated HDU release runner after the stability window and
  confirmed its Helm release, workload, Gateway registration, and temporary
  local target route were absent.
- The preceding runner bootstrap pulled a 1.57 GB image while the single node
  was near its ephemeral-storage eviction threshold. Kubelet evicted the
  managed Agent, and the five-minute Gateway registration window incorrectly
  reported the runner offline even though it registered after rescheduling.
- Runner bootstrap now blocks before image pull unless the managed workload is
  Ready, the selected node reports `DiskPressure=False`, and the root filesystem
  has at least 10% and 5 GiB free.
- Helm `upgrade-install` now runs the same command as a dry-run before the real
  mutation. Invalid scalar overrides such as encoding `workload.selectorLabels`
  as a `set_strings` value therefore fail before creating a Helm revision.
- `set_strings` is restricted by contract to scalar leaf keys. Structured maps
  and lists belong in chart values or explicit `value_files`.

## 2026-07-18 Remove Retired CLI Connection Paths

- Removed direct target fields, SSH target fields, per-target tokens, direct WebSocket routing,
  CLI SSH/install entry points, and the retired migration utility.
- All config files, including `DOOPS_CONFIG` overrides, use one strict Gateway-only schema.
- `targets`, `unlock`, `upgrade`, and credential commands require a configured target and the
  shared Gateway login in `auth.json`; only `login` accepts a user token or username/password.
- Missing login state fails before network access and instructs the user to run
  `doops login --target <name>`.
- Agent registration at `/v1/agent/connect` intentionally remains open and
  unauthenticated. This is a control-plane design decision, not a user-token
  compatibility path; user RPC, Git, and administrator APIs still require the
  unified Gateway user authentication flow.
- A future Agent workload-identity extension may bind identity to
  `cluster/instance`, but it must remain separate from Gateway user tokens and
  must not restore per-target CLI credentials.
- Reserved the future enrollment contract as a short-lived, one-time
  installation registration link. The link must be bound to one
  `cluster/instance`, consumed atomically, and exchanged for a separate Agent
  workload identity before registration becomes closed.

## 2026-07-17 Unified Gateway Authentication

- Gateway target configuration stores routes only and no longer accepts
  per-target user tokens.
- `auth.json` stores one token per normalized Gateway URL. Login and logout
  therefore affect every target on that Gateway.

## 2026-07-18 Reverse Tunnel Heartbeat Recovery

- Bound reverse-tunnel data writes with a 60-second deadline while keeping
  control frames independent from the application data-write mutex. A control
  timeout remains reusable only when it is both temporary and overlaps a
  bounded data write; unexplained control failures and all data-write failures
  close the socket so the reconnect loop can establish a fresh Gateway session.
- Root cause: the heartbeat path reused Gorilla's temporary write-lock timeout
  while data writes had no deadline. A data write pinned on stale transport
  could therefore hold Gorilla's write lock indefinitely. Closing every timeout
  would instead disconnect healthy slow writers, so the implementation records
  data-write begin/end sequence and distinguishes temporary lock contention from
  fatal network write timeouts.
- Release verification must leave an idle target connected for longer than the
  configured Gateway lease and confirm that `last_seen` continues advancing.
  It must also interrupt the Gateway/proxy during an active tunnel, confirm the
  Agent reconnects, and confirm an overlapping data write does not cause a
  temporary heartbeat lock timeout to close the connection.

## Maintenance Rules

- Keep one default local config source: `~/.agent/skills/doops/config.json`.
- Keep token cache in `~/.agent/skills/doops/auth.json` with the `tokens` object shape.
- Do not use or document `~/.doops/auth.json`, `~/.config/doops/config.json`, or project `.doops/` as current config sources.
- Gateway mode is the only DoOps operating path. First installation and Agent
  self-recovery happen outside the DoOps CLI through the documented host or
  Kubernetes administration boundary.
- Do not commit runtime state: `.agents/`, `.doops/`, `auth.json`, `config.json`, `history.jsonl`, or `sessions.json`.
- Never print complete user tokens in logs, docs, commands, or commit messages.
- Treat `/v1/targets` as connection state. Verify health with active probes for `exec`, git `push/pull`, and `ask` when release risk requires it.
- Normal doops-agent upgrades should pull only the lightweight `doops.sh:<version>` layer. Rebuild or roll the base image only when sandbox, doagent, BuildKit, or system packages change.
- Publish `doops-gateway` only with `scripts/deploy-gateway.sh` over SSH/SCP from the host context. Do not use a doops target, `doops exec`, `doops push`, `doops write`, base64, tar, or ad-hoc split chunks to deploy the gateway control plane.
- Do not keep the gateway host as a default doops target. Gateway is an endpoint/package, not an agent target.

## Upgrade Checklist

For every doops release or gateway/agent hotfix:

1. Record the release tag, commit, image tag, and affected targets.
2. Remove obsolete targets, aliases, temporary configs, and generated skill mirrors.
3. Verify the canonical config with `doops list`.
4. Verify live gateway state with `doops targets --target <gateway-mode-target>`.
5. Run active health checks on affected targets:
   - `doops -session <s> exec --target <t> --cmd 'hostname && date -u +%FT%TZ'`
   - `doops -session <s> push --target <t> --src <small test dir or repo>`
   - `doops -session <s> pull --target <t> --dest <tmp dir>` when pull changed
   - `doops -session <s> ask --target <t> --msg '<read-only smoke task>'` when ask changed
6. Run local test suites for touched components.
7. Update `skills/SKILL.md`, README, and deployment docs when config paths, token semantics, image tags, or release flow change.
8. Confirm no runtime state or token material is tracked by Git.

For gateway control-plane releases, additionally verify:

1. `scripts/deploy-gateway.sh --dry-run --host <gateway-host> --user <ssh-user>` succeeds.
2. The remote DB path is the real host DB and is non-empty, normally `/var/lib/doops-gateway/gateway.db`.
3. The running `/proc/<pid>/exe` hash matches the local `bin/doops-gateway` hash.
4. Unauthenticated `/v1/targets` returns `401`, and authenticated `/v1/targets` returns `200`.

## Current Release Line

- Verified source commit: `2d937bc7ae35c4941a1453d6b2c0765f10321bf1`
- Verified deployed Agent image: `docker.cnb.cool/l8ai/ai/doops.sh@sha256:23b34730a62ce8c2cf14a1376571766096fd5d92c4d002d86a971c93e64af80b`
- Embedded doagent version: `0.2.26`
- Verified target: `doops-114-k8s/master-1`
- Gateway binary: extracted from the verified CNB CI image digest with
  `scripts/extract-gateway-from-image.sh`, then deployed with
  `scripts/deploy-gateway.sh --no-build`.
- CLI binaries: published as immutable CNB Release Assets.

New release candidates must use the digest printed by the successful CNB build
for the selected commit. Do not copy this deployed baseline digest into a new
environment declaration unless that exact image is the intended release.

The earlier `v1.1` line introduced the gateway-only cleanup for:

- gateway busy/status/resource accounting;
- same-agent multi-session Git push support;
- agent Git-over-WS auth forwarding fix;
- canonical CLI config source cleanup;
- gateway deployment boundary cleanup.

Do not publish new agent fixes under floating `v1`; use the explicit release version.

- Fixed Git-backed source snapshots to use Git's own ignore semantics, keeping
  tracked release files and respecting root-anchored patterns such as `/ecp`.
## 2026-05-19 Gateway And Config Cleanup

- Consolidated the CLI default config source to `~/.agent/skills/doops/config.json`.
- Historical decision: direct agent mode remained in the config schema. This is
  superseded by the 2026-07-18 removal of all direct CLI and Agent endpoints.
- Clarified that `auth.json` is only a token cache and must use `{"tokens":{...}}`.
- Removed tracked `.agents/skills/*` generated skill mirrors from the repository.
- Added ignore rules for `.agents/`, `.doops/`, and doops runtime credential/history/session files.
- Historical Git tunnel authentication was superseded by the authorized
  Gateway reverse tunnel and the process-internal Agent Git handler.
- Fixed gateway resource busy accounting so resource-locked operations are visible in target snapshots.
- Verified `skills/doops-cli` tests and targeted gateway busy tests after changes.
- Added an SSH-only gateway deployment boundary after a failed attempt showed that routing gateway deployment through a gateway-host agent can run in the agent root and create an empty gateway DB.
- Removed the gateway host from the default target model; gateway should be represented as an endpoint/package, not `cluster/instance`.

## 2026-05-19 JM Runtime Mount Fix

- Fixed the standard agent runtime mounts so containerized agents mount the host
  `/run/containerd`, `/var/lib/containerd`, and `/var/lib/nerdctl`, not only the
  containerd socket.
- Root cause: with only the socket mounted, `nerdctl pull` can succeed but
  `nerdctl run` may fail with overlay snapshot, FIFO, `/etc/hosts`, or name-store
  errors because containerd/runc resolve those files from the host namespace.
- Recovery rule: pull the new image first, keep the old agent online, then start
  or roll the new agent. If the current agent container has a broken mount view,
  repair from the host namespace, for example through a one-shot privileged
  Kubernetes pod on the target node.
- JM verification for `v1.0.4`: `exec` returned the expected agent hash,
  `nerdctl run --rm ... echo direct-ok` succeeded, and a small `doops push`
  round trip succeeded.

## 2026-07-17 Historical Retired Structured Terminal Result Recovery

- Fixed apply operations that completed after a reverse-tunnel disconnect but
  previously returned `agent disconnected` because the unique terminal report
  could no longer reach Gateway.
- Historical implementation persisted the terminal response by the digest of
  the retired credential-run binding before delivery. Gateway waited for target
  reconnection and performed an internal read-only result query; it never
  replayed the apply prompt. This path was retired with the natural-language
  release contract.
- Explicit `tools/cancel` still cancels the admitted operation. Connection
  cleanup no longer cancels the admitted operation solely because the transport disappeared.
- The detached Helm executor now always uses `--reset-values`, preventing
  removed fields in historical release values from violating the current chart
  schema.
# 2026-10-03 CNB Helm Managed Authentication

- Scope: verify and consume the exact authorized CNB Helm HTTPS artifact; no index probe,
  anonymous retry, persisted plaintext login or payload in model/public tool arguments.
- Add internal metadata-only bindings and an expiring prompt lifetime lease; match
  session/namespace/release/chart and materialization IDs/version before consumption.
- Add authoritative bundled-values publishing with release-tag validation, server dry-run
  and final Deployment verification, without merging prior values or image overrides.
- Target publication and live credential/browser acceptance are pending; see
  `CNB_HELM_REPAIR_PROGRESS.md`. This is not a claim of production completion.
