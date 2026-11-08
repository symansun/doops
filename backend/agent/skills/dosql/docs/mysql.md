# MySQL Rules

## Read Operations

Read-only commands include:

- `SELECT`;
- `SHOW`;
- `DESCRIBE` / `DESC`;
- `EXPLAIN`.

They are audited by DoSql. They do not enter change documents by default.

## Change Operations

MySQL schema and data changes must become migration scripts:

- `CREATE`, `ALTER`, `DROP`, `RENAME`, `TRUNCATE`;
- `INSERT`, `UPDATE`, `DELETE`, `REPLACE`;
- grants and account operations.

## Validation

Before execution:

- inspect current table schema;
- render final SQL script;
- run syntax validation where possible;
- produce rollback or mitigation notes;
- record script fingerprint.

After execution:

- capture affected rows;
- capture warnings;
- run verification query;
- store evidence.
