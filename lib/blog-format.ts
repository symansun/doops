// Shared, dependency-free pieces of the blog reader.
//
// This module must stay importable from Client Components: it deliberately
// contains no `node:` imports. The filesystem side lives in `lib/blog.ts`,
// which re-exports everything here so server code needs a single import.

export const BLOG_LANGS = ["zh", "en", "ru"] as const

export type BlogLang = (typeof BLOG_LANGS)[number]

export const BLOG_LANG_LABELS: Record<BlogLang, string> = {
  zh: "中文",
  en: "English",
  ru: "Русский",
}

/** UI strings the blog routes need; articles are already written per language. */
export const BLOG_UI: Record<BlogLang, { allPosts: string; minutes: string; backToAll: string; empty: string }> = {
  zh: { allPosts: "全部文章", minutes: "分钟", backToAll: "返回文章列表", empty: "暂无文章" },
  en: { allPosts: "All posts", minutes: "min read", backToAll: "Back to all posts", empty: "No posts yet" },
  ru: { allPosts: "Все статьи", minutes: "мин чтения", backToAll: "К списку статей", empty: "Пока нет статей" },
}

/** URL prefix of the blog image route; the route maps it to `blog/images/`. */
export const BLOG_IMAGE_PREFIX = "/blog/images"

export type BlogPostMeta = {
  lang: BlogLang
  slug: string
  title: string
  subtitle: string
  date: string
  series: string
  /** Raw front-matter cover path, already resolved to a `/blog/images/…` URL. */
  cover: string
  tags: string[]
  /** Source file name inside `blog/<lang>/`, kept for evidence and debugging. */
  filename: string
  readingMinutes: number
}

export function isBlogLang(value: string | undefined | null): value is BlogLang {
  return typeof value === "string" && (BLOG_LANGS as readonly string[]).includes(value)
}

/**
 * Turn a front-matter or Markdown image path into a URL served by the blog image
 * route. Articles keep their images next to `blog/images/` and reference them as
 * `../images/x.png` (relative to `blog/<lang>/`), so that prefix is stripped.
 * Absolute URLs and data URIs pass through untouched.
 */
export function resolveBlogImageSrc(input: string | null | undefined): string {
  const src = (input ?? "").trim()
  if (!src) return ""
  // http:, https:, data:, mailto:, protocol-relative //…
  if (/^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(src)) return src
  if (src.startsWith("#")) return src
  // Idempotent: callers may pass a value already resolved by the blog reader.
  if (src === BLOG_IMAGE_PREFIX || src.startsWith(`${BLOG_IMAGE_PREFIX}/`)) return src
  const withoutParents = src.replace(/^(?:\.{1,2}\/)+/, "")
  const relative = withoutParents.replace(/^\/+/, "")
  if (!relative) return ""
  const segments = relative.split("/")
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) return ""
  const withoutImagesDir = segments[0] === "images" ? segments.slice(1) : segments
  if (withoutImagesDir.length === 0) return ""
  return `${BLOG_IMAGE_PREFIX}/${withoutImagesDir.map(encodeSegment).join("/")}`
}

function encodeSegment(segment: string): string {
  try {
    return encodeURIComponent(decodeURIComponent(segment))
  } catch {
    return encodeURIComponent(segment)
  }
}

/** `YYYY-MM-DD` in the article's own language, stable across builds. */
export function formatBlogDate(date: string | undefined | null, lang: BlogLang): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec((date ?? "").trim())
  if (!match) return (date ?? "").trim()
  const when = new Date(`${match[1]}-${match[2]}-${match[3]}T00:00:00Z`)
  if (Number.isNaN(when.getTime())) return (date ?? "").trim()
  const locale = lang === "zh" ? "zh-CN" : lang === "ru" ? "ru-RU" : "en-US"
  try {
    return new Intl.DateTimeFormat(locale, {
      year: "numeric",
      month: "long",
      day: "numeric",
      timeZone: "UTC",
    }).format(when)
  } catch {
    return `${match[1]}-${match[2]}-${match[3]}`
  }
}

const CJK_GLOBAL = /[\u3400-\u4dbf\u4e00-\u9fff\u3040-\u30ff\uac00-\ud7af]/g
const WORD_GLOBAL = /[A-Za-z0-9\u0400-\u04ff]+/g

/** Rough reading time; CJK is counted per character, other scripts per word. */
export function readingMinutes(markdown: string): number {
  const text = stripMarkdown(markdown)
  const cjk = text.match(CJK_GLOBAL)?.length ?? 0
  const words = text.replace(CJK_GLOBAL, " ").match(WORD_GLOBAL)?.length ?? 0
  return Math.max(1, Math.round(cjk / 380 + words / 220))
}

function stripMarkdown(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/`[^`]*`/g, " ")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, " ")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/<[^>]+>/g, " ")
}
