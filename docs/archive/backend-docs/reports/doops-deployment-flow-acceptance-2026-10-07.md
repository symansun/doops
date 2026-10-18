# DoOps 部署流程真实验收（2026-10-07）

## 验收范围

本次验证的是以下真实路径：

1. 本地代码和 Helm Chart 同步到目标 Agent 的 session workspace。
2. 使用同一 session 的 `ask --apply` 调用 `mcp_doops_helm upgrade-install`。
3. 独立读取 live Kubernetes 对象，确认发布结果，而不是只相信 Agent 文本。
4. 使用 Helm uninstall 清理合成 workload，并清理 session workspace。

目标为 `gw-edu-coder`（`doops-edu/edu-coder`），使用现有 `test` namespace 和唯一的合成
release；没有修改其它业务 release。

## 结果

### 1. 上传工作区

- session：`flow-upload-20261007`
- 上传 3 个合成文件，Git HTTP workspace push 成功。
- 远端逐个 `sha256sum` 与上传内容一致。
- `doops clean` 成功删除远端 workspace 和对应裸仓库。

### 2. 上传 Chart 并发布

- session：`flow-deploy-20261007`
- 上传 5 个文件，其中包含相对路径 `deploy-chart/Chart.yaml`、`values.yaml` 和两个模板。
- 目标 Agent 上 `helm lint` 和 `helm template` 均通过。
- `ask --apply` 明确使用：
  - release：`doops-flow-smoke-20261007`
  - namespace：`test`
  - `asset_source=workspace`
  - chart：`deploy-chart`
  - `confirm=true`
  - image：`repo.l8ai.cn:8443/library/nginx:1.25.3`
- Agent 返回并完成了相同输入的 server-side dry-run、真实 Helm 收敛和最终 Deployment 校验。

### 3. 独立 live 校验

独立 `doops exec` 读取结果如下：

- Deployment `observedGeneration=1`，期望/更新/就绪/可用副本均为 `1`。
- 实际容器镜像为 `repo.l8ai.cn:8443/library/nginx:1.25.3`。
- Service 存在，类型为 `ClusterIP`，端口 `80`，EndpointSlice 有 Ready 地址。

### 4. 清理

- 通过 `mcp_doops_helm uninstall` 删除唯一合成 release。
- Deployment、Service、EndpointSlice 均独立查询为 NotFound。
- `flow-deploy-20261007` workspace 和裸仓库已清理。
- 目标清理后仍保持在线，未留下本次测试的运行资源。

## 结论与边界

当前可以真实完成的是：

`上传目录/Chart -> session workspace -> ask --apply -> Helm 发布 -> live 验收 -> 清理或回滚`

当前不是单一 DoOps 操作、不能宣称自动完成的是：

- 任意源代码不会因 `push` 自动构建镜像；镜像必须先由 CI 构建并取得精确 digest，再交给发布任务。
- 原始 `.tar.gz` 或其它部署包不能直接作为 workspace Chart；需要解包成包含 `Chart.yaml` 的
  session 相对 Chart 目录，或使用明确的 HTTPS `.tgz` 远程 Chart。
- 控制台结构化部署表单面向远程 HTTPS Chart；工作区 Chart 的上传与发布应使用文件工作区加
  Agent 对话中的 `asset_source=workspace` 任务。
- `push` 只负责文件同步，不等价于发布成功；最终成功必须有 Helm、Deployment 和功能验收证据。

## 额外验证

- Go Helm/MCP 定向测试通过。
- Python 发布契约测试通过（6 项）。
- Web 控制台测试通过（33 项），生产构建通过。
- 本地浏览器 smoke 验证了 HTTPS Chart 占位符和可复制 `doops -session ... push --target ...`
  命令。演示目标的 Agent session 请求返回预期 `401 unauthenticated`，因此没有把演示数据误报为
  真实 Agent 对话成功。
