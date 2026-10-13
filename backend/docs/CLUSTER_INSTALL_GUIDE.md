# Doops Agent 集群接入指南

本文档介绍如何将 `doops-agent` 部署到一个新的服务器或集群环境。Agent 被设计为**无状态执行通道**，只需要有网络连通性和合适的权限，即可连接并执行不同底层架构的资源。

---

## 先选部署对象

本文是 Agent 安装与升级的入口。不要把 CLI 安装、Agent 注册、操作员登录和
Agent 镜像升级当作同一件事。下列脚本命令均从仓库 `backend/` 执行。

| 当前状态 | 使用入口 | 前提 |
| :--- | :--- | :--- |
| 只安装客户端和 Skill | `bash scripts/install.sh --version <tag> --project <项目绝对路径>` | 不部署 Agent；不修改线上 Helm |
| 空 Ubuntu/Debian 服务器首次 Docker 接入 | `bash scripts/bootstrap-host-agent.sh ...` | root、模型 settings 文件、唯一 cluster/instance、指定发布版本 |
| 已有标准 Helm Agent，命令通道和升级控制器可用 | `doops -session <s> upgrade --target <t> --image <已核验镜像>` | 操作员已登录 Gateway；Agent 挂载共享 kubeconfig |
| 旧 Helm Agent 或命令通道损坏，需要外部恢复 | `bash scripts/release-managed-agent.sh ...` | 操作机 kubeconfig、Helm、kubectl、python3，以及可查询目标的 Gateway 登录 |
| 没有 Agent 的新集群 | 同一外部脚本的 `--values` 模式 | 完整 values 与操作机 kubeconfig；安装后另做 Gateway 验收 |
| 已有独立 Docker Agent | `release-host-agent.sh` | 见下文 DockerHost 流程 |

标准 Helm Agent 的日常镜像升级是一条命令：

```bash
doops -session agent-upgrade upgrade \
  --target <configured-target> \
  --image <repository>@sha256:<verified-ci-digest>
```

`--image` 也接受发布 tag，但 tag 不是不可变版本证据。`--dry-run` 只预览参数，
不能证明控制器、挂载或权限可用。没有命令通道的旧 Agent 不能用这条命令自恢复。
当前 CLI 会将升级后的 Agent commit 与自身 commit 比较，因此先安装候选镜像
同版 CLI；升级前旧 Agent 不需要与 CLI 同版。

操作员在一台管理机上登录一次，同一 Gateway 的 targets 共用登录态：

```bash
doops -session setup login --target <configured-target>
doops -session preflight targets --target <configured-target>
doops -session preflight exec --target <configured-target> --cmd hostname
```

登录支持账号密码交互，不必另外找管理员签发 token。已有登录不要重建隔离配置：
`DOOPS_CONFIG` 会改变配置及相邻 `auth.json` 的位置，单独 `doops add` 只添加路由，
不会复制登录态。Agent 免 token 注册不等于操作员免登录访问。

### HDU 升级记录的纠正（2026-10-13）

本次目标是 `gw-hdu-235`，身份 `doops-235/hdu-235`，已有 Helm release
`ai/doops-agent`；操作机 K3s kubeconfig 是 `/etc/rancher/k3s/k3s.yaml`。
附件中的 `v1.3.48` 是该次选定版本，不是本文自动追踪的“最新版”。

- `install.sh` 只更新 CLI/Skill，成功也不代表 Agent 已升级。安装器必须在替换前
  用候选 CLI 检查 canonical 配置和遗留配置；不能装完后才发现配置不可用。
- 当前管理机的 Gateway 登录可查询 HDU，目标显示 idle，但 `RELEASE_COMMIT`
  为 `<unknown>`，只读命令返回 `execution_outcome_unknown`。这不是完整健康状态，
  不能据此声称 `doops upgrade` 已具备执行条件。
- 外部脚本的已有 release 模式确实依赖 Gateway 登录，不能宣传为免登录一键部署；
  本机 kubeconfig 授权与 Gateway 操作员授权是两个独立边界。
- 外部脚本 `--version` 当前调用 Docker 拉取镜像解析 digest。K3s/containerd
  不等于安装了 Docker；此类操作机使用已核验的 `--image`，不要仅为解析版本安装 Docker。
- 外部脚本目前直接比较请求 digest 与 CRI `imageID`，且用本机 CLI commit
  校验新 Agent commit。OCI index、平台 manifest 和 CRI imageID 可能不同；
  这两项限制修复前，不能将附件中的 index digest 命令标为已验证的一键恢复命令。

外部恢复的参数形式如下，**这是入口说明，不是 HDU 已通过验收的命令**：

```bash
bash scripts/release-managed-agent.sh \
  --target gw-hdu-235 --session hdu-agent-recovery \
  --cluster doops-235 --instance hdu-235 \
  --release doops-agent --namespace ai \
  --kubeconfig /etc/rancher/k3s/k3s.yaml \
  --image <repository>@sha256:<verified-ci-digest> \
  --dry-run
```

dry-run 必须通过，且镜像身份验收限制必须解决后再执行正式恢复。不能用临时 target、
复制 token、跳过验收或把失败标为成功来完成升级。

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
第二条标准 Helm 升级路径；不要通过 `kubectl set image`、手工 Docker `run` 或重跑旧清单换镜像。

本仓库的 `scripts/release-managed-agent.sh` 用于新集群安装、旧 Helm Agent 首次启用
自升级控制器，以及显式 Agent Helm 配置变更。新集群的 `--values` 模式
是 bootstrap-only，只做 Helm wait 和最终 Deployment 检查，不做 Gateway 接管或自动回滚；
完成后必须继续执行本节安装验收。已有 workload 但没有 Helm release 时，脚本会在 Helm
查询阶段停止，不执行 adoption；必须先完成独立、明确的 Helm adoption。控制器就绪后，
后续镜像替换用 `doops upgrade`，不再传 kubeconfig；该路径要求 Agent 自身 namespace、
标准 Helm-managed Deployment、已有匹配 Helm release，以及可由 Job 共享的
`/root/.kube` 或 `/root/.kube/config` 挂载，Job 的 Kubernetes 权限来自该 kubeconfig。
`doops upgrade` 只替换 Agent 镜像或回滚，不修改 `proxy.allowedHosts` 或 `hostAliases`；
这些配置仍只能通过该外部 Helm 脚本显式传入。

旧副本卡在 Terminating 时，先核对替代 Pod 已接管 Gateway，再检查事件、finalizer 和
节点运行时；不能仅凭一分钟超时就强制删除，也不要对同一身份再跑一次安装器。
非标准 `hostPID` 配置必须在独立迁移中处理，不能通过临时容器绕过。

## 空服务器 Docker 首装

支持使用 systemd 的 Ubuntu/Debian，amd64/arm64（所选发布镜像必须包含对应架构）。
这是首次安装入口，不用于覆盖已有容器。以下命令从仓库根执行，Unicom 示例：

```bash
sudo bash backend/scripts/bootstrap-host-agent.sh \
  --version v1.3.48 \
  --gateway https://doops.l8ai.cn \
  --cluster doops-unicom --instance unicom-1 \
  --settings /root/doagent-settings.json
```

`v1.3.48` 是明确示例版本，不表示动态最新版。settings 是私有 doagent 模型配置
JSON（model、provider 等），格式见 `agent/agent-config.yaml` 中的 `settings.json`，
不要把 YAML Secret 整份传入，不要使用占位密钥。通过安全渠道放到服务器，不在聊天
或命令参数中传密钥。现有镜像强制要求模型配置；缺失时脚本在安装 Docker 前停止。

脚本使用 Docker 官方 apt 仓库安装 Engine/Compose，已安装 Docker 时验证并复用；
不卸载其它运行时、不迁移已有环境。拉取明确版本后锁定 registry digest，检查镜像
内版本信息，将部署和私有配置写入 `/opt/doops-agent`，使用 Compose 启动。
容器使用标准镜像要求的 privileged 模式；不发布宿主端口，不挂载宿主 Docker socket。
Agent 管理的是容器执行环境；此安装不会自动获得宿主文件系统或 Kubernetes 权限。

工作区和 Agent 状态用 Docker volumes 持久化。健康检查要求 Gateway 隧道已连接；
240 秒内失败则停止容器、返回失败并保留配置供排查，不输出可能含密钥的原始日志。
已有 `/opt/doops-agent` 或同名容器时拒绝覆盖。修正首装配置后可显式重试：

```bash
sudo docker compose -f /opt/doops-agent/compose.yaml up -d --wait --wait-timeout 240
```

选择未占用的 cluster/instance，安装前在管理端核对已有节点。注册不需要 operator
token；安装后的完整命令验收在已登录的管理机执行：

```bash
doops add --name gw-unicom --gateway https://doops.l8ai.cn \
  --cluster doops-unicom --instance unicom-1
doops -session unicom-install targets --target gw-unicom
doops -session unicom-install exec --target gw-unicom --cmd hostname
```

尚未登录的管理机先执行 `doops -session setup login --target gw-unicom`。
本地健康仅证明隧道连接；管理端 RPC 和 AI 请求还须真实验收，不能仅凭启动完成宣称
全部功能可用。已有 Docker Agent 后续发布仍走下面的外部升级入口。

## 已有独立 Docker 节点

已有独立 Docker Agent 保持现网运行方式，通过正式外部
`backend/scripts/release-host-agent.sh` 发布；不要求迁 K8S，不使用 `doops upgrade`
或旧 Agent `exec` 替换自己。脚本不是无 Agent 主机的安装配方，不自动迁 Compose/Helm。
显式 target/session、SSH 地址/用户/端口、原容器、cluster/instance 和 CI 制品参数、
真实只读预检、宿主锁、配置保留与外部回滚见
[主 SOP](BUILD_DEPLOY_SOP.md#独立-dockerhost-发布)。新接入方案须另行确认授权，不能为检查硬件擅自安装。

## 安装完成后的验收标准

CLI 不提供直连 Agent 安装入口。不要以“容器起来了”或“`ps` 里有进程”
作为安装成功标准；必须从 Gateway 和真实远程调用两侧验收：

- `doops -session install-verify targets --target <gateway-target>` 返回指定 `cluster/instance`，不是其它同
  Gateway 节点。
- `doops -session install-verify exec --target <gateway-target> --cmd 'do-agent --version'`
  返回当前发布版本。
- 容器运行时可见（`nerdctl` / `docker` / `podman`）。
- K8S 节点的 `kubectl` 和 Kubernetes 身份来源明确，不是查找开发机 kubeconfig；独立 Docker 不要求 K8S。
- 声明 BuildKit 能力的节点须验证 `buildctl` 与 `/run/buildkit/buildkitd.sock` 可见。
- 独立 Docker 发布还须完成主 SOP 的实际容器/镜像/版本、Gateway `release_commit`、structuredSSH 协议/argv/预期失败终态、foreground/bg RPC 流与 Root RequestID 验收；不宣称 SSH 目的地主机成功认证。

Gateway heartbeat、Agent 版本或容器运行时任一失败，安装都应直接视为失败；如果
节点所需的 `kubectl` 或 `buildkit` 为 `MISSING`，不能被当成具备相应能力的发布节点；
独立 Docker 未部署 K8S 本身不是升级失败。

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
独立 Docker Agent 使用上述正式外部入口；非 Helm K8S 清单或 bind-mount 二进制不能
借此手工换镜像，也不能让当前 Agent 自行替换自己。
