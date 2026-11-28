#!/usr/bin/env node
/**
 * Standalone zero-dependency stdio MCP Server packaged inside the DoEmail plugin.
 *
 * Runs completely outside the DoOps Agent binary: synced with the plugin package
 * under <skills_root>/doemail/mcp/server.mjs and registered via platform_mcp_servers.
 * Enforces zero plaintext passwords, strict Secret URI references, and workspace
 * path confinement for manifest validation.
 */
import { readFileSync, realpathSync } from "node:fs"
import { isAbsolute, resolve, sep } from "node:path"
import { createInterface } from "node:readline"
import { fileURLToPath } from "node:url"
import {
  createAccountCreatePlan,
  createDnsPlan,
  createIntegrationGuide,
  createKubernetesInspectPlan,
  createListenerPlan,
  evaluateDns,
  validateAccountManifest,
} from "../scripts/doemail.mjs"
import { assertNoSecretFields } from "../scripts/email-policy.mjs"

const SECRET_REF_RE = /^(k8s|secret|vault|doops):\/\/[a-zA-Z0-9._/-]+$/

const TOOLS = [
  {
    name: "dns_plan",
    description: "Generate required A/MX/SPF/DKIM/DMARC/rDNS records for onboarding a mail domain.",
    inputSchema: {
      type: "object",
      additionalProperties: false,
      required: ["domain"],
      properties: {
        domain: { type: "string" },
        mail_hostname: { type: "string" },
        public_ip: { type: "string" },
        dkim_selector: { type: "string" },
        dkim_public_key: { type: "string" },
      },
    },
  },
  {
    name: "dns_evaluate",
    description: "Evaluate mail domain DNS readiness from boolean record verification flags.",
    inputSchema: {
      type: "object",
      additionalProperties: false,
      required: ["domain"],
      properties: {
        domain: { type: "string" },
        mail_hostname: { type: "string" },
        a_ok: { type: "boolean" },
        mx_ok: { type: "boolean" },
        spf_ok: { type: "boolean" },
        dkim_ok: { type: "boolean" },
        dmarc_ok: { type: "boolean" },
        rdns_ok: { type: "boolean" },
      },
    },
  },
  {
    name: "integration_guide",
    description: "Generate secret-free SMTP/IMAPS developer integration guidance.",
    inputSchema: {
      type: "object",
      additionalProperties: false,
      required: ["domain"],
      properties: {
        domain: { type: "string" },
        mail_hostname: { type: "string" },
        language: { type: "string", enum: ["python", "node", "java", "go"] },
      },
    },
  },
  {
    name: "k8s_inspect_plan",
    description: "Generate read-only kubectl commands to inspect a Kubernetes mailserver runtime.",
    inputSchema: {
      type: "object",
      additionalProperties: false,
      properties: {
        namespace: { type: "string" },
        deployment: { type: "string" },
      },
    },
  },
  {
    name: "account_create_plan",
    description: "Plan mailbox account creation using a Secret reference URI (plaintext passwords strictly forbidden).",
    inputSchema: {
      type: "object",
      additionalProperties: false,
      required: ["address", "password_secret_ref"],
      properties: {
        address: { type: "string" },
        password_secret_ref: { type: "string" },
        environment: { type: "string", enum: ["dev", "staging", "prod", "production"] },
        namespace: { type: "string" },
        deployment: { type: "string" },
      },
    },
  },
  {
    name: "listener_plan",
    description: "Generate a bounded run-once mailbox event listener plan with approval rules.",
    inputSchema: {
      type: "object",
      additionalProperties: false,
      required: ["mailbox"],
      properties: {
        mailbox: { type: "string" },
        max_messages: { type: "integer", minimum: 1, maximum: 50 },
      },
    },
  },
  {
    name: "validate_manifest",
    description: "Validate a workspace-relative JSON account manifest enforcing Secret references and zero plaintext passwords.",
    inputSchema: {
      type: "object",
      additionalProperties: false,
      required: ["manifest_file"],
      properties: {
        manifest_file: { type: "string" },
      },
    },
  },
]

function assertCleanArgs(args) {
  if (!args || typeof args !== "object" || Array.isArray(args)) {
    throw new Error("arguments must be a JSON object")
  }
  assertNoSecretFields(args)
  for (const key of Object.keys(args)) {
    if (key === "password_secret_ref") continue
    if (/password|passwd|secret|token|private_key/i.test(key)) {
      throw new Error(`forbidden plaintext sensitive field: ${key}`)
    }
  }
}

function resolveWorkspaceFile(relPath) {
  const val = String(relPath || "").trim()
  if (!val || isAbsolute(val) || val.includes("\0") || val.split(/[\\/]/).includes("..")) {
    throw new Error("manifest_file must be a relative path inside the working directory")
  }
  const cwd = realpathSync(process.cwd())
  const full = realpathSync(resolve(cwd, val))
  if (!full.startsWith(cwd + sep)) {
    throw new Error("manifest_file escapes working directory")
  }
  return full
}

export function dispatchTool(name, args = {}) {
  assertCleanArgs(args)
  switch (name) {
    case "dns_plan":
      return createDnsPlan({
        domain: args.domain,
        "mail-hostname": args.mail_hostname,
        "public-ip": args.public_ip,
        "dkim-selector": args.dkim_selector,
        "dkim-public-key": args.dkim_public_key,
      })
    case "dns_evaluate":
      return evaluateDns({
        domain: args.domain,
        "mail-hostname": args.mail_hostname,
        "a-ok": Boolean(args.a_ok),
        "mx-ok": Boolean(args.mx_ok),
        "spf-ok": Boolean(args.spf_ok),
        "dkim-ok": Boolean(args.dkim_ok),
        "dmarc-ok": Boolean(args.dmarc_ok),
        "rdns-ok": Boolean(args.rdns_ok),
      })
    case "integration_guide":
      return createIntegrationGuide({
        domain: args.domain,
        "mail-hostname": args.mail_hostname,
        language: args.language,
      })
    case "k8s_inspect_plan":
      return createKubernetesInspectPlan({
        namespace: args.namespace,
        deployment: args.deployment,
      })
    case "account_create_plan": {
      const ref = String(args.password_secret_ref || "").trim()
      if (!SECRET_REF_RE.test(ref)) {
        throw new Error("password_secret_ref must be a valid Secret reference URI (e.g. k8s://mail/secret/ops/password)")
      }
      return createAccountCreatePlan({
        address: args.address,
        "password-secret-ref": ref,
        env: args.environment,
        namespace: args.namespace,
        deployment: args.deployment,
      })
    }
    case "listener_plan":
      return createListenerPlan({
        mailbox: args.mailbox,
        "max-messages": args.max_messages,
      })
    case "validate_manifest": {
      const fullPath = resolveWorkspaceFile(args.manifest_file)
      const parsed = JSON.parse(readFileSync(fullPath, "utf8"))
      return validateAccountManifest(parsed)
    }
    default: {
      const err = new Error(`unknown tool: ${name}`)
      err.code = -32601
      throw err
    }
  }
}

function startStdio() {
  const rl = createInterface({ input: process.stdin, crlfDelay: Infinity })
  rl.on("line", (line) => {
    const trimmed = line.trim()
    if (!trimmed) return
    let req
    try {
      req = JSON.parse(trimmed)
    } catch {
      process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "parse error" } })}\n`)
      return
    }
    const id = req.id ?? null
    if (req.method === "notifications/initialized") return
    let resp
    if (req.method === "initialize") {
      resp = {
        jsonrpc: "2.0",
        id,
        result: {
          protocolVersion: "2025-03-26",
          capabilities: { tools: {} },
          serverInfo: { name: "doemail-plugin-mcp", version: "1.0.0" },
        },
      }
    } else if (req.method === "tools/list") {
      resp = { jsonrpc: "2.0", id, result: { tools: TOOLS } }
    } else if (req.method === "tools/call") {
      const params = req.params || {}
      try {
        const out = dispatchTool(params.name, params.arguments || {})
        resp = {
          jsonrpc: "2.0",
          id,
          result: { content: [{ type: "text", text: JSON.stringify(out, null, 2) }], isError: false },
        }
      } catch (err) {
        if (err && err.code === -32601) {
          resp = { jsonrpc: "2.0", id, error: { code: -32601, message: err.message } }
        } else {
          resp = {
            jsonrpc: "2.0",
            id,
            result: { content: [{ type: "text", text: err instanceof Error ? err.message : String(err) }], isError: true },
          }
        }
      }
    } else {
      resp = { jsonrpc: "2.0", id, error: { code: -32601, message: "method not found" } }
    }
    process.stdout.write(`${JSON.stringify(resp)}\n`)
  })
}

if (process.argv[1] && realpathSync(fileURLToPath(import.meta.url)) === realpathSync(process.argv[1])) {
  startStdio()
}
