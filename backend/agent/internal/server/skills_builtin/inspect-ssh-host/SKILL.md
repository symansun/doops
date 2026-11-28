---
name: 远程主机只读 SSH 巡检 Skill
description: 对指定远程 Linux 主机执行只读资源与安全基线巡检（AI 驱动；当前平台无 AI 可见的受管 SSH 通道）
---

# 远程主机只读 SSH 巡检 Skill

## 执行形态（如实声明，不保留不成立的能力承诺）
- **AI 驱动、无确定性脚本**：本包不提供 `scripts/`，也不声明 `entrypoint`；由 AI 按下面的契约组织只读检查序列。
- **能力依赖缺口（重要）**：平台当前没有 AI 可见的受管 SSH 工具。`doops_ssh` 属于 clientCallable 通道，不在 AI 工具白名单（白名单当前仅 `mcp_doops_k8s_read`、`mcp_dosql_read`）；运行镜像虽有 `/usr/bin/ssh`，但没有 `/root/.ssh`（无密钥与 known_hosts）。
- 结论：在平台补齐 AI 可见的受管 SSH 通道、或目标机注入只读凭据之前，本 Skill **无法对远程主机确定性执行**，不得对外声称「已受管可运行」。此时应如实上报能力缺口，而不是伪造采集结果。

## 受管 SSH 通道怎么走（唯一合法路径）
1. 平台唯一受管 SSH 通道是 `doops_ssh`（clientCallable），本 Skill **不能**直接调用它，必须由父会话或平台显式授予：把 `doops_ssh` 纳入 AI 可见工具白名单（当前白名单只有 `mcp_doops_k8s_read`、`mcp_dosql_read`），或由平台提供等价的只读受管通道。授权生效前一律不产生「已采集」的确定性结果。
2. 授权生效后也只能走受管通道：禁止自行拼装 `ssh` 命令行，禁止交互式密码，禁止把私钥或口令写进提示词、参数或产物。

## 通道不可用时的退化分支（不伪造）
- 通道不可用或目标不可达时，`status` 取 `warning`（无法采集/依赖缺失）或 `critical`（确知目标异常），并在 `checks` 中写入显式缺口：`id=managed_ssh_channel`、`name=受管 SSH 通道`、`category=capability`、`status=warning`、`value=unavailable`；`detail` 说明缺哪一步（例如「`doops_ssh` 未加入 AI 可见白名单，且镜像无 `/root/.ssh`」）以及如何授予（由父会话/平台把 `doops_ssh` 授权给本目标）。
- 此时 `metrics` 留空，不做任何估算。
- **为什么不用 `status=skipped`**：spec §4.1 的 `status` 枚举只有 `pass|warning|critical`，`skipped` 已被取消；本包与 SB-01/SF-07 整改口径一致，缺口用 `warning` 加 `checks[].detail` 表达。

## 目标自述
- 若环境提供 `DOOPS_TARGET_CLUSTER` / `DOOPS_TARGET_INSTANCE`，产物应带 `target: {cluster, instance}`；缺失取 `null`，不得编造。

## 只读约束
1. 仅使用 `BatchMode=yes` 只读连接检查目标的 CPU、内存、磁盘、运行时间与内核安全基线。
2. 禁止写入远程文件、禁止交互输入密码、禁止在提示词或产物中输出任何凭据或私钥。
3. 目标不可达或依赖缺失时必须显式报错并给出原因（`status` 取 `warning`/`critical` 并在 `checks` 说明），禁止伪造数据或静默返回空结果。

## 输出格式要求
输出简明 Markdown 报告，并附一个 JSON 代码块，含 `status`（仅 `pass|warning|critical`）、`summary`、`metrics`（数组：`key/label/value/unit/status`）与 `checks`（数组：`id/name/category/status/value/detail`）。
