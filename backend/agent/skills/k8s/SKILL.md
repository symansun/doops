---
name: k8s
description: 用 doops 结构化工具读 Kubernetes/Helm 状态并发布业务 workload
triggers:
  keywords: [k8s, kubernetes, kubectl, pod, deployment, service, namespace, daemonset, ingress, helm, rollout, 部署, 重启, 扩容, 缩容]
  regex: "kubectl\\s+|k8s|rollout|部署.*服务|重启.*服务"
phase: [analyze, translate, cleanup]
priority: 10
max_tokens: 800
requires: []
conflicts: []
---

## 读状态：`mcp_doops_k8s_read`

| 操作 | 必填 | 例子 |
|---|---|---|
| `get` | `kind` | `kind=pods namespace=apps output=wide`；单个对象加 `name`；`selector=app=api`；`all_namespaces=true`；`output=jsonpath={.items[*].spec.containers[0].image}` |
| `describe` | `kind`、`name` | `kind=deployment name=api namespace=apps` |
| `logs` | `resource` | `resource=deployment/api namespace=apps tail=200`；崩溃前日志加 `previous=true`；`since=10m` |
| `events` | — | 整个 namespace 省略 `resource`；只看一个对象用 `resource=pod/<name>` |
| `top` | `kind` | `kind=pods namespace=apps` |
| `rollout-status` | `resource` | `resource=deployment/api namespace=apps` |
| `helm-history` / `helm-status` | `name`、`namespace` | `name=<release>`；发布前先读 history 记下回滚目标。status 只给修订、状态和时间，不给 values/manifest/notes |
| `helm-list` | — | `namespace=<ns>` 或 `all_namespaces=true` |
| `helm-values` | `name`、`namespace` | 只返回 key 路径，看 chart 支持哪些配置 |
| `configmap-keys` | — | `namespace=apps` 或 `all_namespaces=true`；可加 `name`、`selector`。只返回 ConfigMap 的键名，不返回值；不要对 ConfigMap 用 `describe` 或 `output=json` |

namespaced 资源总是带 `namespace`；`nodes` 这类集群级资源不带。kind 用 kubectl 小写名。
Secret、ServiceAccount 不开放读取。

## 发布：`mcp_doops_helm`

- `upgrade-install`：`release`、`namespace`、`chart`（HTTPS `.tgz`，或 `asset_source=workspace`
  时的 session 相对路径）、`confirm=true`；可选 `value_files`、`set_strings`、`expected_image`、
  `timeout`。只改镜像也走这里。
- `rollback`：`release`、`namespace`、`revision`、`confirm=true`。
- 工具自己做 server dry-run → helm → 核对每个渲染出的工作负载（含其它 namespace 的对象、
  hook Job、initContainers），并等滚动完成。你只需一次调用。
- 只含 Ingress 的 release 也能发：Ingress 按路由核对（class、每条 route 的 host/path/
  pathType 与后端 Service、TLS），引用的后端 Service 必须存在。Ingress 没有容器，
  这种 release 不要传 `expected_image`。
- 渲染出的对象已存在但不归 Helm 管（报 `invalid ownership metadata`）：只有任务明确要求纳入
  本 release 时才传 `take_ownership=true`；不要用 kubectl 手工补 Helm 标签。
- 失败返回 `错误码: 原因`：
  - `helm_preflight_failed`：Helm 渲染报错，原因是 Helm 的原话，通常是 values 或 chart 问题。
  - `helm_manifest_invalid` / `helm_no_workload`：chart 渲染结果有问题（没有可核对的工作负载
    或 Ingress）。
  - `helm_image_mismatch`：`expected_image` 不在渲染结果里，原因列出了实际渲染的镜像。
  - `helm_mutation_failed`：Helm 执行失败，原因是 Helm 的原话。
  - `helm_verification_failed`：线上对象和渲染结果不一致或超时未就绪，原因指出是哪个对象。
- 发布前可以对比现有 workload 和新 chart 的 command/args/env/probes/volumes，确认没有漏掉运行参数。
- 发布后用 `rollout-status`、`get pods`、`logs` 和任务给的功能路径做验收。

## 运维变更

重启、扩缩、cordon、删 Pod 由用户用 `doops k8s ... --confirm` 执行；你在 ask 里只读不改。
