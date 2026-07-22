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

权威源码在 doops.sh 仓库 `backend/skills/SKILL.md`。项目里的
`.agent/skills/doops/SKILL.md` 是安装副本；修改 skill 时必须先改权威源码，再通过
`scripts/install.sh` 同步。

## 核心纪律

- 优先使用 PATH 中的 `doops`；标准安装路径是 `~/.local/bin/doops`。
- 所有远端调用必须带 `-session <name>`。
- CLI 只接受 Gateway target；不存在直连 target、SSH target 或按 target 保存 token 的模式。
- 从客户端直接 SSH 到 Agent 宿主机只用于首次安装、自恢复 agent，以及发布
  `doops-gateway` 控制面；DoOps CLI 不把 SSH 主机建模为 target，也不直连 Agent。
- 标准 Agent 镜像提供出站 `ssh` 客户端，但不安装或启动 `sshd`。已在线 target 可以
  通过 `doops exec` 或 `doops exec --bg` 代理执行用户明确给出的任意 SSH 命令，不限于
  首次安装或自恢复；SSH 命令遵守与其它 exec 相同的权限、排队、超时、取消和审计规则。
  目标、参数和远端命令必须由当前任务明确给出，不得把私钥、密码或 token 内联到命令、
  prompt、日志或工作区。
- `doops-gateway` 是独立发布包，必须用 `scripts/deploy-gateway.sh --host <gateway-host>` 从 SSH 宿主机上下文发布。
- gateway 控制面不登记为 doops target，不通过任何 doops target 发布 gateway 本体。
- 不要用 base64、tar 或手写分片传输绕过 `push`。`doops_workspace_*` 是低层兼容 JSON-RPC 工具，不是 CLI、发布脚本和 skill 的标准路径；如果看到 512KB 分块日志，说明调用方绕过了当前 `doops push/pull`。应修复 Git HTTP 反向隧道、目标 agent 版本或发布脚本。
- 普通业务 workload 的标准发布入口是 `doops ask --apply`。自然语言任务必须直接给出 target、服务范围、明确镜像版本、
  变更原因和验收条件；不得从文件路径、环境名、业务名或历史声明推断这些值。
- 普通 `ask` 运行在 restricted 权限，只允许内置只读工具和 `mcp_doops_k8s_read`；
  `--apply` 才进入原生 build 模式，并且只允许 `mcp_doops_helm` 执行发布 mutation。
- 更新现有 Helm release 时，只要自然语言任务已明确 release、namespace、chart 和
  不可变镜像，Agent 必须直接调用一次 `mcp_doops_helm upgrade-install`。`value_files`
  可以为空，`set_strings` 只传 `image.repository` 和 `image.digest`；工具自动保留
  当前 values 并先 dry-run。不得要求用户补充候选 values，也不得用缺少
  `confirm=true` 的调用试探。
- 当项目以完整 Helm values 管理业务镜像时，chart 默认 values 与显式 `value_files`
  必须收敛出合法 `image.repository` 和 `image.tag=release-YYYYMMDD`。该模式禁止
  `image.digest` 和任何 `set_strings`，也不得与 digest 覆盖模式混用；缺少明确 release
  tag 时工具必须拒绝 mutation。
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
- `doops-agent` 自身升级是例外：当前 Agent 不得修改承载自身的 workload，必须由真实存在的
  外部 CI/GitOps controller 执行；禁止创建临时 Agent、临时 target 或 release-runner。
- 长构建可以使用 `doops -session <s> exec --bg --target <t> --cmd '<cmd>'`。自然语言
  发布中的 Helm 收敛必须使用 `mcp_doops_helm`；运维人员显式给出的前台 `exec` Helm
  `upgrade/install/rollback/uninstall` 仍是合法的确定性操作，并保持 Gateway 串行化
  和完整退出审计。Agent 必须拒绝后台 doops-agent Helm mutation。不要在普通 `exec`
  命令中使用 `&`、`nohup` 或 `setsid`，因为调用结束时 agent 会终止该 shell 的子进程。
- 不要混用 token：
  - gateway CLI 操作用 gateway user token。
  - agent 注册 gateway 不使用 token，只声明 cluster/instance。
- 不要在日志、终端输出、文档、提交记录里暴露完整 token。
- 如果用户是在发账号、发 token、授权、查审计、清理审计、查看/取消 gateway active operations，要按 gateway 管理动作处理，不要混成普通 target 运维。
- 遇到 `user operation limit exceeded` 时，先用 `doops admin operations list --target <gateway-target>` 查占槽操作；确认 stale 后再用 `doops admin operations cancel --target <gateway-target> --id <operation-id>` 取消。升级后的 agent 会按进程组终止 shell 类命令；`ask` 需要额外核查 doagent session 是否仍在工作。
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

doops login --target jm
```

Agent 的 K8s/容器部署和 Gateway 注册见 `docs/CLUSTER_INSTALL_GUIDE.md`。CLI 只有在 Gateway heartbeat 可见后才能操作该节点。

## 使用引导

先确认本地配置和 gateway 在线状态：

```bash
doops list
doops targets --target unicom
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
| 工作区清理 | `doops -session <s> clean --target <t>` |
| 预演 agent 升级 | `doops -session <s> upgrade ... --dry-run` |
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
- `doops targets --target <gateway-mode-target>` 只展示当前有效 gateway targets。
- 旧 target、旧 alias、旧直连节点、短 token 测试配置已经淘汰。
- `.agents/`、`.doops/`、`auth.json`、`config.json`、`history.jsonl`、`sessions.json` 没有混进仓库。
- 文档里的镜像 tag 和发布策略一致；日常只更新轻镜像，不应反复拉大 base。
- gateway 控制面发布走 `scripts/deploy-gateway.sh`；不要走 `doops exec/push/write`。
- 至少对一个真实 gateway target 回归 `exec`、`push`、`pull`、`ask`、`targets` 中受影响的路径。

普通业务发布流程和自然语言任务约束见 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`。

## 故障入口

- `401 Unauthorized`：认证失败。先确认 CLI 读的是 `~/.agent/skills/doops/config.json`，并确认统一 `auth.json` 中存在该 Gateway 的 user token；标准 target 配置不得包含 token。
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
- target 不在线：先跑 `doops targets --target <gateway-target>`。
- `busy=true`：表示 target-wide 阻塞；`status=active` 且 `busy=false` 表示有 session 在跑，但其它 session 仍可使用。
- gateway `push/pull` 失败：确认 `/v1/git/<cluster>/<instance>/<session>.git` 能路由到在线 agent，并检查反向隧道内的 Git handler。

更完整的 API、隧道和故障细节见 `docs/DOOPS_GATEWAY_TUNNEL.md`、`docs/DOOPS_API_GUIDE.md`。
