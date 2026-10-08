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
- 执行生命周期由 Gateway 统一管理。只有 `exec --bg` 要求 Agent 声明完整的
  `executionLifecycle` 能力，CLI、公网 Gateway 和内部调度入口都在后台任务派发前校验；
  普通前台 `exec`、`ssh`、`ask` 和 `ask --apply` 不受该能力门禁限制。
  `exec`、`exec --bg`、`ssh`、`ask` 和 `ask --apply` 都必须等待对应 RPC 的实际终态；
  取消 ACK 不是终态，取消、超时或断连且缺少终态证据时如实写入 `outcome_unknown` 并报告失败，
  不自动重放 mutation；历史未知审计不参与新任务准入。结构化 `ssh` 仍只要求
  `structuredSSH` 参数协议，不得把未知结果宣称为成功或静默降级。
- `capabilities --require` 接受 `structuredSSH` 和 `executionLifecycle`；其它名称
  在联网前直接报参数错误。后台能力覆盖实际终态、受控任务寿命及取消，不承诺任务跨断连存活。
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
- 从客户端直接 SSH 到 Agent 宿主机用于已授权的首次安装、自恢复、正式外部 DockerHost
  发布和 `doops-gateway` 控制面发布；DoOps CLI 不把 SSH 主机建模为 target，也不直连 Agent。
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
  取消、断连或缺少结构化终态仍报告未知结果；不得根据命令文本猜测只读，也不得
  把本地 SSH 进程退出当成远端工作已停止。
- `doops-gateway` 是独立发布包，必须从 CNB 不可变镜像提取，并用
  `scripts/deploy-gateway.sh --host <gateway-host> --source-commit <main-commit>`
  从 SSH 宿主机上下文发布；旧的本地二进制会被拒绝。
- gateway 控制面不登记为 doops target，不通过任何 doops target 发布 gateway 本体。
- 不要用 base64、tar 或手写分片传输绕过 `push`。`doops_workspace_*` 是低层兼容 JSON-RPC 工具，不是 CLI、发布脚本和 skill 的标准路径；如果看到 512KB 分块日志，说明调用方绕过了当前 `doops push/pull`。应修复 Git HTTP 反向隧道、目标 agent 版本或发布脚本。
- 普通业务 workload 的标准发布入口是 `doops ask --apply`。自然语言任务必须明确 target、服务范围、
  变更原因、允许使用的资产和验收条件。业务镜像版本必须来自任务或其明确授权的仓库、
  Chart、values、现有 release 的只读证据；不得猜测，也不得要求已明确资产中的事实再粘贴到 prompt。
  不把业务镜像键、版本格式、CORS Origin、仓库名或项目目录作为全局前置闸门。
- 普通 `ask` 运行在 restricted 权限，只允许内置只读工具和 `mcp_doops_k8s_read`；
  `--apply` 才进入原生 build 模式。Kubernetes workload mutation 只允许
  `mcp_doops_helm`；目标本地 Nginx 网站变更使用 `mcp_doops_web`。两者均要求
  本轮 runtime attestation，不能用 Shell 或模型成功文本替代执行证据。
- 发布事实齐备后，Agent 调用 `mcp_doops_helm upgrade-install`，显式提供 release、
  namespace、chart 和 `confirm=true`。`value_files` 可选，覆盖遵循 Chart schema；普通升级
  统一 `--reset-values`，不隐式继承历史 values。工具自动先执行同输入的 server dry-run，
  不得询问是否启用 dry-run，不得用缺少 `confirm=true` 的调用试探。
  保留受管 Helm 认证、精确 scope、包 digest 钉住和最终 Deployment generation/spec/status
  校验，不以 shell、revision 或 Pod Ready 宣称发布成功。资产路径、可选 `expected_image`
  和完整迁移说明见 [自然语言发布契约](docs/NATURAL_LANGUAGE_DEPLOYMENT.md)；
  remote 当前支持 HTTPS `.tgz`，不支持 `oci://`。
- CI 的精确 source commit、镜像 digest 和构建成功证据必须由发布者在调用前核对。
  `ask --apply`、`--image`、`--version` 和 Helm 工具只校验允许的输入形态、解析结果和
  运行时证据，不自动核验镜像是否来自指定 CI。
  发布镜像只由 CI 构建；Agent 内存在 BuildKit 不代表允许在业务 target 上重新构建，
  也不能把工作区同步或 SCP 源码传输作为普通发布的前置步骤。
- 需要凭据时使用可重复的 `--credential <resource-name>`。该参数只传递资源名；Gateway
  将它绑定到当前用户、target 和 session，并按对应 credential use 授权和物化。对 database
  use，Gateway 只管理用户/平台归属、cluster/instance、物化 namespace 和凭据可用性；数据库
  asset、environment、engine、database name 与 SQL 变更授权由 DoSql/目标 Agent 负责。公开 prompt、doagent 上下文和 metadata
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
- 禁止当前 Agent 自行替换自己。具备控制器的标准 Helm Agent 用 `doops upgrade`，
  保留集群内 Job、匹配 Helm release 和共享 kubeconfig 的前置条件；其 `--dry-run`
  仍只预览参数。旧 Helm Agent、K8S 首次接入或显式 Helm 配置变更用外部
  `scripts/release-managed-agent.sh`，不自动 adoption。
- 自升级与回滚 Job 固定到当前 Agent 节点并保留 runtime class；新镜像必须与独立
  controller Pod 的不可变 spec 引用及同运行时 imageID 同时匹配。CRI imageID 不能与
  registry manifest digest 直接等同。自动回滚使用升级前捕获的运行身份；手动回滚
  controller 使用已记录成功验收证据的具体历史 revision 镜像。证据保存在自身 namespace 的
  `doops-upgrade-verified-<release>` ConfigMap；不以 Helm superseded 状态推断成功。
  revision/镜像/namespace 变化、证据缺失或写入失败时明确失败。
- 已有独立 Docker Agent 用正式外部 `scripts/release-host-agent.sh`，保持原运行方式，
  不要求先迁 K8S，也不是 `upgrade` 失败后的自动 fallback。必须显式指定 SSH 端口，
  严格校验主机密钥；可引用已认证私有 SSH socket，不接收密码/token。真实只读预检、
  宿主锁、配置保留、旧容器保留和外部回滚见
  [主 SOP](docs/BUILD_DEPLOY_SOP.md#独立-dockerhost-发布)。发布须验证实际容器/镜像/版本、
  Gateway 精确身份与 `release_commit`、structuredSSH 协议/argv 派发/预期失败终态、
  foreground/bg 的真实 RPC 流与 Root RequestID；不宣称 SSH 目的地主机认证成功。
  替换调用前即启用外部回滚保护，SSH 返回状态丢失仍请求回滚；部分回滚失败允许
  按持久阶段继续，不放宽旧/新容器所有权检查。脚本自动执行功能探针，失败不重放。
  外部 bootstrap 密码只走 stdin，不给 Agent；不能只看 heartbeat 或把 SSH 改写成普通 `exec`。
- 禁止临时 Agent、临时 target、release-runner、`kubectl set image`，以及通过旧 Agent
  `exec`、Docker restart 或进程内 Helm mutation 升级其自身。
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
  方法按精确代理 route 互斥，不独占 target，详见 `docs/DOOPS_GATEWAY_TUNNEL.md`。
- 不要在日志、终端输出、文档、提交记录里暴露完整 token。
- 如果用户是在发账号、发 token、授权、查审计、清理审计、查看/取消 gateway active operations，要按 gateway 管理动作处理，不要混成普通 target 运维。
- 同一 target 的结构化 `push/write/clean/read/pull` 按 workspace 互斥，不同 workspace
  可并行；普通 Shell/SSH/bg 与 `ask --apply` 使用 `target + session` mutation lease，
  同一 session 串行，不同 session 可并行。session 只表示并发归属，不是 OS 或远端资源
  隔离。结构化 K8s 查询、任务状态和节点信息可在发布期间查询；Agent 自升级仍独占 target。
  取消和审计收尾遵守同一 session/资源作用域，不阻挡无关 session；普通 mutation 缺少
  有效 session 时拒绝，不退回 target 独占。不同 session 不得同时修改同一 release 或数据库。
  当前没有跨 target 的 release/
  数据库统一锁，也没有执行器 fencing，不能宣称支持安全强制抢占发布锁。
- 遇到并发限额或资源冲突，先用 `doops -session <s> admin operations list --target <t>`
  核对 `state/resource/blocked_by`。Gateway 会按 target 的 `cluster/instance` 过滤管理操作，
  等待锁不占用户执行槽；不能因本会话着急就取消其它合法发布。获得明确取消授权后才用
  `admin operations cancel --target <t> --id <operation-id>`。
  `202 cancel_requested` 不是停止证据。缺少终态，或 Shell/SSH/bg、apply、K8s 写操作
  被中断而无法证明外部变更已停止时，持久化 `outcome_unknown` 并报告失败，不自动重放。
  可信 Helm 工具的明确失败是已结束的失败，不写入 `outcome_unknown`；一次成功工具调用
  也不能掩盖同轮其它工具的明确失败。历史未知审计不阻断新任务，也不改为 success 或删除；
  活跃任务仍按资源锁串行。`unlock` 只重连空闲 target，不强制释放活动 lease 或重放。
  受管凭据按资源 ID 互斥；不同凭据可并行，同一凭据的发布、授权、轮换和吊销仍串行。
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

统一工作台还承载 DoSql、DoWeb、DoDNS、DoSecurity 等 Agent skill。用户只使用这一套
Gateway 登录态和 `doops` CLI；skill 只负责自己的业务规则、执行证据和结果解释，不再
各自实现登录、target 选择、Gateway token 解析或 target grant。DoSql 的签名审批属于
数据库变更证据，不替代 Gateway 的用户/target/action 授权。边界和授权矩阵见
`docs/UNIFIED_OPERATIONS_WORKBENCH.md`。

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
| 构建 OCI 镜像 | `doops -session <s> image build --target <t> --context <remote-dir> --output oci --output-path <remote-file>` |
| 复制 OCI 镜像 | `doops -session <s> image copy --target <t> --source <ref> --destination <ref>` |
| 同步 OCI 镜像 | `doops -session <s> registry sync --target <t> --source-registry <src> --target-registry <dst> --tag <release> --source-authfile <remote-file> --target-authfile <remote-file> --image <name>` |
| AI 自主分析 | `doops -session <s> ask --target <t> --msg '<task>'` |
| 发布任务 | `doops -session <s> ask --target <t> --apply --credential <resource-name> --msg '<target、范围、明确镜像版本、原因、验收>'` |
| DoSql 只读查询/巡检 | `doops -session <s> sql --target <t> --mode query --credential <database-resource> --request '<数据库目标和问题>'` |
| DoSql 变更计划 | `doops -session <s> sql --target <t> --mode plan --request '<数据库变更>'` |
| DoSql 受控变更 | `doops -session <s> sql --target <t> --mode apply --credential <database-resource> --request '<已批准的变更请求>'` |
| DoSql 发现数据库资产 | `doops -session <s> sql --target <t> --mode discover --request '<发现数据库资产>'` | 通过 `mcp_dosql_read` 扫描当前 target 的非 Secret 元数据；授权代码资产另用 `--root/--resources-file/--project/--environment/--dosql-script` 持久化 metadata-only 清单。 |
| DoWeb 只读巡检 | `doops -session <s> web --target <t> --mode check --request '<站点、Nginx、TLS 或健康检查需求>'` |
| DoWeb 发布计划 | `doops -session <s> web --target <t> --mode plan --request '<站点发布需求>'` |
| DoWeb 受控发布 | `doops -session <s> web --target <t> --mode apply --credential <web-resource> --request '<站点、dist、域名、代理、验收>'` |

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

DoSql 必须从 `doops sql` 入口进入。普通 `doops ask --msg '使用 DoSql'` 是通用 Agent
对话，不保证注入 `mcp_dosql_read/execute`，也不能用来判断目标是否支持 DoSql。
| 推送本地目录 | `doops -session <s> push --target <t> --src <dir>` |
| 拉取远端工作区 | `doops -session <s> pull --target <t> --dest <dir>` |
| 小文本读写 | `doops read/write ...` |
| 工作区清理 | `doops -session <s> clean --target <t> --workspace <s>` |
| 升级标准 Helm Agent | `doops -session <s> upgrade --target <t> --image <repository>@sha256:<digest>` |
| 预览 Helm Agent 升级参数 | `doops -session <s> upgrade ... --dry-run` |
| 发布已有独立 Docker Agent | 外部 `scripts/release-host-agent.sh`；参数与只读预检见主 SOP |
| 创建 Gateway 用户和 API key | `doops admin user create --target <t> --user <u> (--password-stdin\|--generate-password)` |
| 查看 Gateway 用户 | `doops admin user list --target <t>` |
| 管理 target grant | `doops admin grant <list\|add\|delete> ...` |
| 管理员签发 token | `doops admin token create --target <t> --user <u> --name <label> --expires 720h` |
| 管理员查看 token | `doops admin token list --target <t>` |
| 管理员撤销 token | `doops admin token revoke --target <t> --id <token-id>` |

`registry sync` 是 DoOps 自带的通用能力，实际通过目标 Agent 的受审计 exec 执行
`skopeo copy/inspect`，完成 OCI 镜像复制和源/目标 manifest digest 校验；它不依赖
AMP 专用脚本、Job 或外部镜像。标准 DoOps Agent 镜像必须同时提供 `buildctl`（构建）
和 `skopeo`（复制/检查）。目标缺少 `skopeo` 时直接报告工具缺失，不在线安装、不静默
切换到其它工具。

`image build` 是通用 BuildKit 构建入口，默认输出 OCI archive，也支持明确指定
`--output image --image <ref> --push` 推送到 Registry；`image copy` 支持 Skopeo 的
标准 transport。两者都通过受审计的 `doops_shell` 执行，不绑定 Docker、nerdctl、AMP
或某个项目脚本。私有 Registry 凭据只能引用目标上的受控 authfile，禁止写入命令参数。

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

先按现网运行方式选择入口：Helm Agent 与已有独立 Docker Agent 的升级互不自动切换，
外部控制器可以是授权管理主机或 CI。入口、参数、验收和回滚见
[构建部署 SOP](docs/BUILD_DEPLOY_SOP.md)；K8S 首次安装与接入验收见
[集群接入指南](docs/CLUSTER_INSTALL_GUIDE.md)。DockerHost 不依赖旧 Agent 的
`doops_agent_upgrade` 或 `managedHelm`，不改变凭据体系/API。普通业务 workload 仍使用
`doops ask --apply`，不需要本机 kubeconfig。

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
- `credential_scope_invalid`：凭据引用没有匹配到对应 use 所需的 Gateway scope；database use
  只要求 target、物化 namespace 和 credential usability，数据库资产与 SQL 规则由 DoSql/Agent
  校验，不要把业务数据库字段重新塞回 Gateway grant。
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
