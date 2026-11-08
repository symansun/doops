"use client"

import { Boxes, Play, RefreshCw, Square } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import type { Session, Target, ToolCancel } from "@/lib/client"
import { TOOLS } from "@/lib/gateway"
import {
  formatReadyList,
  helmReleaseTable,
  hostSections,
  isAbortError,
  k8sTable,
  labelSelectorString,
  parseAlignedColumns,
  parseDatabaseInventory,
  parseJSONDocument,
  parseSqlReport,
  runTargetTool,
  toolBody,
  type DatabaseAsset,
  type SqlTable,
} from "@/lib/workbench"
import { Button, SegmentedControl } from "./ui"

type Scope = "cluster" | "database" | "host"
type KindId =
  | "deployments" | "statefulsets" | "daemonsets" | "jobs" | "cronjobs"
  | "pods" | "services" | "ingresses" | "configmaps"
  | "persistentvolumeclaims" | "persistentvolumes" | "storageclasses"
  | "nodes" | "namespaces" | "helm"

interface KindSpec {
  id: KindId
  label: string
  group: string
  namespaced: boolean
  columns: string[]
  projection: "aligned" | "nodes" | "helm"
  output?: string
  // An Agent-side read with a fixed, values-free output instead of a get projection.
  operation?: "configmap-keys"
}

interface ResourceRow {
  name: string
  namespace: string
  kind: KindId
  cells: string[]
}

const POD_OUTPUT = "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,READY:.status.containerStatuses[*].ready,PHASE:.status.phase,NODE:.spec.nodeName"
// kubectl jsonpath cannot range over map keys, so the selector comes back as JSON.
const SELECTOR_OUTPUT = "jsonpath={.spec.selector}"

const KINDS: KindSpec[] = [
  { id: "deployments", label: "Deployment", group: "工作负载", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "期望", "就绪", "可用"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,DESIRED:.spec.replicas,READY:.status.readyReplicas,AVAILABLE:.status.availableReplicas" },
  { id: "statefulsets", label: "StatefulSet", group: "工作负载", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "期望", "就绪", "可用"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,DESIRED:.spec.replicas,READY:.status.readyReplicas,AVAILABLE:.status.availableReplicas" },
  { id: "daemonsets", label: "DaemonSet", group: "工作负载", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "期望", "就绪", "可用"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,DESIRED:.status.desiredNumberScheduled,READY:.status.numberReady,AVAILABLE:.status.numberAvailable" },
  { id: "jobs", label: "Job", group: "工作负载", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "完成数", "成功数"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,COMPLETIONS:.spec.completions,SUCCEEDED:.status.succeeded" },
  { id: "cronjobs", label: "CronJob", group: "工作负载", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "调度", "是否暂停"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,SCHEDULE:.spec.schedule,SUSPEND:.spec.suspend" },
  { id: "pods", label: "Pod", group: "实例", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "阶段", "就绪", "节点"], output: POD_OUTPUT },
  { id: "services", label: "Service", group: "网络", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "类型", "ClusterIP", "端口"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,TYPE:.spec.type,CLUSTER-IP:.spec.clusterIP,PORTS:.spec.ports[*].port" },
  { id: "ingresses", label: "Ingress", group: "网络", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "类", "主机"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,CLASS:.spec.ingressClassName,HOSTS:.spec.rules[*].host" },
  { id: "configmaps", label: "ConfigMap", group: "配置", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "键名"], operation: "configmap-keys" },
  { id: "persistentvolumeclaims", label: "PVC", group: "存储", namespaced: true, projection: "aligned", columns: ["命名空间", "名称", "状态", "容量", "存储类"], output: "custom-columns=NS:.metadata.namespace,NAME:.metadata.name,STATUS:.status.phase,CAPACITY:.status.capacity.storage,CLASS:.spec.storageClassName" },
  { id: "persistentvolumes", label: "PV", group: "存储", namespaced: false, projection: "aligned", columns: ["名称", "状态", "容量", "绑定的 PVC"], output: "custom-columns=NAME:.metadata.name,STATUS:.status.phase,CAPACITY:.spec.capacity.storage,CLAIM:.spec.claimRef.name" },
  { id: "storageclasses", label: "StorageClass", group: "存储", namespaced: false, projection: "aligned", columns: ["名称", "制备器"], output: "custom-columns=NAME:.metadata.name,PROVISIONER:.provisioner" },
  { id: "nodes", label: "Node", group: "集群", namespaced: false, projection: "nodes", columns: ["名称", "Ready", "角色", "内网 IP", "Kubelet"], output: "json" },
  { id: "namespaces", label: "Namespace", group: "集群", namespaced: false, projection: "aligned", columns: ["名称", "状态"], output: "custom-columns=NAME:.metadata.name,STATUS:.status.phase" },
  { id: "helm", label: "Helm 发布", group: "发布", namespaced: true, projection: "helm", columns: ["命名空间", "发布", "修订", "状态", "Chart"] },
]

const GROUPS = ["工作负载", "实例", "网络", "配置", "存储", "集群", "发布"]
const ENGINES = ["全部", "mysql", "postgres", "mongodb", "mssql"] as const
const CHECKS = [
  { label: "运行状态", command: "uptime" },
  { label: "内存", command: "free -m" },
  { label: "磁盘", command: "df -h" },
  { label: "进程", command: "ps -eo pid,user,pcpu,pmem,comm --sort=-pcpu | head -n 20" },
  { label: "监听端口", command: "ss -lnt" },
]
const ROLLOUT = new Set<KindId>(["deployments", "statefulsets", "daemonsets"])
const LOGGABLE = new Set<KindId>(["pods", "deployments", "statefulsets", "daemonsets", "jobs"])
const SCALABLE = new Set<KindId>(["deployments", "statefulsets"])

function kindSpec(id: KindId): KindSpec {
  return KINDS.find((item) => item.id === id) || KINDS[0]
}

function dash(value: string): string {
  return value.trim() ? value : "—"
}

function parseRows(spec: KindSpec, text: string): ResourceRow[] | null {
  if (spec.projection === "nodes") {
    const parsed = parseJSONDocument(text)
    const items = parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as { items?: unknown }).items : null
    if (!Array.isArray(items)) return null
    if (items.length === 0) return []
    const table = k8sTable(text)
    if (!table) return null
    return table.rows.map((cells) => ({ kind: "nodes", namespace: "", name: cells[0] === "—" ? "" : cells[0], cells }))
  }
  if (spec.projection === "helm") {
    const parsed = parseJSONDocument(text)
    if (!Array.isArray(parsed)) return null
    if (parsed.length === 0) return []
    if (!helmReleaseTable(text)) return null
    const rows: ResourceRow[] = []
    for (const item of parsed) {
      if (!item || typeof item !== "object") return null
      const release = item as { name?: string; namespace?: string; revision?: number; status?: string; chart?: string }
      if (!release.name || !release.chart) return null
      rows.push({
        kind: "helm",
        name: release.name,
        namespace: release.namespace || "",
        cells: [release.namespace || "", release.name, release.revision == null ? "" : String(release.revision), release.status || "", release.chart || ""],
      })
    }
    return rows
  }
  const grid = parseAlignedColumns(text)
  if (!grid) return null
  const nameAt = spec.columns[0] === "命名空间" ? 1 : 0
  const namespaceAt = spec.columns[0] === "命名空间" ? 0 : -1
  return grid.flatMap((cells) => {
    const name = cells[nameAt] || ""
    if (!name) return []
    const shown = spec.id === "pods"
      ? [cells[0], cells[1], cells[3] || "", formatReadyList(cells[2] || ""), cells[4] || ""]
      : cells
    return [{ kind: spec.id, name, namespace: namespaceAt >= 0 ? cells[namespaceAt] || "" : "", cells: shown }]
  })
}

// An Agent older than configmap-keys rejects the operation. The list is not
// downgraded to another projection; the Agent has to be upgraded.
function listFailure(spec: KindSpec, message: string, allNamespaces: boolean): string {
  if (spec.operation && message.includes(`unsupported k8s operation "${spec.operation}"`)) {
    return "当前 Agent 还不能只读取 ConfigMap 键名，需要升级 Agent。"
  }
  // A namespace-scoped ServiceAccount cannot list across namespaces; the
  // page does not pick a namespace for it.
  if (spec.namespaced && allNamespaces && isForbidden(message)) {
    return `当前 Agent 没有跨命名空间列表的权限，请在左侧选择或填写一个命名空间。\n${message}`
  }
  return message
}

function isForbidden(message: string): boolean {
  return /\bforbidden\b/i.test(message)
}

const NAMESPACE_PATTERN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

function inventoryFailure(message: string): string {
  if (message.includes("dosql_discovery_unavailable") || /unknown tool|unsupported|not found|未注册|不存在/i.test(message)) {
    return "当前 Agent 还没有数据库清单，需要升级 Agent。"
  }
  return message
}

export function ResourcePanel({
  session,
  target,
  sessionId,
  onRunningChange,
}: {
  session: Session
  target: Target
  sessionId: string
  onRunningChange: (running: boolean) => void
}) {
  const [scope, setScope] = useState<Scope>("cluster")
  const [kind, setKind] = useState<KindId>("deployments")
  const [namespaces, setNamespaces] = useState<string[]>([])
  const [namespaceError, setNamespaceError] = useState("")
  const [namespace, setNamespace] = useState("")
  const [allNamespaces, setAllNamespaces] = useState(true)
  const [namespaceForbidden, setNamespaceForbidden] = useState(false)
  const [namespaceDraft, setNamespaceDraft] = useState("")
  const [rows, setRows] = useState<ResourceRow[]>([])
  const [selectedKey, setSelectedKey] = useState("")
  const [phase, setPhase] = useState<"loading" | "refreshing" | "ready">("loading")
  const [listError, setListError] = useState("")
  const [actionOutput, setActionOutput] = useState("")
  const [actionTable, setActionTable] = useState<string[][] | null>(null)
  const [actionColumns, setActionColumns] = useState<string[]>([])
  const [actionError, setActionError] = useState("")
  const [confirmed, setConfirmed] = useState(false)
  const [pendingWrite, setPendingWrite] = useState("")
  const [replicas, setReplicas] = useState("1")
  const [tail, setTail] = useState("200")
  const [container, setContainer] = useState("")
  const [running, setRunning] = useState(false)
  const [stopPending, setStopPending] = useState(false)
  const [assets, setAssets] = useState<DatabaseAsset[]>([])
  const [engine, setEngine] = useState<(typeof ENGINES)[number]>("全部")
  const [databaseName, setDatabaseName] = useState("")
  const [sql, setSql] = useState("select 1")
  const [report, setReport] = useState<SqlTable | null>(null)
  const [dbError, setDbError] = useState("")
  const [sections, setSections] = useState<Array<{ title: string; body: string }>>([])
  const [hostError, setHostError] = useState("")
  const [command, setCommand] = useState("")
  const listAbort = useRef<AbortController | null>(null)
  const actionAbort = useRef<AbortController | null>(null)
  const cancelRef = useRef<ToolCancel | null>(null)
  const spec = kindSpec(kind)
  const selected = rows.find((row) => `${row.namespace}/${row.name}` === selectedKey) || null

  function clearAction() {
    setActionOutput("")
    setActionTable(null)
    setActionColumns([])
    setActionError("")
    setReport(null)
    setConfirmed(false)
    setPendingWrite("")
  }

  useEffect(() => {
    return () => {
      listAbort.current?.abort()
      actionAbort.current?.abort()
    }
  }, [])

  useEffect(() => {
    if (scope !== "cluster") return
    const controller = new AbortController()
    void runTargetTool(session, target, sessionId, TOOLS.k8s, {
      operation: "get",
      kind: "namespaces",
      output: "custom-columns=NAME:.metadata.name,STATUS:.status.phase",
    }, { signal: controller.signal })
      .then((text) => {
        const body = toolBody(text)
        const grid = parseAlignedColumns(body)
        if (!grid) {
          if (body.trim()) setNamespaceError(body)
          return
        }
        setNamespaceError("")
        setNamespaceForbidden(false)
        setNamespaces(grid.map((cells) => cells[0]).filter(Boolean))
      })
      .catch((err: unknown) => {
        if (isAbortError(err) || controller.signal.aborted) return
        const message = err instanceof Error ? err.message : "读取命名空间失败"
        if (isForbidden(message)) {
          setNamespaceForbidden(true)
          setNamespaceError("")
          return
        }
        setNamespaceError(message)
      })
    return () => controller.abort()
  }, [session, target, sessionId, scope])

  useEffect(() => {
    if (scope !== "cluster") return
    const controller = new AbortController()
    listAbort.current?.abort()
    listAbort.current = controller
    setPhase("loading")
    setRows([])
    setSelectedKey("")
    setListError("")
    clearAction()
    void loadCluster(controller, false)
    return () => controller.abort()
    // The list identity is the kind and namespace. Refresh uses loadCluster directly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, target, sessionId, scope, kind, namespace, allNamespaces])

  useEffect(() => {
    if (scope !== "database") return
    const controller = new AbortController()
    setPhase("loading")
    setAssets([])
    setDbError("")
    setReport(null)
    setSelectedKey("")
    void runTargetTool(session, target, sessionId, TOOLS.databaseInventory, {}, { signal: controller.signal })
      .then((text) => {
        const inventory = parseDatabaseInventory(toolBody(text))
        if (!inventory) {
          setDbError("资产清单不是可识别的 JSON。")
          return
        }
        setAssets(inventory.candidates)
      })
      .catch((err: unknown) => {
        if (!isAbortError(err) && !controller.signal.aborted) {
          setDbError(inventoryFailure(err instanceof Error ? err.message : "扫描数据库实例失败"))
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setPhase("ready")
      })
    return () => controller.abort()
  }, [session, target, sessionId, scope])

  useEffect(() => {
    if (scope !== "host") return
    const controller = new AbortController()
    setPhase("loading")
    setSections([])
    setHostError("")
    void runTargetTool(session, target, sessionId, TOOLS.nodeInfo, {}, { signal: controller.signal })
      .then((text) => setSections(hostSections(toolBody(text))))
      .catch((err: unknown) => {
        if (!isAbortError(err) && !controller.signal.aborted) setHostError(err instanceof Error ? err.message : "读取主机信息失败")
      })
      .finally(() => {
        if (!controller.signal.aborted) setPhase("ready")
      })
    return () => controller.abort()
  }, [session, target, sessionId, scope])

  async function loadCluster(controller: AbortController, refresh: boolean) {
    const current = kindSpec(kind)
    if (refresh) setPhase("refreshing")
    try {
      const args: Record<string, unknown> = current.id === "helm"
        ? { operation: "helm-list" }
        : current.operation
          ? { operation: current.operation }
          : { operation: "get", kind: current.id, output: current.output }
      if (current.namespaced) {
        if (allNamespaces || !namespace.trim()) args.all_namespaces = true
        else args.namespace = namespace.trim()
      }
      const text = await runTargetTool(session, target, sessionId, TOOLS.k8s, args, { signal: controller.signal })
      if (controller.signal.aborted) return
      const body = toolBody(text)
      const parsed = parseRows(current, body)
      if (!parsed) {
        setListError(body || "列表不是可识别的表格。")
        if (!refresh) setRows([])
        return
      }
      setRows(parsed)
      setListError("")
    } catch (err) {
      if (isAbortError(err) || controller.signal.aborted) return
      setListError(listFailure(current, err instanceof Error ? err.message : "读取资源失败", allNamespaces || !namespace.trim()))
    } finally {
      if (!controller.signal.aborted) setPhase("ready")
    }
  }

  function refreshList() {
    if (scope !== "cluster" || phase === "loading") return
    const controller = new AbortController()
    listAbort.current?.abort()
    listAbort.current = controller
    void loadCluster(controller, true)
  }

  function chooseKind(next: KindId) {
    if (next === kind) return
    setKind(next)
    setConfirmed(false)
  }

  function chooseNamespace(next: string) {
    setAllNamespaces(next === "")
    setNamespace(next)
  }

  async function runTool(tool: string, args: Record<string, unknown>, signal: AbortSignal) {
    return runTargetTool(session, target, sessionId, tool, args, {
      signal,
      onCancelReady: (cancel) => { cancelRef.current = cancel },
    })
  }

  async function perform(action: string, write: boolean) {
    if (!selected || running || stopPending) return
    // A mutation is sent only for the action the warning names and only once it is checked.
    if (write && (!confirmed || pendingWrite !== action)) return
    const count = Number(replicas)
    if (action === "scale" && (!Number.isInteger(count) || count < 0 || count > 100)) {
      setActionError("副本数必须是 0 到 100 的整数。")
      return
    }
    const lines = Number(tail)
    if (action === "logs" && (!/^\d+$/.test(tail.trim()) || lines < 1 || lines > 1000)) {
      setActionError("日志行数必须是 1 到 1000 的整数。")
      return
    }
    const controller = new AbortController()
    actionAbort.current?.abort()
    actionAbort.current = controller
    setRunning(true)
    setStopPending(false)
    clearAction()
    onRunningChange(true)
    cancelRef.current = null
    try {
      if (action === "pods") {
        const probe = await runTool(TOOLS.k8s, {
          operation: "get",
          kind: selected.kind,
          name: selected.name,
          namespace: selected.namespace,
          output: SELECTOR_OUTPUT,
        }, controller.signal)
        const selector = labelSelectorString(toolBody(probe))
        if (!selector) {
          setActionError("这个工作负载的选择器不能原样写成标签选择器，没有查询 Pod。")
          return
        }
        const listed = await runTool(TOOLS.k8s, {
          operation: "get",
          kind: "pods",
          namespace: selected.namespace,
          selector,
          output: POD_OUTPUT,
        }, controller.signal)
        const grid = parseAlignedColumns(toolBody(listed))
        if (!grid) {
          setActionError(toolBody(listed) || "Pod 列表不是可识别的表格。")
          return
        }
        setActionColumns(["命名空间", "名称", "阶段", "就绪", "节点"])
        setActionTable(grid.map((cells) => [cells[0], cells[1], cells[3] || "", formatReadyList(cells[2] || ""), cells[4] || ""]))
        return
      }
      const args: Record<string, unknown> = { operation: action }
      const resource = `${selected.kind}/${selected.name}`
      if (selected.kind !== "nodes" && selected.kind !== "namespaces" && selected.namespace) args.namespace = selected.namespace
      if (action === "describe") {
        args.kind = selected.kind
        args.name = selected.name
      } else if (action === "events" || action === "logs" || action === "rollout-status" || action === "rollout-restart") {
        args.resource = resource
      } else if (action === "scale") {
        args.resource = resource
        args.replicas = count
      } else if (action === "delete-pod" || action === "cordon" || action === "uncordon" || action.startsWith("helm-")) {
        args.name = selected.name
      }
      if (action === "logs") {
        args.tail = lines
        if (container.trim()) args.container = container.trim()
      }
      if (write) args.confirm = true
      const text = await runTool(TOOLS.k8s, args, controller.signal)
      setActionOutput(toolBody(text) || "完成，没有输出。")
      if (write) {
        const refreshController = new AbortController()
        listAbort.current?.abort()
        listAbort.current = refreshController
        await loadCluster(refreshController, true)
      }
    } catch (err) {
      if (!isAbortError(err) && !controller.signal.aborted) setActionError(err instanceof Error ? err.message : "操作失败")
    } finally {
      if (actionAbort.current === controller) {
        setRunning(false)
        setStopPending(false)
        onRunningChange(false)
        setConfirmed(false)
        setPendingWrite("")
        cancelRef.current = null
      }
    }
  }

  async function queryDatabase() {
    const name = databaseName.trim()
    const statement = sql.trim()
    if (!name || !statement || running || stopPending || assets.length === 0) return
    const controller = new AbortController()
    actionAbort.current?.abort()
    actionAbort.current = controller
    setRunning(true)
    setStopPending(false)
    setActionError("")
    setReport(null)
    setActionOutput("")
    onRunningChange(true)
    try {
      const args: Record<string, unknown> = {
        operation: "ask",
        skill: "dosql",
        credential_mode: "auto",
        sql_database: name,
        sql: statement,
      }
      const text = await runTool(TOOLS.prompt, args, controller.signal)
      const body = toolBody(text)
      setActionOutput(body)
      setReport(parseSqlReport(body))
    } catch (err) {
      if (!isAbortError(err) && !controller.signal.aborted) setActionError(err instanceof Error ? err.message : "查询失败")
    } finally {
      if (actionAbort.current === controller) {
        setRunning(false)
        setStopPending(false)
        onRunningChange(false)
        cancelRef.current = null
      }
    }
  }

  async function runCommand(next: string) {
    const cmd = next.trim()
    if (!cmd || running || stopPending) return
    const controller = new AbortController()
    actionAbort.current?.abort()
    actionAbort.current = controller
    setRunning(true)
    setStopPending(false)
    setCommand(cmd)
    setActionError("")
    setActionOutput("")
    onRunningChange(true)
    try {
      const text = await runTool(TOOLS.shell, { command: cmd }, controller.signal)
      setActionOutput(toolBody(text) || "命令已结束，没有输出。")
    } catch (err) {
      if (!isAbortError(err) && !controller.signal.aborted) setActionError(err instanceof Error ? err.message : "命令失败")
    } finally {
      if (actionAbort.current === controller) {
        setRunning(false)
        setStopPending(false)
        onRunningChange(false)
        cancelRef.current = null
      }
    }
  }

  function stop() {
    if (!cancelRef.current || stopPending) return
    setStopPending(true)
    actionAbort.current?.abort()
    void cancelRef.current().catch((err: unknown) => {
      setStopPending(false)
      setActionError(err instanceof Error ? err.message : "停止请求失败")
    })
  }

  const visibleAssets = assets.filter((asset) => engine === "全部" || asset.engine === engine)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 items-center justify-between gap-3 border-b bg-card px-4 py-3">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <Boxes aria-hidden="true" className="size-4 text-primary" />
            资源
          </h2>
          <p className="text-xs text-muted-foreground">{target.cluster}/{target.instance} · 点一行再操作。Secret 不在目录里。</p>
        </div>
        {scope === "cluster" ? (
          <Button type="button" onClick={refreshList} disabled={phase === "loading"} size="sm">
            <RefreshCw aria-hidden="true" className="size-3.5" /> {phase === "refreshing" ? "正在刷新" : "刷新"}
          </Button>
        ) : null}
      </header>
      <div className="grid min-h-0 flex-1 grid-cols-1 xl:grid-cols-[16rem_minmax(0,1fr)_22rem]">
        <aside className="min-h-0 overflow-auto border-b p-3 xl:border-r xl:border-b-0">
          <SegmentedControl
            value={scope}
            onChange={(value) => { setScope(value); clearAction() }}
            className="mb-3 grid w-full grid-cols-3"
            ariaLabel="资源类型"
            options={[
              { value: "cluster", label: "集群" },
              { value: "database", label: "数据库" },
              { value: "host", label: "主机" },
            ]}
          />
          {scope === "cluster" ? (
            <>
              <p className="mb-1 text-xs font-medium text-muted-foreground">命名空间</p>
              {namespaceError ? <ErrorText>{namespaceError}</ErrorText> : null}
              <div className="mb-3 max-h-40 space-y-1 overflow-auto">
                <NamespaceButton active={allNamespaces} label="全部" onClick={() => chooseNamespace("")} />
                {namespaces.map((item) => (
                  <NamespaceButton key={item} active={!allNamespaces && namespace === item} label={item} onClick={() => chooseNamespace(item)} />
                ))}
                {namespaceForbidden && !allNamespaces && namespace && !namespaces.includes(namespace) ? (
                  <NamespaceButton active label={namespace} onClick={() => chooseNamespace(namespace)} />
                ) : null}
              </div>
              {namespaceForbidden ? (
                <form
                  className="mb-3 space-y-1.5"
                  onSubmit={(event) => {
                    event.preventDefault()
                    const next = namespaceDraft.trim()
                    if (NAMESPACE_PATTERN.test(next)) chooseNamespace(next)
                  }}
                >
                  <p className="text-xs text-muted-foreground">当前 Agent 不能列出命名空间。填写它有权限的命名空间：</p>
                  <div className="flex gap-1">
                    <input value={namespaceDraft} onChange={(event) => setNamespaceDraft(event.target.value)} placeholder="例如 apps" aria-label="命名空间" className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1 font-mono text-xs" />
                    <button type="submit" disabled={!NAMESPACE_PATTERN.test(namespaceDraft.trim())} className="rounded-md border px-2 py-1 text-xs text-foreground hover:bg-muted disabled:opacity-50">查看</button>
                  </div>
                </form>
              ) : null}
              {GROUPS.map((group) => (
                <div key={group} className="mb-2">
                  <p className="mb-1 text-xs font-medium text-muted-foreground">{group}</p>
                  {KINDS.filter((item) => item.group === group).map((item) => (
                    <button key={item.id} type="button" onClick={() => chooseKind(item.id)} className={`mb-1 block w-full rounded-md px-2 py-1.5 text-left text-xs ${kind === item.id ? "bg-muted font-medium text-foreground" : "text-foreground hover:bg-muted/60"}`}>{item.label}</button>
                  ))}
                </div>
              ))}
            </>
          ) : scope === "database" ? (
            <label className="flex flex-col gap-1.5 text-xs font-medium text-foreground">
              引擎
              <select value={engine} onChange={(event) => setEngine(event.target.value as (typeof ENGINES)[number])} className="rounded-md border bg-background px-2 py-2 text-sm font-normal">
                {ENGINES.map((item) => <option key={item} value={item}>{item}</option>)}
              </select>
            </label>
          ) : (
            <p className="text-xs text-muted-foreground">当前目标这一台主机。检查是固定命令。</p>
          )}
        </aside>
        <section className="min-h-0 overflow-auto border-b p-3 xl:border-r xl:border-b-0" aria-live="polite">
          {scope === "cluster" ? (
            <ClusterTable spec={spec} rows={rows} phase={phase} error={listError} selectedKey={selectedKey} onSelect={(row) => { setSelectedKey(`${row.namespace}/${row.name}`); clearAction() }} />
          ) : null}
          {scope === "database" ? (
            <DatabaseTable
              assets={visibleAssets}
              phase={phase}
              error={dbError}
              selectedKey={selectedKey}
              onSelect={(asset) => {
                setSelectedKey(asset.database_asset_id)
                setDatabaseName(asset.database_name || "")
                setReport(null)
                setActionOutput("")
                setActionError("")
              }}
            />
          ) : null}
          {scope === "host" ? (
            <div className="space-y-3">
              {hostError ? <ErrorText>{hostError}</ErrorText> : null}
              {phase === "loading" ? <p className="text-sm text-muted-foreground">正在读取</p> : null}
              {sections.map((section) => (
                <article key={section.title} className="rounded-md border">
                  <h3 className="border-b px-3 py-2 text-xs font-medium text-foreground">{section.title}</h3>
                  <pre className="overflow-auto p-3 font-mono text-xs leading-5 whitespace-pre-wrap break-all">{section.body}</pre>
                </article>
              ))}
              {phase === "ready" && !hostError && sections.length === 0 ? <p className="text-sm text-muted-foreground">这个范围里没有对象</p> : null}
            </div>
          ) : null}
        </section>
        <aside className="min-h-0 overflow-auto p-3">
          {running || stopPending ? (
            <button type="button" onClick={stop} disabled={stopPending || !cancelRef.current} className="mb-3 flex items-center gap-1.5 rounded-md border border-destructive/50 px-3 py-2 text-sm text-destructive disabled:opacity-60">
              <Square aria-hidden="true" className="size-3.5" /> {stopPending ? "停止中…" : "停止"}
            </button>
          ) : null}
          {scope === "cluster" && selected ? (
            <ClusterActions
              row={selected}
              targetLabel={`${target.cluster}/${target.instance}`}
              confirmed={confirmed}
              pendingWrite={pendingWrite}
              replicas={replicas}
              container={container}
              tail={tail}
              disabled={running || stopPending}
              onConfirm={setConfirmed}
              onPick={(action) => { setPendingWrite(action); setConfirmed(false) }}
              onCancel={() => { setPendingWrite(""); setConfirmed(false) }}
              onReplicas={(value) => { setReplicas(value); setConfirmed(false) }}
              onContainer={setContainer}
              onTail={setTail}
              onRun={(action, write) => void perform(action, write)}
            />
          ) : null}
          {scope === "cluster" && !selected ? <p className="text-sm text-muted-foreground">选中一行后，这里只显示这一行能做的事。</p> : null}
          {scope === "database" ? (
            <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); void queryDatabase() }}>
              {assets.length === 0 ? (
                <p className="text-xs text-muted-foreground">{phase === "loading" ? "正在扫描数据库实例。" : "没有数据库实例，不能查询。"}</p>
              ) : null}
              <fieldset disabled={assets.length === 0} className="space-y-3 disabled:opacity-50">
                <label className="flex flex-col gap-1.5 text-xs font-medium text-foreground">
                  库名
                  <input value={databaseName} onChange={(event) => setDatabaseName(event.target.value)} placeholder="填写凭据指向的库名" className="rounded-md border bg-background px-2 py-2 font-mono text-sm font-normal" />
                </label>
                <label className="flex flex-col gap-1.5 text-xs font-medium text-foreground">
                  SQL
                  <textarea value={sql} onChange={(event) => setSql(event.target.value)} rows={5} className="rounded-md border bg-background px-2 py-2 font-mono text-sm font-normal" />
                </label>
              </fieldset>
              <button type="submit" disabled={running || stopPending || assets.length === 0 || !databaseName.trim() || !sql.trim()} className="btn btn-primary">
                <Play aria-hidden="true" className="size-3.5" /> 查询
              </button>
              <p className="text-xs text-muted-foreground">只发送 operation=ask。变更仍走 doops sql --mode apply。</p>
            </form>
          ) : null}
          {scope === "host" ? (
            <div className="space-y-2">
              {CHECKS.map((item) => (
                <button key={item.command} type="button" disabled={running || stopPending} onClick={() => void runCommand(item.command)} className="block w-full rounded-md border px-2 py-1.5 text-left text-xs text-foreground hover:bg-muted disabled:opacity-50">{item.label}</button>
              ))}
              <form className="space-y-2 pt-2" onSubmit={(event) => { event.preventDefault(); void runCommand(command) }}>
                <textarea value={command} onChange={(event) => setCommand(event.target.value)} rows={3} placeholder="一条命令" className="w-full rounded-md border bg-background px-2 py-2 font-mono text-sm" />
                <button type="submit" disabled={running || stopPending || !command.trim()} className="btn btn-primary">执行</button>
              </form>
            </div>
          ) : null}
          {actionError ? <ErrorText>{actionError}</ErrorText> : null}
          {report ? <SqlView report={report} /> : null}
          {actionTable ? (
            <div className="mt-3 overflow-auto rounded-md border">
              <SimpleTable columns={actionColumns} rows={actionTable} />
            </div>
          ) : null}
          {actionOutput && !report ? <pre className="mt-3 overflow-auto rounded-md border bg-background p-3 font-mono text-xs leading-5 whitespace-pre-wrap break-all">{actionOutput}</pre> : null}
        </aside>
      </div>
    </div>
  )
}

function rowActions(row: ResourceRow): Array<{ id: string; label: string; write: boolean }> {
  if (row.kind === "helm") {
    // helm-history/status/values require a namespace; without one the tool refuses.
    if (!row.namespace) return []
    return [
      { id: "helm-history", label: "历史", write: false },
      { id: "helm-status", label: "状态", write: false },
      { id: "helm-values", label: "键名", write: false },
    ]
  }
  const actions: Array<{ id: string; label: string; write: boolean }> = []
  if (row.kind !== "configmaps") actions.push({ id: "describe", label: "详情", write: false })
  if (kindSpec(row.kind).namespaced && row.namespace) actions.push({ id: "events", label: "事件", write: false })
  if (LOGGABLE.has(row.kind)) actions.push({ id: "logs", label: "日志", write: false })
  if (ROLLOUT.has(row.kind)) {
    actions.push({ id: "rollout-status", label: "发布状态", write: false })
    actions.push({ id: "pods", label: "看 Pod", write: false })
    actions.push({ id: "rollout-restart", label: "滚动重启", write: true })
  }
  if (SCALABLE.has(row.kind)) actions.push({ id: "scale", label: "伸缩", write: true })
  if (row.kind === "pods") actions.push({ id: "delete-pod", label: "删除 Pod", write: true })
  if (row.kind === "nodes") {
    actions.push({ id: "cordon", label: "隔离节点", write: true })
    actions.push({ id: "uncordon", label: "恢复节点", write: true })
  }
  return actions
}

function ClusterActions({
  row, targetLabel, confirmed, pendingWrite, replicas, container, tail, disabled,
  onConfirm, onPick, onCancel, onReplicas, onContainer, onTail, onRun,
}: {
  row: ResourceRow
  targetLabel: string
  confirmed: boolean
  pendingWrite: string
  replicas: string
  container: string
  tail: string
  disabled: boolean
  onConfirm: (value: boolean) => void
  onPick: (action: string) => void
  onCancel: () => void
  onReplicas: (value: string) => void
  onContainer: (value: string) => void
  onTail: (value: string) => void
  onRun: (action: string, write: boolean) => void
}) {
  const actions = rowActions(row)
  const pending = actions.find((item) => item.write && item.id === pendingWrite) || null
  const objectLabel = `${kindSpec(row.kind).label} ${row.namespace ? `${row.namespace}/` : ""}${row.name}`
  return (
    <div className="space-y-2">
      <p className="font-mono text-xs text-foreground">{row.namespace ? `${row.namespace}/` : ""}{row.name}</p>
      {actions.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {row.kind === "helm" ? "这个发布没有命名空间，不能查看历史、状态和键名。" : "这一行没有可用的动作。"}
        </p>
      ) : null}
      {row.kind === "pods" || LOGGABLE.has(row.kind) ? (
        <label className="flex flex-col gap-1 text-xs text-foreground">
          容器名
          <input value={container} onChange={(event) => onContainer(event.target.value)} placeholder="不填则不指定" className="rounded-md border bg-background px-2 py-1.5 font-mono text-sm" />
        </label>
      ) : null}
      {LOGGABLE.has(row.kind) ? (
        <label className="flex flex-col gap-1 text-xs text-foreground">
          日志行数（1 到 1000）
          <input value={tail} onChange={(event) => onTail(event.target.value)} inputMode="numeric" className="rounded-md border bg-background px-2 py-1.5 font-mono text-sm" />
        </label>
      ) : null}
      {SCALABLE.has(row.kind) ? (
        <label className="flex flex-col gap-1 text-xs text-foreground">
          副本数
          <input value={replicas} onChange={(event) => onReplicas(event.target.value)} inputMode="numeric" className="rounded-md border bg-background px-2 py-1.5 font-mono text-sm" />
        </label>
      ) : null}
      <div className="flex flex-wrap gap-1.5">
        {actions.filter((item) => !item.write).map((item) => (
          <button key={item.id} type="button" disabled={disabled} onClick={() => onRun(item.id, false)} className="rounded-md border px-2 py-1.5 text-xs text-foreground hover:bg-muted disabled:opacity-50">{item.label}</button>
        ))}
      </div>
      <div className="flex flex-wrap gap-1.5">
        {actions.filter((item) => item.write).map((item) => (
          <button key={item.id} type="button" disabled={disabled} onClick={() => onPick(item.id)} className={`rounded-md border px-2 py-1.5 text-xs disabled:opacity-50 ${pendingWrite === item.id ? "border-destructive bg-destructive/10 text-destructive" : "border-destructive/40 text-destructive"}`}>{item.label}</button>
        ))}
      </div>
      {pending ? (
        <div className="space-y-2 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-foreground">
          <p>
            将对 <span className="font-mono">{targetLabel}</span> 上的 <span className="font-mono">{objectLabel}</span> 执行「{pending.label}」
            {pending.id === "scale" ? <>，副本数改为 <span className="font-mono">{replicas || "—"}</span></> : null}。
          </p>
          <label className="flex items-start gap-2">
            <input type="checkbox" checked={confirmed} onChange={(event) => onConfirm(event.target.checked)} disabled={disabled} className="mt-0.5" />
            <span>我确认执行这一次变更。只对这一次有效。</span>
          </label>
          <div className="flex gap-1.5">
            <button type="button" disabled={disabled || !confirmed} onClick={() => onRun(pending.id, true)} className="rounded-md bg-destructive px-2.5 py-1.5 text-xs font-medium text-destructive-foreground disabled:opacity-50">执行</button>
            <button type="button" disabled={disabled} onClick={onCancel} className="rounded-md border px-2.5 py-1.5 text-xs text-foreground hover:bg-muted disabled:opacity-50">取消</button>
          </div>
        </div>
      ) : null}
    </div>
  )
}

function ClusterTable({
  spec, rows, phase, error, selectedKey, onSelect,
}: {
  spec: KindSpec
  rows: ResourceRow[]
  phase: "loading" | "refreshing" | "ready"
  error: string
  selectedKey: string
  onSelect: (row: ResourceRow) => void
}) {
  return (
    <div>
      {error ? <ErrorText>{error}</ErrorText> : null}
      {phase === "refreshing" ? <p className="mb-2 text-xs text-muted-foreground">正在刷新</p> : null}
      {phase === "loading" ? <p className="text-sm text-muted-foreground">正在读取</p> : null}
      {phase !== "loading" && rows.length > 0 ? (
        <div className="overflow-auto rounded-md border">
          <table className="w-full min-w-[28rem] border-collapse text-sm">
            <thead>
              <tr className="border-b bg-muted/40 text-left text-xs text-muted-foreground">
                {spec.columns.map((column) => <th key={column} className="px-3 py-2 font-medium">{column}</th>)}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const key = `${row.namespace}/${row.name}`
                return (
                  <tr key={key} onClick={() => onSelect(row)} className={`cursor-pointer border-b border-border/50 ${selectedKey === key ? "bg-primary/10" : "hover:bg-muted/40"}`}>
                    {row.cells.map((cell, index) => <td key={index} className="px-3 py-2 font-mono text-xs">{dash(cell)}</td>)}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ) : null}
      {phase === "ready" && !error && rows.length === 0 ? <p className="text-sm text-muted-foreground">这个范围里没有对象</p> : null}
    </div>
  )
}

function DatabaseTable({
  assets, phase, error, selectedKey, onSelect,
}: {
  assets: DatabaseAsset[]
  phase: "loading" | "refreshing" | "ready"
  error: string
  selectedKey: string
  onSelect: (asset: DatabaseAsset) => void
}) {
  const columns = ["引擎", "命名空间", "工作负载", "端口", "库名", "状态"]
  return (
    <div>
      {error ? <ErrorText>{error}</ErrorText> : null}
      {phase === "loading" ? <p className="text-sm text-muted-foreground">正在读取</p> : null}
      {phase !== "loading" && assets.length > 0 ? (
        <div className="overflow-auto rounded-md border">
          <table className="w-full min-w-[32rem] border-collapse text-sm">
            <thead>
              <tr className="border-b bg-muted/40 text-left text-xs text-muted-foreground">
                {columns.map((column) => <th key={column} className="px-3 py-2 font-medium">{column}</th>)}
              </tr>
            </thead>
            <tbody>
              {assets.map((asset) => (
                <tr key={asset.database_asset_id} onClick={() => onSelect(asset)} className={`cursor-pointer border-b border-border/50 ${selectedKey === asset.database_asset_id ? "bg-primary/10" : "hover:bg-muted/40"}`}>
                  {[asset.engine, asset.namespace, asset.service, asset.port == null ? "" : String(asset.port), asset.database_name || "", asset.status].map((cell, index) => (
                    <td key={index} className="px-3 py-2 font-mono text-xs">{dash(cell)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {phase === "ready" && !error && assets.length === 0 ? <p className="text-sm text-muted-foreground">这个范围里没有对象</p> : null}
    </div>
  )
}

function SqlView({ report }: { report: SqlTable }) {
  return (
    <div className="mt-3 space-y-2">
      <p className="text-xs text-muted-foreground">{[report.engine, report.database, report.credential].filter(Boolean).join(" · ")}{report.truncated ? " · 结果被截断" : ""}</p>
      <div className="overflow-auto rounded-md border">
        <SimpleTable columns={report.columns} rows={report.rows} />
      </div>
    </div>
  )
}

function SimpleTable({ columns, rows }: { columns: string[]; rows: string[][] }) {
  return (
    <table className="w-full border-collapse text-sm">
      <thead>
        <tr className="border-b bg-muted/40 text-left text-xs text-muted-foreground">
          {columns.map((column) => <th key={column} className="px-3 py-2 font-medium">{column}</th>)}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, index) => (
          <tr key={index} className="border-b border-border/50">
            {row.map((cell, cellIndex) => <td key={cellIndex} className="px-3 py-2 font-mono text-xs">{dash(cell)}</td>)}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function NamespaceButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={`block w-full truncate rounded-md px-2 py-1 text-left font-mono text-xs ${active ? "bg-muted font-medium text-foreground" : "text-foreground hover:bg-muted/60"}`}>{label}</button>
  )
}

function ErrorText({ children }: { children: string }) {
  return <p role="alert" className="mb-3 whitespace-pre-wrap break-all rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{children}</p>
}
