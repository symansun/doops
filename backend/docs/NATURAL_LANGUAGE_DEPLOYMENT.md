# Natural-Language Deployment

DoOps v3 普通业务 workload 的标准发布入口是明确授权的自然语言任务，而不是环境注册表、
部署模板或 workflow YAML。
唯一发布命令语义是 `doops ask --apply`；CI 和人工调用都使用同一入口。
apply 不要求先把代码同步到 target 的隔离工作区；发布所需的 Chart、values URL、镜像
digest 和验收条件必须直接出现在任务中或来自任务明确授权读取的现有 release。

正式任务必须直接包含：

- target；
- 服务范围；
- 镜像版本；
- 变更原因；
- 验收条件。

调用示例：

```bash
doops -session release-<commit> ask --target <target> --apply \
  --credential <resource-name> \
  --msg "在 <target> 发布 <服务范围>，镜像 <repository>@sha256:<digest>；使用 chart https://charts.example/<name>.tgz；原因：<原因>；验收：<条件>。"
```

`--apply` 是调用端的 mutation 授权。缺少上述任何发布事实时，Agent 必须返回真实阻塞
原因，不得推断 target、镜像、Helm 参数或凭据引用。运行时 Helm 资产仅从任务明确指定的
远程 Helm chart 和 values URL 使用。`--credential` 只传 resource name，完整生命周期见
[`CREDENTIAL_RESOURCES.md`](CREDENTIAL_RESOURCES.md)。

CNB CI 可以在镜像 digest 已确定后调用同一命令。仓库提供的
`api_trigger_deploy_agent` 只负责准备临时 Gateway 登录、同步当前仓库并提交这条自然
语言任务；它不是独立部署协议，也不读取环境注册表。

Agent 必须把 doagent 会话创建为 `restricted`。普通 `ask` 只允许内置只读工具和
`mcp_doops_k8s_read`；该读取工具明确拒绝 Secret、ServiceAccount 和其它凭据载体。
显式 `--apply` 才切换到 doagent 原生 `build` 模式，并且只允许 `mcp_doops_helm`
执行发布 mutation。包括只更新镜像在内的所有发布，都从任务明确给出的
远程 Helm chart 完整收敛 release。工具缺失、启动失败或没有本轮完成的可信
mutation 证据时必须失败关闭，不能退回 Bash、原始 kubectl、原始 Helm 或文本成功声明。

内置 `doops` MCP 必须设置 `trusted_result_attestation=true`。doagent 只根据实际
stdio 请求和响应在本地生成结果证明，完成验证后才向 ACP 暴露 `rawOutput`。Agent 必须
逐项验证 attestation schema、tool、scope、input/result digest、运行时 session 绑定、
`confirm=true` 和 mutation operation；仅有 completed 状态或工具名不构成成功证据。
一次性 mutation authority 同时绑定精确 allowedTools 与自然语言任务的 context digest，并由
doagent 原子消费；可信 attestation 必须返回同一 authority
digest。同一 target 上的 apply、push、write、clean、shell、Kubernetes mutation 和凭据
物化共享 mutation 锁，避免发布过程中工作区或 workload 被并发改写。

普通前台 `exec` 可以执行明确给出的 Helm `upgrade/install/rollback/uninstall`。
命令受 Gateway 用户 `exec` 权限、session 隔离、目标操作串行化和完整退出审计约束；
不得把 Helm 误判为危险 shell 命令。该路径是低层确定性操作，不替代标准
`ask --apply` 发布编排，也不能使用 managed credential reference。`exec --bg` 的审计只
覆盖任务提交，不能承载 doops-agent Helm mutation；此类后台调用必须明确拒绝。

发布前必须比较当前 workload 与候选 Helm 资产中的 command、args、env、probes、
volumeMounts 和 volumes。发布任务统一调用 `mcp_doops_helm upgrade-install`，显式
提供 release、namespace、远程 `https://`/`oci://` chart、可选远程 HTTP(S)
`value_files`、非敏感 `set_strings` 和 `confirm=true`。`value_files` 不从本地
workspace 读取；`set_strings` 只允许 `image.repository` 与一个受控版本选择器：
`image.digest=sha256:<digest>` 或严格的 `image.tag=release-YYYYMMDD`。两种模式不得混用，
缺少明确版本或使用其它覆盖时工具在 mutation 前拒绝。
`upgrade-install` 自动保留已有 release 的非镜像 values；
`session_id` 不属于模型参数，由 doagent 按会话工作目录启动 MCP 子进程并由 DoOps
运行时绑定。Gateway target 只负责路由，不能作为 session 或工作区路径。镜像必须满足
上述受控版本输入。随后执行 rollout 和功能验收。shell 中的 kubectl 或 Helm
mutation 不构成发布成功证据。

更新现有 release 时，如果任务已经明确 release、namespace、chart 和受控镜像版本，
`value_files` 可以为空。Agent 必须直接调用一次 `mcp_doops_helm upgrade-install`，
并只用 `set_strings` 覆盖 `image.repository` 与一个明确版本选择器；不得要求调用方再提供
当前 values、候选 values 文件或是否启用 dry-run。Helm 工具会在内部读取并保留现有
release values，并用完全相同的参数自动执行 dry-run 后再执行真实 mutation。缺少
`confirm=true` 的调用不是预演接口，也不能作为发布证据。

Helm mutation 成功后，工具还必须读取 release manifest 和最终 Deployment 对象，确认
Deployment 当前 generation 已被 controller 观察、spec 中的容器镜像与渲染结果一致，
updated/ready/available replicas 全部达到 desired 且没有 unavailable replicas；只满足
Helm revision 或 Pod Ready 不构成发布成功。

CNB CI 负责测试、构建和发布不可变制品。Gateway 负责鉴权、审计、资源锁和凭据预物化。
实际业务 Helm/Kubernetes mutation 由 target 指向的 Agent/doagent 在目标运行环境执行。
`doops-agent` 自身升级不能由自身连接执行，必须由真实存在的外部 CI/GitOps controller
执行。DoOps 不创建临时 Agent、临时 target 或临时 release-runner。操作机上的
`scripts/release-managed-agent.sh` 是唯一 Agent 发布命令；已有集群必须提供操作机
kubeconfig，由脚本在 Agent 进程外执行 Helm dry-run、mutation、接管验证和失败回滚。
