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
3. mutation 必须有 `confirm=true`，镜像发布必须使用不可变 `@sha256:` digest。
4. 发布必须使用任务明确授权的 `deploy/helm/<name>/`，通过 `mcp_doops_helm`
   完整收敛明确的 release、namespace、`value_files` 和非敏感 `set_strings`。
   `value_files` 只放 `deploy/` 下的 YAML 文件路径；键值覆盖放 `set_strings`。
   `set_strings` 只允许标量叶子，禁止覆盖 map/list 父键。
   不可变镜像使用 `image.repository` 与 `image.digest`，不得使用 `image.tag`。
   `upgrade-install` 自动保留已有 release 的非镜像 values。只更新镜像也走同一 Helm
   路径，不存在直接 Kubernetes 镜像 mutation 例外。
5. 发布前必须对比当前 workload 与候选 Helm 资产中的 command、args、env、probes、
   volumeMounts 和 volumes，避免旧运行参数被意外继承。
6. 发布后必须读取 rollout、Pod Ready、实际 imageID、重启次数、日志和任务指定的功能路径。
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
