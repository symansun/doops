import type { ReactNode } from "react"

import { BlogLangSwitch } from "@/components/blog-lang-switch"
import { SiteFooter, SiteHeader } from "@/components/site-chrome"
import { slugMap } from "@/lib/blog"

export const metadata = {
  title: "技术博客",
  description: "DoOps 架构、安全边界与 AI 原生运维理念的系列深度长文。",
}

// Shared shell for every /blog page. The slug map is read once at build time and
// handed to the client switcher so changing language keeps the reader in place.
export default async function BlogLayout({ children }: { children: ReactNode }) {
  const slugs = await slugMap()

  return (
    <div className="flex min-h-dvh flex-col bg-background text-foreground">
      <SiteHeader sectionBadge="Blog" rightSlot={<BlogLangSwitch slugs={slugs} />} />

      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-12 sm:px-6 sm:py-16">{children}</main>

      <SiteFooter />
    </div>
  )
}
