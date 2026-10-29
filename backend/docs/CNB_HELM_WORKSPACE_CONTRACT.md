# Managed Helm Workspace Contract

`doops_helm` supports two explicit asset sources:

- `remote`: HTTPS Chart download references and optional remote values URLs.
- `workspace`: paths relative to the runtime-bound `/root/ws/<session>` workspace.

Workspace mode is intended for an uploaded repository or deployment package whose Chart owns
the complete values file. It accepts any session-relative Chart directory containing a regular
`Chart.yaml`; `value_files` may be omitted or may name regular session-relative files. Absolute
paths, `..`, symlink escapes and missing files are rejected before Helm runs.

The caller must provide `expected_image` as a complete repository tag or digest. The
Agent runs Helm with `--reset-values`, performs a server-side dry run, checks rendered
release tags, then performs the upgrade (with `--atomic` only when the caller passes `atomic: true`) and verifies every rendered Deployment's
generation, replicas and expected image state. The runtime session is injected by the
MCP server and cannot be supplied by the model.

Workspace mode does not read or copy credential payloads. Remote artifact credentials,
when required, remain governed by the existing exact managed `helmPull` scope.
