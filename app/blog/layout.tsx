import Link from "next/link"
import type { ReactNode } from "react"

import { BlogLangSwitch } from "@/components/blog-lang-switch"
import { FileTextIcon } from "@/components/icons"
import { slugMap } from "@/lib/blog"

export const metadata = {
  title: "DoOps Blog",
  description: "DoOps 架构、安全与能力的系列长文。",
}

// Shared shell for every /blog page. The slug map is read once at build time and
// handed to the client switcher so changing language keeps the reader in place.
export default async function BlogLayout({ children }: { children: ReactNode }) {
  const slugs = await slugMap()

  return (
    <div className="min-h-dvh bg-background text-foreground">
      <header className="sticky top-0 z-20 border-b border-border/60 bg-background/85 backdrop-blur">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
          <Link href="/blog/zh" className="flex items-center gap-2 transition-opacity hover:opacity-80">
            <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/15 text-primary">
              <FileTextIcon width={18} height={18} />
            </span>
            <span className="text-sm font-semibold tracking-tight">DoOps Blog</span>
          </Link>
          <div className="flex items-center gap-3">
            <nav className="hidden items-center gap-3 text-xs text-muted-foreground sm:flex">
              <Link href="/" className="transition-colors hover:text-foreground">
                首页
              </Link>
              <Link href="/console" className="transition-colors hover:text-foreground">
                控制台
              </Link>
            </nav>
            <BlogLangSwitch slugs={slugs} />
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-6xl px-4 py-10 sm:py-14">{children}</main>

      <footer className="border-t border-border/60">
        <div className="mx-auto flex max-w-6xl flex-col items-center gap-1 px-4 py-8 text-center text-xs text-muted-foreground">
          <span>DoOps · doops.sh</span>
          <span className="text-muted-foreground/70">文章内容来自仓库 blog/ 目录，随镜像一同构建。</span>
        </div>
      </footer>
    </div>
  )
}
