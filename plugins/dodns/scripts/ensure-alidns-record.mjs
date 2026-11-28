#!/usr/bin/env node

import { createHash, createHmac, randomUUID } from "node:crypto"
import { readFileSync, realpathSync } from "node:fs"
import { setTimeout as sleep } from "node:timers/promises"
import { fileURLToPath } from "node:url"

const API_VERSION = "2015-01-09"
const SIGNATURE_ALGORITHM = "ACS3-HMAC-SHA256"
const DEFAULT_ENDPOINT = "alidns.aliyuncs.com"
const ALLOWED_TYPES = new Set(["A", "AAAA", "CNAME", "TXT", "MX"])

function usage() {
  return `Usage:
  node skills/agent/dodns/scripts/ensure-alidns-record.mjs \\
    --domain example.com \\
    --rr app \\
    --type A \\
    --value 203.0.113.10 \\
    --ttl 600 [--dry-run]

Credential resolution order (never pass secrets in CLI args):
  1. Gateway runtime opaque credential file: DOOPS_CRED_FILE_ALIDNS_CREDENTIAL or DOOPS_CRED_FILE (0600 JSON)
  2. Process environment variables: ALIYUN_ACCESS_KEY_ID, ALIYUN_ACCESS_KEY_SECRET

Optional environment:
  ALIYUN_SECURITY_TOKEN
  ALIYUN_ALIDNS_ENDPOINT
`
}

export function loadAliDnsCredentials(env = process.env) {
  const filePath = (env.DOOPS_CRED_FILE_ALIDNS_CREDENTIAL || env.DOOPS_CRED_FILE || "").trim()
  let fromFile = {}
  if (filePath) {
    let parsed
    try {
      parsed = JSON.parse(readFileSync(filePath, "utf8"))
    } catch {
      throw new Error("failed to read managed runtime credential file (check file presence and JSON format)")
    }
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("managed runtime credential file must contain a JSON key-value object")
    }
    fromFile = parsed
  }
  const accessKeyId = String(fromFile.ALIYUN_ACCESS_KEY_ID || env.ALIYUN_ACCESS_KEY_ID || "").trim()
  const accessKeySecret = String(fromFile.ALIYUN_ACCESS_KEY_SECRET || env.ALIYUN_ACCESS_KEY_SECRET || "").trim()
  const securityToken = String(fromFile.ALIYUN_SECURITY_TOKEN || env.ALIYUN_SECURITY_TOKEN || "").trim()
  const endpoint = String(fromFile.ALIYUN_ALIDNS_ENDPOINT || env.ALIYUN_ALIDNS_ENDPOINT || DEFAULT_ENDPOINT).trim()
  if (!accessKeyId || !accessKeySecret) {
    throw new Error("missing AliDNS credentials (bind an opaqueSecret credential via DoOps or set ALIYUN_ACCESS_KEY_ID/ALIYUN_ACCESS_KEY_SECRET)")
  }
  return { accessKeyId, accessKeySecret, securityToken, endpoint, source: filePath ? "doops_runtime_opaque" : "env" }
}

function parseArgs(argv) {
  const args = {}
  for (let i = 0; i < argv.length; i += 1) {
    const key = argv[i]
    if (!key.startsWith("--")) {
      throw new Error(`unexpected argument: ${key}`)
    }
    if (key === "--dry-run") {
      args["dry-run"] = "true"
      continue
    }
    const value = argv[i + 1]
    if (value == null || value.startsWith("--")) {
      throw new Error(`missing value for ${key}`)
    }
    args[key.slice(2)] = value
    i += 1
  }
  return args
}

function requiredArg(args, name) {
  const value = args[name]
  if (!value) {
    throw new Error(`missing --${name}\n${usage()}`)
  }
  return value
}

function encodeRFC3986(value) {
  return encodeURIComponent(String(value)).replace(/[!'()*]/g, (char) =>
    `%${char.charCodeAt(0).toString(16).toUpperCase()}`,
  )
}

function canonicalQuery(params) {
  return Object.entries(params)
    .filter(([, value]) => value !== undefined && value !== null && value !== "")
    .map(([key, value]) => [encodeRFC3986(key), encodeRFC3986(value)])
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}=${value}`)
    .join("&")
}

function sha256Hex(value) {
  return createHash("sha256").update(value, "utf8").digest("hex")
}

function hmacHex(key, value) {
  return createHmac("sha256", key).update(value, "utf8").digest("hex")
}

function normalizeRecordList(payload) {
  const records = payload?.DomainRecords?.Record ?? []
  return Array.isArray(records) ? records : [records]
}

function fqdnFor(domain, rr) {
  return rr === "@" ? domain : `${rr}.${domain}`
}

async function callAliDns(action, params) {
  const { accessKeyId, accessKeySecret, securityToken, endpoint } = loadAliDnsCredentials()
  const payloadHash = sha256Hex("")
  const headers = {
    host: endpoint,
    "x-acs-action": action,
    "x-acs-content-sha256": payloadHash,
    "x-acs-date": new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
    "x-acs-signature-nonce": randomUUID(),
    "x-acs-version": API_VERSION,
  }
  if (securityToken) {
    headers["x-acs-security-token"] = securityToken
  }

  const signedHeaderNames = Object.keys(headers).sort()
  const canonicalHeaders = signedHeaderNames.map((name) => `${name}:${headers[name]}\n`).join("")
  const signedHeaders = signedHeaderNames.join(";")
  const query = canonicalQuery(params)
  const canonicalRequest = ["POST", "/", query, canonicalHeaders, signedHeaders, payloadHash].join("\n")
  const stringToSign = `${SIGNATURE_ALGORITHM}\n${sha256Hex(canonicalRequest)}`
  const signature = hmacHex(accessKeySecret, stringToSign)
  const authorization = `${SIGNATURE_ALGORITHM} Credential=${accessKeyId},SignedHeaders=${signedHeaders},Signature=${signature}`
  const requestHeaders = { ...headers, authorization }
  delete requestHeaders.host

  const response = await fetch(`https://${endpoint}/?${query}`, { method: "POST", headers: requestHeaders })
  const body = await response.text()
  let payload
  try {
    payload = body ? JSON.parse(body) : {}
  } catch {
    throw new Error(`AliDNS ${action} returned non-JSON HTTP ${response.status}`)
  }
  if (!response.ok || payload.Code) {
    throw new Error(`AliDNS ${action} failed: ${payload.Code || `HTTP_${response.status}`}: ${payload.Message || response.statusText}`)
  }
  return payload
}

async function waitForPublicDns({ fqdn, type, value, waitSeconds, intervalSeconds }) {
  if (waitSeconds <= 0) return
  const deadline = Date.now() + waitSeconds * 1000
  let lastAnswers = []
  while (Date.now() <= deadline) {
    const url = new URL("https://cloudflare-dns.com/dns-query")
    url.searchParams.set("name", fqdn)
    url.searchParams.set("type", type)
    const response = await fetch(url, { headers: { accept: "application/dns-json" } })
    const payload = await response.json()
    lastAnswers = (payload.Answer || []).map((answer) => answer.data)
    if (lastAnswers.includes(value)) {
      console.log(`Public DNS ok: ${fqdn} ${type} includes ${value}`)
      return
    }
    await sleep(Math.max(1, intervalSeconds) * 1000)
  }
  throw new Error(`public DNS did not resolve ${fqdn} ${type} to ${value}; last answers: ${lastAnswers.join(", ")}`)
}

async function ensureRecord({ domain, rr, type, value, ttl, waitSeconds, intervalSeconds, dryRun }) {
  const fqdn = fqdnFor(domain, rr)
  if (dryRun) {
    console.log(JSON.stringify({ dryRun: true, fqdn, domain, rr, type, value, ttl }, null, 2))
    return
  }
  const describe = await callAliDns("DescribeSubDomainRecords", { SubDomain: fqdn, Type: type, PageSize: "100" })
  const records = normalizeRecordList(describe).filter((record) => record.RR === rr && record.Type === type)

  if (records.length === 0) {
    const result = await callAliDns("AddDomainRecord", { DomainName: domain, RR: rr, Type: type, Value: value, TTL: String(ttl) })
    console.log(`Created DNS record: ${fqdn} ${type} ${value} (${result.RecordId})`)
  } else if (records.length === 1) {
    const [record] = records
    if (record.Value === value && record.Status === "ENABLE") {
      console.log(`DNS record already correct: ${fqdn} ${type} ${value} (${record.RecordId})`)
    } else {
      await callAliDns("UpdateDomainRecord", { RecordId: record.RecordId, RR: rr, Type: type, Value: value, TTL: String(ttl) })
      console.log(`Updated DNS record: ${fqdn} ${type} ${record.Value} -> ${value} (${record.RecordId})`)
    }
  } else {
    const summary = records.map((record) => `${record.RecordId}:${record.Value}:${record.Status}`).join(", ")
    throw new Error(`refusing to update ambiguous DNS records for ${fqdn} ${type}: ${summary}`)
  }

  await waitForPublicDns({ fqdn, type, value, waitSeconds, intervalSeconds })
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  const domain = requiredArg(args, "domain")
  const rr = requiredArg(args, "rr")
  const type = (args.type || "A").toUpperCase()
  const value = requiredArg(args, "value")
  const ttl = Number(args.ttl || 600)
  const waitSeconds = Number(args["wait-public-seconds"] || 180)
  const intervalSeconds = Number(args["wait-public-interval-seconds"] || 10)
  const dryRun = args["dry-run"] === "true"

  if (!Number.isInteger(ttl) || ttl <= 0) throw new Error("--ttl must be a positive integer")
  if (!ALLOWED_TYPES.has(type)) throw new Error(`--type must be one of: ${[...ALLOWED_TYPES].join(", ")}`)

  await ensureRecord({ domain, rr, type, value, ttl, waitSeconds, intervalSeconds, dryRun })
}

function isCliEntrypoint() {
  if (!process.argv[1]) return false
  try {
    return realpathSync(fileURLToPath(import.meta.url)) === realpathSync(process.argv[1])
  } catch {
    return fileURLToPath(import.meta.url) === process.argv[1]
  }
}

if (isCliEntrypoint()) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.message : error)
    process.exit(1)
  })
}
