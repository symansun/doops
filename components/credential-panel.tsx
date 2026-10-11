"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import type { Session, Target } from "@/lib/client"
import { randomSession } from "@/lib/gateway"
import {
  createCredential,
  grantCredentialToSelf,
  listCredentialGrants,
  listCredentialVersions,
  listCredentials,
  promoteCredentialVersion,
  putCredentialPayload,
  revokeCredentialGrant,
  revokeCredential,
  verifyCredentialVersion,
  type CredentialGrant,
  type CredentialGrantScope,
  type CredentialResource,
  type CredentialScope,
  type CredentialType,
  type CredentialUse,
  type CredentialVerificationResponse,
  type CredentialVersion,
} from "@/lib/credentials"
import {
  BanIcon,
  CheckIcon,
  KeyIcon,
  PlusIcon,
  RefreshIcon,
  SaveIcon,
  ShieldIcon,
} from "./icons"

const TYPE_LABELS: Record<CredentialType, string> = {
  registry: "OCI Registry",
  tls: "TLS",
  opaque: "Opaque Secret",
  helmRepository: "Helm Repository",
  gitToken: "Git Token",
}

const TYPE_USES: Record<CredentialType, CredentialUse> = {
  registry: "imagePull",
  tls: "tls",
  opaque: "opaqueSecret",
  helmRepository: "helmPull",
  gitToken: "gitCheckout",
}

type Action =
  | { kind: "payload"; credential: CredentialResource }
  | { kind: "grant"; credential: CredentialResource }
  | { kind: "version"; credential: CredentialResource; version: CredentialVersion }
  | {
      kind: "verify"
      credential: CredentialResource
      version: CredentialVersion
      grant: CredentialGrant
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

export function CredentialPanel({ session, target }: { session: Session; target: Target }) {
  const [view, setView] = useState<"target" | "managed">("target")
  const [rows, setRows] = useState<CredentialResource[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [showCreate, setShowCreate] = useState(false)
  const [action, setAction] = useState<Action>(null)
  const [versionsByCredential, setVersionsByCredential] = useState<
    Record<string, CredentialVersion[]>
  >({})
  const [selectedVersionIDs, setSelectedVersionIDs] = useState<Record<string, string>>({})
  const [verificationStatuses, setVerificationStatuses] = useState<
    Record<string, VerificationStatus>
  >({})

  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      let nextRows: CredentialResource[]
      const versionEntries: Array<[string, CredentialVersion[]]> = []
      if (session.demo) {
        nextRows = await listCredentials(session, target, view)
      } else if (view === "managed") {
        const managedRows = await listCredentials(session, target, "managed")
        nextRows = await Promise.all(
          managedRows.map(async (resource) => {
            const [versions, grants] = await Promise.all([
              listCredentialVersions(session, resource.id),
              listCredentialGrants(session, resource.id),
            ])
            versionEntries.push([resource.id, versions])
            return { ...resource, grants }
          }),
        )
      } else {
        // Target visibility is authoritative for operators. Do not load the
        // management view here: its owner/admin boundary is independent and
        // must not erase a successful target credential response.
        nextRows = await listCredentials(session, target, "target")
      }
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
      setRows([])
      setVersionsByCredential({})
      setSelectedVersionIDs({})
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }, [session, target, view])

  useEffect(() => {
    load()
  }, [load])

  function completed(message: string) {
    setNotice(message)
    window.setTimeout(() => setNotice(""), 4000)
  }

  async function revoke(resource: CredentialResource) {
    if (!window.confirm(`确认吊销凭据「${resource.name}」？已物化资源将进入清理流程。`)) return
    setError("")
    try {
      await revokeCredential(session, resource.id)
      completed(`已吊销 ${resource.name}`)
      await load()
    } catch (err) {
      setError((err as Error).message)
    }
  }

  async function revokeGrant(resource: CredentialResource, grant: CredentialGrant) {
    if (
      !window.confirm(
        `确认吊销授权「${grant.scope.namespace} · ${grant.scope.workload.name}」？已物化资源将进入清理流程。`,
      )
    ) {
      return
    }
    setError("")
    try {
      await revokeCredentialGrant(session, resource.id, grant.id)
      completed(`已吊销 ${resource.name} 的一条授权`)
      await load()
    } catch (err) {
      setError((err as Error).message)
    }
  }

  async function promote(resource: CredentialResource, version: CredentialVersion) {
    if (version.state !== "staged") return
    if (!window.confirm(`确认激活暂存版本 ${version.id}？Gateway 会重新检查全部 active grant。`)) {
      return
    }
    setError("")
    try {
      await promoteCredentialVersion(session, resource.id, version.id)
      setAction(null)
      await load()
      completed(`已激活 ${resource.name} 的版本 ${version.id}`)
    } catch (err) {
      setError((err as Error).message)
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

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b bg-card px-4 py-3">
        <div>
          <div className="flex items-center gap-2">
            <KeyIcon width={17} height={17} className="text-primary" />
            <h2 className="text-sm font-semibold text-foreground">凭据资源</h2>
          </div>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {target.cluster}/{target.instance} · 操作权限包含凭据使用权限
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="inline-flex rounded-md border bg-background p-0.5 text-xs">
            <button
              onClick={() => setView("target")}
              className={`rounded px-2.5 py-1.5 ${view === "target" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
            >
              当前目标可用
            </button>
            <button
              onClick={() => setView("managed")}
              className={`rounded px-2.5 py-1.5 ${view === "managed" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
            >
              我管理的
            </button>
          </div>
          <button
            onClick={load}
            disabled={loading}
            title="刷新凭据"
            className="flex h-8 w-8 items-center justify-center rounded-md border text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
          >
            <RefreshIcon width={15} height={15} className={loading ? "animate-spin" : ""} />
          </button>
          <button
            onClick={() => {
              setView("managed")
              setShowCreate(true)
            }}
            disabled={session.demo}
            title={session.demo ? "演示模式不执行凭据写操作" : "新建凭据"}
            className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            <PlusIcon width={14} height={14} /> 新建凭据
          </button>
        </div>
      </div>

      {error && (
        <div className="border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-xs text-destructive">
          {error}
        </div>
      )}
      {notice && (
        <div className="flex items-center gap-2 border-b border-success/30 bg-success/10 px-4 py-2 text-xs text-success">
          <CheckIcon width={14} height={14} /> {notice}
        </div>
      )}
      {showCreate && (
        <CreateCredentialForm
          session={session}
          onCancel={() => setShowCreate(false)}
          onCreated={async (resource) => {
            setShowCreate(false)
            completed(`已创建 ${resource.name}`)
            await load()
          }}
          onError={setError}
        />
      )}

      <div className="min-h-0 flex-1 overflow-auto">
        <table className="w-full min-w-[760px] border-collapse text-sm">
          <thead className="sticky top-0 z-10 bg-card">
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-4 py-2.5 font-medium">名称</th>
              <th className="px-3 py-2.5 font-medium">类型</th>
              <th className="px-3 py-2.5 font-medium">归属</th>
              <th className="px-3 py-2.5 font-medium">状态</th>
              <th className="px-3 py-2.5 font-medium">
                {view === "target" ? "目标绑定" : "版本 / 目标绑定"}
              </th>
              <th className="px-4 py-2.5 text-right font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((resource) => {
              const versions = versionsByCredential[resource.id] ?? []
              const version = versions.find(
                (candidate) => candidate.id === selectedVersionIDs[resource.id],
              )
              const verification = version
                ? verificationStatuses[`${resource.id}:${version.id}`]
                : undefined
              return (
              <tr key={resource.id} className="border-b border-border/60 align-top">
                <td className="px-4 py-3">
                  <div className="font-medium text-foreground">{resource.name}</div>
                  <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">{resource.id}</div>
                </td>
                <td className="px-3 py-3 text-xs text-foreground">{TYPE_LABELS[resource.type]}</td>
                <td className="px-3 py-3 text-xs text-muted-foreground">
                  {resource.scope === "platform" ? "平台" : "个人"}
                </td>
                <td className="px-3 py-3">
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${
                      resource.state === "active"
                        ? "bg-success/15 text-success"
                        : "bg-muted text-muted-foreground"
                    }`}
                  >
                    {resource.state}
                  </span>
                </td>
                <td className="max-w-md px-3 py-3 text-xs text-muted-foreground">
                  {view === "target" ? (
                    <div className="flex flex-col gap-2">
                      <GrantSummary
                        resource={resource}
                        version={version}
                        disabled
                        onVerify={(grant) =>
                          version &&
                          setAction({ kind: "verify", credential: resource, version, grant })
                        }
                        onRevoke={(grant) => revokeGrant(resource, grant)}
                      />
                      {versions.length > 0 && (
                        <VersionSummary
                          updatedAt={resource.updated_at}
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
                        updatedAt={resource.updated_at}
                        versions={versions}
                        selectedVersion={version}
                        verification={verification}
                        onSelect={(versionID) => selectVersion(resource.id, versionID)}
                      />
                      <GrantSummary
                        resource={resource}
                        version={version}
                        disabled={Boolean(session.demo)}
                        onVerify={(grant) =>
                          version &&
                          setAction({ kind: "verify", credential: resource, version, grant })
                        }
                        onRevoke={(grant) => revokeGrant(resource, grant)}
                      />
                    </div>
                  )}
                </td>
                <td className="px-4 py-3">
                  <div className="flex justify-end gap-1.5">
                    {view === "managed" && resource.state === "active" && !session.demo && (
                      <>
                        <button
                          onClick={() => setAction({ kind: "payload", credential: resource })}
                          className="rounded-md border px-2 py-1 text-xs text-foreground hover:bg-muted"
                        >
                          写入版本
                        </button>
                        {version && (
                          <>
                            <button
                              onClick={() => setAction({ kind: "version", credential: resource, version })}
                              className="rounded-md border px-2 py-1 text-xs text-foreground hover:bg-muted"
                            >
                              查看版本
                            </button>
                            {version.state === "staged" && (
                              <>
                                <button
                                  onClick={() => promote(resource, version)}
                                  className="rounded-md bg-primary px-2 py-1 text-xs font-medium text-primary-foreground hover:opacity-90"
                                >
                                  激活
                                </button>
                              </>
                            )}
                          </>
                        )}
                        <button
                          onClick={() => setAction({ kind: "grant", credential: resource })}
                          className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-foreground hover:bg-muted"
                        >
                          <ShieldIcon width={12} height={12} /> 授权
                        </button>
                        <button
                          onClick={() => revoke(resource)}
                          title="吊销凭据"
                          className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/15 hover:text-destructive"
                        >
                          <BanIcon width={14} height={14} />
                        </button>
                      </>
                    )}
                  </div>
                </td>
              </tr>
              )
            })}
            {!rows.length && !loading && (
              <tr>
                <td colSpan={6} className="px-4 py-16 text-center text-sm text-muted-foreground">
                  {error
                    ? "凭据加载失败，请查看错误并刷新"
                    : view === "target"
                    ? "当前目标没有已配置的可用凭据"
                    : "当前账号没有可管理的凭据"}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {action?.kind === "payload" && (
        <PayloadDialog
          session={session}
          credential={action.credential}
          onClose={() => setAction(null)}
          onDone={(version) => {
            rememberVersion(action.credential.id, version)
            setAction(null)
            completed(`已写入暂存版本 ${version.id}`)
          }}
          onError={setError}
        />
      )}
      {action?.kind === "grant" && (
        <GrantDialog
          session={session}
          target={target}
          credential={action.credential}
          onClose={() => setAction(null)}
          onDone={async (message) => {
            setAction(null)
            completed(message)
            await load()
          }}
          onError={setError}
        />
      )}
      {action?.kind === "version" && (
        <VersionDialog
          credential={action.credential}
          version={action.version}
          verification={verificationStatuses[`${action.credential.id}:${action.version.id}`]}
          onClose={() => setAction(null)}
          onPromote={() => promote(action.credential, action.version)}
        />
      )}
      {action?.kind === "verify" && (
        <VerifyDialog
          session={session}
          credential={action.credential}
          version={action.version}
          grant={action.grant}
          onClose={() => setAction(null)}
          onDone={(result) => {
            rememberVerification(action.credential, action.version, result)
            setAction(null)
            completed(
              `版本 ${action.version.id} 已验证，返回 ${result.evidence.length} 条 metadata evidence`,
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
  verification,
  onClose,
  onPromote,
}: {
  credential: CredentialResource
  version: CredentialVersion
  verification?: VerificationStatus
  onClose: () => void
  onPromote: () => void
}) {
  return (
    <DialogFrame title={`凭据版本 · ${credential.name}`} onClose={onClose}>
      <div className="space-y-3 p-4 text-xs">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <ReadOnlyField label="Version ID" value={version.id} />
          <ReadOnlyField label="状态" value={version.state} />
          <ReadOnlyField label="Payload digest" value={version.payload_digest} />
          <ReadOnlyField label="创建时间" value={fmtTime(version.created_at)} />
          <ReadOnlyField label="激活时间" value={fmtTime(version.promoted_at)} />
        </div>
        {verification && (
          <div className="rounded-md border border-success/30 bg-success/10 px-3 py-2 text-success">
            已验证：{verification.status} · {verification.evidence} 条 metadata evidence
          </div>
        )}
        <p className="text-muted-foreground">
          页面只保存 Gateway 返回的版本元数据；payload 不会进入页面状态、任务上下文或公开请求结果。
        </p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-md border px-3 py-2 text-xs text-foreground hover:bg-muted"
          >
            关闭
          </button>
          {version.state === "staged" && (
            <button
              onClick={onPromote}
              className="rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground hover:opacity-90"
            >
              激活版本
            </button>
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
  grant,
  onClose,
  onDone,
  onError,
}: {
  session: Session
  credential: CredentialResource
  version: CredentialVersion
  grant: CredentialGrant
  onClose: () => void
  onDone: (result: CredentialVerificationResponse) => void
  onError: (message: string) => void
}) {
  const [busy, setBusy] = useState(false)
  const [sessionID] = useState(() => randomSession("credential-verify"))
  const scope = grant.scope

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
          <ReadOnlyField label="Version ID" value={version.id} />
          <ReadOnlyField label="Grant ID" value={grant.id} />
          <ReadOnlyField label="Cluster / instance" value={`${scope.cluster}/${scope.instance}`} />
          <ReadOnlyField label="Namespace" value={scope.namespace} />
          <ReadOnlyField label="Use" value={scope.use} />
          <ReadOnlyField
            label="Workload"
            value={`${scope.workload.kind}/${scope.workload.name}`}
          />
          {scope.image_repository && (
            <ReadOnlyField label="Image repository" value={scope.image_repository} />
          )}
          {scope.image_digest && <ReadOnlyField label="Image digest" value={scope.image_digest} />}
          {scope.helm_release && <ReadOnlyField label="Helm release" value={scope.helm_release} />}
          {scope.helm_chart && <ReadOnlyField label="Helm chart" value={scope.helm_chart} />}
          {scope.required_keys?.length ? (
            <ReadOnlyField label="Required keys" value={scope.required_keys.join(", ")} />
          ) : null}
        </div>
        <p className="text-xs text-muted-foreground">
          将按该 grant 的完整精确 scope 验证 staged version；返回内容仅为状态和 metadata evidence。
        </p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-md border px-3 py-2 text-xs text-foreground hover:bg-muted"
          >
            取消
          </button>
          <button
            onClick={submit}
            disabled={busy || version.state !== "staged" || grant.state !== "active"}
            className="rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
          >
            {busy ? "验证中" : "验证完整 scope"}
          </button>
        </div>
      </div>
    </DialogFrame>
  )
}

function GrantSummary({
  resource,
  version,
  disabled,
  onVerify,
  onRevoke,
}: {
  resource: CredentialResource
  version?: CredentialVersion
  disabled: boolean
  onVerify: (grant: CredentialGrant) => void
  onRevoke: (grant: CredentialGrant) => void
}) {
  if (!resource.grants?.length) return <span>-</span>
  return (
    <div className="flex flex-col gap-1">
      {resource.grants.map((grant) => (
        <div key={grant.id} className="flex flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="font-mono text-[11px] text-muted-foreground">{grant.id}</span>
            <span
              className={`rounded px-1.5 py-0.5 ${
                grant.state === "active"
                  ? "bg-success/15 text-success"
                  : "bg-muted text-muted-foreground"
              }`}
            >
              {grant.state}
            </span>
            {grant.state === "active" && !disabled && version?.state === "staged" && (
              <button
                onClick={() => onVerify(grant)}
                className="rounded border px-1.5 py-0.5 text-[11px] text-foreground hover:bg-muted"
              >
                验证暂存
              </button>
            )}
            {grant.state === "active" && !disabled && (
              <button
                onClick={() => onRevoke(grant)}
                title="吊销授权"
                className="flex h-6 w-6 items-center justify-center rounded text-muted-foreground hover:bg-destructive/15 hover:text-destructive"
              >
                <BanIcon width={12} height={12} />
              </button>
            )}
          </div>
          <div>
            {grant.scope.cluster}/{grant.scope.instance} · ns/{grant.scope.namespace} ·{" "}
            {grant.scope.use}
          </div>
          <div>
            workload {grant.scope.workload.kind}/{grant.scope.workload.name}
          </div>
          {grant.scope.image_repository && <div>image {grant.scope.image_repository}</div>}
          {grant.scope.image_digest && (
            <div className="font-mono text-[11px]">digest {grant.scope.image_digest}</div>
          )}
          {grant.scope.helm_release && <div>helm release {grant.scope.helm_release}</div>}
          {grant.scope.helm_chart && <div>helm chart {grant.scope.helm_chart}</div>}
          {grant.scope.required_keys?.length ? (
            <div>required keys {grant.scope.required_keys.join(", ")}</div>
          ) : null}
        </div>
      ))}
    </div>
  )
}

function VersionSummary({
  updatedAt,
  versions,
  selectedVersion,
  verification,
  onSelect,
}: {
  updatedAt?: string
  versions: CredentialVersion[]
  selectedVersion?: CredentialVersion
  verification?: VerificationStatus
  onSelect: (versionID: string) => void
}) {
  return (
    <div className="flex flex-col gap-1">
      <span>更新时间：{fmtTime(updatedAt)}</span>
      {versions.length > 0 ? (
        <>
          <label className="flex flex-wrap items-center gap-1.5">
            <span>版本</span>
            <select
              className="input min-w-48 py-1 text-[11px]"
              value={selectedVersion?.id ?? ""}
              onChange={(event) => onSelect(event.target.value)}
            >
              {versions.map((version) => (
                <option key={version.id} value={version.id}>
                  {version.state} · {version.id}
                </option>
              ))}
            </select>
          </label>
          {selectedVersion ? (
            <>
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="rounded bg-muted px-1.5 py-0.5 text-foreground">
                  {selectedVersion.state}
                </span>
                {verification && (
                  <span className="rounded bg-success/15 px-1.5 py-0.5 text-success">
                    {verification.status} · {verification.evidence} 条证据
                  </span>
                )}
              </span>
              <span className="font-mono text-[11px] text-muted-foreground">
                {selectedVersion.id}
              </span>
              <span className="font-mono text-[11px] text-muted-foreground">
                digest {selectedVersion.payload_digest}
              </span>
            </>
          ) : (
            <span className="text-muted-foreground">请选择一个版本</span>
          )}
        </>
      ) : (
        <span className="text-muted-foreground">当前资源没有可见版本</span>
      )}
    </div>
  )
}

function CreateCredentialForm({
  session,
  onCancel,
  onCreated,
  onError,
}: {
  session: Session
  onCancel: () => void
  onCreated: (resource: CredentialResource) => void
  onError: (message: string) => void
}) {
  const [name, setName] = useState("")
  const [scope, setScope] = useState<CredentialScope>("personal")
  const [type, setType] = useState<CredentialType>("registry")
  const [busy, setBusy] = useState(false)

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
    <div className="flex flex-wrap items-end gap-3 border-b bg-muted/30 px-4 py-3">
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-muted-foreground">
        资源名称
        <input
          className="input"
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder="registry-oilan"
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        归属
        <select
          className="input min-w-32"
          value={scope}
          onChange={(event) => setScope(event.target.value as CredentialScope)}
        >
          <option value="personal">个人</option>
          <option value="platform">平台</option>
        </select>
      </label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        类型
        <select
          className="input min-w-44"
          value={type}
          onChange={(event) => setType(event.target.value as CredentialType)}
        >
          {Object.entries(TYPE_LABELS).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
      </label>
      <button
        onClick={onCancel}
        className="rounded-md border px-3 py-2 text-xs text-foreground hover:bg-muted"
      >
        取消
      </button>
      <button
        onClick={submit}
        disabled={busy || !name.trim()}
        className="rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
      >
        {busy ? "创建中" : "创建"}
      </button>
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
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
    >
      <div className="max-h-[90dvh] w-full max-w-xl overflow-auto rounded-lg border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          <button
            onClick={onClose}
            className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
          >
            关闭
          </button>
        </div>
        {children}
      </div>
    </div>
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
  const [payload, setPayload] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit() {
    try {
      JSON.parse(payload)
    } catch {
      onError("凭据内容必须是合法 JSON")
      return
    }
    setBusy(true)
    onError("")
    try {
      const version = await putCredentialPayload(session, credential.id, payload)
      setPayload("")
      onDone(version)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setPayload("")
      setBusy(false)
    }
  }

  return (
    <DialogFrame
      title={`写入新版本 · ${credential.name}`}
      onClose={() => {
        setPayload("")
        onClose()
      }}
    >
      <div className="space-y-3 p-4">
        <p className="text-xs text-muted-foreground">
          内容只发送一次并加密持久化，提交后不会在页面或 API 中回显。
        </p>
        <textarea
          value={payload}
          onChange={(event) => setPayload(event.target.value)}
          rows={9}
          spellCheck={false}
          autoComplete="off"
          className="input w-full resize-y font-mono text-xs"
          placeholder='{"server":"registry.example.com","username":"deploy","password":"..."}'
        />
        <div className="flex justify-end">
          <button
            onClick={submit}
            disabled={busy || !payload.trim()}
            className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
          >
            <SaveIcon width={14} height={14} /> {busy ? "写入中" : "写入暂存版本"}
          </button>
        </div>
      </div>
    </DialogFrame>
  )
}

function GrantDialog({
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
  onDone: (message: string) => void
  onError: (message: string) => void
}) {
  const [selectedUse, setSelectedUse] = useState<CredentialUse>(TYPE_USES[credential.type])
  const use = selectedUse
  const [namespace, setNamespace] = useState("default")
  const [workloadKind, setWorkloadKind] = useState("deployment")
  const [workloadName, setWorkloadName] = useState("")
  const [imageRepository, setImageRepository] = useState("")
  const [imageDigest, setImageDigest] = useState("")
  const [helmRelease, setHelmRelease] = useState("")
  const [helmChart, setHelmChart] = useState("")
  const [requiredKeys, setRequiredKeys] = useState("")
  const [databaseAssetID, setDatabaseAssetID] = useState("")
  const [environmentID, setEnvironmentID] = useState("")
  const [databaseEngine, setDatabaseEngine] = useState<"mysql" | "postgres" | "mongodb">("postgres")
  const [databaseName, setDatabaseName] = useState("")
  const [busy, setBusy] = useState(false)
  const requiredKeyList = useMemo(
    () =>
      requiredKeys
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean),
    [requiredKeys],
  )
  const scopeError = useMemo(() => {
    if (!namespace.trim() || !workloadName.trim()) return "Namespace 和 workload name 必填"
    if (use === "imagePull") {
      if (!imageRepository.trim()) return "Image repository 必填"
      if (!/^sha256:[0-9a-f]{64}$/.test(imageDigest.trim())) {
        return "Image digest 必须是 sha256: 加 64 位小写十六进制"
      }
    }
    if (use === "helmPull") {
      if (!helmRelease.trim()) return "Helm release 必填"
      if (!/^https:\/\/\S+$/.test(helmChart.trim())) {
        return "Helm chart 必须是明确的 HTTPS URL"
      }
    }
    if (use === "opaqueSecret" && requiredKeyList.length === 0) {
      return "Required keys 至少填写一项"
    }
    if (use === "database") {
      if (!databaseAssetID.trim() || !environmentID.trim() || !databaseName.trim()) {
        return "数据库资产、环境和数据库名必填"
      }
    }
    return ""
  }, [
    helmChart,
    helmRelease,
    imageDigest,
    imageRepository,
    namespace,
    requiredKeyList,
    databaseAssetID,
    databaseName,
    environmentID,
    use,
    workloadName,
  ])

  async function submit() {
    if (scopeError) {
      onError(scopeError)
      return
    }
    const scope: CredentialGrantScope = {
      cluster: target.cluster,
      instance: target.instance,
      namespace: namespace.trim(),
      use,
      workload: { kind: workloadKind, name: workloadName.trim() },
    }
    if (credential.type === "opaque") {
      scope.use = use
    }
    if (use === "imagePull") {
      scope.image_repository = imageRepository.trim()
      scope.image_digest = imageDigest.trim()
    } else if (use === "helmPull") {
      scope.helm_release = helmRelease.trim()
      scope.helm_chart = helmChart.trim()
    } else if (use === "opaqueSecret") {
      scope.required_keys = requiredKeyList
    } else if (use === "database") {
      scope.database_asset_id = databaseAssetID.trim()
      scope.environment_id = environmentID.trim()
      scope.database_engine = databaseEngine
      scope.database_name = databaseName.trim()
      scope.required_keys = databaseEngine === "mysql"
        ? ["SPRING_DATASOURCE_URL", "SPRING_DATASOURCE_USERNAME", "SPRING_DATASOURCE_PASSWORD"]
        : ["connection_uri"]
    }
    setBusy(true)
    onError("")
    try {
      await grantCredentialToSelf(session, credential.id, scope)
      onDone(`已授权 ${credential.name} 到 ${target.cluster}/${target.instance}`)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <DialogFrame title={`授权当前目标 · ${credential.name}`} onClose={onClose}>
      <div className="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2">
        <ReadOnlyField label="目标" value={`${target.cluster}/${target.instance}`} />
        {credential.type === "opaque" ? (
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            用途
            <select className="input" value={use} onChange={(event) => setSelectedUse(event.target.value as CredentialUse)}>
              <option value="opaqueSecret">Opaque Secret</option>
              <option value="database">数据库连接</option>
            </select>
          </label>
        ) : <ReadOnlyField label="用途" value={use} />}
        <TextField label="Namespace" value={namespace} onChange={setNamespace} />
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Workload kind
          <select
            className="input"
            value={workloadKind}
            onChange={(event) => setWorkloadKind(event.target.value)}
          >
            <option value="deployment">Deployment</option>
            <option value="statefulset">StatefulSet</option>
            <option value="daemonset">DaemonSet</option>
          </select>
        </label>
        <TextField label="Workload name" value={workloadName} onChange={setWorkloadName} />
        {use === "imagePull" && (
          <>
            <TextField
              label="Image repository"
              value={imageRepository}
              onChange={setImageRepository}
            />
            <TextField
              label="Image digest"
              value={imageDigest}
              onChange={setImageDigest}
              placeholder="sha256:..."
            />
          </>
        )}
        {use === "helmPull" && (
          <>
            <TextField label="Helm release" value={helmRelease} onChange={setHelmRelease} />
            <TextField label="Helm chart" value={helmChart} onChange={setHelmChart} />
          </>
        )}
        {use === "opaqueSecret" && (
          <TextField
            label="Required keys"
            value={requiredKeys}
            onChange={setRequiredKeys}
            placeholder="username,password"
          />
        )}
        {use === "database" && (
          <>
            <TextField label="数据库资产 ID" value={databaseAssetID} onChange={setDatabaseAssetID} />
            <TextField label="环境 ID" value={environmentID} onChange={setEnvironmentID} />
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              数据库引擎
              <select className="input" value={databaseEngine} onChange={(event) => setDatabaseEngine(event.target.value as typeof databaseEngine)}>
                <option value="postgres">PostgreSQL</option>
                <option value="mysql">MySQL</option>
                <option value="mongodb">MongoDB</option>
              </select>
            </label>
            <TextField label="数据库名" value={databaseName} onChange={setDatabaseName} />
            <p className="text-xs text-muted-foreground sm:col-span-2">
              受管凭据 payload 的 keys：{databaseEngine === "mysql" ? "SPRING_DATASOURCE_URL、SPRING_DATASOURCE_USERNAME、SPRING_DATASOURCE_PASSWORD" : "connection_uri"}
            </p>
          </>
        )}
        {scopeError && <p className="text-xs text-destructive sm:col-span-2">{scopeError}</p>}
        <div className="flex justify-end sm:col-span-2">
          <button
            onClick={submit}
            disabled={busy || Boolean(scopeError)}
            className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-50"
          >
            <ShieldIcon width={14} height={14} /> {busy ? "授权中" : "确认授权"}
          </button>
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
      <input
        className="input"
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
      <div className="input font-mono text-xs text-foreground">{value}</div>
    </div>
  )
}
