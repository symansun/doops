# DoOps 用户阻塞复评（2026-11-10）

状态：原评审发现 4 项 P1（3 项代码缺陷、1 项交付缺口）和 2 项 P2；已进入整改。
以下“现场证据”和缺陷章节保留评审时快照，当前进度及后续证据见本节。

## 后续实施进度

用户于本次评审后要求继续，进入代码整改。以下状态与上面的评审时快照分开记录。

- [x] doagent 当前 turn teardown 与独立队列派发；二轮 end/delete、多 session 长任务和队列超时已通过。
- [x] DoOps 准入与取消：复用既有 `params.mode=reject`，补齐 doagent 全局拒绝并在发送前检查
  `initialize.agentCapabilities.promptCapabilities.rejectWhenBusy=true`；不支持时明确失败，
  不发送 prompt、不走旧队列兼容路径。这样避免产生“失败后仍在后台排队”的请求。
- [x] 取消/超时与明确未准入终态的竞态；保留真实执行未知的保护，未知结果不触发确定失败的凭据回滚。
- [x] 复用 `runtime/gcStats` 提供实时准入状态；CLI `targets --admission` 回读内部名额/排队，
  普通 targets 明确标注 Gateway 计数的边界。
- [ ] 终审追加：队列出队后、创建 turn 前校验失败必须发布准确且唯一的失败终态。
- [ ] 运行产物、pin 和授权目标发布验收。不得猜测未发布镜像摘要或把源码测试写成线上成功。

### 职责收敛

| 层 | 当前职责与不变量 |
| --- | --- |
| doagent | 持有实际 turn permit、队列和终态；所有权是 session + turn，teardown 在移除 session 前结束当前 turn。 |
| DoOps Agent | prompt 前核对 rejectWhenBusy，固定 reject 模式；明确未执行与未知结果分开，未知准入不取消共享 session。 |
| Gateway | 保留原请求终态；取消 ACK 不是终态；未知结果不回滚可能仍在使用的凭据，也不自动重放。 |
| CLI | 显示 Gateway 操作计数；按需经只读 node-info RPC 回读 doagent 的实时名额、holder 和队列。 |

不引入旧协议 fallback，不通过 Pod 重启代替根因修复，也不以历史 unknown 永久封锁后续任务。
支持 rejectWhenBusy 的运行时与桥接必须成套交付；构建时实际 initialize 校验能力，防止旧 pin
被打进新 Agent 镜像。

### 本次续办证据

- 2026-11-10，Asia/Shanghai，`zyj` 只读回读仍为 do-agent 0.2.42，
  Agent revision `081f24ab4e92535bce87c23889e242a1ee5f0886`。
  `admission-review-20261110` session 的最小只读 ask 返回 `ADMISSION-REVIEW-20261110`，exit 0。
- doagent 首批代码已 push `origin/main`：
  `b28dad1ef20fdd0fab12cae8750dc550e946df9d`；fetch 与远端包含关系已核对。
  CI `cnb-p2j-1k732ata3` 构建快照中；最终运行时 pin 需等待终审补丁对应产物。
- doagent ACP 388 passed、2 原有 ignored；runtime 31 passed。
  包含 14 个 GC/队列/HTTP 受控功能场景，后续出队失败窗口另行验证。
- DoOps CLI 全套通过；Gateway 包通过；Python 契约 350 passed、8 subtests passed。
  第一轮 Agent 全套达到默认 10 分钟总上限，且发现旧测试仍要求共享 session 取消。
  这些断言已更新为共享/独占场景并定向通过；最终 Agent 全套正在用 20 分钟上限重跑。
- Gateway 48 个明确未执行屏障、16 个未知终态屏障、6 个凭据/审计场景及定向 race 通过。
  新诊断使用真实 WebSocket handler/HTTP runtime fixture；旧非 JSON 或缺失字段不得显示为空闲。
- 未升级 zyj，未执行业务 Helm mutation。当前只读 ask 成功不能证明线上已消除 permit 泄漏，
  受管 Helm 发布、回滚及目标升级仍需匹配产物后的线上验收。

本轮停止条件：上述代码路径的功能场景通过、跨仓协议一致、独立复核无新增阻塞，记录未交付环节。
每个修复最多进行三轮“修改→定向验证→复核”；同一外部阻塞连续出现三次则留下具体证据并升级，
不反复重试线上变更。线上只做已经授权的只读回读；版本发布需要可审查的实际产物与目标范围。

## 范围与验收

用户目标是判断准入整改后是否仍会阻塞日常查询与受管 Helm 发布。

1. 核对 CLI、线上 Agent、内置 do-agent、构建 pin 与发布仓库，确认修复是否实际交付。
2. 核对排队、取消、会话结束与请求终态，区分已修复缺陷和剩余代码路径。
3. 现场检查登录、只读 ask、复用 session、命令执行与 Helm 读取；不以模型摘要替代状态回读。

## 现场证据

时间：2026-11-10 21:39 起，Asia/Shanghai。目标路由为 `gw-zyj`（别名 `zyj`），
Gateway target 为 `doops-zyj/zyj`。所有远端调用使用显式 `-session`。

| 检查 | 结果 | 证据类型 |
| --- | --- | --- |
| `~/.local/bin/doops version` | CLI `v1.3.126`，commit `76abae08a36d920c0058486d2d8ab86880c4c121` | observed |
| `targets --target zyj` | 在线；Agent commit `081f24ab4e92535bce87c23889e242a1ee5f0886`；检查时 idle/active=0/queued=0 | observed |
| `exec --target zyj --cmd 'do-agent --version'` | `do-agent 0.2.42`，exit 0 | observed |
| 新 session 的最小只读 ask | `REVIEW-OK`，exit 0 | observed |
| 复用 session 的下一轮版本查询 | 返回旧的 `REVIEW-OK`，未回答版本问题 | observed，原因未确认 |
| 同 session 第三轮新标记 | `TURN-THREE-20261110`，符合请求 | observed |
| 另一新 session 的新标记 | `FRESH-20261110`，符合请求 | observed |
| 新 session 请求工具查询版本 | 模型调用只读工具，说明当前 ask 工具集没有 shell；未提供版本命令结果 | observed |
| `k8s --target zyj helm list -n ai` | `doops-agent`，revision 49，status deployed，Chart `doops-agent-0.3.0` | observed |
| `exec --target zyj --cmd 'kubectl auth can-i create deployments.apps -n ai'` | `yes`，仅证明此身份对此资源和命名空间的权限 | observed |
| `git ls-remote github refs/heads/main` | `7c4542f15bcdaf85fc7c80109eaab2ba7e308f4e` | observed |
| `git ls-remote cnb refs/heads/main` | `66769eef596b2e40d1dbfdbbc325d0c3695e7553` | observed |

先前事故中的 `gateway_auth_expired` 本次没有复现。单次旧回答尚不足以证明 SSE 串流；
后续两个标记测试通过，保留为待复现项。

## 已确认的剩余代码缺陷

代码基线：doagent `b7d5165` 加当前未提交整改；以下为独立审查后由主审回读确认的
`code-path proof`，没有在线制造故障，也没有运行这两个新并发复现场景。

### P1：GC 的旧 turn 所有权可能漏清当前 turn 的 permit

- 触发：同一 session 的 T1 完成，T2 正在执行时调用 `session/end` 或 `chat/delete`。
- `doagent/src/runtime/gc/heap_reclaimer.rs:18-33` 发现已有 session heap guard/ledger 后直接
  返回或 touch，owner 仍是首次创建时的 T1；新一轮 `start_turn` 不更新该 owner。
- `heap_reclaimer.rs:99-103` 使用旧 owner turn 调用精确清理；
  `doagent/src/acp/handlers/session.rs:1420-1430` 发现实际 runtime 属于 T2 就不释放。
- 启用 GC 时，`session/end` 和 `chat/delete` 依赖上述 cascade，没有再清理当前 turn；
  已从 session store 移除的对象也不再属于 stale scanner 的候选。
- 用户影响：结束/删除会话返回后，T2 的 permit 仍依赖原任务自行退出才释放。若任务仍卡在
  外部等待，后续用户请求继续受该名额占用影响；不能把 Drop guard 等同于 teardown 已完成。
- 最小整改：以 teardown 捕获的当前 turn 所有权执行清理，保留迟到旧 turn 不得清除替代 turn
  的约束；不要用历史 heap owner 代替当前 active turn。
- 验收：真实启用 GC，同 session 连续两轮，将 T2 暂停在受控等待中；分别执行 end/delete，
  验证返回时当前 permit 已恢复，迟到旧清理不会释放替代 turn 的 permit。

### P1：全局队列调度器等待完整 turn，导致其他排队项与超时被拖延

- `doagent/src/acp/handlers/session.rs:5705-5718` 在候选遍历中直接 await
  `handle_admitted_queued_prompt`，该函数进一步 await 完整 prompt 执行。
- `doagent/src/acp/http_server.rs:485-504` 的唯一常驻 pump 必须等该调用返回才再次处理
  tick 或 permit 释放通知；`session.rs:5512-5523` 的 stale recovery 也 await 相同调用。
- 具体场景：两个名额被独立 session A/B 占满，独立 session C/D 排队。A 完成后常驻 pump
  开始长任务 C；B 随后完成且自身没有本 session 排队项，因此不会触发
  `session.rs:5227` 的额外 pump。此时即使名额空闲，D 仍要等正在执行 C 的 pump 返回。
  此例假定 A 自身同样没有排队项，且期间没有其他尾部 pump/stale recovery 介入；
  C 超过队列等待上限、但未到 stale 阈值即可触发，不需假设异常退出。
- 用户影响：有空闲名额仍等待；剩余排队请求的超时判断也不能按配置期限运行。
  当前代码没有保证队列超时与执行耗时相互独立。
- 最小整改：队列原子认领与 turn 执行分开；派发后调度器继续扫描、处理超时，
  执行任务保留精确的 permit/取消/终态所有权。
- 验收：上述 A/B/C/D 功能场景中保持 C 长时间运行，再释放 B 的名额，D 必须及时开始；
  另测没有名额时未派发请求按期获得明确超时终态。

### P1：排队清理未确认后，后台请求失去跟踪且执行 lease 被释放

- 触发：流式 queued 响应无请求 token；或带 token 的请求级取消返回 `queued_not_found`/失败，
  例如排队项恰好在取消前被派发。
- `backend/agent/internal/server/doagent_prompt_admission.go:164-187` 返回
  `doagent_prompt_queue_cleanup_unknown`；`handler_ws.go:1635-1637` 随即结束 handler，
  deferred cleanup 关闭 SSE。
- Gateway 将该回复作为错误结束审计，在 `tunnel_hub.go:1434-1437` 结束 operation，
  `client_service.go:91` 释放 lease；此时只证明了清理未确认，没有证明 queued/active 请求结束。
- 用户影响：调用已报错，原请求仍可能稍后运行；`ask --apply` 的后台动作与后续任务
  不能再依赖原执行 lease 协调。禁止自动重试是必要提示，但没有闭合后台请求的生命周期。
- 这是既有整改进度文档记录过但仍未关闭的缺口。
- 最小整改：保留与该活跃请求关联的清理跟踪，取得请求级取消或权威终态证据后结束；
  不以“历史 unknown 审计”无限期锁死新任务，也不通过换错误码掩盖未结束的执行。
- 验收：用同步屏障让排队项在 cancel 前刚好获准，检查跟踪、Helm 执行证据及同 session
  后续操作；终态未知时不得自动重放 mutation。

### P2：取消/超时竞态仍可把明确未准入改写为 outcome_unknown

- `backend/agent/internal/server/tunnel_hub.go:1838-1854` 等到原请求终态后，
  `:1914-1924` 仍仅凭 apply 的 `uncertainIfInterrupted` 返回 unknown；
  `:1997-1998` 也会在 context 已取消时覆盖回复的准入类别。
- 触发：Gateway deadline/取消与 Agent 已确认 `queued_cancelled` 后返回的
  `doagent_prompt_queued` 等稳定未准入回复交错。
- 用户影响：原请求已经明确没有执行且队列清理成功，用户仍被要求回读变更，
  无法获得正确的可重试失败语义。
- 最小整改：取消收尾保留原请求终态证据，只对确定未准入且清理已确认的情况保留安全失败
  类别；已执行或清理未确认的 mutation 继续禁止自动重试。
- 验收：同步控制 deadline 与 queued-cancel 回复顺序，分别证明明确未准入、
  清理未知和真实执行中三种结果没有被混淆。

## 已确认的交付与诊断缺口

### P1：根因修复尚未进入实际运行版本，当前构建仍锁定旧运行时

- 证据：线上直接命令回读仍是 do-agent 0.2.42；`backend/runtime-versions.env:1-6`
  固定相同版本、旧源码 revision 和不可变镜像摘要。
- `backend/scripts/build-agent-image.sh` 和 `backend/Dockerfile` 读取该 pin；
  `backend/test/test_doagent_runtime_lock.py:22` 仍断言版本必须是 `0.2.42`。
- doagent 本地整改报告明确尚未发布到 zyj。GitHub 主线更新不等于 CNB 发布主线同步，
  也不等于目标已升级。
- 用户影响：当前简单请求可成功，但事故中的 permit/queue 根因尚未被线上修复消除；
  仅重新发布当前 DoOps 源码仍会携带旧运行时。
- 修复验收：发布包含生命周期修复的 doagent 不可变镜像，更新 DoOps runtime pin 和对应契约，
  经正确发布线构建并升级授权目标；回读真实二进制版本/源码 revision/镜像摘要，
  再验证并发、排队取消与受管 Helm 用户路径。

### P2：targets 的 idle/queued 不能说明 do-agent 准入状态

- 证据类型：code-path proof。
- `backend/agent/internal/server/tunnel_hub.go:2483-2529` 只统计 Gateway 的读写锁、
  资源排队与活动 session；`:2534-2554` 将这些值直接写入 targets。
- 返回结构没有 do-agent 的 permit 使用量、内部队列、排队等待时间或持有 permit 的 turn。
- 用户影响：内部阻塞时用户仍可能看到 `idle / active=0 / queued=0`，现有状态不足以定位
  卡在哪里，也不足以判断是否可以重启 Agent。
- 修复验收：为准入状态建立明确的只读诊断来源，区分 Gateway 执行与 do-agent 内部状态；
  用受控排队场景证明状态能显示占用者、等待项和终态。

## 本地验证

`go test ./cli/doops -run 'TestResolveLoginGateway|TestPersistVisibleGatewayRoutes|TestFirstLoginDiscovery' -count=1`
通过（`ok doops/cli/doops 0.481s`）。这只验证当前源码中的首次登录路径，不代表已安装 CLI 或线上交付。

## 整改顺序

1. 修复当前 turn 的 teardown 所有权和全局 pump 派发方式，先跑上述并发功能场景。
2. 闭合 queued 清理未确认的请求跟踪，以及取消与未准入终态的竞态分类。
3. 同步正确发布线、运行时 pin、版本化 CLI/Skill 和目标 Agent，回读实际运行产物。
4. 用只读查询、复用 session、并发排队取消、授权受管 Helm 发布与失败回滚完成验收；
   targets 补充准入状态，确保用户能自行定位阻塞。

## 验证边界

- 未执行 `ask --apply` 或业务 Helm mutation；本轮没有明确业务 Chart、镜像和变更对象。
  Helm list 和权限检查不能替代真实发布与回滚验收。
- 未在线制造 permit 耗尽、强制取消其他用户请求或重建 Pod。
- 未把 commit 差异认定为协议不兼容的根因。
- 生命周期和协议缺陷以代码路径确认；其新列出的并发功能场景尚未执行，不能宣称已复现或已修复。
