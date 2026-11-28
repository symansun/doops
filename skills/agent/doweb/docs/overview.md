# DoWeb Overview

DoWeb is the DoSuite product for publishing customer-built frontend sites and
Nginx-backed web resources. Its first implementation is a skill-side CLI:

```text
Natural-language request
  -> doagent
  -> DoWeb Skill
  -> SiteSpec
  -> Nginx config
  -> nginx -t
  -> reload
  -> health evidence
```

DoWeb is not a build system. Users build source code on their own computer or in
CI, then hand DoWeb a finished `dist/` directory. DoWeb publishes that artifact,
configures Nginx, validates the change, and records state for rollback.

## Current Implementation

The current entrypoint is:

```bash
node /app/skills/doweb/scripts/doweb.mjs <command> [flags]
```

It supports:

- installing or preparing Nginx on a target host;
- registering base-domain metadata;
- rendering a candidate Nginx server block;
- deploying static frontend artifacts;
- reverse proxying paths or the root path;
- rendering named upstreams for load balancing;
- configuring TLS by explicit certificate/key paths;
- reading or replacing managed Nginx config;
- writing site-level snippets;
- checking Nginx and health URLs;
- rolling back to the previous release.

## What DoWeb Owns

DoWeb owns files under the configured runtime paths:

```text
/var/lib/doweb/
  domains/
  sites/<site_id>/releases/<release_id>/
  sites/<site_id>/current
  state/<site_id>.json

/etc/doweb/nginx/sites-available/
/etc/doweb/nginx/snippets/
```

The enabled Nginx path depends on install mode:

- Docker mode: `/etc/doweb/nginx/conf.d/`, mounted into the `doweb-nginx`
  container.
- Host mode: `/etc/nginx/conf.d/`, for hosts that already run Nginx on 80/443.

DoWeb should not edit unrelated Nginx files. If a site must be changed directly,
use `config-set` or `snippet-set` so validation and reload are still enforced.

## Product Direction

The resource-control design extends SiteSpec into ResourceSpec. That future
model covers:

- resource metadata and publish status;
- HTTP/HTTPS and IPv4/IPv6 publish settings;
- certificate registry and rotation;
- internal/external network groups;
- resource-level and path-level access policies;
- reverse proxy and upstream load balancing;
- advanced proxy options such as real IP, WebSocket, timeout and SNI;
- evidence, rollback and natural-language guardrails.

The current CLI is the safe deploy core. The resource-control model is the
planned control-plane layer above it.

## Safety Policy

DoWeb has no silent fallback. A failed validation, reload, health check, copy or
rollback must surface the real error. DoWeb must not hide a broken change by
switching ports, changing domains, disabling TLS, skipping checks or deploying
to another site id.
