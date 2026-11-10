import assert from "node:assert/strict"
import { mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"

import {
  changeCategoryLabel,
  compareVersions,
  mergeReleases,
  parseChangelog,
  parseInline,
  selectReleases,
} from "./changelog.ts"
import { loadChangelogReleases } from "./server/changelog.ts"

const SAMPLE = `# Changelog

本文件由控制台「更新内容」读取。

## [Unreleased]

### Added

- 尚未发布的条目

## [0.2.2] — 2026-11-08

### Added

- gateway \`/metrics\` Prometheus 端点
- token 轮换管理

### Changed

- install.sh 默认从 GitHub Release 下载

### Fixed

- gateway-only 校验错位

### Security

- 护栏拦截事件纳入审计

## [0.2.1] - 2026-11-08

### Added

- doops audit 审计查询命令

## [0.2.0] — 2026-11-08

### Added

- agent 命令护栏

### Fixed

- 旧 dispatcher 的误伤
- 同一版本里的重复条目

### Fixed

- 同一版本里的重复条目
`

test("parseChangelog reads release headers, categories and dates", () => {
  const parsed = parseChangelog(SAMPLE)
  assert.deepEqual(
    parsed.releases.map((release) => release.version),
    ["0.2.2", "0.2.1", "0.2.0"],
  )
  assert.deepEqual(
    parsed.releases.map((release) => release.date),
    ["2026-11-08", "2026-11-08", "2026-11-08"],
  )
  const sections = parsed.releases[0].sections
  assert.deepEqual(
    sections.map((section) => section.category),
    ["added", "changed", "fixed", "security"],
  )
  assert.deepEqual(sections.map((section) => section.label), ["新功能", "改进", "修复", "安全"])
  assert.equal(sections[0].items.length, 2)
  assert.match(sections[0].items[0], /\/metrics/)
})

test("parseChangelog keeps Unreleased out of the shown releases", () => {
  const parsed = parseChangelog(SAMPLE)
  assert.equal(parsed.releases.some((release) => release.version === "0.2.99"), false)
  assert.equal(parsed.unreleased.length, 1)
  assert.equal(parsed.unreleased[0].label, "新功能")
  assert.deepEqual(parsed.unreleased[0].items, ["尚未发布的条目"])
})

test("parseChangelog accepts Chinese headings, v prefixes and parenthesized dates", () => {
  const parsed = parseChangelog(`## v0.3.0 (2026-11-12)

### 新功能

- 控制台「更新内容」

### 修复

- 某个问题
`)
  assert.equal(parsed.releases.length, 1)
  assert.equal(parsed.releases[0].version, "0.3.0")
  assert.equal(parsed.releases[0].date, "2026-11-12")
  assert.deepEqual(
    parsed.releases[0].sections.map((section) => section.category),
    ["added", "fixed"],
  )
})

test("parseChangelog folds wrapped bullets and unknown headings into 其他", () => {
  const parsed = parseChangelog(`## [0.2.2] — 2026-11-08

### Something New

- 第一行
  续行内容
- 第二条
`)
  const sections = parsed.releases[0].sections
  assert.equal(sections.length, 1)
  assert.equal(sections[0].category, "other")
  assert.equal(sections[0].label, "其他")
  assert.deepEqual(sections[0].items, ["第一行 续行内容", "第二条"])
})

test("parseChangelog skips empty releases and repeated versions", () => {
  const parsed = parseChangelog(`## [0.2.3] — 2026-11-09

### Added

## [0.2.2] — 2026-11-08

### Added

- 第一次

## [0.2.2] — 2026-11-08

### Added

- 重复的版本小节
`)
  assert.deepEqual(
    parsed.releases.map((release) => release.version),
    ["0.2.2"],
  )
  assert.deepEqual(parsed.releases[0].sections[0].items, ["第一次"])
})

test("compareVersions orders numbers, prereleases and v prefixes", () => {
  assert.equal(compareVersions("0.2.10", "0.2.9"), 1)
  assert.equal(compareVersions("0.2.2", "v0.2.2"), 0)
  assert.equal(compareVersions("0.2.3-rc.1", "0.2.3"), -1)
  assert.equal(compareVersions("0.2.3-rc.2", "0.2.3-rc.10"), -1)
  assert.equal(compareVersions("1.0.0", "0.9.9"), 1)
})

test("selectReleases ranges by last seen version or by count", () => {
  const releases = parseChangelog(SAMPLE).releases
  assert.deepEqual(
    selectReleases(releases, { since: "0.2.0" }).map((release) => release.version),
    ["0.2.2", "0.2.1"],
  )
  assert.deepEqual(selectReleases(releases, { since: "0.2.2" }), [])
  assert.deepEqual(
    selectReleases(releases, { since: "0.1.0" }).map((release) => release.version),
    ["0.2.2", "0.2.1", "0.2.0"],
  )
  assert.deepEqual(
    selectReleases(releases, { count: 2 }).map((release) => release.version),
    ["0.2.2", "0.2.1"],
  )
})

test("mergeReleases groups by category, tags versions and drops repeats", () => {
  const releases = parseChangelog(SAMPLE).releases
  const merged = mergeReleases(releases)
  assert.ok(merged)
  assert.equal(merged.versions, 3)
  assert.equal(merged.from, "0.2.0")
  assert.equal(merged.to, "0.2.2")
  assert.deepEqual(
    merged.sections.map((section) => section.label),
    ["新功能", "改进", "修复", "安全"],
  )
  const fixed = merged.sections.find((section) => section.category === "fixed")
  assert.ok(fixed)
  assert.equal(
    fixed.items.filter((item) => item.text === "同一版本里的重复条目").length,
    1,
  )
  assert.equal(merged.sections[0].items[0].version, "0.2.2")
})

test("mergeReleases returns null when nothing is left", () => {
  assert.equal(mergeReleases([]), null)
})

test("parseInline renders code, bold and safe links only", () => {
  const tokens = parseInline("用 \`code\`、**粗体** 和 [文档](https://example.com/a) 说明")
  assert.deepEqual(
    tokens.map((token) => token.kind),
    ["text", "code", "text", "strong", "text", "link", "text"],
  )
  assert.equal(tokens[5].href, "https://example.com/a")
  const unsafe = parseInline("[点我](javascript:alert(1))")
  assert.equal(unsafe.some((token) => token.kind === "link"), false)
  assert.equal(unsafe.map((token) => token.text).join(""), "[点我](javascript:alert(1))")
})

test("changeCategoryLabel covers every category", () => {
  for (const category of ["added", "changed", "performance", "fixed", "security", "deprecated", "removed", "other"]) {
    assert.ok(changeCategoryLabel(category).length > 0)
  }
})

test("the repository CHANGELOG.md parses for the console", async () => {
  // Dockerfile.web copies this file into the Web image, so a checkout that
  // cannot parse it would ship a console with an empty release log.
  const releases = await loadChangelogReleases(process.cwd())
  assert.ok(releases.length > 0, "CHANGELOG.md must exist and carry at least one release")
  assert.match(releases[0].version, /^\d+\.\d+\.\d+/)
  for (const release of releases) {
    assert.ok(release.sections.length > 0, release.version + " has no sections")
    for (const section of release.sections) {
      assert.ok(section.label.length > 0)
      assert.ok(section.items.length > 0, release.version + "/" + section.category + " is empty")
    }
  }
})

test("loadChangelogReleases reads the file and tolerates a missing one", async () => {
  const root = mkdtempSync(join(tmpdir(), "doops-changelog-"))
  try {
    assert.deepEqual(await loadChangelogReleases(root), [])
    writeFileSync(join(root, "CHANGELOG.md"), SAMPLE)
    const releases = await loadChangelogReleases(root)
    assert.deepEqual(
      releases.map((release) => release.version),
      ["0.2.2", "0.2.1", "0.2.0"],
    )
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
