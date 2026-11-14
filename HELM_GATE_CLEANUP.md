---
name: helm-gate-cleanup
scope: task
status: in-progress
---

# Helm Gate Cleanup

## Goal

Allow Helm to manage any valid Chart, including releases containing only
NetworkPolicy, ConfigMap, RBAC, CRD, custom resources, or Jobs. Remove DoOps
resource-kind, image, Ingress, and redundant confirmation gates from code,
prompts, and canonical skills.

## Acceptance

- Helm receives the requested release, namespace, Chart, and values without
  a DoOps manifest preflight or mandatory reset of existing values.
- A successful upgrade returns its actual deployed revision and release state.
  Failed commands, invalid receipts, and interrupted calls remain failures.
- Public downloads never receive a private repository's credentials.
- A NetworkPolicy-only Chart completes through the managed Helm tool.
- Canonical skills and documentation describe the implemented behavior.
- A rejected Helm call can be corrected within the same authorized turn.
  A known execution failure can also be repaired by a later serial success for
  the same release and intent. Receipts preserve both attempts; unknown outcomes
  and a different rollback revision or Chart cannot be hidden by later success.
- A connected task with no output for 60 seconds remains active. Execution has
  a total deadline, cancellation waits for the original RPC terminal result,
  and missing evidence remains unknown.

## Progress

- [x] Locate the rejection: `validateHelmRenderedContent` requires a workload
  or Ingress, and rejects all other otherwise valid Charts.
- [x] Identify related gates in command building, attestation validation,
  model prompts, and operator/runtime skills.
- [x] Remove custom Helm policy and preserve native command/result handling.
- [x] Verify functional success/failure paths and run relevant regression checks.
- [x] Review the Helm gate and receipt changes, commit/push, and verify CI.
- [ ] Publish/install matching artifacts and verify the deployed target.

## Current Findings

- The initial Loki incident compared the unrelated CNB credential origin
  `helm.cnb.cool:443` with the public Chart origin `github.com:443`.
- Existing diagnostic session evidence permits verification without creating
  Loki resources. The public Chart also requires storage configuration, so a
  successful anonymous download alone is not proof of a deployable Loki setup.
- On 2026-11-14, `gw-test` still reports
  `76abae08a36d920c0058486d2d8ab86880c4c121`; the changes are not deployed.
- Review found a 45-second first-output timer terminating otherwise connected
  console operations, and historical parameter failures poisoning later
  successful corrections. Both are being fixed with functional coverage.
- Work is split between result/repair handling, origin credential selection,
  and console transport; each worker has disjoint files.
- 2026-11-14 follow-through: extend recovery beyond unexecuted validation and
  preparation errors to known Helm execution failures, preserving the original
  intent and both receipts. Verify stdio-to-WebSocket behavior and unknown
  outcome controls before committing.
- PR #12 merged the gate cleanup and receipt changes as
  `fe01a931ebef3eda84c1099303b0151b88210508`. Main CI run `43617931524`
  and release run `43619006463` both passed; GitHub Release `v0.3.2` is
  published.

## Execution Bounds

Use at most three corrective iterations for the same failing check before
recording its root cause and external dependency. Do not replay a mutation
with an unknown terminal outcome. Stop release work when no authorized
build artifact, reachable target, or required release access is available;
record the exact blocker and completed evidence here.

## Guidance Cleanup (2026-11-14)

- [x] Define business delivery in `backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md`;
  operator rules, build/model docs and README entries link to that owner.
- [x] Remove the unused `backend/skills/config/servers.json` and duplicated
  Helm workspace contract. CLI configuration remains the existing user store.
- [x] Remove retired local-controller examples and generic version-equality,
  CI-proof and build-location requirements from business delivery guidance.
- [x] Scope `image-build` to requested builds and delegate Helm operations to
  the `k8s` skill. Keep target grants, credential isolation and truthful receipts.
- [x] Review residual prerequisite lists and preserve the supported dedicated
  ServiceAccount upgrade path in the canonical SOP and its API references.
- [x] Validate 114 documentation, installer, managed-release and host-access
  checks; focused Helm and CLI tests pass, including NetworkPolicy-only Charts,
  native values, release receipts and canonical configuration parsing.
- [x] Package the actual Skill/docs bundle, verify its checksum and compare the
  packaged entrypoint with source; deleted files are absent. `git diff --check`
  passes.

These checks verify the local source and package. They do not establish a
published release, update installed Skill copies or prove online acceptance.

## Verification Follow-through

- Agent server package completed successfully in 747 seconds on 2026-11-14.
  The earlier run hit the default 10-minute limit; the completed JSON test log
  contains no failed tests and ends with package `pass`.
- Console regression tests: 640 passed, zero failed or skipped. CLI package and
  natural-language deployment contract checks passed.
- A remaining false positive classified `compat` as a PAT field. Match actual
  PAT field boundaries and known provider names, and exercise both the accepted
  Chart key and rejected credential keys.
- Online reread confirms `gw-test` routes to `doops-zyj/zyj-test`, still running
  `76abae08a36d920c0058486d2d8ab86880c4c121`. Published and deployed acceptance is
  not yet established.

## Autonomous Recovery Follow-through (2026-11-15)

- [x] Reread `gw-test`: the installed Agent still reports the old commit,
  doagent is `0.2.42`, and no `loki` release exists in namespace `test`.
- [x] Enable the existing doagent completion review for authorized apply turns;
  preserve the single turn, original mutation authority and all tool receipts.
- [x] Review and verify against the pinned doagent `0.2.44` source commit
  `e4d052e62392fbc7a3ef02cb22765215f53e301f`. Follow-up
  `cec8f0fd2c5bab57bbc2e5ca64d46f819b1169a3` is pushed in PR #13;
  both CI runs passed Go, contract and secret checks.
- [x] Confirm CLI and global Skill installation from release `v0.3.2`,
  commit `fe01a931ebef3eda84c1099303b0151b88210508`.
- [ ] Obtain an immutable Agent artifact from the existing controlled build
  path and verify the target version.
- [ ] Verify a NetworkPolicy-only release and the original Loki Chart through
  the deployed managed tool; retain the tool result, exit state and readback.

Recovery completes only when actual tool evidence confirms the requested result.
The existing doagent completion review bounds continuation, and the DoOps prompt
has a 30-minute deadline. Unknown mutation outcomes require readback before any
new action; a model summary cannot resolve them. Three corrective iterations
without new evidence identify a concrete dependency rather than repeat a mutation.

## Recovery And Installed Guidance Verification (2026-11-15)

- Pinned doagent tests executed an initial failing tool call, rejected a
  premature final reply, and executed a corrected call successfully in the same
  user request. Three tests passed, covering repair, bounded continuation and
  invalid review output. Model replies were deterministic fixtures; this does
  not establish deployed model behavior.
- The Agent MCP/SSE/WebSocket tests verify complete failure and correction
  receipts, progress, terminal results and preservation of unknown outcomes.
- The development link script now moves old Skill/docs and historical backup
  entries outside the Skill load tree, preserves old links, and does not
  recreate backups for an unchanged link. The 22 focused shell/document
  contract tests pass.
- Old `docs.release` and `*.devlink-backup` entries were moved out of the
  repository's installed Skill tree. The parent workspace also held a separate
  old Skill copy. Its formal reinstall failed before replacement with a
  GitHub Release TLS error; it is now explicitly linked to the canonical
  development sources, with the old files retained under `.local/`.
  This development link is not a successful release installation.
- The release task published `v0.3.2`, but Agent image push failed because the
  controlled Docker builder lacks registry authentication. The current registry
  credential setup verifies a Kubernetes Secret and cannot provision this
  standalone builder. No target upgrade is claimed: `gw-test` still reports
  `76abae08a36d920c0058486d2d8ab86880c4c121`.
