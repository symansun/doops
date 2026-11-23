"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { fetchTargets, login, persistTokenSession, type Session } from "@/lib/client"
import { PlugIcon } from "./icons"
import { BrandStacked } from "./brand"
import { Button, Input, SegmentedControl, Textarea } from "./ui"

export function ConnectScreen({
  defaultGateway,
  initialError = "",
  onConnected,
}: {
  defaultGateway: string
  initialError?: string
  onConnected: (s: Session) => void
}) {
  const [gateway, setGateway] = useState(defaultGateway)
  const [mode, setMode] = useState<"password" | "token">("password")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [token, setToken] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(initialError)
  useEffect(() => { setGateway(defaultGateway) }, [defaultGateway])
  useEffect(() => { if (initialError) setError(initialError) }, [initialError])

  async function connect() {
    setError("")
    if (!gateway.trim()) {
      setError("请填写 Gateway 地址")
      return
    }
    setBusy(true)
    try {
      let session: Session
      if (mode === "password") {
        if (!username || !password) throw new Error("请填写用户名和密码")
        const r = await login(gateway, username, password)
        session = {
          gateway: r.gateway,
          username: r.username,
          isAdmin: r.isAdmin,
          tokenId: r.tokenId,
          issuedAt: r.issuedAt,
          expiresAt: r.expiresAt,
        }
      } else {
        if (!token.trim()) throw new Error("请粘贴 user token")
        session = await persistTokenSession(gateway, token.trim())
      }
      // 验证 token 有效性
      await fetchTargets(session)
      onConnected(session)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="console-login relative flex min-h-dvh items-center justify-center overflow-hidden p-4">
      <div aria-hidden className="hero-ambient pointer-events-none absolute inset-0 opacity-60" />
      <div className="relative w-full max-w-md">
        <div className="mb-8 flex flex-col items-center text-center">
          <h1 className="sr-only">DoOps Console</h1>
          <BrandStacked height={88} className="mb-5" />
          <p className="max-w-sm text-sm leading-relaxed text-muted-foreground text-balance">
            智能运维控制台 · 连接 doops-gateway 执行命令、自然语言运维与部署
          </p>
        </div>

        <div className="card-surface p-6 sm:p-7">
          <label htmlFor="login-gateway" className="mb-1.5 block text-sm font-medium text-foreground">
            Gateway 地址
          </label>
          <Input
            id="login-gateway"
            aria-label="Gateway 地址"
            value={gateway}
            onChange={(e) => setGateway(e.target.value)}
            placeholder="https://doops.l8ai.cn"
            className="mb-4 w-full font-mono"
          />

          <SegmentedControl
            value={mode}
            onChange={setMode}
            className="mb-4"
            ariaLabel="登录方式"
            options={[
              { value: "password", label: "账号密码" },
              { value: "token", label: "粘贴 Token" },
            ]}
          />

          {mode === "password" ? (
            <div className="flex flex-col gap-3">
              <Input
                aria-label="用户名"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="用户名"
                autoComplete="username"
                className="w-full"
              />
              <Input
                aria-label="密码"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && connect()}
                type="password"
                placeholder="密码"
                autoComplete="current-password"
                className="w-full"
              />
            </div>
          ) : (
            <Textarea
              aria-label="用户 Token"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="dgw_user_xxx"
              rows={3}
              className="min-h-24 w-full resize-none font-mono"
            />
          )}

          {error && (
            <p role="alert" className="status-error mt-3">
              {error}
            </p>
          )}

          <Button
            onClick={connect}
            disabled={busy}
            variant="primary"
            className="mt-4 w-full"
          >
            <PlugIcon width={16} height={16} />
            {busy ? "连接中…" : "连接"}
          </Button>
        </div>

        <p className="mt-4 text-center text-sm text-muted-foreground">
          登录会话由当前工作台的 HttpOnly Cookie 保存，业务请求统一经过 Gateway 授权。
        </p>

        <p className="mt-2 text-center text-sm">
          <Link href="/blog" className="login-link underline underline-offset-4">
            查看博客 / Blog
          </Link>
        </p>
      </div>
    </main>
  )
}
