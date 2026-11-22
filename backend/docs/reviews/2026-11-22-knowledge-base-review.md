# 知识库全链路问题说明与修改点（2026-11-22）

本文说明控制台「知识库」从编辑、存储到 doops-agent 使用这条链路上的问题，并按文件给出修改点。
挂载部分给出一条统一规则，替代目前四种互不相同的做法。它是开发过程记录，不是产品文档。

> 整理后的执行版本（结论、对本文的修正、三个可执行切片）见
> [2026-11-22-knowledge-base-remediation.md](2026-11-22-knowledge-base-remediation.md)。
> 两份文件冲突时以执行版为准。

| 项 | 内容 |
| --- | --- |
| 基线 | 提交 `85edd83e8c5f`（`main`）加评审时刻的未提交工作区 |
| 评审时间 | 2026-11-22 14:50–16:25（Asia/Shanghai） |
| 验证环境 | 本地开发 Agent `local/dev-agent`（镜像 `doops-local-agent:0.2.47`，doagent 0.2.47）、已安装 CLI v0.3.21、Helm v3.21.0 |
| 是否改动代码 | 否。只新增本文件 |

阅读前请注意两点：

- 评审期间工作区有并行改动，`components/knowledge-base.tsx` 以及 `backend/agent/internal/server/` 下的
  `handler_ws.go`、`tunnel_hub.go`、`doagent_tools.go`、`tool_registry.go` 都在窗口内被修改过。
  文中行号是 16:06 的快照，定位请以函数名为准。
- `backend/docs/` 整个目录会被复制进 Agent 镜像（[Dockerfile](../../Dockerfile) 第 58 行），
  本地开发镜像的 `/app/self-docs/docs/reviews/` 里已经有上一份整改记录，探针中内置 Agent 也检索了这个目录。
  评审类文档是否应随镜像下发，需要单独决定。

证据标注沿用 [深度评审](2026-11-22-doops-deep-review.md)：**已复现**表示运行了命令或测试并得到文中行为；
**代码路径确认**表示代码确定地导出该行为但没有运行；**未验证**表示只是推断。

## 1. 结论

知识库目前是目标 Agent 上一个目录里的 Markdown 文件。人经控制台读写这条路是通的，后端路径校验有测试覆盖。
但它还不是一个可以依赖的知识库，原因有三个：

1. **数据不保证留得住。** Helm Chart 默认不挂任何数据卷，宿主机安装脚本只挂了工作区和 doagent 状态，
   都不包含知识库目录。Agent 重启或升级后文档消失。只有本地开发环境单独挂了它，所以开发时看不出来。
2. **Agent 没有被设计成会用它。** 系统提示词不提知识库，内置 Agent 的原生读工具被限制在会话工作区内。
   实测只读问答能答对，靠的是用 Bash 全盘扫描，而且那次运行以失败退出。`--apply` 会话没有 Bash，够不着。
3. **另有一个不受管的知识来源。** doagent 自带跨会话记忆，控制台看不到也删不掉。
   知识库文档删除后，内容仍留在记忆里。

修改方案的核心是两件事：每个 Agent 只挂一个数据目录（容器内 `/root`），以及给内置 Agent 一个只读的知识库工具。
其余修改是在这两件事之上的补强。

## 2. 现状链路

```text
人 ── 控制台 ──► Gateway ──► Agent
     列表、删除：doops_shell（ls、rm -f）
     读、写：    doops_file_read / doops_file_write，路径 /root/kb/<名>.md
                                      │
                                      ▼
                         /root/kb（可用 DOOPS_KB_ROOT 改）

内置 Agent（doagent）：工作目录 /root/ws/<会话>，原生 Read / glob / grep 只能访问该目录；
系统提示词没有提到知识库；没有回写知识库的路径。
```

读写权限沿用 target 授权：有该 target 授权的用户都能读写它的知识库。本文不改变这一点。

## 3. 问题清单

| 编号 | 级别 | 问题 | 证据 |
| --- | --- | --- | --- |
| KB-01 | 高 | 知识库目录在发布资产里不持久化，四种部署形态的挂载各不相同 | 代码路径确认；`helm template` 已渲染 |
| KB-02 | 高 | 内置 Agent 没有读取知识库的正式通道，`--apply` 会话完全读不到 | 已复现（只读问答）；代码路径确认（apply） |
| KB-03 | 中 | doagent 跨会话记忆是知识库之外的第二知识来源，不可见、不可删 | 已复现 |
| KB-04 | 中 | 写入上限 16 MiB，读取上限 1 MiB，超过 1 MiB 的文档写得进读不出 | 已复现 |
| KB-05 | 中 | 控制台写死 `/root/kb`，Agent 配了 `DOOPS_KB_ROOT` 时读写被拒 | 已复现 |
| KB-06 | 中 | 保存先截断再写，没有冲突检测；删除是 `rm -f`，不可恢复 | 代码路径确认 |
| KB-07 | 低 | 刷新或关闭页面时未保存草稿丢失，没有提示 | 代码路径确认 |
| KB-08 | 低 | 只支持单层目录和 `.md`；没有内容搜索、重命名；每个 Agent 一份 | 代码路径确认 |
| KB-09 | 低 | CLI 没有知识库命令，Operator Skill 和文档没有知识库条目 | 已复现（`doops -h`、全文检索） |

### 3.1 KB-01：哪些目录在哪种部署下留得住

Agent 的全部运行状态都在 `/root` 下。下表列出每个目录在四种部署形态下是否挂了卷。

| 容器内路径 | 内容 | Helm 默认 | 宿主机安装脚本、`unicom-1` | `unicom-k8s` | 本地开发 |
| --- | --- | --- | --- | --- | --- |
| `/root/kb` | 知识库 | 否 | 否 | 是 | 是 |
| `/root/ws` | 会话工作区，24 小时回收 | 否 | 是 | 是 | 是 |
| `/root/.agent` | doagent 设置、skills、日志、记忆，以及 DoOps 的凭据交付记录 | 否 | 是 | 是 | 否 |
| `/root/.do-agent` | doagent 会话库 `data.db` | 否 | 否 | 是 | 否 |
| `/root/.doops/audit` | Agent 侧命令审计 | 否 | 否 | 是 | 是 |
| `/root/.docker/config.json` | registry authfile | 否 | 否 | 是 | 否 |

来源：

- Helm 默认：[values.yaml](../../deploy/helm/doops-agent/values.yaml) 第 57–59 行是空的 `volumeMounts` 和 `volumes`。
  用最小 values 渲染，Deployment 里只有只读的 `node-root`。
- 宿主机安装脚本：[bootstrap-host-agent.sh](../../scripts/bootstrap-host-agent.sh) 第 125–128 行只写了
  `workspace:/root/ws` 和 `agent-state:/root/.agent`。`deploy/instances/unicom-1/compose.yaml` 相同。
- `unicom-k8s`：`deploy/instances/unicom-k8s/values.yaml` 第 25–32 行把宿主目录 `/var/lib/doops-agent` 整个挂到 `/root`。
- 本地开发：`.local/doops-dev/magpie-agent/compose.yaml`（未跟踪）挂了 `ws`、`kb`、`.doops` 三个卷。

分卷还造成成对的文件寿命不一致。宿主机安装脚本持久化了 `/root/.agent`，其中有 registry authfile 的归属记录
`doops-registry-auth.json`，但 authfile 本身在 `/root/.docker`，不持久化。doagent 的日志和记忆在 `/root/.agent`，
会话库却在不持久化的 `/root/.do-agent`。这两处没有复现出故障，只确认了寿命不同。

### 3.2 KB-02：内置 Agent 怎么读到知识库

先向知识库写入一篇只含一条合成事实的文档，然后各用一个新会话发起只读 `ask`。

| 探针 | 结果 |
| --- | --- |
| 直接问这条事实，不提示位置 | 答对。过程是用 Bash 对 `/root`、`/etc`、`/opt`、`/srv`、`/var/lib` 全盘 `grep`。原生 `grep` 工具报 `path_denied`，CLI 输出 `doagent_tool_failed` 并以退出码 1 结束 |
| 只允许用 `Read` 读 `/root/kb/<文档>` | `Error: path is outside the workspace`，退出码 1 |
| 删除文档后在新会话再问 | 没有给出答案，但说明“会话记忆里存有这个值” |

代码侧的原因：

- 系统提示词 [system_prompt.md](../../agent/skills/system_prompt.md) 全文没有知识库，工具表里只有“读 session 工作区里的文件”一行。
- 会话创建时 doagent 的工作目录是 `/root/ws/<会话>`（`handler_ws.go` 的 `handleAgentPromptWS`，第 826 行）。
- `doagentAllowedTools`（`doagent_tools.go`）在 `apply` 为真时不加入 `Bash`。apply 会话因此只剩被限制在工作区内的
  `Read`、`glob`、`grep`，读不到 `/root/kb`。这一条没有实跑。

### 3.3 KB-03：doagent 记忆

doagent 的配置里 `memory.enabled` 为真，上限 1000 条。每轮问答以 `Conversation` 类型整段写入
`/root/.agent/memory/<id>.json`。记录里只有模型名标签，没有会话或用户标识，所以同一个 Agent 上的所有会话共用。探针结束后，9 个记忆文件里有 4 个含探针内容。
DoOps 没有读取、展示或清理它的入口。它现在随容器重启清空（Helm 默认）或长期保留（宿主机安装），取决于 3.1 的挂载。

### 3.4 其余问题的证据

- **KB-04**：[workspace_upload.go](../../agent/internal/server/workspace_upload.go) 第 117–118 行定义了两个上限。
  临时测试里写入 2 MiB 成功，读回报 `file size 2097150 exceeds limit 1048576`。
- **KB-05**：`components/knowledge-base.tsx` 第 32 行是常量 `KB_ROOT = "/root/kb"`。临时测试里把 `DOOPS_KB_ROOT`
  指到别处后写 `/root/kb/a.md`，返回 `unsafe workspace path`。
- **KB-06**：`writeKnowledgeBaseFile` 用 `O_TRUNC` 打开目标文件直接写。控制台保存时不带任何版本信息。
  删除在 `deleteDoc` 里经 `doops_shell` 执行 `rm -f`。控制台按约定不弹确认框，所以误删没有补救。
- **KB-07**：工作区里的 `kbSessionStore` 已经让草稿在切换页签后保留。它是页面内存，刷新或关闭页面仍会丢，
  组件没有 `beforeunload` 提示。
- **KB-09**：`doops -h` 没有知识库命令，只能用 `read`、`write` 加 `--storage gateway --path /root/kb/<名>.md`，列表靠 `exec ls`。
  `backend/skills/SKILL.md` 没有知识库条目，离 12000 字节上限只剩 673 字节。`DOOPS_WEBUI.md` 没有知识库行为一节，
  根 `README.md` 仍写着知识库界面“仍需与后端契约联调”。

## 4. 修改方案

### 4.1 统一挂载（KB-01）

**规则只有一条：每个 Agent 一个数据目录，容器内挂到 `/root`，宿主侧路径由 Agent 身份决定。**

```text
宿主：/var/lib/doops-agent/<cluster>/<instance>      容器：/root
├── kb/            知识库
├── ws/            会话工作区
├── .agent/        doagent 设置、skills（每次启动重新同步）、日志、DoOps 凭据交付记录
├── .do-agent/     doagent 会话库
├── .doops/audit/  Agent 侧命令审计
└── .docker/       registry authfile
```

- kubeconfig 仍然单独挂在 `/root/.kube` 或 `/root/.kube/config`。它是只读输入，来源不同，是唯一允许叠在 `/root` 上的挂载。
- 宿主路径由 `cluster/instance` 决定，同一节点上的多个 Agent 不会共用目录。
- 运行时的 socket 和容器存储挂载（`/run/containerd` 等）不是数据目录，不受这条规则约束。

Helm 写法是一个开关加节点固定：

```yaml
persistence:
  enabled: true
nodeSelector:
  kubernetes.io/hostname: <节点名>
```

Docker 写法是一条绑定挂载：

```yaml
volumes:
  - /var/lib/doops-agent/<cluster>/<instance>:/root
```

需要 PVC 或网络存储的集群继续用 Chart 已有的 `volumes`、`volumeMounts` 透传，同样只挂 `/root`。`unicom-k8s` 现在就是这种写法。

**选择整目录挂 `/root` 的理由**

- 不需要改任何路径。上表几个目录的公共父目录就是 `/root`。
- 3.1 里成对文件寿命不一致的问题一并消失。
- 仓库里已有先例（`unicom-k8s`）。本次用空目录挂 `/root` 启动本地镜像，Agent 和 doagent 健康检查都通过（已复现，未连 Gateway）。
- 备份和迁移的对象是一个目录。

| 没有采用的方案 | 原因 |
| --- | --- |
| 再为 `/root/kb` 单独加一个卷 | 变成第五种布局，成对文件寿命不一致的问题仍在 |
| 新建独立数据根，把各目录迁过去 | `/root/ws` 在非测试代码和文档里写死了约 70 处；doagent 的状态目录跟随 `HOME`，只能连 `HOME` 一起改；知识库根目录拒绝符号链接，不能用软链过渡 |
| 一个卷加多个 subPath | 挂载条目没有减少；Compose 的 subpath 依赖较新的 Docker 版本 |

**修改点**

| 文件 | 修改 |
| --- | --- |
| `backend/deploy/helm/doops-agent/values.yaml` | 新增 `persistence.enabled: false` |
| `backend/deploy/helm/doops-agent/values.schema.json` | 新增 `persistence` 对象，只允许 `enabled` |
| `backend/deploy/helm/doops-agent/templates/deployment.yaml` | 开关为真时渲染卷 `agent-home`（hostPath 为上述宿主路径，`DirectoryOrCreate`）并挂到 `/root`。此时 `nodeSelector` 为空，或 `gateway.cluster`、`gateway.instance` 含字母、数字、`.`、`_`、`-` 以外的字符，渲染失败 |
| `backend/deploy/helm/doops-agent/Chart.yaml` | 递增 Chart 版本 |
| `backend/scripts/bootstrap-host-agent.sh` | `volumes` 改为上面那一条绑定挂载，删除顶层具名卷；用 `install -d -m 0700` 创建宿主目录 |
| `backend/agent/sandbox-entrypoint.sh` | 启动时 `chmod 700 /root`。hostPath 自动创建的目录是 0755，而目录里有工作区和会话库 |
| `backend/agent/agent-entrypoint.sh` | 第 63 行每次启动都向 `/root/.bashrc` 追加审计钩子，改成幂等。镜像默认入口不是它，只影响仍在使用它的实例 |
| `backend/test/test_bootstrap_host_agent.py` | 第 85 行断言 `agent-state:/root/.agent`，改为断言只有一条 `/root` 挂载 |
| `backend/test/test_model_settings_secret_contract.py` | 用现有 `render_chart` 增加三条：默认不渲染 `agent-home`；开启后只有一个 `/root` 挂载；开启但没有 `nodeSelector` 时渲染失败 |

Chart 默认值保持关闭。`doops upgrade` 用 `helm get values` 取回 release 的 values 再渲染（`self_upgrade.go` 第 1538 行），
所以 Chart 默认值一旦改变，会随后续升级作用到所有存量 Agent。默认开启会让没有 `nodeSelector` 的存量实例升级失败。

**持久化之后要一起处理的事**

1. 镜像层的 `/root` 被遮住。本地镜像里 doagent 自带的 23 个通用 skill 不再可见，DoOps 的 6 个 skill 和系统提示词
   由入口脚本每次启动从 `/app/skills` 同步，不受影响。发布镜像上需要再确认一次。
2. 模型凭据（`/root/.agent/settings.json`，0600）和 registry authfile（`/root/.docker/config.json`，0600）会跨重启留在磁盘上。
   宿主机安装今天已经如此。Gateway 在 Agent 重连时重新下发（`deliverAgentCredentials`），启动时
   `EnsureDoagentBaseSettings` 会丢弃没有交付记录的模型字段。需要补一条验收：吊销凭据后重启，文件里不再有它。
3. doagent 的记忆、日志和工具快照变成长期数据，见 4.4。
4. 滚动更新期间新旧两个 Pod 会同时使用这个目录。`unicom-k8s` 是这样运行的，本次没有实测重叠期的行为。
5. kubeconfig 来源为 `mounted` 的实例，kubeconfig 必须保持独立挂载。升级 Job 只认 `/root/.kube` 和
   `/root/.kube/config` 这两个挂载点。

**存量实例迁移**

`doops upgrade` 和 `release-host-agent` 按契约保留原有挂载，不承担这次迁移。每个实例单独处理：

| 现有形态 | 做法 |
| --- | --- |
| Helm，没有数据卷 | 先导出容器内的 `/root/kb`，它会随 Pod 替换丢失，和今天任何一次重启的后果相同。然后把 `persistence.enabled` 和 `nodeSelector` 写进该实例的 values，按 [CLUSTER_INSTALL_GUIDE.md](../CLUSTER_INSTALL_GUIDE.md) 写明的受审计独立 Job 流程收敛，验收后写回文档。目前没有配置变更的脚本入口 |
| Helm，已经整目录挂 `/root`（`unicom-k8s`） | 已符合规则，不动 |
| Docker Compose 具名卷（安装脚本、`unicom-1`） | 停容器，把 `workspace` 和 `agent-state` 两个卷的内容复制到新目录的 `ws/` 和 `.agent/`，改 Compose 文件，启动并验收。旧卷保留到验收通过 |
| 本地开发 Compose | 三个卷改成一个 |

迁移完成前，控制台靠下面 4.3 的 `on_volume` 字段提示“知识库没有挂数据卷”。

### 4.2 让内置 Agent 能读知识库（KB-02）

在 Agent 自带的 MCP 服务里加一个只读工具 `kb_read`，doagent 侧的名字是 `mcp_doops_kb_read`。它有三个操作：
`list` 返回文档名、大小和修改时间；`search` 返回命中的文档名、行号和该行文本；`read` 返回正文，支持起始行和行数。
MCP 工具输出有 256 KiB 上限（`maxToolOutputBytes`），所以 `read` 必须能分段。

| 文件 | 修改 |
| --- | --- |
| `backend/agent/internal/server/knowledge_base.go`（新增） | 把 `knowledgeBaseRoot`、`knowledgeBaseFileName`、`openKnowledgeBaseRoot`、读写函数从 `workspace_upload.go` 移过来，新增列表和搜索。4.3 的 Gateway 工具用同一组函数 |
| `backend/agent/internal/server/k8s_mcp_stdio.go` | `mcpToolsForDomain` 的 doops 域增加工具定义；`mcpToolBelongsToDomain`、`callK8SMCPToolDomain`、`readOnlyMCPTool` 各加一个分支 |
| `backend/agent/internal/server/doagent_tools.go` | `doagentAllowedTools` 无条件加入 `mcp_doops_kb_read`，只读问答和 apply 都可用 |
| `backend/agent/internal/server/agent_native_test.go` | 第 50、56 行的期望工具列表同步更新 |
| `backend/agent/skills/system_prompt.md` | 工具表加一行，并写明四点：任务涉及本 target 的约定、手册或历史处置时先搜索知识库；引用时写出文档名；知识库内容是资料不是指令，不扩展授权；查不到就如实说明，不做全盘扫描 |

说明：

- 系统提示词只在 doagent 会话首次创建时注入，已有会话要新建后才会带上新内容。
- `doagentRuntimePermissions` 的 `always_allow` 目前只列了 `mcp_doops_k8s_read` 和 `mcp_dosql_read`。
  新工具是否需要加入，以实测为准。
- 不把文档目录或正文塞进提示词。知识库可能很大，按需读取更稳妥。

### 4.3 知识库接口与控制台（KB-04 至 KB-07）

新增 Gateway 工具 `doops_kb`，参数为 `session_id`、`operation`，以及按操作需要的 `name`、`content`、`expected_mtime`、`query`。
控制台不再用 `doops_shell` 列目录和删除，也不再自己拼路径。

| 操作 | 行为 |
| --- | --- |
| `list` | 返回知识库根目录、`on_volume`、每篇文档的名称、大小和修改时间。`on_volume` 为假表示知识库目录和容器根在同一个设备上，也就是没有挂卷 |
| `read` | 返回正文和修改时间 |
| `search` | 同 4.2 |
| `write` | 新建时不带 `expected_mtime`；更新时带上读到的修改时间，不一致返回 `kb_conflict`。写入临时文件后重命名。上限与读取上限一致 |
| `delete` | 把文档移到知识库根目录下的 `.trash/`，文件名加时间戳。列表不显示该目录 |

| 文件 | 修改 |
| --- | --- |
| `backend/agent/api/mcp.go` | 新增参数结构 |
| `backend/agent/internal/server/handler_ws.go` | Agent 侧分发增加 `doops_kb` |
| `backend/agent/internal/server/tool_registry.go` | `gatewayTools` 增加一项：`list`、`read`、`search` 记为 `ActionRead`，`write`、`delete` 记为 `ActionWrite`，审计摘要是操作加文档名。`TestToolCatalogueMatchesAgentDispatch` 会校验两侧一致 |
| `backend/agent/internal/server/tunnel_hub.go` | `doops_file_write` 现有的几处特殊处理（变更审计、不保留输出尾部、结果处理、错误脱敏）同样作用于 `doops_kb` 的写入。审计里不能出现正文 |
| `backend/agent/internal/server/protocol_capabilities.go` | 声明知识库能力，供控制台判断 Agent 版本 |
| `backend/agent/internal/server/knowledge_base_functional_test.go` | 现有用例经 `doops_shell` 列表和删除，改为走 `doops_kb`，并补冲突、上限、回收目录、审计不含正文四条 |
| `components/knowledge-base.tsx` | `refresh`、`openFile`、`save`、`deleteDoc` 四处改用 `doops_kb`；删除常量 `KB_ROOT`，路径显示用 `list` 返回的根目录；`on_volume` 为假时显示告警条；`kb_conflict` 时提示重新加载或覆盖；有未保存草稿时注册 `beforeunload` |
| `lib/gateway.ts`、`lib/demo.ts` | `TOOLS` 增加一项；演示模式增加对应的模拟返回 |

补充说明：

- 重命名可以用 `os.Root.Rename`，它需要 Go 1.25。镜像和发版流水线用的是 1.25，但 `backend/agent/go.mod` 写的是 `go 1.24.0`，
  CI 按它选版本。要么升 `go.mod`，要么在同一目录内用 `os.Rename`。
- `on_volume` 是按设备号比较得出的，`emptyDir` 也会被判为已挂卷。它能发现“完全没挂”，不能证明数据一定持久。
- 旧版 Agent 没有 `doops_kb`。控制台按能力声明判断，缺少时提示升级 Agent，不做双路径。
  `doops_file_read`、`doops_file_write` 对 `/root/kb/<名>.md` 的例外先保留，CLI 用户仍在用。

### 4.4 doagent 记忆与长期数据（KB-03）

是否保留记忆需要决策，见第 6 节。建议关闭：它是操作员看不到也删不掉的知识来源，而系统提示词要求结论以工具证据为准；
统一挂载之后它还会跨重启长期保留。

| 文件 | 修改 |
| --- | --- |
| `backend/agent/internal/server/model_provider.go` | `writeDoagentSettings` 写入 `memory.enabled: false` |
| `backend/agent/internal/server/cleanup.go` | `performCleanup` 目前只清理工作区和 `/tmp/repos`。确认 doagent 是否自己清理 `/root/.agent` 下的 `logs`、`tool-snapshots`、`rollouts`；如果不清理，在这里按天数清理 |

已确认 `do-agent config set memory.enabled false` 会把该键写进 `settings.json`。没有确认 `acp-http` 模式下它是否生效。
验证方法是重复 3.2 的第三个探针，并检查 `/root/.agent/memory` 没有新增文件。

### 4.5 入口与文档（KB-08、KB-09）

| 文件 | 修改 |
| --- | --- |
| `backend/cli/cli/doops/main.go` | 新增 `doops kb` 的列表、读取、写入、删除、搜索子命令和帮助文本。使用 CLI 的外部 AI 也能查阅 target 的知识库 |
| `backend/skills/SKILL.md` | 加一条命令入口。只剩 673 字节余量，写在反引号里的命令会被契约测试实际执行 |
| `backend/docs/CLUSTER_INSTALL_GUIDE.md` | 新增“数据目录”一节，作为 4.1 规则的唯一归属；第 287 行“工作区和 Agent 状态用 Docker volumes 持久化”相应改写 |
| `backend/docs/BUILD_DEPLOY_SOP.md` | 写入 4.1 的存量迁移步骤；在“容器运行时挂载规范”里注明数据挂载只有一个 |
| `backend/docs/DOOPS_WEBUI.md` | 新增知识库行为一节：存储提示、冲突、回收目录、上限 |
| `backend/docs/DOOPS_API_GUIDE.md`、`backend/docs/DOOPS_USAGE_GUIDE.md` | 增加 `doops_kb` 和 `doops kb`；文件读写两节对知识库例外的描述指向新接口 |
| `README.md`、`README.en.md` | 完成后删去“仍需与后端契约联调”，保留“不是向量检索或 RAG 服务” |
| `CHANGELOG.md` | 在 `[Unreleased]` 记录 |

KB-08 里的子目录、图片和跨 target 共享不在本次范围内。跨 target 共享需要在 Gateway 一侧新建存储，不建议现在开始。

## 5. 实施顺序与验收

| 顺序 | 内容 | 层级 |
| --- | --- | --- |
| 1 | 4.1 统一挂载，加 4.3 里的 `on_volume` | T2。凭据物化文件的落盘寿命发生变化 |
| 2 | 4.2 `kb_read` 和系统提示词 | T1 |
| 3 | 4.3 `doops_kb` 和控制台 | T1 |
| 4 | 4.4 记忆决策和长期数据清理 | T1 |
| 5 | 4.5 CLI、Skill、文档 | T1。纯文档部分是 T0 |

层级是按 `AGENTS.md` 的定义做的判断，由维护者确认。验收条目：

- 开启持久化的 Agent 写入一篇文档，删除 Pod 或重建容器后文档仍在，`on_volume` 为真。
- 没有数据卷的 Agent，控制台显示知识库未挂卷的告警。
- 知识库里有一条只此一处的事实。只读问答和 `--apply` 会话都通过 `kb_read` 取到它并写出文档名，CLI 退出码为 0，没有 `path_denied`。
- 知识库里没有相关内容时，回答明确说明未找到，没有全盘扫描。
- 两个浏览器同时编辑同一篇文档，后保存的一方得到 `kb_conflict`。
- 删除的文档出现在 `.trash/` 里。
- 超过上限的写入被拒绝，写入上限不大于读取上限。
- 审计记录里有操作和文档名，没有正文。用合成标记字符串断言。
- 持久化目录下吊销模型凭据或 registry 凭据后重启 Agent，对应文件里不再有该凭据。
- 旧版 Agent 上打开知识库页签，显示升级提示。

## 6. 待决策

1. **doagent 记忆**：关闭，还是保留并在控制台提供查看和清除。建议关闭。
2. **Agent 回写知识库**：建议只做人工确认的“把这条回答存入知识库”，不做自动回写。否则 Agent 写下的内容会成为它下一轮的证据。
   启用回写时，知识库预览应当和数据库、网站结果区一样不加载远程图片。
3. **Chart 默认值**：建议保持关闭，并在开启时强制要求 `nodeSelector`。代价是存量 Helm 实例要逐个改 values。
4. **评审文档是否随 Agent 镜像下发**：见文首第二点。

## 7. 未验证项

- 空目录挂 `/root` 的启动只在本地开发镜像上做过，容器没有网络，没有走到 Gateway 就绪。
- 滚动更新期间新旧 Pod 共用数据目录的行为。
- `memory.enabled: false` 在 `acp-http` 模式下是否生效。
- apply 会话读不到知识库是从工具清单推出的，没有实跑 `--apply`。
- doagent 是否自行清理日志和工具快照。
- Gateway 审计是否保存 `doops_file_read` 返回的正文。
- Docker 具名卷到绑定挂载的迁移步骤。

## 附录 A 验证记录

| 做了什么 | 结果 |
| --- | --- |
| `go test ./internal/server/ -run TestKnowledgeBase`（`backend/agent`） | 通过 |
| 临时测试：写 2 MiB 再读回；改 `DOOPS_KB_ROOT` 后写 `/root/kb/a.md`；写子目录 | 读回失败；后两项被拒。测试文件已删除 |
| CLI 经 Gateway 写入、读回、列出一篇合成文档 | 成功 |
| 四次只读 `ask`，各用新会话 | 见 3.2 |
| 最小 values 渲染 Chart；用透传 values 渲染单个 `/root` 挂载 | 默认只有 `node-root`；透传写法可用 |
| 空目录绑定到 `/root` 启动本地镜像 | 约 9 秒后 `/health/live` 通过，doagent `/health` 返回 ok，`/root/.agent/skills` 有 7 项，`settings.json` 为 0600 |
| 临时 `HOME` 下执行 `do-agent config set memory.enabled false` | 键写入该 `HOME` 下的 `.agent/settings.json`。真实配置未改动 |

## 附录 B 评审残留

- 没有修改任何受版本控制的文件。新增的只有本文件。
- 临时 Go 测试文件、探针容器和临时目录都已删除。知识库里的合成文档已删除。
- 本地开发 Agent 上还留有探针痕迹，都含合成字符串 `QUARTZ-HERON-4417`：四个会话目录 `/root/ws/kb-review-ask1` 至 `ask4`
  （24 小时后回收）、对应的 `/root/.agent/logs/kb-review-ask*.jsonl`、4 条 doagent 记忆、2 个工具快照，
  以及 `/root/.doops/audit/kb-review-loop.log`。这些是 doagent 和 Agent 的内部状态，没有动。
- 我在评审前启动过一个 `127.0.0.1:3000` 的开发服务器，15:50 检查时它已不在，不是我停止的。
  16:15 起该端口上有另一个 `next dev` 进程，不是我启动的。
