export type CalloutKind = "note" | "tip" | "important" | "warning" | "caution"

export interface MarkdownTocItem {
  id: string
  text: string
  level: number
}

/** Slugify heading text deterministically for in-page `#anchor` navigation and TOC sync. */
export function slugifyHeading(raw: string, seen: Map<string, number>): string {
  const base =
    raw
      .trim()
      .toLowerCase()
      .replace(/[^\p{L}\p{N}\s_-]/gu, "")
      .replace(/\s+/g, "-")
      .replace(/-+/g, "-")
      .replace(/^-|-$/g, "") || "section"
  const count = seen.get(base) ?? 0
  seen.set(base, count + 1)
  return count === 0 ? base : `${base}-${count}`
}

function stripInlineMarkdown(text: string): string {
  const codeSpans: string[] = []
  const masked = text.replace(/`([^`]+)`/g, (_, inner: string) => {
    const token = `\u0000${codeSpans.length}\u0000`
    codeSpans.push(inner)
    return token
  })
  return masked
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/(\*\*|__|\*|~~)/g, "")
    .replace(/(^|\s)_([^_]+)_(\s|$)/g, "$1$2$3")
    .replace(/<[^>]+>/g, "")
    .replace(/\u0000(\d+)\u0000/g, (_, idx: string) => codeSpans[Number(idx)] ?? "")
    .trim()
}

/** Extract heading outline (`#`..`####`) outside fenced code blocks for Knowledge Base navigation. */
export function extractMarkdownToc(markdown: string): MarkdownTocItem[] {
  if (!markdown) return []
  const items: MarkdownTocItem[] = []
  const seen = new Map<string, number>()
  const lines = markdown.replace(/\r\n/g, "\n").split("\n")
  let fence: string | null = null

  for (const line of lines) {
    const trimmed = line.trimStart()
    const fenceMatch = /^(`{3,}|~{3,})/.exec(trimmed)
    if (fenceMatch) {
      const marker = fenceMatch[1]
      if (!fence) {
        fence = marker
      } else if (trimmed.startsWith(fence[0].repeat(fence.length)) && /^[`~]+\s*$/.test(trimmed)) {
        fence = null
      }
      continue
    }
    if (fence) continue

    const headingMatch = /^(#{1,4})\s+(.+?)\s*#*\s*$/.exec(line)
    if (!headingMatch) continue
    const level = headingMatch[1].length
    const text = stripInlineMarkdown(headingMatch[2])
    if (!text) continue
    items.push({
      id: slugifyHeading(text, seen),
      text,
      level,
    })
  }

  return items
}

type MdastNode = {
  type?: string
  value?: string
  depth?: number
  children?: MdastNode[]
  data?: { hProperties?: Record<string, unknown> }
}

function collectNodeText(node: MdastNode | undefined): string {
  if (!node) return ""
  if (typeof node.value === "string") return node.value
  if (Array.isArray(node.children)) return node.children.map(collectNodeText).join("")
  return ""
}

const CALLOUT_MARKER = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\](?:[ \t]*([^\n]*))?(?:\n([\s\S]*))?$/i

/**
 * Remark plugin paired with Streamdown's default plugins:
 * 1. Stamps deterministic `id` attributes on headings (`h1`..`h6`) matching `extractMarkdownToc`
 * 2. Converts GitHub-style `> [!NOTE]` / `> [!WARNING]` alerts into `data-callout` metadata
 */
export function remarkDoopsEnhancements() {
  return (tree: MdastNode) => {
    const slugs = new Map<string, number>()

    function walk(node: MdastNode) {
      if (node.type === "heading") {
        const text = collectNodeText(node)
        if (text) {
          const id = slugifyHeading(text, slugs)
          node.data = {
            ...node.data,
            hProperties: { ...(node.data?.hProperties ?? {}), id },
          }
        }
      }

      if (node.type === "blockquote" && Array.isArray(node.children) && node.children.length > 0) {
        const firstBlock = node.children[0]
        if (firstBlock.type === "paragraph" && Array.isArray(firstBlock.children) && firstBlock.children.length > 0) {
          const firstInline = firstBlock.children[0]
          if (firstInline.type === "text" && typeof firstInline.value === "string") {
            const match = CALLOUT_MARKER.exec(firstInline.value)
            if (match) {
              const kind = match[1].toLowerCase() as CalloutKind
              const inlineText = (match[2] ?? "").trim()
              const restText = match[3] ?? ""
              const hasMoreContent =
                Boolean(restText.trim()) || firstBlock.children.length > 1 || node.children.length > 1

              const customTitle = hasMoreContent ? inlineText : ""
              const remainingInline = hasMoreContent ? restText : inlineText

              if (remainingInline) {
                firstInline.value = remainingInline
              } else {
                firstBlock.children.shift()
                if (firstBlock.children[0]?.type === "break") {
                  firstBlock.children.shift()
                }
              }
              if (firstBlock.children.length === 0) {
                node.children.shift()
              }

              node.data = {
                ...node.data,
                hProperties: {
                  ...(node.data?.hProperties ?? {}),
                  "data-callout": kind,
                  ...(customTitle ? { "data-callout-title": customTitle } : {}),
                },
              }
            }
          }
        }
      }

      if (Array.isArray(node.children)) {
        for (const child of node.children) walk(child)
      }
    }

    walk(tree)
  }
}

/** Drops raw `html` nodes in mdast (used by BlogMarkdown to strip redundant language-link HTML headers). */
export function remarkDropHtml() {
  return (tree: MdastNode) => {
    function prune(node: MdastNode) {
      if (!Array.isArray(node.children)) return
      node.children = node.children.filter((child) => child.type !== "html")
      for (const child of node.children) prune(child)
    }
    prune(tree)
  }
}

export interface KbTargetCache<M extends string = string> {
  docs: string[]
  openDoc: string | null
  content: string
  dirty: boolean
  mode: M
  drafts: Map<string, string>
}

export function createKbSessionStore<M extends string = string>(defaultMode: M) {
  const bySession = new WeakMap<object, Map<string, KbTargetCache<M>>>()
  return {
    get(session: object, targetKey: string): KbTargetCache<M> {
      let byTarget = bySession.get(session)
      if (!byTarget) {
        byTarget = new Map()
        bySession.set(session, byTarget)
      }
      let cache = byTarget.get(targetKey)
      if (!cache) {
        cache = { docs: [], openDoc: null, content: "", dirty: false, mode: defaultMode, drafts: new Map() }
        byTarget.set(targetKey, cache)
      }
      return cache
    },
  }
}

export const kbSessionStore = createKbSessionStore<"edit" | "split" | "preview">("split")


