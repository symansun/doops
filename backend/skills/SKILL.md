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
| Harbor 镜像复制与 CNB 同步 | `docs/HARBOR_REPLICATION_SPEC.md` |
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

- 只用 `~/.local/bin/doops`。`-session <name>` 是操作上下文和并发归属，任意合法名字即可，
  不需要事先创建，也不要求 Git worktree 或先 `push`；省略时 CLI 自动生成并打印到 stderr，
  要延续同一工作区就复用那个名字。不要因为"没有 session 名"而停下来问。
- CLI 只接受 Gateway target；没有直连 target、SSH target 或按 target 保存的 token。
  Gateway 登录态按 Gateway URL 共享，target 配置只存路由。Agent 注册不用 token。
- 凭据 payload 只走 stdin，不进入命令参数、prompt、日志、文档、提交或工作区。
  运行时只传 `--credential <resource-name>`（DoSql 自动绑定，见下文），只要求该 target 的 grant。
  不要用 `exec` 输出密码再让用户粘贴；已有目标 Secret 用 `credential configure --from-secret` 导入。
- 每次调用都等待 RPC 实际终态。取消 ACK、超时或断连缺少终态时报告 `outcome_unknown`，
  不自动重放 mutation，不宣称成功。
- `ask` 的文字回答是模型写的摘要，不是状态证据，可能写错 revision 或答非所问。发布前后的
  Helm release、Deployment、Pod 状态用 `doops exec`（`helm history`、`kubectl get -o json`）
  或 `doops k8s` 只读入口回读，以回读结果为准。
- 业务 workload 只用 `doops ask --apply` 发布。任务写明 target、服务范围、镜像版本或其
  授权来源、原因、验收条件；镜像版本不靠猜。发布者调用前自行核对 CI commit 与 digest。
- 业务 workload 的发布收敛只走 `ask --apply` 内的 `mcp_doops_helm`，不用 shell kubectl、
  `kubectl set image` 或文本声明冒充成功。运维类变更（重启、扩缩、cordon、删 Pod）用
  `doops k8s ... --confirm`，它不是发布入口。
- 进入 Kubernetes Agent 所在节点只走 `doops exec` 里的 `nsenter`。`hostAccess.enabled`
  默认打开；显式 `false` 才关闭。这是 Gateway 的普通 `exec`：目标 grant 决定能否执行，
  审计记下用户、target、session 和完整命令。不要用 `kubectl exec`、`docker exec` 或直连
  `42222` 绕过。`hostNetwork` 保持关闭，`/host` 只读。细节见 `docs/PHYSICAL_HOST_ACCESS.md`。
- 嵌套 SSH 只用 `doops ssh` 的结构化入口，不拼进 `exec --cmd`。
- 不用 base64、tar 或分片绕过 `push/pull`；大文件和二进制用 `pull`。
- `read` / `write` / `push` / `pull` 默认走 COS。桶和密钥以 Gateway 配置为准，不使用 `--credential`。`--storage gateway` 才走原来的本地中转；COS 失败不回退。
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
| 本地 target / 在线检查 | `doops list`；`doops -session <s> targets --target <t>`；`doops sync --target <t>` |
| 确定性命令 | `doops -session <s> exec --target <t> --cmd '<cmd>'` |
| 进入 K8s Agent 所在节点 | `doops -session <s> exec --target <t> --cmd 'nsenter -t 1 -m -u -i -n -- hostname'`（`hostAccess.enabled` 默认 true；Gateway 按 `exec` 授权并审计） |
| 长任务 | `doops -session <s> exec --bg --target <t> --cmd '<cmd>'` |
| 嵌套 SSH | `doops -session <s> ssh --target <t> --host <user@host> -- <cmd> [args...]` |
| 诊断分析 | `doops -session <s> ask --target <t> --msg '<task>'` |
| 业务发布 | `doops -session <s> ask --target <t> --apply [--credential <name>...] --msg '<facts>'` |
| DoSql 自然语言 | `doops -session <s> sql --target <t> --mode query\|plan\|apply\|discover [--database <name>] --request '<req>'` |
| DoSql 原样 SQL | `doops -session <s> sql --target <t> --mode query\|apply (--sql '<sql>'\|--file <f>) [--verify '<select>'] [--dry-run]` |
| DoSql 跨库复制表 | `doops -session <s> sql --target <t> --mode apply --copy-from <源库> --database <目标库> --tables a,b [--dry-run]`（同引擎、目标表须已存在；细节见 `docs/DOOPS_USAGE_GUIDE.md`） |
| K8s 只读 | `doops -session <s> k8s --target <t> get\|describe\|logs\|top\|events\|rollout status ... [-n <ns>\|-A] [-l <sel>] [-o jsonpath=...]` |
| Helm 只读 | `doops -session <s> k8s --target <t> helm list\|history\|status\|values <release> -n <ns>`（values 只给 key） |
| K8s 运维变更 | `doops -session <s> k8s --target <t> rollout restart\|scale\|node cordon\|delete pod ... --confirm` |
| DoWeb | `doops -session <s> web --target <t> --mode check\|plan\|apply --request '<req>'` |
| 推送 / 拉取工作区 | `doops -session <s> push --target <t> --src <dir>`；`pull --dest <dir>`。默认走 Gateway 配置的 COS；`--storage gateway` 才走 Git 中转 |
| OCI 镜像 | `image build`、`image copy`、`registry sync`（仓库认证由 Gateway 下发到目标） |
| 一次配置凭据 | `doops -session <s> credential configure --target <t> --id <id> ...` |
| 开发凭据预检 | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` |
| 用户与授权 | `doops admin user create\|list`、`admin grant list\|add\|delete` |
| 网站代理路由 | `doops admin proxy list\|set\|delete --target <t> ...`（`set --id` 只改传入字段） |
| token | `doops admin token create\|list\|revoke` |
| Helm Agent 升级 / 回滚 | `doops -session <s> upgrade --target <t> --image <ref>`（`--dry-run` 只预览）；`upgrade --target <t> --rollback` |
| CI 非交互登录 | `printf '%s' "$TOKEN" \| doops login --target <t> --token-stdin` |

DoSql 必须从 `doops sql` 进入；普通 `ask` 不保证注入 DoSql 工具。Gateway 默认绑定当前 target
上全部 active `use=database` 凭据，有多个数据库凭据时用 `--database <name>` 选库；同一库绑了
应用账号和迁移账号时，`--sql/--file` 加 `--credential <name>` 选用其中一个（如 DDL 用迁移账号）。MongoDB 的 `--sql/--file` 是 JSON 操作文档
（读写、事务、索引、explain、currentOp、findAndModify、重命名等），不含用户管理。完整参数和 `dosql_*` 错误类别见
`docs/DOOPS_USAGE_GUIDE.md`，凭据分步命令见 `docs/CREDENTIAL_RESOURCES.md`。
没有通用 `update` 命令；target、用户、grant、token、凭据、job 等元信息怎么改（原地改还是替换）
见 `docs/DOOPS_USAGE_GUIDE.md` 的 “Updating Metadata”。

## 场景链路

场景链路由 CLI 自己给出，和当前安装的二进制一致：`doops guide` 列出场景，
`doops guide <onboard|diagnose|release|credential|database|upgrade|user|files>` 打印按顺序执行的命令。
命令失败时输出里的 `next:` 行就是下一步要执行的命令，照着执行，不必回头翻文档。

## 安装与升级

- 安装 CLI/Skill：`bash scripts/install.sh --version <tag> --project <absolute-path>`。
  它不部署 Agent；Agent 接入见 `docs/CLUSTER_INSTALL_GUIDE.md`。
- 配置唯一来源是 `~/.agent/skills/doops/config.json`；`auth.json` 按 Gateway URL 存 token。
- 创建普通用户用 `admin user create`，交付前确认 grant 只含预期 target，并用隔离的
  `DOOPS_CONFIG` 验证允许与拒绝路径，流程见 `docs/DOOPS_USAGE_GUIDE.md`。
- Agent / Gateway 升级的参数、验收和回滚见 `docs/BUILD_DEPLOY_SOP.md`。

## 故障入口

- `401` 且 `code=gateway_auth_failed`：Gateway user token 缺失、过期或撤销；
  `edge_enrollment_auth_failed` 是边缘通道 enrollment token 问题。裸 `401` 不归因于受管凭据。
- `403`：已认证但权限不足。
- `credential_*` 错误与 `credential preflight failed`：见 `docs/CREDENTIAL_RESOURCES.md`。
- 报错都是 `错误码: 原因`，原因就是要修的东西，不要只看错误码去猜：
  `helm_preflight_failed|helm_manifest_invalid|helm_no_workload|helm_image_mismatch|helm_mutation_failed|helm_verification_failed`
  （含义见 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`）；`doagent_apply_tool_failed: <工具>: <工具错误>` 是工具自己的失败，
  模型文字说"成功"不算数；`execution_outcome_unknown: <原因>` 不重放 mutation，按原因去 target 上核对；
  `blocked_command(delete_root_filesystem)` 是 exec 唯一拦截的命令。
- `ask --apply` 没有可信 mutation 证据：检查 Agent 是否注册 `mcp_doops_helm` 及 attestation，
  见 `docs/NATURAL_LANGUAGE_DEPLOYMENT.md`。
- target 不可见：空结果可能是离线，也可能是缺少精确 grant，不能直接断言集群宕机。
- `busy=true` 只表示 Agent 自升级占用整个 target；普通操作按 session/资源互斥，语义见 `docs/DOOPS_GATEWAY_TUNNEL.md`。
- `push/pull` 失败：确认 `/v1/git/<cluster>/<instance>/<session>.git` 能路由到在线 Agent。
