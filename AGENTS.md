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
| Agent entrypoint | `backend/agent/cmd/agent/` | Imports `internal/server`, which drives `internal/dispatcher` |
| CLI | `backend/cli/cli/doops/` | Separate Go module named `doops` |
| Agent-side skills | `skills/agent/` | Copied into the Agent image (`/app/skills`); includes `dosql/` and `doweb/` |
| Operator Skill | `skills/doops/SKILL.md` + `backend/docs/*.md` | Installed into projects by `backend/scripts/install.sh` |
| Helm Charts | `backend/deploy/helm/` | Runtime assets only |
| Release scripts | `backend/scripts/` | Python contract tests live in `backend/test/` |
| Web console | `app/`, `components/`, `lib/` (Next.js) | Built from `Dockerfile.web`; UI primitives live in `components/ui/` (`components.json`) |
| Frontend design & UI standards | `DESIGN.md` | Authoritative UI framework, tokens, component red lines, and frontend working standards |
| Release notes | `CHANGELOG.md` | Bundled into the Web image; the console's 更新内容 dialog reads it |

`bin/`, `backend/bin/`, `backend/cli/bin/`, `.next/`, `node_modules/`, `.agent/skills/doops/` (the
installed Skill copy), and the local `/examples/`, `/test/`, `/.local/` scratch areas are ignored
artifacts. Do not treat them as sources, and do not read or execute stale binaries in them.

## Topic Ownership

Each topic has one authoritative document. Expand a rule only there; elsewhere, link to it.

| Topic | Authoritative document |
|---|---|
| Workload delivery (`ask --apply`, Helm assets) | `backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md` |
| Credentials | `backend/docs/CREDENTIAL_RESOURCES.md` |
| Harbor image replication | `backend/docs/HARBOR_REPLICATION_SPEC.md` |
| Agent and Gateway release | `backend/docs/BUILD_DEPLOY_SOP.md` |
| Gateway protocol and lifecycle | `backend/docs/DOOPS_GATEWAY_TUNNEL.md` |
| External API | `backend/docs/DOOPS_API_GUIDE.md` |
| Installation and onboarding | `backend/docs/CLUSTER_INSTALL_GUIDE.md` |
| Day-to-day commands | `backend/docs/DOOPS_USAGE_GUIDE.md` |
| Web console behavior | `backend/docs/DOOPS_WEBUI.md` |
| Web frontend UI framework & design standards | `DESIGN.md` |
| Model settings | `backend/docs/MODEL_STANDARDS.md` |
| Configuration and environment variables | `backend/docs/CONFIGURATION.md` |
| Runtime timeouts and limits | `backend/docs/RUNTIME_LIMITS.md` |
| Edge tunnel design | `backend/docs/design/DOOPS_EDGE_TUNNEL.md` |
| Gateway MySQL backend operations | `backend/docs/GATEWAY_MYSQL_OPERATIONS.md` |
| Gateway MySQL migration (offline schema, copier, runtime SQL audit) | `backend/docs/GATEWAY_MYSQL_MIGRATION_PLAN.md` |
| Skill-First inspection & capability framework | `backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md` |
| Console plugins & scenario packs | `backend/docs/design/PLUGIN_ARCHITECTURE_SPEC.md` |
| Skill product design matrix (24 skills) | `backend/docs/design/SKILL_PRODUCT_DESIGN_MATRIX.md` |
| Release notes (console 更新内容) | `CHANGELOG.md` |

## Commit Discipline

Internal development ledgers, patrol logs, review scratch reports, and customer-specific cutover
records are never tracked in this public repository; keep them in a private workspace or untracked
local directory (`.gitignore` excludes `docs/operations/`, `docs/reviews/`, `backend/docs/reviews/`,
`docs/design/`, and `backend/deploy/instances/`). Commits for a single task use
`git commit -o <owned paths>` so a shared index cannot sweep another session's staged work.

## Canonical Architecture

- Helm delivery can use an explicit natural-language task:
  `doops -session <s> ask --target <t> --apply [--credential <name>...] --msg '<facts>'`,
  or an authorized native Helm command through `doops -session <s> exec --target <t>`.
- DoOps supplies the execution channel, target authorization and audit. Business release
  eligibility belongs to the caller's project policy, not to a global DoOps gate.
  Resolve execution parameters from the task or its specified assets; preserve native Helm
  behavior, credential isolation and truthful execution evidence. Detailed asset, result and
  migration contracts belong only in
  [NATURAL_LANGUAGE_DEPLOYMENT.md](backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md).
- GitHub Actions tests, scans, and publishes the public tag release assets (CLI, Gateway binaries,
  and the Skill bundle) from tags on `main`, gated by the same CI workflow and re-verified after
  publishing. Production images are built and pushed by the operator with explicit
  registry/repository/tag and a full source commit; the repository contains no image publishing
  pipeline of record. The CNB pipeline in `.cnb.yml` publishes internal preview images into the CNB
  registry only; it is never the release of record, and a tag release still comes from GitHub
  Actions assets plus the operator's own image build. Gateway authenticates users, authorizes
  targets and credentials, routes traffic, and
  audits. The selected target Agent performs the actual Kubernetes or Helm mutation and returns
  live evidence.
- For DoOps runtime image/version releases, the publisher must establish
  the exact source-commit and registry-digest provenance before invoking the command. The CLI
  and Agent validate the allowed input form and live result; they do not verify build origin.
- Do not add or restore `EnvironmentRegistry`, `EnvironmentDefinition`, `DeploymentTemplate`,
  `deploy/environments.yaml`, `doops cicd run`, prompt-derived deployment scope, `CICDReport`, or
  `doops_cicd_result` as a deployment root or compatibility path.
- A DoOps Agent (one cluster/instance target) is the authorization unit: Gateway authorization
  is (user, cluster, instance) only, and a grant opens every operation on that target and its
  bound credentials. Openness is the default: do not add per-action or per-operation restrictions
  and do not narrow what a target grant covers. Platform admin is a user flag. Gateway has no
  per-action grants, execution quotas, operation admin, unlock, audit-optimize, or repo registry;
  do not reintroduce them. The monitor engine (`StartMonitorRunner`, `-monitor-tick`) is the sole
  sanctioned scheduler: it exists solely to dispatch due `monitor_schedules` inspections through
  the audited tunnel path; do not add other Gateway-side schedulers or periodic job loops.
- `deploy/helm/<name>/` contains runtime assets only. It must not become an environment registry,
  credential store, target selector, or deployment authorization source.
- Agent upgrade has one hard constraint: an Agent cannot replace its own workload from its own
  process, because the rollout kills that process. The replacement always runs in a separate
  controller: an in-cluster Job started by `doops upgrade` (or, for a first upgrade the old Agent
  cannot start itself, the same Job started through `doops exec`), or an external script.
  Every path, whatever starts it, must:
  1. back up what it changes (Helm revision, or the exported Deployment/ConfigMaps when the
     workload is not yet Helm-managed);
  2. verify the result: new Pod Ready on the pinned image digest, Gateway reconnect, and the
     reported `release_commit`;
  3. restore the backup automatically when verification fails;
  4. run through the audited Gateway path, never with credential payloads in arguments or output.
  Bringing an unmanaged Agent Deployment under Helm is allowed under the same four rules. The
  procedure and per-runtime details (Helm, unmanaged Deployment, standalone Docker) live in
  [BUILD_DEPLOY_SOP.md](backend/docs/BUILD_DEPLOY_SOP.md).

## Credential Security

- Public CLI arguments, natural-language prompts, doagent context, Git, issues, logs, audit output,
  and remote workspaces must never contain credential payloads.
- CLI payload input is stdin-only. Gateway encrypts payloads at rest and must fail closed when its
  configured master key or credential audit persistence is unavailable.
- `credential configure` orchestrates the existing lifecycle; do not create a second store or
  bypass grant/verify/promote. Platform-admin Secret import accepts exact references only and
  transfers selected values from Agent to Gateway on the private control channel, never via exec.
- Gateway may send decrypted payload only through the internal credential materialization call to
  the granted target Agent. Public prompt and metadata bindings contain names,
  scopes, Secret names/types/key sets, resource versions, digests, status, and stable error
  categories only.
- Runtime credential use requires only the user's grant on that target. Do not
  add a credential action or a per-operator credential grant. Active grants identify
  which target/use owns a credential; management still belongs to its owner or platform admin.
  Database bindings resolve directly from stored target metadata, without a model-generated scope;
  the saved namespace is used for materialization. DoSql/Agent owns database assets and SQL policy.
  Namespace, workload, release, image and repository paths are execution metadata, not credential
  permission dimensions. Resolve explicit names directly from stored target bindings, without a
  model scope-planning request. Credential management uses owner/admin; personal credential APIs
  do not require separate credential actions. Target Secret import requires a grant on that target.
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
- Users install a checksummed, versioned release asset through
  `backend/scripts/install.sh --version <tag> --project <absolute-project-path>`. GitHub Releases
  (`https://github.com/symansun/doops/releases/download`) are the only install source;
  `DOOPS_RELEASE_ASSET_BASE` may point the same sha256, identity and contract checks at a mirror.
- Before replacement, the installer must validate an existing config with the downloaded release
  CLI's own Go parser. Do not maintain a second Python, Node, jq, or shell JSON schema.
- The canonical Skill source is `skills/doops/SKILL.md`. The installer copies it and
  `backend/docs/*.md` from the exact commit embedded in the downloaded CLI into
  the project and global `.agent/skills/doops/` plus existing Codex/Agents copies, never from the
  current working tree. Missing release source must fail before replacement; the install receipt
  records `skill_commit` and every `skill_path`. Preserve runtime config/auth when replacing
  Skill/docs. Installation scope is documented in `backend/docs/CLUSTER_INSTALL_GUIDE.md`.
  Do not edit installed copies first.
- Command templates in `skills/doops/SKILL.md` and the operator docs are executed by the
  contract tests. `backend/test/test_sql_skill_contract.py` extracts backtick spans that start with
  `doops`, splits them with `shlex`, reads optional flags from `[--flag]`, expands alternatives
  from `(--a|--b)` and `a|b`, and substitutes `<placeholder>` before running the real CLI. Keep
  wording and caveats outside the backtick span: text inside it becomes a stray argument and the
  extracted command fails.
- One `doops login` is shared by configured targets on the same normalized Gateway URL. Target
  credential listing and use inherit target operation authority; management enforces ownership.

## Frontend Engineering Contract

All Web UI work across the public portal (`/`, `/cases`, `/docs`, `/blog`) and the console
(`/console`, `/console/admin`) follows the authoritative standards in [DESIGN.md](DESIGN.md):

- **Framework-only UI (zero px-driven hand styling)**: Use the repository's standard
  **shadcn/ui (`components.json`) + Radix UI + Tailwind CSS v4 + TanStack Table v8** stack via
  `@/components/ui`. Do not write arbitrary `[...px]` Tailwind utility classes (`w-[...px]`,
  `min-w-[...px]`, `text-[11px]`, `shadow-[...px...]`), do not add custom CSS widget classes
  (`.btn*`, `.input*`, `.status-*`, `.console-sidebar`, `.portal-container`), and do not write raw
  HTML `<table>` tags or custom modal/drawer overlays outside `components/ui/`.
- **Semantic OKLCH tokens & standard `rem` scale**: Preserve Tailwind v4's standard `rem`
  typography scale in `app/globals.css` (do not override `--text-xs`/`--text-sm`/`--text-base`/
  `--text-lg`/`--text-xl`). Never hardcode hex colors or bare Tailwind color scales (`slate-*`,
  `sky-*`, `cyan-*`, `emerald-*`, `amber-*`, `rose-*`, etc.) in components or `console.css`. Keep
  `app/console/console.css` strictly scoped to `@agent-cloud/agent-ui` overrides.
- **Canonical layout & component entrypoints**: Every public portal outer rail uses
  `<PortalContainer>`; console navigation uses `<Sidebar>` + Radix `<Sheet>` (never native
  `<dialog>`); dialogs use `components/ui/dialog.tsx` (`<Dialog>`/`<Modal>`); selects use Radix
  `<Select>`; tabular data uses `<Table>` / `<DataTable>`.
- **Package manager discipline**: Always install or update Web dependencies with
  `corepack pnpm` (`pnpm@10.18.0`, matching `package.json#packageManager`) so
  `pnpm-workspace.yaml` `patchedDependencies` (e.g., `@agent-cloud/agent-ui`) remain intact.
- **Automated guard**: `lib/console-guards.test.mjs` enforces the whole-site UI framework rules
  above. Never weaken or bypass the guard; fix violations at the source.

## Required Documentation

A behavior change updates, in the same change, the one authoritative document that owns its topic
(see Topic Ownership), plus CLI `--help` text and README when user-visible commands change. A
user-visible change also records an entry under `## [Unreleased]` in `CHANGELOG.md`, which the
console reads as the release log; a release replaces that heading with `## [version] — YYYY-MM-DD`
and never rewrites the published entries.
`skills/doops/SKILL.md` keeps only red lines, command selection, and links; update it only when a
red line or a command entry changes. Other documents link to the owner instead of restating it.

Do not document intended behavior that the current CLI and Gateway do not implement.

## Verification And Delivery

Pick the highest tier the change touches:

| Tier | Applies to | Done when |
|---|---|---|
| T0 | Docs, tests, or refactors with no behavior change | Relevant local tests pass, `git diff --check` is clean, the branch is pushed, and required CI passes |
| T1 | CLI, Agent, Gateway, or Web behavior | T0, plus the checks below for the touched components, published release assets, `backend/scripts/install.sh` installing the released CLI and Skill, and touched components deployed from the matching immutable build or release artifact |
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
