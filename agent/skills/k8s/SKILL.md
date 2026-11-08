---
name: k8s
description: Kubernetes 集群运维与部署领域知识
triggers:
  keywords: [k8s, kubernetes, kubectl, pod, deployment, service, namespace, daemonset, ingress, helm, rollout, 部署, 重启, 扩容, 缩容]
  regex: "kubectl\\s+|k8s|rollout|部署.*服务|重启.*服务"
phase: [analyze, translate, cleanup]
priority: 10
max_tokens: 800
requires: [shell]
conflicts: []
---

提供 Kubernetes 检查与已授权变更的领域规则；诊断不自动升级为修复或部署。

## 目标确认

- 从本次任务及配置确认 target、cluster/context、资源类型、名称和 namespace。
  不预设 K3s、kubeconfig 路径、业务服务或镜像仓库；不把 namespace 名当作环境。
- 使用已配置的身份，按需检查 `kubectl config current-context` 和 `kubectl auth can-i`。
  不输出 kubeconfig 内容；root 不加 sudo，权限不足不自行安装 sudo、改权限或切换身份。
- 命名空间资源显式带 `-n <namespace>`；Node、Namespace、PV、CRD 等集群级资源不强加 `-n`。
  `-A` 只用于任务所需且已允许的跨命名空间检查。

## 诊断

- Pod 异常先看必要状态、有限日志和事件；CrashLoopBackOff 按需读 `--previous`，Pending 查调度原因。
- 区分 NotFound、Forbidden、认证失败和连接超时；不能把目标不可见直接归因于集群宕机。
- 读取 Secret 仅限所需元数据，不输出 `.data`、完整环境或凭据。
- 使用精确对象或已核验 label selector；不以名称子串或 `grep ... || true` 作为成功判断。

## 已授权发布

1. 确定实际控制器、容器名、目标版本与验收条件，记录变更前版本和回退方式。
   保留项目现有 Helm/manifest/CI 流程；只读、重启或扩缩请求不附带构建推送。
2. 先核验镜像来源与发布结果；按项目配置应用目标版本。`rollout restart` 只重启现有模板，
   不证明新版本已发布；不默认使用 `latest` 或修改 `imagePullPolicy`。
3. 前置操作成功后执行有超时的 rollout 检查，回读 workload 的目标镜像、generation、
   replicas、Pod Ready 和实际 imageID，再做任务要求的健康检查。
4. 超时、失败或断连时报告已知状态、未知部分及证据；核实后再决定恢复，不能自动重放变更。

资源 requests/limits 来自应用需求和实测，不固定给所有服务同一组数值。
重启、缩容、删除、端口暴露、权限及 Secret 变更均需在任务授权内；不强制删除生产 Pod，
不通过 delete/create、关闭身份校验或移除检查绕过失败。
