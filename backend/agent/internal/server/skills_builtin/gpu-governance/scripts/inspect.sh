#!/usr/bin/env sh
set -eu
tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## GPU 算力治理巡检

- **综合结论**：warning — 缺少 python3，未能执行 GPU 采集

```json
MD
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少 python3，未能执行 GPU 算力采集","metrics":[],"checks":[{"id":"gpu.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"目标环境未检测到 python3"}]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")"
  cat <<'MD'
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import shutil
import subprocess
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024
HARD_CAP_BYTES = 256 * 1024

def _env_float(name, default, lo, hi):
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    try:
        v = float(raw)
        return max(lo, min(hi, v))
    except ValueError:
        return default

def _env_bool(name, default):
    raw = os.environ.get(name, "").strip().lower()
    if not raw:
        return default
    return raw in ("1", "true", "yes", "on")

WARN_TEMP = _env_float("DOOPS_INPUT_WARN_TEMP_C", 85.0, 50.0, 105.0)
CRIT_TEMP = min(112.0, WARN_TEMP + 7.0)
WARN_MEM = _env_float("DOOPS_INPUT_WARN_MEM_PCT", 90.0, 50.0, 99.0)
CRIT_MEM = max(WARN_MEM + 2.0, 98.0)
INCLUDE_PROCS = _env_bool("DOOPS_INPUT_INCLUDE_PROCESSES", True)

def target_identity():
    return {
        "cluster": os.environ.get("DOOPS_TARGET_CLUSTER") or None,
        "instance": os.environ.get("DOOPS_TARGET_INSTANCE") or None,
    }

def run_cmd(cmd, timeout=8):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", errors="replace", timeout=timeout)
        return r.returncode, r.stdout or ""
    except Exception:
        return 1, ""

def find_nvidia_prefix():
    if shutil.which("nvidia-smi"):
        return ["nvidia-smi"], "local"
    if shutil.which("nsenter") and os.path.isdir("/proc/1/ns"):
        rc, _ = run_cmd(["nsenter", "-t", "1", "-m", "--", "nvidia-smi", "-L"], timeout=5)
        if rc == 0:
            return ["nsenter", "-t", "1", "-m", "--", "nvidia-smi"], "host_nsenter"
    return None, "none"

def parse_float(val):
    s = (val or "").strip()
    if not s or s.upper() in ("N/A", "[N/A]", "[NOT SUPPORTED]", "ERR!"):
        return None
    try:
        return float(s)
    except ValueError:
        return None

def parse_int(val):
    f = parse_float(val)
    return int(round(f)) if f is not None else None

def emit(intro, payload):
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if len(text.encode("utf-8")) > MAX_PAYLOAD_BYTES:
        payload["gpu_processes"] = (payload.get("gpu_processes") or [])[:20]
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    if len(text.encode("utf-8")) > HARD_CAP_BYTES:
        payload["gpus"] = (payload.get("gpus") or [])[:16]
        payload["gpu_processes"] = []
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    print("\n".join(intro) + "\n\n```json\n" + text + "\n```")

prefix, probe_mode = find_nvidia_prefix()
gpus = []
gpu_procs = []
probe_error = ""

if prefix:
    query = "index,name,uuid,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,ecc.errors.uncorrected.volatile.total"
    rc, out = run_cmd(prefix + [f"--query-gpu={query}", "--format=csv,noheader,nounits"])
    if rc != 0:
        # Older drivers may not support ecc field; fall back cleanly to 8 core fields.
        query8 = "index,name,uuid,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw"
        rc, out = run_cmd(prefix + [f"--query-gpu={query8}", "--format=csv,noheader,nounits"])
    if rc != 0:
        probe_error = f"nvidia-smi 执行失败（rc={rc}）"
    else:
        for line in out.splitlines():
            cols = [c.strip() for c in line.split(",")]
            if len(cols) < 7:
                continue
            util = parse_float(cols[3])
            mem_used = parse_float(cols[4])
            mem_total = parse_float(cols[5])
            mem_pct = round((mem_used / mem_total) * 100.0, 2) if (mem_used is not None and mem_total and mem_total > 0) else None
            temp = parse_float(cols[6])
            power = parse_float(cols[7]) if len(cols) > 7 else None
            ecc_unc = parse_int(cols[8]) if len(cols) > 8 else 0
            gpus.append({
                "index": cols[0],
                "name": cols[1],
                "uuid": cols[2],
                "util_percent": util,
                "mem_used_mib": mem_used,
                "mem_total_mib": mem_total,
                "mem_percent": mem_pct,
                "temp_c": temp,
                "power_w": power,
                "ecc_uncorrected": ecc_unc or 0,
            })
    if INCLUDE_PROCS and not probe_error and gpus:
        prc, pout = run_cmd(prefix + ["--query-compute-apps=gpu_uuid,pid,process_name,used_gpu_memory", "--format=csv,noheader,nounits"])
        if prc == 0:
            for line in pout.splitlines():
                cols = [c.strip() for c in line.split(",")]
                if len(cols) < 4:
                    continue
                comm = os.path.basename(cols[2]) if cols[2] else "unknown"
                gpu_procs.append({
                    "gpu_uuid": cols[0],
                    "pid": cols[1],
                    "process": comm,
                    "used_mib": parse_float(cols[3]),
                })

checks = []
metrics = []
charts = []

if probe_error:
    overall = "warning"
    summary = f"GPU 采集异常：{probe_error}"
    checks.append({"id": "gpu.probe", "name": "GPU 驱动与工具探测", "category": "infrastructure", "status": "warning", "value": "采集异常", "detail": probe_error})
elif not gpus:
    overall = "pass"
    summary = "当前节点未检测到 NVIDIA GPU（无异常，零误报）"
    metrics.append({"key": "gpu_count", "label": "GPU 卡数", "value": 0, "unit": "张", "status": "pass"})
    checks.append({"id": "gpu.presence", "name": "GPU 设备检测", "category": "infrastructure", "status": "pass", "value": "0 张", "detail": "节点未挂载 NVIDIA GPU 或未安装驱动，跳过算力阈值检查"})
else:
    utils = [g["util_percent"] for g in gpus if g["util_percent"] is not None]
    mems = [g["mem_percent"] for g in gpus if g["mem_percent"] is not None]
    temps = [g["temp_c"] for g in gpus if g["temp_c"] is not None]
    ecc_total = sum(int(g.get("ecc_uncorrected") or 0) for g in gpus)

    util_avg = round(sum(utils) / len(utils), 2) if utils else 0.0
    util_max = round(max(utils), 2) if utils else 0.0
    mem_max = round(max(mems), 2) if mems else 0.0
    temp_max = round(max(temps), 1) if temps else 0.0

    mem_st = "critical" if mem_max >= CRIT_MEM else ("warning" if mem_max >= WARN_MEM else "pass")
    temp_st = "critical" if temp_max >= CRIT_TEMP else ("warning" if temp_max >= WARN_TEMP else "pass")
    ecc_st = "critical" if ecc_total > 0 else "pass"

    statuses = [mem_st, temp_st, ecc_st]
    overall = "critical" if "critical" in statuses else ("warning" if "warning" in statuses else "pass")
    summary = f"检出 {len(gpus)} 张 GPU：最高算力 {util_max:.1f}%、最高显存 {mem_max:.1f}%、最高卡温 {temp_max:.1f}°C、活跃占卡进程 {len(gpu_procs)} 个"

    metrics = [
        {"key": "gpu_count", "label": "GPU 卡数", "value": len(gpus), "unit": "张", "status": "pass"},
        {"key": "gpu_util_avg_pct", "label": "平均利用率", "value": util_avg, "unit": "%", "status": "pass"},
        {"key": "gpu_util_max_pct", "label": "最高利用率", "value": util_max, "unit": "%", "status": "pass"},
        {"key": "gpu_mem_max_pct", "label": "最高显存占比", "value": mem_max, "unit": "%", "status": mem_st},
        {"key": "gpu_temp_max_c", "label": "最高温度", "value": temp_max, "unit": "°C", "status": temp_st},
        {"key": "gpu_ecc_uncorrected", "label": "ECC 未纠正错误", "value": ecc_total, "unit": "次", "status": ecc_st},
        {"key": "gpu_process_count", "label": "活跃占卡进程", "value": len(gpu_procs), "unit": "个", "status": "pass"},
    ]
    checks = [
        {"id": "gpu.memory", "name": "GPU 显存水位", "category": "infrastructure", "status": mem_st, "value": f"{mem_max:.1f}%", "threshold": f"告警 {WARN_MEM:.0f}% / 严重 {CRIT_MEM:.0f}%", "detail": f"共 {len(gpus)} 张卡，最高显存占比 {mem_max:.1f}%"},
        {"id": "gpu.temperature", "name": "GPU 温度监控", "category": "infrastructure", "status": temp_st, "value": f"{temp_max:.1f}°C", "threshold": f"告警 {WARN_TEMP:.0f}°C / 严重 {CRIT_TEMP:.0f}°C", "detail": f"最高核心温度 {temp_max:.1f}°C（探测模式：{probe_mode}）"},
        {"id": "gpu.ecc", "name": "GPU ECC 硬件健康", "category": "infrastructure", "status": ecc_st, "value": f"{ecc_total} 次", "threshold": "0 次", "detail": "未检出未纠正 ECC 错误" if ecc_total == 0 else f"发现 {ecc_total} 次未纠正 ECC 硬件错误，建议隔离排障"},
    ]
    charts = [
        {
            "title": "各 GPU 卡利用率与显存占用 (%)",
            "type": "bar",
            "unit": "%",
            "max": 100,
            "series": [
                {"label": f"GPU{g['index']} 利用率", "value": g["util_percent"] or 0.0, "status": "pass"}
                for g in gpus
            ] + [
                {"label": f"GPU{g['index']} 显存", "value": g["mem_percent"] or 0.0, "status": mem_st}
                for g in gpus
            ],
        }
    ]

payload = {
    "target": target_identity(),
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "applied_inputs": {
        "WARN_TEMP_C": WARN_TEMP,
        "WARN_MEM_PCT": WARN_MEM,
        "INCLUDE_PROCESSES": INCLUDE_PROCS,
    },
    "metrics": metrics,
    "checks": checks,
    "charts": charts,
    "gpus": gpus,
    "gpu_processes": gpu_procs[:50],
}

intro = [
    "## GPU 算力治理巡检报告",
    "",
    f"- **综合状态**：`{overall}`",
    f"- **结论摘要**：{summary}",
    f"- **阈值参数**：温度告警 `{WARN_TEMP:.0f}°C` · 显存告警 `{WARN_MEM:.0f}%` · 占卡进程采集 `{'开启' if INCLUDE_PROCS else '关闭'}`",
]
emit(intro, payload)
PY
