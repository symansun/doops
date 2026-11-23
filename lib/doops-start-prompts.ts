import type { AgentStartPromptGroup, AgentStartPromptGroups } from "@agent-cloud/agent-ui"

const operationsPrompts = [
  {
    id: "operations",
    label: { "zh-CN": "运行巡检", "en-US": "Operations" },
    prompts: [
      {
        id: "agent-health",
        label: { "zh-CN": "巡检当前目标", "en-US": "Inspect the current target" },
        prompt: {
          "zh-CN": "只读检查当前 Agent 目标的 CPU、内存、磁盘使用率和可用的运行状态，列出异常、观测时间和建议；无法采集的项目标注为未验证，不修改配置。",
          "en-US": "Run a read-only health check on the selected Agent target. Check CPU, memory, disk usage, and available runtime status. Report anomalies, observation times, and recommendations. Mark unavailable evidence as unverified and make no changes.",
        },
      },
      {
        id: "kubernetes-triage",
        label: { "zh-CN": "排查异常 Pod", "en-US": "Investigate unhealthy Pods" },
        prompt: {
          "zh-CN": "只读检查当前 Agent 所在 Kubernetes 集群中异常 Pod 的状态、重启次数、近期事件和必要的日志摘要，按命名空间列出证据和排查建议；不要重启或修改资源。",
          "en-US": "Investigate unhealthy Pods on the selected Agent's Kubernetes cluster. Check status, restart counts, recent events, and relevant log summaries. Report evidence and next steps by namespace without restarting or changing resources.",
        },
      },
    ],
  },
  {
    id: "delivery",
    label: { "zh-CN": "发布与交付", "en-US": "Delivery" },
    prompts: [
      {
        id: "helm-plan",
        label: { "zh-CN": "准备 Helm 发布方案", "en-US": "Plan a Helm release" },
        prompt: {
          "zh-CN": "先向我确认 release、命名空间、Chart 与版本、values 来源和镜像，再为当前 Agent 目标给出 Helm 变更、验收和回滚方案；只出方案，不执行发布。",
          "en-US": "Plan a Helm release for the selected Agent target. Ask me for the release, namespace, Chart/version, values source, and image. Provide the change, validation, and rollback plan only; do not deploy.",
        },
      },
      {
        id: "helm-verify",
        label: { "zh-CN": "验收 Helm 发布", "en-US": "Verify a Helm release" },
        prompt: {
          "zh-CN": "先确认要验收的命名空间和 release，再只读核对 Helm 状态和修订、工作负载就绪、实际运行镜像与近期事件，区分已验证、异常和未知；不要变更资源。",
          "en-US": "Verify a Helm release on the selected target without making changes. Confirm the namespace and release, then inspect Helm status and revision, workload readiness, running images, and recent events. Distinguish verified, failing, and unknown results.",
        },
      },
    ],
  },
  {
    id: "services",
    label: { "zh-CN": "网站与数据库", "en-US": "Web and databases" },
    prompts: [
      {
        id: "website-health",
        label: { "zh-CN": "检查网站与证书", "en-US": "Check a website and certificate" },
        prompt: {
          "zh-CN": "只读检查站点的 HTTP 状态、健康端点和 TLS 证书有效期，报告观测结果及时间。先向我确认当前目标上的站点名称或网址，不修改站点配置。",
          "en-US": "Ask me for the site name or URL on the selected target, then check HTTP status, its health endpoint, and TLS certificate expiration. Report the findings and observation time without changing site configuration.",
        },
      },
      {
        id: "database-inspect",
        label: { "zh-CN": "检查数据库连接", "en-US": "Check database connectivity" },
        prompt: {
          "zh-CN": "通过 DoSql 只读验证数据库连接，报告可用的数据库版本和状态。先让我指定当前目标上的数据库资产名；使用已绑定的凭据引用，不索取或输出凭据内容，不修改数据。",
          "en-US": "Ask me to identify a database asset on the selected target, then use DoSql for a read-only connectivity check and report the available version and status. Use the bound credential reference without requesting or exposing credential values, and do not modify data.",
        },
      },
    ],
  },
] satisfies readonly AgentStartPromptGroup[]

const doopsStartPromptGroups = {
  work: operationsPrompts,
} satisfies AgentStartPromptGroups

const nonKubernetesStartPromptGroups = {
  work: operationsPrompts.filter(group => group.id !== "delivery").map(group => ({
    ...group,
    prompts: group.prompts.filter(prompt => prompt.id !== "kubernetes-triage"),
  })),
} satisfies AgentStartPromptGroups

export function startPromptGroupsForTarget(kubernetesAvailable?: boolean): AgentStartPromptGroups {
  return kubernetesAvailable === false ? nonKubernetesStartPromptGroups : doopsStartPromptGroups
}
