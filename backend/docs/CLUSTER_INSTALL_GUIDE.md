# Doops Agent 集群接入指南

本文档介绍如何将 `doops-agent` 部署到一个新的服务器或集群环境。Agent 被设计为**无状态执行通道**，只需要有网络连通性和合适的权限，即可连接并执行不同底层架构的资源。

---

## Kubernetes / K3s

新集群首次安装使用本仓库 `scripts/release-managed-agent.sh` 和
`deploy/helm/doops-agent`。以下示例从仓库 `backend/` 执行，完整 values 必须明确
`gateway.cluster/instance`、workload 与运行配置，镜像 digest 必须来自已核验的 CI：

```bash
bash scripts/release-managed-agent.sh \
  --release <helm-release> \
  --namespace <namespace> \
  --values <reviewed-complete-values.yaml> \
  --kubeconfig <external-controller-kubeconfig> \
  --image <repository>@sha256:<verified-ci-digest>
```

外部控制器可以是已授权的集群管理主机或 CI，不要求一定运行在开发机。首次安装或
Agent 自恢复可以通过 SSH 到该主机执行受控流程；日常业务仍通过明确 target 的
`ask --apply`，不以本机 kubeconfig 或 `push` 为前提。
新身份必须确认没有同 `cluster/instance` 的在线实例。外部清单安装器不作为本仓库的
第二条标准升级路径；不要通过 `kubectl set image`、Docker `run` 或重跑旧清单换镜像。

本仓库的 `scripts/release-managed-agent.sh` 用于新集群安装、旧 Helm Agent 首次启用
自升级控制器，以及显式 Agent Helm 配置变更。新集群的 `--values` 模式
是 bootstrap-only，只做 Helm wait 和最终 Deployment 检查，不做 Gateway 接管或自动回滚；
完成后必须继续执行本节安装验收。已有 workload 但没有 Helm release 时，脚本会在 Helm
查询阶段停止，不执行 adoption；必须先完成独立、明确的 Helm adoption。控制器就绪后，
后续镜像替换用 `doops upgrade`，不再传 kubeconfig；该路径要求 Agent 自身 namespace、
标准 Helm-managed Deployment、已有匹配 Helm release，以及可由 Job 共享的
`/root/.kube` 或 `/root/.kube/config` 挂载，Job 的 Kubernetes 权限来自该 kubeconfig。
`doops upgrade` 只替换 Agent 镜像或回滚，不修改 `proxy.allowedHosts` 或 `hostAliases`；
这些配置仍只能通过该外部 Helm 脚本显式传入。那条脚本不是用户一键安装。

旧副本卡在 Terminating 时，先核对替代 Pod 已接管 Gateway，再检查事件、finalizer 和
节点运行时；不能仅凭一分钟超时就强制删除，也不要对同一身份再跑一次安装器。
非标准 `hostPID` 配置必须在独立迁移中处理，不能通过临时容器绕过。

## 安装完成后的验收标准

CLI 不提供直连 Agent 安装入口。不要以“容器起来了”或“`ps` 里有进程”
作为安装成功标准；必须从 Gateway 和真实远程调用两侧验收：

- `doops -session install-verify targets --target <gateway-target>` 返回指定 `cluster/instance`，不是其它同
  Gateway 节点。
- `doops -session install-verify exec --target <gateway-target> --cmd 'do-agent --version'`
  返回当前发布版本。
- 容器运行时可见（`nerdctl` / `docker` / `podman`）。
- 目标 Agent 内的 `kubectl` 和 Kubernetes 身份来源明确，不是查找开发机 kubeconfig。
- `buildctl` 与 `/run/buildkit/buildkitd.sock` 可见。

Gateway heartbeat、Agent 版本或容器运行时任一失败，安装都应直接视为失败；如果
`kubectl` 或 `buildkit` 为 `MISSING`，说明目标只能做部分能力，不能被当成完整发布节点。

---

## 连通性验证

新节点部署并启动容器后，需要确保该 agent 已主动注册到 gateway。

1. **添加 gateway 配置**:
   在 `~/.agent/skills/doops/config.json` 里追加或用 `doops add` 写入：
   ```json
   {
     "name": "new-cluster",
     "gateway": "https://gateway.example.com",
     "cluster": "doops-new",
     "instance": "master-1",
     "use": "新测试环境 via gateway"
   }
   ```

2. **登录 Gateway**:
   ```bash
   doops -session install-verify login --target new-cluster
   ```

3. **确认精确身份已注册**:
   ```bash
   doops -session install-verify targets --target new-cluster
   ```

4. **终端测试验证**:
   ```bash
   doops -session dev01 exec --target new-cluster --cmd "uname -a"
   ```

返回内核信息只证明远程命令通道可用；完成上节全部验收后才能宣布节点接入成功。
宿主 Docker、`agent.yaml` 和 bind-mount
二进制都不是发布路径；迁到 `deploy/helm/doops-agent` 之前不得换镜像。
