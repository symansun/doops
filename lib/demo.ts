"use client"

import type { Target, AuditEvent } from "./gateway"
import type { RpcEvent } from "./client"
import type {
  AdminUser,
  AdminGrant,
  AdminToken,
  AdminInstance,
  ProxyRoute,
  ProxyRouteBody,
} from "./admin"

// 演示模式：内置 mock 数据 + 模拟流式输出，无需连接真实 gateway

export const DEMO_TOKEN = "demo-local-token"

export const DEMO_TARGETS: Target[] = [
  {
    cluster: "prod-cn",
    instance: "web-01",
    key: "prod-cn/web-01",
    remote: "10.0.1.11:51820",
    status: "active",
    busy: false,
    active_ops: 1,
    queued_ops: 0,
    connected_at: new Date(Date.now() - 3600_000 * 6).toISOString(),
    last_seen: new Date().toISOString(),
    resources: ["nginx", "doops-app", "redis"],
    sessions: ["console-a1b2c3d4"],
  },
  {
    cluster: "prod-cn",
    instance: "web-02",
    key: "prod-cn/web-02",
    remote: "10.0.1.12:51820",
    status: "busy",
    busy: true,
    active_ops: 2,
    queued_ops: 1,
    connected_at: new Date(Date.now() - 3600_000 * 6).toISOString(),
    last_seen: new Date().toISOString(),
    resources: ["nginx", "doops-app"],
    sessions: ["console-aa11bb22", "deploy-zz99"],
  },
  {
    cluster: "staging",
    instance: "db-01",
    key: "staging/db-01",
    remote: "10.0.2.21:51820",
    status: "idle",
    busy: false,
    active_ops: 0,
    queued_ops: 0,
    connected_at: new Date(Date.now() - 3600_000 * 24).toISOString(),
    last_seen: new Date().toISOString(),
    resources: ["postgres-16", "pgbouncer"],
    sessions: [],
  },
]

export const DEMO_AUDIT: AuditEvent[] = [
  {
    id: 1046,
    user_id: "admin",
    cluster: "prod-cn",
    instance: "web-01",
    action: "monitor:run",
    session: "monitor-sched_demo01",
    command_summary: "定时全策略巡检 run_demo04（虚机 / SSH / K8s / 容器 / 站点 / 数据库）",
    status: "success",
    bytes_in: 1280,
    bytes_out: 18420,
    started_at: new Date(Date.now() - 180_000).toISOString(),
    ended_at: new Date(Date.now() - 174_000).toISOString(),
  },
  {
    id: 1045,
    user_id: "ops-zhang",
    cluster: "prod-cn",
    instance: "web-02",
    action: "asset_monitor:collect",
    session: "console-aa11bb22",
    command_summary: "隧道采集虚拟机与容器指标（mode=host, containers）",
    status: "success",
    bytes_in: 256,
    bytes_out: 6420,
    started_at: new Date(Date.now() - 420_000).toISOString(),
    ended_at: new Date(Date.now() - 418_000).toISOString(),
  },
  {
    id: 1042,
    user_id: "ops-zhang",
    cluster: "prod-cn",
    instance: "web-01",
    action: "exec",
    session: "console-a1b2c3d4",
    command_summary: "systemctl status doops-app",
    status: "success",
    bytes_in: 28,
    bytes_out: 642,
    started_at: new Date(Date.now() - 600_000).toISOString(),
    ended_at: new Date(Date.now() - 599_000).toISOString(),
  },
  {
    id: 1041,
    user_id: "ops-zhang",
    cluster: "prod-cn",
    instance: "web-02",
    action: "ask",
    session: "deploy-zz99",
    command_summary: "部署最新版本到 web-02 并滚动重启",
    status: "success",
    bytes_in: 64,
    bytes_out: 2048,
    started_at: new Date(Date.now() - 5_400_000).toISOString(),
    ended_at: new Date(Date.now() - 5_280_000).toISOString(),
  },
  {
    id: 1039,
    user_id: "ops-bot",
    cluster: "staging",
    instance: "db-01",
    action: "write",
    session: "console-77aa",
    command_summary: "写入 /etc/pgbouncer/pgbouncer.ini",
    status: "success",
    bytes_in: 512,
    bytes_out: 12,
    started_at: new Date(Date.now() - 86_400_000).toISOString(),
    ended_at: new Date(Date.now() - 86_399_000).toISOString(),
  },
]

// 演示文件系统：目录 -> 条目(目录以 / 结尾)；文件 -> 内容
const DEMO_SETTINGS = `{
  "provider": "openai-compatible",
  "model": "demo/ops-assistant",
  "base_url": "https://api.example.com/v1",
  "api_key": "sk-demo-3f9a2b7c8d1e4f5061728394a5b6c7d8",
  "temperature": 0.2,
  "max_tokens": 4096,
    "models": [
    "demo/ops-assistant"
  ]
}
`

const DEMO_DIRS: Record<string, { name: string; dir: boolean }[]> = {
  "/root/ws": [{ name: "console-a1b2c3d4", dir: true }],
  "/root/ws/console-a1b2c3d4": [
    { name: "releases", dir: true },
    { name: "logs", dir: true },
    { name: "deploy.sh", dir: false },
    { name: "config.yaml", dir: false },
    { name: "doops-report.md", dir: false },
  ],
  "/root/ws/console-a1b2c3d4/releases": [
    { name: "1.8.2", dir: true },
    { name: "1.8.3", dir: true },
  ],
  "/root/ws/console-a1b2c3d4/logs": [
    { name: "app.log", dir: false },
    { name: "access.log", dir: false },
  ],
  "/root/.agent": [
    { name: "skills", dir: true },
    { name: "settings.json", dir: false },
  ],
  "/root/kb": [
    { name: "运维手册.md", dir: false },
    { name: "部署流程.md", dir: false },
    { name: "故障排查.md", dir: false },
  ],
  "/root": [
    { name: "ws", dir: true },
    { name: ".agent", dir: true },
    { name: "kb", dir: true },
  ],
}

// 知识库演示文档内容
const DEMO_KB: Record<string, string> = {
  "/root/kb/运维手册.md": `# 运维手册

> 本手册由团队共同维护，记录节点的日常运维约定。

## 服务清单

| 服务 | 说明 | 端口 |
| --- | --- | --- |
| \`doops-app\` | 主应用 | 8080 |
| \`nginx\` | 反向代理 | 80 / 443 |
| \`redis\` | 缓存 | 6379 |

## 常用命令

\`\`\`bash
systemctl status doops-app
journalctl -u doops-app -n 100 --no-pager
\`\`\`

## 值班约定

1. 告警优先在群里 **认领**，避免重复处理。
2. 生产变更必须先在 staging 验证。
3. 任何回滚都要在本知识库登记。
`,
  "/root/kb/部署流程.md": `# 标准部署流程

## 1. 准备

- 确认目标分支已合并并通过 CI
- 核对发布版本号

## 2. 构建与发布

\`\`\`bash
ln -sfn releases/1.8.3 current
nginx -s reload
\`\`\`

## 3. 验证

- 健康检查 \`/healthz\` 返回 **200**
- 观察 5 分钟错误日志

> 如有异常，立即按《故障排查》回滚。
`,
  "/root/kb/故障排查.md": `# 故障排查

## 健康检查失败

1. 查看应用日志：\`journalctl -u doops-app -p err\`
2. 确认依赖服务（redis / 数据库）可用
3. 必要时回滚到上一个稳定版本

## 磁盘告警

- 清理历史发布包，保留最近 3 个版本
- 压缩并归档旧日志
`,
}

function demoFileContent(path: string): string {
  if (DEMO_KB[path]) return DEMO_KB[path]
  if (path.includes("settings.json")) return DEMO_SETTINGS
  if (path.endsWith("deploy.sh"))
    return "#!/usr/bin/env bash\nset -euo pipefail\nIMAGE=doops/app:${1:-latest}\nbuildctl build --frontend dockerfile.v0 --local context=. --output type=image,name=$IMAGE\nkubectl set image deployment/app app=$IMAGE\nkubectl rollout status deployment/app\n"
  if (path.endsWith("doops-report.md"))
    return "# 部署报告\n\n- 镜像: doops/app:1.8.3\n- 滚动更新: 成功\n- 健康检查: /healthz 200\n- 耗时: 48s\n"
  if (path.endsWith(".md"))
    return "# 工作区记录\n\n这是演示数据，没有连接真实节点。\n"
  if (path.endsWith(".yaml"))
    return "server:\n  port: 8080\n  workers: 4\nlog:\n  level: info\n"
  if (path.endsWith(".log"))
    return "2026-06-27T09:14:32Z INFO  server started on :8080\n2026-06-27T09:14:33Z INFO  connected to redis\n2026-06-27T09:15:01Z INFO  GET /healthz 200 1ms\n"
  return `# ${path}\n(演示文件内容)\n`
}

function demoListing(dir: string): string[] {
  const key = dir.replace(/\/+$/, "") || "/"
  let entries = DEMO_DIRS[key]
  // 任意 /root/ws/<session> 工作区都返回标准结构
  if (!entries) {
    if (/^\/root\/ws\/[^/]+$/.test(key)) entries = DEMO_DIRS["/root/ws/console-a1b2c3d4"]
    else if (/^\/root\/ws\/[^/]+\/releases$/.test(key))
      entries = DEMO_DIRS["/root/ws/console-a1b2c3d4/releases"]
    else if (/^\/root\/ws\/[^/]+\/logs$/.test(key))
      entries = DEMO_DIRS["/root/ws/console-a1b2c3d4/logs"]
  }
  if (!entries) return [`ls: 无法访问 '${dir}': 没有那个文件或目录\n`]
  return entries.map((e) => `${e.name}${e.dir ? "/" : ""}\n`)
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

// 演示集群里的 Deployment 及其主容器镜像，供「检查部署」比对。
const DEMO_DEPLOYMENTS: Record<string, string> = {
  "apps/api": "registry.example.com/doops/api:1.8.2",
  "apps/web": "registry.example.com/doops/web:1.8.2",
}

// 与 Agent 的 imageRefMatches 一致：完全相同，或一方是另一方去掉 :tag / @digest 的前缀。
function demoImageMatches(want: string, got: string) {
  return (
    want === got ||
    got.startsWith(`${want}:`) ||
    got.startsWith(`${want}@`) ||
    want.startsWith(`${got}:`) ||
    want.startsWith(`${got}@`)
  )
}

function demoToolError(onEvent: (ev: RpcEvent) => void, message: string): never {
  onEvent({ type: "error", error: message })
  onEvent({ type: "done" })
  throw new Error(message)
}

async function emitChunks(
  chunks: string[],
  onEvent: (ev: RpcEvent) => void,
  signal?: AbortSignal,
  delay = 220,
) {
  onEvent({ type: "open" })
  for (const c of chunks) {
    signal?.throwIfAborted()
    await sleep(delay)
    signal?.throwIfAborted()
    onEvent({ type: "output", data: c })
  }
}

// 根据 shell 命令产出模拟输出
function shellOutput(cmd: string): string[] {
  const c = cmd.trim()
  // 文件浏览器目录列举：ls -1Ap <path>（允许前置 mkdir -p ...; 等复合命令）
  const lsMatch = c.match(/(?:^|;\s*)ls\s+-1Ap\s+(?:"([^"]+)"|'([^']+)'|(\S+))/)
  if (lsMatch) {
    const dir = lsMatch[1] || lsMatch[2] || lsMatch[3] || "/"
    return demoListing(dir)
  }
  if (/^set -eu\ncommand -v buildctl/.test(c)) {
    return [
      "#1 [internal] load build definition from Dockerfile\n",
      "#5 exporting to image\n",
      "#5 exporting manifest sha256:" + "4f".repeat(32) + " done\n",
      "(演示模式) 未在真实目标上构建镜像。\n",
    ]
  }
  if (/^set -eu\ncommand -v timeout/.test(c) && /skopeo/.test(c)) {
    const synced = [...c.matchAll(/echo "([^"\s]+) manifest_digest=/g)].map((m) => m[1])
    const lines = ["Getting image source signatures\n", "Copying blob sha256:" + "1a".repeat(32) + " done\n", "Writing manifest to image destination\n"]
    if (synced.length) for (const ref of synced) lines.push(`${ref} manifest_digest=sha256:${"9e".repeat(32)}\n`)
    else if (/manifest_digest=/.test(c)) lines.push(`manifest_digest=sha256:${"9e".repeat(32)}\n`)
    lines.push("(演示模式) 未在真实 Registry 上复制镜像。\n")
    return lines
  }
  if (/find .* -printf/.test(c)) {
    return ["d\tartifacts\n", "f\tREADME.md\n", "f\toutput.md\n"]
  }
  if (/git -C .* rev-parse --is-inside-work-tree/.test(c)) {
    return ["git\tready\n", " M output.md\n", "?? artifacts/result.md\n"]
  }
  if (/git -C .* show HEAD:/.test(c)) {
    return ["# Previous result\n\nThe previous workspace result is available here.\n"]
  }
  if (/git -C .* rev-parse HEAD/.test(c)) {
    return ["branch\tmain\n", "head\t0123456789abcdef0123456789abcdef01234567\n"]
  }
  if (/git -C .* log -n/.test(c)) {
    return ["0123456789abcdef0123456789abcdef01234567\t2026-10-11T08:00:00Z\tdoops\tCreate workspace result\n"]
  }
  if (/grep -RFil/.test(c)) {
    const root = c.match(/\/root\/ws\/[A-Za-z0-9._-]+/)?.[0] || "/root/ws/demo-session"
    return [`${root}/output.md\n`, `${root}/artifacts/result.md\n`]
  }
  if (/uptime|^top|load/.test(c))
    return [" 09:14:32 up 6 days,  3:21,  2 users,  load average: 0.18, 0.12, 0.09\n"]
  if (/df(\s|$)/.test(c))
    return [
      "Filesystem      Size  Used Avail Use% Mounted on\n",
      "/dev/vda1        80G   28G   49G  37% /\n",
      "/dev/vdb1       200G   96G  104G  48% /data\n",
    ]
  if (/free|mem/.test(c))
    return [
      "               total        used        free      shared  buff/cache   available\n",
      "Mem:           7.8Gi       2.1Gi       1.2Gi       180Mi       4.5Gi       5.3Gi\n",
    ]
  if (/systemctl status|service/.test(c))
    return [
      "● doops-app.service - DoOps Application\n",
      "   Loaded: loaded (/etc/systemd/system/doops-app.service; enabled)\n",
      "   Active: active (running) since Mon 2026-06-22 03:11:40 UTC; 5 days ago\n",
      " Main PID: 1287 (doops-app)\n",
      "   Memory: 184.2M\n",
    ]
  if (/docker ps|docker container/.test(c))
    return [
      "CONTAINER ID   IMAGE                 STATUS         PORTS                  NAMES\n",
      "a1b2c3d4e5f6   doops/app:1.8.2       Up 5 days      0.0.0.0:8080->8080/tcp app\n",
      "f6e5d4c3b2a1   nginx:1.27            Up 5 days      0.0.0.0:80->80/tcp     nginx\n",
    ]
  if (/ls(\s|$)/.test(c))
    return ["bin  conf  data  logs  releases  current -> releases/1.8.2\n"]
  if (/whoami/.test(c)) return ["doops\n"]
  if (/uname/.test(c)) return ["Linux web-01 6.1.0-21-amd64 #1 SMP x86_64 GNU/Linux\n"]
  return [`$ ${c}\n`, "(演示模式) 命令已在目标节点执行，返回示例输出。\n"]
}

// 演示 doops_k8s：按 operation 返回示例输出，不触达真实集群。
function k8sOutput(args: Record<string, unknown>): string[] {
  const ns = String(args.namespace || "apps")
  const resource = String(args.resource || "deployment/api")
  const name = String(args.name || "")
  switch (args.operation) {
    case "get":
      if (args.output === "json") return [demoK8sResourceJson(String(args.kind), String(args.namespace || ""))]
      if (/^nodes?$/.test(String(args.kind)))
        return [
          "NAME          STATUS   ROLES           AGE   VERSION\n",
          "node-a       Ready    control-plane   92d   v1.30.4\n",
          "node-b       Ready    <none>          92d   v1.30.4\n",
        ]
      return [
        "NAME                   READY   STATUS    RESTARTS   AGE\n",
        "api-7c9d8f6b5d-2xk4p   1/1     Running   0          3d\n",
        "api-7c9d8f6b5d-q8mzn   1/1     Running   1          3d\n",
        "worker-0               1/1     Running   0          5d\n",
      ]
    case "describe":
      return [`Name:         ${name}\n`, `Namespace:    ${ns}\n`, "Status:       Running\n", "Events:       <none>\n"]
    case "logs":
      return [
        `[演示] ${resource} 最近日志\n`,
        "2026-11-01T08:00:01Z INFO  server listening on :8080\n",
        "2026-11-01T08:00:05Z INFO  GET /healthz 200 1ms\n",
      ]
    case "events":
      return [
        "LAST SEEN   TYPE     REASON              OBJECT                     MESSAGE\n",
        "2m          Normal   ScalingReplicaSet   deployment/api             Scaled up replica set api-7c9d8f6b5d to 2\n",
      ]
    case "top":
      return args.kind === "nodes"
        ? ["NAME     CPU(cores)   CPU%   MEMORY(bytes)   MEMORY%\n", "node-a   412m         10%    5120Mi          32%\n"]
        : ["NAME                   CPU(cores)   MEMORY(bytes)\n", "api-7c9d8f6b5d-2xk4p   12m          96Mi\n"]
    case "rollout-status":
      return [`deployment "${resource.split("/")[1]}" successfully rolled out\n`]
    case "configmap-keys":
      return [
        "NAMESPACE   NAME            KEYS\n",
        `${ns}        api-config      LOG_LEVEL,app.yaml\n`,
        `${ns}        orders-config   <none>\n`,
      ]
    case "helm-list":
      return [JSON.stringify([{ name: "api", namespace: ns, revision: "7", status: "deployed", chart: "api-1.8.2", app_version: "1.8.2" }]) + "\n"]
    case "helm-history":
      return [JSON.stringify([{ revision: 6, status: "superseded", chart: "api-1.8.1" }, { revision: 7, status: "deployed", chart: "api-1.8.2" }]) + "\n"]
    case "helm-status":
      return [JSON.stringify({ name, namespace: ns, info: { status: "deployed" }, version: 7 }) + "\n"]
    case "helm-values":
      return ["image.repository\n", "image.tag\n", "replicaCount\n", "resources.limits.cpu\n"]
    case "rollout-restart":
      return [`[演示] ${resource} restarted（未操作真实集群）\n`]
    case "scale":
      return [`[演示] ${resource} scaled to ${args.replicas}（未操作真实集群）\n`]
    case "cordon":
    case "uncordon":
      return [`[演示] node/${name} ${args.operation}ed（未操作真实集群）\n`]
    case "delete-pod":
      return [`[演示] pod "${name}" deleted（未操作真实集群）\n`]
    default:
      return ["(演示模式) 已执行\n"]
  }
}

function demoK8sResourceJson(kind: string, namespace: string): string {
  type DemoResource = { metadata: { name: string; namespace?: string; creationTimestamp: string }; spec?: object; status: object }
  const resources: Record<string, DemoResource[]> = {
    nodes: [
      { metadata: { name: "node-a", creationTimestamp: "2026-08-19T08:00:00Z" }, status: { capacity: { cpu: "8", memory: "32Gi" }, conditions: [{ type: "Ready", status: "True" }] } },
      { metadata: { name: "node-b", creationTimestamp: "2026-08-19T08:00:00Z" }, status: { conditions: [{ type: "Ready", status: "True" }] } },
    ],
    namespaces: [
      { metadata: { name: "apps", creationTimestamp: "2026-09-01T08:00:00Z" }, status: { phase: "Active" } },
      { metadata: { name: "agents", creationTimestamp: "2026-09-01T08:00:00Z" }, status: { phase: "Active" } },
      { metadata: { name: "monitoring", creationTimestamp: "2026-09-01T08:00:00Z" }, status: { phase: "Active" } },
    ],
    deployments: [
      { metadata: { name: "api", namespace: "apps", creationTimestamp: "2026-10-29T08:00:00Z" }, spec: { replicas: 2 }, status: { readyReplicas: 2, availableReplicas: 2 } },
      { metadata: { name: "worker", namespace: "apps", creationTimestamp: "2026-10-27T08:00:00Z" }, spec: { replicas: 1 }, status: { readyReplicas: 1, availableReplicas: 1 } },
      { metadata: { name: "doops-agent", namespace: "agents", creationTimestamp: "2026-10-25T08:00:00Z" }, spec: { replicas: 2 }, status: { readyReplicas: 1, availableReplicas: 1 } },
    ],
    pods: [
      { metadata: { name: "api-7c9d8f6b5d-2xk4p", namespace: "apps", creationTimestamp: "2026-11-16T08:00:00Z" }, status: { phase: "Running", containerStatuses: [{ ready: true }] } },
      { metadata: { name: "api-7c9d8f6b5d-q8mzn", namespace: "apps", creationTimestamp: "2026-11-16T08:00:00Z" }, status: { phase: "Running", containerStatuses: [{ ready: true }] } },
      { metadata: { name: "worker-0", namespace: "apps", creationTimestamp: "2026-11-14T08:00:00Z" }, status: { phase: "Running", containerStatuses: [{ ready: true }] } },
      { metadata: { name: "doops-agent-0", namespace: "agents", creationTimestamp: "2026-11-18T08:00:00Z" }, status: { phase: "Pending", containerStatuses: [{ ready: false }] } },
    ],
  }
  const items = resources[kind] || []
  const scoped = namespace && (kind === "pods" || kind === "deployments")
  return JSON.stringify({ apiVersion: "v1", kind: "List", items: scoped ? items.filter((item) => item.metadata.namespace === namespace) : items }) + "\n"
}

export function demoAssistantReply(prompt: string): string {
  const p = prompt.toLowerCase()
  if (/状态|检查|巡检|status|健康|检查一下/.test(p)) {
    return `## 巡检结果：节点健康 ✅

**总体状态**：负载正常、核心服务全部存活、无错误日志。以下是演示数据，没有连接真实节点。

| 检查项 | 结果 | 说明 |
| --- | --- | --- |
| 系统负载 | 正常 | load 0.18，低于告警阈值 |
| 服务存活 | 正常 | \`doops-app\` / \`nginx\` / \`redis\` 均 active |
| 磁盘使用 | 正常 | 根分区 37% |
| 错误日志 | 无 | 近 1 小时无 error 级日志 |

**建议**：无需处理，保持当前监控频率即可。`
  }
  if (/回滚|rollback|上一个版本|恢复/.test(p)) {
    return `## 回滚完成 ✅

已将 \`current\` 切回上一个稳定版本 **releases/1.8.2**。这是演示数据，没有改动真实服务。

**执行步骤**

1. 定位上一个稳定版本 \`releases/1.8.2\`
2. 切换软链并重载服务：

\`\`\`bash
ln -sfn releases/1.8.2 current
systemctl reload doops-app
\`\`\`

3. 健康检查 \`/healthz\` 返回 **200 OK**`
  }
  return `## 部署完成 ✅

已将 **doops-app** 滚动升级到 \`1.8.3\`。这是演示数据，没有改动真实集群。

**变更摘要**

- 新增发布：\`releases/1.8.3\`
- \`current\` 软链已指向新版本
- \`nginx\` 平滑重载，连接无中断
- 健康检查 \`/healthz\` → **200 OK**

完整报告已写入 \`doops-report.md\`。`
}

export function demoApplyReply(): string {
  return "演示发布完成。这是本地假数据，没有改动真实集群。真实发布以 Helm release 回执为准，业务验收按任务执行。"
}

export async function demoCallTool(
  opts: { cluster?: string; instance?: string; tool: string; arguments: Record<string, unknown>; signal?: AbortSignal },
  onEvent: (ev: RpcEvent) => void,
): Promise<void> {
  const { tool, arguments: args, signal } = opts

  if (tool === "doops_bg" && args.detach === true) {
    onEvent({ type: "open" })
    onEvent({ type: "background_task", audit_id: 1284, operation_id: "op-demo-1284" })
    onEvent({ type: "capabilities", capabilities: { gatewayProtocols: { detachedBackground: { schemaVersion: "doops.sh/detached-background/v1" } } } })
    await emitChunks(
      ["Task submitted.\nTaskID: task-9f2c\nPID: 4213\n", "running…\n"],
      onEvent,
      signal,
      160,
    )
    onEvent({ type: "result", result: { content: [{ type: "text", text: "Task complete.\nTaskID: task-9f2c\nExitCode: 0" }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_shell") {
    const cmd = String(args.command || args.cmd || "")
    await emitChunks(shellOutput(cmd), onEvent, signal, 160)
    onEvent({ type: "result", result: { content: [{ type: "text", text: "exit code: 0" }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_node_info") {
    if (args.section === "admission") {
      onEvent({ type: "open" })
      await sleep(120)
      const busy = opts.instance === "web-02"
      const current = {
        limit: 4,
        inUse: busy ? 4 : 1,
        available: busy ? 0 : 3,
        holders: busy
          ? [{ sessionId: "console-aa11bb22", turnId: "turn-01" }, { sessionId: "deploy-zz99", turnId: "turn-02" }]
          : [{ sessionId: "console-a1b2c3d4", turnId: "turn-09" }],
        queues: busy ? [{ sessionId: "release-wait-03", queued: 1 }] : [],
      }
      onEvent({ type: "result", result: { content: [{ type: "text", text: JSON.stringify(current, null, 2) }] } })
      onEvent({ type: "done" })
      return
    }
    await emitChunks(
      [
        "Linux demo-node 6.8.0-41-generic #41-Ubuntu SMP x86_64 GNU/Linux\n",
        "uptime: 14 days, 03:12, load average: 0.18, 0.22, 0.19\n",
        "free -m: total 15872 MiB, used 6210 MiB, available 9144 MiB\n",
        "df -h /: /dev/sda1 80G 30G 47G 39% /\n",
        "kubectl: OK v1.30.2\n",
        "container-runtime: docker 26.1\n",
        "buildctl: MISSING\n",
        "skopeo: OK 1.15\n",
        "buildkit-sock: MISSING\n",
        "kubeconfig: OK\n",
      ],
      onEvent,
      signal,
      120,
    )
    onEvent({ type: "result", result: { content: [{ type: "text", text: "Operation complete." }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_web" && args.operation === "site-list") {
    const sites = [
      { site_id: "portal", domain: "portal.example.test", current_release: "demo-2", previous_release: "demo-1", updated_at: "2026-11-18T09:45:00Z" },
      { site_id: "docs", domain: "docs.example.test", current_release: "demo-1", previous_release: null, updated_at: "2026-11-17T08:10:00Z" },
    ]
    onEvent({ type: "result", result: { content: [{ type: "text", text: JSON.stringify({ sites }) }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_database_inventory") {
    const { cluster, instance } = opts
    if (!cluster || !instance) throw new Error("数据库发现需要指定目标")
    const candidates = [
      { service: "orders-postgres", engine: "postgres", port: 5432, database_name: "orders" },
      { service: "billing-mysql", engine: "mysql", port: 3306, database_name: "billing" },
      { service: "events-mongodb", engine: "mongodb", port: 27017 },
    ].map((row) => ({ ...row, cluster, instance, namespace: "data", source: "StatefulSet", host: `${row.service}.data.svc`,
      database_asset_id: `dbscan_${cluster}_${instance}_data_${row.service}_${row.engine}`,
      status: row.database_name ? "candidate" : "needs_database_name_or_credential" }))
    onEvent({ type: "result", result: { content: [{ type: "text", text: JSON.stringify({ status: "discovered", candidates }) }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_k8s") {
    await emitChunks(k8sOutput(args), onEvent, signal, 120)
    onEvent({ type: "result", result: { content: [{ type: "text", text: "Kubernetes operation complete." }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_agent_prompt") {
    if (args.mode === "metadata") {
      onEvent({ type: "result", result: { content: [{ type: "text", text: JSON.stringify({ status: "connected", model: JSON.parse(DEMO_SETTINGS).model }) }] } })
      onEvent({ type: "done" })
      return
    }
    if (args.operation === "apply") {
      await emitChunks(["[演示] 收到发布授权；未操作真实目标。\n", "[演示工具] mcp_doops_helm upgrade-install\n"], onEvent, signal, 200)
      onEvent({ type: "output", kind: "assistant_delta", data: demoApplyReply() })
      onEvent({ type: "result", result: { content: [{ type: "text", text: "Operation complete." }] } })
      onEvent({ type: "done" })
      return
    }
    const prompt = String(args.instruction || args.prompt || args.task || "")
    const p = prompt.toLowerCase()
    let chunks: string[]
    const summary = demoAssistantReply(prompt)

    if (/状态|检查|巡检|status|健康|检查一下/.test(p)) {
      chunks = [
        `[plan] 收到任务：${prompt}\n`,
        "[step 1/3] 采集节点负载与服务状态…\n",
        "[tool] doops_shell: uptime && systemctl is-active doops-app nginx redis\n",
        "[observe] load 0.18 / 服务全部 active\n",
        "[step 2/3] 检查磁盘与近一小时错误日志…\n",
        "[tool] doops_shell: df -h / && journalctl -u doops-app --since '1 hour ago' -p err\n",
        "[observe] 磁盘 37%，无 error 级日志\n",
        "[done] 巡检完成，节点健康。\n",
      ]
    } else if (/回滚|rollback|上一个版本|恢复/.test(p)) {
      chunks = [
        `[plan] 收到任务：${prompt}\n`,
        "[step 1/2] 定位上一个稳定版本 releases/1.8.2…\n",
        "[tool] doops_shell: ln -sfn releases/1.8.2 current && systemctl reload doops-app\n",
        "[step 2/2] 健康检查 /healthz…\n",
        "[done] 已回滚至 1.8.2，服务恢复正常。\n",
      ]
    } else {
      chunks = [
        `[plan] 收到任务：${prompt}\n`,
        "[step 1/4] 检查当前部署状态与磁盘空间…\n",
        "[tool] doops_shell: df -h / && systemctl is-active doops-app\n",
        "[step 2/4] 拉取最新发布包到 releases/1.8.3…\n",
        "[step 3/4] 切换 current 软链并平滑重载 nginx…\n",
        "[tool] doops_shell: ln -sfn releases/1.8.3 current && nginx -s reload\n",
        "[step 4/4] 健康检查 /healthz 返回 200，部署完成。\n",
        "[done] 已将 doops-app 滚动升级到 1.8.3，无停机。\n",
      ]
    }

    await emitChunks(chunks, onEvent, signal, 340)
    onEvent({ type: "output", kind: "assistant_delta", data: summary })
    onEvent({ type: "result", result: { content: [{ type: "text", text: "Operation complete." }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_file_read") {
    const path = String(args.path || "")
    onEvent({ type: "open" })
    await sleep(200)
    onEvent({
      type: "result",
      result: { content: [{ type: "text", text: demoFileContent(path) }] },
    })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_workspace_read") {
    const operation = String(args.operation || "")
    const path = String(args.path || "")
    const result = operation === "list"
      ? { entries: [{ name: "artifacts", path: "artifacts", type: "directory" }, { name: "README.md", path: "README.md", type: "file" }, { name: "output.md", path: "output.md", type: "file" }] }
      : operation === "changes"
        ? { gitStatus: "ready", gitAvailable: true, changes: [{ name: "output.md", path: "output.md", status: "modified" }, { name: "result.md", path: "artifacts/result.md", status: "created" }] }
        : operation === "search"
          ? { entries: [{ name: "output.md", path: "output.md", type: "file" }, { name: "result.md", path: "artifacts/result.md", type: "file" }], truncated: false }
          : operation === "diff"
            ? { path, baseline: "0123456789abcdef0123456789abcdef01234567", gitStatus: "ready", stale: false, before: { present: true, content: "# Previous result\n\nThe previous workspace result is available here.\n", encoding: "utf-8", truncated: false }, after: { present: true, content: demoFileContent(path), encoding: "utf-8", truncated: false } }
            : operation === "git-summary"
              ? { branch: "main", clean: false, detached: false, head: "0123456789abcdef0123456789abcdef01234567", refs: [], status: "ready" }
              : operation === "git-log"
                ? { commits: [{ oid: "0123456789abcdef0123456789abcdef01234567", authoredAt: "2026-10-11T08:00:00Z", author: "doops", subject: "Create workspace result", parents: [] }], cursor: null, ref: String(args.ref || "HEAD"), truncated: false }
                : { path, content: demoFileContent(path), encoding: "utf-8", contentType: "text/markdown", bytes: demoFileContent(path).length, truncated: false, binary: false }
    onEvent({ type: "result", result: { content: [{ type: "text", text: JSON.stringify(result) }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_file_write") {
    onEvent({ type: "open" })
    await sleep(260)
    onEvent({
      type: "result",
      result: { content: [{ type: "text", text: "写入成功 (演示模式，未持久化)" }] },
    })
    onEvent({ type: "done" })
    return
  }

  // 以下两个工具的参数、成功文本和报错文本与 Agent 一致
  // （backend/agent/internal/server/workspace_upload.go）；失败时和真实 callTool
  // 一样先推 error 事件再抛出同一错误。
  if (tool === "doops_check_deployment") {
    onEvent({ type: "open" })
    await sleep(260)
    signal?.throwIfAborted()
    const namespace = String(args.namespace || "").trim() || "default"
    const deployment = String(args.deployment || "").trim()
    const image = String(args.image || "").trim()
    const actual = DEMO_DEPLOYMENTS[`${namespace}/${deployment}`]
    if (!deployment || !image) demoToolError(onEvent, "namespace, deployment and image are required")
    else if (!actual)
      demoToolError(
        onEvent,
        `kubectl get deploy ${namespace}/${deployment} failed: exit status 1: Error from server (NotFound): deployments.apps "${deployment}" not found`,
      )
    else if (!demoImageMatches(image, actual)) demoToolError(onEvent, `image mismatch: want "${image}" got "${actual}"`)
    onEvent({ type: "result", result: { content: [{ type: "text", text: `check ok: ${namespace}/${deployment} -> ${actual}` }] } })
    onEvent({ type: "done" })
    return
  }

  if (tool === "doops_clean_workspace") {
    onEvent({ type: "open" })
    await sleep(260)
    signal?.throwIfAborted()
    const workspace = String(args.workspace || "").trim()
    if (!workspace || workspace === "." || workspace.includes("..") || /[\\/]/.test(workspace)) {
      demoToolError(onEvent, `unsafe session id: ${JSON.stringify(workspace)}`)
    }
    onEvent({ type: "result", result: { content: [{ type: "text", text: `Cleaned workspace and bare repo: ${workspace}` }] } })
    onEvent({ type: "done" })
    return
  }

  // 兜底
  onEvent({ type: "open" })
  await sleep(150)
  onEvent({ type: "result", result: { content: [{ type: "text", text: "(演示模式) 已执行" }] } })
  onEvent({ type: "done" })
}

// ===================== 管理后台演示数据（可变，便于体验增删） =====================

const nowISO = () => new Date().toISOString()
const ago = (h: number) => new Date(Date.now() - h * 3600_000).toISOString()

const demoUsers: AdminUser[] = [
  { id: "usr_admin01", name: "admin", disabled: false, has_password: true, grant_count: 1, is_admin: true, created_at: ago(720) },
  { id: "usr_ops01", name: "ops-zhang", disabled: false, has_password: true, grant_count: 2, is_admin: false, created_at: ago(240) },
  { id: "usr_dev01", name: "dev-li", disabled: false, has_password: true, grant_count: 1, is_admin: false, created_at: ago(72) },
  { id: "usr_bot01", name: "ci-bot", disabled: true, has_password: false, grant_count: 1, is_admin: false, created_at: ago(48) },
]

const demoGrants: AdminGrant[] = [
  { id: 1, user_id: "usr_admin01", user_name: "admin", cluster: "*", instance: "*", created_at: ago(720) },
  { id: 2, user_id: "usr_ops01", user_name: "ops-zhang", cluster: "prod-cn", instance: "*", created_at: ago(240) },
  { id: 3, user_id: "usr_ops01", user_name: "ops-zhang", cluster: "staging", instance: "*", created_at: ago(120) },
  { id: 4, user_id: "usr_dev01", user_name: "dev-li", cluster: "staging", instance: "db-01", created_at: ago(72) },
  { id: 5, user_id: "usr_bot01", user_name: "ci-bot", cluster: "prod-cn", instance: "*", created_at: ago(48) },
]

const demoTokens: AdminToken[] = [
  { id: "tok_a1", kind: "user", user_id: "usr_ops01", user_name: "ops-zhang", name: "笔记本", prefix: "dgw_user_3f2a", revoked: false, created_at: ago(240), expires_at: ago(-480) },
  { id: "tok_a2", kind: "user", user_id: "usr_dev01", user_name: "dev-li", name: "CLI", prefix: "dgw_user_88c1", revoked: false, created_at: ago(72) },
]

const demoInstances: AdminInstance[] = [
  { cluster: "prod-cn", instance: "web-01", status: "online", remote: "10.0.1.11:51820", busy: false, active_ops: 1, queued_ops: 0, connected_at: ago(6), last_seen: nowISO() },
  { cluster: "prod-cn", instance: "web-02", status: "online", remote: "10.0.1.12:51820", busy: true, active_ops: 2, queued_ops: 1, connected_at: ago(6), last_seen: nowISO() },
  { cluster: "staging", instance: "db-01", status: "offline", remote: "10.0.2.21:51820", busy: false, active_ops: 0, queued_ops: 0, connected_at: ago(48), last_seen: ago(2) },
]

let grantSeq = 100
let userSeq = 100

export function demoListUsers(): AdminUser[] {
  return demoUsers.map((u) => ({ ...u }))
}
export function demoCreateUser(body: { name: string; admin?: boolean }): void {
  demoUsers.push({
    id: `usr_demo${userSeq++}`,
    name: body.name,
    disabled: false,
    has_password: true,
    grant_count: 1,
    is_admin: !!body.admin,
    created_at: nowISO(),
  })
}
export function demoSetUserDisabled(body: { user_id: string; disabled: boolean }): void {
  const u = demoUsers.find((x) => x.id === body.user_id)
  if (u) u.disabled = body.disabled
}
export function demoListGrants(user?: string): AdminGrant[] {
  const list = user ? demoGrants.filter((g) => g.user_id === user) : demoGrants
  return list.map((g) => ({ ...g }))
}
export function demoCreateGrant(body: { user_id: string; cluster: string; instance: string }): void {
  const u = demoUsers.find((x) => x.id === body.user_id)
  demoGrants.push({
    id: grantSeq++,
    user_id: body.user_id,
    user_name: u?.name,
    cluster: body.cluster || "*",
    instance: body.instance || "*",
    created_at: nowISO(),
  })
  if (u) u.grant_count += 1
}
export function demoDeleteGrant(id: number): void {
  const idx = demoGrants.findIndex((g) => g.id === id)
  if (idx >= 0) {
    const [g] = demoGrants.splice(idx, 1)
    const u = demoUsers.find((x) => x.id === g.user_id)
    if (u && u.grant_count > 0) u.grant_count -= 1
  }
}
export function demoListTokens(): AdminToken[] {
  return demoTokens.map((t) => ({ ...t }))
}
export function demoCreateToken(body: { user: string; name?: string }): {
  token: string
  token_id: string
} {
  const id = `tok_demo${randomToken(4)}`
  const user = demoUsers.find((item) => item.name === body.user)
  demoTokens.unshift({
    id,
    kind: "user",
    user_id: user?.id,
    user_name: user?.name || body.user,
    name: body.name || "新令牌",
    prefix: `dgw_user_${randomToken(4)}`,
    revoked: false,
    created_at: nowISO(),
  })
  return { token: `dgw_user_demo_${id}_${randomToken(32)}`, token_id: id }
}
export function demoRevokeToken(id: string): void {
  const t = demoTokens.find((x) => x.id === id)
  if (t) t.revoked = true
}
export function demoListInstances(): AdminInstance[] {
  return demoInstances.map((i) => ({ ...i }))
}
export function demoArchiveInstance(body: { cluster: string; instance: string }): AdminInstance {
  const instance = demoInstances.find((item) => item.cluster === body.cluster && item.instance === body.instance)
  if (!instance) throw new Error("实例不存在")
  if (instance.status === "online") throw new Error("在线实例不能归档")
  instance.archived = true
  instance.archived_at = nowISO()
  instance.archived_by = "usr_admin01"
  return { ...instance }
}
export function demoUnarchiveInstance(body: { cluster: string; instance: string }): AdminInstance {
  const instance = demoInstances.find((item) => item.cluster === body.cluster && item.instance === body.instance)
  if (!instance) throw new Error("实例不存在")
  instance.archived = false
  delete instance.archived_at
  delete instance.archived_by
  return { ...instance }
}

const demoProxyRoutes: ProxyRoute[] = [
  { id: "route_demo01", host: "app.demo.example", cluster: "prod-cn", instance: "web-01", upstream: "http://127.0.0.1:8080", enabled: true, created_at: ago(96), updated_at: ago(24) },
  { id: "route_demo02", host: "status.demo.example", cluster: "staging", instance: "db-01", upstream: "http://127.0.0.1:9090", enabled: false, created_at: ago(48), updated_at: ago(48) },
]

// 与 Gateway 一致：按 host 排序，host 唯一，id 为空时新建。
export function demoListProxyRoutes(): ProxyRoute[] {
  return demoProxyRoutes.map((r) => ({ ...r })).sort((a, b) => a.host.localeCompare(b.host))
}
export function demoSetProxyRoute(body: ProxyRouteBody): ProxyRoute {
  const clash = demoProxyRoutes.find((r) => r.host === body.host && r.id !== body.id)
  if (clash) throw new Error(`域名 ${body.host} 已被路由 ${clash.id} 使用`)
  const now = nowISO()
  const existing = body.id ? demoProxyRoutes.find((r) => r.id === body.id) : undefined
  if (existing) {
    Object.assign(existing, { ...body, id: existing.id, updated_at: now })
    return { ...existing }
  }
  const route: ProxyRoute = { ...body, id: body.id || `route_demo${randomToken(6)}`, created_at: now, updated_at: now }
  demoProxyRoutes.push(route)
  return { ...route }
}
export function demoDeleteProxyRoute(id: string): void {
  const idx = demoProxyRoutes.findIndex((r) => r.id === id)
  if (idx < 0) throw new Error("proxy route not found")
  demoProxyRoutes.splice(idx, 1)
}
function randomToken(n: number): string {
  const chars = "0123456789abcdef"
  let out = ""
  for (let i = 0; i < n; i++) out += chars[Math.floor(Math.random() * chars.length)]
  return out
}

// --- Detached background tasks (demo) -------------------------------------
// 合成几条 staging/db-01 的任务，覆盖运行中/成功/失败/已停止等状态徽标。

interface DemoBackgroundTask {
  audit_id: number
  operation_id: string
  cluster: string
  instance: string
  session: string
  command_summary: string
  status: string
  started_at: string
  finished_at?: string
  task_id?: string
  cancel_requested?: boolean
  output: string
}

const demoBgTasks: DemoBackgroundTask[] = [
  {
    audit_id: 1291, operation_id: "op-demo-1291", cluster: "prod-cn", instance: "web-01",
    session: "console-a1b2c3d4", command_summary: "tar -czf /var/backups/nginx-access-20261122.tgz /var/log/nginx/access.log*",
    status: "running", started_at: ago(1), task_id: "task-a18f",
    output: "Task submitted.\nTaskID: task-a18f\nPID: 18421\nArchiving /var/log/nginx/access.log (312 MiB)...\n",
  },
  {
    audit_id: 1288, operation_id: "op-demo-1288", cluster: "prod-cn", instance: "web-01",
    session: "console-a1b2c3d4", command_summary: "certbot renew --dry-run --quiet && nginx -t",
    status: "success", started_at: ago(95), finished_at: ago(94), task_id: "task-b71d",
    output: "Task submitted.\nTaskID: task-b71d\nCongratulations, all simulated renewals succeeded.\nnginx: configuration file /etc/nginx/nginx.conf test is successful\nTask complete.\nExitCode: 0\n",
  },
  {
    audit_id: 1286, operation_id: "op-demo-1286", cluster: "prod-cn", instance: "web-02",
    session: "deploy-zz99", command_summary: "nerdctl -n k8s.io image pull harbor.edu.example/apps/edu-portal:2.4.1",
    status: "running", started_at: ago(3), task_id: "task-c40e",
    output: "Task submitted.\nTaskID: task-c40e\nPulling harbor.edu.example/apps/edu-portal:2.4.1...\nsha256:88a1c3... downloading 68%\n",
  },
  {
    audit_id: 1285, operation_id: "op-demo-1285", cluster: "prod-cn", instance: "web-02",
    session: "deploy-zz99", command_summary: "docker system prune --filter until=168h -f",
    status: "success", started_at: ago(180), finished_at: ago(179), task_id: "task-d29b",
    output: "Task submitted.\nTaskID: task-d29b\nTotal reclaimed space: 4.82GB\nTask complete.\nExitCode: 0\n",
  },
  {
    audit_id: 1284, operation_id: "op-demo-1284", cluster: "staging", instance: "db-01",
    session: "console-demo", command_summary: "pg_dump campus_core -Fc -f /tmp/campus_core.dump",
    status: "running", started_at: ago(2), task_id: "task-9f2c",
    output: "Task submitted.\nTaskID: task-9f2c\nPID: 4213\npg_dump: dumping table orders (41238 rows)\n",
  },
  {
    audit_id: 1281, operation_id: "op-demo-1281", cluster: "staging", instance: "db-01",
    session: "console-demo", command_summary: "apt update && apt install -y sysstat",
    status: "success", started_at: ago(240), finished_at: ago(238), task_id: "task-7ad1",
    output: "Task submitted.\nTaskID: task-7ad1\nReading package lists... Done\nTask complete.\nTaskID: task-7ad1\nExitCode: 0\n",
  },
  {
    audit_id: 1277, operation_id: "op-demo-1277", cluster: "staging", instance: "db-01",
    session: "console-demo", command_summary: "tar czf /tmp/logs.tgz /var/log/nginx",
    status: "failed", started_at: ago(480), finished_at: ago(479), task_id: "task-52be",
    output: "Task submitted.\nTaskID: task-52be\ntar: /var/log/nginx: Cannot read: Permission denied\nTask failed.\nTaskID: task-52be\nExitCode: 2\n",
  },
  {
    audit_id: 1270, operation_id: "op-demo-1270", cluster: "staging", instance: "db-01",
    session: "console-demo", command_summary: "sleep 3600",
    status: "canceled", started_at: ago(720), finished_at: ago(700), task_id: "task-1c04", cancel_requested: true,
    output: "Task submitted.\nTaskID: task-1c04\nTask canceled.\nTaskID: task-1c04\n",
  },
]

export async function demoBackgroundTasks(target: Target) {
  await sleep(120)
  return demoBgTasks
    .filter((task) => task.cluster === target.cluster && task.instance === target.instance)
    .map(({ output: _output, ...view }) => ({ ...view }))
}

export async function demoBackgroundTask(id: number) {
  await sleep(120)
  const task = demoBgTasks.find((t) => t.audit_id === id)
  if (!task) {
    const err = new Error("后台任务不存在") as Error & { status?: number }
    err.status = 404
    throw err
  }
  return { ...task }
}

export async function demoStopBackgroundTask(id: number) {
  await sleep(120)
  const task = demoBgTasks.find((t) => t.audit_id === id)
  if (!task) {
    const err = new Error("后台任务不存在") as Error & { status?: number }
    err.status = 404
    throw err
  }
  if (task.status !== "running") {
    const err = new Error("任务已结束") as Error & { status?: number }
    err.status = 409
    throw err
  }
  task.cancel_requested = true
  task.status = "canceled"
  task.finished_at = nowISO()
  task.output += "Task canceled.\n"
}

export async function demoPlatformMonitor(
  isAdmin: boolean,
  range: "1h" | "24h" | "7d" | "30d",
  cluster?: string,
): Promise<import("./platform-monitor").PlatformMonitorData> {
  await sleep(100)
  const now = Date.now()
  const windowMap = {
    "1h": { ms: 3600_000, buckets: 12, scale: 1 },
    "24h": { ms: 86_400_000, buckets: 24, scale: 8 },
    "7d": { ms: 7 * 86_400_000, buckets: 14, scale: 34 },
    "30d": { ms: 30 * 86_400_000, buckets: 30, scale: 110 },
  } as const
  const spec = windowMap[range]
  const from_ms = now - spec.ms
  const commitNew = "a6f7f3e2910bc4d8812ef01928374655aa112233"
  const commitOld = "531feb0788ab12cd34ef5678901234567890abcd"

  const allAgents: import("./platform-monitor").PlatformMonitorAgent[] = [
    {
      cluster: "prod-cn", instance: "web-02", key: "prod-cn/web-02",
      activity: "online", status: "busy", busy: true, busy_reason: "exclusive_operation",
      enrolled: true, release_commit: commitNew, remote: "10.0.1.12:51820",
      connected_at: ago(6), last_seen: nowISO(), heartbeat_age_ms: 2400,
      active_ops: 2, queued_ops: 1, sessions: ["console-aa11bb22", "deploy-zz99"],
      resources: ["target:prod-cn/web-02", "helm:apps/doops-app"],
      window_connects: 2, window_disconnects: 1,
      window_calls: 18 * spec.scale, window_failed: 1 * Math.max(1, Math.floor(spec.scale / 4)),
      window_bytes_in: 48_200 * spec.scale, window_bytes_out: 612_400 * spec.scale,
      window_tokens: 46_800 * spec.scale,
    },
    {
      cluster: "prod-cn", instance: "web-01", key: "prod-cn/web-01",
      activity: "online", status: "active", busy: false,
      enrolled: true, release_commit: commitNew, remote: "10.0.1.11:51820",
      connected_at: ago(18), last_seen: nowISO(), heartbeat_age_ms: 4100,
      active_ops: 1, queued_ops: 0, sessions: ["console-a1b2c3d4"],
      resources: ["workspace:console-a1b2c3d4"],
      window_connects: 0, window_disconnects: 0,
      window_calls: 24 * spec.scale, window_failed: 0,
      window_bytes_in: 31_000 * spec.scale, window_bytes_out: 498_000 * spec.scale,
      window_tokens: 62_400 * spec.scale,
    },
    {
      cluster: "staging", instance: "db-01", key: "staging/db-01",
      activity: "online", status: "idle", busy: false,
      enrolled: false, release_commit: commitOld, remote: "10.0.2.21:51820",
      connected_at: ago(24), last_seen: new Date(now - 14_000).toISOString(), heartbeat_age_ms: 14_000,
      active_ops: 0, queued_ops: 0, sessions: [], resources: [],
      window_connects: 1, window_disconnects: 0,
      window_calls: 9 * spec.scale, window_failed: 1,
      window_bytes_in: 19_400 * spec.scale, window_bytes_out: 184_000 * spec.scale,
      window_tokens: 18_200 * spec.scale,
    },
    ...(isAdmin ? [{
      cluster: "staging", instance: "worker-legacy", key: "staging/worker-legacy",
      activity: "recent_offline" as const, status: "offline", busy: false,
      enrolled: true, release_commit: commitOld, remote: "10.0.2.45:49100",
      connected_at: ago(72), last_seen: ago(5), heartbeat_age_ms: 5 * 3600_000,
      active_ops: 0, queued_ops: 0, sessions: [], resources: [],
      window_connects: 0, window_disconnects: 1,
      window_calls: 2, window_failed: 1, window_bytes_in: 1200, window_bytes_out: 480,
      window_tokens: 3400,
    }] : []),
  ]

  const clusters = [
    { name: "prod-cn", online: 2, total: 2 },
    { name: "staging", online: 1, total: isAdmin ? 2 : 1 },
  ]
  const agents = cluster ? allAgents.filter((a) => a.cluster === cluster) : allAgents
  const onlineAgents = agents.filter((a) => a.activity === "online")
  const versionMap = new Map<string, number>()
  for (const a of onlineAgents) {
    const k = a.release_commit || ""
    versionMap.set(k, (versionMap.get(k) || 0) + 1)
  }

  const totalCalls = agents.reduce((s, a) => s + a.window_calls, 0)
  const totalFailed = agents.reduce((s, a) => s + a.window_failed, 0)
  const runningCount = onlineAgents.reduce((s, a) => s + a.active_ops, 0)
  const successCount = Math.max(0, totalCalls - totalFailed - runningCount)
  const bytesIn = agents.reduce((s, a) => s + a.window_bytes_in, 0)
  const bytesOut = agents.reduce((s, a) => s + a.window_bytes_out, 0)
  const totalTokens = agents.reduce((s, a) => s + a.window_tokens, 0)
  const inputTokens = Math.round(totalTokens * 0.68)
  const outputTokens = Math.max(0, totalTokens - inputTokens)

  const trend = Array.from({ length: spec.buckets }, (_, idx) => {
    const start_ms = Math.round(from_ms + (spec.ms * idx) / spec.buckets)
    const end_ms = Math.round(from_ms + (spec.ms * (idx + 1)) / spec.buckets)
    const wave = 0.55 + 0.45 * Math.sin(idx * 0.65 + 0.8)
    const total = Math.max(1, Math.round((totalCalls / spec.buckets) * wave))
    const failed = idx === spec.buckets - 3 && totalFailed > 0 ? Math.min(total, Math.ceil(totalFailed / 2)) : 0
    return {
      start_ms, end_ms, total, failed, success: Math.max(0, total - failed),
      bytes_in: Math.round(bytesIn / spec.buckets), bytes_out: Math.round(bytesOut / spec.buckets),
      total_tokens: Math.round((totalTokens / spec.buckets) * wave),
    }
  })

  const load_trend = isAdmin && !cluster
    ? Array.from({ length: spec.buckets }, (_, idx) => {
        const start_ms = Math.round(from_ms + (spec.ms * idx) / spec.buckets)
        const end_ms = Math.round(from_ms + (spec.ms * (idx + 1)) / spec.buckets)
        const peak = idx >= spec.buckets - 5
        return {
          start_ms, end_ms, samples: 5,
          max_online_agents: idx < 3 ? 4 : 3,
          max_busy_agents: peak ? 1 : 0,
          max_active_ops: peak ? 4 : 2,
          max_queued_ops: idx === spec.buckets - 2 ? 2 : peak ? 1 : 0,
          max_active_sessions: peak ? 3 : 2,
        }
      })
    : []

  const connection_events: import("./platform-monitor").PlatformConnectionEvent[] = [
    ...(!cluster || cluster === "prod-cn"
      ? [
          { id: 41, cluster: "prod-cn", instance: "web-02", key: "prod-cn/web-02", generation: 4, event: "online", release_commit: commitNew, remote: "10.0.1.12:51820", enrolled: true, occurred_at: ago(6) },
          { id: 40, cluster: "prod-cn", instance: "web-02", key: "prod-cn/web-02", generation: 3, event: "offline", release_commit: commitNew, remote: "10.0.1.12:51814", enrolled: true, occurred_at: ago(6.05) },
        ]
      : []),
    ...(isAdmin && (!cluster || cluster === "staging")
      ? [
          { id: 38, cluster: "staging", instance: "worker-legacy", key: "staging/worker-legacy", generation: 2, event: "offline", release_commit: commitOld, remote: "10.0.2.45:49100", enrolled: true, occurred_at: ago(5) },
        ]
      : []),
  ]

  return {
    scope: isAdmin ? "global" : "self",
    range,
    ...(cluster ? { cluster } : {}),
    from_ms,
    to_ms: now,
    clusters,
    agent_summary: {
      online: onlineAgents.length,
      active: onlineAgents.filter((a) => a.active_ops > 0 || a.queued_ops > 0).length,
      busy: onlineAgents.filter((a) => a.busy).length,
      recent_offline: agents.filter((a) => a.activity === "recent_offline").length,
      history: 0,
      archived: 0,
      enrolled_online: onlineAgents.filter((a) => a.enrolled).length,
      unenrolled_online: onlineAgents.filter((a) => !a.enrolled).length,
      flapping_agents: agents.filter((a) => a.window_connects >= 2 || (a.window_connects >= 1 && a.window_disconnects >= 1)).length,
      active_ops: runningCount,
      queued_ops: onlineAgents.reduce((s, a) => s + a.queued_ops, 0),
      active_sessions: onlineAgents.reduce((s, a) => s + a.sessions.length, 0),
      resource_locks: onlineAgents.reduce((s, a) => s + a.resources.length, 0),
      versions: [...versionMap.entries()].map(([release_commit, count]) => ({ release_commit, count })),
    },
    agents,
    connection_events,
    load_trend,
    usage_summary: {
      total: totalCalls,
      running: runningCount,
      success: successCount,
      failed: totalFailed,
      outcome_unknown: (!cluster || cluster === "staging") ? 1 : 0,
      avg_duration_ms: 1840,
      bytes_in: bytesIn,
      bytes_out: bytesOut,
      input_tokens: inputTokens,
      output_tokens: outputTokens,
      total_tokens: totalTokens,
      active_users: isAdmin ? (cluster === "staging" ? 2 : 3) : 1,
      active_targets: agents.filter((a) => a.window_calls > 0).length,
      active_models: 2,
    },
    trend,
    by_action: [
      { action: "ask", total: Math.round(totalCalls * 0.42), success: Math.round(totalCalls * 0.4), failed: Math.min(1, totalFailed), running: 1, avg_duration_ms: 3420, bytes_in: Math.round(bytesIn * 0.45), bytes_out: Math.round(bytesOut * 0.52), total_tokens: totalTokens },
      { action: "exec", total: Math.round(totalCalls * 0.31), success: Math.round(totalCalls * 0.3), failed: Math.max(0, totalFailed - 1), running: Math.max(0, runningCount - 1), avg_duration_ms: 920, bytes_in: Math.round(bytesIn * 0.25), bytes_out: Math.round(bytesOut * 0.28), total_tokens: 0 },
      { action: "check", total: Math.round(totalCalls * 0.15), success: Math.round(totalCalls * 0.15), failed: 0, running: 0, avg_duration_ms: 480, bytes_in: Math.round(bytesIn * 0.12), bytes_out: Math.round(bytesOut * 0.1), total_tokens: 0 },
      { action: "write", total: Math.max(1, totalCalls - Math.round(totalCalls * 0.88)), success: Math.max(1, totalCalls - Math.round(totalCalls * 0.88)), failed: 0, running: 0, avg_duration_ms: 610, bytes_in: Math.round(bytesIn * 0.18), bytes_out: Math.round(bytesOut * 0.1), total_tokens: 0 },
    ],
    by_model: [
      { model: "openai/gpt-5.2", total: Math.max(1, Math.round(totalCalls * 0.28)), input_tokens: Math.round(inputTokens * 0.72), output_tokens: Math.round(outputTokens * 0.74), total_tokens: Math.round(totalTokens * 0.73) },
      { model: "deepseek/deepseek-v3", total: Math.max(1, Math.round(totalCalls * 0.14)), input_tokens: Math.round(inputTokens * 0.28), output_tokens: Math.round(outputTokens * 0.26), total_tokens: Math.max(0, totalTokens - Math.round(totalTokens * 0.73)) },
    ],
    by_target: agents.map((a) => ({
      cluster: a.cluster, instance: a.instance, key: a.key,
      online: a.activity === "online", status: a.status,
      total: a.window_calls,
      success: Math.max(0, a.window_calls - a.window_failed - a.active_ops),
      failed: a.window_failed,
      outcome_unknown: a.key === "staging/db-01" ? 1 : 0,
      running: a.active_ops,
      avg_duration_ms: a.busy ? 2850 : 1120,
      bytes_in: a.window_bytes_in,
      bytes_out: a.window_bytes_out,
      total_tokens: a.window_tokens,
      last_used_at: a.last_seen,
    })),
    by_user: isAdmin
      ? [
          { user_id: "usr_ops01", user_name: "ops-zhang", is_admin: false, total: Math.round(totalCalls * 0.54), success: Math.round(totalCalls * 0.52), failed: Math.min(1, totalFailed), active_targets: 2, token_count: 2, bytes_in: Math.round(bytesIn * 0.55), bytes_out: Math.round(bytesOut * 0.56), input_tokens: Math.round(inputTokens * 0.58), output_tokens: Math.round(outputTokens * 0.58), total_tokens: Math.round(totalTokens * 0.58), last_active_at: nowISO() },
          { user_id: "usr_admin01", user_name: "admin", is_admin: true, total: Math.round(totalCalls * 0.3), success: Math.round(totalCalls * 0.3), failed: 0, active_targets: agents.length, token_count: 1, bytes_in: Math.round(bytesIn * 0.3), bytes_out: Math.round(bytesOut * 0.29), input_tokens: Math.round(inputTokens * 0.27), output_tokens: Math.round(outputTokens * 0.27), total_tokens: Math.round(totalTokens * 0.27), last_active_at: ago(1) },
          { user_id: "usr_dev01", user_name: "dev-li", is_admin: false, total: Math.max(1, totalCalls - Math.round(totalCalls * 0.84)), success: Math.max(0, totalCalls - Math.round(totalCalls * 0.84) - 1), failed: 1, active_targets: 1, token_count: 1, bytes_in: Math.round(bytesIn * 0.15), bytes_out: Math.round(bytesOut * 0.15), input_tokens: Math.round(inputTokens * 0.15), output_tokens: Math.round(outputTokens * 0.15), total_tokens: Math.max(0, totalTokens - Math.round(totalTokens * 0.85)), last_active_at: ago(2) },
        ]
      : [
          { user_id: "usr_demo_self", user_name: "演示用户", is_admin: false, total: totalCalls, success: successCount, failed: totalFailed, active_targets: agents.length, token_count: 1, bytes_in: bytesIn, bytes_out: bytesOut, input_tokens: inputTokens, output_tokens: outputTokens, total_tokens: totalTokens, last_active_at: nowISO() },
        ],
    running_ops: (!cluster || cluster === "prod-cn")
      ? [
          { id: 1098, user_id: "usr_ops01", user_name: "ops-zhang", cluster: "prod-cn", instance: "web-02", action: "ask", session: "deploy-zz99", command_summary: "Helm upgrade doops-app release 1.8.3 in apps", status: "executing", correlation_id: "op-helm-prod-1098", model: "openai/gpt-5.2", total_tokens: 8420, bytes_in: 512, bytes_out: 4096, started_at: new Date(now - 42_000).toISOString(), duration_ms: 42_000 },
          { id: 1097, user_id: "usr_admin01", user_name: "admin", cluster: "prod-cn", instance: "web-01", action: "exec", session: "console-a1b2c3d4", command_summary: "kubectl top pods -n apps", status: "running", bytes_in: 48, bytes_out: 312, started_at: new Date(now - 9_000).toISOString(), duration_ms: 9_000 },
        ]
      : [],
    detached_tasks: demoBgTasks
      .filter((t) => !cluster || t.cluster === cluster)
      .map(({ output: _out, cancel_requested, ...rest }) => {
        const own = rest.instance === "web-01" || !isAdmin
        return {
          ...rest,
          user_id: own ? "usr_admin01" : "usr_ops01",
          user_name: own ? (isAdmin ? "admin" : "演示用户") : "ops-zhang",
          is_own: own,
          cancel_requested: Boolean(cancel_requested),
        }
      }),
    recent_issues: [
      ...(!cluster || cluster === "staging"
        ? [{
            id: 1086, user_id: "usr_dev01", user_name: isAdmin ? "dev-li" : "演示用户",
            cluster: "staging", instance: "db-01", action: "write", session: "console-db-check",
            command_summary: "更新 /etc/pgbouncer/pgbouncer.ini 连接池上限",
            status: "outcome_unknown", error: "agent tunnel reconnected before terminal receipt; verify target state before retry",
            correlation_id: "op-cfg-db01-1086", bytes_in: 640, bytes_out: 0,
            started_at: ago(1), ended_at: ago(1), duration_ms: 15_200,
          }]
        : []),
      ...(!cluster || cluster === "prod-cn"
        ? [{
            id: 1079, user_id: "usr_ops01", user_name: isAdmin ? "ops-zhang" : "演示用户",
            cluster: "prod-cn", instance: "web-02", action: "exec", session: "console-aa11bb22",
            command_summary: "helm upgrade api https://charts.example/api-1.8.3.tgz",
            status: "busy", error: "target_queue_timeout: exclusive operation held by deploy-zz99",
            bytes_in: 128, bytes_out: 0, started_at: ago(3), ended_at: ago(3), duration_ms: 2000,
          }]
        : []),
    ],
  }
}

const demoMonitorRules = new Map<string, { threshold: number; duration_seconds: number }>([
  ["web-02/vm-web-02/agent_host/memory/", { threshold: 24 * 1073741824, duration_seconds: 300 }],
  ["db-01/vm-db-01/agent_host/disk_write_latency_ms/vda", { threshold: 15, duration_seconds: 180 }],
])
const demoCollectedNamespaces = new Set<string>(["apps", "auth"])

export async function demoAssetMonitorRead(url: string): Promise<unknown> {
  await sleep(70)
  const parsed = new URL(url, "http://console.local")
  const q = parsed.searchParams
  const instance = q.get("instance") || "web-01"
  const typeFilter = q.get("type") || ""
  const assetId = q.get("asset_id") || ""
  const origin = q.get("origin") || ""
  const range = (q.get("range") || "1h") as "1h" | "6h" | "24h" | "7d"

  const allAssets = [
    { type: "machine", asset_id: `vm-${instance}`, namespace: "", origin: "agent_host" },
    ...(instance === "web-02"
      ? [{ type: "machine", asset_id: "phy-gpu-01", namespace: "", origin: "agent_host" }]
      : []),
    { type: "kernel", asset_id: `vm-${instance}`, namespace: "", origin: "agent_host" },
    { type: "process", asset_id: "nginx", namespace: "", origin: "exporter" },
    { type: "process", asset_id: "doops-app", namespace: "", origin: "exporter" },
    { type: "container", asset_id: "doops-app", namespace: "", origin: "container_stats" },
    { type: "container", asset_id: "nginx-gateway", namespace: "", origin: "container_stats" },
    { type: "container", asset_id: "redis-cache", namespace: "", origin: "container_stats" },
    { type: "app", asset_id: "portal.edu.example", namespace: "", origin: "app_probe" },
    { type: "app", asset_id: "nginx", namespace: "", origin: "app_probe" },
    { type: "database", asset_id: "pg-main-01", namespace: "", origin: "exporter" },
    ...[...demoCollectedNamespaces].map((ns) => ({ type: "app", asset_id: `${ns}-edu-portal-7c9d8f`, namespace: ns, origin: "k8s_top" })),
  ]

  if (!assetId) {
    return {
      assets: allAssets.filter((a) => !typeFilter || a.type === typeFilter),
    }
  }

  const now = Date.now()
  const spanMap = { "1h": 3600_000, "6h": 21_600_000, "24h": 86_400_000, "7d": 604_800_000 }
  const span = spanMap[range] || 3600_000
  const from_ms = now - span
  const mkSeries = (metric: string, unit: string, base: number, device_id?: string, constant = false) => {
    const key = `${instance}/${assetId}/${origin}/${metric}/${device_id || ""}`
    const rule = demoMonitorRules.get(key) || null
    const points = Array.from({ length: 24 }, (_, idx) => {
      const at_ms = Math.round(from_ms + (span * (idx + 1)) / 24)
      const factor = constant ? 1 : 0.8 + 0.24 * Math.sin(idx * 0.55 + metric.length)
      return { at_ms, status: "ok", value: Number((base * factor).toPrecision(5)) }
    })
    const latest = points[points.length - 1]
    const alerts = rule && latest.value > rule.threshold
      ? [{ opened_ms: now - 420_000, ended_ms: null, state: "open", threshold: rule.threshold, duration_seconds: rule.duration_seconds }]
      : []
    return {
      ...(device_id ? { device_id } : {}),
      metric,
      unit,
      latest,
      stale: false,
      points,
      rule,
      alerts,
    }
  }

  let series: unknown[] = []
  if (q.get("type") === "container") {
    const scale = assetId === "doops-app" ? 1 : assetId === "nginx-gateway" ? 0.38 : 0.22
    const limit = assetId === "doops-app" ? 2 * 1073741824 : 536870912
    series = [
      mkSeries("cpu", "cores", 0.74 * scale),
      mkSeries("memory", "bytes", 620 * 1048576 * scale),
      mkSeries("memory_limit", "bytes", limit, undefined, true),
    ]
  } else if (q.get("type") === "kernel") {
    series = [
      mkSeries("cpu", "cores", 0.28),
      mkSeries("memory", "bytes", 518 * 1048576),
    ]
  } else if (q.get("type") === "process") {
    series = [
      mkSeries("cpu", "cores", assetId === "doops-app" ? 0.52 : 0.19),
      mkSeries("memory", "bytes", (assetId === "doops-app" ? 840 : 218) * 1048576),
    ]
  } else if (q.get("type") === "database") {
    series = [
      mkSeries("response_time_ms", "ms", 4.2),
      mkSeries("active_connections", "connections", 64),
      mkSeries("requests", "req/s", 312),
    ]
  } else if (origin === "app_probe") {
    series = assetId === "nginx"
      ? [
          mkSeries("active_connections", "connections", 128),
          mkSeries("requests", "req/s", 186),
        ]
      : [
          mkSeries("response_time_ms", "ms", 42.6),
          mkSeries("error_rate", "%", 0.18),
        ]
  } else if (origin === "k8s_top") {
    series = [
      mkSeries("cpu", "cores", 0.46),
      mkSeries("memory", "bytes", 384 * 1048576),
    ]
  } else if (assetId === "phy-gpu-01") {
    series = [
      mkSeries("cpu", "cores", 37.4),
      mkSeries("memory", "bytes", 342 * 1073741824),
      mkSeries("gpu", "%", 74.2, "GPU-a1b2c3d4-1111-2222-3333-444455556666"),
      mkSeries("gpu", "%", 61.5, "GPU-e5f6a7b8-7777-8888-9999-aaaabbbbcccc"),
      mkSeries("network", "bytes/s", 48_600_000, "ens3f0"),
      mkSeries("storage_total", "bytes", 8000 * 1073741824, "/data/models", true),
      mkSeries("storage", "bytes", 4920 * 1073741824, "/data/models"),
      mkSeries("storage_available", "bytes", 3080 * 1073741824, "/data/models"),
      mkSeries("disk_read_iops", "ops/s", 1420, "nvme0n1"),
      mkSeries("disk_write_iops", "ops/s", 680, "nvme0n1"),
      mkSeries("disk_read_bytes_per_second", "bytes/s", 215_000_000, "nvme0n1"),
      mkSeries("disk_write_bytes_per_second", "bytes/s", 94_000_000, "nvme0n1"),
      mkSeries("disk_read_latency_ms", "ms", 0.4, "nvme0n1"),
      mkSeries("disk_write_latency_ms", "ms", 0.7, "nvme0n1"),
    ]
  } else {
    const isWeb02 = instance === "web-02"
    const isDb01 = instance === "db-01"
    series = [
      mkSeries("cpu", "cores", isWeb02 ? 7.8 : isDb01 ? 5.2 : 1.85),
      mkSeries("memory", "bytes", (isWeb02 ? 26.8 : isDb01 ? 41.2 : 5.4) * 1073741824),
      mkSeries("network", "bytes/s", isWeb02 ? 6_420_000 : 1_850_000, "eth0"),
      mkSeries("storage_total", "bytes", (isDb01 ? 1000 : 100) * 1073741824, "/", true),
      mkSeries("storage", "bytes", (isDb01 ? 618 : 38.8) * 1073741824, "/"),
      mkSeries("storage_available", "bytes", (isDb01 ? 382 : 61.2) * 1073741824, "/"),
      mkSeries("storage_total", "bytes", 80 * 1073741824, "/var/log", true),
      mkSeries("storage", "bytes", 24 * 1073741824, "/var/log"),
      mkSeries("storage_available", "bytes", 56 * 1073741824, "/var/log"),
      mkSeries("disk_read_iops", "ops/s", isDb01 ? 820 : 145, "vda"),
      mkSeries("disk_write_iops", "ops/s", isDb01 ? 640 : 92, "vda"),
      mkSeries("disk_read_bytes_per_second", "bytes/s", isDb01 ? 28_400_000 : 3_200_000, "vda"),
      mkSeries("disk_write_bytes_per_second", "bytes/s", isDb01 ? 19_200_000 : 1_900_000, "vda"),
      mkSeries("disk_read_latency_ms", "ms", isDb01 ? 6.4 : 2.1, "vda"),
      mkSeries("disk_write_latency_ms", "ms", isDb01 ? 18.6 : 3.4, "vda"),
    ]
  }
  return { from_ms, to_ms: now, max_gap_ms: 300_000, series }
}

export async function demoAssetMonitorWrite(body: Record<string, unknown>): Promise<unknown> {
  await sleep(100)
  if (body.action === "rule") {
    const key = `${body.instance || "web-01"}/${body.asset_id}/${body.origin}/${body.metric}/${body.device_id || ""}`
    demoMonitorRules.set(key, {
      threshold: Number(body.threshold),
      duration_seconds: Number(body.duration_seconds),
    })
    return { action: "rule", accepted: 0 }
  }
  const mode = String(body.mode || "k8s_pods")
  if (mode === "containers") {
    return { action: "collect", runtime: "docker", accepted: 9 }
  }
  if (mode === "apps") {
    return { action: "collect", accepted: 4 }
  }
  if (mode === "host") {
    return { action: "collect", accepted: 14 }
  }
  if (typeof body.namespace === "string" && body.namespace.trim()) {
    demoCollectedNamespaces.add(body.namespace.trim())
  }
  return { action: "collect", accepted: 2 }
}

const demoMonitorSchedulesStore: Array<Record<string, unknown>> = []
const demoMonitorRunsStore: Array<Record<string, unknown>> = []
const demoMonitorRunItemsMap = new Map<string, Array<Record<string, unknown>>>()

export async function demoMonitorEngineRequest(path: string, body?: Record<string, unknown>): Promise<unknown> {
  await sleep(70)
  const now = Date.now()
  const parsed = new URL(`http://console.local/${path.replace(/^\/+/, "")}`)
  const route = parsed.pathname.replace(/^\/+/, "")
  const clusterFilter = parsed.searchParams.get("cluster") || ""
  const kindFilter = parsed.searchParams.get("kind") || ""
  const tagFilter = parsed.searchParams.get("tag") || ""
  const statusFilter = parsed.searchParams.get("status") || ""

  const strategies = [
    { id: "vm_host", name: "虚机 / 物理机本机策略", description: "直采 CPU、内存、磁盘 I/O、网络、GPU 及 M1–M5 巡检。", asset_kinds: ["vm", "physical"], collector_method: "agent_local", checks: ["M1", "M2", "M3", "M4", "M5"] },
    { id: "ssh_host", name: "SSH 远程主机策略", description: "经结构化只读 SSH 采集无 Agent 主机指标与巡检项。", asset_kinds: ["vm", "physical"], collector_method: "ssh", checks: ["SSH连通性", "M1", "M2", "磁盘容量"] },
    { id: "k8s_cluster", name: "Kubernetes 集群策略", description: "采集节点健康、Pod 异常、副本、PVC 与 Warning 事件。", asset_kinds: ["k8s"], collector_method: "k8s_api", checks: ["节点健康", "Pod状态", "控制器副本"] },
    { id: "container_runtime", name: "容器运行时策略", description: "采集 Docker/nerdctl/Podman 容器 CPU 与内存限额。", asset_kinds: ["container"], collector_method: "agent_local", checks: ["运行时可用性", "内存限额"] },
    { id: "app_service", name: "应用 / Web / API 策略", description: "采集 Nginx 并发、请求速率、响应时间及 HTTPS 安全头。", asset_kinds: ["app"], collector_method: "http_probe", checks: ["A1可用性", "响应时间", "A4安全头"] },
    { id: "database_service", name: "数据库策略", description: "探测已登记数据库连通性与延迟。", asset_kinds: ["database"], collector_method: "tcp_probe", checks: ["A2连通性", "建连延迟"] },
  ]

  const allResources = [
    {
      id: 1, cluster: "prod-cn", instance: "web-01", asset_id: "vm-web-01", asset_name: "教务门户节点 01", asset_kind: "vm",
      strategy: "vm_host", collector_method: "agent_local", collected_at_ms: now - 25_000, run_id: "run_demo04", status: "ok",
      online: true, stale: false, cpu_cores: 1.85, cpu_total_cores: 8, cpu_percent: 23.1,
      memory_used_bytes: 5.4 * 1073741824, memory_total_bytes: 16 * 1073741824, memory_percent: 33.8,
      disk_used_bytes: 62.8 * 1073741824, disk_total_bytes: 180 * 1073741824, disk_percent: 38.8,
      net_bytes_per_sec: 1_850_000, disk_iops: 237, disk_latency_ms: 2.8, uptime_seconds: 864000, load_1m: 0.82,
      os_info: "Ubuntu 24.04 LTS", kernel_info: "Linux 6.8.0-49-generic", tags: ["env=prod", "dept=edu", "role=web"],
      details: {
        mounts: [
          { mount: "/", total_bytes: 100 * 1073741824, used_bytes: 38.8 * 1073741824, avail_bytes: 61.2 * 1073741824, used_pct: 38.8 },
          { mount: "/var/log", total_bytes: 80 * 1073741824, used_bytes: 24 * 1073741824, avail_bytes: 56 * 1073741824, used_pct: 30.0 },
        ],
        disks: [
          { device_id: "vda", read_iops: 145, write_iops: 92, read_bytes_per_sec: 3_200_000, write_bytes_per_sec: 1_900_000, read_latency_ms: 2.1, write_latency_ms: 3.4 },
        ],
        networks: [{ device_id: "eth0", bytes_per_sec: 1_850_000 }],
      },
    },
    {
      id: 2, cluster: "prod-cn", instance: "web-02", asset_id: "phy-gpu-01", asset_name: "科研算力 GPU 物理机 G01", asset_kind: "physical",
      strategy: "vm_host", collector_method: "agent_local", collected_at_ms: now - 30_000, run_id: "run_demo04", status: "ok",
      online: true, stale: false, cpu_cores: 37.4, cpu_total_cores: 64, cpu_percent: 58.4,
      memory_used_bytes: 342 * 1073741824, memory_total_bytes: 512 * 1073741824, memory_percent: 66.8,
      disk_used_bytes: 4920 * 1073741824, disk_total_bytes: 8000 * 1073741824, disk_percent: 61.5,
      net_bytes_per_sec: 48_600_000, disk_iops: 2100, disk_latency_ms: 0.7, gpu_percent: 74.2, uptime_seconds: 2_592_000, load_1m: 14.6,
      os_info: "Ubuntu 24.04 LTS", kernel_info: "Linux 6.8.0-49-generic", tags: ["env=prod", "rack=G01", "gpu=a100", "dept=research"],
      details: {
        mounts: [
          { mount: "/", total_bytes: 500 * 1073741824, used_bytes: 165 * 1073741824, avail_bytes: 335 * 1073741824, used_pct: 33.0 },
          { mount: "/data/models", total_bytes: 7500 * 1073741824, used_bytes: 4755 * 1073741824, avail_bytes: 2745 * 1073741824, used_pct: 63.4 },
        ],
        disks: [
          { device_id: "nvme0n1", read_iops: 1420, write_iops: 680, read_bytes_per_sec: 215_000_000, write_bytes_per_sec: 94_000_000, read_latency_ms: 0.4, write_latency_ms: 0.7 },
        ],
        networks: [
          { device_id: "ens3f0", bytes_per_sec: 48_600_000 },
          { device_id: "ib0", bytes_per_sec: 128_000_000 },
        ],
        gpus: [
          { uuid: "GPU-a1b2c3d4-1111-2222-3333-444455556666", name: "NVIDIA A100-SXM4-80GB #0", util_percent: 74.2, mem_used_mib: 58420, mem_total_mib: 81920, temp_c: 67 },
          { uuid: "GPU-e5f6a7b8-7777-8888-9999-aaaabbbbcccc", name: "NVIDIA A100-SXM4-80GB #1", util_percent: 61.5, mem_used_mib: 49152, mem_total_mib: 81920, temp_c: 63 },
        ],
      },
    },
    {
      id: 3, cluster: "prod-cn", instance: "web-02", asset_id: "k8s-prod-cn", asset_name: "核心业务生产集群 (K8s)", asset_kind: "k8s",
      strategy: "k8s_cluster", collector_method: "k8s_api", collected_at_ms: now - 30_000, run_id: "run_demo04", status: "warning",
      online: true, stale: false, cpu_cores: 18.4, cpu_total_cores: 32, cpu_percent: 57.5,
      memory_used_bytes: 53.6 * 1073741824, memory_total_bytes: 64 * 1073741824, memory_percent: 83.8,
      os_info: "K8s v1.30.2", tags: ["env=prod", "tier=core"],
      details: {
        nodes: [
          { name: "node-web-01", ready: true, pressures: [] },
          { name: "node-web-02", ready: true, pressures: ["MemoryPressure"] },
          { name: "node-gpu-01", ready: true, pressures: [] },
        ],
        abnormal_pods: [
          { namespace: "apps", name: "report-worker-7b8d", phase: "Running", reason: "HighRestarts", restarts: 6 },
        ],
      },
    },
    {
      id: 4, cluster: "prod-cn", instance: "web-02", asset_id: "docker-web-02", asset_name: "核心节点容器运行时 (Docker)", asset_kind: "container",
      strategy: "container_runtime", collector_method: "agent_local", collected_at_ms: now - 28_000, run_id: "run_demo04", status: "ok",
      online: true, stale: false, cpu_cores: 1.18, cpu_total_cores: 16, cpu_percent: 7.4,
      memory_used_bytes: 1.02 * 1073741824, memory_total_bytes: 3 * 1073741824, memory_percent: 34.0,
      os_info: "Docker 27.3.1", tags: ["env=prod", "runtime=docker"],
      details: {
        containers: [
          { name: "doops-app", cpu_cores: 0.74, memory_bytes: 620 * 1048576, memory_limit_bytes: 2 * 1073741824 },
          { name: "nginx-gateway", cpu_cores: 0.28, memory_bytes: 235 * 1048576, memory_limit_bytes: 512 * 1048576 },
          { name: "redis-cache", cpu_cores: 0.16, memory_bytes: 186 * 1048576, memory_limit_bytes: 512 * 1048576 },
        ],
      },
    },
    {
      id: 5, cluster: "prod-cn", instance: "web-01", asset_id: "ssh-legacy-01", asset_name: "教务老旧业务机 (SSH)", asset_kind: "vm",
      strategy: "ssh_host", collector_method: "ssh", collected_at_ms: now - 45_000, run_id: "run_demo04", status: "ok",
      online: true, stale: false, cpu_cores: 1.1, cpu_total_cores: 4, cpu_percent: 27.5,
      memory_used_bytes: 4.9 * 1073741824, memory_total_bytes: 8 * 1073741824, memory_percent: 61.3,
      disk_used_bytes: 68 * 1073741824, disk_total_bytes: 100 * 1073741824, disk_percent: 68.0,
      uptime_seconds: 1_920_000, load_1m: 1.05, os_info: "Rocky Linux 9.4", tags: ["env=prod", "via=ssh", "dept=edu"],
      details: { mounts: [{ mount: "/", total_bytes: 100 * 1073741824, used_bytes: 68 * 1073741824, avail_bytes: 32 * 1073741824, used_pct: 68.0 }] },
    },
    {
      id: 6, cluster: "prod-cn", instance: "web-01", asset_id: "ssh-archive-02", asset_name: "财务归档服务器 (SSH)", asset_kind: "vm",
      strategy: "ssh_host", collector_method: "ssh", collected_at_ms: now - 50_000, run_id: "run_demo04", status: "warning",
      online: true, stale: false, cpu_cores: 2.2, cpu_total_cores: 8, cpu_percent: 27.5,
      memory_used_bytes: 10.6 * 1073741824, memory_total_bytes: 16 * 1073741824, memory_percent: 66.3,
      disk_used_bytes: 438 * 1073741824, disk_total_bytes: 500 * 1073741824, disk_percent: 87.6,
      uptime_seconds: 4_120_000, load_1m: 1.42, os_info: "CentOS Stream 9", tags: ["env=prod", "via=ssh", "dept=finance"],
      details: { mounts: [{ mount: "/", total_bytes: 500 * 1073741824, used_bytes: 438 * 1073741824, avail_bytes: 62 * 1073741824, used_pct: 87.6 }] },
    },
    {
      id: 7, cluster: "prod-cn", instance: "web-01", asset_id: "portal.edu.example", asset_name: "教务门户站点 (HTTPS)", asset_kind: "app",
      strategy: "app_service", collector_method: "http_probe", collected_at_ms: now - 25_000, run_id: "run_demo04", status: "ok",
      online: true, stale: false, response_time_ms: 42.6, error_rate: 0.2, active_connections: 128, requests_per_sec: 186.4,
      tags: ["env=prod", "dept=edu", "proto=https"],
      details: {},
    },
    {
      id: 8, cluster: "staging", instance: "db-01", asset_id: "pg-main-01", asset_name: "PostgreSQL 核心主库 (TCP)", asset_kind: "database",
      strategy: "database_service", collector_method: "tcp_probe", collected_at_ms: now - 30_000, run_id: "run_demo04", status: "ok",
      online: true, stale: false, response_time_ms: 4.2, error_rate: 0, active_connections: 64, requests_per_sec: 312,
      tags: ["env=staging", "dept=finance", "db=postgres"],
      details: {},
    },
  ]

  const sampleResources = allResources.filter((r) => {
    if (clusterFilter && r.cluster !== clusterFilter) return false
    if (kindFilter && r.asset_kind !== kindFilter) return false
    if (tagFilter && !r.tags.includes(tagFilter)) return false
    return true
  })

  const allAlerts = [
    {
      cluster: "prod-cn", instance: "web-02", type: "machine", asset_id: "vm-web-02",
      origin: "agent_host", metric: "memory",
      threshold: 24 * 1073741824, duration_seconds: 300, opened_ms: now - 420_000,
    },
    {
      cluster: "staging", instance: "db-01", type: "machine", asset_id: "vm-db-01",
      origin: "agent_host", metric: "disk_write_latency_ms", device_id: "vda",
      threshold: 15, duration_seconds: 180, opened_ms: now - 260_000,
    },
  ]
  const thresholdAlerts = clusterFilter
    ? allAlerts.filter((a) => a.cluster === clusterFilter)
    : allAlerts

  const defaultThresholds = {
    cpu_warn_percent: 80, cpu_crit_percent: 90,
    mem_warn_percent: 80, mem_crit_percent: 90,
    disk_warn_percent: 85, disk_crit_percent: 95,
    disk_latency_warn_ms: 50, latency_warn_ms: 1000,
    error_rate_warn_pct: 5, cert_warn_days: 14, min_uptime_seconds: 300,
  }

  const seedSchedules = [
    {
      id: "sched_demo01", name: "全校核心基础设施 5 分钟自动巡检",
      description: "覆盖虚机/GPU物理机、SSH 远程主机、K8s、容器、站点与数据库全策略巡检",
      enabled: true, scope_mode: "all_online", targets: [], tags: [],
      strategies: ["vm_host", "ssh_host", "k8s_cluster", "container_runtime", "app_service", "database_service"],
      thresholds: defaultThresholds,
      interval_seconds: 300, next_run_at_ms: now + 240_000, last_run_at_ms: now - 60_000, last_run_id: "run_demo04", last_status: "warning",
      created_by: "admin", created_at: new Date(now - 7 * 86400_000).toISOString(), updated_at: new Date(now - 60_000).toISOString(),
    },
    {
      id: "sched_demo02", name: "重点门户站点与数据库 15 分钟拨测",
      description: "专注 HTTPS 可用性、证书剩余天数、Nginx 并发与 PostgreSQL 建连延迟",
      enabled: true, scope_mode: "targets", targets: [{ cluster: "prod-cn", instance: "web-01" }, { cluster: "staging", instance: "db-01" }], tags: [],
      strategies: ["app_service", "database_service"],
      thresholds: { ...defaultThresholds, latency_warn_ms: 300, cert_warn_days: 30 },
      interval_seconds: 900, next_run_at_ms: now + 540_000, last_run_at_ms: now - 360_000, last_run_id: "run_demo02", last_status: "pass",
      created_by: "admin", created_at: new Date(now - 5 * 86400_000).toISOString(), updated_at: new Date(now - 360_000).toISOString(),
    },
    {
      id: "sched_demo03", name: "GPU 算力节点每小时深度合规巡检",
      description: "针对带 gpu=a100 标签的高密度算力物理机核查 GPU 温度、显存与 M4/M5 安全基线",
      enabled: false, scope_mode: "tags", targets: [], tags: ["gpu=a100"],
      strategies: ["vm_host", "container_runtime"],
      thresholds: defaultThresholds,
      interval_seconds: 3600, next_run_at_ms: 0, last_run_at_ms: now - 4 * 3600_000, last_run_id: "run_demo01", last_status: "critical",
      created_by: "admin", created_at: new Date(now - 3 * 86400_000).toISOString(), updated_at: new Date(now - 2 * 3600_000).toISOString(),
    },
  ]

  const buildStandardItems = (runId: string, baseMs: number, includeOffline = false, includeCriticalK8s = false): Array<Record<string, unknown>> => [
    {
      id: 1, run_id: runId, cluster: "prod-cn", instance: "web-01", asset_id: "vm-web-01", asset_name: "教务门户节点 01", asset_kind: "vm",
      strategy: "vm_host", collector_method: "agent_local", status: "pass", started_at_ms: baseMs, finished_at_ms: baseMs + 420, duration_ms: 420,
      metrics: { cpu_percent: 23.1, memory_percent: 33.8, disk_percent: 38.8, memory_used_bytes: 5.4 * 1073741824, os_info: "Ubuntu 24.04 LTS" },
      checks: [
        { id: "host.uptime", name: "运行与重启 (M1)", category: "availability", status: "pass", value: "864000s", threshold: ">=300s", detail: "运行稳定，近 10 天无异常重启" },
        { id: "host.cpu_memory", name: "CPU 与内存水位 (M2)", category: "resource", status: "pass", value: "CPU 23.1% / 内存 33.8%", threshold: "<80%", detail: "水位正常" },
        { id: "host.disk_capacity", name: "磁盘存储容量", category: "resource", status: "pass", value: "/ 38.8%", threshold: "<85%", detail: "检查 2 个挂载点，最高占用 38.8%" },
        { id: "host.disk_io", name: "磁盘 I/O 与延迟", category: "resource", status: "pass", value: "237 ops/s · 3.4ms", threshold: "<50ms", detail: "vda 读写延迟正常" },
        { id: "host.processes", name: "进程与可疑进程 (M3)", category: "security", status: "pass", value: "142 进程 / 0 僵尸", threshold: "无黑名单进程", detail: "正常" },
        { id: "host.sysctl_compliance", name: "内核配置合规 (M4)", category: "compliance", status: "pass", value: "4/4 合规", threshold: "全部基线符合", detail: "全部满足加固基线" },
        { id: "host.auth_security", name: "登录与权限基线 (M5)", category: "security", status: "pass", value: "0 异常失败登录", threshold: "账户基线可核验", detail: "指纹一致" },
      ],
      evidence: { mounts: [{ mount: "/", used_pct: 38.8 }, { mount: "/var/log", used_pct: 30.0 }], load_1m: 0.82 },
    },
    {
      id: 2, run_id: runId, cluster: "prod-cn", instance: "web-02", asset_id: "phy-gpu-01", asset_name: "科研算力 GPU 物理机 G01", asset_kind: "physical",
      strategy: "vm_host", collector_method: "agent_local", status: "pass", started_at_ms: baseMs + 100, finished_at_ms: baseMs + 680, duration_ms: 580,
      metrics: { cpu_percent: 58.4, memory_percent: 66.8, disk_percent: 63.4, gpu_percent: 74.2, os_info: "Ubuntu 24.04 LTS" },
      checks: [
        { id: "host.cpu_memory", name: "CPU 与内存水位 (M2)", category: "resource", status: "pass", value: "CPU 58.4% / 内存 66.8%", threshold: "<80%", detail: "算力负载健康" },
        { id: "host.gpu", name: "NVIDIA GPU 状态", category: "resource", status: "pass", value: "2 卡在线 · 峰值 74.2% / 67°C", threshold: "<95% / <85°C", detail: "A100 #0 74.2%，A100 #1 61.5%" },
        { id: "host.disk_io", name: "NVMe 磁盘 I/O", category: "resource", status: "pass", value: "2100 ops/s · 0.7ms", threshold: "<50ms", detail: "nvme0n1 读写吞吐正常" },
      ],
      evidence: { gpu_count: 2, max_temp_c: 67, nvme_iops: 2100 },
    },
    {
      id: 3, run_id: runId, cluster: "prod-cn", instance: "web-02", asset_id: "k8s-prod-cn", asset_name: "核心业务生产集群 (K8s)", asset_kind: "k8s",
      strategy: "k8s_cluster", collector_method: "k8s_api", status: includeCriticalK8s ? "critical" : "warning", started_at_ms: baseMs + 200, finished_at_ms: baseMs + 1050, duration_ms: 850,
      metrics: { cpu_percent: 57.5, memory_percent: 83.8, os_info: "K8s v1.30.2" },
      checks: [
        { id: "k8s.nodes", name: "K8s 节点健康", category: "availability", status: "warning", value: "3 节点 Ready · 1 节点 MemoryPressure", threshold: "0 压力节点", detail: "node-web-02 报告 MemoryPressure" },
        {
          id: "k8s.pods", name: "工作负载 Pod 状态", category: "workload",
          status: includeCriticalK8s ? "critical" : "warning",
          value: includeCriticalK8s ? "1 CrashLoopBackOff · 1 高重启" : "1 异常重启",
          threshold: "0 异常 Pod",
          detail: includeCriticalK8s ? "batch/etl-sync-9x2k CrashLoopBackOff（重启 14 次）" : "apps/report-worker-7b8d 重启 6 次",
        },
        { id: "k8s.controllers", name: "Deployment / StatefulSet 副本", category: "workload", status: "pass", value: "14/14 就绪", threshold: "期望与可用一致", detail: "核心控制器副本全部就绪" },
      ],
      evidence: { nodes: 3, memory_pressure_nodes: ["node-web-02"], abnormal_pods: includeCriticalK8s ? 2 : 1 },
    },
    {
      id: 4, run_id: runId, cluster: "prod-cn", instance: "web-02", asset_id: "docker-web-02", asset_name: "核心节点容器运行时 (Docker)", asset_kind: "container",
      strategy: "container_runtime", collector_method: "agent_local", status: "pass", started_at_ms: baseMs + 300, finished_at_ms: baseMs + 720, duration_ms: 420,
      metrics: { cpu_percent: 7.4, memory_percent: 34.0, os_info: "Docker 27.3.1" },
      checks: [
        { id: "container.runtime", name: "容器运行时可用性", category: "runtime", status: "pass", value: "docker · 3 运行中", threshold: "运行时在线", detail: "doops-app / nginx-gateway / redis-cache 均正常" },
        { id: "container.memory_limit", name: "容器内存限额占用", category: "resource", status: "pass", value: "最高 45.9% (nginx-gateway)", threshold: "<85%", detail: "无容器逼近 OOM 上限" },
      ],
      evidence: { runtime: "docker", running_containers: 3 },
    },
    {
      id: 5, run_id: runId, cluster: "prod-cn", instance: "web-01", asset_id: "ssh-archive-02", asset_name: "财务归档服务器 (SSH)", asset_kind: "vm",
      strategy: "ssh_host", collector_method: "ssh", status: "warning", started_at_ms: baseMs + 400, finished_at_ms: baseMs + 1310, duration_ms: 910,
      metrics: { cpu_percent: 27.5, memory_percent: 66.3, disk_percent: 87.6, os_info: "CentOS Stream 9" },
      checks: [
        { id: "ssh.reachability", name: "只读 SSH 连通性", category: "availability", status: "pass", value: "reachable (10.0.3.42:22)", threshold: "BatchMode=yes", detail: "只读结构化采集成功" },
        { id: "ssh.disk", name: "远程主机磁盘容量", category: "storage", status: "warning", value: "/ 87.6%", threshold: "告警 85% / 严重 95%", detail: "归档分区占用超过 85%，建议清理历史快照" },
      ],
      evidence: { collector: "ssh_batch_readonly", root_used_pct: 87.6 },
    },
    {
      id: 6, run_id: runId, cluster: "prod-cn", instance: "web-01", asset_id: "portal.edu.example", asset_name: "教务门户站点 (HTTPS)", asset_kind: "app",
      strategy: "app_service", collector_method: "http_probe", status: "pass", started_at_ms: baseMs + 500, finished_at_ms: baseMs + 690, duration_ms: 190,
      metrics: { response_time_ms: 42.6, error_rate: 0.2, active_connections: 128, requests_per_sec: 186.4 },
      checks: [
        { id: "app.availability", name: "HTTPS 可用性与响应 (A1)", category: "availability", status: "pass", value: "HTTP 200 · 42.6ms", threshold: "<1000ms", detail: "状态正常，错误率 0.2%" },
        { id: "app.tls_security", name: "证书有效期与安全头 (A4)", category: "security", status: "pass", value: "剩余 68 天 · HSTS/nosniff", threshold: ">=14 天", detail: "TLS 1.3 + 完整安全响应头" },
        { id: "app.nginx", name: "Nginx 并发与请求速率", category: "resource", status: "pass", value: "128 并发 · 186.4 req/s", threshold: "stub_status 可达", detail: "吞吐平稳" },
      ],
      evidence: { status_code: 200, tls_days_remaining: 68, hsts: true },
    },
    {
      id: 7, run_id: runId, cluster: "staging", instance: "db-01", asset_id: "pg-main-01", asset_name: "PostgreSQL 核心主库 (TCP)", asset_kind: "database",
      strategy: "database_service", collector_method: "tcp_probe", status: "pass", started_at_ms: baseMs + 600, finished_at_ms: baseMs + 615, duration_ms: 15,
      metrics: { response_time_ms: 4.2, error_rate: 0 },
      checks: [
        { id: "db.connectivity", name: "数据库 TCP 连通与延迟 (A2)", category: "availability", status: "pass", value: "4.2ms (10.0.2.21:5432)", threshold: "<500ms", detail: "TCP 握手成功，连接池健康" },
      ],
      evidence: { host: "10.0.2.21", port: 5432, latency_ms: 4.2 },
    },
    ...(includeOffline
      ? [{
          id: 8, run_id: runId, cluster: "staging", instance: "worker-legacy", asset_id: "vm-worker-legacy", asset_name: "离线计算测试节点", asset_kind: "vm",
          strategy: "vm_host", collector_method: "agent_local", status: "offline", reason: "target_offline",
          started_at_ms: baseMs + 650, finished_at_ms: baseMs + 652, duration_ms: 2,
          metrics: {},
          checks: [
            { id: "target.online", name: "Agent 在线可达性", category: "availability", status: "offline", value: "offline", threshold: "online", detail: "目标 staging/worker-legacy 当前离线，记为巡检缺口" },
          ],
          evidence: { reason: "target_offline" },
        }]
      : []),
  ]

  if (demoMonitorSchedulesStore.length === 0) {
    demoMonitorSchedulesStore.push(...seedSchedules)
  }
  if (demoMonitorRunsStore.length === 0) {
    const seedRuns = [
      {
        id: "run_demo04", schedule_id: "sched_demo01", schedule_name: "全校核心基础设施 5 分钟自动巡检", trigger_kind: "scheduled",
        triggered_by: "system", status: "warning", total_targets: 3, total_assets: 7, passed_checks: 16, warning_checks: 3,
        failed_checks: 0, skipped_checks: 0, offline_targets: 0, metrics_written: 124,
        started_at_ms: now - 65_000, finished_at_ms: now - 60_000, duration_ms: 5000,
        summary: "7 项资产完成巡检：通过 16 项，告警 3 项（K8s 节点内存压力、归档服务器磁盘 87.6%）",
      },
      {
        id: "run_demo03", schedule_id: "", schedule_name: "全量节点手动核查", trigger_kind: "manual",
        triggered_by: "admin", status: "partial", total_targets: 4, total_assets: 8, passed_checks: 16, warning_checks: 3,
        failed_checks: 0, skipped_checks: 0, offline_targets: 1, metrics_written: 124,
        started_at_ms: now - 25 * 60_000, finished_at_ms: now - 25 * 60_000 + 5400, duration_ms: 5400,
        summary: "部分完成：离线缺口 1（staging/worker-legacy），通过 16，告警 3",
      },
      {
        id: "run_demo02", schedule_id: "sched_demo02", schedule_name: "重点门户站点与数据库 15 分钟拨测", trigger_kind: "scheduled",
        triggered_by: "system", status: "pass", total_targets: 2, total_assets: 2, passed_checks: 4, warning_checks: 0,
        failed_checks: 0, skipped_checks: 0, offline_targets: 0, metrics_written: 18,
        started_at_ms: now - 6 * 60_000, finished_at_ms: now - 6 * 60_000 + 620, duration_ms: 620,
        summary: "全部通过：门户 HTTPS (42.6ms) 与 PostgreSQL 主库 (4.2ms) 正常",
      },
      {
        id: "run_demo01", schedule_id: "sched_demo01", schedule_name: "全校核心基础设施 5 分钟自动巡检", trigger_kind: "scheduled",
        triggered_by: "system", status: "critical", total_targets: 3, total_assets: 7, passed_checks: 15, warning_checks: 2,
        failed_checks: 1, skipped_checks: 0, offline_targets: 0, metrics_written: 120,
        started_at_ms: now - 90 * 60_000, finished_at_ms: now - 90 * 60_000 + 5800, duration_ms: 5800,
        summary: "发现 1 项严重异常（K8s batch/etl-sync-9x2k CrashLoopBackOff），通过 15，告警 2",
      },
    ]
    demoMonitorRunsStore.push(...seedRuns)
    demoMonitorRunItemsMap.set("run_demo04", buildStandardItems("run_demo04", now - 65_000, false, false))
    demoMonitorRunItemsMap.set("run_demo03", buildStandardItems("run_demo03", now - 25 * 60_000, true, false))
    demoMonitorRunItemsMap.set("run_demo02", buildStandardItems("run_demo02", now - 6 * 60_000, false, false).slice(5, 7))
    demoMonitorRunItemsMap.set("run_demo01", buildStandardItems("run_demo01", now - 90 * 60_000, false, true))
  }

  if (route === "overview") {
    const byKind: Record<string, number> = {}
    for (const r of sampleResources) {
      byKind[r.asset_kind] = (byKind[r.asset_kind] || 0) + 1
    }
    const cpuItems = sampleResources.filter((r) => typeof r.cpu_percent === "number")
    const memItems = sampleResources.filter((r) => typeof r.memory_percent === "number")
    const diskItems = sampleResources.filter((r) => typeof r.disk_percent === "number")
    const hostItems = sampleResources.filter((r) => r.asset_kind === "vm" || r.asset_kind === "physical")
    const rangeParam = parsed.searchParams.get("range") || "1h"
    const spanMap: Record<string, { ms: number; buckets: number }> = {
      "1h": { ms: 3600_000, buckets: 12 },
      "6h": { ms: 6 * 3600_000, buckets: 18 },
      "24h": { ms: 24 * 3600_000, buckets: 24 },
      "7d": { ms: 7 * 86400_000, buckets: 21 },
    }
    const sp = spanMap[rangeParam] || spanMap["1h"]
    return {
      scope: "global", range: rangeParam, from_ms: now - sp.ms, to_ms: now,
      summary: {
        total_assets: sampleResources.length,
        online_agents: clusterFilter === "staging" ? 1 : clusterFilter === "prod-cn" ? 2 : 3,
        offline_agents: clusterFilter === "prod-cn" ? 0 : 1,
        covered_assets: sampleResources.length,
        probe_coverage_pct: sampleResources.length ? (clusterFilter === "prod-cn" ? 100 : 87.5) : 0,
        healthy_assets: sampleResources.filter((r) => r.status === "ok").length,
        warning_assets: sampleResources.filter((r) => r.status === "warning").length,
        critical_assets: sampleResources.filter((r) => r.status === "critical").length,
        offline_assets: clusterFilter === "prod-cn" ? 0 : 1,
        open_threshold_alerts: thresholdAlerts.length,
        avg_cpu_percent: cpuItems.length ? Number((cpuItems.reduce((s, r) => s + (r.cpu_percent || 0), 0) / cpuItems.length).toFixed(1)) : 0,
        avg_memory_percent: memItems.length ? Number((memItems.reduce((s, r) => s + (r.memory_percent || 0), 0) / memItems.length).toFixed(1)) : 0,
        avg_disk_percent: diskItems.length ? Number((diskItems.reduce((s, r) => s + (r.disk_percent || 0), 0) / diskItems.length).toFixed(1)) : 0,
        total_cpu_cores: hostItems.reduce((s, r) => s + (r.cpu_total_cores || 0), 0),
        used_cpu_cores: Number(hostItems.reduce((s, r) => s + (r.cpu_cores || 0), 0).toFixed(2)),
        total_memory_bytes: hostItems.reduce((s, r) => s + (r.memory_total_bytes || 0), 0),
        used_memory_bytes: hostItems.reduce((s, r) => s + (r.memory_used_bytes || 0), 0),
        total_disk_bytes: hostItems.reduce((s, r) => s + (r.disk_total_bytes || 0), 0),
        used_disk_bytes: hostItems.reduce((s, r) => s + (r.disk_used_bytes || 0), 0),
        active_schedules: demoMonitorSchedulesStore.filter((s) => s.enabled).length,
        total_runs: demoMonitorRunsStore.length,
        last_collected_at_ms: now - 25_000,
      },
      by_kind: byKind,
      resources: sampleResources,
      top_cpu: [...cpuItems].sort((a, b) => (b.cpu_percent || 0) - (a.cpu_percent || 0)).slice(0, 5),
      top_memory: [...memItems].sort((a, b) => (b.memory_percent || 0) - (a.memory_percent || 0)).slice(0, 5),
      top_disk: [...diskItems].sort((a, b) => (b.disk_percent || 0) - (a.disk_percent || 0)).slice(0, 5),
      trends: Array.from({ length: sp.buckets }, (_, i) => {
        const wave = Math.sin(i * 0.55)
        return {
          start_ms: Math.round(now - sp.ms + (sp.ms * i) / sp.buckets),
          end_ms: Math.round(now - sp.ms + (sp.ms * (i + 1)) / sp.buckets),
          samples: sampleResources.length,
          avg_cpu_percent: Number((33.8 + wave * 6.2).toFixed(1)),
          avg_memory_percent: Number((58.4 + Math.cos(i * 0.4) * 4.1).toFixed(1)),
          avg_disk_percent: Number((63.9 + (i * 0.08)).toFixed(1)),
        }
      }),
      recent_runs: demoMonitorRunsStore.slice(0, 6),
      threshold_alerts: thresholdAlerts,
      strategies,
    }
  }
  if (route === "resources/history") {
    const assetId = parsed.searchParams.get("asset_id") || "vm-web-01"
    const rangeParam = parsed.searchParams.get("range") || "1h"
    const spanMs = ({ "1h": 3600_000, "6h": 6 * 3600_000, "24h": 24 * 3600_000, "7d": 7 * 86400_000 } as Record<string, number>)[rangeParam] || 3600_000
    const match = allResources.find((r) => r.asset_id === assetId) || allResources[0]
    const points = Array.from({ length: 20 }, (_, idx) => {
      const wave = 0.86 + 0.16 * Math.sin(idx * 0.6 + assetId.length)
      return {
        ...match,
        collected_at_ms: Math.round(now - spanMs + (spanMs * (idx + 1)) / 20),
        cpu_percent: typeof match.cpu_percent === "number" ? Number(Math.min(99, match.cpu_percent * wave).toFixed(1)) : undefined,
        memory_percent: typeof match.memory_percent === "number" ? Number(Math.min(99, match.memory_percent * (0.95 + 0.06 * Math.cos(idx * 0.4))).toFixed(1)) : undefined,
        disk_percent: typeof match.disk_percent === "number" ? Number(match.disk_percent.toFixed(1)) : undefined,
        net_bytes_per_sec: typeof match.net_bytes_per_sec === "number" ? Math.round(match.net_bytes_per_sec * wave) : undefined,
      }
    })
    return {
      cluster: match.cluster, instance: match.instance, asset_kind: match.asset_kind, asset_id: match.asset_id,
      range: rangeParam, from_ms: now - spanMs, to_ms: now,
      latest: match, points,
    }
  }
  if (route === "schedules") {
    if (body) {
      const action = String(body.action || "create")
      if (action === "delete") {
        const idx = demoMonitorSchedulesStore.findIndex((s) => s.id === body.id)
        if (idx >= 0) demoMonitorSchedulesStore.splice(idx, 1)
        return { deleted: true }
      }
      if (action === "toggle") {
        const found = demoMonitorSchedulesStore.find((s) => s.id === body.id)
        if (found) {
          found.enabled = Boolean(body.enabled)
          found.updated_at = new Date(now).toISOString()
        }
        return { schedule: found || demoMonitorSchedulesStore[0] }
      }
      const next = {
        ...seedSchedules[0],
        id: String(body.id || `sched_demo_${Math.floor(now / 1000)}`),
        name: String(body.name || "新建自动巡检计划"),
        description: String(body.description || ""),
        enabled: body.enabled !== false,
        scope_mode: String(body.scope_mode || "all_online"),
        targets: Array.isArray(body.targets) ? body.targets : [],
        tags: Array.isArray(body.tags) ? body.tags : [],
        interval_seconds: Number(body.interval_seconds || 300),
        strategies: Array.isArray(body.strategies) && body.strategies.length ? body.strategies : seedSchedules[0].strategies,
        thresholds: body.thresholds && typeof body.thresholds === "object" ? { ...defaultThresholds, ...(body.thresholds as Record<string, unknown>) } : defaultThresholds,
        updated_at: new Date(now).toISOString(),
      }
      const idx = demoMonitorSchedulesStore.findIndex((s) => s.id === next.id)
      if (idx >= 0) demoMonitorSchedulesStore[idx] = next
      else demoMonitorSchedulesStore.unshift(next)
      return { schedule: next }
    }
    return {
      schedules: [...demoMonitorSchedulesStore],
      strategies,
      defaults: defaultThresholds,
    }
  }
  if (route.startsWith("runs/")) {
    const runId = route.slice("runs/".length)
    const run = demoMonitorRunsStore.find((r) => r.id === runId) || demoMonitorRunsStore[0]
    const items = demoMonitorRunItemsMap.get(String(run.id)) || buildStandardItems(String(run.id), now - 60_000)
    return { run, strategies, items }
  }
  if (body) {
    const newId = `run_demo_${Math.floor(now / 1000)}`
    const reqStrategies = Array.isArray(body.strategies) && body.strategies.length ? (body.strategies as string[]) : null
    const reqTargets = Array.isArray(body.targets) ? (body.targets as Array<{ cluster: string; instance: string }>) : []
    const sshHosts = Array.isArray(body.ssh_hosts) ? (body.ssh_hosts as Array<Record<string, unknown>>) : []

    let generatedItems = buildStandardItems(newId, now - 2200, false, false)
    if (body.retest_run_id) {
      const prevItems = demoMonitorRunItemsMap.get(String(body.retest_run_id)) || []
      const flagged = prevItems.filter((i) => ["warning", "critical", "error", "offline"].includes(String(i.status)))
      generatedItems = (flagged.length ? flagged : generatedItems.slice(0, 2)).map((it, idx) => ({
        ...it,
        id: idx + 1,
        run_id: newId,
        status: it.status === "offline" ? "offline" : "pass",
        started_at_ms: now - 1400,
        finished_at_ms: now - 200,
      }))
    } else {
      if (reqTargets.length > 0) {
        generatedItems = generatedItems.filter((it) =>
          reqTargets.some((t) => t.cluster === it.cluster && t.instance === it.instance),
        )
      }
      if (reqStrategies) {
        generatedItems = generatedItems.filter((it) => reqStrategies.includes(String(it.strategy)))
      }
      for (const h of sshHosts) {
        const addr = String(h.address || "10.0.9.50")
        const aid = String(h.asset_id || addr)
        generatedItems.push({
          id: generatedItems.length + 1,
          run_id: newId,
          cluster: reqTargets[0]?.cluster || "prod-cn",
          instance: reqTargets[0]?.instance || "web-01",
          asset_id: aid,
          asset_name: `${aid} (临时 SSH)`,
          asset_kind: "vm",
          strategy: "ssh_host",
          collector_method: "ssh",
          status: "pass",
          started_at_ms: now - 1600,
          finished_at_ms: now - 400,
          duration_ms: 1200,
          metrics: { cpu_percent: 19.4, memory_percent: 48.2, disk_percent: 54.0, os_info: "Ubuntu 22.04 LTS" },
          checks: [
            { id: "ssh.reachability", name: "只读 SSH 连通性", category: "availability", status: "pass", value: `reachable (${addr}:${Number(h.port) || 22})`, threshold: "BatchMode=yes", detail: "临时只读 SSH 巡检采集成功" },
            { id: "ssh.resource", name: "远程主机 CPU/内存/磁盘", category: "resource", status: "pass", value: "CPU 19.4% / 内存 48.2% / 盘 54.0%", threshold: "<80%", detail: "水位正常" },
          ],
          evidence: { collector: "ssh_batch_readonly", address: addr },
        })
      }
      if (generatedItems.length === 0) {
        generatedItems = buildStandardItems(newId, now - 1800, false, false).slice(0, 2)
      }
    }

    const passedChecks = generatedItems.reduce((sum, it) => sum + ((it.checks as Array<{ status: string }>).filter((c) => c.status === "pass").length), 0)
    const warnChecks = generatedItems.reduce((sum, it) => sum + ((it.checks as Array<{ status: string }>).filter((c) => c.status === "warning").length), 0)
    const failChecks = generatedItems.reduce((sum, it) => sum + ((it.checks as Array<{ status: string }>).filter((c) => ["critical", "error"].includes(c.status)).length), 0)
    const offlineCount = generatedItems.filter((it) => it.status === "offline").length
    const runStatus = failChecks > 0 ? "critical" : offlineCount > 0 ? "partial" : warnChecks > 0 ? "warning" : "pass"
    const sched = body.schedule_id ? demoMonitorSchedulesStore.find((s) => s.id === body.schedule_id) : null
    const newRun = {
      id: newId,
      schedule_id: String(body.schedule_id || ""),
      schedule_name: sched ? String(sched.name) : body.retest_run_id ? "异常项一键复检" : "手动立即巡检",
      trigger_kind: body.retest_run_id ? "retest" : "manual",
      triggered_by: "admin",
      status: runStatus,
      total_targets: new Set(generatedItems.map((i) => `${i.cluster}/${i.instance}`)).size || 1,
      total_assets: generatedItems.length,
      passed_checks: passedChecks,
      warning_checks: warnChecks,
      failed_checks: failChecks,
      skipped_checks: 0,
      offline_targets: offlineCount,
      metrics_written: generatedItems.length * 16,
      started_at_ms: now - 2200,
      finished_at_ms: now,
      duration_ms: 2200,
      summary: `本次完成 ${generatedItems.length} 项资产巡检：通过 ${passedChecks}，告警 ${warnChecks}${failChecks ? `，严重 ${failChecks}` : ""}${offlineCount ? `，离线 ${offlineCount}` : ""}；指标已同步。`,
    }
    demoMonitorRunItemsMap.set(newId, generatedItems)
    demoMonitorRunsStore.unshift(newRun)
    return { run: newRun, items: generatedItems }
  }
  const filteredRuns = statusFilter
    ? demoMonitorRunsStore.filter((r) => r.status === statusFilter)
    : [...demoMonitorRunsStore]
  return { runs: filteredRuns, strategies }
}
