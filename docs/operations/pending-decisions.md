# 待决策（pending-decisions）

用户给出决定后，总控移入文末「已决」，并按决定重新派活。

## D1 `.worktrees/agent-resource-sync` 半成品处置

- 来源：Antigravity 会话 `ad402c4c`（2026-11-23 23:49 停），执行 `backend/docs/design/AGENT_RESOURCE_SYNC.md` 四期优化。
- 现状：detached HEAD `34de4d3f`，27 文件 +429/−146，无新增测试与文档；基线落后 main 53 个提交；worktree 违反 R1「禁用 git worktree」。
- 选项：A 废弃重做（按当前 main 重读设计文档，重写四期）；B 逐条挑拣（人工比对 27 文件与 main 差异后择优重提）；C 暂不处理。
- 推荐：A —— 半成品无测试、基线过旧，挑拣的成本高于重做。

## D2 是否在 doops.sh 长期启用 l8ai 三本账

- 现状：`docs/operations/` 已有 `backlog.md`、`pm-worklog.md`、`pending-decisions.md`；规格实体在 `agentcloud` 仓库，本仓库此前没有。
- 需确认：是否把「任务先入 backlog、提交按 R3/R4、报告模板 A」作为本仓库的常设约束，并写进 `AGENTS.md` 的 Topic Ownership 表。
- 推荐：是 —— 已有 4 条并行会话，无台账无法判重与交接。

## D3 `rescue/dedupe-web-tests` 里两个前端重构是否值得挑拣

- 来源：2026-11-22 的一条分支（4 个提交，落后 main 94 个提交，9 个 `components/` 文件冲突）。分支内容是「删除 lib 下全部 Node 测试」+ 两个重构：新增 `lib/format.ts`（15 个文件 `toLocaleString` 收敛）、`components/ui/modal.tsx`（三处手写弹窗外壳共用，补 Escape/焦点/滚动锁定）、新增 `lib/use-loader.ts`（4 个面板 AbortController 收敛，修 `k8s-resource-overview` 失败一直转圈）。
- 现状：`lib/format.ts`、`lib/use-loader.ts`、`components/ui/modal.tsx` 在 main 中均不存在，即重构未落地；main 仍保留全部 `lib/*.test.mjs`（删测试这一提交与 T1「不许删测试」冲突，不应合入）。
- 选项：A 只挑两个重构重写（丢弃「删测试」提交）；B 整支废弃；C 暂存不动。
- 推荐：A（重构有确定收益：loading 不收敛、时间单位隐患、弹窗可用性），但按当前 main 重写比 cherry-pick 便宜。

## 已决

- D1（2026-11-24 00:52，用户 m00245 "worktree整理合并，清理，后边主线开发"）：采用「先保全、再清理、后合入」。明细见下。
  - 保全：`.worktrees/agent-resource-sync`（36 文件）→ 分支 `rescue/agent-resource-sync`（d5250a6d）；`.worktrees/gateway-mysql`（12 个未跟踪文件）→ 分支 `rescue/gateway-mysql`（d32abbfd）；游离 commit 也各留分支 `rescue/cnb-ci-split`（f616beee）、`rescue/dedupe-web-tests`（2cca31bf）。
  - 清理：19 个 worktree 全部移除，只剩主工作区；18 个已合入 main 的本地分支删除；`rescue/cnb-ci-split` 判为已废弃（其唯一改动 `.cnb.yml` 在 main 的 31115116 里被「publish from GitHub only」主动删除）。
  - 合入：`rescue/gateway-mysql` 干净合并，先在隔离 clone 验 build/test 再落 main；`rescue/agent-resource-sync` 4 处冲突（agent_registry.go、gateway_store.go、handler_ws.go、tunnel_hub.go）需人工移植，排 INT-02；`rescue/dedupe-web-tests` 见 D3。
