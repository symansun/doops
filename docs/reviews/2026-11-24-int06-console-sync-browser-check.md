# INT-06 Console 侧 Agent 资源同步状态真实浏览器验收

- 轮次：2026-11-24 10:16–11:05（本机 PC，Asia/Shanghai）
- 结论：**通过（四态全部取到证据）**，链路完全由本仓库代码在本机构建，无 docker、无集群、无线上 Gateway。
- 源码基线：`/tmp/int06-src` = `git worktree add --detach origin/main`，origin/main=`5f2eff0b`（绕开 INT-18 在 `backend/agent/internal/server/` 的未提交改动，二进制因此与主线一致）。
- 截图（全部在 /tmp，未入库）：

| 状态 | 截图 | sha256 | 大小 |
|---|---|---|---|
| 已同步 | `/tmp/int06-synced.png` | `7f31c90d84d30c958163f4d992b93a4498d9fdf2ccc4075219f14854c27ae388` | 132196 B |
| Agent 离线 | `/tmp/int06-offline.png` | `6543c517bfb2526701e6d7c62264ac71a8e9c379a8b3fb664d583036b92ef908` | 133157 B |
| 等待同步 | `/tmp/int06-pending.png` | `84c1b263fb16962382fb9cdae84d850aa7afac8a3016ba18268a280926362068` | 133846 B |
| 不支持同步 | `/tmp/int06-unsupported.png` | `09f33ab5ce5a804e5bbb2a498d602d9d5ad576e44518f72c79358ab8db05db92` | 138196 B |

## 状态驱动的字段（源码核对）

`components/target-manifest-card.tsx:21-28` 的 `manifestStatus()` 是四态唯一判据，顺序固定：

1. `!data.online` → `Agent 离线`（warning）
2. `!data.supported` → `Agent 不支持同步`（warning）
3. `!data.manifest || (data.applied_revision ?? 0) < data.manifest.revision` → `等待同步`（warning）
4. 否则 → `已同步`（success）

后端字段来自 `GET /v1/assets/target-manifest?cluster=&instance=`（`lib/assets.ts` 的 `TargetManifestResponse`）：`{manifest:{target,revision,digest,generated_at,resources[],counts}, applied_revision, online, supported, last_observed_at}`。`online` 与 `supported` 读 `agent_status` / `agent_resource_state`（`backend/agent/internal/server/agent_resource_store.go:93`、`:123`）；连接代次（`connection_at`）与当前连接不一致时强制 `supported=false, applied_revision=0, last_observed_at=""`（`backend/agent/internal/server/agent_resource_store.go:127-131`）。

## 关键命令

### 1) 构建（两条都成功）

```bash
git worktree add --detach /tmp/int06-src origin/main   # 5f2eff0b
cd /tmp/int06-src && env -u GOROOT go build -o /tmp/int06/doops-gateway ./backend/gateway
cd /tmp/int06-src && env -u GOROOT go build -o /tmp/int06/doops-agent  ./backend/agent/cmd/agent
```

（默认 shell 的 `GOROOT=/Users/wwyz/.local/share/go` 是旧 go1.26.2，与 go 1.27.4 不匹配，所有 go/node 命令一律 `env -u GOROOT`。）

### 2) Gateway（SQLite，独立 db 与密钥）

```bash
cd /tmp/int06
# 32 字节随机密钥写入 /tmp/int06/key（内容不入库、不进报告）
DOOPS_GATEWAY_SECRET_KEY=$(cat key) ./doops-gateway serve -port 42322 -db /tmp/int06/gw.db -monitor-tick 0

./doops-gateway enroll create -cluster local -instance local-1 -db /tmp/int06/gw.db   # -> token=<...>，存 /tmp/int06/token.txt
./doops-gateway user  create -name admin -<登录口令> '<口令>' -db /tmp/int06/gw.db
./doops-gateway grant -user admin -admin -db /tmp/int06/gw.db
```

### 3) 资产表迁移（本机首次启动必须做，否则同步能力恒 false）

```bash
for f in /tmp/int06-src/backend/docs/migrations/*.sql; do sqlite3 /tmp/int06/gw.db < "$f" && echo "OK $f" || echo "FAIL $f"; done
# asset_coverage.sql / asset_runtime_topology.sql / inventory_node_links.sql / machine_diagnostics.sql 首次因前置表缺失 FAIL，
# 在 cluster_inventory.sql / business_inventory.sql / machine_inventory.sql 之后重跑即全部 OK。
```

`agent_resource_sync.go` 的 `runAgentResourceSync()` 在 Agent 连接时若 `agentResourceSyncReady()` 为 false 会**直接 return**（`backend/agent/internal/server/agent_resource_store.go:57-75` 要求 11 张表同时存在），所以**必须先迁移、再重连 Agent**，同一 enroll token 可复用。

### 4) Agent（反向隧道连回本机 Gateway）

```bash
cd /tmp/int06
DOOPS_AGENT_SKILLS_ROOT=/tmp/int06/skills DOOPS_AGENT_WORKSPACE=/tmp/int06/workspace \
  ./doops-agent -port 42323 -listen 127.0.0.1 -gateway-url ws://127.0.0.1:42322 \
  -cluster local -instance local-1 -enroll-token "$(sed 's/^token=//' token.txt)"
# 日志: ✅ reverse tunnel connected: cluster=local instance=local-1
```

### 5) Console + 浏览器

```bash
PORT=3014 env -u GOROOT pnpm dev        # http://localhost:3014
```

ego-browser 实际点击路径：打开 `http://localhost:3014/console` → 填 Gateway 地址 `http://127.0.0.1:42322` + admin + 口令 → 「连接」→ 关掉首次弹窗「更新内容 v0.3.31」（会遮挡卡片）→ 「Agent 概览」→ 目标卡 `local/local-1`「进入工作区」→ 页面「资源清单」卡片。

**坑（值得写进文档）**：用 `127.0.0.1:3014` 打开时 Next dev 以跨源为由拒发 `/_next/webpack-hmr`（日志 `Blocked cross-origin request to Next.js dev resource`），页面永久停在「正在恢复登录会话...」且不 hydrate；换 `localhost:3014` 正常。Gateway 地址仍可写 `127.0.0.1`，因为登录走 console 的服务端路由 `app/api/login/route.ts`。

## 每个状态的证据

### A. 已同步（真实，未改数据）

```json
{"manifest":{"target":{"cluster":"local","instance":"local-1","mode":"linux","kubernetes_available":false},
 "revision":4,"digest":"sha256:bff35cc8...5ff","resources":[{"ref":"container/docker","kind":"container",
 "name":"docker","state":"discovered","source":["observed"],"count":6}],"counts":{"container":1}},
 "applied_revision":4,"online":true,"supported":true}
```

页面文案（`/tmp/int06-synced.png`）：`资源清单 [已同步] ｜ 清单版本 4 ｜ Agent 已应用版本 4 ｜ 最后同步时间 2026/11/24 10:26:38 ｜ docker / container · Agent 观测 / [已发现未登记]`。与 curl 逐项一致（`applied_revision == revision` 正是已同步判据）。

### B. Agent 离线（真实，杀掉 agent 进程）

kill 掉 agent 后 curl：`{"applied_revision":4,"online":false,"supported":true,"manifest.revision":4}`（`supported` 保留上次记录；第 1 条判据 `!online` 先命中）。
页面文案（`/tmp/int06-offline.png`）：顶栏变 `未上报`、机器卡 `离线`、`在线连接 0`；卡片 `资源清单 [Agent 离线] ｜ 清单版本 4 ｜ Agent 已应用版本 4 ｜ 最后同步时间 2026/11/24 10:27:08`；节点信息卡追加「Agent 离线，重新连接后可读取」。随后用同一 token 重启 agent，curl 回到 `applied 4 / online true / supported true`。

### C. 等待同步（数据构造 + 一次真实窗口观察）

- **真实窗口**：`sqlite3 UPDATE agent_resource_state SET manifest_revision = manifest_revision + 1` → 库内 `5|4`；12 秒内 Agent 收到新 revision 并 apply，curl 回到 `5|5`。自然发生的等待窗口 < 10 秒（短于卡片刷新周期），所以没能锁到帧。
- **稳定截图用构造值**：`UPDATE agent_resource_state SET supported=1, applied_revision=0`（revision 保持 8）→ curl `{"applied_revision":0,"manifest.revision":8,"online":true,"supported":true}`，满足第 3 条判据。
- 页面文案（`/tmp/int06-pending.png`）：`资源清单 [等待同步] ｜ 清单版本 8 ｜ Agent 已应用版本 暂无 ｜ 最后同步时间 2026/11/24 10:29:33 ｜ docker … [已发现未登记]`。
- 恢复：`supported=1` 后点卡片「重新同步」，出现绿色提示「已请求重新同步。」，curl 回到 `11|11`，卡片回到「已同步」。

### D. 不支持同步（数据构造）

- `UPDATE agent_resource_state SET supported=0`（online 保持 true）→ curl `{"online":true,"supported":false,"manifest.revision":9,"applied_revision":8}`，第 2 条判据命中。
- 页面文案（`/tmp/int06-unsupported.png`）：`资源清单 [Agent 不支持同步] ｜ 清单版本 9 ｜ Agent 已应用版本 8 ｜ 最后同步时间 2026/11/24 10:31:03`，下挂黄色提示「升级 Agent 后可同步观测结果，现有台账登记仍可查看。」，资源区为「此目标暂无资源记录。」，「重新同步」按钮 disabled（`components/target-manifest-card.tsx:103` 的 `disabled={busy || !data?.online || !data.supported}`）。
- 列表为空与实现一致：`backend/agent/internal/server/agent_resource_store.go:144-149` 在 `!supported` 时不读观测行，只合并台账登记（本机台账为空）。

## 偏差（发现的问题，仅建议，未改前端）

1. **资源类型枚举不全**：`components/target-manifest-card.tsx:16-19` 的 `resourceKinds` 里是 `container_engine: "容器引擎"`，但后端 target manifest 的 `kind` 是 `container`，于是 UI 回退显示英文 `container · Agent 观测`（三张有资源的截图都能看到）。建议在 `:18` 增加 `container: "容器引擎"`。
2. **「最后同步时间」名不副实**：`components/target-manifest-card.tsx:117-118` 渲染的是 `data.last_observed_at`（Agent 观测时间），不是「已应用」时间。等待同步截图里「Agent 已应用版本 暂无，最后同步时间 2026/11/24 10:29:33」并列，容易被读成已同步。建议改标签为「最后观测时间」。
3. **不支持同步态的三个字段仍在**：徽章说不支持同步，但「清单版本 9 / 已应用版本 8 / 最后同步时间」仍在刷新（观测 epoch 仍在推进）。语义上略矛盾，建议后端在该态不返回 revision/时间，或前端隐藏这三项。
4. 首次进入控制台会自动弹出「更新内容 v0.3.31」模态框，第一次「不支持同步」截图被它整个盖住，已重拍（上表 sha256 即重拍后）。

## 未覆盖与原因

- **自然发生的「等待同步」瞬时样本**未截到：实测窗口 < 10 s，短于卡片刷新周期；截图用的是 `applied_revision=0` 数据构造（真实 `5|4` 窗口有 curl 记录，但没锁到帧）。
- **真实旧 Agent（未宣告 `targetManifestV1`）**的不支持同步未验证：本轮用 `supported=0` 构造；该字段真实来源是 `agent_resource_sync.go` 的连接期能力检查，需旧版 Agent 二进制才能自然复现。
- 资源五态只验证了 `discovered`（本机真实观测只有 docker）；`verified / unverified / lost / conflict` 未构造。
- `kubernetes_available=false`（本机无集群）、多 cluster/多 instance、403 forbidden 态（admin 账号全权）未测。
- 未对 `/console?demo=1` 做回归；本轮全部走真实 Gateway 会话。

## 下一步

1. 建议由前端 owner 决定是否修 `components/target-manifest-card.tsx:16-19`（补 `container`）与 `:117-118`（改标签），以及 unsupported 态字段显隐；两处都在 5–10 行改动范围内，本任务按边界未动。
2. 若要把「等待同步」做成可稳定回归的态，建议 Gateway 侧提供测试钩子（例如暂停 apply），或前端测试用 mock 响应覆盖 `manifestStatus()` 纯函数。
3. 文档侧建议把「本机自建 Gateway+Agent 链路」步骤（迁移顺序、先迁移后重连 Agent、必须用 localhost 打开 Next dev）补进 `backend/docs/DOOPS_WEBUI.md` 或验收手册，避免下个会话再踩。

## 环境清理

本轮三个后台进程（Gateway 42322、Agent 42323、`pnpm dev` 3014）与 `/tmp/int06-src` worktree 在报告提交后关闭；截图与二进制留在 /tmp，未入库。
