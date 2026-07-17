---
name: doops-cicd
description: 根据明确的自然语言任务构建、诊断或部署用户指定的服务。
---

# DoOps CI/CD

DoOps v3 不使用 `EnvironmentRegistry`、`EnvironmentDefinition`、
`DeploymentTemplate`、`SemanticRelease` 或 YAML workflow。

部署请求必须明确给出 target、服务范围、镜像版本或 digest、变更原因、验收条件和
mutation 授权。不得从环境名、域名、目录、历史发布或默认值推断任何参数。

## 执行

1. 提取并复述任务中的 target、服务范围、镜像、原因、验收和授权。
2. 只读任务检查当前运行状态、镜像和验收证据，不执行变更。
3. 有明确 mutation 授权时，使用当前实际可用的模块执行最小变更。
4. 验证任务明确列出的镜像、工作负载和健康条件。

缺少必要信息或执行失败时直接返回真实错误。不要 fallback，不生成中间 workflow，
不运行第二套编排器，不改写 Agent 返回结果。
