# DoWeb Change Safety

Every DoWeb operation must produce evidence. The minimum evidence is the
candidate Nginx config, `nginx -t` output, reload result, and the final site or
health-check status.

## Standard Change Flow

1. Normalize the natural-language request into a SiteSpec.
2. Copy the customer-built dist artifact into a new release directory when this
   is a deployment.
3. Render or receive the candidate Nginx config.
4. Write the candidate to the managed site config path.
5. Activate the site config symlink.
6. Run `nginx -t`.
7. Reload Nginx only after validation passes.
8. Run HTTP/HTTPS health checks when a URL is provided.
9. Persist release state.

## Domain Assignment

`domain-set` records trusted base-domain metadata, for example
`aihub.org.cn -> 114.55.61.32`. A deploy may use either an explicit `--domain`
or a registered `--domain-base`. With `--domain-base`, DoWeb derives
`<site_id>.<base_domain>`, checks existing site state for conflicts, and fails
before writing release files if the domain is already owned by another site.

## Remote Installation

`install-nginx` executes only on the selected target, with an explicit Host or
Docker mode. The bundled executor rejects SSH/password options and never creates
a second login. Docker mode installs Docker when explicitly requested,
starts the `doweb-nginx` container, mounts DoWeb config and release directories,
then validates the container with `/usr/local/bin/doweb-nginx -t`.

After Docker bootstrap, deployment should use the deploy flags returned by
`install-nginx`, especially:

```bash
--nginx-bin /usr/local/bin/doweb-nginx
--reload-cmd 'docker exec doweb-nginx nginx -s reload'
```

## rollback

Rollback restores the previous release symlink and the previous rendered config,
then runs `nginx -t` and reload again. If there is no previous release, DoWeb
must report that condition instead of pretending rollback succeeded.

## Direct Config Editing

`config-set` and `snippet-set` are the only supported direct Nginx editing
methods in the MVP.

- `config-set` replaces the managed site config with a complete server block.
- `snippet-set` writes a named snippet included inside that site's server block.
- Both commands must run `nginx -t` and reload before reporting success.
- If validation or reload fails, the previous file is restored.

## Failure Policy

DoWeb has no silent fallback. A failed validation, reload, copy, health check or
rollback is a real failure. The operator should see the command, path and error
message needed to fix the root cause. Do not skip checks, silently disable a
route, rewrite the user's domain, or deploy to a different site id.

After restoring a failed activation, run `nginx -t` and reload the restored
configuration. Restoring files alone is not recovery of the serving process.

## Natural-Language Guardrails

When a user asks for reverse proxy or load balancing changes, doagent should ask
for the missing domain, path, upstream URLs or release artifact path if they are
not already present. It should not invent production domains, certificate paths,
or API endpoints.
