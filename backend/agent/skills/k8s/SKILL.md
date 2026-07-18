---
name: k8s
description: 使用 doops_k8s 执行受约束的 Kubernetes 运维与部署
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

1. 所有 Kubernetes 读取和 mutation 都调用 `doops_k8s`，不得用 shell 执行 kubectl mutation。
2. 所有请求显式传 namespace；不得依赖 default namespace。
3. mutation 必须有 `confirm=true`，镜像发布必须使用不可变 `@sha256:` digest。
4. workload 镜像更新只调用 `set-image`；该操作使用 strategic merge，仅修改指定容器的
   image 和可选 `doops.sh/revision`，不得替换整个 containers 数组。
5. 发布后必须读取 rollout、Pod Ready、实际 imageID、重启次数、日志和任务指定的功能路径。
6. 结构化工具能力不足时明确返回 blocked，不得退回 shell mutation。

## 安全合规
- 不要在命令行明文传递 Secret
- 不读取或输出 Secret payload
- 生产环境禁止强制删除 Pod
