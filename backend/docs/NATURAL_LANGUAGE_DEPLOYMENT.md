# Natural-Language Deployment

DoOps v3 普通业务 workload 的标准发布入口是明确授权的自然语言任务，而不是环境注册表、
部署模板或 workflow YAML。
唯一发布命令语义是 `doops ask --apply`；CI 和人工调用都使用同一入口。
CLI 会把同 target/session 最近一次成功 `push` 的精确 workspace commit 自动绑定到
apply；缺少本地 commit 记录、远端 commit 不一致或工作区存在额外改动时，Agent 在启动
doagent 前失败关闭。调用方不需要也不能用自然语言覆盖该绑定。

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
  --msg "在 <target> 发布 <服务范围>，镜像 <repository>@sha256:<digest>；允许使用 deploy/helm/<name>/；原因：<原因>；验收：<条件>。"
```

`--apply` 是调用端的 mutation 授权。缺少上述任何发布事实时，Agent 必须返回真实阻塞
原因，不得推断 target、镜像、Helm 参数或凭据引用。运行时 Helm 资产仅从任务明确指定的
`deploy/helm/<name>/` 使用。`--credential` 只传 resource name，完整生命周期见
[`CREDENTIAL_RESOURCES.md`](CREDENTIAL_RESOURCES.md)。

CNB CI 可以在镜像 digest 已确定后调用同一命令。仓库提供的
`api_trigger_deploy_agent` 只负责准备临时 Gateway 登录、同步当前仓库并提交这条自然
语言任务；它不是独立部署协议，也不读取环境注册表。

Agent 必须把 doagent 会话创建为 `restricted`。普通 `ask` 只允许内置只读工具和
`mcp_doops_k8s_read`；该读取工具明确拒绝 Secret、ServiceAccount 和其它凭据载体。
显式 `--apply` 才切换到 doagent 原生 `build` 模式，并且只允许 `mcp_doops_helm`
执行发布 mutation。包括只更新镜像在内的所有发布，都从任务明确给出的
`deploy/helm/<name>/` 完整收敛 release。工具缺失、启动失败或没有本轮完成的可信
mutation 证据时必须失败关闭，不能退回 Bash、原始 kubectl、原始 Helm 或文本成功声明。

内置 `doops` MCP 必须设置 `trusted_result_attestation=true`。doagent 只根据实际
stdio 请求和响应在本地生成结果证明，完成验证后才向 ACP 暴露 `rawOutput`。Agent 必须
逐项验证 attestation schema、tool、scope、input/result digest、运行时 session 绑定、
`confirm=true` 和 mutation operation；仅有 completed 状态或工具名不构成成功证据。
一次性 mutation authority 同时绑定精确 allowedTools、自然语言任务与 workspace commit
的 context digest，并由 doagent 原子消费；可信 attestation 必须返回同一 authority
digest。同一 target 上的 apply、push、write、clean、shell、Kubernetes mutation 和凭据
物化共享 mutation 锁，避免发布过程中工作区或 workload 被并发改写。

普通前台 `exec` 可以执行明确给出的 Helm `upgrade/install/rollback/uninstall`。
命令受 Gateway 用户 `exec` 权限、session 隔离、目标操作串行化和完整退出审计约束；
不得把 Helm 误判为危险 shell 命令。该路径是低层确定性操作，不替代标准
`ask --apply` 发布编排，也不能使用 managed credential reference。`exec --bg` 的审计只
覆盖任务提交，不能承载 doops-agent Helm mutation；此类后台调用必须明确拒绝。

发布前必须比较当前 workload 与候选 Helm 资产中的 command、args、env、probes、
volumeMounts 和 volumes。发布任务统一调用 `mcp_doops_helm upgrade-install`，显式
提供 release、namespace、chart、values、非敏感 set strings 和 `confirm=true`；
`session_id` 不属于模型参数，由 doagent 按会话工作目录启动 MCP 子进程并由 DoOps
运行时绑定。Gateway target 只负责路由，不能作为 session 或工作区路径。镜像必须使用
不可变 digest。随后执行 rollout 和功能验收。shell 中的 kubectl 或 Helm mutation
不构成发布成功证据。

CNB CI 负责测试、构建和发布不可变制品。Gateway 负责鉴权、审计、资源锁和凭据预物化。
实际业务 Helm/Kubernetes mutation 由 target 指向的 Agent/doagent 在目标运行环境执行。
`doops-agent` 自身升级是例外，必须由另一个隔离 Agent target 或真实存在的外部
CI/GitOps controller 执行。

仓库的 `api_trigger_bootstrap_release_runner` 只负责从目标集群现有 Helm release
复制非镜像 values，并通过合法的前台 `exec` Helm 操作创建隔离 release-runner。
它不发布受管 Agent，也不替代 `ask --apply`。runner 注册到 Gateway 后，
`api_trigger_deploy_agent` 必须把 runner 作为 target，由 runner 对受管 Agent release
执行自然语言 Helm 收敛。发布和验收完成后应卸载临时 runner，避免把控制器变成长期
业务 target。
