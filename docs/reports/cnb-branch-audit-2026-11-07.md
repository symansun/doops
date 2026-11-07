# CNB 分支审计报告（2026-11-07）

本报告只记录一次性分支盘点，不把未合并工作误报为已交付。审计对象是 `https://cnb.cool/l8ai/ai/doops.sh.git` 的远端引用，目标主线是 `cnb/main`。

## 基线

| 项目 | 值 |
| --- | --- |
| 审计日期 | 2026-11-07（Asia/Shanghai） |
| `cnb/main` | `0c6e69935d16340d192a07a63832b6bc1f2da68b` |
| `cnb/main` 提交 | `chore(deploy): pin verified zyj-test identity and acceptance evidence (#87)` |
| `cnb/master` | `d01347279398da40a9d25af42e5e2db9441b4804` |
| `cnb/master` 与 `cnb/main` | `cnb/main` 领先 943 个提交，`cnb/master` 有 13 个独有提交 |

审计前执行：

```bash
git fetch cnb --prune
```

对 `cnb/HEAD`、`cnb/main` 和 `cnb/master` 以外的 120 个远端分支，依次执行：

```bash
git merge-base --is-ancestor <branch> cnb/main
git cherry cnb/main <branch>
```

## 结果

| 分类 | 数量 | 判定 |
| --- | ---: | --- |
| tip 已是 `cnb/main` 祖先 | 70 | 已合并 |
| tip 不是祖先，但 `git cherry` 无 `+` 补丁 | 9 | 补丁已等价进入主线，可关闭分支 |
| 仍有 `+` 补丁 | 41 | 未合并，不能视为已交付 |

结论：**CNB 分支没有全部合并到 `main`。**

## 仍有未合并补丁的分支

### 近期工作链（2026-11-05 至 2026-11-07，共 25 个）

```text
cnb/claude/g1-deploy-hardening
cnb/claude/g2-proxy-route-web-errors
cnb/claude/g3-credential-error-guidance
cnb/claude/g5-k8s-hardening
cnb/claude/g6-image-hardening
cnb/claude/integration-20261106
cnb/claude/integration-20261106b
cnb/claude/k10-agent-model-error-scope
cnb/claude/k13-login-errors
cnb/claude/k14-no-grant-empty-state
cnb/claude/k16-k24-gateway-errors-cicd-url
cnb/claude/k18-files-maintenance
cnb/claude/k20-agent-result-markdown
cnb/claude/k25-refresh-keeps-target
cnb/claude/k31-configmap-keys
cnb/claude/k4-grant-revoke-offline
cnb/claude/k4-grant-revoke-offline-v2
cnb/claude/k6-audit-action-index
cnb/claude/permission-audit
cnb/claude/r3-1-deploy-notice
cnb/claude/r3-agent-error-followups
cnb/claude/r4-deploy-followups
cnb/claude/r6-deploy-followups
cnb/claude/r7-4-revoke-body
cnb/claude/r8-k8s-old-agents
```

这些分支存在集成链、评审状态或真实环境验收依赖，应按功能组逐个形成 GitHub Pull Request；不要把整条分支链一次性合入。

### 历史或专项分支（共 16 个）

```text
cnb/auto/exception-report-a7c3
cnb/codex/dosecurity-scanner-deploy
cnb/codex/fix-gateway-systemd-release
cnb/codex/full-release-qa-20260627
cnb/codex/native-execution-merge-20261003
cnb/codex/native-grant-metadata-20261003
cnb/codex/vscode-codex-master
cnb/feature/generic-credential-contract
cnb/fix/audit-log-workspace-isolation
cnb/fix/bootstrap-input-validation
cnb/fix/credential-revoke-lock
cnb/fix/dosql-journal-utf8
cnb/fix/external-agent-release-controller
cnb/fix/helm-auto-preserve-contract
cnb/fix/retired-ws-cutover
cnb/fix/session-scoped-mutation-lock-20261007
```

这些分支先做补丁级复核：如果功能已经由后续提交覆盖，关闭并删除；如果仍有用户价值，补齐评审、测试和迁移说明后再进入 GitHub `main`。

### 补丁已等价进入 `main` 的分支（共 9 个）

```text
cnb/codex/fix-doops-bg-gateway
cnb/codex/frontend-backend-integration
cnb/feat/gateway-agent-http-proxy
cnb/feat/gateway-agent-proxy-allowlist
cnb/feat/release-agent-proxy-allowlist
cnb/fix/agent-image-skopeo-20261008
cnb/fix/cicd-agent-report-reconcile
cnb/fix/release-proxy-host-validation
cnb/fix/release-trigger-input-validation
```

这类分支不能因为 tip SHA 不在 `main` 就再次合并；应保留审计记录后归档或删除。

## 收敛动作

1. 先把近期工作链按功能拆成 GitHub Pull Request，补齐评审和成功/失败路径验证。
2. 对历史分支逐一做 `git cherry` 和代码审查，不能用“分支很旧”代替事实判断。
3. 将 CNB 默认分支从 `master` 切换到 `main`，并停止在 `master` 上创建新工作。
4. GitHub `main` 合并完成后，CNB 只保留同步构建所需的引用；分支删除动作要记录分支名、替代 commit 和删除日期。
