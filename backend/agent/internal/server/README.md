# internal/server

One Go package holds both the Gateway control plane and the Agent runtime. Both binaries import
it: `backend/gateway` (via `pkg/gatewaycmd`) and `backend/agent/cmd/agent`.

## Naming trap

| Type | Defined in | Runs in |
|---|---|---|
| `Gateway` / `NewGateway` | `gateway.go`, `handler_ws.go` | **Agent** process. Executes tool calls received over the tunnel. |
| `GatewayHub` / `NewGatewayHub` | `tunnel_hub.go` | **Gateway** process. HTTP/WS entry, auth, routing, audit. |
| `GatewayStore` | `gateway_store*.go` | Gateway. SQLite persistence. |
| `GatewayAgent` | `agent_session.go` | Gateway. Its handle on one connected Agent. |

## Files by prefix

| Prefix | Side | Responsibility |
|---|---|---|
| `tunnel_hub.go`, `auth_http.go`, `http_response.go` | Gateway | Hub construction, route registration, login and HTTP responses |
| `client_rpc.go`, `client_tool_call.go`, `client_service.go` | Gateway | Client connection, individual call orchestration, active operation lifecycle |
| `agent_registration.go`, `agent_registry.go`, `agent_session.go`, `agent_relay.go`, `agent_message_queue.go` | Gateway | Register an Agent, maintain its connection, forward calls and buffer replies |
| `agent_leases.go`, `tool_resource_keys.go`, `gateway_actions.go` | Gateway | Concurrency admission, resource keys and action vocabulary |
| `execution_*` | Gateway | Execution lifecycle, terminal evidence, `outcome_unknown` |
| `gateway_store*`, `audit_*`, `admin_*`, `secret_box.go` | Gateway | Persistence, audit, admin API, encryption at rest |
| `credential_store*`, `credential_types.go`, `credential_validation.go`, `credential_http.go`, `credential_runtime.go`, `credential_lock.go`, `credential_import.go`, `credential_bundle_http.go`, `credential_materialization_store.go` | Gateway | Credential lifecycle, grants, verify/promote, materialization dispatch |
| `credential_resolve.go`, `credential_verify_http.go`, `credential_cleanup.go`, `credential_errors.go`, `credential_http_audit.go` | Gateway | Credential resolution, verification, cleanup and HTTP outcomes |
| `credential_materializer*.go`, `credential_tools.go`, `credential_scope.go` | Agent | Target-side credential materialization, Secret operations and verification probes |
| `edge_*` | Both | Edge tunnel. `edge_listener`, `edge_registry`, `edge_sni`, `edge_route` are Gateway; `edge_agent`, `edge_backend` are Agent; frame/conn/sched are shared |
| `proxy_*` | Both | HTTP proxy tunnel. `proxy_route_*` is Gateway; `proxy_tunnel.go` has both sides |
| `git_*` | Both | Git HTTP reverse tunnel. `git_tunnel.go` is Gateway; `git_http.go`, `git_ws.go` are Agent |
| `gateway.go`, `handler_ws.go`, `tool_notifications.go` | Agent | Agent runtime, inbound message loop and progress notifications |
| `command_exec.go`, `command_process_group.go`, `command_output.go` | Agent | Command start, process group lifecycle and output streams |
| `ws_transport.go`, `jsonrpc_response.go`, `redaction.go`, `tool_args.go`, `tool_operations.go` | Both | Wire I/O, response construction, output redaction and shared tool argument/operation rules |
| `tool_registry.go` | Gateway | Agent tool catalogue: client visibility, action and audit summary |
| `tool_dispatch.go`, `tool_handlers_*.go` | Agent | All executable Agent handlers, grouped by domain; dispatch does not grant client access |
| `doagent_*` | Agent | doagent ACP bridge, tool binding, final answer, UI events |
| `mutation_authority.go`, `json_digest.go`, `retired_ws.go` | Agent | Signed mutation authorization, attestation digests, retired inbound `/ws` |
| `dosql_*` | Agent | DoSql direct execution and database runner |
| `helm_*`, `native_helm_context.go`, `k8s_*` | Agent | Helm tool, managed Helm auth, Kubernetes MCP |
| `self_upgrade_request.go` | Both | Upgrade arguments and image validation |
| `self_upgrade.go`, `self_upgrade_cluster.go`, `self_upgrade_job.go`, `self_upgrade_scripts.go` | Agent | Upgrade orchestration, cluster discovery, Job lifecycle and embedded scripts |
| `knowledge_base.go`, `deployment_check.go`, `runtime_limits.go` | Runtime | Knowledge-base files, deployment checks and runtime limits |
| `ssh_command.go`, `web_tool.go`, `workspace_*`, `cleanup.go` | Agent | Structured SSH, DoWeb, workspace push/pull, workspace GC |
| `protocol_capabilities.go`, `prompt_authorization.go` | Both | Capability declarations and prompt credential checks used by Gateway and Agent |

## Adding a tool

1. In `tool_registry.go`, declare the Agent tool as `clientCallable` or `gatewayInternal`. An action classifies execution for audit and admission; it does not grant client access. The Gateway handles the public COS RPC itself; the Agent executes only its internal sync step.
2. Add the handler to `agentToolHandlers` and implement it in the relevant domain file. The parity test covers both public and private handlers. Credential and COS relays retain their internal actions; the diagnostic collector uses a direct relay with no action.
3. For tools with side effects, review the resource key and interruption rules. Test cancellation and disconnect without a terminal response before adding CLI or Web callers.
4. When the public Agent tool list changes, update `testdata/public-agent-tools.json`. The Go test checks it against the registry; the console tests check the actual `TOOLS` values against it. Add Web callers via `TOOLS` rather than duplicating wire names.

Protocol and lifecycle semantics are documented in
[DOOPS_GATEWAY_TUNNEL.md](../../../docs/DOOPS_GATEWAY_TUNNEL.md).
