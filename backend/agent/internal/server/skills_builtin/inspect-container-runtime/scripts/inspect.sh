#!/usr/bin/env sh
set -eu

# 无 python3 时的降级路径：仍输出协议合规载荷（status=warning，不伪造 0 值）
if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 容器运行时巡检

- **综合结论**：`warning` — 缺少 python3，未能采集容器运行时指标

```json
{"status":"warning","summary":"缺少 python3，未采集容器运行时指标","metrics":[],"checks":[{"id":"runtime.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3，容器运行时探测与统计均未执行"}]}
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import re
import shutil
import subprocess
from datetime import datetime, timezone

MEM_WARN, MEM_CRIT = 80.0, 92.0
MAX_PAYLOAD_BYTES = 200 * 1024  # spec L28 单条产物硬上限 256 KiB，预留保护余量
TRIM_LIST_KEYS = ("containers", "charts")
RUNTIMES = ("docker", "nerdctl", "podman")
BYTE_MULT = {
    "b": 1, "kb": 1000, "kib": 1024, "mb": 1000 ** 2, "mib": 1024 ** 2,
    "gb": 1000 ** 3, "gib": 1024 ** 3, "tb": 1000 ** 4, "tib": 1024 ** 4,
    "pb": 1000 ** 5, "pib": 1024 ** 5,
}

def _nbytes(obj):
    return len(json.dumps(obj, ensure_ascii=False).encode("utf-8"))

def finish(payload):
    """保证单条产物 <= MAX_PAYLOAD_BYTES；发生裁剪时追加 output.size 检查项（真实反映）。"""
    for c in payload.get("checks") or []:
        if isinstance(c.get("detail"), str) and len(c["detail"]) > 600:
            c["detail"] = c["detail"][:600] + "...(已截断)"
    original = _nbytes(payload)
    if original <= MAX_PAYLOAD_BYTES:
        payload["output_bytes"] = _nbytes(payload)
        return payload
    for limit in (50, 10, 5):
        for k in TRIM_LIST_KEYS:
            v = payload.get(k)
            if isinstance(v, list) and len(v) > limit:
                payload[k] = v[:limit]
        if _nbytes(payload) <= MAX_PAYLOAD_BYTES:
            break
    if _nbytes(payload) > MAX_PAYLOAD_BYTES:
        for k in TRIM_LIST_KEYS:
            if isinstance(payload.get(k), list) and k != "charts":
                payload[k] = []
        if _nbytes(payload) > MAX_PAYLOAD_BYTES:
            payload["charts"] = []
            payload["checks"] = (payload.get("checks") or [])[:20]
            for c in payload["checks"]:
                c["detail"] = (c.get("detail") or "")[:120]
            payload["metrics"] = (payload.get("metrics") or [])[:20]
    payload.setdefault("checks", []).append({
        "id": "output.size",
        "name": "输出体积保护",
        "category": "runtime",
        "status": "warning",
        "value": "%dB" % original,
        "detail": "载荷超过 %d 字节保护阈值，非必需列表已裁剪；原始 %d 字节" % (MAX_PAYLOAD_BYTES, original),
    })
    payload["output_bytes"] = _nbytes(payload)
    return payload

def run(cmd, timeout=15):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=timeout)
        return r.returncode, r.stdout.strip(), r.stderr.strip()
    except Exception as e:
        return 1, "", str(e)

def parse_pct(s):
    m = re.search(r"([0-9]+(?:\.[0-9]+)?)", str(s or ""))
    return round(float(m.group(1)), 2) if m else None

def parse_bytes(s):
    m = re.match(r"\s*([0-9.]+)\s*([KMGTP]i?B|B)\s*$", str(s or ""), re.IGNORECASE)
    if not m:
        return None
    mult = BYTE_MULT.get(m.group(2).lower())
    return int(float(m.group(1)) * mult) if mult else None

def emit(payload):
    finish(payload)
    print("## 容器运行时巡检（%s）\n\n- %s\n\n```json\n%s\n```" % (
        payload.get("runtime") or "未探测到", payload["summary"],
        json.dumps(payload, ensure_ascii=False, indent=2)))

collected_at = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
present = [c for c in RUNTIMES if shutil.which(c)]
runtime = None
for cand in present:
    rc, _, _ = run([cand, "info"], timeout=8)
    if rc == 0:
        runtime = cand
        break

if not runtime:
    detail = "未安装运行时 CLI" if not present else "已安装 %s，但 info 探测未通过" % ",".join(present)
    emit({
        "status": "warning",
        "summary": "未检测到可用的 Docker / nerdctl / Podman 运行时守护进程，容器指标未采集",
        "collected_at": collected_at,
        "runtime": None,
        "runtime_candidates": present,
        "metrics": [],
        "charts": [],
        "checks": [
            {"id": "container.runtime", "name": "运行时守护进程", "category": "runtime",
             "status": "warning", "value": "unavailable", "detail": detail},
        ],
        "containers": [],
    })
    raise SystemExit(0)

rc, out, err = run([runtime, "stats", "--no-stream", "--format", "{{json .}}"], timeout=25)
stats_ok = rc == 0
stats_detail = "统计命令返回码 %d" % rc
if err:
    stats_detail += "；stderr: %s" % err.replace("\n", " ")[:200]

containers = []
unparsed = 0
if stats_ok:
    for line in out.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            row = json.loads(line)
        except Exception:
            unparsed += 1
            continue
        cpu = parse_pct(row.get("CPUPerc"))
        mem_pct = parse_pct(row.get("MemPerc"))
        used_bytes = limit_bytes = None
        mu = str(row.get("MemUsage") or "")
        if "/" in mu:
            left, right = mu.split("/", 1)
            used_bytes = parse_bytes(left)
            limit_bytes = parse_bytes(right)
        if mem_pct is None and used_bytes and limit_bytes:
            mem_pct = round(used_bytes * 100.0 / limit_bytes, 2)
        if mem_pct is None:
            st = "warning"
            unparsed += 1
        elif mem_pct >= MEM_CRIT:
            st = "critical"
        elif mem_pct >= MEM_WARN:
            st = "warning"
        else:
            st = "pass"
        containers.append({
            "name": str(row.get("Name") or row.get("ID") or "").lstrip("/"),
            "cpu_percent": cpu,
            "mem_percent": mem_pct,
            "mem_usage": mu,
            "mem_limit_bytes": limit_bytes,
            "pids": str(row.get("PIDs") or ""),
            "status": st,
        })

parsed = [c for c in containers if c["mem_percent"] is not None]
max_mem = max((c["mem_percent"] for c in parsed), default=None)
high_mem = [c for c in parsed if c["mem_percent"] >= MEM_WARN]
crit_mem = [c for c in parsed if c["mem_percent"] >= MEM_CRIT]

if not stats_ok:
    overall = "warning"
    summary = "运行时 %s：容器统计不可用，未能获取容器数量与内存水位（%s）" % (runtime, stats_detail)
elif crit_mem:
    overall = "critical"
    summary = "运行时 %s：共 %d 个运行中容器，%d 个内存占比达到 %.0f%% 红线，最高 %s%%" % (
        runtime, len(containers), len(crit_mem), MEM_CRIT, max_mem)
elif high_mem or unparsed:
    overall = "warning"
    summary = "运行时 %s：共 %d 个运行中容器，最高内存占比 %s%%%s%s" % (
        runtime, len(containers), max_mem,
        "（%d 个高于 %.0f%%）" % (len(high_mem), MEM_WARN) if high_mem else "",
        "（%d 条统计记录无法解析）" % unparsed if unparsed else "")
else:
    overall = "pass"
    summary = "运行时 %s：共 %d 个运行中容器，内存水位正常（最高 %s%%）" % (runtime, len(containers), max_mem)

metrics = []
if stats_ok:
    metrics.append({"key": "containers_running", "label": "运行中容器数", "value": len(containers),
                    "unit": "", "status": "pass" if not unparsed else "warning"})
    if max_mem is not None:
        metrics.append({"key": "container_max_mem_percent", "label": "最高容器内存占比",
                        "value": max_mem, "unit": "%", "status": overall})

checks = [
    {"id": "container.runtime", "name": "运行时守护进程", "category": "runtime",
     "status": "pass", "value": runtime, "detail": "%s 守护进程 info 探测响应正常" % runtime},
    {"id": "container.stats", "name": "容器统计采集", "category": "runtime",
     "status": "pass" if stats_ok else "warning",
     "value": "ok" if stats_ok else "failed",
     "detail": "docker/nerdctl/podman stats --no-stream 采集成功" if stats_ok else stats_detail},
    {"id": "container.memory_limit", "name": "容器内存上限水位", "category": "resource",
     "status": overall,
     "value": ("最高 %s%%" % max_mem) if max_mem is not None else "不可得",
     "detail": "%s；内存占比口径：优先采用运行时 MemPerc，缺失时按 MemUsage 的 used/limit 计算" % summary},
]

emit({
    "status": overall,
    "summary": summary,
    "collected_at": collected_at,
    "runtime": runtime,
    "metrics": metrics,
    "charts": [{
        "title": "容器内存占比 Top 8",
        "type": "bar",
        "unit": "%",
        "max": 100,
        "series": [{"label": c["name"][:24], "value": c["mem_percent"], "status": c["status"]}
                   for c in sorted(parsed, key=lambda x: x["mem_percent"], reverse=True)[:8]],
    }] if parsed else [],
    "checks": checks,
    "containers": containers,
})
PY
