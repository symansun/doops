# DoOps

<p align="center">
  <img src="docs/images/slogan.svg" alt="不用 SSH，也能运维任何一台机器。" width="760">
</p>

> 本项目以 [MIT License](LICENSE) 开源。

**核心贡献者：孙勇、高家祺、宋晨宇。** 具体署名顺序见[贡献者](#contributors)。

**还在为 SSH 端口、跳板机、密钥分发、NAT 和内网隔离买单？** `doops` 把这些统统拿掉：

- **一条命令直达内网机器**：agent 主动向公网 gateway 建立长连接，无需公网 IP、VPN、跳板机或堡垒机
- **零 SSH 资产负担**：不用再维护 SSH 用户、端口、密码和密钥清单，断网重连、内网隔离都不再是障碍
- **为 Agent 和自动化而生**：CLI、控制台与多领域 skill 共用同一条可审计执行链路，AI 可以安全地排障、巡检、部署，而不是靠人肉复制粘贴
- **安全默认收口**：统一认证、按 cluster/instance 授权、全链路审计，高危操作默认拒绝

排障、巡检、部署、文件同步——所有远程操作收进同一个入口，每一步都有记录，每一次执行都可复核。

## 界面预览

![DoOps 智能运维工具首页](docs/images/doops-home.jpg)

![DoOps Console 控制台总览](docs/images/doops-console.png)

DoOps 统一入口：把智能体、控制台、CLI 和多领域 skill 组合成一套运维工具，覆盖服务器、Kubernetes、网站、数据库、文件与巡检等场景，并用同一套认证、授权与审计完成日常运维。

## 目录结构

| 路径 | 说明 |
| --- | --- |
| `app/`、`components/`、`lib/` | Next.js 控制台、API 代理和 Gateway 会话适配 |
| `backend/agent/` | Agent、Gateway 共享运行时及 DoSql/DoWeb Skills |
| `backend/gateway/` | Gateway Go 模块入口 |
| `backend/cli/` | DoOps CLI Go 模块源码 |
| `backend/skills/` | 安装到用户项目的 Operator Skill |
| `backend/docs/` | 当前 API、权限、安装、部署和运行文档 |
| `backend/scripts/`、`backend/test/` | 构建发布脚本和契约验收 |
| `backend/deploy/helm/` | Agent Helm Chart，不含真实环境清单 |
| `docs/` | 公开指南、架构图与项目维护规范 |

此版本将完整 Web 控制台和后端工作区纳入公开仓库。旧根目录的 `agent/`、
`gateway/`、`skills/doops-cli/` 已分别迁至上表所列路径；旧文档链接保留迁移入口。
代码基线为 CNB `main` 的 `0c6e69935d16340d192a07a63832b6bc1f2da68b`，
未合并分支和本地未提交工作不在本次迁移范围内。

## 架构

<p align="center">
  <img src="docs/images/architecture.svg" alt="DoOps 架构图" width="1100">
</p>

客户端经 Gateway 完成登录、精确目标授权和审计；Agent 主动向 Gateway 注册，
负责受控的命令、文件、数据库、网站和 Kubernetes 操作。Gateway 的主要实现在
[`backend/agent/internal/server/`](backend/agent/internal/server/README.md)，
控制台通过本仓库的 API 适配器访问同一控制面。

- [后端使用与架构](backend/README.md)
- [API 和协议](backend/docs/DOOPS_API_GUIDE.md)
- [安装指南](backend/docs/CLUSTER_INSTALL_GUIDE.md)
- [Agent UI 集成](docs/agent-ui-integration.md)
- [受管凭据生命周期](backend/docs/CREDENTIAL_RESOURCES.md)
- [发布和回滚](backend/docs/BUILD_DEPLOY_SOP.md)

## SQL 与网站运维

DoSql 和 DoWeb 复用 Gateway 登录、精确 target 授权和审计。数据库请求使用 `query`、`plan`、`discover`，写入使用明确的 `apply`：

```bash
doops -session db-check sql --target <target> --mode query --request 'Count rows in orders'
doops -session db-plan sql --target <target> --mode plan --request 'Plan an external_id column for orders'
doops -session db-discover sql --target <target> --mode discover --request 'List database workloads'
doops -session db-change sql --target <target> --mode apply --file change.sql --verify 'SELECT external_id FROM orders WHERE 1 = 0'
```

凭据通过 target 绑定的受管资源解析；不要把数据库密码、token 或连接串写进命令、prompt、日志或文档。完整边界见 [SQL 使用指南](backend/docs/DOOPS_USAGE_GUIDE.md) 和 [凭据资源](backend/docs/CREDENTIAL_RESOURCES.md)。

## 开发与验收

```bash
git clone https://github.com/symansun/doops.git
cd doops
(cd backend/agent && go test ./...)
(cd backend/gateway && go test ./...)
(cd backend/cli && go test ./...)
python3 -m pytest backend/test -q
corepack pnpm@11.7.0 install --frozen-lockfile
corepack pnpm@11.7.0 test:gateway-policy
corepack pnpm@11.7.0 test:console
corepack pnpm@11.7.0 exec tsc --noEmit
corepack pnpm@11.7.0 build
```

开发控制台用 `corepack pnpm@11.7.0 dev`，生产部署必须配置
`DOOPS_CONSOLE_ALLOWED_GATEWAYS` 为实际 Gateway origin。演示模式只在开发环境可用。
浏览器验收应覆盖登录、授权目标、失败提示、空状态及桌面/移动布局。

## 安装与发布边界

GitHub `symansun/doops` 是后续源码和 Pull Request 的主入口。现有版本化二进制、
镜像和 CNB 构建发布链继续沿用真实来源；本次没有发布 GitHub Release，
也没有把已有镜像 digest 改指向另一个 registry。

安装 CLI/Skill 需从发布记录取得 tag 和二进制内嵌的源码 commit，确保本地 Git
对象包含该 commit 的 `backend/skills/SKILL.md` 与 `backend/docs/`，再运行：

```bash
bash scripts/install.sh --version <release-tag> --project /absolute/path/to/project
```

安装器只下载和校验指定 CNB Release 资产，CLI 唯一安装入口为 `~/.local/bin/doops`。
如果发布 commit 尚不在当前克隆中，按发布记录从对应源码仓库获取它后重试；
安装器会在覆盖前拒绝缺失源码、校验失败和无效配置，不混用当前工作区文件。
Agent 镜像不包含 Gateway，也不单独发布 `do-agent` 二进制；其不可变运行时来源见
[`backend/runtime-versions.env`](backend/runtime-versions.env)。

远端操作必须指定目标并带 session。安装或升级完成后，至少用受授权账号验证：

```bash
doops -session acceptance login --target <target>
doops -session acceptance targets --target <target>
doops -session acceptance exec --target <target> --cmd 'hostname'
```

涉及发布、数据库写入或升级时，按对应权威文档执行授权、验收与回滚流程。

## Contributors

核心贡献者署名顺序：孙勇、高家祺、宋晨宇。欢迎通过 Issue 和 Pull Request 贡献。

## License

本项目以 [MIT License](LICENSE) 开源。
