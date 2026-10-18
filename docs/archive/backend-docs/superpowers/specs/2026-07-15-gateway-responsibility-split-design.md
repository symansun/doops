# Gateway Responsibility Split Design

## Goal

Separate gateway registration, user-facing calls, and per-agent connection handling so an
agent restart is an explicit lifecycle transition rather than a failed user RPC.

## Scope

- Keep the existing HTTP and WebSocket endpoints unchanged.
- Keep user authentication separate from tokenless agent registration.
- Persist agent session generations and status in SQLite.
- Move ownership out of `GatewayHub` without adding a second network service.
- Make registration freshness observable to user-call flows.

## Component Boundaries

### AgentRegistry

`AgentRegistry` owns the live `cluster/instance -> AgentSession` mapping and the persisted
agent status. It provides:

- register and replacement of an initialized agent session;
- unregister only when the disconnected session is still current;
- heartbeat updates and online target snapshots;
- live-session lookup;
- target status persistence with returned errors logged rather than discarded.

The registry owns no HTTP request handling, user authorization, audit records, or WebSocket
request forwarding.

### AgentSession

`AgentSession` owns one agent WebSocket connection. It provides:

- initialization, read loop, ping loop, and close notification;
- JSON-RPC and Git request relay;
- resource locks and per-session operation state;
- a target snapshot for the registry.

An `AgentSession` does not access `GatewayStore` and does not decide user permissions.

### AgentRegistrationHandler

`AgentRegistrationHandler` constructs an `AgentSession` without a registration token,
completes initialization and an exact ping/pong heartbeat preflight, sends a writable
registration acknowledgement, then registers it with `AgentRegistry` and waits for the
session to close. A candidate that fails any preflight step cannot replace or disconnect the
existing healthy session. The handler has no user-token authentication and does not dispatch
user tool calls.

### ClientService

`ClientService` owns user-facing RPC, Git HTTP, and internal calls. It performs user
authentication, action authorization, audit, concurrency limits, and active-operation
tracking. It gets a live session only through `AgentRegistry`.

`GatewayHub` remains the composition root for route registration, admin handlers, and the
scheduler. It delegates instead of directly owning the agent map or session protocol.

## Upgrade Lifecycle

An agent must not mutate the workload that runs itself in its own process.
The current `doops upgrade` and `doops_agent_upgrade` flow creates an
in-cluster Job that reuses the Agent's mounted kubeconfig, applies the image
change outside the Agent process, verifies the
replacement Pod and its gateway heartbeat, and performs rollback when verification fails.

The Helm readiness probe represents reverse-tunnel initialization, not merely the local agent
TCP listener. The agent becomes ready only after it answers the gateway initialize request and
remains ready while that reverse-tunnel session is alive. Liveness remains process-local.

## Error Handling

- A normal target call with no registered session returns `target offline`.
- A target whose persisted status is stale is reported offline by the registry; persistence
  errors are logged and surfaced to operation telemetry.
- Agent replacement closes only the old session. A late close from the old session cannot
  mark the replacement offline.
- A candidate registration is accepted only after its correlated heartbeat preflight succeeds.

No fallback transport or alternate target path is introduced.

## Verification

1. Registration tests prove replacement and stale-disconnect behavior through `AgentRegistry`.
2. Session tests prove ping/pong updates and relay behavior without user authorization setup.
3. Client-service tests prove authorization, audit, and offline behavior using a registry
   interface.
4. Upgrade tests prove non-dry-run requests are rejected before dispatch.
5. A candidate test proves an unsolicited Pong cannot satisfy registration preflight.
6. Helm rendering and a live browser-independent gateway smoke test verify that a target is not
   considered ready until it has registered through the reverse tunnel.
