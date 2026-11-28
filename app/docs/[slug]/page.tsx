import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"
import { ArrowLeft, ArrowRight, Clock, ExternalLink, FileText } from "lucide-react"

import { DocsMarkdown } from "@/components/docs-markdown"
import { DocsToc } from "@/components/docs-shell"
import {
  DOC_CATEGORIES,
  DOC_STATUS_META,
  GITHUB_REPO_URL,
  docHref,
  findDocBySlug,
  markdownDocSlugs,
} from "@/lib/docs-catalog"
import { loadDocBySlug } from "@/lib/docs"

type DocPageProps = { params: Promise<{ slug: string }> }

export const dynamicParams = false

export function generateStaticParams() {
  return markdownDocSlugs().map((slug) => ({ slug }))
}

export async function generateMetadata({ params }: DocPageProps): Promise<Metadata> {
  const { slug } = await params
  const entry = findDocBySlug(slug)
  if (!entry) return {}
  return {
    title: entry.title,
    description: entry.description,
  }
}

export default async function DocDetailPage({ params }: DocPageProps) {
  const { slug } = await params
  const loaded = await loadDocBySlug(slug)
  if (!loaded) notFound()

  const { entry, headingTitle, body, toc, readingMinutes, prev, next } = loaded
  const statusMeta = DOC_STATUS_META[entry.status]
  const category = DOC_CATEGORIES.find((c) => c.id === entry.category)
  const sourceGithubUrl = entry.sourcePath
    ? `${GITHUB_REPO_URL}/blob/main/backend/docs/${entry.sourcePath}`
    : null

  return (
    <div className="flex items-start gap-8 pb-14">
      <article className="min-w-0 flex-1">
        {/* Breadcrumb */}
        <nav aria-label="面包屑导航" className="mb-5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          <Link href="/docs" className="transition-colors hover:text-foreground">
            文档中心
          </Link>
          <span aria-hidden="true">/</span>
          {category ? (
            <>
              <span>{category.title}</span>
              <span aria-hidden="true">/</span>
            </>
          ) : null}
          <span className="text-foreground">{entry.shortTitle}</span>
        </nav>

        {/* Article Header */}
        <header className="mb-8 border-b border-border/55 pb-6">
          <div className="mb-3 flex flex-wrap items-center gap-2">
            <span className={`inline-flex rounded-full border px-2.5 py-0.5 font-mono text-2xs ${statusMeta.className}`}>
              {statusMeta.label}
            </span>
            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
              <Clock className="size-3.5" aria-hidden="true" />
              约 {readingMinutes} 分钟阅读
            </span>
            {entry.sourcePath && sourceGithubUrl ? (
              <a
                href={sourceGithubUrl}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground transition-colors hover:text-accent"
              >
                <FileText className="size-3" aria-hidden="true" />
                backend/docs/{entry.sourcePath}
                <ExternalLink className="size-3 opacity-70" aria-hidden="true" />
              </a>
            ) : null}
          </div>

          <h1 className="text-balance text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
            {headingTitle}
          </h1>
          <p className="mt-2.5 text-pretty text-sm leading-relaxed text-muted-foreground">
            {entry.description}
          </p>
        </header>

        {/* Rendered Markdown */}
        <div className="min-w-0">
          <DocsMarkdown content={body} />
        </div>

        {/* Prev / Next Navigation */}
        <nav
          aria-label="上一篇与下一篇文档"
          className="mt-12 grid gap-3 border-t border-border/55 pt-8 sm:grid-cols-2"
        >
          {prev ? (
            <Link
              href={docHref(prev.slug)}
              className="card-surface card-interactive group flex flex-col gap-1 p-4"
            >
              <span className="inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
                <ArrowLeft className="size-3 transition-transform group-hover:-translate-x-0.5" aria-hidden="true" />
                上一篇
              </span>
              <span className="text-sm font-semibold text-foreground group-hover:text-accent">
                {prev.shortTitle}
              </span>
            </Link>
          ) : (
            <div />
          )}
          {next ? (
            <Link
              href={docHref(next.slug)}
              className="card-surface card-interactive group flex flex-col items-end gap-1 p-4 text-right"
            >
              <span className="inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
                下一篇
                <ArrowRight className="size-3 transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
              </span>
              <span className="text-sm font-semibold text-foreground group-hover:text-accent">
                {next.shortTitle}
              </span>
            </Link>
          ) : null}
        </nav>
      </article>

      <DocsToc items={toc} />
    </div>
  )
}
