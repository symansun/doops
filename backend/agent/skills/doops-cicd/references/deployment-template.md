# DoOps v3 声明契约

## 根类型

- `BuildDefinition`：`deploy/deploy.yaml`。
- `EnvironmentRegistry`：`deploy/environments.yaml`。

`ServiceDefinition` 和 `EnvironmentService` 只能被根声明引用，不能单独作为部署入口。

## EnvironmentRegistry

每个 `environments.<environment>` 必须声明：

- `target.name/cluster/instance/session`；
- `source.repository/branch/build`；
- `helm.release/namespace/chart/values/atomic/wait`；
- `artifacts.targetRegistry` 和三个 Secret 引用；
- `services.<service>` 的明确文件路径；
- `healthChecks`。

执行必须显式选择 `inputs.environment` 和 `inputs.service`。所选服务路径必须位于
`deploy/environments/<environment>/services/`。

## 引用闭合

所选 `EnvironmentService` 必须：

1. 与所选环境一致；
2. 引用 `deploy/services/` 下的同版本 `ServiceDefinition`；
3. 与公共定义保持 service、Helm value、workload 和 container 一致；
4. 声明源/目标镜像和有效 `sha256` digest；
5. 使用根环境声明的 Chart、values 和 target registry。

拒绝路径逃逸、未知环境或服务、跨环境引用、版本混用、隐式目录扫描和未声明凭据。

## v2 边界

v2 `DeploymentTemplate` 继续使用 v2 `EnvironmentRegistry` configurationSource。v3 根
不得生成或读取 v2 workflow 作为中间声明。
