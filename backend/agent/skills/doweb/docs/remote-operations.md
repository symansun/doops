# DoWeb Remote Operations

This page documents how DoWeb runs on a selected DoOps target. The Agent is the
remote execution boundary; the user does not pass SSH credentials to DoWeb.

## Preferred Access Path

Use doops when the target agent is online:

```bash
doops list
doops -session doweb_check exec --target gw-114-k8s --cmd "hostname"
```

Every doops remote call must include `-session <name>`.

SSH is reserved for first installation, credential bootstrap, or Agent recovery
outside the normal DoWeb workflow. Normal publish, check and rollback use the
Gateway target and the target-local skill runtime.

## Docker Mode

`install-nginx` requires an explicit Host or Docker mode. The workbench and
`doops web --runtime` use Host unless Docker is selected; they never infer the
instance from a failed Nginx command.

It creates:

```text
/var/lib/doweb/
/etc/doweb/nginx/sites-available/
/etc/doweb/nginx/conf.d/
/etc/doweb/nginx/snippets/
/usr/local/bin/doweb-nginx
```

It starts a container similar to:

```text
doweb-nginx -> nginx:1.27-alpine
host 80 -> container 80
host 443 -> container 443
```

Use the returned deploy flags:

```bash
--nginx-bin /usr/local/bin/doweb-nginx
--reload-cmd 'docker exec doweb-nginx nginx -s reload'
```

## Host Mode

Use host mode when an existing host Nginx already owns ports 80/443:

```bash
node backend/agent/skills/doweb/scripts/doweb.mjs install-nginx \
  --local true \
  --mode host
```

Host mode prepares DoWeb directories and uses:

```text
/etc/doweb/nginx/sites-available/
/etc/nginx/conf.d/
/etc/doweb/nginx/snippets/
nginx
nginx -s reload
```

Before deploying to a host-mode target, inspect existing 80/443 listeners:

```bash
ss -ltnp | grep -E ':(80|443) '
grep -R "listen 443\\|stream" -n /etc/nginx
```

Do not assume DoWeb can safely own HTTPS if another stream server already owns
port 443.

## Remote Evidence Checklist

For deploy or config changes, capture:

```bash
nginx -t
curl --resolve <domain>:80:<gateway-ip> http://<domain>/
curl -vk --resolve <domain>:443:<gateway-ip> https://<domain>/  # only for HTTPS sites
```

For reverse proxy:

```bash
curl -v <upstream-url>
curl --resolve <domain>:80:<gateway-ip> http://<domain>/
```

For DNS separation:

```bash
curl -sS 'https://dns.google/resolve?name=<domain>&type=A'
```

## Secret Handling

The bundled skill accepts no SSH/password inputs. Use the existing DoOps
managed credential API for credential provisioning; a site refers to already
mounted TLS certificate/key paths. First Agent bootstrap is an external process.

Do not write passwords into:

- SiteSpec;
- state JSON;
- Markdown docs;
- command history in shared terminals;
- Git commits.

## Recovery Notes

If a managed config needs to be disabled manually, use the reversible disable
procedure in `site-lifecycle.md`. Keep timestamped backups and run `nginx -t`
before reload.

If the doops target is offline but the host is reachable, fix or restart the
agent separately. DoWeb deployment state should not be inferred from agent
online/offline status alone.
