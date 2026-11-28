import test from "node:test"
import assert from "node:assert/strict"
import { stat } from "node:fs/promises"
import { join } from "node:path"

import {
  DOCS_CATALOG,
  GITHUB_REPO_URL,
  adjacentDocs,
  markdownDocSlugs,
  rewriteDocLinks,
} from "./docs-catalog.ts"
import { docsRoot, loadDocBySlug } from "./docs.ts"

test("GITHUB_REPO_URL is a valid https GitHub URL exported from server/client-safe module", () => {
  assert.equal(typeof GITHUB_REPO_URL, "string")
  assert.match(GITHUB_REPO_URL, /^https:\/\/github\.com\/[^/]+\/[^/]+$/)
})

test("DOCS_CATALOG has unique slugs and unique non-null source paths", () => {
  const slugs = new Set()
  const sources = new Set()
  for (const item of DOCS_CATALOG) {
    assert.ok(item.slug, "every doc entry needs a slug")
    assert.ok(!slugs.has(item.slug), `duplicate slug: ${item.slug}`)
    slugs.add(item.slug)
    if (item.sourcePath) {
      const normalized = item.sourcePath.toLowerCase()
      assert.ok(!sources.has(normalized), `duplicate sourcePath: ${item.sourcePath}`)
      sources.add(normalized)
    }
  }
})

test("every markdown-backed doc in DOCS_CATALOG exists under backend/docs and loads cleanly", async () => {
  const root = process.cwd()
  const slugs = markdownDocSlugs()
  assert.ok(slugs.length >= 20, `expected at least 20 markdown docs, got ${slugs.length}`)

  for (const slug of slugs) {
    const loaded = await loadDocBySlug(slug, root)
    assert.ok(loaded, `failed to load doc for slug ${slug}`)
    assert.ok(loaded.entry.sourcePath, `missing sourcePath for ${slug}`)
    const info = await stat(join(docsRoot(root), loaded.entry.sourcePath))
    assert.ok(info.isFile(), `sourcePath is not a file: ${loaded.entry.sourcePath}`)
    assert.ok(loaded.headingTitle.length > 0, `empty headingTitle for ${slug}`)
    assert.ok(loaded.body.trim().length > 0, `empty markdown body for ${slug}`)
    assert.ok(loaded.readingMinutes >= 1, `readingMinutes must be >= 1 for ${slug}`)
    // YAML frontmatter should never leak into the rendered body.
    assert.ok(!loaded.body.startsWith("---\n"), `frontmatter leaked into body for ${slug}`)
  }
})

test("loadDocBySlug returns null for unknown slugs or custom React routes", async () => {
  assert.equal(await loadDocBySlug("does-not-exist"), null)
  assert.equal(await loadDocBySlug("deploy"), null)
})

test("rewriteDocLinks maps internal backend/docs links to /docs/<slug> and preserves hashes & fences", () => {
  const sample = [
    "See [Credentials](CREDENTIAL_RESOURCES.md#rotation) and [DoSql](design/DOSQL_ENGINE_SPEC.md).",
    "Outside link: [Script](../scripts/machine_diagnostic_collector.py).",
    "Uncataloged doc: [Migration](GATEWAY_MYSQL_MIGRATION_PLAN.md).",
    "External: [GitHub](https://github.com/symansun/doops) and [Anchor](#section).",
    "```md",
    "[NotRewritten](CREDENTIAL_RESOURCES.md)",
    "```",
  ].join("\n")

  const out = rewriteDocLinks(sample, "DOOPS_USAGE_GUIDE.md")
  assert.match(out, /\[Credentials\]\(\/docs\/credentials#rotation\)/)
  assert.match(out, /\[DoSql\]\(\/docs\/dosql\)/)
  assert.match(
    out,
    /\[Script\]\(https:\/\/github\.com\/symansun\/doops\/blob\/main\/backend\/scripts\/machine_diagnostic_collector\.py\)/,
  )
  assert.match(
    out,
    /\[Migration\]\(https:\/\/github\.com\/symansun\/doops\/blob\/main\/backend\/docs\/GATEWAY_MYSQL_MIGRATION_PLAN\.md\)/,
  )
  assert.match(out, /\[GitHub\]\(https:\/\/github\.com\/symansun\/doops\)/)
  assert.match(out, /\[Anchor\]\(#section\)/)
  assert.match(out, /\[NotRewritten\]\(CREDENTIAL_RESOURCES\.md\)/)
})

test("rewriteDocLinks resolves parent links from backend/docs/design/*", () => {
  const sample = "Related: [WebUI](../DOOPS_WEBUI.md) and [API](../DOOPS_API_GUIDE.md#auth)."
  const out = rewriteDocLinks(sample, "design/SKILL_FIRST_INSPECTION_SPEC.md")
  assert.match(out, /\[WebUI\]\(\/docs\/webui\)/)
  assert.match(out, /\[API\]\(\/docs\/api-guide#auth\)/)
})

test("adjacentDocs chains entries in catalog order without holes", () => {
  const first = adjacentDocs(DOCS_CATALOG[0].slug)
  assert.equal(first.prev, null)
  assert.equal(first.next?.slug, DOCS_CATALOG[1].slug)

  const last = adjacentDocs(DOCS_CATALOG[DOCS_CATALOG.length - 1].slug)
  assert.equal(last.next, null)
  assert.equal(last.prev?.slug, DOCS_CATALOG[DOCS_CATALOG.length - 2].slug)
})
