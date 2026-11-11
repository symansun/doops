import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"

import { BlogCover, BlogMarkdown } from "@/components/blog-markdown"
import {
  BLOG_LANGS,
  BLOG_LANG_LABELS,
  BLOG_UI,
  formatBlogDate,
  getPost,
  isBlogLang,
  listPosts,
} from "@/lib/blog"

type SlugPageProps = { params: Promise<{ lang: string; slug: string }> }

export const dynamicParams = false

export async function generateStaticParams() {
  const groups = await Promise.all(
    BLOG_LANGS.map(async (lang) => {
      const posts = await listPosts(lang)
      return posts.map((post) => ({ lang, slug: post.slug }))
    }),
  )
  return groups.flat()
}

export async function generateMetadata({ params }: SlugPageProps): Promise<Metadata> {
  const { lang, slug } = await params
  if (!isBlogLang(lang)) return {}
  const post = await getPost(lang, slug)
  if (!post) return {}
  return {
    title: `${post.title} · DoOps Blog`,
    description: post.subtitle || post.title,
    openGraph: post.cover ? { images: [{ url: post.cover }] } : undefined,
  }
}

export default async function BlogPostPage({ params }: SlugPageProps) {
  const { lang, slug } = await params
  if (!isBlogLang(lang)) notFound()

  const post = await getPost(lang, slug)
  if (!post) notFound()

  const ui = BLOG_UI[lang]

  return (
    <article>
      <Link
        href={`/blog/${lang}`}
        className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
      >
        ← {ui.backToAll}
      </Link>

      <header className="mt-6 border-b border-border/40 pb-8">
        {post.series ? (
          <span className="inline-flex items-center rounded-full bg-primary/10 px-2.5 py-1 text-[11px] font-medium text-primary ring-1 ring-inset ring-primary/20">
            {post.series}
          </span>
        ) : null}
        <h1 className="mt-4 text-balance text-3xl leading-[1.15] font-semibold tracking-tight sm:text-[2.6rem]">
          {post.title}
        </h1>
        {post.subtitle ? (
          <p className="mt-3 text-base leading-relaxed text-muted-foreground">{post.subtitle}</p>
        ) : null}
        <div className="mt-4 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
          {post.date ? <time dateTime={post.date}>{formatBlogDate(post.date, lang)}</time> : null}
          <span aria-hidden="true">·</span>
          <span>{BLOG_LANG_LABELS[lang]}</span>
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
        {post.cover ? (
          <div className="mt-8 overflow-hidden rounded-2xl ring-1 ring-border/60">
            <BlogCover src={post.cover} alt={post.title} />
          </div>
        ) : null}
      </header>

      <div className="pt-8">
        <BlogMarkdown content={post.body} />
      </div>
    </article>
  )
}
