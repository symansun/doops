import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import {
  DEFAULT_MACHINE_PAGE_SIZE,
  MACHINE_PAGE_SIZE_OPTIONS,
  paginateItems,
  visiblePageNumbers,
} from "./machine-pagination.ts"

test("Given 388 machines, When paginating with default page size 50, Then each page renders a bounded slice and clamps out-of-range pages", () => {
  const machines = Array.from({ length: 388 }, (_, idx) => `m-${idx + 1}`)

  const first = paginateItems(machines, 1, DEFAULT_MACHINE_PAGE_SIZE)
  assert.equal(first.page, 1)
  assert.equal(first.pageSize, 50)
  assert.equal(first.totalItems, 388)
  assert.equal(first.totalPages, 8)
  assert.equal(first.items.length, 50)
  assert.equal(first.items[0], "m-1")
  assert.equal(first.items[49], "m-50")
  assert.equal(first.startIndex, 1)
  assert.equal(first.endIndex, 50)
  assert.equal(first.hasPrev, false)
  assert.equal(first.hasNext, true)

  const last = paginateItems(machines, 8, DEFAULT_MACHINE_PAGE_SIZE)
  assert.equal(last.page, 8)
  assert.equal(last.items.length, 38)
  assert.equal(last.items[0], "m-351")
  assert.equal(last.items[37], "m-388")
  assert.equal(last.startIndex, 351)
  assert.equal(last.endIndex, 388)
  assert.equal(last.hasPrev, true)
  assert.equal(last.hasNext, false)

  // Clamps pages beyond totalPages (e.g. after narrowing filters) and below 1.
  const clampedHigh = paginateItems(machines, 99, 100)
  assert.equal(clampedHigh.page, 4)
  assert.equal(clampedHigh.items.length, 88)
  const clampedLow = paginateItems(machines, 0, 20)
  assert.equal(clampedLow.page, 1)
  assert.equal(clampedLow.items.length, 20)
})

test("Given an empty list or invalid page size, When paginating, Then safe defaults are returned", () => {
  const empty = paginateItems([], 3, 50)
  assert.deepEqual(empty, {
    items: [],
    page: 1,
    pageSize: 50,
    totalItems: 0,
    totalPages: 1,
    startIndex: 0,
    endIndex: 0,
    hasPrev: false,
    hasNext: false,
  })
  const fallbackSize = paginateItems(["a", "b", "c"], 1, Number.NaN)
  assert.equal(fallbackSize.pageSize, DEFAULT_MACHINE_PAGE_SIZE)
  assert.deepEqual(fallbackSize.items, ["a", "b", "c"])
  assert.deepEqual([...MACHINE_PAGE_SIZE_OPTIONS], [20, 50, 100, 200])
})

test("Given multi-page counts, When computing visible page buttons, Then ellipses keep navigation compact", () => {
  assert.deepEqual(visiblePageNumbers(1, 1), [1])
  assert.deepEqual(visiblePageNumbers(3, 6), [1, 2, 3, 4, 5, 6])
  assert.deepEqual(visiblePageNumbers(1, 8), [1, 2, "ellipsis", 8])
  assert.deepEqual(visiblePageNumbers(5, 10), [1, "ellipsis", 4, 5, 6, "ellipsis", 10])
  assert.deepEqual(visiblePageNumbers(10, 10), [1, "ellipsis", 9, 10])
})

test("Wiring lock: MachineAssetsPanel paginates rendered table rows instead of rendering all filtered rows at once", () => {
  const src = readFileSync(new URL("../components/machine-assets-panel.tsx", import.meta.url), "utf8")
  assert.match(src, /from "@\/lib\/machine-pagination"/, "MachineAssetsPanel must use lib/machine-pagination")
  assert.match(src, /<TableBody>\{paged\.items\.map\(/, "TableBody must render paged.items rather than all filtered rows")
  assert.doesNotMatch(src, /<TableBody>\{filtered\.map\(/, "TableBody must not render unfiltered/unpaginated filtered.map directly")
  assert.match(src, /aria-label="机器台账分页"/, "MachineAssetsPanel must expose accessible pagination controls")
})
