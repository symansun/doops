---
name: image-build
description: 容器镜像自动构建专家
triggers:
  keywords: [dockerfile, 镜像, 编译, 构建, compile, build, "source code", 打包, 打镜像]
  regex: "docker\\s+build|镜像构建|构建镜像|打.*镜像"
phase: [analyze, translate, cleanup]
priority: 20
max_tokens: 1200
requires: [shell]
conflicts: []
---

为已明确的构建任务提供 BuildKit 命令与验证条件；仅构建不包含推送或部署。

## 输入与准备

- 确认构建目标、session 工作区、源码版本、平台和产物位置。复用已获授权的范围，
  缺少推送目的地时只暂停推送，不猜仓库、tag 或部署对象。
- Agent 使用 `doops push` 已同步的 session 工作区；上位调用方负责 `push/pull`。
  不用 SCP、base64 或重新打包绕过同步，也不把用户工作区静默替换为 `git archive HEAD`。
- 优先读取已有 Dockerfile、锁文件和构建脚本。语言版本、CGO、Go 入口、Java jar、Node
  静态/服务端模式和代理设置以项目事实为准；`.dockerignore` 保留实际 COPY 所需的产物。
- Agent 的构建工具是 `buildctl`；核验二进制和配置的 BuildKit endpoint 可用。
  默认 socket 为 `unix:///run/buildkit/buildkitd.sock`，不可用即报告原因，不改用其它构建器掩盖错误。
- 仓库认证只有在启动配置完整时才写入；仅检查存在性和授权结果，不读取、打印认证内容。

## 输出与授权

- 仅构建时显式导出 OCI 归档，例如 `--output type=oci,dest=<task-output>/image.tar`。
  不以构建缓存作为已交付镜像，不默认附加 `push=true`。
- 已授权推送时才用 `--output type=image,name=<approved-image>,push=true`，目的地及版本来自任务。
  默认使用 TLS；`registry.insecure=true` 仅用于任务已批准且配置确实需要的特定仓库。
- 生产发布沿用项目 CI 构建产物；不要在业务 target 上临时构建替代 CI。
  部署是独立阶段，只有任务同时授权且镜像验证通过才交给指定目标的发布流程。
- 多阶段、非 root 和缓存优化按项目需要应用；不覆盖现有运行要求或删除必需文件。

## 完成与清理

- 每条独立 shell 步骤都带明确工作目录，不能依赖上一步的 `cd` 或局部变量。
- 长任务使用执行环境已有的任务跟踪能力，保留任务 ID、日志和退出码；提交成功不等于构建成功。
- 构建须退出 0 且存在有效产物；OCI 归档检查结构和 digest，推送核对 registry 中的目标 digest。
  失败停止依赖步骤；只有日志片段或连接中断时报告结果未知，不自动重放推送或部署。
- 临时目录用本次任务独占的 `mktemp -d` 创建；任务结束后才清理自己创建且不再使用的文件。
  失败保留必要诊断，不删除源码工作区、待交付镜像或仍在后台使用的上下文。
