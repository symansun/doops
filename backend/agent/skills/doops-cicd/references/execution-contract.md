# DoOps v3 执行契约

## dry-run

- 不执行 mutation，`mutationCount` 必须为 0。
- 验证所选源镜像 digest、目标镜像状态、Helm release、工作负载和公开健康检查。
- 目标镜像缺失是待同步事实；认证、网络或非 404 registry 错误必须阻断。

## apply

1. 从声明的 HTTPS repository/branch 获取部署声明。
2. 验证所选源镜像 digest。
3. 仅在目标 digest 缺失时同步镜像，并验证源目标 digest 相等。
4. 观察当前共享 Helm release。
5. 使用 `--reuse-values` 仅覆盖所选服务的 image、enabled 和 replicas。
6. 执行一次 atomic/wait Helm 收敛。
7. 验证所选工作负载、Endpoints、日志和公开健康检查。

不得使用全量 values 更新未选服务。缺少运行时能力、凭据、声明或目标连接时返回
`blocked`，不得 fallback。

## DeploymentRun

- `metadata`：仅 `workflow/runId`。
- `spec`：仅 `mode`。
- `status`：`phase/mutationCount/capabilities/failures`。
- 不包含 `workspaceCommit` 或 `evidence`。

phase 只允许 `planned`、`converged`、`blocked`、`failed`、`outcome-unknown`。
final response 必须是唯一 JSON object，不得包含 Secret。
