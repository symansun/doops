"use client"

import { useState } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"

import { CheckIcon, CopyIcon } from "./icons"
import { resolveBlogImageSrc } from "@/lib/blog-format"

// Rendering for blog articles: article-scale typography (the console Markdown
// component is density-first, this one is reading-first), GFM tables, fenced
// code with a copy button, and article images served by /blog/images/….
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
        <span className="font-mono text-[11px] break-all text-muted-foreground/70">{url}</span>
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

function ArticleCodeBlock({ code, lang }: { code: string; lang: string }) {
  const [copied, setCopied] = useState(false)

  function copy() {
    navigator.clipboard?.writeText(code).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div className="my-6 overflow-hidden rounded-xl border border-border bg-muted/30">
      <div className="flex items-center justify-between border-b border-border bg-muted/50 px-3.5 py-1.5">
        <span className="font-mono text-[11px] tracking-wide text-muted-foreground">{lang || "code"}</span>
        <button
          type="button"
          onClick={copy}
          title="复制代码"
          className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:bg-background hover:text-foreground"
        >
          {copied ? <CheckIcon width={12} height={12} /> : <CopyIcon width={12} height={12} />}
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="overflow-x-auto px-4 py-3.5 font-mono text-[13px] leading-relaxed text-foreground">
        <code>{code}</code>
      </pre>
    </div>
  )
}

export function BlogMarkdown({ content }: { content: string }) {
  return (
    <div className="text-[15px] leading-[1.9] text-foreground/90 sm:text-base">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        // Articles open with a hand-written HTML paragraph of language links.
        // Rendering it is both redundant (the layout owns a real switcher) and
        // wrong here (without rehype-raw the tags show up as escaped text), so
        // raw HTML is dropped instead.
        skipHtml
        components={{
          h1: ({ children }) => (
            <h2 className="mt-12 mb-4 text-xl font-semibold tracking-tight text-foreground first:mt-0 sm:text-2xl">
              {children}
            </h2>
          ),
          h2: ({ children }) => (
            <h2 className="mt-12 mb-4 text-xl font-semibold tracking-tight text-foreground first:mt-0 sm:text-2xl">
              {children}
            </h2>
          ),
          h3: ({ children }) => (
            <h3 className="mt-9 mb-3 text-lg font-semibold text-foreground">{children}</h3>
          ),
          h4: ({ children }) => (
            <h4 className="mt-7 mb-2 text-base font-semibold text-foreground">{children}</h4>
          ),
          p: ({ children, node }) =>
            isCaptionParagraph(node as HastNode | undefined) ? (
              <p className="mt-2 mb-7 text-center text-[13px] leading-relaxed text-muted-foreground">{children}</p>
            ) : (
              <p className="my-4 break-words">{children}</p>
            ),
          ul: ({ children }) => <ul className="my-4 ml-5 list-disc space-y-1.5">{children}</ul>,
          ol: ({ children }) => <ol className="my-4 ml-5 list-decimal space-y-1.5">{children}</ol>,
          li: ({ children }) => <li className="break-words pl-1 marker:text-muted-foreground/70">{children}</li>,
          a: ({ children, href }) => (
            <a
              href={href}
              className="font-medium text-primary underline decoration-primary/30 underline-offset-4 transition-colors hover:decoration-primary"
            >
              {children}
            </a>
          ),
          strong: ({ children }) => <strong className="font-semibold text-foreground">{children}</strong>,
          em: ({ children }) => <em className="italic">{children}</em>,
          blockquote: ({ children }) => (
            <blockquote className="my-6 rounded-r-lg border-l-2 border-primary/40 bg-muted/30 py-1 pl-4 text-muted-foreground">
              {children}
            </blockquote>
          ),
          hr: () => <hr className="my-10 border-border" />,
          img: ({ src, alt }) => <BlogImage src={typeof src === "string" ? src : undefined} alt={alt ?? ""} />,
          table: ({ children }) => (
            <div className="my-6 overflow-x-auto rounded-xl border border-border">
              <table className="w-full border-collapse text-sm">{children}</table>
            </div>
          ),
          thead: ({ children }) => <thead className="bg-muted/50">{children}</thead>,
          tbody: ({ children }) => <tbody>{children}</tbody>,
          tr: ({ children }) => <tr className="border-b border-border/60 last:border-0 even:bg-muted/20">{children}</tr>,
          th: ({ children }) => (
            <th className="border-b border-border px-3.5 py-2.5 text-left font-semibold whitespace-nowrap text-foreground">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="px-3.5 py-2.5 align-top text-foreground/85">{children}</td>
          ),
          pre: ({ children }) => <>{children}</>,
          code: ({ className, children }) => {
            const lang = /language-([\w+-]+)/.exec(className ?? "")?.[1] ?? ""
            const raw = String(children ?? "")
            const isBlock = Boolean(lang) || raw.includes("\n")
            if (!isBlock) {
              return (
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em] text-foreground">
                  {children}
                </code>
              )
            }
            return <ArticleCodeBlock code={raw.replace(/\n$/, "")} lang={lang} />
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
}
