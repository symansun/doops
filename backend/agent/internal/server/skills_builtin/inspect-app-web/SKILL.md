---
name: 应用服务与 Web 安全巡检 Skill
description: 检查本机监听服务端口、Nginx 配置基线及可选 HTTP/HTTPS 端点可用性与安全头
entrypoint: scripts/inspect.sh
---

# 应用服务与 Web 安全巡检 Skill

## 核心检查项
1. **监听端口盘点**：只读列出本机 TCP 监听端口与服务进程。
2. **Nginx 配置基线**：若节点安装 Nginx，执行只读 `nginx -t` 语法检查。
3. **HTTP/HTTPS 探测**：消费 `DOOPS_INSPECT_URLS`（逗号分隔；由 Gateway 自动注入该目标在资源目录已登记的 `website` 端点，最多 12 个且不含凭据或查询参数），探测状态码、延迟、TLS 证书剩余天数与 `Strict-Transport-Security` / `X-Content-Type-Options` 响应头，并自动桥接响应时间与错误率至 Agent 监控曲线。