# zyj ask 准入阻塞事故复盘（2026-11-10）

## 结论

- target `zyj` 上所有 `doops ask`（含只读）曾稳定失败为
  `execution_outcome_unknown: prompt_turn_id_missing`。
- 直接原因不是"边缘 Agent 与本机 CLI 版本协议不匹配"，而是 **zyj 上 do-agent 0.2.42 的全局并发
  准入 permit 泄漏**：`[GC admission]` 把新 prompt 排进全局队列（`global position ~0`），
  do-agent 对流式 prompt 只返回 `{status:"queued"}` 而没有 `turnId`；DoOps Agent（v1.3.124）
  的 `doagentPromptTurnID` 因此把"尚未准入"误判为"执行结果未知"。
- Lead 已重建 zyj 的 `doops-agent` Pod 清空 do-agent 内存状态（镜像/Chart/配置未变），
  重连后只读 ask 恢复 `OK`。
- 根因修复已有实现但**尚未发布**：doops 侧 A1 在 `doops-admission-remediation` 分支，
  do-agent 侧 A3 在 doagent 仓库 `doagent-admission-remediation` 分支。

## 1. 现象

```text
$ doops -session lead-diag-20261110a ask --target zyj -msg '只读诊断：不要修改任何资源。请只回答 OK。'
Error: remote error: execution_outcome_unknown: prompt_turn_id_missing
```

用户此前已尝试 3 个 session（含全新 session）、显式 `--credential`、最小消息、伪 TTY，
全部同样失败。

## 2. 现场取证

- zyj Agent 运行 `v1.3.124` = `081f24ab4e92`；本机 CLI `v1.3.126` = `76abae08a36d`。
  二者不是必须相同的组件，且 `081f24ab` 是 `76abae08` 的祖先（`git merge-base --is-ancestor` 成立），
  v1.3.124 的 `handler_ws.go` 已包含同一段 `prompt_turn_id_missing` 校验。
- zyj 上 `do-agent --version` = `do-agent 0.2.42`；Agent Pod 内进程为
  `/usr/local/bin/do-agent acp-http --port 9000 --cwd /root/ws`。
- 触发只读 ask 时，Agent 日志出现（UTC）：
  ```text
  2026/11/10 01:05:03 🤖 WS Running doagent via ACP HTTP: session=lead-diag-20261110a structured=false model=
  2026/11/10 01:05:03 🔗 Bound Doops Session lead-diag-20261110a -> doagent Session lead-diag-20261110a
  [SSE] Client connected: session=lead-diag-20261110a
  [GC admission] Queued prompt for session lead-diag-20261110a (global position ~0), queue length: 1
  [RequestQueue] Cleared 1 queued prompt(s)
  ```
  即：prompt 根本没被准入执行，后续 `Cleared` 是 DoOps 断连后的队列清理。
- 新 Pod 启动日志给出并发上限：
  `[do-agent gc] runtime mode=Lite budget_mb=3200 concurrent_turns=2 memory_sessions=16`。
  两个全局 turn permit 均被历史 turn 占住/泄漏，因此 `global position ~0` 表示"没有可用 permit"。
- 同期 `doops capabilities --target zyj --require structuredSSH` = `ready`，`doops exec`
  一直可用；问题被限定在 do-agent prompt 准入层，而不是 Gateway 鉴权、隧道或结构化执行链路。

## 3. 根因链

1. do-agent 0.2.42 的全局并发 permit 只在本轮 turn 正常结束时释放；某个/些历史 turn 未正常收尾
   （断连、取消或 GC 与 turn 生命周期竞争）后，permit 永久泄漏。
2. 后续所有 prompt 在 `[GC admission]` 排队；流式响应只回 `{status:"queued"}`，没有 `turnId`。
3. DoOps Agent 把 "没有 turnId 的 prompt 响应" 归入 `execution_outcome_unknown`
   （该分类对只读请求是噪声，对 `ask --apply` 则意味着"可能已经改了集群但结果未知"，必须禁止）。
4. Pod 重建后该内存状态重置，ask 立即恢复 —— 这是本次恢复手段，不是根因修复。

### 为什么不是"边缘 Agent 与本机 CLI 协议不匹配"

- 恢复前后 Agent 版本、镜像 digest、do-agent 版本都没变，只是重建了 Pod：
  同一组版本从"全部失败"变为"只读 ask OK"，说明版本组合本身是可用的。
- 失败点在 do-agent 准入层：日志显示 prompt 从未被执行，报错发生在拿到准入响应之后。
- 跨版本兼容由 Gateway 的 capability/relay 契约处理，不是靠 CLI 与 Agent 的 commit 相等
  （gw-test 恰好相等只是巧合）。

## 4. 恢复动作与验证

- 动作：`kubectl -n ai delete pod doops-agent-668f996958-49nl5 --wait=false`
  （单副本 Deployment + Helm release `doops-agent`，revision 49；未修改 Deployment 模板/镜像/配置，
  Helm 状态不受影响）。
- 新 Pod `doops-agent-668f996958-nvm88` 1/1 Running，Agent 于 `2026-11-10T01:07:41Z` 重连
  Gateway（release commit 仍为 `081f24ab4e92`）。
- 验证：`doops -session lead-diag-20261110b ask --target zyj --msg '只读诊断：不要修改任何资源。请只回答 OK。'`
  → `OK`（exit 0）。
- 未验证：`ask --apply`（当时没有业务 workload 的任务事实）。恢复后应使用真实任务重跑，
  并在同一时刻抓 Agent 日志确认没有 `[GC admission]`。

## 5. 为什么 Gateway 指标看不出来

`doops targets --target zyj` 在事故期间显示 `BUSY=false STATUS=idle ACTIVE=0 QUEUED=0`。
这些计数只覆盖 Gateway/Agent 自己跟踪的 DoOps 执行，不反映 do-agent 内部的全局准入 permit
占用。因此"看起来空闲"和"所有 prompt 都排队"可以同时成立。

## 6. 修复状态（截至本记录）

- **A1（doops 侧，已就绪未发布）**：分支 `doops-admission-remediation` @
  `fbce260f`（A1 代码 HEAD `1ef51421` + 1 个 docs 纠偏提交），基线与生产线
  `cnb/main` = `66769eef` 完全一致（无需 rebase，零冲突、未改写提交）。
  行为核对通过：queued/未准入被归类为稳定类别（`doagent_prompt_queued` /
  `doagent_prompt_queue_cleanup_unknown`），Gateway relay 原文转发、不再改写为
  `execution_outcome_unknown`；session 级 cleanup 只用于本次请求铸出的机器结果
  session；请求级取消用 `queueRequestId`，未确认即 fail-closed；mutation 未知护栏保留。
  测试：agent `./internal/server` ok（535s）+ agent `./...` 8 包全 ok + CLI `./...` ok
  + `git diff --check`/gofmt 干净；文档契约 pytest 10 passed / 1 已存在的 README
  退役指引失败（与本分支无关）。注意：doagent 仍是 0.2.42 时，流式 ask/apply 的
  queued 只能 fail-closed 为 `doagent_prompt_queue_cleanup_unknown`（不自动重试），
  要变成可重试的 `doagent_prompt_queued` 需要 A3 的 `queueRequestId`。
- **A3（do-agent 侧，未发布，审计结论）**：doagent 仓库分支
  `doagent-admission-remediation`（基于生产 pin `08ecb01` = 0.2.42），含
  `bef0d9e`（A3.1/A3.2 精确 queue cancel + 独立全局队列 drain）、
  `8cfd28a`（A3.3 主动恢复 stale running turns）、`30cadc8`（A3.2 无 permit 时遵守
  全局队列上限）。审计认为三处修复成立但只覆盖症状：**P0 缺口**是
  `session/end`、`chat/delete` 触发 `clear_turn_runtime` 的运行中 turn 不会执行
  `finish_turn`，全局 permit 永久泄漏；该 session 已不在 store，A3.3 扫描看不到，
  默认 stale 超时 7200s 也太慢。发布前必须补齐 teardown 释放 permit 与终态可关联 id。
  版本只可能发 `0.2.45`（0.2.43/0.2.44 tag 已占用，且必须 rebase 到 0.2.44 线）。
- 发布链路 runbook：`docs/reviews/2026-11-10-doagent-admission-release-runbook.md`
  （生产线 `cnb/main`、tag 后必须 `cnb build start-build --event api_trigger_release`、
  `runtime-versions.env` pin + `test_doagent_runtime_lock.py` 硬编码需同 commit 修改、
  zyj upgrade 的 preflight/apply 与回滚点、D1–D7 待决策）。

## 6.1 验证后发现的独立阻塞：Gateway 用户 token 到期

- 恢复验证（09:09–09:18 Asia/Shanghai）全部通过；09:29 起所有 `doops` 命令改为报
  `gateway_auth_expired (auth_domain=gateway): Gateway user token has expired; Run doops login`。
- 本机登录记录 `~/.agent/skills/doops/auth.json`：`issued_at=2026-11-09T01:24:08Z`、
  `expires_at=2026-11-10T01:24:08Z`（24h token，无自动续签），到期时点正好落在验证之后。
- 这与 Agent 侧修复无关，但会挡住用户重跑被阻塞的 Helm 升级；必须先 `doops login`（或
  `login --token-stdin` 导入有效 token）再重试。不要把 managed credential 当 Gateway token 用。

## 7. 残余风险与后续

1. **同样的 wedge 可在任何仍运行 do-agent 0.2.42 的 target 上复发**；Pod 重建只清状态，
   不防复发。根治需要 A3 镜像 + A1 的 Agent 发布。
2. 发布前需要用户决策：do-agent 新版本号（用户当前在 `release/test-env-isolation-0.2.43`
   分支工作，另有 `release/permission-0.2.44`）、doops 新 tag、以及升级范围（只 zyj 还是全量 target）。
3. 用户被阻塞的"受管 Helm 升级"在恢复后应可直接重试；如需 DoOps 侧代跑，需要任务中的
   target/范围/授权 Chart 资产/验收标准。
4. 建议给 Agent 加一个"准入队列/permits 可见"的只读诊断（A4 方向），避免下次只能靠
   `kubectl logs` 判断。

## 8. 证据

- `docs/reviews/evidence/2026-11-10-zyj-admission-block/ask-before-recovery.txt`
- `docs/reviews/evidence/2026-11-10-zyj-admission-block/ask-after-recovery.txt`
- `docs/reviews/evidence/2026-11-10-zyj-admission-block/agent-log-excerpt-old-pod.txt`
- `docs/reviews/evidence/2026-11-10-zyj-admission-block/new-pod-log.txt`
- `docs/reviews/evidence/2026-11-10-zyj-admission-block/doagent-probe-after-reset.json`
- `docs/reviews/evidence/2026-11-10-zyj-admission-block/targets-zyj-after-recovery.txt`
- 相关设计/进度：`.worktrees/a1-remediation/docs/design/DOOPS-ASK-ADMISSION-REMEDIATION-PROGRESS-2026-11-09.md`
