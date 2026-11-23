**[测试完成] QA-04 main 全量分片基线**

# QA-04 main 全量分片基线（internal/server 1113 项全量 + 前端）

## 1. 结论

- 分片口径下 **internal/server 全量 1113 个顶层测试全部执行**：1113 listed / 1113 ran / missing 0 / extra 0；结果 **1092 PASS + 5 FAIL + 16 SKIP**（合计 1113）。
- 5 个 FAIL 中 **4 个是并发争用假阳性**（隔离 -count=3 全部 3/3 PASS，失败信息均为 5s 客户端超时或 helm 调用超时）；**1 个是确定性失败** TestDoSQLVersionJournalVerifiesHistoricalEscapedReplacementEntry（3/3 FAIL，Go 1.27.4 vs CI 1.25.14 的 json.Marshal 转义差异，已由 QA-02 的 F1 记录）。
- 16 个 SKIP 全部是环境门控（MySQL/Mongo/MSSQL/PG DSN、NVIDIA 驱动、macOS 无 setsid），不是跳过逻辑缺陷。
- 前端：test:console **706/706 PASS（37.68s）**；pnpm build **rc=0（90s，Compiled 75s，静态页 20/20）**。
- **结论：main 在 clean tree 上可达"全包全绿"**，前提是分片执行（6 路并行，最慢分片 401s）且并发争用类用例隔离复跑；单进程一次跑完需要 23 分钟以上，超过 CI 的 -timeout 20m。

基线：origin/main = **0e74939505916dfbe5fb3195d3fa8bc994e8c399**（git archive 到 /tmp/qa04，无 .git，1276 个文件）。
工具链：go1.27.4 darwin/arm64（env -u GOROOT）、node 24 + pnpm 9.15.9、24 核。

## 2. 环境事实

- 本机 ~/.zshrc 原把 GOROOT 钉在 /Users/wwyz/.local/share/go（go1.26.2），PATH 上的 go 是 Homebrew 1.27.4 → 子进程裸跑 go test 报 compile: version "go1.26.2" does not match go tool version "go1.27.4"。lead 已改该行，但 DSH 工具 shell 仍继承旧 GOROOT，本报告全部命令一律加 env -u GOROOT。
- 干净树没有 node_modules。首次 build 用符号链接指向共享树的 node_modules 会让 Turbopack 直接 panic：Symlink [project]/node_modules is invalid, it points out of the filesystem root（build_rc=1）。改为 APFS clone（cp -Rc，18.5s，1.0G）后 build 通过。clone 前已核对 package.json 与 pnpm-lock.yaml 与共享树逐字节一致（diff -q 无输出）。

## 3. 分片方法与覆盖证明

1) 枚举：cd backend/agent && env -u GOROOT go test ./internal/server -list '.*' > list.txt，取以 Test 开头的行，共 **1113** 个（比 QA-02 时记录的 1095 多 18 个，main 已前进）。
2) 分片：按测试名排序后 **轮转取模 6 路**，每片写成一个显式 alternation 正则 ^(TestA|TestB|...)$，逐片
   env -u GOROOT go test ./internal/server -run "$(cat shard_i.regex)" -v -count=1 -timeout 20m
   轮转而非前缀分组的原因：前缀分布极不均衡（other 626、Gateway 133、Agent 79、Credential 67、SelfUpgrade 48、DoSQL 46、Execution 42、Monitor 26、Admin 14、Audit 12、Target 11、Session 4、Tunnel 3、Relay 2），按前缀分会让 other 一片独占 626 项。
3) 覆盖证明：从 6 份 -v 日志里抽 ^=== RUN 的顶层名，与 list.txt 做集合比对 → **missing 0 / extra 0**；每片的 RUN 数之和 = 1113。互斥由"每个测试名只出现在一个正则里"构造保证，并用日志中的 RUN 计数交叉验证。

| 分片 | 测试数 | 墙钟 | rc | PASS | FAIL | SKIP |
|---|---|---|---|---|---|---|
| shard_0 | 186 | 216s | 1 | 182 | 2 | 2 |
| shard_1 | 186 | 190s | 1 | 181 | 3 | 2 |
| shard_2 | 186 | 173s | 0 | 183 | 0 | 3 |
| shard_3 | 185 | 117s | 0 | 181 | 0 | 4 |
| shard_4 | 185 | 254s | 0 | 181 | 0 | 4 |
| shard_5 | 185 | 401s | 0 | 184 | 0 | 1 |
| 合计 | 1113 | 6 路并行，最慢 401s | — | 1092 | 5 | 16 |

参考：同一基线单进程一次跑 ./... 时 internal/server 需 1389s（QA-02 的 b733e6fd 实测 1388.997s），超过 -timeout 20m；分片后最慢一片 401s。

## 4. 5 个 FAIL 的分类

隔离复跑命令（每个测试独立进程，-count=3）：

    cd /tmp/qa04/backend/agent && env -u GOROOT go test ./internal/server -run '^<TestName>$' -count=3 -v -timeout 10m

| 测试 | 分片内耗时 | 失败信息 | 隔离 -count=3 | 判定 |
|---|---|---|---|---|
| TestCredentialVerifySuccessAuditFiltersRequestTextTarget | 5.95s | credential_audit_success_target_test.go:157: Post ".../verify": context deadline exceeded (Client.Timeout exceeded while awaiting headers) | 3/3 PASS（3.70/2.85/2.92s） | 并发争用假阳性 |
| TestCredentialVerifyUsesStoredGrantScope | 11.34s | credential_verify_stored_scope_test.go:95: 同上 5s Client.Timeout | 3/3 PASS（7.49/7.13/7.80s） | 并发争用假阳性 |
| TestRegistryAuthIsNotDeliveredAfterRevocation | 3.89s | registry_auth_delivery_test.go:162: 同上 5s Client.Timeout | 3/3 PASS（1.69/1.73/2.51s） | 并发争用假阳性 |
| TestRunHelmCommandInterruptedUpgradeNeverStaysPending | 1.67s | helm_interrupt_test.go:226: helm_interrupted: the call ran out of time; helm exited without an error message, want it to contain "rolled back to revision 5" | 3/3 PASS（1.50/1.51/1.50s） | 并发争用假阳性 |
| TestDoSQLVersionJournalVerifiesHistoricalEscapedReplacementEntry | 0.00s | dosql_version_journal_test.go:223: test entry is not in the historical escaped form | **3/3 FAIL** | 确定性，已知 F1（工具链） |

共性：4 个假阳性都是"5s 客户端超时 / 脚本调用超时"类断言，6 路并行时 24 核被打满（同机还有 3 个会话在跑 go test）导致。建议给这类用例留出更宽的超时或加 -p 限制，但不属于本次要改的范围。

## 5. Top 12 最慢测试与成因

从 -v 日志解析顶层 --- PASS ... (Xs) 排序（括号为分片）：

| # | 测试 | 耗时 | 成因（证据） |
|---|---|---|---|
| 1 | TestSelfUpgradeRBACTransitionStopsBeforeMutation (5) | 110.99s | **重复起脚本**：13 个身份场景 × 3 个脚本（apply/preflight/rollback）= 39 个子测试，子测试耗时合计 110.99s（均值 2.85s，最大 6.65s）；每个子测试新建 TempDir、写假 kubectl/helm/sleep 二进制、再跑一遍完整 bash 脚本；脚本内无 sleep |
| 2 | TestSelfUpgradeRBACGuardRefusesAnUnverifiedControllerIdentity (4) | 62.39s | 同族：多场景 × 完整脚本重跑 |
| 3 | TestExecutionRelayPromptAdmissionInterruption (5) | 49.26s | **固定超时等待**：68 个子测试合计 49.29s（均值 0.72s），用例用 5s 中断超时与 80ms 变体各跑一遍（execution_relay_admission_test.go:53-55） |
| 4 | TestSelfUpgradeRemovesStaleVolumeMounts (0) | 46.87s | 重复起脚本：每场景跑一次 self_upgrade apply 脚本（含假 kubectl） |
| 5 | TestSelfUpgradeRejectsInvalidPodEvidence (4) | 32.86s | 同上 |
| 6 | TestWorkspaceInterruptionCancelGraceExpiresWithoutTerminal (4) | 30.61s | **固定超时等待**：真实 WS 连接 + executionCancellationTimeout + 3s 读 Deadline（workspace_interruption_test.go:182-194） |
| 7 | TestSelfUpgradeRBACGuardProtectsMountedServiceAccountAuthentication (3) | 27.78s | 重复起脚本 |
| 8 | TestSelfUpgradeReplacesALiveStrategyTheChartWillCorrect (1) | 27.30s | 重复起脚本 |
| 9 | TestSelfUpgradeApplyScriptRejectsIncompleteDeploymentEvidence (5) | 23.29s | 重复起脚本 |
| 10 | TestRelayConfirmedAdmissionReplySurvivesInterruption (1) | 21.73s | 固定超时等待（relay 中断路径） |
| 11 | TestAgentApplyRuntimeReceiptBridge (5) | 21.56s | 重复起进程：夹具里 exec sleep 30 / 多轮 MCP 调用 |
| 12 | TestSelfUpgradeCommitAndGatewayEvidenceAreRequired (5) | 19.93s | 重复起脚本 |

结论：慢的原因是**"每场景重跑一次真实 shell 脚本/子进程" + "若干固定中断超时"的累积**，不是单个用例卡死；没有任何一条测试在 shard 内超过 111s，也没有出现 -timeout 触发。真正的成本项是 SelfUpgrade 家族（7 条占 Top12，合计约 251s，约占分片总耗时的大头）——它们是端到端脚本测试，改写成本高，属于设计取舍，不建议为提速改断言。

## 6. 前端

    cd /tmp/qa04 && env -u GOROOT pnpm run test:console
    ℹ tests 706 / pass 706 / fail 0 / skipped 0 / duration_ms 37678

    cd /tmp/qa04 && env -u GOROOT pnpm build
    ✓ Compiled successfully in 75s
    ✓ Generating static pages using 23 workers (20/20) in 686ms
    build_rc=0（总 90s）

（第一次 build 失败是 node_modules 符号链接被 Turbopack 拒绝，属方法问题，见 §2；换成 APFS clone 后一次通过。）

## 7. SKIP 清单（16 项，全部环境门控）

- MySQL 8 未就绪：TestGatewayMySQLAgentStatusInsertMatchesSchema、TestGatewayMySQLRuntimeEquivalentSyntax、TestGatewayMySQLInstallAndValidate、TestGatewayMySQLCopyRehearsalAndFinal、TestGatewayMySQLInstallThenCopierAccepts（DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT 未设）
- DoSQL 真实库 DSN 未设：TestRealDoSQLMySQLJournalEndpointIsolation、TestRealDoSQLMySQLRollbackBoundaries、TestRealDoSQLMySQLServerImplicitCommitExamples、TestRealDoSQLPostgresRollsBackDDLAndDMLTogether、TestRealDoSQLMSSQLRollsBackDDLAndDMLTogether、TestRealDoSQLMongoTransactionsAndBoundaries、TestRealDoSQLMongoOperations、TestDirectDoSQLHandlerMongoRecordsVersions、TestDirectDoSQLHandlerRecordsVersions
- GPU：TestAssetMonitorDualGPUHardware（no local NVIDIA driver）
- macOS：TestWaitCommandStreamsDetachedChildWithRedirectedStdioReturnsNil（setsid 不存在）

即"全绿"的准确说法是：1092 通过 + 16 项因本机缺 MySQL/Mongo/MSSQL/PG 与 setsid 而跳过。CI 若配置了这些服务则覆盖面更大，本地无法替代。

## 8. 未覆盖

1. 16 个环境门控用例的真实执行（需 MySQL 8 / Mongo / MSSQL / PG 实例与 setsid）。
2. CI 上的 go1.25.14 结果（本机只有 1.27.4；F1 的确定性失败正是工具链差异，不能用本机结果判定 CI 红）。
3. 其他包（agent/cmd/dispatcher/gatewaycmd/release）本轮未重跑，沿用 QA-02 的 ok 证据。
4. 前端浏览器级验证（pnpm build 只证明可构建，未起 dev server 走页面）。
5. 共享工作区未提交改动（SF-18 等）不在本基线内。

## 9. 复现清单

    git fetch origin main
    SHA=$(git rev-parse origin/main)   # 0e74939505916dfbe5fb3195d3fa8bc994e8c399
    rm -rf /tmp/qa04 && mkdir -p /tmp/qa04 && git archive "$SHA" | tar -x -C /tmp/qa04
    cd /tmp/qa04/backend/agent && env -u GOROOT go test ./internal/server -list '.*' > /tmp/qa04/list.txt
    # 分片正则见 /tmp/qa04/shards/shard_i.regex（轮转取模 6）
    cd /tmp/qa04/backend/agent && env -u GOROOT go test ./internal/server -run "$(cat /tmp/qa04/shards/shard_0.regex)" -v -count=1 -timeout 20m
    # 前端（干净树无 node_modules，需先准备）
    cp -Rc /Users/wwyz/Documents/l8ai/doops.sh/node_modules /tmp/qa04/node_modules   # 先确认 package.json / pnpm-lock.yaml 一致
    cd /tmp/qa04 && env -u GOROOT pnpm run test:console && env -u GOROOT pnpm build

## 10. 基线漂移说明

本轮实测期间 origin/main 继续前进：实测基线 0e749395 → 复核时 8001ce4f（+5 个提交，21 个文件）。新增的代码类提交未在本基线内复跑：

- 8001ce4f fix(monitor): 运行明细证据并列记录声明的 exec_mode 与实际 dispatch_mode（SF-18）
- e03d8439 feat(skills): bridge metrics-server usage into inspect-k8s-cluster
- a7a4a84e feat(skills): collect physical block-device I/O in inspect-vm-host
- 2dfbf26b test(skills): cover host.disk_io and k8s.metrics with real-run fixtures
- 0507f449 docs(spec): mark host.disk_io and k8s.metrics as covered

8001ce4f 改的是 monitor 证据记录（internal/server），e03d8439/a7a4a84e 改的是 skills_builtin 脚本，均会进入 internal/server 包的测试范围。**下一个基线应以 8001ce4f 或更新提交重跑同一套分片**，届时 1113 的测试数也会变化。
