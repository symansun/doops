---
name: doops-cicd
description: 按 DoOps v3 EnvironmentRegistry 部署用户明确指定的单个服务。
---

# DoOps CI/CD

唯一发布根是仓库中的 `deploy/environments.yaml`：
`doops.sh/v3 EnvironmentRegistry`。部署请求必须明确给出 `environment` 和
`service`，每次只执行该服务。禁止旧 v2 workflow、扫描目录、遍历其他服务、推断
默认环境或生成中间 workflow。

Gateway 在 Ask 前必须完成 `doops.sh/credential-plan/v2` prepare，并将返回的
`CredentialRun` 绑定到用户、target、session、声明路径、源仓库、branch、不可变
commit 和执行模式。Agent 内 doagent 必须声明
`doops.reconciliation-context/v2` 的完整能力；不一致时必须在模型执行前阻断。

## 执行

1. 读取请求指定的 YAML 及其显式引用。
2. 确认源、环境、服务、target、凭据引用、镜像、Helm 和健康检查声明存在。
3. dry-run 只观察并报告计划，不执行变更。
4. apply 使用声明中的 target 和工具执行该服务的变更。
5. 验证该服务的镜像、工作负载和声明的健康检查。

Helm apply 必须使用仓库提供的 detached Job 执行器，并强制
`helm upgrade --install --reset-values`。发布只接受当前声明 values，禁止继承或
复用历史 release values，也禁止在 schema 失败后临场改命令重试。

只使用 `CredentialRun` 中的凭据元数据和目标侧已物化资源，不读取或输出 Secret
内容。缺少声明、能力、授权或执行失败时直接返回真实错误。不要 fallback，不生成
中间 workflow，不运行第二套编排器，不改写 Agent 返回结果。

## 结构化终态

完成所有观察或变更后，必须且只能调用一次 `CICDReport`。禁止用 `Pending` 或
`Reconciling` 做中间调用，也禁止失败后重试同一报告工具。

`CICDReport` 参数必须严格只有以下四个字段：

- `planDigest`：`sha256:` 加 64 位小写十六进制。它是发布计划 canonical JSON 的
  SHA-256，不是 Git commit 前加 `sha256:`。canonical JSON 至少包含 source commit、
  environment、service、target、executionMode、image digest、Helm chart 和 values。
- `status`：成功的 dry-run 或 apply 使用 `Converged`；无法继续使用 `Blocked`；
  已执行但失败使用 `Failed`。
- `evidence`：实际观察到的声明、运行时和健康检查证据数组。
- `violations`：`Converged` 时为空数组；`Blocked` 或 `Failed` 时必须包含真实错误。

调用前先用确定性工具计算并检查 `planDigest` 正好匹配
`^sha256:[a-f0-9]{64}$`。`CICDReport` 成功后不得再调用它；普通 Markdown 总结不是
CI/CD 机器终态。
