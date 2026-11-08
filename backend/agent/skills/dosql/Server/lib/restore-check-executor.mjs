import { createHash } from "node:crypto";

export async function executeRestoreChecks(input) {
  const restorePlan = requireRestorePlan(input.restorePlan);
  const adapter = requireObject(input.adapter, "adapter");
  if (typeof adapter.executeScalarCheck !== "function") {
    throw new Error("adapter.executeScalarCheck is required");
  }
  const checks = requireArray(input.checks, "checks");
  if (checks.length === 0) {
    throw new Error("checks must include at least one restore check");
  }
  const executionResults = [];
  for (const check of checks) {
    const checkName = requireText(check.checkName, "check.checkName");
    const expected = requireText(check.expected, "check.expected");
    if (checkName === "schema_fingerprint") {
      const targetFingerprint = requireText(
        restorePlan.targetNode?.schemaFingerprint,
        "restorePlan.targetNode.schemaFingerprint",
      );
      if (expected !== targetFingerprint) {
        throw new Error("schema_fingerprint expected value must match restore plan target");
      }
    }
    const sqlText = requireText(check.sqlText, "check.sqlText");
    const result = await adapter.executeScalarCheck({ checkName, sqlText });
    executionResults.push({
      ...result,
      checkName,
      expected,
    });
  }
  return createRestoreCheckExecutionArtifact({
    restorePlan,
    executionResults,
    executedBy: input.executedBy,
    executedAt: input.executedAt,
    connectionRef: input.connectionRef,
  });
}

export function createRestoreCheckExecutionArtifact(input) {
  const restorePlan = requireRestorePlan(input.restorePlan);
  const executionResults = requireArray(input.executionResults, "executionResults");
  if (executionResults.length === 0) {
    throw new Error("executionResults must include at least one restore check result");
  }
  const checks = executionResults.map((result) => {
    const checkName = requireText(result.checkName, "result.checkName");
    const expected = requireText(result.expected, "result.expected");
    const actual = requireText(result.actual, "result.actual");
    if (result.status !== "succeeded" || actual !== expected) {
      throw new Error(`restore check must pass: ${checkName}`);
    }
    return sortObject({
      checkName,
      checkStatus: "passed",
      expected,
      actual,
      transactionId: requireText(result.transactionId, "result.transactionId"),
      statementCount: requireInteger(result.statementCount, "result.statementCount"),
    });
  });
  const executedBy = requireText(input.executedBy, "executedBy");
  const executedAt = requireIsoDate(input.executedAt, "executedAt");
  const targetNode = requireObject(restorePlan.targetNode, "restorePlan.targetNode");
  const base = sortObject({
    schema: "dosql.restore-check-execution.v1",
    status: "verified",
    restoreCheckExecutionId: `rchk_${sha256(
      `${restorePlan.restorePlanId}\u001f${executedAt}\u001f${stableJson(checks)}`,
    ).slice(0, 16)}`,
    restorePlanId: requireText(restorePlan.restorePlanId, "restorePlan.restorePlanId"),
    changeRequestId: requireText(restorePlan.changeRequestId, "restorePlan.changeRequestId"),
    databaseAssetId: requireText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId"),
    targetTimelineNodeId: requireText(
      targetNode.timelineNodeId,
      "restorePlan.targetNode.timelineNodeId",
    ),
    targetSchemaFingerprint: requireText(
      targetNode.schemaFingerprint,
      "restorePlan.targetNode.schemaFingerprint",
    ),
    checkCount: checks.length,
    checks,
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executedBy,
    executedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function requireRestorePlan(value) {
  const restorePlan = requireObject(value, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  if (restorePlan.status === "blocked") {
    throw new Error("blocked restore plan cannot execute restore checks");
  }
  return restorePlan;
}

function requireObject(value, fieldName) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${fieldName} is required`);
  }
  return value;
}

function requireArray(value, fieldName) {
  if (!Array.isArray(value)) {
    throw new Error(`${fieldName} must be an array`);
  }
  return value;
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
}

function requireIsoDate(value, fieldName) {
  const text = requireText(value, fieldName);
  if (Number.isNaN(Date.parse(text))) {
    throw new Error(`${fieldName} must be an ISO timestamp`);
  }
  return text;
}

function requireInteger(value, fieldName) {
  const number = Number(value);
  if (!Number.isInteger(number)) {
    throw new Error(`${fieldName} must be an integer`);
  }
  return number;
}

function sha256(value) {
  return createHash("sha256").update(String(value)).digest("hex");
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
