#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  printf '```json\n{"status":"skipped","summary":"未检测到 python3，无法解析容器运行时指标","metrics":[],"checks":[{"id":"container.runtime","name":"容器运行时","category":"runtime","status":"skipped","value":"no_python3","detail":"节点缺少 python3"}]}\n```\n'
  exit 0
fi

python3 - <<'PY'
import json
import re
import shutil
import subprocess
from datetime import datetime, timezone

def run(cmd, timeout=15):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=timeout)
        return r.returncode, r.stdout.strip(), r.stderr.strip()
    except Exception as e:
        return 1, "", str(e)

def parse_pct(s):
    m = re.search(r"([0-9]+(?:\.[0-9]+)?)", str(s or ""))
    return round(float(m.group(1)), 2) if m else 0.0

runtime = None
for cand in ("docker", "nerdctl", "podman"):
    if shutil.which(cand):
        rc, _, _ = run([cand, "info"], timeout=5)
        if rc == 0:
            runtime = cand
            break

if not runtime:
    payload = {
        "status": "skipped",
        "summary": "当前节点未检测到可用的 Docker / nerdctl / Podman 运行时守护进程",
        "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "metrics": [{"key": "containers_running", "label": "运行中容器数", "value": 0, "unit": "", "status": "skipped"}],
        "checks": [{"id": "container.runtime", "name": "容器运行时探测", "category": "runtime", "status": "skipped", "value": "unavailable", "detail": "未安装或未挂载运行时 Socket"}],
        "containers": [],
    }
    print("## 容器运行时巡检\n\n未检测到可用容器运行时守护进程。\n\n```json\n" + json.dumps(payload, ensure_ascii=False, indent=2) + "\n```")
    raise SystemExit(0)

rc, out, err = run([runtime, "stats", "--no-stream", "--format", "{{json .}}"], timeout=20)
containers = []
if rc == 0 and out:
    for line in out.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            row = json.loads(line)
        except Exception:
            continue
        cpu = parse_pct(row.get("CPUPerc"))
        mem_pct = parse_pct(row.get("MemPerc"))
        st = "critical" if mem_pct >= 92 else ("warning" if mem_pct >= 80 else "pass")
        containers.append({
            "name": str(row.get("Name") or row.get("ID") or "").lstrip("/"),
            "cpu_percent": cpu,
            "mem_usage": str(row.get("MemUsage") or ""),
            "mem_percent": mem_pct,
            "pids": str(row.get("PIDs") or ""),
            "status": st,
        })

max_mem = max((c["mem_percent"] for c in containers), default=0.0)
high_mem = [c for c in containers if c["mem_percent"] >= 80.0]
overall = "critical" if any(c["status"] == "critical" for c in containers) else ("warning" if high_mem else "pass")
summary = f"运行时 {runtime}：共 {len(containers)} 个运行中容器，最高内存占比 {max_mem}%" + (f"（{len(high_mem)} 个高于 80%）" if high_mem else "")

payload = {
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "runtime": runtime,
    "metrics": [
        {"key": "containers_running", "label": "运行中容器数", "value": len(containers), "unit": "", "status": "pass"},
        {"key": "container_max_mem_percent", "label": "最高容器内存占比", "value": max_mem, "unit": "%", "status": overall},
    ],
    "charts": [
        {
            "title": "容器内存占比 Top 8",
            "type": "bar",
            "unit": "%",
            "max": 100,
            "series": [{"label": c["name"][:24], "value": c["mem_percent"], "status": c["status"]} for c in sorted(containers, key=lambda x: x["mem_percent"], reverse=True)[:8]],
        }
    ] if containers else [],
    "checks": [
        {"id": "container.runtime", "name": "运行时守护进程", "category": "runtime", "status": "pass", "value": runtime, "detail": f"{runtime} 守护进程响应正常"},
        {"id": "container.memory_limit", "name": "容器内存上限水位", "category": "resource", "status": overall, "value": f"最高 {max_mem}%", "detail": summary},
    ],
    "containers": containers,
}
print(f"## 容器运行时巡检（{runtime}）\n\n- {summary}\n\n```json\n{json.dumps(payload, ensure_ascii=False, indent=2)}\n```")
PY