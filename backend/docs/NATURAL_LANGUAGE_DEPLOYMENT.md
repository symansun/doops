# Natural-Language Deployment

DoOps v3 普通业务 workload 的标准发布入口是明确的自然语言任务，而不是环境注册表、
部署模板或 workflow YAML。
唯一发布命令语义是 `doops ask --apply`；CI 和人工调用都使用同一入口。
Gateway 控制台的部署表单调用同一 `doops_agent_prompt operation=apply`，要求发布者
明确勾选变更授权并填写 CI 来源、target、服务范围、release、namespace、远程 Chart、
镜像、原因与验收；公开调用只携带 credential resource names，不携带 payload。
页面的工具失败、连接中断和缺失终态不能成为成功结果，诊断模式不会授权 mutation。
控制台入口在「部署 → 发布工作负载」：表单经 agent-session 通道发送扩展命令
`doops.operations/apply`（schema 1，payload 仅 `input` 与 `confirmed: true`），浏览器和服务端
用同一 `deploymentRequest()` 校验；未勾选授权不构造命令。执行轨迹与 Agent 返回的 rollout、
最终 Deployment 状态在同一会话时间线显示；发布会话和未提交的表单草稿按 Gateway、用户和 target
保存在浏览器标签页内，切换视图后回到同一会话。令牌登录没有用户名，只保存在当前页面的内存里，
刷新后不再保留；退出登录时清除。
草稿含凭据内容时不保存。
apply 不要求先把代码同步到 target 的隔离工作区。业务镜像版本必须来自任务或任务指定的仓库、Chart、values、现有 release 的只读证据，不猜测，也不要求已明确资产中的
事实再粘贴到 prompt。镜像键和版本由 Chart 定义，同 Chart 可有不同服务镜像及不同版本，
不固定 `image.repository/tag/digest` 或日期 tag。CORS Origin、仓库业务名、项目目录不是
全局前置闸门；可读取任务指定的代码/values 解析相关事实。

正式任务必须直接包含：

- target；
- 服务范围；
- 镜像版本，或任务指定的、能读取该事实的仓库、Chart、values、现有 release；
- 变更原因；
- 验收条件。

调用示例：

```bash
doops -session release-<commit> ask --target <target> --apply \
  --credential <resource-name> \
  --msg "在 <target> 发布 <服务范围>，镜像 <repository>@sha256:<digest>；使用 chart https://charts.example/<name>.tgz；原因：<原因>；验收：<条件>。"
```

`--apply` 是调用端的 mutation 授权。缺少上述任何发布事实时，Agent 必须返回真实阻塞
原因，不得猜测 target、镜像、Helm 参数或凭据引用；可先读取任务指定的资产取得事实。
运行时 Helm 资产仅使用任务指定的远程 Chart/values 或本 session workspace 资产。
首次配置用 `doops -session <s> credential configure` 复用受管凭据系统完成写入、目标绑定、
验证和激活；已有资源用 ID 复用，不把配置步骤或秘密塞入发布 prompt。
`--credential` 只传 resource name，完整生命周期见
[`CREDENTIAL_RESOURCES.md`](CREDENTIAL_RESOURCES.md)。

凭据运行时只要求该 target 的 grant；已有目标绑定不需逐操作员重授权。
数据库绑定直接读取保存的 metadata，凭据 namespace 由配置决定，不依赖模型规划。

CNB CI 可以在镜像版本已确定后调用同一命令。发布者必须在调用前独立核对精确 CI
source commit、registry digest 和构建成功证据；CLI、Agent 和 Helm 工具不自动验证
CI provenance。仓库提供的
`api_trigger_deploy_agent` 只负责准备临时 Gateway 登录并提交这条自然语言任务；它不
同步当前仓库、不创建 target 隔离 workspace，也不是独立部署协议，不读取环境注册表。

Agent 必须把 doagent 会话创建为 `auto`。普通 `ask` 暴露内置读取工具、
`Bash`、`mcp_doops_k8s_read` 和 `mcp_doops_web_read`；K8s 读取工具明确拒绝 Secret、ServiceAccount 和其它凭据载体。
`Bash` 由 doagent 的统一 PermissionEngine 决策，运行在目标 Agent 执行环境中，
用于结构化读取工具未覆盖的诊断；普通 `ask` 不暴露业务 workload mutation MCP。
DoOps 不提供交互式 `permission/reply`，Auto 会话的合法工具不应触发 `permission.updated`。
显式 `--apply` 才切换到 doagent 原生 `build` 模式。Kubernetes workload mutation 只允许
`mcp_doops_helm`；目标本地 Nginx 站点变更使用 `mcp_doops_web`，详细契约见
[DOWEB_INTEGRATION.md](DOWEB_INTEGRATION.md)。对于 Kubernetes 业务 workload，
包括只更新镜像在内的所有发布，都从任务指定的 Helm chart 完整收敛 release。
工具缺失、启动失败或没有本轮完成的可信
mutation 证据时必须失败关闭，不能退回 Bash、原始 kubectl、原始 Helm 或文本成功声明。

内置 `doops` MCP 必须设置 `trusted_result_attestation=true`。doagent 只根据实际
stdio 请求和响应在本地生成结果证明，完成验证后才向 ACP 暴露 `rawOutput`。Agent 必须
逐项验证 attestation schema、tool、scope、input/result digest、运行时 session 绑定、
`confirm=true` 和 mutation operation；仅有 completed 状态或工具名不构成成功证据。
一次性 mutation authority 同时绑定精确 allowedTools 与自然语言任务的 context digest，并由
doagent 原子消费；可信 attestation 必须返回同一 authority
digest。同一 target 上的 apply、shell、Kubernetes mutation 和凭据物化在同一 session
内共享 mutation 锁，
并排斥同 session 的结构化 workspace 操作。不同 session/workspace 可以并行；session
不是资源沙箱，不得用不同 session 同时修改同一 Helm release 或数据库。
结构化诊断查询不受发布锁阻挡。`ask --apply`
本身不要求先执行 `push`，也不要求发布输入存在于 session workspace。

普通前台 `exec` 可以执行明确给出的 Helm `upgrade/install/rollback/uninstall`。
命令受 Gateway target grant、session 隔离、同 session 的目标操作串行化和完整退出审计约束；
该路径是低层确定性操作，不替代标准
`ask --apply` 发布编排，也不能使用 managed credential reference。`exec --bg` 同样按原样
执行；其原始 RPC 和审计保留到进程终止，取消请求不等于终态。
Agent 只拒绝删除根目录（`rm -rf /`、`rm -rf /*`），报错 `blocked_command(<rule>): <做法>`。
只有 `exec --bg` 派发前要求完整 `executionLifecycle` 能力；普通前台 `exec`、
结构化 `ssh`、`ask` 和 `ask --apply` 不要求该声明。缺少后台能力不能阻断前台发布，
也不能静默改走另一条执行路径。

取消 Shell/SSH/bg、apply 或 Kubernetes 写操作只请求停止；本地进程/模型轮次终止不等于
远端变更已停止，Gateway 如实保留 `outcome_unknown`，仍报告失败，不自动重放 mutation。
历史未知审计不参与新任务准入，不改为 success，也不删除；活动任务仍由实际资源锁、
排队和取消机制串行，执行结束及清理完成后释放 lease；审计落盘失败返回错误并独立重试原始证据，
不再保留执行锁。人工声明不能替代真实终态或强制释放活动 lease。
已验签的 Helm 工具明确失败按失败结束，不归为未知结果；同轮另一工具成功不能掩盖失败。
当前锁按 target 路由
隔离，没有跨 target 的 release/数据库统一锁或执行器 fencing，不支持安全强制抢占。

## Helm 资产与迁移

发布前必须比较当前 workload 与候选 Helm 资产中的 command、args、env、probes、
volumeMounts 和 volumes。发布任务统一调用 `mcp_doops_helm upgrade-install`，显式
提供 release、namespace、chart 和 `confirm=true`。`asset_source` 默认是 `remote`，
当前支持任务指定的 HTTPS `.tgz` Chart 下载和可选 HTTP(S) `value_files` URL；
`oci://` 尚不支持，不能自动替换引用或新增 fallback。
普通升级统一执行 `--reset-values`，输入顺序为 Chart 默认值、按顺序零或多个任务指定的 `value_files`、非敏感 Chart-specific `set_strings`；覆盖键及类型由 Chart schema 决定。
真实 mutation 前使用相同输入执行 server-side dry-run 并校验渲染 manifest。
受管 CNB Chart 通过官方 Helm HTTPS 下载接口认证读取。凭据权限继承目标操作权限，
不逐项匹配 namespace、workload、release、Chart 版本和仓库路径；当前包校验后一次
staging，dry-run 与实际 Helm 使用相同文件。允许任务指定的 `value_files` 和 `set_strings`。
这些覆盖不扩大凭据授权；认证或摘要失败不重试匿名、旧登录态或其它资产。

需要使用已绑定 session workspace 中的 Helm 资产时，任务必须显式传
`asset_source=workspace`。此模式接受任意 session 相对 Chart 目录，必须包含 regular
`Chart.yaml`；`value_files` 可为零或多个任意 session 相对 regular 文件，不要求在 Chart
目录内，也不限定文件名。绝对路径、`..` 和 symlink escape 在 mutation 前拒绝。
与 remote 一样允许非敏感 Chart-specific `set_strings`。Helm 在精确 `/root/ws/<session>`
cwd 中执行，使用任务指定的默认值与覆盖、`--reset-values` 和 server dry-run，不隐式
读取旧 release values；
`session_id` 不属于模型参数，由 doagent 按会话工作目录启动 MCP 子进程并由 DoOps
运行时绑定。Gateway target 只负责路由，不能作为 session 或工作区路径。
随后执行 rollout 和功能验收。shell 中的 kubectl 或 Helm
mutation 不构成发布成功证据。

### Values 迁移

**原自动保留现有非镜像 values 的行为已取消。** 需要保留的 values 必须
显式作为输入资产传入，不隐式 inherit，也不偷偷读取、改写或合并历史 release values。
任务指定读取现有 release 以确定镜像或运行事实，不等于继承其全部 values。
更新现有 release 时，`value_files` 可以为空，使用 Chart 默认值和可选 `set_strings`；
发布事实齐备后调用 `mcp_doops_helm upgrade-install`，不要求用户重复粘贴已明确资产中的
事实，也不得询问是否启用内置 dry-run。缺少 `confirm=true` 的调用不是预演接口，
也不能作为发布证据。

`expected_image` 是可选显式参数，接受完整 `repository:tag` 或
`repository@sha256:<64 hex digest>`，不要求日期 tag，工具不从 values 推导。指定时同时
校验 dry-run（包括 Helm JSON 的 hook manifest）和最终 release 内容；未指定时仍以 Chart
渲染及最终工作负载状态证明收敛，不把不同服务版本强制统一。校验的工作负载是
Deployment、StatefulSet、DaemonSet、CronJob，以及带 `pre-install` 或 `pre-upgrade` 的
hook Job；镜像包括 initContainers。Deployment/StatefulSet/DaemonSet 必须满足
generation 已观察、spec 镜像与渲染一致且副本全部就绪；hook Job 必须 `succeeded=1`、
`active=0`、`failed=0`；带 `helm.sh/hook-delete-policy: hook-succeeded` 的 hook Job 成功后
会被 Helm 删除，读到 NotFound 即视为已成功（Helm 只在 hook 成功后才继续）；CronJob 只校验镜像。
普通 Job 不被当作发布工作负载。
工作负载渲染了 `metadata.namespace` 时，按该 namespace 读取线上对象；未写则用 release namespace。
Helm `--wait` 在 ready >= replicas - maxUnavailable 时就返回，所以验收会继续轮询尚未就绪的
工作负载，直到全部就绪或超过本次 `timeout`（未传时 5 分钟）。镜像与渲染不一致、对象不存在、
hook Job 已失败这几种不会因等待而恢复，立即判失败。工具失败时 `ask --apply` 返回
`doagent_apply_tool_failed: <工具>: <工具错误码与原因>`。

**Ingress 也是可校验的发布内容。** 只含 Ingress 的 release（例如给已有服务加一个域名
入口）没有自己的 Pod 可供滚动，但它的路由可以被核对，所以照样发布。校验方式是按渲染
结果核对线上的 Ingress：

- 线上对象必须存在，且 `ingressClassName`、每条 route（host、path、pathType）和后端
  Service 与端口、TLS Secret 与 hosts，都与渲染结果完全一致；渲染与线上用同一套
  规范化签名比较，route 顺序和 `networking.k8s.io/v1` 与旧版 backend 写法不影响结论。
- Ingress 引用的每个后端 Service 必须存在，否则这个入口服务的是错误，发布失败。
- 没有 route 的 Ingress 视为渲染结果有问题（`helm_manifest_invalid`）。
- Ingress 不参与 `expected_image` 校验：它没有容器。只有 Ingress 的 release 不应传
  `expected_image`，传了会因渲染结果里没有该镜像而失败。

渲染结果里既没有上述工作负载也没有 Ingress 时，仍返回 `helm_no_workload`：这种 release
没有任何可核对的内容。

**接管已存在的对象。** 渲染出的对象已经存在、但没有 Helm 所有权元数据（例如曾被手工
apply）时，Helm 默认拒绝接管，以 `helm_preflight_failed: ... invalid ownership metadata`
失败，release 不改动。任务明确要求把这些对象纳入本 release 时，传 `take_ownership: true`，
dry-run 与真实 Helm 同时加 `--take-ownership`，接管随本次 mutation 一起进入审计，之后照常按
渲染结果核对。它从不默认开启；不要在受管路径之外手工补 `managed-by` / `meta.helm.sh/*`
元数据来绕过这一检查。

`upgrade-install` 分三步：preflight（server dry-run 渲染并检查）→ mutate（真实 Helm）→
verify（按本次 revision 回读并对比集群）。每步失败返回一个稳定错误码，后接 `: ` 和具体原因：

| 错误码 | 含义 |
|---|---|
| `helm_preflight_failed` | Helm dry-run 本身失败，原因是脱敏后的 Helm 报错首行 |
| `helm_manifest_invalid` | 渲染结果无法解码或格式不对，原因指出第几个 YAML 文档或哪个 `Kind/name` |
| `helm_no_workload` | 渲染结果里没有可校验的工作负载或 Ingress |
| `helm_image_mismatch` | `expected_image` 不在任何渲染容器里，原因列出实际渲染的镜像 |
| `helm_mutation_failed` | 真实 Helm 命令失败，原因是脱敏后的 Helm 报错首行 |
| `helm_mutation_invalid_result` | Helm 输出里没有本次 mutation 的 revision |
| `helm_verification_failed` | 集群状态与渲染结果不一致或未就绪 |
| `helm_interrupted` | 本次调用用完时间预算；原因写明本次 revision 和 release 当前所处状态 |

Chart 的 stdout（NOTES、渲染 manifest）始终不返回。

一次 `mcp_doops_helm` 调用必须在 doagent 的 MCP 工具超时（900 秒）之前结束并留下终态。
preflight、Helm `--wait` 和验收共用一个调用预算（810 秒），`timeout` 最大 `10m`，超出时
构建阶段直接拒绝，不开始升级。预算用完时 Agent 先给 helm 发 SIGTERM，让 Helm 自己把
revision 记为 failed 并释放锁，30 秒后仍未退出才强制结束。

升级失败后（含 Helm 自己的 `--wait` 超时，helm 非零退出）Agent 读取 release history，
按本次 mutation 的 description 找到自己的 revision，并处理它留下的状态：

| 本次 revision 状态 | Agent 的处理 |
|---|---|
| 未创建 | release 未改动，只返回 helm 的失败原因 |
| `failed` | 不回滚，报告失败 revision 和仍在生效的 deployed revision |
| `deployed`（验收未完成） | 不回滚，提示先回读工作负载再判断 |
| `pending-*` | 回滚到最近的 deployed revision 以解锁 release，报告两个 revision |
| `pending-*` 且没有 deployed revision | 不处理，提示卸载或重新安装 |

预算用完时以 `helm_interrupted: <原因>` 返回；Helm 自己失败但状态已收敛时以
`helm_mutation_failed: <revision 状态>; helm reported: <原因>` 返回。两种情况都不会把
release 留在无人处理的 `pending-*`。

不同 session 的 Helm 调用不额外获取 DoOps release 独占锁。每次 upgrade-install 写入唯一
description，从本次 Helm JSON 结果取得 revision，并按该 revision 读取 manifest；
最终验收还要求当前 release revision/description 与本次 mutation 一致。被并发发布覆盖时
明确返回 `helm_verification_failed`，不借用另一任务的结果，也不自动重放。
upgrade-install 默认不加 `--atomic`：升级失败时失败的 revision 保留在集群里，由操作者查看后自行决定是否回滚、回滚到哪个 revision。只有调用方显式传 `atomic: true` 时才加 `--atomic`，让 Helm 在失败时自动回滚。结构化 rollback 使用 Helm 支持的 wait/timeout 参数，不传 `--atomic`。

Helm mutation 成功后，工具还必须读取本次 revision 的 release manifest（必要时读取 hook
manifest）和最终工作负载对象，确认 Deployment 当前 generation 已被 controller 观察、
spec 中的容器镜像与渲染结果一致，updated/ready/available replicas 全部达到 desired
且没有 unavailable replicas；hook Job 必须已成功完成。只满足 Helm revision 或 Pod
Ready 不构成发布成功。

CNB CI 负责测试、构建和发布制品。Gateway 负责鉴权、审计、资源锁和凭据预物化。
实际业务 Helm/Kubernetes mutation 由 target 指向的 Agent/doagent 在目标运行环境执行；
普通业务发布与 Agent 镜像构建是两条独立路径。
标准 Helm `doops-agent` 自身升级走 `doops upgrade`：仅适用于 Agent 自身 namespace 中的标准
Helm-managed Deployment 和已有匹配 Helm release；未给 `--release` 时必须只有一个匹配
release，给出时按名称选择并校验。升级 Job 使用 Agent 挂载的 `/root/.kube` kubeconfig，
没有挂载时使用 Agent 自己的 ServiceAccount，不需要操作机 kubeconfig。Agent 进程不修改承载
自己的 Deployment，替换总在独立的 Job 或外部脚本里执行。K8S 新集群首次接入用
`scripts/release-managed-agent.sh --values`；没有 Helm release 的旧 Agent 先按
[BUILD_DEPLOY_SOP.md](BUILD_DEPLOY_SOP.md) 收编。`doops upgrade`
只修改 Agent 镜像或执行 rollback，不修改 `proxy.allowedHosts`、`hostAliases` 或其它
Agent Helm 配置；这些变更由外部脚本完成。

已有独立 Docker Agent 保持运行方式，由正式外部 `scripts/release-host-agent.sh` 发布，
不走 `ask --apply` 或旧 Agent 自行替换，不依赖 `doops_agent_upgrade` / `managedHelm`，
不要求先迁 K8S。制品准备、只读预检、版本检测、回滚及协议/RPC 流验收见
[主 SOP](BUILD_DEPLOY_SOP.md#独立-dockerhost-发布)；不扩展凭据 API 或 Agent 的 SSH 认证能力。
