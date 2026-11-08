---
name: doops-open-source-maintenance
scope: repository
owner: doops-maintainers
status: policy
---

# DoOps 开源维护规范

GitHub [symansun/doops](https://github.com/symansun/doops) 是源码、Issue、PR 和代码评审的主入口。
本规范规定维护行为；实际启用的分支保护、必需检查和安全报告选项须在 GitHub 设置中回读确认。
文档中列出要求不等于平台已经强制执行。

## 语义分支、原子提交、审计后推送

- `main` 为唯一长期开发主线。短期分支用 `feat/`、`fix/`、`docs/`、`chore/`、
  `refactor/`、`perf/`、`test/`、`build/`、`ci/` 或 `revert/` 加英文短语描述目的。
- 一个 commit 表达一个可独立验证和回滚的完整变更。不能按文件机械拆分耦合的目录、
  源码、CI 和测试修改，也不能把无关任务混成“整理代码”提交。
- commit 和 PR 标题使用 Conventional Commits；每个 commit 正文有 `Audit:` 和
  `Validation:`，说明已审查边界、实测结果和证据位置。不能伪造审计人或测试结果。
- 每次 push 前审查全部待推送提交，包括中间树的可构建性、敏感内容、权限、回归风险、
  文档和回滚。只推送通过审计的变更。多引用推送使用 `git push --atomic`。
- `.github/workflows/contribution-policy.yml` 检查分支/标题、提交记录与 diff 格式；
  它无法判断业务原子性，必须结合人工或独立 reviewer 审查。

## PR 合并与平台保护

所有合并通过实际 Pull Request 发起。发送 compare 链接或把分支推到远端不代表 PR 已创建。

维护者应在 GitHub 配置 `main` 保护：要求 PR、至少一名维护者批准、必需 CI 检查通过、
新提交使旧批准失效、禁止强制推送和删除主线；避免允许管理员或机器人绕过评审。
调用者没有管理权限时，记录尚未启用的设置，交由有管理权限的维护者操作。

评审关注需求、成功/失败路径、安全/权限、审计与观测、构建/安装/发布契约及文档。
中大型迁移必须有独立审查。作者不能把自己的自查冒充独立批准。

仅当 PR 的全部相关检查通过、阻塞意见已解决、必要批准到位时，维护者通过 GitHub 合并。
默认 squash 合并为一个清楚描述最终行为的提交；需要保留独立变更时，可保留已经逐条审计的提交。
合并前重新核对 base/head SHA；合并后回读 merge commit、CI，并删除已合入的工作分支。

## 源码和制品来源

GitHub 维护全部后续代码。版本化 CLI/Gateway 资产发布到 GitHub Release，容器镜像发布到
`ghcr.io/symansun/`，安装器、校验脚本和发布脚本必须使用同一仓库与版本标签。

版本更新必须记录 GitHub commit、目标基线、变更范围和验证证据。
不提交本地运行态、真实凭据、客户环境清单、临时日志和编译缓存。
发布只从已评审并进入 `main` 的源码构建；未合并分支先单独评审。

安装器、CLI、Skill/docs、镜像 revision 和发布 commit 必须能相互核对。
新增发布渠道时，在同一变更中实现 producer、下载/校验、源码绑定和回读验收，
不能只修改下载 URL。新的制品渠道未经验证前，不替换现有可用来源。

## 发布和回滚

发版使用语义版本 `vMAJOR.MINOR.PATCH`，从已通过评审和 CI 的 `main` 创建版本标签。
记录用户影响、破坏性变化、迁移、不可变镜像 digest、二进制校验和、测试证据和回滚方式。
发布资产或镜像不等同于部署；部署需另有明确目标、授权和上线验收。

安装、升级和发布失败必须显式停止并保留证据。不降低测试、跳过权限检查或使用旧协议旁路。
任何兼容路径必须有实际业务需求、明确边界、验证与退出条件。

上线操作优先通过 DoOps 和 CI/CD，核对 target、namespace、数据库、镜像和运行版本；
所有远端 DoOps 调用携带 `-session`。SSH 仅用于首次安装或 Agent 自恢复。
数据库变更通过受控 DoOps 执行并留审计，不启用 Flyway 或应用启动时自动执行 SQL。

## Issue、安全和依赖

- 一个 Issue 描述一个可验收目标。Bug 给出版本、复现步骤、预期/实际结果；功能说明用户角色与验收场景。
- 使用私密渠道处理未修复漏洞，不在 Issue、PR、日志、截图或 Git 中写入真实凭据。
- 依赖、Actions 和基础镜像升级需要说明影响和验证，保留锁文件与来源校验。
- README 对用户负责，权威文档对行为负责，PR 对变更证据负责；不能把计划写成已实现或已上线。

## 旧分支收敛

先 fetch，再对固定主线 SHA 判断祖先关系和 patch 等价性：

```bash
git merge-base --is-ancestor <branch> <main-sha>
git cherry <main-sha> <branch>
```

`git cherry` 无新增 patch 不等于功能仍在当前代码中；删除前还需确认没有后续回退和未交付工作。
真正未合并的变更应按用途发起独立 PR，不将旧分支盲合进主线。
一次性迁移审计见 [公开发布审计](reports/public-release-audit-2026-11-08.md)，结论固定在报告所列日期与 SHA。
