# Managed Helm Workspace Contract

`doops_helm` supports two explicit asset sources:

- `remote`: HTTPS/OCI Chart references and optional remote values URLs.
- `workspace`: paths relative to the runtime-bound `/root/ws/<session>` workspace.

Workspace mode is intended for repositories whose environment Chart owns the complete
values file. It accepts only `deploy/helm/<env>` with a regular `Chart.yaml` and exactly
`deploy/helm/<env>/values.yaml`. Absolute paths, `..`, symlink escapes, missing files,
extra values overlays and `set_strings` are rejected before Helm runs.

The caller must provide `expected_image` as a complete repository tag or digest. The
Agent runs Helm with `--reset-values`, performs a server-side dry run, checks rendered
release tags, then performs the atomic upgrade and verifies every rendered Deployment's
generation, replicas and expected image state. The runtime session is injected by the
MCP server and cannot be supplied by the model.

Workspace mode does not read or copy credential payloads. Remote artifact credentials,
when required, remain governed by the existing exact managed `helmPull` scope.
