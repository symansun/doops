# Skill-First 巡检与能力框架规范（Spec）

> 状态：权威架构规范（生效中）。
> 适用范围：Gateway 巡检调度与产物存储、Agent Skill/MCP 运行时、Web 控制台「Skill 管理 / MCP 管理 / 智能巡检」。
> 相关文档：[DOOPS_WEBUI.md](../DOOPS_WEBUI.md)、[DOOPS_API_GUIDE.md](../DOOPS_API_GUIDE.md)、[CREDENTIAL_RESOURCES.md](../CREDENTIAL_RESOURCES.md)。

---

## 1. 核心架构原则（红线）

DoOps 平台层定位为 **极薄的通用运维与巡检框架**，不承载写死的业务巡检逻辑：

1. **Skill-First（一切巡检能力 100% 进 Skill 包）**
   - 严禁在平台核心保留 `builtin_strategy` / `strategy_id` 双轨后门。
   - 所有系统预置巡检（虚机/物理机、容器运行时、Kubernetes、Web/应用服务、数据库、违规代理、补丁合规、远程主机）与用户自定义巡检，统一以 **自包含的 Skill 目录包** 存在。
   - 在控制台修改任意 Skill 的 `SKILL.md` 或 `scripts/` 脚本后，下次执行必须立即反映修改，不允许绕开 Skill 文件走硬编码逻辑。
   - **巡检能力的唯一来源是 Skill 包。** 原生采集器（`vm_host` / `ssh_host` / `k8s_cluster` / `container_runtime` / `app_service` / `database_service` 六条 `builtin_strategy`）与内部工具 `doops_monitor_inspect` 已整体退役，代码中不再存在；新增巡检能力只能新增或修改 Skill 包，不得复活任何原生采集分支。
   - 请求仍携带 `strategies` 或 `ssh_hosts` 时，服务端一律返回 `400`（`strategies_removed` / `ssh_hosts_removed`）并提示改用 `skill/actions`，不得静默忽略。历史 run 记录的 `strategy` 字段只读展示，不再参与分派。
2. **MCP 为纯粹工具能力层**
   - 「MCP 管理」统一治理内置 MCP（`doops`、`dosql`）与自定义外部 MCP（`stdio` / `http` / `sse`），提供真实的 JSON-RPC 2.0 `initialize` + `tools/list` 探测与原子热同步（写入 `/root/.agent/doops-mcp-servers.json` 并合并进 `/root/.agent/settings.json` 与 `do-agent` 允许工具列表）。
   - 「AI 助手」与「智能巡检」共享同一套已同步 Skill 与 MCP，不存在仅归属于巡检的孤立 MCP 开关。
3. **File-First 存储（文件落盘，数据库只做轻量索引）**
   - Skill 正文与附属脚本落盘在 Gateway `<dbDir>/skills/<slug>/`，SQLite 仅保存元数据索引。
   - 巡检产物（`artifact.md`、`artifact.json`、`raw.txt`）落盘在 Gateway `<dbDir>/artifacts/<YYYY-MM-DD>/<run_id>/<item_id>/`，SQLite 仅存轻量索引（状态、摘要、路径、大小、SHA-256），支持按日期滚动清理，避免长期运行拖垮 SQLite。
4. **异构零锁死 + 轻量可选协议（面向源源不断的巡检流）**
   - 任意 Skill、提示词或临时 Shell 命令可输出任何结构的 `JSON`、`Markdown + JSON`、`Markdown` 或纯文本，前端动态可视化器不得写死业务字段。
   - 同时定义轻量可选输出约定（`status` / `summary` / `metrics[]` / `checks[]` / `charts[]`）：遵守该约定时自动解锁 **通用时序趋势曲线、跨次对比 Diff、通用图表原语** 与「态势感知」指标桥接。
5. **真实可信与安全边界**
   - 沿用 `(user, cluster, instance)` 目标授权模型；离线节点必须显式报错/标缺口，不得伪造结果或静默降级为演示数据。
   - 凭据载荷严禁进入 Skill 源码、命令参数、环境变量、日志或巡检产物；所有产物在落盘前执行凭据脱敏与字节上限保护（单条上限 `256 KiB`）。

---

## 2. Skill 包规范与统一执行模型

### 2.1 目录结构

Gateway 侧目录 `<dbDir>/skills/<slug>/` 经内部工具 `doops_skill_sync` 原子镜像至目标 Agent `/root/.agent/skills/<slug>/`：

```text
<slug>/
├── SKILL.md            # 必需：YAML Frontmatter + Markdown 规范正文
├── scripts/            # 可选：可执行采集/检查脚本（自动赋予 0755 权限）
│   └── inspect.sh      # 推荐默认确定性采集入口
└── references/         # 可选：基线文档、查询模板、参考说明
```

安全约束：
- `slug` 仅允许 `^[a-z0-9][a-z0-9-]{0,62}$`。
- 所有附属路径必须为安全相对路径（禁止绝对路径、`..`、隐藏段或覆盖 `SKILL.md`）。
- 单个 Skill 总大小上限 `1 MiB`，最多 `16` 个附属文件。

### 2.2 `SKILL.md` Frontmatter 约定

```yaml
---
name: 虚机与物理机系统巡检 Skill
description: 只读采集 CPU、内存、去重磁盘容量、运行时长、进程与内核安全基线
entrypoint: scripts/inspect.sh
required_mcp: doops
---
```

| 字段 | 必需 | 说明 |
|---|---|---|
| `name` | 否 | 显示名称，缺省回退 `slug` |
| `description` | 否 | 简介说明 |
| `entrypoint` | 否 | 确定性脚本入口相对路径（如 `scripts/inspect.sh`），缺省若存在 `scripts/inspect.sh` 自动识别 |
| `required_mcp` | 否 | 逗号分隔的推荐/依赖 MCP 标识（如 `dosql`、`doops`），供 UI 展示能力依赖 |

### 2.3 统一执行模型（`exec_mode`）

取消 `builtin_strategy`，每个 Skill 仅通过 `exec_mode`（单次发起/试跑可选：`auto` | `script` | `agent`，默认 `auto`）统一执行：

| 模式 | 触发条件 | Agent 实际执行通道 | 适用场景 |
|---|---|---|---|
| **脚本直跑（`script`）** | `exec_mode=script`，或 `exec_mode=auto` 且 Skill 具备有效 `entrypoint` 且本次未传入自然语言 `prompt` | 先 `doops_skill_sync` 同步最新目录，再经审计 `doops_shell` 执行 `/root/.agent/skills/<slug>/<entrypoint>` | 高频定时采集、零 Token、百毫秒确定性输出 |
| **AI 驱动（`agent`）** | `exec_mode=agent`，或 `exec_mode=auto` 且 Skill 无脚本入口、或本次传入了自定义 `prompt` | 先 `doops_skill_sync` 同步最新目录，再经 `doops_agent_prompt` 驱动 `do-agent` 读取 `SKILL.md`、按需运行 `scripts/` 与调用已启用的 MCP 工具 | 复杂根因分析、调用 `dosql`/`doops`/自定义 MCP、多源关联诊断 |

脚本直跑模式向子进程注入且仅注入以下非敏感元数据环境变量：
- `DOOPS_SKILL_DIR`（如 `/root/.agent/skills/inspect-vm-host`）
- `DOOPS_SKILL_SLUG`
- `DOOPS_TARGET_CLUSTER`
- `DOOPS_TARGET_INSTANCE`
- `DOOPS_RUN_ID`

脚本可读取这些变量用于自述目标，也可以完全不读，二者都不影响判定结果；判定只依据脚本的 stdout 产物。脚本目录根由 `DOOPS_AGENT_SKILLS_ROOT` 决定（缺省 `/root/.agent/skills`），脚本以 `sh <root>/<slug>/<entrypoint>` 方式执行。

#### 2.3.1 执行证据里的「声明模式」与「调度模式」（SF-18）

运行明细（`GET /v1/monitor/runs/<id>` 的 `items[]`）对每个 Skill 项同时记录两个模式，二者含义不同、不得互相覆盖：

| 位置 | 字段 | 取值域 | 含义 |
|---|---|---|---|
| `evidence.exec_mode` | 声明模式 | `auto` \| `script` \| `agent` | `SKILL.md` frontmatter 里声明的 `exec_mode`（未声明按 `auto`）。它表达包作者的意图，与实际是否执行脚本无关。 |
| `evidence.dispatch_mode`，以及明细项顶层 `dispatch_mode` | 调度模式 | `script` \| `agent` | 本次运行调用 `resolveSkillDispatchMode` 得到的调度决策：`script` 表示走受审计 `doops_shell` 直跑 `entrypoint`，`agent` 表示走 `doops_agent_prompt`。 |

顶层 `dispatch_mode` 是 `evidence.dispatch_mode` 的便捷投影，两者恒等。`dispatch_mode` 为空（JSON 中省略）表示该 run 写入于本字段引入之前，读取方按「未知」处理，不得据此推断为 `agent`；旧 run 的其余字段与解析行为不受影响。

声明与调度不一致是合法且常见的情况，也不代表执行出错：

- `exec_mode=auto` 且包内有有效 `entrypoint`、本次未传 `prompt` ⇒ `dispatch_mode=script`（最常见的正常态，`auto` 不等于 `agent`）；
- `exec_mode=auto` 且本次传入自定义 `prompt` ⇒ `dispatch_mode=agent`；
- `exec_mode=script` ⇒ `dispatch_mode=script`；`exec_mode=agent` ⇒ `dispatch_mode=agent`。

`dispatch_mode` 只记录**调度决策**，不记录执行中途的降级。AI 驱动路径在 `doops_agent_prompt` 失败且包内有可执行脚本时会降级为脚本直跑，此时 `collector_method=skill_script`、`evidence.script` 与 `evidence.fallback_from_ai` 记录真实执行路径，而 `dispatch_mode` 保持 `agent`（不丢失「本应按 AI 调度」这一事实）。运维与前端判断「到底跑了什么」看 `collector_method` 与实际产物，判断「本应怎么调度」看 `dispatch_mode`。

### 2.4 系统预置 Skill 包（全部真实可运行、可编辑）

| Skill Slug | 默认形态 | 真实采集/能力来源 |
|---|---|---|
| `inspect-vm-host` | `scripts/inspect.sh`（脚本直跑 / 可切换 AI） | 只读读取 OS、内核、uptime、load、CPU、内存、**按底层文件系统/设备去重的磁盘挂载点（禁止多挂载点重复累加）**、进程/僵尸进程、sysctl 基线、账户文件指纹 |
| `inspect-container-runtime` | `scripts/inspect.sh`（脚本直跑 / 可切换 AI） | 自动探测 `docker` / `nerdctl` / `podman`，采集运行中容器状态、CPU、内存与 `memory_limit` 占比 |
| `inspect-k8s-cluster` | `scripts/inspect.sh` + `required_mcp: doops` | 只读调用 `kubectl` 检查 Node Ready/压力、异常与高重启 Pod、Deployment/StatefulSet 副本、PVC 与近期 Warning 事件 |
| `inspect-app-web` | `scripts/inspect.sh` | 检查本机监听端口、Nginx 配置/状态及可选 URL 健康检查（`DOOPS_INSPECT_URLS`） |
| `inspect-database` | `SKILL.md` + `required_mcp: dosql`（AI 驱动） | 通过受管 `dosql` MCP 只读巡检数据库连通性、慢查询/锁/表空间，凭据不落脚本 |
| `inspect-ssh-host` | `SKILL.md`（AI 驱动 / 可扩展脚本） | 通过受管 SSH 路径对无 Agent 主机执行只读基线检查 |
| `inspect-proxy-evasion` | `scripts/inspect.sh`（脚本直跑 / 可切换 AI） | 检查代理监听端口（1080/7890/7891/8118/10808/20170/3128）、代理环境变量与可疑隧道进程 |
| `inspect-package-vuln` | `scripts/inspect.sh`（脚本直跑 / 可切换 AI） | 检查发行版、内核、待更新安全补丁（`apt`/`dnf`/`yum`/`apk`）与重启需求标记 |

平台管理员可在控制台编辑上述系统 Skill 的 `SKILL.md` 与 `scripts/`，也可一键 `reset_system` 恢复内置默认。

历史兼容：早期 run 记录里的 `strategy` 值（`vm_host`、`ssh_host`、`k8s_cluster`、`container_runtime`、`app_service`、`database_service`）在重测（`retest_run_id`）时按上表映射到对应 Skill 包并重新执行；映射不到的历史资产保留为 `skipped` 项并给出 `legacy_strategy_retired` 原因，不静默丢失。

#### 2.4.1 退役采集器的能力落点（D6：不静默删能力）

六条原生采集器在 SF-09 退役，能力必须由 Skill 包承接。逐条核对如下；包内容欠账显式标注，不靠「已退役」掩盖能力消失。

| 退役 strategy | 承接 Skill 包 | 覆盖状态 |
|---|---|---|
| `vm_host` | `inspect-vm-host` | 已覆盖：uptime、CPU/内存、磁盘容量（按底层设备去重）、`host.disk_io`（读 `/proc/diskstats` 物理块设备的读写次数/字节/平均延迟，排除分区与 `loop`/`dm-`/`md` 等虚拟设备；延迟阈值默认 50 ms，对齐 `DiskLatencyWarnMs`）、进程/僵尸、sysctl 基线、登录安全基线 |
| `ssh_host` | `inspect-ssh-host` | 部分覆盖：包内契约已含 CPU/内存/磁盘/uptime/内核安全基线，但平台当前没有 AI 可见的受管 SSH 通道，只能如实上报 `managed_ssh_channel=unavailable`。能力缺口不在包内容，在 AI 工具白名单（另立任务，不在 SF-09/SF-13 范围） |
| `k8s_cluster` | `inspect-k8s-cluster` | 已覆盖：apiserver 连通、节点/工作负载/Pod/PVC/事件、`k8s.metrics`（`kubectl top` 取节点与 Pod 实时 CPU/内存；metrics-server 未安装或无权限时该检查记 `warning` 并写明原因，不改变集群健康结论） |
| `container_runtime` | `inspect-container-runtime` | 已覆盖：runtime 可用性、容器内存上限与用量 |
| `app_service` | `inspect-app-web` | 已覆盖：本机监听、Nginx 配置、HTTP 探活（状态码/延迟/证书剩余天数/安全响应头） |
| `database_service` | `inspect-database` | 已覆盖：受管数据库只读连通性、活跃连接、锁等待与慢查询概况（AI 驱动，无确定性脚本） |

`host.disk_io` 与 `k8s.metrics` 产出的 `metrics[]` 键按 §4.2 通用时序落库，不新增 `monitor_resource_snapshots` 桥接键；两者的降级路径（`/proc/diskstats` 不可读、metrics-server 不可用）都必须写进对应 `checks[].detail`，不得静默返回 0。

---

## 3. 巡检产物文件存储规范

### 3.1 目录布局

```text
<dbDir>/artifacts/
└── 2026-11-23/
    └── run_xxxxxxxxxxxx/
        └── item_<id>/
            ├── artifact.md     # Markdown 正文（若存在）
            ├── artifact.json   # 压缩后的合法 JSON（若存在）
            └── raw.txt         # 脱敏原始输出（若与上两者不同）
```

### 3.2 SQLite 索引契约

`monitor_run_items` 新增文件索引列：
- `artifact_dir TEXT NOT NULL DEFAULT ''`（相对 `<dbDir>/artifacts` 的路径，如 `2026-11-23/run_xxx/item_12`）
- `artifact_bytes INTEGER NOT NULL DEFAULT 0`
- `artifact_sha256 TEXT NOT NULL DEFAULT ''`
- `artifact_truncated INTEGER NOT NULL DEFAULT 0`（1 表示该条产物超过 256 KiB 上限已被截断）

写入规则：
- 新产生的巡检记录一律落盘至 `artifact_dir`，SQLite 对应文本列（`artifact_markdown` / `artifact_json` / `raw_output`）置空不再重复存储大文本。
- 读取 `GET /v1/monitor/runs/<id>` 时，优先从 `artifact_dir` 透明加载；若 `artifact_dir` 为空（历史升级前旧数据），自动回读 SQLite 旧文本列，保证平滑无损升级。

产物可用状态（`artifact_state`）：
- run 详情返回的每个 item 都带 `artifact_state`：`present` = 正文可渲染（从磁盘文件读出，或 `artifact_dir` 为空时从 SQLite 文本列回退）；`missing` = `artifact_dir` 有值但该目录下读不到任何产物文件（`artifact.md` / `artifact.json` / `raw.txt` 全不可读）。`artifact_truncated=true` 只出现在 `present` 下，表示文件在但被 256 KiB 上限截断，是 `present` 的子状态而不是第三个取值。
- `missing` 是**正常生命周期状态**，不是异常：§3.3 的保留策略按「先删文件、后删索引行」执行，两步之间天然存在「索引行指向已删目录」的窗口；目录删不掉但行已删、运维手工删目录、挂载丢失也会落到 `missing`。run 级别的 `artifact_state` 是聚合：任一 item 为 `missing` 即 `missing`，否则 `present`（无 item 的运行也是 `present`）。
- 接口仍返回 200，`run` 与 `items` 结构完整，只是正文为空；不得用 4xx——run 本身存在，只是附件没了。前端对 `missing` 的话术：「本次巡检的原始产物已被保留策略清理（默认保留 30 天），正文不可用；指标趋势仍按 90 天保留」。手工删除与挂载异常无法和保留策略区分，话术只说明产物文件已不存在/不可读，不断言具体原因。`present` 但正文为空只会来自本就没有产物的 gap 行（如 target offline）或空产物，不是丢失。
- 老数据（`artifact_dir` 为空、从 SQLite 文本列回退）必须有正文并报 `present`，不得误报 `missing`。
- 落盘前统一执行凭据脱敏（`redactSecretText`）与 `maxInspectionArtifactBytes` 字节上限保护：Markdown 与原始输出按字节截断并追加 `... [truncated]`；JSON 不得截断成非法结构，超限时改写为 `{"truncated":true,"original_bytes":N,"limit_bytes":262144}` 信封，截断后的原文保留在 `raw.txt`。上限按单个文件（`artifact.md` / `artifact.json` / `raw.txt`）各自 ≤256 KiB 执行，不做单条记录合计。
- 超期记录由 §3.3 的保留策略清理：删除对应磁盘产物目录并回收空日期目录。

### 3.3 产物与指标的生命周期（保留策略）

巡检产物与指标点都不是只增不减。清理必须满足三条：不影响正在跑的巡检、不抹掉每个 schedule 的近期历史、指标比产物留得久。

保留窗口（环境变量在每次清理时读取）：

| 环境变量 | 默认 | 含义 |
|---|---|---|
| `DOOPS_MONITOR_ARTIFACT_RETENTION_DAYS` | 30 | 巡检 run 及其产物目录、`monitor_run_items`、`monitor_resource_snapshots` 的保留天数 |
| `DOOPS_MONITOR_ARTIFACT_KEEP_RUNS` | 50 | 每个 schedule 最近 N 次 run 无条件保留，即使已超出保留天数 |
| `DOOPS_MONITOR_METRIC_RETENTION_DAYS` | 90 | `inspection_metric_points` 的保留天数 |

- 非法值（非整数、0、负数）回落默认并记一条日志；关闭清理不受支持（要留更久就把天数调大）。
- 永不删除：`status='running'` 的 run；每个 schedule 分组（含 `schedule_id=''` 的手动巡检分组）最近 `DOOPS_MONITOR_ARTIFACT_KEEP_RUNS` 次 run。保留次数下限存在的原因：低频 schedule 一旦停跑几个月，超出天数的历史会在它下次运行的瞬间被全部清掉；保底最近 N 次让重新启用的 schedule 仍有可对比的基线。
- 指标比产物留得久：产物是单次巡检的正文凭据，指标是 §4.2 趋势图的数据点；产物过期不影响曲线连续性，所以指标窗口默认更长（90 天）。
- 顺序是先删文件、后删索引行：反过来的话，进程在两步之间被杀会留下「索引行指向已消失目录」的状态，详情接口读到空目录只能返回 200 空正文，用户看不出是数据没了还是采集失败。先删文件后删行，最坏结果是「目录已删、行还在」，该行下一轮扫描会重新命中并补删（自愈），期间详情正文为空且 `artifact_state=missing`（见 §3.2），用户可区分于采集失败；数据库写失败不属于可本地消除的风险，只能靠重试收敛。
- 目录删不掉（只读挂载、权限不足）时记一条 warn（`run_id`、`artifact_dir`、错误），**仍然删除索引行**：否则索引永远指向删不掉的目录、每次扫描都重复失败刷日志。遗留的孤儿目录由运维回收，不阻塞清理。
- 触发点两个：巡检落库后触发一次（进程内 15 分钟节流、单飞，同一时刻只有一个清理在跑，避免并发重复删除），以及 `instance_retention` 的每小时兜底（强制全扫，不受节流影响）。
- 单次扫描有界：最多处理 50 个 run；指标按 500 行一批、最多 20 批。剩余量留给下一轮，避免一次清理长时间持有写锁。

---

## 4. 异构产物通用约定、时序趋势与跨次 Diff

### 4.1 轻量可选输出协议（Zero Lock-In）

任意 Skill/Prompt/命令输出纯文本、纯 JSON 或 Markdown+JSON 均合法。若 JSON 顶层包含以下可选字段，框架自动赋能高级观测能力：

```json
{
  "status": "pass | warning | critical",
  "summary": "一句话总结",
  "metrics": [
    { "key": "cpu_percent", "label": "CPU 使用率", "value": 12.4, "unit": "%", "status": "pass" },
    { "key": "disk_percent", "label": "最高挂载点占比", "value": 2.4, "unit": "%", "status": "pass" }
  ],
  "checks": [
    { "id": "host.disk", "name": "磁盘容量", "category": "resource", "status": "pass", "value": "/ 2.4%", "detail": "..." }
  ],
  "charts": [
    {
      "title": "关键资源水位",
      "type": "bar | gauge | line",
      "unit": "%",
      "series": [
        { "label": "CPU", "value": 12.4, "status": "pass" },
        { "label": "内存", "value": 26.4, "status": "pass" },
        { "label": "磁盘", "value": 2.4, "status": "pass" }
      ]
    }
  ]
}
```

### 4.2 通用异构指标时序表（`inspection_metric_points`）

- Gateway 解析产物时，自动从 `metrics[]`（或合法数值型顶层指标字段）提取数值点，写入：
  `inspection_metric_points(id, collected_at_ms, run_id, schedule_id, cluster, instance, skill_slug, metric_key, metric_label, value, unit, status)`。
- 当 `skill_slug` 输出标准主机/容器指标键（如 `cpu_percent`、`memory_percent`、`disk_percent`、`load_1m`、`uptime_seconds`、`memory_used_bytes`、`memory_total_bytes`、`disk_used_bytes`、`disk_total_bytes`）时，自动桥接写入 `monitor_resource_snapshots`，持续驱动「态势感知」真实快照。
- 对外提供只读查询接口：
  `GET /v1/monitor/metrics?skill=<slug>&target=<cluster/instance>&range=<1h|6h|24h|7d|30d>`
  按 `metric_key` 聚合返回时间序列点，任何新编写的自定义 Skill 只要输出 `metrics[]` 即自动获得历史趋势曲线。

### 4.3 跨次自动对比（Diff）

- `GET /v1/monitor/runs/<id>` 对每条 `item` 自动按同 `(cluster, instance, strategy/skill_slug)` 查找紧邻的前一次已完成执行记录，计算并返回 `diff`：
  - `previous_run_id`、`previous_at_ms`、`previous_status`、`status_changed`
  - `metric_deltas[]`：`{ key, label, unit, previous, current, delta, delta_pct }`
  - `added_checks[]` / `resolved_checks[]` / `changed_checks[]`（按 `check.id` 或 `check.name` 匹配，突出显示新出现的异常、已恢复项与状态退化项）
- `status_changed` 与 `previous_status` 固定为 **run 级**（实现即 run 级）：一次巡检的整体状态变化由 run 表达，item 级的差异由 `changed_checks[]` 表达，不再引入 item 级 `status_changed`，避免同一份响应里出现两套互相矛盾的状态口径。
- `metric_deltas[]` 的渲染契约：未变化的键直接省略（不输出 `delta=0`），上一次存在而本次消失的键按 `0` 计负 delta，保证前端只在有变化时渲染芯片且不会出现空涨跌。
- 有界上限（超出上限一律截断，不报错）：时序查询 `LIMIT 4000` 行，单张趋势图最多渲染 `720` 点；`range` 白名单为 `1h` / `6h` / `24h` / `7d` / `30d`，其中 `30d` 仅在新指标时序接口开放，run 详情与概览不接受 `30d`。

### 4.4 前端动态可视化器（`InspectionArtifactViewer`）

按如下层次渲染，零业务硬编码：
1. **跨次变化条（Diff）**：存在 `diff` 时，顶部紧凑展示状态变化、指标涨跌芯片与新增/恢复异常项。
2. **历史趋势条（Trends）**：存在多点 `inspection_metric_points` 时，展示轻量 SVG 趋势折线小卡。
3. **声明式通用图表（`charts[]`）**：若产物 JSON 含 `charts[]`，用内置通用 SVG/CSS 图表原语渲染 `bar` / `gauge` / `line`。
4. **零硬编码动态结构渲染（`DynamicJsonNode`）**：Markdown 正文 + 结论横幅 + 标量 KPI / 进度条 + 对象数组动态表格 + 嵌套分组卡片。
5. **三态切换与导出**：始终支持「可视化 / Markdown / JSON」切换、复制与 `.md`/`.json` 下载。

---

## 5. 验收红线

1. 修改系统 Skill（如 `inspect-vm-host`）的 `scripts/inspect.sh` 后直接试跑，返回结果必须真实反映修改内容。
2. `inspect-vm-host` 在存在同盘多挂载点（如 `/` 与 `/root/.doops` 同属一块磁盘）的 Linux 容器/主机上，`disk_total_bytes` 必须按设备/文件系统去重，不得出现翻倍累加。
3. 新巡检产物必须真实落盘在 `<dbDir>/artifacts/<date>/<run_id>/item_<id>/`，SQLite `monitor_run_items` 文本列为空而详情接口完整返回。
4. 连续两次执行同一 Skill（系统或自定义）后，`GET /v1/monitor/runs/<id>` 返回准确 `diff`，`GET /v1/monitor/metrics` 返回对应时序点并在前端展示趋势图。
5. AI Skill 驱动模式与脚本直跑模式均通过真实 Gateway + Agent 验证，全站 0 假数据、0 控制台报错、0 HTTP >= 400。
