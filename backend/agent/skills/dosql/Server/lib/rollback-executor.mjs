import { createHash } from "node:crypto";

export async function executeRollbackArtifactManifest(input) {
  const manifest = requireRollbackManifest(input.manifest);
  const adapter = requireObject(input.adapter, "adapter");
  const artifactTexts = requireObject(input.artifactTexts, "artifactTexts");
  const executionResults = [];
  for (const artifact of manifest.artifacts) {
    const artifactRef = requireText(artifact.artifactRef, "artifact.artifactRef");
    const artifactText = requireText(artifactTexts[artifactRef], `artifactTexts.${artifactRef}`);
    const expectedFingerprint = requireText(
      artifact.artifactFingerprint,
      "artifact.artifactFingerprint",
    );
    const actualFingerprint = `sha256:${sha256(artifactText)}`;
    if (actualFingerprint !== expectedFingerprint) {
      throw new Error(`rollback artifact fingerprint mismatch for ${artifactRef}`);
    }
    const result = await executeArtifactWithAdapter({
      adapter,
      artifact,
      artifactText,
      artifactRef,
      expectedFingerprint,
    });
    executionResults.push({
      ...result,
      artifactRef,
      artifactFingerprint: expectedFingerprint,
    });
  }
  return createRollbackExecutionArtifact({
    manifest,
    executionResults,
    executedBy: input.executedBy,
    executedAt: input.executedAt,
    connectionRef: input.connectionRef,
  });
}

async function executeArtifactWithAdapter({
  adapter,
  artifact,
  artifactText,
  artifactRef,
  expectedFingerprint,
}) {
  const artifactKind = requireText(artifact.artifactKind, "artifact.artifactKind");
  const requestBase = {
    artifactRef,
    artifactKind,
    artifactFingerprint: expectedFingerprint,
    sourceArtifactFingerprint: expectedFingerprint,
    timelineNodeId: artifact.timelineNodeId,
    method: artifact.method,
  };
  if (artifactKind === "rollback_sql") {
    if (typeof adapter.executeSqlArtifact !== "function") {
      throw new Error("adapter.executeSqlArtifact is required for rollback_sql artifacts");
    }
    return adapter.executeSqlArtifact({
      ...requestBase,
      sqlText: artifactText,
    });
  }
  if (artifactKind === "snapshot_manifest" || artifactKind === "pitr_marker") {
    if (typeof adapter.executeSnapshotRestoreArtifact !== "function") {
      throw new Error(
        "adapter.executeSnapshotRestoreArtifact is required for snapshot or PITR artifacts",
      );
    }
    return adapter.executeSnapshotRestoreArtifact({
      ...requestBase,
      artifactText,
      artifactPayload: parseArtifactJson(artifactText, artifactRef),
    });
  }
  if (artifactKind === "restore_report") {
    if (typeof adapter.executeRestoreReportArtifact !== "function") {
      throw new Error(
        "adapter.executeRestoreReportArtifact is required for restore_report artifacts",
      );
    }
    return adapter.executeRestoreReportArtifact({
      ...requestBase,
      artifactText,
      artifactPayload: parseArtifactJson(artifactText, artifactRef),
    });
  }
  throw new Error(`Unsupported rollback artifactKind for execution: ${artifactKind}`);
}

function parseArtifactJson(value, artifactRef) {
  try {
    return JSON.parse(value);
  } catch (error) {
    throw new Error(`rollback artifact JSON is invalid for ${artifactRef}: ${error.message}`);
  }
}

export function createRollbackExecutionArtifact(input) {
  const manifest = requireRollbackManifest(input.manifest);
  const executionResults = requireArray(input.executionResults, "executionResults");
  const byRef = new Map(executionResults.map((result) => [result.artifactRef, result]));
  const artifactExecutions = manifest.artifacts.map((artifact) => {
    const artifactRef = requireText(artifact.artifactRef, "artifact.artifactRef");
    const result = requireObject(byRef.get(artifactRef), `executionResults.${artifactRef}`);
    const expectedFingerprint = requireText(
      artifact.artifactFingerprint,
      "artifact.artifactFingerprint",
    );
    if (result.artifactFingerprint !== expectedFingerprint) {
      throw new Error(`rollback execution fingerprint mismatch for ${artifactRef}`);
    }
    if (result.status !== "succeeded") {
      throw new Error(`rollback artifact execution must succeed: ${artifactRef}`);
    }
    return sortObject({
      timelineNodeId: requireText(artifact.timelineNodeId, "artifact.timelineNodeId"),
      nodeLabel: requireText(artifact.nodeLabel, "artifact.nodeLabel"),
      method: requireText(artifact.method, "artifact.method"),
      restoreCapability: requireText(artifact.restoreCapability, "artifact.restoreCapability"),
      artifactKind: requireText(artifact.artifactKind, "artifact.artifactKind"),
      artifactRef,
      artifactFingerprint: expectedFingerprint,
      transactionId: requireText(result.transactionId, "result.transactionId"),
      statementCount: requireInteger(result.statementCount, "result.statementCount"),
      affectedRows: requireInteger(result.affectedRows, "result.affectedRows"),
    });
  });
  const executedBy = requireText(input.executedBy, "executedBy");
  const executedAt = requireIsoDate(input.executedAt, "executedAt");
  const base = sortObject({
    schema: "dosql.rollback-execution.v1",
    status: "verified",
    databaseAssetId: requireText(manifest.databaseAssetId, "manifest.databaseAssetId"),
    manifestId: requireText(manifest.manifestId, "manifest.manifestId"),
    restorePlanId: requireText(manifest.restorePlanId, "manifest.restorePlanId"),
    changeRequestId: requireText(manifest.changeRequestId, "manifest.changeRequestId"),
    sourceArtifactManifestFingerprint: requireText(
      manifest.artifactFingerprint,
      "manifest.artifactFingerprint",
    ),
    artifactExecutions,
    executionCount: artifactExecutions.length,
    connectionRef: input.connectionRef ? String(input.connectionRef).trim() : "",
    executedBy,
    executedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function requireRollbackManifest(value) {
  const manifest = requireObject(value, "manifest");
  if (manifest.schema !== "dosql.rollback-artifact-manifest.v1") {
    throw new Error(`Unsupported rollback artifact manifest schema: ${manifest.schema}`);
  }
  if (manifest.status !== "ready") {
    throw new Error("rollback artifact manifest status must be ready");
  }
  const artifacts = requireArray(manifest.artifacts, "manifest.artifacts");
  if (artifacts.length === 0) {
    throw new Error("rollback artifact manifest must include at least one artifact");
  }
  return { ...manifest, artifacts };
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
