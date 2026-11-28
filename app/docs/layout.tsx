import type { Metadata } from "next"
import { SiteFooter, SiteHeader } from "@/components/site-chrome"
import { DocsShell } from "@/components/docs-shell"

export const metadata: Metadata = {
  title: {
    default: "文档中心",
    template: "%s · DoOps 文档中心",
  },
  description:
    "DoOps 权威配套文档中心：客户端与集群接入、CLI 使用手册、Gateway 反向隧道、受管凭据、DoSql 数据治理、Skill-First 巡检与运维 SOP。",
}

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col bg-background text-foreground">
      <SiteHeader sectionBadge="Docs" />
      <DocsShell>{children}</DocsShell>
      <SiteFooter />
    </div>
  )
}
