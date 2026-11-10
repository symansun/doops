# DoSQL 执行、记录、版本与权限审查（2026-11-10）

状态：已用真实 MySQL/PostgreSQL 凭据实测执行、dry-run、真实 apply、evidence、version journal 和审计；修复了 database 选择摩擦；确认真正的阻塞点是自然语言 `--request` 路径，而不是权限系统本身。

## 结论先行

1. **直接 SQL 能有效执行**：`--mode query/apply --sql ...` 在 MySQL、PostgreSQL 均返回真实结果；非管理员只要拥有 target grant 也能执行，不需要额外 credential grant。
2. **SQL 有记录**：每次操作的 evidence JSON 保存 statement、columns、rows、credential、fingerprint、operation_id；真实 apply 另有 hash 链 version journal。
3. **版本管理真实存在**：每个 database 独立 journal；真实 apply 先 `begin` 取 version，再执行，再 `end` 记录 committed/verified；query/dry-run 不占 version。实测真实 no-op apply 得到 `version=4`，journal 从 6 行变 8 行，begin/end 都有 hash/prev_hash。
4. **真正的“卡”不是权限，而是入口选择**：`--database` 传了 credential/binding 名而不是 database 名；Gateway 的 binding 元数据 `database_engine/database_name` 为空；自然语言 `--request` 路径会反复做 database asset / live-snapshot 发现，180s 都没完成。

## 实测证据

### 1. 直接查询（MySQL / PostgreSQL）

- MySQL `lab_management`：`select 1`、`information_schema.columns` 均返回真实 rows（如 `accessory_equipment/id` 等）。
- PostgreSQL `agentcloud`：`select 1`、`information_schema.columns` 均返回真实 rows（如 `agent_sessions/id` 等）。
- 证据：CLI JSON `result.statements[0].rows` 有值；不是只有 status。

### 2. 非管理员权限

- 用非管理员操作员的 token（非 admin，仅 target grant）执行 `sql --mode query --database lab_management --sql 'select 1'`：rc=0，返回 operation_id / engine / credential / evidence_path。
- 说明运行时只要求 target grant + target 上绑定的 database credential，没有 per-credential grant、没有每次询问管理员。

### 3. dry-run

- `UPDATE accessory_equipment SET id=id WHERE 1=0` + `--verify 'SELECT 1'` + `--dry-run`：
  - `result.status=dry_run_rolled_back`，`committed=false`，无 version，journal 行数保持 6。

### 4. 真实 apply（no-op）

- 同一 no-op UPDATE 真实执行：`result.status=verified`，`committed=true`，`version=4`，返回 evidence path。
- version journal：6 → 8 行，`begin`/`end` 各有 `statement_fingerprint`、`statements`、`status=verified`、`committed=true`、`prev_hash`/`hash`。
- evidence JSON 含完整 statements 和结果，operation_id 可追溯。

### 5. 自然语言 SQL（问题）

- `sql --mode query --database lab_management --request '只读查询：列出当前数据库中的表名...'` 180s 超时。
- Agent 日志显示循环：反复 `mcp_dosql_read` → `dosql_database_asset_not_found` → live-snapshot repair → 再试。
- 这会造成“不能有效执行/一直卡”的体感；直接 `--sql/--file` 路径没有这个问题。

## 权限/记录/版本的设计判断

- **权限**：没有层层卡权限。目标授权（target grant）打开该 target 上所有操作和绑定凭据；DB 选择失败被错误分类为 `credential_operation_failed` / `not_bound` 时，看起来像权限问题。
- **记录**：target 侧 evidence + version journal 是完整记录；Gateway `audit_events` 只记 `doops_agent_prompt operation=apply skill=dosql`，不含 SQL 文本；CLI 本地 `history.jsonl` 只记 `dosql apply`/`dosql ask`，也不含 SQL 文本。
- **版本**：每个 DB 独立 journal；query/dry-run 不占版本；真实 apply 预分配版本，失败/崩溃会留下 begin 无 end，便于恢复。
- **缺口**：没有面向用户的 CLI 命令查看 version journal/evidence 历史；用户只能从 apply 输出的 `version`/`evidence_path` 感知。

## 本轮代码修复

1. `--database` 现在接受 **唯一的 credential/binding 名** 作为别名，解析到其 database；不会再把用户从 `credential list` 复制的名称报成 not_bound。
   - 修改：`backend/agent/internal/server/dosql_direct_prompt.go`
   - 新增测试：`backend/agent/internal/server/dosql_binding_alias_test.go`
2. 错误文案改为 `pass --database <database> (name=engine/database)`；CLI next hint 同步。
3. `DOOPS_USAGE_GUIDE` 补充 `--database` 语义、binding 别名、read/write 的 `--storage gateway` 说明。
4. 修复 6 处会误导的引导/错误信息（见上一轮报告）。

注意：alias 修复在 agent server 代码里，**要滚动 Agent 镜像后才在线上生效**；当前线上 zyj-test Agent 仍是 release build，实测直接传 database 名可用，传 binding 名仍会走旧错误。

## 建议

1. 生产使用确定 SQL 时优先 `--sql/--file`；自然语言 `--request` 在未修资产发现前不适合关键操作。
2. 修复 NL 路径：在 `--database` 已给出时直接解析已绑定 credential，避免让模型再做 database asset discovery。
3. 在 `credential configure/list` 写入并展示 `database_engine/database_name`，消除 binding 元数据为空。
4. 给 `doops sql` 增加只读的 journal/evidence 查询（例如 `--mode history`），或在 apply 输出中固定打印 journal 路径。
5. Gateway audit 记录 operation_id/fingerprint（不记录敏感 SQL 文本），与 target evidence 通过 operation_id 关联。

## 验证

- `go test ./internal/server -run 'DoSQL|DirectDoSQL|Binding'`：ok
- `go test ./...`（CLI）：ok
- `pytest backend/test`：349 passed，8 subtests
- `git diff --check`：干净
- `SKILL.md`：11969 bytes（<12000）

## 追加修复（第二轮，本轮已实现）

### 1. 自然语言 `--request` 循环的根因与修复

根因（取自 `/root/ws/verify-sqlnl/.agent/sessions/verify-sqlnl/messages.json`）：
- 模型先 discover，但随后用 **错配的 credential**（`zyj-agents-test-agentcloud-dosql`，Postgres）去查询 `database_name=lab_management`（MySQL），并自造/复用了其它集群的 `database_asset_id`。
- 工具只返回 `dosql_database_asset_not_found`，没有列出可用候选；模型无法纠正，于是反复 discover/猜 id，180s 超时。

修复：
- `authorizeDoSQLDatabaseAsset` 的 not_found/ambiguous 错误现在携带**可用候选**：`database_asset_id(engine,db,ns,source,status)`，最多 8 个；模型能据此选择或停止，不再无限猜。
- `buildSQLInstruction` 增加约束：只能使用本次 discover 返回的 asset id；候选 engine 必须与 credential 匹配；同一错误最多重试一次，第二次失败就停止并报告候选。
- `--database` 现在接受**唯一的 credential/binding 名**作为别名（agent 侧），减少用户从 `credential list` 复制名称导致的假权限错误。

### 2. 版本/证据可见性

- `doops sql` 真实 apply 的 JSON 增加 `journal_path`；配合已有 `version`、`evidence_path`，用户能看到版本 journal 与完整证据位置。
- `DOOPS_USAGE_GUIDE` 补充说明：apply 才有 version/journal，query/dry-run 只有 evidence。

### 3. 验证

- Agent DoSQL/asset/journal 测试：ok
- CLI `go test ./...`：ok
- `pytest backend/test`：349 passed，8 subtests
- `git diff --check`：干净

### 4. 仍未闭环（需要发布/部署）

- 以上 agent 侧修复（候选错误、alias、journal_path）**当前线上 zyj-test Agent 仍是 release build，尚未生效**；需要滚动 Agent 镜像后才可复测自然语言路径。
- CLI 侧 instruction 与 `--database` 文档也需要新 CLI release。
- 更彻底的 NL 修复：在 `--database` 已给出时，由 CLI/Agent 先确定性解析绑定凭据，避免模型自由选择 credential/asset；建议作为下一步。
