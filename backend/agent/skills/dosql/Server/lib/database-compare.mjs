import { createHash } from "node:crypto";

const AUTO_CHANGE_ACTIONS = new Set(["add_column"]);

export function compareDatabaseStructures(input) {
  const structureSnapshots = requireArray(input.structureSnapshots, "structureSnapshots");
  const referenceDatabaseAssetId = requireText(
    input.referenceDatabaseAssetId,
    "referenceDatabaseAssetId",
  );
  const targetDatabaseAssetIds = requireArray(
    input.targetDatabaseAssetIds,
    "targetDatabaseAssetIds",
  ).map((assetId) => requireText(assetId, "targetDatabaseAssetIds[]"));
  const comparedAt = requireIsoDate(input.comparedAt, "comparedAt");
  const comparedBy = requireText(input.comparedBy, "comparedBy");
  const assets = structureSnapshots.flatMap((snapshot) =>
    requireArray(snapshot.assets, "structureSnapshot.assets").map((asset) => ({
      ...asset,
      sourceSnapshotFingerprint: requireText(
        snapshot.snapshotFingerprint,
        "structureSnapshot.snapshotFingerprint",
      ),
      sourceCapturedAt: requireIsoDate(snapshot.capturedAt, "structureSnapshot.capturedAt"),
    })),
  );
  const referenceAsset = findAsset(assets, referenceDatabaseAssetId, "referenceDatabaseAssetId");
  const targetOrder = new Map(
    targetDatabaseAssetIds.map((databaseAssetId, index) => [databaseAssetId, index]),
  );
  const differences = targetDatabaseAssetIds.flatMap((targetDatabaseAssetId) => {
    const targetAsset = findAsset(assets, targetDatabaseAssetId, "targetDatabaseAssetIds[]");
    return compareAssetToReference({ referenceAsset, targetAsset });
  }).sort((left, right) => compareDifferences({ left, right, targetOrder }));
  const base = sortObject({
    schema: "dosql.database-comparison.v1",
    status: "ready",
    comparisonId: `dcmp_${sha256(
      `${referenceDatabaseAssetId}\u001f${targetDatabaseAssetIds.join(",")}\u001f${comparedAt}`,
    ).slice(0, 16)}`,
    referenceDatabaseAssetId,
    targetDatabaseAssetIds,
    comparedAt,
    comparedBy,
    sourceSnapshotFingerprints: [
      ...new Set(assets.map((asset) => asset.sourceSnapshotFingerprint)),
    ].sort(),
    differences,
    differenceCount: differences.length,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

export function deriveChangePlanFromComparison(input) {
  const comparison = requireComparison(input.comparison);
  const changeRequestId = requireText(input.changeRequestId, "changeRequestId");
  const createdBy = requireText(input.createdBy, "createdBy");
  const createdAt = requireIsoDate(input.createdAt, "createdAt");
  const targetPlans = comparison.targetDatabaseAssetIds.map((databaseAssetId) => {
    const targetDifferences = comparison.differences.filter(
      (difference) => difference.targetDatabaseAssetId === databaseAssetId,
    );
    const changeDescriptors = targetDifferences
      .map((difference) => difference.suggestedAction)
      .filter((action) => AUTO_CHANGE_ACTIONS.has(action?.action))
      .map((action) => sortObject(action.changeDescriptor));
    const manualDifferences = targetDifferences
      .filter((difference) => !AUTO_CHANGE_ACTIONS.has(difference.suggestedAction?.action))
      .map(stripDifferenceForPlan);
    return sortObject({
      databaseAssetId,
      changeDescriptors,
      manualDifferences,
    });
  });
  const base = sortObject({
    schema: "dosql.compare-change-plan.v1",
    status: "draft",
    changeRequestId,
    sourceComparisonId: comparison.comparisonId,
    sourceComparisonFingerprint: comparison.artifactFingerprint,
    referenceDatabaseAssetId: comparison.referenceDatabaseAssetId,
    targetPlans,
    createdBy,
    createdAt,
  });
  return sortObject({
    ...base,
    artifactFingerprint: `sha256:${sha256(stableJson(base))}`,
  });
}

function compareAssetToReference({ referenceAsset, targetAsset }) {
  if (referenceAsset.engine !== targetAsset.engine) {
    return [
      createDifference({
        referenceAsset,
        targetAsset,
        differenceKind: "engine_mismatch",
        objectKind: "database",
        path: "$",
        riskLevel: "high",
        suggestedAction: {
          action: "manual_review",
          reason: "Cross-engine database comparison is report-only in this version.",
        },
      }),
    ];
  }
  if (referenceAsset.engine === "mongodb") {
    return compareCollections({ referenceAsset, targetAsset });
  }
  return compareTables({ referenceAsset, targetAsset });
}

function compareDifferences({ left, right, targetOrder }) {
  const leftTarget = targetOrder.get(left.targetDatabaseAssetId) ?? Number.MAX_SAFE_INTEGER;
  const rightTarget = targetOrder.get(right.targetDatabaseAssetId) ?? Number.MAX_SAFE_INTEGER;
  if (leftTarget !== rightTarget) return leftTarget - rightTarget;
  const leftPriority = differencePriority(left.differenceKind);
  const rightPriority = differencePriority(right.differenceKind);
  if (leftPriority !== rightPriority) return leftPriority - rightPriority;
  return left.path.localeCompare(right.path);
}

function differencePriority(kind) {
  if (kind === "engine_mismatch") return 0;
  if (kind === "missing_table") return 10;
  if (kind === "extra_table") return 11;
  if (kind === "missing_collection") return 12;
  if (kind === "extra_collection") return 13;
  if (kind === "missing_column") return 20;
  if (kind === "extra_column") return 21;
  if (kind === "column_type_changed") return 30;
  if (kind === "nullable_changed") return 31;
  if (kind === "key_changed") return 32;
  return 100;
}

function compareTables({ referenceAsset, targetAsset }) {
  const referenceTables = objectsByName(referenceAsset, "table");
  const targetTables = objectsByName(targetAsset, "table");
  const differences = [];
  for (const [tableName, referenceTable] of referenceTables) {
    const targetTable = targetTables.get(tableName);
    if (!targetTable) {
      differences.push(
        createDifference({
          referenceAsset,
          targetAsset,
          differenceKind: "missing_table",
          objectKind: "table",
          path: `table.${tableName}`,
          riskLevel: "medium",
          referenceValue: summarizeTable(referenceTable),
          suggestedAction: {
            action: "manual_review",
            reason: "Table creation needs explicit review before SQL is derived.",
          },
        }),
      );
      continue;
    }
    differences.push(
      ...compareColumns({ referenceAsset, targetAsset, tableName, referenceTable, targetTable }),
    );
  }
  for (const [tableName, targetTable] of targetTables) {
    if (referenceTables.has(tableName)) continue;
    differences.push(
      createDifference({
        referenceAsset,
        targetAsset,
        differenceKind: "extra_table",
        objectKind: "table",
        path: `table.${tableName}`,
        riskLevel: "high",
        targetValue: summarizeTable(targetTable),
        suggestedAction: {
          action: "manual_review",
          reason: "Dropping extra tables is destructive and requires manual mitigation.",
        },
      }),
    );
  }
  return differences;
}

function compareColumns({ referenceAsset, targetAsset, tableName, referenceTable, targetTable }) {
  const referenceColumns = columnsByName(referenceTable);
  const targetColumns = columnsByName(targetTable);
  const differences = [];
  for (const [columnName, referenceColumn] of referenceColumns) {
    const targetColumn = targetColumns.get(columnName);
    if (!targetColumn) {
      differences.push(
        createDifference({
          referenceAsset,
          targetAsset,
          differenceKind: "missing_column",
          objectKind: "column",
          path: `table.${tableName}.column.${columnName}`,
          riskLevel: "low",
          referenceValue: summarizeColumn(referenceColumn),
          suggestedAction: {
            action: "add_column",
            changeDescriptor: {
              action: "add_column",
              table: tableName,
              column: columnName,
              dataType: requireText(referenceColumn.dataType, "referenceColumn.dataType"),
              nullable: Boolean(referenceColumn.nullable),
              key: String(referenceColumn.key ?? ""),
            },
          },
        }),
      );
      continue;
    }
    addColumnAttributeDifferences({
      differences,
      referenceAsset,
      targetAsset,
      tableName,
      columnName,
      referenceColumn,
      targetColumn,
    });
  }
  for (const [columnName, targetColumn] of targetColumns) {
    if (referenceColumns.has(columnName)) continue;
    differences.push(
      createDifference({
        referenceAsset,
        targetAsset,
        differenceKind: "extra_column",
        objectKind: "column",
        path: `table.${tableName}.column.${columnName}`,
        riskLevel: "high",
        targetValue: summarizeColumn(targetColumn),
        suggestedAction: {
          action: "manual_review",
          reason: "Dropping extra columns can lose data and requires manual mitigation.",
        },
      }),
    );
  }
  return differences;
}

function addColumnAttributeDifferences({
  differences,
  referenceAsset,
  targetAsset,
  tableName,
  columnName,
  referenceColumn,
  targetColumn,
}) {
  const checks = [
    ["column_type_changed", "dataType", "high"],
    ["nullable_changed", "nullable", "medium"],
    ["key_changed", "key", "high"],
  ];
  for (const [differenceKind, fieldName, riskLevel] of checks) {
    const referenceValue = normalizeComparableValue(referenceColumn[fieldName]);
    const targetValue = normalizeComparableValue(targetColumn[fieldName]);
    if (referenceValue === targetValue) continue;
    differences.push(
      createDifference({
        referenceAsset,
        targetAsset,
        differenceKind,
        objectKind: "column",
        path: `table.${tableName}.column.${columnName}.${fieldName}`,
        riskLevel,
        referenceValue,
        targetValue,
        suggestedAction: {
          action: "manual_review",
          reason: "Column attribute changes require explicit approval before a migration is derived.",
        },
      }),
    );
  }
}

function compareCollections({ referenceAsset, targetAsset }) {
  const referenceCollections = objectsByName(referenceAsset, "collection");
  const targetCollections = objectsByName(targetAsset, "collection");
  const differences = [];
  for (const [collectionName, referenceCollection] of referenceCollections) {
    if (targetCollections.has(collectionName)) continue;
    differences.push(
      createDifference({
        referenceAsset,
        targetAsset,
        differenceKind: "missing_collection",
        objectKind: "collection",
        path: `collection.${collectionName}`,
        riskLevel: "low",
        referenceValue: summarizeCollection(referenceCollection),
        suggestedAction: {
          action: "manual_review",
          reason: "MongoDB collection creation needs explicit review in this version.",
        },
      }),
    );
  }
  for (const [collectionName, targetCollection] of targetCollections) {
    if (referenceCollections.has(collectionName)) continue;
    differences.push(
      createDifference({
        referenceAsset,
        targetAsset,
        differenceKind: "extra_collection",
        objectKind: "collection",
        path: `collection.${collectionName}`,
        riskLevel: "high",
        targetValue: summarizeCollection(targetCollection),
        suggestedAction: {
          action: "manual_review",
          reason: "Dropping extra MongoDB collections can lose data and requires manual mitigation.",
        },
      }),
    );
  }
  return differences;
}

function createDifference({
  referenceAsset,
  targetAsset,
  differenceKind,
  objectKind,
  path,
  riskLevel,
  referenceValue = null,
  targetValue = null,
  suggestedAction,
}) {
  const seed = stableJson({
    referenceDatabaseAssetId: referenceAsset.databaseAssetId,
    targetDatabaseAssetId: targetAsset.databaseAssetId,
    differenceKind,
    path,
  });
  return sortObject({
    differenceId: `diff_${sha256(seed).slice(0, 16)}`,
    referenceDatabaseAssetId: referenceAsset.databaseAssetId,
    targetDatabaseAssetId: targetAsset.databaseAssetId,
    engine: targetAsset.engine,
    differenceKind,
    objectKind,
    path,
    riskLevel,
    referenceValue,
    targetValue,
    suggestedAction,
  });
}

function findAsset(assets, databaseAssetId, fieldName) {
  const asset = assets.find((candidate) => candidate.databaseAssetId === databaseAssetId);
  if (!asset) throw new Error(`${fieldName} not found: ${databaseAssetId}`);
  return requireAsset(asset, fieldName);
}

function requireAsset(asset, fieldName) {
  requireText(asset.databaseAssetId, `${fieldName}.databaseAssetId`);
  requireText(asset.engine, `${fieldName}.engine`);
  requireArray(asset.objects, `${fieldName}.objects`);
  return asset;
}

function requireComparison(value) {
  const comparison = requireObject(value, "comparison");
  if (comparison.schema !== "dosql.database-comparison.v1") {
    throw new Error(`Unsupported comparison schema: ${comparison.schema}`);
  }
  requireText(comparison.artifactFingerprint, "comparison.artifactFingerprint");
  requireArray(comparison.differences, "comparison.differences");
  requireArray(comparison.targetDatabaseAssetIds, "comparison.targetDatabaseAssetIds");
  return comparison;
}

function objectsByName(asset, kind) {
  return new Map(
    requireArray(asset.objects, "asset.objects")
      .filter((object) => object.kind === kind)
      .map((object) => [requireText(object.name, `${kind}.name`), object]),
  );
}

function columnsByName(table) {
  return new Map(
    requireArray(table.columns, "table.columns").map((column) => [
      requireText(column.name, "column.name"),
      column,
    ]),
  );
}

function summarizeTable(table) {
  return sortObject({
    name: requireText(table.name, "table.name"),
    columns: requireArray(table.columns, "table.columns").map(summarizeColumn),
  });
}

function summarizeColumn(column) {
  return sortObject({
    name: requireText(column.name, "column.name"),
    dataType: requireText(column.dataType, "column.dataType"),
    nullable: Boolean(column.nullable),
    key: String(column.key ?? ""),
  });
}

function summarizeCollection(collection) {
  return sortObject({
    name: requireText(collection.name, "collection.name"),
    database: String(collection.database ?? ""),
  });
}

function stripDifferenceForPlan(difference) {
  return sortObject({
    differenceId: difference.differenceId,
    differenceKind: difference.differenceKind,
    objectKind: difference.objectKind,
    path: difference.path,
    riskLevel: difference.riskLevel,
    suggestedAction: difference.suggestedAction,
  });
}

function normalizeComparableValue(value) {
  if (typeof value === "boolean") return value;
  if (value === undefined || value === null) return "";
  return String(value);
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
