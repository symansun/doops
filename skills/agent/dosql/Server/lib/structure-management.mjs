import { createHash } from "node:crypto";

import {
  createDatabaseNameMetadata,
  normalizeDatabaseEngine,
} from "./database-discovery.mjs";

export function buildDatabaseAssetInventory({ projectId, environmentId, probe, naming = {} }) {
  requireText(projectId, "projectId");
  requireText(environmentId, "environmentId");
  const target = normalizeTarget(probe.target);
  const namespace = requireText(probe.namespace, "namespace");
  const assets = probe.services.map((service) => {
    const engine = normalizeDatabaseEngine(service.engine);
    const nameMetadata = createDatabaseNameMetadata({
      service,
      environmentId,
      naming,
    });
    return {
      databaseAssetId: `db_${projectId}_${environmentId}_${service.name}`,
      projectId,
      environmentId,
      engine,
      ...nameMetadata,
      namespace,
      serviceName: service.name,
      host: service.host,
      port: Number(service.port),
      databaseName: service.database,
      versionText: service.version,
      connectionRef: `k8s://${target.cluster}/${target.instance}/${namespace}/service/${service.name}:${service.port}`,
      version: {
        current: 0,
        label: "dosql_000000",
      },
      source: {
        targetName: target.name,
        cluster: target.cluster,
        instance: target.instance,
      },
    };
  });

  return {
    projectId,
    environmentId,
    namespace,
    target,
    assets,
  };
}

export function createStructureSnapshot({ inventory, probe, capturedAt }) {
  const assets = inventory.assets.map((asset) => {
    const objects = createObjectsForEngine({ engine: asset.engine, probe });
    const structure = {
      databaseAssetId: asset.databaseAssetId,
      engine: asset.engine,
      objects,
    };
    return {
      ...structure,
      version: asset.version,
      structureFingerprint: `sha256:${fingerprint(stableJson(structure))}`,
    };
  });
  const snapshot = {
    projectId: inventory.projectId,
    environmentId: inventory.environmentId,
    capturedAt: capturedAt ?? new Date().toISOString(),
    assets,
  };
  return {
    ...snapshot,
    snapshotFingerprint: fingerprint(stableJson(snapshot)),
  };
}

export function createMaintenanceChecklist({ inventory }) {
  const checks = [
    {
      suffix: "connection",
      title: "连接探测",
      description: "验证服务可达、认证可用，并记录数据库版本。",
    },
    {
      suffix: "version_baseline",
      title: "版本基线",
      description: "确认数据库资产已经建立 DoSql 版本号。",
    },
    {
      suffix: "structure_snapshot",
      title: "结构快照",
      description: "采集表、列、集合等结构化信息。",
    },
    {
      suffix: "change_recording",
      title: "变更记录链路",
      description: "验证所有变更必须先生成 operation record。",
    },
  ];

  return {
    projectId: inventory.projectId,
    environmentId: inventory.environmentId,
    items: inventory.assets.flatMap((asset) =>
      checks.map((check) => ({
        checkId: `${asset.engine}.${check.suffix}`,
        databaseAssetId: asset.databaseAssetId,
        engine: asset.engine,
        title: check.title,
        description: check.description,
        mode: "read_only",
      })),
    ),
  };
}

function createMysqlObjects(mysqlProbe = {}) {
  const columnsByTable = mysqlProbe.columns ?? {};
  return (mysqlProbe.tables ?? []).map((table) => ({
    kind: "table",
    name: table,
    columns: (columnsByTable[table] ?? []).map((column) => ({
      kind: "column",
      name: column.name,
      dataType: column.type,
      nullable: Boolean(column.nullable),
      key: column.key ?? "",
    })),
  }));
}

function createPostgresObjects(postgresProbe = {}) {
  const columnsByTable = postgresProbe.columns ?? {};
  return (postgresProbe.tables ?? []).map((table) => ({
    kind: "table",
    name: table,
    columns: (columnsByTable[table] ?? []).map((column) => ({
      kind: "column",
      name: column.name,
      dataType: column.type,
      nullable: Boolean(column.nullable),
      key: column.key ?? "",
    })),
  }));
}

function createMongoObjects(mongoProbe = {}) {
  return (mongoProbe.collections ?? []).map((collection) => ({
    kind: "collection",
    name: collection,
    database: "test",
  }));
}

function createObjectsForEngine({ engine, probe }) {
  if (engine === "mysql") return createMysqlObjects(probe.mysql);
  if (engine === "postgresql") return createPostgresObjects(probe.postgresql ?? probe.postgres);
  if (engine === "mongodb") return createMongoObjects(probe.mongodb);
  throw new Error(`Unsupported database engine: ${engine}`);
}

function normalizeTarget(target) {
  return {
    name: requireText(target?.name, "target.name"),
    cluster: requireText(target?.cluster, "target.cluster"),
    instance: requireText(target?.instance, "target.instance"),
  };
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
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

function fingerprint(value) {
  return createHash("sha256").update(value).digest("hex");
}
