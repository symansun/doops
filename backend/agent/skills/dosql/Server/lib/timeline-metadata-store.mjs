import { createHash } from "node:crypto";

import { projectDatabaseVersionHead } from "./timeline-baseline.mjs";

const RESTORE_PLAN_STATUSES = new Set([
  "planned",
  "blocked",
  "approved",
  "executing",
  "verified",
  "failed",
]);

const TIMELINE_ARTIFACT_KINDS = new Set([
  "forward_sql",
  "rollback_sql",
  "mongodb_command",
  "verification_query",
  "schema_snapshot",
  "before_image",
  "snapshot_manifest",
  "pitr_marker",
  "restore_report",
]);

export function renderTimelineNodeMetadataCommit(input) {
  const node = requireObject(input.node, "node");
  const projection = projectDatabaseVersionHead({
    currentVersion: input.currentVersion,
    nextNode: node,
    updatedBy: input.updatedBy,
    updatedAt: input.updatedAt,
  });
  const sqlText = renderCommitSql({ node, projection });
  const base = sortObject({
    schema: "dosql.timeline-metadata-commit.v1",
    status: "ready",
    databaseAssetId: projection.projectedVersion.databaseAssetId,
    timelineNodeId: node.timelineNodeId,
    sourceProjectionFingerprint: projection.artifactFingerprint,
    projection,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderBaselineRecordsMetadataCommit(input) {
  const recordSet = requireObject(input.recordSet, "recordSet");
  const { databaseAssetId, timelineNodeId, records, sourceRecordSetFingerprint } =
    validateBaselineRecordSet(recordSet);
  const sqlText = renderBaselineRecordsCommitSql(records);
  const base = sortObject({
    schema: "dosql.baseline-records-metadata-commit.v1",
    status: "ready",
    databaseAssetId,
    timelineNodeId,
    recordCount: records.length,
    sourceRecordSetFingerprint,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderTimelineArtifactsMetadataCommit(input) {
  const artifactManifest = validateTimelineArtifactManifest(input.artifactManifest);
  const records = artifactManifest.artifacts.map((artifact) =>
    createTimelineArtifactRecord({
      artifact,
      databaseAssetId: artifactManifest.databaseAssetId,
      createdAt: artifactManifest.createdAt,
    }),
  );
  const sqlText = [
    "begin;",
    renderTimelineArtifactsInsert(records),
    "commit;",
    "",
  ].join("\n");
  const base = sortObject({
    schema: "dosql.timeline-artifacts-metadata-commit.v1",
    status: "ready",
    databaseAssetId: artifactManifest.databaseAssetId,
    recordCount: records.length,
    sourceArtifactManifestFingerprint: artifactManifest.artifactFingerprint,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderChangeMetadataCommit(input) {
  const node = requireObject(input.node, "node");
  const recordSet = requireObject(input.recordSet, "recordSet");
  const projection = projectDatabaseVersionHead({
    currentVersion: input.currentVersion,
    nextNode: node,
    updatedBy: input.updatedBy,
    updatedAt: input.updatedAt,
  });
  const { databaseAssetId, timelineNodeId, records, sourceRecordSetFingerprint } =
    validateBaselineRecordSet(recordSet);
  if (databaseAssetId !== node.databaseAssetId) {
    throw new Error("recordSet databaseAssetId must match node databaseAssetId");
  }
  if (timelineNodeId !== node.timelineNodeId) {
    throw new Error("recordSet timelineNodeId must match node timelineNodeId");
  }
  requireStateBaselineForNode({ node, records });
  const sqlText = [
    "begin;",
    renderTimelineNodeInsert(node),
    renderCurrentHeadUpdate(projection),
    renderBaselineRecordsInsert(records),
    "commit;",
    "",
  ].join("\n");
  const base = sortObject({
    schema: "dosql.change-metadata-commit.v1",
    status: "ready",
    databaseAssetId,
    timelineNodeId,
    recordCount: records.length,
    sourceProjectionFingerprint: projection.artifactFingerprint,
    sourceRecordSetFingerprint,
    projection,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderRestorePlanMetadataCommit(input) {
  const restorePlan = validateRestorePlan(input.restorePlan);
  const sqlText = [
    "begin;",
    renderRestorePlanInsert(restorePlan),
    "commit;",
    "",
  ].join("\n");
  const base = sortObject({
    schema: "dosql.restore-plan-metadata-commit.v1",
    status: "ready",
    restorePlanId: restorePlan.restorePlanId,
    changeRequestId: restorePlan.changeRequestId,
    databaseAssetId: restorePlan.databaseAssetId,
    sourceRestorePlanFingerprint: restorePlan.artifactFingerprint,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderForwardEvidenceMetadataCommit(input) {
  const forwardExecution = validateForwardExecution(input.forwardExecution);
  const forwardExecutionId = deriveForwardExecutionId(forwardExecution);
  const sqlText = [
    "begin;",
    renderForwardExecutionInsert({ forwardExecution, forwardExecutionId }),
    "commit;",
    "",
  ].join("\n");
  const base = sortObject({
    schema: "dosql.forward-evidence-metadata-commit.v1",
    status: "ready",
    forwardExecutionId,
    changeRequestId: forwardExecution.changeRequestId,
    databaseAssetId: forwardExecution.databaseAssetId,
    timelineNodeId: forwardExecution.timelineNodeId,
    sourceForwardExecutionFingerprint: forwardExecution.artifactFingerprint,
    sourceChangePlanFingerprint: forwardExecution.sourceChangePlanFingerprint,
    sourceArtifactManifestFingerprint: forwardExecution.sourceArtifactManifestFingerprint,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderRestoreEvidenceMetadataCommit(input) {
  const rollbackExecution = validateRollbackExecution(input.rollbackExecution);
  const restoreCheckExecution = validateRestoreCheckExecution(input.restoreCheckExecution);
  const restoreVerification = validateRestoreVerification(input.restoreVerification);
  validateRestoreEvidenceBinding({
    rollbackExecution,
    restoreCheckExecution,
    restoreVerification,
  });
  const rollbackExecutionId = deriveRollbackExecutionId(rollbackExecution);
  const sqlText = [
    "begin;",
    renderRollbackExecutionInsert({ rollbackExecution, rollbackExecutionId }),
    renderRestoreCheckExecutionInsert(restoreCheckExecution),
    renderRestoreVerificationInsert({
      restoreVerification,
      rollbackExecutionId,
      restoreCheckExecutionId: restoreCheckExecution.restoreCheckExecutionId,
    }),
    "commit;",
    "",
  ].join("\n");
  const base = sortObject({
    schema: "dosql.restore-evidence-metadata-commit.v1",
    status: "ready",
    restorePlanId: rollbackExecution.restorePlanId,
    changeRequestId: rollbackExecution.changeRequestId,
    databaseAssetId: rollbackExecution.databaseAssetId,
    rollbackExecutionId,
    restoreCheckExecutionId: restoreCheckExecution.restoreCheckExecutionId,
    restoreVerificationId: restoreVerification.restoreVerificationId,
    sourceRollbackExecutionFingerprint: rollbackExecution.artifactFingerprint,
    sourceRestoreCheckExecutionFingerprint: restoreCheckExecution.artifactFingerprint,
    sourceRestoreVerificationFingerprint: restoreVerification.artifactFingerprint,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function renderTimepointStateQuery(input) {
  const databaseAssetId = requireText(input.databaseAssetId, "databaseAssetId");
  const timestamp = requireIsoDate(input.timestamp, "timestamp");
  const sqlText = renderTimepointStateQuerySql({ databaseAssetId, timestamp });
  const base = sortObject({
    schema: "dosql.timepoint-state-query.v1",
    status: "ready",
    databaseAssetId,
    timestamp,
    sqlText,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function renderCommitSql({ node, projection }) {
  return [
    "begin;",
    renderTimelineNodeInsert(node),
    renderCurrentHeadUpdate(projection),
    "commit;",
    "",
  ].join("\n");
}

function renderTimepointStateQuerySql({ databaseAssetId, timestamp }) {
  const databaseLiteral = renderRequiredSqlText(databaseAssetId, "databaseAssetId");
  const timestampLiteral = `${renderRequiredSqlText(timestamp, "timestamp")}::timestamptz`;
  return [
    "with target_node as (",
    "  select *",
    "  from dosql_timeline_nodes",
    `  where database_asset_id = ${databaseLiteral}`,
    "    and state_status = 'verified'",
    `    and valid_from <= ${timestampLiteral}`,
    "  order by valid_from desc, node_sequence desc",
    "  limit 1",
    "), baseline_records as (",
    "  select coalesce(jsonb_agg(to_jsonb(br) order by br.baseline_kind, br.captured_at), '[]'::jsonb) as records",
    "  from dosql_baseline_records br",
    "  join target_node tn on tn.timeline_node_id = br.timeline_node_id",
    "), timeline_artifacts as (",
    "  select coalesce(jsonb_agg(to_jsonb(ta) order by ta.artifact_kind, ta.artifact_ref), '[]'::jsonb) as artifacts",
    "  from dosql_timeline_artifacts ta",
    "  join target_node tn on tn.timeline_node_id = ta.timeline_node_id",
    "), forward_executions as (",
    "  select coalesce(jsonb_agg(to_jsonb(fe) order by fe.executed_at, fe.forward_execution_id), '[]'::jsonb) as records",
    "  from dosql_forward_executions fe",
    "  join target_node tn on tn.timeline_node_id = fe.timeline_node_id",
    "), restore_verifications as (",
    "  select coalesce(jsonb_agg(to_jsonb(rv) order by rv.verified_at, rv.restore_verification_id), '[]'::jsonb) as records",
    "  from dosql_restore_verifications rv",
    "  join target_node tn on tn.database_asset_id = rv.database_asset_id",
    "    and tn.baseline_after_ref = rv.baseline_after_ref",
    "    and tn.schema_fingerprint = rv.schema_fingerprint",
    ")",
    "select jsonb_build_object(",
    `  'databaseAssetId', ${databaseLiteral},`,
    `  'timestamp', ${renderRequiredSqlText(timestamp, "timestamp")},`,
    "  'timelineNode', to_jsonb(tn),",
    "  'baselineRecords', br.records,",
    "  'timelineArtifacts', ta.artifacts,",
    "  'executionEvidence', jsonb_build_object(",
    "    'forwardExecutions', fe.records,",
    "    'restoreVerifications', rv.records",
    "  )",
    ") as timepoint_state",
    "from target_node tn",
    "cross join baseline_records br",
    "cross join timeline_artifacts ta",
    "cross join forward_executions fe",
    "cross join restore_verifications rv;",
    "",
  ].join("\n");
}

function renderBaselineRecordsCommitSql(records) {
  return [
    "begin;",
    renderBaselineRecordsInsert(records),
    "commit;",
    "",
  ].join("\n");
}

function renderTimelineArtifactsInsert(records) {
  return [
    "insert into dosql_timeline_artifacts (",
    "  artifact_id,",
    "  database_asset_id,",
    "  timeline_node_id,",
    "  artifact_kind,",
    "  artifact_ref,",
    "  source_node_ids,",
    "  artifact_fingerprint,",
    "  created_at",
    ") values",
    `${records.map(renderTimelineArtifactValueTuple).join(",\n")};`,
  ].join("\n");
}

function renderTimelineArtifactValueTuple(record) {
  return [
    "(",
    `  ${renderRequiredSqlText(record.artifactId, "record.artifactId")},`,
    `  ${renderRequiredSqlText(record.databaseAssetId, "record.databaseAssetId")},`,
    `  ${renderRequiredSqlText(record.timelineNodeId, "record.timelineNodeId")},`,
    `  ${renderRequiredSqlText(record.artifactKind, "record.artifactKind")},`,
    `  ${renderRequiredSqlText(record.artifactRef, "record.artifactRef")},`,
    `  ${renderJsonbLiteral(record.sourceNodeIds)},`,
    `  ${renderRequiredSqlText(record.artifactFingerprint, "record.artifactFingerprint")},`,
    `  ${renderRequiredSqlText(
      requireIsoDate(record.createdAt, "record.createdAt"),
      "record.createdAt",
    )}`,
    ")",
  ].join("\n");
}

function renderRestorePlanInsert(restorePlan) {
  return [
    "insert into dosql_restore_plans (",
    "  restore_plan_id,",
    "  change_request_id,",
    "  database_asset_id,",
    "  current_node_id,",
    "  target_node_id,",
    "  status,",
    "  plan_json,",
    "  created_by,",
    "  created_at",
    ") values (",
    `  ${renderRequiredSqlText(restorePlan.restorePlanId, "restorePlan.restorePlanId")},`,
    `  ${renderRequiredSqlText(restorePlan.changeRequestId, "restorePlan.changeRequestId")},`,
    `  ${renderRequiredSqlText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId")},`,
    `  ${renderRequiredSqlText(
      restorePlan.currentNode.timelineNodeId,
      "restorePlan.currentNode.timelineNodeId",
    )},`,
    `  ${renderRequiredSqlText(
      restorePlan.targetNode.timelineNodeId,
      "restorePlan.targetNode.timelineNodeId",
    )},`,
    `  ${renderRequiredSqlText(restorePlan.status, "restorePlan.status")},`,
    `  ${renderJsonbLiteral(restorePlan.plan)},`,
    `  ${renderRequiredSqlText(restorePlan.createdBy, "restorePlan.createdBy")},`,
    `  ${renderRequiredSqlText(
      requireIsoDate(restorePlan.createdAt, "restorePlan.createdAt"),
      "restorePlan.createdAt",
    )}`,
    ");",
  ].join("\n");
}

function renderRollbackExecutionInsert({ rollbackExecution, rollbackExecutionId }) {
  return [
    "insert into dosql_rollback_executions (",
    "  rollback_execution_id,",
    "  restore_plan_id,",
    "  change_request_id,",
    "  database_asset_id,",
    "  source_artifact_manifest_fingerprint,",
    "  artifact_executions,",
    "  execution_count,",
    "  connection_ref,",
    "  executed_by,",
    "  executed_at,",
    "  artifact_fingerprint",
    ") values (",
    `  ${renderRequiredSqlText(rollbackExecutionId, "rollbackExecutionId")},`,
    `  ${renderRequiredSqlText(rollbackExecution.restorePlanId, "rollbackExecution.restorePlanId")},`,
    `  ${renderRequiredSqlText(rollbackExecution.changeRequestId, "rollbackExecution.changeRequestId")},`,
    `  ${renderRequiredSqlText(rollbackExecution.databaseAssetId, "rollbackExecution.databaseAssetId")},`,
    `  ${renderRequiredSqlText(
      rollbackExecution.sourceArtifactManifestFingerprint,
      "rollbackExecution.sourceArtifactManifestFingerprint",
    )},`,
    `  ${renderJsonbLiteral(rollbackExecution.artifactExecutions)},`,
    `  ${requireInteger(rollbackExecution.executionCount, "rollbackExecution.executionCount")},`,
    `  ${renderRequiredSqlText(rollbackExecution.connectionRef, "rollbackExecution.connectionRef")},`,
    `  ${renderRequiredSqlText(rollbackExecution.executedBy, "rollbackExecution.executedBy")},`,
    `  ${renderRequiredSqlText(
      requireIsoDate(rollbackExecution.executedAt, "rollbackExecution.executedAt"),
      "rollbackExecution.executedAt",
    )},`,
    `  ${renderRequiredSqlText(rollbackExecution.artifactFingerprint, "rollbackExecution.artifactFingerprint")}`,
    ");",
  ].join("\n");
}

function renderForwardExecutionInsert({ forwardExecution, forwardExecutionId }) {
  return [
    "insert into dosql_forward_executions (",
    "  forward_execution_id,",
    "  change_request_id,",
    "  database_asset_id,",
    "  timeline_node_id,",
    "  source_change_plan_fingerprint,",
    "  source_artifact_manifest_fingerprint,",
    "  artifact_executions,",
    "  execution_count,",
    "  connection_ref,",
    "  executed_by,",
    "  executed_at,",
    "  artifact_fingerprint",
    ") values (",
    `  ${renderRequiredSqlText(forwardExecutionId, "forwardExecutionId")},`,
    `  ${renderRequiredSqlText(forwardExecution.changeRequestId, "forwardExecution.changeRequestId")},`,
    `  ${renderRequiredSqlText(forwardExecution.databaseAssetId, "forwardExecution.databaseAssetId")},`,
    `  ${renderRequiredSqlText(forwardExecution.timelineNodeId, "forwardExecution.timelineNodeId")},`,
    `  ${renderRequiredSqlText(
      forwardExecution.sourceChangePlanFingerprint,
      "forwardExecution.sourceChangePlanFingerprint",
    )},`,
    `  ${renderRequiredSqlText(
      forwardExecution.sourceArtifactManifestFingerprint,
      "forwardExecution.sourceArtifactManifestFingerprint",
    )},`,
    `  ${renderJsonbLiteral(forwardExecution.artifactExecutions)},`,
    `  ${requireInteger(forwardExecution.executionCount, "forwardExecution.executionCount")},`,
    `  ${renderRequiredSqlText(forwardExecution.connectionRef, "forwardExecution.connectionRef")},`,
    `  ${renderRequiredSqlText(forwardExecution.executedBy, "forwardExecution.executedBy")},`,
    `  ${renderRequiredSqlText(
      requireIsoDate(forwardExecution.executedAt, "forwardExecution.executedAt"),
      "forwardExecution.executedAt",
    )},`,
    `  ${renderRequiredSqlText(forwardExecution.artifactFingerprint, "forwardExecution.artifactFingerprint")}`,
    ");",
  ].join("\n");
}

function renderRestoreCheckExecutionInsert(restoreCheckExecution) {
  return [
    "insert into dosql_restore_check_executions (",
    "  restore_check_execution_id,",
    "  restore_plan_id,",
    "  change_request_id,",
    "  database_asset_id,",
    "  target_timeline_node_id,",
    "  target_schema_fingerprint,",
    "  checks,",
    "  check_count,",
    "  connection_ref,",
    "  executed_by,",
    "  executed_at,",
    "  artifact_fingerprint",
    ") values (",
    `  ${renderRequiredSqlText(
      restoreCheckExecution.restoreCheckExecutionId,
      "restoreCheckExecution.restoreCheckExecutionId",
    )},`,
    `  ${renderRequiredSqlText(restoreCheckExecution.restorePlanId, "restoreCheckExecution.restorePlanId")},`,
    `  ${renderRequiredSqlText(restoreCheckExecution.changeRequestId, "restoreCheckExecution.changeRequestId")},`,
    `  ${renderRequiredSqlText(restoreCheckExecution.databaseAssetId, "restoreCheckExecution.databaseAssetId")},`,
    `  ${renderRequiredSqlText(
      restoreCheckExecution.targetTimelineNodeId,
      "restoreCheckExecution.targetTimelineNodeId",
    )},`,
    `  ${renderRequiredSqlText(
      restoreCheckExecution.targetSchemaFingerprint,
      "restoreCheckExecution.targetSchemaFingerprint",
    )},`,
    `  ${renderJsonbLiteral(restoreCheckExecution.checks)},`,
    `  ${requireInteger(restoreCheckExecution.checkCount, "restoreCheckExecution.checkCount")},`,
    `  ${renderRequiredSqlText(restoreCheckExecution.connectionRef, "restoreCheckExecution.connectionRef")},`,
    `  ${renderRequiredSqlText(restoreCheckExecution.executedBy, "restoreCheckExecution.executedBy")},`,
    `  ${renderRequiredSqlText(
      requireIsoDate(restoreCheckExecution.executedAt, "restoreCheckExecution.executedAt"),
      "restoreCheckExecution.executedAt",
    )},`,
    `  ${renderRequiredSqlText(
      restoreCheckExecution.artifactFingerprint,
      "restoreCheckExecution.artifactFingerprint",
    )}`,
    ");",
  ].join("\n");
}

function renderRestoreVerificationInsert({
  restoreVerification,
  rollbackExecutionId,
  restoreCheckExecutionId,
}) {
  return [
    "insert into dosql_restore_verifications (",
    "  restore_verification_id,",
    "  restore_plan_id,",
    "  rollback_execution_id,",
    "  restore_check_execution_id,",
    "  change_request_id,",
    "  database_asset_id,",
    "  baseline_before_ref,",
    "  baseline_after_ref,",
    "  schema_fingerprint,",
    "  checks,",
    "  verified_by,",
    "  verified_at,",
    "  artifact_fingerprint",
    ") values (",
    `  ${renderRequiredSqlText(
      restoreVerification.restoreVerificationId,
      "restoreVerification.restoreVerificationId",
    )},`,
    `  ${renderRequiredSqlText(restoreVerification.restorePlanId, "restoreVerification.restorePlanId")},`,
    `  ${renderRequiredSqlText(rollbackExecutionId, "rollbackExecutionId")},`,
    `  ${renderRequiredSqlText(restoreCheckExecutionId, "restoreCheckExecutionId")},`,
    `  ${renderRequiredSqlText(restoreVerification.changeRequestId, "restoreVerification.changeRequestId")},`,
    `  ${renderRequiredSqlText(restoreVerification.databaseAssetId, "restoreVerification.databaseAssetId")},`,
    `  ${renderRequiredSqlText(restoreVerification.baselineBeforeRef, "restoreVerification.baselineBeforeRef")},`,
    `  ${renderRequiredSqlText(restoreVerification.baselineAfterRef, "restoreVerification.baselineAfterRef")},`,
    `  ${renderRequiredSqlText(restoreVerification.schemaFingerprint, "restoreVerification.schemaFingerprint")},`,
    `  ${renderJsonbLiteral(restoreVerification.checks)},`,
    `  ${renderRequiredSqlText(restoreVerification.verifiedBy, "restoreVerification.verifiedBy")},`,
    `  ${renderRequiredSqlText(
      requireIsoDate(restoreVerification.verifiedAt, "restoreVerification.verifiedAt"),
      "restoreVerification.verifiedAt",
    )},`,
    `  ${renderRequiredSqlText(
      restoreVerification.artifactFingerprint,
      "restoreVerification.artifactFingerprint",
    )}`,
    ");",
  ].join("\n");
}

function renderBaselineRecordsInsert(records) {
  return [
    "insert into dosql_baseline_records (",
    "  baseline_id,",
    "  database_asset_id,",
    "  timeline_node_id,",
    "  baseline_kind,",
    "  captured_at,",
    "  schema_snapshot_ref,",
    "  schema_fingerprint,",
    "  data_scope,",
    "  data_evidence_ref,",
    "  artifact_fingerprint,",
    "  created_at",
    ") values",
    `${records.map(renderBaselineRecordValueTuple).join(",\n")};`,
  ].join("\n");
}

function renderBaselineRecordValueTuple(record) {
  return [
    "(",
    `  ${renderRequiredSqlText(record.baselineId, "record.baselineId")},`,
    `  ${renderRequiredSqlText(record.databaseAssetId, "record.databaseAssetId")},`,
    `  ${renderRequiredSqlText(record.timelineNodeId, "record.timelineNodeId")},`,
    `  ${renderRequiredSqlText(record.baselineKind, "record.baselineKind")},`,
    `  ${renderRequiredSqlText(requireIsoDate(record.capturedAt, "record.capturedAt"), "record.capturedAt")},`,
    `  ${renderRequiredSqlText(record.schemaSnapshotRef, "record.schemaSnapshotRef")},`,
    `  ${renderRequiredSqlText(record.schemaFingerprint, "record.schemaFingerprint")},`,
    `  ${renderRequiredSqlText(record.dataScope, "record.dataScope")},`,
    `  ${renderOptionalSqlText(record.dataEvidenceRef)},`,
    `  ${renderRequiredSqlText(record.artifactFingerprint, "record.artifactFingerprint")},`,
    `  ${renderRequiredSqlText(requireIsoDate(record.createdAt, "record.createdAt"), "record.createdAt")}`,
    ")",
  ].join("\n");
}

function renderTimelineNodeInsert(node) {
  const sequence = requireInteger(node.nodeSequence, "node.nodeSequence");
  return [
    "insert into dosql_timeline_nodes (",
    "  timeline_node_id,",
    "  database_asset_id,",
    "  node_sequence,",
    "  node_label,",
    "  parent_node_id,",
    "  operation_id,",
    "  node_kind,",
    "  state_status,",
    "  valid_from,",
    "  baseline_before_ref,",
    "  baseline_after_ref,",
    "  schema_fingerprint,",
    "  data_checkpoint_ref,",
    "  restore_capability,",
    "  restore_from_node_id,",
    "  restore_target_node_id,",
    "  created_at",
    ") values (",
    `  ${renderRequiredSqlText(node.timelineNodeId, "node.timelineNodeId")},`,
    `  ${renderRequiredSqlText(node.databaseAssetId, "node.databaseAssetId")},`,
    `  ${sequence},`,
    `  ${renderRequiredSqlText(node.nodeLabel, "node.nodeLabel")},`,
    `  ${renderOptionalSqlText(node.parentNodeId)},`,
    `  ${renderOptionalSqlText(node.operationId)},`,
    `  ${renderRequiredSqlText(node.nodeKind, "node.nodeKind")},`,
    `  ${renderRequiredSqlText(node.stateStatus, "node.stateStatus")},`,
    `  ${renderRequiredSqlText(requireIsoDate(node.validFrom, "node.validFrom"), "node.validFrom")},`,
    `  ${renderOptionalSqlText(node.baselineBeforeRef)},`,
    `  ${renderRequiredSqlText(node.baselineAfterRef, "node.baselineAfterRef")},`,
    `  ${renderRequiredSqlText(node.schemaFingerprint, "node.schemaFingerprint")},`,
    `  ${renderOptionalSqlText(node.dataCheckpointRef)},`,
    `  ${renderRequiredSqlText(node.restoreCapability, "node.restoreCapability")},`,
    `  ${renderOptionalSqlText(node.restoreFromNodeId)},`,
    `  ${renderOptionalSqlText(node.restoreTargetNodeId)},`,
    `  ${renderRequiredSqlText(requireIsoDate(node.createdAt, "node.createdAt"), "node.createdAt")}`,
    ");",
  ].join("\n");
}

function renderCurrentHeadUpdate(projection) {
  const previous = requireObject(projection.previousVersion, "projection.previousVersion");
  const projected = requireObject(projection.projectedVersion, "projection.projectedVersion");
  const previousVersion = requireInteger(previous.currentVersion, "projection.previousVersion.currentVersion");
  const projectedVersion = requireInteger(projected.currentVersion, "projection.projectedVersion.currentVersion");
  return [
    "with updated_current_head as (",
    "  update dosql_database_versions",
    `  set current_version = ${projectedVersion},`,
    `      current_label = ${renderRequiredSqlText(projected.currentLabel, "projection.projectedVersion.currentLabel")},`,
    `      current_timeline_node_id = ${renderRequiredSqlText(
      projected.currentTimelineNodeId,
      "projection.projectedVersion.currentTimelineNodeId",
    )},`,
    `      updated_by = ${renderRequiredSqlText(projected.updatedBy, "projection.projectedVersion.updatedBy")},`,
    `      updated_at = ${renderRequiredSqlText(
      requireIsoDate(projected.updatedAt, "projection.projectedVersion.updatedAt"),
      "projection.projectedVersion.updatedAt",
    )}`,
    `  where database_asset_id = ${renderRequiredSqlText(
      previous.databaseAssetId,
      "projection.previousVersion.databaseAssetId",
    )}`,
    `  and current_version = ${previousVersion}`,
    renderPreviousHeadPredicate(previous.currentTimelineNodeId),
    "  returning database_asset_id",
    ")",
    "select case when count(*) = 1 then 1 else 1 / 0 end as dosql_current_head_guard",
    "from updated_current_head;",
  ].join("\n");
}

function renderPreviousHeadPredicate(currentTimelineNodeId) {
  if (
    currentTimelineNodeId === undefined ||
    currentTimelineNodeId === null ||
    String(currentTimelineNodeId).trim() === ""
  ) {
    return "  and current_timeline_node_id is null";
  }
  return `  and current_timeline_node_id = ${renderRequiredSqlText(
    currentTimelineNodeId,
    "projection.previousVersion.currentTimelineNodeId",
  )}`;
}

function validateBaselineRecordForSet({ record, databaseAssetId, timelineNodeId }) {
  const baselineRecord = requireObject(record, "record");
  if (baselineRecord.databaseAssetId !== databaseAssetId) {
    throw new Error("record databaseAssetId must match recordSet databaseAssetId");
  }
  if (baselineRecord.timelineNodeId !== timelineNodeId) {
    throw new Error("record timelineNodeId must match recordSet timelineNodeId");
  }
}

function validateBaselineRecordSet(recordSet) {
  if (recordSet.schema !== "dosql.baseline-record-set.v1") {
    throw new Error(`Unsupported baseline record set schema: ${recordSet.schema}`);
  }
  const databaseAssetId = requireText(recordSet.databaseAssetId, "recordSet.databaseAssetId");
  const timelineNodeId = requireText(recordSet.timelineNodeId, "recordSet.timelineNodeId");
  const records = requireArray(recordSet.records, "recordSet.records");
  if (records.length === 0) {
    throw new Error("recordSet.records must include at least one baseline record");
  }
  for (const record of records) {
    validateBaselineRecordForSet({ record, databaseAssetId, timelineNodeId });
  }
  return {
    databaseAssetId,
    timelineNodeId,
    records,
    sourceRecordSetFingerprint: requireText(
      recordSet.artifactFingerprint,
      "recordSet.artifactFingerprint",
    ),
  };
}

function requireStateBaselineForNode({ node, records }) {
  const baselineAfterRef = requireText(node.baselineAfterRef, "node.baselineAfterRef");
  const schemaFingerprint = requireText(node.schemaFingerprint, "node.schemaFingerprint");
  const allowedBaselineKinds = stateBaselineKindsForNode(node);
  const hasStateBaseline = records.some((record) => {
    const baselineRecord = requireObject(record, "record");
    return (
      allowedBaselineKinds.has(baselineRecord.baselineKind) &&
      baselineRecord.schemaSnapshotRef === baselineAfterRef &&
      baselineRecord.schemaFingerprint === schemaFingerprint
    );
  });
  if (!hasStateBaseline) {
    throw new Error(
      `recordSet must include a state baseline with baselineKind ${[...allowedBaselineKinds].join(
        " or ",
      )} matching node baselineAfterRef and schemaFingerprint`,
    );
  }
}

function stateBaselineKindsForNode(node) {
  const nodeKind = requireText(node.nodeKind, "node.nodeKind");
  if (nodeKind === "baseline") return new Set(["initial"]);
  if (nodeKind === "drift_import") return new Set(["drift"]);
  if (nodeKind === "change" || nodeKind === "restore") return new Set(["after"]);
  throw new Error(`Unsupported nodeKind: ${nodeKind}`);
}

function validateTimelineArtifactManifest(value) {
  const artifactManifest = requireObject(value, "artifactManifest");
  if (
    artifactManifest.schema !== "dosql.rollback-artifact-manifest.v1" &&
    artifactManifest.schema !== "dosql.forward-artifact-manifest.v1"
  ) {
    throw new Error(`Unsupported timeline artifact manifest schema: ${artifactManifest.schema}`);
  }
  if (artifactManifest.status !== "ready") {
    throw new Error("timeline artifact manifest status must be ready");
  }
  const databaseAssetId = requireText(
    artifactManifest.databaseAssetId,
    "artifactManifest.databaseAssetId",
  );
  const artifacts = requireArray(artifactManifest.artifacts, "artifactManifest.artifacts");
  if (artifacts.length === 0) {
    throw new Error("artifactManifest.artifacts must include at least one artifact");
  }
  return {
    ...artifactManifest,
    databaseAssetId,
    artifacts,
    createdAt: requireIsoDate(artifactManifest.createdAt, "artifactManifest.createdAt"),
    artifactFingerprint: requireText(
      artifactManifest.artifactFingerprint,
      "artifactManifest.artifactFingerprint",
    ),
  };
}

function createTimelineArtifactRecord({ artifact, databaseAssetId, createdAt }) {
  const timelineArtifact = requireObject(artifact, "artifact");
  if (
    timelineArtifact.databaseAssetId !== undefined &&
    timelineArtifact.databaseAssetId !== databaseAssetId
  ) {
    throw new Error("artifact databaseAssetId must match artifactManifest databaseAssetId");
  }
  const timelineNodeId = requireText(timelineArtifact.timelineNodeId, "artifact.timelineNodeId");
  const artifactKind = requireTimelineArtifactKind(timelineArtifact.artifactKind);
  const artifactRef = requireText(timelineArtifact.artifactRef, "artifact.artifactRef");
  const artifactFingerprint = requireText(
    timelineArtifact.artifactFingerprint,
    "artifact.artifactFingerprint",
  );
  return sortObject({
    artifactId: `tart_${sha256(
      `${databaseAssetId}\u001f${timelineNodeId}\u001f${artifactKind}\u001f${artifactRef}\u001f${artifactFingerprint}`,
    ).slice(0, 16)}`,
    databaseAssetId,
    timelineNodeId,
    artifactKind,
    artifactRef,
    sourceNodeIds: [timelineNodeId],
    artifactFingerprint,
    createdAt,
  });
}

function requireTimelineArtifactKind(value) {
  const artifactKind = requireText(value, "artifact.artifactKind");
  if (!TIMELINE_ARTIFACT_KINDS.has(artifactKind)) {
    throw new Error(`Unsupported timeline artifact kind: ${artifactKind}`);
  }
  return artifactKind;
}

function validateRestorePlan(value) {
  const restorePlan = requireObject(value, "restorePlan");
  if (restorePlan.schema !== "dosql.restore-plan.v1") {
    throw new Error(`Unsupported restore plan schema: ${restorePlan.schema}`);
  }
  const status = requireText(restorePlan.status, "restorePlan.status");
  if (!RESTORE_PLAN_STATUSES.has(status)) {
    throw new Error(`Unsupported restore plan status: ${status}`);
  }
  const currentNode = requireObject(restorePlan.currentNode, "restorePlan.currentNode");
  const targetNode = requireObject(restorePlan.targetNode, "restorePlan.targetNode");
  const plan = requireObject(restorePlan.plan, "restorePlan.plan");
  const currentNodeId = requireText(
    currentNode.timelineNodeId,
    "restorePlan.currentNode.timelineNodeId",
  );
  const targetNodeId = requireText(
    targetNode.timelineNodeId,
    "restorePlan.targetNode.timelineNodeId",
  );
  if (plan.currentNodeId !== currentNodeId) {
    throw new Error("restorePlan.plan.currentNodeId must match restorePlan.currentNode.timelineNodeId");
  }
  if (plan.targetNodeId !== targetNodeId) {
    throw new Error("restorePlan.plan.targetNodeId must match restorePlan.targetNode.timelineNodeId");
  }
  return {
    ...restorePlan,
    restorePlanId: requireText(restorePlan.restorePlanId, "restorePlan.restorePlanId"),
    changeRequestId: requireText(restorePlan.changeRequestId, "restorePlan.changeRequestId"),
    databaseAssetId: requireText(restorePlan.databaseAssetId, "restorePlan.databaseAssetId"),
    status,
    currentNode,
    targetNode,
    plan,
    createdBy: requireText(restorePlan.createdBy, "restorePlan.createdBy"),
    createdAt: requireIsoDate(restorePlan.createdAt, "restorePlan.createdAt"),
    artifactFingerprint: requireText(
      restorePlan.artifactFingerprint,
      "restorePlan.artifactFingerprint",
    ),
  };
}

function validateRollbackExecution(value) {
  const rollbackExecution = requireObject(value, "rollbackExecution");
  if (rollbackExecution.schema !== "dosql.rollback-execution.v1") {
    throw new Error(`Unsupported rollback execution schema: ${rollbackExecution.schema}`);
  }
  if (rollbackExecution.status !== "verified") {
    throw new Error("rollback execution must be verified");
  }
  const artifactExecutions = requireArray(
    rollbackExecution.artifactExecutions,
    "rollbackExecution.artifactExecutions",
  );
  if (artifactExecutions.length === 0) {
    throw new Error("rollbackExecution.artifactExecutions must include at least one execution");
  }
  if (
    requireInteger(rollbackExecution.executionCount, "rollbackExecution.executionCount") !==
    artifactExecutions.length
  ) {
    throw new Error("rollbackExecution.executionCount must match artifactExecutions length");
  }
  return {
    ...rollbackExecution,
    restorePlanId: requireText(rollbackExecution.restorePlanId, "rollbackExecution.restorePlanId"),
    changeRequestId: requireText(rollbackExecution.changeRequestId, "rollbackExecution.changeRequestId"),
    databaseAssetId: requireText(rollbackExecution.databaseAssetId, "rollbackExecution.databaseAssetId"),
    sourceArtifactManifestFingerprint: requireText(
      rollbackExecution.sourceArtifactManifestFingerprint,
      "rollbackExecution.sourceArtifactManifestFingerprint",
    ),
    artifactExecutions,
    connectionRef: requireText(rollbackExecution.connectionRef, "rollbackExecution.connectionRef"),
    executedBy: requireText(rollbackExecution.executedBy, "rollbackExecution.executedBy"),
    executedAt: requireIsoDate(rollbackExecution.executedAt, "rollbackExecution.executedAt"),
    artifactFingerprint: requireText(
      rollbackExecution.artifactFingerprint,
      "rollbackExecution.artifactFingerprint",
    ),
  };
}

function validateForwardExecution(value) {
  const forwardExecution = requireObject(value, "forwardExecution");
  if (forwardExecution.schema !== "dosql.forward-execution.v1") {
    throw new Error(`Unsupported forward execution schema: ${forwardExecution.schema}`);
  }
  if (forwardExecution.status !== "verified") {
    throw new Error("forward execution must be verified");
  }
  const artifactExecutions = requireArray(
    forwardExecution.artifactExecutions,
    "forwardExecution.artifactExecutions",
  );
  if (artifactExecutions.length === 0) {
    throw new Error("forwardExecution.artifactExecutions must include at least one execution");
  }
  if (
    requireInteger(forwardExecution.executionCount, "forwardExecution.executionCount") !==
    artifactExecutions.length
  ) {
    throw new Error("forwardExecution.executionCount must match artifactExecutions length");
  }
  return {
    ...forwardExecution,
    changeRequestId: requireText(forwardExecution.changeRequestId, "forwardExecution.changeRequestId"),
    databaseAssetId: requireText(forwardExecution.databaseAssetId, "forwardExecution.databaseAssetId"),
    timelineNodeId: requireText(forwardExecution.timelineNodeId, "forwardExecution.timelineNodeId"),
    sourceChangePlanFingerprint: requireText(
      forwardExecution.sourceChangePlanFingerprint,
      "forwardExecution.sourceChangePlanFingerprint",
    ),
    sourceArtifactManifestFingerprint: requireText(
      forwardExecution.sourceArtifactManifestFingerprint,
      "forwardExecution.sourceArtifactManifestFingerprint",
    ),
    artifactExecutions,
    connectionRef: requireText(forwardExecution.connectionRef, "forwardExecution.connectionRef"),
    executedBy: requireText(forwardExecution.executedBy, "forwardExecution.executedBy"),
    executedAt: requireIsoDate(forwardExecution.executedAt, "forwardExecution.executedAt"),
    artifactFingerprint: requireText(
      forwardExecution.artifactFingerprint,
      "forwardExecution.artifactFingerprint",
    ),
  };
}

function validateRestoreCheckExecution(value) {
  const restoreCheckExecution = requireObject(value, "restoreCheckExecution");
  if (restoreCheckExecution.schema !== "dosql.restore-check-execution.v1") {
    throw new Error(`Unsupported restore check execution schema: ${restoreCheckExecution.schema}`);
  }
  if (restoreCheckExecution.status !== "verified") {
    throw new Error("restore check execution must be verified");
  }
  const checks = requireArray(restoreCheckExecution.checks, "restoreCheckExecution.checks");
  if (checks.length === 0) {
    throw new Error("restoreCheckExecution.checks must include at least one check");
  }
  if (
    requireInteger(restoreCheckExecution.checkCount, "restoreCheckExecution.checkCount") !==
    checks.length
  ) {
    throw new Error("restoreCheckExecution.checkCount must match checks length");
  }
  return {
    ...restoreCheckExecution,
    restoreCheckExecutionId: requireText(
      restoreCheckExecution.restoreCheckExecutionId,
      "restoreCheckExecution.restoreCheckExecutionId",
    ),
    restorePlanId: requireText(
      restoreCheckExecution.restorePlanId,
      "restoreCheckExecution.restorePlanId",
    ),
    changeRequestId: requireText(
      restoreCheckExecution.changeRequestId,
      "restoreCheckExecution.changeRequestId",
    ),
    databaseAssetId: requireText(
      restoreCheckExecution.databaseAssetId,
      "restoreCheckExecution.databaseAssetId",
    ),
    targetTimelineNodeId: requireText(
      restoreCheckExecution.targetTimelineNodeId,
      "restoreCheckExecution.targetTimelineNodeId",
    ),
    targetSchemaFingerprint: requireText(
      restoreCheckExecution.targetSchemaFingerprint,
      "restoreCheckExecution.targetSchemaFingerprint",
    ),
    checks,
    connectionRef: requireText(
      restoreCheckExecution.connectionRef,
      "restoreCheckExecution.connectionRef",
    ),
    executedBy: requireText(restoreCheckExecution.executedBy, "restoreCheckExecution.executedBy"),
    executedAt: requireIsoDate(restoreCheckExecution.executedAt, "restoreCheckExecution.executedAt"),
    artifactFingerprint: requireText(
      restoreCheckExecution.artifactFingerprint,
      "restoreCheckExecution.artifactFingerprint",
    ),
  };
}

function validateRestoreVerification(value) {
  const restoreVerification = requireObject(value, "restoreVerification");
  if (restoreVerification.schema !== "dosql.restore-verification.v1") {
    throw new Error(`Unsupported restore verification schema: ${restoreVerification.schema}`);
  }
  if (restoreVerification.status !== "verified") {
    throw new Error("restore verification must be verified");
  }
  const checks = requireArray(restoreVerification.checks, "restoreVerification.checks");
  if (checks.length === 0) {
    throw new Error("restoreVerification.checks must include at least one check");
  }
  return {
    ...restoreVerification,
    restoreVerificationId: requireText(
      restoreVerification.restoreVerificationId,
      "restoreVerification.restoreVerificationId",
    ),
    restorePlanId: requireText(
      restoreVerification.restorePlanId,
      "restoreVerification.restorePlanId",
    ),
    changeRequestId: requireText(
      restoreVerification.changeRequestId,
      "restoreVerification.changeRequestId",
    ),
    databaseAssetId: requireText(
      restoreVerification.databaseAssetId,
      "restoreVerification.databaseAssetId",
    ),
    baselineBeforeRef: requireText(
      restoreVerification.baselineBeforeRef,
      "restoreVerification.baselineBeforeRef",
    ),
    baselineAfterRef: requireText(
      restoreVerification.baselineAfterRef,
      "restoreVerification.baselineAfterRef",
    ),
    schemaFingerprint: requireText(
      restoreVerification.schemaFingerprint,
      "restoreVerification.schemaFingerprint",
    ),
    checks,
    sourceRollbackExecutionFingerprint: requireText(
      restoreVerification.sourceRollbackExecutionFingerprint,
      "restoreVerification.sourceRollbackExecutionFingerprint",
    ),
    sourceRestoreCheckExecutionFingerprint: requireText(
      restoreVerification.sourceRestoreCheckExecutionFingerprint,
      "restoreVerification.sourceRestoreCheckExecutionFingerprint",
    ),
    verifiedBy: requireText(restoreVerification.verifiedBy, "restoreVerification.verifiedBy"),
    verifiedAt: requireIsoDate(restoreVerification.verifiedAt, "restoreVerification.verifiedAt"),
    artifactFingerprint: requireText(
      restoreVerification.artifactFingerprint,
      "restoreVerification.artifactFingerprint",
    ),
  };
}

function validateRestoreEvidenceBinding({
  rollbackExecution,
  restoreCheckExecution,
  restoreVerification,
}) {
  validateSameField({
    left: rollbackExecution,
    right: restoreCheckExecution,
    rightName: "restoreCheckExecution",
    fieldName: "restorePlanId",
  });
  validateSameField({
    left: rollbackExecution,
    right: restoreCheckExecution,
    rightName: "restoreCheckExecution",
    fieldName: "changeRequestId",
  });
  validateSameField({
    left: rollbackExecution,
    right: restoreCheckExecution,
    rightName: "restoreCheckExecution",
    fieldName: "databaseAssetId",
  });
  validateSameField({
    left: rollbackExecution,
    right: restoreVerification,
    rightName: "restoreVerification",
    fieldName: "restorePlanId",
  });
  validateSameField({
    left: rollbackExecution,
    right: restoreVerification,
    rightName: "restoreVerification",
    fieldName: "changeRequestId",
  });
  validateSameField({
    left: rollbackExecution,
    right: restoreVerification,
    rightName: "restoreVerification",
    fieldName: "databaseAssetId",
  });
  if (
    restoreVerification.sourceRollbackExecutionFingerprint !==
    rollbackExecution.artifactFingerprint
  ) {
    throw new Error(
      "restoreVerification.sourceRollbackExecutionFingerprint must match rollbackExecution.artifactFingerprint",
    );
  }
  if (
    restoreVerification.sourceRestoreCheckExecutionFingerprint !==
    restoreCheckExecution.artifactFingerprint
  ) {
    throw new Error(
      "restoreVerification.sourceRestoreCheckExecutionFingerprint must match restoreCheckExecution.artifactFingerprint",
    );
  }
  if (restoreVerification.schemaFingerprint !== restoreCheckExecution.targetSchemaFingerprint) {
    throw new Error(
      "restoreVerification.schemaFingerprint must match restoreCheckExecution.targetSchemaFingerprint",
    );
  }
  if (stableJson(restoreVerification.checks) !== stableJson(restoreCheckExecution.checks)) {
    throw new Error("restoreVerification.checks must match restoreCheckExecution.checks");
  }
}

function validateSameField({ left, right, rightName, fieldName }) {
  if (right[fieldName] !== left[fieldName]) {
    throw new Error(`${rightName}.${fieldName} must match rollbackExecution.${fieldName}`);
  }
}

function deriveRollbackExecutionId(rollbackExecution) {
  return `rex_${sha256(
    requireText(rollbackExecution.artifactFingerprint, "rollbackExecution.artifactFingerprint"),
  ).slice(0, 16)}`;
}

function deriveForwardExecutionId(forwardExecution) {
  return `fex_${sha256(
    requireText(forwardExecution.artifactFingerprint, "forwardExecution.artifactFingerprint"),
  ).slice(0, 16)}`;
}

function renderRequiredSqlText(value, fieldName) {
  return renderSqlLiteral(requireText(value, fieldName));
}

function renderOptionalSqlText(value) {
  if (value === undefined || value === null || String(value).trim() === "") return "null";
  return renderSqlLiteral(String(value).trim());
}

function renderSqlLiteral(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function renderJsonbLiteral(value) {
  return `${renderSqlLiteral(stableJson(value))}::jsonb`;
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
