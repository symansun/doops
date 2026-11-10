import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"

import { BlogCover } from "@/components/blog-markdown"
import { BLOG_LANGS, BLOG_LANG_LABELS, BLOG_UI, formatBlogDate, isBlogLang, listPosts } from "@/lib/blog"

type LangPageProps = { params: Promise<{ lang: string }> }

// Only the three configured languages exist. Anything else is a 404 instead of
// an empty shell, which is what a typo in the URL should be.
export const dynamicParams = false

export function generateStaticParams() {
  return BLOG_LANGS.map((lang) => ({ lang }))
}

export async function generateMetadata({ params }: LangPageProps): Promise<Metadata> {
  const { lang } = await params
  if (!isBlogLang(lang)) return {}
  const label = BLOG_LANG_LABELS[lang]
  return {
    title: `DoOps Blog · ${label}`,
    description: `DoOps 系列长文（${label}）。`,
    alternates: { languages: Object.fromEntries(BLOG_LANGS.map((code) => [code, `/blog/${code}`])) },
  }
}

export default async function BlogLangIndexPage({ params }: LangPageProps) {
  const { lang } = await params
  if (!isBlogLang(lang)) notFound()

  const posts = await listPosts(lang)
  const ui = BLOG_UI[lang]

  return (
    <div>
      <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-border/60 pb-5">
        <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">{ui.allPosts}</h1>
        <span className="text-xs text-muted-foreground">
          {BLOG_LANG_LABELS[lang]} · {posts.length}
        </span>
      </div>

      {posts.length === 0 ? (
        <p className="py-16 text-center text-sm text-muted-foreground">{ui.empty}</p>
      ) : (
        <div>
          {posts.map((post) => (
            <article key={post.slug} className="border-b border-border/60 py-8 last:border-0 last:pb-0">
              <Link href={`/blog/${lang}/${post.slug}`} className="group flex flex-col gap-5 sm:flex-row sm:items-start">
                <div className="min-w-0 flex-1">
                  {post.series ? (
                    <span className="mb-2 inline-block rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
                      {post.series}
                    </span>
                  ) : null}
                  <h2 className="text-lg font-semibold tracking-tight text-foreground transition-colors group-hover:text-primary sm:text-xl">
                    {post.title}
                  </h2>
                  {post.subtitle ? (
                    <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{post.subtitle}</p>
                  ) : null}
                  <div className="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                    {post.date ? (
                      <time dateTime={post.date}>{formatBlogDate(post.date, lang)}</time>
                    ) : null}
                    <span aria-hidden="true">·</span>
                    <span>
                      {post.readingMinutes} {ui.minutes}
                    </span>
                  </div>
                  {post.tags.length > 0 ? (
                    <div className="mt-3 flex flex-wrap gap-1.5">
                      {post.tags.map((tag) => (
                        <span
                          key={tag}
                          className="rounded-md border border-border/60 px-1.5 py-0.5 text-[11px] text-muted-foreground"
                        >
                          {tag}
                        </span>
                      ))}
                    </div>
                  ) : null}
                </div>
                {post.cover ? (
                  <div className="w-full shrink-0 sm:w-44">
                    <BlogCover src={post.cover} alt={post.title} className="aspect-[16/10] object-cover" />
                  </div>
                ) : null}
              </Link>
            </article>
          ))}
        </div>
      )}
    </div>
  )
}
