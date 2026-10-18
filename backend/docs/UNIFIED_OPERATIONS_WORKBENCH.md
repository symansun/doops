# Unified Operations Workbench

本文档记录 2026-10-07 的统一运维工作台边界。`doops.sh` 是运行宿主，Web 控制台和
`doops` CLI 是两种入口，DoOps Gateway 是唯一用户认证与 target/action 授权中心，Agent
skill 负责领域执行和证据。

## 运行结构

```text
Web Console / doops CLI
        | one Gateway user login
        v
DoOps Gateway: authenticate -> target/action grant -> audit -> lease
        |
        v
doops-agent: tool dispatch -> selected skill
        |
        +-- doops: host, K8s, deploy, files, CI/CD
        +-- dosql: database discovery, query, migration, rollback, evidence
        +-- doweb: SiteSpec, Nginx publish, health, TLS, rollback, evidence
        +-- other skills: their domain rules and evidence
```

Web 登录后只返回用户名和 Gateway 标识。Gateway user token 放在当前工作台的 HttpOnly
session cookie 中，浏览器业务请求不再把 token 放进 React state、localStorage、表单或
skill 参数。CLI 仍直接使用 `doops login` 保存的 Gateway token；两种入口最终都调用同一
Gateway `/v1/auth/login`、`/v1/targets`、`/v1/rpc` 和凭据 API。

## 授权矩阵

| 边界 | 唯一事实源 | 负责内容 | skill 是否重复实现 |
| --- | --- | --- | --- |
| 用户认证 | Gateway user/password 或 user token | 身份、有效期、撤销 | 否 |
| Target 可见性 | Gateway target grant（`UserCanUseTarget`） | `cluster/instance` 精确匹配 | 否 |
| 普通工具 | Gateway target grant | `ask`、`exec`、读写、push/pull 等 | 否 |
| Managed credential | Gateway credential resource/target binding | 资源名、target 绑定、物化和清理 | 否 |
| 数据库变更控制 | DoSql operation journal + signed approval | 变更说明、指纹、验证、未知结果证据 | 是领域规则，不是登录授权 |
| 网站变更控制 | DoWeb SiteSpec + Nginx validation/reload/health evidence | 站点范围、配置验证、回滚和健康状态 | 是领域规则，不是登录授权 |
| Web 会话 | DoOps Web `/api/session` | HttpOnly cookie 的建立、恢复、清理 | 否 |
| CLI 会话 | `~/.agent/skills/doops/auth.json` | Gateway token 本地缓存 | 否 |

以下入口已经复用同一 Web 代理和 session 读取：targets、RPC、审计、凭据、admin、Agent
Workbench session、附件上传。缺少 session 时统一返回未建立 Gateway 会话；Gateway 的
403/401 原样保留，不能由 skill 或页面伪造成功。

## DoSql 边界

DoSql 已接入受限的数据库 MCP 工具和 Gateway 受管凭据到数据库 adapter 的内部绑定。
`doops sql`、控制台数据库页和 DoSql skill 共用 Gateway 登录、target/action 授权、
审计和 session；skill 不实现第二套认证，也不能通过 Bash、本地连接文件、环境变量或
密码 prompt 绕过授权。只读查询和变更都会进入 DoSql 的 journal/evidence 生命周期，
变更还必须携带精确的数据库凭据、签名审批和执行后验证。

DoSql 通过 `doops_agent_prompt` 进入 Agent runtime。数据库目标来自当前 Gateway target 的
探测结果；`config/resources.json` 只能放标签和示例，不能选择 target、保存权限或保存密码。
数据库凭据只以 DoOps managed credential 的资源名进入任务，payload 由 Gateway 内部物化，
不进入 prompt、日志或 Web state。Gateway 只判断目标和凭据是否可用；数据库资产、环境、
引擎、数据库名由 DoSql/目标 Agent 根据 target discovery 和任务上下文校验。只读请求携带
credential reference 时必须显式声明 `skill=dosql`；普通只读 prompt 不能附带数据库凭据。

DoSql 的签名审批是变更控制证据，必须绑定 operation、change request、数据库资产、语句和
验证指纹。它不能绕过 Gateway grant，也不应被当作另一种用户登录。只读查询也必须写入
journal/evidence；写操作的未知结果必须阻止同一 operation 重放，直到人工核对。

数据库发现使用 `mcp_dosql_read operation=discover`，只扫描 Kubernetes 非 Secret 元数据，
从 Pod、Service、Deployment 和 StatefulSet 识别数据库实例、引擎、端口以及非 Secret 配置
中可见的数据库名，并返回稳定的 `database_asset_id`。缺少数据库名的候选必须由用户补充
精确选择和数据库凭据，不能猜测或扩大 scope。对话任务可以同时携带数据库资产引用和
代码仓库引用；它们只提供任务上下文，不替代 Gateway 授权、仓库权限或 database grant。

DoWeb 运行在选定 target 上，接收客户已经构建好的 `dist/`，通过 SiteSpec 渲染 Nginx，
执行 `nginx -t`、reload、健康检查并保留回滚状态。正常入口不接受 SSH host、用户名、密码
或第二套 token；首次主机安装也必须进入明确授权的 target bootstrap 流程。

DoWeb 巡检使用 `mcp_doops_web_read`，变更使用 `mcp_doops_web` 并返回本轮 runtime
attestation。只有精确的 `skill=doweb` apply 才获得网站写工具，普通 apply 不能取得
Web mutation 工具，网站 apply 也不会取得 Helm mutation 工具。`doops sql/web` 共用
CLI prompt 客户端。Nginx 是 target 上的共享资源，
网站 mutation 按 `nginx:<cluster>/<instance>` 精确资源互斥，并记录调用 session；同一
session 的其它 mutation/workspace 操作不得重叠，但其它 session 的非 Nginx 操作可以
并行。Nginx reload 不独占整个 target，session 名称也不是 Nginx 隔离边界。
目标容器的文件系统挂载、运行方式、TLS 已挂载路径和验收说明见
`DOWEB_INTEGRATION.md`。本地代码集成不代表线上镜像已经发布。

## 验收场景

- Given 浏览器没有 session，When 打开控制台，Then 只能看到登录页，targets/RPC 不会向外转发。
- Given 用户登录一个 Gateway，When 切换控制台的数据库页签，Then 请求沿用同一 session、target 和 audit，不出现第二个登录入口。
- Given 用户只有 `ask` 权限，When 提交 DoSql 只读请求，Then Gateway 允许或拒绝由 `UserCan` 决定，skill 不自行扩大权限。
- Given 用户没有精确数据库 credential grant，When 提交 DoSql 变更，Then Gateway/Agent 明确拒绝，不能回退到本地连接文件、环境变量或旧 token。
- Given 用户提交 DoWeb 发布，When target、站点范围或 dist/域名事实缺失，Then 只返回阻塞，不猜测站点或回退到另一台机器。
- Given DoWeb `nginx -t`、reload 或健康检查失败，When 变更已开始，Then 恢复上一份激活状态并返回真实失败证据。
- Given DoSql 数据库执行成功但 evidence 写入失败，When CLI/Agent 返回，Then 结果标记为审计失败并保留真实数据库 outcome，不宣称成功。
- Given 用户退出 Web，When 再次请求 targets、RPC 或 Agent session，Then HttpOnly session 已清理并返回未认证。
