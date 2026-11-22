# DoOps 整改状态与剩余工作（2026-11-22）

本文是 [深度评审](2026-11-22-doops-deep-review.md) 的整改台账，记录已发布内容、线上迁移状态和剩余工作。它不是产品文档，不随 CLI、Skill 或 Agent 镜像发布。

- 代码基线：`main` @ `6ee053c1`（v0.3.23 之后的功能批次）
- 已部署版本：v0.3.22 构建，commit `e28d33ac`
- 部署目标：`doops.aiedulab.cn`（Gateway 主机通过 SSH 发布，不登记为 doops target）

## 总览

| 状态 | 数量 | 说明 |
|---|---:|---|
| 已修复并发布 | 7 | 进入 v0.3.22，线上验证通过 |
| 分阶段 / 迁移窗口 | 5 | 第一阶段已上线，剩余阶段或收口动作未完成 |
| 未开始 | 12 | 仍在台账中，按优先级排期 |

## 逐项状态

| 编号 | 优先级 | 状态 | 落点 / 证据 | 备注 |
|---|---|---|---|---|
| B-01 | P0 | 已修复 | [tool_deadline.go](../../agent/internal/server/tool_deadline.go)、[tool_handlers_command.go](../../agent/internal/server/tool_handlers_command.go) | 超时返回 `agent_tool_timeout`，审计记 `timeout` |
| B-02 | P0 | 已修复 | [handler_ws.go](../../agent/internal/server/handler_ws.go) 的 `waitCommandOutput` / `streamCommandOutput` | 取消 1 MiB 行上限；已知退出不再改写 `outcome_unknown` |
| B-03 | P1 | 第一阶段 | 隧道读超时可配置 90 秒，断线记录在途执行数 | 断线续接协议未做 |
| B-04 | P1 | 未开始 | `agent_registry.go`、`execution_lifecycle.go` | 全局锁内访问单连接 SQLite |
| B-05 | P1 | 已修复 | [tunnel_hub.go](../../agent/internal/server/tunnel_hub.go) 的 `targetReconnectGrace` | 探测和凭据工具立即返回 |
| B-06 | P1 | 已修复 | [login_limit.go](../../agent/internal/server/login_limit.go)、[console-forward.ts](../../../lib/server/console-forward.ts) | 签名浏览器地址 + 成功登录清零 |
| B-07 | P1 | 未开始 | `gatewaycmd.go`、`client_service.go` | 无优雅停机/排空 |
| B-08 | P1 | 第一阶段 | [secret_box.go](../../agent/internal/server/secret_box.go) 的 `VerifySecretKey` | 启动校验已上线；密钥轮换未做 |
| B-09 | P2 | 未开始 | `acquireExclusive` / `acquireResource` | 升级等待没有写者优先 |
| B-10 | P2 | 未开始 | `model_provider_delivery.go` | 模型凭据投递内联在 ask 前 |
| B-11 | P2 | 未开始 | [proxy_tunnel.go](../../agent/internal/server/proxy_tunnel.go) | Proxy 写请求串行、逐请求同步审计 |
| B-12 | P2 | 未开始 | [audit_purge.go](../../agent/internal/server/audit_purge.go) | 无自动保留；过期 token 未清理 |
| B-13 | P3 | 未开始 | 各层超时常量 | 默认值未收敛，控制台未接 Gateway 时限 |
| S-01 | P0 | 迁移窗口 | [agent_registration.go](../../agent/internal/server/agent_registration.go)、凭据投递开关 | 代码已完成；线上显式开放，见下节 |
| S-02 | P2 | Chart 部分 | [values.yaml](../../deploy/helm/doops-agent/values.yaml)、[rbac.yaml](../../deploy/helm/doops-agent/templates/rbac.yaml) | 特权/RBAC 可配置；入口探测和 `node_info` 能力上报未合 |
| S-03 | P2 | 未开始 | `proxy_tunnel.go` 的审计摘要 | 查询串仍进审计 |
| S-04 | P2 | 未开始 | `gateway_store.go` 的 token 校验 | 每请求 bcrypt；需先跑基准 |
| S-05 | P3 | 已修复 | [handler_ws.go](../../agent/internal/server/handler_ws.go) 的 `checkWebSocketOrigin` | 主机 + 端口精确匹配 |
| S-06 | P3 | 未开始 | `gatewaycmd.go` 的 user/passwd | 口令仍可走命令行参数 |
| S-07 | P3 | 已修复 | [next.config.mjs](../../../next.config.mjs)、博客图片路由 | 安全头 + SVG 沙箱 CSP |
| S-08 | P3 | 未开始 | [dispatcher.go](../../agent/internal/dispatcher/dispatcher.go) | 拒绝规则仍是文本正则 |
| M-01 | — | 第一阶段 | [超大文件拆分设计](../../../docs/design/2026-11-22-oversized-file-split.md) | 纯搬运已落地；巨型函数/Web 拆分未做 |
| M-02 | — | 未开始 | `unresolvedExecutions`、`DeleteExpiredTokens` 等 | 死代码与从未置位标志 |
| M-03 | — | 已修复 | [package.json](../../../package.json)、三个 `go.mod` | pnpm 10.18.0（锁文件补丁哈希不兼容 pnpm 11）、Go 1.25.14 |

## 线上迁移状态（重要）

`doops.aiedulab.cn` 没有任何 enrollment token，同时使用 173 个凭据和 222 条授权。为了让升级不中断现有目标，Gateway 环境显式保留：

```text
DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=1
DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=1
```

这两个开关等同恢复 S-01 之前的行为：能访问 `/v1/agent/connect` 的人可以冒名注册并收到该目标凭据。它是迁移窗口，不是长期配置。

收口步骤：

1. 为每个 target 执行 `doops-gateway enroll create`，把 token 注入对应 Agent。
2. 用 `doops targets` 确认 `ENROLLED` 不再是 `NO`。
3. 用 `--require-agent-enrollment --require-enrolled-credential-delivery` 重新发布，或删除两行环境变量并重启 Gateway。

回滚资料：DB 备份 `/var/lib/doops-gateway/backups/gateway-pre-v0.3.22.db`（0600），旧二进制 `/usr/local/bin/doops-gateway.rollback-*`。

## 验证记录

- v0.3.22 GitHub Actions：Go tests 13m58s、Contract tests、Secret scan 全绿。
- 本机 `go test ./internal/server -skip 'TestRunHelm|TestSelfUpgrade|...'`：521s 通过。
- CLI `go test ./...`：42s 通过；`gatewaycmd`、`dispatcher` 通过。
- Web：`test:console` 684 项、`test:gateway-policy` 7 项、`test:dosql`、`tsc --noEmit`、`pnpm build` 通过。
- Helm：默认与 `securityContext.privileged=false` 两种渲染通过。
- 线上：`/health` 200；`exec` 返回 `still-ok`；`ask` 返回 `OK`；Gateway 日志无主密钥校验失败或 panic。

未完成：本机完整 `go test ./internal/server`（含 Helm/K8s/升级用例）会等待集群依赖，本地以 `-skip` 版本为准；CI 的完整套件已通过。

## 建议的下一批

1. **可用性/锁**：B-04（按目标加锁、读写连接分离）+ B-07（优雅停机）+ B-09（升级优先）。
2. **ask/Proxy**：B-10（模型投递不阻塞 ask）+ B-11 + S-03（Proxy 并发与查询串脱敏）。
3. **存储治理**：B-12（审计保留、token 清理）+ M-02（接上 `DeleteExpiredTokens`）。
4. **收口**：S-01 登记迁移；B-03 第二期；B-08 轮换；S-04 基准；S-06/S-08/B-13/M-01 阶段二。
