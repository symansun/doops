# DoOps ask 准入阻塞整改进度

- 任务书：[DOOPS-ASK-ADMISSION-REMEDIATION-TASKBOOK-2026-11-09.md](./DOOPS-ASK-ADMISSION-REMEDIATION-TASKBOOK-2026-11-09.md)
- 实施分支：`doops-admission-remediation`（worktree `/tmp/doops-admission-remediation`）
- 基线：`66769eef`（main，2026-11-09 16:23 +08）
- 分支内容：`8e7d6e0a`（A1 初版）+ `d56ada0d`（A9，cherry-pick 自 `0c6f539d`）+ `16f5f81d`（进度）+ `d8352e27`（A1 rev2）+ `2fbb150f`（relay 集成测试 + 文档修正）
- 状态：A0 完成；A1 经独立评审打回 → rev2 修复 → 复评 **approve-with-followups（无阻塞项）**；A9 完成；其余未开始
- 本文件只记录已执行的动作与可复现证据；未执行的包不写成已完成

## A0 工作区、变更归属与发布冻结

已完成：

- 用户工作区未改动。`main` 为 `66769eef`，脏项为 1 个 ` M README.md` + 33 个未跟踪文档/证据目录；整改全部在干净 worktree 进行。
- 新 worktree 与分支：
  - `/tmp/doops-admission-remediation` → `doops-admission-remediation`（A1 + A9 合入目标）
  - `/tmp/doops-docker-start-failure` → `doops-docker-start-failure`（A9 实施，保留独立提交）
- runtime pin 与线上事实一致（任务书 V1）：`backend/runtime-versions.env` 为 doagent `0.2.42 / 08ecb0193767f559844d985cc7365484c61bf9c5`；线上 oilan Agent 为 `v1.3.123 / 8683d671…`，内置 doagent `0.2.42`（`do-agent --version` 与 `GET /health` 双重回读）。
- `/tmp/doops-ask-queued-fix` 旧补丁不再作为基线：它对 queued/no_prompt 使用 session-wide cancel，不满足任务书 A1.7 与 §3.1 的请求级 ownership 要求；A1 已按任务书重新实现。
- 任务书命令纠偏（实施记录，不改任务书）：§A1 的验证命令写作 `cd doops.sh/backend && go test ./agent/internal/server`，但 Go module 根在 `backend/agent`（`module github.com/user/doops/agent`）。实际命令为 `cd backend/agent && go test ./internal/server -count=1`。

## A1 Gateway 准入分类止血（D1）

实现：commit `8e7d6e0a`。

- 新增 `backend/agent/internal/server/doagent_prompt_admission.go`：把 `session/prompt` 应答解析为单一类型
  `accepted | queued_no_turn | no_prompt | rejected | transport_failed | malformed`，加上队列作用域
  `session | global`；删除旧的 `doagentPromptTurnID` / `doagentPromptRejected` 散落解析。
- `handler_ws.go`：按类型映射 wire error；发送前拒绝空 prompt（`-32602 doagent_prompt_no_prompt`），
  该路径不创建 session、不发 cancel。
- 归属规则（pinned doagent 0.2.42 没有 request-scoped cancel，唯一清理原语是 session-wide cancel）：
  - `queued` + `globalPosition` + 本次请求新建的专用 session → 发一次 `session/cancel` 清理后返回
    `doagent_prompt_queued`（未准入、无 turn、可换新 session 重试）；
  - 只有 `queuePosition`（per-session FIFO，说明该 session 还有 turn）或复用 session → 不取消，
    返回 `doagent_prompt_queue_cleanup_unknown`（本请求无 turn，但后台是否仍执行不可证明，禁止自动重试）；
  - `no_prompt` → `-32602 doagent_prompt_no_prompt`，不发 cancel；
  - `accepted` 缺 turnId、result 非对象/null、未知 status、transport 失败 → 保留
    `execution_outcome_unknown` 语义（`prompt_turn_id_missing` / `prompt_transport_failed`）；
  - 准入日志只含 category、队列作用域/位置、RPC code，不含 prompt 内容或凭据。
- 文档：`backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md`（该主题权威文档）新增"准入前拒绝不是未知结果"规则。

独立评审记录（重要）：初版 `8e7d6e0a` 被评审 **reject**，四项发现全部成立：

1. **Gateway 端到端重写（critical）**：Agent 返回的新类别在 Gateway 进程被
   `remoteMutationFailed` 重写成 `-32008 execution_outcome_unknown`，`ask --apply` 仍显示原症状；
   审计也丢掉类别。
2. **归属判定不成立**：`entry == nil` 不是"本次请求独占 session"的证明；并发首次 prompt 可同时满足，
   doagent `session/new` 会复用同 id → session 级 cancel 可能打到别人的 turn/排队项。
3. **wire 形状变化**：error 与 result 并存时旧代码按 transport-unknown 处理，新分类器误判为 rejected。
4. **文档过度声明**：把"queued 不产生 unknown / 不必回读 mutation"写成无条件事实。

rev2 `d8352e27` 的修复：

- `execution_lifecycle.go` 新增 `promptPreAdmissionReply`，`remoteMutationFailed` 对四个准入前稳定类别
  返回 false → Gateway 原样转发；未知原因仍保留 mutation-unknown 护栏。
- `tunnel_hub.go` 新增 `relayErrorAuditText`，prompt 审计记录稳定类别而非格式化 envelope
  （后者会被 sanitize 成 `credential_operation_failed`）。
- queued 清理只对"本次请求铸出唯一 session id"的 `response_format=json` prompt 执行（session id 增加
  递增后缀）；流式/复用 session 一律不 cancel，返回 `doagent_prompt_queue_cleanup_unknown`。
- 分类器恢复 error+result 的保守优先级（transport failure）。
- 文档重写为可证明的表述，并明确"共享 session 的可重试 queued"依赖 doagent 请求级取消（A3）。

验证（本分支实测）：

- 新增定向测试：`TestClassifyDoagentPromptAdmission`（15 个应答形状）+ 6 个 WS 级行为测试
  （global queued、per-session queued、复用 session、no_prompt、empty prompt、accepted 缺 turnId）→ ok
- 回归：`go test ./internal/server -run 'TestAgentPrompt|TestAgentCancellation|TestAgentApply|TestAgentSSE|TestDoagent|TestSubscribe'` → ok（19.3s）
- 全量：`go test ./internal/server -count=1` → ok，549.858s（exit 0）
- rev2 定向测试：`TestPromptPreAdmissionCategoriesAreNotMutationUnknown`、
  `TestPromptAuditKeepsTheAdmissionCategory`（Gateway 侧类别转发/审计）、
  `TestAgentPromptStreamingQueuedSessionIsNotCancelled`、
  `TestAgentPromptMachineResultQueuedCleansItsOwnSession`、error+result 优先级 → ok
- rev2 回归：`go test ./internal/server -run 'TestAgentPrompt|TestAgentCancellation|TestAgentApply|TestExecution|TestPrompt|TestClassifyDoagent|TestDocker|TestRelay|TestAgentSSE|TestDoagent|TestSubscribe'` → ok（48.2s）
- `git diff --check` → CLEAN
- final 全量（`2fbb150f` + 本进度提交）：`go test ./internal/server -count=1 -timeout 900s` → ok，524.436s（exit 0）
- rev3：新增 `TestGatewayRelayForwardsPromptAdmissionCategory`（GatewayHub relay 端到端：apply 收到
  `-32603 doagent_prompt_queued` 必须原样到达客户端；未知原因的 apply 失败仍被改写成
  `execution_outcome_unknown`）→ PASS；文档补充 `globalPosition` 前提与凭据回滚例外。
- 文档契约：`python3 -m pytest backend/test/test_natural_language_deployment_contract.py backend/test/test_stale_contracts.py backend/test/test_doc_links.py` → 10 passed，1 failed（见"基线既有失败"）

复评结论（rev2 `d8352e27`，delta review）：**approve-with-followups，无阻塞项**。

- Gateway 转发覆盖 relay 终态路径（两处调用点），客户端拿到原样类别，审计保留类别。
- 铸出 session 的唯一性成立，流式/复用 session 不再有任何 session 级 cancel。
- 文档不再过度声明；error+result 恢复保守优先级；无护栏回归。

复评遗留 followups（记录，待后续包处理）：

1. `doagent_prompt_queue_cleanup_unknown` 需要被 A2 的前端错误契约覆盖（任务书 A2 清单未列出该类别），并按"禁止自动重试、先核对队列/审计"处理。
2. cleanup_unknown 目前审计状态为 `error` 而非 `outcome_unknown`，且系统没有自动的 unresolved-execution 围栏；唯一保护是"禁止自动重试"规则。是否升级为不同审计终态需要在 A4/A2 里决定。
3. `relayErrorAuditText` 现在保留 Agent 的 prompt 错误原文（Agent 侧均为常量类别，风险低）；若未来 Agent 错误文本可能含敏感内容，需要在 Gateway 侧加类别白名单。
4. 凭据准备回滚失败仍会产生 `credential_apply_outcome_unknown`（真实凭据不确定护栏，不属于本次 P0）。

未覆盖（显式依赖 A3 与后续包）：

- request-scoped cancel/token、"queued 的 claim-and-wait"、以及"清理已确认"的线上语义。当前实现对复用
  session 的 queued 只能返回 cleanup_unknown，这是有意的 fail-closed，不是遗漏。
- 前端错误类别映射（A2）、target admission 容量观测（A4）、runtime 契约门禁（A5）。

## A9 doops_docker 启动失败伪成功（R18）

实现：`0c6f539d`（独立分支），已 cherry-pick 为 `d56ada0d`。

- `handler_ws.go` `case "doops_docker"`：`cmd.Start` 失败从 `buildSuccessResponse` 改为
  `buildToolErrorResponse`，与既有 `doops_shell` 修复 `0043d1b9` 一致；消息文本、危险字符拒绝和
  docker 其余路径不变。
- 新增 `docker_command_outcome_test.go`：table-driven 覆盖六个终态（启动失败、运行时不可用、成功、
  非零退出、取消、outcome unknown）；把修复回退后启动失败用例会失败，证明回归测试有效。
- 验证：定向 `TestDockerToolCommandOutcomes` ok（13.1s）；全量 `go test ./internal/server -count=1`
  ok（548.5s）；`git diff --check` CLEAN；gofmt 干净。
- 未闭环项（记录，不在 A9 范围内改）：`doops_docker` 不在 Gateway `actionForTool` 中，经 Gateway RPC
  实测返回 `-32601` 且不产生 Gateway 审计行。因此"审计状态为 error"目前只在响应契约层成立
  （`buildToolErrorResponse` → `result.isError=true`，是 `tunnel_hub.go` 映射审计 error 的输入）；
  打通 Gateway 路由属于授权/路由变更，需单独立项。

## 基线既有失败（非本轮引入）

`backend/test/test_stale_contracts.py::test_retired_guidance_does_not_reappear` 在**干净的
`66769eef` main** 上就失败：`README.md` 含已退役的安装指引（"单机或没有 DaemonSet 的节点，
推荐标准安装" 等）。用 detached worktree 在 pristine HEAD 复现，确认与本轮改动无关。
未在本轮修复：`README.md` 在用户工作区仍有未提交改动，且不在 A1/A9 的文档职责内；建议按 A12
（文档契约收敛）单独立项处理。

## 未开始的包

A2（Web 错误契约与浏览器验收）、A3（doagent permit/queue/stale 根修）、A4（观测与 targets 契约）、
A5（runtime pin 与协议矩阵门禁）、A6（受管升级 controller）、A7（OILAN 发布恢复）、A8（modelProvider
凭据迁移）、A10（handler_ws 外科拆分）、A11（enrollment T2）、A12（权限/模式文档收敛）。

阻塞与依赖：

- A7 依赖用户两项业务裁决（`0fdd9b1a6e3` chart vs `5ca4f114f29` 重跑；是否接受 `AuthzLoginView`
  替换线上自研注册登录页），以及 DoOps ask 通道恢复/并行 target lease 清空。
- A3 在 `/Users/wwyz/Documents/l8ai/doagent` 实施；该 checkout 当前在 `release/test-env-isolation-0.2.43`
  分支且带未提交改动，需要独立 worktree，不能直接在该树上改。
- A2/A4 需要可运行的 Web console 与浏览器验收路径；当前尚未建立本轮验收环境。
- A5 需要 CNB CI 与发布链路；A6 需要目标集群的 controller 能力与 rollback baseline 可拉取性。
