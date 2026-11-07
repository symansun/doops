# 贡献指南

感谢参与 doops.sh。公开代码、文档和发布标签以 [symansun/doops](https://github.com/symansun/doops) 的 `main` 为准。

## 开始前

行为变更、协议变更、安全修复和发布流程变更，先开 Issue 说明目标、影响范围和验收条件。拼写、链接和小型文档修正可以直接提交 Pull Request。

请先阅读：

- [开源项目维护规范](docs/OPEN_SOURCE_MAINTENANCE.md)
- [安全漏洞报告流程](SECURITY.md)
- [README](README.md) 中的安装和运行说明

## 分支和提交

- `main` 是唯一长期维护分支，禁止直接推送和强制推送。
- 从最新 `main` 创建短生命周期分支，命名使用 `feat/…`、`fix/…`、`docs/…`、`chore/…` 或 `release/…`。
- 一个 Pull Request 只解决一个可验收的问题；跨组件改动要在描述中说明依赖关系。
- 提交标题使用 `<type>(<scope>): <summary>`，例如 `fix(agent): close the gateway tunnel cleanly`、`docs: clarify release steps`。
- 合并后删除分支。未合并分支不能被当成已交付功能。

## 本地验证

修改哪个组件，就运行哪个组件的构建和测试；跨组件改动运行全部检查：

```bash
(cd agent && go test ./...)
(cd gateway && go test ./...)
(cd skills/doops-cli && go test ./...)
python3 -m pytest -q test/test_sandbox_image_contract.py
```

发布 CLI 预构建包时，还要运行：

```bash
bash scripts/build-cli.sh --all
git diff --check
```

不要把 `bin/` 下的本地构建产物、运行时配置、token、日志或真实凭据加入提交。预构建 CLI 只放在 `skills/doops-cli/bin/`，并在 Pull Request 中说明构建平台和验证结果。

## Pull Request 要求

Pull Request 描述必须包含：

1. 用户可见的行为变化和问题触发条件。
2. 受影响的组件、接口、配置或部署步骤。
3. 已执行的测试命令及结果。
4. 是否需要文档、迁移、版本号或发布说明更新。
5. 已知风险、回滚方式和未验证的边界。

涉及认证、授权、token、文件传输、远端执行、镜像发布或升级时，必须说明敏感数据边界和失败时的状态。测试和截图只能使用合成数据。

评审通过且必需 CI 检查为绿色后，维护者使用 Squash Merge 合并到 `main`。如果 PR 改变了用户流程、CLI 参数、API 或部署契约，应在同一个 PR 中更新权威文档。

## 新增组件的最低要求

新增 CLI 命令、Gateway 端点、Agent 能力或 Skill 时，应同时提供：

- 输入、输出、权限和失败类别的说明；
- 至少一个成功路径和一个拒绝或失败路径测试；
- README 或对应 `docs/` 文档中的使用示例；
- 对 release、升级和回滚影响的说明。

不确定的行为先记录为 Issue，不通过兼容分支、静默降级或未声明的 fallback 掩盖问题。
