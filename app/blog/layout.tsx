import Link from "next/link"
import type { ReactNode } from "react"

import { BrandLockup } from "@/components/brand"
import { BlogLangSwitch } from "@/components/blog-lang-switch"
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
      <header className="site-header">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-x-4 gap-y-2 px-4">
          <Link href="/blog/zh" className="flex items-center gap-2.5 transition-opacity hover:opacity-80">
            <BrandLockup height={24} />
            <span className="border-l border-border pl-2.5 text-xs font-medium tracking-tight text-muted-foreground">
              Blog
            </span>
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

      <main className="mx-auto w-full max-w-6xl px-4 py-12 sm:py-16">{children}</main>

      <footer className="border-t border-border/40">
        <div className="mx-auto flex max-w-6xl flex-col items-center gap-3 px-4 py-10 text-center text-xs text-muted-foreground">
          <BrandLockup height={20} />
          <span className="text-muted-foreground/80">文章内容来自仓库 blog/ 目录，随镜像一同构建。</span>
        </div>
      </footer>
    </div>
  )
}
