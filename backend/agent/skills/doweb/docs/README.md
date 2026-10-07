# DoWeb Documentation

The user-facing DoWeb entry is the unified DoOps Gateway runtime. Use
`doops web` or the DoOps Console `网站` workbench. The examples below describe
the bundled target-local engine; they do not create a second login, target
registry or SSH credential flow.

This directory contains the operator and developer documentation for DoWeb.

## Start Here

- `overview.md`: product model, current MVP scope and architecture.
- `cli-reference.md`: all supported `doweb.mjs` commands and flags.
- `site-lifecycle.md`: publish, update, check, rollback, disable and restore.
- `dns-and-routing.md`: DNS, wildcard records, fake IPs, multi-A records,
  HTTP/HTTPS boundaries and gateway routing.
- `troubleshooting.md`: common failure modes and evidence-driven triage.
- `remote-operations.md`: target-local DoOps execution, first-install bootstrap,
  Docker mode, host mode and remote evidence.
- `resource-control-roadmap.md`: staged roadmap from MVP SiteSpec to the
  mature gateway-style ResourceSpec model.

## Current vs Planned

Current behavior is documented in:

- `cli-reference.md`
- `site-lifecycle.md`
- `remote-operations.md`

Planned resource-control behavior is documented in:

- `resource-control-roadmap.md`

When these disagree, treat the CLI reference as the source of current runtime
truth and the roadmap/spec as future product direction.
