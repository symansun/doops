import test from "node:test"
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdtempSync, writeFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { fileURLToPath } from "node:url"

const SERVER_PATH = fileURLToPath(new URL("./server.mjs", import.meta.url))

function callMCP(requests, cwd) {
  const input = `${requests.map((r) => JSON.stringify(r)).join("\n")}\n`
  const stdout = execFileSync(process.execPath, [SERVER_PATH], {
    input,
    cwd,
    encoding: "utf8",
  })
  return stdout
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => JSON.parse(line))
}

test("doemail standalone MCP server handles initialize, tools/list, dns_plan, and account_create_plan", () => {
  const work = mkdtempSync(join(tmpdir(), "doemail-mcp-"))
  try {
    const resps = callMCP([
      { jsonrpc: "2.0", id: 1, method: "initialize", params: {} },
      { jsonrpc: "2.0", id: 2, method: "tools/list", params: {} },
      {
        jsonrpc: "2.0",
        id: 3,
        method: "tools/call",
        params: { name: "dns_plan", arguments: { domain: "example.org", public_ip: "203.0.113.20" } },
      },
      {
        jsonrpc: "2.0",
        id: 4,
        method: "tools/call",
        params: {
          name: "account_create_plan",
          arguments: { address: "ops@example.org", password_secret_ref: "k8s://mail/secret/ops/password", environment: "prod" },
        },
      },
    ], work)
    assert.equal(resps[0].result.serverInfo.name, "doemail-plugin-mcp")
    const names = resps[1].result.tools.map((t) => t.name)
    assert.ok(names.includes("dns_plan") && names.includes("account_create_plan") && names.includes("validate_manifest"))
    assert.equal(resps[2].result.isError, false)
    assert.match(resps[2].result.content[0].text, /doemail\.dns-plan\.v1/)
    assert.equal(resps[3].result.isError, false)
    assert.match(resps[3].result.content[0].text, /"requiresApproval": true/)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

test("doemail standalone MCP server rejects plaintext passwords, non-URI refs, dirty manifests, and path traversal", () => {
  const work = mkdtempSync(join(tmpdir(), "doemail-mcp-sec-"))
  try {
    writeFileSync(
      join(work, "dirty.json"),
      JSON.stringify({ accounts: [{ address: "ops@example.org", password: "canary-plaintext-pw", passwordSecretRef: "k8s://mail/secret/ops/password" }] }),
    )
    writeFileSync(
      join(work, "clean.json"),
      JSON.stringify({ accounts: [{ address: "ops@example.org", passwordSecretRef: "k8s://mail/secret/ops/password" }] }),
    )
    const resps = callMCP([
      {
        jsonrpc: "2.0",
        id: 10,
        method: "tools/call",
        params: { name: "account_create_plan", arguments: { address: "ops@example.org", password: "canary-arg-pw", password_secret_ref: "k8s://mail/secret/ops/password" } },
      },
      {
        jsonrpc: "2.0",
        id: 11,
        method: "tools/call",
        params: { name: "account_create_plan", arguments: { address: "ops@example.org", password_secret_ref: "not-a-uri" } },
      },
      {
        jsonrpc: "2.0",
        id: 12,
        method: "tools/call",
        params: { name: "validate_manifest", arguments: { manifest_file: "dirty.json" } },
      },
      {
        jsonrpc: "2.0",
        id: 13,
        method: "tools/call",
        params: { name: "validate_manifest", arguments: { manifest_file: "../escape.json" } },
      },
      {
        jsonrpc: "2.0",
        id: 14,
        method: "tools/call",
        params: { name: "validate_manifest", arguments: { manifest_file: "clean.json" } },
      },
    ], work)
    for (let i = 0; i < 4; i++) {
      assert.equal(resps[i].result.isError, true, `case ${i} should fail closed`)
      assert.ok(!resps[i].result.content[0].text.includes("canary-"), `case ${i} leaked secret`)
    }
    assert.equal(resps[4].result.isError, false)
    assert.match(resps[4].result.content[0].text, /"valid": true/)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})
