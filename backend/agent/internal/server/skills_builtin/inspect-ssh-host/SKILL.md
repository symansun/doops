---
name: 远程主机只读 SSH 巡检 Skill
description: 对指定远程 Linux 主机执行只读资源与安全基线巡检（AI 驱动；当前平台无 AI 可见的受管 SSH 通道）
---

# 远程主机只读 SSH 巡检 Skill

## 执行形态（如实声明，不保留不成立的能力承诺）
- **AI 驱动、无确定性脚本**：本包不提供 `scripts/`，也不声明 `entrypoint`；由 AI 按下面的契约组织只读检查序列。
- **能力依赖缺口（重要）**：平台当前没有 AI 可见的受管 SSH 工具。`doops_ssh` 属于 clientCallable 通道，不在 AI 工具白名单（白名单当前仅 `mcp_doops_k8s_read`、`mcp_dosql_read`）；运行镜像虽有 `/usr/bin/ssh`，但没有 `/root/.ssh`（无密钥与 known_hosts）。
- 结论：在平台补齐 AI 可见的受管 SSH 通道、或目标机注入只读凭据之前，本 Skill **无法对远程主机确定性执行**，不得对外声称「已受管可运行」。此时应如实上报能力缺口，而不是伪造采集结果。

## 只读约束
1. 仅使用 `BatchMode=yes` 只读连接检查目标的 CPU、内存、磁盘、运行时间与内核安全基线。
2. 禁止写入远程文件、禁止交互输入密码、禁止在提示词或产物中输出任何凭据或私钥。
3. 目标不可达或依赖缺失时必须显式报错并给出原因（`status` 取 `warning`/`critical` 并在 `checks` 说明），禁止伪造数据或静默返回空结果。

## 输出格式要求
输出简明 Markdown 报告，并附一个 JSON 代码块，含 `status`（仅 `pass|warning|critical`）、`summary`、`metrics`（数组：`key/label/value/unit/status`）与 `checks`（数组：`id/name/category/status/value/detail`）。
