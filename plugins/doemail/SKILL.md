---
name: doemail
description: DoEmail 独立可安装插件包：按需核查邮件域 A/MX/SPF/DKIM/DMARC/rDNS、SMTP/IMAPS 探活与 K8s 邮件运行时健康，并自带独立 stdio MCP Server 执行零明文密码的账号规划与清单校验。
entrypoint: scripts/inspect.sh
exec_mode: auto
---

# DoEmail 邮件服务治理插件

本插件包为完全自包含的 DoOps 外部可安装插件（不耦合 DoOps 内核二进制或基础镜像），集成邮件域/运行时观测脚本、资产目录 Schema（`plugin:doemail:domain`）与独立 stdio MCP Server（`mcp/server.mjs`）。

## 核心安全红线

1. **零明文密码**：任何账号清单、工具入参、变更计划、审计记录或输出中严禁包含明文密码或令牌；所有账号操作强制使用 `passwordSecretRef`（例如 `k8s://mail/secret/ops/password`）。
2. **双层验证**：宣称域名接入完成前须同时核验 `A` / `MX` / `SPF` / `DKIM` / `DMARC` / `rDNS` 与 SMTP/IMAPS/Pod/PVC 运行态。
3. **有界监听**：事件监听严格限定 `run-once` 模式，涉及发信、删信、建账号或改 DNS 均须显式审批。

## 自带 MCP 工具（`mcp/server.mjs`）

- 只读工具：`dns_plan`、`dns_evaluate`、`integration_guide`、`k8s_inspect_plan`
- 受控变更工具：`account_create_plan`、`listener_plan`、`validate_manifest`
