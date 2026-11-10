# JM Single-Gateway Proxy Progress

Date: 2026-09-21

## Objective

Use the existing public Gateway at `doops.l8ai.cn` and one Agent that can reach
the JM network to expose `https://jm.example.com` through a temporary
`*.l8ai.cn` test hostname.

The target topology is:

```text
public client
  -> temporary *.l8ai.cn hostname
  -> existing doops.l8ai.cn Gateway
  -> selected online Agent
  -> https://jm.example.com (Ali ingress 47.114.103.22:443)
```

There is no second Gateway. The public test hostname belongs to the `l8ai.cn`
test environment; `jm.example.com` remains the Ali/JM upstream and its DNS is
not changed by this test.

## Acceptance Criteria

1. Identify an online Agent with a real TCP/TLS path to the Ali ingress that
   serves `jm.example.com`.
2. Confirm that the Agent permits the exact upstream hostname
   `jm.example.com`.
3. Create one temporary Gateway route under `*.l8ai.cn`, bound to that Agent
   and upstream `https://jm.example.com`.
4. From outside the Agent network, complete a real HTTPS request through the
   Gateway and preserve the HTTP status, response headers, and response body
   evidence.
5. Exercise the public URL in a browser and inspect the rendered page,
   console, and failed network requests.
6. Keep the temporary route available for the user's verification because the
   user asked for a test address; delete it only when the user asks.

## Guardrails

- Do not create or deploy a second Gateway.
- Do not change public DNS for `jm.example.com`.
- Do not use an Agent-online check as proof of upstream reachability.
- Do not weaken the Agent upstream allowlist or add a fallback route.
- Use SSH only to recover or inspect the offline JM Agent path; normal remote
  execution and verification use DoOps sessions.
- Stop after six remediation attempts or two consecutive attempts with the
  same unchanged network/authentication blocker, then report the exact failing
  command boundary and required operator action.

## Current State

- Gateway: `https://doops.l8ai.cn` (`106.54.197.139`)
- Selected Agent: `doops-oilan/oilan-node`
- Original JM Agent: `doops-jm/jm-228` remains offline and is not used by the
  test route.
- Upstream: `https://jm.example.com`, pinned for this Agent as
  `47.114.103.22:443` with TLS SNI/Host `jm.example.com`.
- Agent release: Helm revision 29; exact allowlist contains
  `jm.example.com`; Pod host alias maps it to `47.114.103.22`.
- Gateway route: `jm-proxy.l8ai.cn` -> `doops-oilan/oilan-node` ->
  `https://jm.example.com/`, enabled and retained for user verification.
- DNS: `jm-proxy.l8ai.cn` still follows the existing wildcard to
  `89.250.81.244`; no DNS mutation has been made. Exact testing therefore
  uses `curl --resolve` to `106.54.197.139`.

## Iteration Log

- Iteration 1: architecture corrected to one Gateway plus multiple Agents.
  No route or DNS mutation has been made.
- Iteration 2: `doops-jm/jm-228` and every other previously known Agent path to
  `192.168.150.228:443` were verified unavailable. The former 89 nested DoOps
  client is no longer installed.
- Iteration 3: verified the Ali ingress address `47.114.103.22` serves
  `https://jm.example.com` with a valid certificate and HTTP 200 from both the
  workstation and `doops-oilan/oilan-node`. The response body is 15,441 bytes.
- Iteration 4: implementing an external-controller Agent release with
  `jm.example.com -> 47.114.103.22` as an exact Pod host alias and
  `jm.example.com` as the exact proxy allowlist entry.
- Iteration 5: the first real handoff reached Helm revision 24, but the
  release loop accepted the still-serving old Pod because the image digest was
  unchanged. The later verifier then read the isolated old-Pod command
  environment and automatically rolled back to revision 23. No route or DNS
  mutation had been made.
- Iteration 6: the release controller is being corrected to record the
  pre-release Pod, require a different Ready Pod to be the Gateway-selected
  hostname, and verify the candidate directly through the external Kubernetes
  controller.
- Iteration 7: released the corrected Agent through the external Helm
  controller at revision 29. The replacement Pod is Gateway-selected, has the
  exact proxy allowlist and host alias, and resolves `jm.example.com` to
  `47.114.103.22` inside the Pod.
- Iteration 8: created one route on the existing Gateway and completed real
  public GET/POST checks. GET returned HTTP 200 with a body hash matching the
  direct upstream; POST returned the same HTTP 405 as the upstream.
- Iteration 9: found and fixed the remaining Host forwarding defect. The Agent
  now strips client `Forwarded` headers and sends the upstream host in both
  `Host` and `X-Forwarded-Host`, so JM tenant resolution sees
  `jm.example.com` rather than `jm-proxy.l8ai.cn`.
- Iteration 10: deployed Gateway commit `b06e197` to
  `106.54.197.139`; the deployed `/proc` executable hash is
  `89292459d04498b655670206740e36070d95ed8959c51f9dcef458e37e15f3e3`.
  Gateway health returned `200`, unauthenticated target listing remained
  `401`, and `doops-oilan/oilan-node` reconnected.
- Iteration 11: verified the retained route
  `route_e1f7a64862d429e0a05fd369` from outside the Agent network using
  `curl --resolve jm-proxy.l8ai.cn:443:106.54.197.139`. GET returned `200`
  with 15,441 bytes and an identical body hash to the direct upstream. POST
  returned the same upstream `405` and 157-byte body. The tenant API returned
  the same `404 HOST_NOT_MAPPED` through both paths.
- Iteration 12: loaded the site in real Chrome with a host resolver mapping
  `jm-proxy.l8ai.cn` to `106.54.197.139`. After the site's parallel asset
  loading completed, the proxy page rendered the same tenant-config error
  page as the direct upstream. There were no failed browser requests or
  proxy-specific console errors; the four `404` responses were the upstream
  tenant bootstrap endpoints.

## User Verification

The exact public test route is:

```text
https://jm-proxy.l8ai.cn/
```

At present both Cloudflare DoH and Google DoH resolve this name to the
existing wildcard address `89.250.81.244`, not the current Gateway
`106.54.197.139`. DNS was intentionally not changed. Verify the new route
without relying on the old wildcard with:

```bash
curl --noproxy '*' \
  --resolve jm-proxy.l8ai.cn:443:106.54.197.139 \
  -i https://jm-proxy.l8ai.cn/
```

For a browser, add the equivalent temporary host mapping or use Chrome with:

```text
--host-resolver-rules=MAP jm-proxy.l8ai.cn 106.54.197.139
```

The remaining `HOST_NOT_MAPPED` page is an upstream Ali/JM tenant
configuration result. It is identical when accessing
`https://jm.example.com` directly with the same upstream ingress, so it is
not a Gateway or Agent transport failure.
