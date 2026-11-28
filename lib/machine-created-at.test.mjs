import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import {
  formatMachineCreatedDate,
  formatMachineCreatedDetail,
  resolveMachineCreatedAtPayload,
  toMachineDateInput,
} from "./machine-created-at.ts"

test("Given empty or missing created_at, When formatted, Then fallback is shown and input stays empty", () => {
  assert.equal(formatMachineCreatedDate(undefined), "未登记")
  assert.equal(formatMachineCreatedDate(""), "未登记")
  assert.equal(formatMachineCreatedDetail(null), "未登记")
  assert.equal(toMachineDateInput(""), "")
})

test("Given canonical UTC date-only RFC3339, When formatted, Then YYYY-MM-DD is preserved without timezone drift", () => {
  assert.equal(formatMachineCreatedDate("2025-10-20T00:00:00Z"), "2025-10-20")
  assert.equal(formatMachineCreatedDate("2025-10-20"), "2025-10-20")
  assert.equal(formatMachineCreatedDetail("2025-10-20T00:00:00Z"), "2025-10-20")
  assert.equal(toMachineDateInput("2025-10-20T00:00:00Z"), "2025-10-20")
})

test("Given full RFC3339 timestamp with time-of-day, When formatted, Then date input and detail preserve timestamp semantics", () => {
  const iso = "2025-10-20T04:15:30Z"
  assert.match(formatMachineCreatedDate(iso), /^\d{4}-\d{2}-\d{2}$/)
  assert.ok(formatMachineCreatedDetail(iso).includes("2025"))
  const inputVal = toMachineDateInput(iso)
  assert.match(inputVal, /^\d{4}-\d{2}-\d{2}$/)
  // Leaving the date picker untouched on edit must omit created_at so time-of-day is not clobbered.
  assert.equal(resolveMachineCreatedAtPayload(inputVal, iso), undefined)
  assert.equal(resolveMachineCreatedAtPayload("2025-10-21", iso), "2025-10-21")
})

test("Given form submission values, When resolved, Then blank omits created_at and explicit date passes through", () => {
  assert.equal(resolveMachineCreatedAtPayload("", undefined), undefined)
  assert.equal(resolveMachineCreatedAtPayload("  ", "2025-10-20T00:00:00Z"), undefined)
  assert.equal(resolveMachineCreatedAtPayload("2025-10-20", undefined), "2025-10-20")
  assert.equal(resolveMachineCreatedAtPayload("2025-10-20", ""), "2025-10-20")
  assert.equal(resolveMachineCreatedAtPayload("2025-10-20", "2025-10-20T00:00:00Z"), undefined)
  assert.equal(resolveMachineCreatedAtPayload("2025-10-19", "2025-10-20T00:00:00Z"), "2025-10-19")
})

test("Given MachineAssetsPanel source, When inspected, Then 录入时间 is rendered in list, detail, and form", () => {
  const src = readFileSync(new URL("../components/machine-assets-panel.tsx", import.meta.url), "utf8")
  assert.ok(src.includes("录入时间"))
  assert.ok(src.includes("formatMachineCreatedDate"))
  assert.ok(src.includes("formatMachineCreatedDetail"))
  assert.ok(src.includes("resolveMachineCreatedAtPayload"))
  assert.ok(src.includes('name="created_at"'))
})
