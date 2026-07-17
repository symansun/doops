---
name: doops-cicd
description: 生成、评审、预演和执行 DoOps 声明式构建与部署。v3 使用 deploy/deploy.yaml、deploy/environments.yaml、公共服务定义和逐环境服务定义。
---

# DoOps CI/CD

本 Skill 由 doagent 原生执行。它解释声明和约束，不生成命令式 workflow、部署
shell 或本地源码快照。

## v3 入口

- 声明版本：`doops.sh/v3`。
- 构建根：`deploy/deploy.yaml`，kind 为 `BuildDefinition`。
- 部署根：`deploy/environments.yaml`，kind 为 `EnvironmentRegistry`。
- 公共服务：`deploy/services/<service>.yaml`，kind 为 `ServiceDefinition`。
- 环境服务：`deploy/environments/<environment>/services/<service>.yaml`，kind 为
  `EnvironmentService`。

`environment.yaml` 和 v3 `EnvironmentDefinition` 不再是可执行入口。
`doops.sh/v2` 继续使用既有
`DeploymentTemplate`，不得把 v3 降级生成 v2 workflow。

## v3 调用

每次部署必须显式提供：

- `environment`：选择 `environments.<environment>`。
- `service`：选择该环境 `services.<service>`。
- `reason`：可选操作原因。

不得推断默认环境、默认服务或扫描目录，也不得从命令行覆盖 target、repository、
digest、副本、Chart、values、namespace、release、registry 或 Secret 引用。

## 执行链

1. CLI 从根声明读取所选环境的 HTTPS repository 和 branch。
2. Agent 直接克隆该 branch；CLI 不上传工作区快照，不解析或校验远端 commit。
3. Credential prepare 和可信执行使用同一组 `environment/service/reason` inputs。
4. 严格校验所选 `EnvironmentService` 与 `ServiceDefinition` 的闭合引用。
5. 验证并按 digest 同步所选服务镜像。
6. 共享 Helm release 使用现有 values，仅覆盖所选服务的 image、enabled 和 replicas。
7. 验证所选工作负载和环境公开健康检查。

任一必需 Secret 引用为 `undeclared` 或 `deploymentBlockedUntilDeclared` 时立即阻断。
不得使用 fallback、旧入口或空计划继续执行。

## DeploymentRun

dry-run/apply 返回与根声明同 apiVersion 的 `DeploymentRun`：

- 顶层只允许 `apiVersion`、`kind`、`metadata`、`spec`、`status`。
- `metadata` 只包含 `workflow` 和 `runId`。
- `spec` 只包含 `mode`。
- `status` 可包含 `phase`、`mutationCount`、`capabilities` 和 `failures`。
- 不包含 `workspaceCommit` 或 `evidence`。

使用 `response_format=json` 时，final response 必须是唯一 JSON object，不得输出
Markdown、YAML、外围文本或 Secret 内容。
