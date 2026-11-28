import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import { createAccountCreatePlan, createDnsPlan, createListenerPlan, evaluateDns, validateAccountManifest } from "./doemail.mjs"

test("doemail source has zero site-branches or plaintext account branch references", () => {
  const src = readFileSync(new URL("./doemail.mjs", import.meta.url), "utf8")
  assert.ok(!src.includes("site-branches"), "doemail.mjs must never import site-branches")
  assert.ok(!src.includes("accounts.json"), "doemail.mjs must never reference plaintext accounts.json")
})

test("createDnsPlan and evaluateDns report required records and missing items", () => {
  const plan = createDnsPlan({ domain: "example.com", "mail-hostname": "mail.example.com", "public-ip": "203.0.113.10" })
  assert.equal(plan.records.length, 6)
  assert.ok(plan.records.some((r) => r.type === "DMARC"))

  const ev = evaluateDns({ domain: "example.com", "a-ok": true, "mx-ok": true })
  assert.equal(ev.readyForExternalDelivery, false)
  assert.deepEqual(ev.missingRecords, ["SPF", "DKIM", "DMARC", "rDNS"])
})

test("account plans and manifest validation enforce Secret references and reject plaintext password fields", () => {
  const plan = createAccountCreatePlan({
    address: "ops@example.com",
    "password-secret-ref": "k8s://mail/secret/ops/password",
    env: "prod",
  })
  assert.equal(plan.policy.riskLevel, "high")
  assert.equal(plan.policy.requiresApproval, true)

  assert.throws(
    () => createAccountCreatePlan({ address: "ops@example.com", "password-secret-ref": "k8s://mail/secret/ops/password", password: "CANARY_PASSWORD" }),
    /plaintext secret/,
  )
  assert.throws(
    () => validateAccountManifest({ accounts: [{ address: "ops@example.com", passwordSecretRef: "plain-not-uri" }] }),
    /Secret reference/,
  )
  assert.equal(
    validateAccountManifest({ accounts: [{ address: "ops@example.com", passwordSecretRef: "k8s://mail/secret/ops/password" }] }).valid,
    true,
  )
})

test("listener plan stays strictly bounded to run-once", () => {
  const listener = createListenerPlan({ mailbox: "alerts@example.com", "max-messages": "999" })
  assert.equal(listener.mode, "run-once")
  assert.equal(listener.maxMessages, 50)
  assert.ok(listener.approvalRequiredActions.includes("send_mail"))
})
