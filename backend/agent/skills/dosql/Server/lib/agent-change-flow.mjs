import { createHash } from "node:crypto";

import { createDatabaseOperationRecord } from "./change-policy.mjs";

const SUPPORTED_ENGINES = new Set(["mysql", "postgres", "mongodb"]);

export function connectTestDatabase(input) {
  const engine = normalizeEngine(input.engine);
  return {
    kind: "test_database_fixture",
    connected: true,
    projectId: requireText(input.projectId, "projectId"),
    environmentId: requireText(input.environmentId, "environmentId"),
    databaseAssetId: requireText(input.databaseAssetId, "databaseAssetId"),
    engine,
    schemaVersion: Number(input.schemaVersion ?? 0),
    tables: structuredClone(input.tables ?? {}),
    changeRecords: [],
    conversationLog: [],
  };
}

export function proposeAgentSchemaChange(input) {
  assertConnectedDatabase(input.database);
  const analysis = normalizeAgentAnalysis(input.agentAnalysis);
  const version = nextVersion(input.database);
  const internalScript = renderMigrationScript(input.database.engine, analysis);
  const requestedAt = input.requestedAt ?? new Date().toISOString();
  const operationRecord = createDatabaseOperationRecord({
    projectId: input.database.projectId,
    databaseAssetId: input.database.databaseAssetId,
    environmentId: input.database.environmentId,
    engine: input.database.engine,
    actor: input.actor,
    statement: internalScript,
    requestedAt,
    intent: "migration",
  });
  const userConfirmation = buildHumanReadableConfirmation({
    database: input.database,
    analysis,
    version,
    userMessage: input.userMessage,
  });

  input.database.conversationLog.push({
    role: "user",
    content: input.userMessage,
    at: requestedAt,
  });
  input.database.conversationLog.push({
    role: "agent",
    content: userConfirmation.confirmationText,
    at: requestedAt,
  });

  return {
    proposalId: `chgprop_${fingerprint(
      `${input.database.databaseAssetId}:${version.label}:${operationRecord.operationId}`,
    ).slice(0, 16)}`,
    status: "awaiting_user_confirmation",
    version,
    analysis,
    userMessage: input.userMessage,
    userConfirmation,
    internalScript,
    operationRecord,
  };
}

export function applyConfirmedChangeProposal({ database, proposal, confirmation }) {
  assertConnectedDatabase(database);
  if (!confirmation?.accepted || confirmation.format !== "human_readable") {
    throw new Error("human-readable confirmation is required before applying a database change");
  }
  if (proposal.status !== "awaiting_user_confirmation") {
    throw new Error(`Proposal is not awaiting confirmation: ${proposal.status}`);
  }
  if (proposal.version.from !== database.schemaVersion) {
    throw new Error(
      `Database version moved from ${proposal.version.from} to ${database.schemaVersion}; regenerate the proposal`,
    );
  }

  applyToTestSchema(database, proposal.analysis);
  database.schemaVersion = proposal.version.to;

  const changeRecord = {
    changeRecordId: `sqlchg_${fingerprint(
      `${proposal.operationRecord.operationId}:${confirmation.approvedAt ?? ""}`,
    ).slice(0, 16)}`,
    status: "succeeded",
    approvedBy: confirmation.approvedBy,
    approvedAt: confirmation.approvedAt ?? new Date().toISOString(),
    version: proposal.version,
    operationId: proposal.operationRecord.operationId,
    operationRecord: {
      ...proposal.operationRecord,
      proxy: {
        ...proposal.operationRecord.proxy,
        status: "succeeded",
      },
    },
    humanSummary: proposal.userConfirmation.summary,
    internalScript: proposal.internalScript,
    scriptFingerprint: fingerprint(proposal.internalScript),
  };

  database.changeRecords.push(changeRecord);

  return {
    status: "succeeded",
    databaseVersion: database.schemaVersion,
    changeRecord,
  };
}

function nextVersion(database) {
  const from = database.schemaVersion;
  const to = from + 1;
  return {
    from,
    to,
    label: `dosql_${String(to).padStart(6, "0")}`,
  };
}

function normalizeAgentAnalysis(analysis) {
  if (!analysis || typeof analysis !== "object") {
    throw new Error("agentAnalysis is required");
  }
  if (analysis.action !== "add_column") {
    throw new Error(`Unsupported agent analysis action: ${analysis.action}`);
  }
  return {
    action: "add_column",
    table: requireIdentifier(analysis.table, "table"),
    column: requireIdentifier(analysis.column, "column"),
    dataType: requireText(analysis.dataType, "dataType"),
    nullable: analysis.nullable !== false,
    defaultValue: analysis.defaultValue,
    businessReason: requireText(analysis.businessReason, "businessReason"),
  };
}

function renderMigrationScript(engine, analysis) {
  if (engine === "mongodb") {
    throw new Error("MongoDB schema migration rendering is not implemented in this test fixture");
  }
  const nullableClause = analysis.nullable ? "" : " not null";
  const defaultClause =
    analysis.defaultValue === undefined ? "" : ` default ${renderSqlLiteral(analysis.defaultValue)}`;
  return `alter table ${analysis.table} add column ${analysis.column} ${analysis.dataType}${nullableClause}${defaultClause};`;
}

function buildHumanReadableConfirmation({ database, analysis, version }) {
  const tableLabel = displayTable(analysis.table);
  const nullableText = analysis.nullable ? "允许为空，便于平滑上线" : "不能为空";
  const defaultText =
    analysis.defaultValue === undefined ? "" : `，默认值为「${analysis.defaultValue}」`;
  const title = `确认变更：为${tableLabel}增加「${analysis.column}」字段`;
  const summary = [
    `将在 ${database.projectId} 的 ${database.environmentId} 环境中修改 ${tableLabel}。`,
    `新增字段「${analysis.column}」，类型为 ${analysis.dataType}，${nullableText}${defaultText}。`,
    `业务原因：${analysis.businessReason}`,
  ].join("\n");
  const confirmationText = [
    title,
    "",
    summary,
    "",
    `版本变化：${version.from} -> ${version.to}（${version.label}）。`,
    "执行前会记录变更单、脚本指纹和执行证据。",
    "请确认你理解这次变更的业务含义和影响范围。",
  ].join("\n");

  return {
    format: "human_readable",
    title,
    summary,
    versionLabel: version.label,
    target: {
      projectId: database.projectId,
      environmentId: database.environmentId,
      databaseAssetId: database.databaseAssetId,
      engine: database.engine,
    },
    changes: [
      {
        kind: "add_column",
        table: analysis.table,
        column: analysis.column,
        description: `为${tableLabel}增加「${analysis.column}」字段`,
      },
    ],
    risks: [
      "需要确认应用代码是否已经兼容新增字段。",
      "生产环境执行前需要先在较低环境验证。",
    ],
    verification: [
      `检查 ${tableLabel} 是否存在「${analysis.column}」字段。`,
      "确认相关查询和写入流程正常。",
    ],
    confirmationText,
  };
}

function applyToTestSchema(database, analysis) {
  database.tables[analysis.table] ??= { columns: {} };
  const table = database.tables[analysis.table];
  if (table.columns[analysis.column]) {
    throw new Error(`Column already exists: ${analysis.table}.${analysis.column}`);
  }
  table.columns[analysis.column] = {
    dataType: analysis.dataType,
    nullable: analysis.nullable,
    defaultValue: analysis.defaultValue,
  };
}

function displayTable(table) {
  const labels = {
    orders: "订单表",
    users: "用户表",
    accounts: "账户表",
  };
  return labels[table] ?? `「${table}」表`;
}

function renderSqlLiteral(value) {
  if (typeof value === "number") return String(value);
  if (typeof value === "boolean") return value ? "true" : "false";
  return `'${String(value).replaceAll("'", "''")}'`;
}

function normalizeEngine(engine) {
  const normalized = String(engine ?? "").toLowerCase();
  if (!SUPPORTED_ENGINES.has(normalized)) {
    throw new Error(`Unsupported database engine: ${engine}`);
  }
  return normalized;
}

function assertConnectedDatabase(database) {
  if (!database?.connected) {
    throw new Error("A connected test database is required");
  }
}

function requireIdentifier(value, fieldName) {
  const text = requireText(value, fieldName);
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(text)) {
    throw new Error(`${fieldName} must be a simple database identifier`);
  }
  return text;
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
}

function fingerprint(value) {
  return createHash("sha256").update(value).digest("hex");
}
