---
name: shell
description: 结构化工具没覆盖的操作用 shell 完成
triggers:
  keywords: []
phase: [analyze]
priority: 5
max_tokens: 400
requires: []
conflicts: []
---

结构化工具没覆盖的文件、进程、网络探测或镜像诊断，优先使用当前 Auto ask 会话提供的
`Bash` 工具在目标 Agent 执行环境中完成。需要用户可复核的确定性命令、完整 Gateway
执行审计或后台任务时，交给上层通过 `doops -session <s> exec --target <t>` 执行。
不要因为工具名单或交互审批 UI 缺失而退回让用户手工执行。
只处理目标和范围已明确的任务；仅方案/诊断不变更，缺关键目标或授权时返回缺项，
不猜测对象。已有具体授权沿用，不逐命令重复确认。

- 按已核实的 OS、工具和身份生成命令；不固定包管理器，不因缺 sudo 自动安装或提权。
  代理、入口和工作目录取自项目及调用环境；每次 exec 需要工作区时显式 cd。
- Bash 脚本用 `set -euo pipefail`；其它 shell 保留等价的关键步骤和管道失败检查。
  不用 `|| true` 吞掉验证、权限或连接失败，不把删除重建当通用幂等手段。
- 长命令设有界超时，由上层使用 `exec --bg` 等待终态；不自行 nohup 或放到后台。
- 需要临时文件时用 `mktemp -d /tmp/doops-XXXXXX`；只清理本轮独占目录，不清理前缀通配符。
  依赖它的任务结束后才清理；失败保留必要诊断并报告位置。
- 使用受管凭据引用；不打印、展开或把秘密内联进命令、prompt、日志、工作区，禁用敏感链路的 xtrace。
- K8s/Helm 发布仍用 `mcp_doops_helm`；运维变更交上层
  `doops -session <s> k8s --target <t> ... --confirm`，数据库变更交上层
  `doops -session <s> sql --target <t> --mode apply --sql/--file`，不改用 shell。
