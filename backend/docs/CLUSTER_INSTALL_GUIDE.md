# Doops Agent 集群接入指南

本文档介绍如何将 `doops-agent` 部署到一个新的服务器或集群环境。Agent 被设计为**无状态执行通道**，只需要有网络连通性和合适的权限，即可接管不同底层架构的资源。

---

## Kubernetes / K3s

唯一安装和升级入口是 Helm chart `deploy/helm/doops-agent`，命令只有一条：

```bash
# 新集群：本机 kubeconfig + 完整 values（Secret 必须已经存在）
bash scripts/release-managed-agent.sh \
  --release doops-agent \
  --namespace doops-system \
  --values /path/to/complete-values.yaml \
  --version <vX.Y.Z>

# 已在线的 Helm release
bash scripts/release-managed-agent.sh \
  --target <configured-target> \
  --session agent-release-<tag> \
  --release doops-agent \
  --namespace doops-system \
  --version <vX.Y.Z>
```

不要用 `kubectl apply`、`kubectl set image`、`agent.yaml` 或 Docker `run` 作为发布路径。
Chart 禁止 `hostNetwork`，并以 `maxSurge=1` / `maxUnavailable=0` 完成隧道交接。

`doagentSettings` 指向的 Secret 必须在 `helm upgrade --install` 之前已经存在于目标
namespace。公开仓库里的 `agent/agent-config.yaml` 只是字段模板，生产 Secret 由私有
配置生成，不作为 Deployment 清单。

## 安装完成后的验收标准

CLI 不提供直连 Agent 安装入口。不要以“容器起来了”或“`ps` 里有进程”
作为安装成功标准；必须从 Gateway 和真实远程调用两侧验收：

- `doops targets --target <gateway-target>` 返回指定 `cluster/instance`，不是其它同
  Gateway 节点。
- `doops -session install-verify exec --target <gateway-target> --cmd 'do-agent --version'`
  返回当前发布版本。
- 容器运行时可见（`nerdctl` / `docker` / `podman`）。
- `kubectl` 或 kubeconfig 来源明确。
- `buildctl` 与 `/run/buildkit/buildkitd.sock` 可见。

Gateway heartbeat、Agent 版本或容器运行时任一失败，安装都应直接视为失败；如果
`kubectl` 或 `buildkit` 为 `MISSING`，说明目标只能做部分能力，不能被当成完整发布节点。

---

## 连通性验证

新节点部署并启动容器后，需要确保该 agent 已主动注册到 gateway。

1. **确认 agent 注册 gateway**:
   ```bash
   doops targets --target <gateway-target>
   ```

2. **添加 gateway 配置**:
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

3. **登录 Gateway**:
   ```bash
   doops login --target new-cluster
   ```

4. **终端测试验证**:
   ```bash
   doops -session dev01 exec --target new-cluster --cmd "uname -a"
   ```

如果能返回新服务器的内核信息，新节点接入成功。宿主 Docker、`agent.yaml` 和 bind-mount
二进制都不是发布路径；迁到 `deploy/helm/doops-agent` 之前不得换镜像。
