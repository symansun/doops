---
name: 数据库运行与连通性巡检 Skill
description: 通过受管 DoSql MCP 执行只读数据库巡检，凭据不落地
required_mcp: dosql
---

# 数据库运行与连通性巡检 Skill

## 执行形态（如实声明）
- **AI 驱动、无确定性脚本**：本包不提供 `scripts/`，也不声明 `entrypoint`；能力入口是 `required_mcp: dosql` 提供的受管 `mcp_dosql_read`，由 AI 组织只读巡检。
- 无可绑定数据库目标或 dosql 不可用时，必须在 `checks` 中显式说明缺口，`status` 取 `warning`/`critical`，`metrics` 留空；**禁止编造指标或用 0 静默通过**。
- **目标自述**：若环境提供 `DOOPS_TARGET_CLUSTER` / `DOOPS_TARGET_INSTANCE`，产物应带 `target: {cluster, instance}`；缺失取 `null`，不得编造。

## 执行步骤（严格只读）
1. 优先调用 `mcp_dosql_read` 列出当前目标可用的受管数据库连接并核查只读连通性。
2. 如存在已绑定数据库，仅执行只读健康查询（版本、当前活跃连接数、锁等待或慢查询概况）。
3. 严禁修改任何表结构或数据，严禁在输出中泄露数据库密码或连接串。

## 输出格式要求
输出简明 Markdown 总结，并附一个 JSON 代码块，包含 `status`（仅 `pass|warning|critical`）、`summary`、`metrics`（数组：`key/label/value/unit/status`）与 `checks`（数组：`id/name/category/status/value/detail`）。

## 时序指标桥接（series_points）
当本次巡检拿到了真实数值（活跃连接数、QPS/TPS、锁等待数、慢查询数）时，在同一 JSON 代码块顶层追加 `series_points` 数组，把数值接入控制台「数据库」时序曲线。每个元素：

```json
{"type":"database","asset_id":"<已绑定数据库名>","origin":"exporter","metric":"db_tps","unit":"tx/s","at_ms":<毫秒时间戳>,"status":"ok","value":12.3}
```

- 可用指标与固定单位：`db_tps`（`tx/s`）、`db_lock_waits`（`waits`）、`active_connections`（`connections`）、`requests`（`req/s`）、`response_time_ms`（`ms`）、`error_rate`（`%`）。
- `origin` 固定 `exporter`；`asset_id` 必须是 dosql 已绑定且实际采集到数据的数据库名，**禁止为无数据的数据库编造点位**；`status` 仅 `ok|missing|not_applicable`，无数据时用 `missing`/`not_applicable` 且 `value` 置 `null`。
- `unit` 必须与上表完全一致、`at_ms` 不得晚于当前时间，否则该点位会被网丢弃。
