# PostgreSQL Rules

## Read Operations

Read-only commands include:

- `SELECT`;
- `EXPLAIN`;
- catalog inspection queries;
- `SHOW`.

## Change Operations

PostgreSQL changes must become migration scripts:

- `CREATE`, `ALTER`, `DROP`, `COMMENT`, `TRUNCATE`;
- `INSERT`, `UPDATE`, `DELETE`, `MERGE`;
- role and grant changes.

## Validation

Before execution:

- inspect current schema with catalog queries;
- render migration SQL;
- prefer transaction-wrapped migration when safe;
- run dry-run or parse validation when available;
- define verification queries.

After execution:

- capture notices and affected rows;
- verify schema diff;
- attach output and artifact checksums.
