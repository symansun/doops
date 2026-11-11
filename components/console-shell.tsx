"use client"

import Link from "next/link"
import dynamic from "next/dynamic"
import type { AgentSessionRuntime } from "@agent-cloud/agent-ui"
import { Boxes, Database, Globe2 } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { clearServerSession, fetchTargets, restoreServerSession, sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { randomSession, resolveDefaultGateway } from "@/lib/gateway"
import { isDemoAllowed } from "@/lib/demo-policy"
import { DEMO_TOKEN } from "@/lib/demo"
import { errorAfterRefresh, reconcileSelected, reconcileTargets, type RefreshTrigger } from "@/lib/console-refresh"
import { displayUserName } from "@/lib/session-display"
import { NETWORK_FAILURE_MESSAGE, subscribeNetworkStatus } from "@/lib/network-error"
import { consoleViewEntries, credentialsDestination, type ConsoleView, type ConsoleViewEntry } from "@/lib/console-views"
import {
  browserSessionStorage,
  clearConsoleState,
  commandJournal,
  consoleScopeKey,
  loadSelection,
  restoreSelection,
  saveSelection,
} from "@/lib/console-persistence"
import type { StorageLike } from "@/lib/console-persistence"
import { interruptedBannerNotices, interruptedBannerView } from "@/lib/interrupted-banner"
import { installJournalUnloadHandlers } from "@/lib/unload-journal"
import type { ChangelogRelease } from "@/lib/changelog"
import {
  autoOpenDecision,
  hasUnseenVersions,
  localDay,
  readWhatsNewState,
  recordLastVersion,
  setQuietDay,
} from "@/lib/whats-new"
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
import { WhatsNewDialog, type WhatsNewMode } from "./whats-new-dialog"
import { Button, Input, PageTabs } from "./ui"
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
const TERMINAL_RUNNING_MESSAGE = "终端命令仍在执行，请先等待结束或停止命令，再切换目标、会话或页面"

// 「更新内容」的已读版本和「今天不再弹出」只写本机 localStorage。隐私模式或禁用
// 存储时访问会抛错，按首次访问处理：手动入口仍然可用，只是每次都不弹出。
function browserLocalStorage(): StorageLike | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage
  } catch {
    return null
  }
}

type WhatsNewOpen = {
  mode: WhatsNewMode
  /** The version seen before this open; the dialog's "since" range starts after it. */
  sinceVersion: string | null
  /** Whether newer versions existed before this open, for the default range. */
  hasUnseen: boolean
}

export function ConsoleShell({ changelog = [] }: { changelog?: ChangelogRelease[] }) {
  const [session, setSession] = useState<Session | null>(null)
  const [defaultGateway, setDefaultGateway] = useState("")
  const [targets, setTargets] = useState<Target[]>([])
  const [selected, setSelected] = useState<Target | null>(null)
  const [sessionId, setSessionId] = useState(randomSession())
  const [tab, setTab] = useState<Tab>("overview")
  const [terminalPrefill, setTerminalPrefill] = useState<TerminalPrefill | null>(null)
  // 终端标签按需挂载；记录已消费的 nonce，避免重新进入终端时再次预填旧命令。
  const [consumedPrefillNonce, setConsumedPrefillNonce] = useState(0)
  const terminalRunning = useRef(false)
  const runInTerminal = useCallback((command: string) => {
    if (terminalRunning.current) { setError(TERMINAL_RUNNING_MESSAGE); return }
    setTerminalPrefill((previous) => nextTerminalPrefill(previous, command))
    setTab("terminal")
  }, [])
  const [view, setView] = useState<"console" | "deploy" | "cicd" | "admin">("console")
  const [authLoading, setAuthLoading] = useState(true)
  const [disconnecting, setDisconnecting] = useState(false)
  const [skillRunning, setSkillRunning] = useState(false)
  const onTerminalRunningChange = useCallback((running: boolean) => {
    terminalRunning.current = running
    setSkillRunning(running)
    if (!running) setError((current) => current === TERMINAL_RUNNING_MESSAGE ? "" : current)
  }, [])
  const [execRunning, setExecRunning] = useState(false)
  const [loading, setLoading] = useState(false)
  const [refreshVersion, setRefreshVersion] = useState(0)
  const [error, setError] = useState("")
  // A failed target poll is shown apart from operation errors and clears itself
  // on the next successful poll.
  const [targetsError, setTargetsError] = useState("")
  // Shown once when a reload could not return to the target this tab had.
  const [restoreNotice, setRestoreNotice] = useState("")
  // The Gateway + user whose saved selection has been restored in this tab.
  const restoredScope = useRef("")
  // Commands an earlier page started and never saw end, shown once in the
  // terminal. They are reported, never replayed.
  const [interruptedNotices, setInterruptedNotices] = useState<string[]>([])
  // True while the browser reports it is offline; the banner goes away on its
  // own when the connection comes back. Nothing is retried automatically.
  const [offline, setOffline] = useState(false)
  const [showGuide, setShowGuide] = useState(false)
  // The release the built console shows. The server passes the parsed
  // CHANGELOG.md down, so the dialog has no fetch of its own.
  const releaseVersion = changelog[0]?.version ?? ""
  const [whatsNewOpen, setWhatsNewOpen] = useState<WhatsNewOpen | null>(null)
  const [whatsNewUnread, setWhatsNewUnread] = useState(false)
  const autoOpenChecked = useRef(false)
  const autoOpenHandled = useRef(false)
  const [autoOpenPending, setAutoOpenPending] = useState(false)

  const openWhatsNew = useCallback(
    (mode: WhatsNewMode) => {
      if (!releaseVersion) return
      // Any open (manual included) counts as having seen the current version,
      // so neither a reload nor a second open re-opens the automatic dialog.
      autoOpenHandled.current = true
      const storage = browserLocalStorage()
      const state = readWhatsNewState(storage)
      setWhatsNewOpen({
        mode,
        sinceVersion: state.lastVersion,
        hasUnseen: hasUnseenVersions(state, releaseVersion),
      })
      recordLastVersion(storage, releaseVersion)
      setWhatsNewUnread(false)
    },
    [releaseVersion],
  )

  const closeWhatsNew = useCallback(
    (options: { quietToday: boolean }) => {
      const storage = browserLocalStorage()
      if (options.quietToday) setQuietDay(storage, localDay())
      if (releaseVersion) recordLastVersion(storage, releaseVersion)
      setWhatsNewUnread(false)
      setWhatsNewOpen(null)
    },
    [releaseVersion],
  )

  // Read the remembered version once: a first visit records it silently, an
  // upgrade asks for the automatic dialog, and "today" keeps that dialog (and
  // the unread dot) out of the way while the manual entry stays available.
  useEffect(() => {
    if (autoOpenChecked.current) return
    autoOpenChecked.current = true
    if (!releaseVersion) return
    const storage = browserLocalStorage()
    const state = readWhatsNewState(storage)
    setWhatsNewUnread(hasUnseenVersions(state, releaseVersion))
    const decision = autoOpenDecision({ state, currentVersion: releaseVersion, today: localDay() })
    if (decision === "record") {
      recordLastVersion(storage, releaseVersion)
      return
    }
    if (decision === "open") setAutoOpenPending(true)
  }, [releaseVersion])

  // The automatic dialog waits for a restored session and for the onboarding
  // guide to be out of the way, and opens at most once per page load.
  useEffect(() => {
    if (!autoOpenPending || autoOpenHandled.current) return
    if (!session || showGuide) return
    setAutoOpenPending(false)
    openWhatsNew("auto")
  }, [autoOpenPending, session, showGuide, openWhatsNew])

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

  // The 10-second poll is a background refresh: it keeps unchanged target
  // objects and leaves an operation error on screen. Only a refresh the user
  // asks for clears it.
  const refresh = useCallback(async (trigger: RefreshTrigger = "user") => {
    if (!session) return
    setLoading(true)
    setError((current) => errorAfterRefresh(current, trigger))
    try {
      const t = await fetchTargets(session)
      setTargets((previous) => reconcileTargets(previous, t))
      setTargetsError("")
      setRefreshVersion((version) => version + 1)
      const scopeKey = consoleScopeKey(session)
      if (restoredScope.current !== scopeKey) {
        restoredScope.current = scopeKey
        const restored = restoreSelection(loadSelection(browserSessionStorage(), session), t)
        setSelected(restored.selected)
        if (restored.sessionId) setSessionId(restored.sessionId)
        setRestoreNotice(restored.notice)
        const interrupted = commandJournal.takeInterrupted(session)
        if (interrupted.length) {
          setInterruptedNotices(interruptedBannerNotices(interrupted, restored))
          if (restored.selected) setTab("terminal")
        }
      } else {
        setSelected((cur) => reconcileSelected(cur, t, terminalRunning.current))
      }
    } catch (err) {
      setTargetsError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    if (!session) return
    refresh("user")
    const id = setInterval(() => refresh("background"), 10000)
    return () => clearInterval(id)
  }, [session, refresh])

  const clearInterruptedNotices = useCallback(() => setInterruptedNotices([]), [])

  useEffect(() => {
    setOffline(typeof navigator !== "undefined" && navigator.onLine === false)
    return subscribeNetworkStatus(window, (online) => setOffline(!online))
  }, [])

  // A reload or close aborts the /api/rpc stream, which ends a running command
  // on the target. Warn while one runs; the unloading flag must be set in
  // beforeunload, because Chrome ends the aborted stream before pagehide.
  useEffect(() => installJournalUnloadHandlers(window, commandJournal), [])

  // Only the target key and session id are kept, never output or tokens.
  useEffect(() => {
    if (!session || !selected || restoredScope.current !== consoleScopeKey(session)) return
    saveSelection(browserSessionStorage(), session, { targetKey: selected.key, sessionId })
  }, [session, selected?.key, sessionId])

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
    if (blockTerminalSwitch()) return
    if (skillRunning) { setError("请先等待当前操作结束，再退出登录"); return }
    if (disconnecting) return
    setDisconnecting(true)
    const current = session
    void (async () => {
      try {
        if (!current?.demo) await clearServerSession()
        setError("")
      } catch (err) {
        setError((err as Error).message)
      } finally {
        localStorage.removeItem(LEGACY_DEMO_KEY)
        if (current) clearConsoleState(browserSessionStorage(), current)
        restoredScope.current = ""
        setRestoreNotice("")
        setInterruptedNotices([])
        setSession(null)
        setTargets([])
        setSelected(null)
        setDisconnecting(false)
      }
    })()
  }

  // Switching views clears the operation error, like a refresh the user asked for.
  function openView(next: typeof view) {
    if (next !== view && blockTerminalSwitch()) return
    setError("")
    setView(next)
  }

  function blockTerminalSwitch() {
    if (!terminalRunning.current) return false
    setError(TERMINAL_RUNNING_MESSAGE)
    return true
  }

  function selectTab(next: Tab) {
    if (next !== tab && blockTerminalSwitch()) return
    setTab(next)
  }

  function changeSession(next: string) {
    if (next !== sessionId && blockTerminalSwitch()) return
    setSessionId(next)
  }

  function selectTarget(t: Target) {
    if (blockTerminalSwitch()) return
    const blocker = targetSwitchBlocker({
      skillRunning,
      agentBusy: Boolean(agentRuntime && isDoopsAgentSessionBusy(agentRuntime.getSnapshot(sessionId))),
      execRunning,
    })
    if (blocker) { setError(blocker); return }
    setError("")
    setRestoreNotice("")
    setInterruptedNotices([])
    setSelected(t)
    setSessionId(randomSession())
    // AI 优先：切换机器后，默认进入 AI 助手而非终端
    setTab((cur) => (cur === "overview" || cur === "terminal" ? "ask" : cur))
  }

  if (authLoading || disconnecting) {
    return <main className="flex min-h-dvh items-center justify-center text-sm text-muted-foreground">{disconnecting ? "正在断开连接..." : "正在恢复登录会话..."}</main>
  }

  if (!session) {
    return <ConnectScreen defaultGateway={defaultGateway} initialError={error} onConnected={handleConnected} />
  }

  // 只控制管理入口是否显示；管理接口的权限仍由 Gateway 检查。
  const isAdmin = sessionIsAdmin(session)
  const viewEntries = consoleViewEntries(isAdmin)
  // "打开凭据" on the AI assistant and the release page: the credentials tab,
  // where a modelProvider credential is bound. Only admins get the link.
  const credentials = credentialsDestination(isAdmin)
  const openCredentials = credentials
    ? () => {
        openView(credentials.view)
        selectTab(credentials.tab)
      }
    : undefined

  // K25：刷新后的「结果未知」提示固定在 shell 顶层渲染，不依赖终端标签页是否打开，
  // 也不依赖当次 /api/targets 是否返回目标；记录留在状态里，直到用户关闭横幅。
  const interruptedBanner = interruptedBannerView(interruptedNotices)

  return (
    <div className="flex h-dvh w-full flex-col">
      <header className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-card px-4 py-2.5">
        <div className="flex min-w-0 flex-1 items-center gap-2 lg:flex-initial">
          <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
            <ServerIcon width={16} height={16} />
          </div>
          <span className="hidden truncate text-sm font-semibold text-foreground lg:inline">
            Doops Console
          </span>
          {(view === "console" || view === "deploy") && (
            <TargetSwitcher
              targets={targets}
              selected={selected}
              onSelect={selectTarget}
              onRefresh={() => refresh("user")}
              loading={loading}
              isAdmin={isAdmin}
            />
          )}
          {/* From 640px up the view entries sit inline, as before. */}
          <div className="ml-2 hidden shrink-0 items-center gap-0.5 rounded-lg border bg-muted/40 p-0.5 sm:flex">
            {viewEntries.map((entry) => (
              <ViewButton
                key={entry.id}
                entry={entry}
                active={view === entry.id}
                disabled={skillRunning}
                onSelect={openView}
                className="px-2.5 py-1"
              />
            ))}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span className="hidden text-xs text-muted-foreground sm:inline">{displayUserName(session)}</span>
          <Link
            href="/blog"
            onClick={(event) => {
              if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && blockTerminalSwitch()) event.preventDefault()
            }}
            title="博客"
            className="flex items-center gap-1.5 rounded-lg border px-2 py-1.5 text-xs text-foreground transition-colors hover:bg-muted"
          >
            <SparkIcon width={14} height={14} />
            <span className="hidden sm:inline">博客</span>
          </Link>
          <Button
            onClick={() => openWhatsNew("manual")}
            disabled={!releaseVersion}
            data-testid="whats-new-button"
            title={releaseVersion ? "更新内容" : "当前版本没有更新内容"}
            size="sm"
            className="relative"
          >
            <HistoryIcon width={14} height={14} />
            <span className="hidden sm:inline">更新内容</span>
            {whatsNewUnread && releaseVersion && (
              <>
                <span className="sr-only">有新版本</span>
                <span
                  aria-hidden="true"
                  className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-primary ring-2 ring-card"
                />
              </>
            )}
          </Button>
          <Button
            onClick={() => setShowGuide(true)}
            title="新手引导"
            size="sm"
          >
            <HelpIcon width={14} height={14} /> <span className="hidden sm:inline">帮助</span>
          </Button>
          <Button
            onClick={logout}
            title="断开连接"
            size="sm"
          >
            <LogoutIcon width={14} height={14} /> <span className="hidden sm:inline">断开</span>
          </Button>
        </div>
      </header>
      {/* Below 640px the view entries get a row of their own, so every one of
          them stays visible without scrolling and nothing covers it. */}
      <nav
        aria-label="视图"
        data-testid="view-nav-narrow"
        className="grid shrink-0 gap-1 border-b bg-card px-2 py-1.5 sm:hidden"
        style={{ gridTemplateColumns: `repeat(${viewEntries.length}, minmax(0, 1fr))` }}
      >
        {viewEntries.map((entry) => (
          <ViewButton
            key={entry.id}
            entry={entry}
            active={view === entry.id}
            disabled={skillRunning}
            onSelect={openView}
            className="justify-center border px-1 py-1.5"
          />
        ))}
      </nav>

      {showGuide && <OnboardingGuide onClose={closeGuide} />}

      {whatsNewOpen && releaseVersion && changelog.length > 0 && (
        <WhatsNewDialog
          releases={changelog}
          mode={whatsNewOpen.mode}
          sinceVersion={whatsNewOpen.sinceVersion}
          hasUnseen={whatsNewOpen.hasUnseen}
          onClose={closeWhatsNew}
        />
      )}

      {offline && (
        <div role="alert" data-testid="offline-banner" className="shrink-0 border-b border-warning/40 bg-warning/10 px-4 py-2 text-sm text-warning">
          {NETWORK_FAILURE_MESSAGE}。网络恢复后此提示会自动消失，不会自动重试操作。
        </div>
      )}
      {interruptedBanner && (
        <div role={interruptedBanner.role} data-testid="interrupted-commands-banner"
          className="flex shrink-0 flex-wrap items-start gap-2 border-b border-warning/40 bg-warning/10 px-4 py-2 text-sm text-warning">
          <span className="min-w-0 flex-1">
            {interruptedBanner.notices.map((notice, index) => (
              <span key={index} className="block">{notice}</span>
            ))}
          </span>
          <button onClick={clearInterruptedNotices} className="shrink-0 rounded px-2 py-0.5 text-xs hover:bg-warning/20">
            {interruptedBanner.dismissLabel}
          </button>
        </div>
      )}
      {(error || targetsError) && <div role="alert" className="shrink-0 border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive">{[error, targetsError].filter(Boolean).join("；")}</div>}
      {terminalRunning.current && selected && (
        <div role="status" className="shrink-0 border-b bg-card px-4 py-2 text-sm text-muted-foreground">
          {selected.cluster}/{selected.instance} · {sessionId}：{TERMINAL_RUNNING_MESSAGE}
        </div>
      )}
      {restoreNotice && (
        <div role="status" className="flex shrink-0 items-center gap-2 border-b border-warning/40 bg-warning/10 px-4 py-2 text-sm text-warning">
          <span className="min-w-0 flex-1">{restoreNotice}</span>
          <button onClick={() => setRestoreNotice("")} className="shrink-0 rounded px-2 py-0.5 text-xs hover:bg-warning/20">
            知道了
          </button>
        </div>
      )}
      {view === "admin" && isAdmin ? (
        <AdminConsole session={session} />
      ) : view === "deploy" ? (
        <DeployView session={session} target={selected} sessionId={sessionId} onEnterConsole={() => { openView("console"); refresh("user") }}
          onOpenCredentials={openCredentials} />
      ) : view === "cicd" ? (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <CicdGuide session={session} />
        </div>
      ) : (
      <div className={tab === "ask" && selected ? "flex shrink-0" : "flex min-h-0 flex-1"}>
        <main className="flex min-w-0 flex-1 flex-col">
          <PageTabs
            value={tab}
            onChange={selectTab}
            disabled={skillRunning}
            ariaLabel="控制台功能"
            items={TABS.map(({ id, label, icon: Icon, secondary }) => ({
              id,
              icon: <Icon width={16} height={16} />,
              label: <>{label}{secondary && <span className="rounded bg-muted px-1 py-0.5 text-[10px] font-normal text-muted-foreground">高级</span>}</>,
              secondary,
              title: secondary ? "高级：直接执行 shell 命令" : undefined,
            }))}
          />

          {tab === "overview" ? (
            <DashboardPanel
              session={session}
              targets={targets}
              selected={selected}
              loading={loading}
              refreshVersion={refreshVersion}
              onRefresh={() => refresh("user")}
              onDeployAgent={() => openView("deploy")}
              onOpenTab={(nextTab, target) => {
                if (target) selectTarget(target)
                selectTab(nextTab)
              }}
            />
          ) : !selected ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-3 px-4 text-center text-muted-foreground">
              <ServerIcon width={32} height={32} />
              <p className="text-sm">还没有可用的机器，部署 agent 或联系管理员接入后即可使用此功能</p>
              <div className="flex flex-wrap items-center justify-center gap-2">
                <Button
                  onClick={() => openView("deploy")}
                  variant="primary"
                  size="sm"
                >
                  部署 Doops Agent
                </Button>
                <Button
                  onClick={() => selectTab("overview")}
                  size="sm"
                >
                  返回概览
                </Button>
              </div>
            </div>
          ) : tab === "ask" ? null : (
            <>
              {/* files / ask / kb 面板自带头部，避免重复占一行 */}
              {tab !== "files" && tab !== "kb" && (
                <div className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-b bg-card px-4 py-1.5">
                  <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    session
                    <Input
                      value={sessionId}
                      disabled={skillRunning}
                      onChange={(e) => changeSession(e.target.value)}
                      scale="sm"
                      className="w-44 font-mono"
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
                    onRunningChange={onTerminalRunningChange}
                  />
                )}
                {tab === "files" && (
                  <FilesPanel
                    key={`files-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onSessionChange={changeSession}
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
            onConfigureModel={openCredentials}
            onSessionChange={changeSession}
            onExecRunningChange={setExecRunning}
          />
        </div>
      )}
    </div>
  )
}

const VIEW_ICONS: Partial<Record<ConsoleView, typeof RocketIcon>> = {
  deploy: RocketIcon,
  cicd: GitIcon,
  admin: ShieldIcon,
}

function ViewButton({
  entry,
  active,
  disabled,
  onSelect,
  className,
}: {
  entry: ConsoleViewEntry
  active: boolean
  disabled: boolean
  onSelect: (view: ConsoleView) => void
  className: string
}) {
  const Icon = VIEW_ICONS[entry.id]
  return (
    <Button
      disabled={disabled}
      onClick={() => onSelect(entry.id)}
      aria-current={active ? "page" : undefined}
      size="sm"
      variant={active ? "primary" : "ghost"}
      className={`whitespace-nowrap ${className}`}
    >
      {Icon && <Icon width={13} height={13} />} {entry.label}
    </Button>
  )
}
