# 对抗性复核：六条"反向实验"在 origin/main 上是否仍成立

- 复核者：qa-recheck-01（独立验证者，不写产品代码）
- 基线：`origin/main` = `c1b62473`（`docs(ops): queue QA-05 pre-release baseline rerun`，含 SF-18 的 `8001ce4f`）
- 方法：`git archive c1b62473 | tar -x -C /tmp/qa4`，另解一份 `/tmp/qa4-pristine`；所有破坏只在 /tmp 树内，恢复后 `diff -rq /tmp/qa4-pristine /tmp/qa4` 无输出（DIFF_RC=0）。工作区未被改动。
- 环境：go1.27.4 darwin/arm64；docker 29.4.0，镜像 `python:3.12-slim`、`mysql:8.4`（两项都可用，**无降级**）。
- 基线漂移：提交时 `origin/main` 已前进到 `7ebb8a2b`；`git diff --name-only c1b62473..7ebb8a2b` 只涉及 `docs/`、`lib/`、`package.json`，本次破坏涉及的 Go 源码、`inspect.sh`、夹具与 `verify.sh` 在该区间逐字节未变，故结论对 `7ebb8a2b` 同样成立。

## 结论摘要

| # | 实验 | 声称 | 实测（`c1b62473`） | 判定 |
|---|---|---|---|---|
| 1 | SF-08 DEFECT-1 非 UTF-8 输出 | 删 `encoding/errors` → 数据全丢，`mounts=0/unique=0` | 破坏后 `mounts=0`、`vdb1` 出现 0 次、`ROBUST_CHECK_FAIL` RC=1；恢复后 `mounts=1` RC=0 | 通过（守住） |
| 2 | SF-08 DEFECT-2 256 KiB 上限 | 去掉裁剪 → 产物超 262144 B（404113 B） | 破坏后 405391 B，`单条产物 405391 字节超过上限 262144` RC=1；恢复后 205158 B RC=0 | 通过（守住），**字节数与声称不符** |
| 3 | INT-08 列默认值守卫 | 删 `revoked_at` → 红并指名；逐个删 5 个 DDL 默认 → 全红（`skills_registry.go:255`/`mcp_registry.go:81`） | 三处破坏均红且错误行与声称一致；真机 mysql:8.4 复现 `ERROR 1364` | 通过（守住） |
| 4 | SF-16 artifact_state | 禁用 `missing` 判定 → `artifact_state = present after deletion, want missing` | 破坏后同一条断言红（`monitor_engine_artifact_state_test.go:70`）；恢复后 PASS | 通过（守住） |
| 5 | SF-15 保留策略顺序 | 顺序对调 → 红（`BEFORE DELETE` 触发器观测删除顺序） | 触发器测试红（`monitor_retention_test.go:230`）；**另一个顺序测试仍绿** | 通过（守住），附带说明 |
| 6 | 审计 purge 秒边界（F5） | 回退到 `started_at` 文本比较 → 越界清理 | 忠实回退（谓词+字符串参数）→ `purge crossed the cutoff: count=2`；只改谓词则仍绿 | 通过（守住），附带说明 |

六条全部"破坏→红、恢复→绿"，与台账声称的结论一致。与声称**数值/措辞**不一致之处见「与声称不符之处」。

## 复核基线（破坏之前先证明树是绿的）

```
cd /tmp/qa4/backend/agent
env -u GOROOT go build ./...        # BUILD=0
env -u GOROOT go vet ./internal/server/  # VET=0
go test ./internal/server/ -run 'TestGatewayMySQLColumnsMatchExport|TestGatewayMySQLInsertDefaultsMatchSchema|TestMonitorRetentionRemovesFilesBeforeIndexRows|TestMonitorRetentionFilesGoBeforeRowsEvenWhenRowDeleteFails|TestAuditTimeRangePreservesSecondBoundary' -count=1
# --- PASS ×5 ；ok github.com/user/doops/agent/internal/server 1.756s
```

SF-16、SF-15、审计三条涉及的 7 个源文件在 `2a29ed67` 与 `c1b62473` 之间逐字节相同（sha256：`monitor_retention.go 7d3652ae…`、`monitor_retention_test.go 365f7c04…`、`audit_purge.go 09b4b8be…`、`gateway_store_audit.go 2e47bb76…`、`upgrade_audit_test.go 1db233ae…`、`gateway_mysql_columns_test.go eb841565…`、`gateway_mysql_schema.sql` 同哈希）；`monitor_engine_store.go` 因 SF-18（`8001ce4f`）有改动，第 4 项在 `c1b62473` 上重跑了破坏与恢复。

## 逐项证据

### 1. SF-08 DEFECT-1：非 UTF-8 命令输出被整段丢弃

- 声称（`docs/operations/backlog.md:53`、`docs/operations/pm-worklog.md:94`）：6 个 `inspect.sh` 的 run 助手缺 `encoding="utf-8", errors="replace"`，反向实验 `mounts=0/unique=0` 数据全丢。
- 现状核对：`grep -c 'encoding="utf-8", errors="replace"' skills_builtin/*/scripts/inspect.sh` → app-web 1、container-runtime 1、k8s-cluster 2、package-vuln 1、proxy-evasion 2、vm-host 1（6 个文件共 8 处）。
- 破坏：`skills_builtin/inspect-vm-host/scripts/inspect.sh` 中 `text=True, encoding="utf-8", errors="replace", timeout=timeout` → `text=True, timeout=timeout`。
- 命令（真跑，`python:3.12-slim`，夹具只读挂载）：
  ```
  docker run --rm --entrypoint sh -v $W:/w:ro -v $FIX:/f:ro -v /tmp/qa4-e1b:/out -e CASE=nonutf8 -e OUT=/out -e W=/w python:3.12-slim -c 'sh /f/robust/case_nonutf8.sh'
  python3 testdata/skills_builtin_verify/robust_check.py /tmp/qa4-e1b/nonutf8.inspect-vm-host.md inspect-vm-host --spec 'minmounts=1,forbid=未采集到可去重'
  ```
  其中 `verify.sh:485` 的期望是 `minmounts=1,forbid=未采集到可去重`。
- 期望：红（挂载点丢失）。实际（原始判定行）：
  ```
  ROBUST_OK nonutf8
  -rw-r--r--  1 wwyz  wheel  6163 ... nonutf8.inspect-vm-host.md
  ROBUST_CHECK_FAIL inspect-vm-host: mounts 数量不足（0 < 1）
  E1BREAK_RC=1
  ```
  破坏后产物中 `vdb1` 出现 0 次；恢复后同命令 → `status=warning ... checks=7 mounts=1 bytes=6845`，`E1BASE_RC=0`。
- 判定：**一致**。补充：`case_nonutf8.sh` 自身仍打印 `ROBUST_OK` 并退出 0——真正变红的是 `robust_check.py` 断言阶段；"脚本失败"的说法不准确。

### 2. SF-08 DEFECT-2：Markdown 正文未受 256 KiB 约束

- 声称（`docs/operations/pm-worklog.md:94`）：256 KiB 只约束 JSON payload、正文未裁剪；反向实验 longline `404113 B` 超限。
- 破坏：`inspect-vm-host/scripts/inspect.sh:100` `budget = HARD_CAP_BYTES - len(json_text.encode("utf-8")) - 14` → `budget = 10 ** 9`（`HARD_CAP_BYTES = 256*1024`）。
- 命令：同上，`CASE=longline`，断言 `--spec 'max=262144'`（`verify.sh:484`）。
- 期望：红（产物超 262144 B）。实际：
  ```
  ROBUST_OK longline
  -rw-r--r--  1 wwyz  wheel  405391 ... longline.inspect-vm-host.md
  ROBUST_CHECK_FAIL inspect-vm-host: 单条产物 405391 字节超过上限 262144
  E2BREAK_RC=1
  ```
  恢复后：`status=critical ... 载荷超过 204800 字节保护阈值，非必需列表已裁剪；原始 604763 checks=8 mounts=0 bytes=205158`，`E2BASE_RC=0`。
- 判定：**结论一致，字节数不一致**（见下节）。

### 3. INT-08：列默认值守卫（静态断言 + 真机 MySQL 8.4）

- 声称（`docs/operations/pm-worklog.md:174`）：删 `revoked_at` → 红且指名 `credentials.revoked_at`；逐个删 5 个 DDL 默认 → 全红（`skills_registry.go:255`、`mcp_registry.go:81`）。
- 基线：`TestGatewayMySQLColumnsMatchExport`、`TestGatewayMySQLInsertDefaultsMatchSchema` 均 PASS（1.827s）；守卫是"内嵌 DDL vs 冻结 SQLite 导出"的纯静态比对，不需要真机，故**未降级**。
- 破坏 A：`gateway_mysql_schema.sql:219` 去掉 `DEFAULT 'agent_prompt'`：
  ```
  --- FAIL: TestGatewayMySQLInsertDefaultsMatchSchema (0.14s)
      gateway_mysql_columns_test.go:89: platform_skills.driver_mode is NOT NULL without a default in the MySQL DDL, but skills_registry.go:255 inserts into platform_skills without it (MySQL error 1364); give the column a VARCHAR default or add it to the INSERT
  ```
- 破坏 B：删除 `credentials` 表的 `revoked_at` 列（`gateway_mysql_schema.sql:850`）：
  ```
  --- FAIL: TestGatewayMySQLColumnsMatchExport (0.01s)
      gateway_mysql_columns_test.go:28: credentials columns differ between the frozen SQLite export and the MySQL DDL:
           only in export: [revoked_at]
  ```
- 破坏 C：`gateway_mysql_schema.sql:201` 去掉 `last_probe_status` 的 `DEFAULT ''`：
  ```
      gateway_mysql_columns_test.go:89: platform_mcp_servers.last_probe_status is NOT NULL without a default in the MySQL DDL, but mcp_registry.go:81 inserts into platform_mcp_servers without it (MySQL error 1364); give the column a VARCHAR default or add it to the INSERT
  ```
- 真机（`mysql:8.4`，schema 与 `c1b62473` 逐字节相同）：整份 DDL 应用成功（69 张表，`DDL_APPLY=ok`），`SHOW COLUMNS FROM platform_skills LIKE 'driver_mode'` → `Default: agent_prompt`；按 `skills_registry.go:255` 的列清单 INSERT 成功；`ALTER TABLE platform_skills ALTER COLUMN driver_mode DROP DEFAULT` 后同一条 INSERT → `ERROR 1364 (HY000) at line 1: Field 'driver_mode' doesn't have a default value`；恢复默认后 INSERT 成功且回读 `driver_mode=agent_prompt`。真机用例 `TestGatewayMySQLRuntimeEquivalentSyntax`（`DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT` 指向容器）PASS（0.965s）。
- 判定：**一致**（措辞差异见下节）。

### 4. SF-16：`artifact_state` 的 `missing` 判定

- 声称（`docs/operations/pm-worklog.md:192`）：`missing` 判定加 `&& false` → `artifact_state = present after deletion, want missing` @ `monitor_engine_artifact_state_test.go:70`。
- 破坏：`monitor_engine_store.go:578`（SF-18 后行号由 569 移到 578）`if item.ArtifactDir != "" && !artifactFilesRead {` → `if false && item.ArtifactDir != "" && !artifactFilesRead {`。
- 期望：红。实际：
  ```
  --- FAIL: TestMonitorRunDetailArtifactStateMissingAfterPayloadDeleted (0.31s)
      monitor_engine_artifact_state_test.go:70: run artifact_state = present after deletion, want missing: {"items":[{"id":1,...,"artifact_state":"present","diff":null}],"run":{...,"artifact_state":"present"}}
  ```
  恢复后两条测试 PASS（`ok ... 1.491s`）。
- 判定：**一致**。

### 5. SF-15：先删文件后删索引行

- 声称（`docs/operations/pm-worklog.md:163`）：4 组反向实验全红，其中用 `BEFORE DELETE` 触发器直接观测删除顺序。
- 基线：`TestMonitorRetentionRemovesFilesBeforeIndexRows`、`TestMonitorRetentionFilesGoBeforeRowsEvenWhenRowDeleteFails` 均 PASS。
- 破坏：把 `monitor_retention.go` 的 "Files first" 目录删除循环（`287-298` 行）整体移到 `tx.Commit()` 之后——即"先删索引行、后删文件"。
- 期望：红。实际：
  ```
  --- FAIL: TestMonitorRetentionFilesGoBeforeRowsEvenWhenRowDeleteFails (0.06s)
      monitor_retention_test.go:230: the payload must be removed before the index delete is attempted, got runs=0 items=0 metrics=0 dirs=0 dir_failures=0 throttled=false busy=false
  ```
- 判定：**一致**，但 `TestMonitorRetentionRemovesFilesBeforeIndexRows` 在同一破坏下**仍绿**（它只要求"最终文件与行都不在"，顺序反转不可见）。见下节。

### 6. 审计 purge 秒边界（台账 F5，lead 在 m00094 追加）

- 声称（`docs/operations/backlog.md` QA-03 条目 + `1446b965` 修复）：边界 `started_at_ms` 与文本 `started_at` 混用，回退后会"越界清理"。
- 基线：`TestAuditTimeRangePreservesSecondBoundary` PASS（0.05s）。测试播种 `base-1s/base/base+0.5s/base+1s`，要求 `DeleteAuditBefore(base)` 只删 1 条。
- 破坏（忠实回退）：`audit_purge.go:63` 与 `:78` 的 `WHERE started_at_ms < ?` → `WHERE started_at < ?`，且 `gateway_store_audit.go:319` `before.UTC().UnixMilli()` → `formatTime(before.UTC())`。
- 期望：红（清理穿过 cutoff）。实际：
  ```
  --- FAIL: TestAuditTimeRangePreservesSecondBoundary (0.05s)
      upgrade_audit_test.go:389: purge crossed the cutoff: count=2 err=<nil>
  ```
- 判定：**一致**（配对回退时）。只改谓词、参数仍是 int64 时测试**仍绿**——见下节。

## 与声称不符之处

1. **SF-08 的 404113 B 不是一个可复现的常数。** 我在三个 sha 上跑同一破坏：`a8587ffc` → 404086 B，`94fe9a1e` → 404089 B，`2a29ed67`/`c1b62473`（SB-02 加了 `disk_io` 采集）→ 405391 B。同一 sha 的基线也随 SB-02 从 203983 B 长到 205158 B。差异来自时间戳/主机名/新增检查项，结论（超 262144 B）稳定，但**"404113 B"不能作为复现基准**，复核应以"破坏后 > 262144 B"为准。
2. **INT-08 的"指名 credentials.revoked_at"措辞偏宽。** 实际断言把表和列分开打印：先 `credentials columns differ between the frozen SQLite export and the MySQL DDL:`，再 `only in export: [revoked_at]`，并不输出 `credentials.revoked_at` 这个完整 token。定位足够，但台账里"指名"的表述比实际输出更强。
3. **SF-15 的"4 组反向实验全红"容易误读。** 顺序测试只有触发器那条具备判别力；`TestMonitorRetentionRemovesFilesBeforeIndexRows` 在顺序反转后依然绿。声称本身没撒谎（触发器那条确实红），但若有人据此认为两条测试都守顺序，是错的。
4. **审计边界测试的守卫是"配对"的。** 只把 `deleteAuditBatchSQL` 的谓词改成 `started_at < ?` 而参数仍是 `UnixMilli()` 时，测试保持绿（SQLite 里 INTEGER < TEXT，该批次一条也匹配不到，加之覆盖全部 completed 行的 `deleteCompletedAuditSQL` 仍用 ms 谓词，越界被掩盖）。只有"谓词 + 字符串参数"一起回退才会红。这不是漏洞，但意味着这个测试不能拦截"只改谓词"的半吊子回退。
5. **SF-08 DEFECT-1 的"红"发生在断言层，不在脚本层。** 破坏后夹具脚本仍 `ROBUST_OK` 退出 0，靠 `robust_check.py` 的 `minmounts` 规格才红。声称"反向实验 mounts=0"属实，但"脚本会失败"不属实。

## 未覆盖说明

- 第 1、2 项只对 `inspect-vm-host` 做了真跑破坏；其余 5 个包仅用 `grep -c` 确认 `encoding="utf-8", errors="replace"` 仍在位（8 处），**未逐包跑非 UTF-8/长行夹具**。
- 第 3 项测了 3 处（`platform_skills.driver_mode`、`platform_mcp_servers.last_probe_status`、`credentials.revoked_at`），台账提到的"5 个 DDL 默认"另 3 个未逐个删；`TEXT` 列加 `DEFAULT ('x')` 导致 copier `mysql_copy_schema_mismatch` 的那条实验未重做（需真机 copier 路径）。
- 第 4 项只做了"禁用 missing 判定"；声称的另一组"去序列化（item tag 改 `json:"-"`）"未重做。
- 未跑 `go test ./...` 全量、未跑 Python 契约测试、未做前端验证；本复核只覆盖上述 6 条实验。
- 守卫里 6 条 `INSERT without a parsable column list cannot be checked against the schema`（`asset_inspection_evidence.go:220/270`、`asset_inspection_schedule.go:94`、`cluster_profiles.go:86`、`dosql_copy.go:71`）只 `t.Logf`，不失败——本次未逐一核实这些表的 NOT NULL 默认值是否真的安全。

## 最该被关注的一条风险

**INT-08 守卫对"动态拼列清单的 INSERT"是静默放行。** `gateway_mysql_columns_test.go` 遇到无列清单的 INSERT 只打印 skip 日志，不计数、不设上限地继续通过；新增一条这样的 INSERT 会让对应表的 "NOT NULL 无默认" 立刻失去守卫，而 CI 依旧全绿。相比已经守住的三条性质（非 UTF-8、256 KiB、删除顺序），这是当前唯一"破坏不会被发现"的入口。
