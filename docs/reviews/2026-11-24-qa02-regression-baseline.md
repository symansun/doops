**[测试完成] QA-02 全仓回归基线（部分覆盖，见未覆盖项）**

# QA-02 全仓回归基线

## 1. 环境

| 项 | 值 |
|---|---|
| 基线 sha | 开工 b5765106（01:51 origin/main）→ 中途复核 b733e6fd（02:14）→ 提交时 origin/main = a8587ffc（新增长尾未覆盖） |
| 干净树 | git archive 到 /tmp/qa02、/tmp/qa02b（无 .git）；依赖 .git 的测试在 clone 同 sha 检出 /tmp/qa02-git、/tmp/qa02b-git 跑 |
| 是否共享树 | 否。共享工作区未参与测试（其未提交改动会让 test:console 两个失败：lib/shell-blocks.test.mjs:173 期望 done\n 得 done；lib/server/agent-session.test.mjs:371 超时 —— lead 提供，非本报告实测，非产品缺陷） |
| Go | 本机 go1.27.4 darwin/arm64（env -u GOROOT）；CI 与 backend/Dockerfile:13 固定 1.25.14 / golang:1.25-alpine |

## 2. 已跑命令与结果

| # | 命令 | 结果 | 耗时 |
|---|---|---|---|
| 1 | cd backend/agent && env -u GOROOT go build ./... | rc=0 | 1s(b5765106)/5s(b733e6fd) |
| 2 | env -u GOROOT go vet ./... | rc=0 | 2s/3s |
| 3 | backend/gateway、backend/cli go build ./... | rc=0 / rc=0 | 2s/1s |
| 4 | go test ./... -count=1 -timeout 20m（b5765106） | rc=1：internal/server 20m 超时 + 1 真失败；pkg/gatewaycmd 1 次争用失败；其余 ok | 1202s 到点 |
| 5 | go test $(go list ./... \| grep -v internal/server) | 全 ok（agent 7.3s、cmd/agent 6.3s、dispatcher 2.3s、gatewaycmd 8.7s、release 2.9s） | 10s |
| 6 | internal/server -run 'Monitor\|Skill'（干净树） | ok | 45s |
| 7 | internal/server -run 'Gateway\|Store\|MySQL' | ok | 171s |
| 8 | internal/server -run 'Agent\|Tunnel\|Relay' | ok | 193s |
| 9 | 单包 verbose 8m 自限复跑 | 到点 panic（481.6s）；到点前 1236 RUN / 524 PASS / 2 FAIL | 481s |
| 10 | python3 -m pytest -q test/（env -u GOROOT） | 4 failed / 507 passed / 8 subtests；失败全在 test/test_install_contract.py（缺 .git） | 260s / 252s |
| 11 | 同文件真实检出复跑 | 32 passed，rc=0 | 118s / 86s |
| 12 | verify.sh | b5765106 20/20 PASS；b733e6fd 34/34 PASS（含 14 项 SF-08） | 28s / 494s |
| 13 | 残留 grep（monitorStrategyCatalog/buildBuiltinStrategyArtifact/DriverMode/StrategyID/builtin_strategy） | 51 命中（b5765106）→ 27（b733e6fd），生产代码 0 使用 | — |

## 3. 失败项逐条

### F1 产品相关测试缺陷（低-中，非本批引入）TestDoSQLVersionJournalVerifiesHistoricalEscapedReplacementEntry
- 复现：cd /tmp/qa02-git/backend/agent && env -u GOROOT go test ./internal/server -run TestDoSQLVersionJournalVerifiesHistoricalEscapedReplacementEntry -count=1 -v → FAIL 0.00s
- 实际：backend/agent/internal/server/dosql_version_journal_test.go:223: test entry is not in the historical escaped form: {"version":1,...,"statements":["SELECT '??'"],...}
- 根因：go1.27.4 的 json.Marshal 对非法 UTF-8 直接写 U+FFFD 字节（ef bf bd），go1.26.2 写 \ufffd 转义（探针 /tmp/qa02/probe/main.go 两工具链对比）；测试用 json.Marshal 造"历史转义形态"夹具，对工具链敏感。换 go1.26.2 跑同用例 → PASS。
- 性质：测试对工具链版本的假设失效，不是产品逻辑缺陷（产品按原始字节哈希）。CI 固定 1.25.14 不受影响。
- 建议 owner：dev-dosql（265ae9a3）；台账建议 QA-02-1。

### F2 环境（并发争用）TestCredentialVerifyUsesStoredGrantScope/explicit_opaque_use_and_keys_stored_on_the_binding
- 争用时 FAIL 5.23s：backend/agent/internal/server/credential_verify_stored_scope_test.go:95: Post "http://127.0.0.1:57773/v1/credentials/opaque/verify": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
- 隔离复跑 → PASS 7.75s。非产品缺陷（5s 客户端超时 + CPU 打满）。建议 QA-02-3。

### F3 环境（并发争用）TestGatewayServeLocksDatabaseBeforeRecovery
- 全量跑 FAIL 0.22s：backend/agent/pkg/gatewaycmd/gatewaycmd_lock_test.go:32: gateway exited before health: exit status 1
- 隔离复跑 go test ./pkg/gatewaycmd → 全 PASS（该用例 1.46s）。非产品缺陷；建议失败时打印 gateway stderr（QA-02-4）。

### F4 残留（低）前端死类型
- lib/skills-registry.ts:16、:57-58、:219 仍定义 driver_mode: "builtin_strategy" | "agent_prompt"、strategy_id、SKILL_DRIVER_LABELS，全仓已无消费者（SF-05 已清组件侧）。建议删除（QA-02-2）。

### F5 产品缺陷（中，两条基线均确定性复现）TestAuditTimeRangePreservesSecondBoundary
- 复现：cd /tmp/qa02b-git/backend/agent && env -u GOROOT go test ./internal/server -run TestAuditTimeRangePreservesSecondBoundary -count=5 -v → 5/5 FAIL（0.04-0.06s）；同一用例在 b5765106 干净 clone 上 -count=2 也 2/2 FAIL，非本批引入。
- 实际：backend/agent/internal/server/upgrade_audit_test.go:389: purge crossed the cutoff: count=2 err=<nil>（期望 deleted=1：只删除 base-1s 那条；实际删了 2 条，把 cutoff 之后 500ms 的那条也删了）。
- 根因（已定位到代码）：backend/agent/internal/server/audit_purge.go:61/76 用字符串比较 "WHERE started_at < ?"，cutoff 由 backend/agent/internal/server/gateway_store.go:139-141 formatTime = RFC3339Nano 生成。base="2026-11-11T00:00:00Z"，base+500ms="2026-11-11T00:00:00.5Z"；字典序下 '.'(0x2E) < 'Z'(0x5A)，故 "…00:00:00.5Z" < "…00:00:00Z" 成立，cutoff 之后的行被误删。
- 影响：审计保留清理可删除 cutoff 之后最多 <1s 的行（有小数秒的行被判为更早），属审计证据丢失；同一缺陷也会影响 PurgeExpiredAudit/PurgeLocalAuditBefore（同一字符串比较路径 audit_purge.go:61/76/219）。
- 期望：按数值时间比较（如 started_at_ms 或 julianday/strftime 归一化），或把 cutoff 与存储值统一到固定小数位（如 9 位）后再比较；不要依赖 RFC3339Nano 的字典序。
- 建议 owner：dev-audit（audit_purge.go 作者）；建议台账编号 QA-02-5（中）。

> 共享树半成品：全部在 git archive 干净树执行，未遇到编译错误类半成品。

## 4. 未覆盖

1. internal/server 全量一次性跑完：1095 个 func Test，20m 到点（超时时在跑 TestSelfUpgradeRemovesStaleVolumeMounts），8m 自限同样到点；已用 3 组名称过滤 + 其余包覆盖，三组之外的用例（Credential/DoSQL/SelfUpgrade）只有 8m 窗口内 524 PASS 证据。
2. b733e6fd 的 go test ./...：02:25 时仍在 internal/server，未完成。
3. GitHub Actions 实际 CI 结果（本机无 go1.25.14）。
4. a8587ffc 及 b733e6fd 之后新增提交（43e8a8cb / bbe4b9c8 / e2e4afdf），未复跑。
5. 前端 pnpm test:console / pnpm build：未在干净树执行（两失败由 lead 提供，非实测）。
6. 真实 Gateway + Agent + 集群端到端。

## 5. 结论

当前 main 不具备"本机一次性全绿"的发布级证据，但没有发现阻塞性产品缺陷。可发布依据：三模块 build/vet 全绿、除 internal/server 外全部包 ok、internal/server 三分组全 ok、Skill 自校验 20/20→34/34、契约测试真实检出 32 passed。红灯：F1（测试自身对 Go 版本敏感）、F5（审计清理 cutoff 边界误删，产品缺陷，建议发布前修复），F2/F3 为并发争用；另 b733e6fd 的全量跑在 02:38 完成（internal/server 1389s，即 20m 超时在 CI 同参下也可能触发，建议 CI 提高 timeout 或分片）。发布判定建议以 CI（go1.25.14）为准。

## 6. 复现清单

\`\`\`sh
git archive b5765106 | tar -x -C /tmp/qa02
cd /tmp/qa02/backend/agent && env -u GOROOT go build ./... && env -u GOROOT go vet ./...
cd /tmp/qa02/backend/agent && env -u GOROOT go test $(env -u GOROOT go list ./... | grep -v internal/server) -count=1
cd /tmp/qa02-git/backend/agent && env -u GOROOT go test ./internal/server -count=1 -run 'Monitor|Skill'
cd /tmp/qa02-git/backend/agent && env -u GOROOT go test ./internal/server -count=1 -run 'Gateway|Store|MySQL'
cd /tmp/qa02-git/backend/agent && env -u GOROOT go test ./internal/server -count=1 -run 'Agent|Tunnel|Relay'
cd /tmp/qa02/backend && env -u GOROOT python3 -m pytest -q test/
cd /tmp/qa02 && sh backend/agent/internal/server/testdata/skills_builtin_verify/verify.sh
\`\`\`

日志：/tmp/qa02/、/tmp/qa02b/（agent.log、g1..g3.log、server-v.log、pytest2.log、verify.log、install_git.log、residual.txt），不进仓库。
