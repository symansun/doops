import { createHash } from "node:crypto";
import { appendFile, chmod, mkdir, readFile } from "node:fs/promises";
import { dirname } from "node:path";

export async function appendJournalEvent({ journalPath, event }) {
  requireText(journalPath, "journalPath");
  const normalized = normalizeJournalEvent(event);
  await mkdir(dirname(journalPath), { recursive: true });
  const existing = await readExistingJournal(journalPath);
  verifyJournalChain(existing);
  const previousEventHash = existing.at(-1)?.eventHash ?? "";
  const chained = {
    ...normalized,
    previousEventHash,
  };
  chained.eventHash = `sha256:${sha256(stableJson(chained))}`;
  await appendFile(journalPath, `${JSON.stringify(chained)}\n`, {
    encoding: "utf8",
    mode: 0o600,
  });
  await chmod(journalPath, 0o600);
  return chained;
}

export async function readJournalEvents({ journalPath }) {
  requireText(journalPath, "journalPath");
  const events = await readExistingJournal(journalPath);
  verifyJournalChain(events);
  return events;
}

async function readExistingJournal(journalPath) {
  try {
    const content = await readFile(journalPath, "utf8");
    return content
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => JSON.parse(line));
  } catch (error) {
    if (error.code === "ENOENT") return [];
    throw error;
  }
}

function verifyJournalChain(events) {
  let previousEventHash = "";
  for (const event of events) {
    if (event.previousEventHash !== previousEventHash) {
      throw new Error("DoSql journal hash chain is broken");
    }
    const claimedHash = requireText(event.eventHash, "event.eventHash");
    const payload = { ...event };
    delete payload.eventHash;
    const expectedHash = `sha256:${sha256(stableJson(payload))}`;
    if (claimedHash !== expectedHash) {
      throw new Error("DoSql journal event hash is invalid");
    }
    previousEventHash = claimedHash;
  }
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
  requireText(event?.eventType, "event.eventType");
  requireText(event?.operationId, "event.operationId");
  requireText(event?.createdAt, "event.createdAt");
  return sortObject({
    ...event,
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
  return createHash("sha256").update(String(value)).digest("hex");
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
}
