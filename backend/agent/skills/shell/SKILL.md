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

结构化工具（K8s/Helm 读取、Helm 发布、DoWeb、DoSql）没覆盖的事用 shell：文件、进程、
网络探测、包管理、镜像工具（`buildctl`、`skopeo`）等。

- 系统是 Ubuntu/Debian 或 Alpine，包管理器 `apt-get` 或 `apk`。
- 长命令加 `timeout`；关键步骤失败就停（`set -eu`）。
- 临时文件放 `/tmp/doops-*`。
- 凭据从环境变量或已绑定的受管凭据文件读取，不要打印出来。
- Go 代理用 `GOPROXY=https://proxy.golang.org,direct`。
- 在 `ask --apply` 里，K8s/Helm 变更用 `mcp_doops_helm`；shell 里的 kubectl/helm 变更不算发布证据。
