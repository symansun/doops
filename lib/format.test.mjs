import assert from "node:assert/strict"
import test from "node:test"
import { formatDate, formatDateTime, formatMiB, formatNumber } from "./format.ts"

// 这些断言锁住"全站只有一处时间格式"的契约：区域、24 小时制、单位声明和兜底文本。
// 改动前 15 个文件各写一份 toLocaleString，区域和单位都不一致（有的把秒当毫秒，有的没有 1000 换算）。

const zh = (date) => date.toLocaleString("zh-CN", { hour12: false })

test("Given an ISO string, When formatted, Then it uses the shared zh-CN 24-hour form", () => {
  const iso = "2026-11-21T08:30:00Z"
  assert.equal(formatDateTime(iso), zh(new Date(iso)))
  assert.doesNotMatch(formatDateTime(iso), /上午|下午/)
})

test("Given unix seconds, When the caller declares unit s, Then the value is not read as milliseconds", () => {
  const seconds = 1_763_712_600
  assert.equal(formatDateTime(seconds, { unit: "s" }), zh(new Date(seconds * 1000)))
  assert.notEqual(formatDateTime(seconds, { unit: "s" }), formatDateTime(seconds, { unit: "ms" }))
})

test("Given unparsable or missing values, Then the raw string or the fallback is shown, never Invalid Date", () => {
  assert.equal(formatDateTime(null), "—")
  assert.equal(formatDateTime(undefined), "—")
  assert.equal(formatDateTime(""), "—")
  assert.equal(formatDateTime("not-a-date"), "not-a-date")
  assert.equal(formatDateTime(null, { fallback: "-" }), "-")
  assert.doesNotMatch(formatDateTime("not-a-date"), /Invalid Date|NaN/)
})

test("Given an explicit timeZone, Then the same instant renders in that zone", () => {
  const iso = "2026-11-21T00:30:00Z"
  const shanghai = formatDateTime(iso, { timeZone: "Asia/Shanghai" })
  const utc = formatDateTime(iso, { timeZone: "UTC" })
  assert.notEqual(shanghai, utc)
  assert.equal(utc, new Date(iso).toLocaleString("zh-CN", { hour12: false, timeZone: "UTC" }))
})

test("Given dates only, Then the date part has no clock time", () => {
  assert.doesNotMatch(formatDate("2026-11-21T08:30:00Z"), /:/)
  assert.equal(formatDate(null), "—")
})

test("Given non-finite numbers, Then formatting falls back instead of printing NaN", () => {
  assert.equal(formatNumber(1234.5), "1,234.5")
  assert.equal(formatNumber(3.14159, { digits: 2 }), "3.14")
  assert.equal(formatNumber(Number.NaN), "—")
  assert.equal(formatNumber(null), "—")
  assert.equal(formatMiB(1048576), "1")
  assert.equal(formatMiB(1572864), "1.5")
  assert.equal(formatMiB(undefined), "—")
})
