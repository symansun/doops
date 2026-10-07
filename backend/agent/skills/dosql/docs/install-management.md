# Install And Management

DoSql is bundled inside the DoOps Agent runtime. Users install and authenticate
one product, `doops`; they do not install a second database gateway, target
registry or token store.

## User Installation

Install the versioned DoOps CLI and skill through the DoOps release installer:

```bash
bash backend/scripts/install.sh \
  --version <release-tag> \
  --project /absolute/path/to/business-project
```

Then configure a Gateway target and log in once:

```bash
doops add --name <target> --gateway <gateway-url> \
  --cluster <cluster> --instance <instance> --use '<target purpose>'
doops -session setup login --target <target>
```

The target route and Gateway login are the only user-facing connection
configuration. DoSql does not read `~/.config/dosql/connections.json`, a skill
local default target, or credentials passed through command-line arguments.

## Unified Entry Points

Use the same `doops` CLI for all skills:

```bash
doops -session db-read sql --target <target> --mode query \
  --database <logical-database> \
  --request '统计最近 24 小时的失败订单，只读执行'

doops -session db-plan sql --target <target> --mode plan \
  --request '为订单表增加 external_id 生成变更计划，不执行'

doops -session db-change sql --target <target> --mode apply \
  --credential <database-resource> \
  --request '按已批准的变更请求执行并返回 journal/evidence'
```

`sql --mode query` and `sql --mode plan` use Gateway `ask` and declare
`skill=dosql`. `sql --mode apply` uses Gateway `apply`. Gateway authenticates
the user, checks the exact target/action/credential scope and materializes only
metadata bindings. DoSql then applies database classification, journal,
verification and signed change approval rules.

## Agent Runtime

The bundled executable skill lives at:

```text
backend/agent/skills/dosql/
```

Its entrypoint is `scripts/dosql-agent.mjs`. The Agent may call it for
deterministic database operations, but all user-facing admission still comes
through the authenticated DoOps Gateway operation. Direct execution against a
business database without a Gateway target and DoSql operation record is not a
supported path.
