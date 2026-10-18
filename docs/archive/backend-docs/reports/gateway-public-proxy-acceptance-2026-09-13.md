# DoOps Gateway 公网代理真实验收

日期：2026-09-13  
仓库：`doops.sh`  
分支：`fix/release-kubelet-image-pull`

## 验收目标

验证以下真实链路：

```text
公网 HTTPS 域名
  -> Nginx TLS
  -> doops-gateway
  -> Agent 主动建立的 WSS
  -> Agent 所在网络的本地网站
```

## Gateway 发布证据

- Gateway 功能提交：`b8e8d35a7f33d761f29e5e4ceb68dad4b7b46166`
- 发布主机：`106.54.197.139`
- systemd 服务：`doops-gateway`
- 运行端口：`42222`
- 发布二进制 SHA-256：
  `aba522e29265db7a4076c10c17c06616faa9f275c99e5452618561d845aab590`
- 运行中的 `/proc/<pid>/exe` 与发布文件 SHA-256 一致
- 回滚备份：
  `/usr/local/bin/doops-gateway.rollback-dd89008f3000-20260913T072828Z`
- 发布后 `http://127.0.0.1:42222/health`：`200`
- 发布后未授权 `/v1/targets`：`401`
- 公网 `https://doops.l8ai.cn/health`：`200`
- 发布后通过 `gw-edu-coder` 执行无副作用 `printf` 命令：成功

发布脚本的回滚保护已启用并验证了备份、替换、重启、进程 hash、健康检查和原有
Agent 重连；本次验证全部通过，因此没有触发回滚。

## 公网代理验收

测试使用临时合成路由和本地测试网站，结束后删除路由，不修改真实业务域名。

- 临时公网 Host：`proxy-smoke-20260913.l8ai.cn`
- Gateway 公网 IP：`106.54.197.139`
- Agent：`public-final-20260913/local-agent`
- Agent 上游：`127.0.0.1:<临时端口>`
- Agent `/health/ready`：HTTP `200`
- 公网 HTTPS 响应：HTTP/2 `200`
- 上游响应头 `x-upstream: public-final-smoke`：保留
- 上游分段响应：
  - `stream-first`
  - `stream-second`
- 两段到达间隔：`0.804s`
- 结果：`public_proxy_e2e=PASS`

当前发布版本的再次复验使用临时 Host
`proxy-debug-20260913.l8ai.cn` 和 Agent
`proxy-debug-20260913/local-agent`，通过 WSS 连接到公网 Gateway；公网请求返回
`HTTP/2 200`，上游返回头 `x-upstream: public-debug`，响应体
`public-debug-first` / `public-debug-second` 完整返回。测试路由已删除，管理员接口复查
为空，测试 Agent 和网站进程已清理。

### 本地 Agent 经公网 Gateway 复验

2026-09-13 再次按用户路径执行真实测试，不使用本地 Gateway：

- Agent 源码 revision：
  `15d81b2730f12f0d047295a966ee2a41146524ee`
- 本地 Agent 二进制 SHA-256：
  `7e5744a832722ef22a54d22dfe6c86d6bb15f2dee87b101090b98062e3d0e1fe`
- 临时 Agent：`proxy-local-20260913/codex-mac`
- Agent 主动连接：`wss://doops.l8ai.cn/v1/agent/connect`
- Agent 上游 allowlist：`127.0.0.1`
- 临时内网站点：`http://127.0.0.1:53620`
- 临时公网 Host：`proxy-local-20260913.l8ai.cn`
- Gateway 公网 IP：`106.54.197.139`
- GET：HTTP/2 `200`，远端 IP `106.54.197.139`
- GET 上游响应头：`x-upstream: local-codex-agent`
- GET 两段响应间隔：`0.755s`
- POST：HTTP/2 `201`，请求体返回
  `local-post=through-public-gateway`
- 结果：`local_agent_public_gateway_e2e=PASS`

本次链路为：

```text
公网 HTTPS 请求
  -> 106.54.197.139 Nginx/TLS
  -> doops-gateway
  -> 本地 Agent 主动建立的 WSS
  -> 仅监听 127.0.0.1 的临时网站
```

公网 DNS 仍将 `*.l8ai.cn` 解析到 `89.250.81.244`，不是当前 Gateway
`106.54.197.139`，因此本次用 `curl --resolve` 固定入口 IP，同时保留真实 Host 和
有效通配符 TLS 校验。该结果证明公网 Gateway 与单向出站 Agent 的反代链路可用，
但不构成 DNS 已切换的证据。

测试结束后：

- 临时 `proxy_routes` 路由已删除，并通过管理员接口确认不存在。
- 临时测试 token 已撤销。
- 本地 Agent 和测试网站进程已清理。
- 清理后相同 Host 经 Gateway 返回 `404`。
- Gateway 管理接口确认 `proxy_route_count=0`。

## 114 Agent 升级状态

目标 `gw-114-k8s` 已通过外部控制器完成升级并完成公网反代验收：

- 当前 Helm release：`doops-agent-gateway`
- 发布前 Helm revision：`12`
- 发布前镜像：
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:b72f26ce37d49dc5daf0413ce4136d6e9b15e7a5bfaa636ec708d1ff9ed9791a`
- 候选源码 revision：
  `15d81b2730f12f0d047295a966ee2a41146524ee`
- 候选镜像：
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:dc5d1ad170ba19389b98e088a1fd17fdfd54bfe1ccc6ecf5dd8eed47ecff0e9a`
- CNB build：`cnb-rug-1k2ddtt96`，全量流水线成功
- 外部控制器：操作机临时 kubeconfig，经 SSH TCP 转发访问 114 API；Helm
  server-side dry-run 通过，当前 Agent 未执行 Helm mutation
- 发布后 Helm revision：`13`，状态 `deployed`
- 发布后 Deployment：`1/1 Ready`，策略 `maxSurge=1/maxUnavailable=0`
- 发布后 Pod：`doops-agent-gateway-7f4875c87-nmdck`
- 发布后镜像：
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:dc5d1ad170ba19389b98e088a1fd17fdfd54bfe1ccc6ecf5dd8eed47ecff0e9a`
- 发布后镜像 ID 与候选 digest 一致，重启数 `0`
- Gateway target：`doops-114-k8s/master-1`，发布后在线且 idle

本次修复了发布脚本的真实根因：已有集群发布现在必须由操作机提供 kubeconfig，
脚本在 Agent 进程外执行 Helm dry-run、mutation、候选 Pod/事件诊断和自动 rollback；
Gateway target 只承担心跳、接管和功能验证。这样不再要求旧 Agent 镜像携带
`nerdctl`、`docker` 或 `crictl`，也不会让当前 Agent 修改承载自身的 Deployment。

发布过程保持 Gateway target 在线，完成后复查：

- `doops admin operations list --target gw-114-k8s`：无 active operation
- Agent 临时站点 `127.0.0.1:18081` 已停止，端口已关闭
- 临时路由 `route_36f1374a20bc85c5e42a5d34` 已删除
- `GET /v1/admin/proxy-routes`：`proxy_route_count=0`
- `https://proxy-114-e2e-20260913.l8ai.cn/`：HTTP `404`

## 失败与修复记录

1. 从仓库根目录执行 `go build ./backend/gateway` 失败，原因是根目录不是 Go
   module。修正为分别在 `backend/gateway` 和 `backend/agent` module 中构建。
2. 固化 smoke 脚本的多行 `test` 断言被 Bash 拆参，导致 unary operator error。
   改为 `[[ ... == ... ]]` 字符串比较后通过。
3. 首次公网测试的 HTTP/1.1 chunk 测试夹具构造错误，导致预热 curl 等待。
   改为 HTTP/1.0、分段 flush、连接关闭的测试网站后通过。
4. 首次公网响应头断言未处理 HTTP/2 状态行尾随空格，实际响应已为 200。
   改为带空白容忍的正则断言后通过。
5. 114 原发布脚本要求旧 Agent 在容器内预拉取镜像，但旧镜像没有容器运行时
   CLI，同时该路径让 Agent 自升级变得可达。改为操作机外部 Helm 控制器，并加入
   server-side dry-run、候选失败诊断、接管验证和失败自动回滚。
6. 首次 114 外部控制器 dry-run 发现内嵌 Python import 错误，修复后重新通过
   本机 API server dry-run，未产生 Helm revision。

## 可重复命令

本地进程级验收：

```bash
bash backend/scripts/smoke-proxy-tunnel.sh
```

该脚本使用临时端口、临时 SQLite 和合成数据，覆盖 GET、POST、Host 路由、请求体、
响应头以及 Agent ready 状态。
