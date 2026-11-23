# DoOps 整改状态与剩余工作（2026-11-22）

本文是 [深度评审](2026-11-22-doops-deep-review.md) 的整改台账，记录已发布内容、线上迁移状态和剩余工作。它不是产品文档，不随 CLI、Skill 或 Agent 镜像发布。

- 代码基线：`main` @ `65aea31f`（v0.3.25 发布线）
- 已部署版本：v0.3.24 构建，commit `72f2e733`；二进制 sha256 `7165d20c...`
- 最新发布：v0.3.26（部署脚本就绪超时与契约测试修复）；v0.3.25 未发布，已由 v0.3.26 取代
- 部署目标：`doops.aiedulab.cn`（Gateway 主机通过 SSH 发布，不登记为 doops target）

## 总览

| 状态 | 数量 | 说明 |
|---|---:|---|
| 已修复并发布 | 7 | 进入 v0.3.22，线上验证通过 |
| 分阶段 / 迁移窗口 | 5 | 第一阶段已上线，剩余阶段或收口动作未完成 |
| 已发布并部署（v0.3.24） | 4 | B-04、B-07、B-09、S-03 |
| 未开始 | 8 | 仍在台账中，按优先级排期 |

## 逐项状态

| 编号 | 优先级 | 状态 | 落点 / 证据 | 备注 |
|---|---|---|---|---|
| B-01 | P0 | 已修复 | [tool_deadline.go](../../agent/internal/server/tool_deadline.go)、[tool_handlers_command.go](../../agent/internal/server/tool_handlers_command.go) | 超时返回 `agent_tool_timeout`，审计记 `timeout` |
| B-02 | P0 | 已修复 | [handler_ws.go](../../agent/internal/server/handler_ws.go) 的 `waitCommandOutput` / `streamCommandOutput` | 取消 1 MiB 行上限；已知退出不再改写 `outcome_unknown` |
| B-03 | P1 | 第一阶段 | 隧道读超时可配置 90 秒，断线记录在途执行数 | 断线续接协议未做 |
| B-04 | P1 | 已发布并部署（v0.3.24） | [agent_registry.go](../../agent/internal/server/agent_registry.go)、[execution_lifecycle.go](../../agent/internal/server/execution_lifecycle.go)、[gateway_store.go](../../agent/internal/server/gateway_store.go) | 按目标锁 + DB 访问移出全局锁 + `Wait` 读锁快路径 + 只读连接池 + 心跳批量落库；`-race` 回归通过 |
| B-05 | P1 | 已修复 | [tunnel_hub.go](../../agent/internal/server/tunnel_hub.go) 的 `targetReconnectGrace` | 探测和凭据工具立即返回 |
| B-06 | P1 | 已修复 | [login_limit.go](../../agent/internal/server/login_limit.go)、[console-forward.ts](../../../lib/server/console-forward.ts) | 签名浏览器地址 + 成功登录清零 |
| B-07 | P1 | 已发布并部署（v0.3.24） | [gatewaycmd.go](../../agent/pkg/gatewaycmd/gatewaycmd.go)、[client_service.go](../../agent/internal/server/client_service.go)、[tunnel_hub.go](../../agent/internal/server/tunnel_hub.go)、[main.go](../../agent/cmd/agent/main.go) | `SIGTERM` 排空与 `/health` 503、Agent 重连指数退避加抖动（按 AGENTS.md 未重新引入 operation admin）、`deploy-gateway.sh` systemd drop-in；`-race` 与二进制 `SIGTERM` 生命周期测试通过 |
| B-08 | P1 | 第一阶段 | [secret_box.go](../../agent/internal/server/secret_box.go) 的 `VerifySecretKey` | 启动校验已上线；密钥轮换未做 |
| B-09 | P2 | 已进入 v0.3.24，待发布验收 | `agent_leases.go`、`session_concurrency_test.go` | 升级等待阻止新请求准入，超时释放；本轮定向 race 测试三轮通过 |
| B-10 | P2 | 未开始 | `model_provider_delivery.go` | 模型凭据投递内联在 ask 前 |
| B-11 | P2 | 未开始 | [proxy_tunnel.go](../../agent/internal/server/proxy_tunnel.go) | Proxy 写请求串行、逐请求同步审计 |
| B-12 | P2 | 未开始 | [audit_purge.go](../../agent/internal/server/audit_purge.go) | 无自动保留；过期 token 未清理 |
| B-13 | P3 | 未开始 | 各层超时常量 | 默认值未收敛，控制台未接 Gateway 时限 |
| S-01 | P0 | 迁移窗口 | [agent_registration.go](../../agent/internal/server/agent_registration.go)、凭据投递开关 | 代码已完成；线上显式开放，见下节 |
| S-02 | P2 | Chart 部分 | [values.yaml](../../deploy/helm/doops-agent/values.yaml)、[rbac.yaml](../../deploy/helm/doops-agent/templates/rbac.yaml) | 特权/RBAC 可配置；入口探测和 `node_info` 能力上报未合 |
| S-03 | P2 | 已发布并部署（v0.3.24） | Proxy/Git 审计与活动操作不再保存 URL，公网 Proxy 返回固定错误 | 功能回归通过；测试 Agent 改用临时库签发的 enrollment token；Proxy/Git、相关凭据和升级排队定向 race 回归通过；历史记录未清洗 |
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
- 本机 `go test ./internal/server -skip 'TestRunHelm|TestSelfUpgrade|...'`：595s 通过（v0.3.24 线）。
- v0.3.24 生产部署：官方资产 sha256 `7165d20c...`，`/health` 200，`exec`/`ask` 通过，30 次 Agent 上线，0 主密钥或 panic 错误。
- v0.3.25 修复部署脚本 30 秒就绪窗口过短的问题，首次大库迁移可用 `--readiness-timeout` 放宽。
- CLI `go test ./...`：42s 通过；`gatewaycmd`、`dispatcher` 通过。
- Web：`test:console` 684 项、`test:gateway-policy` 7 项、`test:dosql`、`tsc --noEmit`、`pnpm build` 通过。
- Helm：默认与 `securityContext.privileged=false` 两种渲染通过。
- 线上：`/health` 200；`exec` 返回 `still-ok`；`ask` 返回 `OK`；Gateway 日志无主密钥校验失败或 panic。

未完成：本机完整 `go test ./internal/server`（含 Helm/K8s/升级用例）会等待集群依赖，本地以 `-skip` 版本为准；CI 的完整套件已通过。

## 建议的下一批

1. **ask/Proxy**：B-10（模型投递不阻塞 ask）+ B-11（Proxy 并发）；B-04、B-07、B-09、S-03 随 v0.3.24 发布后做生产验收。
2. **存储治理**：B-12（审计保留、token 清理）+ M-02（接上 `DeleteExpiredTokens`）。
3. **收口**：S-01 登记迁移；B-03 第二期；B-08 轮换；S-04 基准；S-06/S-08/B-13/M-01 阶段二。

## v0.3.27 发布部署记录（2026-11-23）

- 源提交：`6c64e52cc07a5f204f210b94975aa4744ce4c7ae`（tag `v0.3.27`，GitHub main 与 CNB main/master 已同步）。
- CLI：GitHub Release `v0.3.27` 资产齐全；本机 `~/.local/bin/doops` sha256 `3ea13a47…` 与 `doops-darwin-arm64` 资产一致，`doops version` 报 `release=v0.3.27 commit=6c64e52c`。
- Gateway：`doops-gateway-linux-amd64` 资产 sha256 `d1d93e6e…`；主机 `/usr/local/bin/doops-gateway` 实测同 hash，systemd active。`agent_registration=open`、`unenrolled_credential_delivery=open` 迁移窗口配置保持不变（见 S-01 收口步骤，仍未登记）。
- Web：CNB 构建 `cnb-mft-1k83eqmav` 全绿；`docker.cnb.cool/l8ai/ai/doops.sh/web@sha256:db695f2edb7db0261d076a307bf82fbc07b131cee775839e5eaa0a7ad3b037f4` 经 `deploy-web-console.sh` 部署到 `doops.aiedulab.cn`，容器 running，`/console` 200；回滚容器 `doops-web-rollback-20261123T064625Z-28270` 保留。
- Agent 镜像：`docker.cnb.cool/l8ai/ai/doops.sh@sha256:58b9cf3c61143c1a63a77971bb182f660a7347b27a98261d398005e8874e9cb1`（label `org.opencontainers.image.revision=6c64e52c`，doagent 0.2.47 / `aed1b4de`，base-light `inputs-53755a1eedca14c9`）。

### Agent 逐 target 验收

| Target | 路径 | 结果 | 证据 |
|---|---|---|---|
| gw-test | `doops upgrade` 原生控制器 | ✅ v0.3.27 | operation `upgrade_db42a74e…`，helm rev 29，receipt `doops-upgrade-receipt-ef72d802…-apply`，structuredSSH ready |
| gw-zy | 首升控制器 `first-upgrade.py`（rbac_controller_identity_changed） | ✅ v0.3.27 | Job `zy-first-upgrade-v0327-r2` Complete；receipt phase=complete；Pod `doops-agent-239-74d99c698-kzl7x` imageID=digest；helm rev 3；备份 Secret/receipt 保留 |
| gw-zyj | `doops upgrade` | ✅ v0.3.27 | operation `upgrade_64d10f46…`，release_commit 读回一致 |
| gw-edu-coder | `doops upgrade` | ✅ v0.3.27 | operation `upgrade_d5bfb9b2…`，release_commit 读回一致 |
| gw-hdu-235 | `doops upgrade` | ✅ v0.3.27 | operation `upgrade_d0e6f03a…`，release_commit 读回一致 |
| gw-silicon | `release-host-agent.sh` 外部宿主控制器（sub2api-49 SSH） | ✅ v0.3.27 | audit 877831；`host_release_complete`；回滚容器 `doops-agent-rollback-agent-release-6c64e52c` 保留 |
| gw-scu | — | ❌ 未升级 | preflight `rbac_controller_identity_changed`；首升控制器要求独立 kubeconfig，该实例为 serviceAccount 身份（`DOOPS_KUBECONFIG_SOURCE=serviceAccount`），控制器不支持此身份模式（2026-11-16 台账已记录的缺口） |
| unicom-k8s | — | ❌ 未升级 | `upgrade_audit_protocol_required`（v1.3.126 无 upgradeAudit v1）；SA 身份同 gw-scu，首升控制器不适用 |
| unicom-1 | — | ❌ 未升级 | Compose 一次性控制器绑定 2026-11-08 旧基线（容器 fc58d9a6、镜像 fd20c510），不可复用到 v0.3.27 |
| gw-keyi | — | ❌ 未升级 | systemd 静态二进制（v1.3.43），无已审查的替换通道与主机访问 |
| gw-jm / gw-114-k8s / oilan | — | ⚠️ 不可达 | 当前 Gateway 登录下 target unavailable，未做任何变更 |

### CNB 构建说明

- `api_trigger_release` 构建在 cloud-dev 节点上，`internal/server` 全量套件反复于约 2 分钟处被 SIGTERM（exit 143，非测试失败；隔离探针同命令完整跑完 523s）。release 配置改为：非 server 包正常跑 + `internal/server` 仅跑 `TestPlatformSkills|TestMonitorEngine` 冒烟子集；全量套件由 GitHub CI 在同一 commit `6c64e52c` 上 `go test -timeout 20m ./...` 完整通过作为覆盖证据。
- `DO_AGENT_IMAGE` 为操作方输入，release stage 内按 `runtime-versions.env` 的 `DO_AGENT_INDEX_DIGEST` 导出 `docker.cnb.cool/l8ai/ai/doagent@sha256:35769bda…`（CNB 仓库未配置该变量，历史构建均跳过 Agent 镜像）。
- `.cnb.yml` 正式修复（stage 拆分 + GOMEMLIMIT/GOGC + DO_AGENT_IMAGE 导出）尚未提交到 CNB 分支；本次 release 使用 API 内联 config。

## v0.3.30 发布部署记录（2026-11-23/24）

- 源提交：`d7c2bcf1704d092b6b76fecbb590e9d5e7ed8d15`（tag `v0.3.30`，main 线上；首个打在 `hotfix/v0.3.30` 分支的同名 tag 被 GitHub "Validate release tag" 拒绝后已删除并重打）。
- 变更内容：（1）target 资源同步接受合法分层观测键（含 `/` 的数据库/命名空间键），修复全舰队 resource sync 被 `valid()` 拒绝的问题；（2）`pinExecutionManifest` 由硬门槛降级为可选增强——迁移/inventory 前置缺失时不再以 `target_manifest_unavailable` 阻断普通 exec/ask/shell/k8s/helm/bg。
- 线上事故根因：v0.3.29 的 exec 门控要求 11 张资源同步表 + inventory 前置，而发布的 SQLite 迁移缺 5 张表，导致全部 v0.3.29 target 的远程操作瘫痪；先对 `/var/lib/doops-gateway/gateway.db` 在线备份后应用 `backend/docs/migrations/` 的 3 个算子迁移补齐表，再由 v0.3.30 代码消除硬依赖。
- CLI：GitHub Release `v0.3.30` 资产齐全，`checksums.txt` 校验 OK；`doops version` 报 `release=v0.3.30 commit=d7c2bcf1`。
- Gateway：`doops-gateway-linux-amd64` 资产 sha256 `b97a29db…`；`/proc/<pid>/exe` hash 与资产一致，systemd active；`/health` 200、未认证 `/v1/targets` 401；10 条基线路由全部回连。首次部署误用默认 `--verify-url https://doops.l8ai.cn` 触发自动回滚，改用 `https://doops.aiedulab.cn` 后成功；`agent_registration=open`、`unenrolled_credential_delivery=open` 迁移窗口配置保持。
- Web：`docker.cnb.cool/l8ai/ai/doops.sh/web@sha256:276a658630672f020b9fc049a21b196f7f4be265a10c214592594cfab1c21a09`（CNB 构建 `cnb-d01-1k84vdonk` 全绿），`/console` 200；回滚容器 `doops-web-rollback-20261123T201644Z-91551` 保留。
- Agent 镜像：`docker.cnb.cool/l8ai/ai/doops.sh@sha256:3c7f12e82832b9f5f5708783e0b345cf32c3f9b0847c7628510b46c46337d88b`（OCI index，含 amd64 运行时 manifest 与 attestation）。

### Agent 逐 target 验收（v0.3.30）

| Target | 路径 | 结果 | 证据 |
|---|---|---|---|
| gw-test | `doops upgrade` | ✅ | operation `upgrade_58ba6cf4…`，helm rev 31，receipt `doops-upgrade-receipt-d72b95bcc476…-apply`，release_commit 读回一致 |
| gw-zyj | `doops upgrade` | ✅ | operation `upgrade_1321ea1b…`，helm rev 4，release_commit 读回一致 |
| gw-edu-coder | `doops upgrade` | ✅ | operation `upgrade_ed173131…`，helm rev 4，release_commit 读回一致 |
| gw-hdu-235 | `doops upgrade` | ✅ | operation `upgrade_27fe3488…`，helm rev 4，release_commit 读回一致 |
| gw-zy | 首升控制器 `first-upgrade.py` | ✅ | Job `zy-first-upgrade-v0330` Complete（6m32s），receipt phase=complete、`releaseCommit=d7c2bcf1`、`runtimeDigest=sha256:3c7f12e8`；helm rev 7→升级；备份 Secret `zy-first-upgrade-v0330-backup` 与 receipt 保留。**r3/r5 失败真因**：`accept` 阶段需经 Gateway exec 送达新 Agent，而 `target_manifest_unavailable` 门控阻断 exec → `gateway_acceptance_timeout` 回滚；v0.3.30 软化门控后 accept 送达即完成。此前 `old_runtime_descriptor_mismatch`/`old_runtime_manifest_mismatch` 为 OCI index digest 与 attestation 处理问题，已修复并回填仓库 `first-upgrade.py`（3357f66c） |
| gw-silicon | `release-host-agent.sh` 外部宿主（sub2api-49 SSH，ControlMaster 复用别名密钥） | ✅ | audit 882092，`host_release_complete`，回滚容器 `doops-agent-rollback-rel-v0330` 保留，`targets` 读回 `d7c2bcf1` |
| gw-scu | — | ❌ 未升级 | `doops upgrade` preflight `rbac_controller_identity_changed`；首升 render 拒绝：`independent_kubeconfig_required`（容器内 `/root/.kube/config` 非挂载卷，SA 身份） |
| unicom-k8s | — | ❌ 未升级 | 老版本不通告 upgradeAudit v1（`upgrade_audit_protocol_required`）；首升 render 同样 `independent_kubeconfig_required`（`/root` hostPath 挂载存在但不满足精确 mountPath 校验，未擅自放宽） |
| gw-keyi | — | ❌ 未升级 | systemd 裸二进制 Agent，无已审查替换通道与主机访问 |
| unicom-1 | — | ❌ 未升级 | 裸二进制 Agent（无容器），`release-host-agent.py` 仅支持 Docker 容器运行时 |
| zhaoteng | — | ⏭️ 跳过 | 本地开发 Agent（`doops-local/agent:latest` 非发布镜像，commit `<unknown>`），非本次发布范围 |
| gw-jm / gw-114-k8s / oilan | — | ⚠️ 不可达 | 当前 Gateway 登录下 target unavailable，未做任何变更 |

### 遗留事项

- 全部在线 target `ENROLLED=NO`：enrollment token 迁移仍是未收口项（S-01），迁移窗口开关保留。
- gw-zy 历史失败证据：`zy-first-upgrade-v0329-r2/r3/r4/r5-*` receipt ConfigMap 均保留在 `default` ns，未覆盖。
- target 资源同步的端到端验证（清单下发、审计增强）需在 inventory 前置齐备后另行验收；当前按设计降级为不阻断执行。
