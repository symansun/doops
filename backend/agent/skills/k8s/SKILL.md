---
name: k8s
description: 使用 doops 结构化 MCP 执行 Kubernetes 只读诊断与 Helm 运维
triggers:
  keywords: [k8s, kubernetes, kubectl, pod, deployment, service, namespace, daemonset, ingress, helm, rollout, 部署, 重启, 扩容, 缩容]
  regex: "kubectl\\s+|k8s|rollout|部署.*服务|重启.*服务"
phase: [analyze, translate, cleanup]
priority: 10
max_tokens: 800
requires: []
conflicts: []
---

执行 Kubernetes 任务时，目标、namespace、resource、container 和镜像必须来自当前任务
或本轮只读工具证据，不得从环境名、域名、目录名或历史记录推断。

## 核心原则

1. Kubernetes 读取只调用 `mcp_doops_k8s_read`；所有发布 mutation 调用
   `mcp_doops_helm`（规范能力名 `doops_helm`）。`session_id` 由运行时绑定，模型不得
   传入 target、session 或工作区路径；不得用 shell 执行 kubectl 或 Helm mutation。
2. 所有请求显式传 namespace；不得依赖 default namespace。
3. mutation 必须有 `confirm=true`，镜像发布必须使用明确的
   `image.repository` 加一个受控版本选择器：不可变的
   `image.digest=sha256:<digest>`，或严格匹配 `image.tag=release-YYYYMMDD`。
4. 发布必须使用任务明确授权的远程 `https://`/`oci://` chart，通过 `mcp_doops_helm`
   完整收敛明确的 release、namespace、可选 HTTP(S) `value_files` 和非敏感
   `set_strings`。本地 source workspace 不属于 Helm 输入；`set_strings` 只允许
   `image.repository` 加 `image.digest` 或 `image.tag` 其中之一，禁止覆盖 map/list
   父键或混用两种镜像选择器。
   `upgrade-install` 自动保留已有 release 的非镜像 values。只更新镜像也走同一 Helm
   路径，不存在直接 Kubernetes 镜像 mutation 例外。
   对现有 release，如果任务已给出 release、namespace、chart 和受控镜像版本，
   `value_files` 可以为空；必须直接调用一次 `mcp_doops_helm upgrade-install`，
   `set_strings` 只覆盖 `image.repository` 和一个明确版本选择器。不得要求用户补充当前
   values 或候选 values 文件。工具内部会读取并保留现有 values，并自动用相同参数先
   dry-run；不得发起缺少 `confirm=true` 的试探调用，也不得询问是否启用 dry-run。
5. 发布前必须对比当前 workload 与候选 Helm 资产中的 command、args、env、probes、
   volumeMounts 和 volumes，避免旧运行参数被意外继承。
6. 发布后必须读取 rollout、最终 Deployment generation/spec/status、Pod Ready、实际
   imageID、重启次数、日志和任务指定的功能路径；只看 Helm revision 或 Pod Ready 不足以
   判定成功。
7. 结构化工具能力不足时明确返回 blocked，不得退回 shell mutation。
8. 不得用 shell 执行 kubectl mutation；shell 输出也不得作为 `ask --apply` 的成功证据。

## 参数契约

- `kind` 使用 kubectl 小写资源名，例如 `deployment`、`pods`、`services`。
- 读取单个对象使用 `operation=get`、`kind=deployment`、`name=<name>`、
  `namespace=<namespace>`、`output=yaml`。
- `output` 单独传 `json`、`yaml`、`wide` 或 `name`，不要拼进 `name`。

## 安全合规
- 不要在命令行明文传递 Secret
- `mcp_doops_k8s_read` 不得读取 Secret、ServiceAccount 或其它凭据载体
- 生产环境禁止强制删除 Pod
