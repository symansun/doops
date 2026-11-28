"use client"

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import {
  createWebhook,
  deleteWebhook,
  listWebhookDeliveries,
  listWebhooks,
  rotateWebhookSecret,
  testWebhook,
  updateWebhook,
  type AdminWebhookCreateInput,
  type AdminWebhookDelivery,
  type AdminWebhookEndpoint,
  type AdminWebhookUpdateInput,
} from "@/lib/admin"
import type { Session } from "@/lib/client"
import { formatDateTime, formatNumber } from "@/lib/format"
import { useAppDialog } from "./app-dialog"
import {
  BanIcon,
  CheckIcon,
  CopyIcon,
  HistoryIcon,
  KeyIcon,
  PencilIcon,
  PlusIcon,
  RefreshIcon,
  SendIcon,
  TrashIcon,
} from "./icons"
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Checkbox,
  Input,
  Modal,
  StatusBanner,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Textarea,
} from "./ui"

const P0_EVENTS = [
  { value: "execution.finished", label: "执行完成" },
  { value: "execution.outcome_unknown", label: "执行结果未知" },
  { value: "agent.connected", label: "Agent 已连接" },
  { value: "agent.disconnected", label: "Agent 已断开" },
  { value: "monitor.alert.opened", label: "监控告警打开" },
  { value: "monitor.alert.resolved", label: "监控告警恢复" },
  { value: "inspection.run.finished", label: "巡检运行完成" },
] as const

type WebhookFormState = {
  name: string
  url: string
  description: string
  eventTypes: string[]
  scopeCluster: string
  scopeInstance: string
  enabled: boolean
}


type SecretState = {
  endpointName: string
  secret: string
}

function errorStatus(error: unknown): number | undefined {
  if (typeof error !== "object" || error === null) return undefined
  const status = (error as { status?: unknown }).status
  return typeof status === "number" ? status : undefined
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback
}

function emptyForm(): WebhookFormState {
  return {
    name: "",
    url: "",
    description: "",
    eventTypes: [],
    scopeCluster: "",
    scopeInstance: "",
    enabled: true,
  }
}

function formFromEndpoint(endpoint: AdminWebhookEndpoint): WebhookFormState {
  return {
    name: endpoint.name || "",
    // The API intentionally never returns the URL. A blank URL on edit means
    // preserve the current URL instead of asking the browser to reveal it.
    url: "",
    description: endpoint.description || "",
    eventTypes: Array.isArray(endpoint.event_types) ? endpoint.event_types : [],
    scopeCluster: endpoint.scope_cluster || "",
    scopeInstance: endpoint.scope_instance || "",
    enabled: endpoint.enabled !== false,
  }
}

function endpointStatus(endpoint: AdminWebhookEndpoint): { label: string; variant: "success" | "warning" } {
  return endpoint.enabled ? { label: "已启用", variant: "success" } : { label: "已停用", variant: "warning" }
}

function eventLabel(value: string): string {
  return P0_EVENTS.find((event) => event.value === value)?.label || value
}

function scopeLabel(endpoint: AdminWebhookEndpoint): string {
  const cluster = endpoint.scope_cluster?.trim()
  const instance = endpoint.scope_instance?.trim()
  if (!cluster && !instance) return "全局"
  if (cluster && instance) return "集群 " + cluster + " / 实例 " + instance
  if (cluster) return "集群 " + cluster
  return "实例 " + instance
}

function deliveryStatusVariant(status: string): "success" | "danger" | "warning" | "default" {
  if (status === "delivered" || status === "succeeded" || status === "success") return "success"
  if (status === "failed" || status === "dead" || status === "discarded") return "danger"
  if (status === "pending" || status === "delivering" || status === "retrying") return "warning"
  return "default"
}

function deliveryStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    delivered: "已送达",
    succeeded: "成功",
    success: "成功",
    failed: "失败",
    dead: "终止重试",
    discarded: "已丢弃",
    pending: "等待中",
    delivering: "发送中",
    retrying: "等待重试",
  }
  return labels[status] || status || "未知"
}

function formatEndpointTime(value: string | number | undefined): string {
  return formatDateTime(value, { fallback: "—" })
}

function formatDeliveryTime(value: number | undefined): string {
  return formatDateTime(value, { unit: "ms", fallback: "—" })
}

function isValidUrl(value: string): boolean {
  try {
    const parsed = new URL(value)
    return parsed.protocol === "https:"
  } catch {
    return false
  }
}

export function AdminWebhooksPanel({ session }: { session: Session }) {
  const { confirm, dialog } = useAppDialog()
  const [endpoints, setEndpoints] = useState<AdminWebhookEndpoint[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [busyAction, setBusyAction] = useState<string | null>(null)
  const [editingEndpoint, setEditingEndpoint] = useState<AdminWebhookEndpoint | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<WebhookFormState>(emptyForm)
  const [formError, setFormError] = useState("")
  const [secretState, setSecretState] = useState<SecretState | null>(null)
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle")
  const [deliveriesEndpoint, setDeliveriesEndpoint] = useState<AdminWebhookEndpoint | null>(null)
  const [deliveries, setDeliveries] = useState<AdminWebhookDelivery[]>([])
  const [deliveriesLoading, setDeliveriesLoading] = useState(false)
  const [deliveriesError, setDeliveriesError] = useState("")

  const readonly = session.demo === true
  const isBusy = busyAction !== null
  const forbidden = errorStatus(error) === 403

  const loadEndpoints = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const result = await listWebhooks(session)
      setEndpoints(Array.isArray(result) ? result : [])
    } catch (nextError) {
      setError(nextError)
    } finally {
      setLoading(false)
    }
  }, [session])

  useEffect(() => {
    void loadEndpoints()
  }, [loadEndpoints])

  const closeForm = useCallback(() => {
    if (busyAction === "save") return
    setFormOpen(false)
    setEditingEndpoint(null)
    setForm(emptyForm())
    setFormError("")
  }, [busyAction])

  const openCreate = useCallback(() => {
    if (readonly || isBusy) return
    setEditingEndpoint(null)
    setForm(emptyForm())
    setFormError("")
    setFormOpen(true)
  }, [isBusy, readonly])

  const openEdit = useCallback((endpoint: AdminWebhookEndpoint) => {
    if (readonly || isBusy) return
    setEditingEndpoint(endpoint)
    setForm(formFromEndpoint(endpoint))
    setFormError("")
    setFormOpen(true)
  }, [isBusy, readonly])

  const toggleEvent = useCallback((value: string) => {
    setForm((current) => ({
      ...current,
      eventTypes: current.eventTypes.includes(value)
        ? current.eventTypes.filter((event) => event !== value)
        : [...current.eventTypes, value],
    }))
  }, [])

  const submitForm = useCallback(async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (readonly || busyAction) return

    const name = form.name.trim()
    const url = form.url.trim()
    if (!name) {
      setFormError("请填写端点名称")
      return
    }
    if (!editingEndpoint && !url) {
      setFormError("请填写回调 URL")
      return
    }
    if (url && !isValidUrl(url)) {
      setFormError("URL 必须是有效的 HTTPS 地址（仅支持 HTTPS）")
      return
    }
    if (form.eventTypes.length === 0) {
      setFormError("至少选择一个事件")
      return
    }

    const confirmed = await confirm({
      title: editingEndpoint ? "保存 Webhook 修改？" : "创建 Webhook？",
      message: editingEndpoint
        ? "将更新此端点的名称、描述、事件和作用域；URL 留空时保持现有 URL 不变。"
        : "创建后 URL 只会以主机和 hash 的形式展示。",
      confirmLabel: editingEndpoint ? "保存" : "创建",
    })
    if (!confirmed) return

    setBusyAction("save")
    setFormError("")
    try {
      if (editingEndpoint) {
        const input: AdminWebhookUpdateInput = {
          name,
          description: form.description.trim(),
          event_types: form.eventTypes,
          scope_cluster: form.scopeCluster.trim(),
          scope_instance: form.scopeInstance.trim(),
          enabled: form.enabled,
        }
        if (url) input.url = url
        await updateWebhook(session, editingEndpoint.id, input)
      } else {
        const input: AdminWebhookCreateInput = {
          name,
          description: form.description.trim(),
          url,
          event_types: form.eventTypes,
          scope_cluster: form.scopeCluster.trim() || undefined,
          scope_instance: form.scopeInstance.trim() || undefined,
          enabled: form.enabled,
        }
        const created = await createWebhook(session, input)
        const secret = created.endpoint?.secret
        if (secret) setSecretState({ endpointName: name, secret })
      }
      closeForm()
      await loadEndpoints()
    } catch (nextError) {
      setFormError(errorMessage(nextError, "保存 Webhook 失败"))
    } finally {
      setBusyAction(null)
    }
  }, [busyAction, closeForm, confirm, editingEndpoint, form, loadEndpoints, readonly, session])

  const removeEndpoint = useCallback(async (endpoint: AdminWebhookEndpoint) => {
    if (readonly || busyAction) return
    const confirmed = await confirm({
      title: "删除 Webhook？",
      message: "删除后将停止投递并撤销此端点，操作不可撤销。",
      confirmLabel: "删除",
      danger: true,
    })
    if (!confirmed) return
    setBusyAction("delete:" + endpoint.id)
    try {
      await deleteWebhook(session, endpoint.id)
      await loadEndpoints()
    } catch (nextError) {
      setError(nextError)
    } finally {
      setBusyAction(null)
    }
  }, [busyAction, confirm, loadEndpoints, readonly, session])

  const toggleEndpoint = useCallback(async (endpoint: AdminWebhookEndpoint) => {
    if (readonly || busyAction) return
    const nextEnabled = !endpoint.enabled
    const confirmed = await confirm({
      title: nextEnabled ? "启用 Webhook？" : "停用 Webhook？",
      message: nextEnabled ? "启用后会重新接收匹配事件。" : "停用后不会再创建新的投递任务。",
      confirmLabel: nextEnabled ? "启用" : "停用",
      danger: !nextEnabled,
    })
    if (!confirmed) return
    setBusyAction("toggle:" + endpoint.id)
    try {
      await updateWebhook(session, endpoint.id, { enabled: nextEnabled })
      await loadEndpoints()
    } catch (nextError) {
      setError(nextError)
    } finally {
      setBusyAction(null)
    }
  }, [busyAction, confirm, loadEndpoints, readonly, session])

  const rotateSecret = useCallback(async (endpoint: AdminWebhookEndpoint) => {
    if (readonly || busyAction) return
    const confirmed = await confirm({
      title: "轮换 Webhook Secret？",
      message: "旧 Secret 会立即失效。新 Secret 只展示一次，请确认接收方已准备好更新。",
      confirmLabel: "轮换 Secret",
      danger: true,
    })
    if (!confirmed) return
    setBusyAction("rotate:" + endpoint.id)
    try {
      const result = await rotateWebhookSecret(session, endpoint.id)
      if (result.secret) setSecretState({ endpointName: endpoint.name, secret: result.secret })
      await loadEndpoints()
    } catch (nextError) {
      setError(nextError)
    } finally {
      setBusyAction(null)
    }
  }, [busyAction, confirm, loadEndpoints, readonly, session])

  const sendTest = useCallback(async (endpoint: AdminWebhookEndpoint) => {
    if (readonly || busyAction) return
    const confirmed = await confirm({
      title: "发送测试事件？",
      message: "这会向端点发送一次 webhook.test 测试投递，不会修改订阅事件。",
      confirmLabel: "发送测试",
    })
    if (!confirmed) return
    setBusyAction("test:" + endpoint.id)
    try {
      await testWebhook(session, endpoint.id)
      await loadEndpoints()
    } catch (nextError) {
      setError(nextError)
    } finally {
      setBusyAction(null)
    }
  }, [busyAction, confirm, loadEndpoints, readonly, session])

  const openDeliveries = useCallback(async (endpoint: AdminWebhookEndpoint) => {
    if (busyAction) return
    const confirmed = await confirm({
      title: "查看投递记录？",
      message: "将读取此端点最近的投递元数据；不会显示回调 URL 或 Secret。",
      confirmLabel: "查看记录",
    })
    if (!confirmed) return
    setBusyAction("deliveries:" + endpoint.id)
    setDeliveriesEndpoint(endpoint)
    setDeliveries([])
    setDeliveriesError("")
    setDeliveriesLoading(true)
    try {
      const result = await listWebhookDeliveries(session, endpoint.id, { limit: 50 })
      setDeliveries(Array.isArray(result) ? result : [])
    } catch (nextError) {
      setDeliveriesError(errorMessage(nextError, "读取投递记录失败"))
    } finally {
      setDeliveriesLoading(false)
      setBusyAction(null)
    }
  }, [busyAction, confirm, session])

  const closeDeliveries = useCallback(() => {
    if (busyAction?.startsWith("deliveries:")) return
    setDeliveriesEndpoint(null)
    setDeliveries([])
    setDeliveriesError("")
  }, [busyAction])

  const closeSecret = useCallback(() => {
    setSecretState(null)
    setCopyState("idle")
  }, [])

  const copySecret = useCallback(async () => {
    if (!secretState) return
    try {
      await navigator.clipboard.writeText(secretState.secret)
      setCopyState("copied")
    } catch {
      setCopyState("failed")
    }
  }, [secretState])

  const summary = useMemo(() => {
    const enabled = endpoints.filter((endpoint) => endpoint.enabled).length
    return { total: endpoints.length, enabled }
  }, [endpoints])

  return (
    <section className="space-y-4" aria-labelledby="admin-webhooks-title">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id="admin-webhooks-title" className="text-lg font-semibold">Webhook 端点</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            管理平台事件通知。列表仅显示端点主机和 URL hash，不回显完整 URL。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button type="button" size="sm" onClick={() => void loadEndpoints()} disabled={loading || isBusy} title="刷新">
            <RefreshIcon aria-hidden="true" />
            刷新
          </Button>
          {!readonly ? (
            <Button type="button" size="sm" variant="primary" onClick={openCreate} disabled={isBusy}>
              <PlusIcon aria-hidden="true" />
              新建端点
            </Button>
          ) : null}
        </div>
      </div>

      {readonly ? (
        <StatusBanner tone="info" compact>
          当前为 Demo 会话：Webhook 仅提供只读视图，不会创建、修改、删除、测试、轮换 Secret 或加载可用 Secret。
        </StatusBanner>
      ) : null}

      {error ? (
        <StatusBanner tone={forbidden ? "warning" : "error"}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <span>{forbidden ? "当前账号没有管理员权限，无法查看 Webhook 端点。" : errorMessage(error, "读取 Webhook 端点失败")}</span>
            <Button type="button" size="sm" onClick={() => void loadEndpoints()} disabled={loading || isBusy}>
              <RefreshIcon aria-hidden="true" />
              重试
            </Button>
          </div>
        </StatusBanner>
      ) : null}

      {!error && !loading ? (
        <Card>
          <CardHeader>
            <CardTitle>端点列表</CardTitle>
            <CardDescription>{summary.total} 个端点，{summary.enabled} 个已启用</CardDescription>
          </CardHeader>
          <CardContent>
            {endpoints.length === 0 ? (
              <div className="rounded-md border border-dashed p-8 text-center text-sm text-muted-foreground">
                {readonly ? "Demo 会话没有可用的 Webhook 端点。" : "还没有 Webhook 端点，创建第一个端点开始接收事件。"}
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>端点</TableHead>
                    <TableHead>事件</TableHead>
                    <TableHead>作用域</TableHead>
                    <TableHead>状态 / 投递</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {endpoints.map((endpoint) => {
                    const status = endpointStatus(endpoint)
                    const endpointBusy = busyAction?.endsWith(endpoint.id) === true
                    return (
                      <TableRow key={endpoint.id}>
                        <TableCell>
                          <div className="min-w-[220px] space-y-1">
                            <div className="font-medium text-foreground">{endpoint.name}</div>
                            <div className="font-mono text-xs text-muted-foreground" title={endpoint.url_host || undefined}>
                              {endpoint.url_host || "未知主机"}
                            </div>
                            <div className="break-all font-mono text-[11px] text-muted-foreground" title={endpoint.url_sha256 || undefined}>
                              {endpoint.url_sha256 ? "sha256:" + endpoint.url_sha256 : "URL hash 不可用"}
                            </div>
                            <div className="text-xs text-muted-foreground">更新于 {formatEndpointTime(endpoint.updated_at || endpoint.created_at)}</div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex max-w-[300px] flex-wrap gap-1">
                            {(endpoint.event_types || []).map((event) => (
                              <Badge key={event} variant="outline" title={event}>{eventLabel(event)}</Badge>
                            ))}
                          </div>
                        </TableCell>
                        <TableCell>
                          <span className="text-xs text-muted-foreground">{scopeLabel(endpoint)}</span>
                        </TableCell>
                        <TableCell>
                          <div className="min-w-[190px] space-y-1.5">
                            <Badge variant={status.variant}>{status.label}</Badge>
                            <div className="text-xs text-muted-foreground">
                              最近成功：{formatDeliveryTime(endpoint.last_success_at_ms)}
                            </div>
                            <div className="text-xs text-muted-foreground">
                              最近失败：{formatDeliveryTime(endpoint.last_failure_at_ms)}
                            </div>
                            {endpoint.last_error_category ? (
                              <div className="max-w-[220px] truncate text-xs text-destructive" title={endpoint.last_error_category}>
                                {endpoint.last_error_category}
                              </div>
                            ) : null}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex min-w-[230px] flex-wrap justify-end gap-1.5">
                            {!readonly ? (
                              <>
                                <Button type="button" size="sm" variant="ghost" onClick={() => openEdit(endpoint)} disabled={isBusy} title="编辑">
                                  <PencilIcon aria-hidden="true" />
                                  编辑
                                </Button>
                                <Button type="button" size="sm" variant="ghost" onClick={() => void toggleEndpoint(endpoint)} disabled={isBusy} title={endpoint.enabled ? "停用" : "启用"}>
                                  {endpoint.enabled ? <BanIcon aria-hidden="true" /> : <CheckIcon aria-hidden="true" />}
                                  {endpoint.enabled ? "停用" : "启用"}
                                </Button>
                                <Button type="button" size="sm" variant="ghost" onClick={() => void rotateSecret(endpoint)} disabled={isBusy} title="轮换 Secret">
                                  <KeyIcon aria-hidden="true" />
                                  轮换
                                </Button>
                                <Button type="button" size="sm" variant="ghost" onClick={() => void sendTest(endpoint)} disabled={isBusy || !endpoint.enabled} title="发送测试">
                                  <SendIcon aria-hidden="true" />
                                  测试
                                </Button>
                                <Button type="button" size="sm" variant="danger-outline" onClick={() => void removeEndpoint(endpoint)} disabled={isBusy} title="删除">
                                  <TrashIcon aria-hidden="true" />
                                  删除
                                </Button>
                              </>
                            ) : null}
                            <Button type="button" size="sm" variant="ghost" onClick={() => void openDeliveries(endpoint)} disabled={isBusy} title="查看投递记录">
                              <HistoryIcon aria-hidden="true" />
                              投递
                            </Button>
                            {endpointBusy ? <span className="self-center text-xs text-muted-foreground">处理中…</span> : null}
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      ) : null}

      {loading ? (
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">正在加载 Webhook 端点…</CardContent>
        </Card>
      ) : null}

      {formOpen ? (
        <Modal
          title={editingEndpoint ? "编辑 Webhook 端点" : "新建 Webhook 端点"}
          description="Secret 仅在创建或轮换成功后展示一次。"
          size="lg"
          onClose={closeForm}
          dismissOnOutsideClick={!busyAction}
        >
          <form className="space-y-5" onSubmit={(event) => void submitForm(event)}>
            {formError ? <StatusBanner tone="error" compact>{formError}</StatusBanner> : null}
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="space-y-1.5 text-sm">
                <span className="font-medium">名称</span>
                <Input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} placeholder="例如：生产告警通知" required disabled={busyAction === "save"} />
              </label>
              <label className="space-y-1.5 text-sm">
                <span className="font-medium">URL</span>
                <Input type="url" value={form.url} onChange={(event) => setForm((current) => ({ ...current, url: event.target.value }))} placeholder={editingEndpoint ? "留空以保留现有 URL" : "https://example.com/hooks/doops"} required={!editingEndpoint} disabled={busyAction === "save"} autoComplete="off" />
                <span className="block text-xs text-muted-foreground">仅支持 HTTPS URL；列表不会回显完整 URL。{editingEndpoint ? "编辑时留空会保留现有 URL。" : ""}</span>
              </label>
            </div>
            <label className="block space-y-1.5 text-sm">
              <span className="font-medium">描述（可选）</span>
              <Textarea value={form.description} onChange={(event) => setForm((current) => ({ ...current, description: event.target.value }))} rows={3} placeholder="说明用途或接收方" disabled={busyAction === "save"} />
            </label>
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">订阅事件（P0）</legend>
              <div className="grid gap-2 sm:grid-cols-2">
                {P0_EVENTS.map((eventOption) => (
                  <label key={eventOption.value} className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm hover:bg-muted/30">
                    <Checkbox checked={form.eventTypes.includes(eventOption.value)} onChange={() => toggleEvent(eventOption.value)} disabled={busyAction === "save"} />
                    <span>{eventOption.label}</span>
                    <code className="ml-auto text-[10px] text-muted-foreground">{eventOption.value}</code>
                  </label>
                ))}
              </div>
            </fieldset>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="space-y-1.5 text-sm">
                <span className="font-medium">集群作用域（可选）</span>
                <Input value={form.scopeCluster} onChange={(event) => setForm((current) => ({ ...current, scopeCluster: event.target.value }))} placeholder="留空表示全部集群" disabled={busyAction === "save"} />
              </label>
              <label className="space-y-1.5 text-sm">
                <span className="font-medium">实例作用域（可选）</span>
                <Input value={form.scopeInstance} onChange={(event) => setForm((current) => ({ ...current, scopeInstance: event.target.value }))} placeholder="留空表示全部实例" disabled={busyAction === "save"} />
              </label>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={form.enabled} onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))} disabled={busyAction === "save"} />
              创建后立即启用
            </label>
            <div className="flex justify-end gap-2 border-t pt-4">
              <Button type="button" onClick={closeForm} disabled={busyAction === "save"}>取消</Button>
              <Button type="submit" variant="primary" disabled={busyAction === "save"}>
                {busyAction === "save" ? "保存中…" : editingEndpoint ? "保存修改" : "创建端点"}
              </Button>
            </div>
          </form>
        </Modal>
      ) : null}

      {secretState ? (
        <Modal
          title="Webhook Secret（仅展示一次）"
          description="请立即复制并安全保存。关闭后本页面不会保留此 Secret。"
          size="md"
          onClose={closeSecret}
          dismissOnOutsideClick={false}
          footer={
            <div className="flex justify-end gap-2">
              <Button type="button" onClick={closeSecret}>清除并关闭</Button>
              <Button type="button" variant="primary" onClick={() => void copySecret()}>
                <CopyIcon aria-hidden="true" />
                {copyState === "copied" ? "已复制" : "复制 Secret"}
              </Button>
            </div>
          }
        >
          <div className="space-y-3">
            <StatusBanner tone="warning" compact>Secret 只在本次操作中返回。不要将它粘贴到 URL、日志或客户端存储中。</StatusBanner>
            <p className="text-sm text-muted-foreground">端点：{secretState.endpointName}</p>
            <code className="block max-h-40 overflow-auto break-all rounded-md border bg-muted/40 p-3 text-xs">{secretState.secret}</code>
            {copyState === "failed" ? <p className="text-xs text-destructive">复制失败，请使用受信任的密码管理器手动保存。</p> : null}
          </div>
        </Modal>
      ) : null}

      {deliveriesEndpoint ? (
        <Modal
          title={"投递记录：" + deliveriesEndpoint.name}
          description="仅展示投递状态和响应元数据，不展示 URL 或 Secret。"
          size="lg"
          onClose={closeDeliveries}
          dismissOnOutsideClick={!deliveriesLoading}
        >
          {deliveriesError ? <StatusBanner tone="error" compact>{deliveriesError}</StatusBanner> : null}
          {deliveriesLoading ? (
            <div className="py-8 text-center text-sm text-muted-foreground">正在加载投递记录…</div>
          ) : deliveries.length === 0 && !deliveriesError ? (
            <div className="py-8 text-center text-sm text-muted-foreground">暂无投递记录。</div>
          ) : !deliveriesError ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>事件 ID</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>尝试</TableHead>
                  <TableHead>响应</TableHead>
                  <TableHead>时间</TableHead>
                  <TableHead>错误类别</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {deliveries.map((delivery) => (
                  <TableRow key={delivery.id}>
                    <TableCell><code className="max-w-[180px] truncate text-xs" title={delivery.event_id}>{delivery.event_id}</code></TableCell>
                    <TableCell><Badge variant={deliveryStatusVariant(delivery.status)}>{deliveryStatusLabel(delivery.status)}</Badge></TableCell>
                    <TableCell>{formatNumber(delivery.attempt, { fallback: "—" })}</TableCell>
                    <TableCell>{delivery.response_status > 0 ? formatNumber(delivery.response_status, { fallback: "—" }) : "—"}</TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDeliveryTime(delivery.delivered_at_ms || delivery.updated_at_ms || delivery.created_at_ms)}</TableCell>
                    <TableCell className="max-w-[180px] truncate text-xs text-muted-foreground" title={delivery.error_category || undefined}>{delivery.error_category || "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : null}
        </Modal>
      ) : null}

      {dialog}
    </section>
  )
}

export default AdminWebhooksPanel
