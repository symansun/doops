"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import type { Session, Target } from "@/lib/client"
import { mapWithConcurrency } from "@/lib/console-refresh"
import { randomSession } from "@/lib/gateway"
import {
  createCredential,
  bindCredentialToTarget,
  buildBindingScope,
  listCredentialBindings,
  listCredentialVersions,
  listCredentials,
  promoteCredentialVersion,
  putCredentialPayload,
  revokeCredentialBinding,
  revokeCredential,
  verifyCredentialVersion,
  canImportFromSecret,
  type CredentialBinding,
  type CredentialBindingScope,
  type CredentialResource,
  type CredentialScope,
  type CredentialType,
  type CredentialUse,
  type CredentialVerificationResponse,
  type CredentialVersion,
} from "@/lib/credentials"
import {
  MODEL_PROVIDER_PRESETS,
  bindingNeedsExplicitNamespace,
  buildStructuredCredentialPayload,
  credentialPayloadPlaceholder,
  defaultBindingNamespace,
  resolveCredentialStage,
  type OpaquePayloadEntry,
  type StructuredCredentialPayloadInput,
} from "@/lib/credential-workflow"
import { Badge, Button, Input, Modal, SegmentedControl, Select, StatusBanner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Textarea } from "./ui"
import {
  BanIcon,
  CheckIcon,
  KeyIcon,
  PlusIcon,
  RefreshIcon,
  SaveIcon,
  ShieldIcon,
} from "./icons"
import { SecretImportDialog } from "./credential-secret-import"
import { CredentialBundleButton } from "./credential-bundle-dialog"

const TYPE_ORDER: readonly CredentialType[] = [
  "modelProvider",
  "registry",
  "opaque",
  "helmRepository",
  "gitToken",
  "tls",
]

const TYPE_LABELS: Record<CredentialType, string> = {
  registry: "镜像仓库",
  tls: "TLS 证书",
  opaque: "通用密钥",
  helmRepository: "Helm 仓库",
  gitToken: "Git 令牌",
  modelProvider: "模型服务",
}

const TYPE_NAME_PLACEHOLDERS: Record<CredentialType, string> = {
  modelProvider: "model-openai",
  registry: "registry-prod",
  opaque: "db-prod",
  helmRepository: "helm-charts",
  gitToken: "git-repo",
  tls: "tls-ingress",
}

const USE_LABELS: Record<CredentialUse, string> = {
  imagePull: "拉取镜像", gitCheckout: "读取代码", helmPull: "拉取 Helm 包", tls: "TLS 证书",
  opaqueSecret: "通用密钥", database: "数据库连接", modelProvider: "调用模型",
}
const VERSION_LABELS: Record<CredentialVersion["state"], string> = {
  staged: "待激活", active: "使用中", superseded: "已替换", revoked: "已吊销",
}
function stateLabel(state: string, binding = false) {
  switch (state) {
    case "active": return binding ? "已绑定" : "已启用"
    case "revoking": return binding ? "解除中" : "吊销中"
    case "revoked": return binding ? "已解除" : "已吊销"
    default: return "状态未知"
  }
}

const TYPE_USES: Record<CredentialType, CredentialUse> = {
  registry: "imagePull",
  tls: "tls",
  opaque: "opaqueSecret",
  helmRepository: "helmPull",
  gitToken: "gitCheckout",
  modelProvider: "modelProvider",
}

// Two requests per managed resource; at most this many resources load at once.
const MANAGED_DETAIL_CONCURRENCY = 4

export interface CredentialReturnContext {
  label: string
  description?: string
  focusType?: CredentialType
}

type Action =
  | { kind: "payload"; credential: CredentialResource }
  | { kind: "binding"; credential: CredentialResource }
  | { kind: "import"; credential: CredentialResource }
  | { kind: "version"; credential: CredentialResource; version?: CredentialVersion }
  | {
      kind: "verify"
      credential: CredentialResource
      version: CredentialVersion
      binding: CredentialBinding
    }
  | null

interface VerificationStatus {
  status: string
  evidence: number
}

function fmtTime(value?: string) {
  if (!value) return "-"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false })
}

export function CredentialPanel({
  session,
  target,
  initialView = "target",
  initialTypeFilter = "all",
  isAdmin,
  returnContext,
  onReturn,
  onModelCredentialActivated,
}: {
  session: Session
  target?: Target
  initialView?: "target" | "managed"
  initialTypeFilter?: CredentialType | "all"
  isAdmin?: boolean
  returnContext?: CredentialReturnContext | null
  onReturn?: () => void
  onModelCredentialActivated?: () => void
}) {
  const effectiveIsAdmin = isAdmin ?? (session.isAdmin === true || (session.demo === true && session.isAdmin !== false))
  const [view, setView] = useState<"target" | "managed">(target ? initialView : "managed")
  const [typeFilter, setTypeFilter] = useState<CredentialType | "all">(initialTypeFilter)
  const [rows, setRows] = useState<CredentialResource[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [modelActivated, setModelActivated] = useState(false)
  const [mutating, setMutating] = useState(false)
  const mutationRef = useRef(false)
  const [showCreate, setShowCreate] = useState(false)
  const [action, setAction] = useState<Action>(null)
  const [versionsByCredential, setVersionsByCredential] = useState<
    Record<string, CredentialVersion[]>
  >({})
  const [selectedVersionIDs, setSelectedVersionIDs] = useState<Record<string, string>>({})
  const [verificationStatuses, setVerificationStatuses] = useState<
    Record<string, VerificationStatus>
  >({})

  const loadController = useRef<AbortController | null>(null)

  // Loads run on a view or target change and on a user action, never on the
  // console's background target poll. A newer load aborts the older one.
  const load = useCallback(async () => {
    loadController.current?.abort()
    const controller = new AbortController()
    loadController.current = controller
    const { signal } = controller
    setLoading(true)
    setError("")
    try {
      let nextRows: CredentialResource[]
      const versionEntries: Array<[string, CredentialVersion[]]> = []
      if (session.demo) {
        nextRows = await listCredentials(session, target, view)
      } else if (view === "managed") {
        // Gateway has no batch endpoint for versions and grants, so each
        // resource takes two requests; keep only a few resources in flight.
        const managedRows = await listCredentials(session, target, "managed", signal)
        nextRows = await mapWithConcurrency(
          managedRows,
          MANAGED_DETAIL_CONCURRENCY,
          async (resource) => {
            const [versions, bindings] = await Promise.all([
              listCredentialVersions(session, resource.id, signal),
              listCredentialBindings(session, resource.id, signal),
            ])
            versionEntries.push([resource.id, versions])
            return { ...resource, bindings }
          },
          signal,
        )
      } else {
        // Target visibility is authoritative for operators. Do not load the
        // management view here: its owner/admin boundary is independent and
        // must not erase a successful target credential response.
        nextRows = await listCredentials(session, target, "target", signal)
      }
      if (signal.aborted) return
      const nextVersions = Object.fromEntries(versionEntries)
      setRows(nextRows)
      setVersionsByCredential(nextVersions)
      setSelectedVersionIDs((current) => {
        const next = { ...current }
        for (const [credentialID, versions] of Object.entries(nextVersions)) {
          if (!versions.some((version) => version.id === next[credentialID])) {
            next[credentialID] =
              versions.find((version) => version.state === "staged")?.id ??
              versions.find((version) => version.state === "active")?.id ??
              versions[0]?.id ??
              ""
          }
        }
        return next
      })
    } catch (err) {
      if (signal.aborted) return
      setRows([])
      setVersionsByCredential({})
      setSelectedVersionIDs({})
      setError((err as Error).message)
    } finally {
      if (loadController.current === controller) setLoading(false)
    }
    // The target poll replaces the object on every heartbeat; reload only when
    // the target itself changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, target?.cluster, target?.instance, view])

  useEffect(() => {
    load()
    return () => loadController.current?.abort()
  }, [load])

  const countsByType = useMemo(() => {
    const counts: Partial<Record<CredentialType, number>> = {}
    for (const row of rows) {
      counts[row.type] = (counts[row.type] ?? 0) + 1
    }
    return counts
  }, [rows])

  const visibleRows = useMemo(
    () => (typeFilter === "all" ? rows : rows.filter((row) => row.type === typeFilter)),
    [rows, typeFilter],
  )

  function completed(message: string) {
    setNotice(message)
    window.setTimeout(() => setNotice(""), 5000)
  }

  function openCreateForm(preferredType?: CredentialType) {
    if (preferredType) setTypeFilter(preferredType)
    setView("managed")
    setShowCreate(true)
  }

  async function revoke(resource: CredentialResource) {
    if (mutationRef.current) return
    mutationRef.current = true
    setMutating(true)
    setError("")
    try {
      await revokeCredential(session, resource.id)
      completed(`已吊销 ${resource.name}`)
      await load()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      mutationRef.current = false
      setMutating(false)
    }
  }

  async function revokeBinding(resource: CredentialResource, binding: CredentialBinding) {
    if (mutationRef.current) return
    mutationRef.current = true
    setMutating(true)
    setError("")
    try {
      await revokeCredentialBinding(session, resource.id, binding.id)
      completed(`已解除 ${resource.name} 的一条目标绑定`)
      await load()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      mutationRef.current = false
      setMutating(false)
    }
  }

  async function promote(resource: CredentialResource, version: CredentialVersion) {
    if (version.state !== "staged" || mutationRef.current) return
    mutationRef.current = true
    setMutating(true)
    setError("")
    try {
      await promoteCredentialVersion(session, resource.id, version.id)
      setAction(null)
      await load()
      if (resource.type === "modelProvider") {
        setModelActivated(true)
        onModelCredentialActivated?.()
        completed(`已激活模型凭据「${resource.name}」，返回 AI 助手后将自动重连`)
      } else {
        completed(`已激活「${resource.name}」的所选版本`)
      }
    } catch (err) {
      setError((err as Error).message)
    } finally {
      mutationRef.current = false
      setMutating(false)
    }
  }

  function selectVersion(credentialID: string, versionID: string) {
    setSelectedVersionIDs((current) => ({ ...current, [credentialID]: versionID }))
  }

  function rememberVersion(credentialID: string, version: CredentialVersion) {
    setVersionsByCredential((current) => ({
      ...current,
      [credentialID]: [
        version,
        ...(current[credentialID] ?? []).filter((candidate) => candidate.id !== version.id),
      ],
    }))
    selectVersion(credentialID, version.id)
  }

  function rememberVerification(
    resource: CredentialResource,
    version: CredentialVersion,
    result: CredentialVerificationResponse,
  ) {
    setVerificationStatuses((current) => ({
      ...current,
      [`${resource.id}:${version.id}`]: {
        status: result.status,
        evidence: result.evidence.length,
      },
    }))
  }

  const defaultCreateType: CredentialType =
    typeFilter !== "all"
      ? typeFilter
      : returnContext?.focusType ?? "registry"

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b bg-card px-4 py-3">
        <div>
          <div className="flex items-center gap-2">
            <KeyIcon width={17} height={17} className="text-primary" />
            <h2 className="text-sm font-semibold text-foreground">凭据资源</h2>
          </div>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {target
              ? `${target.cluster}/${target.instance} · 流程：写入内容 → 绑定目标 → 验证暂存 → 激活`
              : "我管理的凭据 · 绑定或导入需先选择目标"}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {target && <SegmentedControl
            value={view}
            onChange={setView}
            ariaLabel="凭据范围"
            options={[
              { value: "target", label: "当前目标可用" },
              { value: "managed", label: "我管理的" },
            ]}
          />}
          <Button
            onClick={load}
            disabled={loading}
            title="刷新凭据"
            aria-label="刷新凭据"
            size="sm"
            icon
          >
            <RefreshIcon width={15} height={15} className={loading ? "animate-spin" : ""} />
          </Button>
          {target && <CredentialBundleButton session={session} target={target} />}
          <Button
            onClick={() => openCreateForm()}
            disabled={session.demo}
            title={session.demo ? "演示模式不执行凭据写操作" : "新建凭据"}
            variant="primary"
            size="sm"
          >
            <PlusIcon width={14} height={14} /> 新建凭据
          </Button>
        </div>
      </div>

      {returnContext && onReturn && (
        <StatusBanner
          tone={modelActivated ? "success" : "info"}
          compact
          className="flex flex-wrap items-center justify-between gap-2 rounded-none border-x-0 border-t-0 px-4"
        >
          <span className="min-w-0 flex-1 text-xs">
            {modelActivated
              ? "模型凭据已激活并下发，可立即返回 AI 助手自动重连使用。"
              : (returnContext.description || "完成凭据配置与激活后，可一键返回继续操作。")}
          </span>
          <Button
            size="sm"
            variant={modelActivated ? "primary" : "default"}
            onClick={onReturn}
            type="button"
          >
            {modelActivated ? "返回 AI 助手并重连" : returnContext.label}
          </Button>
        </StatusBanner>
      )}

      <div
        role="group"
        aria-label="按凭据类型筛选"
        className="flex flex-wrap items-center gap-1.5 border-b bg-muted/20 px-4 py-2"
      >
        <Button
          type="button"
          size="sm"
          variant={typeFilter === "all" ? "primary" : "ghost"}
          onClick={() => setTypeFilter("all")}
        >
          全部 ({rows.length})
        </Button>
        {TYPE_ORDER.map((type) => {
          const count = countsByType[type] ?? 0
          return (
            <Button
              key={type}
              type="button"
              size="sm"
              variant={typeFilter === type ? "primary" : "ghost"}
              onClick={() => setTypeFilter(type)}
            >
              {TYPE_LABELS[type]} ({count})
            </Button>
          )
        })}
      </div>

      {error && (
        <StatusBanner tone="error" compact className="rounded-none border-x-0 border-t-0 px-4">
          {error}
        </StatusBanner>
      )}
      {notice && (
        <StatusBanner tone="success" compact className="flex items-center gap-2 rounded-none border-x-0 border-t-0 px-4">
          <CheckIcon width={14} height={14} /> {notice}
        </StatusBanner>
      )}
      {showCreate && (
        <CreateCredentialForm
          session={session}
          defaultType={defaultCreateType}
          isAdmin={effectiveIsAdmin}
          onCancel={() => setShowCreate(false)}
          onCreated={async (resource) => {
            setShowCreate(false)
            if (typeFilter !== "all" && typeFilter !== resource.type) {
              setTypeFilter(resource.type)
            }
            await load()
            completed(`已创建「${resource.name}」，请继续第 1/4 步：写入凭据内容`)
            setAction({ kind: "payload", credential: resource })
          }}
          onError={setError}
        />
      )}

      <div className="min-h-0 flex-1 overflow-auto">
        <Table bare className="min-w-full border-collapse text-sm">
          <TableHeader className="sticky top-0 z-20 bg-card">
            <TableRow className="hover:bg-transparent">
              <TableHead className="w-60 px-4 py-2.5">名称</TableHead>
              <TableHead className="w-32">类型</TableHead>
              <TableHead className="w-16">归属</TableHead>
              <TableHead className="w-20">状态</TableHead>
              <TableHead>
                {view === "target" ? "目标绑定" : "版本 / 目标绑定"}
              </TableHead>
              <TableHead className="sticky right-0 w-60 border-l bg-card text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {visibleRows.map((resource) => {
              const versions = versionsByCredential[resource.id] ?? []
              const version = versions.find(
                (candidate) => candidate.id === selectedVersionIDs[resource.id],
              )
              const verification = version
                ? verificationStatuses[`${resource.id}:${version.id}`]
                : undefined
              const stage = view === "managed"
                ? resolveCredentialStage({
                    resource,
                    versions,
                    selectedVersion: version,
                    target,
                    verified: Boolean(verification),
                  })
                : null
              return (
              <TableRow key={resource.id} className="align-top">
                <TableCell className="px-4 py-3 align-top">
                  <div className="break-words font-medium text-foreground">{resource.name}</div>
                  {stage && (
                    <div className="mt-1 space-y-0.5">
                      <Badge
                        variant={
                          stage.key === "active"
                            ? "success"
                            : stage.key === "revoked"
                            ? "default"
                            : stage.key === "ready_to_promote"
                            ? "primary"
                            : "warning"
                        }
                      >
                        {stage.label}
                      </Badge>
                      <div className="text-2xs leading-snug text-muted-foreground">{stage.hint}</div>
                    </div>
                  )}
                </TableCell>
                <TableCell className="py-3 align-top text-xs text-foreground">{TYPE_LABELS[resource.type]}</TableCell>
                <TableCell className="py-3 align-top text-xs text-muted-foreground">
                  {resource.scope === "platform" ? "平台" : "个人"}
                </TableCell>
                <TableCell className="py-3 align-top">
                  <Badge variant={resource.state === "active" ? "success" : "default"}>
                    {stateLabel(resource.state)}
                  </Badge>
                </TableCell>
                <TableCell className="min-w-0 break-words py-3 align-top text-xs text-muted-foreground">
                  {view === "target" ? (
                    <div className="flex flex-col gap-2">
                      <BindingSummary
                        resource={resource}
                        version={version}
                        disabled
                        onVerify={(binding) =>
                          version &&
                          setAction({ kind: "verify", credential: resource, version, binding })
                        }
                        onRevoke={(binding) => revokeBinding(resource, binding)}
                      />
                      {versions.length > 0 && (
                        <VersionSummary
                          versions={versions}
                          selectedVersion={version}
                          verification={verification}
                          onSelect={(versionID) => selectVersion(resource.id, versionID)}
                        />
                      )}
                    </div>
                  ) : (
                    <div className="flex flex-col gap-2">
                      <VersionSummary
                        versions={versions}
                        selectedVersion={version}
                        verification={verification}
                        onSelect={(versionID) => selectVersion(resource.id, versionID)}
                      />
                      <BindingSummary
                        resource={resource}
                        version={version}
                        disabled={Boolean(session.demo)}
                        busy={mutating}
                        onVerify={(binding) =>
                          version &&
                          setAction({ kind: "verify", credential: resource, version, binding })
                        }
                        onRevoke={(binding) => revokeBinding(resource, binding)}
                      />
                    </div>
                  )}
                </TableCell>
                <TableCell className="sticky right-0 z-10 border-l border-border/60 bg-background py-3 align-top">
                  <div className="flex flex-wrap justify-end gap-1.5 [&>button]:whitespace-nowrap">
                    <Button size="sm" onClick={() => setAction({ kind: "version", credential: resource, version })}>详情</Button>
                    {target && view === "managed" && resource.state === "active" && canImportFromSecret(resource.type) && (
                      <Button
                        onClick={() => setAction({ kind: "import", credential: resource })}
                        disabled={mutating}
                        size="sm"
                      >
                        从 Secret 导入
                      </Button>
                    )}
                    {view === "managed" && resource.state === "active" && !session.demo && (
                      <>
                        <Button
                          onClick={() => setAction({ kind: "payload", credential: resource })}
                          disabled={mutating}
                          variant={stage?.primaryAction === "payload" ? "primary" : "default"}
                          size="sm"
                        >
                          写入版本
                        </Button>
                        {stage?.primaryAction === "verify" && stage.verifyBinding && version?.state === "staged" && (
                          <Button
                            onClick={() =>
                              setAction({
                                kind: "verify",
                                credential: resource,
                                version,
                                binding: stage.verifyBinding!,
                              })
                            }
                            disabled={mutating}
                            variant="primary"
                            size="sm"
                          >
                            验证暂存
                          </Button>
                        )}
                        {version?.state === "staged" && (
                          <Button
                            onClick={() => promote(resource, version)}
                            disabled={mutating}
                            variant={stage?.primaryAction === "promote" ? "primary" : "default"}
                            size="sm"
                          >
                            激活
                          </Button>
                        )}
                        <Button
                          onClick={() => setAction({ kind: "binding", credential: resource })}
                          disabled={!target || mutating}
                          variant={stage?.primaryAction === "binding" ? "primary" : "default"}
                          size="sm"
                        >
                          <ShieldIcon width={12} height={12} /> 绑定目标
                        </Button>
                        <Button
                          onClick={() => revoke(resource)}
                          disabled={mutating}
                          title="吊销凭据"
                          aria-label="吊销凭据"
                          variant="danger-outline"
                          size="sm"
                          icon
                        >
                          <BanIcon width={14} height={14} />
                        </Button>
                      </>
                    )}
                  </div>
                </TableCell>
              </TableRow>
              )
            })}
            {!visibleRows.length && !loading && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={6} className="px-4 py-14 text-center text-sm text-muted-foreground">
                  {error ? (
                    "凭据加载失败，请查看错误并刷新"
                  ) : typeFilter !== "all" ? (
                    <div className="flex flex-col items-center gap-2.5">
                      <span>
                        {view === "target"
                          ? `当前目标暂无可用的「${TYPE_LABELS[typeFilter]}」凭据`
                          : `当前筛选下暂无「${TYPE_LABELS[typeFilter]}」凭据`}
                      </span>
                      <div className="flex flex-wrap items-center justify-center gap-2">
                        {rows.length > 0 && (
                          <Button size="sm" onClick={() => setTypeFilter("all")} type="button">
                            显示全部 ({rows.length})
                          </Button>
                        )}
                        {!session.demo && (
                          <Button
                            size="sm"
                            variant="primary"
                            onClick={() => openCreateForm(typeFilter)}
                            type="button"
                          >
                            <PlusIcon width={14} height={14} /> 新建{TYPE_LABELS[typeFilter]}凭据
                          </Button>
                        )}
                      </div>
                    </div>
                  ) : view === "target" ? (
                    "当前目标没有已配置的可用凭据"
                  ) : (
                    "当前账号没有可管理的凭据"
                  )}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {action?.kind === "payload" && (
        <PayloadDialog
          session={session}
          credential={action.credential}
          onClose={() => setAction(null)}
          onDone={(version) => {
            const cred = action.credential
            rememberVersion(cred.id, version)
            const hasActiveBinding = (cred.bindings ?? []).some((binding) => binding.state === "active")
            if (target && !hasActiveBinding) {
              setAction({ kind: "binding", credential: cred })
              completed(`已为「${cred.name}」写入待激活版本，请继续第 2/4 步：绑定目标`)
            } else {
              setAction(null)
              completed(`已为「${cred.name}」写入待激活版本，请点击「验证暂存」后再激活`)
            }
          }}
          onError={setError}
        />
      )}
      {target && action?.kind === "import" && (
        <SecretImportDialog
          session={session}
          target={target}
          credential={action.credential}
          onClose={() => setAction(null)}
          onDone={(version) => {
            const cred = action.credential
            rememberVersion(cred.id, version)
            const hasActiveBinding = (cred.bindings ?? []).some((binding) => binding.state === "active")
            if (!hasActiveBinding) {
              setAction({ kind: "binding", credential: cred })
              completed(`已为「${cred.name}」导入待激活版本，请继续绑定到当前目标`)
            } else {
              setAction(null)
              completed(`已为「${cred.name}」导入待激活版本，请点击「验证暂存」后再激活`)
            }
          }}
          onError={setError}
        />
      )}
      {target && action?.kind === "binding" && (
        <BindingDialog
          session={session}
          target={target}
          credential={action.credential}
          onClose={() => setAction(null)}
          onDone={async (binding, message) => {
            const cred = action.credential
            const staged = (versionsByCredential[cred.id] ?? []).find(
              (candidate) => candidate.state === "staged",
            )
            setAction(null)
            await load()
            if (staged && binding?.id && binding.state === "active") {
              completed(`${message}，请继续第 3/4 步：验证暂存版本`)
              setAction({ kind: "verify", credential: cred, version: staged, binding })
            } else {
              completed(message)
            }
          }}
          onError={setError}
        />
      )}
      {action?.kind === "version" && (
        <VersionDialog
          credential={action.credential}
          version={action.version}
          busy={mutating}
          verification={action.version && verificationStatuses[`${action.credential.id}:${action.version.id}`]}
          onClose={() => setAction(null)}
          onPromote={view === "managed" && !session.demo && action.credential.state === "active"
            ? (version) => promote(action.credential, version) : undefined}
        />
      )}
      {action?.kind === "verify" && (
        <VerifyDialog
          session={session}
          credential={action.credential}
          version={action.version}
          binding={action.binding}
          onClose={() => setAction(null)}
          onDone={(result) => {
            rememberVerification(action.credential, action.version, result)
            setAction(null)
            completed(
              `「${action.credential.name}」已完成验证，请点击「激活」使其立即生效`,
            )
          }}
          onError={setError}
        />
      )}
    </div>
  )
}

function VersionDialog({
  credential,
  version,
  busy,
  verification,
  onClose,
  onPromote,
}: {
  credential: CredentialResource
  version?: CredentialVersion
  busy: boolean
  verification?: VerificationStatus
  onClose: () => void
  onPromote?: (version: CredentialVersion) => void
}) {
  return (
    <DialogFrame title={`凭据详情 · ${credential.name}`} onClose={onClose}>
      <div className="space-y-3 p-4 text-xs">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <ReadOnlyField label="凭据编号" value={credential.id} />
          <ReadOnlyField label="更新时间" value={fmtTime(credential.updated_at)} />
          {version && <>
            <ReadOnlyField label="所选版本" value={VERSION_LABELS[version.state]} />
            <ReadOnlyField label="版本编号" value={version.id} />
            <ReadOnlyField label="内容摘要（校验值）" value={version.payload_digest} />
            <ReadOnlyField label="创建时间" value={fmtTime(version.created_at)} />
            {version.promoted_at && <ReadOnlyField label="激活时间" value={fmtTime(version.promoted_at)} />}
          </>}
          {credential.bindings?.map(binding => <ReadOnlyField key={binding.id} label={`绑定编号 · ${binding.scope.cluster}/${binding.scope.instance}`} value={binding.id} />)}
        </div>
        {verification && (
          <div className="rounded-md border border-success/30 bg-success/10 px-3 py-2 text-success">
            验证记录：{verification.evidence} 条
          </div>
        )}
        <p className="text-muted-foreground">
          这里只显示凭据信息，不显示密钥内容。
        </p>
        <div className="flex justify-end gap-2">
          <Button
            onClick={onClose}
            size="sm"
          >
            关闭
          </Button>
          {version?.state === "staged" && onPromote && (
            <Button
              onClick={() => onPromote(version)}
              disabled={busy}
              variant="primary" size="sm"
            >
              激活版本
            </Button>
          )}
        </div>
      </div>
    </DialogFrame>
  )
}

function VerifyDialog({
  session,
  credential,
  version,
  binding,
  onClose,
  onDone,
  onError,
}: {
  session: Session
  credential: CredentialResource
  version: CredentialVersion
  binding: CredentialBinding
  onClose: () => void
  onDone: (result: CredentialVerificationResponse) => void
  onError: (message: string) => void
}) {
  const [busy, setBusy] = useState(false)
  const [sessionID] = useState(() => randomSession("credential-verify"))
  const scope = binding.scope

  async function submit() {
    setBusy(true)
    onError("")
    try {
      const result = await verifyCredentialVersion(session, credential.id, {
        version_id: version.id,
        session_id: sessionID,
        scope,
      })
      onDone(result)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <DialogFrame title={`验证暂存版本 · ${credential.name}`} onClose={onClose}>
      <div className="space-y-3 p-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <ReadOnlyField label="待验证版本" value={fmtTime(version.created_at)} />
          <ReadOnlyField label="目标 Agent" value={`${scope.cluster}/${scope.instance}`} />
          <ReadOnlyField label="用途" value={USE_LABELS[scope.use] || "按凭据类型推断"} />
        </div>
        <p className="text-xs text-muted-foreground">
          {credential.type === "modelProvider"
            ? "将在目标 Agent 上发起一次真实模型探测，验证通过后再点击「激活」才会下发并替换运行中的模型配置。"
            : "将在此目标验证所选版本，不会激活或替换当前版本。"}
        </p>
        <div className="flex justify-end gap-2">
          <Button
            onClick={onClose}
            size="sm"
          >
            取消
          </Button>
          <Button
            onClick={submit}
            disabled={busy || version.state !== "staged" || binding.state !== "active"}
            variant="primary" size="sm"
          >
            {busy ? "验证中" : "验证版本"}
          </Button>
        </div>
      </div>
    </DialogFrame>
  )
}

function BindingSummary({
  resource,
  version,
  disabled,
  busy = false,
  onVerify,
  onRevoke,
}: {
  resource: CredentialResource
  version?: CredentialVersion
  disabled: boolean
  busy?: boolean
  onVerify: (binding: CredentialBinding) => void
  onRevoke: (binding: CredentialBinding) => void
}) {
  if (!resource.bindings?.length) return <span>暂无目标绑定</span>
  return (
    <div className="flex flex-col gap-1">
      {resource.bindings.map((binding) => (
        <div key={binding.id} className="flex flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-foreground">{binding.scope.cluster}/{binding.scope.instance}</span>
            <Badge variant={binding.state === "active" ? "success" : "default"}>
              {stateLabel(binding.state, true)}
            </Badge>
            {binding.scope.use && <span>{USE_LABELS[binding.scope.use]}</span>}
            {binding.scope.namespace && bindingNeedsExplicitNamespace(binding.scope.use) && (
              <span className="font-mono text-2xs">ns:{binding.scope.namespace}</span>
            )}
            {binding.state === "active" && !disabled && version?.state === "staged" && (
              <Button
                onClick={() => onVerify(binding)}
                disabled={busy}
                size="sm"
              >
                验证暂存
              </Button>
            )}
            {binding.state === "active" && !disabled && (
              <Button
                onClick={() => onRevoke(binding)}
                disabled={busy}
                title="解除绑定"
                aria-label="解除绑定"
                variant="danger-outline"
                size="sm"
                icon
              >
                <BanIcon width={12} height={12} />
              </Button>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

function VersionSummary({ versions, selectedVersion, verification, onSelect }: {
  versions: CredentialVersion[]
  selectedVersion?: CredentialVersion
  verification?: VerificationStatus
  onSelect: (versionID: string) => void
}) {
  if (!versions.length) return <span>暂无可见版本</span>
  return (
    <div className="flex flex-wrap items-center gap-2">
      <label className="inline-flex max-w-full items-center gap-2">
        版本
        <Select aria-label="选择版本" className="min-w-0 py-1 text-sm" value={selectedVersion?.id ?? ""} onChange={e => onSelect(e.target.value)}>
          {versions.map((version, i) => (
            <option key={version.id} value={version.id}>
              第 {versions.length - i} 版 · {VERSION_LABELS[version.state]}
            </option>
          ))}
        </Select>
      </label>
      {verification && <span>验证记录：{verification.evidence} 条</span>}
    </div>
  )
}

function CreateCredentialForm({
  session,
  defaultType = "registry",
  isAdmin = false,
  onCancel,
  onCreated,
  onError,
}: {
  session: Session
  defaultType?: CredentialType
  isAdmin?: boolean
  onCancel: () => void
  onCreated: (resource: CredentialResource) => void
  onError: (message: string) => void
}) {
  const [name, setName] = useState("")
  const [type, setType] = useState<CredentialType>(defaultType)
  const [scope, setScope] = useState<CredentialScope>(
    defaultType === "modelProvider" && isAdmin ? "platform" : "personal",
  )
  const scopeTouched = useRef(false)
  const [busy, setBusy] = useState(false)

  function handleTypeChange(nextType: CredentialType) {
    setType(nextType)
    if (!scopeTouched.current) {
      setScope(nextType === "modelProvider" && isAdmin ? "platform" : "personal")
    }
  }

  async function submit() {
    if (!name.trim()) return
    setBusy(true)
    onError("")
    try {
      onCreated(await createCredential(session, { name: name.trim(), scope, type }))
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-2 border-b bg-muted/30 px-4 py-3">
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-muted-foreground">
          资源名称
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={TYPE_NAME_PLACEHOLDERS[type]}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          类型
          <Select
            className="min-w-44"
            value={type}
            onChange={(event) => handleTypeChange(event.target.value as CredentialType)}
          >
            {TYPE_ORDER.map((value) => (
              <option key={value} value={value}>
                {TYPE_LABELS[value]}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          归属
          <Select
            className="min-w-32"
            value={scope}
            onChange={(event) => {
              scopeTouched.current = true
              setScope(event.target.value as CredentialScope)
            }}
          >
            <option value="personal">个人</option>
            <option value="platform">平台</option>
          </Select>
        </label>
        <Button
          onClick={onCancel}
          size="sm"
        >
          取消
        </Button>
        <Button
          onClick={submit}
          disabled={busy || !name.trim()}
          variant="primary" size="sm"
        >
          {busy ? "创建中" : "创建并继续"}
        </Button>
      </div>
      {type === "modelProvider" && (
        <p className="text-xs text-muted-foreground">
          模型服务凭据用于向目标 Agent 下发 AI 助手模型；创建后将自动打开结构化表单填写服务商、模型与 API Key。
        </p>
      )}
    </div>
  )
}

function DialogFrame({
  title,
  children,
  onClose,
}: {
  title: string
  children: React.ReactNode
  onClose: () => void
}) {
  return (
    <Modal title={title} size="lg" onClose={onClose} bodyClassName="p-0">
      {children}
    </Modal>
  )
}

function PayloadDialog({
  session,
  credential,
  onClose,
  onDone,
  onError,
}: {
  session: Session
  credential: CredentialResource
  onClose: () => void
  onDone: (version: CredentialVersion) => void
  onError: (message: string) => void
}) {
  const [mode, setMode] = useState<"form" | "json">("form")
  const [formError, setFormError] = useState("")
  const [rawJson, setRawJson] = useState("")
  const [busy, setBusy] = useState(false)

  // modelProvider fields
  const [providerPreset, setProviderPreset] = useState(MODEL_PROVIDER_PRESETS[0].id)
  const [customProviderId, setCustomProviderId] = useState("")
  const [modelName, setModelName] = useState("")
  const [apiKey, setApiKey] = useState("")
  const [baseURL, setBaseURL] = useState("")

  // registry / git / helm fields
  const [endpoint, setEndpoint] = useState("")
  const [username, setUsername] = useState("")
  const [secretValue, setSecretValue] = useState("")
  const [email, setEmail] = useState("")

  // tls fields
  const [certificate, setCertificate] = useState("")
  const [privateKey, setPrivateKey] = useState("")

  // opaque fields
  const [opaqueEntries, setOpaqueEntries] = useState<OpaquePayloadEntry[]>([{ key: "", value: "" }])

  const activePreset = MODEL_PROVIDER_PRESETS.find((item) => item.id === providerPreset) ?? MODEL_PROVIDER_PRESETS[0]

  function clearSecrets() {
    setRawJson("")
    setApiKey("")
    setSecretValue("")
    setCertificate("")
    setPrivateKey("")
    setOpaqueEntries([{ key: "", value: "" }])
  }

  function buildFormInput(): StructuredCredentialPayloadInput {
    switch (credential.type) {
      case "modelProvider":
        return {
          type: "modelProvider",
          values: {
            providerId: providerPreset === "custom" ? customProviderId : providerPreset,
            modelName,
            apiKey,
            baseURL,
          },
        }
      case "registry":
        return {
          type: "registry",
          values: { server: endpoint, username, password: secretValue, email },
        }
      case "gitToken":
        return {
          type: "gitToken",
          values: { url: endpoint, username, token: secretValue },
        }
      case "helmRepository":
        return {
          type: "helmRepository",
          values: { url: endpoint, username, password: secretValue },
        }
      case "tls":
        return {
          type: "tls",
          values: { certificate, privateKey },
        }
      case "opaque":
        return {
          type: "opaque",
          values: { entries: opaqueEntries },
        }
    }
  }

  async function submit() {
    setFormError("")
    let serialized: string
    if (mode === "json") {
      try {
        JSON.parse(rawJson)
        serialized = rawJson
      } catch {
        setFormError("凭据内容必须是合法 JSON")
        return
      }
    } else {
      try {
        serialized = buildStructuredCredentialPayload(buildFormInput())
      } catch (err) {
        setFormError((err as Error).message)
        return
      }
    }

    setBusy(true)
    onError("")
    try {
      const version = await putCredentialPayload(session, credential.id, serialized)
      clearSecrets()
      onDone(version)
    } catch (err) {
      const message = (err as Error).message
      setFormError(message)
      onError(message)
    } finally {
      clearSecrets()
      setBusy(false)
    }
  }

  function handleClose() {
    clearSecrets()
    onClose()
  }

  return (
    <DialogFrame
      title={`写入新版本 · ${credential.name}（${TYPE_LABELS[credential.type]}）`}
      onClose={handleClose}
    >
      <div className="space-y-3 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="text-xs text-muted-foreground">
            内容只发送一次并加密持久化，提交后不会在页面或 API 中回显。
          </p>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => {
              setFormError("")
              setMode((current) => (current === "form" ? "json" : "form"))
            }}
          >
            {mode === "form" ? "切换到高级 JSON" : "返回结构化表单"}
          </Button>
        </div>

        {formError && (
          <StatusBanner tone="error" compact>
            {formError}
          </StatusBanner>
        )}

        {mode === "json" ? (
          <Textarea
            value={rawJson}
            onChange={(event) => setRawJson(event.target.value)}
            rows={10}
            spellCheck={false}
            autoComplete="off"
            className="w-full resize-y font-mono text-xs"
            placeholder={credentialPayloadPlaceholder(credential.type)}
          />
        ) : credential.type === "modelProvider" ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              模型服务商
              <Select
                value={providerPreset}
                onChange={(event) => setProviderPreset(event.target.value)}
              >
                {MODEL_PROVIDER_PRESETS.map((preset) => (
                  <option key={preset.id} value={preset.id}>
                    {preset.label}
                  </option>
                ))}
              </Select>
            </label>
            {providerPreset === "custom" ? (
              <TextField
                label="自定义服务商标识"
                value={customProviderId}
                onChange={setCustomProviderId}
                placeholder="例如 volcengine / moonshot"
              />
            ) : (
              <TextField
                label="模型名称"
                value={modelName}
                onChange={setModelName}
                placeholder={activePreset.modelPlaceholder}
              />
            )}
            {providerPreset === "custom" && (
              <TextField
                label="模型名称"
                value={modelName}
                onChange={setModelName}
                placeholder={activePreset.modelPlaceholder}
              />
            )}
            <label className="flex flex-col gap-1 text-xs text-muted-foreground sm:col-span-2">
              API Key
              <Input
                type="password"
                autoComplete="new-password"
                spellCheck={false}
                value={apiKey}
                onChange={(event) => setApiKey(event.target.value)}
                placeholder="直接填写密钥值，不要使用 {env:...} 等占位符"
              />
            </label>
            <div className="flex flex-col gap-1 sm:col-span-2">
              <TextField
                label="Base URL（可选）"
                value={baseURL}
                onChange={setBaseURL}
                placeholder={activePreset.baseURLPlaceholder}
              />
              <p className="text-2xs text-muted-foreground">
                留空使用服务商默认接口；如使用兼容代理或自定义端点请填写完整 https:// 地址，高级字段（如 image_model）可切换高级 JSON。
              </p>
            </div>
          </div>
        ) : credential.type === "registry" ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <TextField
              label="镜像仓库地址"
              value={endpoint}
              onChange={setEndpoint}
              placeholder="registry.example.com"
            />
            <TextField
              label="用户名"
              value={username}
              onChange={setUsername}
              placeholder="deploy"
            />
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              密码
              <Input
                type="password"
                autoComplete="new-password"
                value={secretValue}
                onChange={(event) => setSecretValue(event.target.value)}
                placeholder="输入仓库密码或访问令牌"
              />
            </label>
            <TextField
              label="邮箱（可选）"
              value={email}
              onChange={setEmail}
              placeholder="ops@example.com"
            />
          </div>
        ) : credential.type === "gitToken" ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="sm:col-span-2">
              <TextField
                label="Git 仓库地址"
                value={endpoint}
                onChange={setEndpoint}
                placeholder="https://git.example.com/org/repo.git"
              />
            </div>
            <TextField
              label="用户名"
              value={username}
              onChange={setUsername}
              placeholder="git-bot"
            />
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              访问令牌（Token）
              <Input
                type="password"
                autoComplete="new-password"
                value={secretValue}
                onChange={(event) => setSecretValue(event.target.value)}
                placeholder="输入 Personal Access Token"
              />
            </label>
          </div>
        ) : credential.type === "helmRepository" ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="sm:col-span-2">
              <TextField
                label="Helm 仓库地址"
                value={endpoint}
                onChange={setEndpoint}
                placeholder="https://charts.example.com"
              />
            </div>
            <TextField
              label="用户名"
              value={username}
              onChange={setUsername}
              placeholder="helm-bot"
            />
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              密码
              <Input
                type="password"
                autoComplete="new-password"
                value={secretValue}
                onChange={(event) => setSecretValue(event.target.value)}
                placeholder="输入仓库密码"
              />
            </label>
          </div>
        ) : credential.type === "tls" ? (
          <div className="space-y-3">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              TLS 证书（PEM）
              <Textarea
                value={certificate}
                onChange={(event) => setCertificate(event.target.value)}
                rows={4}
                spellCheck={false}
                autoComplete="off"
                className="w-full resize-y font-mono text-xs"
                placeholder="-----BEGIN CERTIFICATE-----"
              />
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              TLS 私钥（PEM）
              <Textarea
                value={privateKey}
                onChange={(event) => setPrivateKey(event.target.value)}
                rows={4}
                spellCheck={false}
                autoComplete="off"
                className="w-full resize-y font-mono text-xs"
                placeholder="-----BEGIN PRIVATE KEY-----"
              />
            </label>
          </div>
        ) : (
          <div className="space-y-2">
            <div className="flex items-center justify-between text-xs text-muted-foreground">
              <span>键值对（数据库连接可填写 connection_uri）</span>
              <Button
                type="button"
                size="sm"
                onClick={() => setOpaqueEntries((current) => [...current, { key: "", value: "" }])}
              >
                <PlusIcon width={12} height={12} /> 添加键值
              </Button>
            </div>
            {opaqueEntries.map((entry, index) => (
              <div key={index} className="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto]">
                <Input
                  value={entry.key}
                  onChange={(event) =>
                    setOpaqueEntries((current) =>
                      current.map((item, idx) => (idx === index ? { ...item, key: event.target.value } : item)),
                    )
                  }
                  placeholder="键名，例如 connection_uri"
                />
                <Input
                  type="password"
                  autoComplete="new-password"
                  value={entry.value}
                  onChange={(event) =>
                    setOpaqueEntries((current) =>
                      current.map((item, idx) => (idx === index ? { ...item, value: event.target.value } : item)),
                    )
                  }
                  placeholder="密钥值（提交后不回显）"
                />
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  disabled={opaqueEntries.length <= 1}
                  onClick={() =>
                    setOpaqueEntries((current) => current.filter((_, idx) => idx !== index))
                  }
                >
                  移除
                </Button>
              </div>
            ))}
          </div>
        )}

        <div className="flex justify-end gap-2">
          <Button type="button" onClick={handleClose} size="sm">
            取消
          </Button>
          <Button
            type="button"
            onClick={submit}
            disabled={busy || (mode === "json" && !rawJson.trim())}
            variant="primary"
            size="sm"
          >
            <SaveIcon width={14} height={14} /> {busy ? "写入中" : "写入暂存版本"}
          </Button>
        </div>
      </div>
    </DialogFrame>
  )
}

function BindingDialog({
  session,
  target,
  credential,
  onClose,
  onDone,
  onError,
}: {
  session: Session
  target: Target
  credential: CredentialResource
  onClose: () => void
  onDone: (binding: CredentialBinding, message: string) => void
  onError: (message: string) => void
}) {
  const [selectedUse, setSelectedUse] = useState<CredentialUse>(TYPE_USES[credential.type])
  const use = selectedUse
  const needsExplicitNamespace = bindingNeedsExplicitNamespace(use)
  const [namespace, setNamespace] = useState(() => defaultBindingNamespace(TYPE_USES[credential.type]))
  const [requiredKeys, setRequiredKeys] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit() {
    let scope: CredentialBindingScope
    try {
      scope = buildBindingScope(target, {
        use,
        namespace: needsExplicitNamespace ? namespace : defaultBindingNamespace(use),
        requiredKeys,
      })
    } catch (err) {
      onError((err as Error).message)
      return
    }
    setBusy(true)
    onError("")
    try {
      const binding = await bindCredentialToTarget(session, credential.id, scope)
      onDone(binding, `已绑定 ${credential.name} 到 ${target.cluster}/${target.instance}`)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <DialogFrame title={`绑定当前目标 · ${credential.name}`} onClose={onClose}>
      <div className="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2">
        <ReadOnlyField label="目标" value={`${target.cluster}/${target.instance}`} />
        {credential.type === "opaque" ? (
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            用途
            <Select value={use} onChange={(event) => setSelectedUse(event.target.value as CredentialUse)}>
              <option value="opaqueSecret">Opaque Secret</option>
              <option value="database">数据库连接</option>
            </Select>
          </label>
        ) : <ReadOnlyField label="用途" value={USE_LABELS[use]} />}
        {needsExplicitNamespace ? (
          <>
            <TextField
              label="Secret namespace"
              value={namespace}
              onChange={setNamespace}
              placeholder="例如 agents-test"
            />
            <p className="text-xs text-muted-foreground sm:col-span-2">
              请填写目标 Agent 已获 Kubernetes RBAC 授权、可创建临时 Secret 的 namespace。Gateway 当前不会提供可写 namespace 列表；不要假设 default 可用。
            </p>
          </>
        ) : (
          <p className="rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground sm:col-span-2">
            模型服务凭据会直接加密下发到目标 Agent 的运行时模型配置（settings.json），不创建 Kubernetes Secret，无需填写 Secret namespace。
          </p>
        )}
        {use === "opaqueSecret" && (
          <div className="flex flex-col gap-1 sm:col-span-2">
            <TextField
              label="required keys（可选，逗号分隔）"
              value={requiredKeys}
              onChange={setRequiredKeys}
              placeholder="例如 DB_URL,DB_PASSWORD"
            />
            <p className="text-xs text-muted-foreground">
              填写后，验证要求版本内容的键集合与之完全相等；不填则按版本内容推断。只填键名，不要填值。
            </p>
          </div>
        )}
        <div className="flex justify-end gap-2 sm:col-span-2">
          <Button type="button" onClick={onClose} size="sm">
            取消
          </Button>
          <Button
            type="button"
            onClick={submit}
            disabled={busy || (needsExplicitNamespace && !namespace.trim())}
            variant="primary" size="sm"
          >
            <ShieldIcon width={14} height={14} /> {busy ? "绑定中" : "确认绑定"}
          </Button>
        </div>
      </div>
    </DialogFrame>
  )
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      <Input
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
      />
    </label>
  )
}

function ReadOnlyField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      <div className="min-h-9 rounded-md border border-input bg-muted/40 px-3 py-2 break-all text-sm text-foreground">{value}</div>
    </div>
  )
}
