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

const URI_PATTERN = /(?:jdbc:)?(postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?:?)\:\/\/(?:[^@\s"'`]+@)?([^/\s"'`]+)(?:\/([A-Za-z0-9_.-]+))?/gi;
const HOST_PATTERNS = [
  { engine: "postgres", names: "POSTGRES_HOST|POSTGRESQL_HOST|PGHOST" },
  { engine: "mysql", names: "MYSQL_HOST|MARIADB_HOST" },
  { engine: "mongodb", names: "MONGO_HOST|MONGODB_HOST" },
  { engine: "", names: "DATABASE_HOST" },
];
const DATABASE_PATTERNS = [
  { engine: "postgres", names: "POSTGRES_DB|POSTGRES_DATABASE|PGDATABASE" },
  { engine: "mysql", names: "MYSQL_DATABASE|MYSQL_DB|MARIADB_DATABASE" },
  { engine: "mongodb", names: "MONGO_INITDB_DATABASE|MONGODB_DATABASE" },
  { engine: "", names: "DATABASE_NAME|DB_NAME" },
];
const SERVICE_HOST_PATTERN = /\b([a-z0-9][a-z0-9-]*)\.([a-z0-9][a-z0-9-]*)\.svc(?:\.cluster\.local)?\b/gi;
const DEFAULT_PORTS = { postgres: 5432, mysql: 3306, mongodb: 27017 };

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
  for (const asset of assets) {
    const existing = [...merged.values()].find((candidate) => assetIdentityKey(candidate) === assetIdentityKey(asset));
    const next = mergeAsset(existing, asset);
    if (existing && existing.databaseAssetId !== next.databaseAssetId) merged.delete(existing.databaseAssetId);
    merged.set(next.databaseAssetId, next);
  }
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
    const existing = [...merged.values()].find((resource) => resource.kind === kind && resource.logicalEndpoint === endpoint);
    if (existing && existing.id !== asset.databaseAssetId) merged.delete(existing.id);
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
    const engine = normalizeEngine(match[1]);
    const authority = match[2];
    const databaseName = match[3] || "";
    const [host, portText] = authority.split(":");
    result.push(makeAsset({
      ...context,
      engine,
      host,
      port: Number(portText || DEFAULT_PORTS[engine]),
      databaseName,
      sourceRef: relative(root, file),
      sourceKind: sourceKindForFile(file),
    }));
  }
  const hosts = [];
  for (const pattern of HOST_PATTERNS) {
    for (const match of text.matchAll(new RegExp(`(?:${pattern.names})\\s*[:=]\\s*["']?([A-Za-z0-9_.-]+)`, "gi"))) {
      hosts.push({ engine: pattern.engine || inferEngine(text), host: match[1] });
    }
  }
  const databases = [];
  for (const pattern of DATABASE_PATTERNS) {
    for (const match of text.matchAll(new RegExp(`(?:${pattern.names})\\s*[:=]\\s*["']?([A-Za-z0-9_.-]+)`, "gi"))) {
      databases.push({ engine: pattern.engine, name: match[1] });
    }
  }
  for (const { engine, host } of hosts) {
    if (!engine) continue;
    const names = databases
      .filter((database) => !database.engine || database.engine === engine)
      .map((database) => database.name);
    for (const databaseName of names.length ? names : [""]) {
      result.push(makeAsset({
        ...context,
        engine,
        host,
        port: DEFAULT_PORTS[engine],
        databaseName,
        sourceRef: relative(root, file),
        sourceKind: sourceKindForFile(file),
      }));
    }
  }
  for (const match of text.matchAll(SERVICE_HOST_PATTERN)) {
    const engine = inferEngine(match[1]);
    if (!engine) continue;
    const database = databases.find((candidate) => !candidate.engine || candidate.engine === engine)?.name || "";
    result.push(makeAsset({
      ...context,
      engine,
      host: `${match[1]}.${match[2]}.svc.cluster.local`,
      port: DEFAULT_PORTS[engine],
      databaseName: database,
      namespace: match[2],
      serviceName: match[1],
      sourceRef: relative(root, file),
      sourceKind: sourceKindForFile(file),
    }));
  }
  return [...result, ...extractKubernetesCandidates(text, file, root, context)].filter(Boolean);
}

function extractKubernetesCandidates(text, file, root, context) {
  const documents = text.split(/^---\s*$/m);
  const result = [];
  for (const document of documents) {
    const kind = document.match(/^\s*kind:\s*["']?([A-Za-z0-9]+)["']?/m)?.[1] || "";
    if (!kind && !/\bapiVersion\s*:/i.test(document)) continue;
    const metadata = yamlMetadata(document);
    const name = metadata.name;
    if (!name) continue;
    const engine = inferEngine(`${kind} ${name} ${document}`);
    if (!engine) continue;
    const namespace = metadata.namespace || "default";
    const database = databaseNamesForEngine(document, engine)[0] || "";
    const serviceName = kind.toLowerCase() === "service" ? name : findServiceName(document, "");
    if (kind.toLowerCase() !== "service" && !serviceName) continue;
    const host = `${serviceName || name}.${namespace}.svc.cluster.local`;
    result.push(makeAsset({
      ...context,
      engine,
      host,
      port: manifestPort(document, engine),
      databaseName: database,
      namespace,
      serviceName: serviceName || name,
      sourceRef: relative(root, file),
      sourceKind: sourceKindForFile(file, true),
    }));
  }
  return result;
}

function yamlMetadata(document) {
  const lines = document.split(/\r?\n/);
  const metadataLine = lines.findIndex((line) => /^\s*metadata:\s*$/.test(line));
  if (metadataLine < 0) return { name: "", namespace: "" };
  const metadataIndent = lines[metadataLine].match(/^\s*/)[0].length;
  let name = "";
  let namespace = "";
  for (const line of lines.slice(metadataLine + 1)) {
    if (line.trim() && line.match(/^\s*/)[0].length <= metadataIndent) break;
    const field = line.match(/^\s+(name|namespace):\s*["']?([^\s"']+)/);
    if (!field) continue;
    if (field[1] === "name") name = field[2];
    if (field[1] === "namespace") namespace = field[2];
  }
  return { name, namespace };
}

function findServiceName(document, fallback) {
  const serviceHost = document.match(/\b([a-z0-9][a-z0-9-]*)\.([a-z0-9][a-z0-9-]*)\.svc(?:\.cluster\.local)?\b/i);
  return serviceHost?.[1] ||
    document.match(/(?:serviceName|service\.name|service):\s*["']?([a-z0-9][a-z0-9-]*)/i)?.[1] ||
    fallback;
}

function manifestPort(document, engine) {
  const port = document.match(/(?:^|\n)\s*(?:port|targetPort):\s*["']?(\d+)/m)?.[1];
  return Number(port || DEFAULT_PORTS[engine]);
}

function databaseNamesForEngine(text, engine) {
  return DATABASE_PATTERNS
    .filter((pattern) => !pattern.engine || pattern.engine === engine)
    .flatMap((pattern) => [...text.matchAll(new RegExp(`(?:${pattern.names})\\s*[:=]\\s*["']?([A-Za-z0-9_.-]+)`, "gi"))].map((match) => match[1]));
}

function inferEngine(text) {
  const value = String(text || "").toLowerCase();
  if (/postgres|postgresql|pgvector/.test(value)) return "postgres";
  if (/mysql|mariadb/.test(value)) return "mysql";
  if (/mongodb|mongo/.test(value)) return "mongodb";
  return "";
}

function normalizeEngine(value) {
  const normalized = String(value || "").toLowerCase().replace(/:$/, "");
  if (normalized === "postgresql") return "postgres";
  if (normalized === "mariadb") return "mysql";
  if (normalized === "mongodb+srv") return "mongodb";
  if (normalized === "postgres" || normalized === "mysql" || normalized === "mongodb") return normalized;
  return "";
}

function sourceKindForFile(file, kubernetes = false) {
  const lower = String(file).toLowerCase();
  if (kubernetes) return /helm|chart|values/.test(lower) ? "helm" : "kubernetes";
  if (/helm|chart|values/.test(lower)) return "helm";
  return "code";
}

function assetFromHint(hint, context) {
  return makeAsset({
    ...context,
    ...hint,
    sourceRef: hint.sourceRef || "operator-discovery",
    sourceKind: hint.sourceKind || "target-discovery",
  });
}

function makeAsset({ projectId, environmentId, target, databaseAssetId = "", engine = "postgres", host, port = DEFAULT_PORTS[engine], databaseName = "", namespace = "", serviceName = "", sourceRef, sourceKind }) {
  engine = normalizeEngine(engine);
  if (!engine) return null;
  if (!host || !target?.cluster || !target?.instance) return null;
  const parsedHost = parseServiceHost(host);
  namespace = namespace || parsedHost.namespace;
  serviceName = serviceName || parsedHost.service;
  const normalizedHost = host.trim().toLowerCase();
  const normalizedDatabase = databaseName.trim();
  const canonicalAssetId = String(databaseAssetId || `db_${slug(projectId)}_${slug(environmentId)}_${slug(namespace || "default")}_${slug(serviceName || normalizedHost)}_${slug(normalizedDatabase || "unknown")}`).trim();
  const connectionRef = serviceName && namespace
    ? `k8s://${target.cluster}/${target.instance}/${namespace}/service/${serviceName}:${Number(port)}${normalizedDatabase ? `#db=${normalizedDatabase}` : ""}`
    : `code://${normalizedHost}:${Number(port)}${normalizedDatabase ? `/${normalizedDatabase}` : ""}`;
  return {
    databaseAssetId: canonicalAssetId,
    projectId,
    environmentId,
    engine,
    logicalName: normalizedDatabase || serviceName || normalizedHost,
    displayName: `${environmentId} ${normalizedDatabase || serviceName || "PostgreSQL"}`,
    aliases: uniqueStrings([normalizedDatabase, serviceName, `${environmentId} ${engine}`, engine, ...(engine === "postgres" ? ["postgresql"] : [])]),
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
  const targetAssetId = incoming.source?.kind === "target-discovery"
    ? incoming.databaseAssetId
    : existing.source?.kind === "target-discovery"
      ? existing.databaseAssetId
      : "";
  const refs = uniqueStrings([
    existing.source?.ref,
    ...(existing.source?.refs ?? []),
    incoming.source?.ref,
    ...(incoming.source?.refs ?? []),
  ]);
  return {
    ...existing,
    ...incoming,
    databaseAssetId: targetAssetId || preferred.databaseAssetId,
    logicalName: preferred.logicalName,
    displayName: preferred.displayName,
    databaseName: preferred.databaseName || "",
    connectionRef: preferred.connectionRef,
    namespace: incoming.namespace !== "unknown" ? incoming.namespace : existing.namespace,
    serviceName: incoming.serviceName !== "unknown" ? incoming.serviceName : existing.serviceName,
    host: incoming.host || existing.host,
    port: incoming.port || existing.port,
    aliases: uniqueStrings([...(existing.aliases ?? []), ...(incoming.aliases ?? [])]),
    source: {
      ...existing.source,
      ...incoming.source,
      kind: targetAssetId ? "target-discovery" : incoming.source?.kind || existing.source?.kind,
      refs,
      ref: refs[0],
    },
  };
}

function assetIdentityKey(asset) {
  return [
    asset.engine,
    asset.environmentId,
    asset.namespace,
    asset.serviceName,
    asset.host,
    asset.port,
    asset.databaseName || "",
  ].join("\u001f");
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
