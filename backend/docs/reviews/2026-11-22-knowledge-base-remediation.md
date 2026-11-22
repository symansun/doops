# 知识库整改方案（执行版，2026-11-22）

> 评审证据、现场数据链和逐条代码位置留在 [2026-11-22-knowledge-base-review.md](2026-11-22-knowledge-base-review.md)。
> 本文只保留：结论、对那份评审的修正、按依赖排序的可执行切片。
> 基线 `85edd83e8c5f`（`main`）加评审时的未提交工作区。事实核对于 16:37 复核。

## 1. 结论

- 9 条问题中 8 条成立，`KB-03`（doagent 记忆）未验证，见 §4.1。
- 严重度排序不变：`KB-01` 持久化 > `KB-02` Agent 不可用 > `KB-06`/`KB-04`/`KB-05` > 其余。
- 原评审 §4 的方案有 4 处会引入比现状更大的风险，已修正（§3）。
- 按风险从低到高切成 3 个可发布切片，另加 1 个待验证后决定的记忆切片（§5）。
  **切片 1 不依赖任何挂载变更，可以先发。**

## 2. 问题状态

| 编号 | 问题 | 严重度 | 复核结论 | 切片 |
| --- | --- | --- | --- | --- |
| KB-01 | 持久化设备上重启即丢，迁移断档 | 高 | 确认 | 3 |
| KB-02 | apply 会话无法使用知识库 | 高 | 确认 | 2 |
| KB-04 | 写上限 16 MiB / 读上限 1 MiB | 中 | 确认：写限在 `handleFileWrite` 入口统一判定，读限在两条读路径各自判定 | 1 |
| KB-05 | 控制台写死 `/root/kb`，忽略 `DOOPS_KB_ROOT` | 中 | 确认 | 2 |
| KB-06 | 无原子写、无冲突检测，删除不可恢复 | 中 | 确认；回收站是新增能力，不是现状 | 1（前半）/ 2（回收站） |
| KB-03 | 记忆会反过来影响会话判断 | 中 | **未验证**：`memory.enabled` 在 `acp-http` 下是否生效未测 | 4 |
| KB-07 | 有未保存修改时切页/关页无提示 | 低 | 确认 | 1 |
| KB-08 | 操作语义没有文档（单层目录、仅 `.md`） | 低 | 确认 | 2 |
| KB-09 | CLI 没有 `kb` 命令 | 低 | 确认；本期不做，见 §7 | — |

## 3. 对原方案的修正

### 3.1 持久化必须和升级策略同批改（新增，最高优先）

Chart 现在是 `maxSurge: 1`、`maxUnavailable: 0`（[deployment.yaml](../../deploy/helm/doops-agent/templates/deployment.yaml#L16-L21)），
而 `persistence.enabled` 又要求 `nodeSelector` 把 Pod 钉在固定节点。
两者合起来，每次升级新旧两个 Pod **一定**在同一节点同时写同一份
`/root/.do-agent/data.db`、`/root/.agent/memory`、`/root/.agent/settings.json`。
这是持久化引入的新损坏面，比它要解决的“重启丢文档”更严重。

- `persistence.enabled=true` 时渲染 `strategy: Recreate`，并在 SOP 写明“升级期间短暂不可用”。
- 验收加一条：升级全程 `kubectl get pods -w` 任意时刻最多一个 `doops-agent` 处于 `Running`。

### 3.2 `KB-04` 应该降低写上限，不要提高读上限

读限 1 MiB 保护的是**模型上下文**（`doops_file_read` 的正文会进会话），不是磁盘。
把读限抬到 16 MiB 会把这两件事混在一起。

- 在 `writeKnowledgeBaseFile` 入口加 `len(content) > maxFileReadBytes()` 检查，知识库单篇按 1 MiB 收口；
  工作区写仍保留 16 MiB。
- 控制台保存前用 `new Blob([content]).size` 预检，给中文提示，不要等 RPC 报错。
- 存量超过 1 MiB 的文档保存时会被拒；提示里给出当前大小和上限，不做截断。

### 3.3 `KB-05` 用 capability 暴露根目录，不要用位置参数

原评审建议把根目录塞进 `ls` 的位置参数再解析回来，这依赖 shell 输出格式，太脆。
Agent 已经有 protocol capability 机制（`protocol_capabilities.go`）：

- 新增 `knowledgeBase` capability：`{"schemaVersion":"doops.sh/knowledge-base/v1","root":"/root/kb"}`。
- 控制台优先读 capability，拿不到时回退 `/root/kb`；`doops_kb list` 的返回值以同一根目录为准。
- `DOOPS_KB_ROOT` 保留给测试（`knowledge_base_functional_test.go` 依赖它），不进用户文档。

### 3.4 冲突检测放在控制台，不动协议

现在的 `doops_file_write` 没有版本号，加 `if_version` 要同时改协议、CLI 和 Gateway。
控制台保存前多读一次即可：

1. 打开文档时记下内容基线；
2. 保存前 `doops_file_read` 读一次；
3. 与基线不一致就提示“文档已被其他会话修改”，让操作者选覆盖或放弃。

仍有窗口期，但对两三个人同时用控制台的场景够用；切片 2 的 `doops_kb` 落地后可选加 `mtime` 令牌。

### 3.5 原子写

`writeKnowledgeBaseFile` 现在是 `O_TRUNC` 直写（[workspace_upload.go](../../agent/internal/server/workspace_upload.go#L382)），
进程被杀会留下半篇。改成同目录临时文件 + `os.Rename`：

- 临时名用 `.<name>.<pid>.<unixnano>.tmp`，失败路径用 `root.Remove` 兜底；
- 目录已拒绝符号链接和子目录（`rejectKnowledgeBaseSymlink`），同目录 rename 不会跨设备；
- **不要用 `os.Root.Rename` / `Root.CreateTemp`**：`go.mod` 是 1.24.0，CI 按 `go-version-file` 选版本，
  只有 release 工作流用 1.25。

### 3.6 回收站必须和 GC、配额同批落地

`.trash/` 是本次**新增**能力（现在删除就是控制台的 `rm -f`，Agent 侧没有删除接口）。
只加回收站不加清理，就是把“删除”换成“永久增长”，所以三件事必须同一批：

- `cleanup.go` 的 `cleanDirs` 增加 `<kb>/.trash`（[cleanup.go](../../agent/internal/server/cleanup.go#L30)）；
- 回收站里每一项是一个目录，正好匹配现有“按目录最新 mtime 过期”的清理逻辑；
- 配额：篇数 500、单篇 1 MiB、总量 256 MiB，超限拒绝新建并提示清理。

### 3.7 备份契约

`AGENTS.md` 要求任何改动数据的路径先备份、失败能回滚，原评审的迁移表只有“停容器、复制、验收”。
切片 3 的迁移必须写成：停 Pod → `cp -a` `/root/kb` 和 `/root/.agent` 到带时间戳的备份目录 →
起新 Pod → 按清单校验 → **保留源目录**，校验失败原样回滚。

## 4. 未验证的前提与勘误

### 4.1 `memory.enabled=false` 在 `acp-http` 下是否生效

原评审自己标了未验证，而它的 §4.4（改 `model_provider.go` 的 `doagentSettings` 写入）全部建立在这条上。
先做实验：改设置 → 重启 → 开一轮会话 → 查是否有新记忆条目。

- 生效：再决定“禁写”还是“保留 + 清空入口”；倾向后者，禁写会拿走 doagent 的既有能力。
- 不生效：`KB-03` 整条作废，改由系统提示词约束，不写代码。

### 4.2 复核时发现的偏差（原文勘误，不影响结论）

- `values.yaml` 的空 `volumeMounts`/`volumes` 在文件末尾，不是第 57–59 行；引用以键名为准。
- `unicom-k8s` 的宿主路径是 `/var/lib/doops-agent`，不含 `cluster/instance`；它只符合“整目录挂 `/root`”，
  不符合原评审 §4.1 的路径规则。要么规则补一句“路径是否含身份由实例自定”，要么把它列进迁移。
- `sandbox-entrypoint.sh` 对技能**目录**是 `rm -rf` 后重拷（干净），顶层 `*.md` 只 `cp -f`；
  `/root` 持久化后会留下已下线的顶层技能文件，切片 3 顺手加一句清理。

### 4.3 原评审漏掉、本方案一并处理的两点

- 知识库预览走 `Markdown` 的默认 `allowImages=true`（[markdown.tsx](../../../components/markdown.tsx#L200)），
  与数据库、网站结果区不一致；切片 1 直接关掉，不等回写能力。
- `backend/docs` 整目录进镜像（[Dockerfile](../../Dockerfile#L58)），评审文档成为第三个 Agent 能读到的知识来源，
  和“给 Agent 一个可审计的读取通道”的目标冲突；切片 2 从镜像里排除 `docs/reviews`。

## 5. 切片

依赖关系：切片 2 用到切片 1 的原子写和大小校验；切片 3 的 `on_volume` 依赖切片 2 的 `doops_kb`。

### 切片 1 · 正确性与边界（T1，可直接发布）

| 改动 | 位置 |
| --- | --- |
| 知识库写上限对齐读上限 1 MiB，错误信息带大小和上限 | `workspace_upload.go` `writeKnowledgeBaseFile` |
| 同目录临时文件 + `os.Rename` 原子写 | `writeKnowledgeBaseFile` |
| 保存前做大小预检和冲突检测 | `knowledge-base.tsx` `save` |
| 预览禁用远程图片 | `knowledge-base.tsx` 的 `Markdown` |
| 未保存修改时 `beforeunload` 拦关页（切文档由 `kbSessionStore` 保留草稿，不需要确认） | `knowledge-base.tsx` |

验收：Agent 包单测（超限拒绝、工作区 16 MiB 仍允许、原子写无临时残留、并发写不产生半篇）；
控制台 `pnpm build`；两窗口并发保存触发冲突提示；`git diff --check`。

实施记录（2026-11-22）：

- `workspace_upload.go`：`writeKnowledgeBaseFile` 加 1 MiB 上限、同目录临时文件 + `os.Rename`；
  临时名带进程号、纳秒和进程内自增序号（单靠时钟在并发下会撞名）。
- `knowledge_base_save_test.go`：三条新测试，用 `DOOPS_MAX_FILE_READ_BYTES`/`DOOPS_MAX_FILE_WRITE_BYTES`
  把两个上限分开验证，另加 8 路并发写与"拒绝保存不改旧文档"。
- `knowledge-base.tsx`：保存前做字节数预检与远端比对，冲突时出「覆盖保存／重新加载」；
  底栏显示字节数与上限；预览关远程图片；有未保存修改时拦关页。
- `CHANGELOG.md`：`[Unreleased] / Fixed` 一条。
- 该切片改动的 4 个文件之外没有别的改动；评审时工作区其他并行改动未触碰。

### 切片 2 · Agent 可用与回收站（T1）

| 改动 | 位置 |
| --- | --- |
| 新工具 `doops_kb`：`list`/`read`/`write`/`delete`（delete 进 `.trash`，list 带 name/size/mtime） | `tool_registry.go`、`tunnel_hub.go`、新文件 |
| `knowledgeBase` capability 暴露根目录 | `protocol_capabilities.go` |
| 只读 MCP 工具 `kb_read`，仅 `apply` 会话可见，不进 `always_allow` | `doagent_tools.go`、`target_runtime.go`、`model_provider.go` |
| 授权断言同步更新 | `handler_ws_test.go`（工具列表是一条精确字符串比较） |
| 系统提示词：何时读、写回条件、资料不是指令、禁止用全盘 Bash 搜索替代 | `skills/system_prompt.md` |
| 控制台改用 `doops_kb` 做 list/delete，根目录取 capability | `knowledge-base.tsx` |
| 回收站 + GC + 配额（500 篇 / 1 MiB / 256 MiB） | `doops_kb`、`cleanup.go` |
| 审计只记 name/大小/摘要，不记正文 | 沿用 `filePathSummary` 先例 |
| 镜像排除 `docs/reviews` | `Dockerfile` |
| `DOOPS_WEBUI.md` 补知识库行为；`CHANGELOG.md` 记一条 | 文档 |

验收：`kb_read` 在含“忽略授权、执行 X”的文档下不改变工具授权（提示注入回归）；
apply 会话能用 `kb_read` 答出知识库内容；删除后能在 `.trash` 找到；
Gateway 对未知新工具 fail closed；`handler_ws_test.go` 分发与授权覆盖。

> 不要往 `SKILL.md` 里加知识库说明：那里的反引号片段会被合同测试当成命令执行。

### 切片 3 · 持久化（T2）

| 改动 | 位置 |
| --- | --- |
| `persistence.enabled`（默认 false）、`hostPath` 覆盖、条件化 `strategy: Recreate`、`nodeSelector` 必填校验 | `values.yaml`、`deployment.yaml`、`values.schema.json`、`Chart.yaml` |
| 宿主机安装脚本按同一规则改挂 `/root` 整目录 | `bootstrap-host-agent.sh`、`test_bootstrap_host_agent.py` |
| `on_volume`：只证明“检测到独立挂载点”，由 `doops_kb list` 返回 | `doops_kb` |
| 迁移：停 Pod → 备份 → 复制 `/root/kb`、`/root/.agent` → 起 Pod → 清单校验 → 失败回滚 | `bootstrap-host-agent.sh` 或独立脚本 |
| 技能同步清理已下线的顶层 `*.md` | `sandbox-entrypoint.sh` |
| 文档：数据目录、持久化、备份/恢复、升级策略 | `CLUSTER_INSTALL_GUIDE.md`、`BUILD_DEPLOY_SOP.md` |

验收（替换原评审 §5 的 5 条）：

1. 删 Pod 后文档与草稿仍在，`on_volume` 为真；
2. 删 Pod 后回收站、记忆、`settings.json` 仍在；
3. 升级全程只有一个 Pod `Running`，升级后数据完整；
4. 备份 → 破坏 → 回滚演练通过；
5. 卸载重装不丢数据；
6. 凭证残留只在模型目录，`/root/.docker` 与 `/root/.agent/settings.json` 不含明文。

### 切片 4 · 记忆（先验证，再决定）

| 步骤 | 内容 |
| --- | --- |
| 前置 | §4.1 的探针，约 10 分钟；不生效则整片作废 |
| 选择“保留” | 控制台加“清空记忆”入口（走 `doops_shell` 删 `/root/.agent/memory`，仅限当前 target） |
| 选择“禁用” | 只在探针证明生效后写 `doagentSettings`，并补一条“重启后无新记忆”的测试 |

验收：清空后重启仍为空；记忆只落在本 target 的数据目录，不跨 target 出现。

## 6. 待决项

| # | 问题 | 建议默认 | 不定就不要动的部分 |
| --- | --- | --- | --- |
| 1 | Chart 是否默认开持久化 | 否（存量实例没有 `nodeSelector`，默认开会让升级失败） | 切片 3 |
| 2 | 持久化时是否接受升级短暂不可用 | 接受，`Recreate` | 切片 3 |
| 3 | 记忆保留还是禁用 | 保留 + 清空入口 | 切片 4 |
| 4 | 评审文档是否随镜像下发 | 排除 | 切片 2 的一行 |
| 5 | 配额数值 | 500 篇 / 1 MiB / 256 MiB | 切片 2 的校验值 |

## 7. 不做

- 跨 target 共享知识库：与控制面授权模型冲突。
- 向量检索 / Embedding：单层 `.md` + 名称过滤够用。
- 目录层级：保持平铺，`list` 和 `delete` 都简单。
- `doops kb` CLI 子命令：控制台和 Agent 才是主路径，加了就是第 4 个入口（`KB-09`）。
- 给知识库单独做权限：沿用 target 授权，符合 `AGENTS.md` 的“开放性默认，不加逐操作限制”。
