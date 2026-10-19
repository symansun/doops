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
或任务指定的仓库、Chart、values、现有 release 的本轮只读证据，不得从环境名、
域名、目录名或历史记录猜测，也不得要求将已明确资产中的事实重新粘贴到 prompt。

以下发布规则用于普通业务 workload。当前 Agent 自身镜像升级使用 CLI
`doops -session <s> upgrade --target <t> --image <repository>@sha256:<digest>`，
由集群内 Job 执行；不得在当前 Agent 进程内用 Helm 替换自身。两类路径都不以操作机
kubeconfig 或 `doops push` 为前置条件；旧 Agent 首次启用控制器需另行完成外部受控升级。

## 核心原则

1. Kubernetes 与 Helm 读取只调用 `mcp_doops_k8s_read`（含 `helm-history`/`helm-status`/
   `helm-list`/`helm-values`，可用 `selector`、`all_namespaces`、`previous`、`since`、
   `output=jsonpath=...`）；所有发布 mutation 调用
   `mcp_doops_helm`（规范能力名 `doops_helm`）。`session_id` 由运行时绑定，模型不得
   传入 target、session 或工作区根路径；chart/values 可使用任务指定的 session 相对路径。
   不得用 shell 执行 kubectl 或 Helm mutation。
2. 所有 namespaced 请求显式传 namespace；cluster-scoped 资源（例如 nodes）不传 namespace，
   不得依赖 namespaced 请求的 default namespace。
3. mutation 必须有 `confirm=true`；业务镜像键、版本格式和配置由任务指定的 Chart 定义，
   不作为 DoOps 全局前置闸门。
4. 通过 `mcp_doops_helm` 完整收敛明确的 release、namespace 和任务指定的 chart。
   `value_files` 可选，覆盖遵循 Chart schema；普通升级统一 `--reset-values`，不隐式
   继承历史 values。只更新镜像也走同一 Helm 路径，不存在直接 Kubernetes 镜像 mutation 例外。
   工具自动以相同输入先执行 server dry-run；不得用缺少 `confirm=true` 的调用试探，
   不得询问是否启用 dry-run。保留受管 Helm 认证、精确 scope、包 digest 钉住和 session
   路径隔离；可选 `expected_image` 只接受显式输入，不从 values 推导。
   资产与完整迁移契约见 [自然语言发布文档](../../../docs/NATURAL_LANGUAGE_DEPLOYMENT.md)；
   remote 支持 HTTPS `.tgz`，当前不支持 `oci://`。
5. 发布前必须对比当前 workload 与候选 Helm 资产中的 command、args、env、probes、
   volumeMounts 和 volumes，避免旧运行参数被意外继承。
6. 发布后必须读取 rollout、最终 Deployment generation/spec/status、Pod Ready、实际
   imageID、重启次数、日志和任务指定的功能路径；只看 Helm revision 或 Pod Ready 不足以
   判定成功。
7. 结构化工具能力不足时明确返回 blocked，不得退回 shell mutation。
8. 不得用 shell 执行 kubectl mutation；shell 输出也不得作为 `ask --apply` 的成功证据。

## 参数契约

- `kind` 使用 kubectl 小写资源名，例如 `deployment`、`pods`、`services`。
- 结构化工具参数按操作固定：`get`/`top` 必须传 `kind`，`describe` 必须传 `kind` 和 `name`，
  `logs`/`rollout-status` 必须传 `resource`；不要先发送缺字段请求等待工具报错。
- 读取单个对象使用 `operation=get`、`kind=deployment`、`name=<name>`、
  `namespace=<namespace>`、`output=yaml`。
- 集群健康排查直接执行以下结构化读取，不要求用户粘贴 kubectl 输出，也不调用 Bash：
  1. `operation=get`、`kind=nodes`、`output=wide`；Node 是集群级资源，不传 namespace。
  2. `operation=get`、`kind=pods`、`namespace=<明确 namespace>`、`output=wide`。
  3. `operation=events`、`namespace=<明确 namespace>`；查询 namespace 的全部事件时省略 `resource`。
- `operation=events` 的 `resource` 只有在按对象过滤时才填写，格式必须是
  `<kind>/<name>`，例如 `pod/node-debugger-0`；不得把 namespace、`events` 或裸名称放进
  `resource`。如果事件读取报参数错误，修正结构化参数并重试一次，禁止改用 Bash/kubectl。
- `output` 单独传 `json`、`yaml`、`wide`、`name`、`jsonpath=...` 或 `custom-columns=...`，
  不要拼进 `name`。

## 安全合规
- 不要在命令行明文传递 Secret
- `mcp_doops_k8s_read` 不得读取 Secret、ServiceAccount 或其它凭据载体
- 生产环境禁止强制删除 Pod
