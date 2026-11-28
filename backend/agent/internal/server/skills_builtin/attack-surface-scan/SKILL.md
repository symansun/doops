---
name: 外部暴露面与安全基线扫描 Skill
description: L2+ 声明式能力插件：低速只读核查已登记站点的 OWASP 安全响应头、敏感路径暴露（抗 SPA 误报且零正文回显）、登录面暴露与可选 Nuclei 覆盖缺口
entrypoint: scripts/inspect.sh
---

# 外部暴露面与安全基线扫描 Skill

本 Skill 是 DoOps 内置的 **L2+ 声明式安全能力插件（`skill.attack-surface-scan`）**，源自 DoSecurity 授权低速扫描规范，与主机内部的 `host-security` 和基础探活 `inspect-app-web` 错位互补。

## 核心扫描能力

1. **完整 OWASP 安全头矩阵**：检查 `Content-Security-Policy`、`Strict-Transport-Security`、`X-Frame-Options`、`X-Content-Type-Options`、`Referrer-Policy`、`Permissions-Policy`，以及 `Server` / `X-Powered-By` 版本指纹泄露。
2. **8 类常见敏感路径低速只读探测**：在严格请求预算与限速下探测 `/.git/config`、`/.env`、`/swagger-ui.html`、`/swagger-ui/index.html`、`/v3/api-docs`、`/openapi.json`、`/server-status`、`/actuator/env`。
3. **防误报与防泄密双红线**：
   - 必须同时满足 `200 <= status < 300`、特征正则匹配且与根路径 SPA 兜底页面哈希不同，杜绝单页应用 200 误报；
   - 命中 `.env`/`.git` 等敏感路径时，**只记录路径、HTTP 状态码与规则名，绝不回显任何响应正文**。
4. **登录面暴露识别与可选 Nuclei 守卫**：信息级记录公开登录/统一认证入口；若开启可选 Nuclei 但未安装工具或只读 allowlist 模板，如实写入 `coverage_gaps[]`。

## 安全纪律

- 严格低速只读，严禁爆破、默认口令、SQL 注入利用、Metasploit、DoS 与 fuzzing。
- 本插件仅做安全评估与只读加固建议，不提供破坏性变更通道；配置加固请跳转 `website`（DoWeb）受控发布。
