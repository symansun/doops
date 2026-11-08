import type { Metadata, Viewport } from "next"
import "./globals.css"
import "@agent-cloud/agent-ui/styles.css"

export const metadata: Metadata = {
  title: "Doops · 全方位智能运维工具",
  description:
    "Doops 是一个全方位的智能运维工具，统一提供 Web 控制台、CLI 和多领域 skill，覆盖服务器、Kubernetes、网站、数据库、文件与审计。",
}

export const viewport: Viewport = {
  themeColor: "#1a1d23",
  width: "device-width",
  initialScale: 1,
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="zh-CN" className="bg-background">
      <body className="font-sans antialiased">{children}</body>
    </html>
  )
}
