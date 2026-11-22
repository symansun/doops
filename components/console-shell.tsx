"use client"

import Link from "next/link"
import { Menu } from "lucide-react"
import dynamic from "next/dynamic"
import type { AgentSessionRuntime } from "@agent-cloud/agent-ui"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { clearServerSession, fetchTargets, restoreServerSession, sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { isLostGatewaySession } from "@/lib/gateway-auth"
import { randomSession, resolveDefaultGateway } from "@/lib/gateway"
import { isDemoAllowed } from "@/lib/demo-policy"
import { DEMO_TOKEN } from "@/lib/demo"
import { errorAfterRefresh, reconcileSelected, reconcileTargets, workspaceTargets, type TargetRef, type RefreshTrigger } from "@/lib/console-refresh"
import { assetRequest } from "@/lib/assets"
import { displayUserName } from "@/lib/session-display"
import { NETWORK_FAILURE_MESSAGE, subscribeNetworkStatus } from "@/lib/network-error"
import { CONSOLE_HOME, assetKinds, credentialsDestination, targetTabLabel, type CatalogKind, type AssetArea, type ConsoleNavEntry, type ConsoleView, type DeploySection, type TargetTab as Tab } from "@/lib/console-views"
import { buildConsoleSearch, parseConsoleSearch } from "@/lib/console-url"
import {
  browserSessionStorage,
  clearConsoleState,
  clearSelection,
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
import { BackgroundTasksPanel } from "./background-tasks-panel"
import { navigationBlocker, nextTerminalPrefill, prefillToApply, targetSwitchBlocker, TERMINAL_RUNNING_MESSAGE, type TerminalPrefill } from "@/lib/shell-blocks"
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
import { ResourceCatalogPanel, resourceAddress, type CatalogRow } from "./resource-catalog-panel"
import { LegacyAssetNotes } from "./legacy-asset-notes"
import { AssetInventory, assetReturnLabel, type AssetPage } from "./asset-inventory"
import { AgentAssets, AgentDirectory } from "./agent-asset-scope"
import { AgentMonitor } from "./asset-monitor-panel"
import { CurrentMachineSection, DashboardPanel } from "./dashboard-panel"
import { NodeInfoCard } from "./node-info-card"
import { DeployView } from "./deploy-view"
import { ConsoleNav } from "./console-nav"
import { SessionInput } from "./session-input"
import { OnboardingGuide } from "./onboarding-guide"
import { WhatsNewDialog, type WhatsNewMode } from "./whats-new-dialog"
import { Button, StatusBanner } from "./ui"
import {
  SparkIcon,
  HistoryIcon,
  LogoutIcon,
  ServerIcon,
  HelpIcon,
} from "./icons"
import { BrandLockup } from "./brand"

const AgentUiPanel = dynamic(() => import("./agent-ui-panel").then((module) => module.AgentUiPanel), {
  loading: () => <div role="status" className="flex h-full items-center justify-center text-sm text-muted-foreground">正在加载 AI 助手...</div>,
})


const GW_KEY = "doops.gateway"
const LEGACY_DEMO_KEY = "doops.demo"
const ONBOARD_KEY = "doops.onboarded"
const DEMO_ENABLED = isDemoAllowed()

// 「更新内容」的已读版本和「今天不再弹出」只写本机 localStorage。隐私模式或禁用
// 存储时访问会抛错，按首次访问处理：手动入口仍然可用，只是每次都不弹出。
function browserLocalStorage(): StorageLike | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage
  } catch {
    return null
  }
}

// Gateway 地址和引导状态只是便利项：存储不可用时照常登录和使用，只是不记住。
function readLocal(key: string): string | null {
  try { return browserLocalStorage()?.getItem(key) ?? null } catch { return null }
}
function writeLocal(key: string, value: string) {
  try { browserLocalStorage()?.setItem(key, value) } catch { /* not persisted */ }
}
function removeLocal(key: string) {
  try { browserLocalStorage()?.removeItem(key) } catch { /* nothing to remove */ }
}

type WhatsNewOpen = {
  mode: WhatsNewMode
  /** The version seen before this open; the dialog's "since" range starts after it. */
  sinceVersion: string | null
  /** Whether newer versions existed before this open, for the default range. */
  hasUnseen: boolean
}

export function ConsoleShell({ changelog = [] }: { changelog?: ChangelogRelease[] }) {
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [session, setSession] = useState<Session | null>(null)
  const [defaultGateway, setDefaultGateway] = useState("")
  const [targets, setTargets] = useState<Target[]>([])
  const [registered, setRegistered] = useState<TargetRef[]>([])
  const [directoryError, setDirectoryError] = useState("")
  const directory = useMemo(() => workspaceTargets(targets, registered), [targets, registered])
  const latestRefresh = useRef(0)
  const refreshing = useRef(false)
  const [selected, setSelected] = useState<Target | null>(null)
  const onlineSelected = selected?.status === "offline" ? null : selected
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
  const [view, setView] = useState<ConsoleView>(CONSOLE_HOME)
  const pendingHomeTab = useRef<Tab | null>(null)
  const [deploySection, setDeploySection] = useState<DeploySection>("onboard")
  const [catalogKind, setCatalogKind] = useState<CatalogKind>("database")
  const [catalogContext, setCatalogContext] = useState<{row: CatalogRow; targetKey: string; sessionId: string; scope:string} | null>(null)
  const [assetNamespace, setAssetNamespace] = useState("")
  const [assetOrigin, setAssetOrigin] = useState<{ page: AssetPage; targetKey: string; scope: string } | null>(null)
  const [assetPage, setAssetPage] = useState<AssetPage>({ section: "board" })
  // 平台资产台账 reopens the section the user last read; the header owns the area.
  const lastLedger = useRef<AssetPage>({ section: "machines" })
  const urlInitializedRef = useRef(false)
  const poppingHistoryRef = useRef(false)
  const routeRef = useRef({ view, tab, assetPage, deploySection, catalogKind })
  routeRef.current = { view, tab, assetPage, deploySection, catalogKind }
  const [authLoading, setAuthLoading] = useState(true)
  const [disconnecting, setDisconnecting] = useState(false)
  const [skillRunning, setSkillRunning] = useState(false)
  const onTerminalRunningChange = useCallback((running: boolean) => {
    terminalRunning.current = running
    setSkillRunning(running)
    if (!running) setError((current) => current === TERMINAL_RUNNING_MESSAGE ? "" : current)
  }, [])
  const [execRunning, setExecRunning] = useState(false)
  // Read by the popstate listener, which outlives the render that registered it.
  const navLock = useRef({ skillRunning, execRunning })
  navLock.current = { skillRunning, execRunning }
  const navLockMessage = useRef("")
  useEffect(() => {
    if (!skillRunning && !execRunning) setError((current) => current && current === navLockMessage.current ? "" : current)
  }, [skillRunning, execRunning])
  const [loading, setLoading] = useState(false)
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
    if (!session || !onlineSelected) return null
    return createDoopsCodeWorkspace({ session, target: onlineSelected, sessionId })
  }, [session, onlineSelected?.cluster, onlineSelected?.instance, sessionId])
  const agentRuntime = useMemo(() => {
    if (!session || !onlineSelected || !codeWorkspace) return null
    return createDoopsAgentUiRuntime({ session, target: onlineSelected, sessionId, codeWorkspace })
  }, [session, onlineSelected?.cluster, onlineSelected?.instance, sessionId, codeWorkspace])
  const [openedAgentRuntime, setOpenedAgentRuntime] = useState<AgentSessionRuntime | null>(null)
  useEffect(() => {
    if (!agentRuntime || (view === "console" && tab === "ask")) setOpenedAgentRuntime(agentRuntime)
  }, [agentRuntime, tab, view])

  useEffect(() => {
    if (typeof window === "undefined") return
    const saved = readLocal(GW_KEY)
    setDefaultGateway(resolveDefaultGateway(saved, window.location.origin))
    if (!DEMO_ENABLED) {
      removeLocal(LEGACY_DEMO_KEY)
    }
    // 演示模式只允许本地开发通过 URL 显式开启，不持久化 mock 会话。
    const params = new URLSearchParams(window.location.search)
    const wantsDemo = DEMO_ENABLED && params.get("demo") === "1"
    if (wantsDemo) {
      // ?demo=1&role=user 模拟普通用户界面，默认仍是管理员界面。
      const demoAdmin = params.get("role") !== "user"
      setSession({ gateway: "demo://local", token: DEMO_TOKEN, username: "演示用户", demo: true, isAdmin: demoAdmin })
      if (readLocal(ONBOARD_KEY) !== "1") setShowGuide(true)
      setAuthLoading(false)
      return
    }

    let active = true
    void restoreServerSession()
      .then((restored) => {
        if (!active || !restored) return
        writeLocal(GW_KEY, restored.gateway)
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
    if (refreshing.current && trigger === "background") return
    refreshing.current = true
    const request = ++latestRefresh.current
    setLoading(true)
    setError((current) => errorAfterRefresh(current, trigger))
    try {
      const [t, catalog] = await Promise.all([
        fetchTargets(session),
        session.demo ? Promise.resolve({ targets: [], error: "" }) :
          assetRequest<{ targets: TargetRef[] }>("workspace", undefined, new URLSearchParams({ view: "directory" }))
            .then(({ targets }) => ({ targets, error: "" }), (err: Error) => ({ targets: [], error: err.message })),
      ])
      if (request !== latestRefresh.current) return
      setRegistered(catalog.targets)
      setDirectoryError(catalog.error)
      setTargets((previous) => reconcileTargets(previous, t))
      setTargetsError("")
      const available = workspaceTargets(t, catalog.targets)
      const scopeKey = consoleScopeKey(session)
      if (restoredScope.current !== scopeKey) {
        restoredScope.current = scopeKey
        const restored = restoreSelection(loadSelection(browserSessionStorage(), session), available)
        setSelected(restored.selected)
        if (restored.sessionId) setSessionId(restored.sessionId)
        setRestoreNotice(restored.notice)
        const interrupted = commandJournal.takeInterrupted(session)
        if (interrupted.length) {
          setInterruptedNotices(interruptedBannerNotices(interrupted, restored))
          if (restored.selected) setTab("terminal")
        }
      } else {
        setSelected(cur => reconcileSelected(cur, terminalRunning.current ? t : available, terminalRunning.current))
      }
    } catch (err) {
      if (request !== latestRefresh.current) return
      if (isLostGatewaySession(err)) {
        try {
          if (!session.demo) await clearServerSession()
        } catch {
          // Cookie clear is best-effort; the session is already unusable.
        }
        setSession(null)
        setTargets([])
        setSelected(null)
        setRegistered([])
        setDirectoryError("")
        setTargetsError("")
        setError((err as Error).message || "登录已过期，请重新登录")
        return
      }
      setTargetsError((err as Error).message)
    } finally {
      if (request === latestRefresh.current) {
        refreshing.current = false
        setLoading(false)
      }
    }
  }, [session])

  useEffect(() => {
    if (!session) return
    refresh("user")
    const id = setInterval(() => refresh("background"), 10000)
    return () => { clearInterval(id); latestRefresh.current++ }
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

  // Sync in-console route state with URL query params and browser Back/Forward history.
  useEffect(() => {
    if (!session || typeof window === "undefined") return
    if (!urlInitializedRef.current) {
      urlInitializedRef.current = true
      const initial = parseConsoleSearch(window.location.search)
      poppingHistoryRef.current = true
      setView(initial.view)
      setTab(initial.tab)
      setAssetPage(initial.assetPage)
      setDeploySection(initial.deploySection)
      setCatalogKind(initial.catalogKind)
      if (!selected && initial.view === "console") pendingHomeTab.current = initial.tab
      return
    }
    if (poppingHistoryRef.current) {
      poppingHistoryRef.current = false
      return
    }
    const nextSearch = buildConsoleSearch(window.location.search, {
      view,
      tab,
      assetPage,
      deploySection,
      catalogKind,
    })
    if (nextSearch !== window.location.search) {
      window.history.pushState(null, "", `${window.location.pathname}${nextSearch}`)
    }
  }, [session, view, tab, assetPage, deploySection, catalogKind])

  useEffect(() => {
    if (!session || typeof window === "undefined") return
    function onPopState() {
      const blocker = navigationBlocker({ terminalRunning: terminalRunning.current, ...navLock.current })
      if (blocker) {
        navLockMessage.current = blocker
        setError(blocker)
        const lockedSearch = buildConsoleSearch(window.location.search, routeRef.current)
        window.history.pushState(null, "", `${window.location.pathname}${lockedSearch}`)
        return
      }
      const parsed = parseConsoleSearch(window.location.search)
      poppingHistoryRef.current = true
      setError("")
      setView(parsed.view)
      setTab(parsed.tab)
      setAssetPage(parsed.assetPage)
      setDeploySection(parsed.deploySection)
      setCatalogKind(parsed.catalogKind)
    }
    window.addEventListener("popstate", onPopState)
    return () => window.removeEventListener("popstate", onPopState)
  }, [session])

  function handleConnected(s: Session) {
    urlInitializedRef.current = true
    setView(CONSOLE_HOME)
    setTab("overview")
    pendingHomeTab.current = null
    setAssetPage({ section: "board" })
    setAssetOrigin(null)
    if (!s.demo) {
      writeLocal(GW_KEY, s.gateway)
      removeLocal(LEGACY_DEMO_KEY)
    }
    setSession(s)
    if (readLocal(ONBOARD_KEY) !== "1") setShowGuide(true)
  }

  function closeGuide() {
    setShowGuide(false)
    writeLocal(ONBOARD_KEY, "1")
  }

  function logout() {
    if (blockTerminalSwitch()) return
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
        removeLocal(LEGACY_DEMO_KEY)
        if (current) clearConsoleState(browserSessionStorage(), current)
        urlInitializedRef.current = false
        restoredScope.current = ""
        setRestoreNotice("")
        setInterruptedNotices([])
        setSession(null)
        setTargets([])
        setSelected(null)
        setRegistered([])
        setDirectoryError("")
        setDisconnecting(false)
      }
    })()
  }

  function openAssetPage(next: AssetPage) {
    if (next.section !== "board" && next.section !== "scope") lastLedger.current = next
    setAssetPage(next)
  }

  // 平台监控 and 平台资产台账 are separate top-level entries; the ledger keeps the
  // last section so switching between them does not reset the list state.
  function openAssetArea(area: AssetArea): void {
    openView("assets")
    if (area === "machines") return openAssetPage({section:"machines"})
    if (area === "board") return openAssetPage({ section: "board" })
    if (area === "scope") return openAssetPage({ section: "scope" })
    if (lastLedger.current.section === area) return openAssetPage(lastLedger.current)
    if (area === "clusters") return openAssetPage({ section: "clusters", query: "", expanded: [] })
    return openAssetPage({ section: "systems" })
  }

  function navigateEntry(entry: ConsoleNavEntry) {
    const destination = entry.destination
    if (destination.view === CONSOLE_HOME) return openView(CONSOLE_HOME)
    if (destination.view === "assets") {
      openAssetArea(destination.area)
      return
    }
    if (destination.view === "deploy") {
      openView("deploy")
      return setDeploySection(destination.section)
    }
    if (destination.view === "admin") {
      if (session && sessionIsAdmin(session) && !blockTerminalSwitch()) window.location.assign("/console/admin")
      return
    }
    if (destination.view === "catalog") {
      if (blockTerminalSwitch()) return
      setCatalogKind(destination.kind)
      return openView("catalog")
    }
    openView("console")
    if (selected) {
      selectTab(destination.tab)
      return
    }
    // With no target picked the workspace shows the Agent directory. Keep the
    // tool highlighted so the click visibly lands, and remember it for the
    // target the user picks next.
    pendingHomeTab.current = destination.tab
    setTab(destination.tab)
  }

  // Switching views clears the operation error, like a refresh the user asked for.
  function openView(next: typeof view) {
    if (next !== view && blockTerminalSwitch()) return
    if (next !== view) {
      pendingHomeTab.current = null
      if (next === "assets") {
        openAssetPage(session && assetOrigin?.scope === consoleScopeKey(session) && assetOrigin.targetKey === selected?.key ? assetOrigin.page : { section: "board" })
      }
      setAssetOrigin(null)
    }
    if (next === "deploy" && view !== "deploy") setDeploySection(onlineSelected ? "workload" : "onboard")
    setError("")
    setView(next)
  }

  function openAgentSetup() {
    openView("deploy")
    setDeploySection("onboard")
  }

  // 终端有命令在跑时，切换视图、目标或工具会中断那次执行，所以先拦下来告诉用户。
  function blockTerminalSwitch() {
    const blocker = navigationBlocker({ terminalRunning: terminalRunning.current, skillRunning, execRunning })
    if (!blocker) return false
    navLockMessage.current = blocker
    setError(blocker)
    return true
  }

  function selectTab(next: Tab) {
    if (next !== tab && blockTerminalSwitch()) return
    if (next !== "k8s") setAssetNamespace("")
    setTab(next)
  }

  function changeSession(next: string) {
    if (next === sessionId || blockTerminalSwitch()) return
    if (agentRuntime && isDoopsAgentSessionBusy(agentRuntime.getSnapshot(sessionId))) { setError("请先停止当前 AI 任务，再切换会话"); return }
    setSessionId(next)
  }

  function selectTarget(t: Target) {
    if (blockTerminalSwitch()) return false
    const blocker = targetSwitchBlocker({
      skillRunning,
      agentBusy: Boolean(agentRuntime && isDoopsAgentSessionBusy(agentRuntime.getSnapshot(sessionId))),
      execRunning,
    })
    if (blocker) { setError(blocker); return false }
    if (selected?.key !== t.key) setAssetOrigin(null)
    setError("")
    setRestoreNotice("")
    setInterruptedNotices([])
    setSelected(t)
    setAssetNamespace("")
    setSessionId(randomSession())
    // A terminal is scoped to one target and session. Review the new target before opening its terminal.
    setTab((cur) => cur === "terminal" ? "overview" : cur)
    return true
  }

  function openHomeTool(next: Tab) {
    if (blockTerminalSwitch()) return
    openView("console")
    selectTab(next)
    if (!selected) pendingHomeTab.current = next
  }

  function chooseWorkspaceTarget(target: Target) {
    if (!selectTarget(target)) return
    if (!selected && view === "console") {
      setTab(pendingHomeTab.current ?? "overview")
      pendingHomeTab.current = null
    }
  }

  if (authLoading || disconnecting) {
    return (
      <main className="grid min-h-dvh place-items-center px-4">
        <p role="status" className="text-center text-lg font-medium text-foreground">
          {disconnecting ? "正在断开连接..." : "正在恢复登录会话..."}
        </p>
      </main>
    )
  }

  if (!session) {
    return <ConnectScreen defaultGateway={defaultGateway} initialError={error} onConnected={handleConnected} />
  }

  // 只控制管理入口是否显示；管理接口的权限仍由 Gateway 检查。
  const isAdmin = sessionIsAdmin(session)
  // "打开凭据" on the AI assistant and the release page: the credentials tab,
  // where a modelProvider credential is bound. Only admins get the link.
  const credentials = credentialsDestination(isAdmin)
  const openCredentials = credentials
    ? () => openHomeTool(credentials.tab)
    : undefined

  // K25：刷新后的「结果未知」提示固定在 shell 顶层渲染，不依赖终端标签页是否打开，
  // 也不依赖当次 /api/targets 是否返回目标；记录留在状态里，直到用户关闭横幅。
  const interruptedBanner = interruptedBannerView(interruptedNotices)

  const resource = catalogContext && catalogContext.targetKey === selected?.key && catalogContext.sessionId === sessionId && catalogContext.row.kind === tab && catalogContext.scope === consoleScopeKey(session) ? catalogContext.row : null
  const fromAsset = view === "console" && assetOrigin?.scope === consoleScopeKey(session) && assetOrigin.targetKey === selected?.key && targets.some(t => t.key === selected.key && t.status !== "offline")

  return (
    <div className="console-shell flex h-dvh w-full flex-col">
      <header className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border/50 bg-card/80 px-4 py-2.5 backdrop-blur">
        <button type="button" className="console-menu-toggle md:hidden" aria-label="打开主菜单"
          aria-haspopup="dialog" aria-expanded={mobileNavOpen} aria-controls={mobileNavOpen ? "console-nav-drawer" : undefined}
          onClick={() => setMobileNavOpen(true)}><Menu size={20} aria-hidden="true" />菜单</button>
        <Link href="/" aria-label="DoOps 首页" className="shrink-0 transition-opacity hover:opacity-90">
          <BrandLockup height={34} />
        </Link>
        <div className="ml-auto flex shrink-0 flex-wrap items-center gap-2">
          <TargetSwitcher
            targets={directory}
            selected={selected}
            onSelect={chooseWorkspaceTarget}
            onViewAll={() => {
              if (blockTerminalSwitch()) return
              if (agentRuntime && isDoopsAgentSessionBusy(agentRuntime.getSnapshot(sessionId))) { setError("请等待当前 Agent 操作结束。"); return }
              clearSelection(browserSessionStorage(), session)
              setSelected(null); setAssetOrigin(null); setTab("overview"); setSessionId(randomSession())
            }}
            onRefresh={() => refresh("user")}
            loading={loading}
            isAdmin={isAdmin}
          />
          <span className="hidden text-xs text-muted-foreground sm:inline">{displayUserName(session)}</span>
          <Link
            href="/blog"
            onClick={(event) => {
              if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && blockTerminalSwitch()) event.preventDefault()
            }}
            title="博客"
            className="btn btn-sm"
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
      <div className="flex min-h-0 min-w-0 flex-1">
      <ConsoleNav mobileOpen={mobileNavOpen} onDismiss={() => setMobileNavOpen(false)}
        view={view}
        tab={tab}
        assetArea={assetPage.section}
        deploySection={deploySection}
        catalogKind={catalogKind}
        isAdmin={isAdmin}
        hasTarget={Boolean(onlineSelected)}
        onNavigate={navigateEntry}
      />
      <div className="console-workspace flex min-h-0 min-w-0 flex-1 flex-col">
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
        <StatusBanner tone="warning" role="alert" data-testid="offline-banner" className="shrink-0 rounded-none border-x-0 border-t-0 px-4">
          {NETWORK_FAILURE_MESSAGE}。网络恢复后此提示会自动消失，不会自动重试操作。
        </StatusBanner>
      )}
      {interruptedBanner && (
        <StatusBanner tone="warning" role={interruptedBanner.role} data-testid="interrupted-commands-banner"
          className="flex shrink-0 flex-wrap items-start gap-2 rounded-none border-x-0 border-t-0 px-4">
          <span className="min-w-0 flex-1">
            {interruptedBanner.notices.map((notice, index) => (
              <span key={index} className="block">{notice}</span>
            ))}
          </span>
          <Button size="sm" variant="ghost" onClick={clearInterruptedNotices} className="shrink-0">
            {interruptedBanner.dismissLabel}
          </Button>
        </StatusBanner>
      )}
      {(error || targetsError) && (
        <StatusBanner tone="error" className="shrink-0 rounded-none border-x-0 border-t-0 px-4">
          {[error, targetsError].filter(Boolean).join("；")}
        </StatusBanner>
      )}
      {terminalRunning.current && selected && (
        <div role="status" className="shrink-0 border-b bg-card px-4 py-2 text-sm text-muted-foreground">
          {selected.cluster}/{selected.instance} · {sessionId}：{TERMINAL_RUNNING_MESSAGE}
        </div>
      )}
      {restoreNotice && (
        <StatusBanner tone="warning" className="flex shrink-0 items-center gap-2 rounded-none border-x-0 border-t-0 px-4">
          <span className="min-w-0 flex-1">{restoreNotice}</span>
          <Button size="sm" variant="ghost" onClick={() => setRestoreNotice("")} className="shrink-0">
            知道了
          </Button>
        </StatusBanner>
      )}
      {view === "console" && resource && <div className="flex flex-wrap items-center gap-2 border-b p-2 text-sm"><Button size="sm" onClick={()=>{if(!blockTerminalSwitch()){setCatalogKind(resource.kind);openView("catalog")}}}>返回资产台账</Button>{resource.name} · {resourceAddress(resource)}</div>}
      {fromAsset && (
        <section aria-label="目标上下文" className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-card px-4 py-2">
          <Button size="sm" variant="ghost" disabled={skillRunning} onClick={() => openView("assets")}>
            {assetReturnLabel(assetOrigin.page)}
          </Button>
        </section>
      )}
      {view === CONSOLE_HOME ? (
        <DashboardPanel session={session} targets={targets} loading={loading}
          onRefresh={() => refresh("user")} onDeployAgent={openAgentSetup} />
      ) : view === "catalog" ? (
        catalogKind === "credentials" ? <CredentialPanel key={`credentials-${consoleScopeKey(session)}-${selected?.key ?? "none"}`} session={session} target={selected ?? undefined} initialView="managed" />
          : session.demo ? (
            <div className="p-4">
              <div className="card-surface space-y-3 p-4 text-sm">
                <p>请连接 Gateway 查看和维护持久化{assetKinds[catalogKind]}台账。</p>
                {catalogKind !== "k8s" && targets.some((t) => t.status !== "offline") && (
                  <div className="flex flex-wrap items-center gap-2 pt-1">
                    <span className="text-xs text-muted-foreground">演示模式下可直接进入在线目标的{assetKinds[catalogKind]}实时面板：</span>
                    {(onlineSelected ? [onlineSelected] : targets.filter((t) => t.status !== "offline")).map((t) => (
                      <Button key={t.key} size="sm" variant="primary" onClick={() => {
                        if (t.key !== selected?.key && !selectTarget(t)) return
                        setCatalogContext(null)
                        openView("console")
                        selectTab(catalogKind)
                      }}>
                        打开 {t.cluster}/{t.instance} {assetKinds[catalogKind]}面板
                      </Button>
                    ))}
                  </div>
                )}
              </div>
            </div>
          ) : catalogKind === "k8s" ? null : <>
              <ResourceCatalogPanel
                key={JSON.stringify([consoleScopeKey(session), catalogKind])}
                kind={catalogKind}
                targets={directory}
                selectedTarget={onlineSelected}
                onOpenTargetTool={(target) => {
                  if (target.key !== selected?.key && !selectTarget(target)) return
                  setCatalogContext(null)
                  openView("console")
                  selectTab(catalogKind)
                }}
                onOperate={(row,target)=>{
                  if (!selectTarget(target)) return
                  const id=randomSession();setSessionId(id);setAssetOrigin(null);setCatalogContext({row,targetKey:target.key,sessionId:id,scope:consoleScopeKey(session)})
                  openView("console");selectTab(row.kind)
                }}
              /><LegacyAssetNotes key={catalogKind} kind={catalogKind}/></>
      ) : view === "assets" ? (
        <><AssetInventory
          catalog={{ targets: registered, error: directoryError, loading }}
          key={consoleScopeKey(session)}
          page={assetPage}
          onPage={openAssetPage}
          session={session}
          targets={targets}
          selectedTarget={onlineSelected}
          onOpenDirectK8s={(target) => {
            if (target.key !== selected?.key && !selectTarget(target)) return
            setAssetNamespace("")
            openView("console")
            setAssetOrigin({ page: assetPage, targetKey: target.key, scope: consoleScopeKey(session) })
            selectTab("k8s")
          }}
          onOpenTarget={(target, tab, namespace, origin) => {
            if (target.key !== selected?.key && !selectTarget(target)) return
            setAssetNamespace(namespace ?? "")
            openView("console")
            setAssetOrigin({ page: origin, targetKey: target.key, scope: consoleScopeKey(session) })
            selectTab(tab)
          }}
        />{!session.demo && (assetPage.section==="clusters" || assetPage.section==="machines") && <LegacyAssetNotes key={assetPage.section} kind={assetPage.section==="clusters"?"k8s":"vm"}/>}</>
      ) : view === "deploy" ? (
        <DeployView section={deploySection} onSection={setDeploySection} session={session} target={onlineSelected} sessionId={sessionId} onEnterConsole={() => { openView("console"); refresh("user") }}
          onOpenCredentials={openCredentials} />
      ) : (
      <div className={tab === "ask" && onlineSelected ? "flex shrink-0" : "flex min-h-0 flex-1"}>
        <main className="flex min-w-0 flex-1 flex-col">

          {!selected ? (
            <AgentDirectory targets={directory} isAdmin={isAdmin} error={directoryError} loading={loading} onRefresh={() => refresh("user")} onDeploy={openAgentSetup} onEnter={chooseWorkspaceTarget} />
          ) : tab === "overview" ? (
            <div className="min-h-0 flex-1 space-y-4 overflow-auto p-4">
              <CurrentMachineSection selected={selected} />
              <NodeInfoCard
                key={`node-info-${selected.key}`}
                session={session}
                target={selected}
                sessionId={sessionId}
                online={Boolean(onlineSelected)}
                onRunningChange={setSkillRunning}
              />
            </div>
          ) : tab === "assets" || tab === "monitor" ? (
            <section className="min-h-0 flex-1 overflow-auto p-4" key={selected.key + tab}>
              {session.demo ? <p>连接 Gateway 后可读取此 Agent 的资产与指标。</p> : tab === "assets" ? <AgentAssets target={selected}/> : <AgentMonitor session={session} target={selected}/>}
            </section>
          ) : !onlineSelected && tab !== "audit" && tab !== "credentials" && tab !== "tasks" ? (
            <section className="min-h-0 flex-1 overflow-auto p-4">
              <div className="card-surface space-y-3 p-5">
                <h2 className="font-semibold">{targetTabLabel(tab)}</h2>
                <p role="status" className="text-sm text-muted-foreground">{selected.key} 当前离线。Agent 重新连接后即可使用此工具。</p>
                <Button size="sm" disabled={loading} onClick={() => refresh("user")}>刷新连接状态</Button>
              </div>
            </section>
          ) : tab === "ask" ? null : (
            <>
              {/* files / ask / kb 面板自带头部，避免重复占一行 */}
              {tab !== "files" && tab !== "kb" && (
                <div className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-b bg-card px-4 py-1.5">
                  <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    session
                    <SessionInput value={sessionId} disabled={skillRunning} onCommit={changeSession} className="w-44 font-mono" />
                  </label>
                </div>
              )}

              <div className="min-h-0 flex-1">
                {tab === "database" && (
                  <DatabasePanel
                    key={`database-${selected.key}-${sessionId}`} initialHint={resource ? resourceAddress(resource) : undefined}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "website" && (
                  <WebsitePanel
                    key={`website-${selected.key}-${sessionId}`} initialDomain={resource?.details.domain}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "k8s" && (
                  <K8sPanel
                    key={`k8s-${selected.key}-${sessionId}`}
                    initialNamespace={assetNamespace}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "image" && (
                  <ImagePanel
                    key={`image-${selected.key}-${sessionId}`} initialImage={resource ? resourceAddress(resource) : undefined}
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
                {tab === "tasks" && (
                  <BackgroundTasksPanel
                    key={`tasks-${selected.key}`}
                    session={session}
                    target={selected}
                  />
                )}
                {tab === "files" && (
                  <FilesPanel
                    key={`files-${selected.key}-${sessionId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onSessionChange={changeSession}
                    onRunningChange={setSkillRunning}
                  />
                )}
                {tab === "kb" && (
                  <KnowledgeBase
                    key={`kb-${selected.key}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    onRunningChange={setSkillRunning}
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
                    key={`credentials-${consoleScopeKey(session)}-${selected.key}`}
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
        <div hidden={view !== "console" || tab !== "ask" || !onlineSelected} className="min-h-0 flex-1">
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
      </div>
    </div>
  )
}