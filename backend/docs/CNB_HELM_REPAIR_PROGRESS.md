# CNB Helm Managed Authentication Repair

Date: 2026-10-03. User authorized the DoOps implementation repair after saving a
valid CNB credential failed to make the release chain usable.

## Scope And Verification

- Repository: `l8ai/ai/doops.sh`; base main
  `0e9b5ef42898aaa94eed0a3f0a669d68b4653999`.
- Branch: `codex/cnb-helm-managed-auth-20261003`.
- Target: Ali `gw-114-k8s`, `doops-114-k8s/master-1`.
- Final business release: `oilan-system/zhiyong`; project
  `proj-jupyter-947606-c72887`. No other environment or database mutation.
- The supplied token was validated against the private CNB API, then saved through
  protected stdin as a staged Gateway-managed version. No payload belongs here.
- Verify the exact authorized Helm artifact, not a repository index. Consume the
  materialization only for the matching session, namespace, release and chart.
- Reject changed materializations, changed artifact digest, unauthorized host,
  redirects, oversized/non-chart content and mismatched scopes without retries.
- Full chart-values publishing must use the chart's sole values authority and a
  valid release tag, without merging prior release values or image overrides.
- Finish only after pushed commits, successful source-matched CI artifacts,
  supported external Agent release, credential verification/promotion, actual
  business Helm/Pod image checks and browser notebook execution/error evidence.

## Progress

- [x] Preserve the unrelated dirty historical DoOps checkout; create a clean worktree.
- [x] Code-path proof: Helm verification probes index.yaml, and the Helm executor
  does not consume Gateway-managed credential bindings.
- [x] Code-path proof: the Helm executor requires image overrides even for projects
  whose committed chart values are their sole configuration authority.
- [ ] Implement and test artifact verification, scoped consumption and full-values mode.
- [ ] Independent security/contract review, commit, push and CNB CI verification.
- [ ] Update the actual Agent/Gateway components required by the binding protocol.
- [ ] Verify/promote the existing staged credential and publish the business release.
- [ ] Browser acceptance and final audit.

## Bounds

At most three evidence-based repair cycles, 20 minutes per CI wait and 15 minutes
per controlled rollout. Two unchanged failed checks stop retries and require new
root-cause evidence or human input. Do not weaken verifiers or tests to proceed.
The Ali Agent has repeatedly been evicted under DiskPressure; restoring its
availability cannot remove pressure protection or replace it with a new target.

The ordinary CNB Release attachment helper checked metadata hashes, not downloaded
bytes. It is not evidence that DoOps authenticated artifact consumption worked.
Use CNB's documented Helm artifact interface for the candidate chart. A metadata
resource state of active does not establish an active credential version.
