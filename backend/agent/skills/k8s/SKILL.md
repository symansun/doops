---
name: k8s
description: 使用 mcp_doops_k8s 执行受约束的 Kubernetes 运维与部署
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

1. 所有 Kubernetes 读取和 mutation 都调用 `mcp_doops_k8s`，不得用 shell 执行 kubectl mutation。
2. 所有请求显式传 namespace；不得依赖 default namespace。
3. mutation 必须有 `confirm=true`，镜像发布必须使用不可变 `@sha256:` digest。
4. 调用 `set-image` 前必须对比当前 workload 与候选 Helm 资产中的 command、args、env、
   probes、volumeMounts 和 volumes；只有这些运行契约完全未变时，才允许只更新镜像。
5. 纯镜像更新只调用 `set-image`；该操作使用 strategic merge，仅修改指定容器的
   image 和可选 `doops.sh/revision`，不得替换整个 containers 数组。
6. 运行契约发生变化时必须使用任务明确授权的 Helm 资产收敛；不得用 `set-image`
   跳过参数、环境变量、探针或挂载迁移。
7. 发布后必须读取 rollout、Pod Ready、实际 imageID、重启次数、日志和任务指定的功能路径。
8. 结构化工具能力不足时明确返回 blocked，不得退回 shell mutation。

## 参数契约

- `kind` 使用 kubectl 小写资源名，例如 `deployment`、`pods`、`services`。
- 读取单个对象使用 `operation=get`、`kind=deployment`、`name=<name>`、
  `namespace=<namespace>`、`output=yaml`。
- `resource` 只用于 `deployment/<name>` 这类完整资源引用，不要传 `deployments`。
- `output` 单独传 `json`、`yaml`、`wide` 或 `name`，不要拼进 `name`。
- 为工作负载追加已有镜像拉取 Secret 时，使用 `operation=add-image-pull-secret`、
  `resource=deployment/<name>`、`pull_secret=<secret-name>`、`confirm=true`。

## 安全合规
- 不要在命令行明文传递 Secret
- 不读取或输出 Secret payload
- 生产环境禁止强制删除 Pod
