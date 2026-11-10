# Doops 构建与部署强制规范 (SOP)

普通业务 workload 的权威发布流程见 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`；受管凭据的创建、授权、
验证、轮换和部署引用见 `docs/CREDENTIAL_RESOURCES.md`。

## 发布前判定

安装、正常升级和旧 Agent 恢复的入口统一见
[集群接入指南](CLUSTER_INSTALL_GUIDE.md#先选部署对象)。`install.sh` 只更新客户端和
Skill。已有 release 的外部脚本仍要求 Gateway 登录；它不是仅凭 kubeconfig 的免登录
恢复工具。镜像身份验收的已知限制见同文“外部脚本已知限制”，不得把示例命令作为已验证部署证据。

执行生命周期变更必须先验证 Gateway 的统一终态状态机，再切换 Gateway。候选
Agent 不再需要声明独立的生命周期 capability；必须验证前台和后台 RPC 的原始终态、
进程组终止、`202 cancel_requested`、持久化 `outcome_unknown` 以及按
`request_id` 路由流式通知。结构化 SSH 单独验证 `structuredSSH`。发布者仍须逐项
核对 Gateway 在线 Agent 基线，不能只以一个 target 的上线验收代替实际使用场景的
终态证据。任务没有给出升级范围时只完成源码与 CI 制品，不做生产切换。

先用 `doops version`、`doops list` 和
`doops -session <s> targets --target <t>` 核对已安装 CLI、Gateway 路由及精确
`cluster/instance`。升级前分别记录 CLI 和 Agent 的完整 commit，允许旧 Agent 与新 CLI
版本不同；不能要求升级前已经同版。升级后按选定 CI 制品验收。当前外部脚本以 CLI
commit 校验候选 Agent，因此使用该脚本时 CLI 必须与候选制品同版。源码已修改不代表 CLI、Gateway 或 Agent 已更新；
`cli-contract/v3` 只证明协议兼容性，`structuredSSH` 只证明 SSH 协议能力，
两者都不能证明 Agent 具有自升级控制器。

| 发布对象 | 入口 | 控制上下文 |
| :--- | :--- | :--- |
| 普通业务 workload | `doops -session <s> ask --target <t> --apply` | 目标 Agent 的运行环境，不读取本机 kubeconfig |
| 已具备控制器的 Helm Agent 镜像 | `doops -session <s> upgrade --target <t> --image <repository:tag 或 repository@sha256:<digest>` | 集群内 Job 复用 Agent 挂载，不读取本机 kubeconfig |
| K8S 首次安装、旧 Helm Agent 接入控制器、显式 Helm 配置变更 | `scripts/release-managed-agent.sh` | 指定外部控制器的 kubeconfig；没有 Helm release 的现有 workload 不自动接管 |
| 已有独立 Docker Agent | `scripts/release-host-agent.sh` | 外部宿主 SSH；不依赖旧 Agent、K8S 或 Helm |
| 空 Ubuntu/Debian 服务器 | `scripts/bootstrap-host-agent.sh` | 外部 root；安装 Docker/Compose，必须提供模型 settings；不覆盖已有 Agent |
| Gateway 控制面 | `scripts/deploy-gateway.sh --no-build` | 已核验 CI 产物，通过宿主机 SSH 发布，不通过 Agent target |

`-session` 用于操作上下文和审计关联，不是要求创建 Git worktree 或先 `doops push`。
CLI 安装必须把 Release Asset 内嵌 commit 对应的 skill/docs 一起安装；不得从当前
未发布工作区复制规则去搭配另一个版本的 CLI。安装记录中的 `commit` 与 `skill_commit`
必须一致。

> **核心纪律：绝对禁止在本地开发机或业务宿主机直接执行容器镜像构建。**
>
> 本地开发机的架构（如 Apple Silicon ARM64）、网络环境（代理/污染）以及未清理的缓存，极易导致打出的 OCI 镜像体积过大、架构错配或携带不安全的凭据。
> 所有线上与测试环境的 `doops-agent` 镜像，**必须且只能由 `main` 分支的 CNB CI 按标准流程构建、校验并推送。**

---

## 一、 Gateway 发布边界

`doops-gateway` 是公网控制面服务，必须作为独立发布包处理。它不是普通
`doops-agent` target，也不登记为 doops target。

当前 Gateway 是单活服务。`serve` 在打开数据库和恢复执行状态之前，对规范化的
数据库路径持有进程独占锁；第二实例会立即失败，不修改运行中实例的状态。进程退出
自动释放锁，不能删除 `.lock` 文件强行接管。首次升级到此版本必须先停止未实现该锁
的旧服务；这不是跨主机分布式租约。

固定发布入口：

```bash
bash scripts/fetch-gateway-release.sh \
  --release-tag <verified-release-tag> \
  --source-commit <verified-release-commit>

bash scripts/deploy-gateway.sh \
  --host 106.54.197.139 \
  --user ubuntu \
  --binary bin/doops-gateway \
  --no-build \
  --source-commit <verified-main-commit> \
  --verify-target gw-edu-coder \
  --verify-session gateway-release-<release>
```

当前 gateway 维护主机：

- SSH 地址：`106.54.197.139`
- SSH 用户：`ubuntu`
- Gateway URL：`https://doops.aiedulab.cn`
- SSH 凭据由运维密钥库或维护人保管，禁止写入仓库、日志、提交信息和文档正文。

这条脚本只走 SSH/SCP，在宿主机真实 root 下完成：

1. 从 CNB Release Assets 下载同 tag 的 `doops-gateway-linux-amd64`，按
   `checksums.txt` 校验，并确认二进制内嵌 release tag 与 commit；禁止在发布控制端重新编译。
2. SSH 校验远端真实 DB `/var/lib/doops-gateway/gateway.db` 存在且非空。
3. 上传二进制到远端 `/tmp`。
4. 备份 `/usr/local/bin/doops-gateway`。
5. 替换二进制并重启 gateway。
6. 校验 `/proc/<pid>/exe` hash 与下载的 Release Asset 一致。
7. 校验 `/v1/targets` 未认证访问返回 `401`，并通过 `--verify-target` 复用 DoOps 已配置认证，
   确认发布前的全部 target 均已重新连接；不得为发布导出或新建明文 token 文件。
8. 校验候选二进制内嵌的 `source-commit` 与本次 CNB 构建 commit 完全一致；
   旧的本地 `bin/doops-gateway` 或没有 provenance 的二进制会在上传前拒绝。

禁止事项：

- 禁止用 `doops exec` 启动或重启 `doops-gateway`。
- 禁止用 `doops push/write/read` 给 gateway 控制面发版。
- 禁止让 `deploy-gateway.sh` 在发布控制端执行默认本地构建；线上发布必须显式
  使用 `--no-build` 和由 `fetch-gateway-release.sh` 下载并校验的 CI Release Asset。
- 禁止用 base64/tar/分片临时传输替代 gateway 发布脚本。
- 不保留 gateway host agent 作为默认 target；需要主机巡检时走独立 SSH/监控流程。

如果 `doops push` 卡住，应修复 Git HTTP 反向隧道或目标 agent 版本，不允许用分片绕过。

---

## 二、普通业务 workload apply

普通业务 workload 的唯一运行时发布入口是 `doops ask --apply` 自然语言任务。
不得用环境注册表、部署模板或 workflow YAML 作为发布根，也不得从目录名、
环境名、业务名或历史发布记录猜测目标和参数；可读取任务指定的代码、Chart、values
和现有 release 解析事实，不要求已明确资产中的事实再粘贴到 prompt。CNB 负责构建、
校验和推送业务制品；
业务 workload 的 mutation 仍由同一 `ask --apply` 入口授权和验收。

### 1. 普通业务 workload apply
发布者必须先取得待发布制品的受控镜像版本。若制品由 CNB CI 构建，发布者必须在调用
前核对精确 source commit、registry digest 和构建成功证据；`ask --apply`、`--image`
和 `--version` 不自动验证 CI provenance。推荐调用 CNB 的 `api_trigger_deploy_agent`，
由 CI 临时配置单个 Gateway route 并提交下方同一自然语言命令；该入口不要求把仓库同步
到 target，也不要求创建或校验 target 隔离 workspace。人工控制端与 CI 使用同一入口、
相同的显式授权和验收要求。
自然语言任务必须明确 target、服务范围、变更原因、允许使用的 Helm 资产和验收条件；
业务镜像版本必须来自任务或任务指定的仓库、Chart、values、现有 release 的只读证据，
同 Chart 可有不同服务镜像及不同版本；缺少事实且任务没有指定可读取的来源时阻塞，不猜测或补默认值。
`api_trigger_deploy_agent` 只定义在 `main` 分支事件下，Gateway token 仅通过 stdin
交给 main 上构建的 CLI。CI secret 中的 `DOOPS_HELM_ASSET` 必须是任务指定的 HTTPS `.tgz` Chart 下载 URL；当前 remote 不支持 `oci://`，不新增 fallback。
`ask --apply` 直接把该远程 Chart 和明确的镜像事实交给
目标 Agent，不经由代码同步或隔离 workspace 传递发布输入。

```bash
doops -session release-<commit> ask \
  --target <configured-doops-target> \
  --apply \
  --msg '在 <target> 发布 <service-scope>，镜像 <repository>@sha256:<verified-ci-digest>；使用 chart https://charts.example/<name>.tgz；原因：<reason>；验收：<rollout、最终 Deployment spec/status、健康检查和功能路径>。'
```

`--apply` 是本次 mutation 的显式授权，不是兼容开关。任务返回的文本声明不能作为成功证据；验收必须来自本轮实际命令、Kubernetes 状态、健康检查和功能测试。需要凭据时只传 `--credential <resource-name>` 元数据引用，Gateway 必须按当前用户的 target 操作权限和凭据 target binding 授权。公开 prompt、doagent 上下文、CLI 输出和日志不得接收凭据 payload；Gateway 仅通过内部凭据物化调用向目标 Agent 短暂传递 payload。

doagent 会话默认使用 `restricted` 权限，普通任务只额外放行 `mcp_doops_k8s_read`；
该读取工具不得读取 Secret、ServiceAccount 或其它凭据载体。只有 `--apply` 对应的原生
`build` 模式允许 `mcp_doops_helm` mutation；apply 成功只接受规范化后的
`doops_helm` 可信完成证据。MCP 启动失败、工具缺失或仅出现其它工具调用时，发布必须
失败关闭。
该内置 MCP 必须设置 `trusted_result_attestation=true`，由 doagent 根据实际请求和
stdio 响应在本地生成结果证明。

调用方必须在提交任务前核对 CI source commit、registry digest 和构建成功证据；这只是
发布前置条件，不是 Agent 在本轮自动执行的 CI provenance 校验。
**普通业务 apply 执行顺序：**
1. 核对 target、服务范围、原因、验收条件及任务指定的资产；从任务或任务指定资产的只读证据核实各服务镜像版本，不猜测，也不要求用户重复粘贴已有事实。
2. 按任务指定的 Chart 默认值、按顺序可选多个 `value_files` 和非敏感 Chart-specific
   `set_strings` 执行 `--reset-values` 收敛；禁止隐式继承历史 values，也禁止在 schema
   失败后临场改变目标或参数重试。
3. 等待 Helm release 和 Kubernetes rollout 成功，并逐项执行任务中的健康与功能验收。

## 三、Agent 镜像构建工作流 (Server-Side Build)

本节只负责构建、校验和发布 `doops-agent` 运行时镜像，不负责普通业务 workload。
进行任何 Agent 运行时修改后，先由 `main` 的 CNB CI 构建并推送以完整 40 位 commit SHA
为标签的候选镜像，并把同一镜像再推成 `latest`。版本 tag 流水线（`api_trigger_release`）
在全部测试通过后推送 `vX.Y.Z`，也把同一镜像推成 `latest`。因此 `latest` 总是最近一次
成功的构建，main push 或版本发布以后到者为准；不写标签的 `docker pull` 就是它：

```bash
docker pull docker.cnb.cool/l8ai/ai/doops.sh
docker pull docker.cnb.cool/l8ai/ai/doops.sh:latest
```

Web 控制台镜像 `docker.cnb.cool/l8ai/ai/doops.sh/web:latest` 按同样规则更新。
`latest` 会移动，不是版本证据：生产升级仍用 `vX.Y.Z` 或 digest。该镜像携带同一 revision 的 `/app/doops-agent` 和锁定版本的
`/usr/local/bin/do-agent`，不包含 Gateway；Gateway 只作为同 tag 的 Release Asset
`doops-gateway-linux-amd64` 发布，不得在发布控制端重新编译。生产候选镜像必须解析为 registry digest；`doops upgrade` 也接受明确的
`repository:tag`，并在运行时校验实际 `imageID`。发布者必须自行保留精确 source commit、
registry digest 和 CI 成功证据；Agent、CLI 和
发布脚本不会替发布者证明 CI provenance。

### 双镜像构建原则

发布时维护两类镜像：

```bash
docker.cnb.cool/l8ai/ai/doops.sh/base-light:<release>
docker.cnb.cool/l8ai/ai/doops.sh:<release>
```

`Dockerfile.base.light` 只放基础运行时：doagent、BuildKit、`buildctl`、`skopeo`、kubectl、Python+PyYAML、Helm、SSH 客户端和系统包。`buildctl` 是标准镜像构建入口，`skopeo` 是标准 OCI 复制/检查入口。它必须保留 `ssh` 客户端但删除 SSH 服务端。`Dockerfile` 只放轻更新：doops-agent、skills、docs、entrypoint。禁止把基础 rootfs 压平成每个 release 都变化的单个多 GiB layer。

CNB 非同名制品路径使用 `docker.cnb.cool/<owner>/<repo>/base-light:<release>` 这种仓库下级路径；不要使用 `<repo>-base-light:<release>`。

正式 tag 流水线先从同一提交构建并校验 base-light，把其不可变 digest 作为 Agent
构建输入。基础运行时变更（例如 DoWeb 所需 Node）不能只进入 main 构建、却在正式
tag 构建中继续引用旧 base digest；没有本轮基础镜像产物时直接失败，不回退。

发布后必须验证：

1. `/app/doops-agent -help` 可运行。
2. `/usr/local/bin/do-agent --version` 与发布声明的 doAgent 版本完全一致。
3. `buildctl --version` 和 `skopeo --version` 可运行。
4. `python3 -c 'import yaml'` 可运行。
5. `helm version --short` 可运行。
6. `ssh -V` 可运行，且镜像中不存在 `sshd`。
7. `doops exec` 能读取 `hostname/date/df/free`；K8S 节点另验 `kubectl get nodes`，独立 Docker 不要求 K8S。
8. `doops ssh` 能代理用户明确给出的结构化 SSH 客户端命令；不得把多层 SSH
   拼进 `doops exec` 字符串、内联凭据或启动 SSH 服务端。
9. `doops ask` 能通过 doagent ACP HTTP 调用工具并返回结论。

镜像构建必须完成 base label 与前 6 项镜像内自检；第 7 至 9 项必须在 Agent 滚动更新后对真实 target 执行。

---

## 四、 Agent 部署与热更新机制

普通业务 workload 由 Helm 管理的 `Deployment` 托管。具体镜像、目标节点、Secret 引用、
Gateway 地址、Chart 和发布参数必须来自本次任务或任务指定的仓库、Chart、values、
当前 Helm release 的只读证据。发布前必须对比当前业务 workload 与候选 Chart 的 command、args、
env、probes、volumeMounts 和 volumes。包括只更新镜像在内的所有发布都必须调用结构化
`doops_helm upgrade-install`，按 Chart schema 使用任务指定的资产。普通升级统一
`--reset-values`，取消自动保留现有非镜像 values；需保留的值必须显式作为输入资产传入。
完整资产参数、可选 `expected_image` 和迁移说明见
[`NATURAL_LANGUAGE_DEPLOYMENT.md`](NATURAL_LANGUAGE_DEPLOYMENT.md)。
`upgrade-install` 先用相同输入执行 server-side Helm dry-run，再执行真实
mutation。不存在 Kubernetes 镜像 mutation 发布旁路。shell 里的 kubectl 或 Helm 变更不会被拦截，
但它们不产生结构化发布证据，不能让 `ask --apply` 判定发布成功；运维人员给出的 `exec` / `exec --bg` 命令按原样执行，
Agent 只拒绝删除根目录（`rm -rf /`）。用户明确给出的 SSH 命令必须通过结构化 `doops_ssh`
入口由在线 Agent 代理执行，不能把多层 SSH 拼成普通 `doops_shell` 字符串；该入口
遵守相同的权限、排队、超时、取消和审计规则。Helm 成功后还必须以
最终 Deployment manifest/spec/status 为事实源；若 Chart 需要显式 selector，按其 schema
从 live Deployment `spec.selector.matchLabels` 读取并写入任务输入，不固定业务 values 键，
也不要求历史 Helm values 已包含该字段。还要确认 generation、镜像和副本状态一致；只看 Helm revision 或 Pod Ready 不构成
发布成功。

已经运行该控制器的 Helm Agent 用 `doops upgrade` 自升级，不需要操作机 kubeconfig。
该路径要求 Agent 位于自身 namespace、运行标准 Helm-managed Deployment，并已有
`gateway.cluster/instance` 匹配的 Helm release；未提供 `--release` 时必须只有一个匹配
release，提供 `--release` 时按名称选择并校验该 release。Agent Pod 必须挂载可由 Job
共享的 `/root/.kube` 或 `/root/.kube/config`；Job 显式复用该 kubeconfig，其 Kubernetes
权限由该 kubeconfig 决定。Agent 只创建 Job，在 Agent 进程外执行 Helm upgrade，并在
新 Pod 未持续就绪时回滚。镜像可以是 `repository:tag` 或 `repository@sha256:<64 hex>`；
tag 使用 `Always` 拉取，回滚只接受历史已验证的 digest。
真实升级前 CLI 先对每个 target 提交一个预检 Job（`preflight=true`）：Job 以目标镜像启动，
所以镜像必须先被节点成功拉取；随后重复升级的全部改动前检查（Helm release 唯一、
当前 Pod 基线；现有 Deployment 的 `maxSurge`/`maxUnavailable` 不是准入条件，由 Chart 在
同一次升级里改成 `maxSurge=1`、`maxUnavailable=0`），并对同一 values 做 server-side
Helm dry-run。预检不修改 release，也不写验收回执。预检失败（含镜像拉取不到，预检 Job 600 秒
超时）时 CLI 报 `preflight failed, nothing was changed`，不提交真实升级 Job。
未纳入 Helm 的收编路径没有 release 可 dry-run，预检只证明镜像可拉取。
旧 Agent 不认识 `preflight`，会直接执行真实升级，所以预检要等 Agent 升级到该版本后才生效。
CLI 在 Job 成功后执行 `capabilities --require structuredSSH`，探针失败会创建回滚 Job。
升级后校验 `release_commit` 时，CLI 期望的是**自己这次发布**的 commit，即发起 `doops upgrade`
的 CLI 必须与目标 Agent 镜像出自同一次发布（同一 commit）。CLI 比 Agent 新或旧都会导致
`target Agent release commit mismatch`，并按上面第 3 条触发自动回滚；升级前允许 CLI 与 Agent
不同版本，但升级后的 `release_commit` 校验要求同版，因此必须先用与候选 Agent 制品同 commit
的 CLI 发起升级。
`PREVIEW` 仅表示参数预览，不检查 Helm release、挂载、权限或回滚可行性；`ACCEPTED`
仅表示 Job 已提交。Job 完成和协议声明不是完整发布验收：还须通过同一 target 的
`exec hostname` 确认 Gateway 当前实际服务的 Pod，并核对该 Pod 的 UID、Ready、
imageID 与候选 digest、`do-agent --version` 和实际功能路径。

第三条保证（校验失败自动恢复备份）只有在回滚会恢复的那个镜像还在 registry 里时才成立。
因此改动前检查还必须解析回滚目标：从 `helm history` 取唯一 deployed revision，
用 `helm get manifest --revision` 读出该 revision 的 doops-agent 镜像，再用
`skopeo inspect --raw` 读这个镜像的清单。认证用 Agent Pod 自己的 imagePullSecrets，
也就是 kubelet 拉回滚 Pod 时用的凭据；Agent Pod 没有 pull secret 时匿名读取，
和 kubelet 一致。Secret 内容只写进一个 0600 临时文件给 skopeo 读，检查结束即删除，
不进参数也不进 Job 日志。不新增凭据维度、挂载或权限：升级 Job 本来就要读写
同 namespace 里存放 Helm release 的 Secret。

检查直接问 registry 而不是让 kubelet 真拉一遍：kubelet 的拉取要排在节点上其它拉取
后面，繁忙节点上要等几分钟，却证明不了更多。探针失败按稳定类别上报，预检不做任何改动：

- `rollback_baseline_image_unavailable`：registry 回答没有这个清单
  （`manifest unknown`、`name unknown`、CNB 的 `artifact … not found`）。
- `rollback_baseline_image_unresolved`：历史 revision 不含唯一 doops-agent 镜像，
  或 release 没有唯一 deployed revision。
- `rollback_baseline_probe_failed`：其它失败，原因写在 `reason=` 里：registry 拒绝凭据
  （`unauthorized`/`denied`/`forbidden`，先于"缺失"判断，因为有的 registry 会把拒绝说成
  not found）、registry 连不上或超时、pull secret 读不到或不是 registry 凭据。
  这些都不是镜像已丢，不能用 `--allow-missing-rollback-baseline` 放行。

默认 fail closed。`rollback_baseline_image_unavailable` 的标准切换是下面的发新停旧，
不使用 `--allow-missing-rollback-baseline`。该开关只在运维明确拒绝更换
`cluster/instance`、并接受原地升级失败后无法拉回旧镜像时使用；它只作为参数传给
Agent（`allow_missing_rollback_baseline`），Job 日志会留下可见的 `warning:`。

### 回滚镜像缺失时发新停旧

回滚镜像已从 registry 消失时，原地 `doops upgrade` 不能在失败后拉回旧 Pod。默认切换是 0-1：模糊匹配到唯一的旧 release，装上唯一的新 release，验收通过后卸载旧 release。这是长期通道，不是临时 target。回滚镜像仍在仓库、并且不更换 workload 身份时，继续用 `doops upgrade`。

候选镜像先解析成 digest 再写入 Deployment。`latest` 只用于解析，不留在运行中的 tag 上。多架构索引用索引 digest，验收时的 `imageID` 必须等于这次解析出的 digest。候选镜像里的 `doops-agent -version` 给出 release 和 40 位 `release_commit`。这条路径不要求操作机 CLI 事先与候选镜像同 commit；切过去之后，下一次原地升级前再安装同 commit 的 CLI。

旧 release 在安装新 release 之前用模糊匹配选出，结果必须正好一个，并记下它的 namespace 和 release 名。候选的 chart 是 `doops-agent`，并且 release 名、workload 名或 `gateway.instance` 包含这次要切换的身份。同集群里对不上这个身份的其他 Agent 不在范围内；匹配到 0 个或多于 1 个就停止，不猜测。新 release 沿用原来的 `gateway.cluster` 和 `gateway.instance`。最终名字只有一套：Helm release `doops-agent`，`workload.name` 为 `doops-agent`，`selectorLabels` 为 `app: doops-agent`，本机路由为 `gw-<instance>`。旧 release 如果已经占用这个名字，并存期间只用临时 release `doops-agent-next`，验收并卸载旧 release 后，再安装正式的 `doops-agent` 并卸载 `doops-agent-next`。临时名字不留在线上。agent home 使用单独目录，不和并存的旧 Pod 共用 `/root`；正式 Pod 的 home 目录用 `/var/lib/doops-agent`。`hostNetwork` 保持关闭，`nodeSelector` 沿用当前 Pod 所在节点。Chart 用候选镜像里的 `/app/deploy/helm/doops-agent`。

安装由候选镜像中的一次性 Job 执行。旧 Agent 只负责提交这个 Job，不在自己的进程里对旧 release 做 Helm mutation。宿主机 kubeconfig 指向 `127.0.0.1` 时，Pod 网络访问不到 API；Job 用镜像里的 `configure_kubeconfig.py` 把 server 改写到 `KUBERNETES_SERVICE_HOST`，凭据仍来自原来的 kubeconfig。先做 server-side `helm upgrade --install --dry-run=server`，再正式安装。正式安装失败时只卸载新 release，旧 Deployment 继续服务。

新通道全部通过后才删旧：新 Pod Ready 且 `imageID` 等于钉住的 digest；Gateway 上原来的 `cluster/instance` 在线，且 `release_commit` 等于候选镜像的 commit；`capabilities --require structuredSSH` 为 ready；`exec hostname` 等于新 Pod 名，而不是旧 Pod。然后 `helm uninstall` 安装前记下的那一个旧 release。Deployment、ReplicaSet 和 Pod 随 release 删除，不把副本缩到 0 后留着。同集群的其他 Agent release 不动。

还没有这个控制器的旧 Helm Agent，以及 K8S 新集群第一次接入，用
`scripts/release-managed-agent.sh`，从外部控制器执行。新集群无 Agent 时传完整
`--values` 安装；已有 Helm release 时脚本先做 server-side dry-run，再做真实 mutation，
失败时回滚。

所有 Agent 升级路径都满足同一组保证，与由谁发起无关（`doops upgrade`、通过 `doops exec`
在集群内起的同一个 Job，或外部脚本）：

1. 改动前备份：Helm revision，或尚未由 Helm 管理时导出的 Deployment/ConfigMap。
2. 改动后校验：新 Pod Ready 且 imageID 与固定 digest 一致、Gateway 重连、`release_commit` 正确。
3. 校验失败自动恢复备份。备份可恢复的前提是回滚镜像仍可拉取，该前提由上面的
   改动前检查保证。发新停旧在删除旧 release 之前满足这四条，失败时只卸载新 release。旧 release 卸载后，这条通道只剩新 Pod，不再保留一个副本数为 0 的旧 Deployment。
4. 全程走 Gateway 审计路径，参数和输出不含凭据 payload。

唯一的硬性限制是 Agent 不能在自己进程里替换自己的 workload，升级始终由独立的
controller（集群内 Job 或外部脚本）执行。

尚未由 Helm 管理的 Agent Deployment 可以在满足上述保证的前提下收编进 Helm：
Deployment selector 与 Chart 的 `workload.selectorLabels` 一致即可原地接管；
没有 `/root/.kube` 卷的 Agent 设置 `kubeconfig.source=serviceAccount` 和
`serviceAccount.name`，由 Pod 自己的 ServiceAccount 提供集群权限。收编完成后
按已有 Helm release 路径升级。

### Agent RBAC 纳管与回滚

身份与权限配置契约见 [CLUSTER_INSTALL_GUIDE.md](CLUSTER_INSTALL_GUIDE.md#agentgateway-target-与集群身份)。
`rbac.create=true` 是显式集群管理授权，不能只为绕过 Forbidden 而给未授权 target 开启。
第一次把外管 SA/CRB 加入 release 时，必须由另一个已授权管理通道执行 Helm 收敛，
不能依赖正在收编的 SA 做控制器：回滚到旧 revision 会删除新纳管的 SA/CRB，
控制器会同时失去自己的身份或恢复权限。

1. 经 Gateway 审计通道核对 target 对应的集群、release/namespace、当前 revision、SA UID、
   CRB roleRef/subjects 和现有 owner。备份 Helm history、完整 values/manifest、SA/CRB、
   该 SA 的现有 RoleBinding 与 Agent Pod 状态；备份仅用户可读，不包含 token/Secret payload。
2. 用明确版本的 Chart 和完整 values，显式设置 `kubeconfig.source=serviceAccount`、
   `serviceAccount.name` 和 `rbac.create=true`。同名对象归属另一个 release 时停止。
   对已核验的无 owner 对象使用 Helm 的 `--take-ownership` 收编，不全局修改其他对象标签。
   去掉旧 CRB 的 `helm.sh/resource-policy: keep` 前保存原注解，避免残留永久集群权限。
3. 独立控制器先执行 server-side dry-run，再执行真实 Helm mutation。不要在 test Agent
   进程里替换自身。没有 Pod template 差异时，不为增加 RBAC 强行重启 Pod。
4. 回读 manifest 必须同时包含 Deployment、SA、CRB；live CRB 的唯一主体必须等于
   Pod SA。经目标 Agent 验证 `auth whoami`、跨 namespace 读 Pod、列节点和 API server
   dry-run 创建 Namespace/Deployment；验证 `auth can-i` 原有四项，以及 Ready、Gateway
   回连和固定镜像。不得用模型摘要或单独的 Helm ownership 标签当成功证据。
5. 失败由独立管理通道恢复旧 Helm revision，重新应用备份的 SA/CRB 并恢复其原 owner
   和 `keep` 状态，再检查身份、权限和健康。普通应用 `apply` 不会删除新增的 ownership
   注解，恢复时需明确移除备份中没有的 Helm 标签/注解。恢复后的 SA UID 若变化，
   现有 Pod 的投射 token 会失效，需要通过独立通道重新创建 Pod 后验证 Gateway 回连。

迁移后的普通镜像升级仍用 `doops upgrade`。回滚范围必须是已包含托管 RBAC 的 revision；
需要跨越首次纳管点回滚时，按上面的独立控制器流程恢复，不能让当前 SA 删除自己的权限。
预检、升级和显式回滚先用 Kubernetes `auth whoami` 取得控制器真实身份；
实际使用 SA 的控制器会比较前后 manifest 中自己的 SA 及相关绑定（包括挂载 SA kubeconfig 的情况）。
新增纳管、删除、改 roleRef 或 subjects 会在 Helm mutation 前报
`rbac_controller_identity_changed`，要求独立控制器处理，不绕过检查。
卸载 Agent 会撤销 release 管理的集群级绑定；外部维护的历史 RoleBinding 不在此自动删除。

Agent 已离线、Deployment 卡在 ImagePullBackOff 或收编中途失败时，由能访问该集群的
外部主机执行 `scripts/reinstall-k8s-agent.py`（从 `backend/` 运行，需要 kubectl、helm、
python3）：

```bash
python3 scripts/reinstall-k8s-agent.py plan --cluster <gateway-cluster> --instance <gateway-instance>
python3 scripts/reinstall-k8s-agent.py run  --cluster <gateway-cluster> --instance <gateway-instance>
```

`--image` 默认 `docker.cnb.cool/l8ai/ai/doops.sh:latest`，脚本向 registry 解析为
`repository@sha256:<digest>` 后只使用该 digest。`plan` 只读，列出将备份和删除的对象。
`run` 依次执行：用候选镜像在集群里起一次性 Pod 运行 `doops-agent -version`，拉取或启动
失败时不改动任何资源；把上一份可用的 Deployment（已有的收编/重装备份优先，其次是 live）、
清单和 Helm values 写入 `doops-reinstall-backup-<workload>` ConfigMap 与本机备份目录；
卸载旧 release，删除 Deployment、同 selector 的 ReplicaSet/Pod 以及 `doops-adopt-*`、
`doops-self-upgrade-*` 残留，Terminating 超过 60 秒的 Pod 强制删除 API 记录；从模型
settings 来源复制出 Chart Secret 后 `helm upgrade --install`；等待新 Pod Ready
（Ready 即 Gateway 隧道已连接）且 imageID 与拉取检查的运行时 digest 一致。任一步失败
自动卸载新 release 并重新应用备份的 Deployment；`restore` 子命令可手动执行同一恢复。
模型 settings、ServiceAccount 和拉取凭据 Secret 保留不删，payload 不打印、不落盘。
Gateway 离线时该脚本不经 Gateway，完成后须用 `doops targets` 核对 `RELEASE_COMMIT`
与脚本输出的候选 commit 一致，再按[集群接入指南](CLUSTER_INSTALL_GUIDE.md)验收。

已有 Helm release 的路径以 `maxSurge=1`、`maxUnavailable=0` 先拉起新 Pod，Gateway
`Register` 用新会话替换旧 WebSocket 后再删旧副本。Job 成功后 CLI 执行
`capabilities --require structuredSSH`，失败时请求回滚 Job；Gateway 完全离线时
无法自动恢复。自升级 Job 固定到 Agent 节点并保留 runtime class。新 Pod 与 Job 在同一节点时，
imageID 必须等于 Job 自己的运行时 digest。镜像按 `repository@sha256:<digest>` 固定时，调度器
把新 Pod 放到其他节点也可以，此时 imageID 必须等于固定 digest 或 Job 的运行时 digest，其它
digest 视为不同内容并回滚。按 tag 升级时只能在同一节点证明身份，新 Pod 必须与 Job 同节点。
自动回滚恢复的 Pod 按同一规则核对。验证证据保存在 `doops-upgrade-verified-<release>`
ConfigMap，记录实际 Pod 的节点和 digest；手动回滚只使用有验收证据的历史 revision。
旧 Agent 生成的是旧版 Job 脚本，新控制器要等 Agent 升级后才生效。

Helm 只删除它上一个 revision 渲染过的内容。上一 revision 渲染过、新 Chart 不再渲染的卷
会被删除，但 Helm 之外（例如首次 `kubectl apply` 建 Agent 时）加上的同名挂载不归 Helm 管，
会留下来指向已删除的卷，API server 因此拒绝整个 Deployment
（`volumeMounts[N].name: Not found`）。改动前检查用 `helm template` 渲染新 Chart，
与 `helm get manifest --revision <previous>` 对比，列出 live Deployment 里挂载了这些
被删卷的挂载（`stale_volume_mounts=...`）；Helm 从未渲染过的卷不在其列，挂载保持不动。
预检只列出不改。apply Job 在第一次改动前先把这些挂载备份到
`doops-upgrade-mount-backup-<release>` ConfigMap，再用带 `test` 守卫的 JSON patch 删除
（Deployment 在快照之后变化时整个 patch 被拒绝），然后执行 Helm 升级。任一步失败时
自动回滚：Helm rollback 恢复卷后，再把缺少的挂载加回去，最后照常校验回滚后的
Deployment；挂载恢复失败按回滚失败报告，需要人工处理。新 Chart 渲染不出唯一的 Agent
Deployment 时以 `stale_volume_mounts_unresolved` 失败，不做任何改动。

```bash
bash scripts/release-managed-agent.sh \
  --target <configured-target> \
  --session agent-release-<tag> \
  --cluster <gateway-cluster> \
  --instance <gateway-instance> \
  --release <helm-release> \
  --namespace <namespace> \
  --kubeconfig <operator-kubeconfig> \
  --image <repository>@sha256:<verified-ci-digest>
```

上述命令从 `backend/` 执行。`--version` 当前需要操作机 Docker 拉取解析镜像；
K3s/containerd 操作机优先传入已核验 digest。仅更换镜像时不传下面的配置变更参数。

`--proxy-allowed-hosts` 只接受逗号分隔的精确上游 hostname/IP，并写入
`proxy.allowedHosts`；省略时保留现网 Helm 值。传空字符串会显式恢复为拒绝全部。
脚本会拒绝 URL、端口、路径、通配符和空条目，并在新 Pod 上对照 Helm release
验证实际环境值。新集群省略该值时默认拒绝全部；需要开放时在完整 `--values`
文件中声明，不能同时传这个参数。

需要把命名 HTTPS 上游固定到指定地址时，重复传
`--host-alias <hostname>=<ip>`。脚本会把请求合并进 live Helm
`hostAliases`，拒绝 URL、端口、路径、通配符、非法 IP 和同一 hostname
的冲突地址。新 Pod 接管后必须同时通过 Pod spec 对比和容器内 DNS
解析验证；省略参数时保留现网 aliases，不允许解析失败时回退到其他地址。

新集群还没有 Agent 时，同一条命令改传完整 `--values` 和本机 kubeconfig，走这份
Chart 的 `helm upgrade --install`。已有非 Helm workload 按上面的收编方式进入 Helm。

### 已核验实例的发新停旧

旧镜像已从 registry 删除但旧实例仍健康时，先保留旧实例，独立部署固定镜像的
候选，完成 Gateway、Pod 身份和真实模型调用验收后才卸载旧 release。
2026-11-08 `unicom-k8s` 的单次 Job、配置、输入校验和恢复路径见
[该实例操作记录](../deploy/instances/unicom-k8s/README.md)。
Job 明确绑定旧 release/revision，不能直接复用于其他实例或重放未知结果。

### 独立 DockerHost 发布

Compose 管理的容器不能交给下述独立 Docker 控制器。2026-11-08 的
`unicom-1` 升级使用已核验该实例身份的独立 systemd 事务，保留 Compose、
固定镜像和相同的既有 kubeconfig；配置备份、缓存旧镜像、失败自动恢复及
Gateway/真实模型验收见
[该实例操作记录](../deploy/instances/unicom-1/README.md)。
这是绑定该次容器基线的单次控制器，不能套用于其他 Compose 项目。

已有独立 Docker Agent 使用 `backend/scripts/release-host-agent.sh` 和同目录
`release-host-agent.py`，由外部宿主控制器执行；不由当前 Agent 替换自己，
不自动从 `doops upgrade` 降级，也不要求先迁 K8S。旧 Agent 的 `doops_agent_upgrade`
或 `managedHelm` 缺失不能成为恢复前置条件，否则会形成“先升级才能升级”的依赖环。

以下从仓库 `backend/` 执行。全部参数必须显式提供；`--port` 没有默认 22，
`--release` 是 DoOps 发布版本而非 Helm release。先核对 CI 成功证据、完整 40 位
source commit 和不可变平台镜像 digest；脚本不替发布者证明 CI 来源：

候选镜像须先由外部控制器在目标 Docker 宿主执行
`docker pull <repository>@sha256:<verified-ci-digest>` 准备；这是独立于只读预检的制品准备
mutation。`--dry-run` 只检查已有本地候选制品，缺失时明确报告预检制品未准备，不自动 pull。

```bash
bash scripts/release-host-agent.sh \
  --target <configured-target> \
  --session host-agent-release-<release> \
  --host <host> \
  --user <ssh-user> \
  --port <verified-ssh-port> \
  --container <existing-agent-container> \
  --cluster <gateway-cluster> \
  --instance <gateway-instance> \
  --image <repository>@sha256:<verified-ci-digest> \
  --source-commit <verified-40-hex-ci-commit> \
  --release <vX.Y.Z> \
  --dry-run
```

只读预检通过后，使用相同参数去掉 `--dry-run`。可选
`--ssh-control-path <private-authenticated-socket>` 仅复用本机已认证的私有 SSH socket，
不接收 password/token；严格核验已知主机密钥，禁止关闭校验。密码、私钥或 token
不得写入参数、文件、示例或日志；不输出完整 `docker inspect` 或敏感环境值。
宿主密码只用于外部 bootstrap 的 stdin，以建立外部管理 SSH 认证，
不传给 Agent。成功访问目的地主机需要 Agent 内已有授权 SSH 认证材料，发布不新增该能力。

1. `--dry-run` 必须实际只读检查 SSH、宿主身份、Docker 权限、原容器运行配置、
   精确 Gateway route 与本地候选制品元数据；不拉取镜像、不创建/停止/重命名/重启容器，
   不运行候选二进制。不是仅打印参数；制品或关键证据缺失、配置无法保留时明确失败。
2. 正式发布持有宿主侧互斥锁，保留原 command/args、环境、挂载、网络、特权、
   restart policy 等配置；不得把 `network=host` 改成 bridge、增减权限或隐式迁 Compose。
   停止旧容器前，以静态 `--version` 模式校验候选二进制的 release/commit；仅输出版本，
   不启动 Agent、不监听或注册新 target，失败时不停止旧容器。
   停止后保留旧容器供回滚，同一 `cluster/instance` 不同时运行新旧 Agent。
3. 验证实际替代容器 ID、运行状态、镜像 ID/digest、Agent 与 do-agent 版本；Gateway
   必须是同一 `cluster/instance`，`release` 和完整 `release_commit` 分别匹配本次
   `--release` 与 `--source-commit`。真实 RPC 流须覆盖 foreground `exec` 和 `exec --bg`，
   核对终态及流式 `params.request_id` 与每次调用的 Root RequestID 一致。
   structuredSSH 验证协议握手、`host + options[] + command[]` argv 派发及预期失败终态，
   同样核对 Root RequestID；不以能力声明或普通 exec 代替。该验收不宣称目的地主机
   成功认证；外部管理 SSH 的成功访问另记入口、身份和结果，不作为 Agent SSH 认证证据。
   外层脚本自动检查前台双流输出、后台真实输出与零退出终态、结构化 SSH argv 的
   预期连接失败和随后命令可执行。CLI 按每次 Root RequestID 接收通知；能力声明和
   version 返回不能代替这些探针。探针失败立即回滚，不反复重放未知执行。
4. 任一替换或验收失败，由外部控制器停止新容器并恢复旧容器，复核原镜像、配置和
   Gateway 接管；不调用旧 Agent 自升级，不删除旧容器。回滚失败必须明确报告并保留现场。
   外层回滚保护必须在首次宿主替换调用前安装，覆盖 SSH 返回状态丢失；部分回滚
   失败后可从持久阶段重试，不能因已删除新容器而永久卡死，也不能放宽容器所有权校验。
   每次调用复用其唯一 SSH staging ID 绑定 receipt；重复 session 被拒绝后，自动回滚
   不得操作上一次发布的 receipt。该 ID 由脚本内部生成，不新增用户配置或版本门禁。

宿主入口和只读授权边界见 [宿主访问说明](PHYSICAL_HOST_ACCESS.md)。

### 环境依赖注射规范 (Secret)
严禁在 `agent.yaml` 中明文出现 `LLM_API_KEY=sk-xxxx` 之类的配置。此类密钥应由 Secret 提取并通过 `envFrom` 下放。

### 容器运行时挂载规范

容器化 `doops-agent` 需要在 agent 内部继续调用宿主机容器运行时，例如
`nerdctl pull/run`、BuildKit、或一次性 helper 容器。只挂
`/run/containerd/containerd.sock` 不够：`pull` 可能成功，但 `run` 会因为
overlay snapshot、FIFO、`/etc/hosts` 或 nerdctl name-store 位于宿主机路径而失败。

标准 Helm Deployment 的 Agent 容器必须同时挂载：

```text
/run/containerd      -> /run/containerd
/var/lib/containerd  -> /var/lib/containerd
/var/lib/nerdctl     -> /var/lib/nerdctl
```

遇到 containerd overlay/FIFO/name-store 错误时，保留现有 Agent 并读取挂载、节点和
运行时错误证据。挂载修正必须进入仓库 Chart/values，再由上述 Agent 升级路径收敛。
Agent 完全不可用时，SSH 仅用于 Agent 自恢复，恢复后仍须完成 Gateway、版本和
功能验收，不能把一次主机修复当成版本发布。

### 其它非 Helm 节点

已有独立 Docker Agent 按上述 DockerHost 入口保持运行方式发布。非 Helm 的 K8S
`agent.yaml`、二进制 bind-mount 或裸机 tar 不在该入口范围；需先明确其受支持的发布或
迁移方案。

## 五、GitHub Actions 标签发布（开放仓库）

公开 GitHub 仓库的 `vX.Y.Z` tag 由 [`.github/workflows/release.yml`](../../.github/workflows/release.yml)
自动构建并发布；生产镜像与部署仍走 CNB（见第三节、第四节），两条发布路径互不替代。

1. **版本线与触发**：GitHub 公开线固定发 `v0.2.x` 补丁（可带 `-rc.N` 预发布后缀）；只接受
   `push` 的 `v0.2.x` tag 和手工 `workflow_dispatch`（默认 dry-run，不创建 Release）。切换到新的
   minor/major 系列属于维护者决策：先把仓库变量 `DOOPS_RELEASE_SERIES`（默认 `0.2`）改成新系列
   （例如 `0.3`），否则该系列 tag 一律被拒绝。tag 必须已存在且指向 `main` 上的提交；已存在的
   Release 拒绝重建，已发布资产不可变。
2. **CI 门禁**：发布前直接复用 `.github/workflows/ci.yml`（`workflow_call`），tag 不会绕过
   main 的检查（Go/Web 测试、`backend/test` 契约测试）；secret scan 只在 branch push/PR 上运行——tag 和手工发布没有增量 commit 范围，
   全历史扫描会把历史上已存在的测试 fixture 当成泄漏，tag 指向的提交在合入 main 时已扫描。
3. **构建**：`backend/scripts/build-cli.sh --all` 在固定 `go1.25.14` builder 身份下产出
   `doops-{darwin,linux}-{amd64,arm64}`、`doops-gateway-linux-amd64` 和 `checksums.txt`；
   发布前校验 sha256、`release=<tag> commit=<commit>` 身份和 `doops.sh/cli-contract/v3`。
4. **发布**：`gh release create --verify-tag --generate-notes`（预发布加 `--prerelease`），
   只有 publish job 拥有 `contents: write`；二进制同时生成 GitHub build provenance attestation。
5. **发布后复验**：从 GitHub Release URL 重新下载资产，运行
   `backend/scripts/verify-published-cli-release.sh` 复核 checksums 与身份，`gh attestation verify`
   校验来源，并用 `backend/scripts/install.sh`（`DOOPS_RELEASE_ASSET_BASE` 指向该 Release）
   完成一次端到端安装。
6. **安装来源**：`install.sh` 默认仍从 CNB Release Assets 下载；`DOOPS_RELEASE_ASSET_BASE`
   只接受 https（回环地址用于测试），不会放宽 sha256、身份或 contract 校验。
7. **契约测试**：`backend/test/test_github_release_contract.py` 锁定触发条件、CI 门禁、
   不可变发布、资产集合、builder 身份和复验步骤。

手工 dry-run（只构建与复验，不创建 Release）：

```bash
gh workflow run release.yml -f tag=<release-tag> -f publish=false
```
