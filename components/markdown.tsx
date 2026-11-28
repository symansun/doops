"use client"

import { memo, type ComponentPropsWithoutRef } from "react"
import {
  Streamdown,
  defaultComponents,
  defaultRemarkPlugins,
  type AllowedTags,
  type Components,
  type PluginConfig,
  type StreamdownTranslations,
} from "streamdown"
import { code } from "@streamdown/code"
import { mermaid } from "@streamdown/mermaid"
import { math } from "@streamdown/math"
import { cjk } from "@streamdown/cjk"
import { CircleAlert, Info, Lightbulb, ShieldAlert, TriangleAlert } from "lucide-react"
import { cn } from "@/lib/cn"
import { imageAsText } from "@/lib/markdown-images"
import { remarkDoopsEnhancements, type CalloutKind } from "@/lib/markdown-render"

export type MarkdownVariant = "default" | "document" | "compact"

export const STREAMDOWN_PLUGINS: PluginConfig = {
  code,
  mermaid,
  math,
  cjk,
}

export const STREAMDOWN_SHIKI_THEME: [string, string] = ["github-dark-default", "github-dark-default"]

export const STREAMDOWN_MERMAID_OPTIONS = {
  config: {
    theme: "dark",
    securityLevel: "strict",
    suppressErrorRendering: true,
    flowchart: { useMaxWidth: true },
    sequence: { useMaxWidth: true },
  },
}

export const STREAMDOWN_ALLOWED_TAGS: AllowedTags = {
  blockquote: ["data-callout", "data-callout-title", "dataCallout", "dataCalloutTitle"],
  h1: ["id"],
  h2: ["id"],
  h3: ["id"],
  h4: ["id"],
  h5: ["id"],
  h6: ["id"],
}

export const STREAMDOWN_REMARK_PLUGINS = [...Object.values(defaultRemarkPlugins), remarkDoopsEnhancements]

export const ZH_STREAMDOWN_TRANSLATIONS: Partial<StreamdownTranslations> = {
  close: "关闭",
  copied: "已复制",
  copyCode: "复制代码",
  copyLink: "复制链接",
  copyTable: "复制表格",
  copyTableAsCsv: "复制为 CSV",
  copyTableAsMarkdown: "复制为 Markdown",
  copyTableAsTsv: "复制为 TSV",
  downloadDiagram: "导出图表",
  downloadDiagramAsMmd: "下载 Mermaid 源码 (.mmd)",
  downloadDiagramAsPng: "下载 PNG 图片",
  downloadDiagramAsSvg: "下载 SVG 矢量图",
  downloadFile: "下载文件",
  downloadImage: "下载图片",
  downloadTable: "导出表格",
  downloadTableAsCsv: "下载 CSV",
  downloadTableAsMarkdown: "下载 Markdown",
  errorMermaid: "Mermaid 图表渲染失败，请检查语法",
  exitFullscreen: "退出全屏",
  externalLinkWarning: "即将打开外部链接",
  imageNotAvailable: "图片无法加载",
  mermaidAriaLabel: "Mermaid 架构与流程图",
  mermaidFormatMmd: "MMD",
  mermaidFormatPng: "PNG",
  mermaidFormatSvg: "SVG",
  openExternalLink: "打开外部链接",
  openLink: "打开链接",
  resetView: "重置视图",
  tableFormatCsv: "CSV",
  tableFormatMarkdown: "Markdown",
  tableFormatTsv: "TSV",
  viewFullscreen: "全屏查看",
  zoomIn: "放大",
  zoomOut: "缩小",
}

const CALLOUT_META: Record<
  CalloutKind,
  {
    defaultTitle: string
    icon: typeof Info
    containerClass: string
    headerClass: string
  }
> = {
  note: {
    defaultTitle: "提示",
    icon: Info,
    containerClass: "border-primary/40 bg-primary/[0.07]",
    headerClass: "text-primary",
  },
  tip: {
    defaultTitle: "建议",
    icon: Lightbulb,
    containerClass: "border-success/40 bg-success/[0.07]",
    headerClass: "text-success",
  },
  important: {
    defaultTitle: "重要",
    icon: CircleAlert,
    containerClass: "border-accent/45 bg-accent/[0.08]",
    headerClass: "text-accent",
  },
  warning: {
    defaultTitle: "注意",
    icon: TriangleAlert,
    containerClass: "border-warning/45 bg-warning/[0.08]",
    headerClass: "text-warning",
  },
  caution: {
    defaultTitle: "高危注意",
    icon: ShieldAlert,
    containerClass: "border-destructive/45 bg-destructive/[0.08]",
    headerClass: "text-destructive",
  },
}

export function MarkdownBlockquote({
  children,
  node: _node,
  spacing = "default",
  ...props
}: ComponentPropsWithoutRef<"blockquote"> & {
  node?: unknown
  "data-callout"?: string
  "data-callout-title"?: string
  spacing?: "default" | "article"
}) {
  const rawKind = props["data-callout"]
  const kind = rawKind && rawKind in CALLOUT_META ? (rawKind as CalloutKind) : null

  if (kind) {
    const meta = CALLOUT_META[kind]
    const Icon = meta.icon
    const title = props["data-callout-title"] || meta.defaultTitle
    return (
      <aside
        role="note"
        className={cn(
          "rounded-lg border border-l-[3px] px-3.5 py-2.5 first:mt-0 last:mb-0",
          spacing === "article" ? "my-6 px-4 py-3.5" : "my-3",
          meta.containerClass,
        )}
      >
        <div className={cn("mb-1 flex items-center gap-1.5 text-xs font-semibold tracking-wide", meta.headerClass)}>
          <Icon aria-hidden="true" className="size-3.5 shrink-0" />
          <span>{title}</span>
        </div>
        <div className="text-foreground/90 [&>p:first-child]:mt-0 [&>p:last-child]:mb-0">{children}</div>
      </aside>
    )
  }

  const DefaultBlockquote = defaultComponents.blockquote
  return <DefaultBlockquote {...props}>{children}</DefaultBlockquote>
}

const COMPONENTS_WITH_IMAGES: Components = {
  blockquote: MarkdownBlockquote,
}

const COMPONENTS_TEXT_IMAGES: Components = {
  blockquote: MarkdownBlockquote,
  img: ({ src, alt }) => imageAsText({ src, alt }),
}

const CONTROLS_WITH_IMAGES = {
  code: { copy: true, download: true },
  table: { copy: true, download: true, fullscreen: true },
  mermaid: { copy: true, download: true, fullscreen: true, panZoom: true },
  image: { download: false },
} as const

const CONTROLS_NO_IMAGES = {
  code: { copy: true, download: false },
  table: { copy: true, download: true, fullscreen: true },
  mermaid: { copy: true, download: true, fullscreen: true, panZoom: true },
  image: false,
} as const

// allowImages=false renders images as plain text (lib/markdown-images.ts); use it
// for Agent answers that may carry text from outside the console.
export const Markdown = memo(function Markdown({
  content,
  allowImages = true,
  streaming = false,
  variant = "default",
  className,
}: {
  content: string
  allowImages?: boolean
  streaming?: boolean
  variant?: MarkdownVariant
  className?: string
}) {
  return (
    <Streamdown
      mode={streaming ? "streaming" : "static"}
      isAnimating={streaming}
      plugins={STREAMDOWN_PLUGINS}
      shikiTheme={STREAMDOWN_SHIKI_THEME}
      mermaid={STREAMDOWN_MERMAID_OPTIONS}
      remarkPlugins={STREAMDOWN_REMARK_PLUGINS}
      allowedTags={STREAMDOWN_ALLOWED_TAGS}
      translations={ZH_STREAMDOWN_TRANSLATIONS}
      linkSafety={{ enabled: false }}
      controls={allowImages ? CONTROLS_WITH_IMAGES : CONTROLS_NO_IMAGES}
      codeBlockMaxHeight={variant === "document" ? 560 : 420}
      tableMaxHeight={variant === "document" ? 480 : 340}
      components={allowImages ? COMPONENTS_WITH_IMAGES : COMPONENTS_TEXT_IMAGES}
      className={cn(
        "min-w-0 break-words text-foreground [overflow-wrap:anywhere] [&_[data-streamdown^=heading-]]:scroll-mt-16 [&_.contains-task-list]:list-none [&_.contains-task-list]:pl-0 [&_li.task-list-item]:list-none [&_[data-streamdown=mermaid-block]_svg]:h-auto [&_[data-streamdown=mermaid-block]_svg]:max-w-full",
        variant === "document"
          ? "text-sm leading-7 [&_h1]:border-b [&_h1]:border-border [&_h1]:pb-2 [&_h1]:text-2xl [&_h2]:border-b [&_h2]:border-border/60 [&_h2]:pb-1.5 [&_h2]:text-lg [&_h3]:text-base"
          : variant === "compact"
            ? "text-xs leading-5 [&_h1]:text-sm [&_h2]:text-xs [&_h3]:text-xs"
            : "text-sm leading-6 [&_h1]:text-lg [&_h2]:text-base [&_h3]:text-sm",
        className,
      )}
    >
      {content}
    </Streamdown>
  )
})
