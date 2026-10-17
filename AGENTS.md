---
name: doops-development-contract
scope: repository
owner: doops-maintainers
---

# DoOps Development Contract

This file records repository-specific rules that are easy to miss during DoOps development.
General engineering rules belong in the caller's global `AGENTS.md`; keep this file focused on
DoOps architecture, security, release, and verification boundaries.

## Repository Map

| Component | Source | Notes |
|---|---|---|
| Gateway + Agent runtime | `backend/agent/internal/server/` | One shared package; see its `README.md` for the file-prefix index |
| Gateway entrypoint | `backend/gateway/main.go` | 11-line wrapper that calls `backend/agent/pkg/gatewaycmd` |
| Agent entrypoint | `backend/agent/cmd/agent/` | Imports `internal/server` and `internal/dispatcher` |
| CLI | `backend/cli/cli/doops/` | Separate Go module named `doops` |
| Agent-side skills | `backend/agent/skills/` | Copied into the Agent image; includes `dosql/` and `doweb/` |
| Operator Skill | `backend/skills/SKILL.md` + `backend/docs/*.md` | Installed into projects by `backend/scripts/install.sh` |
| Helm Charts | `backend/deploy/helm/` | Runtime assets only |
| Release scripts | `backend/scripts/` | Python contract tests live in `backend/test/` |
| Web console | `app/`, `components/`, `lib/` (Next.js) | Built from `Dockerfile.web` |

`bin/`, `backend/bin/`, `DoSuite/`, `DoSecurity/`, `.next/`, and `node_modules/` are ignored local
artifacts. Do not read, search, or execute them; they include stale binaries.

## Topic Ownership

Each topic has one authoritative document. Expand a rule only there; elsewhere, link to it.

| Topic | Authoritative document |
|---|---|
| Workload delivery (`ask --apply`, Helm assets) | `backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md` |
| Credentials | `backend/docs/CREDENTIAL_RESOURCES.md` |
| Agent and Gateway release | `backend/docs/BUILD_DEPLOY_SOP.md` |
| Gateway protocol and lifecycle | `backend/docs/DOOPS_GATEWAY_TUNNEL.md` |
| External API | `backend/docs/DOOPS_API_GUIDE.md` |
| Installation and onboarding | `backend/docs/CLUSTER_INSTALL_GUIDE.md` |
| Day-to-day commands | `backend/docs/DOOPS_USAGE_GUIDE.md` |

## Canonical Architecture

- Ordinary workload delivery uses an explicit natural-language task:
  `doops -session <s> ask --target <t> --apply [--credential <name>...] --msg '<facts>'`.
- The task must state target, workload/service scope, reason, authorized Helm assets, and
  machine-checkable acceptance criteria. Business image versions must come from the task or
  read-only evidence from its explicitly authorized repository, Chart, values, or existing release;
  never guess them or require facts already in those assets to be pasted into the prompt.
  Do not impose business-specific image keys, tag formats, CORS, repository, or directory gates.
- Ordinary `upgrade-install` uses `--reset-values` with explicit Chart defaults, ordered optional
  values overlays, and non-sensitive Chart-specific string overrides, never implicit historical
  values inheritance. Keep managed Helm authentication, target authority, staged package identity,
  session path isolation, and dry-run/final Deployment verification intact. Full asset, optional
  `expected_image`, and migration contracts belong in
  [NATURAL_LANGUAGE_DEPLOYMENT.md](backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md).
- CNB CI tests, builds, scans, and publishes immutable artifacts. Gateway authenticates, audits,
  limits concurrency, locks resources, and authorizes credentials. The selected target Agent
  performs the actual Kubernetes or Helm mutation and returns live evidence.
- For image/version inputs, the publisher must establish
  the exact CI source-commit and registry-digest provenance before invoking the command. The CLI
  and Agent validate the allowed input form and live result; they do not verify CI origin.
- Do not add or restore `EnvironmentRegistry`, `EnvironmentDefinition`, `DeploymentTemplate`,
  `deploy/environments.yaml`, `doops cicd run`, prompt-derived deployment scope, `CICDReport`, or
  `doops_cicd_result` as a deployment root or compatibility path.
- `deploy/helm/<name>/` contains runtime assets only. It must not become an environment registry,
  credential store, target selector, or deployment authorization source.
- A doops-agent cannot replace the workload from its own process. `doops upgrade` is available
  only when the Agent is in its own namespace, runs as the standard Helm-managed Deployment, and
  has an existing identity-matched managed Helm release. Without `--release` exactly one matching
  release is required; with `--release` the named release is selected and validated. The Agent
  Pod must expose a shared `/root/.kube` or `/root/.kube/config` volume. The in-cluster Job
  explicitly reuses that mounted kubeconfig, so its Kubernetes permissions come from that
  kubeconfig rather than from a local operator file. The CLI waits for the Job, verifies Gateway
  handoff and `structuredSSH`, and starts a rollback Job when the probe fails.
  `--dry-run` is preview-only and accepts either `repository:tag` or immutable
  `repository@sha256:<64 hex>` image references. Tag upgrades use runtime imageID verification;
  rollback still requires an evidenced immutable image.
  A new Kubernetes cluster without an Agent and legacy Helm Agents without the controller use
  `backend/scripts/release-managed-agent.sh` with an operator kubeconfig. Its new-cluster
  `--values` mode is bootstrap-only: it performs Helm wait and final Deployment checks, not
  Gateway takeover or automatic rollback; installation acceptance must follow. Its existing
  release mode updates an existing Helm release, stops when no Helm release exists, and does
  not perform adoption. `doops upgrade` only changes the Agent image or rolls back a managed
  release; `proxy.allowedHosts` and `hostAliases` remain explicit external-script configuration.
  Do not create a temporary Agent, target, or release-runner, and do not use `kubectl set image`
  or an Agent-process Helm mutation.
- Existing standalone Docker Agents use the external controller
  `backend/scripts/release-host-agent.sh`, never `doops upgrade`, automatic fallback, or a forced
  Kubernetes migration. Every parameter is explicit (port never defaults to 22), SSH verifies host
  keys, host passwords are external-bootstrap stdin only and never reach the Agent, and the current
  Agent never execs its own replacement or deletes the old container. The full dry-run, lock,
  verification, and rollback contract lives only in
  [BUILD_DEPLOY_SOP.md](backend/docs/BUILD_DEPLOY_SOP.md#独立-dockerhost-发布).

## Credential Security

- Public CLI arguments, natural-language prompts, doagent context, Git, issues, logs, audit output,
  and remote workspaces must never contain credential payloads.
- CLI payload input is stdin-only. Gateway encrypts payloads at rest and must fail closed when its
  configured master key or credential audit persistence is unavailable.
- `credential configure` orchestrates the existing lifecycle; do not create a second store or
  bypass grant/verify/promote. Platform-admin Secret import accepts exact references only and
  transfers selected values from Agent to Gateway on the private control channel, never via exec.
- Gateway may send decrypted payload only through the internal credential materialization call to
  the explicitly authorized target Agent. Public prompt and metadata bindings contain names,
  scopes, Secret names/types/key sets, resource versions, digests, status, and stable error
  categories only.
- Runtime credential use inherits the current target operation's `ask`/`exec` authority. Do not
  require a second `credential:use` action or a per-operator credential grant. Active grants identify
  which target/use owns a credential; management still belongs to its owner or platform admin.
  Database bindings resolve directly from stored target metadata, without a model-generated scope;
  the saved namespace is used for materialization. DoSql/Agent owns database assets and SQL policy.
  Namespace, workload, release, image and repository paths are execution metadata, not credential
  permission dimensions. Resolve explicit names directly from stored target bindings, without a
  model scope-planning request. Credential management uses owner/admin; personal credential APIs
  do not require separate credential actions. Target Secret import requires target exec authority.
  Keep HTTPS origin protection, package validation and immutable staging for Helm. Do not add
  wildcard, prefix, inferred, project, environment, template, or workflow-path grants to the
  Gateway layer.
- A project may declare metadata-only development requirements in
  `.agent/doops-credentials.yaml`. Before Git, registry, Helm, TLS, or API development work that
  depends on managed credentials, run
  `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml`.
  The manifest is only a fail-closed checklist of `credentialRef` plus `use`; it must never contain
  payloads, grant authority, materialization instructions, local secret paths, or fallback values.
  Credential references must be lowercase resource names, not URLs, paths, tokens, or arbitrary
  scalars. It only checks that the named credential is active and bound to the selected target.
- The lifecycle is `create -> put staged version -> grant -> verify every active grant -> promote`.
  Rotation repeats put/verify/promote. Grant or resource revocation must clean materializations
  before reaching the terminal revoked state.
- Promotion must require at least one current active grant and a successful verification for every
  current active grant. Historical or revoked grants and their verifications must not satisfy or
  block promotion.
- User-facing output and tests must use synthetic canaries and assert that payload values are never
  returned. Never request or store a real credential in fixtures, documentation, issues, or commits.

## CLI And Skill Distribution

- The only supported installed CLI path is `~/.local/bin/doops`.
- Build-tree binaries such as `backend/cli/bin/*` and binaries embedded inside a Skill
  directory are development artifacts, not supported user entrypoints.
- Users install a checksummed, versioned CNB Release Asset through
  `backend/scripts/install.sh --version <tag> --project <absolute-project-path>`.
- Before replacement, the installer must validate an existing config with the downloaded release
  CLI's own Go parser. Do not maintain a second Python, Node, jq, or shell JSON schema.
- The canonical Skill source is `backend/skills/SKILL.md`. The installer copies it and
  `backend/docs/*.md` from the exact commit embedded in the downloaded CLI into
  `<project>/.agent/skills/doops/`, never from the current working tree. Missing release source
  must fail before replacement; the install receipt records `skill_commit`. Do not edit
  installed copies first.
- One `doops login` is shared by configured targets on the same normalized Gateway URL. Target
  credential listing and use inherit target operation authority; management enforces ownership.

## Required Documentation

A behavior change updates, in the same change, the one authoritative document that owns its topic
(see Topic Ownership), plus CLI `--help` text and README when user-visible commands change.
`backend/skills/SKILL.md` keeps only red lines, command selection, and links; update it only when a
red line or a command entry changes. Other documents link to the owner instead of restating it.

Do not document intended behavior that the current CLI and Gateway do not implement.

## Verification And Delivery

Pick the highest tier the change touches:

| Tier | Applies to | Done when |
|---|---|---|
| T0 | Docs, tests, or refactors with no behavior change | Relevant local tests pass, `git diff --check` is clean, the branch is pushed, and required CI passes |
| T1 | CLI, Agent, Gateway, or Web behavior | T0, plus the checks below for the touched components, published release assets, `backend/scripts/install.sh` installing the released CLI and Skill, and touched components deployed from the matching immutable CI artifact |
| T2 | Credentials, CI/CD, permissions, payload boundaries, release contracts | T1, plus the full Given/When/Then matrix and the online synthetic credential lifecycle below |

Skill and docs changes reach user projects with the next release; a T0 change does not need its own
release.

- Credential and CI/CD changes require Given/When/Then coverage for success, denied authority,
  unavailable/wrong target, unavailable audit/key, verification failure, revocation, and payload
  redaction. Do not weaken expected HTTP status or output assertions to make tests pass.
- Development credential preflight changes additionally require strict manifest parsing, missing
  reference, wrong use, revoked grant, target mismatch, required session, and payload-field
  rejection coverage.
- Run Agent and CLI package tests, Python contract tests, `git diff --check`, stale-contract scans,
  and a compiled CLI help/argument smoke test.
- Web changes require production build plus browser testing of login, target/managed credential
  views, loading/empty/error/forbidden states, payload submission, navigation, console, and network
  failures. Use synthetic credentials only.
- T2 delivery is incomplete until an online synthetic credential lifecycle verifies create, list
  isolation, persistence, verify, promote, and revoke against the deployed Gateway.
- Do not use fallback, silent downgrade, manual hotfix, or an old CLI to bypass a failing contract.
  Fix the root cause and rerun the failed evidence path.
