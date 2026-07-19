# Doops 构建与部署强制规范 (SOP)

普通业务 workload 的权威发布流程见 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`；受管凭据的创建、授权、
验证、轮换和部署引用见 `docs/CREDENTIAL_RESOURCES.md`。

> **核心纪律：绝对禁止在本地开发机或业务宿主机直接执行容器镜像构建。**
>
> 本地开发机的架构（如 Apple Silicon ARM64）、网络环境（代理/污染）以及未清理的缓存，极易导致打出的 OCI 镜像体积过大、架构错配或携带不安全的凭据。
> 所有线上与测试环境的 `doops-agent` 镜像，**必须且只能由 `main` 分支的 CNB CI 按标准流程构建、校验并推送。**

---

## 一、 Gateway 发布边界

`doops-gateway` 是公网控制面服务，必须作为独立发布包处理。它不是普通
`doops-agent` target，也不登记为 doops target。

固定发布入口：

```bash
bash scripts/extract-gateway-from-image.sh \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<verified-ci-digest>

bash scripts/deploy-gateway.sh \
  --host 106.54.197.139 \
  --user ubuntu \
  --binary bin/doops-gateway \
  --no-build \
  --verify-target gw-edu-coder \
  --verify-session gateway-release-<release>
```

当前 gateway 维护主机：

- SSH 地址：`106.54.197.139`
- SSH 用户：`ubuntu`
- Gateway URL：`https://doops.l8ai.cn`
- SSH 凭据由运维密钥库或维护人保管，禁止写入仓库、日志、提交信息和文档正文。

这条脚本只走 SSH/SCP，在宿主机真实 root 下完成：

1. 从 `main` 的 CNB CI 候选镜像 digest 提取同 revision 的
   `/app/doops-gateway`；禁止在发布控制端重新编译。
2. SSH 校验远端真实 DB `/var/lib/doops-gateway/gateway.db` 存在且非空。
3. 上传二进制到远端 `/tmp`。
4. 备份 `/usr/local/bin/doops-gateway`。
5. 替换二进制并重启 gateway。
6. 校验 `/proc/<pid>/exe` hash 与 CI 镜像提取产物一致。
7. 校验 `/v1/targets` 未认证访问返回 `401`，并通过 `--verify-target` 复用 DoOps 已配置认证，
   确认发布前的全部 target 均已重新连接；不得为发布导出或新建明文 token 文件。

禁止事项：

- 禁止用 `doops exec` 启动或重启 `doops-gateway`。
- 禁止用 `doops push/write/read` 给 gateway 控制面发版。
- 禁止让 `deploy-gateway.sh` 在发布控制端执行默认本地构建；线上发布必须显式
  使用 `--no-build` 和由 `extract-gateway-from-image.sh` 提取的不可变 CI 产物。
- 禁止用 base64/tar/分片临时传输替代 gateway 发布脚本。
- 不保留 gateway host agent 作为默认 target；需要主机巡检时走独立 SSH/监控流程。

如果 `doops push` 卡住，应修复 Git HTTP 反向隧道或目标 agent 版本，不允许用分片绕过。

---

## 二、 Agent 标准构建工作流 (Server-Side Build)

在进行任何修改后，先由 `main` 的 CNB CI 构建并推送以完整 40 位 commit SHA 为标签的候选镜像。该镜像同时携带同一 revision 的 `/app/doops-agent`、`/app/doops-gateway` 和锁定版本的 `/usr/local/bin/do-agent`；不得在发布控制端重新编译 Gateway。候选镜像必须解析为 registry digest，正式发布只接受不可变 digest。

DoOps v3 的唯一发布入口是明确授权的自然语言任务。不得使用 `EnvironmentRegistry`、`EnvironmentDefinition`、`DeploymentTemplate`、`deploy/environments.yaml`、`cicd run` 或 workflow YAML 作为发布根，也不得从目录名、环境名、业务名或历史发布记录推断目标和参数。CNB 负责构建、校验和推送镜像，也可以在镜像 digest 已确定后调用同一个 `doops ask --apply` 自然语言入口；CI 不得拥有另一套发布协议。

### 1. 触发受控发布
发布者必须先将待发布提交推送到 `main`，确认 CNB CI 已为该精确 commit 生成候选镜像，并取得 registry digest。推荐调用 CNB 的 `api_trigger_deploy_agent`，由 CI 临时配置单个 Gateway route、同步当前仓库，并执行下方同一自然语言命令；人工控制端调用只用于故障诊断。自然语言任务必须直接写明 target、服务范围、不可变镜像 digest、变更原因、允许使用的 Helm 资产和验收条件；缺少任一发布事实时必须阻塞，不得猜测或补默认值。
`api_trigger_deploy_agent` 只定义在 `main` 分支事件下，Gateway token 仅通过 stdin
交给 main 上构建的 CLI。push 生成的 workspace commit 由 CLI 自动保存并随随后的
`ask --apply` 提交，Agent 必须在执行前验证远端工作区仍是该精确 commit。

```bash
doops -session release-<commit> ask \
  --target <configured-doops-target> \
  --apply \
  --msg '在 <target> 发布 <service-scope>，镜像 <repository>@sha256:<digest>；仅使用 deploy/helm/<name>/ 运行资产；原因：<reason>；验收：<rollout、实际镜像、健康检查和功能路径>。'
```

`--apply` 是本次 mutation 的显式授权，不是兼容开关。任务返回的文本声明不能作为成功证据；验收必须来自本轮实际命令、Kubernetes 状态、健康检查和功能测试。需要凭据时只传 `--credential <resource-name>` 元数据引用，Gateway 必须按当前用户、target、session 和精确资源 scope 授权。公开 prompt、doagent 上下文、CLI 输出和日志不得接收凭据 payload；Gateway 仅通过内部凭据物化调用向目标 Agent 短暂传递 payload。

doagent 会话默认使用 `restricted` 权限，普通任务只额外放行 `mcp_doops_k8s_read`；
该读取工具不得读取 Secret、ServiceAccount 或其它凭据载体。只有 `--apply` 对应的原生
`build` 模式允许 `mcp_doops_helm` mutation。Agent 镜像必须把本地结构化 doops MCP
注册为 required，并且 apply 成功只接受规范化后的 `doops_helm` 可信完成证据。MCP
启动失败、工具缺失或仅出现其它工具调用时，发布必须失败关闭。
该内置 MCP 必须设置 `trusted_result_attestation=true`，由 doagent 根据实际请求和
stdio 响应在本地生成结果证明。

**DoOps Agent 执行顺序：**
1. 核对任务中的 target、服务范围、镜像 digest、Helm 资产、原因和验收条件完整。
2. 验证 digest 对应 `main` 上已成功构建的精确 source commit。
3. 在候选镜像内校验 `doops-agent`、`do-agent`、BuildKit、Python/PyYAML、Helm 与嵌入的 Helm Chart。
4. 按任务明确给出的运行参数执行 Helm 收敛；禁止继承已删除的历史值，也禁止在 schema 失败后临场改变目标或参数重试。
5. 等待 Helm release 和 Kubernetes rollout 成功，并逐项执行任务中的健康与功能验收。

### 双镜像构建原则

发布时维护两类镜像：

```bash
docker.cnb.cool/l8ai/ai/doops.sh/base-light:<release>
docker.cnb.cool/l8ai/ai/doops.sh:<release>
```

`Dockerfile.base.light` 只放基础运行时：doagent、BuildKit、kubectl、Python+PyYAML、Helm、系统包。`Dockerfile` 和 `agent/Dockerfile` 只放轻更新：doops-agent、skills、docs、entrypoint。禁止把基础 rootfs 压平成每个 release 都变化的单个多 GiB layer。

CNB 非同名制品路径使用 `docker.cnb.cool/<owner>/<repo>/base-light:<release>` 这种仓库下级路径；不要使用 `<repo>-base-light:<release>`。

发布后必须验证：

1. `/app/doops-agent -help` 可运行。
2. `/usr/local/bin/do-agent --version` 与发布声明的 doAgent 版本完全一致。
3. `buildctl --version` 可运行。
4. `python3 -c 'import yaml'` 可运行。
5. `helm version --short` 可运行。
6. `doops exec` 能读取 `hostname/date/kubectl get nodes/df/free`。
7. `doops ask` 能通过 doagent ACP HTTP 调用工具并返回结论。

镜像构建必须完成 base label 与前 5 项镜像内自检；第 6、7 项必须在 Agent 滚动更新后对真实 target 执行。

---

## 三、 Agent 部署与热更新机制

`doops-agent` 由 Helm 管理的 `Deployment` 托管，Chart 位于 `deploy/helm/doops-agent`。该目录只保存运行时 Helm 模板、默认值和挂载定义，不保存环境注册表或部署 workflow。具体镜像、目标节点、Secret 引用、Gateway 地址和发布参数必须由本次自然语言任务明确给出或来自任务明确授权读取的当前 Helm release。发布前必须对比当前 workload 与候选 Chart 的 command、args、env、probes、volumeMounts 和 volumes。包括只更新镜像在内的所有发布都必须调用结构化 `doops_helm upgrade-install`，从任务明确指定的 Chart、可选 `value_files` 和非敏感 `set_strings` 完整收敛；更新现有 release 且任务已给出 release、namespace、chart 和不可变镜像时，`value_files` 可以为空，工具自动保留当前 values。`value_files` 只放 `deploy/` 下的 YAML 文件路径，`set_strings` 只覆盖标量叶子，禁止把 map/list 父键编码成字符串。`upgrade-install` 先用相同参数执行 Helm dry-run，再执行真实 mutation。镜像版本使用互斥的 digest 覆盖模式，或完整 values 中 `image.repository` 加 `image.tag=release-YYYYMMDD` 的模式；后者禁止 digest 和任何 `set_strings`。不存在 Kubernetes 镜像 mutation 发布旁路。自然语言 apply 禁止通过 shell 执行 kubectl 或 Helm mutation；运维人员明确给出的前台 `exec` Helm 操作合法，但 `exec --bg` Helm mutation 必须拒绝。正常运行中的 Agent 不得从节点 SSH 更新。

临时 release-runner 安装前必须确认受管 Agent rollout Ready、目标节点
`DiskPressure=False`，且根文件系统同时满足至少 10% 和 5 GiB 可用空间。任何条件不满足
都必须在拉取 runner 镜像前失败，避免单节点环境因临时控制器触发 Agent 驱逐。
由于 runner 继承宿主机 kubeconfig 和 hostPath，受管 Deployment 必须显式设置
`nodeSelector.kubernetes.io/hostname`；bootstrap 不从 affinity 或当前 Pod 调度结果猜测节点。

受管 `doops-agent` 不得升级自身。升级窗口先调用
`api_trigger_bootstrap_release_runner`，通过当前 Agent 的前台 Helm `exec` 创建同集群
隔离 runner；该步骤只创建外部 controller，不修改受管 Agent。随后
`api_trigger_deploy_agent` 必须连接 runner，并由 runner 使用 `ask --apply` 发布受管
Agent。验收完成后卸载临时 runner。不得把受管 Agent 本身作为发布 target，否则连接会
在 rollout 中断开，无法形成可信完成证据。

单副本 Agent 的 home 目录包含 doagent SQLite 状态，不能让新旧 Pod 同时挂载。Chart
必须使用 `maxSurge=0`、`maxUnavailable=1`；否则滚动窗口会产生 `database is locked`，
并可能让新实例在旧实例退出前进入不确定状态。

`hostNetwork` 更新必须等待旧实例释放 DoAgent 和 DoOps 监听端口。容器入口使用同一个 120 秒窗口等待两个端口；Chart 的 `startupProbe` 必须覆盖至少 200 秒，并同时验证当前容器内的 DoOps 进程和本地 health，避免误连宿主网络中的旧实例。DoAgent 未启动或健康检查失败时容器必须直接失败，不能只打印警告后继续。发布验收必须确认稳定窗口后的容器重启数仍为 0。

### 环境依赖注射规范 (Secret)
严禁在 `agent.yaml` 中明文出现 `LLM_API_KEY=sk-xxxx` 之类的配置。此类密钥应由 Secret 提取并通过 `envFrom` 下放。

### 容器运行时挂载规范

容器化 `doops-agent` 需要在 agent 内部继续调用宿主机容器运行时，例如
`nerdctl pull/run`、BuildKit、或一次性 helper 容器。只挂
`/run/containerd/containerd.sock` 不够：`pull` 可能成功，但 `run` 会因为
overlay snapshot、FIFO、`/etc/hosts` 或 nerdctl name-store 位于宿主机路径而失败。

标准容器/DaemonSet 必须同时挂载：

```text
/run/containerd      -> /run/containerd
/var/lib/containerd  -> /var/lib/containerd
/var/lib/nerdctl     -> /var/lib/nerdctl
```

升级顺序必须是：

1. 先在目标节点成功 `pull` 新镜像。
2. 再启动新 agent 或触发 DaemonSet 滚动更新。
3. 新 agent 注册 gateway 并通过 `exec` 验证后，再清理旧容器。

遇到 containerd overlay/FIFO/name-store 错误时，不要强切旧 agent。优先保留旧
agent 在线，用宿主机 namespace 或 Kubernetes 一次性特权 Pod 从宿主机视角修复：

```bash
kubectl run <node>-doops-deployer \
  --restart=Never \
  --overrides '<hostPID/hostNetwork/privileged + hostPath / 的 PodSpec>' \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest-from-successful-CI> -- \
  /bin/sh -lc 'chroot /host /usr/bin/nsenter -t 1 -m -n -- nerdctl run ...'
```

修复后必须验证：

```bash
doops -session smoke exec --target <target> --cmd 'sha256sum /app/doops-agent; nerdctl ps | grep doops-agent'
doops -session smoke exec --target <target> --cmd 'nerdctl run --rm --net none --entrypoint /bin/sh docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest-from-successful-CI> -lc "echo direct-ok"'
doops -session smoke push --target <target> --src <small-dir>
```

### 宿主 Docker 直装升级规则

如果某个节点上的 `doops-agent` 由宿主 Docker 管理，而且容器里 `/app/doops-agent`
被宿主二进制 bind mount 覆盖，那么镜像升级不会生效。此时必须先做架构优化：

1. 保留旧 agent 在线。
2. 拉取新镜像。
3. 启动一个不绑定宿主 `/app/doops-agent` 的新容器，或迁移成 K3s/DaemonSet。
4. 确认 gateway 看到新 hash。
5. 再停旧容器。

不要把“换镜像 tag”误当成“换二进制”。如果宿主文件覆盖了 `/app/doops-agent`，
这两件事不是一回事。

### Unicom 过渡节点

Unicom 当前的部署形态是过渡态，不是最终标准态：它同时有 gateway 容器和
旧宿主 Docker 容器。这个形态下，升级应该先把新标准容器/K3s 路径跑起来，
再下线旧容器。不要继续在宿主二进制 bind mount 的容器上堆镜像升级。

---

## 四、 特殊情况：无容器引擎的物理裸机部署

如果需要将其部署到一台没有 K8s 且容器平台残缺的物理机上，**杜绝**使用本地 `docker` 打成 tar 传过去的远古做法。

提供原生高效解法（零外部依赖，极速发版）：

```bash
# 1. 在本地交叉编译目标平台的干净二进制文件 (耗时 < 5秒)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/doops-agent-new ./cmd/agent

# 2. 利用 OCI 生成器直出镜像 tar
python3 scripts/build_oci_image.py /tmp/doops-agent-new /tmp/doops-agent-amd64.tar

# 3. 在目标机器用 ctr (containerd CLI) 直接导入
ctr -n k8s.io images import /tmp/doops-agent-amd64.tar
```
