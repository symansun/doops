"use client"

import Link from "next/link"
import { Streamdown, type Components } from "streamdown"
import {
  MarkdownBlockquote,
  STREAMDOWN_ALLOWED_TAGS,
  STREAMDOWN_MERMAID_OPTIONS,
  STREAMDOWN_PLUGINS,
  STREAMDOWN_REMARK_PLUGINS,
  STREAMDOWN_SHIKI_THEME,
  ZH_STREAMDOWN_TRANSLATIONS,
} from "./markdown"

const DOCS_CONTROLS = {
  code: { copy: true, download: false },
  table: { copy: true, download: true, fullscreen: true },
  mermaid: { copy: true, download: true, fullscreen: true, panZoom: true },
  image: false,
} as const

const DOCS_COMPONENTS: Components = {
  h1: ({ children, id }) => (
    <h2
      id={id}
      className="group mt-10 mb-4 scroll-mt-24 border-b border-border/55 pb-2.5 text-xl font-semibold tracking-tight text-foreground first:mt-0 sm:text-2xl"
    >
      {children}
      {id ? (
        <a
          href={`#${id}`}
          aria-label="定位到此小节"
          className="ml-2 font-mono text-sm font-normal text-primary opacity-0 transition-opacity group-hover:opacity-80"
        >
          #
        </a>
      ) : null}
    </h2>
  ),
  h2: ({ children, id }) => (
    <h2
      id={id}
      className="group mt-10 mb-4 scroll-mt-24 border-b border-border/55 pb-2.5 text-xl font-semibold tracking-tight text-foreground first:mt-0 sm:text-2xl"
    >
      {children}
      {id ? (
        <a
          href={`#${id}`}
          aria-label="定位到此小节"
          className="ml-2 font-mono text-sm font-normal text-primary opacity-0 transition-opacity group-hover:opacity-80"
        >
          #
        </a>
      ) : null}
    </h2>
  ),
  h3: ({ children, id }) => (
    <h3
      id={id}
      className="group mt-8 mb-3 scroll-mt-24 text-lg font-semibold tracking-tight text-foreground"
    >
      {children}
      {id ? (
        <a
          href={`#${id}`}
          aria-label="定位到此小节"
          className="ml-2 font-mono text-xs font-normal text-primary opacity-0 transition-opacity group-hover:opacity-80"
        >
          #
        </a>
      ) : null}
    </h3>
  ),
  h4: ({ children, id }) => (
    <h4 id={id} className="mt-6 mb-2 scroll-mt-24 text-base font-semibold text-foreground">
      {children}
    </h4>
  ),
  table: ({ children }) => (
    <div className="docs-scroll my-5 w-full max-w-full overflow-x-auto rounded-xl border border-border/70 bg-card/45">
      <table className="w-full table-fixed border-collapse text-left text-xs">{children}</table>
    </div>
  ),
  thead: ({ children }) => (
    <thead className="border-b border-border/75 bg-card/85 text-muted-foreground">{children}</thead>
  ),
  tr: ({ children }) => <tr className="border-b border-border/45 last:border-0">{children}</tr>,
  th: ({ children }) => (
    <th className="px-3.5 py-2.5 font-semibold text-foreground/95">{children}</th>
  ),
  td: ({ children }) => (
    <td className="px-3.5 py-2.5 text-foreground/85">{children}</td>
  ),
  a: ({ href, children, ...rest }) => {
    const url = typeof href === "string" ? href : ""
    if (url.startsWith("/docs")) {
      return (
        <Link
          href={url}
          className="font-medium text-accent underline decoration-accent/40 underline-offset-4 transition-colors hover:text-foreground hover:decoration-foreground"
        >
          {children}
        </Link>
      )
    }
    const external = /^https?:\/\//i.test(url)
    return (
      <a
        href={url}
        target={external ? "_blank" : undefined}
        rel={external ? "noreferrer" : undefined}
        className="font-medium text-primary underline decoration-primary/40 underline-offset-4 transition-colors hover:text-accent hover:decoration-accent"
        {...rest}
      >
        {children}
      </a>
    )
  },
  blockquote: (props) => <MarkdownBlockquote {...props} spacing="article" />,
}

export function DocsMarkdown({ content }: { content: string }) {
  return (
    <Streamdown
      mode="static"
      plugins={STREAMDOWN_PLUGINS}
      shikiTheme={STREAMDOWN_SHIKI_THEME}
      mermaid={STREAMDOWN_MERMAID_OPTIONS}
      remarkPlugins={STREAMDOWN_REMARK_PLUGINS}
      allowedTags={STREAMDOWN_ALLOWED_TAGS}
      translations={ZH_STREAMDOWN_TRANSLATIONS}
      linkSafety={{ enabled: false }}
      controls={DOCS_CONTROLS}
      codeBlockMaxHeight={0}
      tableMaxHeight={0}
      components={DOCS_COMPONENTS}
      className="docs-prose min-w-0 break-words text-sm leading-[1.82] text-foreground/90 [overflow-wrap:anywhere] [&_.contains-task-list]:list-none [&_.contains-task-list]:pl-0 [&_li.task-list-item]:list-none [&_[data-streamdown=mermaid-block]_svg]:h-auto [&_[data-streamdown=mermaid-block]_svg]:max-w-full"
    >
      {content}
    </Streamdown>
  )
}
