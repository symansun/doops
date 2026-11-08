import type { AdminInstance } from "./admin"

// Gateway stores a blank cluster or instance as "*". The form therefore submits
// only an explicit choice.
export const GRANT_ALL = "*"

export interface GrantScopeCluster {
  name: string
  instances: string[]
}

export interface GrantFormInput {
  userId: string
  cluster: string
  instance: string
}

export type GrantFormResult =
  | { ok: true; body: { user_id: string; cluster: string; instance: string } }
  | { ok: false; error: string }

// Turn the Gateway instance list into selectable clusters and instances.
export function grantScopeOptions(instances: AdminInstance[]): GrantScopeCluster[] {
  const byCluster = new Map<string, Set<string>>()
  for (const row of instances) {
    const cluster = (row.cluster || "").trim()
    const instance = (row.instance || "").trim()
    if (!cluster || !instance || cluster === GRANT_ALL || instance === GRANT_ALL) continue
    const names = byCluster.get(cluster) || new Set<string>()
    names.add(instance)
    byCluster.set(cluster, names)
  }
  return [...byCluster.entries()]
    .map(([name, names]) => ({ name, instances: [...names].sort((a, b) => a.localeCompare(b)) }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

// Choosing every cluster only allows every instance. Otherwise the choices are
// the instances registered in that cluster.
export function grantInstanceChoices(scope: GrantScopeCluster[], cluster: string): string[] {
  if (cluster === GRANT_ALL) return []
  return scope.find((item) => item.name === cluster)?.instances || []
}

export function validateGrantForm(input: GrantFormInput, scope: GrantScopeCluster[]): GrantFormResult {
  const userId = input.userId.trim()
  const cluster = input.cluster.trim()
  const instance = input.instance.trim()
  if (!userId) return { ok: false, error: "请选择用户" }
  if (!cluster) return { ok: false, error: "请选择集群" }
  if (!instance) return { ok: false, error: "请选择实例" }
  if (cluster === GRANT_ALL && instance !== GRANT_ALL) {
    return { ok: false, error: "选择全部集群时，实例只能选全部" }
  }
  if (cluster !== GRANT_ALL && !scope.some((item) => item.name === cluster)) {
    return { ok: false, error: "所选集群不在实例列表中，请刷新后重新选择" }
  }
  if (instance !== GRANT_ALL && !grantInstanceChoices(scope, cluster).includes(instance)) {
    return { ok: false, error: "所选实例不在该集群中，请重新选择" }
  }
  return { ok: true, body: { user_id: userId, cluster, instance } }
}

// A wildcard is allowed only when the operator picks it, and the form says so
// before submission.
export function grantScopeWarning(cluster: string, instance: string): string {
  if (cluster.trim() === GRANT_ALL) {
    return "将授予该用户全部集群/实例（cluster=*、instance=*），包括以后接入的机器和它们绑定的凭据。"
  }
  if (cluster.trim() && instance.trim() === GRANT_ALL) {
    return `将授予该用户集群 ${cluster.trim()} 的全部实例（instance=*），包括该集群以后接入的机器和它们绑定的凭据。`
  }
  return ""
}
