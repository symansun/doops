---
name: doops-cicd
description: 按 DoOps v3 YAML 构建或部署用户指定的单个服务。
---

# DoOps CI/CD

只使用仓库中的 v3 声明：

- 构建：`deploy/deploy.yaml`
- 部署：`deploy/environments.yaml`

部署请求必须给出 `environment` 和 `service`。读取
`environments.<environment>.services.<service>` 指向的服务文件，只构建、迁移、
部署和验证该服务。不得遍历其他服务，不得推断默认环境或默认服务。

## 执行

1. 读取请求指定的 YAML、环境和服务。
2. 确认环境、服务、target、镜像、Helm 和健康检查声明存在。
3. dry-run 只观察并报告计划，不执行变更。
4. apply 使用声明中的 target 和工具执行该服务的变更。
5. 验证该服务的镜像、工作负载和声明的健康检查。

缺少声明或执行失败时直接返回真实错误。不要 fallback，不生成中间 workflow，不运行
第二套编排器，不改写 Agent 返回结果。
