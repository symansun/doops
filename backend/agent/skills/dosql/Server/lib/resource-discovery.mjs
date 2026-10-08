import { createHash } from "node:crypto";
import { readdir, readFile, rename, mkdir, stat, writeFile } from "node:fs/promises";
import { dirname, extname, relative, resolve, sep } from "node:path";

const TEXT_EXTENSIONS = new Set([
  ".conf", ".env", ".example", ".go", ".java", ".js", ".json", ".md",
  ".properties", ".py", ".sh", ".sql", ".tf", ".toml", ".ts", ".tsx",
  ".yaml", ".yml",
]);
const IGNORED_DIRS = new Set([
  ".git", ".next", ".cache", ".venv", "build", "dist", "node_modules",
  "target", "vendor",
]);

const URI_PATTERN = /(?:jdbc:)?postgres(?:ql)?:\/\/(?:[^@\s"'`]+@)?([^/\s"'`]+)(?:\/([A-Za-z0-9_.-]+))?/gi;
const HOST_PATTERN = /(?:POSTGRES_HOST|PGHOST|DATABASE_HOST)\s*[:=]\s*["']?([A-Za-z0-9_.-]+)/gi;
const DATABASE_PATTERN = /(?:POSTGRES_DB|PGDATABASE|DATABASE_NAME|DB_NAME)\s*[:=]\s*["']?([A-Za-z0-9_.-]+)/gi;
const SERVICE_HOST_PATTERN = /\b([a-z0-9][a-z0-9-]*)\.([a-z0-9][a-z0-9-]*)\.svc(?:\.cluster\.local)?\b/gi;

export async function discoverAndPersistResources({
  rootDir,
  resourcesPath,
  displayResourcesPath = "",
  projectId,
  environmentId,
  target,
  hints = [],
  maxFiles = 5000,
  maxBytes = 20 * 1024 * 1024,
}) {
  const discovered = await discoverResources({
    rootDir,
    projectId,
    environmentId,
    target,
    hints,
    maxFiles,
    maxBytes,
  });
  const inventory = await mergeResourceInventory(resourcesPath, {
    projectId,
    assets: discovered.assets,
  });
  if (displayResourcesPath) {
    await mergeDisplayResources(displayResourcesPath, discovered.assets);
  }
  return {
    ...discovered,
    resourcesPath: resolve(resourcesPath),
    displayResourcesPath: displayResourcesPath ? resolve(displayResourcesPath) : "",
    inventory,
  };
}

export async function discoverResources({
  rootDir,
  projectId,
  environmentId,
  target,
  hints = [],
  maxFiles = 5000,
  maxBytes = 20 * 1024 * 1024,
}) {
  const root = resolve(requireText(rootDir, "rootDir"));
  const files = [];
  await collectTextFiles(root, files, { maxFiles, maxBytes, bytes: 0 });
  const assets = new Map();
  const discoveryKeys = new Map();
  let bytes = 0;
  for (const file of files) {
    const text = await readFile(file, "utf8");
    bytes += Buffer.byteLength(text, "utf8");
    for (const candidate of extractCandidates(text, file, root, { projectId, environmentId, target })) {
      mergeDiscoveredAsset(assets, discoveryKeys, candidate);
    }
  }
  for (const hint of hints) {
    const candidate = assetFromHint(hint, { projectId, environmentId, target });
    mergeDiscoveredAsset(assets, discoveryKeys, candidate);
  }

  return {
    schema: "dosql.database-asset-discovery.v1",
    projectId,
    environmentId,
    target: normalizeTarget(target),
    scanned: { rootDir: root, files: files.length, bytes },
    assets: [...assets.values()].sort((left, right) => left.databaseAssetId.localeCompare(right.databaseAssetId)),
  };
}

export async function mergeResourceInventory(resourcesPath, { projectId, assets }) {
  const targetPath = resolve(requireText(resourcesPath, "resourcesPath"));
  let current = { schema: "dosql.database-asset-inventory.v1", projectId, assets: [] };
  try {
    current = JSON.parse(await readFile(targetPath, "utf8"));
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  if (!Array.isArray(current.assets)) current.assets = [];
  const merged = new Map(current.assets.map((asset) => [asset.databaseAssetId, asset]));
  for (const asset of assets) merged.set(asset.databaseAssetId, mergeAsset(merged.get(asset.databaseAssetId), asset));
  const output = {
    schema: current.schema || "dosql.database-asset-inventory.v1",
    projectId: current.projectId || projectId,
    updatedAt: new Date().toISOString(),
    assets: [...merged.values()].sort((left, right) => left.databaseAssetId.localeCompare(right.databaseAssetId)),
  };
  await mkdir(dirname(targetPath), { recursive: true });
  const temporary = `${targetPath}.${process.pid}.tmp`;
  await writeFile(temporary, `${JSON.stringify(output, null, 2)}\n`, { encoding: "utf8", mode: 0o644 });
  await rename(temporary, targetPath);
  return output;
}

async function mergeDisplayResources(resourcesPath, assets) {
  const targetPath = resolve(requireText(resourcesPath, "displayResourcesPath"));
  let current = { schema: "dosuite.skill-resources.v1", product: "DoSql", resources: [] };
  try {
    current = JSON.parse(await readFile(targetPath, "utf8"));
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  if (!Array.isArray(current.resources)) current.resources = [];
  const merged = new Map(current.resources.map((resource) => [resource.id, resource]));
  for (const asset of assets) {
    const kind = asset.engine === "postgres" ? "PostgreSQL" : asset.engine === "mongodb" ? "MongoDB" : "MySQL";
    const endpoint = `${asset.engine === "postgres" ? "postgresql" : asset.engine}://${asset.host}:${asset.port}${asset.databaseName ? `/${asset.databaseName}` : ""}`;
    merged.set(asset.databaseAssetId, {
      id: asset.databaseAssetId,
      name: asset.displayName,
      kind,
      logicalEndpoint: endpoint,
      owner: "auto-discovered",
      status: asset.status || "discovered",
      risk: "credential scope required",
      tags: [asset.environmentId, asset.namespace, asset.serviceName, "auto-discovered"].filter(Boolean),
      note: "Metadata-only projection from assets.json; credentials and target authorization remain in DoOps managed scope.",
    });
  }
  const output = {
    ...current,
    updatedAt: new Date().toISOString(),
    resources: [...merged.values()].sort((left, right) => String(left.id).localeCompare(String(right.id))),
  };
  await mkdir(dirname(targetPath), { recursive: true });
  const temporary = `${targetPath}.${process.pid}.tmp`;
  await writeFile(temporary, `${JSON.stringify(output, null, 2)}\n`, { encoding: "utf8", mode: 0o644 });
  await rename(temporary, targetPath);
}

async function collectTextFiles(directory, files, state) {
  if (files.length >= state.maxFiles || state.bytes >= state.maxBytes) return;
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    if (files.length >= state.maxFiles || state.bytes >= state.maxBytes) return;
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (!IGNORED_DIRS.has(entry.name)) await collectTextFiles(path, files, state);
      continue;
    }
    if (!entry.isFile() || !isTextFile(entry.name)) continue;
    const fileStat = await stat(path);
    if (fileStat.size > 512 * 1024 || state.bytes + fileStat.size > state.maxBytes) continue;
    files.push(path);
    state.bytes += fileStat.size;
  }
}

function isTextFile(name) {
  const lower = name.toLowerCase();
  if (/(^|[._-])(secret|credential|credentials|password|token|private-key)([._-]|$)/i.test(lower)) return false;
  return TEXT_EXTENSIONS.has(extname(lower)) || lower.endsWith(".env.example");
}

function extractCandidates(text, file, root, context) {
  const result = [];
  for (const match of text.matchAll(URI_PATTERN)) {
    const authority = match[1];
    const databaseName = match[2] || "";
    const [host, portText] = authority.split(":");
    result.push(makeAsset({
      ...context,
      host,
      port: Number(portText || 5432),
      databaseName,
      sourceRef: relative(root, file),
      sourceKind: "code",
    }));
  }
  const hosts = [...text.matchAll(HOST_PATTERN)].map((match) => match[1]);
  const databases = [...text.matchAll(DATABASE_PATTERN)].map((match) => match[1]);
  for (const host of hosts) {
    for (const databaseName of databases.length ? databases : [""]) {
      result.push(makeAsset({
        ...context,
        host,
        port: 5432,
        databaseName,
        sourceRef: relative(root, file),
        sourceKind: "code",
      }));
    }
  }
  for (const match of text.matchAll(SERVICE_HOST_PATTERN)) {
    if (match[1] !== "postgres" && !/postgres/i.test(match[1])) continue;
    result.push(makeAsset({
      ...context,
      host: `${match[1]}.${match[2]}.svc.cluster.local`,
      port: 5432,
      databaseName: databases[0] || "",
      namespace: match[2],
      serviceName: match[1],
      sourceRef: relative(root, file),
      sourceKind: "code",
    }));
  }
  return result.filter(Boolean);
}

function assetFromHint(hint, context) {
  return makeAsset({
    ...context,
    ...hint,
    sourceRef: hint.sourceRef || "operator-discovery",
    sourceKind: hint.sourceKind || "target-discovery",
  });
}

function makeAsset({ projectId, environmentId, target, host, port = 5432, databaseName = "", namespace = "", serviceName = "", sourceRef, sourceKind }) {
  const engine = "postgres";
  if (!host || !target?.cluster || !target?.instance) return null;
  const parsedHost = parseServiceHost(host);
  namespace = namespace || parsedHost.namespace;
  serviceName = serviceName || parsedHost.service;
  const normalizedHost = host.trim().toLowerCase();
  const normalizedDatabase = databaseName.trim();
  const databaseAssetId = `db_${slug(projectId)}_${slug(environmentId)}_${slug(namespace || "default")}_${slug(serviceName || normalizedHost)}_${slug(normalizedDatabase || "unknown")}`;
  const connectionRef = serviceName && namespace
    ? `k8s://${target.cluster}/${target.instance}/${namespace}/service/${serviceName}:${Number(port)}${normalizedDatabase ? `#db=${normalizedDatabase}` : ""}`
    : `code://${normalizedHost}:${Number(port)}${normalizedDatabase ? `/${normalizedDatabase}` : ""}`;
  return {
    databaseAssetId,
    projectId,
    environmentId,
    engine,
    logicalName: normalizedDatabase || serviceName || normalizedHost,
    displayName: `${environmentId} ${normalizedDatabase || serviceName || "PostgreSQL"}`,
    aliases: uniqueStrings([normalizedDatabase, serviceName, `${environmentId} postgres`, "postgres", "postgresql"]),
    namespace: namespace || "unknown",
    serviceName: serviceName || "unknown",
    host: normalizedHost,
    port: Number(port),
    databaseName: normalizedDatabase,
    connectionRef,
    version: { current: 0, label: "dosql_000000" },
    status: "discovered",
    source: { kind: sourceKind, ref: sourceRef, target: normalizeTarget(target) },
  };
}

function mergeAsset(existing, incoming) {
  if (!existing) return incoming;
  const preferred = incoming.databaseName ? incoming : existing.databaseName ? existing : incoming;
  const refs = uniqueStrings([
    existing.source?.ref,
    ...(existing.source?.refs ?? []),
    incoming.source?.ref,
    ...(incoming.source?.refs ?? []),
  ]);
  return {
    ...existing,
    ...incoming,
    databaseAssetId: preferred.databaseAssetId,
    logicalName: preferred.logicalName,
    displayName: preferred.displayName,
    databaseName: preferred.databaseName || "",
    connectionRef: preferred.connectionRef,
    namespace: incoming.namespace !== "unknown" ? incoming.namespace : existing.namespace,
    serviceName: incoming.serviceName !== "unknown" ? incoming.serviceName : existing.serviceName,
    host: incoming.host || existing.host,
    port: incoming.port || existing.port,
    aliases: uniqueStrings([...(existing.aliases ?? []), ...(incoming.aliases ?? [])]),
    source: { ...existing.source, ...incoming.source, refs, ref: refs[0] },
  };
}

function mergeDiscoveredAsset(assets, discoveryKeys, candidate) {
  const key = [candidate.engine, candidate.host, candidate.port, candidate.namespace, candidate.serviceName].join("\u001f");
  const previousId = discoveryKeys.get(key);
  if (!previousId) {
    discoveryKeys.set(key, candidate.databaseAssetId);
    assets.set(candidate.databaseAssetId, candidate);
    return;
  }
  const previous = assets.get(previousId);
  if (!previous) {
    discoveryKeys.set(key, candidate.databaseAssetId);
    assets.set(candidate.databaseAssetId, candidate);
    return;
  }
  if (!previous.databaseName && candidate.databaseName) {
    assets.delete(previousId);
    discoveryKeys.set(key, candidate.databaseAssetId);
    assets.set(candidate.databaseAssetId, mergeAsset(previous, candidate));
    return;
  }
  assets.set(previousId, mergeAsset(previous, candidate));
}

function parseServiceHost(host) {
  const match = host.match(/^([a-z0-9][a-z0-9-]*)\.([a-z0-9][a-z0-9-]*)\.svc(?:\.cluster\.local)?$/i);
  return match ? { service: match[1], namespace: match[2] } : {};
}

function normalizeTarget(target) {
  return { name: requireText(target?.name, "target.name"), cluster: requireText(target?.cluster, "target.cluster"), instance: requireText(target?.instance, "target.instance") };
}

function slug(value) {
  return String(value ?? "").toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "") || "unknown";
}

function uniqueStrings(values) {
  const seen = new Set();
  return values.map((value) => String(value ?? "").trim()).filter((value) => value && !seen.has(value.toLowerCase()) && seen.add(value.toLowerCase()));
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") throw new Error(`${fieldName} is required`);
  return String(value).trim();
}
