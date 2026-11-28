import type { Metadata, Viewport } from "next"
import "@agent-cloud/agent-ui/styles.css"
import "katex/dist/katex.min.css"
import "streamdown/styles.css"
import "./globals.css"

export const metadata: Metadata = {
  title: {
    default: "DoOps · 面向开发者的智能运维中心",
    template: "%s · DoOps",
  },
  description:
    "开源、Gateway-First 的开发者智能运维中心。通过反向隧道网关统一连接 Web 控制台、CLI、IDE Skill 与 API，在同一套权限、受管凭据与全链路审计下协同 AI 完成终端排障、DoSql 数据治理、Kubernetes 交付与智能巡检。",
}

export const viewport: Viewport = {
  themeColor: "#090d16",
  width: "device-width",
  initialScale: 1,
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="zh-CN" className="dark bg-background">
      <body className="font-sans antialiased">{children}</body>
    </html>
  )
}
