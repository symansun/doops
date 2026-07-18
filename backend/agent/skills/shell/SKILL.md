---
name: shell
description: 结构化工具未覆盖时的受约束 Shell 操作知识
triggers:
  keywords: []
phase: [analyze]
priority: 5
max_tokens: 600
requires: []
conflicts: []
---

Shell 只用于结构化工具没有覆盖的文件、进程、构建和只读诊断操作。不得把完整自然语言
任务翻译成待执行脚本，也不得生成固定发布阶段或替代 doagent 的规划与工具调用。

【命令生成规范】
1. **可靠性优先**：在关键命令前加 `set -euo pipefail`，确保一条失败即停止。
2. **幂等性**：使用目标系统已有的幂等接口，不用删除重建掩盖状态问题。
3. **可见性**：在关键步骤前加 `echo ">>> Step N: 描述"` 以便追踪进度。
4. **超时保护**：长命令（如 `buildctl build`、`go mod download`）应使用 `timeout` 包裹。
5. **清理**：临时文件放在 `/tmp/doops-*`，脚本结束时自动清理。

【环境感知】
- 操作系统：Ubuntu/Debian 或 Alpine
- 包管理器：`apt-get` 或 `apk`
- 镜像构建工具：BuildKit（使用 `buildctl --addr unix:///run/buildkit/buildkitd.sock build`）
- 私有镜像仓库：`registry.example.com`（Agent 已预写 BuildKit 认证，无需额外 login）
- Go 代理：`GOPROXY=https://proxy.golang.org,direct`（不使用 goproxy.cn，它会限流）
- Kubernetes mutation 必须使用 `doops_k8s`；shell 中禁止 `kubectl apply`、`patch`、
  `set image`、`rollout restart`、`scale`、`delete`、`annotate`、`label` 或 `edit`

【敏感信息处理】
- 如果需要 sudo，使用 `sudo` 前缀而非手动 echo 密码
- 密码和 token 通过环境变量传递，永不硬编码
- 不要在命令输出中泄露任何凭据
