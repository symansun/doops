# doops.sh 开源项目维护规范

本文档是公开仓库的维护约定。代码、文档、Issue、Pull Request、发布标签和安全公告均以 [github.com/symansun/doops](https://github.com/symansun/doops) 的 `main` 为公开源头。

## 源头和目录边界

- GitHub `main` 是唯一公开开发主线。CNB 只承担镜像、构建和内部协作职责；CNB 与 GitHub 出现差异时，必须通过可审阅的 Pull Request 收敛，不能在两个平台各自继续开发。
- 公开仓库保留完整的 `app/`、`components/`、`lib/` Web 控制台，以及 `backend/agent/`、`backend/gateway/`、`backend/cli/`、`backend/skills/`、`backend/scripts/`、`backend/docs/`、`backend/test/` 和镜像构建文件。内部环境清单、真实凭据、部署证据、工作日志和历史审查记录不得进入公开快照。
- `backend/skills/SKILL.md`、README、协议文档和安装脚本中描述的行为必须与当前实现一致。行为变更应在同一个 Pull Request 更新权威文档。
- 预构建 CLI 是发布输入，不是开发源码。源码在 `backend/cli/`，发布包固定放在 `dist/cli/`，每次重建都要记录平台和 commit。

## 分支模型

### `main`

`main` 是受保护分支，只接受 Pull Request。合并前必须通过 GitHub Actions 的 Agent、Gateway、CLI 和镜像契约检查；发布标签只能从已通过检查的 `main` 提交创建。

### 工作分支

使用短生命周期分支：

| 前缀 | 用途 |
| --- | --- |
| `feat/` | 新能力 |
| `fix/` | 缺陷修复 |
| `docs/` | 文档或维护流程 |
| `chore/` | 构建、依赖和工具维护 |
| `release/` | 版本准备和发布检查 |

分支必须从最新 `main` 创建。合并后立即删除；长期保留的分支必须在 Issue 中写明负责人、用途、同步频率和退出条件。

### CNB 分支收敛

迁移期间，CNB 分支只按以下顺序处理：

1. `git fetch cnb --prune`，固定记录 `cnb/main` 的 commit。
2. 对每个分支先用 `git merge-base --is-ancestor` 判断是否已经合并。
3. 对 tip 不是祖先的分支再用 `git cherry cnb/main <branch>` 判断补丁是否已等价进入主线。
4. 只有仍有 `+` 补丁、且有明确负责人和验收证据的分支，才创建 GitHub Pull Request；已被后续实现覆盖的分支关闭并删除，不重复合并。
5. CNB `master` 不再作为开发主线；在 CNB 平台把默认分支切换到 `main` 后，删除或归档旧 `master`。

一次性审计结果见 [CNB 分支审计报告](reports/cnb-branch-audit-2026-11-07.md)。

## Issue 和 Pull Request

- Bug、功能、文档和安全问题分别使用对应模板。安全漏洞走 [SECURITY.md](../SECURITY.md) 的私密渠道，不公开复现细节。
- Issue 首次分诊目标为 3 个工作日内；维护者需要标记组件、优先级、是否需要迁移和下一步证据。
- Pull Request 必须说明用户影响、代码边界、测试命令、风险、文档影响和回滚方式。涉及权限、token、远端执行、文件传输、镜像或升级时，必须说明敏感数据不会进入日志、Issue、Git 或响应体。
- 一个 Pull Request 只解决一个可验收问题。跨组件改动可以放在同一个 PR，但必须在描述中列出依赖和验证顺序。
- CI 通过、至少一名维护者评审、没有未解决的阻塞评论后，使用 Squash Merge 合并到 `main`。禁止 force push 覆盖他人分支。

## 代码和文档审查

审查顺序固定为：安全边界、权限完整性、失败状态、数据和协议兼容、可观测性、性能、可读性。发现根因后修根因，不使用静默降级、吞错、旧接口旁路或未声明的 fallback 掩盖问题。

文档至少要回答：

- 用户或运维人员要完成什么任务；
- 输入、输出、权限和失败类别是什么；
- 哪个文件或命令是权威入口；
- 如何验证成功，失败后如何回滚；
- 哪些能力仍是限制或未实现状态。

协议、认证、授权和发布契约变更，应同时更新对应文档、CLI `--help`、示例和测试。不要把只在计划中存在的行为写成已支持。

## CI 和发布

每个 Pull Request 至少运行：

```bash
(cd backend/agent && go test ./...)
(cd backend/gateway && go test ./...)
(cd backend/cli && go test ./...)
python3 -m pytest -q backend/test
```

维护者在本地还应运行 `git diff --check`。涉及 CLI 预构建包时运行 `bash backend/scripts/build-cli.sh --all`，并审查二进制是否与源码 commit 对应。

版本使用 `vMAJOR.MINOR.PATCH`：

1. 在 `main` 上确认 CI、文档、版本号和镜像构建结果。
2. 创建带说明的版本标签，并在 GitHub Release 中记录用户影响、升级步骤、已知限制和回滚方式。
3. 镜像和部署引用使用不可变 digest 或明确版本标签；生产环境不依赖浮动 `latest`。
4. 发布后验证安装脚本、CLI、Gateway 健康接口、Agent 注册和一条只读 `exec` 路径。涉及 `push/pull/ask/upgrade` 时追加对应的真实路径验证。
5. 失败时按发布记录回滚，不在服务器上留下无法追踪的手工热修。

安全修复可以跳过常规发布时间，但必须保留私密修复记录、回归测试和发布说明。

## 依赖、凭据和公开内容

- 定期更新 Go modules、Python 测试依赖、GitHub Actions 和基础镜像；依赖更新必须通过同一套 CI。
- 依赖升级需要记录直接影响的组件、许可证和回滚方式。不要为了让 CI 变绿而删除检查、锁定错误版本或复制未知二进制。
- 真实 token、密码、私钥、云账号、内部域名、客户数据和生产日志不得进入仓库、Issue、PR、Release、构建输出或文档。
- 新增日志、审计和错误信息时，先验证其不会回显凭据或内部原文；测试使用合成 canary。
- 公开发布前检查 Git 历史、构建上下文和镜像层，确认没有通过删除当前文件而仍留在历史中的敏感内容。发现泄露时先按安全流程处理，再决定是否重写历史。

## 兼容和弃用

弃用命令、配置、协议或镜像入口时：

1. 在文档和 Release Notes 标明弃用原因、替代路径和移除版本。
2. 保留足够的迁移说明和可验证的失败提示。
3. 删除旧路径前先检查 GitHub、CNB、安装脚本、示例和测试中的引用。
4. 安全风险或数据损坏风险需要立即移除时，在发布说明中明确写出破坏性变化和回滚边界。

不得为了旧调用方保留未审计的兼容分支；确有兼容需求时，要在 Issue 中写明范围、截止版本和删除条件。

## 维护记录

每次发布、分支收敛、安全修复和重大协议变化至少留下以下信息：

- GitHub commit 和 release tag；
- 受影响组件和用户路径；
- CI、浏览器或部署验证证据；
- 已知限制、未验证项和回滚方式；
- 后续 Issue 或责任人。

历史运行排障记录可以保留在 `docs/DOOPS_MAINTENANCE_LOG.md`，但公开维护规则以本文档为准。
