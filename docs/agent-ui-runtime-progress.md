---
name: agent-ui-runtime-convergence
scope: ai-conversation
owner: doops-maintainers
status: release-verification
---

# Shared Agent UI Runtime

User direction: AI conversation must use Agent Cloud agent-ui's existing
implementation, including its runtime, rather than preserving the DoOps
conversation engine behind replacement components.

## Acceptance

- The DoOps client imports `createAgentWorkbenchRuntime` and uses
  `AgentSessionRuntimeV2`; no DoOps class implements the conversation runtime.
- The shared `AgentSessionStore` owns message/event reduction. DoOps adapters
  contain Gateway routing, authentication and authorized deployment dispatch.
- Structured ACP tool/message/plan/terminal updates survive the Gateway bridge
  after authorization, evidence validation and secret redaction.
- Completion and cancellation retain actual Gateway terminal evidence.
- Browser checks cover multi-turn chat, tools, errors, queueing, interruption,
  navigation, target isolation and desktop/mobile layouts.
- Builds and focused transport/protocol checks pass; production rollout is
  separate and cannot use old-runtime fallback.

## Work

1. Inspect shared runtime and native ACP contract: complete.
2. Reuse shared runtime/store through a normalized session transport: implemented.
3. Replace the DoOps runtime implementation and wire the authorized composer: implemented.
4. Verify protocol, browser behavior and documentation: local validation complete.
5. Publish/pin shared package: complete; DoOps source delivery in progress.
6. Production rollout and live acceptance: scope confirmation and CI gates required.

## Release Plan

1. Review both change sets and verify the standalone shared package, including
   Node server exports and the new protocol/capability tests in publication CI.
2. Commit/push Agent Cloud, publish immutable `agent-ui-v0.1.4`, confirm CI and
   registry version/integrity before changing DoOps' dependency.
3. Pin the actual registry release, replace the local artifact with a clean
   frozen install, verify the console and Go bridge, then commit/push DoOps.
4. Confirm PR/CI, immutable Agent/Web source-commit/digest provenance and the
   explicitly authorized production scope. Retain current rollback artifacts.
5. Publish through the supported release controllers, verify health and native
   ACP metadata, then perform real two-turn browser conversation and failure checks.

No third-party cooldown is weakened. Any first-party version exception requires
the user's explicit decision. Active operations on the acceptance Agent are not
cancelled or force-unlocked for this release.

## Current Evidence

- `lib/agent-ui-runtime.ts` imports the shared factory; the previous custom
  runtime class is removed. Transport uses protobuf JSON and strict NDJSON.
- DoOps now supplies Agent UI's native `codeWorkspace` and workspace-backed
  deliverables. The Code pane reads the current session workspace through the
  existing Gateway file boundary; the Work pane previews changed files under
  `output/`, `artifacts/` and `deliverables/`.
- The DoOps host uses Agent UI's native developer composer. Custom ask/apply
  mode controls and database/repository prompt bindings were removed; deployment
  forms remain in the separate deployment surface.
- The server source uses the shared store, not a DoOps reducer. Protocol
  metadata, request/session ownership, public ACP projection, Gateway final
  results and authorized deployment forms are enforced.
- Browser exposed and fixed missing interrupt capability/grants, sending before
  readiness, and uncertain commands blocking all follow-up after an unknown
  outcome. The same command ID cannot be replayed, while a new command remains
  usable after the failure is published.
- Shared source/capability/release: 16 checks passed. Console/policy: 25 checks passed.
  HTTP: 7 checks passed, including verified apply with a structured warning.
  Real Go SSE bridge tests verify structured tool/message/terminal delivery and
  split-secret redaction. Focused Go protocol checks, typecheck and build passed.
- Agent Cloud source `1a45d3d9a92de0a711e0e1ade307615c5230c855`, tag
  `agent-ui-v0.1.4`, publication CI `cnb-6ru-1k472mqb6`: remotely visible and
  successful. Registry SHA-512 is pinned in DoOps. PR 65 merged to main
  `4223270bf0b1c57fe477f8e9d18a4db5b7116ed5`; main contains the release source.
- Independent review found and fixed late old-epoch publication and cancellation
  ACKs prematurely releasing a same-SID operation. Portable WebSocket tests
  cover confirmed cancellation, ACK-only timeout/disconnect, outcome_unknown,
  same-SID prompt/apply denial and new-session isolation. These run in console CI.
- Clean frozen pnpm 11.7.0 installation fails only on the new first-party
  package's 24-hour age policy. Eligibility is 2026-10-07 06:28:44 Asia/Shanghai.
  No compatibility path, cooldown exception or release-gate bypass is enabled.
- In-app-browser desktop 1440x1000 and mobile 390x844 passed meaningful-page,
  interaction, no-overlay, console and horizontal-overflow checks. Mobile
  transcript scrolling exposes the latest answer. Screenshots are outside Git:
  `/tmp/doops-agent-ui-v2-desktop.jpg` and `/tmp/doops-agent-ui-v2-mobile.jpg`.
- Running/pending task context switches are blocked with visible feedback.
- Shared package publication is complete; production DoOps rollout is not.
  Record the DoOps push SHA below, wait for passing exact-source CI and confirmed
  target scope, then release Agent/Web and verify live chat. Do not cancel other
  operations on gw-edu-coder to obtain a release slot.

## Existing Delivery Conditions

The previous component-only implementation is on `feat/agent-ui-embed`.
Agent Cloud worktree: `/tmp/agentcloud-doops-ui-20261006`.
The package release cooldown decision and GitHub email-verification restriction
remain unresolved; neither authorizes weakening checks or credential bypass.

## October 11, 2026 Release

- Scope: the native AI workspace on main, CLI/Skill and Gateway/Web, with core
  Agent targets `gw-114-k8s`, `gw-zyj` and `gw-edu-coder`. No Worktree.
- Existing UI convergence source: `9cc20373fb58bde2d531c09520837b15f7216b58`.
- Production before release: v1.3.39 / `03a2062348e9528051ac2ca9ed4e8535e20343da`.
- Main CI `cnb-jvc-1k4lfeu6t` failed workspace search on Alpine. The root repair
  removes grep from this API, corrects Git history and literal filenames, exposes
  actual non-Git deliverables, and preserves binary preview bytes with session
  filesystem confinement. Workspace errors are visible in the native header.
- Local console/policy tests, TypeScript, production build and workspace
  filesystem checks passed. Independent review and live browser verification
  remain required before completion.
- Next: commit/push this repair on main, publish v1.3.40 from the same source,
  verify CI/digests, roll out through the release controllers and test the real
  browser conversation/code/results workflow.
- Independent review also found stale-running disconnects disabling manual
  resynchronization and retained panels failing to refresh after Files edits.
  Reconnect remains available after disconnect without clearing/replaying tasks;
  returning to the AI panel refreshes workspace-backed results.
- Bounds: at most three repairs of the same failure, 20 minutes per CI build
  observation and 10 minutes per target rollout. Preserve unrelated sessions;
  never force-unlock, bypass checks or report a pending rollout as complete.

### Live Acceptance Follow-up

- v1.3.40 main/tag CI passed. CLI/Skill, Gateway/Web and Ali/Tencent/edu
  were rolled out from source `b2ce4f01697953cebf74bfa51744457e721d2502`.
- Live browser passed two-turn conversation, removed mode/binding controls,
  real code file reads, non-Git Markdown results, PNG rendering and download
  byte verification, authorized login and explicit 401/403 failures.
- Rapid page switching exposed the shared root cause of intermittent read
  refusal: `isConcurrentQuery` excluded ActionRead, placing workspace viewers
  into the session mutation channel. The exception is removed; explicit
  target/action authorization, session write/exec exclusion, maintenance
  isolation and Git pull's snapshot lock remain unchanged.
- v1.3.41 must pass exact-source CI and replace the same core runtime scope,
  followed by live rapid-navigation, active-session inspection, Git/Diff and
  desktop/mobile browser checks before closing this release.
