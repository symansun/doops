import assert from "node:assert/strict"
import test from "node:test"
import { mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadAliDnsCredentials } from "./ensure-alidns-record.mjs"

test("loadAliDnsCredentials loads from Gateway runtime opaque 0600 JSON file without leaking secrets", () => {
  const dir = mkdtempSync(join(tmpdir(), "doops-alidns-test-"))
  const filePath = join(dir, "env.json")
  const canaryId = "LTAI-SYNTHETIC-TEST-ID"
  const canarySecret = "SYNTHETIC-ALIDNS-SECRET-987654"
  try {
    writeFileSync(
      filePath,
      JSON.stringify({
        ALIYUN_ACCESS_KEY_ID: canaryId,
        ALIYUN_ACCESS_KEY_SECRET: canarySecret,
      }),
      { mode: 0o600 },
    )
    const creds = loadAliDnsCredentials({ DOOPS_CRED_FILE_ALIDNS_CREDENTIAL: filePath })
    assert.equal(creds.source, "doops_runtime_opaque")
    assert.equal(creds.accessKeyId, canaryId)
    assert.equal(creds.accessKeySecret, canarySecret)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test("loadAliDnsCredentials fails closed when credentials or JSON file are missing or invalid", () => {
  assert.throws(
    () => loadAliDnsCredentials({}),
    /missing AliDNS credentials/,
  )
  assert.throws(
    () => loadAliDnsCredentials({ DOOPS_CRED_FILE_ALIDNS_CREDENTIAL: "/nonexistent/doops/env.json" }),
    /failed to read managed runtime credential file/,
  )
})
