# internal/server

One Go package holds both the Gateway control plane and the Agent runtime. Both binaries import
it: `backend/gateway` (via `pkg/gatewaycmd`) and `backend/agent/cmd/agent`.

## Naming trap

| Type | Defined in | Runs in |
|---|---|---|
| `Gateway` / `NewGateway` | `gateway.go`, `handler_ws.go` | **Agent** process. Executes tool calls received over the tunnel. |
| `GatewayHub` / `NewGatewayHub` | `tunnel_hub.go` | **Gateway** process. HTTP/WS entry, auth, routing, audit. |
| `GatewayStore` | `gateway_store*.go` | Gateway. SQLite persistence. |
| `GatewayAgent` | `tunnel_hub.go` | Gateway. Its handle on one connected Agent. |

## Files by prefix

| Prefix | Side | Responsibility |
|---|---|---|
| `tunnel_hub.go`, `client_service.go`, `agent_registry.go`, `agent_registration.go` | Gateway | Client RPC entry, Agent sessions, dispatch to Agents |
| `execution_*` | Gateway | Execution lifecycle, terminal evidence, `outcome_unknown` |
| `gateway_store*`, `audit_*`, `admin_*`, `secret_box.go` | Gateway | Persistence, audit, admin API, encryption at rest |
| `credential_store*`, `credential_http.go`, `credential_runtime.go`, `credential_lock.go`, `credential_import.go`, `credential_bundle_http.go`, `credential_materialization_store.go` | Gateway | Credential lifecycle, grants, verify/promote, materialization dispatch |
| `credential_materializer.go`, `credential_tools.go`, `credential_scope.go` | Agent | Writes Secrets on the target cluster for `doops_credential_*` tools |
| `edge_*` | Both | Edge tunnel. `edge_listener`, `edge_registry`, `edge_sni`, `edge_route` are Gateway; `edge_agent`, `edge_backend` are Agent; frame/conn/sched are shared |
| `proxy_*` | Both | HTTP proxy tunnel. `proxy_route_*` is Gateway; `proxy_tunnel.go` has both sides |
| `git_*` | Both | Git HTTP reverse tunnel. `git_tunnel.go` is Gateway; `git_http.go`, `git_ws.go` are Agent |
| `gateway.go`, `handler_ws.go` | Agent | Tool-call dispatch (`case "doops_*"` in `handler_ws.go`), exec, ask, workspace |
| `doagent_*` | Agent | doagent ACP bridge, tool binding, final answer, UI events |
| `mutation_authority.go`, `json_digest.go`, `retired_ws.go` | Agent | Signed mutation authorization, attestation digests, retired inbound `/ws` |
| `dosql_*` | Agent | DoSql direct execution and database runner |
| `helm_*`, `native_helm_context.go`, `k8s_*` | Agent | Helm tool, managed Helm auth, Kubernetes MCP |
| `self_upgrade.go` | Agent | `doops upgrade` in-cluster Job controller |
| `ssh_command.go`, `web_tool.go`, `workspace_*`, `cleanup.go` | Agent | Structured SSH, DoWeb, workspace push/pull, workspace GC |
| `protocol_capabilities.go`, `prompt_authorization.go` | Both | Capability declarations and prompt credential checks used by Gateway and Agent |

Protocol and lifecycle semantics are documented in
[DOOPS_GATEWAY_TUNNEL.md](../../../docs/DOOPS_GATEWAY_TUNNEL.md).
