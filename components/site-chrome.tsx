"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { ArrowUpRight, BookOpen, Menu, Sparkles, Terminal } from "lucide-react"
import { BrandLockup, BrandMark } from "./brand"
import {
  Badge,
  Button,
  PortalContainer,
  Separator,
  Sheet,
  SheetBody,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "./ui"
import { isDemoAllowed } from "@/lib/demo-policy"
import { GITHUB_REPO_URL } from "@/lib/docs-catalog"

const DEMO_ENABLED = isDemoAllowed()

export const PORTAL_NAV = [
  { label: "AI 工具箱", href: "/#ai-tools", match: null },
  { label: "架构安全", href: "/#architecture", match: null },
  { label: "核心能力", href: "/#capabilities", match: null },
  { label: "文档中心", href: "/docs", match: "/docs" },
  { label: "使用案例", href: "/cases", match: "/cases" },
  { label: "技术博客", href: "/blog", match: "/blog" },
] as const

function GitHubGlyph({ className = "size-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
      <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.53 1.032 1.53 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0 1 12 6.844a9.59 9.59 0 0 1 2.504.337c1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.02 10.02 0 0 0 22 12.017C22 6.484 17.522 2 12 2Z" />
    </svg>
  )
}

export function SiteHeader({
  sectionBadge,
  rightSlot,
}: {
  sectionBadge?: string
  rightSlot?: React.ReactNode
}) {
  const pathname = usePathname()
  const [mobileOpen, setMobileOpen] = useState(false)

  useEffect(() => {
    setMobileOpen(false)
  }, [pathname])

  function isActive(match: string | null) {
    if (!match || !pathname) return false
    return pathname === match || pathname.startsWith(`${match}/`)
  }

  const hasRightSlot = Boolean(rightSlot)

  return (
    <header className="site-header">
      <PortalContainer className="flex h-16 items-center justify-between gap-3">
        <div className="flex shrink-0 items-center gap-2.5">
          <Link
            href="/"
            aria-label="DoOps 首页"
            className="flex shrink-0 items-center transition-opacity hover:opacity-90"
          >
            <BrandLockup height={28} />
          </Link>
          {sectionBadge ? (
            <Badge variant="outline" className="hidden bg-card/70 font-mono sm:inline-flex">
              {sectionBadge}
            </Badge>
          ) : null}
        </div>

        <nav
          aria-label="主导航"
          className={`hidden shrink-0 items-center gap-0.5 rounded-full border border-border/65 bg-card/55 p-1 text-xs text-muted-foreground backdrop-blur ${hasRightSlot ? "xl:flex" : "lg:flex"}`}
        >
          {PORTAL_NAV.map((item) => {
            const active = isActive(item.match)
            return (
              <Link
                key={item.label}
                href={item.href}
                aria-current={active ? "page" : undefined}
                className={
                  active
                    ? "whitespace-nowrap rounded-full bg-primary/15 px-3 py-1.5 font-medium text-foreground ring-1 ring-inset ring-primary/30"
                    : "whitespace-nowrap rounded-full px-3 py-1.5 transition-colors hover:bg-foreground/5 hover:text-foreground"
                }
              >
                {item.label}
              </Link>
            )
          })}
        </nav>

        <div className="flex shrink-0 items-center gap-2">
          {rightSlot ? <div className="hidden shrink-0 sm:flex">{rightSlot}</div> : null}
          <Button
            asChild
            variant="ghost"
            size="sm"
            className="hidden gap-1.5 px-2 text-muted-foreground hover:text-foreground md:inline-flex xl:px-2.5"
          >
            <a href={GITHUB_REPO_URL} target="_blank" rel="noreferrer" aria-label="GitHub 仓库" title="GitHub 开源仓库">
              <GitHubGlyph className="size-4" />
              <span className="hidden font-mono text-2xs xl:inline">GitHub</span>
            </a>
          </Button>
          {DEMO_ENABLED && (
            <Button asChild variant="ghost" size="sm" className="hidden sm:inline-flex">
              <Link href="/console?demo=1">
                <Sparkles className="size-3.5 text-accent" aria-hidden="true" />
                在线演示
              </Link>
            </Button>
          )}
          <Button asChild variant="primary" size="sm">
            <Link href="/console">进入控制台</Link>
          </Button>

          <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
            <SheetTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                icon
                aria-label="打开导航菜单"
                className={hasRightSlot ? "xl:hidden" : "lg:hidden"}
              >
                <Menu className="size-4" aria-hidden="true" />
              </Button>
            </SheetTrigger>
            <SheetContent side="right" className="w-4/5 max-w-xs">
              <SheetHeader>
                <SheetTitle>导航菜单</SheetTitle>
              </SheetHeader>
              <SheetBody className="flex flex-col gap-1.5">
                {rightSlot ? (
                  <div className="mb-2 flex items-center justify-between border-b border-border/50 pb-3 sm:hidden">
                    <span className="text-xs text-muted-foreground">语言切换</span>
                    {rightSlot}
                  </div>
                ) : null}
                <nav aria-label="移动端导航" className="flex flex-col gap-1">
                  {PORTAL_NAV.map((item) => {
                    const active = isActive(item.match)
                    return (
                      <Link
                        key={item.label}
                        href={item.href}
                        onClick={() => setMobileOpen(false)}
                        aria-current={active ? "page" : undefined}
                        className={
                          active
                            ? "flex min-h-11 items-center justify-between rounded-lg bg-primary/15 px-3 text-sm font-medium text-foreground ring-1 ring-inset ring-primary/30"
                            : "flex min-h-11 items-center justify-between rounded-lg px-3 text-sm text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
                        }
                      >
                        {item.label}
                      </Link>
                    )
                  })}
                </nav>
                <Separator className="my-3" />
                <div className="grid grid-cols-2 gap-2">
                  {DEMO_ENABLED && (
                    <Button asChild size="sm" className="justify-center">
                      <Link href="/console?demo=1" onClick={() => setMobileOpen(false)}>
                        <Sparkles className="size-3.5 text-accent" aria-hidden="true" />
                        在线演示
                      </Link>
                    </Button>
                  )}
                  <Button asChild size="sm" className="justify-center">
                    <a href={GITHUB_REPO_URL} target="_blank" rel="noreferrer">
                      <GitHubGlyph className="size-3.5" />
                      GitHub
                      <ArrowUpRight className="size-3 text-muted-foreground" aria-hidden="true" />
                    </a>
                  </Button>
                </div>
              </SheetBody>
            </SheetContent>
          </Sheet>
        </div>
      </PortalContainer>
    </header>
  )
}

const FOOTER_COLUMNS = [
  {
    heading: "产品与入口",
    links: [
      { label: "Web 控制台", href: "/console" },
      ...(DEMO_ENABLED ? [{ label: "在线交互演示", href: "/console?demo=1" }] : []),
      { label: "核心能力矩阵", href: "/#capabilities" },
      { label: "快速上手", href: "/#quickstart" },
      { label: "使用案例", href: "/cases" },
    ],
  },
  {
    heading: "开发者文档",
    links: [
      { label: "文档中心总览", href: "/docs" },
      { label: "客户端与集群接入", href: "/docs/cluster-install" },
      { label: "CLI 命令手册", href: "/docs/usage-guide" },
      { label: "Agent 部署与升级", href: "/docs/deploy" },
      { label: "HTTP / WS API 指南", href: "/docs/api-guide" },
    ],
  },
  {
    heading: "架构与安全",
    links: [
      { label: "Gateway 隧道与生命周期", href: "/docs/gateway-tunnel" },
      { label: "受管凭据资源", href: "/docs/credentials" },
      { label: "DoSql 数据治理引擎", href: "/docs/dosql" },
      { label: "Skill-First 智能巡检", href: "/docs/skill-first-inspection" },
      { label: "运行时超时权威表", href: "/docs/runtime-limits" },
    ],
  },
  {
    heading: "开源社区",
    links: [
      { label: "技术深度博客", href: "/blog" },
      { label: "GitHub 仓库", href: GITHUB_REPO_URL, external: true },
      { label: "Issue 与反馈", href: `${GITHUB_REPO_URL}/issues`, external: true },
      { label: "Releases 下载", href: `${GITHUB_REPO_URL}/releases`, external: true },
      { label: "MIT 开源协议", href: `${GITHUB_REPO_URL}/blob/main/LICENSE`, external: true },
    ],
  },
] as const

export function SiteFooter() {
  return (
    <footer className="border-t border-border/50 bg-card/20">
      <PortalContainer className="py-14">
        <div className="grid gap-10 lg:grid-cols-12">
          <div className="lg:col-span-4">
            <div className="flex items-center gap-3">
              <BrandMark height={28} />
              <div>
                <div className="text-base font-semibold tracking-tight text-foreground">DoOps</div>
                <div className="font-mono text-2xs text-muted-foreground">AI-Native Operations Hub</div>
              </div>
            </div>
            <p className="mt-4 max-w-xs text-xs leading-relaxed text-muted-foreground">
              面向开发者的开源智能运维中心。通过 Gateway-First 反向隧道统一收口 Web、CLI、IDE Skill 与
              API，让每一次操作可授权、可审计、可验证。
            </p>
            <div className="mt-5 flex flex-wrap items-center gap-2">
              <Button asChild size="sm">
                <Link href="/docs">
                  <BookOpen className="size-3.5 text-primary" aria-hidden="true" />
                  文档中心
                </Link>
              </Button>
              <Button asChild variant="ghost" size="sm">
                <a href={GITHUB_REPO_URL} target="_blank" rel="noreferrer">
                  <GitHubGlyph className="size-3.5" />
                  Star on GitHub
                </a>
              </Button>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-8 sm:grid-cols-4 lg:col-span-8">
            {FOOTER_COLUMNS.map((col) => (
              <div key={col.heading}>
                <h3 className="text-xs font-semibold tracking-wide text-foreground">{col.heading}</h3>
                <ul className="mt-3.5 space-y-2.5 text-xs text-muted-foreground">
                  {col.links.map((link) => (
                    <li key={link.label}>
                      {"external" in link && link.external ? (
                        <a
                          href={link.href}
                          target="_blank"
                          rel="noreferrer"
                          className="inline-flex items-center gap-1 transition-colors hover:text-foreground"
                        >
                          {link.label}
                          <ArrowUpRight className="size-3 opacity-60" aria-hidden="true" />
                        </a>
                      ) : (
                        <Link href={link.href} className="transition-colors hover:text-foreground">
                          {link.label}
                        </Link>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>

        <Separator className="my-10 opacity-45" />
        <div className="flex flex-col gap-3 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span>© 2026 DoOps · MIT License</span>
            <span aria-hidden="true" className="text-border">·</span>
            <span className="inline-flex items-center gap-1 font-mono text-2xs">
              <Terminal className="size-3 text-accent" aria-hidden="true" />
              Gateway-First · Zero Inbound SSH · Fail-Closed Audit
            </span>
          </div>
          <span className="text-2xs text-muted-foreground/80">
            核心贡献者：孙勇、高家祺、宋晨宇
          </span>
        </div>
      </PortalContainer>
    </footer>
  )
}
