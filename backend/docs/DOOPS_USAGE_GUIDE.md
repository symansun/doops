# Doops Usage Guide

This guide holds command-level usage details. The skill file is only the entry point.

`-session <s>` names the operation context and its `/root/ws/<s>` workspace. Any valid name
works and nothing has to be created first. When it is omitted, the CLI generates `ses_<16 hex>`,
prints it to stderr, and proceeds; pass that name again to continue in the same workspace.
`pull` is the exception: it needs the existing session whose workspace it downloads.

## Command Choice

| Need | Command | Notes |
| :--- | :--- | :--- |
| List configured targets | `doops list` | Local config only; not live gateway state. |
| List live gateway agents | `doops -session <s> targets --target <gateway-target>` | First check for gateway troubleshooting. |
| Retire an offline target | `doops -session <s> remove --target <gateway-target>` | Removes only the local route after a successful Gateway query confirms the exact route is not currently visible; online, unreachable, or unauthorized targets are refused. |
| Check Agent protocol capability | `doops -session <s> capabilities --target <t> --require structuredSSH` | Fails closed when the online Agent is too old for structured SSH. |
| Add a Gateway target | `doops add --name <n> --gateway <url> --cluster <c> --instance <i>` | Stores routing only; never stores a target token. |
| Run deterministic shell | `doops -session <s> exec --target <t> --cmd '<cmd>'` | Known checks and explicitly requested operations; standard releases use `ask --apply`. |
| Sync OCI images | `doops -session <s> registry sync --target <t> --source-registry <src> --target-registry <dst> --tag <release> --source-authfile <remote-file> --target-authfile <remote-file> --image <name>` | Generic target-side `skopeo copy --all`, bounded timeout/retry, and manifest digest verification; auth payloads never enter CLI arguments. |
| Build an OCI image | `doops -session <s> image build --target <t> --context <remote-dir> --output oci --output-path <remote-file>` | Generic BuildKit build; supports `--platform` and `--output image --image <ref> --push` for a registry push. |
| Copy an OCI image | `doops -session <s> image copy --target <t> --source <ref> --destination <ref>` | Generic Skopeo transport-to-transport copy with optional authfiles and raw manifest digest verification. |
| Run nested SSH safely | `doops -session <s> ssh --target <t> --host <user@host> -- <command> [args...]` | Preserves remote argv across nested shells. |
| Run a long build | `doops -session <s> exec --bg --target <t> --cmd '<cmd>'` | One RPC waits for the Agent task's terminal result; no task-status polling or command retries. |
| Ask the edge agent to reason | `doops -session <s> ask --target <t> --msg '<task>'` | Best for diagnostics and multi-step investigation. |
| Run a release | `doops -session <s> ask --target <t> --apply --credential <name> --msg '<facts>'` | Canonical business workload CI/CD entry. |
| DoSql read-only query or scan | `doops -session <s> sql --target <t> --mode query [--database <name>] --request '<request>'` | Automatically binds every active database credential on the selected Gateway target; `--database` picks among several. No local connection file is read. |
| DoSql live schema inspection | `doops -session <s> sql --target <t> --mode query [--database <name>] --request '读取指定 database_asset_id 的表、列或索引结构'` | Uses one read-only engine catalog/information-schema query. `discover` only finds Kubernetes database assets; it does not inspect live tables. |
| DoSql change plan | `doops -session <s> sql --target <t> --mode plan --request '<request>'` | Returns business summary, risk and verification evidence without mutation. |
| DoSql direct SQL | `doops -session <s> sql --target <t> --mode apply [--database <name>] --sql '<sql>' [--verify '<read sql>'] [--dry-run]` | One transaction per batch. Postgres rolls back DDL and DML together. MySQL/Oracle commit DDL implicitly: `--dry-run` rejects DDL (`dosql_dry_run_ddl_unsupported`), a batch mixing DDL and DML is refused (`dosql_mixed_ddl_dml_unsupported`), and a DDL that already ran is reported as `implicit_commits` / `partially_committed`. Each real apply gets a per-database `version` in a hash-chained journal on the Agent (`$(dirname workspace root)/.doops-dosql-versions`, persists only if that path is on a persistent volume); the version is reserved before execution and an apply is refused if the journal is unwritable or corrupt. Queries and dry-runs get no version. MongoDB takes JSON operation documents instead of SQL (`{"collection":"c","op":"updateMany","filter":{},"update":{"$set":{}}}`, array or consecutive documents): writes plus `--verify` share one transaction and roll back together, `--dry-run` rolls back (replica set required, else `dosql_transaction_unsupported`); `createCollection/createIndex/dropIndex/drop` are non-transactional and follow the same DDL rules as MySQL; `$where/$function/$accumulator`, `$out/$merge` and `system.*` are refused; auth retries once with `authSource=admin` and reports `auth_source`. |
| DoSql table copy | `doops -session <s> sql --target <t> --mode apply --copy-from <src-db> --database <dst-db> --tables a,b [--dry-run]` | The Agent copies whole tables between two databases bound to the same target, so rows never pass through the 200-row result limit or the caller. Both databases must use the same engine (mysql or postgres) and the destination tables must already exist. Source is read in a read-only transaction; all batches (500 rows) insert in one destination transaction, so any failure or `--dry-run` leaves the destination untouched. Counts as one versioned apply on the destination (`dosql_copy_*` errors name the cause). |
| DoSql target discovery | `doops -session <s> sql --target <t> --mode discover --request '<request>'` | Uses `mcp_dosql_read` to scan non-Secret target metadata; it never stores connection secrets. |
| DoSql repository/resource discovery | `doops -session <s> sql --target <t> --mode discover --root <repo> --resources-file <assets.json> --project <id> --environment <id> --dosql-script <path>/dosql-agent.mjs` | Scans the repository's code/config metadata and persists a metadata-only database inventory. |
| DoWeb read-only inspection | `doops -session <s> web --target <t> --mode check --request '<site/Nginx/TLS/health request>'` | Uses the selected Gateway target and DoWeb evidence path. |
| DoWeb site plan | `doops -session <s> web --target <t> --mode plan --request '<site publish request>'` | Produces SiteSpec, Nginx plan, checks and rollback without mutation. |
| DoWeb controlled publish | `doops -session <s> web --target <t> --mode apply --credential <web-resource> --request '<site, dist, domain, proxy, acceptance>'` | Requires the target grant and `nginx -t`/reload/health evidence. |
| Push a local directory | `doops -session <s> push --target <t> --src <dir>` | Syncs into `/root/ws/<session>`. |
| Pull a remote workspace | `doops -session <s> pull --target <t> --dest <dir>` | Use for large files and binary assets. |
| Read small text | `doops -session <s> read --target <t> --path <file>` | Small text only. |
| Write small text | `doops -session <s> write --target <t> --path <file> --file <local>` | Also supports `--content` and stdin. |
| Read system information | `doops -session <s> info --target <t>` | CPU, memory and disk metadata. |
| Use structured Kubernetes operations | `doops -session <s> k8s --target <t> ...` | Restricted operational interface; publishing uses `ask --apply`. |
| Clean workspace | `doops -session <s> clean --target <t> --workspace <s>` | Cleans `/root/ws/<workspace>`. |
| Check an image deployment | `doops check ...` | Compares configured service image with build metadata. |
| Upgrade a managed Helm Agent | `doops -session <s> upgrade --target <t> --image <repository:tag 或 repository@sha256:<digest>` | In-cluster Job only. `--dry-run` previews parameters. |
| Release an existing standalone Docker Agent | External `scripts/release-host-agent.sh` | Explicit DockerHost controller, not CLI fallback; real read-only `--dry-run`. See [SOP](BUILD_DEPLOY_SOP.md#独立-dockerhost-发布). |
| Cache token | `doops login ...` | Gateway login can exchange username/password for user token. |
| List target credentials | `doops credential list --target <t>` | Uses the cached gateway login and returns only metadata granted to that user on the selected cluster/instance. |
| Preflight development credentials | `doops -session <s> credential preflight --manifest .agent/doops-credentials.yaml` | Fails closed unless every declared credentialRef is active and bound to the selected target. The manifest `use` describes the requested operation and does not create a second permission. |
| Configure a credential once | `doops -session <s> credential configure --target <t> --id <id> [--namespace <ns>] [--use <use>] <source-and-scope-flags>` | Reuses managed storage, stages, binds to the target, verifies and promotes. See `CREDENTIAL_RESOURCES.md`. |
| Manage credential lifecycle | `doops credential <create|put|grant|verify|promote|grant-revoke|revoke>` | See `CREDENTIAL_RESOURCES.md`. |
| Create Gateway user and API key | `doops admin user create --target <t> --user <u> (--password-stdin\|--generate-password)` | Removes bootstrap wildcard access and applies only explicit `--grant-target` scopes. |
| List Gateway users | `doops admin user list --target <t>` | Returns metadata only. |
| Manage target grants | `doops admin grant <list\|add\|delete> ...` | Grants use exact configured `cluster/instance` routes. |
| Issue token as admin | `doops admin token create ...` | Requires a gateway token with `admin` permission. |
| Remove cached token | `doops logout --target <t>` | Removes local token cache. |

DoSql 必须使用 `doops sql` 入口。普通 `doops ask --msg '使用 DoSql'` 不保证注入
`mcp_dosql_read/execute`，也不能替代 DoSql 的 target discovery、credential scope 或
journal/evidence 链。

DoSql 使用凭据只要求该 target 的 grant，不需要逐操作员绑定。
Gateway 直接解析保存的 target/database 绑定和 active version，不让模型生成凭据 scope。
不存在 active version 时先用 `credential configure` 安全导入；该错误明确为 `credential_version_not_found`。
`doops sql` 不接受 `--credential`：Gateway 总是按 ID 绑定当前 target 上全部 active 的
`use=database` 凭据。只有一个绑定时直接使用；有多个时按 `--database` 匹配凭据指向的数据库，
未指定时返回 `dosql_database_required` 并列出各绑定指向的 `engine/database`。
当前 target 没有 `use=database` 绑定时返回 `dosql_database_binding_missing`；绑定 scope
失效时返回 `credential_binding_scope_invalid`。`--database` 没有凭据指向该库时返回
`dosql_database_not_bound`；多个凭据指向同一库时返回 `dosql_credential_ambiguous`，
吊销重复绑定后重试。完整对照见 `CREDENTIAL_RESOURCES.md`。

### DoSql 直接执行 SQL

已知确切 SQL 时用 `--sql` 或 `--file`，SQL 原样提交给 Agent，不经过模型，与 `--request`
二选一。只支持 `--mode query` 和 `--mode apply`，引擎为 postgres、mysql、oracle、sqlserver、mongodb；MariaDB、TiDB 走 mysql 路径，CockroachDB 走 postgres 路径，由凭据 `connection_uri` 的 scheme 决定。

其它数据库可由运维在 Agent 主机放置外置驱动：可执行文件 `dosql-driver-<scheme>`（默认目录 `<工作区根的上级>/.doops-dosql-drivers`，与版本日志同处，Agent 换 Pod 后随 agent-home 保留；可用 `DOOPS_DOSQL_DRIVER_DIR` 覆盖，不得 world-writable）。Agent 每批调用一次，DSN（含密码）只经 stdin 传入，helper 在进程内完成事务、回滚和 dry-run，并把 `doSQLDirectResult` JSON 写到 stdout；版本日志和 evidence 仍由 Agent 写。没有对应文件时返回 `dosql_engine_unsupported`。模板见 `backend/agent/cmd/dosql-driver`，Agent 不自动下载驱动。

```bash
# 只读：每条语句都必须是 SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/VALUES/TABLE
doops -session db-check sql --target <t> --mode query [--database <name>] \
  --sql 'SELECT count(*) FROM orders'

# 变更：整批语句与验证 SQL 在同一事务内执行，验证失败整批回滚
doops -session db-change sql --target <t> --mode apply [--database <name>] \
  --file migrate.sql --verify 'SELECT count(*) FROM orders WHERE status IS NULL'

# 干跑：执行整批和验证后回滚，用于确认语句可执行
doops -session db-change sql --target <t> --mode apply --file migrate.sql --dry-run
```

- 不要在 SQL 里写 `BEGIN/COMMIT/ROLLBACK/SAVEPOINT`，事务边界由 DoSql 持有；写了返回
  `dosql_statement_transaction_control_forbidden`。
- `--verify`/`--verify-file` 只能是一条只读语句，否则 `dosql_verification_invalid`；
  验证失败返回 `dosql_verification_failed`。`--verify` 与 `--dry-run` 只用于 apply。
- query 中出现写语句返回 `dosql_query_requires_read_only`，改用 `--mode apply`。
- Postgres 和 SQL Server 的 DDL 与 DML 一起回滚。MySQL 和 Oracle 的 DDL 会隐式提交：`--dry-run` 含 DDL
  返回 `dosql_dry_run_ddl_unsupported`，DDL 与 DML 混批返回 `dosql_mixed_ddl_dml_unsupported`；
  已隐式提交的 DDL 在结果中体现为 `implicit_commits` 和 `status=partially_committed`，不报成已回滚。
- 真实 apply 在 Agent 上按库写带哈希链的版本日志（`.doops-dosql-versions/<db>.jsonl`），
  执行前先分配版本号；日志不可写返回 `dosql_version_journal_unavailable`，被篡改返回
  `dosql_version_journal_corrupt`，两者都拒绝执行。query 和 dry-run 不产生版本。日志只在
  Agent 的 `/root` 是持久卷时跨重启保留。
- 结果是带 `operation_id`、`statement_fingerprint` 的 JSON，证据写入目标 session 工作区
  `.dosql/operations/<operation_id>.evidence.json`，可用 `pull` 取回。
- query 和 apply 都只要求该 target 的 grant；数据库选择规则同上一节。

MongoDB 也走 `--sql/--file`，但语句是 JSON 操作，不是 SQL。一批可以是 JSON 数组，也可以是连续多个文档：

```bash
doops -session db-change sql --target <t> --mode apply --database <name> \
  --sql '{"collection":"orders","op":"updateMany","filter":{"status":null},"update":{"$set":{"status":"new"}}}' \
  --verify '{"collection":"orders","op":"count","filter":{"status":null}}'
```

- 读（`--mode query` 可用）：`find`、`count`、`aggregate`、`distinct`、`explain`（`verbosity`）、`listDatabases`、
  `listCollections`、`listIndexes`、`dbStats`、`collStats`、`currentOp`（只返回 opid/op/ns/耗时/client，不含命令文本）、
  `serverStatus`、`replSetStatus`、`buildInfo`。库级和服务级操作不需要 `collection`。
- 写：`insertOne/insertMany/updateOne/updateMany/replaceOne/deleteOne/deleteMany`，以及原子的
  `findOneAndUpdate/findOneAndReplace/findOneAndDelete`（`return` 取 `before` 或 `after`）。
- 运维（非事务，等同 DDL 规则）：`createCollection`、`createIndex`（`unique/sparse/expireAfterSeconds/partialFilter`）、
  `dropIndex`、`drop`、`renameCollection`（`to`）、`collMod`（`options`）、`compact`、`killOp`（`opid`）、
  `dropDatabase`（`confirmDatabase` 必须等于当前目标库名，否则返回 `dosql_mongo_confirm_mismatch`）。
- 刻意不支持：用户和角色管理（`createUser` 等需要密码，会进入语句、版本日志和证据）、`$where/$function`、
  `$out/$merge`。这些要用独立的受控流程。
- 管理类读取不在事务内，看不到同一批里尚未提交的写入。
- 一批写操作与 `--verify` 在同一事务内，失败整批回滚，`--dry-run` 回滚。事务需要副本集，单机返回
  `dosql_transaction_unsupported`。
- `createCollection/createIndex/dropIndex/drop` 不能进事务：`--dry-run` 含它们返回
  `dosql_dry_run_ddl_unsupported`，与写操作混批返回 `dosql_mixed_ddl_dml_unsupported`，执行后计入 `implicit_commits`。
- `$where/$function/$accumulator` 返回 `dosql_mongo_javascript_forbidden`；聚合 `$out/$merge` 返回
  `dosql_mongo_stage_unsupported`；`system.*` 集合、未知操作或结构错误返回 `dosql_mongo_operation_invalid` /
  `dosql_mongo_document_invalid`。`deleteOne/deleteMany` 必须显式给 `filter`（`{}` 表示全部）。
- 认证库默认取 URI 中的库，认证失败时用 `authSource=admin` 重试一次；实际使用的库见结果的 `auth_source`。

`registry sync` is a thin generic wrapper over the target Agent's audited exec path. The
standard DoOps Agent image provides `buildctl` for image builds and `skopeo` for OCI
copy/inspect. The command performs copy and source/destination manifest digest checks;
it has no AMP-specific path and never installs a missing tool at runtime. If the target
does not advertise `skopeo`, the operation fails with a missing-tool error; upgrade the
DoOps Agent image or use a target that already provides it.

`image build` and `image copy` are thin generic wrappers over the same audited exec path.
They use standard BuildKit and Skopeo transports, so the command does not depend on Docker,
nerdctl, Harbor, AMP image names, or a project-specific script. For private Registry output,
the target must already have the remote authfile; credentials are never accepted inline in
the command arguments.

## Basic Checks

```bash
doops list
doops version
doops -session smoke targets --target <gateway-target>
doops -session smoke exec --target <gateway-target> --cmd 'hostname && date -u +%FT%TZ'
doops -session smoke-capabilities capabilities --target <gateway-target> --require structuredSSH
doops -session image-toolchain-check info --target <gateway-target>
```

`info` 输出中的 `buildctl: OK`、`skopeo: OK` 和 `buildkit-sock: OK` 表示目标具备
DoOps 通用镜像工具链。镜像构建使用明确给出的 `exec` 命令调用 `buildctl`；镜像复制
和源/目标 manifest 校验使用 `image copy` 或 `registry sync`。两者都不需要 AMP 专用脚本或外置同步 Job。
目标缺少 `skopeo` 时直接报告工具缺失，不在线安装，也不静默切换到其它工具。私有 Registry
凭据只能引用目标上的受控 authfile，禁止写入命令参数。

运行态文件与配置同目录：`auth.json`（按 Gateway URL 保存的登录态）、`history.jsonl`
（本机命令历史）、`sessions.json`（本机会话缓存）。`~/.config/doops/config.json`、
`~/.doops/auth.json` 和项目 `.doops/` 是旧路径，不作为当前配置依据。

`doops version` 的 `release` 是唯一 DoOps 发布版本，`commit` 是该版本对应的完整
源码 provenance；`doops targets` 的 `RELEASE_COMMIT` 必须与它一致。输出中的
`cli-contract/v3` 只通过 `doops --contract` 检查协议兼容性，不是另一个发布版本。
Agent 未提供完整 `RELEASE_COMMIT` 时只能视为旧运行时，升级验收不会通过。

`doops list` reads local config. `doops targets` asks gateway for live connected agents. A target can be listed locally but unavailable in gateway because it is offline or because the logged-in user lacks the exact target grant; the CLI does not collapse these cases into a definitive offline diagnosis.

To remove a stale local route, use `doops -session <s> remove --target <gateway-target>`. The command first queries the configured Gateway and refuses to modify the config if the Gateway is unreachable, authentication fails, or the exact route is currently visible. It does not delete Gateway grants, audit records, proxy routes, or historical agent status.

`doops targets` 的状态：

- `busy=true` 只表示 Agent 自升级占用整个 target；普通操作不会置位。
- `status=active` 且 `busy=false` 表示有 session 在跑。Shell/SSH/bg/apply 可并发，同 session 也不串行，
  但该 session 的 workspace 写入会等它们结束；session 只是并发归属，不是沙箱，不要从两个 session 同时改同一
  release 或数据库。
- `resources` 列出 `workspace:<session>` 等当前持有的锁。

`exec` 的 Shell 从 Agent 进程目录启动，需要 session 工作区时显式 `cd /root/ws/<session>`。
CLI 原样输出通知文本（含 JSON、空行、分块）；超时会请求取消并继续等原请求终态。

`capabilities --require structuredSSH|executionLifecycle` 只做握手，不证明 Agent 支持自升级；
只有 `exec --bg` 需要 `executionLifecycle`。其它名称在联网前即报参数错误。

执行生命周期（终态、`outcome_unknown`、取消返回 `202 cancel_requested` 不代表已停止、
`finalizing`/`audit_completion_failed`、SSH 退出码 `1..254` 与 `255` 的区别、并发与锁）见
[`DOOPS_GATEWAY_TUNNEL.md`](DOOPS_GATEWAY_TUNNEL.md#multi-user-behavior)。Gateway 控制面发布（`fetch-gateway-release.sh` +
`deploy-gateway.sh --no-build`）见 [`BUILD_DEPLOY_SOP.md`](BUILD_DEPLOY_SOP.md)。

## Gateway Admin Tokens

给用户签发 token 时，优先用 CLI 管理入口，不要 SSH 到 gateway 主机手工跑
`doops-gateway token create`：

```bash
doops admin token create \
  --target jm \
  --user gaojiaqi \
  --name maintenance \
  --expires 720h
```

`--target` 必须是本地配置里的 gateway target，并且它当前使用的 token 要有
`admin` 权限。新 token 只打印一次；需要写入本机缓存时显式使用：

```bash
doops admin token create \
  --target jm \
  --user gaojiaqi \
  --name maintenance \
  --expires 720h \
  --save-as jm
```

查看 token 元数据和撤销旧 token：

```bash
doops admin token list --target jm
doops admin token revoke --target jm --id <token-id>
```

列表只返回 ID、前缀、归属和状态，不返回 token 明文。管理员凭据只从
`--target` 对应的本地受限存储读取，不得通过命令行参数传入。

## Gateway Users And Target Grants

CLI 已封装用户创建、API key 签发和精确 target grant 管理。管理员 token 只从统一
`auth.json` 读取并放入 Authorization header，不得出现在命令行参数、请求 URL、日志或
文档中。用户密码只通过 stdin、交互式终端或 `--generate-password` 输入，禁止放入命令
参数或自然语言 prompt。

创建新用户并签发 API key：

```bash
printf '%s' '<PASSWORD_FROM_SECURE_INPUT>' | \
  doops admin user create \
  --target <gateway-target> \
  --user <username> \
  --password-stdin \
  --grant-target <allowed-target> \
  --name user-create \
  --expires 720h
```

`--grant-target` 可以重复。没有指定时，CLI 会删除后端 bootstrap grant，不授予任何
target 权限，并明确输出 warning。`--generate-password` 可替代 `--password-stdin`。
CLI 会先查同名用户，拒绝重复创建；创建或授权、签发、最终复核任一步失败都会停用新用户，
若 API key 已签发则同时尝试撤销，命令以失败退出。

用户和授权的只读/维护命令：

```bash
doops admin user list --target <gateway-target>
doops admin grant list --target <gateway-target> --user-id <user-id>
doops admin grant add --target <gateway-target> \
  --scope-target <allowed-target> --user-id <user-id>
doops admin grant delete --target <gateway-target> --id <grant-id>
```

已有用户可以设置或轮换密码：

```bash
printf '%s' '<PASSWORD_FROM_SECURE_INPUT>' | \
  doops admin user password --target <gateway-target> \
  --user-id <user-id> --password-stdin
```

停用或恢复用户：

```bash
doops admin user disable --target <gateway-target> --user-id <user-id> --disabled=true
doops admin user disable --target <gateway-target> --user-id <user-id> --disabled=false
```

先确认请求对应的精确路由，并检查同名用户：

```bash
doops list
doops targets --target <allowed-target>
```

```http
GET /v1/admin/users
Authorization: Bearer <ADMIN_USER_TOKEN>
```

同名用户已存在时停止创建，转为核对或调整现有 grant。创建普通用户必须显式使用
`admin:false`：

```http
POST /v1/admin/users
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{
  "name": "alice",
  "password": "<PASSWORD_FROM_SECURE_INPUT>",
  "admin": false
}
```

新用户没有任何 target grant。为每个允许的 target 创建一条 grant：

```http
POST /v1/admin/grants
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{
  "user_id": "usr_xxx",
  "cluster": "doops-oilan",
  "instance": "oilan-node"
}
```

grant 覆盖该 target 的全部操作和它绑定的凭据，没有 action 维度。授权失败时停用用户再退出：

```http
POST /v1/admin/users/disable
Authorization: Bearer <ADMIN_USER_TOKEN>
Content-Type: application/json

{"user_id":"usr_xxx","disabled":true}
```

### End-to-End Verification

不要用新用户登录覆盖本机管理员的共享 Gateway 登录态。创建一个临时目录，将所需的
routing-only targets 写入其 `config.json`，并让 `DOOPS_CONFIG` 指向该文件；其同目录
`auth.json` 将保存隔离的验证登录态。

```bash
DOOPS_CONFIG=<temporary-dir>/config.json \
  doops login --target <allowed-target> --username alice --name user-create-smoke

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops targets --target <allowed-target>

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops -session user_create_smoke exec --target <allowed-target> --cmd 'hostname'

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops -session user_create_smoke_denied exec --target <denied-sibling> --cmd 'hostname'

DOOPS_CONFIG=<temporary-dir>/config.json \
  doops -session user_create_smoke clean --target <allowed-target> --workspace user_create_smoke
```

允许 target 必须成功，未授权 sibling target 必须非零退出并被 Gateway 拒绝。最后用
`doops admin token list` 按 `user-create-smoke` 标签找到 token ID，再执行
`doops admin token revoke`。`doops logout` 只删除本地缓存，不能替代服务端撤销。安全删除
临时配置目录后，再交付账号。

## Exec

```bash
doops -session ops exec --target jm --cmd 'hostname && df -h && free -h'
```

Use `exec` for deterministic commands, read-only checks, and known deployment scripts.
标准 Agent 镜像提供出站 `ssh` 客户端。多层 SSH 必须使用结构化入口，不能把 SSH
命令拼进 `exec --cmd` 的单个字符串；这样可在 OpenSSH 边界保留每个远端 argv，避免
嵌套引号被远端 shell 重解析。这不会创建 SSH target，也不会启动 `sshd`。SSH 目标、
参数、远端命令和认证材料必须来自当前明确任务，禁止把私钥或密码内联到命令、prompt、
日志或工作区。

```bash
doops -session ops ssh --target jm --host ops@app.internal \
  --option=BatchMode=yes -- bash -lc 'hostname && systemctl status api'
```

`--option` 只接受 `-4`、`-6`、`-p`、`-i`，以及 `BatchMode`、`StrictHostKeyChecking`、
`ConnectTimeout`、`IdentitiesOnly`、`IdentityFile` 和 `Port`。`key=value` 简写会转换为
`-o<key>=<value>`。会改写目标或执行本地命令的选项会被拒绝。
CLI 只拦截直接以 `ssh`、`/usr/bin/ssh` 或 `/bin/ssh` 开头的 `exec` 命令，不是完整 Shell
分析器；包装命令仍须由调用者改用结构化入口。

## Ask

```bash
doops -session ops ask --target jm --msg '只读检查 Kubernetes 节点状态，给出结论和风险'
```

Use `ask` for diagnostics that require exploration. The target must have doagent/model configuration available.

`--database-ref <id>` 和 `--repo-ref <id>[@branch]` 可重复，只把引用 ID 作为
`[DoOps 任务引用]` 前缀拼进任务文本，不授予数据库或仓库权限，也不物化凭据。
`--model` 为本次指令选择模型。`--credential` 只允许与 `--apply` 一起使用。

## Kubernetes Operations

`doops k8s` 调用 Agent 的 `doops_k8s` 工具，不走 shell `kubectl`：

```bash
doops -session ops k8s --target <t> get pods --namespace apps
doops -session ops k8s --target <t> describe deployment api --namespace apps
doops -session ops k8s --target <t> logs deployment/api --namespace apps --tail 200
doops -session ops k8s --target <t> top pods --namespace apps
doops -session ops k8s --target <t> events --namespace apps --for deployment/api
doops -session ops k8s --target <t> rollout status deployment/api --namespace apps
doops -session ops k8s --target <t> get pods -A -l app=api
doops -session ops k8s --target <t> get networkpolicy worker -n apps -o yaml
doops -session ops k8s --target <t> get deploy -n apps -o 'jsonpath={.items[*].spec.template.spec.containers[0].image}'
doops -session ops k8s --target <t> logs pod/api-0 -n apps --previous --since 10m
doops -session ops k8s --target <t> helm list -A
doops -session ops k8s --target <t> helm history zhiyong -n zyj
doops -session ops k8s --target <t> helm status zhiyong -n zyj
doops -session ops k8s --target <t> helm values zhiyong -n zyj   # 只返回 key 路径
# 以下是 mutation，必须带 --confirm
doops -session ops k8s --target <t> rollout restart deployment/api --namespace apps --confirm
doops -session ops k8s --target <t> scale deployment/api --replicas 3 --namespace apps --confirm
doops -session ops k8s --target <t> node cordon <node> --confirm
doops -session ops k8s --target <t> delete pod <pod> --namespace apps --confirm
```

可用参数：`--namespace`/`-n`、`--all-namespaces`/`-A`、`--selector`/`-l`、`--container`/`-c`、
`--output`/`-o json|yaml|wide|name|jsonpath=...|custom-columns=...`、`--tail`、`--previous`/`-p`、
`--since`、`--replicas`、`--timeout`、`--for`、`--confirm`。可读 kind 包括工作负载、Service/Ingress、
ConfigMap、NetworkPolicy、HPA、PDB、RBAC、StorageClass、CRD、事件和存储；Secret 与
ServiceAccount 不开放。`helm values` 只返回 key 路径，因为 values 里可能有明文。
`get/describe/logs/top/events/rollout status/helm` 是只读查询，可与 target mutation 并行；`rollout restart/scale/cordon/uncordon/delete pod`
使用 target mutation lease。两类都只要求该 target 的 grant。版本发布不走这里，用
`ask --apply`。

## Development Credential Preflight

需要 Git、Registry、Helm、TLS 或 API 凭据的项目，在仓库中提交 metadata-only 清单：

```yaml
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-114-k8s
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
  - credentialRef: cnb-registry
    use: imagePull
```

Agent 在相关开发操作前运行：

```bash
doops -session dev-preflight-<task> credential preflight \
  --manifest .agent/doops-credentials.yaml
```

CLI 严格解析单文档 YAML，拒绝未知字段、重复的 `credentialRef + use`、target 冲突和
没有 `-session` 的远端检查。它只查询当前登录用户在 manifest target 上可见的 active
resource 和 target binding，输出 `ready`、`missing`、`wrong_use` 或 `ambiguous` 元数据。

清单不是授权源，也不包含 payload、secret 文件路径、默认值或本地 fallback。preflight
不会把凭据注入本机命令或远端 workspace；实际使用仍必须经过 target binding 和受控
materialization 路径。

## CI/CD And Apply

普通业务 workload 用 `doops ask --apply` 发布。任务写明 target、服务范围、镜像版本或其授权来源、
原因和验收条件；Helm 资产参数、`--reset-values`、可选 `expected_image`、凭据预物化和验收见
[`NATURAL_LANGUAGE_DEPLOYMENT.md`](NATURAL_LANGUAGE_DEPLOYMENT.md)。

```bash
doops -session release-<commit> ask \
  --target <configured-target> \
  --apply \
  --credential <resource-name> \
  --msg '在 <target> 的 namespace <namespace> 发布 deployment/<workload>，镜像 <repository>@sha256:<verified-ci-digest>；使用 chart https://charts.example/<name>.tgz；原因：<reason>；验收：rollout、最终 Deployment spec/status、实际镜像、Service/Endpoint 和健康检查通过。'
```

`--credential` 可重复，只传资源名。发布者调用前自行核对 CI source commit 和镜像 digest；
CLI 与 Agent 不验证 CI 来源。Agent 自身升级不走这条路径，见下文 Upgrade。

## Push

```bash
doops -session build push --target jm --src .
doops -session build exec --target jm --cmd 'ls -la /root/ws/build | head'
```

Standard `push` uses Git workspace sync through Gateway:

- Gateway: `/v1/git/<cluster>/<instance>/<session>.git`

Agent 的 Git handler 只接受 Gateway 反向隧道中的进程内调用，不暴露本地
HTTP `/git` 入口。

Do not reimplement push with tar/base64/chunk upload. `doops_workspace_*` chunk tools are low-level compatibility JSON-RPC tools, not the standard CLI/deploy path; seeing 512KB chunk logs means a script is bypassing the supported Git HTTP path. Push respects `.gitignore` and `.doopsignore`; do not hide deploy manifests or scripts behind ignore rules unless intentional.

## Pull

```bash
doops -session build pull --target jm --dest ./doops-build-output
```

Use `pull` for large files, archives, SQL dumps, images, videos, and generated assets. Do not use `read` or `exec base64` for downloads.

## Read And Write

```bash
doops -session ops read --target jm --path deploy.sh
doops -session ops write --target jm --path notes.txt --content 'hello'
doops -session ops write --target jm --path pod.yaml --file ./pod.yaml
cat ./pod.yaml | doops -session ops write --target jm --path pod.yaml
```

File API paths are relative to the named session workspace. The Web file browser
uses the same paths for open, save, new-file and upload, and its navigation stays
inside that workspace. Use explicit Shell operations for host paths.

`read` and `write` are for small text files and short scripts. Use `push/pull` for directories and binary assets.

## Clean

```bash
doops -session build clean --target jm --workspace build
```

Confirm the session name before cleaning. Do not share one session across unrelated tasks.

## Login And Logout

```bash
doops login --target jm --username alice
printf '%s' "$DOOPS_GATEWAY_TOKEN" | doops login --target jm --token-stdin
doops logout --target jm
```

CI 和自动化用 `--token-stdin` 传入 Gateway user token，不能与 `--token`、`--username`、
`--password` 混用。不要用 `--token`/`--password` 把凭据放进命令行参数。

Login and logout apply to the Gateway, not one target. `auth.json` stores one
token under the normalized Gateway URL, and every configured target on that
Gateway uses the same login state. Target configuration contains routing only;
it must not contain a Gateway user token. Gateway permissions still come from
the user's grants.

## Updating Metadata

没有通用的 `update` 命令。各类元信息按下表修改；“替换”表示先建新的再删旧的。

| 对象 | 如何修改 | 不能改的部分 |
| :--- | :--- | :--- |
| 本机 target 路由 | 同名再执行 `doops add --name <n> ...` 原地覆盖 gateway/cluster/instance/aliases/use；alias 与其它 target 冲突时拒绝 | 只有确认离线才能 `remove` |
| Gateway 用户 | `admin user password` 改密码；`admin user disable --disabled=true\|false` 停用/恢复 | 用户名、`is_admin` 无修改入口 |
| target grant | 替换：`admin grant add` 新范围，再 `admin grant delete --id` 旧的 | grant 不支持原地改 scope |
| Gateway token | 替换：`admin token create` 新 token，确认后 `admin token revoke` 旧 token | token 不能续期或改名 |
| 凭据 payload | 轮换：`credential put` → 对每个 active 绑定 `verify` → `promote`；或对已有资源重跑 `credential configure --id <id>`（每次生成新版本） | 历史版本不可改 |
| 凭据 target 绑定 | 同一 scope 重新 `credential grant` 会复活已吊销绑定；换 scope 要新 `grant` 再 `grant-revoke` 旧的 | 绑定不支持原地改 scope |
| 凭据资源 | 只能 `credential revoke` | name、type、scope 无修改入口；要改就新建资源 |
| 网站代理路由 | `doops admin proxy set --id <id> [--host] [--scope-target] [--upstream] [--enabled]` 原地修改，只改传入字段；新建省略 `--id`，见 `DOOPS_GATEWAY_TUNNEL.md` | — |
| Edge 域名路由 / enrollment | 当前没有 HTTP 或 CLI 管理入口；存储层的增改删只被测试调用 | 不要直接改 Gateway SQLite |
| Agent 的 cluster/instance | 由 Agent 部署配置（Helm values）声明，改它就是重新发布 Agent，见 `BUILD_DEPLOY_SOP.md` | CLI 和 Gateway 都不能改在线 Agent 的身份 |

## Credential Resources

凭据模型、payload schema、权限、分步生命周期（create/put/grant/verify/promote/revoke）、轮换、
bundle、审计和错误类别见 [`CREDENTIAL_RESOURCES.md`](CREDENTIAL_RESOURCES.md)。

首次配置用一条命令完成写入、绑定、验证和激活：

```bash
# payload 从 stdin 输入
printf '%s' '<payload-json>' | doops -session setup credential configure \
  --target <t> --name <name> --type <type> --scope personal --namespace <ns> --use <use>

# 从目标上已有 Secret 导入，不回传密码
doops -session setup credential configure --target <t> --id <id> \
  --namespace <ns> --use database --from-secret <secret> --keys connection_uri

doops credential list --target <t>                 # target 可用的凭据
doops credential list --target <t> --view managed  # 本人管理的凭据
```

已有资源传 `--id`，避免重复创建；源 Secret key 与运行时契约不同时加 `--key-map source=runtime`。
`doops sql` 自动使用 target 上的 database 绑定，不传 `--credential`。

## Upgrade

入口按运行方式选择，参数、验收和回滚见 [`BUILD_DEPLOY_SOP.md`](BUILD_DEPLOY_SOP.md)：

| 场景 | 入口 |
| :--- | :--- |
| 安装或更新 CLI / Skill（不动 Agent） | `bash scripts/install.sh --version <vX.Y.Z> --project "$PWD"` |
| 已有自升级控制器的 Helm Agent | `doops upgrade`（下方示例） |
| K8S 首装、旧 Helm Agent、`--proxy-allowed-hosts` / `--host-alias` 等 Helm 配置变更 | `scripts/release-managed-agent.sh`，操作机 kubeconfig |
| 已有独立 Docker Agent | `scripts/release-host-agent.sh`，见 SOP“独立 DockerHost 发布” |
| 空 Ubuntu/Debian 服务器 | `scripts/bootstrap-host-agent.sh`，见 [`CLUSTER_INSTALL_GUIDE.md`](CLUSTER_INSTALL_GUIDE.md#空服务器-docker-首装) |

安装器从 CNB Release Asset 下载带校验的 CLI 到 `~/.local/bin/doops`，回执写在
`~/.local/share/doops/install.txt`，其中 `commit` 与 `skill_commit` 一致。Skill/docs 取自 CLI
内嵌 commit，不取当前工作区；缺少该 commit 时安装在替换前停止。

```bash
doops -session agent-upgrade upgrade --target <t> --image <repository>@sha256:<digest> --dry-run
doops -session agent-upgrade upgrade --target <t> --image <repository>@sha256:<digest>
doops -session agent-upgrade upgrade --target <t> --rollback
```

- 镜像可以是 `repository:tag` 或 `repository@sha256:<64 hex>`；回滚只回到有验收证据的历史 revision。
- `--dry-run` 只预览参数，不检查 release、挂载或权限。`PREVIEW` 不是已部署，`ACCEPTED` 不是已完成。
- `OK` 表示 Job、`structuredSSH` 探针和 `release_commit` 已核验；之后仍要用同一 target 的
  `exec hostname` 确认实际服务的 Pod，并验证功能路径。
- `doops upgrade` 只换 Agent 镜像或回滚，不改 `proxy.allowedHosts`、`hostAliases` 等 Helm 配置。

## BuildKit Availability

Release images are built by CNB CI from the exact source commit. BuildKit in an
Agent is a runtime capability, not permission to build release images on a
business target or to make `push` a prerequisite for `ask --apply`.
Check availability without starting a build:

```bash
doops -session buildkit-check exec --target <target> --cmd 'buildctl --version'
```

Long-running non-release commands use `exec --bg`, not
`&`, `nohup`, or `setsid` inside a foreground `exec`. The native task service owns
the process, logs and exit status; it does not grant deployment authority.
