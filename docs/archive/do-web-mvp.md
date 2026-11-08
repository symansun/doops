# DoWeb MVP Archive

The canonical runtime is now `backend/agent/skills/doweb/` in this repository.
Use the unified `doops web` command and the Console website panel. This archive
describes the original prototype only and is not an installation or
authentication entrypoint for the unified runtime.

DoWeb is the frontend deployment and Nginx operations product.

The first implementation focuses on deployment only. The customer builds the
frontend on their own computer or in CI, then DoWeb receives the customer-built
dist artifact and publishes it into an Nginx-backed Web container. DoWeb does
not run `npm install`, `npm run build`, or application process management in the
MVP.

Target scope:

- connect to an Nginx or Nginx-managed gateway target;
- publish a new static frontend website from `dist/`;
- configure server blocks, custom domains, reverse proxy routes, load balancing,
  TLS certificate paths and static artifact deployment;
- support managed Nginx edits through `config-set` and `snippet-set`;
- validate every change with `nginx -t`;
- inspect site availability, certificate status and access/error logs;
- rollback site configuration and deployments through doagent.

Historical status: standalone MVP script and knowledge base. The maintained
implementation is the Agent skill at `backend/agent/skills/doweb/`.

Resource-control design:

- `docs/archive/root-docs/superpowers/specs/2026-07-05-doweb-agent-nginx-resource-control-design.md`

```text
DoWeb/
├─ Server/   Future UI/API/runtime for website operations.
└─ Skill/    Nginx/site publishing skill, scripts, references and runbooks.
```

## Former prototype usage

The standalone commands that formerly targeted `DoSuite/DoWeb/Skill/` have
been retired. They are retained only as part of this historical description;
use the canonical `doops web` workflow and the Agent skill at
`backend/agent/skills/doweb/` for all current operations.

DoWeb converts natural-language publishing requests into a SiteSpec, renders a
candidate Nginx config, runs `nginx -t`, reloads Nginx, records state, and
returns evidence. There is no silent fallback: failed validation, reload or
health check stops the change and surfaces the root cause.

`install-nginx` supports username/password bootstrap without storing the
password in the repository. The default mode is Docker: DoWeb installs or starts
Docker when needed, runs the `doweb-nginx` container, and returns the deploy
flags needed for later `nginx -t` and reload calls.

`domain-set` maintains base-domain metadata. After `aihub.org.cn` is registered,
`deploy --domain-base aihub.org.cn --site-id demo` automatically publishes
`demo.aihub.org.cn` and rejects conflicts before changing Nginx.
