# DoWeb Troubleshooting

Use this guide to isolate DNS, Nginx, upstream and browser problems without
guessing.

## Quick Triage

1. Check the hostname through the intended gateway:

   ```bash
   curl --resolve <domain>:80:<gateway-ip> http://<domain>/
   ```

2. Check normal public access:

   ```bash
   curl -v http://<domain>/
   ```

3. Check DNS answers:

   ```bash
   curl -sS 'https://dns.google/resolve?name=<domain>&type=A'
   ```

4. Check Nginx on the gateway:

   ```bash
   nginx -t
   grep -R "<domain>" -n /etc/nginx /etc/doweb/nginx
   ```

5. Check the upstream if the site is a reverse proxy:

   ```bash
   curl -v <upstream-url>
   ```

## `ERR_EMPTY_RESPONSE`

Likely causes:

- DNS or proxy selected an IP that does not serve the hostname.
- A wildcard or multi-A record returned an internal address that is not ready.
- The request reached a TCP listener that closes without HTTP response.

Checks:

```bash
curl -v http://<domain>/
curl --resolve <domain>:80:<expected-ip> http://<domain>/
curl -sS 'https://dns.google/resolve?name=<domain>&type=A'
```

If forced gateway access succeeds and normal access fails, the problem is outside
the DoWeb Nginx server block.

## Default Nginx Page After Deleting a Site

If a site is disabled but DNS still points to the gateway, Nginx may return the
default server page:

```text
Welcome to nginx on Anolis OS
```

This means the DoWeb server block no longer matches, but the gateway still has a
default `listen 80 default_server`. It is expected after disabling a site. If a
hard 404 is required, configure a managed default server or remove the DNS
record.

## HTTPS Shows Kubernetes Fake Certificate

Symptoms:

- `curl -vk https://<domain>/` shows `Kubernetes Ingress Controller Fake
  Certificate`.
- HTTPS returns 404 while HTTP works.

Cause:

The gateway's port `443` is owned by another Nginx HTTP or stream configuration.
The current DoWeb server block may only listen on `80`.

Checks:

```bash
ss -ltnp | grep ':443'
grep -R "listen 443\\|ssl_certificate" -n /etc/nginx /etc/doweb/nginx
```

Fix:

- Provide valid `--tls-cert` and `--tls-key` for this DoWeb site, and ensure no
  existing stream server steals port 443 for the hostname.
- Or keep the site HTTP-only and do not test HTTPS.

## Domain Conflict

Error:

```text
domain conflict: <domain> is already used by <site_id>
```

Cause:

Another DoWeb state file under `/var/lib/doweb/state/` already owns the domain.

Fix:

- Use the existing site id to update the site; or
- disable/delete the old site state through the lifecycle runbook; or
- choose another domain.

## `nginx -t` Fails

Do not reload. Read the Nginx error output and inspect the candidate config.

Common causes:

- duplicate `location /`;
- invalid snippet directive;
- missing certificate file;
- upstream syntax error;
- unmanaged Nginx config on the same host is already broken.

DoWeb deploy, `config-set`, `snippet-set` and rollback restore prior activation
when validation fails.

## Health Check Fails

If `nginx -t` and reload pass but `--health-url` fails:

- verify DNS for the health URL;
- try `curl --resolve` against the intended gateway;
- check upstream availability;
- check whether HTTPS or HSTS moved the browser to port 443;
- inspect Nginx access/error logs.

Do not mark the release healthy until the health URL proves the intended route.

## Reverse Proxy Returns the Wrong Site

Checks:

```bash
node /app/skills/doweb/scripts/doweb.mjs config-get --site-id <site_id>
curl --resolve <domain>:80:<gateway-ip> http://<domain>/
curl -v <upstream-url>
```

Make sure the rendered `proxy_pass` target is correct and that the upstream
itself responds with the expected application.

## doops Target Offline

If `doops -session ... exec --target gw-114 ...` reports target offline, do not
assume Nginx is down. It means the doops-agent target is not reachable through
the gateway.

For one-off host recovery or first install, SSH may be used if credentials are
available. Normal DoWeb operations should return to doops or CI/GitOps once the
agent is healthy.
