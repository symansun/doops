# SF-10 SF-04 指标时序与跨次 Diff 独立复验报告

- 复验对象：`0c000b1c`（feat(monitor): index inspection metrics and diff consecutive runs，SF-04）；干净归档 `/tmp/sf10`，当前 HEAD `3577cacbe38b6398f3ce08055b6e2faf3a2a6cda` 已包含它。
- 方法：**只读**源码逐条定位 + 自写独立夹具 `/tmp/sf10/backend/agent/internal/server/zz_sf10_verify_test.go`（不入库）+ 3 组反向实验 + `EXPLAIN QUERY PLAN`；不引用 SF-04 自证结论。
- 结论计数：**符合 2 项（§4.2、§4.3）· 部分符合 1 项（§4.4）· 不符合 0 项**；并给出对 SF-06 报告第 16/17/22 行的改判。
- 对 SF-06 的改判：第 16 行 §4.2、第 17 行 §4.3：**未实现（进行中）→ 符合**；第 22 行 §5 红线 4：**未实现 → 部分符合**（后端 diff/metrics 全部满足，前端趋势图待 SF-05）。

---

## 一、逐条核验表

| # | Spec 条款 | 代码/文件:行 | 判定 | 证据 |
|---|---|---|---|---|
| 1 | §4.2 表字段一一对应 | `gateway_store_schema.go:400-412` DDL 列序与 spec 完全一致：`id, collected_at_ms, run_id, schedule_id, cluster, instance, skill_slug, metric_key, metric_label, value, unit, status`；`id INTEGER PRIMARY KEY AUTOINCREMENT`，其余 `NOT NULL DEFAULT`；spec 未要求 UNIQUE，重复键由提取层去重 | 符合 | T-1 |
| 2 | §4.2 索引（提交声称 4 个） | `gateway_store_schema.go:414/415/416`（series/run/target 三索引，均在 `inspection_metric_points`）+ `:417` `idx_monitor_run_items_series ON monitor_run_items(cluster, instance, strategy)` = 4 个，与 `gateway_mysql_runtime_test.go` 审计计数 CreateIndex 25→29 一致 | 符合 | T-1/T-5 |
| 3 | §4.2 提取只来自产物 JSON | `monitor_metric_store.go:152` `extractInspectionMetricForItem` 仅调用 `:165 extractMonitorArtifactMetrics(item.ArtifactJSON, item.Status)`，**完全不读 item.Metrics**；独立夹具把 `item.Metrics` 设为 999/888、artifact JSON 设为 12/34，落库与桥接快照均为 12/34 | 符合 | T-2/T-4 R2 |
| 4 | §4.2 提取规则 | 优先 `metrics[]`，无则扁平数值顶层兜底；非法 key（`monitorMetricKeyPattern=^[A-Za-z][A-Za-z0-9_.-]{0,63}$`）、重复 key、非数值/NaN/Inf 计入 `skipped`；单 item 上限 `maxInspectionMetricPointsPerItem=200`；`skipped` 经 evidence `inspection_metrics` 暴露而不新增列 | 符合 | T-3 |
| 5 | §4.2 标准键桥接 | `monitor_metric_store.go:327 bridgeInspectionMetricSnapshot` 覆盖 spec 列出的全部 9 键：`cpu_percent/memory_percent/disk_percent/load_1m/uptime_seconds/memory_used_bytes/memory_total_bytes/disk_used_bytes/disk_total_bytes`，details 标记 `source=inspection_metrics` | 符合 | T-3 |
| 6 | §4.2 桥接去重不丢多 target | `monitor_metric_store.go:295-297` 先收集本 run 已有快照的 `(cluster,instance)`，`:315` 逐 item 按键跳过；独立夹具 3 target 同 run（i1/i2/i3）+ 仅 i2 预置策略快照 → 指标点 3 条、快照 3 行且 i2 保留策略值 77 不被覆盖，i1/i3 正常桥接 | 符合 | T-2 |
| 7 | §4.2 GET /v1/monitor/metrics | `monitor_engine_http.go:488` handler：skill 需匹配 slug 正则、target 需 `cluster/instance`、range 经 `:95 monitorMetricRangeDuration` 白名单 `{默认/1h,6h,24h,7d,30d}`；非法分别 400 `invalid_monitor_metrics_query`/`invalid_monitor_metrics_range`，无权 403，非 GET 405；空结果 `"series":[]`（非 null），默认 range `1h` | 符合 | T-2/T-5 |
| 8 | §4.3 diff 字段齐全 | `monitor_metric_store.go:447 monitorRunDiff`：`previous_run_id/previous_at_ms/previous_status/status_changed/metric_deltas[]/added_checks[]/resolved_checks[]/changed_checks[]`；`:510 buildMonitorMetricDeltas` 产出 `{key,label,unit,previous,current,delta,delta_pct}`；`:584 buildMonitorCheckDiff` 按 `check.id` 优先、否则 `check.name` 匹配 | 符合 | T-2 |
| 9 | §4.3 previous=0 / 首次无 previous | `delta_pct` 为 `*float64`，`previous==0` 时为 null（夹具 errors 0→3：delta=3、delta_pct=null）；无前次终态 run 时 `buildMonitorRunItemDiff` 返回 nil → `diff:null`（夹具 run A 实测）；前次查找条件 `r.status<>''` 且 `<>'running'`、`r.started_at_ms<` 当前 | 符合 | T-2 |
| 10 | §4.3 对比数值正确 | 独立夹具 A(25)/B(31.5)/C(20)：B 的 cpu delta=+6.5、pct=+26；C 的 cpu delta=-11.5、pct=-36.51（相对 B）；未变化的 `free_disk_gb` 不进入 deltas | 符合 | T-2 |
| 11 | §4.4 前端可视化器 | `0c000b1c` 归档内 `components/`、`lib/` 无 `metric_deltas/delta_pct/previous_run_id/added_checks` 引用，viewer 无 Diff/Trends 渲染；backlog `docs/operations/backlog.md:12` 记 SF-05「前端接 SF-04：viewer Diff/Trends」**运行中** | **部分符合**（后端就绪，前端在途） | T-1 |
| 12 | §5 红线 4 | 后端两半（run 详情 diff + metrics 时序）均满足；"并在前端展示趋势图"依赖 SF-05 | **部分符合** | 同上 |

---

## 二、独立夹具与运行结果（自写，未入库）

命令（干净归档，注意 `env -u GOROOT`）：

```
cd /tmp/sf10/backend/agent && env -u GOROOT go test ./internal/server \
  -run 'TestSF10|TestMonitorMetricsHTTP|TestMonitorRunDetailDiff|TestInspectionMetric' -v
```

结果：**10 PASS / 0 FAIL，ok github.com/user/doops/agent/internal/server 3.371s**。要点：

- `TestSF10OwnDiffFixture`：A/B/C 三次 run（cpu 25→31.5→20，errors 0→3），断言 delta/pct、未变化键省略、added/changed checks、run A `diff=nil`、run C previous=sf10_b。
- `TestSF10ArtifactIsSourceNotItemMetrics`：`item.Metrics={cpu:999,mem:888}` vs artifact `{cpu:12,mem:34}` → `inspection_metric_points` 与桥接快照均为 12/34，证明不重算 `item.Metrics`、无双计。
- `TestSF10MultiTargetBridgeNoLoss`：输出 `map[i1:cpu=10 bridged=true i2:cpu=77 bridged=false i3:cpu=30 bridged=true]` —— 多 target 同 run 不丢数据、不串行，去重按 target 精确生效。
- `TestSF10HTTPEmptyShapeAndDefaultRange`：空查询 `200`、`"series":[]`、`range=1h`。
- `TestSF10QueryPlan`：列出索引与真实 plan（见第四节）。
- 既有 SF-04 测试同时全绿：`TestInspectionMetricPersistenceAndSnapshotBridge`、`TestInspectionMetricBridgeSkipsExistingSnapshot`、`TestMonitorMetricsHTTP`、`TestMonitorRunDetailDiff`、`TestInspectionMetricSchemaUpgrade`。

## 三、反向实验（证明现有测试真的钉住行为）

| 实验 | 变异 | 结果 |
|---|---|---|
| R1 | `monitor_metric_store.go` 中 `buildMonitorMetricDeltas(prevPoints, currPoints)` 交换为 `(currPoints, prevPoints)` | **FAIL**：`cpu delta = {Previous:31.5 Current:25 Delta:-6.5 ...}`，与期望 +6.5 相悖 |
| R2 | 提取分支 `if metricsRaw, ok := top["metrics"]; ok` 追加 `&& false`（禁读 `metrics[]`） | **FAIL**：`want 6 metric rows, got 0`；`points must come from artifact JSON, got map[]` |
| R3 | `persistInspectionMetricExtractions` 开头插入 `return nil`（跳过指标落库/桥接） | **FAIL**：`want 2 metric series, got 0`；`want 3 metric points (one per target), got 0` |

三组均已还原，还原后全量复跑仍全绿。

## 四、缺陷 / 观察清单（含最小修复建议 + 建议 owner）

**无正确性缺陷**；下列为可优化项，均不改判定：

1. **D1（低危·性能）diff 查询触发 `USE TEMP B-TREE FOR ORDER BY`**
   证据（`EXPLAIN QUERY PLAN`，`monitor_metric_store.go:493` 处的 prev 查询）：
   ```
   SEARCH i USING INDEX idx_monitor_run_items_series (cluster=? AND instance=? AND strategy=?)
   SEARCH r USING INDEX sqlite_autoindex_monitor_runs_1 (id=?)
   USE TEMP B-TREE FOR ORDER BY
   ```
   影响：按 `r.started_at_ms DESC` 排序需对同系列全部候选行排序；run 详情每次 GET 对每条 item 各查一次。`monitor_run_items` 有 30 天保留期，量级可控，故低危。
   最小建议：把排序键改为 `i.started_at_ms`（已有 `idx_monitor_run_items_target(cluster,instance,started_at_ms DESC)`）或补 `(cluster,instance,strategy,started_at_ms)` 索引；owner：dev-artifact-01。
2. **D2（低危·性能）metrics 时序查询用 `idx_inspection_metric_points_target`，`skill_slug` 作残差过滤**
   证据：`SEARCH inspection_metric_points USING INDEX idx_inspection_metric_points_target (cluster=? AND instance=? AND collected_at_ms>? AND collected_at_ms<?)`，无 TEMP B-TREE。`LIMIT 4000` 只对匹配行计数，**不丢数据**；仅目标历史极大时多扫。
   最小建议：可补 `(skill_slug,cluster,instance,collected_at_ms)`；owner：dev-artifact-01（非必须）。
3. **D3（低危·语义待澄清）`status_changed`/`previous_status` 取 run 级状态，而非 item 级**
   证据：`monitor_metric_store.go:493` 查询取 `r.status`，`diff.StatusChanged = run.Status != prevStatus`；而 added/resolved/changed_checks 是 item 级语义。
   建议：spec §4.3 未明确统计层级，建议在 spec 注明"以 run 级状态为准"或改为 item 级；owner：spec 维护者（SF-09 正在改 §1.1/§2.3，可顺带）。
4. **D4（信息·设计选择）** 未变化指标被省略（`math.Abs(delta)<1e-9` continue）；上一轮存在、本轮消失的指标按 `current=0` 计负 delta 而非标记消失。spec 未规定，前端 Diff 需据此实现；owner：dev-artifact-01 / SF-05。
5. **D5（信息）** `GET /v1/monitor/metrics` 上限：单次最多扫 4000 行、每 key 保留最新 720 点；30d 高频场景下会截断，属有界设计，spec 未规定，建议在文档注明。

## 五、仍缺什么（交付未闭环项）

- **§4.4 Diff/Trends 前端未接**：SF-05 运行中（`docs/operations/backlog.md:12`，owner dev-artifact-01），本次 clean tree 无对应渲染；§5 红线 4 的"前端展示趋势图"因此未闭环。
- `charts[]` 第 3 层内置通用 SVG 图表原语（bar/gauge/line）在 `components/` 未见实现（SF-06 已记，本次复核仍成立）。
- **浏览器实测未做**：只读边界 + `components/**`/`lib/**` 归 SF-05，未构建 Web、未做真实前端验证。
- **MySQL 存储后端未实跑**：仅有 `gateway_mysql_runtime_test.go` 审计计数同步（CreateIndex 25→29、AutoIncrement 7→8）；真实 MySQL 上建表/写入/查询未验证。
- **30d 真实数据量性能未压测**：D1/D2 仅 EXPLAIN 级证据。
- **§5 红线 5（真实 Gateway + Agent 双模式端到端）**：本次未覆盖，沿用 SF-06 的未覆盖记录。

## 六、提交

- 报告文件：`docs/reviews/2026-11-24-sf04-metrics-verify.md`（单文件提交）
- Spec: backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md §4.2-§4.4（含 §5 红线 4）
- Tests: `cd /tmp/sf10/backend/agent && env -u GOROOT go test ./internal/server -run 'TestSF10|TestMonitorMetricsHTTP|TestMonitorRunDetailDiff|TestInspectionMetric' -v` → 10 PASS / 0 FAIL；反向实验 R1/R2/R3 → 均按预期 FAIL 后还原；`EXPLAIN QUERY PLAN` 索引与排序计划如上。
- Co-Authored-By: qa-skill-01
