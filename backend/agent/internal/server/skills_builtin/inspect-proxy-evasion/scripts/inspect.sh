#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  printf '```json\n{"status":"warning","summary":"缺少 python3","metrics":[],"checks":[{"id":"proxy.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"节点缺少 python3"}]}\n```\n'
  exit 0
fi

python3 - <<'PY'
import json
import os
import re
import shutil
import subprocess
from datetime import datetime, timezone

SUSPECT_NAMES = ("clash", "mihomo", "v2ray", "xray", "sing-box", "frpc", "frps", "nps", "npc", "trojan", "ss-local", "shadowsocks", "ngrok")
PROXY_PORTS = {"1080", "3128", "7890", "7891", "8118", "10808", "20170", "20171"}

def run(cmd):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, timeout=6)
        return r.stdout if r.returncode == 0 else ""
    except Exception:
        return ""

suspect_procs = []
my_pid = str(os.getpid())
for line in run(["ps", "-eo", "pid=,comm=,args="]).splitlines():
    parts = line.strip().split(None, 2)
    if len(parts) < 2:
        continue
    pid, comm = parts[0], parts[1]
    args = parts[2] if len(parts) > 2 else comm
    if pid == my_pid or "inspect.sh" in args:
        continue
    low = (comm + " " + args).lower()
    for kw in SUSPECT_NAMES:
        if re.search(rf"(^|[/\s_-]){re.escape(kw)}($|[/\s_.-])", low):
            suspect_procs.append({"pid": pid, "comm": comm, "matched": kw, "status": "critical"})
            break

open_proxy_ports = []
port_out = run(["ss", "-tlnH"]) if shutil.which("ss") else run(["netstat", "-tln"])
for line in port_out.splitlines():
    for m in re.findall(r":(\d+)\b", line):
        if m in PROXY_PORTS and m not in [x["port"] for x in open_proxy_ports]:
            open_proxy_ports.append({"port": m, "status": "warning"})

proxy_envs = []
for k in ("http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"):
    v = os.environ.get(k, "").strip()
    if v:
        redacted = re.sub(r"://[^/@]+@", "://***@", v)
        proxy_envs.append({"env": k, "value": redacted})

proc_st = "critical" if suspect_procs else "pass"
port_st = "warning" if open_proxy_ports else "pass"
env_st = "warning" if proxy_envs else "pass"
overall = "critical" if proc_st == "critical" else ("warning" if "warning" in (port_st, env_st) else "pass")
summary = (
    f"发现 {len(suspect_procs)} 个疑似代理进程、{len(open_proxy_ports)} 个常见代理监听端口、{len(proxy_envs)} 个代理环境变量"
    if (suspect_procs or open_proxy_ports or proxy_envs)
    else "未发现违规代理进程、代理端口监听或代理环境变量"
)

payload = {
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "metrics": [
        {"key": "proxy_suspect_procs", "label": "疑似代理进程数", "value": len(suspect_procs), "unit": "", "status": proc_st},
        {"key": "proxy_open_ports", "label": "常见代理监听端口数", "value": len(open_proxy_ports), "unit": "", "status": port_st},
        {"key": "proxy_env_count", "label": "代理环境变量数", "value": len(proxy_envs), "unit": "", "status": env_st},
    ],
    "checks": [
        {"id": "proxy.processes", "name": "疑似代理/穿透进程", "category": "security", "status": proc_st, "value": f"{len(suspect_procs)} 个", "detail": "无命中" if not suspect_procs else ", ".join(f"{x['comm']}(pid={x['pid']})" for x in suspect_procs[:8])},
        {"id": "proxy.ports", "name": "常见代理监听端口", "category": "security", "status": port_st, "value": f"{len(open_proxy_ports)} 个", "detail": "无监听" if not open_proxy_ports else "开放端口：" + ", ".join(x["port"] for x in open_proxy_ports)},
        {"id": "proxy.env", "name": "代理环境变量", "category": "security", "status": env_st, "value": f"{len(proxy_envs)} 个", "detail": "未设置" if not proxy_envs else ", ".join(x["env"] for x in proxy_envs)},
    ],
    "suspect_processes": suspect_procs,
    "proxy_ports": open_proxy_ports,
    "proxy_envs": proxy_envs,
}
print(f"## 主机违规代理与内网穿透巡检\n\n- {summary}\n\n```json\n{json.dumps(payload, ensure_ascii=False, indent=2)}\n```")
PY