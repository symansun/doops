---
name: doops-development-contract
scope: repository
owner: doops-maintainers
---

# DoOps Development Contract

This file records repository-specific rules that are easy to miss during DoOps development.
General engineering rules belong in the caller's global `AGENTS.md`; keep this file focused on
DoOps architecture, security, release, and verification boundaries.

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
  values inheritance. Keep managed Helm authentication, exact scope, package digest pinning,
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
  `--dry-run` is preview-only and still requires an immutable `repository@sha256:<64 hex>` image.
  A new Kubernetes cluster without an Agent and legacy Helm Agents without the controller use
  `backend/scripts/release-managed-agent.sh` with an operator kubeconfig. Its new-cluster
  `--values` mode is bootstrap-only: it performs Helm wait and final Deployment checks, not
  Gateway takeover or automatic rollback; installation acceptance must follow. Its existing
  release mode updates an existing Helm release, stops when no Helm release exists, and does
  not perform adoption. `doops upgrade` only changes the Agent image or rolls back a managed
  release; `proxy.allowedHosts` and `hostAliases` remain explicit external-script configuration.
  Do not create a temporary Agent, target, or release-runner, and do not use `kubectl set image`
  or an Agent-process Helm mutation.
- Existing standalone Docker Agents use the explicit external DockerHost controller
  `backend/scripts/release-host-agent.sh` and its Python implementation, not `doops upgrade`,
  automatic fallback, or a mandatory Kubernetes migration. Require explicit target, session,
  host, user, port (never default to 22), container, cluster, instance, immutable image digest,
  CI source commit, and release. SSH must verify host keys; an optional private authenticated
  control socket is a reference only, never password/token input. Prepare the immutable image
  externally before read-only inspection; dry-run must not pull or create containers and must
  fail explicitly for a missing local artifact. Hold a host-side release lock during mutation,
  preserve the existing container configuration and stopped old container, and roll back
  externally on failure.
  Verify the actual replacement container/image/version, Gateway identity and release commit,
  structuredSSH protocol, argv dispatch and expected failure terminal result, plus real
  foreground/background exec RPC streams with each call's Root RequestID. Successful external
  management SSH is separate evidence, not Agent authentication to the destination; host
  passwords remain external-bootstrap stdin only and must never reach the Agent. Never ask the
  current Agent to exec its own replacement or delete the old container. Full procedure belongs in
  [BUILD_DEPLOY_SOP.md](backend/docs/BUILD_DEPLOY_SOP.md#独立-dockerhost-发布).

## Credential Security

- Public CLI arguments, natural-language prompts, doagent context, Git, issues, logs, audit output,
  and remote workspaces must never contain credential payloads.
- CLI payload input is stdin-only. Gateway encrypts payloads at rest and must fail closed when its
  configured master key or credential audit persistence is unavailable.
- Gateway may send decrypted payload only through the internal credential materialization call to
  the explicitly authorized target Agent. Public prompt and metadata bindings contain names,
  scopes, Secret names/types/key sets, resource versions, digests, status, and stable error
  categories only.
- Credential access is exact and persistent: user plus configured target `cluster/instance`, exact
  namespace, use, workload kind/name, and image, Helm, or required-key fields. Do not add wildcard,
  prefix, inferred, project, environment, template, or workflow-path grants.
- A project may declare metadata-only development requirements in
  `.agent/doops-credentials.yaml`. Before Git, registry, Helm, TLS, or API development work that
  depends on managed credentials, run
  `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml`.
  The manifest is only a fail-closed checklist of `credentialRef` plus `use`; it must never contain
  payloads, grant authority, materialization instructions, local secret paths, or fallback values.
  Credential references must be lowercase resource names, not URLs, paths, tokens, or arbitrary
  scalars. It does not weaken the exact target/workload grant model.
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
- Build-tree binaries such as `backend/skills/doops-cli/bin/*` and binaries embedded inside a Skill
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
- One `doops login` is shared by configured targets on the same normalized Gateway URL. Credential
  listing and use still enforce the logged-in user and selected target's exact cluster/instance.

## Required Documentation

Meaningful CLI, CI/CD, credential, permission, payload-boundary, or release behavior changes must
update all affected authoritative surfaces in the same change:

- `backend/skills/SKILL.md`
- `backend/docs/DOOPS_USAGE_GUIDE.md`
- `backend/docs/CREDENTIAL_RESOURCES.md`
- `backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md`
- `backend/docs/DOOPS_API_GUIDE.md` or `backend/docs/DOOPS_GATEWAY_TUNNEL.md` when protocol changes
- CLI `--help` text and README when user-visible commands change

Do not document intended behavior that the current CLI and Gateway do not implement.

## Verification And Delivery

- Credential and CI/CD changes require Given/When/Then coverage for success, denied authority,
  invalid exact scope, unavailable audit/key, verification failure, revocation, and payload
  redaction. Do not weaken expected HTTP status or output assertions to make tests pass.
- Development credential preflight changes additionally require strict manifest parsing, missing
  reference, wrong use, revoked grant, target mismatch, required session, and payload-field
  rejection coverage.
- Run Agent and CLI package tests, Python contract tests, `git diff --check`, stale-contract scans,
  and a compiled CLI help/argument smoke test.
- Web changes require production build plus browser testing of login, target/managed credential
  views, loading/empty/error/forbidden states, payload submission, navigation, console, and network
  failures. Use synthetic credentials only.
- Delivery is incomplete until commit and push are remotely visible, required CI passes, release
  assets are published, `backend/scripts/install.sh` installs the released CLI and Skill, Gateway
  changes are deployed from the matching immutable CI artifact, and an online synthetic credential
  lifecycle verifies create, list isolation, persistence, verify, promote, and revoke.
- Do not use fallback, silent downgrade, manual hotfix, or an old CLI to bypass a failing contract.
  Fix the root cause and rerun the failed evidence path.
