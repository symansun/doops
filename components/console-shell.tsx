"use client"

import Link from "next/link"
import { Maximize2, Menu, PanelRightClose } from "lucide-react"
import dynamic from "next/dynamic"
import type { AgentSessionRuntime, AgentSessionSnapshot, ComposerPrefill } from "@agent-cloud/agent-ui"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { clearServerSession, fetchTargets, restoreServerSession, sessionIsAdmin, type Session, type Target } from "@/lib/client"
import { isLostGatewaySession } from "@/lib/gateway-auth"
import { randomSession, resolveDefaultGateway, targetClusterAvailable } from "@/lib/gateway"
import { isDemoAllowed } from "@/lib/demo-policy"
import { DEMO_TOKEN } from "@/lib/demo"
import { errorAfterRefresh, reconcileSelected, reconcileTargets, workspaceTargets, type TargetRef, type RefreshTrigger } from "@/lib/console-refresh"
import { assetRequest, setDemoAssetsActive } from "@/lib/assets"
import { displayUserName } from "@/lib/session-display"
import { NETWORK_FAILURE_MESSAGE, subscribeNetworkStatus } from "@/lib/network-error"
import { CONSOLE_HOME, assetKinds, credentialsDestination, targetTabLabel, type AdminTab, type AgentOverviewSubTab, type AutomationSubTab, type CatalogKind, type AssetArea, type ConsoleNavEntry, type ConsoleView, type DeploySection, type PluginCenterSubTab, type ResourceMonitorSubTab, type TargetTab as Tab } from "@/lib/console-views"
import { buildAdminRedirectUrl, buildConsoleSearch, parseConsoleSearch } from "@/lib/console-url"
import {
  clearCachedPluginState,
  fetchPluginState,
  readCachedPluginState,
  writeCachedPluginState,
} from "@/lib/plugins/client"
import {
  buildDefaultPluginState,
  canNavigateDestination,
  findPluginView,
  isPluginEnabled,
  pluginOwningRoute,
} from "@/lib/plugins/resolver"
import type { PluginStateResponse } from "@/lib/plugins/types"
import { PluginDisabledState } from "./plugins/plugin-disabled-state"
import { PluginErrorBoundary } from "./plugins/plugin-error-boundary"
import { isPluginCenterArea } from "./plugins/registry"
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
import { AgentTerminalPane, type AgentTerminalTab } from "./agent-terminal-pane"
import { navigationBlocker, nextTerminalPrefill, prefillToApply, targetSwitchBlocker, TERMINAL_RUNNING_MESSAGE, type TerminalPrefill } from "@/lib/shell-blocks"
import { dispatchTerminalAiRequest, dispatchTerminalPromptToAi, type TerminalAiActionMode, type TerminalModeKind } from "@/lib/terminal-ai"
import { createDoopsAgentUiRuntime, isDoopsAgentSessionBusy } from "@/lib/agent-ui-runtime"
import { extractAgentExecutionEntries, type AgentTimelineToolItemLike } from "@/lib/agent-execution-stream"
import { agentBusyView } from "@/lib/agent-busy-banner"
import { createDoopsCodeWorkspace } from "@/lib/agent-code-workspace"
import type { CredentialType } from "@/lib/credentials"
import { KnowledgeBase } from "./knowledge-base"
import { AuditPanel } from "./audit-panel"
import { CredentialPanel } from "./credential-panel"
import { ResourceCatalogPanel, resourceAddress, type CatalogRow } from "./resource-catalog-panel"
import { LegacyAssetNotes } from "./legacy-asset-notes"
import { AssetInventory, assetReturnLabel, type AssetPage } from "./asset-inventory"
import { AgentDirectory } from "./agent-asset-scope"
import { AgentOverviewPanel } from "./agent-overview-panel"
import { DashboardPanel } from "./dashboard-panel"
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

const pluginPanelLoading = (label: string) => () => (
  <div role="status" className="flex min-h-[12rem] items-center justify-center p-6 text-sm text-muted-foreground">
    正在加载{label}…
  </div>
)

// PLG-01 Q2: L1 built-in plugin views are code-split via next/dynamic so
// disabled plugins never download their panel chunks on initial /console load.
const AutomationPanel = dynamic(() => import("./automation-panel").then((m) => m.AutomationPanel), { loading: pluginPanelLoading("自动化工作台") })
const FilesPanel = dynamic(() => import("./files-panel").then((m) => m.FilesPanel), { loading: pluginPanelLoading("部署包管理") })
const DatabasePanel = dynamic(() => import("./database-panel").then((m) => m.DatabasePanel), { loading: pluginPanelLoading("数据库工作台") })
const WebsitePanel = dynamic(() => import("./website-panel").then((m) => m.WebsitePanel), { loading: pluginPanelLoading("网站工作台") })
const K8sPanel = dynamic(() => import("./k8s-panel").then((m) => m.K8sPanel), { loading: pluginPanelLoading("Kubernetes 面板") })
const ImagePanel = dynamic(() => import("./image-panel").then((m) => m.ImagePanel), { loading: pluginPanelLoading("镜像工作台") })
const AgentMonitor = dynamic(() => import("./asset-monitor-panel").then((m) => m.AgentMonitor), { loading: pluginPanelLoading("资源监控") })
const SkillPluginHost = dynamic(() => import("./plugins/skill-plugin-host").then((m) => m.SkillPluginHost), { loading: pluginPanelLoading("声明式插件工作台") })


const GW_KEY = "doops.gateway"
const LEGACY_DEMO_KEY = "doops.demo"
const ONBOARD_KEY = "doops.onboarded"
const SPLIT_RATIO_KEY = "doops.console.splitRightPct"
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
  setDemoAssetsActive(Boolean(session?.demo))
  useEffect(() => () => setDemoAssetsActive(false), [])
  const [defaultGateway, setDefaultGateway] = useState("")
  const [targets, setTargets] = useState<Target[]>([])
  const [registered, setRegistered] = useState<TargetRef[]>([])
  const [directoryError, setDirectoryError] = useState("")
  const directory = useMemo(() => workspaceTargets(targets, registered), [targets, registered])
  const latestRefresh = useRef(0)
  const refreshing = useRef(false)
  const [selected, setSelected] = useState<Target | null>(null)
  const onlineSelected = selected?.status === "offline" ? null : selected
  const clusterAvailable = targetClusterAvailable(selected)
  const [sessionId, setSessionId] = useState(randomSession())
  const [tab, setTab] = useState<Tab>("overview")
  const [overviewSub, setOverviewSub] = useState<AgentOverviewSubTab>("summary")
  const [tasksSub, setTasksSub] = useState<AutomationSubTab>("schedules")
  const [tasksRunId, setTasksRunId] = useState("")
  const [adminTab, setAdminTab] = useState<AdminTab | undefined>(undefined)
  const [terminalPrefill, setTerminalPrefill] = useState<TerminalPrefill | null>(null)
  // 终端标签按需挂载；记录已消费的 nonce，避免重新进入终端时再次预填旧命令。
  const [consumedPrefillNonce, setConsumedPrefillNonce] = useState(0)
  const [terminalAiOpen, setTerminalAiOpen] = useState(false)
  const [askTerminalOpen, setAskTerminalOpen] = useState(false)
  const [askTerminalTab, setAskTerminalTab] = useState<AgentTerminalTab>("log")
  const [askFocusedToolId, setAskFocusedToolId] = useState<string | undefined>(undefined)
  const [splitRightPct, setSplitRightPct] = useState(45)
  const splitContainerRef = useRef<HTMLDivElement | null>(null)
  const [aiComposerPrefill, setAiComposerPrefill] = useState<ComposerPrefill | undefined>(undefined)
  const [terminalAiNotice, setTerminalAiNotice] = useState("")
  const terminalRunning = useRef(false)
  const [view, setView] = useState<ConsoleView>(CONSOLE_HOME)
  const runInTerminal = useCallback((command: string, options?: { run?: boolean }) => {
    if (terminalRunning.current) { setError(TERMINAL_RUNNING_MESSAGE); return }
    setTerminalPrefill((previous) => nextTerminalPrefill(previous, command, options))
    // 在「AI 助手」页时留在对话左侧，直接展开右侧交互终端，不再强制跳转离开对话上下文。
    if (view === "console" && tab === "ask" && onlineSelected) {
      setAskTerminalOpen(true)
      setAskTerminalTab("pty")
      return
    }
    setTab("terminal")
  }, [onlineSelected, tab, view])
  const toggleAskTerminal = useCallback(() => {
    setAskTerminalOpen((prev) => !prev)
  }, [])
  const focusToolInTerminal = useCallback((toolId: string) => {
    setAskFocusedToolId(toolId)
    setAskTerminalOpen(true)
    setAskTerminalTab("log")
  }, [])
  const startSplitResize = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    const container = splitContainerRef.current
    if (!container) return
    event.preventDefault()
    const targetEl = event.currentTarget
    targetEl.setPointerCapture?.(event.pointerId)
    const onMove = (moveEvent: PointerEvent) => {
      const rect = container.getBoundingClientRect()
      if (rect.width <= 0) return
      const rightPct = ((rect.right - moveEvent.clientX) / rect.width) * 100
      const clamped = Math.round(Math.min(68, Math.max(28, rightPct)))
      setSplitRightPct(clamped)
      writeLocal(SPLIT_RATIO_KEY, String(clamped))
    }
    const onUp = () => {
      window.removeEventListener("pointermove", onMove)
      window.removeEventListener("pointerup", onUp)
      window.removeEventListener("pointercancel", onUp)
    }
    window.addEventListener("pointermove", onMove)
    window.addEventListener("pointerup", onUp)
    window.addEventListener("pointercancel", onUp)
  }, [])
  useEffect(() => {
    const saved = Number(readLocal(SPLIT_RATIO_KEY))
    if (Number.isFinite(saved) && saved >= 28 && saved <= 68) {
      setSplitRightPct(Math.round(saved))
    }
  }, [])
  const pendingHomeTab = useRef<Tab | null>(null)
  const [deploySection, setDeploySection] = useState<DeploySection>("onboard")
  // 没有可用的在线目标时点了资产台账，就在目标选择器上就地说明原因。
  const [needsTarget, setNeedsTarget] = useState(false)
  const [catalogKind, setCatalogKind] = useState<CatalogKind>("database")
  const [activePluginId, setActivePluginId] = useState<string>("")
  const [catalogContext, setCatalogContext] = useState<{row: CatalogRow; targetKey: string; sessionId: string; scope:string} | null>(null)
  const [assetNamespace, setAssetNamespace] = useState("")
  const [auditOperationId, setAuditOperationId] = useState("")
  const [credentialLaunch, setCredentialLaunch] = useState<{
    nonce: number
    initialView: "target" | "managed"
    focusType?: CredentialType
    returnTo?: { view: ConsoleView; tab: Tab; label: string; description: string }
  } | null>(null)
  const [agentReconnectNonce, setAgentReconnectNonce] = useState(0)
  const [automationFocusScheduleId, setAutomationFocusScheduleId] = useState("")
  const [assetOrigin, setAssetOrigin] = useState<{ page: AssetPage; targetKey: string; scope: string } | null>(null)
  const [assetPage, setAssetPage] = useState<AssetPage>({ section: "board" })
  const [pluginState, setPluginState] = useState<PluginStateResponse | null>(null)
  const [pluginStatus, setPluginStatus] = useState<"loading" | "ready" | "unsupported" | "error">("loading")
  const [pluginError, setPluginError] = useState("")
  const pluginEtagRef = useRef("")
  const activeMountedPluginRef = useRef<string | null>(null)
  const pluginStateRef = useRef<PluginStateResponse | null>(null)
  pluginStateRef.current = pluginState
  // 平台资产台账 reopens the section the user last read; the header owns the area.
  const lastLedger = useRef<AssetPage>({ section: "machines" })
  const urlInitializedRef = useRef(false)
  const poppingHistoryRef = useRef(false)
  const routeRef = useRef({ view, tab, overviewSub, tasksSub, tasksRunId: tasksRunId || undefined, adminTab, assetPage, deploySection, catalogKind, pluginId: activePluginId || undefined })
  routeRef.current = { view, tab, overviewSub, tasksSub, tasksRunId: tasksRunId || undefined, adminTab, assetPage, deploySection, catalogKind, pluginId: activePluginId || undefined }
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
    if (!agentRuntime || (view === "console" && (tab === "ask" || terminalAiOpen))) {
      setOpenedAgentRuntime(agentRuntime)
    }
  }, [agentRuntime, tab, terminalAiOpen, view])
  useEffect(() => {
    setAiComposerPrefill(undefined)
    setTerminalAiNotice("")
    setAskFocusedToolId(undefined)
  }, [selected?.key, sessionId])
  const handleComposerPrefillApplied = useCallback((id: string) => {
    setAiComposerPrefill((current) => (current?.id === id ? undefined : current))
  }, [])
  const toggleTerminalAi = useCallback(() => {
    setTerminalAiOpen((prev) => {
      const next = !prev
      if (next && agentRuntime) setOpenedAgentRuntime(agentRuntime)
      if (!next) setTerminalAiNotice("")
      return next
    })
  }, [agentRuntime])
  const askTerminalAi = useCallback(
    async ({ prompt, fallbackPrefill }: { prompt: string; fallbackPrefill: string }): Promise<"sent" | "prefilled" | "error"> => {
      if (!onlineSelected || !agentRuntime) {
        setError("当前目标离线，无法发送到 AI 助手")
        return "error"
      }
      setError("")
      setTerminalAiNotice("")
      setOpenedAgentRuntime(agentRuntime)
      if (tab !== "ask") setTerminalAiOpen(true)
      try {
        const result = await dispatchTerminalPromptToAi({
          runtime: agentRuntime,
          sessionId,
          request: {
            id: crypto.randomUUID(),
            prompt,
            fallbackPrefill,
          },
          onPrefill: (payload) => setAiComposerPrefill(payload),
        })
        if (result.outcome === "sent") return "sent"
        if (result.outcome === "prefilled_busy") {
          setTerminalAiNotice(result.notice)
          return "prefilled"
        }
        if (result.outcome === "prefilled_on_error") {
          setTerminalAiNotice(`未能直接发起执行（${result.error}），已将提问内容填入 AI 输入框`)
          return "error"
        }
        return "prefilled"
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : String(err))
        return "error"
      }
    },
    [onlineSelected, agentRuntime, sessionId, tab],
  )

  const sendTerminalSelectionToAi = useCallback(
    ({
      selection,
      mode,
      cwd,
      terminalMode,
    }: {
      selection: string
      mode: TerminalAiActionMode
      cwd?: string
      terminalMode: TerminalModeKind
    }) => {
      if (!onlineSelected || !agentRuntime) {
        setError("当前目标离线，无法发送到 AI 助手")
        return
      }
      setError("")
      setTerminalAiNotice("")
      setOpenedAgentRuntime(agentRuntime)
      if (tab !== "ask") setTerminalAiOpen(true)
      void dispatchTerminalAiRequest({
        runtime: agentRuntime,
        sessionId,
        target: onlineSelected,
        request: {
          id: crypto.randomUUID(),
          mode,
          selection,
          cwd,
          terminalMode,
        },
        onPrefill: (payload) => setAiComposerPrefill(payload),
      }).then((result) => {
        if (result.outcome === "prefilled_busy") {
          setTerminalAiNotice(result.notice)
        } else if (result.outcome === "prefilled_on_error") {
          setTerminalAiNotice(`未能直接发起执行（${result.error}），已将选中内容填入 AI 输入框`)
        }
      }).catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err))
      })
    },
    [onlineSelected, agentRuntime, sessionId, tab],
  )
  // AI 助手面板切到其他标签只是 hidden，回合仍在执行。这里常驻订阅它的快照，
  // 让「停止 AI 任务」在任何标签页可用，而不是要求用户先切回「AI 助手」。
  const [agentSnapshot, setAgentSnapshot] = useState<AgentSessionSnapshot | null>(null)
  useEffect(() => {
    if (!agentRuntime) { setAgentSnapshot(null); return }
    const read = () => {
      try { setAgentSnapshot(agentRuntime.getSnapshot(sessionId)) } catch { setAgentSnapshot(null) }
    }
    read()
    try { return agentRuntime.subscribe(sessionId, read) } catch { return undefined }
  }, [agentRuntime, sessionId])
  const askExecutionEntries = useMemo(
    () => extractAgentExecutionEntries((agentSnapshot?.items ?? []) as readonly AgentTimelineToolItemLike[]),
    [agentSnapshot?.items],
  )
  const handleAskPtyPrefillHandled = useCallback((nonce: number) => {
    setConsumedPrefillNonce((prev) => Math.max(prev, nonce))
  }, [])
  const [stoppingAgent, setStoppingAgent] = useState(false)
  const stopAgentTurn = useCallback(async () => {
    if (!agentRuntime || stoppingAgent) return
    setStoppingAgent(true)
    setError("")
    try {
      await agentRuntime.interrupt(sessionId, crypto.randomUUID())
    } catch (err) {
      setError((err as Error).message || "中断 AI 任务失败")
    } finally {
      setStoppingAgent(false)
    }
  }, [agentRuntime, sessionId, stoppingAgent])

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

  const refreshPlugins = useCallback(async (options: { force?: boolean } = {}) => {
    if (!session) return
    const result = await fetchPluginState(session, {
      etag: options.force ? undefined : (pluginEtagRef.current || undefined),
    })
    if (result.kind === "ok") {
      pluginEtagRef.current = result.etag
      setPluginState(result.state)
      setPluginStatus("ready")
      setPluginError("")
      writeCachedPluginState(browserSessionStorage(), session, result.state, result.etag)
    } else if (result.kind === "not_modified") {
      if (result.etag) pluginEtagRef.current = result.etag
      setPluginStatus((prev) => (prev === "loading" ? "ready" : prev))
      setPluginError("")
    } else if (result.kind === "unsupported") {
      pluginEtagRef.current = ""
      setPluginState(result.state)
      setPluginStatus("unsupported")
      setPluginError("")
    } else if (result.kind === "error") {
      if (!pluginStateRef.current) {
        setPluginState(buildDefaultPluginState({ supported: true }))
      }
      setPluginStatus("error")
      setPluginError(`插件配置未能刷新：${result.message}`)
    }
  }, [session])

  useEffect(() => {
    if (!session) return
    const cached = readCachedPluginState(browserSessionStorage(), session)
    if (cached) {
      pluginEtagRef.current = cached.etag
      setPluginState(cached.state)
      setPluginStatus(cached.state.supported ? "ready" : "unsupported")
    } else {
      pluginEtagRef.current = ""
      setPluginState(session.demo ? buildDefaultPluginState({ supported: true }) : null)
      setPluginStatus(session.demo ? "ready" : "loading")
    }
    setPluginError("")
    void refreshPlugins()
    const onFocus = () => { void refreshPlugins() }
    window.addEventListener("focus", onFocus)
    const poll = setInterval(() => { void refreshPlugins() }, 60_000)
    return () => {
      window.removeEventListener("focus", onFocus)
      clearInterval(poll)
    }
  }, [session, refreshPlugins])

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
      setOverviewSub(initial.overviewSub ?? (initial.tab === "assets" ? "manifest" : "summary"))
      setTasksSub(initial.tasksSub ?? (initial.tasksRunId ? "runs" : "schedules"))
      setTasksRunId(initial.tasksRunId ?? "")
      setAdminTab(initial.adminTab)
      setAssetPage(initial.assetPage)
      setDeploySection(initial.deploySection)
      setCatalogKind(initial.catalogKind)
      setActivePluginId(initial.pluginId ?? "")
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
      overviewSub,
      tasksSub,
      tasksRunId: tasksRunId || undefined,
      adminTab,
      assetPage,
      deploySection,
      catalogKind,
      pluginId: activePluginId || undefined,
    })
    if (nextSearch !== window.location.search) {
      window.history.pushState(null, "", `${window.location.pathname}${nextSearch}`)
    }
  }, [session, view, tab, overviewSub, tasksSub, tasksRunId, adminTab, assetPage, deploySection, catalogKind, activePluginId])

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
      setOverviewSub(parsed.overviewSub ?? (parsed.tab === "assets" ? "manifest" : "summary"))
      setTasksSub(parsed.tasksSub ?? (parsed.tasksRunId ? "runs" : "schedules"))
      setTasksRunId(parsed.tasksRunId ?? "")
      setAdminTab(parsed.adminTab)
      setAssetPage(parsed.assetPage)
      setDeploySection(parsed.deploySection)
      setCatalogKind(parsed.catalogKind)
      setActivePluginId(parsed.pluginId ?? "")
    }
    window.addEventListener("popstate", onPopState)
    return () => window.removeEventListener("popstate", onPopState)
  }, [session])

  // Deep links like /console?view=admin(&tab=users) route platform admins directly
  // to /console/admin (using replace to avoid trapping browser Back in a redirect loop)
  // while non-admins see an explicit permission banner instead of silently landing on Home.
  useEffect(() => {
    if (!session || view !== "admin" || typeof window === "undefined") return
    if (!sessionIsAdmin(session)) return
    const blocker = navigationBlocker({ terminalRunning: terminalRunning.current, ...navLock.current })
    if (blocker) {
      navLockMessage.current = blocker
      setError(blocker)
      return
    }
    window.location.replace(buildAdminRedirectUrl(window.location.search, adminTab))
  }, [session, view, adminTab])

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
        if (current) {
          clearConsoleState(browserSessionStorage(), current)
          clearCachedPluginState(browserSessionStorage(), current)
        }
        pluginEtagRef.current = ""
        activeMountedPluginRef.current = null
        setPluginState(null)
        setPluginStatus("loading")
        setPluginError("")
        setActivePluginId("")
        urlInitializedRef.current = false
        restoredScope.current = ""
        setRestoreNotice("")
        setInterruptedNotices([])
        setSession(null)
        setTargets([])
        setSelected(null)
        setRegistered([])
        setDirectoryError("")
        setCredentialLaunch(null)
        setDisconnecting(false)
      }
    })()
  }

  function openAssetPage(next: AssetPage) {
    if (
      next.section !== "plugins" &&
      next.section !== "board" &&
      next.section !== "situation" &&
      next.section !== "monitor" &&
      next.section !== "inspection" &&
      next.section !== "skills" &&
      next.section !== "mcp"
    ) {
      lastLedger.current = next
    }
    setAssetPage(next)
  }

  // 仅真正依赖在线目标执行的发布动作（工作负载发布 / 升级 Agent）拦截无在线目标导航；
  // 平台级台账、态势感知、资源监控、智能巡检、Agent 运行监控与插件中心均读取 Gateway 持久化状态，无在线 Agent 也可进入。
  function blockMissingOnlineTarget(destination: ConsoleNavEntry["destination"]): boolean {
    if (
      destination.view === "deploy" &&
      (destination.section === "workload" || destination.section === "upgrade") &&
      !onlineSelected
    ) {
      setNeedsTarget(true)
      return true
    }
    return false
  }

  // 态势感知、资源监控、智能巡检、插件中心（整合 Skill/MCP）、Agent 运行监控与台账各自独立；台账记忆上次打开的分节。
  function openAssetArea(
    area: AssetArea,
    opts?: { sub?: PluginCenterSubTab; monitorSub?: ResourceMonitorSubTab; targetKey?: string; preserveOrigin?: boolean },
  ): void {
    if (view !== "assets" && blockTerminalSwitch()) return
    setError("")
    setNeedsTarget(false)
    if (!opts?.preserveOrigin) setAssetOrigin(null)
    setView("assets")
    if (isPluginCenterArea(area)) {
      if (area === "plugins") return openAssetPage({ section: "plugins", sub: opts?.sub ?? "scenarios" })
      if (area === "skills") return openAssetPage({ section: "skills" })
      return openAssetPage({ section: "mcp" })
    }
    if (area === "situation") return openAssetPage({ section: "situation" })
    if (area === "monitor") {
      return openAssetPage({
        section: "monitor",
        sub: opts?.monitorSub ?? (opts?.targetKey ? "target" : "fleet"),
        ...(opts?.targetKey ? { targetKey: opts.targetKey } : {}),
      })
    }
    if (area === "inspection") return openAssetPage({ section: "inspection" })
    if (area === "machines") return openAssetPage({ section: "machines" })
    if (area === "board") return openAssetPage({ section: "board" })
    if (lastLedger.current.section === area) return openAssetPage(lastLedger.current)
    if (area === "clusters") return openAssetPage({ section: "clusters", query: "", expanded: [] })
    return openAssetPage({ section: "systems" })
  }

  function navigateDestination(destination: ConsoleNavEntry["destination"]) {
    const check = canNavigateDestination(destination, pluginState, {
      kubernetesAvailable: selected?.kubernetes_available,
      isAdmin: session ? sessionIsAdmin(session) : false,
      targetMode: selected?.mode,
    })
    if (!check.ok) {
      setError(check.reason)
      return
    }
    if (destination.view === CONSOLE_HOME) return openView(CONSOLE_HOME)
    if (blockMissingOnlineTarget(destination)) return
    if (destination.view === "assets") {
      openAssetArea(destination.area, {
        sub: destination.sub,
        monitorSub: destination.monitorSub,
        targetKey: destination.targetKey,
      })
      return
    }
    if (destination.view === "deploy") {
      openView("deploy")
      return setDeploySection(destination.section)
    }
    if (destination.view === "admin") {
      if (session && sessionIsAdmin(session) && !blockTerminalSwitch()) {
        window.location.assign(buildAdminRedirectUrl(window.location.search))
      }
      return
    }
    if (destination.view === "catalog") {
      if (blockTerminalSwitch()) return
      setCatalogKind(destination.kind)
      return openView("catalog")
    }
    if (destination.view === "plugin") {
      if (view !== "plugin" && blockTerminalSwitch()) return
      setError("")
      setNeedsTarget(false)
      setActivePluginId(destination.pluginId)
      setView("plugin")
      return
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

  function navigateEntry(entry: ConsoleNavEntry) {
    navigateDestination(entry.destination)
  }

  // Switching views clears the operation error, like a refresh the user asked for.
  function openView(next: typeof view) {
    if (next !== view && blockTerminalSwitch()) return
    if (next !== view) {
      pendingHomeTab.current = null
      if (next !== "admin") setAdminTab(undefined)
      if (next !== "console") setCredentialLaunch(null)
      if (next === "assets") {
        openAssetPage(session && assetOrigin?.scope === consoleScopeKey(session) && assetOrigin.targetKey === selected?.key ? assetOrigin.page : { section: "board" })
      }
      setAssetOrigin(null)
    }
    if (next === "deploy" && view !== "deploy") {
      const canWorkload = Boolean(onlineSelected) && canNavigateDestination(
        { view: "deploy", section: "workload" },
        pluginState,
        { kubernetesAvailable: selected?.kubernetes_available },
      ).ok
      setDeploySection(canWorkload ? "workload" : "onboard")
    }
    setError("")
    setNeedsTarget(false)
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
    if (next !== "audit") setAuditOperationId("")
    if (next !== "credentials") setCredentialLaunch(null)
    if (next === "assets") setOverviewSub("manifest")
    else if (next === "overview" && tab !== "overview") setOverviewSub("summary")
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
    if (selected?.key !== t.key) {
      setAssetOrigin(null)
      setTerminalAiOpen(false)
      setCredentialLaunch(null)
      setAskTerminalOpen(false)
      setAskFocusedToolId(undefined)
    }
    setError("")
    setRestoreNotice("")
    setInterruptedNotices([])
    setSelected(t)
    setAssetNamespace("")
    setAuditOperationId("")
    setSessionId(randomSession())
    // A terminal is scoped to one target and session. Review the new target before opening its terminal.
    setTab((cur) => cur === "terminal" ? "overview" : cur)
    setNeedsTarget(false)
    return true
  }

  function openHomeTool(next: Tab) {
    if (blockTerminalSwitch()) return
    openView("console")
    selectTab(next)
    if (!selected) pendingHomeTab.current = next
  }

  function openAskWithPrompt(prompt: string) {
    if (!onlineSelected || !agentRuntime) {
      setError("请先在顶部选择一台在线 Agent，再使用 AI 助手执行分析")
      return
    }
    setError("")
    setOpenedAgentRuntime(agentRuntime)
    setAiComposerPrefill({
      id: crypto.randomUUID(),
      sessionId,
      text: prompt,
      mode: "append",
    })
    openView("console")
    selectTab("ask")
  }

  function openTerminalWithCommand(command?: string, options?: { openAiSplit?: boolean; aiPrompt?: string }) {
    if (!selected) {
      setError("请先选择目标节点，再打开交互终端")
      return
    }
    setError("")
    if (command && command.trim()) {
      setTerminalPrefill((cur) => nextTerminalPrefill(cur, command))
    }
    if (options?.openAiSplit && onlineSelected && agentRuntime) {
      setOpenedAgentRuntime(agentRuntime)
      setTerminalAiOpen(true)
      if (options.aiPrompt && options.aiPrompt.trim()) {
        setAiComposerPrefill({
          id: crypto.randomUUID(),
          sessionId,
          text: options.aiPrompt.trim(),
          mode: "append",
        })
      }
    }
    openView("console")
    selectTab("terminal")
  }

  function chooseWorkspaceTarget(target: Target) {
    if (view === "assets" && assetPage.section === "monitor") {
      if (!selectTarget(target)) return
      setAssetPage({
        section: "monitor",
        sub: "target",
        targetKey: target.key,
        ...(assetPage.clusterId ? { clusterId: assetPage.clusterId } : {}),
      })
      return
    }
    if (!selectTarget(target)) return
    if (target.status === "offline") {
      openView("console")
      setTab("overview")
      pendingHomeTab.current = null
    } else if (!selected && view === "console") {
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
  // where a modelProvider credential is bound. Only admins get the model-configuration shortcut.
  const credentials = credentialsDestination(isAdmin)
  const openCredentials = credentials
    ? () => {
        if (blockTerminalSwitch()) return
        openHomeTool(credentials.tab)
        setCredentialLaunch({
          nonce: Date.now(),
          initialView: "managed",
          focusType: "modelProvider",
          returnTo: {
            view: "deploy",
            tab,
            label: "返回发布工作负载",
            description: "完成模型或仓库凭据绑定并激活后，可返回发布页面继续操作。",
          },
        })
      }
    : undefined

  // K25：刷新后的「结果未知」提示固定在 shell 顶层渲染，不依赖终端标签页是否打开，
  // 也不依赖当次 /api/targets 是否返回目标；记录留在状态里，直到用户关闭横幅。
  const interruptedBanner = interruptedBannerView(interruptedNotices)
  const terminalSplitAi = view === "console" && tab === "terminal" && terminalAiOpen && Boolean(onlineSelected)
  const openCredentialsFromAssistant = credentials
    ? (options?: { focusType?: CredentialType }) => {
        if (blockTerminalSwitch()) return
        const fromTab: Tab = terminalSplitAi ? "terminal" : "ask"
        openHomeTool(credentials.tab)
        setCredentialLaunch({
          nonce: Date.now(),
          initialView: "managed",
          focusType: options?.focusType,
          returnTo: {
            view: "console",
            tab: fromTab,
            label: fromTab === "terminal" ? "返回终端 AI 协同" : "返回 AI 助手",
            description: options?.focusType === "modelProvider"
              ? "正在为当前目标配置 AI 模型服务（modelProvider）：按顺序完成写入内容、绑定目标、验证暂存并激活，返回后 AI 助手将自动重连。"
              : "正在管理当前目标的凭据，完成后可一键返回 AI 助手。",
          },
        })
      }
    : undefined
  const openTargetCredentialsFromAssistant = () => {
    if (blockTerminalSwitch()) return
    const fromTab: Tab = terminalSplitAi ? "terminal" : "ask"
    openHomeTool("credentials")
    setCredentialLaunch({
      nonce: Date.now(),
      initialView: "target",
      returnTo: {
        view: "console",
        tab: fromTab,
        label: fromTab === "terminal" ? "返回终端 AI 协同" : "返回 AI 助手",
        description: "当前展示目标可用凭据与个人凭据，完成后可一键返回 AI 助手。",
      },
    })
  }
  const askSplitTerminal = view === "console" && tab === "ask" && askTerminalOpen && Boolean(onlineSelected)
  const showAgentPanel = view === "console" && (tab === "ask" || (tab === "terminal" && terminalAiOpen)) && Boolean(onlineSelected)
  const agentBusy = agentBusyView(agentSnapshot, tab, showAgentPanel)

  const resource = catalogContext && catalogContext.targetKey === selected?.key && catalogContext.sessionId === sessionId && catalogContext.row.kind === tab && catalogContext.scope === consoleScopeKey(session) ? catalogContext.row : null
  const fromAsset =
    assetOrigin?.scope === consoleScopeKey(session) &&
    ((view === "console" && assetOrigin.targetKey === selected?.key && targets.some(t => t.key === selected.key && t.status !== "offline")) ||
      (view === "assets" && assetPage.section === "monitor" && assetOrigin.page.section !== "monitor"))

  const currentOwningPlugin = pluginOwningRoute(
    { view, tab, assetPage, deploySection, catalogKind, pluginId: activePluginId || undefined },
    pluginState,
  )
  const pluginsReady = pluginStatus !== "loading"
  const currentPluginEnabled = !currentOwningPlugin || !pluginState || isPluginEnabled(pluginState, currentOwningPlugin.id)
  if (!currentOwningPlugin) {
    activeMountedPluginRef.current = null
  } else if (pluginsReady && currentPluginEnabled) {
    activeMountedPluginRef.current = currentOwningPlugin.id
  } else if (activeMountedPluginRef.current && activeMountedPluginRef.current !== currentOwningPlugin.id) {
    activeMountedPluginRef.current = null
  }
  const pluginBootLoading = Boolean(currentOwningPlugin) && !pluginsReady
  const pluginDisabledBlocked = Boolean(currentOwningPlugin) && pluginsReady && !currentPluginEnabled && activeMountedPluginRef.current !== currentOwningPlugin.id
  const pluginDisabledInPage = Boolean(currentOwningPlugin) && pluginsReady && !currentPluginEnabled && activeMountedPluginRef.current === currentOwningPlugin.id

  return (
    <div className="console-shell flex h-dvh w-full flex-col">
      <header className="relative z-30 flex shrink-0 flex-wrap items-center gap-2 border-b border-border/50 bg-card/80 px-4 py-2.5 backdrop-blur">
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
            needsTarget={needsTarget}
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
        pluginId={activePluginId || undefined}
        isAdmin={isAdmin}
        hasTarget={Boolean(onlineSelected)}
        kubernetesAvailable={clusterAvailable}
        pluginState={pluginState}
        pluginsReady={pluginsReady}
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
      {view === "console" && onlineSelected && agentRuntime && agentBusy && (
        <StatusBanner
          tone="warning"
          data-testid="agent-busy-banner"
          className="flex shrink-0 flex-wrap items-center gap-2 rounded-none border-x-0 border-t-0 px-4"
        >
          <span className="min-w-0 flex-1">{agentBusy.text}</span>
          <Button size="sm" variant="ghost" onClick={() => setTab("ask")} className="shrink-0">
            {agentBusy.openLabel}
          </Button>
          <Button
            size="sm"
            variant="danger-outline"
            disabled={stoppingAgent}
            onClick={() => { void stopAgentTurn() }}
            className="shrink-0"
          >
            {stoppingAgent ? "停止中…" : agentBusy.stopLabel}
          </Button>
        </StatusBanner>
      )}
      {(error || targetsError) && (
        <StatusBanner tone="error" className="shrink-0 rounded-none border-x-0 border-t-0 px-4">
          {[error, targetsError].filter(Boolean).join("；")}
        </StatusBanner>
      )}
      {isAdmin && pluginState && !pluginState.supported && (
        <StatusBanner tone="warning" data-testid="plugin-unsupported-banner" className="shrink-0 rounded-none border-x-0 border-t-0 px-4">
          当前 Gateway 版本不支持插件与场景配置（<code>/v1/plugins</code> 返回 404），控制台已按「全功能模式」兼容展示。
        </StatusBanner>
      )}
      {pluginError && (
        <StatusBanner tone="warning" data-testid="plugin-error-banner" className="flex shrink-0 items-center justify-between gap-2 rounded-none border-x-0 border-t-0 px-4">
          <span>{pluginError}</span>
          <Button size="sm" variant="ghost" onClick={() => void refreshPlugins({ force: true })}>重试</Button>
        </StatusBanner>
      )}
      {pluginDisabledInPage && currentOwningPlugin && (
        <StatusBanner tone="warning" data-testid="plugin-inpage-disabled-banner" className="flex shrink-0 items-center justify-between gap-2 rounded-none border-x-0 border-t-0 px-4">
          <span>「{currentOwningPlugin.name}」插件已被管理员停用，当前页面仍保留以便完成收尾，离开后将无法重新进入。</span>
          <Button size="sm" variant="ghost" onClick={() => openView(CONSOLE_HOME)}>返回首页</Button>
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
      {fromAsset && assetOrigin && (
        <section aria-label="目标上下文" className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-card px-4 py-2">
          <Button
            size="sm"
            variant="ghost"
            disabled={skillRunning}
            onClick={() => {
              const originPage = assetOrigin.page
              setAssetOrigin(null)
              setView("assets")
              openAssetPage(originPage)
            }}
          >
            {assetReturnLabel(assetOrigin.page)}
          </Button>
        </section>
      )}
      {view === CONSOLE_HOME ? (
        <DashboardPanel session={session} targets={targets} directory={directory} loading={loading}
          onRefresh={() => refresh("user")} onDeployAgent={openAgentSetup}
          pluginState={pluginState}
          onOpenDestination={navigateDestination}
          onOpenTargetTool={(target, targetTab) => {
            const check = canNavigateDestination({ view: "console", tab: targetTab }, pluginState, {
              kubernetesAvailable: target.kubernetes_available,
              isAdmin,
              targetMode: target.mode,
            })
            if (!check.ok) { setError(check.reason); return }
            if (target.key !== selected?.key && !selectTarget(target)) return
            openView("console")
            selectTab(targetTab)
          }}
          onOpenInspectionRun={(runId) => {
            const check = canNavigateDestination({ view: "console", tab: "tasks" }, pluginState)
            if (!check.ok) { setError(check.reason); return }
            setTasksSub("runs")
            setTasksRunId(runId)
            openView("console")
            selectTab("tasks")
          }} />
      ) : pluginBootLoading ? (
        <section role="status" className="flex min-h-0 flex-1 items-center justify-center p-6 text-sm text-muted-foreground">
          正在加载插件配置…
        </section>
      ) : pluginDisabledBlocked && currentOwningPlugin && pluginState ? (
        <PluginDisabledState
          session={session}
          plugin={currentOwningPlugin}
          pluginState={pluginState}
          isAdmin={isAdmin}
          onEnabled={() => { void refreshPlugins({ force: true }) }}
          onOpenPluginCenter={() => openAssetArea("plugins", { sub: "scenarios" })}
          onGoHome={() => openView(CONSOLE_HOME)}
        />
      ) : view === "catalog" ? (
        catalogKind === "credentials" ? <CredentialPanel key={`credentials-${consoleScopeKey(session)}-${selected?.key ?? "none"}`} session={session} target={selected ?? undefined} isAdmin={isAdmin} initialView="managed" />
          : catalogKind === "k8s" ? null : <PluginErrorBoundary pluginId={currentOwningPlugin?.id ?? catalogKind} pluginName={currentOwningPlugin?.name} onGoHome={() => openView(CONSOLE_HOME)}>
              <ResourceCatalogPanel
                key={JSON.stringify([consoleScopeKey(session), catalogKind])}
                kind={catalogKind}
                targets={directory}
                selectedTarget={onlineSelected}
                onOpenTargetTool={(target) => {
                  const check = canNavigateDestination({ view: "console", tab: catalogKind }, pluginState, { kubernetesAvailable: target.kubernetes_available })
                  if (!check.ok) { setError(check.reason); return }
                  if (target.key !== selected?.key && !selectTarget(target)) return
                  setCatalogContext(null)
                  openView("console")
                  selectTab(catalogKind)
                }}
                onOperate={(row,target)=>{
                  const check = canNavigateDestination({ view: "console", tab: row.kind }, pluginState, { kubernetesAvailable: target.kubernetes_available })
                  if (!check.ok) { setError(check.reason); return }
                  if (!selectTarget(target)) return
                  const id=randomSession();setSessionId(id);setAssetOrigin(null);setCatalogContext({row,targetKey:target.key,sessionId:id,scope:consoleScopeKey(session)})
                  openView("console");selectTab(row.kind)
                }}
              />{!session.demo && <LegacyAssetNotes key={catalogKind} kind={catalogKind}/>}</PluginErrorBoundary>
      ) : view === "assets" ? (
        <PluginErrorBoundary pluginId={currentOwningPlugin?.id ?? assetPage.section} pluginName={currentOwningPlugin?.name} onGoHome={() => openView(CONSOLE_HOME)}><AssetInventory
          key={consoleScopeKey(session)}
          page={assetPage}
          onPage={openAssetPage}
          session={session}
          targets={targets}
          directory={directory}
          selectedTarget={selected}
          pluginState={pluginState}
          pluginStatus={pluginStatus}
          pluginError={pluginError}
          onRefreshPlugins={() => refreshPlugins({ force: true })}
          onOpenPlugin={(pluginId) => navigateDestination({ view: "plugin", pluginId })}
          onOpenAutomation={(opts) => {
            const check = canNavigateDestination({ view: "console", tab: "tasks" }, pluginState)
            if (!check.ok) { setError(check.reason); return }
            setAutomationFocusScheduleId(opts?.scheduleId ?? "")
            setTasksSub(opts?.sub ?? (opts?.runId ? "runs" : "schedules"))
            setTasksRunId(opts?.runId ?? "")
            openView("console")
            selectTab("tasks")
          }}
          onOpenDirectK8s={(target) => {
            const check = canNavigateDestination({ view: "console", tab: "k8s" }, pluginState, { kubernetesAvailable: target.kubernetes_available })
            if (!check.ok) { setError(check.reason); return }
            if (target.key !== selected?.key && !selectTarget(target)) return
            setAssetNamespace("")
            openView("console")
            setAssetOrigin({ page: assetPage, targetKey: target.key, scope: consoleScopeKey(session) })
            selectTab("k8s")
          }}
          onOpenResourceMonitor={(origin, targetKey) => {
            const check = canNavigateDestination({ view: "assets", area: "monitor" }, pluginState)
            if (!check.ok) { setError(check.reason); return }
            setAssetOrigin({ page: origin, targetKey: targetKey ?? "", scope: consoleScopeKey(session) })
            openAssetArea("monitor", {
              monitorSub: targetKey ? "target" : "fleet",
              targetKey,
              preserveOrigin: true,
            })
          }}
          onOpenTarget={(target, tab, namespace, origin) => {
            if (tab === "monitor") {
              const check = canNavigateDestination({ view: "assets", area: "monitor" }, pluginState)
              if (!check.ok) { setError(check.reason); return }
              setAssetOrigin({ page: origin, targetKey: target.key, scope: consoleScopeKey(session) })
              openAssetArea("monitor", { monitorSub: "target", targetKey: target.key, preserveOrigin: true })
              return
            }
            const check = canNavigateDestination({ view: "console", tab }, pluginState, { kubernetesAvailable: target.kubernetes_available })
            if (!check.ok) { setError(check.reason); return }
            if (target.key !== selected?.key && !selectTarget(target)) return
            setAssetNamespace(namespace ?? "")
            setAuditOperationId("")
            openView("console")
            setAssetOrigin({ page: origin, targetKey: target.key, scope: consoleScopeKey(session) })
            selectTab(tab)
          }}
          onOpenMonitorTarget={(target, nextTab, origin, operationId) => {
            if (nextTab === "monitor") {
              const check = canNavigateDestination({ view: "assets", area: "monitor" }, pluginState)
              if (!check.ok) { setError(check.reason); return }
              setAssetOrigin({ page: origin, targetKey: target.key, scope: consoleScopeKey(session) })
              openAssetArea("monitor", { monitorSub: "target", targetKey: target.key, preserveOrigin: true })
              return
            }
            const check = canNavigateDestination({ view: "console", tab: nextTab }, pluginState, { kubernetesAvailable: target.kubernetes_available })
            if (!check.ok) { setError(check.reason); return }
            if (target.key !== selected?.key && !selectTarget(target)) return
            setAssetNamespace("")
            openView("console")
            setAssetOrigin({ page: origin, targetKey: target.key, scope: consoleScopeKey(session) })
            selectTab(nextTab)
            if (nextTab === "audit") setAuditOperationId(operationId ?? "")
          }}
          onDeployAgent={openAgentSetup}
          onRunningChange={setSkillRunning}
        />{!session.demo && (assetPage.section==="clusters" || assetPage.section==="machines") && <LegacyAssetNotes key={assetPage.section} kind={assetPage.section==="clusters"?"k8s":"vm"}/>}</PluginErrorBoundary>
      ) : view === "deploy" ? (
        <PluginErrorBoundary pluginId={currentOwningPlugin?.id ?? deploySection} pluginName={currentOwningPlugin?.name} onGoHome={() => openView(CONSOLE_HOME)}>
          <DeployView section={deploySection} onSection={setDeploySection} session={session} target={onlineSelected} kubernetesAvailable={clusterAvailable} sessionId={sessionId} onEnterConsole={() => { openView("console"); refresh("user") }}
            onOpenCredentials={openCredentials} />
        </PluginErrorBoundary>
      ) : view === "plugin" ? (
        (() => {
          const activeL2 = findPluginView(pluginState, activePluginId)
          if (!activeL2) {
            return (
              <section className="min-h-0 flex-1 overflow-auto p-6">
                <StatusBanner tone="warning">
                  未找到指定的场景插件（<code>{activePluginId || "未知"}</code>），请前往插件中心确认该 Skill 是否存在。
                </StatusBanner>
              </section>
            )
          }
          return (
            <PluginErrorBoundary pluginId={activeL2.id} pluginName={activeL2.name} onGoHome={() => openView(CONSOLE_HOME)}>
              <SkillPluginHost
                key={activeL2.id}
                session={session}
                sessionId={sessionId}
                plugin={activeL2}
                targets={directory}
                selectedTarget={onlineSelected}
                onOpenAskWithPrompt={openAskWithPrompt}
                onFillTerminal={(cmd) => openTerminalWithCommand(cmd, { openAiSplit: true })}
                onOpenTerminalSplit={(prompt) => openTerminalWithCommand(undefined, { openAiSplit: true, aiPrompt: prompt })}
                onOpenInspectionRun={(runId) => {
                  const check = canNavigateDestination({ view: "console", tab: "tasks" }, pluginState)
                  if (!check.ok) { setError(check.reason); return }
                  setTasksSub("runs")
                  setTasksRunId(runId)
                  openView("console")
                  selectTab("tasks")
                }}
                onOpenPluginCenter={() => openAssetArea("plugins", { sub: "scenarios" })}
              />
            </PluginErrorBoundary>
          )
        })()
      ) : view === "admin" ? (
        <section aria-label="平台管理" className="min-h-0 flex-1 overflow-auto p-6">
          {sessionIsAdmin(session) ? (
            <div role="status" className="max-w-xl space-y-3 text-sm text-muted-foreground">
              <p>正在前往平台管理…</p>
              <Button
                size="sm"
                onClick={() => {
                  if (blockTerminalSwitch()) return
                  window.location.assign(buildAdminRedirectUrl(typeof window !== "undefined" ? window.location.search : "", adminTab))
                }}
              >
                立即进入平台管理
              </Button>
            </div>
          ) : (
            <div className="max-w-xl space-y-3">
              <StatusBanner tone="warning">
                当前账号不是平台管理员，无法访问「平台管理」页面。
              </StatusBanner>
              <Button size="sm" onClick={() => openView(CONSOLE_HOME)}>返回首页</Button>
            </div>
          )}
        </section>
      ) : null}
      <div
        ref={splitContainerRef}
        hidden={view !== "console" || pluginBootLoading || pluginDisabledBlocked}
        style={{ "--split-right-w": `${splitRightPct}%` } as React.CSSProperties}
        className={view === "console" && !pluginBootLoading && !pluginDisabledBlocked ? "flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row" : "hidden"}
      >
      {view === "console" && !pluginBootLoading && !pluginDisabledBlocked && (
      <div className={tab === "ask" && onlineSelected ? "hidden" : "flex min-h-0 min-w-0 flex-1 flex-col"}>
        <main className="flex min-h-0 min-w-0 flex-1 flex-col">

          {tab === "tasks" ? (
            <section className="min-h-0 flex-1 overflow-auto p-4">
              <AutomationPanel
                session={session}
                targets={directory}
                selectedTarget={selected}
                sessionId={sessionId}
                activeTab={tasksSub}
                initialRunId={tasksRunId || undefined}
                focusedScheduleId={automationFocusScheduleId || undefined}
                onTabChange={(next) => {
                  setTasksSub(next)
                  if (next === "bg") setTasksRunId("")
                }}
                onRunIdChange={(runId) => setTasksRunId(runId)}
                onSelectTarget={(t) => { selectTarget(t) }}
                onOpenSkills={() => openAssetArea("plugins", { sub: "skills" })}
                onExplainTask={(prompt) => {
                  if (!onlineSelected || !agentRuntime) {
                    setError("当前目标离线，无法发送到 AI 助手")
                    return
                  }
                  setError("")
                  setOpenedAgentRuntime(agentRuntime)
                  setAiComposerPrefill({
                    id: crypto.randomUUID(),
                    sessionId,
                    text: prompt,
                    mode: "append",
                  })
                  selectTab("ask")
                }}
              />
            </section>
          ) : !selected ? (
            <AgentDirectory targets={directory} isAdmin={isAdmin} error={directoryError} loading={loading} onRefresh={() => refresh("user")} onDeploy={openAgentSetup} onEnter={chooseWorkspaceTarget} />
          ) : tab === "overview" || tab === "assets" ? (
            <AgentOverviewPanel
              key={selected.key}
              session={session}
              target={selected}
              sessionId={sessionId}
              online={Boolean(onlineSelected)}
              pluginState={pluginState}
              cmdbEnabled={!pluginState || isPluginEnabled(pluginState, "doops.cmdb")}
              monitorEnabled={!pluginState || isPluginEnabled(pluginState, "doops.inspection")}
              subTab={overviewSub}
              onSubTabChange={(next) => {
                if (tab === "assets") setTab("overview")
                setOverviewSub(next)
              }}
              onSelectTab={selectTab}
              onOpenResourceMonitor={() => openAssetArea("monitor", { monitorSub: "target", targetKey: selected.key })}
              onRunningChange={setSkillRunning}
            />
          ) : tab.startsWith("plugin:skill.") ? (
            (() => {
              const targetPluginId = tab.slice("plugin:".length)
              const targetPlugin = findPluginView(pluginState, targetPluginId)
              if (!targetPlugin) {
                return (
                  <section className="min-h-0 flex-1 overflow-auto p-6">
                    <StatusBanner tone="warning">未找到对应的场景插件（{targetPluginId}）。</StatusBanner>
                  </section>
                )
              }
              return (
                <PluginErrorBoundary pluginId={targetPlugin.id} pluginName={targetPlugin.name} onGoHome={() => selectTab("overview")}>
                  <SkillPluginHost
                    key={`${targetPlugin.id}-${selected.key}`}
                    session={session}
                    sessionId={sessionId}
                    plugin={targetPlugin}
                    targets={directory}
                    selectedTarget={onlineSelected}
                    scopedTarget={selected}
                    onOpenAskWithPrompt={openAskWithPrompt}
                    onFillTerminal={(cmd) => openTerminalWithCommand(cmd, { openAiSplit: true })}
                    onOpenTerminalSplit={(prompt) => openTerminalWithCommand(undefined, { openAiSplit: true, aiPrompt: prompt })}
                    onOpenInspectionRun={(runId) => {
                      const check = canNavigateDestination({ view: "console", tab: "tasks" }, pluginState)
                      if (!check.ok) { setError(check.reason); return }
                      setTasksSub("runs")
                      setTasksRunId(runId)
                      openView("console")
                      selectTab("tasks")
                    }}
                    onOpenPluginCenter={() => openAssetArea("plugins", { sub: "scenarios" })}
                  />
                </PluginErrorBoundary>
              )
            })()
          ) : tab === "k8s" && clusterAvailable === false ? (
            <section className="min-h-0 flex-1 space-y-3 overflow-auto p-4">
              <StatusBanner tone="warning">当前目标没有可用的 Kubernetes 集群。</StatusBanner>
              <Button size="sm" onClick={() => selectTab("overview")}>返回 Agent 概览</Button>
            </section>
          ) : tab === "monitor" ? (
            <section className="min-h-0 flex-1 space-y-3 overflow-auto p-4" key={selected.key + tab}>
              <div className="flex flex-wrap items-center justify-between gap-2 border-b pb-2.5">
                <div className="text-sm font-medium text-foreground">
                  {selected.cluster}/{selected.instance} · 单目标时序与告警
                </div>
                <Button size="sm" variant="ghost" onClick={() => openAssetArea("monitor", { monitorSub: "target", targetKey: selected.key })}>
                  在统一资源监控中打开
                </Button>
              </div>
              <AgentMonitor
                session={session}
                target={selected}
                onOpenInspection={() => {
                  const check = canNavigateDestination({ view: "console", tab: "tasks" }, pluginState)
                  if (!check.ok) { setError(check.reason); return }
                  setTasksSub("schedules")
                  setTasksRunId("")
                  openView("console")
                  selectTab("tasks")
                }}
                onOpenSkillCenter={() => openAssetArea("plugins", { sub: "skills" })}
              />
            </section>
          ) : !onlineSelected && tab !== "audit" && tab !== "credentials" && tab !== "terminal" ? (
            <section className="min-h-0 flex-1 overflow-auto p-4">
              <div className="card-surface space-y-3 p-5">
                <h2 className="font-semibold">{targetTabLabel(tab)}</h2>
                <p role="status" className="text-sm text-muted-foreground">{selected.key} 当前离线。Agent 重新连接后即可使用此工具。</p>
                <Button size="sm" disabled={loading} onClick={() => refresh("user")}>刷新连接状态</Button>
              </div>
            </section>
          ) : tab === "ask" ? null : (
            <>
              {/* files / ask / kb / terminal 面板自带头部，避免重复占一行 */}
              {tab !== "files" && tab !== "kb" && tab !== "terminal" && (
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
                    onSessionChange={changeSession}
                    sessionDisabled={skillRunning}
                    prefill={prefillToApply(terminalPrefill, consumedPrefillNonce) === null ? null : terminalPrefill}
                    onPrefillApplied={setConsumedPrefillNonce}
                    onRunningChange={onTerminalRunningChange}
                    onSendToAi={sendTerminalSelectionToAi}
                    onAskAi={askTerminalAi}
                    aiOpen={terminalAiOpen}
                    aiBusy={Boolean(agentSnapshot && isDoopsAgentSessionBusy(agentSnapshot))}
                    aiExecutionEntries={askExecutionEntries}
                    targetOnline={Boolean(onlineSelected)}
                    onToggleAi={toggleTerminalAi}
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
                    key={`audit-${selected.key}-${sessionId}-${auditOperationId}`}
                    session={session}
                    target={selected}
                    sessionId={sessionId}
                    operationId={auditOperationId}
                  />
                )}
                {tab === "credentials" && (
                  <CredentialPanel
                    key={`credentials-${consoleScopeKey(session)}-${selected.key}-${credentialLaunch?.nonce ?? 0}`}
                    session={session}
                    target={selected}
                    isAdmin={isAdmin}
                    initialView={credentialLaunch?.initialView ?? "target"}
                    initialTypeFilter={credentialLaunch?.focusType ?? "all"}
                    returnContext={credentialLaunch?.returnTo ? {
                      label: credentialLaunch.returnTo.label,
                      description: credentialLaunch.returnTo.description,
                      focusType: credentialLaunch.focusType,
                    } : null}
                    onReturn={credentialLaunch?.returnTo ? () => {
                      const targetRoute = credentialLaunch.returnTo!
                      setCredentialLaunch(null)
                      if (targetRoute.view === "deploy") {
                        openView("deploy")
                        setDeploySection("workload")
                      } else {
                        openView("console")
                        selectTab(targetRoute.tab)
                      }
                    } : undefined}
                    onModelCredentialActivated={() => {
                      setAgentReconnectNonce((current) => current + 1)
                    }}
                  />
                )}
              </div>
            </>
          )}
        </main>
      </div>
      )}
      {terminalSplitAi && (
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="调整终端与 AI 协同分栏宽度"
          onPointerDown={startSplitResize}
          className="hidden w-1 shrink-0 cursor-col-resize bg-border/60 transition-colors hover:bg-primary/60 lg:block"
        />
      )}
      {selected && codeWorkspace && agentRuntime && openedAgentRuntime === agentRuntime && (
        <div
          hidden={view !== "console" || (tab !== "ask" && !(tab === "terminal" && terminalAiOpen)) || !onlineSelected}
          data-testid="agent-workspace-pane"
          className={
            terminalSplitAi
              ? "flex h-[52%] min-h-0 w-full shrink-0 flex-col border-t bg-card lg:h-full lg:w-[var(--split-right-w)] lg:min-w-[22rem] lg:border-l-0 lg:border-t-0"
              : "flex min-h-0 min-w-0 flex-1 flex-col"
          }
        >
          {terminalSplitAi && (
            <div className="flex h-9 shrink-0 items-center justify-between gap-2 border-b bg-card px-3 text-xs">
              <div className="flex min-w-0 items-center gap-1.5">
                <SparkIcon width={13} height={13} aria-hidden="true" className="shrink-0 text-primary" />
                <span className="shrink-0 font-medium text-foreground">AI 协同</span>
              </div>
              <div className="flex shrink-0 items-center gap-1">
                <Button
                  aria-label="独立打开 AI 助手"
                  variant="ghost"
                  size="sm"
                  disabled={Boolean(navigationBlocker({ terminalRunning: terminalRunning.current, skillRunning, execRunning }))}
                  onClick={() => selectTab("ask")}
                  title={navigationBlocker({ terminalRunning: terminalRunning.current, skillRunning, execRunning }) ?? "切换到 AI 助手页签"}
                  type="button"
                >
                  <Maximize2 aria-hidden="true" className="size-3.5" />
                  <span>完整视图</span>
                </Button>
                <Button
                  aria-label="收起 AI 协同"
                  variant="ghost"
                  size="sm"
                  onClick={() => setTerminalAiOpen(false)}
                  title="收起 AI 协同侧栏（运行中的任务不会中断）"
                  type="button"
                >
                  <PanelRightClose aria-hidden="true" className="size-3.5" />
                </Button>
              </div>
            </div>
          )}
          {terminalSplitAi && terminalAiNotice && (
            <StatusBanner tone="warning" compact className="flex shrink-0 items-center justify-between gap-2 rounded-none border-x-0 border-t-0 px-3 py-1.5">
              <span className="min-w-0 flex-1 text-xs">{terminalAiNotice}</span>
              <Button size="sm" variant="ghost" onClick={() => setTerminalAiNotice("")}>知道了</Button>
            </StatusBanner>
          )}
          <AgentUiPanel
            key={`ask-${selected.key}-${sessionId}`}
            session={session}
            target={selected}
            onRunInTerminal={runInTerminal}
            runtime={agentRuntime}
            sessionId={sessionId}
            codeWorkspace={codeWorkspace}
            active={showAgentPanel}
            composerPrefill={aiComposerPrefill}
            onComposerPrefillApplied={handleComposerPrefillApplied}
            splitMode={terminalSplitAi}
            terminalOpen={askSplitTerminal}
            onToggleTerminal={toggleAskTerminal}
            onFocusToolInTerminal={focusToolInTerminal}
            onConfigureModel={openCredentialsFromAssistant}
            onOpenCredentials={openTargetCredentialsFromAssistant}
            reconnectNonce={agentReconnectNonce}
            onSessionChange={changeSession}
            onExecRunningChange={setExecRunning}
          />
        </div>
      )}
      {askSplitTerminal && selected && (
        <>
          <div
            role="separator"
            aria-orientation="vertical"
            aria-label="调整 AI 对话与右侧终端分栏宽度"
            onPointerDown={startSplitResize}
            className="hidden w-1 shrink-0 cursor-col-resize bg-border/60 transition-colors hover:bg-primary/60 lg:block"
          />
          <div className="flex h-[48%] min-h-0 w-full shrink-0 flex-col border-t lg:h-full lg:w-[var(--split-right-w)] lg:min-w-[22rem] lg:border-t-0">
            <AgentTerminalPane
              session={session}
              target={selected}
              sessionId={sessionId}
              targetOnline={Boolean(onlineSelected)}
              activeTab={askTerminalTab}
              onTabChange={setAskTerminalTab}
              entries={askExecutionEntries}
              focusedEntryId={askFocusedToolId}
              onFocusEntry={setAskFocusedToolId}
              ptyPrefill={prefillToApply(terminalPrefill, consumedPrefillNonce) === null ? null : terminalPrefill}
              onPtyPrefillHandled={handleAskPtyPrefillHandled}
              onReplayCommand={runInTerminal}
              onSendToAi={sendTerminalSelectionToAi}
              onAskAi={askTerminalAi}
              onClose={() => setAskTerminalOpen(false)}
            />
          </div>
        </>
      )}
      </div>
      </div>
      </div>
    </div>
  )
}
