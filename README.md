# doops

`doops` is a gateway-first operations console and agent runtime. The public
repository layout is shared by GitHub and CNB so the same checkout can build the
web console, gateway, agent, and CLI without path rewrites.

## Repository Layout

```text
app/                         Next.js routes and API proxy endpoints
components/                  Console UI components
lib/                         Browser and API client helpers
backend/agent/               doops-agent, gateway hub, ACP bridge, scheduler
backend/gateway/             doops-gateway compatibility entrypoint
backend/skills/doops-cli/    doops CLI source and prebuilt CLI artifacts
backend/docs/                Runtime, deployment, and protocol documentation
Dockerfile.web               Production web console image
.cnb.yml                     CNB CI checks and immutable agent image publication
.github/workflows/ci.yml     GitHub CI checks
```

The current standard source layout keeps frontend code at the repository root
and all Go services under `backend/`. Older CNB branches may still have Go
sources at the repository root; new work should use the layout above.

## Development Checks

Run the same checks before publishing source:

```bash
(cd backend/agent && go test ./...)
(cd backend/gateway && go test ./...)
(cd backend/skills/doops-cli && go test ./...)
pnpm exec tsc --noEmit
pnpm build
```

## Publishing Model

- GitHub and CNB should receive the same commit SHA for the normalized source branch.
- GitHub uses `main` as the normalized public branch.
- CNB CI builds, verifies, and publishes immutable agent images. Environment
  rollout and rollback execute through the registered DoOps control plane.
- Do not publish local runtime directories such as `.next/`, `node_modules/`,
  `.pytest_cache/`, top-level `examples/`, or top-level `test/`.

## Runtime Boundary

Daily operations go through `doops-gateway` and registered `doops-agent`
instances. Do not rely on SSH credentials for normal agent operations; remote
actions should travel through doops RPC, gateway routing, and agent-side tools.

## Development Credential Preflight

Projects can commit a metadata-only `.agent/doops-credentials.yaml` so an Agent
checks required managed credential references before Git, registry, Helm, TLS,
or API development work:

```yaml
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-edu-coder
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
```

```bash
doops -session dev-preflight credential preflight \
  --manifest .agent/doops-credentials.yaml
```

The manifest accepts credential references and uses only. It never contains or
returns payloads, does not grant authority, and fails for missing references,
wrong uses, revoked grants, target conflicts, or ambiguous names.
