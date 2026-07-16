---
name: doops-cicd
description: 生成、评审、预演和执行 DoOps 声明式构建与部署。v3 原生使用 deploy/deploy.yaml、公共服务定义和逐环境服务定义；由 doagent 自主规划、委派 Agent 并选择受信任工具。
---

# DoOps CI/CD

本 Skill 由 doagent 原生多 Agent 引擎执行。Skill 负责解释声明和约束，不内置命令式
流水线，不固定 Helm、镜像同步或 Kubernetes 工具，也不创建第二套编排器。

## 版本边界

- `doops.sh/v3` 是拆分目录模型。构建根为 `deploy/deploy.yaml`
  (`BuildDefinition`)，部署根为
  `deploy/environments/<environment>/environment.yaml`
  (`EnvironmentDefinition`)。
- `doops.sh/v2` 只保留既有 `DeploymentTemplate`、`SemanticRelease` 和
  `ServiceRelease` 契约。不得把 v3 声明降级、编译或复制成 v2 workflow。
- 一个执行请求中的所有引用文件必须使用同一 apiVersion。

执行 v3 前先读
[references/deployment-template.md](references/deployment-template.md)，执行
dry-run/apply 前再读
[references/execution-contract.md](references/execution-contract.md)。

## v3 目录

```text
deploy/
├── deploy.yaml
├── services/<service>.yaml
└── environments/<environment>/
    ├── environment.yaml
    ├── services/<service>.yaml
    └── chart/
```

解析顺序固定为：

1. 构建时读取 `deploy/deploy.yaml` 及其声明的 `deploy/services/*.yaml`。
2. 部署时以目标环境的 `environment.yaml` 为唯一根输入。
3. 按 `spec.services` 读取环境服务文件。
4. 每个 `EnvironmentService` 再读取一个公共 `ServiceDefinition`。
5. 汇总环境服务声明的精确镜像摘要和 Helm 绑定，只执行一次共享 release 收敛。

禁止搜索或推断 `deploy/environments.yaml`、`deploy/workflows/`、环境分支、历史
target、默认 registry、默认 namespace 或默认服务集合。

## 操作

- **generate**：按 v3 目录生成缺失声明；输出中文说明和标准字段名。
- **review**：只读校验完整引用链，不接触目标环境。
- **dry-run**：观察事实并返回尚需执行的 mutation，`mutationCount` 必须为 0。
- **apply**：仅在受信任请求显式授权后执行，并在每次 mutation 后重新观察。

## v3 部署规则

- 命令行只传环境声明允许的操作意图，例如 `reason`；不得覆盖 target、服务、
  repository、digest、副本、Chart、values、namespace、release 或 Secret 引用。
- `EnvironmentDefinition` 必须直接声明 application、source、DoOps target、Helm、
  artifact policy、services、健康检查、回滚和停止条件。
- `EnvironmentService` 必须声明公共服务引用、源/目标镜像、不可变 digest、Helm
  value、工作负载、容器、副本和验收。
- 全量环境发布由 `environment.yaml` 明确列出全部服务，不使用 `all` 作为目录扫描。
- 镜像验证和迁移可以并行；共享 Helm release 只能汇总后原子执行一次。
- Helm 前必须取得当前 release revision 和全部工作负载镜像身份作为回滚基线。
- mutation 后失败必须恢复已观察基线并返回真实回滚证据；未知结果停止观察，不重试。

## 结果

review 返回一个 `DeploymentReview`；dry-run/apply 返回一个与根声明相同 apiVersion
的 `DeploymentRun`。结果必须绑定 workspace commit 和当前 turn 的受信任工具证据。
Agent 先写 `toolRef`，Gateway 再绑定不可改写的 `toolCall`。普通 Bash、文件读取和
浏览器输出不是部署证据。

当 Ask 请求使用 `response_format=json` 时，final response 就是唯一机器结果：
只输出一个 JSON object，不得使用 Markdown、YAML、结果文件、外围文本或第二个
JSON object。Gateway 注入的 `toolCall.result` 是摘要验证通过并移除 transport
`_meta` 后的结果；Agent 不得改写或补充该结果。

## 安全

- 不读取或输出 Secret 内容，只使用声明中的 Secret 引用元数据。
- 不使用 fallback、静默降级、猜测默认值、旧证据或文本成功。
- 缺少声明、权限、凭据引用、运行时能力或回滚基线时立即阻断。
- v2 自托管 Agent 的 detached executor 继续使用
  `helm_detached_job.py submit`，且 control target 必须与被替换工作负载隔离。
- 不生成 deploy shell，不调用旧 `pipeline`、`shell`、`k8s` 或 `image-build`
  编排 Skill 代替原生受信任模块。
