# DoOps 用法正确性审查：代码 → CLI → Skill（2026-11-10）

状态：已完成代码/CLI/Skill 对照审计与实测；修复了 4 处会误导用法的文档/错误信息；仍有 1 个发布阻断（v1.3.126 不含 gateway 首登）和 3 个 P1 待处理。

## 1. 审查环境

- 代码：本地 HEAD `d9ae9909`（包含 gateway 首登 `fc4b68ff`）；已发布 `v1.3.126`（commit `76abae08`）
- Gateway：`https://doops.l8ai.cn`（TLS 正常）
- 实测 target：`gw-test` → `doops-zyj/zyj-test`，在线，release_commit `76abae08`
- 隔离测试：登录/添加/删除/同步用临时 `HOME` + 临时 `DOOPS_CONFIG`；只读命令用主配置；文件/工作区测试用临时 session 并 clean
- 除 1 次“省略 -session”验证外，所有 Gateway/Agent 调用都显式带 `-session`

## 2. 正确用法（实测确认）

### 2.1 安装

- **已发布 v1.3.126 只能显式路由模式**：`--target-name/--target-gateway/--target-cluster/--target-instance` 四项全给；实测安装成功，Skill/docs 来自 release commit `76abae08`。
- **HEAD 安装器 gateway-only 模式**：只给 `--target-gateway` 时安装成功、不写 config、提示 `doops login --gateway <url>`；对 v1.3.126 CLI 明确警告“该 release 不支持 --gateway 首登”。实测：安装 rc=0、无 config、CLI 仍无 `-gateway` 参数。
- 结论：**gateway-only 安装要等包含 `fc4b68ff` + 本轮安装器改动的下一个 release**。

### 2.2 登录与会话存储

- HEAD 实测：`login --gateway https://doops.l8ai.cn --token-stdin` → rc=0，**自动发现 11 个可见 target**，写回 0600 `config.json`/`auth.json`。
- 同一 Gateway 一次登录供全部 target 共用；`login --target <t>` 用于已有路由的后续登录。
- `logout --gateway`/`logout --target` 只删除本地登录，不吊销 Gateway token。
- `login --gateway` 与 `login --target` 同时给出且 gateway 不匹配时，明确报错。
- 已发布 v1.3.126：**没有 `--gateway` flag**，只能先有路由再 `login --target`。

### 2.3 路由

- `list`：**只读本地路由**，不会自动补新上线的 target。
- `sync --target <t>`：把该登录可见、但本地没有的在线 target 写回路由；实测 `11 live, 0 new local routes`。
- `targets --target <t>`：向 Gateway 实时查询在线/可见状态。
- `add`：显式写一条路由；`remove` 需要本地已登录，实测离线 fake target 可移除，移除后路由数归 0。

### 2.4 执行

- `exec --target gw-test --cmd hostname` → `doops-agent-test-6bcbbd8d45-g46cn`。
- `exec --bg` → 返回 PID + 日志路径 + 结果。
- 省略 `-session` 时 CLI 自动生成并打印 session；建议仍显式传。
- `ssh`：结构化 SSH 可用；连接被拒绝时 rc=1（见 P1）。

### 2.5 文件与工作区（重点）

- 路径是**相对 session 工作区**，不是绝对路径；绝对路径会失败。
- `read/write --storage gateway --path <相对路径>`：真正读写工作区内容（实测写入 13 字节、读取内容一致）。
- 默认 `--storage cos`：`read` 输出 presigned GET URL（不是文件内容）；`write` 上传对象并输出 URL。这是设计行为，但命令表/Skill 摘要没有说清楚。
- `push/pull/clean --storage gateway`：实测推送文件、远端 `cat`、拉回本地、清理工作区全部通过。

### 2.6 其他命令（实测）

- `ask` 只读：rc=0，返回 hostname + 内核版本，明确“只读未变更”。
- `web --mode check`：完成并给出结论；耗时超过 60s（本次 <180s），rc=0；内部 DoWeb 工具失败（nginx ENOENT）体现在输出里。
- `sql --database <database>`：`lab_management`、`agentcloud` 均成功；**`--database` 必须是 database 名，不是 credential/binding 名**。
- `k8s get/helm/logs`：只读命令通过。
- `check`：按实际 deployment/namespace/image 返回 `check ok`。
- `upgrade --dry-run`：输出 PREVIEW，不改集群。
- `capabilities --require structuredSSH/executionLifecycle`：均返回 ready。
- `credential list`、`admin user/grant/token list`、`credential preflight`（正确 manifest）：通过。
- `session`、`history`、`version`、`--contract`、`guide`：通过。

## 3. 实测矩阵（摘要）

| 用法 | 实测 | 结论 |
| --- | --- | --- |
| install v1.3.126 显式路由 | 真实下载安装 | PASS |
| install gateway-only（HEAD 安装器） | rc0，不写 config，警告旧 CLI | PASS（release 未支持首登） |
| login --gateway（HEAD） | 发现 11 target，写 0600 | PASS |
| login --gateway（v1.3.126） | flag 不存在 | FAIL（release gap） |
| login --target / logout | 临时状态验证 | PASS |
| list | 只列本地，无自动补 | PASS（Skill 原描述错误，已修） |
| sync | 11 live, 0 new | PASS |
| targets / capabilities / info | 在线状态与协议 | PASS |
| exec / exec --bg / 无 -session | hostname、PID、自动 session | PASS |
| read/write --storage gateway（相对路径） | 内容直读直写 | PASS |
| read/write 默认 COS | 返回 presigned GET；绝对路径报 cos_key_rejected | PASS（语义需文档化） |
| push/pull/clean | 推送/拉回/清理 | PASS |
| k8s get/helm/logs | 只读 | PASS |
| ask 只读 | rc0，只读结论 | PASS |
| web check | rc0，耗时 >60s，含内部失败说明 | PASS（耗时/退出码需说明） |
| sql --database lab_management/agentcloud | 查询成功 | PASS |
| sql --database <credential 名> | generic/矛盾错误 | FAIL（已修错误文案+文档） |
| sql 自然语言 --request | 180s 超时 | 未确认（需更长超时/异步） |
| check | check ok | PASS |
| upgrade --dry-run | PREVIEW | PASS |
| credential list / preflight | ready | PASS |
| admin user/grant/token list | 元数据 | PASS |
| ssh 连接拒绝 | rc1，execution_outcome_unknown | FAIL（错误类别误导） |
| image build/copy、registry sync | 仅 help/未执行 | 变更类，未跑 |
| credential/admin/upgrade 变更 | 仅 help/未执行 | 变更类，未跑 |
| ask --apply、sql apply、web apply | 未执行 | 变更类，未跑 |

## 4. 发现与修复

### P0

1. **发布缺口**：`v1.3.126` 没有 `--gateway` 首登；CNB main 仍 `66769eef`；HEAD 的 gateway-first 流无法通过官方安装到达用户。需要新 release。
2. **SKILL `list` 描述错误**：原写“已登录时自动补上新上线的 target”，实测不会。已改为 `list` 只列本地，`sync` 补新 target；SKILL 压缩到 **11981 bytes**（<12000）。
3. **SQL `--database` 语义误导**：错误文案写 `pass --database <name>`，列表是 `name=engine/database`，实测传 binding/credential 名会得到 `credential_operation_failed` 或自相矛盾的 `not_bound`；正确值是 database 名。
   - 已修 [dosql_direct_prompt.go](backend/agent/internal/server/dosql_direct_prompt.go) 两条错误文案、[sql.go](backend/cli/cli/doops/sql.go) next hint、对应测试。
   - 已修 [DOOPS_USAGE_GUIDE.md](backend/docs/DOOPS_USAGE_GUIDE.md) 的 `--database` 说明。
4. **read/write 默认 COS 语义**：`read/write` 默认返回 presigned GET，不是内容；绝对路径报 `cos_key_rejected`。用法指南详情段本来就正确，但命令表/Skill 摘要没有提示。已更新命令表两行，明确 `--storage gateway` 与相对路径。

### P1

1. `ssh` 对明确 `Connection refused` 返回 `execution_outcome_unknown`，误导为“结果未知”；应映射为确定性连接失败。
2. `web`/`ask` 属于 agent-skill 长任务：`web check` 实测 >60s，`sql --request` 180s 超时；文档应写“可能数分钟”，不要用短超时判断失败。
3. Gateway 的 `credential list` 中 `lab-test-mysql-migration` 绑定显示 `database_engine=`/`database_name=` 为空，但 Agent 端能推断 `mysql/lab_management`；建议绑定写入时保存 engine/database 元数据，减少 SQL 选择歧义。
4. `remove` 需要本地登录（用于确认离线）；文档只写“仅在 target 离线时删除”，未明确需要登录。
5. Skill 红线要求所有调用显式 `-session`，CLI 实际允许省略并生成；建议 Skill 写明“省略会生成新 session，不利复用”。

### P2

- `capabilities`、`history` 命令存在于 CLI，但 Skill 命令表未列。
- `k8s logs` 的资源格式必须是 `pod/<name>` 等，`deploy/<name>` 会报 `invalid kubernetes resource`；文档未给示例。
- COS 绝对路径统一报 `cos_key_rejected`，实际是 workspace 路径规则问题，错误类别不精确。

## 5. 未执行（变更类，按安全边界跳过）

- `image build`、`image copy`、`registry sync`（会构建/推送/复制镜像）
- `credential create/put/grant/verify/promote/revoke`、`credential configure --id ...` 的变更路径
- `admin user create/disable`、`admin grant add/delete`、`admin token create/revoke`、proxy 变更
- `upgrade`（实际执行）、`ask --apply`、`sql --mode apply`、`web --mode apply`
- `k8s` 的 apply/scale/delete 等变更
以上仅验证 `--help`/`--dry-run`/只读 list；未在线上执行变更。

## 6. 验证

- `python3 -m pytest -q backend/test`：**350 passed**，8 subtests
- `go test ./...`（CLI）：ok；`go test ./internal/server -run 'DoSQL|DirectDoSQL'`：ok
- `npm run test:console`：556 passed（Web 改动轮次）
- `npx tsc --noEmit`：无错误
- `git diff --check`：干净
- 真实安装 v1.3.126 gateway-only：rc=0，不写 config，并正确提示 CLI 不支持 `--gateway`

## 7. 结论

- 读、写、执行、K8s、push/pull、check、upgrade dry-run、导入 token、首登发现、sync、admin/credential 只读路径都已实测通过。
- 当前最大问题是**发布缺口**：gateway-first 用法只在 HEAD；官方 v1.3.126 仍要求四项路由。
- SQL 选择和 read/write 存储语义是真实使用中最容易踩的两个点，已修文案和文档；剩余 P1 建议随下一个 release 一起处理。
