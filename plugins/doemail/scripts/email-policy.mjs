import { createHash } from "node:crypto"

export const EMAIL_OPERATION_TYPES = Object.freeze([
  "inspect",
  "connect_domain",
  "create_account",
  "reset_password",
  "disable_account",
  "send_mail",
  "read_mail",
  "permission_change",
  "runtime_change",
])

const SUPPORTED_OPERATIONS = new Set(EMAIL_OPERATION_TYPES)

const SECRET_FIELD_NAMES = new Set([
  "password",
  "plainPassword",
  "mailboxPassword",
  "smtpPassword",
  "imapPassword",
  "apiKey",
  "api_key",
  "privateKey",
  "private_key",
  "dkimPrivateKey",
  "token",
  "secretValue",
  "authorization",
])

export function assertNoSecretFields(value, path = "") {
  if (value === null || value === undefined || typeof value !== "object") return
  if (Array.isArray(value)) {
    value.forEach((item, idx) => assertNoSecretFields(item, `${path}[${idx}]`))
    return
  }
  for (const [key, nested] of Object.entries(value)) {
    const nextPath = path ? `${path}.${key}` : key
    if (SECRET_FIELD_NAMES.has(key)) {
      throw new Error(`${nextPath} must not contain plaintext secret fields`)
    }
    assertNoSecretFields(nested, nextPath)
  }
}

export function classifyEmailOperation(input = {}) {
  const operationType = String(input.operationType ?? "").toLowerCase()
  if (!SUPPORTED_OPERATIONS.has(operationType)) {
    throw new Error(`Unsupported email operation: ${input.operationType}`)
  }
  const environment = String(input.environment ?? "dev").toLowerCase() === "production" ? "prod" : String(input.environment ?? "dev").toLowerCase()

  let riskLevel = "high"
  if (operationType === "inspect") riskLevel = "low"
  else if (operationType === "read_mail") riskLevel = input.mailboxAccess === "self" ? "medium" : "high"
  else if (operationType === "send_mail") riskLevel = environment === "prod" && input.externalDelivery ? "high" : "medium"
  else if (["create_account", "reset_password", "disable_account"].includes(operationType)) riskLevel = environment === "prod" ? "high" : "medium"

  let requiresApproval = true
  if (operationType === "inspect") requiresApproval = false
  else if (operationType === "read_mail") requiresApproval = input.mailboxAccess !== "self"
  else if (operationType === "send_mail") requiresApproval = environment === "prod" && Boolean(input.externalDelivery)
  else if (["create_account", "reset_password", "disable_account"].includes(operationType)) requiresApproval = environment === "prod"

  return {
    operationType,
    environment,
    riskLevel,
    requiresApproval,
    requiresEvidence: operationType !== "read_mail",
  }
}

export function createEmailOperationRecord(input = {}) {
  assertNoSecretFields(input)
  const classification = classifyEmailOperation(input)
  const payload = JSON.parse(JSON.stringify(input.payload ?? {}))
  return {
    schema: "doemail.operation-record.v1",
    operationId: input.operationId || `emop_${createHash("sha256").update(`${Date.now()}:${classification.operationType}`).digest("hex").slice(0, 12)}`,
    createdAt: new Date().toISOString(),
    ...classification,
    target: String(input.target || ""),
    payload,
  }
}

export function buildRequiredDnsRecords(input = {}) {
  if (!input.domain) throw new Error("domain is required")
  if (!input.mailHostname) throw new Error("mailHostname is required")
  const publicIp = input.publicIp || "<mail-server-public-ip>"
  const dkimSelector = input.dkimSelector || "default"
  const dkimValue = input.dkimPublicKey || "<dkim-public-key>"
  return [
    { type: "A", name: `${input.mailHostname}.`, expected: `${input.mailHostname}. A ${publicIp}`, purpose: "Point mail hostname to public mail node IP." },
    { type: "MX", name: `${input.domain}.`, expected: `${input.domain}. MX 10 ${input.mailHostname}.`, purpose: "Route inbound mail for the domain." },
    { type: "SPF", name: `${input.domain}.`, expected: `${input.domain}. TXT "v=spf1 mx -all"`, purpose: "Authorize the MX host to send mail." },
    { type: "DKIM", name: `${dkimSelector}._domainkey.${input.domain}.`, expected: `${dkimSelector}._domainkey.${input.domain}. TXT "v=DKIM1; k=rsa; p=${dkimValue}"`, purpose: "Publish DKIM public key." },
    { type: "DMARC", name: `_dmarc.${input.domain}.`, expected: `_dmarc.${input.domain}. TXT "v=DMARC1; p=none; rua=mailto:postmaster@${input.domain}"`, purpose: "Enable DMARC reporting." },
    { type: "rDNS", name: publicIp, expected: `${publicIp} PTR -> ${input.mailHostname}`, purpose: "Configure reverse DNS at the IP/cloud provider." },
  ]
}
