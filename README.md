# DoOps

[English](README.en.md) · [Русский](README.ru.md)

<p align="center">
  <img src="docs/images/slogan.svg" alt="不用 SSH，也能运维任何一台机器。" width="760">
</p>

> 本项目以 [MIT License](LICENSE) 开源。

第三方依赖保留各自许可证；内置 UI 组件的许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

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
代码、Issue、Pull Request、GitHub Actions、GitHub Release 和 GHCR 镜像都以本仓库 `main`
为唯一公开来源；未提交的本地工作不进入发布流程。

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

GitHub `symansun/doops` 是源码和 Pull Request 的主入口。版本化 CLI/Gateway 二进制进入
GitHub Release，Agent/Web/Base 镜像进入 GHCR；发布脚本会先创建草稿、上传并回读全部资产，
最后才把 Release 公开。

从 [GitHub Releases](https://github.com/symansun/doops/releases) 选择已发布的 tag。
无需克隆仓库，下载安装入口后执行以下命令，并将 `<release-tag>` 替换为所选版本：

```bash
curl --fail --location --output /tmp/doops-install.sh \
  https://raw.githubusercontent.com/symansun/doops/main/scripts/install.sh
bash /tmp/doops-install.sh --version <release-tag> --project /absolute/path/to/project
```

已克隆本仓库时，也可运行 `bash scripts/install.sh` 并传入相同参数。
安装器会从该版本 CLI 内嵌 GitHub commit 的归档读取 `backend/skills/SKILL.md`
与 `backend/docs/`。升级 CLI/Skill 时重新选择发布 tag 并执行同一安装命令；
Agent 升级按[安装与升级指南](backend/docs/CLUSTER_INSTALL_GUIDE.md)选择对应入口。

安装器只下载和校验指定 GitHub Release 资产，CLI 唯一安装入口为 `~/.local/bin/doops`。
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

## 开源协作

- [贡献指南](CONTRIBUTING.md)
- [开源维护规范](docs/OPEN_SOURCE_MAINTENANCE.md)
- [安全报告](SECURITY.md)
- [公开发布审计（固定于 2026-11-08）](docs/reports/public-release-audit-2026-11-08.md)

## Agent 命令护栏

agent 端 `exec` 执行前会经过内置命令护栏（[dispatcher](backend/agent/internal/dispatcher/dispatcher.go)），命中即拒绝执行并写入会话审计（`.doops-audit-log`，标记 `blocked=1` 与规则 ID）。内置规则覆盖：递归删除根/系统目录、`dd` 写块设备、`mkfs`/分区工具、关机重启、fork 炸弹、递归改根权限、覆写 `/etc` 关键文件、下载并直接执行远程脚本、杀死所有进程。匹配前会做空白压缩与小写规范化，防止 `rm  -rf  /`、`RM -RF /` 这类混淆绕过。

护栏可通过 agent 环境变量调整：

- `DOOPS_BLOCK_EXTRA`：追加自定义正则，分号分隔（作用于规范化后的命令）
- `DOOPS_BLOCK_DISABLE`：按规则 ID 禁用内置规则，逗号分隔，如 `net.pipe_shell`
- `DOOPS_BLOCK_OFF=1`：完全关闭护栏（仅限应急）

无效的自定义正则会阻止 Agent 启动，需修正配置后重启。

平台管理员的豁免元数据仅由已认证 Gateway 按当前管理员标记生成；普通调用方提交的
`caller_scopes` 会被清除。豁免命令仍记录 `bypassed_by_scope=1`。

使用 `doops -session audit-review audit --target <target> --session <execution-session>`
查询执行审计，`--blocked` / `--bypassed` 筛选护栏记录，`--json` 导出 JSONL。
不传查询 `--session` 时聚合目标的全部会话；操作上下文仍使用 `-session`。

可运行的 Skill 示例见 [examples/skills](examples/skills/README.md)。

护栏是纵深防御的一层，不是安全边界：它无法覆盖所有混淆写法（编码、变量展开、脚本文件间接执行），日常操作规范仍以 skill 纪律与最小授权为准。

## Contributors

核心贡献者署名顺序：孙勇、高家祺、宋晨宇。欢迎通过 Issue 和 Pull Request 贡献。

## License

本项目以 [MIT License](LICENSE) 开源。
