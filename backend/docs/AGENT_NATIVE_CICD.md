# DoOps Agent 原生 CI/CD

本文是 DoOps 当前 CI/CD 职责和发布路径的唯一权威说明。

## 核心结论

普通业务 workload 的标准发布链路是：

```text
Git main / CI
  -> 构建并发布不可变镜像 digest
  -> doops ask --apply
  -> Gateway 鉴权、审计、并发控制和凭据预物化
  -> 目标 doops-agent
  -> doagent build mode
  -> 目标环境内的结构化 Kubernetes 工具或任务明确授权的 Helm
  -> rollout、实际镜像、健康检查和功能路径证据
```

当前系统不使用以下发布根：

- `EnvironmentRegistry`、`EnvironmentDefinition` 或 `DeploymentTemplate`；
- `deploy/environments.yaml`；
- `doops cicd run`；
- workflow YAML 驱动的环境推断；
- `CICDReport` 或 `doops_cicd_result` 恢复协议。

`deploy/helm/<name>/` 只保存运行时 Helm 资产，不保存环境、target、凭据或发布授权。

## 参与者职责

| 参与者 | 负责 | 不负责 |
| :--- | :--- | :--- |
| Git/CNB CI | 测试、构建、扫描、发布不可变镜像和 CLI Release Asset | 选择生产 target、执行业务集群 mutation |
| 发布调用方 | 选择 target，提供镜像 digest、范围、原因、验收条件和凭据资源名 | 推断未声明环境或向任务内联 secret |
| DoOps CLI | 编译 `ask --apply` 请求、绑定 session 和 configured target | 保存 target 私有 token、读取凭据 payload |
| Gateway | 用户/target/action 鉴权、审计、限流、资源锁、凭据授权与预物化 | 在 Gateway 主机上执行业务 Helm/Kubernetes mutation |
| 目标 Agent/doagent | 在目标运行环境执行发布计划并返回真实工具证据 | 接收 prompt 中的明文凭据、替调用方补全发布事实 |

Gateway 会把凭据 payload 通过内部控制面临时发送到目标 doops-agent 的凭据物化工具。
公开 prompt、doagent 上下文、CLI 输出、审计尾部和 metadata binding 都不包含 payload。

## 发布前置条件

发布调用方必须掌握以下事实：

1. 已合入 `main` 的完整 40 位 source commit。
2. CI 为该 commit 成功构建的镜像 repository 和不可变 digest。
3. configured Gateway target。
4. 要变更的服务或 workload 范围。
5. 允许使用的 `deploy/helm/<name>/` 资产；纯镜像发布可以不指定 Helm。
6. 变更原因。
7. 可由真实工具检查的验收条件。
8. 所需 managed credential 的资源名及其精确 grant。

缺少任一必要事实时必须阻塞，不得从目录、环境名、历史记录或 credential 名称推断。

## 标准发布

无凭据的纯镜像发布：

```bash
doops -session release-<commit> ask \
  --target <configured-target> \
  --apply \
  --msg '在 <target> 发布 <service-scope>，镜像 <repository>@sha256:<digest>；原因：<reason>；验收：Deployment rollout 完成、实际容器镜像为该 digest、Service/Endpoint 正常、/health 和关键功能路径通过。'
```

使用一个或多个受管凭据：

```bash
doops -session release-<commit> ask \
  --target <configured-target> \
  --apply \
  --credential <registry-credential-name> \
  --credential <tls-credential-name> \
  --msg '在 <target> 的 namespace <namespace> 发布 deployment/<name>，镜像 <repository>@sha256:<digest>；使用 deploy/helm/<chart>/；原因：<reason>；验收：Secret 类型和 key 集合正确、rollout 完成、实际镜像 digest、Service/Endpoint、/health 和关键功能路径通过。'
```

`--credential` 只传资源名且必须与 `--apply` 一起使用。Gateway 根据任务生成 metadata-only
scope plan，并要求它与持久 grant 的 cluster、instance、namespace、use、workload、
image 或 Helm 字段完全一致。

## Gateway 和 Agent 执行顺序

1. Gateway 校验 user token 对 target 的 `exec` 权限。
2. Gateway 检查任务没有内联 token、password、cookie 或其他已知 secret 形式。
3. Gateway 根据显式 credential 名称生成 scope plan。
4. Gateway 检查用户拥有 `credential:use`，并精确匹配 active grant 和 active version。
5. Gateway 在目标 Agent 内物化并验证 Secret 或外部凭据。
6. Gateway 只把资源名、用途、namespace、Secret 名、类型、key 集合、resourceVersion 和
   digest 等 metadata binding 传给公开 Agent prompt。
7. doagent 以 build mode 执行任务；apply 必须产生本轮真实工具调用证据。
8. 已知 apply 失败时，Gateway 回滚本轮完成的凭据预物化；结果不确定时明确返回
   `credential_apply_outcome_unknown`，不得重放 mutation。

## Kubernetes 与 Helm 边界

- 运行契约完全不变的纯镜像发布，使用结构化 `doops_k8s set-image`，显式提供
  namespace、resource、container、digest、revision 和 `confirm=true`。
- command、args、env、probe、volumeMount 或 volume 变化时，必须使用任务明确授权的
  Helm 资产收敛完整 workload。
- shell 中的 `kubectl apply/patch/set image/rollout restart` 不构成标准发布证据。
- 普通前台 `doops exec` 可以执行操作者明确给出的 Helm 命令，但它是低层确定性操作，
  不替代标准 `ask --apply` 发布编排，也不能使用 `--credential`。
- `exec --bg` 不允许承载 doops-agent Helm mutation。

## 自托管组件例外

目标 Agent 不能修改承载自身的 workload：

- `doops upgrade` 只允许 `--dry-run`。
- doops-agent 自身升级必须由另一个隔离的 Agent target，或仓库外真实存在的
  CI/GitOps controller 执行。
- `doops-gateway` 使用 `scripts/deploy-gateway.sh` 从 CI 镜像提取同 revision 二进制发布。
- Web 控制台使用不可变 Web 镜像和 `scripts/deploy-web-console.sh` 发布。

“external release runner”是执行角色，不代表仓库中存在另一套 release-runner 引擎。

## 成功证据

发布成功至少需要：

- CI 状态成功，source commit 与镜像 digest 对应；
- mutation 工具返回成功；
- rollout 完成且期望副本 Ready；
- workload 实际镜像为请求中的 digest；
- Service/Endpoint 或等价流量入口正确；
- 任务指定的健康检查和关键功能路径通过；
- 使用凭据时，只输出 Secret 类型、key 集合、resourceVersion、digest 或错误类别，不输出
  value。

任务文本、自报完成、计划、admission 成功或镜像 tag 本身都不是发布成功证据。

## 失败和回滚

- 权限、target、digest、scope 或 Helm 资产缺失：返回 blocked，不执行 mutation。
- rollout 或功能验收失败：保留真实错误，根据任务明确授权的可逆能力回滚。
- 凭据物化失败：返回稳定错误类别，不把 payload 放进日志或任务。
- 客户端断开：当前操作取消；不存在 CICDReport 恢复或自动重放。
- 结果无法确定：返回 outcome unknown，要求人工核查实际资源，不自动重试。
