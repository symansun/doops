"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import {
  BookOpen,
  ChevronRight,
  ListTree,
  Search,
  X,
} from "lucide-react"
import {
  DOC_CATEGORIES,
  DOCS_CATALOG,
  docHref,
  type DocEntry,
} from "@/lib/docs-catalog"
import type { MarkdownTocItem } from "@/lib/markdown-render"

function filterDocs(query: string): DocEntry[] {
  const normalized = query.trim().toLowerCase()
  if (!normalized) return DOCS_CATALOG
  return DOCS_CATALOG.filter((doc) => {
    const haystack = `${doc.title} ${doc.shortTitle} ${doc.description} ${doc.slug} ${doc.keywords.join(" ")}`.toLowerCase()
    return haystack.includes(normalized)
  })
}

function SidebarList({
  query,
  pathname,
  onSelect,
}: {
  query: string
  pathname: string
  onSelect?: () => void
}) {
  const matched = useMemo(() => filterDocs(query), [query])

  const grouped = useMemo(() => {
    const allowed = new Set(matched.map((d) => d.slug))
    return DOC_CATEGORIES.map((cat) => ({
      cat,
      items: DOCS_CATALOG.filter((doc) => doc.category === cat.id && allowed.has(doc.slug)),
    })).filter((g) => g.items.length > 0)
  }, [matched])

  if (grouped.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border/70 px-3 py-6 text-center text-xs text-muted-foreground">
        未找到匹配「{query}」的文档
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div>
        <Link
          href="/docs"
          onClick={onSelect}
          aria-current={pathname === "/docs" ? "page" : undefined}
          className={
            pathname === "/docs"
              ? "flex items-center gap-2 rounded-lg bg-primary/15 px-3 py-2 text-xs font-medium text-foreground ring-1 ring-inset ring-primary/35"
              : "flex items-center gap-2 rounded-lg px-3 py-2 text-xs text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          }
        >
          <BookOpen className="size-3.5 text-accent" aria-hidden="true" />
          文档中心总览
        </Link>
      </div>

      {grouped.map(({ cat, items }) => (
        <div key={cat.id}>
          <div className="mb-1.5 px-3 font-mono text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/75">
            {cat.title}
          </div>
          <ul className="space-y-0.5">
            {items.map((doc: DocEntry) => {
              const href = docHref(doc.slug)
              const active = pathname === href
              return (
                <li key={doc.slug}>
                  <Link
                    href={href}
                    onClick={onSelect}
                    aria-current={active ? "page" : undefined}
                    className={
                      active
                        ? "flex items-center justify-between gap-2 rounded-lg border-l-2 border-accent bg-primary/14 px-3 py-1.5 text-xs font-medium text-foreground"
                        : "flex items-center justify-between gap-2 rounded-lg px-3 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted/55 hover:text-foreground"
                    }
                  >
                    <span className="truncate">{doc.shortTitle}</span>
                    {doc.status === "design" ? (
                      <span className="shrink-0 rounded bg-warning/15 px-1.5 py-0.5 font-mono text-[10px] text-warning">
                        设计
                      </span>
                    ) : null}
                  </Link>
                </li>
              )
            })}
          </ul>
        </div>
      ))}
    </div>
  )
}

export function DocsShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname() || "/docs"
  const router = useRouter()
  const [query, setQuery] = useState("")
  const [drawerOpen, setDrawerOpen] = useState(false)
  const searchRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    setDrawerOpen(false)
  }, [pathname])

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        searchRef.current?.focus()
      } else if (e.key === "Escape" && drawerOpen) {
        setDrawerOpen(false)
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [drawerOpen])

  function handleSearchKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter" && query.trim()) {
      const first = filterDocs(query)[0]
      if (first) {
        router.push(docHref(first.slug))
        setDrawerOpen(false)
      }
    }
  }

  const currentEntry = DOCS_CATALOG.find((d) => docHref(d.slug) === pathname)

  return (
    <div className="mx-auto w-full max-w-[90rem] flex-1 px-4 sm:px-6 lg:px-8">
      {/* Mobile Docs Sub-bar */}
      <div className="flex items-center justify-between gap-2 border-b border-border/55 py-3 lg:hidden">
        <button
          type="button"
          onClick={() => setDrawerOpen((v) => !v)}
          className="inline-flex cursor-pointer items-center gap-2 rounded-lg border border-border/75 bg-card/70 px-3 py-1.5 text-xs font-medium text-foreground"
        >
          <ListTree className="size-3.5 text-accent" aria-hidden="true" />
          文档目录
          <ChevronRight
            className={`size-3.5 text-muted-foreground transition-transform ${drawerOpen ? "rotate-90" : ""}`}
            aria-hidden="true"
          />
        </button>
        <span className="truncate font-mono text-2xs text-muted-foreground">
          {currentEntry ? currentEntry.shortTitle : "文档中心"}
        </span>
      </div>

      {/* Mobile Collapsible Sidebar */}
      {drawerOpen && (
        <div className="border-b border-border/60 bg-card/35 py-4 lg:hidden">
          <div className="relative mb-4">
            <Search
              className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={handleSearchKeyDown}
              placeholder="筛选文档标题或关键词…"
              className="input input-sm w-full pl-9 pr-8"
            />
            {query ? (
              <button
                type="button"
                onClick={() => setQuery("")}
                aria-label="清空搜索"
                className="absolute right-2 top-1/2 -translate-y-1/2 cursor-pointer text-muted-foreground hover:text-foreground"
              >
                <X className="size-3.5" />
              </button>
            ) : null}
          </div>
          <SidebarList query={query} pathname={pathname} onSelect={() => setDrawerOpen(false)} />
        </div>
      )}

      <div className="flex items-start gap-7 py-8 lg:gap-9 lg:py-10">
        {/* Desktop Sticky Left Sidebar (hover-revealed thin scrollbar!) */}
        <aside
          aria-label="文档导航目录"
          className="docs-scroll sticky top-24 hidden max-h-[calc(100dvh-7rem)] w-60 shrink-0 flex-col overflow-y-auto border-r border-border/45 pr-4 lg:flex"
        >
          <div className="relative mb-4">
            <Search
              className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <input
              ref={searchRef}
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={handleSearchKeyDown}
              placeholder="搜索文档…"
              aria-label="搜索文档"
              className="input input-sm w-full pl-9 pr-12"
            />
            {query ? (
              <button
                type="button"
                onClick={() => setQuery("")}
                aria-label="清空搜索"
                className="absolute right-2.5 top-1/2 -translate-y-1/2 cursor-pointer text-muted-foreground hover:text-foreground"
              >
                <X className="size-3.5" />
              </button>
            ) : (
              <kbd className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 rounded border border-border/80 bg-card px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                ⌘K
              </kbd>
            )}
          </div>
          <SidebarList query={query} pathname={pathname} />
        </aside>

        {/* Main Document Content */}
        <div className="min-w-0 flex-1">{children}</div>
      </div>
    </div>
  )
}

export function DocsToc({ items }: { items: MarkdownTocItem[] }) {
  const [activeId, setActiveId] = useState<string>(items[0]?.id ?? "")

  useEffect(() => {
    if (items.length === 0) return
    const elements = items
      .map((item) => document.getElementById(item.id))
      .filter((el): el is HTMLElement => el !== null)
    if (elements.length === 0) return

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (visible[0]?.target.id) {
          setActiveId(visible[0].target.id)
        }
      },
      { rootMargin: "-88px 0px -65% 0px", threshold: 0 },
    )
    for (const el of elements) observer.observe(el)
    return () => observer.disconnect()
  }, [items])

  if (items.length === 0) return null

  return (
    <aside
      aria-label="本页目录"
      className="docs-scroll sticky top-24 hidden max-h-[calc(100dvh-7rem)] w-52 shrink-0 overflow-y-auto border-l border-border/50 pl-4 xl:block"
    >
      <div className="mb-3 font-mono text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/80">
        本页目录
      </div>
      <ul className="space-y-1.5 text-xs">
        {items.map((item) => {
          const isActive = item.id === activeId
          return (
            <li key={item.id} className={item.level >= 3 ? "pl-3" : ""}>
              <a
                href={`#${item.id}`}
                onClick={() => setActiveId(item.id)}
                className={
                  isActive
                    ? "block font-medium text-accent transition-colors"
                    : "block text-muted-foreground transition-colors hover:text-foreground"
                }
              >
                {item.text}
              </a>
            </li>
          )
        })}
      </ul>
    </aside>
  )
}
