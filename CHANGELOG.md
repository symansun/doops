# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 与语义化版本。

本文件是控制台「更新内容」的数据源：Web 镜像构建时从仓库根打包 `CHANGELOG.md`，控制台读取其中最新带版本号的条目作为当前版本。**已发布基线为 `v0.2.6`（2026-11-11）**：`[Unreleased]` 收敛之后的变更，发布时整段改成 `## [版本号] — YYYY-MM-DD`，已发布条目不再改写；0.1.0–0.2.2 为回填的历史记录。

分类标题只使用 `### Added` / `### Changed` / `### Deprecated` / `### Removed` / `### Fixed` / `### Security`（可选 `### Performance`）；条目以 `- ` 开头，续行缩进；允许行内 `code`、**粗体** 与 Markdown 链接，不要使用表格或 HTML。

## [Unreleased]

### Changed

- 构建、发布与安装统一收敛到 GitHub：GitHub Actions 承担 CI 与 tag Release 发布，仓库不再携带镜像发布流水线配置；生产镜像由操作方以显式 registry/repository/tag 和完整 40 位源提交构建推送，安装只有 GitHub Releases 一条路径

### Fixed

- 终端命令执行期间保留输出和停止入口，锁定目标、会话及控制台切换；停止请求失败继续等待终态，退出中禁止启动新命令
- COS push 在创建传输连接前统一解析自动或目的路径推导的 session；低层工作区导出始终打包当前快照，支持 main、旧 master 并存和 detached HEAD
- Edge 隧道正确保留双向半关闭连接，终态释放 socket 与配额；全局连接上限的检查与占位改为同一临界区
- 手工发布历史 tag 时 CI 门禁和产物构建使用同一完整提交 SHA；文档明确远端生成产物需使用 `pull --storage gateway`
- Agent 升级失败不再记为成功：Gateway 持久化结构化结果、失败类别与脱敏证据，区分 Job 受理、运行和阶段终态；操作编号贯通 CLI、控制台、预检、升级与回滚
- 升级或回滚断连后保留未知结果；审计收尾失败保留 Job 信息，客户端继续验收并报告审计失败，不重复变更；手动和自动回滚核对历史回执中的实际 release commit
- 控制台审计支持操作编号、时间范围、游标分页和轮询隐藏；升级来源信息进入审计，最近一次操作可恢复观察
- 审计清理与清理回执原子提交；删除没有写入者的升级孤儿表创建及维护代码，保留现有库历史数据

### Removed

- 删除已退役的 ConfigPanel 及 16 个无调用的内部辅助函数、导出和脚本函数，保留现行配置中心及共享模块

### Security

- CLI 升级历史与调试输出脱敏，并在记录前拒绝带凭据的来源 URL；审计清理保留尚未取得 Job 终态的恢复证据
- K8s 升级脚本统一经 Gateway，不再接受本地 kubeconfig 变更入口；升级派发前检查 `upgradeAudit` v1，缺少结构化审计协议时明确拒绝

## [0.2.6] — 2026-11-11

### Changed

- 安装引导收口：公开线示例更新到当前发布；明确 CNB 默认分支是公开线分发器、CNB `v1.3.x` 必须固定 tag 并使用 `backend/scripts/install.sh`；新机器统一用 `doops login --gateway` 首次发现 target

## [0.2.5] — 2026-11-11

### Changed

- 公开仓库历史清理后重建发布资产：重新生成 CLI、Gateway、Skill 包与 build provenance，恢复 tag/commit 绑定和安装校验链；本次不包含运行时行为变更

## [0.2.4] — 2026-11-11

### Added

- 公开博客 `/blog`：中文 / English / Русский 三语言文章预渲染，含「DoOps 能做什么（业务功能、管理底座与 AI 原生）」与「再见 SSH 之后」两篇长文及 18 张配图；控制台顶栏与落地页可直达
- GitHub Release 现在同时发布 `doops-skill.tar.gz`：按 CNB 资产布局打包 `skills/SKILL.md`、`scripts/install.sh` 与 `docs/*.md`，校验和一并写入 `checksums.txt`，发布后复验包内条目
- DoSQL 在模型运行前于服务端解析唯一数据库：把解析结果作为提示并覆盖模型给出的 credential/database 参数，避免发现循环与超时
- 控制台新增「更新内容」对话框：按 `Added` / `Changed` / `Performance` / `Fixed` / `Security` / `Deprecated` / `Removed` 顺序分组（未知分类归「其他」）聚合多个历史版本，条目带版本徽标与展示范围，手动打开时可切换「自上次查看以来 / 最近 10/20/50/100 个版本」范围，支持行内 `code`、**粗体** 与 http(s) 链接
- 控制台顶栏新增「更新内容」入口与升级后自动弹出：首次访问静默记录当前版本、不弹窗；「今天不再弹出」只抑制当天自动弹出，手动入口始终可用；无数据时入口禁用，有未读版本时显示小圆点。内容来自仓库根 `CHANGELOG.md`，随 Web 镜像构建打包

### Changed

- 公开仓库结构收口：内部设计归档与评审草稿不再随仓库发布，产品文档统一由 `backend/docs/README.md` 索引，对外文章移入 `blog/`；三个 README 的文档入口同步更新
- 离线实例记录不再按 24 小时定时删除：Gateway 在启动时清理一次、之后每小时按 90 天保留窗口清理；7 天活跃窗口只决定默认列表
- 安装文档固定到发布 tag，并区分两条发布线的资产来源：公开发布线（`v0.2.x`，GitHub Releases）支持 `doops login --gateway` 首登发现，CNB 生产线（`v1.3.x`，`install.sh` 默认的 CNB Release Assets）不支持，需四项显式路由；CNB 仓库默认分支 `master` 仍是旧扁平布局，克隆时必须 checkout tag

### Fixed

- 配套文档纠正与实现不符的描述：普通 Agent 反向注册只声明 `cluster/instance`（不存在通用 enrollment token，只有 Edge 隧道使用 `-edge-enroll-token`）；非回环 Gateway 强制 https/wss；`read`/`write`/`push`/`pull` 的 COS 与 workspace 路径限制；`check`、`k8s`、凭据与审计接口的实际参数、过滤维度和错误类别

## [0.2.3] — 2026-11-11

### Added

- 统一 CLI：`login --gateway` 首次登录并自动发现 target，`sync` 补路由、`targets --admission` 回读 do-agent 名额与队列、`check` 比对线上镜像
- `credential` 命令组（create / configure / put / grant / grant-revoke / verify / promote / revoke / preflight / bundle）：凭据元数据存 Gateway，payload 只经 stdin 与私有通道，grant 在 target 上验证通过后才激活
- `sql`、`web` 命令组：经 Gateway/Agent 调用 DoSql、DoWeb skill；`image build|copy` 与 `registry sync` 承接镜像构建与复制
- `k8s` 受限运维入口：get / describe / logs / top / events / rollout status / helm 只读视图，restart / scale / cordon / delete 必须带 `--confirm`，Secret 与 ServiceAccount 不开放
- `ask --apply` 成为业务发布入口，任务内声明 target、范围、原因、Chart 与镜像资产及验收条件；支持 `--credential`、`--repo-ref`、`--database-ref`
- Web 控制台（/console）：Gateway 登录与目标切换，概览 / AI 助手 / 数据库 / 网站 / Kubernetes / 镜像 / 文件 / 知识库 / 凭据 / 审计 / 终端视图，另有部署、CI/CD、管理三个顶栏入口
- Helm Agent 自升级：`upgrade` 由目标集群内 Job 执行，升级前备份、升级后校验并在失败时自动回滚；`--dry-run` 只预览参数
- 发布与安装链：GitHub Actions 由 tag 触发构建不可变 CLI/Gateway 资产，`scripts/install.sh` 校验 sha256、release/commit 与 CLI contract 后安装，并在 receipt 记录 release、commit、skill_commit、contract 与每个 skill 路径
- Agent 镜像内置 shell / k8s / image-build / dosql / doweb 等 skill；`backend/skills/SKILL.md` 与 `backend/docs/*.md` 是项目 Skill 的唯一下发源

### Changed

- 授权单位收敛为 (user, cluster, instance)：一条 target grant 覆盖该 target 上的全部操作与已绑定凭据，旧的 per-action grant 迁移为 target grant
- `exec --bg` 承接原 `bg` 后台任务；`read` / `write` / `push` / `pull` 默认使用 Gateway COS 存储，`--storage gateway` 走本地中继
- 源码树重组为 `backend/`（gateway / agent / cli / docs / deploy / scripts / skills / test），Web 控制台位于仓库根 `app/`、`components/`、`lib/`
- 文档重组到 `backend/docs/` 并按主题单一归属，Skill 只保留红线、命令选择与链接

### Removed

- `doops audit` 命令：审计查询由 Gateway 审计记录与控制台审计视图提供
- Gateway `/metrics` 端点与 `doops-gateway token list|revoke`：token 管理改为 `doops admin token create|list|revoke`
- 命令护栏及 `DOOPS_BLOCK_EXTRA` / `DOOPS_BLOCK_DISABLE` / `DOOPS_BLOCK_OFF` 配置
- `examples/skills/` 示例库与 `install.sh --from-source`：安装只使用校验过的 Release 资产
- 顶层 `doops install`、`doops bg`、`doops build`：分别由 Helm/托管脚本接入、`exec --bg`、`image build` 取代

## [0.2.2] — 2026-11-08

### Added

- gateway `/metrics` Prometheus 端点：在线 agent 数、WS 客户端连接数、活跃操作数、工具调用计数（按 action/status）、护栏拦截计数；回环地址直接放行，非回环需 `Authorization: Bearer $DOOPS_METRICS_TOKEN`
- token 轮换管理：`doops-gateway token list` / `token revoke -id <tok_...>`，吊销后立即失效
- `examples/skills/` 示例库：nginx 巡检、证书到期巡检、日志收集打包（各含 README + 测试说明）

### Changed

- `scripts/install.sh` 默认从 GitHub Release 下载 CLI 二进制与 Skill 包（`doops-skill.tar.gz`），sha256 校验；`--version` 锁定版本，`--from-source` 保留仓库内预构建包路径
- E2E 冒烟新增 `/metrics` 断言（agents_online、exec/success 工具调用计数）

## [0.2.1] — 2026-11-08

### Added

- `doops audit`：审计查询命令，支持 `--blocked`/`--bypassed` 过滤、`--session` 聚合与 `--json` JSONL 导出；agent 侧新增 `doops_audit_query` 工具（只读授权）
- 演示站健康检查 workflow（`/health` + 登录拨测，每 30 分钟）

## [0.2.0] — 2026-11-08

### Added

- agent 命令护栏：`exec` 前拦截 10 类高危命令（递归删根/系统目录、写块设备、mkfs、关机重启、fork 炸弹、远程脚本管道执行等），命中即拒绝并写入审计（`blocked=1 rule=<id>`）；支持 `DOOPS_BLOCK_EXTRA` / `DOOPS_BLOCK_DISABLE` / `DOOPS_BLOCK_OFF` 配置
- CLI 错误分层可操作化：每类失败附下一步建议（401→重新登录、403→授权路径、护栏→规则 ID 与豁免、agent 离线→targets 核查、超时→bg 异步）
- 英文与俄文 README，三语言互链
- 微信公众号图文指南（单文件 standalone HTML）
- 护栏 scope 豁免通道：gateway 转发 exec 时注入调用方 scope（`caller_scopes`，覆盖防伪造），持 `admin`/`exec:privileged` scope 的调用命中护栏规则可放行，审计标记 `bypassed_by_scope=1`；旧 gateway/新 agent 向后兼容
- 本 Release 工作流：tag 触发自动构建 CLI（4 平台）+ agent/gateway（linux amd64/arm64）并发布附件

### Fixed

- gateway-only 校验错位：`exec`/`ask`/`write`/`read`/`info` 对 gateway target 被误拒，已将该校验移回 `bash` 子命令
- 旧 dispatcher 的误伤与混淆绕过：空白/大小写规范化，`rm -rf /tmp/x` 不再被 `rm -rf /` 规则误拦

### Security

- 护栏拦截事件纳入审计（`blocked=1`），可检索、可导出

## [0.1.0] — 2026-11-07

首个公开版本（MIT）：

### Added

- 公网 gateway + 内网 agent 反向注册架构，无需 SSH
- CLI 统一入口：exec / ask / write / read / bg / push / pull / build / upgrade / admin
- token 鉴权与 cluster/instance/action 粒度授权
- 会话工作区隔离与 `.doops-audit-log` 审计
- BuildKit 远端构建、Skills 沙盒、doagent ACP 接入
