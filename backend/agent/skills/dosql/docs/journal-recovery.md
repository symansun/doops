# Journal Diagnosis And Recovery

## Scope

These guarantees apply to Node `query` using one persistent audit root, and to
the journal commands below. The Node `execute` command has been removed.
The query root is `.dosql` in the process working directory, or the trusted
embedding caller's `artifactRootProvider`; it is not selected by a JSON request
field or a `DOSQL_ARTIFACT_ROOT` environment variable. A journal is an audit
record, not a database permission or a release-authorization credential.
DoSql does not provide a hosted release-authority signing service.

## Inspect Before Recovery

All commands use the usual `--input` and `--output` JSON file interface.
The output path must be new and distinct from input and artifact paths. The CLI
checks and reserves `<outputPath>.claim` before command execution, then rechecks
the output. A conflicting output or claim produces a failed JSON response on
stderr with `executionOutcome: "not_started"` and no dispatch or lifecycle
mutation. If final publication fails after execution, stderr reports
`DOSQL_RESPONSE_PERSISTENCE_FAILED` with the actual execution outcome and
`retryable: false`. An old response is never overwritten or returned as the new
attempt's result. See [agent-cli-contract.md](agent-cli-contract.md) for error
fields. Retain claims for reconciliation; they are not automatically released.

`inspect-journal` requires:

```json
{
  "operationId": "inspect-orders",
  "journalPath": "/managed-audit/orders.jsonl"
}
```

It returns `format` (`empty`, `legacy`, `hash-chained`), `eventCount`, and
`headHash`. An absent journal is empty. Invalid JSON, invalid events, mixed
formats, partial chains, unsupported schemas and broken hashes fail explicitly,
with `error.code` and the physical `error.lineNumber`.

`DOSQL_JOURNAL_MIGRATION_REQUIRED` means a nonempty old-format journal, not
proof of tampering. Existing valid schema-less chained journals remain readable;
new entries declare `dosql.execution-journal.v1`. Existing hashes are not changed.

## Explicit Legacy Migration

Never delete an old journal, edit hashes or invent succeeded events to unblock
execution. `migrate-journal` requires a pure, valid, unchained source and a
different, nonexistent target:

```json
{
  "operationId": "migrate-orders-20260921",
  "sourceJournalPath": "/managed-audit/orders-legacy.jsonl",
  "journalPath": "/managed-audit/orders-chained.jsonl",
  "artifactRoot": "/managed-audit",
  "actor": { "type": "user", "id": "actual-maintainer" },
  "createdAt": "2026-09-21T00:00:00Z"
}
```

Use source and target paths in the managed audit directory and set
`artifactRoot` to the same persistent root used by query so imported IDs block
replay. The migration command accepts explicit paths; it does not enforce
query's path containment. Supply the actual actor and migration time.
Stop writers using the old runtime before migration: filesystem
locks coordinate cooperating new writers, not older software that ignores them.
The command:

1. Locks and reads the source without rewriting it.
2. Records the original bytes' SHA-256 and event count in a
   `journal.legacy_imported` event.
3. Reserves the historical operation IDs in the same audit root, then imports
   events with `historicalIntegrity: "unverified"`.
4. Publishes a complete, synced target atomically, refusing to overwrite it.

Check the returned `sourceHash`, source bytes and target `headHash`, then
explicitly change future requests to use the new journal path. Retain the
original journal. Replay preserves the unverified-history marker.
If publication fails after historical IDs have been reserved, those claims
remain protective. Explicitly rerunning migration with the identical source
bytes can reuse only those matching import claims; an ordinary execution claim
or different source hash is a conflict. This never retries SQL.

Migration establishes integrity only from the point of import. It does not
prove the old events were genuine, rerun SQL, reconcile database state, approve a
release or grant database permissions. Partial chains and mixed journals are
not eligible for migration. Investigate them without modifying the original.

## Execution Outcomes And Retries

Node `query` failures include `executionOutcome` and `retryable: false`.
These fields are not a Gateway error schema; the current Go MCP wrapper returns
an execution error with a text reason rather than parsing stderr JSON. See the
[CLI response boundary](agent-cli-contract.md#target-command-shape).

| Outcome | Meaning | Required action |
| --- | --- | --- |
| `not_started` | This attempt did not dispatch the database operation | Correct the cause; inspect any claim before a new operation |
| `unknown` | Dispatch failed ambiguously, or an ID/evidence path is already claimed | Reconcile journal, evidence and database state; do not blindly repeat SQL |
| `succeeded` | The database client reported query success; audit publication failed later | Reconcile the audit failure without blindly repeating the query |

`DOSQL_AUDIT_PERSISTENCE_FAILED` is not a database rollback.
Its `auditErrorCode` preserves the underlying filesystem/journal error code,
with `lineNumber` when available, so recovery does not hide the original cause.
Query has no separate verification statement. Client failure may mean the read
ran before the connection was lost; the executor reports `unknown`, not rollback.
No automatic retry is performed.

Before dispatch, the executor checks for existing evidence (including symlinks
and directories), atomically reserves an operation ID under `operation-claims/`
and then `<evidencePath>.claim`, and validates the journal while appending the
planned event with duplicate rejection under the journal lock.
Claims survive process crashes. Once an ID is claimed, it cannot execute again
within that artifact root, even with a new journal/evidence path. A different
operation cannot reuse a claimed evidence path. A pre-existing journal
operation also blocks reuse in the selected journal even without an earlier
claim; the new ID claim remains protective. Explicitly migrated historical IDs
are reserved across journals.

This is not replay prevention for all historical records. Pre-upgrade IDs
without claims in other journals, including valid hash-chained journals, are
outside cross-journal protection. Keep the original journal selected and
reconcile its history before changing paths. There is no global historical
index or directory scan; `migrate-journal` supports only unchained legacy
journals, not existing hash-chained journals. Claim or selected-journal failure
prevents dispatch.

Claims prove intent, not completion. Never automatically delete them or change
IDs to bypass a failed attempt. After human reconciliation, a genuinely new
operation needs a new ID and evidence path. This is local replay prevention,
not database-wide exactly-once execution: deleting the audit directory, changing
artifact roots or using another client bypasses its scope.

## Evidence And Durability

Evidence `dosql.database-operation-evidence.v1` is published by exclusive atomic
creation, never truncating an existing destination. Persisted results contain
output sizes and fingerprints and an explicit execution outcome. The existing
v1 `evidenceFingerprint` summarizes the returned execution data; it is not a hash
of the redacted file bytes. File writes are synced before publication.

This is application-enforced no-overwrite behavior, not WORM storage, a digital
signature or proof against the operating-system file owner rewriting files and
recomputing hashes. Use independently managed retention/signing if that stronger
trust boundary is required. A complete file can exist even if directory sync or
the final journal append fails; inspect it before attempting recovery.

SQL transactions and filesystem audit records are not one atomic transaction.
Crash recovery requires reconciliation. Lock timeout is explicit; confirm no
writer is active before investigating a stale `.lock` file. Locks are never
silently expired. Do not confuse temporary locks with persistent `.claim` files.

## Other Command Boundaries

The older forward/rollback/metadata/restore-check executors are separate code
paths, not clients of `executeControlledDatabaseOperation`; this release does
not grant them the above replay-prevention or evidence guarantees.
`record-metadata-commit-execution` projects supplied results, not independently
authenticated execution. `scan` analyzes supplied samples, not live database
queries. None of these outputs is a release-authority signature.
