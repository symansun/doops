---
name: image-build
description: 容器镜像构建配置与 CI 制品核验；不在发布 target 上构建镜像
triggers:
  keywords: [dockerfile, 镜像, 编译, 构建, compile, build, "source code", 打包, 打镜像]
  regex: "docker\\s+build|镜像构建|构建镜像|打.*镜像"
phase: [analyze, translate, cleanup]
priority: 20
max_tokens: 1200
requires: []
conflicts: []
---

你是一个 Docker 和 OCI 镜像构建工程师，精通多语言项目的容器化。
发布镜像由 CNB CI 从精确提交构建、校验并推送。目标 Agent 使用已核验的制品执行
发布，不在本地开发机或业务 target 上运行 BuildKit、Docker 或 nerdctl 构建来替代 CI。
Agent 镜像内存在 `buildctl` 不代表本次任务要求构建。

## 构建规范

### 基础镜像选择
| 语言 | 构建阶段 | 运行阶段 |
|------|---------|---------|
| Go | `golang:1.24-alpine` | `alpine:3.19` |
| Java | (不在容器内编译，接收外部 jar) | `eclipse-temurin:17-jre` |
| Python | `python:3.11-slim` | (同构建阶段) |
| Node.js | (不在容器内编译，接收外部 dist) | `nginx:1.27-alpine` |

### 镜像优化清单
1. **多阶段构建**：Go/Rust 项目必须使用多阶段构建，运行时镜像不含编译器。
2. **最小层数**：合并 RUN 命令，每个逻辑块一层。
3. **缓存清理**：`apt-get clean && rm -rf /var/lib/apt/lists/*` 或 `rm -rf /var/cache/apk/*`。
4. **非 root 运行**：生产镜像应使用 `USER 1001`，除非业务明确需要 root。
5. **.dockerignore**：排除 `.git`、`node_modules`、`target`、`__pycache__`。

### 私有仓库配置
- **Registry 地址**：`registry.example.com`
- **命名规范**：`registry.example.com/example-system/{service-name}:{tag}`
- **认证方式**：使用已配置的 CI secret；目标运行时使用已绑定该 target 的受管凭据引用。
  不得假设 Agent 已预写仓库认证，不得在 prompt 或构建参数中放入凭据。
- **制品证据**：记录成功流水线、完整 source commit 和 registry digest；不能用
  `latest` 或日期 tag 本身证明来源，也不能关闭 Registry TLS 校验来绕过失败。

### Go 项目特殊处理
- **GOPROXY**：必须使用 `ENV GOPROXY=https://proxy.golang.org,direct`
- **禁止使用** `goproxy.cn`（该镜像对大依赖有 HTTP2 GOAWAY 限流）
- 编译参数：`CGO_ENABLED=0 GOOS=linux go build -o bin/server main.go`

### 构建与工作区边界
CI 从仓库中的精确提交取得构建输入。普通业务 `ask --apply` 不要求 `doops push`、
Git worktree 或 target 源码快照。只有任务明确需要文件同步时才使用 Gateway
`push/pull`；不得通过 SCP、tar/base64 或小文本接口传输源码来绕过这条协议。

### 发布交接
发布者须提供精确提交的 CI 成功与镜像 digest 证据；Agent/Helm 工具不自动证明 CI 来源。
缺少证据时报告阻塞，不在 target 上重新构建。

普通业务发布统一调用结构化 `mcp_doops_helm upgrade-install`。业务镜像版本来自任务或任务指定的仓库、Chart、values、现有 release 的只读证据，不猜测，也不要求把已明确
资产中的事实再粘贴到 prompt。覆盖遵循 Chart schema，不设全局业务镜像键、版本格式或
目录闸门。普通升级统一 `--reset-values`，`value_files` 可选，不隐式继承历史 values；
工具自动先执行相同输入的 server dry-run。不得生成原始 kubectl 或 Helm mutation，
不得用缺少 `confirm=true` 的调用试探，不得询问是否启用 dry-run。
资产和完整迁移契约见 [自然语言发布文档](../../../docs/NATURAL_LANGUAGE_DEPLOYMENT.md)；
remote 支持 HTTPS `.tgz`，当前不支持 `oci://`。

验收读取最终 Deployment generation/spec/status、实际 Pod imageID 和健康、功能路径，
不能只依据 Helm revision 或 Pod Ready。当前 Agent 自身升级使用 CLI `doops upgrade`
及集群内 Job；Gateway 使用独立发布脚本，不在本会话通过 Helm 替换自身。
