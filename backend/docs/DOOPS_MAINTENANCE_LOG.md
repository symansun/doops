# Doops Maintenance Log

This file records release hygiene decisions for doops CLI, gateway, agent, and skills.

## 2026-07-17 Unified Gateway Authentication

- Gateway target configuration stores routes only and no longer accepts
  per-target user tokens.
- `auth.json` stores one token per normalized Gateway URL. Login and logout
  therefore affect every target on that Gateway.
- Migration from an older installation:
  `python3 backend/scripts/migrate-gateway-auth.py --target
  <one-target-on-the-gateway>`. The selected target's current token becomes the
  Gateway login state, and token fields are removed from all targets on that
  Gateway.

## Maintenance Rules

- Keep one default local config source: `~/.agent/skills/doops/config.json`.
- Keep token cache in `~/.agent/skills/doops/auth.json` with the `tokens` object shape.
- Do not use or document `~/.doops/auth.json`, `~/.config/doops/config.json`, or project `.doops/` as current config sources.
- Gateway mode is the only normal operating path. Direct agent mode is limited
  to explicit first-install or Agent self-recovery work and is not a daily
  target or deployment path.
- Do not commit runtime state: `.agents/`, `.doops/`, `auth.json`, `config.json`, `history.jsonl`, or `sessions.json`.
- Never print complete user or agent tokens in logs, docs, commands, or commit messages.
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

- Verified source commit: `836171d04b1230b0fc3f477a47b85a9250408184`
- Verified deployed Agent image: `docker.cnb.cool/l8ai/ai/doops.sh@sha256:e43a2b207fcf0a6e734216d6ac3cb95235a7fd13d36e7a566aadb48c9a67033c`
- Embedded doagent version: `0.2.21`
- Gateway binary: built from this repository and deployed with `scripts/deploy-gateway.sh`.
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
  now superseded by the Gateway-only normal path; direct mode is restricted to
  explicit first-install or Agent self-recovery work.
- Clarified that `auth.json` is only a token cache and must use `{"tokens":{...}}`.
- Removed tracked `.agents/skills/*` generated skill mirrors from the repository.
- Added ignore rules for `.agents/`, `.doops/`, and doops runtime credential/history/session files.
- Fixed gateway git tunneling so the agent strips external gateway auth and uses its local agent token for its internal `/git` handler.
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

## 2026-07-17 Historical Retired CICD Terminal Result Recovery

- Fixed apply operations that completed after a reverse-tunnel disconnect but
  previously returned `agent disconnected` because the unique `CICDReport`
  could no longer reach Gateway.
- Historical implementation persisted the terminal response by the digest of
  the retired credential-run binding before delivery. Gateway waited for target
  reconnection and performed an internal read-only result query; it never
  replayed the apply prompt. This path was retired with the natural-language
  release contract.
- Explicit `tools/cancel` still cancels the admitted operation. Connection
  cleanup no longer cancels CICD solely because the transport disappeared.
- The detached Helm executor now always uses `--reset-values`, preventing
  removed fields in historical release values from violating the current chart
  schema.
