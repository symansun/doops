# DoOps 用户引导混乱全仓扫描（2026-11-10）

状态：扫描完成；本轮修复了安装、首次登录、域名和 Web 引导中会误导用户/操作员的 P0 项；历史归档、评审证据和 legacy 测试夹具保持原样。

## 方法与范围

- 在仓库根执行 `git grep`，关键词覆盖：`doops.l8ai.cn`、`--target-name`、`--target-gateway`、`--target-cluster`、`--target-instance`、`login --target`、`login --gateway`、`doops add`、`get.doops.sh`、自动注册、cluster/instance。
- 排除不可跟踪产物（`bin/`、`node_modules/`、`.next/` 等）。
- 区分「当前用户指引」与「历史归档/评审证据/legacy 测试夹具」；后者不改写，避免伪造历史。

## P0：已修复的引导混乱

| 位置 | 原问题 | 修复 |
| --- | --- | --- |
| `README.md` | 安装说明假设管理员已给「Gateway、Token、target 信息」 | 改为只要求 Gateway + 登录凭据，target 首次登录自动发现 |
| `README.en.md` / `README.ru.md` | 仍教用户先 `doops add` 四参数再登录，和中文 README 不一致 | 同步为 gateway-first；四参数仅作为显式路由的例外 |
| `backend/README.md` | 安装示例仍要求 `--target-name/cluster/instance` | 主示例改为只给 `--target-gateway`；保留显式路由说明 |
| `components/onboarding-guide.tsx` | 展示不存在的 `curl https://get.doops.sh`，并声称安装会自动注册 Agent | 删除假命令和自动注册表述；改为「选择已接入 target；Agent 接入与授权由管理员完成」 |
| `components/connect-screen.tsx` | 网关输入框占位仍是 `https://doops.l8ai.cn` | 更新为 `https://doops.aiedulab.cn` |
| `backend/docs/CLUSTER_INSTALL_GUIDE.md` | 操作员登录示例先 `login --target`、且含旧域名 | 改为先 `login --gateway`；旧域名改新域名 |
| `backend/docs/DOOPS_USAGE_GUIDE.md` | 登录示例把 `login --target` 放在首登位置 | 首登/无路由改为 `login --gateway`；target 登录放到已有路由之后 |
| `backend/cli/cli/doops/main.go` | 无配置/无登录时只提示 `doops add` 四参数 | 先提示 `doops login --gateway`；`doops add` 作为离线路由兜底；无登录错误补充 gateway 方式 |
| `backend/cli/cli/doops/next.go` | 无 target 时的 `next:` 只给 `doops add` | 改为先给 `doops login --gateway`，再给显式路由兜底 |
| `backend/skills/config/servers.json` | 示例 Gateway 为旧域名 | 更新为 `https://doops.aiedulab.cn` |
| `backend/gateway/nginx/doops-proxy.conf` | 版本化代理配置只含旧域名 | 更新为新域名，和线上代理一致 |
| `backend/scripts/deploy-gateway.sh`、`deploy-web-console.sh`、`deploy-gateway-tls-proxy.sh`、`bootstrap-host-agent.sh` | 默认/校验 URL 仍是旧域名 | 更新为新域名 |
| `backend/deploy/instances/*`（values/compose/cutover） | 已部署/将部署的 Agent 仍指向旧域名 | 更新为新域名；下一次 Agent 发布即使用新地址 |
| `.cnb.yml` | CI 写路由仍用旧域名 | 更新为新域名 |
| `backend/docs/BUILD_DEPLOY_SOP.md`、`DOOPS_GATEWAY_TUNNEL.md`、`design/DOOPS_EDGE_TUNNEL.md`、`gateway/README.md` | 当前运维文档仍写旧域名 | 更新为新域名 |
| `backend/cli/cli/doops/gateway_targets.go` | insecure URL 错误示例用旧域名 | 改为新域名 |

## P0：安装契约与发布能力不一致（不是文档能单独解决的）

- 已发布 `v1.3.126`（commit `76abae08`）的 CLI **没有** `login --gateway` 首次登录发现能力；安装器要求四个 `--target-*` 全给才写配置。
- 首次登录发现能力在 main 的 `fc4b68ff`；当前 HEAD `d9ae9909` 已包含，但**没有任何 tag 包含它**。
- CNB main 仍是 `66769eef`，落后本地/GitHub main；所以「基于 CNB 最新代码」也拿不到该能力。
- 本轮已让 `backend/scripts/install.sh` 支持 gateway-only 模式（只给 `--target-gateway`，安装后提示 `doops login --gateway`；旧发布 CLI 会提示改用显式路由），并加了对应契约测试；但仍需下一个 release 才能到达用户。

## P1/P2：保留不改（避免伪造历史），但不应作为当前指引

- `docs/archive/**`、`docs/reviews/**` 的历史证据仍含旧域名/旧流程，属于当时现场记录。
- `backend/test/test_adopt_agent.py`、`backend/test/test_model_settings_secret_contract.py`、`lib/*.test.mjs` 中的旧域名是 legacy 夹具，不代表当前默认值。
- `docs/agent-ui-integration.md` 是历史验收记录（旧域名），保留原文。
- 工作区还有其它非本轮改动的未提交文件（agent runtime 等），与本扫描无关。

## 验证

- `python3 -m pytest -q backend/test`：349 passed，8 subtests passed
- `go test ./...`（`backend/cli/cli/doops`）：ok
- `npm run test:console`：556 passed
- `npx tsc --noEmit`：无错误
- `git diff --check`：干净

## 建议

1. 以 `backend/docs/CLUSTER_INSTALL_GUIDE.md` 作为唯一安装流程权威，README 三语版本只保留相同步骤和链接。
2. 立即发布包含 `login --gateway` + 本轮 installer/docs 修复的下一个 tag；否则新机器仍只能走四参数/旧 Skill。
3. 发布前把 `backend/deploy/instances/*` 的新域名值随 Agent 发布滚动，确认所有 Agent 重连后 `targets` 回到 10 个在线。
