"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { AdminConsole } from "@/components/admin-console"
import { restoreServerSession, type Session } from "@/lib/client"

export default function PlatformAdminPage() {
  const [admin, setAdmin] = useState<Session | null>(null)
  const [notice, setNotice] = useState("正在核对管理员身份…")
  useEffect(() => {
    let active = true
    restoreServerSession().then(session => {
      if (!active) return
      if (session?.isAdmin === true) setAdmin(session)
      else setNotice(session ? "当前账号不是平台管理员。" : "请先登录控制台。")
    }).catch(() => { if (active) setNotice("无法核对管理员身份，请重新连接 Gateway。") })
    return () => { active = false }
  }, [])
  return <main className="console-shell flex h-dvh flex-col overflow-hidden">
    <header className="flex items-center justify-between border-b bg-card px-4 py-3"><h1 className="text-xl font-semibold">平台管理</h1><Link className="btn" href="/console">返回控制台</Link></header>
    {admin ? <AdminConsole session={admin} /> : <p role="status" className="p-4">{notice}</p>}
  </main>
}
