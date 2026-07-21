# User Confirmation Workflow

DoSql is Agent-native. The user should confirm the intended database change, not
raw SQL text.

## Why Not SQL-First Confirmation

Many users cannot safely review complex SQL. Even when they can, SQL alone does
not explain business intent, target environment, risk, version impact or
verification steps.

The agent must translate database operations into a readable confirmation.

## Required Confirmation Shape

Before a mutating operation can execute, the Skill must return:

```json
{
  "format": "human_readable",
  "title": "确认变更：为订单表增加「external_id」字段",
  "summary": "新增字段用于保存外部系统订单号，便于售后和对账。",
  "target": {
    "projectId": "proj_erp",
    "environmentId": "test",
    "databaseAssetId": "db_orders_test",
    "engine": "mysql"
  },
  "version": {
    "from": 3,
    "to": 4,
    "label": "dosql_000004"
  },
  "changes": [
    "为订单表增加 external_id 字段，类型为 varchar(64)，允许为空。"
  ],
  "risks": [
    "需要确认应用代码已经兼容新增字段。"
  ],
  "verification": [
    "执行后检查订单表是否存在 external_id 字段。"
  ]
}
```

The SQL or MongoDB command is still generated and stored as an internal artifact,
but it is not the primary confirmation UI.

## Execution Gate

The agent may only execute after the approval service turns the accepted
human-readable confirmation into a signed
`dosql.operation-approval.v1` artifact:

- Ed25519 signature verifiable by the fixed trust file at
  `~/.config/dosql/approval-public-key.pem`;
- `status = approved`, approver identity, approval timestamp and expiry;
- operation ID, change-request ID, database name and runtime-resolved
  connection fingerprint created by DoSql;
- exact statement and verification fingerprints.

The signing private key belongs to the approval service and must not be exposed
to the executing agent or stored in a `DOSQL_` runtime variable. If any field,
binding or signature is missing or invalid, the Skill refuses execution.
