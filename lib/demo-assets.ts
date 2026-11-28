"use client"

import type { AssetEndpoint } from "./assets"

const now = () => Date.now()
const isoAgo = (minutes: number) => new Date(now() - minutes * 60_000).toISOString()

type DemoMachine = {
  asset_id: string
  name: string
  kind: "physical" | "vm"
  address: string
  cpu_count: number
  memory_gib: number
  disk_gib: number
  host_asset_id?: string
  cluster?: string
  instance?: string
  created_at?: string
  last_seen?: string
  last_sample_ms?: number
  tags: string[]
  revision: number
  login_accounts: { username: string; credential_id: string }[]
}

const demoMachines: DemoMachine[] = [
  {
    asset_id: "phy-host-01",
    name: "核心虚拟化宿主机 A01",
    kind: "physical",
    address: "10.0.0.10",
    cpu_count: 64,
    memory_gib: 256,
    disk_gib: 4000,
    cluster: "prod-cn",
    instance: "web-01",
    last_seen: isoAgo(1),
    last_sample_ms: now() - 45_000,
    tags: ["env=prod", "rack=A01", "role=hypervisor"],
    revision: 3,
    login_accounts: [{ username: "ops", credential_id: "cred_ssh_ops01" }],
  },
  {
    asset_id: "phy-gpu-01",
    name: "科研算力 GPU 物理机 G01",
    kind: "physical",
    address: "10.0.0.20",
    cpu_count: 64,
    memory_gib: 512,
    disk_gib: 8000,
    cluster: "prod-cn",
    instance: "web-02",
    last_seen: isoAgo(1),
    last_sample_ms: now() - 35_000,
    tags: ["env=prod", "rack=G01", "gpu=a100", "dept=research"],
    revision: 2,
    login_accounts: [{ username: "ops", credential_id: "cred_ssh_gpu01" }],
  },
  {
    asset_id: "vm-web-01",
    name: "教务门户节点 01",
    kind: "vm",
    address: "10.0.1.11",
    cpu_count: 8,
    memory_gib: 16,
    disk_gib: 100,
    host_asset_id: "phy-host-01",
    cluster: "prod-cn",
    instance: "web-01",
    last_seen: isoAgo(0.5),
    last_sample_ms: now() - 25_000,
    tags: ["env=prod", "dept=edu", "role=web"],
    revision: 4,
    login_accounts: [],
  },
  {
    asset_id: "vm-web-02",
    name: "核心应用与容器节点 02",
    kind: "vm",
    address: "10.0.1.12",
    cpu_count: 16,
    memory_gib: 32,
    disk_gib: 200,
    host_asset_id: "phy-host-01",
    cluster: "prod-cn",
    instance: "web-02",
    last_seen: isoAgo(0.5),
    last_sample_ms: now() - 30_000,
    tags: ["env=prod", "dept=edu", "role=k8s-node"],
    revision: 5,
    login_accounts: [],
  },
  {
    asset_id: "vm-db-01",
    name: "数据库主节点 01",
    kind: "vm",
    address: "10.0.2.21",
    cpu_count: 16,
    memory_gib: 64,
    disk_gib: 1000,
    host_asset_id: "phy-host-01",
    cluster: "staging",
    instance: "db-01",
    last_seen: isoAgo(1),
    last_sample_ms: now() - 40_000,
    tags: ["env=staging", "dept=finance", "role=db"],
    revision: 3,
    login_accounts: [],
  },
  {
    asset_id: "ssh-legacy-01",
    name: "教务老旧业务机 (SSH)",
    kind: "vm",
    address: "10.0.3.41",
    cpu_count: 4,
    memory_gib: 8,
    disk_gib: 100,
    host_asset_id: "phy-host-01",
    cluster: "prod-cn",
    instance: "web-01",
    last_seen: isoAgo(2),
    last_sample_ms: now() - 90_000,
    tags: ["env=prod", "dept=edu", "via=ssh"],
    revision: 2,
    login_accounts: [{ username: "monitor", credential_id: "cred_ssh_legacy" }],
  },
  {
    asset_id: "ssh-archive-02",
    name: "财务归档服务器 (SSH)",
    kind: "vm",
    address: "10.0.3.42",
    cpu_count: 8,
    memory_gib: 16,
    disk_gib: 500,
    host_asset_id: "phy-host-01",
    cluster: "prod-cn",
    instance: "web-01",
    last_seen: isoAgo(2),
    last_sample_ms: now() - 110_000,
    tags: ["env=prod", "dept=finance", "via=ssh"],
    revision: 1,
    login_accounts: [{ username: "monitor", credential_id: "cred_ssh_archive" }],
  },
  {
    asset_id: "vm-music-01",
    name: "智慧琴房与音视频节点 01",
    kind: "vm",
    address: "10.0.1.18",
    cpu_count: 16,
    memory_gib: 32,
    disk_gib: 500,
    host_asset_id: "phy-host-01",
    cluster: "prod-cn",
    instance: "web-01",
    last_seen: isoAgo(0.5),
    last_sample_ms: now() - 20_000,
    tags: ["env=prod", "dept=music", "role=media"],
    revision: 2,
    login_accounts: [],
  },
]

const demoClusters = [
  {
    asset_id: "k8s-prod-cn",
    name: "核心业务生产集群",
    cluster: "prod-cn",
    instance: "web-02",
    verified_at: isoAgo(12),
    namespaces: [
      { name: "apps", verified_at: isoAgo(12), scope: "list_namespaces" as const },
      { name: "auth", verified_at: isoAgo(12), scope: "list_namespaces" as const },
      { name: "batch", verified_at: isoAgo(12), scope: "list_namespaces" as const },
      { name: "monitoring", verified_at: isoAgo(12), scope: "list_namespaces" as const },
    ],
  },
  {
    asset_id: "k8s-staging",
    name: "预发验证集群",
    cluster: "staging",
    instance: "db-01",
    verified_at: isoAgo(25),
    namespaces: [
      { name: "staging-apps", verified_at: isoAgo(25), scope: "list_namespaces" as const },
      { name: "data-ops", verified_at: isoAgo(25), scope: "list_namespaces" as const },
    ],
  },
]

const demoClusterProfiles = [
  { id: "k8s-prod-cn", name: "核心业务生产集群", environment: "生产", contact: "张敏 (平台运维组)", revision: 2 },
  { id: "k8s-staging", name: "预发验证集群", environment: "预发", contact: "李强 (测试保障组)", revision: 1 },
]

const demoNodeLinks = [
  { asset_id: "k8s-prod-cn", cluster: "prod-cn", instance: "web-02", node_name: "node-web-02", machine_id: "vm-web-02", source: "manual" as const },
  { asset_id: "k8s-prod-cn", cluster: "prod-cn", instance: "web-02", node_name: "node-gpu-01", machine_id: "phy-gpu-01", source: "manual" as const },
  { asset_id: "k8s-staging", cluster: "staging", instance: "db-01", node_name: "stg-node-01", machine_id: "vm-db-01", source: "manual" as const },
]

const demoSystems = [
  {
    asset_id: "sys-edu-portal",
    name: "全校教务管理系统",
    owner: "张敏",
    active: true,
    revision: 3,
    deployments: [
      { cluster: "prod-cn", instance: "web-02", namespace: "apps", application: "edu-portal", database: "pg-main-01", verified_at: isoAgo(20) },
      { cluster: "prod-cn", instance: "web-01", namespace: "apps", application: "nginx-gateway", database: "", verified_at: isoAgo(20) },
    ],
  },
  {
    asset_id: "sys-sso-auth",
    name: "统一身份认证平台 (SSO)",
    owner: "李强",
    active: true,
    revision: 2,
    deployments: [
      { cluster: "prod-cn", instance: "web-02", namespace: "auth", application: "sso-gateway", database: "pg-main-01", verified_at: isoAgo(45) },
    ],
  },
  {
    asset_id: "sys-music-room",
    name: "智慧琴房与音视频平台",
    owner: "陈晨",
    active: true,
    revision: 2,
    deployments: [
      { cluster: "prod-cn", instance: "web-01", namespace: "apps", application: "music-room-media", database: "mysql-edu-01", verified_at: isoAgo(18) },
    ],
  },
  {
    asset_id: "sys-ai-compute",
    name: "科研算力与 AI 作曲平台",
    owner: "王磊",
    active: true,
    revision: 2,
    deployments: [
      { cluster: "prod-cn", instance: "web-02", namespace: "batch", application: "gpu-trainer", database: "pg-main-01", verified_at: isoAgo(15) },
    ],
  },
  {
    asset_id: "sys-fin-reimburse",
    name: "财务网上报销系统",
    owner: "赵雪",
    active: true,
    revision: 1,
    deployments: [
      { cluster: "staging", instance: "db-01", namespace: "staging-apps", application: "fin-api", database: "pg-fin-01", verified_at: isoAgo(60) },
    ],
  },
]

const demoCatalogRows = [
  {
    id: "db-pg-main",
    kind: "database" as const,
    name: "PostgreSQL 核心主库",
    environment: "生产",
    cluster: "staging",
    instance: "db-01",
    details: { engine: "PostgreSQL", host: "10.0.2.21", port: "5432", database: "campus_core" },
    revision: 2,
  },
  {
    id: "db-mysql-edu",
    kind: "database" as const,
    name: "MySQL 教务选课库",
    environment: "生产",
    cluster: "prod-cn",
    instance: "web-01",
    details: { engine: "MySQL", host: "10.0.2.25", port: "3306", database: "edu_courses" },
    revision: 1,
  },
  {
    id: "db-pg-fin",
    kind: "database" as const,
    name: "PostgreSQL 财务报销库",
    environment: "预发",
    cluster: "staging",
    instance: "db-01",
    details: { engine: "PostgreSQL", host: "10.0.2.21", port: "5432", database: "finance_stg" },
    revision: 1,
  },
  {
    id: "web-edu-portal",
    kind: "website" as const,
    name: "教务门户官网",
    environment: "生产",
    cluster: "prod-cn",
    instance: "web-01",
    details: { scheme: "https", domain: "portal.edu.example", path: "/" },
    revision: 3,
  },
  {
    id: "web-sso-auth",
    kind: "website" as const,
    name: "统一身份认证入口",
    environment: "生产",
    cluster: "prod-cn",
    instance: "web-02",
    details: { scheme: "https", domain: "auth.edu.example", path: "/login" },
    revision: 2,
  },
  {
    id: "web-api-gw",
    kind: "website" as const,
    name: "统一数据开放网关",
    environment: "生产",
    cluster: "prod-cn",
    instance: "web-01",
    details: { scheme: "https", domain: "api.edu.example", path: "/healthz" },
    revision: 1,
  },
  {
    id: "img-doops-app",
    kind: "image" as const,
    name: "教务核心服务镜像",
    environment: "生产",
    cluster: "prod-cn",
    instance: "web-02",
    details: { registry: "harbor.edu.example", repository: "apps/edu-portal", version: "2.4.1" },
    revision: 2,
  },
  {
    id: "img-sso-gateway",
    kind: "image" as const,
    name: "统一认证网关镜像",
    environment: "生产",
    cluster: "prod-cn",
    instance: "web-02",
    details: { registry: "harbor.edu.example", repository: "auth/sso-gateway", version: "1.9.0" },
    revision: 1,
  },
  {
    id: "img-gpu-trainer",
    kind: "image" as const,
    name: "科研算力调度镜像",
    environment: "生产",
    cluster: "prod-cn",
    instance: "web-02",
    details: { registry: "harbor.edu.example", repository: "ai/gpu-trainer", version: "3.1.0" },
    revision: 1,
  },
] as {
  id: string
  kind: string
  name: string
  environment: string
  cluster: string
  instance: string
  details: Record<string, string>
  revision: number
}[]

function buildDemoTopology() {
  const verified = isoAgo(15)
  const nodes = [
    { id: "sys:sys-edu-portal", kind: "system", ref: "sys-edu-portal", name: "全校教务管理系统" },
    { id: "sys:sys-sso-auth", kind: "system", ref: "sys-sso-auth", name: "统一身份认证平台 (SSO)" },
    { id: "sys:sys-music-room", kind: "system", ref: "sys-music-room", name: "智慧琴房与音视频平台" },
    { id: "sys:sys-ai-compute", kind: "system", ref: "sys-ai-compute", name: "科研算力与 AI 作曲平台" },
    { id: "sys:sys-fin-reimburse", kind: "system", ref: "sys-fin-reimburse", name: "财务网上报销系统" },
    { id: "app:edu-portal", kind: "application", ref: "edu-portal", name: "edu-portal", namespace: "apps" },
    { id: "app:sso-gateway", kind: "application", ref: "sso-gateway", name: "sso-gateway", namespace: "auth" },
    { id: "app:music-room-media", kind: "application", ref: "music-room-media", name: "music-room-media", namespace: "apps" },
    { id: "app:gpu-trainer", kind: "application", ref: "gpu-trainer", name: "gpu-trainer", namespace: "batch" },
    { id: "app:fin-api", kind: "application", ref: "fin-api", name: "fin-api", namespace: "staging-apps" },
    { id: "db:pg-main-01", kind: "database", ref: "pg-main-01", name: "PostgreSQL 核心主库" },
    { id: "db:mysql-edu-01", kind: "database", ref: "mysql-edu-01", name: "MySQL 教务选课库" },
    { id: "wl:edu-portal", kind: "workload", ref: "Deployment/edu-portal", name: "Deployment/edu-portal", namespace: "apps" },
    { id: "wl:gpu-trainer", kind: "workload", ref: "StatefulSet/gpu-trainer", name: "StatefulSet/gpu-trainer", namespace: "batch" },
    { id: "pod:edu-portal-7c9d", kind: "pod", ref: "edu-portal-7c9d8f-2kp1x", name: "edu-portal-7c9d8f-2kp1x", namespace: "apps" },
    { id: "pod:gpu-trainer-0", kind: "pod", ref: "gpu-trainer-0", name: "gpu-trainer-0", namespace: "batch" },
    { id: "node:node-web-02", kind: "node", ref: "node-web-02", name: "node-web-02" },
    { id: "node:node-gpu-01", kind: "node", ref: "node-gpu-01", name: "node-gpu-01" },
    { id: "mach:vm-web-02", kind: "machine", ref: "vm-web-02", name: "核心应用与容器节点 02" },
    { id: "mach:vm-music-01", kind: "machine", ref: "vm-music-01", name: "智慧琴房与音视频节点 01" },
    { id: "mach:phy-host-01", kind: "machine", ref: "phy-host-01", name: "核心虚拟化宿主机 A01" },
    { id: "mach:phy-gpu-01", kind: "machine", ref: "phy-gpu-01", name: "科研算力 GPU 物理机 G01" },
    { id: "cluster:k8s-prod-cn", kind: "cluster", ref: "k8s-prod-cn", name: "核心业务生产集群" },
  ]
  const edges = [
    // Business layer
    { from: "sys:sys-edu-portal", to: "app:edu-portal", kind: "contains", layer: "business", evidence: [{ record: "business_deployment", cluster: "prod-cn", instance: "web-02", namespace: "apps", verified_at: verified }] },
    { from: "sys:sys-sso-auth", to: "app:sso-gateway", kind: "contains", layer: "business", evidence: [{ record: "business_deployment", cluster: "prod-cn", instance: "web-02", namespace: "auth", verified_at: verified }] },
    { from: "sys:sys-music-room", to: "app:music-room-media", kind: "contains", layer: "business", evidence: [{ record: "business_deployment", cluster: "prod-cn", instance: "web-01", namespace: "apps", verified_at: verified }] },
    { from: "sys:sys-ai-compute", to: "app:gpu-trainer", kind: "contains", layer: "business", evidence: [{ record: "business_deployment", cluster: "prod-cn", instance: "web-02", namespace: "batch", verified_at: verified }] },
    { from: "sys:sys-fin-reimburse", to: "app:fin-api", kind: "contains", layer: "business", evidence: [{ record: "business_deployment", cluster: "staging", instance: "db-01", namespace: "staging-apps", verified_at: verified }] },
    { from: "app:edu-portal", to: "db:pg-main-01", kind: "uses", layer: "business", evidence: [{ record: "business_deployment", cluster: "prod-cn", instance: "web-02", namespace: "apps", verified_at: verified }] },
    { from: "app:sso-gateway", to: "db:pg-main-01", kind: "uses", layer: "business", evidence: [{ record: "business_deployment", cluster: "prod-cn", instance: "web-02", namespace: "auth", verified_at: verified }] },
    { from: "app:music-room-media", to: "db:mysql-edu-01", kind: "uses", layer: "business", evidence: [{ record: "business_deployment", cluster: "prod-cn", instance: "web-01", namespace: "apps", verified_at: verified }] },
    // Deployment layer
    { from: "app:edu-portal", to: "wl:edu-portal", kind: "bound_workload", layer: "deployment", evidence: [{ record: "runtime_binding", cluster: "prod-cn", instance: "web-02", namespace: "apps", verified_at: verified }] },
    { from: "app:gpu-trainer", to: "wl:gpu-trainer", kind: "bound_workload", layer: "deployment", evidence: [{ record: "runtime_binding", cluster: "prod-cn", instance: "web-02", namespace: "batch", verified_at: verified }] },
    { from: "wl:edu-portal", to: "pod:edu-portal-7c9d", kind: "controls", layer: "deployment", evidence: [{ record: "controller_uid", cluster: "prod-cn", instance: "web-02", namespace: "apps", verified_at: verified }] },
    { from: "wl:gpu-trainer", to: "pod:gpu-trainer-0", kind: "controls", layer: "deployment", evidence: [{ record: "controller_uid", cluster: "prod-cn", instance: "web-02", namespace: "batch", verified_at: verified }] },
    { from: "pod:edu-portal-7c9d", to: "node:node-web-02", kind: "scheduled_on", layer: "deployment", evidence: [{ record: "pod_node_name", cluster: "prod-cn", instance: "web-02", namespace: "apps", verified_at: verified }] },
    { from: "pod:gpu-trainer-0", to: "node:node-gpu-01", kind: "scheduled_on", layer: "deployment", evidence: [{ record: "pod_node_name", cluster: "prod-cn", instance: "web-02", namespace: "batch", verified_at: verified }] },
    { from: "node:node-web-02", to: "cluster:k8s-prod-cn", kind: "member_of", layer: "deployment", evidence: [{ record: "cluster_inventory_sources", cluster: "prod-cn", instance: "web-02", verified_at: verified }] },
    { from: "node:node-gpu-01", to: "cluster:k8s-prod-cn", kind: "member_of", layer: "deployment", evidence: [{ record: "cluster_inventory_sources", cluster: "prod-cn", instance: "web-02", verified_at: verified }] },
    // Physical layer
    { from: "node:node-web-02", to: "mach:vm-web-02", kind: "maps_to", layer: "physical", evidence: [{ record: "inventory_node_links", cluster: "prod-cn", instance: "web-02", verified_at: verified }] },
    { from: "node:node-gpu-01", to: "mach:phy-gpu-01", kind: "maps_to", layer: "physical", evidence: [{ record: "inventory_node_links", cluster: "prod-cn", instance: "web-02", verified_at: verified }] },
    { from: "mach:vm-web-02", to: "mach:phy-host-01", kind: "hosted_on", layer: "physical", evidence: [{ record: "inventory_machines.host_asset_id", verified_at: verified }] },
    { from: "mach:vm-music-01", to: "mach:phy-host-01", kind: "hosted_on", layer: "physical", evidence: [{ record: "inventory_machines.host_asset_id", verified_at: verified }] },
  ]
  const runtime = [
    {
      cluster_id: "k8s-prod-cn",
      cluster: "prod-cn",
      instance: "web-02",
      namespace: "apps",
      status: "ok",
      revision: "rev-20261122-01",
      observed_at: verified,
      applications: ["edu-portal"],
      workloads: [{ uid: "uid-wl-edu-portal", kind: "Deployment", name: "edu-portal" }],
      bindings: [{ application: "edu-portal", uid: "uid-wl-edu-portal", at: verified }],
    },
  ]
  return { nodes, edges, runtime, can_bind: true }
}

function buildDemoCoverage(query?: URLSearchParams) {
  const baselineId = query?.get("baseline_id") || "BL-2026-Q4"
  const kindFilter = query?.get("kind") || "all"
  const statusFilter = query?.get("status") || "all"
  const isLive = baselineId === "live"
  const frozenAt = isoAgo(180)
  const verifiedAt = isoAgo(20)

  const allRows = [
    ...demoMachines.map((m) => ({
      asset_id: m.asset_id,
      name: m.name,
      kind: "machines" as const,
      covered: true,
      evidence: [{
        type: m.kind === "physical" ? ("hosts_vm" as const) : ("host" as const),
        reference: m.host_asset_id || m.address,
        source: { cluster: m.cluster || "prod-cn", instance: m.instance || "web-01" },
        verified: true,
        verified_at: verifiedAt,
        revision: m.revision,
      }],
    })),
    ...demoSystems.map((s) => ({
      asset_id: s.asset_id,
      name: s.name,
      kind: "systems" as const,
      covered: true,
      evidence: s.deployments.map((d) => ({
        type: "deployment" as const,
        reference: `${d.namespace}/${d.application}`,
        source: { cluster: d.cluster, instance: d.instance },
        verified: true,
        verified_at: d.verified_at,
      })),
    })),
  ]

  const rows = allRows.filter((row) => {
    if (kindFilter !== "all" && row.kind !== kindFilter) return false
    if (statusFilter === "covered" && !row.covered) return false
    if (statusFilter === "gap" && row.covered) return false
    return true
  })

  return {
    scope: "platform" as const,
    state: isLive ? ("live" as const) : ("frozen" as const),
    baselines: [{ id: "BL-2026-Q4", frozen_at: frozenAt }],
    baseline: isLive ? null : { id: "BL-2026-Q4", frozen_at: frozenAt },
    document_ref: "ZYCM-ASSET-INV-2026-Q4",
    register_revision: 4,
    accepted: isLive ? null : true,
    metrics: {
      machines: { total: demoMachines.length, covered: demoMachines.length, shortfall: 0, percent: 100, passed: true },
      systems: { total: demoSystems.length, covered: demoSystems.length, shortfall: 0, percent: 100, passed: true },
    },
    machine_lifecycle: demoMachines.map((m) => ({
      asset_id: m.asset_id,
      name: m.name,
      active: true,
      revision: m.revision,
    })),
    rows,
  }
}

function buildDemoWeeklyInspections(query?: URLSearchParams) {
  const view = query?.get("view") || ""
  const runId = Number(query?.get("run_id") || "0")
  const checks: Record<string, string> = {
    M1: "运行与重启基线 (M1)",
    M2: "CPU/内存/磁盘水位 (M2)",
    M3: "进程与异常驻留检查 (M3)",
    M4: "内核与 sysctl 基线 (M4)",
    M5: "账号权限与审计日志 (M5)",
  }
  const assets = [
    { asset_id: "vm-web-01", cluster: "prod-cn", instance: "web-01", cluster_id: "k8s-prod-cn" },
    { asset_id: "vm-web-02", cluster: "prod-cn", instance: "web-02", cluster_id: "k8s-prod-cn" },
    { asset_id: "phy-gpu-01", cluster: "prod-cn", instance: "web-02", cluster_id: "k8s-prod-cn" },
    { asset_id: "vm-db-01", cluster: "staging", instance: "db-01", cluster_id: "k8s-staging" },
  ]
  const nowSec = Math.floor(now() / 1000)
  const plans = [
    {
      plan_id: 1,
      name: "生产核心节点合规周检",
      owner: "张敏",
      executor: "ops-zhang",
      active: 1,
      overdue: 0,
      executor_disabled: 0,
      next_due: nowSec + 3 * 86_400,
      timezone: "Asia/Shanghai",
      weekday: 1,
      clock: "09:00",
      check_count: 5,
    },
    {
      plan_id: 2,
      name: "数据库与归档主机基线复核",
      owner: "赵雪",
      executor: "admin",
      active: 1,
      overdue: 0,
      executor_disabled: 0,
      next_due: nowSec + 5 * 86_400,
      timezone: "Asia/Shanghai",
      weekday: 3,
      clock: "10:00",
      check_count: 4,
    },
  ]
  const runs = [
    { run_id: 204, name: "生产核心节点合规周检", trigger_kind: "scheduled", due: nowSec - 2 * 86_400, checked: 10, planned: 10, gaps: 0 },
    { run_id: 203, name: "数据库与归档主机基线复核", trigger_kind: "manual", due: nowSec - 5 * 86_400, checked: 7, planned: 8, gaps: 1 },
  ]
  if (view === "report" || view === "download") {
    const picked = runs.find((r) => r.run_id === runId) || runs[0]
    return {
      run: { ...picked, owner: "张敏", executor: "ops-zhang" },
      checks,
      items: [
        { item_id: 1, ...assets[0], check_id: "M1", status: "pass", reason: "", repair: "", repair_by: "", generation: 1 },
        { item_id: 2, ...assets[0], check_id: "M2", status: "pass", reason: "", repair: "", repair_by: "", generation: 1 },
        { item_id: 3, ...assets[1], check_id: "M4", status: "finding", reason: "sysctl_deviation", repair: "已排期下周窗口调整 kernel.kptr_restrict=1", repair_by: "ops-zhang", generation: 1 },
        { item_id: 4, ...assets[2], check_id: "M2", status: "pass", reason: "", repair: "", repair_by: "", generation: 1 },
      ],
      history: [
        {
          source: "agent_host",
          evidence_category: "collector_summary",
          item_id: 1,
          generation: 1,
          status: "pass",
          actor: "ops-zhang",
          received: nowSec - 2 * 86_400,
          evidence: { uptime_seconds: 864000, boot_id: "demo-boot-web01" },
          reason: "",
          digest: "sha256:7f4c9a12d8e301ab",
        },
        {
          source: "agent_host",
          evidence_category: "collector_summary",
          item_id: 3,
          generation: 1,
          status: "finding",
          actor: "ops-zhang",
          received: nowSec - 2 * 86_400,
          evidence: { deviated: ["kernel.kptr_restrict=0"] },
          reason: "sysctl_deviation",
          digest: "sha256:9b2e1140fc7a8821",
        },
      ],
    }
  }
  return { plans, runs, assets, checks }
}

function buildDemoDiagnostics(query?: URLSearchParams) {
  const ts = now()
  const assetId = query?.get("asset_id") || "vm-web-01"
  const m = demoMachines.find((x) => x.asset_id === assetId) || demoMachines[2]
  const memTotal = m.memory_gib * 1073741824
  const memUsed = Math.round(memTotal * 0.42)
  return {
    from_ms: ts - 3600_000,
    to_ms: ts,
    evidence_mode: "tunnel" as const,
    generation: 4,
    sample: {
      at_ms: ts - 25_000,
      cpu_total_ticks: 48_200_000,
      cpu_idle_ticks: 36_400_000,
      memory_total_bytes: memTotal,
      memory_used_bytes: memUsed,
      source: "agent_tunnel",
      os: m.asset_id.startsWith("ssh-") ? "Rocky Linux" : "Ubuntu",
      version: m.asset_id.startsWith("ssh-") ? "9.4" : "24.04 LTS",
      kernel: "Linux 6.8.0-49-generic",
      boot_id: `boot-${m.asset_id}-01`,
      boot_at: ts - 10 * 86_400_000,
      oom_kills: 0,
      exited: 0,
      processes: [
        { pid: 1124, start_ticks: 420, name: "doops-agent", rss_bytes: 94 * 1048576 },
        { pid: 1842, start_ticks: 680, name: "nginx", rss_bytes: 218 * 1048576, deployment: { system_id: "sys-edu-portal", namespace: "apps", application: "edu-portal" } },
        { pid: 2109, start_ticks: 710, name: "java (edu-portal)", rss_bytes: 1840 * 1048576, deployment: { system_id: "sys-edu-portal", namespace: "apps", application: "edu-portal" } },
      ],
    },
    comparison: {
      status: "ok",
      cpu_percent: m.asset_id === "phy-gpu-01" ? 58.4 : 23.6,
      previous: {
        at_ms: ts - 85_000,
        cpu_total_ticks: 48_140_000,
        cpu_idle_ticks: 36_354_000,
        memory_total_bytes: memTotal,
        memory_used_bytes: Math.round(memUsed * 0.98),
      },
    },
    application: {
      system_id: "sys-edu-portal",
      system_name: "全校教务门户系统",
      namespace: "apps",
      application: "edu-portal",
      verified_at: isoAgo(15),
      databases: ["pg-main-01"],
      series: [
        { metric: "requests", device: "", at_ms: ts - 25_000, status: "ok", unit: "req/s", value: 186.4 },
        { metric: "response_time_ms", device: "", at_ms: ts - 25_000, status: "ok", unit: "ms", value: 42.6 },
      ],
    },
  }
}

export async function demoAssetRequest(kind: AssetEndpoint, body?: object, query?: URLSearchParams): Promise<unknown> {
  await new Promise((r) => setTimeout(r, 60))
  switch (kind) {
    case "machines": {
      const b = body as Record<string, unknown> | undefined
      if (b?.action === "create" && b.machine && typeof b.machine === "object") {
        const m = b.machine as Partial<DemoMachine>
        const created: DemoMachine = {
          asset_id: String(m.asset_id || `vm-demo-${Date.now()}`),
          name: String(m.name || "新建演示主机"),
          kind: m.kind === "physical" ? "physical" : "vm",
          address: String(m.address || "10.0.9.99"),
          cpu_count: Number(m.cpu_count || 8),
          memory_gib: Number(m.memory_gib || 16),
          disk_gib: Number(m.disk_gib || 100),
          cluster: m.cluster ? String(m.cluster) : undefined,
          instance: m.instance ? String(m.instance) : undefined,
          created_at: m.created_at ? String(m.created_at) : new Date().toISOString(),
          tags: Array.isArray(m.tags) ? m.tags.map(String) : ["env=demo"],
          revision: 1,
          login_accounts: [],
        }
        demoMachines.push(created)
        return { machine: created }
      }
      if (b?.action === "update" && b.machine && typeof b.machine === "object") {
        const m = b.machine as Partial<DemoMachine>
        const idx = demoMachines.findIndex((x) => x.asset_id === m.asset_id)
        if (idx >= 0) {
          demoMachines[idx] = {
            ...demoMachines[idx],
            ...m,
            created_at: m.created_at ? String(m.created_at) : demoMachines[idx].created_at,
            revision: demoMachines[idx].revision + 1,
          }
          return { machine: demoMachines[idx] }
        }
      }
      return { machines: demoMachines.map((m) => ({ ...m, created_at: m.created_at || "2025-10-20T00:00:00Z", tags: [...m.tags] })) }
    }
    case "clusters":
      return { clusters: demoClusters }
    case "cluster-profiles":
      return { profiles: demoClusterProfiles }
    case "node-links":
      return { links: demoNodeLinks }
    case "systems":
      return { systems: demoSystems }
    case "dashboard":
      return {
        machines: demoMachines.length,
        systems: demoSystems.filter((s) => s.active).length,
        clusters: demoClusters.map((c) => ({
          asset_id: c.asset_id,
          name: c.name,
          sources: 1,
          online: 1,
          namespaces: c.namespaces.length,
          machines: demoMachines.filter((m) => m.cluster === c.cluster && m.instance === c.instance).length,
          systems: demoSystems.filter((s) => s.active && s.deployments.some((d) => d.cluster === c.cluster && d.instance === c.instance)).length,
          verified_at: c.verified_at,
        })),
        unassigned: demoMachines.filter((m) => !m.cluster).map((m) => ({ asset_id: m.asset_id, name: m.name })),
      }
    case "topology":
      return buildDemoTopology()
    case "coverage":
      return buildDemoCoverage(query)
    case "inspections":
      return buildDemoWeeklyInspections(query)
    case "machine-diagnostics":
      if (body && (body as Record<string, unknown>).action === "refresh") {
        return { at_ms: now(), new_sample: true }
      }
      return buildDemoDiagnostics(query)
    case "catalog": {
      const b = body as Record<string, unknown> | undefined
      if (b?.op === "create") {
        const created = {
          id: String(b.id || `cat-demo-${Date.now()}`),
          kind: String(b.kind || "website"),
          name: String(b.name || "新建资产"),
          environment: String(b.environment || ""),
          cluster: String(b.cluster || ""),
          instance: String(b.instance || ""),
          details: (b.details && typeof b.details === "object" ? { ...(b.details as Record<string, string>) } : {}),
          revision: 1,
        }
        demoCatalogRows.push(created)
        return { record: created }
      }
      if (b?.op === "update" && b.id) {
        const idx = demoCatalogRows.findIndex((r) => r.id === String(b.id))
        if (idx >= 0) {
          demoCatalogRows[idx] = {
            ...demoCatalogRows[idx],
            name: String(b.name ?? demoCatalogRows[idx].name),
            environment: String(b.environment ?? demoCatalogRows[idx].environment),
            cluster: String(b.cluster ?? demoCatalogRows[idx].cluster),
            instance: String(b.instance ?? demoCatalogRows[idx].instance),
            details:
              b.details && typeof b.details === "object"
                ? { ...(b.details as Record<string, string>) }
                : demoCatalogRows[idx].details,
            revision: demoCatalogRows[idx].revision + 1,
          }
          return { record: demoCatalogRows[idx] }
        }
      }
      if (b?.op === "delete" && b.id) {
        const idx = demoCatalogRows.findIndex((r) => r.id === String(b.id))
        if (idx >= 0) demoCatalogRows.splice(idx, 1)
        return { ok: true }
      }
      const k = query?.get("kind") || ""
      return { records: demoCatalogRows.filter((r) => !k || r.kind === k).map((r) => ({ ...r, details: { ...r.details } })) }
    }
    case "workspace": {
      const view = query?.get("view") || ""
      if (view === "directory" || view === "targets") {
        return {
          targets: [
            { cluster: "prod-cn", instance: "web-01" },
            { cluster: "prod-cn", instance: "web-02" },
            { cluster: "staging", instance: "db-01" },
          ],
        }
      }
      const cluster = query?.get("cluster") || ""
      const instance = query?.get("instance") || ""
      const rows = [
        ...demoMachines
          .filter((m) => (!cluster || m.cluster === cluster) && (!instance || m.instance === instance))
          .map((m) => ({ kind: "machine" as const, asset_id: m.asset_id, name: m.name, address: m.address, linked: true, assigned: true })),
        ...demoClusters
          .filter((c) => (!cluster || c.cluster === cluster) && (!instance || c.instance === instance))
          .map((c) => ({ kind: "cluster" as const, asset_id: c.asset_id, name: c.name, address: `${c.cluster}/${c.instance}`, linked: true, assigned: true })),
        ...demoSystems
          .filter((s) => s.deployments.some((d) => (!cluster || d.cluster === cluster) && (!instance || d.instance === instance)))
          .map((s) => ({ kind: "system" as const, asset_id: s.asset_id, name: s.name, address: s.owner, linked: true, assigned: true })),
      ]
      return { assets: rows }
    }
    case "records":
      return { records: [] }
    default:
      return {}
  }
}
