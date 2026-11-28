import { createHash } from "node:crypto";

export async function executeChangeMetadataCommit(input) {
  return executeMetadataCommit(input);
}

export async function executeMetadataCommit(input) {
  const commit = requireMetadataCommit(input.commit);
  const adapter = requireObject(input.adapter, "adapter");
  if (typeof adapter.executeSqlTransaction !== "function") {
    throw new Error("adapter.executeSqlTransaction is required");
  }
  const executionResult = await adapter.executeSqlTransaction({
    sqlText: commit.sqlText,
    sourceCommitFingerprint: commit.artifactFingerprint,
    schema: commit.schema,
  });
  return createMetadataCommitExecutionArtifact({
    commit,
    executionResult,
    executedBy: input.executedBy,
    executedAt: input.executedAt,
    connectionRef: input.connectionRef,
  });
}

export async function executeTimepointStateQuery(input) {
  const queryArtifact = requireTimepointStateQueryArtifact(input.queryArtifact);
  const adapter = requireObject(input.adapter, "adapter");
  if (typeof adapter.executeTimepointStateQuery !== "function") {
    throw new Error("adapter.executeTimepointStateQuery is required");
  }
  const queryResult = await adapter.executeTimepointStateQuery({
    sqlText: queryArtifact.sqlText,
    sourceQueryFingerprint: queryArtifact.artifactFingerprint,
  });
  return createTimepointStateQueryResultArtifact({
    queryArtifact,
    queryResult,
    queriedBy: input.queriedBy,
    queriedAt: input.queriedAt,
    connectionRef: input.connectionRef,
  });
}

export function createTimepointStateQueryResultArtifact(input) {
  const queryArtifact = requireTimepointStateQueryArtifact(input.queryArtifact);
  const queryResult = normalizeTimepointStateQueryResult(input.queryResult);
  const queriedBy = requireText(input.queriedBy, "queriedBy");
  const queriedAt = requireIsoDate(input.queriedAt, "queriedAt");
  const databaseAssetId = requireText(
    queryArtifact.databaseAssetId,
    "queryArtifact.databaseAssetId",
  );
  const timestamp = requireIsoDate(queryArtifact.timestamp, "queryArtifact.timestamp");
  const timepointState = normalizeTimepointState({
    value: queryResult.timepointState,
    databaseAssetId,
    timestamp,
  });
  const base = sortObject({
    schema: "dosql.timepoint-state-query-result.v1",
    status: "resolved",
    databaseAssetId,
    timestamp,
    sourceQueryFingerprint: requireText(
      queryArtifact.artifactFingerprint,
      "queryArtifact.artifactFingerprint",
    ),
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executionResult: queryResult.executionResult,
    timepointState,
    queriedBy,
    queriedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function createMetadataCommitExecutionArtifact(input) {
  const commit = requireMetadataCommit(input.commit);
  if (commit.schema === "dosql.timeline-artifacts-metadata-commit.v1") {
    return createTimelineArtifactsMetadataCommitExecutionArtifact({
      ...input,
      commit,
    });
  }
  if (commit.schema === "dosql.restore-plan-metadata-commit.v1") {
    return createRestorePlanMetadataCommitExecutionArtifact({
      ...input,
      commit,
    });
  }
  if (commit.schema === "dosql.forward-evidence-metadata-commit.v1") {
    return createForwardEvidenceMetadataCommitExecutionArtifact({
      ...input,
      commit,
    });
  }
  if (commit.schema === "dosql.restore-evidence-metadata-commit.v1") {
    return createRestoreEvidenceMetadataCommitExecutionArtifact({
      ...input,
      commit,
    });
  }
  return createChangeMetadataCommitExecutionArtifact({
    ...input,
    commit,
  });
}

function createChangeMetadataCommitExecutionArtifact(input) {
  const commit = requireChangeMetadataCommit(input.commit);
  const executionResult = normalizeExecutionResult({
    executionResult: input.executionResult,
    expectedBaselineRecordCount: commit.recordCount,
  });
  const executedBy = requireText(input.executedBy, "executedBy");
  const executedAt = requireIsoDate(input.executedAt, "executedAt");
  const base = sortObject({
    schema: "dosql.metadata-commit-execution.v1",
    status: "verified",
    databaseAssetId: requireText(commit.databaseAssetId, "commit.databaseAssetId"),
    timelineNodeId: requireText(commit.timelineNodeId, "commit.timelineNodeId"),
    sourceCommitFingerprint: requireText(commit.artifactFingerprint, "commit.artifactFingerprint"),
    sourceProjectionFingerprint: requireText(
      commit.sourceProjectionFingerprint,
      "commit.sourceProjectionFingerprint",
    ),
    sourceRecordSetFingerprint: requireText(
      commit.sourceRecordSetFingerprint,
      "commit.sourceRecordSetFingerprint",
    ),
    recordCount: requireInteger(commit.recordCount, "commit.recordCount"),
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executionResult,
    executedBy,
    executedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function createTimelineArtifactsMetadataCommitExecutionArtifact(input) {
  const commit = requireTimelineArtifactsMetadataCommit(input.commit);
  const executionResult = normalizeTimelineArtifactsExecutionResult({
    executionResult: input.executionResult,
    expectedRecordCount: commit.recordCount,
  });
  const executedBy = requireText(input.executedBy, "executedBy");
  const executedAt = requireIsoDate(input.executedAt, "executedAt");
  const base = sortObject({
    schema: "dosql.timeline-artifacts-metadata-commit-execution.v1",
    status: "verified",
    databaseAssetId: requireText(commit.databaseAssetId, "commit.databaseAssetId"),
    sourceCommitFingerprint: requireText(commit.artifactFingerprint, "commit.artifactFingerprint"),
    sourceArtifactManifestFingerprint: requireText(
      commit.sourceArtifactManifestFingerprint,
      "commit.sourceArtifactManifestFingerprint",
    ),
    recordCount: requireInteger(commit.recordCount, "commit.recordCount"),
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executionResult,
    executedBy,
    executedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function createRestorePlanMetadataCommitExecutionArtifact(input) {
  const commit = requireRestorePlanMetadataCommit(input.commit);
  const executionResult = normalizeRestorePlanExecutionResult(input.executionResult);
  const executedBy = requireText(input.executedBy, "executedBy");
  const executedAt = requireIsoDate(input.executedAt, "executedAt");
  const base = sortObject({
    schema: "dosql.restore-plan-metadata-commit-execution.v1",
    status: "verified",
    databaseAssetId: requireText(commit.databaseAssetId, "commit.databaseAssetId"),
    restorePlanId: requireText(commit.restorePlanId, "commit.restorePlanId"),
    changeRequestId: requireText(commit.changeRequestId, "commit.changeRequestId"),
    sourceCommitFingerprint: requireText(commit.artifactFingerprint, "commit.artifactFingerprint"),
    sourceRestorePlanFingerprint: requireText(
      commit.sourceRestorePlanFingerprint,
      "commit.sourceRestorePlanFingerprint",
    ),
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executionResult,
    executedBy,
    executedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function createForwardEvidenceMetadataCommitExecutionArtifact(input) {
  const commit = requireForwardEvidenceMetadataCommit(input.commit);
  const executionResult = normalizeForwardEvidenceExecutionResult(input.executionResult);
  const executedBy = requireText(input.executedBy, "executedBy");
  const executedAt = requireIsoDate(input.executedAt, "executedAt");
  const base = sortObject({
    schema: "dosql.forward-evidence-metadata-commit-execution.v1",
    status: "verified",
    databaseAssetId: requireText(commit.databaseAssetId, "commit.databaseAssetId"),
    changeRequestId: requireText(commit.changeRequestId, "commit.changeRequestId"),
    timelineNodeId: requireText(commit.timelineNodeId, "commit.timelineNodeId"),
    forwardExecutionId: requireText(commit.forwardExecutionId, "commit.forwardExecutionId"),
    sourceCommitFingerprint: requireText(commit.artifactFingerprint, "commit.artifactFingerprint"),
    sourceForwardExecutionFingerprint: requireText(
      commit.sourceForwardExecutionFingerprint,
      "commit.sourceForwardExecutionFingerprint",
    ),
    sourceChangePlanFingerprint: requireText(
      commit.sourceChangePlanFingerprint,
      "commit.sourceChangePlanFingerprint",
    ),
    sourceArtifactManifestFingerprint: requireText(
      commit.sourceArtifactManifestFingerprint,
      "commit.sourceArtifactManifestFingerprint",
    ),
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executionResult,
    executedBy,
    executedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function createRestoreEvidenceMetadataCommitExecutionArtifact(input) {
  const commit = requireRestoreEvidenceMetadataCommit(input.commit);
  const executionResult = normalizeRestoreEvidenceExecutionResult(input.executionResult);
  const executedBy = requireText(input.executedBy, "executedBy");
  const executedAt = requireIsoDate(input.executedAt, "executedAt");
  const base = sortObject({
    schema: "dosql.restore-evidence-metadata-commit-execution.v1",
    status: "verified",
    databaseAssetId: requireText(commit.databaseAssetId, "commit.databaseAssetId"),
    restorePlanId: requireText(commit.restorePlanId, "commit.restorePlanId"),
    changeRequestId: requireText(commit.changeRequestId, "commit.changeRequestId"),
    rollbackExecutionId: requireText(commit.rollbackExecutionId, "commit.rollbackExecutionId"),
    restoreCheckExecutionId: requireText(
      commit.restoreCheckExecutionId,
      "commit.restoreCheckExecutionId",
    ),
    restoreVerificationId: requireText(
      commit.restoreVerificationId,
      "commit.restoreVerificationId",
    ),
    sourceCommitFingerprint: requireText(commit.artifactFingerprint, "commit.artifactFingerprint"),
    sourceRollbackExecutionFingerprint: requireText(
      commit.sourceRollbackExecutionFingerprint,
      "commit.sourceRollbackExecutionFingerprint",
    ),
    sourceRestoreCheckExecutionFingerprint: requireText(
      commit.sourceRestoreCheckExecutionFingerprint,
      "commit.sourceRestoreCheckExecutionFingerprint",
    ),
    sourceRestoreVerificationFingerprint: requireText(
      commit.sourceRestoreVerificationFingerprint,
      "commit.sourceRestoreVerificationFingerprint",
    ),
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executionResult,
    executedBy,
    executedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function requireMetadataCommit(value) {
  const commit = requireObject(value, "commit");
  if (
    commit.schema !== "dosql.timeline-artifacts-metadata-commit.v1" &&
    commit.schema !== "dosql.restore-plan-metadata-commit.v1" &&
    commit.schema !== "dosql.change-metadata-commit.v1" &&
    commit.schema !== "dosql.forward-evidence-metadata-commit.v1" &&
    commit.schema !== "dosql.restore-evidence-metadata-commit.v1"
  ) {
    throw new Error(`Unsupported metadata commit schema: ${commit.schema}`);
  }
  if (commit.status !== "ready") {
    throw new Error("commit status must be ready");
  }
  requireText(commit.sqlText, "commit.sqlText");
  requireText(commit.artifactFingerprint, "commit.artifactFingerprint");
  return commit;
}

function requireChangeMetadataCommit(value) {
  const commit = requireMetadataCommit(value);
  if (commit.schema !== "dosql.change-metadata-commit.v1") {
    throw new Error(`Unsupported metadata commit schema: ${commit.schema}`);
  }
  requireInteger(commit.recordCount, "commit.recordCount");
  return commit;
}

function requireTimelineArtifactsMetadataCommit(value) {
  const commit = requireMetadataCommit(value);
  if (commit.schema !== "dosql.timeline-artifacts-metadata-commit.v1") {
    throw new Error(`Unsupported metadata commit schema: ${commit.schema}`);
  }
  requireText(commit.databaseAssetId, "commit.databaseAssetId");
  requireInteger(commit.recordCount, "commit.recordCount");
  requireText(
    commit.sourceArtifactManifestFingerprint,
    "commit.sourceArtifactManifestFingerprint",
  );
  return commit;
}

function requireRestorePlanMetadataCommit(value) {
  const commit = requireMetadataCommit(value);
  if (commit.schema !== "dosql.restore-plan-metadata-commit.v1") {
    throw new Error(`Unsupported metadata commit schema: ${commit.schema}`);
  }
  requireText(commit.restorePlanId, "commit.restorePlanId");
  requireText(commit.changeRequestId, "commit.changeRequestId");
  requireText(commit.databaseAssetId, "commit.databaseAssetId");
  requireText(
    commit.sourceRestorePlanFingerprint,
    "commit.sourceRestorePlanFingerprint",
  );
  return commit;
}

function requireForwardEvidenceMetadataCommit(value) {
  const commit = requireMetadataCommit(value);
  if (commit.schema !== "dosql.forward-evidence-metadata-commit.v1") {
    throw new Error(`Unsupported metadata commit schema: ${commit.schema}`);
  }
  requireText(commit.forwardExecutionId, "commit.forwardExecutionId");
  requireText(commit.changeRequestId, "commit.changeRequestId");
  requireText(commit.databaseAssetId, "commit.databaseAssetId");
  requireText(commit.timelineNodeId, "commit.timelineNodeId");
  requireText(
    commit.sourceForwardExecutionFingerprint,
    "commit.sourceForwardExecutionFingerprint",
  );
  requireText(commit.sourceChangePlanFingerprint, "commit.sourceChangePlanFingerprint");
  requireText(
    commit.sourceArtifactManifestFingerprint,
    "commit.sourceArtifactManifestFingerprint",
  );
  return commit;
}

function requireRestoreEvidenceMetadataCommit(value) {
  const commit = requireMetadataCommit(value);
  if (commit.schema !== "dosql.restore-evidence-metadata-commit.v1") {
    throw new Error(`Unsupported metadata commit schema: ${commit.schema}`);
  }
  requireText(commit.restorePlanId, "commit.restorePlanId");
  requireText(commit.changeRequestId, "commit.changeRequestId");
  requireText(commit.databaseAssetId, "commit.databaseAssetId");
  requireText(commit.rollbackExecutionId, "commit.rollbackExecutionId");
  requireText(commit.restoreCheckExecutionId, "commit.restoreCheckExecutionId");
  requireText(commit.restoreVerificationId, "commit.restoreVerificationId");
  requireText(
    commit.sourceRollbackExecutionFingerprint,
    "commit.sourceRollbackExecutionFingerprint",
  );
  requireText(
    commit.sourceRestoreCheckExecutionFingerprint,
    "commit.sourceRestoreCheckExecutionFingerprint",
  );
  requireText(
    commit.sourceRestoreVerificationFingerprint,
    "commit.sourceRestoreVerificationFingerprint",
  );
  return commit;
}

function requireTimepointStateQueryArtifact(value) {
  const queryArtifact = requireObject(value, "queryArtifact");
  if (queryArtifact.schema !== "dosql.timepoint-state-query.v1") {
    throw new Error(`Unsupported timepoint state query schema: ${queryArtifact.schema}`);
  }
  if (queryArtifact.status !== "ready") {
    throw new Error("queryArtifact status must be ready");
  }
  requireText(queryArtifact.databaseAssetId, "queryArtifact.databaseAssetId");
  requireIsoDate(queryArtifact.timestamp, "queryArtifact.timestamp");
  requireText(queryArtifact.sqlText, "queryArtifact.sqlText");
  requireText(queryArtifact.artifactFingerprint, "queryArtifact.artifactFingerprint");
  return queryArtifact;
}

function normalizeExecutionResult({ executionResult, expectedBaselineRecordCount }) {
  const result = requireObject(executionResult, "executionResult");
  if (result.status !== "succeeded") {
    throw new Error(`metadata commit execution must succeed: ${result.status}`);
  }
  const timelineNodeInsertCount = requireInteger(
    result.timelineNodeInsertCount,
    "executionResult.timelineNodeInsertCount",
  );
  if (timelineNodeInsertCount !== 1) {
    throw new Error("metadata commit must insert exactly one timeline node");
  }
  const currentHeadUpdateCount = requireInteger(
    result.currentHeadUpdateCount,
    "executionResult.currentHeadUpdateCount",
  );
  if (currentHeadUpdateCount !== 1 || result.currentHeadGuardPassed !== true) {
    throw new Error("metadata commit current-head guard did not prove exactly one update");
  }
  const baselineRecordInsertCount = requireInteger(
    result.baselineRecordInsertCount,
    "executionResult.baselineRecordInsertCount",
  );
  if (baselineRecordInsertCount !== expectedBaselineRecordCount) {
    throw new Error("metadata commit baseline record insert count does not match the commit recordCount");
  }
  return sortObject({
    status: "succeeded",
    transactionId: requireText(result.transactionId, "executionResult.transactionId"),
    statementCount: requireInteger(result.statementCount, "executionResult.statementCount"),
    timelineNodeInsertCount,
    currentHeadUpdateCount,
    currentHeadGuardPassed: true,
    baselineRecordInsertCount,
  });
}

function normalizeTimelineArtifactsExecutionResult({
  executionResult,
  expectedRecordCount,
}) {
  const result = requireObject(executionResult, "executionResult");
  if (result.status !== "succeeded") {
    throw new Error(`timeline artifacts metadata commit execution must succeed: ${result.status}`);
  }
  const timelineArtifactInsertCount = requireInteger(
    result.timelineArtifactInsertCount,
    "executionResult.timelineArtifactInsertCount",
  );
  if (timelineArtifactInsertCount !== expectedRecordCount) {
    throw new Error(
      "timeline artifact metadata commit insert count does not match the commit recordCount",
    );
  }
  return sortObject({
    status: "succeeded",
    transactionId: requireText(result.transactionId, "executionResult.transactionId"),
    statementCount: requireInteger(result.statementCount, "executionResult.statementCount"),
    timelineArtifactInsertCount,
  });
}

function normalizeRestorePlanExecutionResult(executionResult) {
  const result = requireObject(executionResult, "executionResult");
  if (result.status !== "succeeded") {
    throw new Error(`restore plan metadata commit execution must succeed: ${result.status}`);
  }
  const restorePlanInsertCount = requireInteger(
    result.restorePlanInsertCount,
    "executionResult.restorePlanInsertCount",
  );
  if (restorePlanInsertCount !== 1) {
    throw new Error("restore plan metadata commit must insert exactly one restore plan");
  }
  return sortObject({
    status: "succeeded",
    transactionId: requireText(result.transactionId, "executionResult.transactionId"),
    statementCount: requireInteger(result.statementCount, "executionResult.statementCount"),
    restorePlanInsertCount,
  });
}

function normalizeRestoreEvidenceExecutionResult(executionResult) {
  const result = requireObject(executionResult, "executionResult");
  if (result.status !== "succeeded") {
    throw new Error(`restore evidence metadata commit execution must succeed: ${result.status}`);
  }
  const rollbackExecutionInsertCount = requireInteger(
    result.rollbackExecutionInsertCount,
    "executionResult.rollbackExecutionInsertCount",
  );
  if (rollbackExecutionInsertCount !== 1) {
    throw new Error("restore evidence metadata commit must insert exactly one rollback execution");
  }
  const restoreCheckExecutionInsertCount = requireInteger(
    result.restoreCheckExecutionInsertCount,
    "executionResult.restoreCheckExecutionInsertCount",
  );
  if (restoreCheckExecutionInsertCount !== 1) {
    throw new Error("restore evidence metadata commit must insert exactly one restore check execution");
  }
  const restoreVerificationInsertCount = requireInteger(
    result.restoreVerificationInsertCount,
    "executionResult.restoreVerificationInsertCount",
  );
  if (restoreVerificationInsertCount !== 1) {
    throw new Error("restore evidence metadata commit must insert exactly one restore verification");
  }
  return sortObject({
    status: "succeeded",
    transactionId: requireText(result.transactionId, "executionResult.transactionId"),
    statementCount: requireInteger(result.statementCount, "executionResult.statementCount"),
    rollbackExecutionInsertCount,
    restoreCheckExecutionInsertCount,
    restoreVerificationInsertCount,
  });
}

function normalizeForwardEvidenceExecutionResult(executionResult) {
  const result = requireObject(executionResult, "executionResult");
  if (result.status !== "succeeded") {
    throw new Error(`forward evidence metadata commit execution must succeed: ${result.status}`);
  }
  const forwardExecutionInsertCount = requireInteger(
    result.forwardExecutionInsertCount,
    "executionResult.forwardExecutionInsertCount",
  );
  if (forwardExecutionInsertCount !== 1) {
    throw new Error("forward evidence metadata commit must insert exactly one forward execution");
  }
  return sortObject({
    status: "succeeded",
    transactionId: requireText(result.transactionId, "executionResult.transactionId"),
    statementCount: requireInteger(result.statementCount, "executionResult.statementCount"),
    forwardExecutionInsertCount,
  });
}

function normalizeTimepointStateQueryResult(queryResult) {
  const result = requireObject(queryResult, "queryResult");
  if (result.status !== "succeeded") {
    throw new Error(`timepoint state query must succeed: ${result.status}`);
  }
  return {
    executionResult: sortObject({
      status: "succeeded",
      transactionId: requireText(result.transactionId, "queryResult.transactionId"),
      statementCount: requireInteger(result.statementCount, "queryResult.statementCount"),
    }),
    timepointState: requireObject(result.timepointState, "queryResult.timepointState"),
  };
}

function normalizeTimepointState({ value, databaseAssetId, timestamp }) {
  const timepointState = requireObject(value, "timepointState");
  const stateDatabaseAssetId = requireText(
    timepointState.databaseAssetId,
    "timepointState.databaseAssetId",
  );
  if (stateDatabaseAssetId !== databaseAssetId) {
    throw new Error("timepointState databaseAssetId must match queryArtifact databaseAssetId");
  }
  const stateTimestamp = requireIsoDate(timepointState.timestamp, "timepointState.timestamp");
  if (stateTimestamp !== timestamp) {
    throw new Error("timepointState timestamp must match queryArtifact timestamp");
  }
  requireObject(timepointState.timelineNode, "timepointState.timelineNode");
  return sortObject(timepointState);
}

function requireObject(value, fieldName) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${fieldName} is required`);
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
