---
name: 主机违规代理与内网穿透巡检 Skill
description: 只读检查主机违规代理、内网穿透进程、常见代理端口与环境变量
entrypoint: scripts/inspect.sh
---

# 主机违规代理与内网穿透巡检 Skill

## 核心检查项（严格只读）
1. **进程扫描**：检查是否存在 `clash`、`mihomo`、`v2ray`、`xray`、`sing-box`、`frpc`、`frps`、`nps`、`trojan`、`ss-local`、`shadowsocks`、`ngrok` 等未备案进程。
2. **端口扫描**：核查 `1080`、`3128`、`7890`、`7891`、`8118`、`10808`、`20170`、`20171` 等常见代理端口是否处于监听状态。
3. **环境变量核查**：只读检查 `http_proxy / https_proxy / all_proxy` 是否被设置（自动脱敏任何认证信息）。