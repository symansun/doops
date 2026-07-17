# DoOps v3 执行与证据契约

## Agent 原生执行

doagent 根据声明发现运行时能力、委派独立 Agent，并选择受信任 DoOps 工具。Skill
不固定工具名称，不生成 shell 流水线。缺少所需能力时返回 blocked。

## dry-run

- 不执行 mutation。
- 观察源码身份、六个源/目标 digest、目标节点镜像可拉取性、Helm 基线、权限和健康检查。
- 返回 apply 仍需执行的动作，`mutationCount: 0`。

## apply

顺序为：

1. 重新验证工作区源码身份。
2. 验证所有声明源 digest。
3. 只迁移目标仓库缺失的精确 digest。
4. 验证源和目标 manifest digest 相等。
5. 在工作负载不变时预拉取目标 digest。
6. 观察当前 Helm revision 和全部工作负载镜像，形成 rollback baseline。
7. 汇总所有服务，只执行一次 atomic/wait 共享 Helm 收敛
   (exactly one shared-release convergence)。
8. 验证实际镜像、就绪副本、Endpoints、日志和公开健康检查。

任一 Helm 前置条件失败时，部署执行器调用次数必须为 0
(zero deployment executor invocations)。mutation 后失败只能恢复
本次观察到的 baseline，并返回回滚后的 Helm 和工作负载证据。

v2 部分服务发布仍必须读取并保持每个 unselected service 的当前镜像身份。自托管
Agent 发布必须由独立 control target 执行，不能让正在被替换的 Agent 控制自身升级。

## DeploymentRun

结果 apiVersion 必须与根声明一致，kind 为 `DeploymentRun`。必须包含：

- `metadata.workflow/runId/workspaceCommit`；
- `spec.mode`；
- `status.phase/mutationCount/capabilities/evidence/failures`。

顶层只允许 `apiVersion`、`kind`、`metadata`、`spec`、`status`。禁止把
`phase`、`mutationCount`、`capabilities`、`evidence`、`failures`、`runId` 或
`workspaceCommit` 放到顶层。Gateway 从已验证 instruction 重建
`apiVersion/metadata/spec`；模型仍必须输出完整五字段对象，且这些字段必须先与
instruction 精确一致，任何缺失、额外字段或值漂移都直接失败。

phase 只允许 `planned`、`converged`、`blocked`、`failed`、
`outcome-unknown`。每条 evidence 必须引用当前 turn 中完成的受信任工具调用；Gateway
只接受匹配 `turnId` 的调用，并负责把 `toolRef` 解析为不可改写的 `toolCall`。
受信任 DoOps plan 工具必须用模型可见名称 `plan.<Tool>` 和精确输入 `{}` 引用；
声明字段、观察结果或 Gateway 注入的受信任上下文都不是模型调用参数。

当请求使用 `response_format=json` 时，doagent 必须把一个且仅一个 JSON object
作为 final response；该 final response 就是机器结果，不得写结果文件或输出
Markdown、YAML、外围文本。`toolCall.result` 只包含摘要验证通过且移除 transport
`_meta` 后的工具结果。

不得把普通 Bash、Read、浏览器、子 Agent 文本或 Secret 内容当成部署证据。
禁止 fallback 或静默降级。Image generation 与 Agent delegation 属于 doagent，
不属于 DoOps 部署证据工具。
