---
name: doops-cicd
description: 按 DoOps v3 YAML 构建或部署用户指定的单个服务。
---

# DoOps CI/CD

只使用仓库中由 `cicd run -f` 明确指定的声明。支持的公开根契约是：

- `doops.sh/v2` `DeploymentTemplate` 或 `SemanticRelease`
- `doops.sh/v3` `EnvironmentDefinition`

v2 与 v3 是显式契约，不是 fallback。禁止混用版本、扫描目录、推断默认环境或
生成中间 workflow。部署请求必须给出声明要求的环境和服务范围。

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
