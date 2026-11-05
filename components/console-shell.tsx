"use client"

import dynamic from "next/dynamic"
import type { AgentSessionRuntime } from "@agent-cloud/agent-ui"
import { Boxes, Database, Globe2 } from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { clearServerSession, fetchTargets, restoreServerSession, sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { randomSession, resolveDefaultGateway } from "@/lib/gateway"
import { isDemoAllowed } from "@/lib/demo-policy"
import { DEMO_TOKEN } from "@/lib/demo"
import { ConnectScreen } from "./connect-screen"
import { TargetSwitcher } from "./target-switcher"
import { TerminalPanel } from "./terminal-panel"
import { nextTerminalPrefill, prefillToApply, targetSwitchBlocker, type TerminalPrefill } from "@/lib/shell-blocks"
import { createDoopsAgentUiRuntime, isDoopsAgentSessionBusy } from "@/lib/agent-ui-runtime"
import { createDoopsCodeWorkspace } from "@/lib/agent-code-workspace"
import { FilesPanel } from "./files-panel"
import { KnowledgeBase } from "./knowledge-base"
import { DatabasePanel } from "./database-panel"
import { WebsitePanel } from "./website-panel"
import { K8sPanel } from "./k8s-panel"
import { ImagePanel } from "./image-panel"
import { AuditPanel } from "./audit-panel"
import { CredentialPanel } from "./credential-panel"
import { AdminConsole } from "./admin-console"
import { DashboardPanel } from "./dashboard-panel"
import { DeployView } from "./deploy-view"
import { CicdGuide } from "./cicd-guide"
import { OnboardingGuide } from "./onboarding-guide"
import {
  TerminalIcon,
  SparkIcon,
  FileIcon,
  FileTextIcon,
  HistoryIcon,
  LogoutIcon,
  ServerIcon,
  ShieldIcon,
  ActivityIcon,
  HelpIcon,
  RocketIcon,
  GitIcon,
  KeyIcon,
} from "./icons"

const AgentUiPanel = dynamic(() => import("./agent-ui-panel").then((module) => module.AgentUiPanel), {
  loading: () => <div role="status" className="flex h-full items-center justify-center text-sm text-muted-foreground">正在加载 AI 助手...</div>,
})

type Tab = "overview" | "ask" | "database" | "website" | "k8s" | "image" | "files" | "kb" | "credentials" | "audit" | "terminal"

const TABS: { id: Tab; label: string; icon: typeof TerminalIcon | typeof Database | typeof Globe2 | typeof Boxes; secondary?: boolean }[] = [
  { id: "overview", label: "概览", icon: ActivityIcon },
  { id: "ask", label: "AI 助手", icon: SparkIcon },
  { id: "database", label: "数据库", icon: Database },
  { id: "website", label: "网站", icon: Globe2 },
  { id: "k8s", label: "Kubernetes", icon: Boxes },
  { id: "image", label: "镜像", icon: Boxes },
  { id: "files", label: "文件", icon: FileIcon },
  { id: "kb", label: "知识库", icon: FileTextIcon },
  { id: "credentials", label: "凭据", icon: KeyIcon },
  { id: "audit", label: "审计", icon: HistoryIcon },
  { id: "terminal", label: "终端", icon: TerminalIcon, secondary: true },
]

const GW_KEY = "doops.gateway"
const LEGACY_DEMO_KEY = "doops.demo"
const ONBOARD_KEY = "doops.onboarded"
const DEMO_ENABLED = isDemoAllowed()

export function ConsoleShell() {
  const [session, setSession] = useState<Session | null>(null)
  const [defaultGateway, setDefaultGateway] = useState("")
  const [targets, setTargets] = useState<Target[]>([])
  const [selected, setSelected] = useState<Target | null>(null)
  const [sessionId, setSessionId] = useState(randomSession())
  const [tab, setTab] = useState<Tab>("overview")
  const [terminalPrefill, setTerminalPrefill] = useState<TerminalPrefill | null>(null)
  // 终端标签按需挂载；记录已消费的 nonce，避免重新进入终端时再次预填旧命令。
  const [consumedPrefillNonce, setConsumedPrefillNonce] = useState(0)
  const runInTerminal = useCallback((command: string) => {
    setTerminalPrefill((previous) => nextTerminalPrefill(previous, command))
    setTab("terminal")
  }, [])
  const [view, setView] = useState<"console" | "deploy" | "cicd" | "admin">("console")
  const [authLoading, setAuthLoading] = useState(true)
  const [skillRunning, setSkillRunning] = useState(false)
  const [execRunning, setExecRunning] = useState(false)
  const [loading, setLoading] = useState(false)
  const [refreshVersion, setRefreshVersion] = useState(0)
  const [error, setError] = useState("")
  const [showGuide, setShowGuide] = useState(false)
  const codeWorkspace = useMemo(() => {
    if (!session || !selected) return null
    return createDoopsCodeWorkspace({ session, target: selected, sessionId })
  }, [session, selected?.cluster, selected?.instance, sessionId])
  const agentRuntime = useMemo(() => {
    if (!session || !selected || !codeWorkspace) return null
    return createDoopsAgentUiRuntime({ session, target: selected, sessionId, codeWorkspace })
  }, [session, selected?.cluster, selected?.instance, sessionId, codeWorkspace])
  const [openedAgentRuntime, setOpenedAgentRuntime] = useState<AgentSessionRuntime | null>(null)
  useEffect(() => {
    if (!agentRuntime || (view === "console" && tab === "ask")) setOpenedAgentRuntime(agentRuntime)
  }, [agentRuntime, tab, view])
  const [adminDefaultTab, setAdminDefaultTab] = useState<
    "instances" | "config" | "users" | "grants" | "tokens"
  >("instances")

  useEffect(() => {
    if (typeof window === "undefined") return
    const saved = localStorage.getItem(GW_KEY)
    setDefaultGateway(resolveDefaultGateway(saved, window.location.origin))
    if (!DEMO_ENABLED) {
      localStorage.removeItem(LEGACY_DEMO_KEY)
    }
    // 演示模式只允许本地开发通过 URL 显式开启，不持久化 mock 会话。
    const params = new URLSearchParams(window.location.search)
    const wantsDemo = DEMO_ENABLED && params.get("demo") === "1"
    if (wantsDemo) {
      // ?demo=1&role=user 模拟普通用户界面，默认仍是管理员界面。
      const demoAdmin = params.get("role") !== "user"
      setSession({ gateway: "demo://local", token: DEMO_TOKEN, username: "演示用户", demo: true, isAdmin: demoAdmin })
      if (localStorage.getItem(ONBOARD_KEY) !== "1") setShowGuide(true)
      setAuthLoading(false)
      return
    }

    let active = true
    void restoreServerSession()
      .then((restored) => {
        if (!active || !restored) return
        localStorage.setItem(GW_KEY, restored.gateway)
        setDefaultGateway(restored.gateway)
        setSession(restored)
      })
      .catch((err) => {
        if (active) setError(`登录会话恢复失败，请重新连接 Gateway：${(err as Error).message}`)
      })
      .finally(() => {
        if (active) setAuthLoading(false)
      })
    return () => {
      active = false
    }
  }, [])

  const refresh = useCallback(async () => {
    if (!session) return
    setLoading(true)
    setError("")
    try {
      const t = await fetchTargets(session)
      setTargets(t)
      setRefreshVersion((version) => version + 1)
      setSelected((cur) => {
        if (cur) {
          const still = t.find((x) => x.key === cur.key)
          return still || t[0] || null
        }
        return t[0] || null
      })
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    if (!session) return
    refresh()
    const id = setInterval(refresh, 10000)
    return () => clearInterval(id)
  }, [session, refresh])

  function handleConnected(s: Session) {
    if (!s.demo) {
      localStorage.setItem(GW_KEY, s.gateway)
      localStorage.removeItem(LEGACY_DEMO_KEY)
    }
    setSession(s)
    if (localStorage.getItem(ONBOARD_KEY) !== "1") setShowGuide(true)
  }

  function closeGuide() {
    setShowGuide(false)
    localStorage.setItem(ONBOARD_KEY, "1")
  }

  function logout() {
    if (skillRunning) { setError("请先等待当前操作结束，再退出登录"); return }
    const current = session
    void (async () => {
      try {
        if (!current?.demo) await clearServerSession()
      } catch (err) {
        setError((err as Error).message)
      } finally {
        localStorage.removeItem(LEGACY_DEMO_KEY)
        setSession(null)
        setTargets([])
        setSelected(null)
      }
    })()
  }

  function selectTarget(t: Target) {
    const blocker = targetSwitchBlocker({
      skillRunning,
      agentBusy: Boolean(agentRuntime && isDoopsAgentSessionBusy(agentRuntime.getSnapshot(sessionId))),
      execRunning,
    })
    if (blocker) { setError(blocker); return }
    setSelected(t)
    setSessionId(randomSession())
    // AI 优先：切换机器后，默认进入 AI 助手而非终端
    setTab((cur) => (cur === "overview" || cur === "terminal" ? "ask" : cur))
  }

  if (authLoading) {
    return <main className="flex min-h-dvh items-center justify-center text-sm text-muted-foreground">正在恢复登录会话...</main>
  }

  if (!session) {
    return <ConnectScreen defaultGateway={defaultGateway} initialError={error} onConnected={handleConnected} />
  }

  // 只控制管理入口是否显示；管理接口的权限仍由 Gateway 检查。
  const isAdmin = sessionIsAdmin(session)

  return (
    <div className="flex h-dvh flex-col">
      <header className="flex shrink-0 items-center justify-between border-b bg-card px-4 py-2.5">
        <div className="flex min-w-0 max-w-full items-center gap-2 overflow-x-auto">
          <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
            <ServerIcon width={16} height={16} />
          </div>
          <span className="hidden truncate text-sm font-semibold text-foreground sm:inline">
            Doops Console
          </span>
          {(view === "console" || view === "deploy") && (
            <TargetSwitcher
              targets={targets}
              selected={selected}
              onSelect={selectTarget}
              onRefresh={refresh}
              loading={loading}
            />
          )}
          <div className="ml-1 flex shrink-0 items-center gap-0.5 rounded-lg border bg-muted/40 p-0.5 sm:ml-2">
            <button
              disabled={skillRunning}
              onClick={() => setView("console")}
              className={`whitespace-nowrap rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                view === "console"
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              控制台
            </button>
            <button
              disabled={skillRunning}
              onClick={() => setView("deploy")}
              className={`flex items-center gap-1 whitespace-nowrap rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                view === "deploy"
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              <RocketIcon width={13} height={13} /> 部署
            </button>
            <button
              disabled={skillRunning}
              onClick={() => setView("cicd")}
              className={`flex items-center gap-1 whitespace-nowrap rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                view === "cicd"
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              <GitIcon width={13} height={13} /> CI/CD
            </button>
            {isAdmin && (
              <button
                disabled={skillRunning}
                onClick={() => setView("admin")}
                className={`flex items-center gap-1 whitespace-nowrap rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                  view === "admin"
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                <ShieldIcon width={13} height={13} /> 管理
              </button>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {session.username && (
            <span className="hidden text-xs text-muted-foreground sm:inline">{session.username}</span>
          )}
          <button
            onClick={() => setShowGuide(true)}
            title="新手引导"
            className="flex items-center gap-1.5 rounded-lg border px-2 py-1.5 text-xs text-foreground transition-colors hover:bg-muted"
          >
            <HelpIcon width={14} height={14} /> <span className="hidden sm:inline">帮助</span>
          </button>
          <button
            onClick={logout}
            title="断开连接"
            className="flex items-center gap-1.5 rounded-lg border px-2 py-1.5 text-xs text-foreground transition-colors hover:bg-muted"
          >
            <LogoutIcon width={14} height={14} /> <span className="hidden sm:inline">断开</span>
          </button>
        </div>
      </header>

      {showGuide && <OnboardingGuide onClose={closeGuide} />}

      {error && <div role="alert" className="shrink-0 border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive">{error}</div>}
      {view === "admin" && isAdmin ? (
        <AdminConsole session={session} defaultTab={adminDefaultTab} />
      ) : view === "deploy" ? (
        <DeployView session={session} target={selected} sessionId={sessionId} onEnterConsole={() => { setView("console"); refresh() }}
          onOpenCredentials={isAdmin ? () => { setView("console"); setTab("credentials") } : undefined} />
      ) : view === "cicd" ? (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <CicdGuide session={session} />
        </div>
      ) : (
      <div className={tab === "ask" && selected ? "flex shrink-0" : "flex min-h-0 flex-1"}>
        <main className="flex min-w-0 flex-1 flex-col">
          <nav className="flex shrink-0 gap-1 overflow-x-auto border-b bg-card px-2">
            {TABS.map((t) => {
              const Icon = t.icon
              const active = tab === t.id
              return (
                <button
                  key={t.id}
                  onClick={() => setTab(t.id)}
                  disabled={skillRunning}
                  title={t.secondary ? "高级：直接执行 shell 命令" : undefined}
                  className={`-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2.5 text-sm transition-colors ${
                    t.secondary ? "ml-auto" : ""
                  } ${
                    active
                      ? "border-primary text-foreground"
                      : t.secondary
                        ? "border-transparent text-muted-foreground/70 hover:text-foreground"
                        : "border-transparent text-muted-foreground hover:text-foreground"
                  }`}
                >
                  <Icon width={16} height={16} />
                  {t.label}
                  {t.secondary && (
                    <span className="rounded bg-muted px-1 py-0.5 text-[10px] font-normal text-muted-foreground">
                      高级
                    </span>
                  )}
                </button>
              )
            })}
          </nav>

          {tab === "overview" ? (
            <DashboardPanel
              session={session}
              targets={targets}
              selected={selected}
              loading={loading}
              refreshVersion={refreshVersion}
              onRefresh={refresh}
              onDeployAgent={() => setView("deploy")}
              onOpenTab={(nextTab, target) => {
                if (target) selectTarget(target)
                setTab(nextTab)
              }}
            />
          ) : !selected ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-3 px-4 text-center text-muted-foreground">
              <ServerIcon width={32} height={32} />
              <p className="text-sm">还没有可用的机器，部署 agent 或联系管理员接入后即可使用此功能</p>
              <div className="flex flex-wrap items-center justify-center gap-2">
                <button
                  onClick={() => setView("deploy")}
                  className="rounded-lg bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90"
                >
                  部署 Doops Agent
                </button>
                <button
                  onClick={() => setTab("overview")}
                  className="rounded-lg border px-3 py-1.5 text-xs text-foreground transition-colors hover:bg-muted"
                >
                  返回概览
                </button>
              </div>
            </div>
          ) : tab === "ask" ? null : (
            <>
              {/* files / ask / kb 面板自带头部，避免重复占一行 */}
              {tab !== "files" && tab !== "kb" && (
                <div className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-b bg-card px-4 py-1.5">
                  <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    session
                    <input
                      value={sessionId}
                      disabled={skillRunning}
                      onChange={(e) => setSessionId(e.target.value)}
                      className="w-44 rounded-md border bg-background px-2 py-1 font-mono text-xs text-foreground outline-none focus:border-ring"
                    />
                  </label>
                </div>
              )}

              <div className="min-h-0 flex-1">
                {tab === "database" && (
                  <DatabasePanel
                    key={`database-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "website" && (
                  <WebsitePanel
                    key={`website-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "k8s" && (
                  <K8sPanel
                    key={`k8s-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "image" && (
                  <ImagePanel
                    key={`image-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "terminal" && (
                  <TerminalPanel
                    key={`term-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    prefill={prefillToApply(terminalPrefill, consumedPrefillNonce) === null ? null : terminalPrefill}
                    onPrefillApplied={setConsumedPrefillNonce}
                  />
                )}
                {tab === "files" && (
                  <FilesPanel
                    key={`files-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onSessionChange={setSessionId}
                  />
                )}
                {tab === "kb" && (
                  <KnowledgeBase
                    key={`kb-${selected.key}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                  />
                )}
                {tab === "audit" && (
                  <AuditPanel
                    key={`audit-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                  />
                )}
                {tab === "credentials" && (
                  <CredentialPanel
                    key={`credentials-${selected.key}`}
                    session={session}
                    target={selected}
                  />
                )}
              </div>
            </>
          )}
        </main>
      </div>
      )}
      {selected && codeWorkspace && agentRuntime && openedAgentRuntime === agentRuntime && (
        <div hidden={view !== "console" || tab !== "ask"} className="min-h-0 flex-1">
          <AgentUiPanel
            key={`ask-${selected.key}-${sessionId}`}
            session={session}
            target={selected}
            onRunInTerminal={runInTerminal}
            runtime={agentRuntime}
            sessionId={sessionId}
            codeWorkspace={codeWorkspace}
            active={view === "console" && tab === "ask"}
            onConfigureModel={isAdmin ? () => {
              setAdminDefaultTab("config")
              setView("admin")
            } : undefined}
            onSessionChange={setSessionId}
            onExecRunningChange={setExecRunning}
          />
        </div>
      )}
    </div>
  )
}
