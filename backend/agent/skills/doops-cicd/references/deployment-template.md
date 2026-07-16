# DoOps v3 拆分声明契约

## 根类型

`doops.sh/v3` 只定义两个入口：

- `BuildDefinition`：固定路径 `deploy/deploy.yaml`。
- `EnvironmentDefinition`：固定路径
  `deploy/environments/<metadata.name>/environment.yaml`。

公共和环境服务文件是被引用声明，不能单独发起环境部署：

- `ServiceDefinition`：`deploy/services/<metadata.name>.yaml`。
- `EnvironmentService`：
  `deploy/environments/<environment>/services/<metadata.name>.yaml`。

## EnvironmentDefinition

必须声明：

- `metadata.name`；
- `spec.application`；
- `spec.source.repository/branch/buildManifest`；
- `spec.execution.deploy.target/cluster/instance/session`；
- `spec.helm.release/namespace/chart/values/atomic/wait/rollbackOnFailure`；
- `spec.artifacts.targetRegistry` 和 Secret 引用元数据；
- `spec.services` 中每个服务的明确文件路径；
- `spec.healthChecks`、`spec.releaseProcess`、`spec.stopConditions`。

环境文件就是部署根，不得包含 `configurationSource` 或 `workflow`。

## 引用闭合

对每个 `spec.services.<name>`：

1. 路径必须位于当前环境的 `services/`。
2. 文件必须是同版本 `EnvironmentService`，且 environment 与根环境一致。
3. `serviceDefinition` 必须位于 `deploy/services/`。
4. 公共定义必须是同版本 `ServiceDefinition`。
5. 服务身份、Helm value、workload 和 container 必须在两层定义中一致。
6. 环境服务的 target repository 与 digest 必须与 Helm values 完全一致。

拒绝路径逃逸、重复服务、隐式目录扫描、未知字段替代、跨环境引用和版本混用。

## v2 边界

`doops.sh/v2` 的 `DeploymentTemplate`、`SemanticRelease` 和 `ServiceRelease` 保持原
契约。v3 执行器不得生成或读取 v2 workflow 作为中间产物。
