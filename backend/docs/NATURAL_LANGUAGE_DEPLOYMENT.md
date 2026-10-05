# Natural-Language Deployment

DoOps v3 普通业务 workload 的标准发布入口是明确授权的自然语言任务，而不是环境注册表、
部署模板或 workflow YAML。
唯一发布命令语义是 `doops ask --apply`；CI 和人工调用都使用同一入口。
Gateway 控制台的部署表单调用同一 `doops_agent_prompt operation=apply`，要求发布者
明确勾选变更授权并填写 CI 来源、target、服务范围、release、namespace、远程 Chart、
镜像、原因与验收；公开调用只携带 credential resource names，不携带 payload。
页面的工具失败、连接中断和缺失终态不能成为成功结果，诊断模式不会授权 mutation。
apply 不要求先把代码同步到 target 的隔离工作区。业务镜像版本必须来自任务或其明确
授权的仓库、Chart、values、现有 release 的只读证据，不猜测，也不要求已明确资产中的
事实再粘贴到 prompt。镜像键和版本由 Chart 定义，同 Chart 可有不同服务镜像及不同版本，
不固定 `image.repository/tag/digest` 或日期 tag。CORS Origin、仓库业务名、项目目录不是
全局前置闸门；可读取明确授权的代码/values 解析相关事实，不扩大授权范围。

正式任务必须直接包含：

- target；
- 服务范围；
- 镜像版本，或明确授权读取该事实的仓库、Chart、values、现有 release；
- 变更原因；
- 验收条件。

调用示例：

```bash
doops -session release-<commit> ask --target <target> --apply \
  --credential <resource-name> \
  --msg "在 <target> 发布 <服务范围>，镜像 <repository>@sha256:<digest>；使用 chart https://charts.example/<name>.tgz；原因：<原因>；验收：<条件>。"
```

`--apply` 是调用端的 mutation 授权。缺少上述任何发布事实时，Agent 必须返回真实阻塞
原因，不得猜测 target、镜像、Helm 参数或凭据引用；可先读取明确授权资产取得事实。
运行时 Helm 资产仅使用明确授权的远程 Chart/values 或本 session workspace 资产。
`--credential` 只传 resource name，完整生命周期见
[`CREDENTIAL_RESOURCES.md`](CREDENTIAL_RESOURCES.md)。

CNB CI 可以在镜像版本已确定后调用同一命令。发布者必须在调用前独立核对精确 CI
source commit、registry digest 和构建成功证据；CLI、Agent 和 Helm 工具不自动验证
CI provenance。仓库提供的
`api_trigger_deploy_agent` 只负责准备临时 Gateway 登录并提交这条自然语言任务；它不
同步当前仓库、不创建 target 隔离 workspace，也不是独立部署协议，不读取环境注册表。

Agent 必须把 doagent 会话创建为 `restricted`。普通 `ask` 只允许内置只读工具和
`mcp_doops_k8s_read`；该读取工具明确拒绝 Secret、ServiceAccount 和其它凭据载体。
显式 `--apply` 才切换到 doagent 原生 `build` 模式，并且只允许 `mcp_doops_helm`
执行发布 mutation。包括只更新镜像在内的所有发布，都从任务明确授权的
Helm chart 完整收敛 release。工具缺失、启动失败或没有本轮完成的可信
mutation 证据时必须失败关闭，不能退回 Bash、原始 kubectl、原始 Helm 或文本成功声明。

内置 `doops` MCP 必须设置 `trusted_result_attestation=true`。doagent 只根据实际
stdio 请求和响应在本地生成结果证明，完成验证后才向 ACP 暴露 `rawOutput`。Agent 必须
逐项验证 attestation schema、tool、scope、input/result digest、运行时 session 绑定、
`confirm=true` 和 mutation operation；仅有 completed 状态或工具名不构成成功证据。
一次性 mutation authority 同时绑定精确 allowedTools 与自然语言任务的 context digest，并由
doagent 原子消费；可信 attestation 必须返回同一 authority
digest。同一 target 上的 apply、shell、Kubernetes mutation 和凭据物化共享 mutation 锁，
并排斥结构化 workspace 写入。不同 workspace 的 push/write/clean 可彼此并行；
结构化诊断查询不受发布锁阻挡。`ask --apply`
本身不要求先执行 `push`，也不要求发布输入存在于 session workspace。

普通前台 `exec` 可以执行明确给出的 Helm `upgrade/install/rollback/uninstall`。
命令受 Gateway 用户 `exec` 权限、session 隔离、目标操作串行化和完整退出审计约束；
不得把 Helm 误判为危险 shell 命令。该路径是低层确定性操作，不替代标准
`ask --apply` 发布编排，也不能使用 managed credential reference。`exec --bg` 的原始 RPC
和审计保留到进程终止，取消请求不等于终态；它不能承载 doops-agent Helm mutation，
此类后台调用必须明确拒绝。
只有 `exec --bg` 派发前要求完整 `executionLifecycle` 能力；普通前台 `exec`、
结构化 `ssh`、`ask` 和 `ask --apply` 不要求该声明。缺少后台能力不能阻断前台发布，
也不能静默改走另一条执行路径。

取消 Shell/SSH/bg、apply 或 Kubernetes 写操作只请求停止；本地进程/模型轮次终止不等于
远端变更已停止，Gateway 如实保留 `outcome_unknown`，仍报告失败，不自动重放 mutation。
历史未知审计不参与新任务准入，不改为 success，也不删除；活动任务仍由实际资源锁、
排队和取消机制串行，完成审计持久化后才释放 lease。人工声明不能替代真实终态或强制释放活动 lease。
已验签的 Helm 工具明确失败按失败结束，不归为未知结果；同轮另一工具成功不能掩盖失败。
当前锁按 target 路由
隔离，没有跨 target 的 release/数据库统一锁或执行器 fencing，不支持安全强制抢占。

## Helm 资产与迁移

发布前必须比较当前 workload 与候选 Helm 资产中的 command、args、env、probes、
volumeMounts 和 volumes。发布任务统一调用 `mcp_doops_helm upgrade-install`，显式
提供 release、namespace、chart 和 `confirm=true`。`asset_source` 默认是 `remote`，
当前支持任务明确授权的 HTTPS `.tgz` Chart 下载和可选 HTTP(S) `value_files` URL；
`oci://` 尚不支持，不能自动替换引用或新增 fallback。
普通升级统一执行 `--reset-values`，输入顺序为 Chart 默认值、按顺序零或多个明确授权的
`value_files`、非敏感 Chart-specific `set_strings`；覆盖键及类型由 Chart schema 决定。
真实 mutation 前使用相同输入执行 server-side dry-run 并校验渲染 manifest。
受管 CNB Chart 通过官方 Helm HTTPS 下载接口认证读取，保留精确 scope 与物化时验证的
包 digest 钉住；允许明确授权的 `value_files` 和 `set_strings`，不再强制 bundled-only。
这些覆盖不扩大凭据授权；认证或摘要失败不重试匿名、旧登录态或其它资产。

需要使用已绑定 session workspace 中的 Helm 资产时，任务必须显式传
`asset_source=workspace`。此模式接受任意 session 相对 Chart 目录，必须包含 regular
`Chart.yaml`；`value_files` 可为零或多个任意 session 相对 regular 文件，不要求在 Chart
目录内，也不限定文件名。绝对路径、`..` 和 symlink escape 在 mutation 前拒绝。
与 remote 一样允许非敏感 Chart-specific `set_strings`。Helm 在精确 `/root/ws/<session>`
cwd 中执行，使用明确授权的默认值与覆盖、`--reset-values` 和 server dry-run，不隐式
读取旧 release values；
`session_id` 不属于模型参数，由 doagent 按会话工作目录启动 MCP 子进程并由 DoOps
运行时绑定。Gateway target 只负责路由，不能作为 session 或工作区路径。
随后执行 rollout 和功能验收。shell 中的 kubectl 或 Helm
mutation 不构成发布成功证据。

### Values 迁移

**原自动保留现有非镜像 values 的行为已取消。** 需要保留的 values 必须
显式作为输入资产授权，不隐式 inherit，也不偷偷读取、改写或合并历史 release values。
明确授权读取现有 release 以确定镜像或运行事实，不等于授权继承其全部 values。
更新现有 release 时，`value_files` 可以为空，使用 Chart 默认值和可选 `set_strings`；
发布事实齐备后调用 `mcp_doops_helm upgrade-install`，不要求用户重复粘贴已明确资产中的
事实，也不得询问是否启用内置 dry-run。缺少 `confirm=true` 的调用不是预演接口，
也不能作为发布证据。

`expected_image` 是可选显式参数，接受完整 `repository:tag` 或
`repository@sha256:<64 hex digest>`，不要求日期 tag，工具不从 values 推导。指定时同时
校验 dry-run 和最终 manifest；未指定时仍以 Chart 渲染及最终 Deployment
generation/spec/status、镜像和副本状态证明收敛，不把不同服务版本强制统一。

Helm mutation 成功后，工具还必须读取 release manifest 和最终 Deployment 对象，确认
Deployment 当前 generation 已被 controller 观察、spec 中的容器镜像与渲染结果一致，
updated/ready/available replicas 全部达到 desired 且没有 unavailable replicas；只满足
Helm revision 或 Pod Ready 不构成发布成功。

CNB CI 负责测试、构建和发布制品。Gateway 负责鉴权、审计、资源锁和凭据预物化。
实际业务 Helm/Kubernetes mutation 由 target 指向的 Agent/doagent 在目标运行环境执行；
普通业务发布与 Agent 镜像构建是两条独立路径。
`doops-agent` 自身升级走 `doops upgrade`：仅适用于 Agent 自身 namespace 中的标准
Helm-managed Deployment 和已有匹配 Helm release；未给 `--release` 时必须只有一个匹配
release，给出时按名称选择并校验。Agent Pod 必须有可由 Job 共享的 `/root/.kube` 或
`/root/.kube/config` 挂载，Job 的 Kubernetes 权限来自该 kubeconfig，不需要操作机
kubeconfig。Agent 进程不修改承载自己的 Deployment。
DoOps 不创建临时 Agent、临时 target 或临时 release-runner。还没有该控制器的旧 Agent
和新集群第一次接入仍用 `scripts/release-managed-agent.sh`，这条路径需要操作机 kubeconfig；
其 `--values` 新集群模式是 bootstrap-only，只做 Helm wait 和最终 Deployment 检查，不做
Gateway 接管或自动回滚，完成后必须继续安装验收；已有 workload 但没有 Helm release 时，
该脚本会停止，不执行 adoption。`doops upgrade`
只修改 Agent 镜像或执行 rollback，不修改 `proxy.allowedHosts`、`hostAliases` 或其它
Agent Helm 配置；这些变更仍由外部脚本显式完成。
