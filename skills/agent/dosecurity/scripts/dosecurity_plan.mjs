#!/usr/bin/env node

import { createHash } from "node:crypto"
import { fileURLToPath } from "node:url"

export const DEFAULT_RATE_LIMIT = Object.freeze({
  requestsPerSecond: 1,
  concurrency: 1,
  delayMs: 800,
})

export const DEFAULT_REQUEST_BUDGET = Object.freeze({
  maxRequests: 200,
  maxHosts: 50,
  maxDepth: 1,
})

export const FORBIDDEN_CAPABILITIES = Object.freeze([
  "bruteforce",
  "default-login",
  "exploit",
  "fuzzing",
  "metasploit",
  "dos",
])

const SECRET_META_RE = /password|secret|token|cookie|privatekey|apikey|authorization/i

export function normalizeHost(host) {
  return String(host || "").trim().toLowerCase().replace(/\.$/, "")
}

export function isHostInScope(host, rootDomain, includeSubdomains = true) {
  const normalizedHost = normalizeHost(host)
  const normalizedRoot = normalizeHost(rootDomain)
  if (!normalizedHost || !normalizedRoot) return false
  return normalizedHost === normalizedRoot || (includeSubdomains && normalizedHost.endsWith(`.${normalizedRoot}`))
}

export function buildScanPlan({
  rootUrl,
  rootDomain,
  includeSubdomains = true,
  authorizedBy,
  rateLimit = DEFAULT_RATE_LIMIT,
  requestBudget = DEFAULT_REQUEST_BUDGET,
} = {}) {
  if (!rootUrl) throw new Error("rootUrl is required")
  if (!rootDomain) throw new Error("rootDomain is required")
  if (!authorizedBy) throw new Error("authorizedBy is required")

  const url = new URL(rootUrl)
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("rootUrl must use http or https")
  }
  if (url.username || url.password) {
    throw new Error("rootUrl must not contain embedded credentials")
  }
  const normalizedDomain = normalizeHost(rootDomain)
  const host = normalizeHost(url.hostname)
  if (!isHostInScope(host, normalizedDomain, includeSubdomains)) {
    throw new Error(`${host} is outside authorized root domain ${normalizedDomain}`)
  }

  return {
    schema: "dosecurity.scan-plan.v1",
    createdAt: new Date().toISOString(),
    mode: "low-speed-readonly",
    authorizedBy: String(authorizedBy).trim(),
    scope: {
      rootUrl: url.toString(),
      rootDomain: normalizedDomain,
      includeSubdomains: Boolean(includeSubdomains),
      allowedHosts: includeSubdomains ? [`*.${normalizedDomain}`, normalizedDomain] : [host],
    },
    rateLimit: {
      requestsPerSecond: Math.min(2, Math.max(1, Number(rateLimit.requestsPerSecond ?? DEFAULT_RATE_LIMIT.requestsPerSecond))),
      concurrency: 1,
      delayMs: Math.max(200, Number(rateLimit.delayMs ?? DEFAULT_RATE_LIMIT.delayMs)),
    },
    requestBudget: {
      maxRequests: Math.min(DEFAULT_REQUEST_BUDGET.maxRequests, Math.max(1, Number(requestBudget.maxRequests ?? DEFAULT_REQUEST_BUDGET.maxRequests))),
      maxHosts: Math.min(DEFAULT_REQUEST_BUDGET.maxHosts, Math.max(1, Number(requestBudget.maxHosts ?? DEFAULT_REQUEST_BUDGET.maxHosts))),
      maxDepth: Math.min(DEFAULT_REQUEST_BUDGET.maxDepth, Math.max(1, Number(requestBudget.maxDepth ?? DEFAULT_REQUEST_BUDGET.maxDepth))),
    },
    forbiddenCapabilities: [...FORBIDDEN_CAPABILITIES],
  }
}

export function normalizeAssets(input = []) {
  if (!Array.isArray(input)) throw new Error("assets must be an array")
  return input.map((asset) => {
    const credentialInputs = [
      ...(Array.isArray(asset.credentials) ? asset.credentials : []),
      ...(asset.credential ? [asset.credential] : []),
    ]
    return {
      id: String(asset.id || `asset-${createHash("sha256").update(`${asset.kind || "website"}:${asset.endpoint || asset.name || ""}`).digest("hex").slice(0, 10)}`),
      name: String(asset.name || asset.endpoint || asset.id || "Unnamed asset"),
      kind: ["website", "server", "ip", "machine"].includes(asset.kind) ? asset.kind : "website",
      endpoint: String(asset.endpoint || ""),
      owner: String(asset.owner || "unassigned"),
      tags: Array.isArray(asset.tags) ? asset.tags.map(String) : [],
      credentialRefs: credentialInputs.map((c = {}) => ({
        username: c.username ? String(c.username) : undefined,
        secretRef: c.secretRef ? String(c.secretRef) : undefined,
        hasPassword: Boolean(c.secretRef),
      })),
      metadata: sanitizeMetadata(asset.metadata),
    }
  })
}

export function sanitizeMetadata(metadata) {
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) return {}
  const clean = {}
  for (const [key, value] of Object.entries(metadata)) {
    if (SECRET_META_RE.test(key)) continue
    if (["string", "number", "boolean"].includes(typeof value)) clean[key] = value
  }
  return clean
}

export function evaluateSensitiveExposure({ status, path, bodySample, contentType = "", rootBodyHash = "" }) {
  if (!(status >= 200 && status < 300)) return null
  const body = String(bodySample || "")
  const bodyHash = createHash("sha256").update(body).digest("hex").slice(0, 16)
  if (rootBodyHash && bodyHash === rootBodyHash) return null
  const rules = [
    { match: ".git", rule: "git-config-exposure", re: /\[core\]|repositoryformatversion/i, secret: true },
    { match: ".env", rule: "dotenv-exposure", re: /^[A-Z][A-Z0-9_]{2,}\s*=.+/m, secret: true },
    { match: "swagger", rule: "swagger-ui-exposure", re: /swagger-ui|swaggerui/i, secret: false },
    { match: "openapi", rule: "openapi-docs-exposure", re: /"openapi"\s*:|"swagger"\s*:/i, secret: false },
    { match: "actuator", rule: "spring-actuator-exposure", re: /"activeProfiles"|"propertySources"/i, secret: true },
  ]
  for (const item of rules) {
    if (!String(path || "").includes(item.match)) continue
    if (item.secret && /text\/html/i.test(contentType) && /<html/i.test(body.slice(0, 512))) return null
    if (item.re.test(body)) {
      return {
        severity: item.secret ? "critical" : "high",
        root_cause: item.rule,
        path,
        evidence: `HTTP ${status}; rule=${item.rule} (body redacted)`,
      }
    }
  }
  return null
}

function parseFlags(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 1) {
    const k = argv[i]
    if (!k.startsWith("--")) continue
    const next = argv[i + 1]
    if (next == null || next.startsWith("--")) {
      out[k.slice(2)] = true
    } else {
      out[k.slice(2)] = next
      i += 1
    }
  }
  return out
}

const isCliEntry = process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]
if (isCliEntry) {
  const [, , cmd, ...rest] = process.argv
  const flags = parseFlags(rest)
  if (cmd === "plan") {
    const plan = buildScanPlan({
      rootUrl: flags["root-url"],
      rootDomain: flags["root-domain"],
      includeSubdomains: flags["include-subdomains"] !== "false",
      authorizedBy: flags["authorized-by"],
    })
    process.stdout.write(`${JSON.stringify(plan, null, 2)}\n`)
  } else {
    process.stderr.write("Usage: dosecurity_plan.mjs plan --root-url <url> --root-domain <domain> --authorized-by <actor>\n")
    process.exitCode = 1
  }
}
