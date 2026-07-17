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

只使用 `CredentialRun` 中的凭据元数据和目标侧已物化资源，不读取或输出 Secret
内容。缺少声明、能力、授权或执行失败时直接返回真实错误。不要 fallback，不生成
中间 workflow，不运行第二套编排器，不改写 Agent 返回结果。
