import { createHash } from "node:crypto";

const RESTORE_CAPABILITIES = new Set([
  "schema_reversible",
  "data_patch_reversible",
  "snapshot_required",
  "manual_mitigation",
  "unrestorable",
]);

const BASELINE_KINDS = new Set(["before", "after", "initial", "drift"]);

const DATA_SCOPES = new Set(["none", "primary_keys", "before_image", "snapshot", "pitr"]);

const SNAPSHOT_RESTORE_ARTIFACT_KINDS = new Set(["snapshot_manifest", "pitr_marker"]);

export function createBaselineRecordSet(input) {
  const node = requireNode(input.node, "node");
  const createdBy = requireText(input.createdBy, "createdBy");
  const createdAt = requireIsoDate(input.createdAt, "createdAt");
  const records = requireArray(input.records, "records").map((record) =>
    createBaselineRecordForNode(node, record),
  );
  if (records.length === 0) {
    throw new Error("records must include at least one baseline record");
  }
  const base = sortObject({
    schema: "dosql.baseline-record-set.v1",
    databaseAssetId: node.databaseAssetId,
    timelineNodeId: node.timelineNodeId,
    nodeLabel: node.nodeLabel ?? formatNodeLabel(node.nodeSequence),
    records,
    createdBy,
    createdAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function createInitialBaselineFromStructureSnapshot(input) {
  const databaseAssetId = requireText(input.databaseAssetId, "databaseAssetId");
  const structureSnapshot = requireObject(input.structureSnapshot, "structureSnapshot");
  const asset = findSnapshotAsset({
    snapshot: structureSnapshot,
    databaseAssetId,
    fieldName: "structureSnapshot",
  });
  const schemaFingerprint = requireText(
    asset.structureFingerprint,
    "structureSnapshot.asset.structureFingerprint",
  );
  const baselineAfterRef = requireText(input.baselineAfterRef, "baselineAfterRef");
  const capturedAt = requireIsoDate(structureSnapshot.capturedAt, "structureSnapshot.capturedAt");
  const timelineNode = createInitialTimelineNode({
    databaseAssetId,
    validFrom: input.validFrom ? requireIsoDate(input.validFrom, "validFrom") : capturedAt,
    baselineAfterRef,
    schemaFingerprint,
    dataCheckpointRef: input.dataCheckpointRef,
    createdAt: input.createdAt,
  });
  const recordSet = createBaselineRecordSet({
    node: timelineNode,
    createdBy: input.createdBy,
    createdAt: input.createdAt,
    records: [
      createStructureSnapshotBaselineRecord({
        baselineKind: "initial",
        node: timelineNode,
        snapshot: structureSnapshot,
        asset,
        schemaSnapshotRef: baselineAfterRef,
        dataScope: input.dataScope ?? "none",
        dataEvidenceRef: input.dataEvidenceRef ?? "",
      }),
    ],
  });
  const base = sortObject({
    schema: "dosql.initial-baseline.v1",
    databaseAssetId,
    sourceSnapshotFingerprint: structureSnapshot.snapshotFingerprint ?? "",
    sourceStructureFingerprint: schemaFingerprint,
    timelineNode,
    recordSet,
    createdBy: requireText(input.createdBy, "createdBy"),
    createdAt: requireIsoDate(input.createdAt, "createdAt"),
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function createBaselineRecordSetFromStructureSnapshots(input) {
  const currentNode = requireNode(input.currentNode, "currentNode");
  const node = requireNode(input.node, "node");
  if (currentNode.databaseAssetId !== node.databaseAssetId) {
    throw new Error("currentNode and node must belong to the same database asset");
  }
  if (node.parentNodeId !== currentNode.timelineNodeId) {
    throw new Error("node parentNodeId must match currentNode timelineNodeId");
  }
  const beforeSnapshot = requireObject(input.beforeSnapshot, "beforeSnapshot");
  const afterSnapshot = requireObject(input.afterSnapshot, "afterSnapshot");
  const beforeAsset = findSnapshotAsset({
    snapshot: beforeSnapshot,
    databaseAssetId: node.databaseAssetId,
    fieldName: "beforeSnapshot",
  });
  const afterAsset = findSnapshotAsset({
    snapshot: afterSnapshot,
    databaseAssetId: node.databaseAssetId,
    fieldName: "afterSnapshot",
  });
  const afterStructureFingerprint = requireText(
    afterAsset.structureFingerprint,
    "afterSnapshot.asset.structureFingerprint",
  );
  const beforeStructureFingerprint = requireText(
    beforeAsset.structureFingerprint,
    "beforeSnapshot.asset.structureFingerprint",
  );
  const currentSchemaFingerprint = requireText(
    currentNode.schemaFingerprint,
    "currentNode.schemaFingerprint",
  );
  if (beforeStructureFingerprint !== currentSchemaFingerprint) {
    throw new Error("before structure fingerprint must match currentNode schemaFingerprint");
  }
  const nodeSchemaFingerprint = requireText(node.schemaFingerprint, "node.schemaFingerprint");
  if (afterStructureFingerprint !== nodeSchemaFingerprint) {
    throw new Error("after structure fingerprint must match timeline node schemaFingerprint");
  }
  return createBaselineRecordSet({
    node,
    createdBy: input.createdBy,
    createdAt: input.createdAt,
    records: [
      createStructureSnapshotBaselineRecord({
        baselineKind: "before",
        node,
        snapshot: beforeSnapshot,
        asset: beforeAsset,
        schemaSnapshotRef: node.baselineBeforeRef,
        dataScope: input.beforeDataScope ?? input.dataScope ?? "none",
        dataEvidenceRef: input.beforeDataEvidenceRef ?? input.dataEvidenceRef ?? "",
      }),
      createStructureSnapshotBaselineRecord({
        baselineKind: "after",
        node,
        snapshot: afterSnapshot,
        asset: afterAsset,
        schemaSnapshotRef: node.baselineAfterRef,
        dataScope: input.afterDataScope ?? input.dataScope ?? "none",
        dataEvidenceRef: input.afterDataEvidenceRef ?? input.dataEvidenceRef ?? "",
      }),
    ],
  });
}

export function createRestoreBaselineRecordSetFromVerificationArtifact(input) {
  const restoreNode = requireNode(input.restoreNode, "restoreNode");
  if (restoreNode.nodeKind !== "restore") {
    throw new Error("restoreNode must be a restore timeline node");
  }
  if (restoreNode.stateStatus !== "verified") {
    throw new Error("restoreNode must be verified");
  }
  const restoreVerification = requireObject(input.restoreVerification, "restoreVerification");
  if (restoreVerification.schema !== "dosql.restore-verification.v1") {
    throw new Error(`Unsupported restore verification schema: ${restoreVerification.schema}`);
  }
  if (restoreVerification.status !== "verified") {
    throw new Error("restore verification must be verified");
  }
  const databaseAssetId = requireText(
    restoreVerification.databaseAssetId,
    "restoreVerification.databaseAssetId",
  );
  if (restoreNode.databaseAssetId !== databaseAssetId) {
    throw new Error("restoreNode databaseAssetId must match restore verification");
  }
  const operationId = requireText(
    restoreVerification.operationId,
    "restoreVerification.operationId",
  );
  if (restoreNode.operationId !== operationId) {
    throw new Error("restoreNode operationId must match restore verification");
  }
  const currentNode = requireObject(restoreVerification.currentNode, "restoreVerification.currentNode");
  const targetNode = requireObject(restoreVerification.targetNode, "restoreVerification.targetNode");
  const currentNodeId = requireText(
    currentNode.timelineNodeId,
    "restoreVerification.currentNode.timelineNodeId",
  );
  const targetNodeId = requireText(
    targetNode.timelineNodeId,
    "restoreVerification.targetNode.timelineNodeId",
  );
  if (restoreNode.parentNodeId !== currentNodeId || restoreNode.restoreFromNodeId !== currentNodeId) {
    throw new Error("restoreNode restore source must match restore verification currentNode");
  }
  if (restoreNode.restoreTargetNodeId !== targetNodeId) {
    throw new Error("restoreNode restore target must match restore verification targetNode");
  }
  const baselineBeforeRef = requireText(
    restoreVerification.baselineBeforeRef,
    "restoreVerification.baselineBeforeRef",
  );
  if (restoreNode.baselineBeforeRef !== baselineBeforeRef) {
    throw new Error("restoreNode baselineBeforeRef must match restore verification");
  }
  const baselineAfterRef = requireText(
    restoreVerification.baselineAfterRef,
    "restoreVerification.baselineAfterRef",
  );
  if (restoreNode.baselineAfterRef !== baselineAfterRef) {
    throw new Error("restoreNode baselineAfterRef must match restore verification");
  }
  const restoredSchemaFingerprint = requireText(
    restoreVerification.schemaFingerprint,
    "restoreVerification.schemaFingerprint",
  );
  if (restoreNode.schemaFingerprint !== restoredSchemaFingerprint) {
    throw new Error("restoreNode schemaFingerprint must match restore verification");
  }
  const targetSchemaFingerprint = requireText(
    targetNode.schemaFingerprint,
    "restoreVerification.targetNode.schemaFingerprint",
  );
  if (targetSchemaFingerprint !== restoredSchemaFingerprint) {
    throw new Error("restore verification target schemaFingerprint must match restored schemaFingerprint");
  }
  const currentSchemaFingerprint = requireText(
    currentNode.schemaFingerprint,
    "restoreVerification.currentNode.schemaFingerprint",
  );
  const verifiedAt = requireIsoDate(restoreVerification.verifiedAt, "restoreVerification.verifiedAt");
  const restoreNodeValidFrom = requireIsoDate(restoreNode.validFrom, "restoreNode.validFrom");
  if (restoreNodeValidFrom !== verifiedAt) {
    throw new Error("restoreNode validFrom must match restore verification verifiedAt");
  }
  const restoreVerificationRef = requireText(input.restoreVerificationRef, "restoreVerificationRef");
  if (restoreNode.dataCheckpointRef !== restoreVerificationRef) {
    throw new Error("restoreNode dataCheckpointRef must match restoreVerificationRef");
  }
  const restoreVerificationFingerprint = requireText(
    restoreVerification.artifactFingerprint,
    "restoreVerification.artifactFingerprint",
  );
  const beforeDataEvidenceRef = input.beforeDataEvidenceRef
    ? String(input.beforeDataEvidenceRef)
    : "";
  const afterDataEvidenceRef = input.afterDataEvidenceRef
    ? String(input.afterDataEvidenceRef)
    : requireText(restoreVerification.evidenceRef, "restoreVerification.evidenceRef");

  return createBaselineRecordSet({
    node: restoreNode,
    createdBy: input.createdBy,
    createdAt: input.createdAt,
    records: [
      createRestoreBaselineRecord({
        baselineKind: "before",
        capturedAt: input.beforeCapturedAt ?? verifiedAt,
        schemaSnapshotRef: baselineBeforeRef,
        schemaFingerprint: currentSchemaFingerprint,
        dataScope: input.beforeDataScope ?? "none",
        dataEvidenceRef: beforeDataEvidenceRef,
        restoreVerificationFingerprint,
        restoreVerificationId: restoreVerification.restoreVerificationId,
        restoreNodeId: restoreNode.timelineNodeId,
      }),
      createRestoreBaselineRecord({
        baselineKind: "after",
        capturedAt: input.afterCapturedAt ?? verifiedAt,
        schemaSnapshotRef: baselineAfterRef,
        schemaFingerprint: restoredSchemaFingerprint,
        dataScope: input.afterDataScope ?? "none",
        dataEvidenceRef: afterDataEvidenceRef,
        restoreVerificationFingerprint,
        restoreVerificationId: restoreVerification.restoreVerificationId,
        restoreNodeId: restoreNode.timelineNodeId,
      }),
    ],
  });
}

export function checkTimelineHeadDrift(input) {
  const currentNode = requireNode(input.currentNode, "currentNode");
  const liveSchemaFingerprint = requireText(input.liveSchemaFingerprint, "liveSchemaFingerprint");
  const checkedAt = requireIsoDate(input.checkedAt, "checkedAt");
  const evidenceRef = requireText(input.evidenceRef, "evidenceRef");
  const expectedSchemaFingerprint = requireText(
    currentNode.schemaFingerprint,
    "currentNode.schemaFingerprint",
  );
  const base = {
    currentNodeId: currentNode.timelineNodeId,
    currentLabel: currentNode.nodeLabel ?? formatNodeLabel(currentNode.nodeSequence),
    expectedSchemaFingerprint,
    liveSchemaFingerprint,
    checkedAt,
    evidenceRef,
  };
  if (liveSchemaFingerprint === expectedSchemaFingerprint) {
    return sortObject({
      ...base,
      status: "matched",
      canPlanChange: true,
      reason: "",
    });
  }
  return sortObject({
    ...base,
    status: "drift_detected",
    canPlanChange: false,
    reason: "Live schema fingerprint differs from the current verified timeline head; import drift before planning a new change.",
  });
}

export function createInitialTimelineNode(input) {
  const databaseAssetId = requireText(input.databaseAssetId, "databaseAssetId");
  const validFrom = requireIsoDate(input.validFrom, "validFrom");
  const baselineAfterRef = requireText(input.baselineAfterRef, "baselineAfterRef");
  const schemaFingerprint = requireText(input.schemaFingerprint, "schemaFingerprint");
  const node = {
    timelineNodeId: `tln_${sha256(
      `${databaseAssetId}\u001f0\u001f${validFrom}\u001f${schemaFingerprint}`,
    ).slice(0, 16)}`,
    databaseAssetId,
    nodeSequence: 0,
    nodeLabel: formatNodeLabel(0),
    parentNodeId: "",
    operationId: "",
    nodeKind: "baseline",
    stateStatus: "verified",
    validFrom,
    baselineBeforeRef: "",
    baselineAfterRef,
    schemaFingerprint,
    dataCheckpointRef: input.dataCheckpointRef ? String(input.dataCheckpointRef) : "",
    restoreCapability: "schema_reversible",
    restoreFromNodeId: "",
    restoreTargetNodeId: "",
    derivedFromSql: false,
    createdAt: input.createdAt ? requireIsoDate(input.createdAt, "createdAt") : validFrom,
  };
  return sortObject(node);
}

export function createDriftImportTimelineNode(input) {
  const currentNode = requireNode(input.currentNode, "currentNode");
  const operationId = requireText(input.operationId, "operationId");
  const validFrom = requireIsoDate(input.validFrom, "validFrom");
  const schemaFingerprint = requireText(input.schemaFingerprint, "schemaFingerprint");
  const currentFingerprint = requireText(currentNode.schemaFingerprint, "currentNode.schemaFingerprint");
  if (schemaFingerprint === currentFingerprint) {
    throw new Error("drift schemaFingerprint must differ from current head schemaFingerprint");
  }
  const restoreCapability = requireRestoreCapability(input.restoreCapability);
  const nodeSequence = Number(currentNode.nodeSequence) + 1;
  const node = {
    timelineNodeId: `tln_${sha256(
      `${currentNode.databaseAssetId}\u001f${nodeSequence}\u001fdrift\u001f${operationId}\u001f${validFrom}\u001f${schemaFingerprint}`,
    ).slice(0, 16)}`,
    databaseAssetId: currentNode.databaseAssetId,
    nodeSequence,
    nodeLabel: formatNodeLabel(nodeSequence),
    parentNodeId: currentNode.timelineNodeId,
    operationId,
    nodeKind: "drift_import",
    stateStatus: "verified",
    validFrom,
    baselineBeforeRef: requireText(currentNode.baselineAfterRef, "currentNode.baselineAfterRef"),
    baselineAfterRef: requireText(input.baselineAfterRef, "baselineAfterRef"),
    schemaFingerprint,
    dataCheckpointRef: requireText(input.evidenceRef, "evidenceRef"),
    restoreCapability,
    restoreFromNodeId: "",
    restoreTargetNodeId: "",
    derivedFromSql: false,
    createdAt: input.createdAt ? requireIsoDate(input.createdAt, "createdAt") : validFrom,
  };
  return sortObject(node);
}

export function projectDatabaseVersionHead(input) {
  const currentVersion = requireObject(input.currentVersion, "currentVersion");
  const nextNode = requireNode(input.nextNode, "nextNode");
  if (nextNode.stateStatus !== "verified") {
    throw new Error(`nextNode must be verified: ${nextNode.timelineNodeId}`);
  }
  const databaseAssetId = requireText(currentVersion.databaseAssetId, "currentVersion.databaseAssetId");
  if (nextNode.databaseAssetId !== databaseAssetId) {
    throw new Error("currentVersion and nextNode must belong to the same database asset");
  }
  const currentVersionNumber = Number(currentVersion.currentVersion);
  if (!Number.isInteger(currentVersionNumber) || currentVersionNumber < 0) {
    throw new Error("currentVersion.currentVersion must be a non-negative integer");
  }
  const nextSequence = Number(nextNode.nodeSequence);
  const currentTimelineNodeId = String(currentVersion.currentTimelineNodeId ?? "");
  if (nextSequence === 0) {
    if (currentVersionNumber !== 0 || currentTimelineNodeId !== "") {
      throw new Error("initial timeline node can only initialize an empty current-head cache");
    }
  } else {
    if (nextSequence !== currentVersionNumber + 1) {
      throw new Error("nextNode sequence must be contiguous with currentVersion");
    }
    if (nextNode.parentNodeId !== currentTimelineNodeId) {
      throw new Error("nextNode parent must match currentVersion currentTimelineNodeId");
    }
  }
  const updatedBy = requireText(input.updatedBy, "updatedBy");
  const updatedAt = requireIsoDate(input.updatedAt, "updatedAt");
  const projectedVersion = sortObject({
    databaseAssetId,
    currentVersion: nextSequence,
    currentLabel: nextNode.nodeLabel ?? formatNodeLabel(nextSequence),
    currentTimelineNodeId: nextNode.timelineNodeId,
    updatedBy,
    updatedAt,
  });
  const base = sortObject({
    schema: "dosql.database-version-projection.v1",
    status: "projected",
    previousVersion: {
      databaseAssetId,
      currentVersion: currentVersionNumber,
      currentLabel: String(currentVersion.currentLabel ?? formatNodeLabel(currentVersionNumber)),
      currentTimelineNodeId,
    },
    projectedVersion,
    sourceTimelineNodeId: nextNode.timelineNodeId,
    createdBy: updatedBy,
    createdAt: updatedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function createVerifiedChangeNode(input) {
  const currentNode = requireNode(input.currentNode, "currentNode");
  const operationId = requireText(input.operationId, "operationId");
  const validFrom = requireIsoDate(input.validFrom, "validFrom");
  const restoreCapability = requireRestoreCapability(input.restoreCapability);
  const nodeSequence = Number(currentNode.nodeSequence) + 1;
  const node = {
    timelineNodeId: `tln_${sha256(
      `${currentNode.databaseAssetId}\u001f${nodeSequence}\u001f${operationId}\u001f${validFrom}`,
    ).slice(0, 16)}`,
    databaseAssetId: currentNode.databaseAssetId,
    nodeSequence,
    nodeLabel: formatNodeLabel(nodeSequence),
    parentNodeId: currentNode.timelineNodeId,
    operationId,
    nodeKind: "change",
    stateStatus: "verified",
    validFrom,
    baselineBeforeRef: requireText(input.baselineBeforeRef, "baselineBeforeRef"),
    baselineAfterRef: requireText(input.baselineAfterRef, "baselineAfterRef"),
    schemaFingerprint: requireText(input.schemaFingerprint, "schemaFingerprint"),
    dataCheckpointRef: input.dataCheckpointRef ? String(input.dataCheckpointRef) : "",
    restoreCapability,
    restoreFromNodeId: "",
    restoreTargetNodeId: "",
    derivedFromSql: false,
    ...(input.changeDescriptor ? { changeDescriptor: normalizeChangeDescriptor(input.changeDescriptor) } : {}),
    createdAt: input.createdAt ? requireIsoDate(input.createdAt, "createdAt") : validFrom,
  };
  return sortObject(node);
}

export function resolveTimelineNodeAt(input) {
  const databaseAssetId = requireText(input.databaseAssetId, "databaseAssetId");
  const timestamp = Date.parse(requireIsoDate(input.timestamp, "timestamp"));
  return [...requireArray(input.nodes, "nodes")]
    .filter((node) => node.databaseAssetId === databaseAssetId)
    .filter((node) => node.stateStatus === "verified")
    .filter((node) => Date.parse(node.validFrom) <= timestamp)
    .sort((left, right) => {
      const byTime = Date.parse(right.validFrom) - Date.parse(left.validFrom);
      if (byTime !== 0) return byTime;
      return Number(right.nodeSequence) - Number(left.nodeSequence);
    })[0] ?? null;
}

export function createTimepointStateManifest(input) {
  const queryResultArtifact = requireTimepointStateQueryResult(
    input.queryResultArtifact,
  );
  const createdBy = requireText(input.createdBy, "createdBy");
  const createdAt = requireIsoDate(input.createdAt, "createdAt");
  const databaseAssetId = requireText(
    queryResultArtifact.databaseAssetId,
    "queryResultArtifact.databaseAssetId",
  );
  const timestamp = requireIsoDate(queryResultArtifact.timestamp, "queryResultArtifact.timestamp");
  const timepointState = requireObject(
    queryResultArtifact.timepointState,
    "queryResultArtifact.timepointState",
  );
  if (requireText(timepointState.databaseAssetId, "timepointState.databaseAssetId") !== databaseAssetId) {
    throw new Error("timepointState databaseAssetId must match queryResultArtifact databaseAssetId");
  }
  if (requireIsoDate(timepointState.timestamp, "timepointState.timestamp") !== timestamp) {
    throw new Error("timepointState timestamp must match queryResultArtifact timestamp");
  }
  const timelineNode = requireObject(timepointState.timelineNode, "timepointState.timelineNode");
  const timelineNodeId = requireRecordText(
    timelineNode,
    "timelineNodeId",
    "timeline_node_id",
    "timepointState.timelineNode.timelineNodeId",
  );
  const nodeDatabaseAssetId = requireRecordText(
    timelineNode,
    "databaseAssetId",
    "database_asset_id",
    "timepointState.timelineNode.databaseAssetId",
  );
  if (nodeDatabaseAssetId !== databaseAssetId) {
    throw new Error("timepointState timelineNode databaseAssetId must match queryResultArtifact databaseAssetId");
  }
  const nodeSequence = requireIntegerRecord(
    timelineNode,
    "nodeSequence",
    "node_sequence",
    "timepointState.timelineNode.nodeSequence",
  );
  const nodeLabel =
    optionalRecordText(timelineNode, "nodeLabel", "node_label") ?? formatNodeLabel(nodeSequence);
  const nodeKind = requireRecordText(
    timelineNode,
    "nodeKind",
    "node_kind",
    "timepointState.timelineNode.nodeKind",
  );
  const stateStatus = requireRecordText(
    timelineNode,
    "stateStatus",
    "state_status",
    "timepointState.timelineNode.stateStatus",
  );
  if (stateStatus !== "verified") {
    throw new Error("timepointState timelineNode must be verified");
  }
  const validFrom = requireIsoDate(
    requireRecordText(timelineNode, "validFrom", "valid_from", "timepointState.timelineNode.validFrom"),
    "timepointState.timelineNode.validFrom",
  );
  const schemaSnapshotRef = requireRecordText(
    timelineNode,
    "baselineAfterRef",
    "baseline_after_ref",
    "timepointState.timelineNode.baselineAfterRef",
  );
  const schemaFingerprint = requireRecordText(
    timelineNode,
    "schemaFingerprint",
    "schema_fingerprint",
    "timepointState.timelineNode.schemaFingerprint",
  );
  const restoreCapability = optionalRecordText(
    timelineNode,
    "restoreCapability",
    "restore_capability",
  ) ?? "";
  if (restoreCapability) requireRestoreCapability(restoreCapability);
  const baselineRecords = requireArray(
    timepointState.baselineRecords,
    "timepointState.baselineRecords",
  );
  const timelineArtifacts = Array.isArray(timepointState.timelineArtifacts)
    ? timepointState.timelineArtifacts
    : [];
  const executionEvidence = normalizeTimepointExecutionEvidence(
    timepointState.executionEvidence,
  );
  const stateBaseline = findTimepointStateBaseline({
    baselineRecords,
    schemaSnapshotRef,
    schemaFingerprint,
  });
  const base = sortObject({
    schema: "dosql.timepoint-state-manifest.v1",
    status: "resolved",
    databaseAssetId,
    timestamp,
    timelineNodeId,
    nodeSequence,
    nodeLabel,
    nodeKind,
    stateStatus,
    validFrom,
    schemaFingerprint,
    schemaSnapshotRef,
    restoreCapability,
    stateBaseline,
    baselineRecords,
    timelineArtifacts,
    executionEvidence,
    sourceQueryResultFingerprint: requireText(
      queryResultArtifact.artifactFingerprint,
      "queryResultArtifact.artifactFingerprint",
    ),
    createdBy,
    createdAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function normalizeTimepointExecutionEvidence(value) {
  const evidence =
    value && typeof value === "object" && !Array.isArray(value)
      ? value
      : {};
  return sortObject({
    forwardExecutions: Array.isArray(evidence.forwardExecutions)
      ? evidence.forwardExecutions
      : [],
    restoreVerifications: Array.isArray(evidence.restoreVerifications)
      ? evidence.restoreVerifications
      : [],
  });
}

export function planRollbackToNode(input) {
  const databaseAssetId = requireText(input.databaseAssetId, "databaseAssetId");
  const nodes = requireArray(input.nodes, "nodes").filter(
    (node) => node.databaseAssetId === databaseAssetId,
  );
  const byId = new Map(nodes.map((node) => [node.timelineNodeId, node]));
  const currentNode = byId.get(requireText(input.currentNodeId, "currentNodeId"));
  const targetNode = byId.get(requireText(input.targetNodeId, "targetNodeId"));
  if (!currentNode) throw new Error(`currentNodeId not found: ${input.currentNodeId}`);
  if (!targetNode) throw new Error(`targetNodeId not found: ${input.targetNodeId}`);
  if (currentNode.stateStatus !== "verified") {
    throw new Error(`current node must be verified: ${currentNode.timelineNodeId}`);
  }
  if (targetNode.stateStatus !== "verified") {
    throw new Error(`target node must be verified: ${targetNode.timelineNodeId}`);
  }

  const steps = [];
  let cursor = currentNode;
  while (cursor.timelineNodeId !== targetNode.timelineNodeId) {
    if (!cursor.parentNodeId) {
      throw new Error(`target node is not an ancestor of current node: ${targetNode.timelineNodeId}`);
    }
    const step = {
      timelineNodeId: cursor.timelineNodeId,
      nodeLabel: cursor.nodeLabel,
      restoreCapability: cursor.restoreCapability,
      method: restoreMethod(cursor.restoreCapability),
      baselineBeforeRef: cursor.baselineBeforeRef ?? "",
      baselineAfterRef: cursor.baselineAfterRef ?? "",
      dataCheckpointRef: cursor.dataCheckpointRef ?? "",
      ...(cursor.changeDescriptor ? { changeDescriptor: cursor.changeDescriptor } : {}),
    };
    if (cursor.restoreCapability === "unrestorable") {
      return sortObject({
        status: "blocked",
        currentNodeId: currentNode.timelineNodeId,
        targetNodeId: targetNode.timelineNodeId,
        blockingNodeId: cursor.timelineNodeId,
        reason: `Timeline node ${cursor.nodeLabel} is unrestorable because required baseline evidence is missing.`,
        steps: [...steps, step],
      });
    }
    steps.push(step);
    cursor = byId.get(cursor.parentNodeId);
    if (!cursor) {
      throw new Error("parent node not found while planning rollback");
    }
  }

  return sortObject({
    status: "planned",
    currentNodeId: currentNode.timelineNodeId,
    targetNodeId: targetNode.timelineNodeId,
    steps,
  });
}

export function createRestoreTimelineNode(input) {
  const currentNode = requireNode(input.currentNode, "currentNode");
  const targetNode = requireNode(input.targetNode, "targetNode");
  if (currentNode.databaseAssetId !== targetNode.databaseAssetId) {
    throw new Error("currentNode and targetNode must belong to the same database asset");
  }
  const operationId = requireText(input.operationId, "operationId");
  const validFrom = requireIsoDate(input.validFrom, "validFrom");
  const nodeSequence = Number(currentNode.nodeSequence) + 1;
  const node = {
    timelineNodeId: `tln_${sha256(
      `${currentNode.databaseAssetId}\u001f${nodeSequence}\u001frestore\u001f${operationId}\u001f${validFrom}`,
    ).slice(0, 16)}`,
    databaseAssetId: currentNode.databaseAssetId,
    nodeSequence,
    nodeLabel: formatNodeLabel(nodeSequence),
    parentNodeId: currentNode.timelineNodeId,
    operationId,
    nodeKind: "restore",
    stateStatus: "verified",
    validFrom,
    baselineBeforeRef: requireText(input.baselineBeforeRef, "baselineBeforeRef"),
    baselineAfterRef: requireText(input.baselineAfterRef, "baselineAfterRef"),
    schemaFingerprint: requireText(input.schemaFingerprint, "schemaFingerprint"),
    dataCheckpointRef: input.evidenceRef ? String(input.evidenceRef) : "",
    restoreCapability: "schema_reversible",
    restoreFromNodeId: currentNode.timelineNodeId,
    restoreTargetNodeId: targetNode.timelineNodeId,
    derivedFromSql: false,
    createdAt: input.createdAt ? requireIsoDate(input.createdAt, "createdAt") : validFrom,
  };
  return sortObject(node);
}

export function createTimelineNodeFromVerifiedExecutionEvent(input) {
  const currentNode = requireNode(input.currentNode, "currentNode");
  const event = requireObject(input.event, "event");
  if (event.eventType !== "sql.execution.verified") {
    throw new Error(`event must be sql.execution.verified: ${event.eventType}`);
  }
  if (event.databaseAssetId !== currentNode.databaseAssetId) {
    throw new Error("event and currentNode must belong to the same database asset");
  }
  const fromVersion = Number(event.version?.from);
  const toVersion = Number(event.version?.to);
  const currentSequence = Number(currentNode.nodeSequence);
  if (fromVersion !== currentSequence || toVersion !== currentSequence + 1) {
    throw new Error(
      `verified event version must be contiguous with current timeline head ${currentSequence}`,
    );
  }
  const expectedLabel = formatNodeLabel(toVersion);
  if (event.version?.label !== expectedLabel) {
    throw new Error(`verified event label must be ${expectedLabel}`);
  }
  const timeline = requireObject(event.timeline, "event.timeline");
  return createVerifiedChangeNode({
    currentNode,
    operationId: event.operationId,
    validFrom: event.createdAt,
    baselineBeforeRef: timeline.baselineBeforeRef,
    baselineAfterRef: timeline.baselineAfterRef,
    schemaFingerprint: timeline.schemaFingerprint,
    dataCheckpointRef: timeline.dataCheckpointRef,
    restoreCapability: timeline.restoreCapability,
  });
}

export function createRestorePlanArtifact(input) {
  const changeRequestId = requireText(input.changeRequestId, "changeRequestId");
  const databaseAssetId = requireText(input.databaseAssetId, "databaseAssetId");
  const currentNode = requireNode(input.currentNode, "currentNode");
  const targetNode = requireNode(input.targetNode, "targetNode");
  if (currentNode.databaseAssetId !== databaseAssetId || targetNode.databaseAssetId !== databaseAssetId) {
    throw new Error("restore plan nodes must belong to the requested database asset");
  }
  const plan = requireObject(input.plan, "plan");
  if (plan.currentNodeId !== currentNode.timelineNodeId) {
    throw new Error("plan currentNodeId must match currentNode");
  }
  if (plan.targetNodeId !== targetNode.timelineNodeId) {
    throw new Error("plan targetNodeId must match targetNode");
  }
  const createdAt = requireIsoDate(input.createdAt, "createdAt");
  const createdBy = requireText(input.createdBy, "createdBy");
  const base = sortObject({
    schema: "dosql.restore-plan.v1",
    changeRequestId,
    databaseAssetId,
    restorePlanId: `rplan_${sha256(
      `${changeRequestId}\u001f${databaseAssetId}\u001f${currentNode.timelineNodeId}\u001f${targetNode.timelineNodeId}\u001f${createdAt}`,
    ).slice(0, 16)}`,
    status: requireText(plan.status, "plan.status"),
    currentNode: summarizeNode(currentNode),
    targetNode: summarizeNode(targetNode),
    sourceNodeIds: [currentNode.timelineNodeId, targetNode.timelineNodeId],
    plan,
    createdBy,
    createdAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function createRestoreTimelineNodeFromPlanArtifact(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  if (restorePlan.status === "blocked") {
    throw new Error("blocked restore plan cannot be finalized");
  }
  const targetNodeSummary = requireObject(restorePlan.targetNode, "restorePlan.targetNode");
  const schemaFingerprint = requireText(input.schemaFingerprint, "schemaFingerprint");
  if (targetNodeSummary.schemaFingerprint && schemaFingerprint !== targetNodeSummary.schemaFingerprint) {
    throw new Error("restored schemaFingerprint must match target node schema fingerprint");
  }
  const databaseAssetId = requireText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId");
  const currentNode = expandPlanNode(restorePlan.currentNode, databaseAssetId, "restorePlan.currentNode");
  const targetNode = expandPlanNode(targetNodeSummary, databaseAssetId, "restorePlan.targetNode");
  return createRestoreTimelineNode({
    currentNode,
    targetNode,
    operationId: input.operationId,
    validFrom: input.verifiedAt,
    baselineBeforeRef: input.baselineBeforeRef,
    baselineAfterRef: input.baselineAfterRef,
    schemaFingerprint,
    evidenceRef: input.evidenceRef,
  });
}

export function createRestoreVerificationArtifact(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  if (restorePlan.status === "blocked") {
    throw new Error("blocked restore plan cannot be verified");
  }
  const schemaFingerprint = requireText(input.schemaFingerprint, "schemaFingerprint");
  const targetNode = requireObject(restorePlan.targetNode, "restorePlan.targetNode");
  if (targetNode.schemaFingerprint && schemaFingerprint !== targetNode.schemaFingerprint) {
    throw new Error("restored schemaFingerprint must match target node schema fingerprint");
  }
  const checks = normalizeRestoreVerificationChecks(input.checks);
  const verifiedAt = requireIsoDate(input.verifiedAt, "verifiedAt");
  const operationId = requireText(input.operationId, "operationId");
  const rollbackExecutionEvidence = normalizeRollbackExecutionEvidence(input);
  const base = sortObject({
    schema: "dosql.restore-verification.v1",
    restoreVerificationId: `rver_${sha256(
      `${restorePlan.restorePlanId}\u001f${operationId}\u001f${verifiedAt}\u001f${schemaFingerprint}`,
    ).slice(0, 16)}`,
    restorePlanId: requireText(restorePlan.restorePlanId, "restorePlan.restorePlanId"),
    changeRequestId: requireText(restorePlan.changeRequestId, "restorePlan.changeRequestId"),
    databaseAssetId: requireText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId"),
    operationId,
    status: "verified",
    currentNode: requireObject(restorePlan.currentNode, "restorePlan.currentNode"),
    targetNode,
    baselineBeforeRef: requireText(input.baselineBeforeRef, "baselineBeforeRef"),
    baselineAfterRef: requireText(input.baselineAfterRef, "baselineAfterRef"),
    schemaFingerprint,
    evidenceRef: requireText(input.evidenceRef, "evidenceRef"),
    checks,
    ...rollbackExecutionEvidence,
    verifiedBy: requireText(input.verifiedBy, "verifiedBy"),
    verifiedAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function createRestoreVerificationFromRollbackExecution(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  const rollbackExecution = requireObject(input.rollbackExecution, "rollbackExecution");
  const restoreCheckExecution = requireObject(input.restoreCheckExecution, "restoreCheckExecution");
  validateRollbackExecutionForRestorePlan({ restorePlan, rollbackExecution });
  validateRestoreCheckExecutionForRestorePlan({ restorePlan, restoreCheckExecution });
  return createRestoreVerificationArtifact({
    restorePlan,
    operationId: input.operationId,
    verifiedBy: input.verifiedBy,
    verifiedAt: input.verifiedAt,
    baselineBeforeRef: input.baselineBeforeRef,
    baselineAfterRef: input.baselineAfterRef,
    schemaFingerprint: restoreCheckExecution.targetSchemaFingerprint,
    evidenceRef: input.evidenceRef,
    checks: restoreCheckExecution.checks,
    rollbackExecutionRef: input.rollbackExecutionRef,
    sourceRollbackExecutionFingerprint: rollbackExecution.artifactFingerprint,
    restoreCheckExecutionRef: input.restoreCheckExecutionRef,
    sourceRestoreCheckExecutionFingerprint: restoreCheckExecution.artifactFingerprint,
  });
}

export function createRestoreTimelineNodeFromVerificationArtifact(input) {
  const restoreVerification = requireObject(input.restoreVerification, "restoreVerification");
  if (restoreVerification.schema !== "dosql.restore-verification.v1") {
    throw new Error(`Unsupported restore verification schema: ${restoreVerification.schema}`);
  }
  if (restoreVerification.status !== "verified") {
    throw new Error("restore verification must be verified before finalization");
  }
  const databaseAssetId = requireText(
    restoreVerification.databaseAssetId,
    "restoreVerification.databaseAssetId",
  );
  const currentNode = expandPlanNode(
    restoreVerification.currentNode,
    databaseAssetId,
    "restoreVerification.currentNode",
  );
  const targetNode = expandPlanNode(
    restoreVerification.targetNode,
    databaseAssetId,
    "restoreVerification.targetNode",
  );
  return createRestoreTimelineNode({
    currentNode,
    targetNode,
    operationId: restoreVerification.operationId,
    validFrom: restoreVerification.verifiedAt,
    baselineBeforeRef: restoreVerification.baselineBeforeRef,
    baselineAfterRef: restoreVerification.baselineAfterRef,
    schemaFingerprint: restoreVerification.schemaFingerprint,
    evidenceRef: requireText(input.restoreVerificationRef, "restoreVerificationRef"),
  });
}

export function createRollbackArtifactManifest(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  if (restorePlan.status === "blocked") {
    throw new Error("blocked restore plan cannot render rollback artifacts");
  }
  const plan = requireObject(restorePlan.plan, "restorePlan.plan");
  const steps = requireArray(plan.steps, "restorePlan.plan.steps");
  const suppliedArtifacts = requireArray(input.artifacts, "artifacts");
  const artifacts = steps.map((step) => {
    const requiredKinds = requiredArtifactKindsForMethod(step.method);
    const artifact = suppliedArtifacts.find(
      (candidate) =>
        candidate.timelineNodeId === step.timelineNodeId &&
        requiredKinds.includes(candidate.artifactKind),
    );
    if (!artifact) {
      throw new Error(
        `Missing rollback artifact for ${step.nodeLabel} using ${step.method}; expected ${requiredKinds.join(" or ")}`,
      );
    }
    return sortObject({
      timelineNodeId: step.timelineNodeId,
      nodeLabel: step.nodeLabel,
      method: step.method,
      restoreCapability: step.restoreCapability,
      artifactKind: requireText(artifact.artifactKind, "artifact.artifactKind"),
      artifactRef: requireText(artifact.artifactRef, "artifact.artifactRef"),
      artifactFingerprint: requireText(artifact.artifactFingerprint, "artifact.artifactFingerprint"),
    });
  });
  const createdAt = requireIsoDate(input.createdAt, "createdAt");
  const createdBy = requireText(input.createdBy, "createdBy");
  const base = sortObject({
    schema: "dosql.rollback-artifact-manifest.v1",
    manifestId: `rart_${sha256(
      `${restorePlan.restorePlanId}\u001f${restorePlan.databaseAssetId}\u001f${createdAt}\u001f${stableJson(artifacts)}`,
    ).slice(0, 16)}`,
    restorePlanId: requireText(restorePlan.restorePlanId, "restorePlan.restorePlanId"),
    changeRequestId: requireText(restorePlan.changeRequestId, "restorePlan.changeRequestId"),
    databaseAssetId: requireText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId"),
    sourceRestorePlanFingerprint: requireText(
      restorePlan.artifactFingerprint,
      "restorePlan.artifactFingerprint",
    ),
    status: "ready",
    artifacts,
    createdBy,
    createdAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function createForwardArtifactManifest(input) {
  const changePlan = requireCompareChangePlan(input.changePlan);
  const targetPlan = findCompareTargetPlan({
    changePlan,
    targetDatabaseAssetId: input.targetDatabaseAssetId,
  });
  const timelineNode = requireNode(input.timelineNode, "timelineNode");
  if (timelineNode.databaseAssetId !== targetPlan.databaseAssetId) {
    throw new Error("timelineNode databaseAssetId must match target plan databaseAssetId");
  }
  const suppliedArtifacts = requireArray(input.artifacts, "artifacts");
  const descriptors = requireArray(targetPlan.changeDescriptors, "targetPlan.changeDescriptors");
  if (descriptors.length === 0) {
    throw new Error("targetPlan.changeDescriptors must include at least one descriptor");
  }
  const artifacts = descriptors.map((descriptor, index) => {
    const artifact = suppliedArtifacts.find(
      (candidate) =>
        candidate.timelineNodeId === timelineNode.timelineNodeId &&
        candidate.artifactKind === "forward_sql" &&
        Number(candidate.descriptorIndex) === index,
    );
    if (!artifact) {
      throw new Error(`Missing forward artifact for descriptor ${index + 1}`);
    }
    return sortObject({
      timelineNodeId: timelineNode.timelineNodeId,
      nodeLabel: timelineNode.nodeLabel ?? formatNodeLabel(timelineNode.nodeSequence),
      descriptorIndex: index,
      artifactKind: "forward_sql",
      artifactRef: requireText(artifact.artifactRef, "artifact.artifactRef"),
      artifactFingerprint: requireText(artifact.artifactFingerprint, "artifact.artifactFingerprint"),
    });
  });
  const createdAt = requireIsoDate(input.createdAt, "createdAt");
  const createdBy = requireText(input.createdBy, "createdBy");
  const base = sortObject({
    schema: "dosql.forward-artifact-manifest.v1",
    manifestId: `fart_${sha256(
      `${changePlan.changeRequestId}\u001f${targetPlan.databaseAssetId}\u001f${timelineNode.timelineNodeId}\u001f${createdAt}\u001f${stableJson(artifacts)}`,
    ).slice(0, 16)}`,
    changeRequestId: changePlan.changeRequestId,
    databaseAssetId: targetPlan.databaseAssetId,
    timelineNodeId: timelineNode.timelineNodeId,
    nodeLabel: timelineNode.nodeLabel ?? formatNodeLabel(timelineNode.nodeSequence),
    sourceChangePlanFingerprint: changePlan.artifactFingerprint,
    status: "ready",
    artifacts,
    createdBy,
    createdAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderForwardChangeArtifacts(input) {
  const changePlan = requireCompareChangePlan(input.changePlan);
  const targetPlan = findCompareTargetPlan({
    changePlan,
    targetDatabaseAssetId: input.targetDatabaseAssetId,
  });
  const timelineNode = requireNode(input.timelineNode, "timelineNode");
  if (timelineNode.databaseAssetId !== targetPlan.databaseAssetId) {
    throw new Error("timelineNode databaseAssetId must match target plan databaseAssetId");
  }
  const artifactBaseRef = requireText(input.artifactBaseRef, "artifactBaseRef").replace(/\/+$/g, "");
  const nodeLabel = timelineNode.nodeLabel ?? formatNodeLabel(timelineNode.nodeSequence);
  const artifacts = requireArray(targetPlan.changeDescriptors, "targetPlan.changeDescriptors")
    .map((descriptor, index) => {
      const normalized = normalizeForwardChangeDescriptor(descriptor);
      const artifactText = renderForwardChangeSql(normalized);
      const artifactRef = `${artifactBaseRef}/forward-${nodeLabel}-${String(index + 1).padStart(3, "0")}-${normalized.table}-${normalized.column}.sql`;
      return sortObject({
        timelineNodeId: timelineNode.timelineNodeId,
        nodeLabel,
        descriptorIndex: index,
        artifactKind: "forward_sql",
        artifactRef,
        artifactText,
        artifactFingerprint: `sha256:${sha256(artifactText)}`,
      });
    });
  if (artifacts.length === 0) {
    throw new Error("targetPlan.changeDescriptors must include at least one descriptor");
  }
  return sortObject({ artifacts });
}

export function renderSchemaRollbackArtifacts(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  const plan = requireObject(restorePlan.plan, "restorePlan.plan");
  const steps = requireArray(plan.steps, "restorePlan.plan.steps");
  const artifactBaseRef = requireText(input.artifactBaseRef, "artifactBaseRef").replace(/\/+$/g, "");
  const artifacts = [];
  for (const step of steps) {
    if (step.method !== "derived_rollback_sql") continue;
    const descriptor = requireObject(step.changeDescriptor, "step.changeDescriptor");
    const sql = renderSchemaRollbackSql(descriptor);
    const artifactRef = `${artifactBaseRef}/rollback-${requireText(step.nodeLabel, "step.nodeLabel")}.sql`;
    artifacts.push(
      sortObject({
        timelineNodeId: requireText(step.timelineNodeId, "step.timelineNodeId"),
        nodeLabel: requireText(step.nodeLabel, "step.nodeLabel"),
        artifactKind: "rollback_sql",
        artifactRef,
        artifactText: sql,
        artifactFingerprint: `sha256:${sha256(sql)}`,
      }),
    );
  }
  return sortObject({ artifacts });
}

export function renderDataRollbackArtifacts(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  const plan = requireObject(restorePlan.plan, "restorePlan.plan");
  const steps = requireArray(plan.steps, "restorePlan.plan.steps");
  const beforeImages = requireArray(input.beforeImages, "beforeImages");
  const artifactBaseRef = requireText(input.artifactBaseRef, "artifactBaseRef").replace(/\/+$/g, "");
  const artifacts = [];
  for (const step of steps) {
    if (step.method !== "inverse_data_patch") continue;
    const stepImages = beforeImages.filter((image) => image.timelineNodeId === step.timelineNodeId);
    if (stepImages.length === 0) {
      throw new Error(`Missing before image for ${requireText(step.nodeLabel, "step.nodeLabel")}`);
    }
    const sql = stepImages.map(renderDataRollbackSql).join("\n");
    const artifactRef = `${artifactBaseRef}/rollback-${requireText(step.nodeLabel, "step.nodeLabel")}-data.sql`;
    artifacts.push(
      sortObject({
        timelineNodeId: requireText(step.timelineNodeId, "step.timelineNodeId"),
        nodeLabel: requireText(step.nodeLabel, "step.nodeLabel"),
        artifactKind: "rollback_sql",
        artifactRef,
        artifactText: sql,
        artifactFingerprint: `sha256:${sha256(sql)}`,
      }),
    );
  }
  return sortObject({ artifacts });
}

export function renderSnapshotRestoreArtifacts(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  const plan = requireObject(restorePlan.plan, "restorePlan.plan");
  const steps = requireArray(plan.steps, "restorePlan.plan.steps");
  const restoreEvidence = requireArray(input.restoreEvidence, "restoreEvidence");
  const artifactBaseRef = requireText(input.artifactBaseRef, "artifactBaseRef").replace(/\/+$/g, "");
  const artifacts = [];
  for (const step of steps) {
    if (step.method !== "snapshot_or_pitr_restore") continue;
    const evidence = restoreEvidence.find(
      (candidate) =>
        candidate.timelineNodeId === step.timelineNodeId &&
        SNAPSHOT_RESTORE_ARTIFACT_KINDS.has(candidate.artifactKind),
    );
    if (!evidence) {
      throw new Error(`Missing snapshot or PITR evidence for ${requireText(step.nodeLabel, "step.nodeLabel")}`);
    }
    const payload = renderSnapshotRestoreArtifactPayload(step, evidence);
    const artifactText = JSON.stringify(payload, null, 2);
    const suffix = payload.artifactKind === "snapshot_manifest" ? "snapshot" : "pitr";
    const artifactRef = `${artifactBaseRef}/restore-${requireText(step.nodeLabel, "step.nodeLabel")}-${suffix}.json`;
    artifacts.push(
      sortObject({
        timelineNodeId: requireText(step.timelineNodeId, "step.timelineNodeId"),
        nodeLabel: requireText(step.nodeLabel, "step.nodeLabel"),
        artifactKind: payload.artifactKind,
        artifactRef,
        artifactText,
        artifactFingerprint: `sha256:${sha256(artifactText)}`,
      }),
    );
  }
  return sortObject({ artifacts });
}

function formatNodeLabel(version) {
  return `dosql_${String(Number(version)).padStart(6, "0")}`;
}

function renderSchemaRollbackSql(descriptor) {
  const normalized = normalizeChangeDescriptor(descriptor);
  if (normalized.action === "add_column") {
    return `alter table ${normalized.table} drop column ${normalized.column};`;
  }
  throw new Error(`Unsupported schema rollback descriptor action: ${normalized.action}`);
}

function renderForwardChangeSql(descriptor) {
  if (descriptor.action === "add_column") {
    const nullability = descriptor.nullable ? "" : " not null";
    return `alter table ${descriptor.table} add column ${descriptor.column} ${descriptor.dataType}${nullability};`;
  }
  throw new Error(`Unsupported forward change descriptor action: ${descriptor.action}`);
}

function renderDataRollbackSql(beforeImage) {
  const image = requireObject(beforeImage, "beforeImage");
  const table = requireIdentifier(image.table, "beforeImage.table");
  const primaryKey = requireObject(image.primaryKey, "beforeImage.primaryKey");
  const before = requireObject(image.before, "beforeImage.before");
  const setEntries = Object.entries(before);
  const whereEntries = Object.entries(primaryKey);
  if (setEntries.length === 0) {
    throw new Error("beforeImage.before must include at least one column");
  }
  if (whereEntries.length === 0) {
    throw new Error("beforeImage.primaryKey must include at least one column");
  }
  const assignments = setEntries
    .map(([column, value]) => `${requireIdentifier(column, "beforeImage.before column")} = ${renderSqlLiteral(value)}`)
    .join(", ");
  const predicates = whereEntries
    .map(([column, value]) => `${requireIdentifier(column, "beforeImage.primaryKey column")} = ${renderSqlLiteral(value)}`)
    .join(" and ");
  return `update ${table} set ${assignments} where ${predicates};`;
}

function createBaselineRecordForNode(node, input) {
  const record = requireObject(input, "record");
  const baselineKind = requireBaselineKind(record.baselineKind);
  const capturedAt = requireIsoDate(record.capturedAt, "record.capturedAt");
  const schemaSnapshotRef = requireText(record.schemaSnapshotRef, "record.schemaSnapshotRef");
  validateBaselineRecordRef(node, baselineKind, schemaSnapshotRef);
  const schemaFingerprint = requireText(record.schemaFingerprint, "record.schemaFingerprint");
  const dataScope = requireDataScope(record.dataScope);
  const dataEvidenceRef = record.dataEvidenceRef ? String(record.dataEvidenceRef) : "";
  const artifactFingerprint = requireText(record.artifactFingerprint, "record.artifactFingerprint");
  return sortObject({
    baselineId: `bln_${sha256(
      `${node.databaseAssetId}\u001f${node.timelineNodeId}\u001f${baselineKind}\u001f${capturedAt}\u001f${schemaSnapshotRef}\u001f${schemaFingerprint}\u001f${dataScope}\u001f${dataEvidenceRef}\u001f${artifactFingerprint}`,
    ).slice(0, 16)}`,
    databaseAssetId: node.databaseAssetId,
    timelineNodeId: node.timelineNodeId,
    baselineKind,
    capturedAt,
    schemaSnapshotRef,
    schemaFingerprint,
    dataScope,
    dataEvidenceRef,
    artifactFingerprint,
    createdAt: record.createdAt ? requireIsoDate(record.createdAt, "record.createdAt") : capturedAt,
  });
}

function createStructureSnapshotBaselineRecord(input) {
  const snapshot = requireObject(input.snapshot, "snapshot");
  const asset = requireObject(input.asset, "asset");
  const schemaSnapshotRef = requireText(input.schemaSnapshotRef, "schemaSnapshotRef");
  return {
    baselineKind: requireBaselineKind(input.baselineKind),
    capturedAt: requireIsoDate(snapshot.capturedAt, "snapshot.capturedAt"),
    schemaSnapshotRef,
    schemaFingerprint: requireText(asset.structureFingerprint, "asset.structureFingerprint"),
    dataScope: input.dataScope,
    dataEvidenceRef: input.dataEvidenceRef,
    artifactFingerprint: `sha256:${sha256(
      stableJson({
        databaseAssetId: input.node.databaseAssetId,
        schemaSnapshotRef,
        snapshotFingerprint: snapshot.snapshotFingerprint ?? "",
        capturedAt: snapshot.capturedAt,
        asset,
      }),
    )}`,
  };
}

function createRestoreBaselineRecord(input) {
  const baselineKind = requireBaselineKind(input.baselineKind);
  const schemaSnapshotRef = requireText(input.schemaSnapshotRef, "schemaSnapshotRef");
  const schemaFingerprint = requireText(input.schemaFingerprint, "schemaFingerprint");
  const dataScope = requireDataScope(input.dataScope);
  const dataEvidenceRef = input.dataEvidenceRef ? String(input.dataEvidenceRef) : "";
  const capturedAt = requireIsoDate(input.capturedAt, "capturedAt");
  const restoreVerificationFingerprint = requireText(
    input.restoreVerificationFingerprint,
    "restoreVerificationFingerprint",
  );
  return {
    baselineKind,
    capturedAt,
    schemaSnapshotRef,
    schemaFingerprint,
    dataScope,
    dataEvidenceRef,
    artifactFingerprint: `sha256:${sha256(
      stableJson({
        baselineKind,
        dataEvidenceRef,
        dataScope,
        restoreNodeId: requireText(input.restoreNodeId, "restoreNodeId"),
        restoreVerificationFingerprint,
        restoreVerificationId: requireText(input.restoreVerificationId, "restoreVerificationId"),
        schemaFingerprint,
        schemaSnapshotRef,
      }),
    )}`,
  };
}

function findSnapshotAsset({ snapshot, databaseAssetId, fieldName }) {
  const assets = requireArray(snapshot.assets, `${fieldName}.assets`);
  const asset = assets.find((candidate) => candidate.databaseAssetId === databaseAssetId);
  if (!asset) {
    throw new Error(`${fieldName} does not include database asset: ${databaseAssetId}`);
  }
  return asset;
}

function renderSnapshotRestoreArtifactPayload(step, input) {
  const evidence = requireObject(input, "restoreEvidence");
  const artifactKind = requireSnapshotRestoreArtifactKind(evidence.artifactKind);
  const restoreTargetRef = requireText(evidence.restoreTargetRef, "restoreEvidence.restoreTargetRef");
  const expectedTargetRef = requireText(step.baselineBeforeRef, "step.baselineBeforeRef");
  if (restoreTargetRef !== expectedTargetRef) {
    throw new Error(`restoreTargetRef must match step baselineBeforeRef: ${expectedTargetRef}`);
  }
  return sortObject({
    schema:
      artifactKind === "snapshot_manifest"
        ? "dosql.snapshot-restore-artifact.v1"
        : "dosql.pitr-restore-artifact.v1",
    artifactKind,
    timelineNodeId: requireText(step.timelineNodeId, "step.timelineNodeId"),
    nodeLabel: requireText(step.nodeLabel, "step.nodeLabel"),
    restoreTargetRef,
    evidenceRef: requireText(evidence.evidenceRef, "restoreEvidence.evidenceRef"),
    evidenceFingerprint: requireText(evidence.evidenceFingerprint, "restoreEvidence.evidenceFingerprint"),
    capturedAt: requireIsoDate(evidence.capturedAt, "restoreEvidence.capturedAt"),
  });
}

function normalizeRollbackExecutionEvidence(input) {
  const hasRollbackExecutionRef = input.rollbackExecutionRef !== undefined;
  const hasSourceFingerprint = input.sourceRollbackExecutionFingerprint !== undefined;
  const hasRestoreCheckRef = input.restoreCheckExecutionRef !== undefined;
  const hasRestoreCheckFingerprint = input.sourceRestoreCheckExecutionFingerprint !== undefined;
  if (!hasRollbackExecutionRef && !hasSourceFingerprint && !hasRestoreCheckRef && !hasRestoreCheckFingerprint) {
    return {};
  }
  return {
    ...(hasRollbackExecutionRef || hasSourceFingerprint
      ? {
          rollbackExecutionRef: requireText(input.rollbackExecutionRef, "rollbackExecutionRef"),
          sourceRollbackExecutionFingerprint: requireText(
            input.sourceRollbackExecutionFingerprint,
            "sourceRollbackExecutionFingerprint",
          ),
        }
      : {}),
    ...(hasRestoreCheckRef || hasRestoreCheckFingerprint
      ? {
          restoreCheckExecutionRef: requireText(input.restoreCheckExecutionRef, "restoreCheckExecutionRef"),
          sourceRestoreCheckExecutionFingerprint: requireText(
            input.sourceRestoreCheckExecutionFingerprint,
            "sourceRestoreCheckExecutionFingerprint",
          ),
        }
      : {}),
  };
}

function validateRestoreCheckExecutionForRestorePlan(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  const restoreCheckExecution = requireObject(input.restoreCheckExecution, "restoreCheckExecution");
  if (restoreCheckExecution.schema !== "dosql.restore-check-execution.v1") {
    throw new Error(`Unsupported restore check execution schema: ${restoreCheckExecution.schema}`);
  }
  if (restoreCheckExecution.status !== "verified") {
    throw new Error("restore check execution must be verified");
  }
  const restorePlanId = requireText(restorePlan.restorePlanId, "restorePlan.restorePlanId");
  if (restoreCheckExecution.restorePlanId !== restorePlanId) {
    throw new Error("restoreCheckExecution.restorePlanId must match restorePlan.restorePlanId");
  }
  const changeRequestId = requireText(restorePlan.changeRequestId, "restorePlan.changeRequestId");
  if (restoreCheckExecution.changeRequestId !== changeRequestId) {
    throw new Error("restoreCheckExecution.changeRequestId must match restorePlan.changeRequestId");
  }
  const databaseAssetId = requireText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId");
  if (restoreCheckExecution.databaseAssetId !== databaseAssetId) {
    throw new Error("restoreCheckExecution.databaseAssetId must match restorePlan.databaseAssetId");
  }
  const targetNode = requireObject(restorePlan.targetNode, "restorePlan.targetNode");
  const targetTimelineNodeId = requireText(targetNode.timelineNodeId, "restorePlan.targetNode.timelineNodeId");
  if (restoreCheckExecution.targetTimelineNodeId !== targetTimelineNodeId) {
    throw new Error("restoreCheckExecution.targetTimelineNodeId must match restorePlan target");
  }
  const targetSchemaFingerprint = requireText(
    targetNode.schemaFingerprint,
    "restorePlan.targetNode.schemaFingerprint",
  );
  if (restoreCheckExecution.targetSchemaFingerprint !== targetSchemaFingerprint) {
    throw new Error("restoreCheckExecution.targetSchemaFingerprint must match restorePlan target");
  }
  const checks = requireArray(restoreCheckExecution.checks, "restoreCheckExecution.checks");
  if (Number(restoreCheckExecution.checkCount) !== checks.length) {
    throw new Error("restoreCheckExecution.checkCount must match checks length");
  }
  for (const check of checks) {
    if (check.checkStatus !== "passed") {
      throw new Error(`restore check execution checks must pass: ${check.checkName}`);
    }
  }
}

function validateRollbackExecutionForRestorePlan(input) {
  const restorePlan = requireObject(input.restorePlan, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  const rollbackExecution = requireObject(input.rollbackExecution, "rollbackExecution");
  if (rollbackExecution.schema !== "dosql.rollback-execution.v1") {
    throw new Error(`Unsupported rollback execution schema: ${rollbackExecution.schema}`);
  }
  if (rollbackExecution.status !== "verified") {
    throw new Error("rollback execution must be verified");
  }
  const restorePlanId = requireText(restorePlan.restorePlanId, "restorePlan.restorePlanId");
  if (rollbackExecution.restorePlanId !== restorePlanId) {
    throw new Error("rollbackExecution.restorePlanId must match restorePlan.restorePlanId");
  }
  const changeRequestId = requireText(restorePlan.changeRequestId, "restorePlan.changeRequestId");
  if (rollbackExecution.changeRequestId !== changeRequestId) {
    throw new Error("rollbackExecution.changeRequestId must match restorePlan.changeRequestId");
  }
  const databaseAssetId = requireText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId");
  if (rollbackExecution.databaseAssetId !== databaseAssetId) {
    throw new Error("rollbackExecution.databaseAssetId must match restorePlan.databaseAssetId");
  }
  const plan = requireObject(restorePlan.plan, "restorePlan.plan");
  const steps = requireArray(plan.steps, "restorePlan.plan.steps");
  const executions = requireArray(
    rollbackExecution.artifactExecutions,
    "rollbackExecution.artifactExecutions",
  );
  if (Number(rollbackExecution.executionCount) !== executions.length) {
    throw new Error("rollbackExecution.executionCount must match artifactExecutions length");
  }
  if (executions.length !== steps.length) {
    throw new Error("rollback execution must cover every restore plan step");
  }
  const executionsByNode = new Map(
    executions.map((execution) => [execution.timelineNodeId, execution]),
  );
  for (const step of steps) {
    const timelineNodeId = requireText(step.timelineNodeId, "step.timelineNodeId");
    const execution = requireObject(
      executionsByNode.get(timelineNodeId),
      `rollbackExecution.artifactExecutions.${timelineNodeId}`,
    );
    if (execution.method !== step.method) {
      throw new Error(
        `rollback execution method must match restore plan step: ${timelineNodeId}`,
      );
    }
    if (execution.restoreCapability !== step.restoreCapability) {
      throw new Error(
        `rollback execution restoreCapability must match restore plan step: ${timelineNodeId}`,
      );
    }
    const allowedArtifactKinds = requiredArtifactKindsForMethod(step.method);
    if (!allowedArtifactKinds.includes(execution.artifactKind)) {
      throw new Error(
        `rollback execution artifactKind must match restore plan method ${step.method}: ${timelineNodeId}`,
      );
    }
  }
}

function normalizeRestoreVerificationChecks(value) {
  const checks = requireArray(value, "checks");
  if (checks.length === 0) {
    throw new Error("checks must include at least one restore verification check");
  }
  return checks.map((entry) => {
    const check = requireObject(entry, "check");
    const checkName = requireText(check.checkName, "check.checkName");
    const checkStatus = requireText(check.checkStatus, "check.checkStatus");
    if (checkStatus !== "passed") {
      throw new Error(`restore verification checks must pass: ${checkName}`);
    }
    return sortObject({
      checkName,
      checkStatus,
      expected: check.expected === undefined ? "" : String(check.expected),
      actual: check.actual === undefined ? "" : String(check.actual),
    });
  });
}

function validateBaselineRecordRef(node, baselineKind, schemaSnapshotRef) {
  if (baselineKind === "before") {
    const expected = requireText(node.baselineBeforeRef, "node.baselineBeforeRef");
    if (schemaSnapshotRef !== expected) {
      throw new Error(`baseline before ref must match timeline node baselineBeforeRef: ${expected}`);
    }
    return;
  }
  const expected = requireText(node.baselineAfterRef, "node.baselineAfterRef");
  if (schemaSnapshotRef !== expected) {
    throw new Error(`baseline ${baselineKind} ref must match timeline node baselineAfterRef: ${expected}`);
  }
}

function normalizeChangeDescriptor(descriptor) {
  const value = requireObject(descriptor, "changeDescriptor");
  const action = requireText(value.action, "changeDescriptor.action");
  if (action === "add_column") {
    return {
      action,
      table: requireIdentifier(value.table, "changeDescriptor.table"),
      column: requireIdentifier(value.column, "changeDescriptor.column"),
    };
  }
  throw new Error(`Unsupported changeDescriptor action: ${action}`);
}

function normalizeForwardChangeDescriptor(descriptor) {
  const value = requireObject(descriptor, "changeDescriptor");
  const action = requireText(value.action, "changeDescriptor.action");
  if (action === "add_column") {
    const key = String(value.key ?? "");
    if (key.trim() !== "") {
      throw new Error("Unsupported forward add_column descriptor key");
    }
    return {
      action,
      table: requireIdentifier(value.table, "changeDescriptor.table"),
      column: requireIdentifier(value.column, "changeDescriptor.column"),
      dataType: requireSqlDataType(value.dataType, "changeDescriptor.dataType"),
      nullable: Boolean(value.nullable),
      key,
    };
  }
  throw new Error(`Unsupported forward change descriptor action: ${action}`);
}

function requireCompareChangePlan(value) {
  const changePlan = requireObject(value, "changePlan");
  if (changePlan.schema !== "dosql.compare-change-plan.v1") {
    throw new Error(`Unsupported compare change plan schema: ${changePlan.schema}`);
  }
  requireText(changePlan.changeRequestId, "changePlan.changeRequestId");
  requireText(changePlan.artifactFingerprint, "changePlan.artifactFingerprint");
  requireArray(changePlan.targetPlans, "changePlan.targetPlans");
  return changePlan;
}

function findCompareTargetPlan({ changePlan, targetDatabaseAssetId }) {
  const databaseAssetId = requireText(targetDatabaseAssetId, "targetDatabaseAssetId");
  const targetPlan = requireArray(changePlan.targetPlans, "changePlan.targetPlans")
    .find((candidate) => candidate.databaseAssetId === databaseAssetId);
  if (!targetPlan) {
    throw new Error(`changePlan target plan not found: ${databaseAssetId}`);
  }
  return {
    ...targetPlan,
    databaseAssetId,
    changeDescriptors: requireArray(targetPlan.changeDescriptors, "targetPlan.changeDescriptors"),
  };
}

function requireIdentifier(value, fieldName) {
  const text = requireText(value, fieldName);
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(text)) {
    throw new Error(`${fieldName} must be a SQL identifier`);
  }
  return text;
}

function requireSqlDataType(value, fieldName) {
  const text = requireText(value, fieldName);
  if (!/^[A-Za-z][A-Za-z0-9_]*(?:\([0-9]+(?:,\s*[0-9]+)?\))?(?:\s+[A-Za-z][A-Za-z0-9_]*)*$/.test(text)) {
    throw new Error(`${fieldName} must be a SQL data type`);
  }
  return text.replace(/\s+/g, " ");
}

function requiredArtifactKindsForMethod(method) {
  if (method === "derived_rollback_sql") return ["rollback_sql"];
  if (method === "inverse_data_patch") return ["rollback_sql"];
  if (method === "snapshot_or_pitr_restore") return ["snapshot_manifest", "pitr_marker"];
  if (method === "human_approved_mitigation") return ["restore_report"];
  throw new Error(`Unsupported rollback artifact method: ${method}`);
}

function expandPlanNode(node, databaseAssetId, fieldName) {
  const summary = requireObject(node, fieldName);
  return {
    databaseAssetId,
    timelineNodeId: requireText(summary.timelineNodeId, `${fieldName}.timelineNodeId`),
    nodeSequence: Number(summary.nodeSequence),
    nodeLabel: summary.nodeLabel ?? formatNodeLabel(summary.nodeSequence),
    schemaFingerprint: summary.schemaFingerprint ?? "",
    validFrom: summary.validFrom ?? "",
  };
}

function summarizeNode(node) {
  return {
    timelineNodeId: node.timelineNodeId,
    nodeSequence: Number(node.nodeSequence),
    nodeLabel: node.nodeLabel ?? formatNodeLabel(node.nodeSequence),
    schemaFingerprint: node.schemaFingerprint ?? "",
    validFrom: node.validFrom ?? "",
  };
}

function restoreMethod(capability) {
  const normalized = requireRestoreCapability(capability);
  if (normalized === "schema_reversible") return "derived_rollback_sql";
  if (normalized === "data_patch_reversible") return "inverse_data_patch";
  if (normalized === "snapshot_required") return "snapshot_or_pitr_restore";
  if (normalized === "manual_mitigation") return "human_approved_mitigation";
  if (normalized === "unrestorable") return "refuse";
  throw new Error(`Unsupported restoreCapability: ${capability}`);
}

function requireTimepointStateQueryResult(value) {
  const artifact = requireObject(value, "queryResultArtifact");
  if (artifact.schema !== "dosql.timepoint-state-query-result.v1") {
    throw new Error(`Unsupported timepoint state query result schema: ${artifact.schema}`);
  }
  if (artifact.status !== "resolved") {
    throw new Error("queryResultArtifact status must be resolved");
  }
  requireText(artifact.databaseAssetId, "queryResultArtifact.databaseAssetId");
  requireIsoDate(artifact.timestamp, "queryResultArtifact.timestamp");
  requireText(artifact.artifactFingerprint, "queryResultArtifact.artifactFingerprint");
  requireObject(artifact.timepointState, "queryResultArtifact.timepointState");
  return artifact;
}

function findTimepointStateBaseline({
  baselineRecords,
  schemaSnapshotRef,
  schemaFingerprint,
}) {
  for (const entry of baselineRecords) {
    const record = requireObject(entry, "timepointState.baselineRecords[]");
    const baselineKind = requireBaselineKind(
      recordValue(record, "baselineKind", "baseline_kind"),
    );
    if (!["after", "initial", "drift"].includes(baselineKind)) continue;
    const recordSnapshotRef = requireRecordText(
      record,
      "schemaSnapshotRef",
      "schema_snapshot_ref",
      "baselineRecord.schemaSnapshotRef",
    );
    if (recordSnapshotRef !== schemaSnapshotRef) continue;
    const recordSchemaFingerprint = requireRecordText(
      record,
      "schemaFingerprint",
      "schema_fingerprint",
      "baselineRecord.schemaFingerprint",
    );
    if (recordSchemaFingerprint !== schemaFingerprint) {
      throw new Error("timepoint state baseline schemaFingerprint must match timeline node");
    }
    return sortObject({
      baselineKind,
      capturedAt: requireIsoDate(
        requireRecordText(record, "capturedAt", "captured_at", "baselineRecord.capturedAt"),
        "baselineRecord.capturedAt",
      ),
      schemaSnapshotRef: recordSnapshotRef,
      schemaFingerprint: recordSchemaFingerprint,
      dataScope: requireDataScope(recordValue(record, "dataScope", "data_scope")),
      dataEvidenceRef: optionalRecordText(record, "dataEvidenceRef", "data_evidence_ref") ?? "",
      artifactFingerprint: requireRecordText(
        record,
        "artifactFingerprint",
        "artifact_fingerprint",
        "baselineRecord.artifactFingerprint",
      ),
    });
  }
  throw new Error(`timepoint state baseline is required for ${schemaSnapshotRef}`);
}

function recordValue(record, camelName, snakeName) {
  if (record[camelName] !== undefined) return record[camelName];
  return record[snakeName];
}

function requireRecordText(record, camelName, snakeName, fieldName) {
  return requireText(recordValue(record, camelName, snakeName), fieldName);
}

function optionalRecordText(record, camelName, snakeName) {
  const value = recordValue(record, camelName, snakeName);
  if (value === undefined || value === null || String(value).trim() === "") return null;
  return String(value).trim();
}

function requireIntegerRecord(record, camelName, snakeName, fieldName) {
  const value = Number(recordValue(record, camelName, snakeName));
  if (!Number.isInteger(value)) {
    throw new Error(`${fieldName} must be an integer`);
  }
  return value;
}

function requireNode(value, fieldName) {
  if (!value || typeof value !== "object") {
    throw new Error(`${fieldName} is required`);
  }
  requireText(value.timelineNodeId, `${fieldName}.timelineNodeId`);
  requireText(value.databaseAssetId, `${fieldName}.databaseAssetId`);
  if (!Number.isInteger(Number(value.nodeSequence))) {
    throw new Error(`${fieldName}.nodeSequence must be an integer`);
  }
  return value;
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

function requireRestoreCapability(value) {
  const normalized = requireText(value, "restoreCapability");
  if (!RESTORE_CAPABILITIES.has(normalized)) {
    throw new Error(`Unsupported restoreCapability: ${value}`);
  }
  return normalized;
}

function requireBaselineKind(value) {
  const normalized = requireText(value, "baselineKind");
  if (!BASELINE_KINDS.has(normalized)) {
    throw new Error(`Unsupported baselineKind: ${value}`);
  }
  return normalized;
}

function requireDataScope(value) {
  const normalized = requireText(value, "dataScope");
  if (!DATA_SCOPES.has(normalized)) {
    throw new Error(`Unsupported dataScope: ${value}`);
  }
  return normalized;
}

function requireSnapshotRestoreArtifactKind(value) {
  const normalized = requireText(value, "artifactKind");
  if (!SNAPSHOT_RESTORE_ARTIFACT_KINDS.has(normalized)) {
    throw new Error(`Unsupported snapshot restore artifactKind: ${value}`);
  }
  return normalized;
}

function requireIsoDate(value, fieldName) {
  const text = requireText(value, fieldName);
  if (Number.isNaN(Date.parse(text))) {
    throw new Error(`${fieldName} must be an ISO timestamp`);
  }
  return text;
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
}

function renderSqlLiteral(value) {
  if (value === null) return "null";
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new Error("SQL literal number must be finite");
    return String(value);
  }
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "string") return `'${value.replaceAll("'", "''")}'`;
  throw new Error("SQL literal must be a string, number, boolean or null");
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
