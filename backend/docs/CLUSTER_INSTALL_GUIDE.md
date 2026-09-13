# Doops Agent 集群接入指南

本文档介绍如何将 `doops-agent` 部署到一个新的服务器或集群环境。Agent 被设计为**无状态执行通道**，只需要有网络连通性和合适的权限，即可接管不同底层架构的资源。

---

## Kubernetes / K3s

用户在目标集群上的安装和更新是同一条命令。当前基线 **v1.3.0**。权威脚本和清单在
`https://cnb.cool/l8ai/dowork` 的 `doops-k8s/`，必须传入这个 Agent 的 `cluster` 和
`instance`：

```bash
curl -fsSL https://cnb.cool/l8ai/dowork/-/git/raw/main/doops-k8s/install.sh | sh -s -- <cluster> <instance>
```

例如经贸：

```bash
curl -fsSL https://cnb.cool/l8ai/dowork/-/git/raw/main/doops-k8s/install.sh | sh -s -- doops-jm jm-228
```

重复执行会复用已有仓库（`./doops-k8s`、`./dowork` 或 `$HOME/dowork`），只 fetch
`main` 再 `kubectl apply`，不会再 clone 一份。不要填已经在线的身份，后连上的会顶掉
先连上的。镜像版本钉在 `doops-k8s/doops.yaml`，必须与 `base/doops-agent.yaml`、`values.yaml`、
`install.sh` 注释和 README 标题同一 tag/digest。要换版本先改这几份到同一基线，再重跑
同一条 curl。不要用 `kubectl set image` 或 Docker `run` 换镜像。

本仓库的 `scripts/release-managed-agent.sh` 只给已经改用 Helm chart
`deploy/helm/doops-agent` 的维护者节点。已有集群必须由持有 kubeconfig 的操作机作为
外部控制器执行，不能通过当前 Agent 修改自身 Deployment。那条路径不是用户一键安装。

旧副本卡在 Terminating 超过一分钟时，强制删除该 Pod，不要对同一身份再跑一遍
install。`hostPID` 会让 `tini -s` 收养宿主机孤儿进程，kubelet 删不掉容器。

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
