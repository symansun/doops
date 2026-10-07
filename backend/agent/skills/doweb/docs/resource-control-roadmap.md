# DoWeb Resource Control Roadmap

This roadmap turns the mature application delivery gateway reference into
incremental DoWeb work. It describes planned capabilities and must not be read
as current CLI behavior unless the feature is listed in current docs.

## Phase 1: Resource, TLS and Access Core

Target model:

- Resource registry with group, name, description, tags and enabled state.
- Publish model with domain, HTTP/HTTPS mode, IPv4/IPv6 mode and DNS metadata.
- Certificate registry with existing certs, uploads, self-signed certs and
  explicit rotation.
- Network group registry with reusable CIDR sets.
- Resource-level and path-level access policies.
- Proxy options for real IP, WebSocket, host behavior and timeout.

Validation:

- certificate and key match;
- certificate covers the domain unless explicitly development/self-signed;
- network groups exist before rendering access rules;
- path policies only affect intended locations;
- `nginx -t`, reload and HTTP/HTTPS checks are mandatory.

## Phase 2: Managed Load Balancing

Target model:

- upstream registry;
- endpoint enable/disable;
- round-robin, `ip_hash` and `least_conn`;
- Nginx OSS passive health settings such as `max_fails` and `fail_timeout`;
- DoWeb-side active probes for reporting.

Active health checks should not be represented as native Nginx OSS behavior
unless the runtime actually includes the required module or product edition.

## Phase 3: Server API and UI

The first Server UI should reuse the same ResourceSpec model as the Agent.

Screens should cover:

- resource list and status;
- resource edit;
- certificate management;
- network groups and access policies;
- path access control;
- upstream/load-balancing configuration;
- change evidence and rollback.

The UI must call DoWeb managed operations. It must not write live Nginx files
directly.

## Phase 4: DNS and Certificate Automation

Planned automation:

- provider DNS record changes when credentials are configured;
- ACME or provider-backed certificate issuance;
- renewal monitoring;
- certificate expiration alerts;
- split-horizon/internal/external publishing policies.

Provider automation should fail visibly when credentials, ownership validation,
DNS challenge or HTTP challenge cannot be proven.

## Natural-Language Examples

The Agent should normalize requests like:

- "发布这个 dist 到 demo.aihub.org.cn";
- "把 proxy-106.aihub.org.cn 反代到 http://106.54.197.139/";
- "只允许内网访问 /admin";
- "给这个站点换成 cert-prod-aihub";
- "两台后端做 least_conn 负载均衡";
- "启用 WebSocket 和真实 IP 透传";
- "检查这个变更，失败就回滚".

For missing production decisions, ask one focused question instead of inventing
domains, certificate paths, CIDRs or upstream URLs.
