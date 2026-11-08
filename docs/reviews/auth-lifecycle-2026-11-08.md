# Gateway 登录凭证整改验收记录

范围：`fix/auth-lifecycle`，Gateway 用户令牌、CLI 登录文件、控制台登录与恢复。
目标：失败原因可区分；同一令牌身份与到期时间由 Gateway 校验后返回；校验失败不覆盖本机凭证；
临时故障不清登录；撤销当前令牌时说明所有使用者均受影响。Agent/K8s RBAC 单独保留实例声明，
不将 Gateway 用户令牌作为 Kubernetes 身份。

## 执行与验证

1. Gateway：区分 missing/invalid/expired/revoked/user_disabled/invalid_expiry 与存储不可用；
   验证 HTTP、WebSocket 重新校验以及当前令牌身份接口，只返回当前令牌元数据。
2. CLI：登录前严格读取本机 schema；导入经 `/v1/auth/me` 验证；0600 原子替换；
   以编译 CLI 的真实 HTTP 登录、持久化、撤销、重新登录功能场景验证。
3. 控制台：登录/导入/恢复统一读取 `/v1/auth/me`；移除管理员探测；区分 401、403、503；
   cookie 不超过 token 到期时间；通过浏览器验证成功、拒绝、临时故障与当前令牌撤销。
4. 交付：同步权威文档；检查差异、提交及远端可见性、CI、版本化产物、部署和在线合成验收。

## 当前状态

- 前序提交：`8d5f5adbe08c8d9f167c2ac6aeb73e60a3443ca7`，继续补齐收敛项。
- 2026-11-08：CLI 编译后 HTTP/PTY 功能测试、完整 CLI 包测试（隔离 GOCACHE，29.624s）、
  Gateway 包测试通过；Web 生产构建与 554 项 console tests 通过。SQL 文档契约 81 项通过。
  明文 `login --token/--password` 四种参数形式均拒绝，零 HTTP 且不覆盖原缓存。
- 浏览器使用本机独立 Gateway、独立 SQLite 和合成账号，验证错误密码、普通用户无 grant
  登录、刷新恢复、管理员入口、标记当前 token、确认后自撤销、401 具体原因及清 cookie、
  token 导入恢复真实用户名、Gateway 中断 502 保留 cookie、恢复后无需再次登录。
  桌面截图 `/tmp/doops-auth-tokens-desktop.png`、窄屏截图 `/tmp/doops-auth-login-mobile.png`。
- 本机完整 Agent 包测试：原 COS 测试旧错误断言已修复并单独通过；其它失败涉及本机磁盘满
  和 Go 构建缓存文件缺失，不能作为成功证据。认证/WS 针对性回归已通过，待 Linux CI 全量验证。
  环境恢复后，真实 CLI→Gateway→Agent 的执行与账号全生命周期，以及原失败认证场景
  重跑通过（10.841s）。
- PR #89 已解决与当前 main 的冲突并推送；Skill 已压回既有字节上限，相关契约测试通过。
- 2026-05-11 首版 `4cf0086` 的 `CreateToken` 已签发包含 token ID 的格式；2026-05-13
  的 `925d4ce` 仅增加按 ID 查询，没有改变签发格式。无 ID 分支只由人为 legacy fixture
  覆盖，已删除全表 bcrypt 扫描；在关闭的数据库上仍拒绝无 ID 输入，确定性证明不访问存储。
- 2026-11-08 12:22（Asia/Shanghai）经 `gw-test` 回读 Agent 身份仍为
  `system:serviceaccount:agents-test:doops-agent-test`，跨命名空间 Pod、default Deployment、
  nodes、namespaces 四项 `can-i` 均为 yes。实例声明在 `backend/deploy/instances/zyj-test/`。
- 线上 `/v1/auth/me` 仍为 404；尚未发布此修复，线上闭环未成立。
- 未完成项不得作为已发布或线上闭环报告；真实用户 token 不在验收中枚举或批量撤销。

## 待发布范围与验收

发布对象为 `https://doops.l8ai.cn` 的 Gateway、同域 Web 控制台，以及本机
`~/.local/bin/doops` 与匹配 Skill。先通过 PR CI，再由 main/Release CI 构建版本化产物；
逐项记录完整 source commit、Gateway/CLI checksum 和 Web 镜像 digest。

顺序为 Gateway→Web→CLI，因为新控制台和 token 导入要求 `/v1/auth/me`。Gateway 用
`fetch-gateway-release.sh`、`deploy-gateway.sh --no-build --source-commit ...`，由脚本备份
原 binary/DB/unit，发布失败自动回滚。Web 用 digest 和保留的旧容器回滚。CLI 安装前保留
私密 auth/config 备份，按 `install.sh --version ...` 安装对应源码 Skill。

线上验收仅操作本轮创建并记录精确 ID 的合成用户/令牌/凭据，覆盖当前身份、24h 新签发与
长期 token 导入的差异、错误密码、过期/撤销/停用、拒绝授权、重登、退出、服务故障，
并按 T2 跑受管凭据 create/list isolation/persistence/verify/promote/revoke。
检查已有 Agent 重连、健康与真实浏览器路径。具体 SOP 见 `backend/docs/BUILD_DEPLOY_SOP.md`。
