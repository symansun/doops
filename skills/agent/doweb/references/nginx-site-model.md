# DoWeb Nginx Site Model

DoWeb manages frontend deployments as a SiteSpec. A SiteSpec is the declarative
object that doagent should produce from natural language before changing Nginx.

```yaml
site_id: demo
domain: demo.example.com
artifact: /path/to/customer-built/dist
static_root: /var/lib/doweb/sites/demo/current
spa_fallback: true
proxies:
  - path: /api/
    target: https://api.example.com
upstreams:
  - name: app
    endpoints:
      - http://127.0.0.1:3001
      - http://127.0.0.1:3002
tls:
  cert: /etc/letsencrypt/live/demo.example.com/fullchain.pem
  key: /etc/letsencrypt/live/demo.example.com/privkey.pem
```

## Runtime Layout

```text
/var/lib/doweb/
├─ sites/<site_id>/
│  ├─ releases/<release_id>/
│  └─ current -> releases/<release_id>
└─ state/<site_id>.json

/etc/nginx/sites-available/doweb-<site_id>.conf
/etc/nginx/sites-enabled/doweb-<site_id>.conf -> ../sites-available/doweb-<site_id>.conf
/etc/nginx/doweb-snippets/<site_id>/*.conf
```

Docker bootstrap uses a host-mounted equivalent by default:

```text
/etc/doweb/nginx/sites-available/
/etc/doweb/nginx/conf.d/        # mounted into the Docker Nginx container
/etc/doweb/nginx/snippets/
/usr/local/bin/doweb-nginx      # wrapper for docker exec doweb-nginx nginx
```

`deploy` copies a customer-built dist directory into a new immutable release.
The active version is the `current` symlink. The Nginx server block points to
that symlink so rollback only has to restore the prior symlink and config.

## Nginx Features

- Static frontend hosting with `index.html`.
- SPA fallback through `try_files $uri $uri/ /index.html`.
- reverse proxy locations, for example `/api/` to `https://api.example.com`.
- Named upstream groups for load balancing, rendered as `upstream <name>`.
- Optional TLS certificate paths when the operator supplies them.
- Direct managed config editing through `config-set`.
- Site-level server-block snippets through `snippet-set`.
- Target-local Nginx bootstrap with an explicit Host or Docker runtime.

DoWeb should prefer SiteSpec rendering for common changes. `config-set` exists
for explicit Nginx config editing, but it must still go through validation,
reload and rollback rules.

## Domain Registry

DoWeb stores base domains under:

```text
/var/lib/doweb/domains/<domain>.json
```

Use `domain-set` before automatic publishing:

```bash
node /app/skills/doweb/scripts/doweb.mjs domain-set \
  --domain example.com \
  --host-ip <agent-host-ip> \
  --provider aliyun
```

When a user asks to publish a site without giving a full hostname, doagent should
pick a stable site id and call deploy with `--domain-base example.com`. DoWeb
then uses `<site_id>.example.com`, records the assigned domain in site state,
and rejects conflicts if another site already uses that hostname.
