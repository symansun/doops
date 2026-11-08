# DoWeb Site Lifecycle

This runbook covers the current CLI-supported site lifecycle. It also documents
the manual disable flow used until DoWeb has a first-class `delete` command.

## 1. Prepare the Target

Use the selected DoOps Gateway target. Inspect the existing Nginx instance
before choosing Host or Docker. Installation must use an explicit mode; normal
website operations use the same runtime selection thereafter.

```bash
node /app/skills/doweb/scripts/doweb.mjs install-nginx --local true --mode host
# Or use --mode docker on a Docker target.
```

The bundled runtime rejects SSH hosts, usernames, passwords and alternate
logins. First Agent installation and recovery remain external DoOps procedures.

## 2. Register the Base Domain

```bash
node /app/skills/doweb/scripts/doweb.mjs domain-set \
  --domain aihub.org.cn \
  --host-ip 114.55.61.32 \
  --provider aliyun
```

This records metadata for DoWeb conflict checks and automatic subdomain
assignment. It does not change public DNS records.

## 3. Publish a Static Site

```bash
node /app/skills/doweb/scripts/doweb.mjs deploy \
  --site-id vibe-demo \
  --domain-base aihub.org.cn \
  --dist dist \
  --health-url http://vibe-demo.aihub.org.cn/
```

Validation:

```bash
curl --resolve vibe-demo.aihub.org.cn:80:114.55.61.32 \
  http://vibe-demo.aihub.org.cn/
```

Use `--resolve` when public DNS has not propagated or when wildcard/multi-A DNS
may route the request to another gateway.

## 4. Publish a Reverse Proxy

```bash
node /app/skills/doweb/scripts/doweb.mjs deploy \
  --site-id proxy-106 \
  --domain proxy-106.aihub.org.cn \
  --dist dist \
  --proxy /=http://192.0.2.10/
```

When `--proxy /=...` is used, DoWeb renders a root proxy location and does not
also render the static SPA `location /`. This avoids duplicate root locations.

## 5. Update an Existing Site

Run `deploy` again with the same `--site-id`. DoWeb creates a new immutable
release, moves the `current` symlink, renders config, validates and reloads.

State keeps the latest releases under:

```text
/var/lib/doweb/state/<site_id>.json
```

## 6. Check a Site

```bash
node /app/skills/doweb/scripts/doweb.mjs check \
  --site-id vibe-demo \
  --health-url http://vibe-demo.aihub.org.cn/
```

This runs `nginx -t` and optional HTTP checks.

## 7. Roll Back

```bash
node /app/skills/doweb/scripts/doweb.mjs rollback \
  --site-id vibe-demo
```

Rollback restores the previous release and config, then validates and reloads.
If no previous release exists, DoWeb reports that condition.

## 8. Disable a Site

The current CLI does not have `delete` or `disable`. Use this manual, reversible
host-mode procedure for tests:

```bash
ts=$(date +%Y%m%d%H%M%S)
mkdir -p /etc/doweb/nginx/deleted /var/lib/doweb/deleted-state

cp -a /etc/doweb/nginx/sites-available/doweb-<site_id>.conf \
  /etc/doweb/nginx/deleted/doweb-<site_id>.conf.$ts
cp -a /var/lib/doweb/state/<site_id>.json \
  /var/lib/doweb/deleted-state/<site_id>.json.$ts

mv /etc/nginx/conf.d/doweb-<site_id>.conf \
  /etc/doweb/nginx/deleted/doweb-<site_id>.enabled-symlink.$ts
mv /etc/doweb/nginx/sites-available/doweb-<site_id>.conf \
  /etc/doweb/nginx/deleted/doweb-<site_id>.available.$ts
mv /var/lib/doweb/state/<site_id>.json \
  /var/lib/doweb/deleted-state/<site_id>.json.disabled.$ts

nginx -t
nginx -s reload
```

Expected result: requests for the removed host no longer hit the DoWeb server
block. They may still return an Nginx default page if the hostname points to the
same gateway.

## 9. Restore a Disabled Site

Using the same timestamp:

```bash
mv /etc/doweb/nginx/deleted/doweb-<site_id>.available.<ts> \
  /etc/doweb/nginx/sites-available/doweb-<site_id>.conf
ln -s /etc/doweb/nginx/sites-available/doweb-<site_id>.conf \
  /etc/nginx/conf.d/doweb-<site_id>.conf
mv /var/lib/doweb/deleted-state/<site_id>.json.disabled.<ts> \
  /var/lib/doweb/state/<site_id>.json

nginx -t
nginx -s reload
```

Verify with a forced Host/IP check:

```bash
curl --resolve <domain>:80:<gateway-ip> http://<domain>/
```

## 10. Evidence to Record

For every production change, keep:

- command and flags, with secrets redacted;
- candidate config or config diff;
- `nginx -t` output;
- reload result;
- health-check output;
- state file path;
- rollback or restore path.
