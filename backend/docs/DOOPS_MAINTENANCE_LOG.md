# Doops Maintenance Log

This file records release hygiene decisions for doops CLI, gateway, agent, and skills.

## 2026-09-13 Managed Agent proxy allowlist release

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
- There is one operator command: `scripts/release-managed-agent.sh`. Image pull
  on the node must succeed before Helm. `hostNetwork` and custom entrypoints are
  refused. `install-managed-agent.sh` and `upgrade-managed-agent.sh` are removed.
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

- Verified source commit: `848abca090cbcdcd8251fa8978055e6ad822df95`
- Verified deployed Agent image: `docker.cnb.cool/l8ai/ai/doops.sh@sha256:7f8582052367bf54f2ff052e530a8782b1e96a6e26178a4a4fc95a5c0039eb15`
- Embedded doagent version: `0.2.26`
- Verified target: `doops-235/hdu-235`
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
