---
name: gateway-console-release-2026-10-02
scope: deployment-and-browser-acceptance
owner: doops-maintainers
---

# Gateway Console Release And Acceptance

## Objective

Release the reviewed DoOps Gateway, console and CLI from the exact successful CNB
commit. Verify administrator management and authorized user deployment, operations
and audit through rendered pages, with live API and target evidence.

## Completion Conditions

- Reviewed commit and push are remotely visible; exact-commit CI and immutable
  Gateway/Web artifacts succeed; CLI and Skill come from the same released commit.
- Gateway binary source commit and Web image digest match the CI artifacts; existing
  target connections recover and health checks pass. Record rollback artifacts.
- Browser acceptance covers authentication, user/token/target grants, credential
  lifecycle, explicit Helm apply, deterministic operations, errors and cancellation,
  own-user audit isolation and administrator audit. Record any untested live action.
- No payload enters prompts, audit or source. No workspace/push/local kubeconfig
  requirement is introduced for ordinary workload deployment.

## Execution Bounds

At most three repair iterations per failed path and three production attempts per
artifact; stop on repeated identical failures, CI artifact mismatch, unavailable
audit or failed rollback. Observe CI and deployment with bounded tools. Escalate
missing authentication or production input explicitly. No synthetic result is a
substitute for live acceptance.

## Progress

- Source baseline: 10d14f80314de19a39bc8c72b083166ce2c1b042 on
  fix/helm-image-mode-contract; the branch has no build pipeline artifact yet.
- Confirmed defects: AI UI reads legacy settings instead of model metadata; no
  explicit apply request; RPC tool isError ignored; ordinary user audit unavailable;
  administrator mutation audit coverage incomplete; credential lifecycle incomplete.
- In progress: repair application paths, review permission/payload boundaries,
  browser checks, exact-commit CI and controlled Gateway/Web/CLI release.
- Production browser initially has demo mode. Administrator login requested through
  the existing console without exposing a password or token in chat.
- Local verification passed: Agent/Gateway/CLI Go suites, 50 Python contracts,
  5 Gateway-origin policy tests, 7 console transport/deployment tests, TypeScript
  and the production Web build. The new persisted credential metadata endpoints
  reject cross-owner/unauthenticated access and exclude payload/ciphertext.
- Browser on the production build against the real local Gateway/SQLite control
  plane verified ordinary-user login, successful and failed RPC commands, own
  audit records, global-audit denial and status filtering. The runtime is explicitly
  stubbed; this is not a live Kubernetes or production-user acceptance.
  Screenshot: /tmp/doops-console-browser-qa/own-failed-audit.png. No console errors.
- Remaining known product gaps: the legacy model configuration center uses an old
  settings schema and browser-local API-key templates; binary/directory upload
  still depends on CLI synchronization. Do not claim all management is web-complete.
