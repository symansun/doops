import type { Metadata } from "next"
import { CasesPage } from "@/components/cases-page"

export const metadata: Metadata = {
  title: "使用案例",
  description:
    "看开发者团队如何用 DoOps 落地 AI 编码闭环交付、DoSql 多引擎数据库治理、AI 交互终端值班排障与跨区域 Skill-First 智能巡检。",
}

export default function Page() {
  return <CasesPage />
}
