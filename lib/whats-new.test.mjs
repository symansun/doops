import assert from "node:assert/strict"
import test from "node:test"

import {
  LAST_VERSION_KEY,
  QUIET_DAY_KEY,
  autoOpenDecision,
  hasUnseenVersions,
  localDay,
  rangeReleases,
  readWhatsNewState,
  recordLastVersion,
  setQuietDay,
} from "./whats-new.ts"

function fakeStorage(initial = {}) {
  const values = new Map(Object.entries(initial))
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => void values.set(key, value),
    removeItem: (key) => void values.delete(key),
  }
}

function throwingStorage() {
  return {
    getItem: () => {
      throw new Error("storage disabled")
    },
    setItem: () => {
      throw new Error("storage disabled")
    },
    removeItem: () => {
      throw new Error("storage disabled")
    },
  }
}

const TODAY = "2026-11-11"

function state(overrides = {}) {
  return readWhatsNewState(fakeStorage(overrides))
}

test("a first visit records the version instead of showing history", () => {
  assert.equal(
    autoOpenDecision({ state: state(), currentVersion: "0.2.2", today: TODAY }),
    "record",
  )
  const storage = fakeStorage()
  recordLastVersion(storage, "0.2.2")
  assert.equal(readWhatsNewState(storage).lastVersion, "0.2.2")
})

test("an upgrade opens the notes once", () => {
  assert.equal(
    autoOpenDecision({
      state: state({ [LAST_VERSION_KEY]: "0.2.2" }),
      currentVersion: "0.2.3",
      today: TODAY,
    }),
    "open",
  )
  assert.equal(
    autoOpenDecision({
      state: state({ [LAST_VERSION_KEY]: "0.2.3" }),
      currentVersion: "0.2.3",
      today: TODAY,
    }),
    "skip",
  )
})

test("today's quiet request suppresses only the popup", () => {
  const seen = { [LAST_VERSION_KEY]: "0.2.2" }
  assert.equal(
    autoOpenDecision({ state: state({ ...seen, [QUIET_DAY_KEY]: TODAY }), currentVersion: "0.2.3", today: TODAY }),
    "skip",
  )
  assert.equal(
    autoOpenDecision({ state: state({ ...seen, [QUIET_DAY_KEY]: "2026-11-10" }), currentVersion: "0.2.3", today: TODAY }),
    "open",
  )
  const storage = fakeStorage()
  setQuietDay(storage, TODAY)
  assert.deepEqual(readWhatsNewState(storage), { lastVersion: null, quietDay: TODAY })
  setQuietDay(storage, null)
  assert.equal(readWhatsNewState(storage).quietDay, null)
})

test("a storage that throws behaves like a first visit and never throws", () => {
  const storage = throwingStorage()
  assert.deepEqual(readWhatsNewState(storage), { lastVersion: null, quietDay: null })
  recordLastVersion(storage, "0.2.2")
  setQuietDay(storage, TODAY)
  assert.equal(autoOpenDecision({ state: readWhatsNewState(storage), currentVersion: "0.2.3", today: TODAY }), "record")
})

test("hasUnseenVersions only when a newer release exists", () => {
  assert.equal(hasUnseenVersions({ lastVersion: null, quietDay: null }, "0.2.2"), false)
  assert.equal(hasUnseenVersions({ lastVersion: "0.2.2", quietDay: null }, "0.2.3"), true)
  assert.equal(hasUnseenVersions({ lastVersion: "0.2.3", quietDay: null }, "0.2.3"), false)
  assert.equal(hasUnseenVersions({ lastVersion: "0.2.2", quietDay: null }, ""), false)
})

test("localDay is the browser's calendar day", () => {
  assert.equal(localDay(new Date(2026, 10, 11, 0, 5, 43)), "2026-11-11")
  assert.equal(localDay(new Date(2026, 0, 2, 23, 59, 59)), "2026-01-02")
})

function release(version) {
  return { version, date: null, sections: [] }
}

test("rangeReleases starts after the last seen version, otherwise keeps the latest", () => {
  const releases = [release("0.2.3"), release("0.2.2"), release("0.2.1")]
  assert.deepEqual(
    rangeReleases(releases, { since: "0.2.1" }).map((item) => item.version),
    ["0.2.3", "0.2.2"],
  )
  assert.deepEqual(
    rangeReleases(releases, { since: "0.2.99" }).map((item) => item.version),
    ["0.2.3", "0.2.2", "0.2.1"],
  )
  assert.deepEqual(
    rangeReleases(releases, { since: null, count: 2 }).map((item) => item.version),
    ["0.2.3", "0.2.2"],
  )
  assert.deepEqual(rangeReleases([], { since: "0.2.1" }), [])
})
