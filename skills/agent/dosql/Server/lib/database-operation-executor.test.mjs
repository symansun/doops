import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import {
  appendFile, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, symlink, writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

import { executeControlledDatabaseOperation } from "./database-operation-executor.mjs";
import { appendJournalEvent, migrateLegacyJournal, readJournalEvents } from "./execution-journal.mjs";

const RESULT = {
  status: "succeeded",
  transactionId: "test-tx",
  stdout: "synthetic-private-query-result",
  stderr: "",
  affectedRows: 0,
};

async function setup(t) {
  const root = await realpath(await mkdtemp(join(tmpdir(), "dosql-query-audit-")));
  t.after(() => rm(root, { recursive: true, force: true }));
  const dispatchPath = join(root, "dispatches.jsonl");
  const input = {
    operationId: "query-test",
    mode: "query",
    projectId: "synthetic-project",
    databaseAssetId: "synthetic-database",
    environmentId: "test",
    engine: "postgres",
    actor: { type: "user", id: "synthetic-user" },
    statement: "select 1",
    journalPath: join(root, "journal.jsonl"),
    evidencePath: join(root, "evidence.json"),
    artifactRootProvider: () => root,
    adapter: {
      trustedClient: true,
      targetFingerprint: () => "synthetic-target",
      async execute(request) {
        await appendFile(dispatchPath, `${JSON.stringify(request)}\n`);
        return RESULT;
      },
    },
  };
  return { root, input, dispatchPath };
}

async function assertAbsent(path) {
  await assert.rejects(lstat(path), { code: "ENOENT" });
}

async function statuses(input) {
  return (await readJournalEvents(input)).map((event) => event.status);
}

function claimPath(root, operationId) {
  const hash = createHash("sha256").update(operationId.trim()).digest("hex");
  return join(root, "operation-claims", `${hash}.json`);
}

test("query publishes immutable redacted evidence and a complete journal", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  const result = await executeControlledDatabaseOperation(input);
  assert.equal(result.status, "verified");
  assert.equal(result.executionOutcome, "succeeded");
  assert.equal(result.executionResult.stdout, RESULT.stdout);
  assert.deepEqual(await statuses(input), ["planned", "running", "succeeded", "verified"]);
  const persisted = await readFile(input.evidencePath, "utf8");
  assert.ok(!persisted.includes(RESULT.stdout));
  assert.equal(JSON.parse(persisted).executionResult.stdoutBytes, Buffer.byteLength(RESULT.stdout));
  assert.equal((await lstat(input.evidencePath)).mode & 0o777, 0o600);
  assert.equal(JSON.parse(await readFile(claimPath(root, input.operationId))).operationId, input.operationId);
  assert.equal(JSON.parse(await readFile(`${input.evidencePath}.claim`)).operationId, input.operationId);
  const dispatched = JSON.parse((await readFile(dispatchPath, "utf8")).trim());
  assert.equal(dispatched.readOnly, true);
  assert.equal(dispatched.statement, "select 1");
});

test("repeating a query preserves the original evidence and journal bytes", async (t) => {
  const { input, dispatchPath } = await setup(t);
  await executeControlledDatabaseOperation(input);
  const evidence = await readFile(input.evidencePath);
  const journal = await readFile(input.journalPath);
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_EVIDENCE_ALREADY_EXISTS", executionOutcome: "unknown", retryable: false,
  });
  assert.deepEqual(await readFile(input.evidencePath), evidence);
  assert.deepEqual(await readFile(input.journalPath), journal);
  assert.equal((await readFile(dispatchPath, "utf8")).trim().split("\n").length, 1);
});

test("concurrent identical IDs in different journals dispatch once across the audit root", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  const attempts = Array.from({ length: 8 }, (_, index) => ({
    ...input,
    operationId: index % 2 ? ` ${input.operationId} ` : input.operationId,
    journalPath: join(root, `journal-${index}.jsonl`),
    evidencePath: join(root, `evidence-${index}.json`),
  }));
  const results = await Promise.allSettled(attempts.map(executeControlledDatabaseOperation));
  assert.equal(results.filter((result) => result.status === "fulfilled").length, 1);
  for (const [index, result] of results.entries()) {
    if (result.status === "fulfilled") continue;
    assert.equal(result.reason.code, "DOSQL_OPERATION_ALREADY_EXISTS");
    assert.equal(result.reason.executionOutcome, "unknown");
    assert.equal(result.reason.retryable, false);
    await assertAbsent(attempts[index].journalPath);
    await assertAbsent(attempts[index].evidencePath);
  }
  assert.equal((await readFile(dispatchPath, "utf8")).trim().split("\n").length, 1);
  assert.equal((await readdir(join(root, "operation-claims"))).length, 1);
});

test("independent Node processes share the persistent operation ID reservation", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  const moduleUrl = new URL("./database-operation-executor.mjs", import.meta.url).href;
  const script = `
    import { appendFile } from "node:fs/promises";
    import { executeControlledDatabaseOperation } from ${JSON.stringify(moduleUrl)};
    const input = JSON.parse(process.argv[1]);
    input.artifactRootProvider = () => ${JSON.stringify(root)};
    input.adapter = {
      trustedClient: true,
      targetFingerprint: () => "synthetic-target",
      async execute(request) {
        await appendFile(${JSON.stringify(dispatchPath)}, JSON.stringify(request) + "\\n");
        return ${JSON.stringify(RESULT)};
      },
    };
    try {
      await executeControlledDatabaseOperation(input);
      console.log(JSON.stringify({ status: "succeeded" }));
    } catch (error) {
      console.log(JSON.stringify({
        status: "failed", code: error.code, executionOutcome: error.executionOutcome,
      }));
    }
  `;
  const attempts = Array.from({ length: 6 }, (_, index) => ({
    ...input,
    journalPath: join(root, `process-journal-${index}.jsonl`),
    evidencePath: join(root, `process-evidence-${index}.json`),
  }));
  const outputs = await Promise.all(attempts.map((attempt) =>
    promisify(execFile)(process.execPath, [
      "--input-type=module", "-e", script, JSON.stringify(attempt),
    ], { timeout: 10000 }),
  ));
  const results = outputs.map(({ stdout }) => JSON.parse(stdout));
  assert.equal(results.filter((result) => result.status === "succeeded").length, 1);
  for (const result of results.filter((entry) => entry.status === "failed")) {
    assert.equal(result.code, "DOSQL_OPERATION_ALREADY_EXISTS");
    assert.equal(result.executionOutcome, "unknown");
  }
  assert.equal((await readFile(dispatchPath, "utf8")).trim().split("\n").length, 1);
});

test("concurrent different IDs cannot reuse one evidence path", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  const attempts = Array.from({ length: 8 }, (_, index) => ({
    ...input,
    operationId: `query-${index}`,
    journalPath: join(root, `journal-${index}.jsonl`),
  }));
  const results = await Promise.allSettled(attempts.map(executeControlledDatabaseOperation));
  assert.equal(results.filter((result) => result.status === "fulfilled").length, 1);
  for (const [index, result] of results.entries()) {
    if (result.status === "fulfilled") continue;
    assert.equal(result.reason.code, "DOSQL_EVIDENCE_ALREADY_EXISTS");
    await assertAbsent(attempts[index].journalPath);
  }
  assert.equal((await readFile(dispatchPath, "utf8")).trim().split("\n").length, 1);
  assert.equal(JSON.parse(await readFile(input.evidencePath)).status, "verified");
});

for (const kind of ["file", "directory", "dangling-symlink"]) {
  test(`pre-existing evidence ${kind} is checked before reserving an ID or dispatching`, async (t) => {
    const { root, input, dispatchPath } = await setup(t);
    if (kind === "file") await writeFile(input.evidencePath, "original evidence");
    if (kind === "directory") await mkdir(input.evidencePath);
    if (kind === "dangling-symlink") await symlink(join(root, "missing-target"), input.evidencePath);
    await assert.rejects(executeControlledDatabaseOperation(input), {
      code: "DOSQL_EVIDENCE_ALREADY_EXISTS", executionOutcome: "unknown", retryable: false,
    });
    await assertAbsent(join(root, "operation-claims"));
    await assertAbsent(`${input.evidencePath}.claim`);
    await assertAbsent(input.journalPath);
    await assertAbsent(dispatchPath);
    if (kind === "file") assert.equal(await readFile(input.evidencePath, "utf8"), "original evidence");
    if (kind === "directory") assert.equal((await lstat(input.evidencePath)).isDirectory(), true);
    if (kind === "dangling-symlink") {
      assert.equal((await lstat(input.evidencePath)).isSymbolicLink(), true);
      await assertAbsent(join(root, "missing-target"));
    }
  });
}

test("an old journal ID without a claim blocks dispatch and stays reserved across journals", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  await appendJournalEvent({
    journalPath: input.journalPath,
    event: {
      operationId: ` ${input.operationId} `,
      eventType: "database.operation.planned",
      status: "planned",
      createdAt: "2026-11-06T00:00:00Z",
    },
  });
  const original = await readFile(input.journalPath);
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_OPERATION_ALREADY_EXISTS", executionOutcome: "unknown", retryable: false,
  });
  assert.deepEqual(await readFile(input.journalPath), original);
  await assertAbsent(dispatchPath);
  await assertAbsent(input.evidencePath);
  await assert.rejects(executeControlledDatabaseOperation({
    ...input,
    journalPath: join(root, "another-journal.jsonl"),
    evidencePath: join(root, "another-evidence.json"),
  }), { code: "DOSQL_OPERATION_ALREADY_EXISTS" });
  await assertAbsent(join(root, "another-journal.jsonl"));
});

test("an evidence claim surviving a crash blocks dispatch even without evidence", async (t) => {
  const { input, dispatchPath } = await setup(t);
  const bytes = '{"operationId":"previous-attempt"}\n';
  await writeFile(`${input.evidencePath}.claim`, bytes);
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_EVIDENCE_ALREADY_EXISTS", executionOutcome: "unknown", retryable: false,
  });
  assert.equal(await readFile(`${input.evidencePath}.claim`, "utf8"), bytes);
  await assertAbsent(dispatchPath);
  await assertAbsent(input.journalPath);
});

test("legacy migration reserves historical IDs even when query selects a different journal", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  const sourceJournalPath = join(root, "legacy.jsonl");
  const source = `${JSON.stringify({
    operationId: input.operationId,
    eventType: "database.operation.succeeded",
    createdAt: "2026-11-06T00:00:00Z",
  })}\n`;
  await writeFile(sourceJournalPath, source);
  await migrateLegacyJournal({
    sourceJournalPath,
    journalPath: join(root, "imported.jsonl"),
    artifactRoot: root,
    operationId: "migration-test",
    actor: input.actor,
    createdAt: "2026-11-06T01:00:00Z",
  });
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_OPERATION_ALREADY_EXISTS", executionOutcome: "unknown", retryable: false,
  });
  assert.equal(await readFile(sourceJournalPath, "utf8"), source);
  await assertAbsent(input.journalPath);
  await assertAbsent(dispatchPath);
});

test("operation claim filesystem failure never dispatches", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  await writeFile(join(root, "operation-claims"), "blocked claim directory");
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_AUDIT_PERSISTENCE_FAILED",
    auditErrorCode: "EEXIST",
    executionOutcome: "not_started",
    retryable: false,
  });
  await assertAbsent(dispatchPath);
  await assertAbsent(input.journalPath);
  await assertAbsent(`${input.evidencePath}.claim`);
});

for (const [name, bytes, code, lineNumber] of [
  ["invalid JSON", "{invalid}\n", "DOSQL_JOURNAL_INVALID_JSON", 1],
  ["legacy", '{"operationId":"old-id","eventType":"old","createdAt":"2026-11-06"}\n',
    "DOSQL_JOURNAL_MIGRATION_REQUIRED", undefined],
]) {
  test(`${name} journal fails before dispatch and retains the original bytes`, async (t) => {
    const { input, dispatchPath } = await setup(t);
    await writeFile(input.journalPath, bytes);
    await assert.rejects(executeControlledDatabaseOperation(input), (error) => {
      assert.equal(error.code, "DOSQL_AUDIT_PERSISTENCE_FAILED");
      assert.equal(error.auditErrorCode, code);
      assert.equal(error.executionOutcome, "not_started");
      assert.equal(error.lineNumber, lineNumber);
      return true;
    });
    assert.equal(await readFile(input.journalPath, "utf8"), bytes);
    await assertAbsent(dispatchPath);
    await assertAbsent(input.evidencePath);
  });
}

test("evidence publication failure after SQL success preserves success and existing evidence", async (t) => {
  const { root, input, dispatchPath } = await setup(t);
  const execute = input.adapter.execute;
  input.adapter.execute = async (request) => {
    const result = await execute(request);
    await writeFile(input.evidencePath, "externally published evidence", { flag: "wx" });
    return result;
  };
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_AUDIT_PERSISTENCE_FAILED",
    auditErrorCode: "EEXIST",
    executionOutcome: "succeeded",
    retryable: false,
  });
  assert.equal(await readFile(input.evidencePath, "utf8"), "externally published evidence");
  assert.deepEqual(await statuses(input), ["planned", "running", "succeeded"]);
  await assert.rejects(executeControlledDatabaseOperation({
    ...input,
    evidencePath: join(root, "retry-evidence.json"),
    journalPath: join(root, "retry-journal.jsonl"),
  }), { code: "DOSQL_OPERATION_ALREADY_EXISTS" });
  assert.equal((await readFile(dispatchPath, "utf8")).trim().split("\n").length, 1);
  assert.equal((await readdir(root)).some((name) => name.endsWith(".tmp")), false);
});

test("journal failure after SQL success is an audit failure, not a rollback", async (t) => {
  const { input } = await setup(t);
  const execute = input.adapter.execute;
  input.adapter.execute = async (request) => {
    const result = await execute(request);
    await appendFile(input.journalPath, "{damaged}\n");
    return result;
  };
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_AUDIT_PERSISTENCE_FAILED",
    auditErrorCode: "DOSQL_JOURNAL_INVALID_JSON",
    lineNumber: 3,
    executionOutcome: "succeeded",
    retryable: false,
  });
  assert.ok((await readFile(input.journalPath, "utf8")).endsWith("{damaged}\n"));
  await assertAbsent(input.evidencePath);
});

test("final journal rejection preserves already published successful evidence", async (t) => {
  const { input, dispatchPath } = await setup(t);
  await assert.rejects(executeControlledDatabaseOperation({
    ...input, verifiedAt: "",
  }), {
    code: "DOSQL_AUDIT_PERSISTENCE_FAILED",
    auditErrorCode: "DOSQL_JOURNAL_INVALID_EVENT",
    executionOutcome: "succeeded",
    retryable: false,
  });
  const bytes = await readFile(input.evidencePath);
  const evidence = JSON.parse(bytes);
  assert.equal(evidence.status, "verified");
  assert.equal(evidence.executionOutcome, "succeeded");
  assert.equal(evidence.executionResult.transactionId, RESULT.transactionId);
  assert.deepEqual(await statuses(input), ["planned", "running", "succeeded"]);
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "DOSQL_EVIDENCE_ALREADY_EXISTS",
  });
  assert.deepEqual(await readFile(input.evidencePath), bytes);
  assert.equal((await readFile(dispatchPath, "utf8")).trim().split("\n").length, 1);
});

test("client failure records an unknown outcome without leaking its output into evidence", async (t) => {
  const { input, dispatchPath } = await setup(t);
  const execute = input.adapter.execute;
  input.adapter.execute = async (request) => {
    await execute(request);
    throw Object.assign(new Error("synthetic-private-driver-error"), { code: "CLIENT_DISCONNECTED" });
  };
  await assert.rejects(executeControlledDatabaseOperation(input), {
    code: "CLIENT_DISCONNECTED", executionOutcome: "unknown", retryable: false,
  });
  assert.deepEqual(await statuses(input), ["planned", "running", "failed"]);
  const bytes = await readFile(input.evidencePath, "utf8");
  assert.equal(JSON.parse(bytes).executionOutcome, "unknown");
  assert.ok(!bytes.includes("synthetic-private-driver-error"));
  assert.equal((await readFile(dispatchPath, "utf8")).trim().split("\n").length, 1);
});

test("policy rejection records not_started and never dispatches", async (t) => {
  const { input, dispatchPath } = await setup(t);
  await assert.rejects(executeControlledDatabaseOperation({
    ...input, statement: "update items set value = 1",
  }), { executionOutcome: "not_started", retryable: false });
  assert.deepEqual(await statuses(input), ["planned", "failed"]);
  assert.equal(JSON.parse(await readFile(input.evidencePath)).executionOutcome, "not_started");
  await assertAbsent(dispatchPath);
});
