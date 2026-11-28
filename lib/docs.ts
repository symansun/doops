// Server-side documentation loader for `/docs/[slug]`.
// Reads authoritative Markdown directly from `backend/docs/` (copied into the
// Docker image by Dockerfile.web) so the web portal never drifts from repo truth.

import { readFile } from "node:fs/promises"
import { join } from "node:path"

import { readingMinutes } from "./blog-format.ts"
import {
  DOCS_CATALOG,
  adjacentDocs,
  findDocBySlug,
  rewriteDocLinks,
  type DocEntry,
} from "./docs-catalog.ts"
import { extractMarkdownToc, type MarkdownTocItem } from "./markdown-render.ts"

export interface LoadedDoc {
  entry: DocEntry
  /** Clean Markdown body with YAML frontmatter and redundant leading H1 stripped. */
  body: string
  /** Heading title extracted from the source H1 (falls back to catalog title). */
  headingTitle: string
  toc: MarkdownTocItem[]
  readingMinutes: number
  prev: DocEntry | null
  next: DocEntry | null
}

const FRONT_MATTER_OPEN = /^---[ \t]*\r?\n/
const FRONT_MATTER_CLOSE = /^---[ \t]*(?:\r?\n|$)/m

export function docsRoot(root: string = process.cwd()): string {
  return join(root, "backend", "docs")
}

function stripFrontMatter(markdown: string): string {
  const text = markdown.replace(/^\uFEFF/, "")
  const open = FRONT_MATTER_OPEN.exec(text)
  if (!open) return text
  const rest = text.slice(open[0].length)
  const close = FRONT_MATTER_CLOSE.exec(rest)
  if (!close || close.index === undefined) return text
  return rest.slice(close.index + close[0].length).replace(/^\r?\n+/, "")
}

function stripLeadingHeading(markdown: string): { heading: string | null; body: string } {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n")
  let idx = 0
  while (idx < lines.length && lines[idx].trim() === "") idx += 1
  if (idx >= lines.length) return { heading: null, body: markdown }
  const match = /^#\s+(.+?)\s*$/.exec(lines[idx].trim())
  if (!match) return { heading: null, body: markdown }
  const remaining = lines.slice(idx + 1).join("\n").replace(/^\n+/, "")
  return { heading: match[1].trim(), body: remaining }
}

export async function loadDocBySlug(
  slug: string,
  root: string = process.cwd(),
): Promise<LoadedDoc | null> {
  const entry = findDocBySlug(slug)
  if (!entry || !entry.sourcePath) return null

  const fullPath = join(docsRoot(root), entry.sourcePath)
  const raw = await readFile(fullPath, "utf8").catch(() => null)
  if (raw === null) return null

  const withoutFm = stripFrontMatter(raw)
  const { heading, body: withoutH1 } = stripLeadingHeading(withoutFm)
  const rewritten = rewriteDocLinks(withoutH1, entry.sourcePath)
  const toc = extractMarkdownToc(rewritten).filter((item) => item.level >= 2 && item.level <= 3)
  const { prev, next } = adjacentDocs(slug)

  return {
    entry,
    body: rewritten,
    headingTitle: heading || entry.title,
    toc,
    readingMinutes: readingMinutes(rewritten),
    prev,
    next,
  }
}

export function allCatalogDocs(): DocEntry[] {
  return DOCS_CATALOG
}
