# Scanning

DoSql `scan` analyzes supplied database samples and log lines. It does not
connect to a database, collect live samples or append an execution journal.
Collect authorized live evidence through `doops sql --mode query` first.

## Commands

Current implemented interface:

```bash
node backend/agent/skills/dosql/scripts/dosql-agent.mjs scan \
  --input scan-request.json \
  --output scan-report.json
```

Log extraction and security analysis are exposed through `scan`. Standalone
`extract-sql-logs` and `analyze-security` commands are not implemented.

## Scan Request

```json
{
  "operationId": "dbop_...",
  "asset": {
    "databaseAssetId": "db_proj_olap_test_mysql",
    "engine": "mysql",
    "versionText": "8.0"
  },
  "samples": {
    "mysql": {
      "status": { "Threads_connected": 85, "Slow_queries": 2 },
      "variables": { "max_connections": 100, "slow_query_log": "ON" }
    },
    "sqlLogs": {
      "source": "authorized-slow-log-sample",
      "lines": ["SELECT * FROM orders WHERE id = 1 OR 1 = 1;"]
    }
  },
  "scannedAt": "2026-11-06T00:00:00Z"
}
```

## Results And Limits

- Modules are selected by `asset.engine`; an input `modules` list is not a filter.
- Supplied MySQL slow log lines become structured events and SQL injection
  indicators, not proof of an exploit.
- The result contains health findings, log events and security findings.
  Its `audit.captured` field is report metadata, not proof of a persisted journal.
- Missing samples do not prove database health. Preserve collection failures
  separately and do not present this report as a successful live inspection.
- Samples and logs must contain no passwords or credential payloads.

## MySQL Initial Checks

- `Threads_connected` versus `max_connections`;
- `slow_query_log` state;
- `Slow_queries` counter;
- extraction from supplied slow log lines; version is supplied asset metadata.

## MongoDB Initial Checks

- an explicit `samples.mongodb.pingOk=false` produces a failed-ping finding;
- version is supplied asset metadata; database listing, replica state, storage
  and slow operation collection are not implemented by `scan`.
