"use client"

import { memo, useState, type ReactNode } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import { CopyIcon, CheckIcon } from "./icons"
import { imageAsText } from "@/lib/markdown-images"

function safeHttpUrl(value: string | undefined, mailto = false) {
  const raw = value?.trim()
  if (!raw) return undefined
  try {
    const url = new URL(raw)
    if (url.protocol === "http:" || url.protocol === "https:") return raw
    if (mailto && url.protocol === "mailto:") return raw
  } catch {
    return undefined
  }
  return undefined
}

function safeImageSrc(value: string | undefined) {
  const raw = value?.trim()
  if (!raw) return undefined
  const lower = raw.slice(0, 32).toLowerCase()
  if (lower.startsWith("data:image/") && !lower.includes("svg")) return raw
  return safeHttpUrl(raw)
}

function CodeBlock({ children, className }: { children: ReactNode; className?: string }) {
  const [copied, setCopied] = useState(false)
  const lang = (className || "").replace(/language-/, "")
  const raw = extractRawText(children)

  function copy() {
    navigator.clipboard?.writeText(raw).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div className="group/code my-4 overflow-hidden rounded-lg border bg-muted/40">
      <div className="flex items-center justify-between border-b bg-muted/60 px-3 py-1">
        <span className="font-mono text-[11px] text-muted-foreground">{lang || "代码"}</span>
        <button
          onClick={copy}
          className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:bg-background hover:text-foreground"
          title="复制代码"
        >
          {copied ? <CheckIcon width={12} height={12} /> : <CopyIcon width={12} height={12} />}
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="overflow-x-auto px-3 py-2.5 font-mono text-[13px] leading-relaxed text-foreground">
        <code>{children}</code>
      </pre>
    </div>
  )
}

function extractRawText(node: ReactNode): string {
  if (typeof node === "string") return node
  if (Array.isArray(node)) return node.map(extractRawText).join("")
  if (node && typeof node === "object" && "props" in node) {
    // @ts-expect-error props children traversal
    return extractRawText(node.props?.children)
  }
  return ""
}

function isBlockCode(className: string | undefined, children: ReactNode) {
  return Boolean(className) || extractRawText(children).includes("\n")
}

// allowImages=false renders images as plain text (lib/markdown-images.ts); use it
// for Agent answers that may carry text from outside the console.
export const Markdown = memo(function Markdown({ content, allowImages = true }: { content: string; allowImages?: boolean }) {
  return (
    <div className="text-[15px] leading-7 text-foreground">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          p: ({ children }) => <p className="my-3 first:mt-0 last:mb-0 break-words">{children}</p>,
          h1: ({ children }) => (
            <h1 className="mb-4 mt-0 border-b pb-2 text-2xl font-semibold tracking-tight text-foreground">{children}</h1>
          ),
          h2: ({ children }) => (
            <h2 className="mb-3 mt-8 border-b pb-1 text-xl font-semibold tracking-tight text-foreground first:mt-0">{children}</h2>
          ),
          h3: ({ children }) => (
            <h3 className="mb-2 mt-6 text-lg font-semibold text-foreground first:mt-0">{children}</h3>
          ),
          h4: ({ children }) => <h4 className="mb-2 mt-5 text-base font-semibold text-foreground">{children}</h4>,
          h5: ({ children }) => <h5 className="mb-1.5 mt-4 text-sm font-semibold text-foreground">{children}</h5>,
          h6: ({ children }) => <h6 className="mb-1.5 mt-4 text-sm font-medium text-muted-foreground">{children}</h6>,
          ul: ({ children }) => <ul className="my-3 ml-6 list-disc space-y-1">{children}</ul>,
          ol: ({ children }) => <ol className="my-3 ml-6 list-decimal space-y-1">{children}</ol>,
          li: ({ children }) => <li className="break-words pl-0.5 marker:text-muted-foreground">{children}</li>,
          a: ({ children, href }) => {
            const safe = safeHttpUrl(href, true)
            if (!safe) return <span className="text-foreground">{children}</span>
            return (
              <a
                href={safe}
                target="_blank"
                rel="noreferrer"
                className="font-medium text-primary underline underline-offset-2 hover:opacity-80"
              >
                {children}
              </a>
            )
          },
          img: ({ src, alt }) => {
            if (!allowImages) return imageAsText({ src, alt })
            const safe = safeImageSrc(typeof src === "string" ? src : undefined)
            if (!safe) return <span className="text-muted-foreground">[{alt || "图片不可显示"}]</span>
            return (
              // Remote knowledge-base images are not known at build time.
              // eslint-disable-next-line @next/next/no-img-element
              <img src={safe} alt={alt || ""} className="my-4 max-w-full rounded-lg border" />
            )
          },
          strong: ({ children }) => <strong className="font-semibold text-foreground">{children}</strong>,
          em: ({ children }) => <em className="italic">{children}</em>,
          del: ({ children }) => <del className="text-muted-foreground line-through">{children}</del>,
          blockquote: ({ children }) => (
            <blockquote className="my-4 border-l-2 border-primary/40 pl-4 text-muted-foreground">{children}</blockquote>
          ),
          hr: () => <hr className="my-6 border-border" />,
          input: ({ type, checked }) =>
            type === "checkbox" ? (
              <input type="checkbox" checked={Boolean(checked)} readOnly className="mr-2 align-middle" />
            ) : null,
          code: ({ className, children }) => {
            if (!isBlockCode(className, children)) {
              return (
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em] text-foreground">{children}</code>
              )
            }
            return <CodeBlock className={className}>{children}</CodeBlock>
          },
          pre: ({ children }) => <>{children}</>,
          table: ({ children }) => (
            <div className="my-4 overflow-x-auto">
              <table className="w-full border-collapse text-sm">{children}</table>
            </div>
          ),
          thead: ({ children }) => <thead className="bg-muted/50">{children}</thead>,
          th: ({ children }) => (
            <th className="border px-3 py-1.5 text-left font-semibold text-foreground">{children}</th>
          ),
          td: ({ children }) => <td className="border px-3 py-1.5 align-top text-foreground/90">{children}</td>,
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
})
