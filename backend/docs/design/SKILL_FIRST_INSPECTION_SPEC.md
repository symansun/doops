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
- `DOOPS_CLUSTER`
- `DOOPS_INSTANCE`
- `DOOPS_RUN_ID`

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
- 落盘前统一执行凭据脱敏（`redactSecretText`）与 `maxInspectionArtifactBytes` 字节上限保护：Markdown 与原始输出按字节截断并追加 `... [truncated]`；JSON 不得截断成非法结构，超限时改写为 `{"truncated":true,"original_bytes":N,"limit_bytes":262144}` 信封，截断后的原文保留在 `raw.txt`。上限按单个文件（`artifact.md` / `artifact.json` / `raw.txt`）各自 ≤256 KiB 执行，不做单条记录合计。
- `PruneMonitorEngineHistory` 清理超期记录（默认 30 天）时，同步删除对应磁盘产物目录并回收空日期目录。

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
