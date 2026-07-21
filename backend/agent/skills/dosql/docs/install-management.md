# Install And Management

DoSql is an executable Skill and must be installed in a trusted user-level
doagent Skill root. Project-local Skills remain available as prompt content but
cannot execute local runtimes.

## Current Manual Install

Install DoOps first so the project has `doops` and a gateway target:

```bash
bash backend/scripts/install.sh \
  --project /path/to/business-project \
  --target-name <target-name> \
  --target-gateway <gateway-url> \
  --target-cluster <cluster> \
  --target-instance <instance> \
  --target-token '<gateway-user-token>' \
  --use '<database-capable target>'
```

Then install DoSql:

```bash
mkdir -p ~/.codex/skills/dosql
cp -R DoSuite/DoSql/Skill/. ~/.codex/skills/dosql/
```

Validate:

```bash
doagent skills validate \
  ~/.codex/skills/dosql
```

`SKILL.md` declares the `node` runtime and
`scripts/dosql-agent.mjs` entrypoint. When the Skill is installed in a
directory discovered by doagent, execute commands through doagent:

```bash
doagent skills execute dosql-agent \
  classify \
  --input request.json \
  --output response.json
```

The primary onboarding flow should then discover candidates, ask the user to
confirm names, and register the confirmed assets:

```bash
doagent skills execute dosql-agent \
  discover-databases \
  --input discovery-request.json \
  --output discovery.json
doagent skills execute dosql-agent \
  register-database \
  --input database.json \
  --output registration.json
```

## Target One-Click Install

The intended management interface is:

```bash
doops skill install \
  --project /path/to/business-project \
  --product DoSql
```

The installer should copy the Skill, install or verify the `dosql-agent` CLI,
and check that the target gateway can reach the selected database-capable
doagent node.

## Target One-Click Database Registration

The intended database registration interface is:

```bash
doagent skills execute dosql-agent \
  discover-databases \
  --input discovery-request.json \
  --output discovery.json
doagent skills execute dosql-agent \
  register-database \
  --input database.json \
  --output registration.json
```

Registration should create:

- project and environment mapping;
- database assets;
- user-visible display names and aliases;
- connection or secret references;
- initial version baseline;
- structure snapshot;
- maintenance checklist;
- audit event.

Registration must be read-only against the target database. Mutating operations
belong to the change lifecycle and require user confirmation.

Before query, scan or change planning, the Agent should resolve the database
name from the user sentence:

```bash
doagent skills execute dosql-agent \
  resolve-database \
  --input resolve-request.json \
  --output resolved-database.json
```

If resolution returns `ambiguous`, the Agent must ask the user to choose the
environment or database asset.
