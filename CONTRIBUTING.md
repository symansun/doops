# 贡献指南

DoOps 的源码主仓为 [symansun/doops](https://github.com/symansun/doops)。
所有合并通过 Pull Request 进行，完整约定见[维护规范](docs/OPEN_SOURCE_MAINTENANCE.md)。

## 分支与原子提交

从最新 `main` 创建语义分支，例如 `fix/gateway-cancellation`、
`feat/unified-operations-workspace`、`docs/installation-guide`。
分支名说明工作目的，不使用日期、随机名字或笼统的 `sync` 代替含义。

每个 commit 只解决一个完整问题，同时包含实现、必要测试和文档。
该 commit 应可以独立构建、验证和回滚。互相依赖的目录迁移、导入、构建脚本和 CI
必须一起提交，不拆成缺文件或 CI 必然失败的中间版本。无关的治理或功能改动单独提交。

提交标题使用 Conventional Commits：

```text
fix(gateway): retain target ownership until cancellation finishes
```

提交正文记录实际审计和验证证据：

```text
Audit: reviewed target authorization and cancellation cleanup; no credential payloads added.
Validation: executed the relevant functional scenario and git diff --check; include evidence location.
```

记录未执行的检查及原因，不把“应当通过”写成通过。

## 推送前

1. 逐 commit 查看文件清单和 diff，确认需求范围、依赖、权限、数据和发布影响。
2. 运行 README 所列受影响组件检查，并用真实命令或浏览器验证主要成功与失败路径。
3. 检查凭据、生产配置、内部资产和生成文件；只使用合成测试数据。
4. 运行 `git show --check <commit>`，确认每个 commit 的审计、验证记录完整。
5. 只推送审计通过的语义分支，随后创建实际 PR，并回读远端完整 SHA。

## Pull Request

PR 标题使用同样的语义格式，正文说明用户行为、变更范围、验收证据、风险和回滚。
每个 commit 的 SHA 和审计结论可列在 PR 的验证部分。

CI 通过、阻塞问题修复、至少一名有权限的维护者评审后，才可通过 PR 合并。
CI 的语义/格式检查不能代替代码审查，也不能证明一个提交在业务上原子。
不要直接推送 `main`，不要自行批准自己的 PR，不绕过分支保护。
后续修正仍按一个问题一个完整 commit 追加；未经明确授权不改写已推送历史。

用户流程、API、CLI、配置和发布行为改变时，同步更新对应权威文档及迁移说明。
漏洞和敏感数据问题按 [SECURITY.md](SECURITY.md) 私密报告。
