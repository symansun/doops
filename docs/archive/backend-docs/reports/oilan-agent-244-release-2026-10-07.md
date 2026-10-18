---
name: oilan-agent-244-release
scope: managed-agent-update-and-runtime
owner: doops-maintainers
status: source-verification
---

# Scope And Gates

Authorized scope is the DoOps Agent UI release, gw-114-k8s, JM upgrade, and a
new Oilan Agent in the EDU cluster on gpu-ampere01 (the authorized public node 244).
Unrelated ongoing DoSql/DoWeb and session-lock changes are not part of this release.

Completion requires source commit/push visibility, exact-source CNB CI, immutable
Agent/Web/CLI provenance, Ready Pod and node evidence, actual managed update and
rollback terminal evidence, and browser chat/upload/read-only Kubernetes checks.
JM remains incomplete without an authorized reachable external control path.

Each CI observation is bounded to 20 minutes and a rollout to the controller's
existing deadline. Limit evidence-based repairs to three per failing group.
Do not restart an unknown mutation, cancel other users' operations, weaken
validators, or claim completion from a Job admission/heartbeat alone.

# Confirmed Evidence

- Published baseline: v1.3.23 / b96c7a46259798babd2701abf07330121b4fe9da.
- Main CI cnb-amm-1k4ap9g6u and release CI cnb-bd5-1k4aq1jpt passed.
- Live Web serves that source; HTTPS and host health return 200.
- gw-114-k8s serves that source; Helm revision 53 and do-agent 0.2.35.
- New doops-oilan/oilan-node bootstrapped at revision 2 on gpu-ampere01.
- Initial values omitted the doagent-config mount. Logs explicitly reported
  missing /opt/doagent_config/settings.json; adding the mount fixed startup.
- The public image pulled without imagePullSecrets. A separate host-client
  401 was not evidence that kubelet required a new credential dependency.
- Job doops-self-upgrade-7b3e213d rolled to a Ready replacement Pod but rejected
  its imageID: requested manifest 7b3e213d, runtime index 071fc68a.
  It failed and automatically restored revision 2, Pod UID
  f5a09e5d-9c76-4b3e-bd1e-5ef490894356, verified Ready and correct image.
- The fixed verifier retains immutable spec pin, UID, readiness, complete
  Deployment/generation/replica checks and exact runtime identity equality.
  Independent review identified the corresponding rollback issue; forward,
  automatic rollback and historical rollback paths are being verified together.
- Successful verification receipts now bind exact historical revision/image,
  scope and runtime identity; superseded status alone is never success evidence.
- Local SelfUpgrade scenarios and 101 Python contracts passed. Exact final
  source CI and runtime testing remain separate gates.

# Remaining Evidence

- [ ] Commit and push the isolated controller fix; exact-source PR/main/tag CI.
- [ ] Install the matching release CLI/Skill and verify candidate image labels.
- [ ] Update the existing Agent first, then exercise its new controller script.
- [ ] Verify a changed-reference rollout, fresh Pod UID, 244 placement and rollback.
- [ ] Commit final runtime values and record source/digest/Helm evidence.
- [ ] Browser login, attachment ingestion, multi-turn chat and K8s reads.
- [ ] Upgrade JM through its authorized external control path.
