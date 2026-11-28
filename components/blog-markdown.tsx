"use client"

import { useState } from "react"
import { Streamdown, type Components } from "streamdown"

import { resolveBlogImageSrc } from "@/lib/blog-format"
import { remarkDropHtml } from "@/lib/markdown-render"
import {
  MarkdownBlockquote,
  STREAMDOWN_ALLOWED_TAGS,
  STREAMDOWN_MERMAID_OPTIONS,
  STREAMDOWN_PLUGINS,
  STREAMDOWN_REMARK_PLUGINS,
  STREAMDOWN_SHIKI_THEME,
  ZH_STREAMDOWN_TRANSLATIONS,
} from "./markdown"

// Rendering for blog articles: article-scale typography powered by Streamdown
// (Shiki syntax highlighting, Mermaid diagrams, KaTeX math, CJK typography, and
// GFM tables), with article images served by /blog/images/….
//
// Blog content is repository content, so images are fetched normally; the
// console's untrusted-agent rule (lib/markdown-images.ts) does not apply here.

type HastNode = { tagName?: string; value?: string; children?: HastNode[] }

/** An italic-only paragraph is a figure caption in these articles. */
function isCaptionParagraph(node: HastNode | undefined): boolean {
  const children = (node?.children ?? []).filter((child) => child.tagName || (child.value ?? "").trim() !== "")
  return children.length === 1 && children[0].tagName === "em"
}

/** Image with a tidy placeholder when the file is not in the build yet. */
function BlogImage({ src, alt }: { src?: string; alt?: string }) {
  const [failed, setFailed] = useState(false)
  const url = resolveBlogImageSrc(src)
  const label = (alt ?? "").trim()

  if (!url) return null

  if (failed) {
    return (
      <span className="my-6 flex flex-col items-center gap-1 rounded-xl border border-dashed border-border bg-muted/30 px-4 py-10 text-center">
        <span className="text-sm text-muted-foreground">{label || "配图"} · 图片未找到</span>
        <span className="font-mono text-2xs break-all text-muted-foreground/70">{url}</span>
      </span>
    )
  }

  return (
    <img
      src={url}
      alt={label}
      loading="lazy"
      decoding="async"
      onError={() => setFailed(true)}
      className="mx-auto my-6 w-full rounded-xl border border-border/60 bg-muted/20"
    />
  )
}

/** Cover image for the list and the article header, with the same fallback. */
export function BlogCover({ src, alt, className = "" }: { src?: string; alt?: string; className?: string }) {
  const [failed, setFailed] = useState(false)
  const url = resolveBlogImageSrc(src)
  if (!url || failed) return null
  return (
    <img
      src={url}
      alt={alt ?? ""}
      onError={() => setFailed(true)}
      className={`w-full rounded-2xl border border-border/60 bg-muted/20 ${className}`}
    />
  )
}

const BLOG_COMPONENTS: Components = {
  h1: ({ children, id }) => (
    <h2
      id={id}
      className="mt-12 mb-4 scroll-mt-20 border-b border-border/50 pb-2.5 text-xl font-semibold tracking-tight text-foreground first:mt-0 sm:text-2xl"
    >
      {children}
    </h2>
  ),
  h2: ({ children, id }) => (
    <h2
      id={id}
      className="mt-12 mb-4 scroll-mt-20 border-b border-border/50 pb-2.5 text-xl font-semibold tracking-tight text-foreground first:mt-0 sm:text-2xl"
    >
      {children}
    </h2>
  ),
  h3: ({ children, id }) => (
    <h3 id={id} className="mt-9 mb-3 scroll-mt-20 text-lg font-semibold text-foreground">
      {children}
    </h3>
  ),
  h4: ({ children, id }) => (
    <h4 id={id} className="mt-7 mb-2 scroll-mt-20 text-base font-semibold text-foreground">
      {children}
    </h4>
  ),
  p: ({ children, node }) =>
    isCaptionParagraph(node as HastNode | undefined) ? (
      <p className="mt-2 mb-7 text-center text-xs leading-relaxed text-muted-foreground">{children}</p>
    ) : (
      <p className="my-4 break-words">{children}</p>
    ),
  blockquote: (props) => <MarkdownBlockquote {...props} spacing="article" />,
  img: ({ src, alt }) => <BlogImage src={typeof src === "string" ? src : undefined} alt={alt ?? ""} />,
}

const BLOG_REMARK_PLUGINS = [...STREAMDOWN_REMARK_PLUGINS, remarkDropHtml]

export function BlogMarkdown({ content }: { content: string }) {
  return (
    <Streamdown
      mode="static"
      // Articles open with a hand-written HTML paragraph of language links.
      // Rendering it is both redundant (the layout owns a real switcher) and
      // wrong here, so remarkDropHtml strips raw HTML nodes before rendering.
      plugins={STREAMDOWN_PLUGINS}
      shikiTheme={STREAMDOWN_SHIKI_THEME}
      mermaid={STREAMDOWN_MERMAID_OPTIONS}
      remarkPlugins={BLOG_REMARK_PLUGINS}
      allowedTags={STREAMDOWN_ALLOWED_TAGS}
      translations={ZH_STREAMDOWN_TRANSLATIONS}
      linkSafety={{ enabled: false }}
      codeBlockMaxHeight={0}
      tableMaxHeight={0}
      components={BLOG_COMPONENTS}
      className="text-sm leading-[1.9] text-foreground/90 sm:text-base [&_.contains-task-list]:list-none [&_.contains-task-list]:pl-0 [&_li.task-list-item]:list-none [&_[data-streamdown=mermaid-block]_svg]:h-auto [&_[data-streamdown=mermaid-block]_svg]:max-w-full"
    >
      {content}
    </Streamdown>
  )
}
