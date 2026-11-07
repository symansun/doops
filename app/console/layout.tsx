import type { Metadata } from "next"

export const metadata: Metadata = {
  title: "Doops Console · 智能运维工作台",
  description: "Doops 统一运维工作台：连接目标、调用领域 skill，并通过 Gateway 完成认证、授权、执行与审计。",
}

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  return children
}
