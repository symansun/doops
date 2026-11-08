import { createHash } from "node:crypto";

const SCANNER_MODULES = [
  {
    moduleId: "mysql.health",
    engine: "mysql",
    capability: "health_analysis",
    mode: "read_only",
  },
  {
    moduleId: "mysql.sql_logs",
    engine: "mysql",
    capability: "sql_log_extraction",
    mode: "read_only",
  },
  {
    moduleId: "security.sql_injection",
    engine: "all",
    capability: "sql_injection_detection",
    mode: "read_only",
  },
  {
    moduleId: "mongodb.health",
    engine: "mongodb",
    capability: "health_analysis",
    mode: "read_only",
  },
];

const SQL_INJECTION_PATTERNS = [
  {
    pattern: "boolean_tautology",
    regex: /\b(or|and)\b\s+(['"]?\w+['"]?\s*=\s*['"]?\w+['"]?|1\s*=\s*1|true\s*=\s*true)/i,
    severity: "high",
  },
  {
    pattern: "union_select",
    regex: /\bunion\s+(all\s+)?select\b/i,
    severity: "high",
  },
  {
    pattern: "time_delay_function",
    regex: /\b(sleep|benchmark|pg_sleep|waitfor\s+delay)\s*\(/i,
    severity: "high",
  },
  {
    pattern: "stacked_statement",
    regex: /;\s*(drop|alter|insert|update|delete|create)\b/i,
    severity: "high",
  },
  {
    pattern: "sql_comment_truncation",
    regex: /(--|#|\/\*)/,
    severity: "medium",
  },
];

export function listDatabaseScannerModules() {
  return SCANNER_MODULES.map((module) => ({ ...module }));
}

export function extractSqlLogEvents({ engine, source, lines }) {
  if (engine !== "mysql") {
    throw new Error(`SQL log extraction is not implemented for engine: ${engine}`);
  }
  const events = [];
  let current = emptyMysqlLogEvent(source);

  for (const line of lines ?? []) {
    const text = String(line);
    if (text.startsWith("# Time:")) {
      pushMysqlEvent(events, current, engine);
      current = emptyMysqlLogEvent(source);
      current.observedAt = text.replace("# Time:", "").trim();
      continue;
    }
    if (text.startsWith("# User@Host:")) {
      current.actor = parseMysqlUserHost(text);
      continue;
    }
    if (text.startsWith("# Query_time:")) {
      current.metrics = parseMysqlMetrics(text);
      continue;
    }
    if (/^SET timestamp=/i.test(text) || text.trim() === "") {
      continue;
    }
    if (!text.startsWith("#")) {
      current.statementLines.push(text);
    }
  }
  pushMysqlEvent(events, current, engine);

  return events;
}

export function analyzeSqlInjectionRisk({ events }) {
  const findings = [];
  for (const event of events ?? []) {
    for (const rule of SQL_INJECTION_PATTERNS) {
      if (!rule.regex.test(event.statement)) continue;
      findings.push({
        findingId: `sqli_${fingerprint(`${event.statementFingerprint}:${rule.pattern}`).slice(0, 16)}`,
        category: "sql_injection",
        pattern: rule.pattern,
        severity: rule.severity,
        source: event.source,
        statementFingerprint: event.statementFingerprint,
        evidence: extractEvidence(event.statement, rule.regex),
        recommendation:
          "Use parameterized queries, bind variables and allow-listed query construction before executing user-controlled input.",
      });
      break;
    }
  }
  return findings;
}

export function analyzeDatabaseHealth({ asset, mysql, mongodb }) {
  if (asset.engine === "mongodb") {
    return analyzeMongoHealth({ asset, mongodb });
  }
  return analyzeMysqlHealth({ asset, mysql });
}

export function runDatabaseScan({ asset, samples, scannedAt }) {
  const modules = listDatabaseScannerModules().filter(
    (module) => module.engine === asset.engine || module.engine === "all",
  );
  const sqlLogEvents = samples?.sqlLogs
    ? extractSqlLogEvents({
        engine: asset.engine,
        source: samples.sqlLogs.source,
        lines: samples.sqlLogs.lines,
      })
    : [];
  const securityFindings = analyzeSqlInjectionRisk({ events: sqlLogEvents });
  const health = analyzeDatabaseHealth({
    asset,
    mysql: samples?.mysql,
    mongodb: samples?.mongodb,
  });
  const scanSeed = stableJson({
    assetId: asset.databaseAssetId,
    scannedAt,
    health,
    securityFindings,
  });

  return {
    scanId: `dbscan_${fingerprint(scanSeed).slice(0, 16)}`,
    scannedAt: scannedAt ?? new Date().toISOString(),
    asset,
    modules,
    health,
    sqlLogEvents,
    securityFindings,
    audit: {
      mode: "read_only",
      captured: true,
    },
  };
}

function emptyMysqlLogEvent(source) {
  return {
    source,
    observedAt: "",
    actor: {},
    metrics: {},
    statementLines: [],
  };
}

function pushMysqlEvent(events, current, engine) {
  const statement = normalizeStatement(current.statementLines.join(" "));
  if (!statement) return;
  events.push({
    eventId: `sqllog_${fingerprint(`${current.observedAt}:${statement}`).slice(0, 16)}`,
    engine,
    source: current.source,
    observedAt: current.observedAt,
    actor: current.actor,
    metrics: current.metrics,
    statement,
    statementFingerprint: fingerprint(statement),
  });
}

function parseMysqlUserHost(line) {
  const match = line.match(/# User@Host:\s+([^\[]+)\[[^\]]*\]\s+@\s+([^\[]+)/);
  return {
    user: match?.[1]?.trim() ?? "",
    host: match?.[2]?.trim() ?? "",
  };
}

function parseMysqlMetrics(line) {
  const metricPairs = Object.fromEntries(
    [...line.matchAll(/([A-Za-z_]+):\s+([0-9.]+)/g)].map((match) => [
      match[1],
      Number(match[2]),
    ]),
  );
  return {
    queryTimeSeconds: metricPairs.Query_time ?? 0,
    lockTimeSeconds: metricPairs.Lock_time ?? 0,
    rowsSent: metricPairs.Rows_sent ?? 0,
    rowsExamined: metricPairs.Rows_examined ?? 0,
  };
}

function analyzeMysqlHealth({ asset, mysql = {} }) {
  const status = mysql.status ?? {};
  const variables = mysql.variables ?? {};
  const maxConnections = numberValue(variables.max_connections);
  const threadsConnected = numberValue(status.Threads_connected);
  const slowQueries = numberValue(status.Slow_queries);
  const findings = [];

  if (maxConnections > 0 && threadsConnected / maxConnections >= 0.8) {
    findings.push({
      checkId: "mysql.connection_pressure",
      severity: "high",
      message: `Threads_connected is ${threadsConnected}/${maxConnections}.`,
    });
  }
  if (String(variables.slow_query_log ?? "").toUpperCase() !== "ON") {
    findings.push({
      checkId: "mysql.slow_query_log_disabled",
      severity: "medium",
      message: "slow_query_log is not enabled.",
    });
  }
  if (slowQueries > 0) {
    findings.push({
      checkId: "mysql.slow_queries_present",
      severity: "medium",
      message: `Slow_queries is ${slowQueries}.`,
    });
  }

  return {
    databaseAssetId: asset.databaseAssetId,
    engine: "mysql",
    versionText: asset.versionText,
    status: healthStatus(findings),
    healthScore: healthScore(findings),
    findings,
  };
}

function analyzeMongoHealth({ asset, mongodb = {} }) {
  const findings = [];
  if (mongodb.pingOk === false) {
    findings.push({
      checkId: "mongodb.ping_failed",
      severity: "high",
      message: "MongoDB ping failed.",
    });
  }
  return {
    databaseAssetId: asset.databaseAssetId,
    engine: "mongodb",
    versionText: asset.versionText,
    status: healthStatus(findings),
    healthScore: healthScore(findings),
    findings,
  };
}

function healthStatus(findings) {
  if (findings.some((finding) => finding.severity === "high")) return "degraded";
  if (findings.length > 0) return "attention";
  return "healthy";
}

function healthScore(findings) {
  return Math.max(
    0,
    100 -
      findings.reduce((score, finding) => {
        if (finding.severity === "high") return score + 30;
        if (finding.severity === "medium") return score + 15;
        return score + 5;
      }, 0),
  );
}

function extractEvidence(statement, regex) {
  return statement.match(regex)?.[0] ?? statement.slice(0, 80);
}

function normalizeStatement(statement) {
  return String(statement ?? "").trim().replace(/;+$/g, "").replace(/\s+/g, " ");
}

function numberValue(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
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
  return createHash("sha256").update(String(value)).digest("hex");
}
