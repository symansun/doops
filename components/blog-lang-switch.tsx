"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"

import { BLOG_LANGS, BLOG_LANG_LABELS, isBlogLang, type BlogLang } from "@/lib/blog-format"

// Language switcher for the blog shell. It keeps the reader on the same article
// when that slug is published in the target language, and falls back to that
// language's index otherwise. `slugs` is the per-language slug map built by the
// layout at build time, so no request is made to decide the destination.
export function BlogLangSwitch({ slugs }: { slugs: Record<BlogLang, string[]> }) {
  const pathname = usePathname() ?? "/blog"
  const segments = pathname.split("/").filter(Boolean)
  const activeLang: BlogLang = isBlogLang(segments[1]) ? segments[1] : "zh"
  const currentSlug = decodeSegment(segments[2])

  return (
    <nav aria-label="Language" className="flex items-center gap-0.5 rounded-full border border-border/60 bg-muted/40 p-0.5">
      {BLOG_LANGS.map((lang) => {
        const staysOnArticle = currentSlug !== "" && (slugs?.[lang] ?? []).includes(currentSlug)
        const href = staysOnArticle ? `/blog/${lang}/${encodeURIComponent(currentSlug)}` : `/blog/${lang}`
        const active = lang === activeLang
        return (
          <Link
            key={lang}
            href={href}
            hrefLang={lang}
            aria-current={active ? "page" : undefined}
            className={`rounded-full px-2.5 py-1 text-xs font-medium transition-colors ${
              active ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {BLOG_LANG_LABELS[lang]}
          </Link>
        )
      })}
    </nav>
  )
}

function decodeSegment(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}
