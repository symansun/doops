// Server-side blog reader: front matter parsing plus the list/lookup helpers
// the /blog routes need. Everything here reads `blog/` from the deployed
// working directory, so the image build must copy that folder in (Dockerfile.web).
//
// Articles live in `blog/<lang>/<semantic-slug>.md` with YAML-ish front matter:
//
//   ---
//   title: "…"        subtitle: "…"      lang: zh
//   date: 2026-11-11  slug: after-…      series: "…"
//   cover: ../images/x.png               tags: [DoOps, 运维]
//   ---
//
// Body images use `../images/x.png` (relative to the language directory) and are
// served by `app/blog/images/[...path]/route.ts`.

import { readdir, readFile } from "node:fs/promises"
import { join } from "node:path"

import {
  BLOG_IMAGE_PREFIX,
  BLOG_LANGS,
  BLOG_LANG_LABELS,
  BLOG_UI,
  formatBlogDate,
  isBlogLang,
  readingMinutes,
  resolveBlogImageSrc,
  type BlogLang,
  type BlogPostMeta,
} from "@/lib/blog-format"

export {
  BLOG_IMAGE_PREFIX,
  BLOG_LANGS,
  BLOG_LANG_LABELS,
  BLOG_UI,
  formatBlogDate,
  isBlogLang,
  readingMinutes,
  resolveBlogImageSrc,
}
export type { BlogLang, BlogPostMeta }

export type BlogPost = BlogPostMeta & { body: string }

export type FrontMatterValue = string | string[]

const FRONT_MATTER_OPEN = /^---[ \t]*\r?\n/
const FRONT_MATTER_CLOSE = /^---[ \t]*(?:\r?\n|$)/m
const ARTICLE_FILE = /\.md$/i
const NON_ARTICLE_FILE = /^(?:readme|index)\.md$/i
const LIST_ITEM = /^\s*-\s+(.*)$/
const FIELD_PAIR = /^([A-Za-z_][\w-]*)\s*:\s*(.*)$/

/** `blog/` for a checkout or an installed console image. */
export function blogRoot(root: string = process.cwd()): string {
  return join(root, "blog")
}

/** `blog/images`, the only directory the image route may serve from. */
export function blogImagesRoot(root: string = process.cwd()): string {
  return join(blogRoot(root), "images")
}

/**
 * Split a Markdown document into front matter and body. Documents without a
 * leading `---` block are returned as body-only, so a missing header never
 * breaks the page.
 */
export function parseFrontMatter(markdown: string): {
  fields: Record<string, FrontMatterValue>
  body: string
} {
  const text = markdown.replace(/^\uFEFF/, "")
  const open = FRONT_MATTER_OPEN.exec(text)
  if (!open) return { fields: {}, body: text }
  const rest = text.slice(open[0].length)
  const close = FRONT_MATTER_CLOSE.exec(rest)
  if (!close || close.index === undefined) return { fields: {}, body: text }
  const fields = parseFields(rest.slice(0, close.index))
  const body = rest.slice(close.index + close[0].length).replace(/^\r?\n+/, "")
  return { fields, body }
}

function parseFields(raw: string): Record<string, FrontMatterValue> {
  const fields: Record<string, FrontMatterValue> = {}
  let pendingListKey: string | null = null
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) continue
    const item = LIST_ITEM.exec(line)
    if (item && pendingListKey) {
      const existing = fields[pendingListKey]
      const values = Array.isArray(existing) ? existing : existing ? [existing] : []
      fields[pendingListKey] = [...values, parseScalar(item[1])]
      continue
    }
    const pair = FIELD_PAIR.exec(line)
    if (!pair) {
      pendingListKey = null
      continue
    }
    const key = pair[1].toLowerCase()
    const rawValue = pair[2].trim()
    if (rawValue === "") {
      // Could be a block sequence (`tags:\n  - a`) or an empty value.
      pendingListKey = key
      fields[key] = []
      continue
    }
    pendingListKey = null
    fields[key] = parseValue(rawValue)
  }
  return fields
}

function parseValue(raw: string): FrontMatterValue {
  if (raw.startsWith("[")) {
    const inner = raw.replace(/^\[/, "").replace(/\]\s*$/, "")
    return splitInlineList(inner)
  }
  return parseScalar(raw)
}

function splitInlineList(inner: string): string[] {
  const items: string[] = []
  let buffer = ""
  let quote: string | null = null
  for (const char of inner) {
    if (quote) {
      if (char === quote) quote = null
      else buffer += char
      continue
    }
    if (char === '"' || char === "'") {
      quote = char
      continue
    }
    if (char === ",") {
      items.push(parseScalar(buffer))
      buffer = ""
      continue
    }
    buffer += char
  }
  items.push(parseScalar(buffer))
  return items.filter((item) => item !== "")
}

function parseScalar(raw: string): string {
  const value = raw.trim()
  if (value.length >= 2) {
    const first = value[0]
    const last = value[value.length - 1]
    if (first === '"' && last === '"') {
      return value.slice(1, -1).replace(/\\(["\\])/g, "$1").replace(/\\n/g, "\n")
    }
    if (first === "'" && last === "'") {
      return value.slice(1, -1).replace(/''/g, "'")
    }
  }
  return value
}

function asString(value: FrontMatterValue | undefined): string {
  if (typeof value === "string") return value.trim()
  if (Array.isArray(value)) return value.join(", ").trim()
  return ""
}

function asTags(value: FrontMatterValue | undefined): string[] {
  if (Array.isArray(value)) return value.map((tag) => tag.trim()).filter(Boolean)
  const single = asString(value)
  if (!single) return []
  return single.split(",").map((tag) => tag.trim()).filter(Boolean)
}

function isDraft(fields: Record<string, FrontMatterValue>): boolean {
  return ["true", "yes", "1"].includes(asString(fields.draft).toLowerCase())
}

function buildMeta(
  lang: BlogLang,
  filename: string,
  fields: Record<string, FrontMatterValue>,
  body: string,
): BlogPostMeta {
  const fileSlug = filename.replace(ARTICLE_FILE, "")
  return {
    lang,
    slug: asString(fields.slug) || fileSlug,
    title: asString(fields.title) || fileSlug,
    subtitle: asString(fields.subtitle),
    date: asString(fields.date),
    series: asString(fields.series),
    cover: resolveBlogImageSrc(asString(fields.cover)),
    tags: asTags(fields.tags),
    filename,
    readingMinutes: readingMinutes(body),
  }
}

/** Article files of one language, newest first (ties broken by slug). */
export async function listPosts(lang: BlogLang, root: string = process.cwd()): Promise<BlogPostMeta[]> {
  const directory = join(blogRoot(root), lang)
  let entries: string[]
  try {
    entries = await readdir(directory)
  } catch {
    return []
  }
  const posts: BlogPostMeta[] = []
  for (const entry of entries.sort()) {
    if (!ARTICLE_FILE.test(entry) || NON_ARTICLE_FILE.test(entry)) continue
    const markdown = await readFile(join(directory, entry), "utf8").catch(() => null)
    if (markdown === null) continue
    const { fields, body } = parseFrontMatter(markdown)
    if (isDraft(fields)) continue
    posts.push(buildMeta(lang, entry, fields, body))
  }
  return posts.sort((left, right) => {
    if (left.date !== right.date) return left.date < right.date ? 1 : -1
    return left.slug.localeCompare(right.slug)
  })
}

/** Slugs published for one language; used for static params and the switcher. */
export async function listPostSlugs(lang: BlogLang, root: string = process.cwd()): Promise<string[]> {
  const posts = await listPosts(lang, root)
  return posts.map((post) => post.slug)
}

/** Slugs grouped by language, so the switcher can stay on the same article. */
export async function slugMap(root: string = process.cwd()): Promise<Record<BlogLang, string[]>> {
  const grouped = await Promise.all(BLOG_LANGS.map(async (lang) => [lang, await listPostSlugs(lang, root)] as const))
  return Object.fromEntries(grouped) as Record<BlogLang, string[]>
}

/**
 * One article by slug. Both the front-matter `slug` and the file name are
 * accepted, so a renamed header cannot silently 404 a working URL.
 */
export async function getPost(
  lang: BlogLang,
  slug: string,
  root: string = process.cwd(),
): Promise<BlogPost | null> {
  const directory = join(blogRoot(root), lang)
  const wanted = slug.trim()
  if (!wanted) return null
  let entries: string[]
  try {
    entries = await readdir(directory)
  } catch {
    return null
  }
  for (const entry of entries.sort()) {
    if (!ARTICLE_FILE.test(entry) || NON_ARTICLE_FILE.test(entry)) continue
    const fileSlug = entry.replace(ARTICLE_FILE, "")
    const markdown = await readFile(join(directory, entry), "utf8").catch(() => null)
    if (markdown === null) continue
    const { fields, body } = parseFrontMatter(markdown)
    if (isDraft(fields)) continue
    const meta = buildMeta(lang, entry, fields, body)
    if (meta.slug !== wanted && fileSlug !== wanted) continue
    return { ...meta, body: stripDuplicateHeading(body, meta.title) }
  }
  return null
}

/**
 * Articles open with an H1 that repeats the front-matter title, and several also
 * open with a hand-written HTML paragraph of language links before it. The page
 * renders the title itself, so drop that one duplicated heading (skipping any
 * leading raw-HTML blocks) instead of showing the title twice.
 */
function stripDuplicateHeading(body: string, title: string): string {
  const blocks = body.split(/\n{2,}/)
  let index = 0
  while (index < blocks.length && /^\s*</.test(blocks[index])) index += 1
  if (index >= blocks.length) return body
  const match = /^#\s+(.+?)\s*$/.exec(blocks[index].trim())
  if (!match) return body
  if (normalizeHeading(match[1]) !== normalizeHeading(title)) return body
  blocks.splice(index, 1)
  return blocks.join("\n\n")
}

function normalizeHeading(value: string): string {
  return value
    .replace(/[*_`]/g, "")
    .replace(/\s+/g, " ")
    .trim()
    .toLowerCase()
}
