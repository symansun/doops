# DoOps Gateway 公网代理真实验收

日期：2026-09-13  
仓库：`doops.sh`  
分支：`feat/gateway-agent-http-proxy`

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

测试结束后：

- 临时 `proxy_routes` 路由已删除，并通过管理员接口确认不存在。
- 临时测试 token 已撤销。
- 本地 Agent 和测试网站进程已清理。

## 失败与修复记录

1. 从仓库根目录执行 `go build ./backend/gateway` 失败，原因是根目录不是 Go
   module。修正为分别在 `backend/gateway` 和 `backend/agent` module 中构建。
2. 固化 smoke 脚本的多行 `test` 断言被 Bash 拆参，导致 unary operator error。
   改为 `[[ ... == ... ]]` 字符串比较后通过。
3. 首次公网测试的 HTTP/1.1 chunk 测试夹具构造错误，导致预热 curl 等待。
   改为 HTTP/1.0、分段 flush、连接关闭的测试网站后通过。
4. 首次公网响应头断言未处理 HTTP/2 状态行尾随空格，实际响应已为 200。
   改为带空白容忍的正则断言后通过。

## 可重复命令

本地进程级验收：

```bash
bash backend/scripts/smoke-proxy-tunnel.sh
```

该脚本使用临时端口、临时 SQLite 和合成数据，覆盖 GET、POST、Host 路由、请求体、
响应头以及 Agent ready 状态。
