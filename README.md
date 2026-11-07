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
backend/cli/    doops CLI source and prebuilt CLI artifacts
backend/docs/                Runtime, deployment, and protocol documentation
Dockerfile.web               Production web console image
.cnb.yml                     CNB CI checks and immutable agent image publication
.github/workflows/ci.yml     GitHub CI checks
```

The current standard source layout keeps frontend code at the repository root
and all Go services under `backend/`. Older CNB branches may still have Go
sources at the repository root; new work should use the layout above.

The AI Assistant embeds Agent Cloud's `@agent-cloud/agent-ui` React workspace
through a DoOps Gateway runtime adapter. Diagnosis, deployment forms,
target/session isolation, and acceptance scenarios are documented in
[Agent UI Integration](docs/agent-ui-integration.md).

The same Console and CLI host DoSql and DoWeb skills. `doops sql` and
`doops web` reuse the existing Gateway login, target routes and audit. The
website panel operates through structured read/change tools and returns Nginx
validation, health and rollback evidence. Target prerequisites and functional
acceptance are documented in [DoWeb Integration](backend/docs/DOWEB_INTEGRATION.md).

DoSql natural-language requests use `query`, `plan`, or `discover`. SQL changes use
`apply` with `--sql` or `--file`, within the user's existing authorization:

```bash
doops -session db-check sql --target <t> --mode query --request 'Count rows in orders'
doops -session db-plan sql --target <t> --mode plan --request 'Plan an external_id column for orders'
doops -session db-discover sql --target <t> --mode discover --request 'List database workloads'
doops -session db-change sql --target <t> --mode apply --file change.sql --verify 'SELECT external_id FROM orders WHERE 1 = 0'
```

Database credentials bind automatically from the target; direct SQL can select one
with `--credential`. Dry-run requires a rollback-capable batch. For engine limits,
DDL handling and acceptance checks, see the [SQL usage guide](backend/docs/DOOPS_USAGE_GUIDE.md#dosql-直接执行-sql).

## Development Checks

Run the same checks before publishing source:

```bash
(cd backend/agent && go test ./...)
(cd backend/gateway && go test ./...)
(cd backend/cli && go test ./...)
pnpm exec tsc --noEmit
pnpm build
```

## Publishing Model

Agent deployment entry points, prerequisites and commands are maintained in
[Cluster Install Guide](backend/docs/CLUSTER_INSTALL_GUIDE.md).
`scripts/install.sh` installs the CLI and Skill only; it does not upgrade an Agent.
Empty Ubuntu/Debian hosts use `backend/scripts/bootstrap-host-agent.sh` to install
Docker and start the Agent from a pinned release with private model settings.

- GitHub and CNB should receive the same commit SHA for the normalized source branch.
- GitHub uses `main` as the normalized public branch.
- CNB CI builds, verifies, and publishes immutable agent images. Business workload
  rollout and rollback execute through the registered DoOps control plane.
- Managed Helm Agents keep the `doops upgrade` in-cluster Job path. Existing
  standalone Docker Agents use the explicit external DockerHost controller
  `backend/scripts/release-host-agent.sh`, not an automatic fallback or a required
  Kubernetes migration. It requires an explicit SSH port, immutable digest, CI source
  commit and release, performs real read-only preflight, preserves container configuration
  and the stopped old container, and rolls back externally on failure. It never accepts
  passwords/tokens; host keys must be verified. Procedure and functional acceptance:
  [Build And Deploy SOP](backend/docs/BUILD_DEPLOY_SOP.md#独立-dockerhost-发布).
- The Gateway owns one execution lifecycle state machine for foreground, background,
  SSH, prompt, and `ask --apply` operations. It observes execution cleanup before releasing
  the resource lease; cancellation acknowledgements are not terminal evidence, and
  missing terminal evidence records `outcome_unknown` without blocking new tasks
  through historical audits or replaying the uncertain mutation. Active operations
  retain session/resource ownership until execution ends. Audit persistence failures
  are reported and retried without retaining execution locks. Background execution checks
  `executionLifecycle`; structured SSH checks `structuredSSH`. See
  `backend/docs/DOOPS_USAGE_GUIDE.md` for operation states and concurrency.
- Do not publish local runtime directories such as `.next/`, `node_modules/`,
  `.pytest_cache/`, top-level `examples/`, or top-level `test/`.

## Runtime Boundary

Target `ask`/`exec` authority includes using credentials already bound to that
target. Operators do not need a second credential permission or individual grant.
Credential management remains restricted to the owner or platform administrator.
DoSql resolves database bindings directly from stored metadata instead of asking
the model to generate credential authorization scopes.

Daily operations go through `doops-gateway` and registered `doops-agent`
instances. Do not rely on SSH credentials for normal agent operations; remote
actions should travel through doops RPC, gateway routing, and agent-side tools.
An Agent must not replace itself through its own exec or Helm process. Authorized
external DockerHost publication controls the host independently of the old Agent;
it does not introduce direct CLI targets or change credential APIs.

## Development Credential Preflight

For first-time managed credential setup, `doops -session <s> credential configure`
reuses the existing resource/version/grant lifecycle in one invocation. It accepts
stdin payloads or a target-exec-authorized `--from-secret <name> --keys <keys>`
reference on the selected target. Existing resources are reused by ID or unique
name and scope. It configures the target binding, verifies active materializations,
and promotes only on success. Operators can use bound credentials with the target grant alone; no extra per-user grant or model-generated scope is needed. Secret values never enter CLI output.
See [Credential Resources](backend/docs/CREDENTIAL_RESOURCES.md) for flags and
failure semantics; database connectivity is a separate DoSql acceptance step.

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

## 开源协作

- 贡献流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。
- 安全漏洞请按 [SECURITY.md](SECURITY.md) 的私密渠道报告。
- 分支、发布、依赖和公开内容边界见 [docs/OPEN_SOURCE_MAINTENANCE.md](docs/OPEN_SOURCE_MAINTENANCE.md)。
- CNB 分支一次性审计见 [docs/reports/cnb-branch-audit-2026-11-07.md](docs/reports/cnb-branch-audit-2026-11-07.md)。

## License

本项目以 [MIT License](LICENSE) 开源。
