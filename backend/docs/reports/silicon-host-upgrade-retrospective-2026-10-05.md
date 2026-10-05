---
name: silicon-host-upgrade-retrospective
scope: standalone-docker-agent-recovery
owner: doops-maintainers
status: controller-verification
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

# Actual Deployment

- Authorized target: gw-silicon / doops-silicon/vm-49, host 49.51.244.8,
  SSH user ubuntu, port 2022, hostname VM-0-15-ubuntu.
- The second supplied host 43.173.87.215 / VM-0-6-ubuntu was accessed read-only;
  no Agent replacement on that host is included without scope confirmation.
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
- Keep request_id validation and credential/permission boundaries intact. No
  temporary Agent identity, release-runner or Kubernetes installation is introduced.

# Verification

- Actual SSH authentication and exact-host Docker inventory completed on both hosts.
- Actual gw-silicon read-only controller preflight passed for the published
  v1.3.20 image/source; the old container remained running.
- Focused controller transaction, preservation, credential-canary redaction,
  preflight-denial, rollback and ownership tests are machine-checked.
- Publication and production replacement are pending exact-source CI and final
  independent review. Do not claim a successful upgrade from preflight alone.
