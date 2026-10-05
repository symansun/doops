---
name: doops-agent-ui-integration
scope: console-ai-assistant
owner: doops-maintainers
status: accepted
---

# Agent Cloud Component Integration

The AI Assistant embeds `AgentWorkspace` from `@agent-cloud/agent-ui@0.1.3`.
Agent Cloud owns the shared React conversation components; DoOps owns login,
target selection, the operation session, Gateway routing, and the deployment form.
No Agent Cloud server, tenant, Worker provisioning, or iframe is required.

The shared package comes from the Agent Cloud repository at
`packages/agent-ui/`. Its `composer` slot lets DoOps replace the free-text input
with its existing authorized deployment form while retaining the conversation,
execution progress, error feedback, and runtime lifecycle.

## Runtime Boundary

`components/agent-ui-panel.tsx` is the component host.
`lib/agent-ui-runtime.ts` implements `AgentSessionRuntime` using the existing
`callTool` client and `/api/rpc` proxy. Every request includes the explicit
target and `session_id`. The Gateway retains authentication, authorization,
credential resolution, cancellation, and audit ownership.

- Model metadata comes only from `doops_agent_prompt mode=metadata`.
- Free-text messages use `operation=ask`.
- Deployments use the validated form, explicit confirmation, `operation=apply`,
  and metadata-only `credential_refs`. The deployment input is never sent as
  a free-text `ask` message.
- Assistant deltas and tool output are displayed separately. The terminal result
  is a transport receipt (`Operation complete.`), not replacement answer text;
  completion requires the existing RPC terminal evidence check. Tool failures
  remain visible in the transcript.
- Stopping or changing an active conversation's target/session stops local waiting and requests
  upstream cancellation. Without remote terminal evidence it is displayed as
  an unconfirmed outcome, never as successful remote cancellation.
- The shell keeps the workspace mounted across tab and configuration-page
  navigation, so active execution and the current transcript remain available.
  A new operation session or a target change creates a new runtime.

DoOps does not currently expose historical transcript reads, interactive
permission prompts, or chat attachments. These capabilities are not advertised;
file upload remains available through the Files page. This change does not add
server-side history persistence or browser storage of conversation contents.

## Acceptance Scenarios

1. Given an authenticated user and configured target, opening AI Assistant reads
   metadata, enables the composer, and two messages reach the same target/session.
2. Given a stream of tool output and assistant deltas, the UI renders both and
   preserves the streamed answer after the separate terminal receipt arrives.
3. Given an unconfigured model, invalid metadata, or denied ask permission,
   the error is explicit and message submission stays disabled.
4. Given a failed or interrupted task, the failure remains in its transcript;
   a later successful task clears the stale error banner.
5. Given deployment mode, only the authorized form is present; submission includes
   `operation=apply` and credential resource names. A missing confirmation blocks it.
6. Given navigation away and back, the current transcript remains. Changing the
   target or starting a new session clears it and cannot accept old request events.
7. Given desktop and mobile viewports, the composer and target/session controls
   remain visible without horizontal page overflow or framework/console errors.

## Checks

Run `pnpm test:console`, `pnpm test:gateway-policy`, `pnpm exec tsc --noEmit`,
`pnpm build`, and `git diff --check`. Browser verification must exercise the
scenarios above through the console and `/api/rpc`, then perform a read-only
two-turn doagent check against an existing authorized Gateway target.
The main-targeted CNB pull-request pipeline also installs the frozen dependency
lock, runs both console test groups, typechecks, and builds the web application.

Source delivery, package publication, and production rollout are separate evidence.
Do not infer a deployed web version from a local build or an npm package release.

## Integration Evidence

- DoOps baseline: `d1cd4365f8652542c61291650468800313acd694`.
- Agent Cloud baseline: `29e42be269291ba23b7e21a384d27143c3a1116b`.
- Agent Cloud package source: `ed89c29e8c951467501caf8edbf17c556a536bb0`,
  tag `agent-ui-v0.1.3`; package publication CI `cnb-mba-1k46inj96` passed.
- Browser: 18 scenarios passed through the console's HTTP login and RPC proxy,
  including desktop 1440x1000, mobile 390x844, task survival across navigation,
  separate transport receipts, errors, metadata recovery, and authorized forms.
- Live acceptance: the local console completed two read-only prompts on
  `doops-edu/edu-coder` through `https://doops.l8ai.cn`. The second prompt returned
  the first prompt's marker, verifying real doagent session context. No mutation
  or deployment was performed for this acceptance.
- Local production build, TypeScript, console tests, Gateway policy tests, and
  independent review passed. The web deployment itself is not covered by the
  local-console evidence.
