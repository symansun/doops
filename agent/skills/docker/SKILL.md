---
name: docker
description: Docker 容器运维领域知识
triggers:
  keywords: [docker, container, 容器, 镜像, registry]
  regex: "docker\\s+(build|run|push|pull|compose|logs|inspect|ps|stop|start|rm|exec)"
phase: [analyze, translate, cleanup]
priority: 10
max_tokens: 500
requires: [shell]
conflicts: []
---

按任务选择检查或变更；只读诊断不启动、删除、重建或清理容器。

## 对象与环境

- 先确认 Docker 客户端、daemon/context 和明确的容器 ID 或名称，不假设 Agent 内有 Docker。
- 用精确对象的 `docker inspect` 查询必要字段，避免 `grep` 子串误选；不输出含凭据的完整环境配置。
- “对象不存在”和 daemon 不可用、权限不足是不同结果；后两者必须停止，不能吞错后创建替代对象。
- 镜像、仓库、认证、网络、端口与卷来自任务和已核验配置，不假设 IP 白名单免登录。

## 生命周期

- 已存在容器的启动使用 `docker start <exact-name>`；创建新容器前确定镜像和所需配置。
- 替换仅在删除/重建影响已获授权时进行；先记录镜像、卷、网络、端口、健康检查和回退路径。
  不将 `docker rm -f` 当通用幂等步骤，不用 `|| true` 隐藏停止、删除或创建失败。
- 日志默认有限条数，如 `docker logs --tail 50 <exact-name>`；无人值守命令不使用 `-it`。
- `docker system df` 可用于诊断；prune、删镜像和删卷需要明确对象及清理范围，不能作为构建失败的自动修复。
- 变更成功须有容器状态、实际镜像和适用健康检查证据；仅进程运行不代表应用健康。

## 镜像与网络

- BuildKit 构建流程按 image-build skill；保留现有 Dockerfile 和项目运行要求。
- Dockerfile 的 `USER` 是独立指令。Alpine 新建用户示例：

```dockerfile
RUN adduser -D appuser
USER appuser
```

- 用户创建命令按基础镜像选择；非 root 用户须有必要目录权限。
- 发布镜像用明确版本或 digest；`EXPOSE` 不等于发布主机端口。
- 仅按已授权访问范围创建网络、端口映射或 Kubernetes Service，不默认扩大外部可达性。
