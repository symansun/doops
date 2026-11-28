import { createHash, randomUUID } from "node:crypto";
import {
  O_APPEND,
  O_CREAT,
  O_EXCL,
  O_NOFOLLOW,
  O_RDONLY,
  O_WRONLY,
} from "node:constants";
import { link, lstat, mkdir, open, unlink } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { reserveOperationId } from "./audit-artifacts.mjs";

const JOURNAL_SCHEMA = "dosql.execution-journal.v1";

export async function appendJournalEvent({ journalPath, event, rejectDuplicateOperation = false }) {
  requireText(journalPath, "journalPath");
  const normalized = normalizeJournalEvent(event);
  await mkdir(dirname(journalPath), { recursive: true });
  const releaseLock = await acquireJournalLock(journalPath);
  try {
    const existing = await readExistingJournal(journalPath);
    rejectLegacy(existing);
    if (rejectDuplicateOperation &&
        existing.events.some((entry) => entry.operationId.trim() === normalized.operationId.trim())) {
      throw Object.assign(
        journalError("DOSQL_OPERATION_ALREADY_EXISTS", "operationId already exists in journal"),
        { executionOutcome: "unknown" },
      );
    }
    const chained = chainEvent(normalized, existing.headHash);
    const handle = await open(
      journalPath,
      O_WRONLY | O_APPEND | O_CREAT | O_NOFOLLOW,
      0o600,
    );
    try {
      const metadata = await handle.stat();
      if (!metadata.isFile()) {
        throw new Error("DoSql journal path must be a regular file");
      }
      const separator = existing.bytes.length && existing.bytes.at(-1) !== 10 ? "\n" : "";
      await handle.writeFile(`${separator}${JSON.stringify(chained)}\n`, "utf8");
      await handle.chmod(0o600);
      await handle.sync();
    } finally {
      await handle.close();
    }
    return chained;
  } finally {
    await releaseLock();
  }
}

async function acquireJournalLock(journalPath) {
  const lockPath = `${journalPath}.lock`;
  for (let attempt = 0; attempt < 500; attempt += 1) {
    try {
      const handle = await open(
        lockPath,
        O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW,
        0o600,
      );
      try {
        await handle.writeFile(`${process.pid}\n`, "utf8");
        await handle.sync();
      } catch (error) {
        await unlink(lockPath);
        throw error;
      } finally {
        await handle.close();
      }
      return async () => {
        await unlink(lockPath);
      };
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  }
  throw journalError("DOSQL_JOURNAL_LOCK_TIMEOUT", "DoSql journal lock timed out; inspect the lock manually");
}

export async function readJournalEvents({ journalPath }) {
  requireText(journalPath, "journalPath");
  const journal = await readExistingJournal(journalPath);
  rejectLegacy(journal);
  return journal.events;
}

export async function inspectJournal({ journalPath }) {
  requireText(journalPath, "journalPath");
  const { format, events, headHash } = await readExistingJournal(journalPath);
  return { format, eventCount: events.length, headHash };
}

export async function migrateLegacyJournal({
  sourceJournalPath, journalPath, operationId, actor, createdAt, artifactRoot,
}) {
  requireText(sourceJournalPath, "sourceJournalPath");
  requireText(journalPath, "journalPath");
  requireText(artifactRoot, "artifactRoot");
  if (resolve(sourceJournalPath) === resolve(journalPath)) {
    throw journalError("DOSQL_JOURNAL_SAME_PATH", "Migration source and target must differ");
  }
  const importEvent = normalizeJournalEvent({
    eventType: "journal.legacy_imported",
    operationId,
    actor: {
      type: requireText(actor?.type, "actor.type"),
      id: requireText(actor?.id, "actor.id"),
    },
    createdAt,
    historicalIntegrity: "unverified",
  });
  const releaseLock = await acquireJournalLock(sourceJournalPath);
  let temporaryPath;
  let releaseTargetLock;
  try {
    const source = await readExistingJournal(sourceJournalPath);
    if (source.format !== "legacy") {
      throw journalError("DOSQL_JOURNAL_UNSUPPORTED_FORMAT", "Migration requires a nonempty pure legacy journal");
    }
    await mkdir(dirname(journalPath), { recursive: true });
    releaseTargetLock = await acquireJournalLock(journalPath);
    try {
      await lstat(journalPath);
      throw journalError("DOSQL_JOURNAL_TARGET_EXISTS", "Migration target already exists");
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
    const sourceHash = `sha256:${sha256(source.bytes)}`;
    for (const historicalId of new Set(source.events.map((event) => event.operationId.trim()))) {
      await reserveOperationId({
        artifactRoot,
        operationId: historicalId,
        claim: {
          schema: "dosql.legacy-operation-claim.v1",
          sourceHash,
          historicalIntegrity: "unverified",
        },
      });
    }
    const imported = chainEvent({
      ...importEvent,
      sourceHash,
      sourceEventCount: source.events.length,
    }, "");
    const events = [imported];
    for (const event of source.events) {
      events.push(chainEvent(
        { ...normalizeJournalEvent(event), historicalIntegrity: "unverified" },
        events.at(-1).eventHash,
      ));
    }
    const candidate = `${journalPath}.${randomUUID()}.tmp`;
    const handle = await open(candidate, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600);
    temporaryPath = candidate;
    try {
      await handle.writeFile(events.map((event) => `${JSON.stringify(event)}\n`).join(""), "utf8");
      await handle.sync();
    } finally {
      await handle.close();
    }
    // Publish only the complete, synced journal; link never replaces an existing target.
    try {
      await link(temporaryPath, journalPath);
    } catch (error) {
      if (error.code === "EEXIST") {
        throw journalError("DOSQL_JOURNAL_TARGET_EXISTS", "Migration target already exists");
      }
      throw error;
    }
    const directory = await open(dirname(journalPath), O_RDONLY);
    try {
      await directory.sync();
    } finally {
      await directory.close();
    }
    return {
      sourceJournalPath,
      journalPath,
      sourceHash,
      sourceEventCount: source.events.length,
      eventCount: events.length,
      headHash: events.at(-1).eventHash,
      historicalIntegrity: "unverified",
    };
  } finally {
    try {
      if (temporaryPath) await unlink(temporaryPath);
    } finally {
      try {
        if (releaseTargetLock) await releaseTargetLock();
      } finally {
        await releaseLock();
      }
    }
  }
}

async function readExistingJournal(journalPath) {
  let handle;
  try {
    handle = await open(journalPath, O_RDONLY | O_NOFOLLOW);
    const metadata = await handle.stat();
    if (!metadata.isFile()) {
      throw new Error("DoSql journal path must be a regular file");
    }
    return parseJournal(await handle.readFile());
  } catch (error) {
    if (error.code === "ENOENT") return parseJournal(Buffer.alloc(0));
    throw error;
  } finally {
    await handle?.close();
  }
}

function parseJournal(bytes) {
  const events = [];
  let format = "empty";
  let previousEventHash = "";
  for (const [index, line] of bytes.toString("utf8").split("\n").entries()) {
    if (!line.trim()) continue;
    const lineNumber = index + 1;
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      throw journalError("DOSQL_JOURNAL_INVALID_JSON", "DoSql journal contains invalid JSON", lineNumber);
    }
    validateEvent(event, lineNumber);
    const hasHash = Object.hasOwn(event, "eventHash");
    const hasPreviousHash = Object.hasOwn(event, "previousEventHash");
    if (Object.hasOwn(event, "schema") && event.schema !== JOURNAL_SCHEMA) {
      throw journalError("DOSQL_JOURNAL_UNSUPPORTED_FORMAT", "DoSql journal schema is unsupported", lineNumber);
    }
    if (hasHash !== hasPreviousHash) {
      throw journalError("DOSQL_JOURNAL_PARTIAL_CHAIN", "DoSql journal event has partial hash fields", lineNumber);
    }
    if (!hasHash && Object.hasOwn(event, "schema")) {
      throw journalError("DOSQL_JOURNAL_UNSUPPORTED_FORMAT", "Versioned journal events require hash fields", lineNumber);
    }
    const eventFormat = hasHash ? "hash-chained" : "legacy";
    if (format !== "empty" && format !== eventFormat) {
      throw journalError("DOSQL_JOURNAL_MIXED_FORMAT", "DoSql journal mixes legacy and hash-chained events", lineNumber);
    }
    format = eventFormat;
    if (hasHash) {
      if (event.previousEventHash !== previousEventHash) {
        throw journalError("DOSQL_JOURNAL_CHAIN_BROKEN", "DoSql journal hash chain is broken", lineNumber);
      }
      const payload = { ...event };
      delete payload.eventHash;
      const expectedHash = `sha256:${sha256(stableJson(payload))}`;
      if (event.eventHash !== expectedHash) {
        throw journalError("DOSQL_JOURNAL_HASH_INVALID", "DoSql journal event hash is invalid", lineNumber);
      }
      previousEventHash = event.eventHash;
    }
    events.push(event);
  }
  return { bytes, events, format, headHash: previousEventHash };
}

function validateEvent(event, lineNumber) {
  if (!event || typeof event !== "object" || Array.isArray(event)) {
    throw journalError("DOSQL_JOURNAL_INVALID_EVENT", "DoSql journal event must be an object", lineNumber);
  }
  for (const field of ["eventType", "operationId", "createdAt"]) {
    if (typeof event[field] !== "string" || !event[field].trim()) {
      throw journalError("DOSQL_JOURNAL_INVALID_EVENT", `event.${field} is required and must be text`, lineNumber);
    }
  }
}

function rejectLegacy(journal) {
  if (journal.format === "legacy") {
    throw journalError("DOSQL_JOURNAL_MIGRATION_REQUIRED", "Legacy journal requires explicit migrate-journal to a new target before reading or appending");
  }
}

function chainEvent(event, previousEventHash) {
  const payload = { ...event, schema: JOURNAL_SCHEMA, previousEventHash };
  delete payload.eventHash;
  return { ...payload, eventHash: `sha256:${sha256(stableJson(payload))}` };
}

function journalError(code, message, lineNumber) {
  return Object.assign(new Error(lineNumber ? `${message} (line ${lineNumber})` : message), {
    code,
    ...(lineNumber === undefined ? {} : { lineNumber }),
  });
}

export function createSqlExecutionEvents(input) {
  const base = createBaseExecutionEvent(input);
  const events = [
    {
      ...base,
      eventType: "sql.execution.planned",
      status: "planned",
      createdAt: requireText(input.timestamps?.plannedAt, "timestamps.plannedAt"),
    },
    {
      ...base,
      eventType: "sql.execution.running",
      status: "running",
      createdAt: requireText(input.timestamps?.runningAt, "timestamps.runningAt"),
    },
  ];

  const executionStatus = input.execution?.status ?? "succeeded";
  if (executionStatus === "failed") {
    events.push({
      ...base,
      eventType: "sql.execution.failed",
      status: "failed",
      errorMessage: requireText(input.execution?.errorMessage, "execution.errorMessage"),
      evidenceRef: requireText(input.evidenceRef, "evidenceRef"),
      createdAt: requireText(input.timestamps?.finishedAt, "timestamps.finishedAt"),
    });
    return events.map(normalizeJournalEvent);
  }

  events.push({
    ...base,
    eventType: "sql.execution.succeeded",
    status: "succeeded",
    affectedRows: Number(input.execution?.affectedRows ?? 0),
    evidenceRef: requireText(input.evidenceRef, "evidenceRef"),
    createdAt: requireText(input.timestamps?.finishedAt, "timestamps.finishedAt"),
  });

  if (input.verification) {
    events.push({
      ...base,
      eventType: verificationEventType(input.verification.status),
      status: input.verification.status,
      evidenceRef: requireText(input.evidenceRef, "evidenceRef"),
      verification: {
        status: input.verification.status,
        querySummary: input.verification.querySummary ?? "",
      },
      createdAt: requireText(input.timestamps?.verifiedAt, "timestamps.verifiedAt"),
    });
  }

  return events.map(normalizeJournalEvent);
}

export function replayEnvironmentTimeline(events) {
  const timeline = {
    environments: {},
    changeRequests: {},
    operations: {},
  };

  for (const event of events) {
    if (event.operationId) {
      timeline.operations[event.operationId] = reduceOperation(
        timeline.operations[event.operationId],
        event,
      );
    }
    if (!event.environmentId || !event.changeRequestId) continue;
    const current = timeline.environments[event.environmentId] ?? {
      status: "unknown",
      currentVersion: event.version?.from,
      currentLabel: formatVersionLabel(event.version?.from),
      lastOperationId: "",
      lastEventType: "",
      evidenceRef: "",
    };
    const next = reduceEnvironment(current, event);
    timeline.environments[event.environmentId] = next;

    timeline.changeRequests[event.changeRequestId] = {
      changeRequestId: event.changeRequestId,
      status: next.status,
      lastOperationId: event.operationId,
      lastEventType: event.eventType,
      environments: {
        ...(timeline.changeRequests[event.changeRequestId]?.environments ?? {}),
        [event.environmentId]: next.status,
      },
    };
  }

  return timeline;
}

function reduceOperation(current = {}, event) {
  return {
    operationId: event.operationId,
    status: event.status ?? current.status ?? "unknown",
    projectId: event.projectId ?? current.projectId ?? "",
    environmentId: event.environmentId ?? current.environmentId ?? "",
    databaseAssetId: event.databaseAssetId ?? current.databaseAssetId ?? "",
    engine: event.engine ?? current.engine ?? "",
    operationKind: event.operationKind ?? current.operationKind ?? "",
    changeRequestId: event.changeRequestId ?? current.changeRequestId ?? "",
    lastEventType: event.eventType,
    evidenceRef: event.evidenceRef ?? current.evidenceRef ?? "",
    errorMessage: event.errorMessage ?? current.errorMessage ?? "",
    ...((event.executionOutcome ?? current.executionOutcome) ? {
      executionOutcome: event.executionOutcome ?? current.executionOutcome,
    } : {}),
    ...((event.historicalIntegrity ?? current.historicalIntegrity) ? {
      historicalIntegrity: event.historicalIntegrity ?? current.historicalIntegrity,
    } : {}),
    updatedAt: event.createdAt,
  };
}

function createBaseExecutionEvent(input) {
  const statementText = requireText(input.statementText, "statementText");
  const statementFingerprint = sha256(normalizeStatement(statementText));
  return {
    operationId: requireText(input.operationId, "operationId"),
    changeRequestId: requireText(input.changeRequestId, "changeRequestId"),
    projectId: requireText(input.projectId, "projectId"),
    environmentId: requireText(input.environmentId, "environmentId"),
    databaseAssetId: requireText(input.databaseAssetId, "databaseAssetId"),
    engine: requireText(input.engine, "engine"),
    actor: {
      type: requireText(input.actor?.type, "actor.type"),
      id: requireText(input.actor?.id, "actor.id"),
    },
    statementKind: requireText(input.statementKind, "statementKind"),
    statementFingerprint,
    statementTextRef: `inline:sha256:${statementFingerprint}`,
    version: {
      from: Number(input.version?.from),
      to: Number(input.version?.to),
      label: requireText(input.version?.label, "version.label"),
    },
    ...(input.timeline ? { timeline: normalizeTimelineEvidence(input.timeline) } : {}),
  };
}

function reduceEnvironment(current, event) {
  const next = {
    ...current,
    status: event.status,
    lastOperationId: event.operationId,
    lastEventType: event.eventType,
    evidenceRef: event.evidenceRef ?? current.evidenceRef ?? "",
    ...((event.historicalIntegrity ?? current.historicalIntegrity) ? {
      historicalIntegrity: event.historicalIntegrity ?? current.historicalIntegrity,
    } : {}),
  };

  if (event.eventType === "sql.execution.verified") {
    next.currentVersion = event.version.to;
    next.currentLabel = event.version.label;
  }

  if (event.eventType === "sql.execution.failed") {
    next.currentVersion = event.version.from;
    next.currentLabel = formatVersionLabel(event.version.from);
  }

  return next;
}

function verificationEventType(status) {
  if (status === "verified") return "sql.execution.verified";
  if (status === "verification_failed") return "sql.execution.verification_failed";
  throw new Error(`Unsupported verification status: ${status}`);
}

function normalizeJournalEvent(event) {
  validateEvent(event);
  if (Object.hasOwn(event, "schema") && event.schema !== JOURNAL_SCHEMA) {
    throw journalError("DOSQL_JOURNAL_UNSUPPORTED_FORMAT", "DoSql journal schema is unsupported");
  }
  return sortObject({
    ...event,
    schema: JOURNAL_SCHEMA,
    journalEventId: event.journalEventId ?? `jevt_${sha256(stableJson(event)).slice(0, 16)}`,
  });
}

function normalizeStatement(statement) {
  return String(statement).trim().replace(/;+$/g, "").replace(/\s+/g, " ");
}

function formatVersionLabel(version) {
  if (version === undefined || Number.isNaN(Number(version))) return "";
  return `dosql_${String(Number(version)).padStart(6, "0")}`;
}

function normalizeTimelineEvidence(timeline) {
  return {
    baselineBeforeRef: requireText(timeline.baselineBeforeRef, "timeline.baselineBeforeRef"),
    baselineAfterRef: requireText(timeline.baselineAfterRef, "timeline.baselineAfterRef"),
    schemaFingerprint: requireText(timeline.schemaFingerprint, "timeline.schemaFingerprint"),
    dataCheckpointRef: timeline.dataCheckpointRef ? String(timeline.dataCheckpointRef).trim() : "",
    restoreCapability: requireText(timeline.restoreCapability, "timeline.restoreCapability"),
  };
}

function stableJson(value) {
  return JSON.stringify(sortObject(value));
}

function sortObject(value) {
  if (Array.isArray(value)) return value.map(sortObject);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, entry]) => [key, sortObject(entry)]),
  );
}

function sha256(value) {
  return createHash("sha256").update(Buffer.isBuffer(value) ? value : String(value)).digest("hex");
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
}
