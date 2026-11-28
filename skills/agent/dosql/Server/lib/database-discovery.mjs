const ENGINE_ALIASES = new Map([
  ["mysql", "mysql"],
  ["mongodb", "mongodb"],
  ["mongo", "mongodb"],
  ["postgresql", "postgresql"],
  ["postgres", "postgresql"],
  ["pg", "postgresql"],
]);

export function buildDatabaseDiscovery({ projectId, environmentId, probe, naming = {} }) {
  requireText(projectId, "projectId");
  requireText(environmentId, "environmentId");
  const target = normalizeTarget(probe.target);
  const namespace = requireText(probe.namespace, "namespace");
  const candidates = (probe.services ?? []).map((service) => {
    const engine = normalizeDatabaseEngine(service.engine);
    const nameMetadata = createDatabaseNameMetadata({
      service,
      environmentId,
      naming,
    });

    return {
      candidateId: `dbc_${projectId}_${environmentId}_${service.name}`,
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
      connectionRef: buildConnectionRef({ target, namespace, service }),
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
    candidates,
    namingPrompt: {
      status: candidates.length > 0 ? "needs_user_confirmation" : "no_databases_found",
      message: "请确认每个数据库的业务名称和常用别名，后续自然语言操作会用这些名称定位数据库。",
      candidates: candidates.map((candidate) => ({
        candidateId: candidate.candidateId,
        databaseAssetId: candidate.databaseAssetId,
        engine: candidate.engine,
        serviceName: candidate.serviceName,
        suggestedDisplayName: candidate.displayName,
        aliases: candidate.aliases,
      })),
    },
  };
}

export function resolveDatabaseReference({ utterance, assets, environmentId }) {
  const text = normalizeSearchText(requireText(utterance, "utterance"));
  const scopedAssets = environmentId
    ? assets.filter((asset) => asset.environmentId === environmentId)
    : assets;
  const matches = scopedAssets
    .map((asset) => ({ asset, match: findBestMatch({ text, asset }) }))
    .filter((entry) => entry.match)
    .sort((left, right) => right.match.score - left.match.score);

  if (matches.length === 0) {
    return {
      status: "not_found",
      question: "没有找到匹配的数据库，请从可用数据库中选择一个，或先注册数据库名称。",
      availableDatabases: scopedAssets.map(toDatabaseChoice),
    };
  }

  const bestScore = matches[0].match.score;
  const bestMatches = matches.filter((entry) => entry.match.score === bestScore);
  if (bestMatches.length > 1) {
    return {
      status: "ambiguous",
      question: "请选择环境或数据库，当前名称匹配到多个数据库。",
      candidates: bestMatches.map((entry) => toDatabaseChoice(entry.asset)),
      match: bestMatches[0].match,
    };
  }

  return {
    status: "resolved",
    asset: bestMatches[0].asset,
    match: bestMatches[0].match,
  };
}

export function createDatabaseNameMetadata({ service, environmentId, naming = {} }) {
  const config = resolveNamingConfig({ service, naming });
  const logicalName = requireText(service.name, "service.name");
  const displayName = requireText(config.displayName ?? service.displayName ?? logicalName, "displayName");
  const aliases = uniqueStrings([
    ...(config.aliases ?? []),
    ...(service.aliases ?? []),
    logicalName,
    `${environmentId} ${logicalName}`,
  ]).filter((alias) => normalizeSearchText(alias) !== normalizeSearchText(displayName));

  return {
    logicalName,
    displayName,
    aliases,
  };
}

export function normalizeDatabaseEngine(engine) {
  const normalized = ENGINE_ALIASES.get(String(engine ?? "").trim().toLowerCase());
  if (!normalized) {
    throw new Error(`Unsupported database engine: ${engine}`);
  }
  return normalized;
}

function findBestMatch({ text, asset }) {
  const terms = uniqueStrings([
    asset.displayName,
    ...(asset.aliases ?? []),
    asset.logicalName,
    asset.databaseName,
    asset.serviceName,
    asset.databaseAssetId,
  ]);
  const matchingTerms = terms
    .map((term) => ({ raw: term, normalized: normalizeSearchText(term) }))
    .filter((term) => term.normalized && text.includes(term.normalized));

  if (matchingTerms.length === 0) return null;

  matchingTerms.sort((left, right) => right.normalized.length - left.normalized.length);
  const best = matchingTerms[0];
  return {
    matchedText: best.raw,
    score: best.normalized.length,
  };
}

function resolveNamingConfig({ service, naming }) {
  return (
    naming[service.name] ??
    naming[service.database] ??
    naming[normalizeDatabaseEngine(service.engine)] ??
    {}
  );
}

function toDatabaseChoice(asset) {
  return {
    databaseAssetId: asset.databaseAssetId,
    projectId: asset.projectId,
    environmentId: asset.environmentId,
    engine: asset.engine,
    displayName: asset.displayName,
    aliases: asset.aliases ?? [],
    logicalName: asset.logicalName,
    databaseName: asset.databaseName,
  };
}

function normalizeTarget(target) {
  return {
    name: requireText(target?.name, "target.name"),
    cluster: requireText(target?.cluster, "target.cluster"),
    instance: requireText(target?.instance, "target.instance"),
  };
}

function buildConnectionRef({ target, namespace, service }) {
  return `k8s://${target.cluster}/${target.instance}/${namespace}/service/${service.name}:${service.port}`;
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
}

function normalizeSearchText(value) {
  return String(value ?? "")
    .trim()
    .toLowerCase()
    .replace(/\s+/g, " ");
}

function uniqueStrings(values) {
  const seen = new Set();
  const result = [];
  for (const value of values) {
    const text = String(value ?? "").trim();
    const key = normalizeSearchText(text);
    if (!text || seen.has(key)) continue;
    seen.add(key);
    result.push(text);
  }
  return result;
}
