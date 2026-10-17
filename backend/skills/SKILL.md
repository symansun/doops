---
name: doops-server
description: |
  用 doops 通过 doops-gateway 连接和操作主动注册的 doops-agent 节点。
---

## 定位

本 skill 只放红线、命令选择和文档导航。规则细节只在对应主题文档里展开，按需读取：

| 主题 | 文档 |
| :--- | :--- |
| 安装、接入、Agent 入口选择 | `docs/CLUSTER_INSTALL_GUIDE.md` |
| 日常命令、用户与 token、凭据命令 | `docs/DOOPS_USAGE_GUIDE.md` |
| 业务发布（`ask --apply`、Helm 资产） | `docs/NATURAL_LANGUAGE_DEPLOYMENT.md` |
| 凭据生命周期与错误类别 | `docs/CREDENTIAL_RESOURCES.md` |
| Agent / Gateway 发布与回滚 | `docs/BUILD_DEPLOY_SOP.md` |
| 执行生命周期、并发、取消、代理 | `docs/DOOPS_GATEWAY_TUNNEL.md` |
| HTTP / WebSocket API | `docs/DOOPS_API_GUIDE.md` |
| 模型与 ask 配置 | `docs/MODEL_STANDARDS.md` |
| 物理机 / 宿主机硬件检查 | `docs/PHYSICAL_HOST_ACCESS.md` |
| Web 控制台 | `docs/DOOPS_WEBUI.md` |
| DoSql / DoWeb 等统一工作台 skill | `docs/UNIFIED_OPERATIONS_WORKBENCH.md` |

权威源码是 doops.sh 仓库的 `backend/skills/SKILL.md`；项目里的 `.agent/skills/doops/` 是
安装副本，只能通过 `scripts/install.sh` 从 Release CLI 内嵌 commit 同步，不要直接改副本。

## 红线

- 只用 `~/.local/bin/doops`。所有远端调用带 `-session <name>`；session 是操作上下文和
  并发归属，不要求 Git worktree 或先 `push`。
- CLI 只接受 Gateway target；没有直连 target、SSH target 或按 target 保存的 token。
  Gateway 登录态按 Gateway URL 共享，target 配置只存路由。Agent 注册不用 token。
- 凭据 payload 只走 stdin，不进入命令参数、prompt、日志、文档、提交或工作区。
  运行时只传 `--credential <resource-name>`（DoSql 自动绑定，见下文），权限继承当前 target 的 `ask`/`exec`。
  不要用 `exec` 输出密码再让用户粘贴；已有目标 Secret 用 `credential configure --from-secret` 导入。
- 每次调用都等待 RPC 实际终态。取消 ACK、超时或断连缺少终态时报告 `outcome_unknown`，
  不自动重放 mutation，不宣称成功。
- 业务 workload 只用 `doops ask --apply` 发布。任务写明 target、服务范围、镜像版本或其
  授权来源、原因、验收条件；镜像版本不靠猜。发布者调用前自行核对 CI commit 与 digest。
- Kubernetes mutation 只走 `mcp_doops_helm`，不用 shell kubectl、`kubectl set image` 或文本声明冒充成功。
- 嵌套 SSH 只用 `doops ssh` 的结构化入口，不拼进 `exec --cmd`。
- 不用 base64、tar 或分片绕过 `push/pull`；大文件和二进制用 `pull`。
- 长任务用 `exec --bg`，不在 `exec` 里用 `&`、`nohup`、`setsid`。
- 发布镜像只由 CI 构建，不在业务 target 或本机构建。
- Agent 不替换自己。升级入口按运行方式选择，互不自动 fallback：
  已有控制器的 Helm Agent 用 `doops upgrade`；K8S 首装、旧 Helm Agent、显式 Helm 配置
  变更用 `scripts/release-managed-agent.sh`；独立 Docker Agent 用 `scripts/release-host-agent.sh`；
  空服务器用 `scripts/bootstrap-host-agent.sh`。
- Gateway 本体只用 `scripts/deploy-gateway.sh --source-commit <commit>` 从宿主机 SSH 发布，
  不登记为 target，不通过 target 发布。
- 不在任何输出中暴露完整 token；签发 token 用 `doops admin token create`，不 SSH 改 Gateway DB。
- 发布前核对 `doops version` 与 target 的 `release_commit` 一致；`cli-contract/v3` 和
  `structuredSSH` 都不证明 Agent 版本或自升级能力。

## 命令选择

| 需求 | 命令 |
| :--- | :--- |
| 本地 target / 在线检查 | `doops list`；`doops -session <s> targets --target <t>` |
| 确定性命令 | `doops -session <s> exec --target <t> --cmd '<cmd>'` |
| 长任务 | `doops -session <s> exec --bg --target <t> --cmd '<cmd>'` |
| 嵌套 SSH | `doops -session <s> ssh --target <t> --host <user@host> -- <cmd> [args...]` |
| 诊断分析 | `doops -session <s> ask --target <t> --msg '<task>'` |
| 业务发布 | `doops -session <s> ask --target <t> --apply [--credential <name>...] --msg '<facts>'` |
| DoSql | `doops -session <s> sql --target <t> --mode query\|plan\|apply\|discover [--database <name>] --request '<req>'` |
| DoWeb | `doops -session <s> web --target <t> --mode check\|plan\|apply --request '<req>'` |
| 推送 / 拉取工作区 | `doops -session <s> push --target <t> --src <dir>`；`pull --dest <dir>` |
| OCI 镜像 | `image build`、`image copy`、`registry sync`（authfile 只引用目标文件） |
| 一次配置凭据 | `doops -session <s> credential configure --target <t> --id <id> ...` |
| 开发凭据预检 | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` |
| 用户与授权 | `doops admin user create\|list`、`admin grant list\|add\|delete` |
| token | `doops admin token create\|list\|revoke` |
| 活动操作 | `doops -session <s> admin operations list --target <t>` |
| Helm Agent 升级 | `doops -session <s> upgrade --target <t> --image <ref>`（`--dry-run` 只预览） |

DoSql 必须从 `doops sql` 进入；普通 `ask` 不保证注入 DoSql 工具。DoSql 只有自动凭据模式：
Gateway 绑定当前 target 上全部 active `use=database` 凭据，`doops sql` 不接受 `--credential`；
有多个数据库凭据时用 `--database <name>` 选库。完整参数和 `dosql_*` 错误类别见
`docs/DOOPS_USAGE_GUIDE.md`，凭据分步命令见 `docs/CREDENTIAL_RESOURCES.md`。

## 安装与升级

- 安装 CLI/Skill：`bash scripts/install.sh --version <tag> --project <absolute-path>`。
  它不部署 Agent；Agent 接入见 `docs/CLUSTER_INSTALL_GUIDE.md`。
- 配置唯一来源是 `~/.agent/skills/doops/config.json`；`auth.json` 按 Gateway URL 存 token。
- 创建普通用户用 `admin user create`，交付前确认 wildcard grant 为 0 并用隔离的
  `DOOPS_CONFIG` 验证允许与拒绝路径，流程见 `docs/DOOPS_USAGE_GUIDE.md`。
- Agent / Gateway 升级的参数、验收和回滚见 `docs/BUILD_DEPLOY_SOP.md`。

## 故障入口

- `401` 且 `code=gateway_auth_failed`：Gateway user token 缺失、过期或撤销；
  `edge_enrollment_auth_failed` 是边缘通道 enrollment token 问题。裸 `401` 不归因于受管凭据。
- `403`：已认证但权限不足。
- `credential_*` 错误与 `credential preflight failed`：见 `docs/CREDENTIAL_RESOURCES.md`。
- `ask --apply` 没有可信 mutation 证据：检查 Agent 是否注册 `mcp_doops_helm` 及 attestation，
  见 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`。
- target 不可见：空结果可能是离线，也可能是缺少精确 grant，不能直接断言集群宕机。
- 并发冲突或 `busy=true`：先 `admin operations list` 核对 `state/resource/blocked_by`；
  未获明确授权不取消其它发布。语义见 `docs/DOOPS_GATEWAY_TUNNEL.md`。
- `push/pull` 失败：确认 `/v1/git/<cluster>/<instance>/<session>.git` 能路由到在线 Agent。
- 审计查询超时：平台管理员显式执行 `doops -session <s> admin audit optimize --target <t>`。
