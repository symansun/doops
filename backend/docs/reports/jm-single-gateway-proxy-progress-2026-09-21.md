# JM Single-Gateway Proxy Progress

Date: 2026-09-21

## Objective

Use the existing public Gateway at `doops.l8ai.cn` and one Agent that can reach
the JM network to expose `https://jm.aiedulab.cn` through a temporary
`*.l8ai.cn` test hostname.

The target topology is:

```text
public client
  -> temporary *.l8ai.cn hostname
  -> existing doops.l8ai.cn Gateway
  -> selected online Agent
  -> https://jm.aiedulab.cn (192.168.150.228:443)
```

There is no second Gateway. The public test hostname belongs to the `l8ai.cn`
test environment; `jm.aiedulab.cn` remains the Ali/JM upstream and its DNS is
not changed by this test.

## Acceptance Criteria

1. Identify an online Agent with a real TCP/TLS path to
   `192.168.150.228:443`.
2. Confirm that the Agent permits the exact upstream hostname
   `jm.aiedulab.cn`.
3. Create one temporary Gateway route under `*.l8ai.cn`, bound to that Agent
   and upstream `https://jm.aiedulab.cn`.
4. From outside the Agent network, complete a real HTTPS request through the
   Gateway and preserve the HTTP status, response headers, and response body
   evidence.
5. Exercise the public URL in a browser and inspect the rendered page,
   console, and failed network requests.
6. Delete the temporary route after verification unless the user asks to keep
   it, and confirm that the route no longer exists.

## Guardrails

- Do not create or deploy a second Gateway.
- Do not change public DNS for `jm.aiedulab.cn`.
- Do not use an Agent-online check as proof of upstream reachability.
- Do not weaken the Agent upstream allowlist or add a fallback route.
- Use SSH only to recover or inspect the offline JM Agent path; normal remote
  execution and verification use DoOps sessions.
- Stop after six remediation attempts or two consecutive attempts with the
  same unchanged network/authentication blocker, then report the exact failing
  command boundary and required operator action.

## Current State

- Gateway: `https://doops.l8ai.cn`
- Intended JM Agent: `doops-jm/jm-228`
- Intended upstream: `https://jm.aiedulab.cn`
- Resolved JM service address from prior Agent evidence:
  `192.168.150.228:443`
- Current blocker: `doops-jm/jm-228` is offline.
- Next action: test the online Agents, starting with
  `doops-oilan/oilan-node`, for direct TCP/TLS access to the JM service and
  inspect their exact proxy allowlist.

## Iteration Log

- Iteration 1: architecture corrected to one Gateway plus multiple Agents.
  No route or DNS mutation has been made.
- Iteration 2: `doops-jm/jm-228` and every other previously known Agent path to
  `192.168.150.228:443` were verified unavailable. The former 89 nested DoOps
  client is no longer installed.
- Iteration 3: verified the Ali ingress address `47.114.103.22` serves
  `https://jm.aiedulab.cn` with a valid certificate and HTTP 200 from both the
  workstation and `doops-oilan/oilan-node`. The response body is 15,441 bytes.
- Iteration 4: implementing an external-controller Agent release with
  `jm.aiedulab.cn -> 47.114.103.22` as an exact Pod host alias and
  `jm.aiedulab.cn` as the exact proxy allowlist entry.
