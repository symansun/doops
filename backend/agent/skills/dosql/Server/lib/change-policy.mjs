import { createHash } from "node:crypto";

const SUPPORTED_ENGINES = new Set(["mysql", "postgres", "mongodb"]);
const READ_SQL_COMMANDS = new Set(["SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN"]);
const SCHEMA_SQL_COMMANDS = new Set([
  "ALTER",
  "COMMENT",
  "CREATE",
  "DROP",
  "RENAME",
  "TRUNCATE",
]);
const DATA_SQL_COMMANDS = new Set([
  "DELETE",
  "INSERT",
  "MERGE",
  "REPLACE",
  "UPDATE",
]);
const ADMIN_SQL_COMMANDS = new Set([
  "ANALYZE",
  "GRANT",
  "REINDEX",
  "REVOKE",
  "VACUUM",
]);

const MONGO_READ_PATTERNS = [
  /\.aggregate\s*\(/i,
  /\.countDocuments\s*\(/i,
  /\.distinct\s*\(/i,
  /\.estimatedDocumentCount\s*\(/i,
  /\.explain\s*\(/i,
  /\.find\s*\(/i,
  /\.findOne\s*\(/i,
  /\.getCollectionNames\s*\(/i,
];
const MONGO_DATA_PATTERNS = [
  /\.bulkWrite\s*\(/i,
  /\.deleteMany\s*\(/i,
  /\.deleteOne\s*\(/i,
  /\.insertMany\s*\(/i,
  /\.insertOne\s*\(/i,
  /\.replaceOne\s*\(/i,
  /\.updateMany\s*\(/i,
  /\.updateOne\s*\(/i,
];
const MONGO_SCHEMA_PATTERNS = [
  /\.collMod\s*\(/i,
  /\.createCollection\s*\(/i,
  /\.createIndex\s*\(/i,
  /\.createIndexes\s*\(/i,
  /\.drop\s*\(/i,
  /\.dropIndex\s*\(/i,
  /\.dropIndexes\s*\(/i,
  /\.renameCollection\s*\(/i,
];
const MONGO_ADMIN_PATTERNS = [
  /\.createUser\s*\(/i,
  /\.dropUser\s*\(/i,
  /\.grantRolesToUser\s*\(/i,
  /\.revokeRolesFromUser\s*\(/i,
];

export function classifyDatabaseOperation(input) {
  const engine = normalizeEngine(input.engine);
  const normalizedStatement = normalizeStatement(input.statement);
  const operationKind =
    engine === "mongodb"
      ? classifyMongoOperation(normalizedStatement)
      : classifySqlOperation(normalizedStatement);
  const changeLike = isChangeLike(operationKind, input.intent);
  const documentPreference = input.documentPreference ?? "auto";
  const documentDecision = decideChangeDocument({
    changeLike,
    documentPreference,
    intent: input.intent,
  });

  return {
    engine,
    operationKind,
    riskLevel: resolveRiskLevel(operationKind, input.environment),
    includeInChangeDocument: documentDecision.include,
    documentDecisionSource: documentDecision.source,
    approvalRequired: operationKind !== "read",
    reason: documentDecision.reason,
  };
}

export function createDatabaseOperationRecord(input) {
  const normalizedStatement = normalizeStatement(input.statement);
  const requestedAt = input.requestedAt ?? new Date().toISOString();
  const classification = classifyDatabaseOperation(input);
  const statementFingerprint = sha256(normalizedStatement);
  const operationId =
    input.operationId ??
    `dbop_${sha256(
      [
        input.projectId,
        input.databaseAssetId,
        input.environmentId,
        classification.engine,
        input.actor?.type,
        input.actor?.id,
        requestedAt,
        statementFingerprint,
      ].join("\u001f"),
    ).slice(0, 16)}`;

  requireNonEmpty(operationId, "operationId");
  requireNonEmpty(input.projectId, "projectId");
  requireNonEmpty(input.databaseAssetId, "databaseAssetId");
  requireNonEmpty(input.environmentId, "environmentId");
  requireNonEmpty(input.actor?.type, "actor.type");
  requireNonEmpty(input.actor?.id, "actor.id");

  return {
    operationId,
    projectId: input.projectId,
    databaseAssetId: input.databaseAssetId,
    environmentId: input.environmentId,
    engine: classification.engine,
    actor: {
      type: input.actor.type,
      id: input.actor.id,
    },
    requestedAt,
    statement: input.statement,
    normalizedStatement,
    statementFingerprint,
    operationKind: classification.operationKind,
    riskLevel: classification.riskLevel,
    classification,
    changeDocument: {
      include: classification.includeInChangeDocument,
      decisionSource: classification.documentDecisionSource,
      reason: classification.reason,
    },
    proxy: {
      mode: "dosql-agent-proxy",
      status: "planned",
    },
    audit: {
      captured: true,
      immutableFields: [
        "operationId",
        "projectId",
        "databaseAssetId",
        "environmentId",
        "engine",
        "actor",
        "requestedAt",
        "statementFingerprint",
      ],
    },
  };
}

export function normalizeStatement(statement) {
  requireNonEmpty(statement, "statement");
  return String(statement).trim().replace(/;+$/g, "").replace(/\s+/g, " ");
}

function classifySqlOperation(statement) {
  const firstCommand = firstSqlCommand(statement);

  if (firstCommand === "WITH") {
    return /\b(DELETE|INSERT|MERGE|REPLACE|UPDATE)\b/i.test(statement)
      ? "data_change"
      : "read";
  }
  if (READ_SQL_COMMANDS.has(firstCommand)) return "read";
  if (SCHEMA_SQL_COMMANDS.has(firstCommand)) return "schema_change";
  if (DATA_SQL_COMMANDS.has(firstCommand)) return "data_change";
  if (ADMIN_SQL_COMMANDS.has(firstCommand)) return "admin";
  return "unknown";
}

function classifyMongoOperation(statement) {
  if (MONGO_DATA_PATTERNS.some((pattern) => pattern.test(statement))) {
    return "data_change";
  }
  if (MONGO_SCHEMA_PATTERNS.some((pattern) => pattern.test(statement))) {
    return "schema_change";
  }
  if (MONGO_ADMIN_PATTERNS.some((pattern) => pattern.test(statement))) {
    return "admin";
  }
  if (MONGO_READ_PATTERNS.some((pattern) => pattern.test(statement))) {
    return "read";
  }
  return "unknown";
}

function decideChangeDocument({ changeLike, documentPreference, intent }) {
  if (changeLike && documentPreference === "exclude") {
    return {
      include: true,
      source: "safety_override",
      reason: "Mutating or administrative database operations must be part of the change document.",
    };
  }
  if (documentPreference === "include") {
    return {
      include: true,
      source: "actor_override",
      reason: "The actor explicitly requested change-document inclusion.",
    };
  }
  if (documentPreference === "exclude") {
    return {
      include: false,
      source: "actor_override",
      reason: "Read-only operation was explicitly kept out of the change document.",
    };
  }
  if (changeLike || ["migration", "repair", "rollback"].includes(intent)) {
    return {
      include: true,
      source: "policy",
      reason: "Database changes, migrations, repairs and rollbacks are documented.",
    };
  }
  return {
    include: false,
    source: "policy",
    reason: "Read-only inspection is audited but not added to the change document by default.",
  };
}

function firstSqlCommand(statement) {
  const withoutLeadingComments = stripLeadingSqlComments(statement);
  return withoutLeadingComments.match(/^[a-zA-Z]+/)?.[0]?.toUpperCase() ?? "UNKNOWN";
}

function stripLeadingSqlComments(statement) {
  let remaining = statement.trim();
  let changed = true;
  while (changed) {
    changed = false;
    const lineComment = remaining.match(/^--[^\n]*(\n|$)/);
    if (lineComment) {
      remaining = remaining.slice(lineComment[0].length).trim();
      changed = true;
      continue;
    }
    const blockComment = remaining.match(/^\/\*[\s\S]*?\*\//);
    if (blockComment) {
      remaining = remaining.slice(blockComment[0].length).trim();
      changed = true;
    }
  }
  return remaining;
}

function isChangeLike(operationKind, intent) {
  return (
    operationKind === "schema_change" ||
    operationKind === "data_change" ||
    operationKind === "admin" ||
    operationKind === "unknown" ||
    ["migration", "repair", "rollback"].includes(intent)
  );
}

function resolveRiskLevel(operationKind, environment) {
  if (operationKind === "read") return "low";
  if (operationKind === "data_change") {
    return environment === "prod" ? "high" : "medium";
  }
  return "high";
}

function normalizeEngine(engine) {
  const normalized = String(engine ?? "").toLowerCase();
  if (!SUPPORTED_ENGINES.has(normalized)) {
    throw new Error(`Unsupported database engine: ${engine}`);
  }
  return normalized;
}

function requireNonEmpty(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}
