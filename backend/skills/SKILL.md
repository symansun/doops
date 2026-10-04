---
name: doops-server
description: |
  用 doops 通过 doops-gateway 连接和操作主动注册的 doops-agent 节点。
---

## Skill 定位

本 skill 是 doops 的入口导航和操作纪律，不承载所有功能细节。

- 安装与接入：先看本文，再按 `docs/CLUSTER_INSTALL_GUIDE.md` 执行。
- 日常使用：先看本文命令选择，再按 `docs/DOOPS_USAGE_GUIDE.md` 执行。
- Gateway/API 细节：看 `docs/DOOPS_GATEWAY_TUNNEL.md` 和 `docs/DOOPS_API_GUIDE.md`。
- CI/CD 与发布：看 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`。
- 凭据管理：看 `docs/CREDENTIAL_RESOURCES.md`。
- 升级维护：看 `docs/BUILD_DEPLOY_SOP.md` 和 `docs/DOOPS_MAINTENANCE_LOG.md`。
- 模型与 ask 配置：看 `docs/MODEL_STANDARDS.md`。
- 物理机/宿主机/Bare-metal 接入：看 `docs/PHYSICAL_HOST_ACCESS.md`。

权威源码在 doops.sh 仓库 `backend/skills/SKILL.md`。项目里的
`.agent/skills/doops/SKILL.md` 是安装副本；修改 skill 时必须先改权威源码，再通过
`scripts/install.sh` 同步。安装器只读取指定 Release CLI 内嵌 commit 的 skill/docs，
不复制当前未发布工作区；本地缺少该 Git 对象时应获取对应提交后重试，不混装版本。

## 核心纪律

- 优先使用 PATH 中的 `doops`；标准安装路径是 `~/.local/bin/doops`。
- 所有远端调用必须带 `-session <name>`。
- 执行生命周期由 Gateway 统一管理，不再通过 Agent capability 字段决定是否允许执行。
  `exec`、`exec --bg`、`ssh`、`ask` 和 `ask --apply` 都必须等待对应 RPC 的实际终态；
  取消 ACK 不是终态，取消、超时或断连且缺少终态证据时写入 `outcome_unknown` 并阻止后续
  mutation。结构化 `ssh` 仍只要求 `structuredSSH` 参数协议，不得删除未知结果保护或
  静默降级。
- Agent 流式 `notifications/message` 必须携带 `params.request_id`；Gateway 和 CLI
  按请求 ID 路由事件，同一 session 的并发调用不能互相覆盖。Gateway 会先持久化
  执行终态或 `outcome_unknown`，再释放资源 lease；持久化失败时保留 lease 并自动重试。
- 定时巡检由 Gateway 按 job 持久 lease 单飞执行；`tick` 与 `run-now` 冲突时不重复
  占用目标。执行结束、失败、取消或 Gateway 重启后，lease 必须通过终态 `defer` 或
  到期自动回收；禁止要求人工 unlock 作为正常调度流程。
- 发布前先核对 `doops version`、精确 target 和实际 Agent 的 `release_commit`。同一次
  DoOps 只有一个发布版本标签；CLI、Gateway 和 Agent 必须回传同一个 release，并同时
  回传同一份完整 40 位 `release_commit` 作为不可变 provenance。`cli-contract/v3` 是
  协议兼容标识，不是第二个软件版本；
  `doagent` 依赖版本只属于镜像构建 provenance。`structuredSSH` 不能证明自升级控制器已上线。
  `-session` 是操作上下文，不是源码同步要求；普通发布不要求 Git worktree 或先 `push`。
- CLI 只接受 Gateway target；不存在直连 target、SSH target 或按 target 保存 token 的模式。
- 从客户端直接 SSH 到 Agent 宿主机只用于首次安装、自恢复 agent，以及发布
  `doops-gateway` 控制面；DoOps CLI 不把 SSH 主机建模为 target，也不直连 Agent。
- 标准 Agent 镜像提供出站 `ssh` 客户端，但不安装或启动 `sshd`。已在线 target 的
  SSH 必须通过 `doops ssh` 的结构化 `host + options[] + command[]` 入口代理；该入口
  在 OpenSSH 边界统一编码远端 argv，避免多层 SSH 的嵌套引号被远端 shell 破坏。
  允许的选项只有 `-4`、`-6`、端口、身份文件，以及 `BatchMode`、`StrictHostKeyChecking`、
  `ConnectTimeout`、`IdentitiesOnly`。会改写目标或执行本地命令的选项由 Agent 拒绝。
  `doops exec` 仍用于普通 Shell，不得把嵌套 SSH 拼成一个命令字符串；CLI 只拦截
  直接以 `ssh`、`/usr/bin/ssh` 或 `/bin/ssh` 开头的命令，不是完整 Shell 分析器，
  包装命令仍须由调用者遵守结构化入口要求。SSH 操作遵守与其它 exec 相同的权限、排队、超时、取消
  和审计规则。目标、参数和远端命令必须由当前任务明确给出，不得把私钥、密码或
  token 内联到命令、prompt、日志或工作区。
- SSH 收到 Agent 结构化终态时，明确远端退出码 `1..254` 按普通失败审计，不因非零
  退出阻断后续操作；结构化工具错误即使没有退出码也已到达终态。`255`、信号终止、
  取消、断连或缺少结构化终态仍保留未知结果保护；不得根据命令文本猜测只读，也不得
  把本地 SSH 进程退出当成远端工作已停止。
- `doops-gateway` 是独立发布包，必须从 CNB 不可变镜像提取，并用
  `scripts/deploy-gateway.sh --host <gateway-host> --source-commit <main-commit>`
  从 SSH 宿主机上下文发布；旧的本地二进制会被拒绝。
- gateway 控制面不登记为 doops target，不通过任何 doops target 发布 gateway 本体。
- 不要用 base64、tar 或手写分片传输绕过 `push`。`doops_workspace_*` 是低层兼容 JSON-RPC 工具，不是 CLI、发布脚本和 skill 的标准路径；如果看到 512KB 分块日志，说明调用方绕过了当前 `doops push/pull`。应修复 Git HTTP 反向隧道、目标 agent 版本或发布脚本。
- 普通业务 workload 的标准发布入口是 `doops ask --apply`。自然语言任务必须直接给出 target、服务范围、明确镜像版本、
  变更原因和验收条件；不得从文件路径、环境名、业务名或历史声明推断这些值。
- 普通 `ask` 运行在 restricted 权限，只允许内置只读工具和 `mcp_doops_k8s_read`；
  `--apply` 才进入原生 build 模式，并且只允许 `mcp_doops_helm` 执行发布 mutation。
- 更新现有 Helm release 时，只要自然语言任务已明确 release、namespace、chart 和
  不可变镜像，Agent 必须直接调用一次 `mcp_doops_helm upgrade-install`。`value_files`
  可以为空，但必须使用任务明确给出的远程 `https://`/`oci://` chart 和可选
  HTTP(S) values URL；`set_strings` 只传 `image.repository` 加一个受控版本选择器：
  不可变的 `image.digest` 或严格的 `image.tag=release-YYYYMMDD`，不得混用。工具自动
  在显式镜像覆盖模式保留当前 values 并先 dry-run。完整 Chart 值模式同时省略
  `value_files` 和 `set_strings`，从 HTTPS Chart 内唯一 `values.yaml` 校验
  `image.repository` 与 `image.tag=release-YYYYMMDD`；不合并历史值、不覆盖镜像。
  受管 `helmPull` 只按已授权 namespace/release/chart 消费 Secret，验证并钉住实际包摘要；
  CNB 使用其官方 Helm OCI 仓库的 HTTPS `.tgz` 下载接口，不读取 `index.yaml`。
  不得读取 target 隔离 workspace、不得要求用户补充本地
 候选 values，也不得用缺少 `confirm=true` 的调用试探。
- 更新现有 Helm release 时，Agent 必须显式传入 `asset_source`，省略时默认为 `remote`。
  workspace 模式只允许 runtime-bound session workspace 下的相对
  `deploy/helm/<env>` Chart 与该 Chart 唯一的 `values.yaml`；路径必须是存在的 regular
  文件或 Chart 目录，拒绝绝对路径、`..`、symlink escape 和额外 values overlay。
  workspace 的 `set_strings` 必须为空，任务必须显式传完整 `expected_image`：
  `repository:release-YYYYMMDD` 或 `repository@sha256:<64 hex digest>`。Helm cwd 必须是
  精确 `/root/ws/<session>`，使用 committed values、`--reset-values` 并先执行 server
  dry-run，不读取旧 release values。不得要求用户补充其它 values，也不得用缺少
  `confirm=true` 的调用试探。
- CI 的精确 source commit、镜像 digest 和构建成功证据必须由发布者在调用前核对。
  `ask --apply`、`--image`、`--version` 和 Helm 工具只校验允许的输入形态、解析结果和
  运行时证据，不自动核验镜像是否来自指定 CI。
  发布镜像只由 CI 构建；Agent 内存在 BuildKit 不代表允许在业务 target 上重新构建，
  也不能把工作区同步或 SCP 源码传输作为普通发布的前置步骤。
- 需要凭据时使用可重复的 `--credential <resource-name>`。该参数只传递资源名；Gateway
  将它绑定到当前用户、target 和 session，并按 cluster/instance、namespace、workload、
  image 或 Helm 的精确 scope 授权和物化。公开 prompt、doagent 上下文和 metadata
  binding 不接收 credential payload；目标 doops-agent 只在内部凭据控制面临时接收
  解密 payload 以创建或验证目标资源。
- 开发项目可以在 `.agent/doops-credentials.yaml` 声明 metadata-only 的
  `credentialRef + use`。Agent 在 Git、Registry、Helm、TLS 或 API 开发操作前必须运行
  `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml`。
  preflight 只检查当前用户在 manifest target 上是否拥有匹配 use 的 active grant，
  且凭据存在 active version；
  它不读取 payload、不授予权限、不物化凭据，也不允许回退到环境变量、旧登录态或
  Skill 目录中的文件。
- CNB CI 负责测试、构建和发布不可变制品，不选择生产 target。Gateway 负责鉴权、审计、
  资源锁和凭据预物化，实际业务 Helm/Kubernetes mutation 由目标 Agent 在其运行环境执行。
- 当前没有 EnvironmentRegistry、`cicd run`、CICDReport 或 `doops_cicd_result` 发布链路。
- `doops-agent` 自升级只适用于 Agent 自身 namespace 中的标准 Helm-managed Deployment，
  且必须已有 `gateway.cluster/instance` 与 Agent 匹配的 Helm release；未提供 `--release`
  时必须只有一个匹配 release，提供 `--release` 时按名称选择并校验。Agent Pod 还必须
  挂载可由 Job 共享的 `/root/.kube` 或 `/root/.kube/config`。集群内 Job 显式复用该
  kubeconfig，Kubernetes 权限由该 kubeconfig 决定；这些是集群内前置条件，不要求用户
  提供本机 kubeconfig。在线 Agent 创建集群内 Job，在 Agent 进程外执行 Helm upgrade。
  Chart 保持 `maxSurge=1`、`maxUnavailable=0`；新 Pod 未持续就绪时由 Job 回滚。
  镜像必须是 `repository@sha256:<64 hex>`，包括 `--dry-run` 预演。
  Job 校验最终 Deployment 与实际 Pod 的 UID、Ready 和 imageID；发布者须使用已核验的
  平台镜像 digest，不能用多架构索引 digest 冒充实际 imageID。
  命令：`doops -session <s> upgrade --target <t> --image <repository>@sha256:<digest>`。
  `--dry-run` 只预览参数，不校验现网 release、挂载或权限。`--rollback` 回到前一个
  成功且已被替代的 revision。
  CLI 在 Job 成功后执行 `doops -session <s> capabilities --target <t> --require structuredSSH`；
  探针失败会尝试再创建回滚 Job，需要 target 仍可连接。完整交付仍须核对 Gateway
  实际服务 Pod 的 UID、imageID、版本和功能路径，不能把 Job 完成或能力声明当作全部验收。
  CLI 的 `OK` 仅确认 Job 与协议检查，不代表这些发布验收已经完成。
  不能把 SSH 改写成普通 `exec`。
  还没有这个控制器的旧 Agent，以及尚无 Agent 的新集群，第一次接入仍用
  `scripts/release-managed-agent.sh` 和操作机 kubeconfig。其 `--values` 新集群模式仅
  做 Helm wait 和最终 Deployment 检查，不做 Gateway 接管或自动回滚；完成后必须继续
  执行安装验收。该脚本只安装新 Helm release 或更新已有 Helm release；已有 workload
  但没有 Helm release 时会停止，不执行 adoption。`doops upgrade` 不修改
  `proxy.allowedHosts`、`hostAliases` 或其它 Agent Helm 配置，这些变更只走外部脚本。
  禁止创建临时 Agent、临时 target 或 release-runner，禁止 `kubectl set image`、
  Docker restart，以及让 Agent 进程直接 helm upgrade 自己。
- 已有 Helm Agent 需要开放网站代理上游时，唯一发布脚本必须显式传
  `--proxy-allowed-hosts <comma-separated-exact-hosts>`；省略保留现网值，传空字符串恢复
  为拒绝全部。URL、端口、路径、通配符和空条目必须在 mutation 前拒绝，发布后必须核对
  新 Agent 环境与 Helm release。该参数只控制 Agent 所在网络的上游 hostname/IP，不控制公网域名。
- 长构建可以使用 `doops -session <s> exec --bg --target <t> --cmd '<cmd>'`。自然语言
  发布中的 Helm 收敛必须使用 `mcp_doops_helm`；运维人员显式给出的前台 `exec` Helm
  `upgrade/install/rollback/uninstall` 仍是合法的确定性操作，并保持 Gateway 串行化
  和完整退出审计。Agent 必须拒绝后台 doops-agent Helm mutation。不要在普通 `exec`
  命令中使用 `&`、`nohup` 或 `setsid`，因为调用结束时 agent 会终止该 shell 的子进程。
- 不要混用 token：
  - gateway CLI 操作用 gateway user token。
  - agent 注册 gateway 不使用 token，只声明 cluster/instance。
- 物理机硬件检查先核对现有拓扑和授权入口：已有物理机 target 可直接检查，也可经
  已在线 target 用结构化 `doops ssh` 到明确的宿主地址。查看硬件不要求先安装 Agent；
  VM 内的 `nsenter` 不能跨越 hypervisor。工具缺失、容器/VM 不可见不等于宿主无 GPU。
  入口、认证前置条件与证据要求见 `docs/PHYSICAL_HOST_ACCESS.md`。
- 网站公网代理：管理员先在 Gateway 创建 `host -> cluster/instance -> upstream`
  路由；Agent 必须通过 Helm `proxy.allowedHosts` 配置精确上游 hostname（Chart 注入
  `DOOPS_PROXY_ALLOW_HOSTS`，默认拒绝全部）。当前只支持 HTTP/HTTPS 流式代理，
  不支持 WebSocket 或裸 TCP；`GET/HEAD/OPTIONS/TRACE` 按只读查询处理，其它 HTTP
  方法按 target mutation 处理，详见 `docs/DOOPS_GATEWAY_TUNNEL.md`。
- 不要在日志、终端输出、文档、提交记录里暴露完整 token。
- 如果用户是在发账号、发 token、授权、查审计、清理审计、查看/取消 gateway active operations，要按 gateway 管理动作处理，不要混成普通 target 运维。
- 同一 target 的结构化 `push/write/clean/read/pull` 按 workspace 互斥，不同 workspace
  可并行；普通 Shell/SSH/bg 与 `ask --apply` 使用 target mutation 锁，session 名称
  不能证明其副作用隔离，不按命令关键词猜测只读性。结构化 K8s 查询、任务状态和节点
  信息可在发布期间查询；Agent 自升级仍独占 target。当前没有跨 target 的 release/
  数据库统一锁，也没有执行器 fencing，不能宣称支持安全强制抢占发布锁。
- 遇到并发限额或资源冲突，先用 `doops -session <s> admin operations list --target <t>`
  核对 `state/resource/blocked_by`，等待锁不占用户执行槽。不能因本会话着急就取消其它
  合法发布；获得明确取消授权后才用 `admin operations cancel --id <operation-id>`。
  `202 cancel_requested` 不是停止证据。缺少终态，或 Shell/SSH/bg、apply、K8s 写操作
  被中断而无法证明外部变更已停止时，持久化 `outcome_unknown` 并阻止新写入。
  管理员须独立核对实际状态，再用
  `doops -session <s> admin operations reconcile --target <t> --id audit-N --confirm-stopped --reason <reason> --evidence <evidence>`
  记录人工确认；不得自动解锁、接管或重放。`unlock` 只重连空闲 target。
- 给用户签发 gateway user token 时，优先使用 `doops admin token create`；不要 SSH 到 gateway 主机手工运行内部 DB 命令。

## 配置与凭据

Doops CLI 默认只有一个配置源：

```text
~/.agent/skills/doops/config.json
```

同目录运行态文件：

- `auth.json`：`doops login/logout` 管理的 Gateway 登录态，结构必须是 `{"tokens":{"https://gateway.example.com":"..."}}`。
- 同一 Gateway 的所有 target 共用一份 token；target 配置只保存路由，禁止写入 Gateway user token。
- `history.jsonl`：本机命令历史。
- `sessions.json`：本机会话缓存。

旧路径和运行痕迹不能作为当前配置依据：

- `~/.config/doops/config.json`
- `~/.doops/auth.json`
- 项目 `.doops/`

`DOOPS_CONFIG=/path/to/config.json` 只改变配置文件位置，仍执行相同的严格 Gateway-only schema 校验。

标准配置文件只放 gateway target：

```json
{
  "servers": [
    {
      "name": "jm",
      "aliases": ["jy"],
      "gateway": "https://gateway.example.com",
      "cluster": "doops-jm",
      "instance": "jm-228",
      "use": "JM via gateway"
    }
  ]
}
```

## 安装引导

安装 CLI 和 skill：

```bash
git clone https://cnb.cool/l8ai/ai/doops.sh.git
cd doops.sh
bash scripts/install.sh --version <release-tag> --project /absolute/path/to/project
```

安装结果：

- CLI：`~/.local/bin/doops`
- Skill 副本：`<project>/.agent/skills/doops/SKILL.md`
- Canonical config：`~/.agent/skills/doops/config.json`

接入 gateway target：

```bash
doops add \
  --name jm \
  --gateway https://gateway.example.com \
  --cluster doops-jm \
  --instance jm-228 \
  --aliases jy \
  --use 'JM via gateway'

doops -session setup login --target jm
```

Agent 的 K8s/容器部署和 Gateway 注册见 `docs/CLUSTER_INSTALL_GUIDE.md`。CLI 只有在 Gateway heartbeat 可见后才能操作该节点。

淘汰本机遗留的离线 target：

```bash
doops -session retire-<date> remove --target <gateway-target>
```

该命令先成功查询 Gateway，再确认精确 `cluster/instance` 当前不可见后删除本机 route 配置。Gateway 不可达、认证失败、权限不明确或 target 在线时都会拒绝；它不删除 Gateway grant、审计、代理路由或历史 agent 状态。

## 使用引导

Gateway 的 `/console` 提供用户登录、目标选择、明确授权的业务部署、运维与审计。
页面部署与 CLI 使用同一 `operation=apply`、精确 target 和凭据资源名边界；任务所需
Chart 与镜像来源须先核对，不要求源码同步。用户可查本人操作，平台管理员可查全局
审计。页面行为与验收边界见 `docs/DOOPS_WEBUI.md`。

先确认本地配置和 gateway 在线状态：

```bash
doops list
doops -session preflight targets --target <gateway-target>
```

`targets --target` 只检查该配置对应的 `cluster/instance`，不会返回同一 gateway
上的其它节点；目标离线时命令明确失败。

常用命令：

| 需求 | 命令 |
| :--- | :--- |
| 执行确定性命令 | `doops -session <s> exec --target <t> --cmd '<cmd>'` |
| AI 自主分析 | `doops -session <s> ask --target <t> --msg '<task>'` |
| 发布任务 | `doops -session <s> ask --target <t> --apply --credential <resource-name> --msg '<target、范围、明确镜像版本、原因、验收>'` |
| 创建凭据 | `doops credential create --target <t> --name <name> --scope personal --type <type>` |
| 写入凭据版本 | `printf '%s' '<json>' \| doops credential put --target <t> --id <credential-id>` |
| 查询 target 可用凭据 | `doops credential list --target <t>` |
| 查询本人管理凭据 | `doops credential list --target <t> --view managed` |
| 检查项目开发凭据 | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` |
| 授权凭据 | `doops credential grant --target <t> --id <id> <exact-scope-flags>` |
| 验证 staged 版本 | `doops -session <s> credential verify --target <t> --id <id> --version-id <version> <exact-scope-flags>` |
| 激活凭据版本 | `doops credential promote --target <t> --id <id> --version-id <version>` |
| 吊销授权 | `doops credential grant-revoke --target <t> --id <id> --grant-id <grant>` |
| 吊销资源 | `doops credential revoke --target <t> --id <id>` |
| 推送本地目录 | `doops -session <s> push --target <t> --src <dir>` |
| 拉取远端工作区 | `doops -session <s> pull --target <t> --dest <dir>` |
| 小文本读写 | `doops read/write ...` |
| 工作区清理 | `doops -session <s> clean --target <t> --workspace <s>` |
| 升级 agent | `doops -session <s> upgrade --target <t> --image <repository>@sha256:<digest>` |
| 预演 agent 升级 | `doops -session <s> upgrade ... --dry-run` |
| 创建 Gateway 用户和 API key | `doops admin user create --target <t> --user <u> (--password-stdin\|--generate-password)` |
| 查看 Gateway 用户 | `doops admin user list --target <t>` |
| 管理 target grant | `doops admin grant <list\|add\|delete> ...` |
| 管理员签发 token | `doops admin token create --target <t> --user <u> --name <label> --expires 720h` |
| 管理员查看 token | `doops admin token list --target <t>` |
| 管理员撤销 token | `doops admin token revoke --target <t> --id <token-id>` |

大文件、压缩包、数据库导出、图片、视频、课程资源包等资产，用 `pull` 拉取整个 session 工作区。不要用 `read`，也不要让 `exec` 输出 base64。

Gateway 管理员签发 token 示例：

```bash
doops admin token create \
  --target jm \
  --user gaojiaqi \
  --name maintenance \
  --expires 720h
```

`--target` 负责选择 gateway 地址和当前管理员登录态。新 token 只打印一次；如需把新 token 设为该 Gateway 的统一登录态，显式加 `--save-as <target-name>`。
轮换时先创建并缓存新 token，再用 `admin token list` 按 ID/前缀确认旧 token，最后执行 `admin token revoke`。管理员 token 不得通过命令行参数传入。

## Gateway 用户创建与目标授权

创建用户、设置密码或授权 target 使用 CLI；CLI 通过 HTTPS Gateway 管理 API 完成流程，
不要 SSH 到 Gateway 或直接修改数据库：

```bash
doops admin user list --target <gateway-target>
printf '%s' '<PASSWORD_FROM_SECURE_INPUT>' | \
  doops admin user create --target <gateway-target> --user <username> \
  --password-stdin --grant-target <allowed-target> --name user-create --expires 720h
```

也可以用 `--generate-password` 生成一次性密码。CLI 会拒绝同名用户，创建普通用户时
固定发送 `admin:false`，删除 Gateway 自动写入的 wildcard bootstrap grant，再按
`--grant-target` 写入精确 scope 和普通操作 actions。任何中途错误都会停用新用户；
没有 `--grant-target` 时不会授予任何 target 权限，只创建没有操作权限的用户和 API key。
密码和 API key 只在需要时输出一次，Gateway 只保存它们的哈希。

独立查询或调整权限：

```bash
doops admin grant list --target <gateway-target> --user-id <user-id>
doops admin grant add --target <gateway-target> --scope-target <allowed-target> \
  --user-id <user-id>
doops admin grant delete --target <gateway-target> --id <grant-id>
```

已有用户可以通过 `admin user password` 设置或轮换密码，通过 `admin user disable`
停用或恢复账号；这两个命令同样不接受命令行明文密码。

- 先用 `doops list` 和 `doops targets --target <t>` 确认用户请求对应的精确
  `cluster/instance`，并确认同名用户是否已存在。已存在时不要重复创建。
- 创建普通用户必须发送 `admin:false`。CLI 通过 `POST /v1/admin/users` 创建后，会在交付账号
  或 token 前删除并复核 Gateway 可能写入的 wildcard bootstrap grant；流程失败时会停用
  新用户，不得交付半完成账号。
- 创建后立即列出该用户的 grant，删除所有 `cluster="*"`、`instance="*"` 或包含
  action `"*"` 的规则，再为每个允许 target 添加显式 actions。不要省略 `actions`，因为
  空列表会展开为默认权限集合。
- 普通机器操作使用 `targets:list,exec,ask,read,write,push,pull,info,check,clean`。
  `credential:*`、`agent:upgrade` 和 `admin` 只有在用户明确需要时才单独授权。
- 任一步在通配 grant 删除前失败，立即通过 `/v1/admin/users/disable` 停用新用户，然后
  报告未完成；不要让半完成账号保持可登录状态。
- 最终复核用户启用、密码存在、`is_admin=false`、grant 数量和精确 scope/actions，且
  wildcard grant 数为 0。使用隔离的 `DOOPS_CONFIG` 登录新用户，验证允许 target 的只读
  命令成功、至少一个未授权 sibling target 被拒绝；清理验证 workspace，并按 token ID
  撤销验证登录 token，不要覆盖本机管理员登录态。

完整命令说明见 `docs/DOOPS_USAGE_GUIDE.md`；发布见
`docs/NATURAL_LANGUAGE_DEPLOYMENT.md`；凭据完整生命周期见 `docs/CREDENTIAL_RESOURCES.md`。

## 升级引导

升级 doops CLI、gateway、agent 或 skill 前，先更新维护记录：

```text
docs/DOOPS_MAINTENANCE_LOG.md
```

升级前检查：

- 唯一配置源仍是 `~/.agent/skills/doops/config.json`。
- `auth.json` 使用 `tokens` 字段，没有写到旧的 `~/.doops/auth.json` 或 `passwords` 字段。
- `doops -session <s> targets --target <gateway-mode-target>` 只展示当前可见的精确 target。
- 旧 target、旧 alias、旧直连节点、短 token 测试配置已经淘汰。
- `.agents/`、`.doops/`、`auth.json`、`config.json`、`history.jsonl`、`sessions.json` 没有混进仓库。
- 文档里的镜像 tag 和发布策略一致；日常只更新轻镜像，不应反复拉大 base。
- gateway 控制面发布走 `scripts/deploy-gateway.sh`；不要走 `doops exec/push/write`。
- 至少对一个真实 gateway target 回归 `exec`、`push`、`pull`、`ask`、`targets` 中受影响的路径。

首次安装只走本仓库 `scripts/release-managed-agent.sh --values <reviewed-values>`，
使用 `deploy/helm/doops-agent`；values 必须明确 cluster/instance，完整命令和安装验收见
`docs/CLUSTER_INSTALL_GUIDE.md`。外部控制器可以是授权管理主机或 CI，不必是开发机；
不要把外部清单安装器当作第二条标准升级路径。
已经改用 Helm chart、且 Agent 镜像包含自升级控制器的节点用
`doops -session <s> upgrade --target <t> --image <repository>@sha256:<digest>`；
Job 复用 Agent 已挂载的 kubeconfig，不需要操作机 kubeconfig。第一次接入
Helm chart（新集群无 Agent）、现有 Helm release 的 Agent 尚无控制器，或显式修改 Agent
Helm 运行配置时，
才由操作机使用 `scripts/release-managed-agent.sh`，并显式提供操作机 kubeconfig、
精确 `cluster/instance`。已有 workload 但没有 Helm release 时，该脚本不执行 adoption，
必须先完成独立的 Helm adoption。普通业务 workload 始终使用 `doops ask --apply`，
不需要本机 kubeconfig。

普通业务发布流程和自然语言任务约束见 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`。

## 故障入口

- 审计按用户或 session 筛选超时：先检查真实 query plan；缺少索引时由平台管理员
  显式执行 `doops -session <s> admin audit optimize --target <t>`，或在全局审计页面维护
  索引。DDL 仅走受审计 Gateway 控制面，不在启动、CI 或宿主机直连时自动执行。

- `401 Unauthorized`：先看结构化错误字段。`code=gateway_auth_failed` 且
  `auth_domain=gateway` 表示 Gateway user token 缺失、无效、过期或撤销；确认 CLI
  读取 `~/.agent/skills/doops/config.json`，并确认统一 `auth.json` 中存在该 Gateway
  的 user token。裸 `401` 不能直接归因于受管凭据；标准 target 配置不得包含 token。
- `edge_enrollment_auth_failed` 且 `auth_domain=edge_enrollment` 表示边缘通道 enrollment
  token 缺失、失效或与精确 `cluster/instance` 不匹配；它不等于 Gateway user token
  失效，也不等于受管凭据错误。
- `403 Forbidden`：认证通过但权限不足，或用户被显式收窄。
- `credential_scope_invalid`：凭据引用没有匹配到完整的 cluster/instance、namespace、workload、
  image 或 Helm scope；修正显式 scope，不得放宽授权。
- `credential preflight failed`：项目清单中的 credentialRef 缺失、use 不匹配、grant
  已吊销或同名资源不唯一。先修复受管凭据和精确授权；不得读取本地 secret 文件兜底。
- `credential_verification_required`：staged 版本尚未覆盖全部 active grant 的验证；对每个
  active grant 使用完全相同的 scope 执行 `credential verify`。
- 凭据物化失败：按 Gateway 返回的错误类别定位目标或权限状态；不得重试、猜测或将 payload 放入任务。
- `ask --apply` 未出现可信 mutation 完成证据：检查 Agent 运行时是否注册 required
  `mcp_doops_helm` 工具，并核对 attestation 的运行时 session 绑定、confirm 和
  input/result digest；不得让 Read、Bash、普通 completed 事件或文本声明冒充发布成功。
- target 不可见：先跑 `doops targets --target <gateway-target>`；空结果只能说明该 route 对当前 Gateway 登录态不可见，可能是 target 离线，也可能是用户缺少精确 grant，不能直接断言集群宕机。
- `busy=true`：表示 target-wide 阻塞；`status=active` 且 `busy=false` 表示有 session 在跑，但其它 session 仍可使用。
- gateway `push/pull` 失败：确认 `/v1/git/<cluster>/<instance>/<session>.git` 能路由到在线 agent，并检查反向隧道内的 Git handler。

更完整的 API、隧道和故障细节见 `docs/DOOPS_GATEWAY_TUNNEL.md`、`docs/DOOPS_API_GUIDE.md`。
