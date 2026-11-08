---
name: doops-agent-ui-integration
scope: console-ai-assistant
owner: doops-maintainers
status: release-verification
---

# Agent Cloud Component Integration

The AI Assistant embeds `AgentWorkspace` and reuses Agent Cloud's
`createAgentWorkbenchRuntime`, `AgentSessionRuntimeV2` and `AgentSessionStore`.
Agent Cloud owns the React conversation, queue and protocol reduction; DoOps owns login,
target selection, the operation session, Gateway routing, and the deployment form.
No Agent Cloud server, tenant, Worker provisioning, or iframe is required.

The shared package comes from the Agent Cloud repository at
`packages/agent-ui/`. Its `composer` slot lets DoOps replace the free-text input
with its existing authorized deployment form while retaining the conversation,
execution progress, error feedback, and runtime lifecycle.

## Runtime Boundary

`components/agent-ui-panel.tsx` is the component host.
`lib/agent-ui-runtime.ts` only creates the shared runtime. It does not implement
a DoOps conversation engine. `lib/agent-session-transport.ts` uses the typed
v2 snapshot, command and delta protocol at `/api/agent-sessions`. The server
adapter uses the shared `AgentWorkbenchSessionSource`; it maps public ACP events
without implementing a second reducer. Every request includes the explicit
target and `session_id`. The Gateway retains authentication, authorization,
credential resolution, cancellation, and audit ownership.

- Model metadata comes only from `doops_agent_prompt mode=metadata`.
- Each session API request rechecks Gateway authorization. Snapshot and stream
  scopes are isolated by authenticated token, Gateway, target and operation session.
- Metadata must advertise `doops.sh/acp-events/v1` and the `acp` format.
  Unsupported Agents fail explicitly; there is no old-runtime compatibility path.
- Free-text messages use `operation=ask`.
- The unified database workbench uses `doops_agent_prompt` with `skill=dosql`; a read-only
  database request may carry an exact managed credential reference only with this explicit
  skill marker. Generic free-text `ask` cannot attach database credentials.
- Deployments use the validated form, explicit confirmation, `operation=apply`,
  and metadata-only `credential_refs`. The deployment input is never sent as
  a free-text `ask` message.
- The console uses Agent UI's native `developer` presentation so the composer,
  Agent label, model selector, attachments, work/code modes and execution
  surfaces match Agent UI. DoOps does not add a second operation-mode selector,
  database/repository binding bar or prompt prefix. The terminal result is a
  transport receipt (`Operation complete.`), not replacement answer text;
  Gateway audit and execution evidence remain authoritative.
- The console passes Agent UI's `codeWorkspace` for the current DoOps session
  workspace. Code view uses read-only workspace metadata, file reads, search,
  Git changes/Diff and Git history; it does not replace the existing Files panel
  or grant write access. Changed files under `output/`, `artifacts/` and
  `deliverables/` are exposed through Agent UI's Results pane and loaded through
  the same session-scoped workspace read boundary.
  Workspace reads preserve binary bytes for image/PDF previews, enforce the
  existing 1 MiB preview limit, and reject symlinks outside the session. New
  sessions show an empty workspace; actual files remain discoverable without
  a Git repository. Read failures are visible rather than displayed as no results.
- Stopping requests upstream cancellation and waits for the original RPC's
  terminal result; a cancel ACK does not release the active operation. Confirmed
  cancel produces a cancelled task receipt. A disconnect, timeout or authoritative
  `execution_outcome_unknown` marks that command as uncertain, rejects replay of
  the same command ID, and releases the active slot after the terminal failure is
  published. A new command in the same session remains usable; uncertain
  mutations are never automatically replayed.
- Stop the current task before switching targets. Session editing and new-session
  creation are disabled during execution. Unfinished tools are shown as
  unconfirmed after a lost terminal outcome, so they cannot leave the UI queue stuck.
- The shell keeps the workspace mounted across tab and configuration-page
  navigation, so active execution and the current transcript remain available.
  A new operation session or a target change creates a new runtime.

DoOps does not currently expose historical transcript reads or interactive
permission prompts. Conversation attachments are supported by the shared Agent
UI composer for read-only messages: up to four files, each no larger than 25 MiB.
The web server keeps each upload in a target/session-scoped temporary file until
the next prompt, then the Gateway stores it through DoAgent's native
`attachment/upload` RPC and sends an ACP attachment block. Deployment mode keeps
the controlled form and rejects attachments. Temporary uploads expire after 30
minutes; no document bytes are stored in browser storage or public audit output.
This change does not add server-side history persistence.
The normalized session source is process-local to the existing single-container
Web deployment. Restarting Web loses that display history; remote audit and
DoAgent state remain authoritative. Inactive, unsubscribed sessions expire after
30 minutes. Multiple Web instances require session affinity before this path can
be enabled; this change does not introduce distributed session storage.

## Acceptance Scenarios

1. Given an authenticated user and configured target, opening AI Assistant reads
   metadata, enables the composer, and two messages reach the same target/session.
2. Given a stream of tool output and assistant deltas, the user presentation
   preserves the streamed answer and terminal status without exposing the
   internal execution trace.
3. Given a session workspace with source files and deliverables, Agent UI
   exposes Code and Work views; Code supports file inspection and changes/Diff,
   while Work exposes previewable/downloadable deliverables.
4. Given an unconfigured model, invalid metadata, or denied ask permission,
   the error is explicit and message submission stays disabled.
5. Given a failed or interrupted task, the failure remains in its transcript;
   a later successful task clears the stale error banner.
6. Given a read-only conversation, selecting a PDF/DOCX/XLSX/TXT file shows the
   Agent UI attachment chip, and sending the prompt delivers the document through
   DoAgent's native attachment protocol. Empty, oversized, cross-session, and
   malformed uploads are rejected explicitly.
7. Given deployment mode, only the authorized form is present; submission includes
   `operation=apply` and credential resource names. A missing confirmation blocks it.
8. Given navigation away and back, the current transcript remains. Changing the
   target or starting a new session clears it and cannot accept old request events.
9. Given desktop and mobile viewports, the composer and target/session controls
   remain visible without horizontal page overflow or framework/console errors.

## Checks

Run `pnpm test:console`, `pnpm test:gateway-policy`, `pnpm exec tsc --noEmit`,
`pnpm build`, and `git diff --check`. Browser verification must exercise the
scenarios above through the console and `/api/agent-sessions`, then perform a read-only
two-turn doagent check against an existing authorized Gateway target.
The main-targeted GitHub pull-request pipeline also installs the frozen dependency
lock, runs both console test groups, typechecks, and builds the web application.

Source delivery, package publication, and production rollout are separate evidence.
Do not infer a deployed web version from a local build or an npm package release.

## Integration Evidence

### Shared Runtime Development

- The shared source has the Node-only `./server` export in published version
  `0.1.4`. Every producer passes its original stream epoch when publishing;
  resets cannot accept late events or acknowledgments from old handlers.
  Browser composition requires both a connected session and explicit
  authorized send capability; ACP mode alone is not permission.
- Agent Cloud source `1a45d3d9a92de0a711e0e1ade307615c5230c855` is pushed and
  tagged `agent-ui-v0.1.4`. Publication CI `github-6ru-1k472mqb6` passed, including
  independent tarball installation, Node server imports, portable declarations,
  browser conversation and unmount cleanup. PR 65 merged as
  `4223270bf0b1c57fe477f8e9d18a4db5b7116ed5`.
- DoOps pins the actual registry version and SHA-512 integrity in the lock.
  A clean pnpm 11.7.0 frozen install verifies that dependency graph but rejects
  this new package under its 24-hour supply-chain policy. No exception is set;
  automatic eligibility is 2026-10-07 06:28:44 Asia/Shanghai. Do not merge or
  deploy DoOps before frozen CI passes.
- Shared-source and capability tests, DoOps typecheck/build and focused Go
  protocol tests passed. HTTP checks verified authorization, version rejection,
  command digest/session isolation, deduplication, deployment confirmation and
  event cursor rejection. Browser checks verified two-turn chat, tool events,
  queue order, stopping/recovery, navigation, failed metadata and authorized forms.
- These checks use a synthetic ACP Gateway. They are not evidence of the new
  runtime on production. The previous live evidence below belongs to the
  component-only implementation.

### Previous Component-Only Delivery

- DoOps baseline: `d1cd4365f8652542c61291650468800313acd694`.
- Agent Cloud baseline: `29e42be269291ba23b7e21a384d27143c3a1116b`.
- Agent Cloud package source: `ed89c29e8c951467501caf8edbf17c556a536bb0`,
  tag `agent-ui-v0.1.3`; package publication CI `github-mba-1k46inj96` passed.
- Browser: 18 scenarios passed through the console's HTTP login and RPC proxy,
  including desktop 1440x1000, mobile 390x844, task survival across navigation,
  separate transport receipts, errors, metadata recovery, and authorized forms.
- Live acceptance: the local console completed two read-only prompts on
  `doops-edu/edu-coder` through `https://gateway.example.com`. The second prompt returned
  the first prompt's marker, verifying real doagent session context. No mutation
  or deployment was performed for this acceptance.
- Local production build, TypeScript, console tests, Gateway policy tests, and
  independent review passed. The web deployment itself is not covered by the
  local-console evidence.

## Delivery Gates

### October 11, 2026 Native Workspace Release

The native developer composer and removal of the ask/apply and database/repository
binding bars are on main at `9cc20373fb58bde2d531c09520837b15f7216b58`.
Production still serves v1.3.39, source `03a2062348e9528051ac2ca9ed4e8535e20343da`.
Main build `github-jvc-1k4lfeu6t` failed its workspace-search functional check:
the new grep arguments were incompatible with the Alpine runtime. The repair
uses Go filesystem traversal, preserves binary previews, handles non-Git results
and literal filenames, and corrects Git history revision arguments.

Release v1.3.40 requires passing exact-source GitHub Actions, immutable image provenance,
the CLI/Skill, Gateway/Web and core Ali/Tencent/edu Agent rollout, then live
conversation, code/search/history and result-preview browser checks. The older
cooldown and previous component-only records below are historical evidence.

Production standalone rendering also passed the 18 browser scenarios and the
real two-turn Gateway conversation. Source is pushed to the GitHub DoOps branch
`feat/agent-ui-embed` (PR 57); Agent Cloud is pushed to `feat/doops-composer-slot`
(PR 65). Neither main branch nor the production web service has been updated.

The web CI stage exposed pnpm 11's 24-hour publication cooldown. The third-party
`package-manager-detector` lock is corrected to compatible version `1.8.0`,
published on 2026-07-21. An isolated pnpm 11 frozen-install check now rejects only
the newly published first-party `@agent-cloud/agent-ui@0.1.3`.

No cooldown exception is enabled. The pending decision is whether to allow this
exact first-party version based on the source commit, passing release CI and
registry integrity. The newer pinned 0.1.4 release is eligible at
2026-10-07 06:28:44 Asia/Shanghai. Third-party
cooldown checks must remain enabled.

GitHub rejected the mirror push because the active account's email is not
verified. GitHub source visibility is confirmed; GitHub visibility is not claimed.
