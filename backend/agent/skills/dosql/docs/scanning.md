# Scanning

DoSql scanning modules let the agent inspect database health and security
signals without changing data.

## Commands

Current implemented interface:

```bash
node DoSuite/DoSql/Skill/scripts/dosql-agent.mjs scan \
  --input scan-request.json \
  --output scan-report.json
```

Target standalone interfaces:

```bash
dosql-agent extract-sql-logs --input log-request.json --output sql-log-events.json
dosql-agent analyze-security --input sql-log-events.json --output findings.json
```

The first CLI implementation exposes log extraction and security analysis
through `scan`; standalone `extract-sql-logs` and `analyze-security` commands
are still target commands.

## Scan Request

```json
{
  "operationId": "dbop_...",
  "projectId": "proj_olap",
  "environmentId": "test",
  "databaseAssetId": "db_proj_olap_test_mysql",
  "engine": "mysql",
  "modules": [
    "mysql.health",
    "mysql.sql_logs",
    "security.sql_injection"
  ],
  "connectionRef": "secret://dosql/proj_olap/test/mysql"
}
```

## Required Behavior

- Create or receive an operation record before scanning.
- Run read-only checks only.
- Extract logs into structured events.
- Detect SQL injection indicators as findings, not as final exploit proof.
- Return health status, findings, evidence references and recommendations.
- Do not print or store database passwords.
- Do not skip failed checks; include them as failed evidence.

## MySQL Initial Checks

- connection and version;
- `Threads_connected` versus `max_connections`;
- `slow_query_log` state;
- `Slow_queries` counter;
- slow log extraction when available.

## MongoDB Initial Checks

- ping;
- version and database list;
- future replica state, storage and slow operation samples.
