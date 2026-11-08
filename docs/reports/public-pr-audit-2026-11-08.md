# GitHub 公开同步 PR 审计（2026-11-08）

审计对象：`symansun/doops` 的语义分支 `chore/public-sync-cnb-main-20261108`。

## 输入与边界

- GitHub base：`e7a65c2d37bcc28568f96c3298cb7d756d46326d`。
- CNB source：`cnb/main` at `0c6e69935d16340d192a07a63832b6bc1f2da68b`。
- 迁移范围：完整 `app/`、`components/`、`lib/`、`backend/`、公开 `docs/`、测试和构建入口。
- 排除范围：真实环境清单、凭据、内部工作日志、历史审查记录、编译二进制和未合并分支。
- 制品来源：保留 CNB 已验证的镜像和 CLI Release 资产来源；未创建未验证的 GitHub Release 下载链。

## 原子 commit

每个提交都只表达一个可审阅目的，并在正文中带 `Audit:` 与 `Validation:`：

1. `a6c5635` — `chore: migrate complete public source tree`：目录迁移、Go module identity、Web/Agent/CLI 源码、旧路径删除、公开地址脱敏、CNB CI/release 契约和 public assets。
2. `07ce79c` — `docs: establish audited open-source contribution policy`：语义分支、原子提交、逐 commit 审计、PR-only 合并、Issue/PR/Security 模板和维护规范。

不把目录迁移拆成会让 CI 访问已删除路径的中间提交。治理检查会在每个 PR 提交上验证语义 branch/title、`Audit:`、`Validation:` 和 `git show --check`。

## 已执行检查

- `python3 -m pytest -q backend/test`：329 passed。
- `(cd backend/agent && GOCACHE=<task-cache> go test ./...)`：passed。
- `(cd backend/gateway && GOCACHE=<task-cache> go test ./...)`：passed。
- `(cd backend/cli && GOCACHE=<task-cache> go test ./...)`：passed。
- Node Web tests、Gateway policy、TypeScript、Next production build：使用同一迁移树验证；依赖目录必须由 frozen install 提供，不能使用跨工作树 symlink。
- `node .../cnb-pipeline/validator/validate.js .cnb.yml`：YAML、语义和 schema 通过。
- `git diff --check`、公开地址/真实 IP/凭据路径扫描：通过。
- 迁移后的 README、安装/发布路径、部署脚本、旧文档迁移入口和 GitHub 最新公开图片均已回读。

## 合并门槛

- 先推送语义分支，再创建 GitHub PR；不直接推送 `main`。
- PR 必须显示这两个语义提交、审计报告和 CI 结果。
- 需要维护者批准、分支保护和必需检查通过后才合并。
- 合并前重新回读 base/head SHA；合并后回读 `main`、CI 和 PR 状态。
- 旧同步分支不作为交付入口；确认新 PR 可审阅后，再删除旧分支的远端引用。
