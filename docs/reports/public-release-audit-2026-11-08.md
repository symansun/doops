# GitHub 公开发布审计（2026-11-08）

审计对象：`symansun/doops` 的语义分支 `chore/github-publication`。

## 公开边界

- GitHub 仓库是源码、Issue、Pull Request、Actions、Release 和 GHCR 镜像的唯一公开入口。
- 仓库不提交真实环境清单、凭据、访问令牌、客户地址、编译缓存或平台内部流水线配置。
- Agent、Gateway、CLI、Web 控制台、Skill、文档、测试和构建脚本都位于同一公开树中。

## 提交与审计

- 相互依赖的目录、源码、测试和发布链作为一个完整提交；独立的治理文档与贡献门禁单独提交。
- 每个交付 commit 的正文带 `Audit:` 与 `Validation:`，并由 GitHub Pull Request 审阅。
- 分支使用 `feat/`、`fix/`、`docs/`、`chore/`、`ci/` 等语义前缀；`main` 不接受直接开发推送。

## 发布链

1. GitHub Actions 在 Pull Request 和版本 tag 上运行 Go、Python、Node、TypeScript、生产构建和 secret scan。
2. 版本 tag 必须解析到已进入 `main` 的完整 commit SHA。
3. 发布脚本创建 draft Release，预检 CLI/Gateway 资产，逐个上传并回读大小和名称，最后才公开 Release。
4. 安装器从 `https://github.com/symansun/doops/releases/download/<tag>/` 下载资产，校验 `checksums.txt`，检查 CLI 的 release/commit/contract 身份，再从同一 commit 归档安装 Skill 与文档。
5. 容器镜像使用 `ghcr.io/symansun/doops-agent`、`ghcr.io/symansun/doops-base-light` 和 `ghcr.io/symansun/doops-web`，发布和部署均要求 digest。

## 验证证据

- `git diff --check` 与公开地址扫描通过。
- GitHub Actions workflow 语法可由 YAML 解析器读取；发布脚本通过 Node 语法检查。
- 发布和安装契约测试覆盖 GitHub Release URL、校验和、源码归档、CLI 身份和不可覆盖资产规则。
- 本地验证包括完整 Agent/Gateway/CLI 检查；最终认证整合后，认证功能组和完整 CLI 再次通过。
- 637 项 Web 功能检查、7 项 Gateway 策略检查、TypeScript 与 Next.js 生产构建通过，零跳过。
- 362 项 Python 契约检查的唯一导航大小失败已通过精简重复文档解决，并通过原门禁复验；未修改测试上限。
- 公开源码精确目录经 Gitleaks 扫描无泄漏；Actions 通过 actionlint，三语 README 的 69 个本地链接有效。
- 最终远端验证以 Pull Request 检查为准。尚未执行版本发布或业务环境部署，不以源码检查冒充可下载资产或线上验收。

## 合并门槛

Pull Request 必须显示原子提交、审计报告和 CI 结果；合并前重新核对 base/head SHA，合并后回读 `main`、Release 和 Actions 状态。发布资产不代表已经部署到任何业务环境，部署需要单独的目标、授权、健康检查和回滚证据。
