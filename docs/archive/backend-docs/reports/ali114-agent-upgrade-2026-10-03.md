---
name: ali114-agent-upgrade-2026-10-03
scope: single-agent-upgrade
owner: doops-maintainers
status: preparing
---

# Authorized Target

The user selected ali, then confirmed 114. Upgrade only gw-114-k8s at
114.55.61.32, identity doops-114-k8s/master-1. Do not switch the shared Gateway
or Web console and do not upgrade other Agents in this pilot.

# Acceptance

- Exact source-commit Linux Go execution tests and Git process race checks pass.
- Reviewed source is remotely visible and its main CI publishes an immutable
  Agent image with matching revision and registry digest.
- Node storage is above its unchanged eviction thresholds with rollout margin;
  the Agent is not repeatedly evicted.
- Helm-managed doops-agent-gateway in kube-system reaches the candidate image,
  observed generation, ready/available replicas and matching Pod imageID.
- The exact Gateway identity reconnects, structuredSSH and deterministic exec
  work, and do-agent runtime version matches the candidate.
- Record prior Helm revision/image, recovery and upgrade steps, live evidence,
  and any rollback or untested protocol path. Pod Ready alone is insufficient.

# Bounds

At most three repairs per failed evidence group, three attempts per artifact,
20 minutes per CI observation and 10 minutes per rollout. Unknown-outcome
mutations must not be replayed. Preserve business data, existing permissions,
eviction thresholds and validators. SSH is used only for this offline Agent's
documented recovery through the existing dev alias.

# Baseline And Recovery Evidence

- CLI v1.3.8, source 0e9b5ef42898aaa94eed0a3f0a669d68b4653999.
- Source review branch fix/session-operation-coordination contains the reviewed
  Git repair 789db3db5972a6904d3c7a1cb6e7754f9f80c008.
- Gateway reports the target offline; last heartbeat was
  2026-10-03T02:15:48.916071182Z (10:15:48 Beijing time).
- The existing recovery alias dev resolves to this exact host and key-based
  access succeeds. Hostname is master-1. No recovery credential payload is logged.
- Helm release doops-agent-gateway, kube-system, revision 15, deployed; Deployment
  generation 39, selector app=doops-agent-gateway.
- Prior image docker.cnb.cool/l8ai/ai/doops.sh@sha256:23b34730a62ce8c2cf14a1376571766096fd5d92c4d002d86a971c93e64af80b.
- Kubelet repeatedly evicted Agent Pods for ephemeral-storage. A replacement
  briefly reached Ready, was evicted again and its next replacement is Pending.
- Node DiskPressure=True; other sampled pressure conditions are false.
  imagefs.available=15%, nodefs.available=10%, nodefs.inodesFree=5%; pressure
  transition period is five minutes. Current sampled available bytes
  17231474688 versus capacity 105347694592; inodes are not constrained.
- Linux verification is being added to the existing PR checks using the pinned
  CI Go builder. Candidate artifacts and rollout have not started.
