#!/usr/bin/env node

import { realpathSync } from "node:fs"
import { fileURLToPath } from "node:url"
import {
  assertNoSecretFields,
  buildRequiredDnsRecords,
  classifyEmailOperation,
  createEmailOperationRecord,
} from "./email-policy.mjs"

const EMAIL_RE = /^[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$/i
const SECRET_REF_RE = /^(k8s|secret|vault|doops):\/\/[a-z0-9._/-]+$/i

export function createDnsPlan(flags = {}) {
  const domain = requireFlag(flags, "domain")
  const mailHostname = flags["mail-hostname"] || `mail.${domain}`
  return {
    schema: "doemail.dns-plan.v1",
    domain,
    mailHostname,
    records: buildRequiredDnsRecords({
      domain,
      mailHostname,
      publicIp: flags["public-ip"],
      dkimSelector: flags["dkim-selector"] || "default",
      dkimPublicKey: flags["dkim-public-key"],
    }),
  }
}

export function evaluateDns(flags = {}) {
  const domain = requireFlag(flags, "domain")
  const mailHostname = flags["mail-hostname"] || `mail.${domain}`
  const missing = []
  if (!flags["a-ok"]) missing.push("A")
  if (!flags["mx-ok"]) missing.push("MX")
  if (!flags["spf-ok"]) missing.push("SPF")
  if (!flags["dkim-ok"]) missing.push("DKIM")
  if (!flags["dmarc-ok"]) missing.push("DMARC")
  if (!flags["rdns-ok"]) missing.push("rDNS")
  return {
    schema: "doemail.dns-evaluation.v1",
    domain,
    mailHostname,
    readyForExternalDelivery: missing.length === 0,
    missingRecords: missing,
  }
}

export function createIntegrationGuide(flags = {}) {
  const domain = requireFlag(flags, "domain")
  const mailHostname = flags["mail-hostname"] || `mail.${domain}`
  const language = String(flags.language || "python").toLowerCase()
  return {
    schema: "doemail.integration-guide.v1",
    domain,
    mailHostname,
    language,
    endpoints: {
      smtpSubmission: { host: mailHostname, port: 587, security: "STARTTLS" },
      imaps: { host: mailHostname, port: 993, security: "TLS" },
    },
    credentialRule: "Inject SMTP_USERNAME and SMTP_PASSWORD via Kubernetes Secret or DoOps managed credential; never hardcode credentials in source code.",
  }
}

export function createKubernetesInspectPlan(flags = {}) {
  const namespace = flags.namespace || "mail"
  const deployment = flags.deployment || "mailserver"
  return {
    schema: "doemail.k8s-inspect-plan.v1",
    namespace,
    deployment,
    commands: [
      `kubectl -n ${namespace} get pod,pvc`,
      `kubectl -n ${namespace} logs deploy/${deployment} --tail=100`,
      `kubectl -n ${namespace} exec deploy/${deployment} -- setup email list`,
    ],
  }
}

export function createAccountCreatePlan(flags = {}) {
  assertNoSecretFields(flags)
  const address = requireFlag(flags, "address")
  if (!EMAIL_RE.test(address)) throw new Error(`invalid email address: ${address}`)
  const secretRef = requireFlag(flags, "password-secret-ref")
  if (!SECRET_REF_RE.test(secretRef)) {
    throw new Error("password-secret-ref must be a secret reference URI (e.g. k8s://mail/secret/ops/password)")
  }
  const env = flags.env || "dev"
  const namespace = flags.namespace || "mail"
  const deployment = flags.deployment || "mailserver"
  return {
    schema: "doemail.account-create-plan.v1",
    address,
    passwordSecretRef: secretRef,
    policy: classifyEmailOperation({ operationType: "create_account", environment: env }),
    record: createEmailOperationRecord({ operationType: "create_account", environment: env, payload: { address, passwordSecretRef: secretRef, namespace, deployment } }),
    note: "Resolve password solely inside the target cluster from passwordSecretRef at execution time; never echo password to stdout.",
  }
}

export function validateAccountManifest(manifest) {
  assertNoSecretFields(manifest)
  if (!manifest || !Array.isArray(manifest.accounts)) {
    throw new Error("manifest must contain an accounts array")
  }
  for (const [idx, acc] of manifest.accounts.entries()) {
    assertNoSecretFields(acc, `accounts[${idx}]`)
    if (!EMAIL_RE.test(String(acc.address || ""))) throw new Error(`accounts[${idx}].address is invalid`)
    if (!SECRET_REF_RE.test(String(acc.passwordSecretRef || ""))) {
      throw new Error(`accounts[${idx}].passwordSecretRef must be a valid Secret reference`)
    }
  }
  return { valid: true, accountCount: manifest.accounts.length }
}

export function createListenerPlan(flags = {}) {
  const mailbox = requireFlag(flags, "mailbox")
  if (!EMAIL_RE.test(mailbox)) throw new Error(`invalid mailbox: ${mailbox}`)
  const maxMessages = Math.min(50, Math.max(1, Number(flags["max-messages"] || 10)))
  return {
    schema: "doemail.listener-plan.v1",
    mode: "run-once",
    mailbox,
    maxMessages,
    approvalRequiredActions: ["send_mail", "delete_mail", "create_account", "disable_account", "dns_update"],
  }
}

function requireFlag(flags, key) {
  const value = flags[key]
  if (!value || value === true) throw new Error(`missing required flag --${key}`)
  return String(value).trim()
}

function parseFlags(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i]
    if (!token.startsWith("--")) continue
    const key = token.slice(2)
    const next = argv[i + 1]
    if (next == null || next.startsWith("--")) {
      out[key] = true
    } else {
      out[key] = next
      i += 1
    }
  }
  return out
}

export function dispatch(group, action, flags) {
  if (group === "dns" && action === "plan") return createDnsPlan(flags)
  if (group === "dns" && action === "evaluate") return evaluateDns(flags)
  if (group === "integration" && action === "guide") return createIntegrationGuide(flags)
  if (group === "k8s" && action === "inspect-plan") return createKubernetesInspectPlan(flags)
  if (group === "account" && action === "create-plan") return createAccountCreatePlan(flags)
  if (group === "listener" && action === "create-plan") return createListenerPlan(flags)
  throw new Error("Usage: doemail.mjs <dns plan|evaluate | integration guide | k8s inspect-plan | account create-plan | listener create-plan>")
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
  const [, , group, action, ...rest] = process.argv
  try {
    const out = dispatch(group, action, parseFlags(rest))
    process.stdout.write(`${JSON.stringify(out, null, 2)}\n`)
  } catch (err) {
    process.stderr.write(`${err instanceof Error ? err.message : String(err)}\n`)
    process.exitCode = 1
  }
}
