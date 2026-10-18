# `ds.aihub.org.cn` -> local 3080 acceptance

Date: 2026-09-25

## Target

Expose the workstation service at `http://127.0.0.1:3080/` through the existing
single Gateway at `106.54.197.139`. No second Gateway was created.

The persistent route is:

```text
ds.aihub.org.cn
  -> doops-gateway
  -> doops-local/local-3080 Agent reverse WebSocket
  -> http://127.0.0.1:3080/
```

Gateway route ID: `route_f24f1a5527b794815d1526f9`.

## Evidence

- Local upstream: HTTP `200`, `X-Upstream: local-3080-test`.
- Agent process: `doops-agent -gateway-url http://106.54.197.139:42222`,
  `cluster=doops-local`, `instance=local-3080`.
- Agent reverse tunnel: connected to `106.54.197.139:42222`.
- Gateway route: enabled and bound to `doops-local/local-3080`.
- Public DNS: `ds.aihub.org.cn A 106.54.197.139`.
- External request from the online `gw-114-k8s` Agent:
  - `GET http://ds.aihub.org.cn/`
  - HTTP `200`
  - `X-Upstream: local-3080-test`
  - body: `local-3080-test\npath=/\nhost=127.0.0.1:3080\n`
  - body SHA-256:
    `64cb95bf00e91db3cccb066dfec47733670a3fe281d6082b76902b4cc47a7f1c`
- Repository smoke test: `proxy_tunnel_smoke=PASS`.
- Go Agent and Gateway tests: passed.

## HTTPS limitation

The Gateway currently serves the existing `*.l8ai.cn` certificate. It does not
cover `ds.aihub.org.cn`, so HTTPS must not be reported as ready.

An attempted Let's Encrypt HTTP-01 issuance failed because the CA's secondary
validation timed out while connecting to port 80. The Nginx and Gateway
services were restored, and no invalid certificate reference was published.
Issuing a certificate requires a DNS-01 flow or a valid certificate covering
`ds.aihub.org.cn`, plus a subsequent 443 Nginx deployment.

Until that is completed, the verified public address is:

```text
http://ds.aihub.org.cn/
```

## Follow-up revalidation

The route and tunnel were rechecked on September 25, 2026:

- Local upstream `http://127.0.0.1:3080/` returned HTTP `200` with
  `X-Upstream: local-3080-test`.
- The local Agent health endpoint returned `ready`; its log recorded an active
  reverse WebSocket to `106.54.197.139:42222` for
  `doops-local/local-3080`.
- Gateway route persistence was confirmed:
  `route_f24f1a5527b794815d1526f9`,
  `ds.aihub.org.cn`,
  `doops-local/local-3080`,
  `http://127.0.0.1:3080/`,
  `enabled=true`.
- From the online `gw-114-k8s` Agent, `getent hosts ds.aihub.org.cn` returned
  `106.54.197.139`; a no-proxy GET to
  `http://ds.aihub.org.cn/health?probe=external-114` returned HTTP `200`.
- From the same external Agent, a no-proxy GET to
  `http://ds.aihub.org.cn/demo/path?from=114` returned the local upstream body,
  preserved the query string, and returned `X-Upstream: local-3080-test`.
- The repository smoke test passed again:
  `gateway_health=ok`, `agent_ready=ready`, `proxy_tunnel_smoke=PASS`.
- `go test ./...` passed for both `backend/agent` and `backend/gateway`.

The workstation resolver is currently intercepted by Clash and returns a
`198.18.x.x` fake address. A direct workstation request can also receive a
DNSPod webblock response, while the independent 114 Agent reaches the Gateway
route successfully. Treat the independent Agent result as the authoritative
HTTP proxy evidence for this test.

HTTPS remains unverified. The Gateway host currently has only the
`*.l8ai.cn` certificate, whose subject does not cover `ds.aihub.org.cn`.
