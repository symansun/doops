---
name: 远程主机只读 SSH 巡检 Skill
description: 对指定远程 Linux 主机执行只读资源与安全基线巡检
---

# 远程主机只读 SSH 巡检 Skill

## 执行约束
1. 仅使用 `BatchMode=yes` 只读连接检查目标主机的 CPU、内存、磁盘、运行时间与内核安全基线。
2. 禁止写入远程文件、禁止交互输入密码、禁止在提示词或产物中输出任何凭据。

## 输出格式要求
输出简明 Markdown 报告与结构化 JSON 代码块（含 `status`、`summary`、`metrics`、`checks`）。