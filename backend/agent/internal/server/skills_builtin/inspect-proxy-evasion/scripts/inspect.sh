#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 主机违规代理与内网穿透巡检

- **综合结论**：warning — 缺少 python3，未能采集

```json
{"status":"warning","summary":"缺少 python3，未采集代理与穿透线索","metrics":[],"checks":[{"id":"proxy.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3，进程与端口线索均未采集"}]}
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import re
import shutil
import subprocess
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024  # spec L28 单条产物硬上限 256 KiB，预留保护余量
HARD_CAP_BYTES = 256 * 1024  # spec L28 单条产物硬上限（整条 stdout，含 Markdown 正文）
TRIM_LIST_KEYS = ("suspect_processes", "proxy_ports", "proxy_envs", "checks")

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


def emit(intro, payload):
    """打印 Markdown 正文与 JSON 代码块；整条产物受 HARD_CAP_BYTES 约束（正文超长时截断，JSON 始终完整合法）。"""
    json_text = json.dumps(payload, ensure_ascii=False, indent=2)
    if len(json_text.encode("utf-8")) > HARD_CAP_BYTES:
        json_text = json.dumps(payload, ensure_ascii=False)
    marker = "...(正文超长已截断)"
    budget = HARD_CAP_BYTES - len(json_text.encode("utf-8")) - 14
    kept, used = [], 0
    for line in intro:
        cost = len(line.encode("utf-8")) + (1 if kept else 0)
        if used + cost > budget:
            break
        kept.append(line)
        used += cost
    if len(kept) < len(intro):
        if used + len(marker.encode("utf-8")) + 1 <= budget:
            kept.append(marker)
        elif len(marker.encode("utf-8")) <= budget:
            kept = [marker]
        else:
            kept = []
    body = "\n".join(kept)
    print(body + ("\n\n" if body else "") + "```json\n" + json_text + "\n```")

SUSPECT_NAMES = ("clash", "mihomo", "v2ray", "xray", "sing-box", "frpc", "frps", "nps", "npc", "trojan", "ss-local", "shadowsocks", "ngrok")
# 端口集合与 spec 2.4 严格一致（1080/7890/7891/8118/10808/20170/3128）
PROXY_PORTS = {"1080", "3128", "7890", "7891", "8118", "10808", "20170"}

def run(cmd):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", errors="replace", timeout=6)
        return r.stdout if r.returncode == 0 else ""
    except Exception:
        return ""

def run_rc(cmd):
    """返回 (returncode, stdout)；失败时 rc != 0，调用方必须显式报缺口而非静默当 0。"""
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", errors="replace", timeout=6)
        return r.returncode, r.stdout or ""
    except Exception:
        return 1, ""

suspect_procs = []
my_pid = str(os.getpid())
ps_rc, ps_out = run_rc(["ps", "-eo", "pid=,comm=,args="])
ps_note = "" if ps_rc == 0 else ("ps 执行失败（rc=%d），进程线索未采集" % ps_rc)
for line in ps_out.splitlines():
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
if shutil.which("ss"):
    port_tool = "ss"
    port_rc, port_out = run_rc(["ss", "-tlnH"])
elif shutil.which("netstat"):
    port_tool = "netstat"
    port_rc, port_out = run_rc(["netstat", "-tln"])
else:
    port_tool = None
    port_rc, port_out = 1, ""
if not port_tool:
    port_note = "未找到 ss 或 netstat，监听端口线索未采集"
elif port_rc != 0:
    port_note = "%s 执行失败（rc=%d），监听端口线索未采集" % (port_tool, port_rc)
else:
    port_note = ""
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

proc_st = "critical" if suspect_procs else ("warning" if ps_note else "pass")
port_st = "warning" if (open_proxy_ports or port_note) else "pass"
env_st = "warning" if proxy_envs else "pass"
overall = "critical" if proc_st == "critical" else ("warning" if "warning" in (proc_st, port_st, env_st) else "pass")
summary = (
    f"发现 {len(suspect_procs)} 个疑似代理进程、{len(open_proxy_ports)} 个常见代理监听端口、{len(proxy_envs)} 个代理环境变量"
    if (suspect_procs or open_proxy_ports or proxy_envs)
    else "未发现违规代理进程、代理端口监听或代理环境变量"
)
_gaps = [x for x in (ps_note, port_note) if x]
if _gaps:
    summary += "；采集缺口：" + "；".join(_gaps)

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
        {"id": "proxy.processes", "name": "疑似代理/穿透进程", "category": "security", "status": proc_st, "value": f"{len(suspect_procs)} 个" if not ps_note else "不可得", "detail": ps_note or ("无命中" if not suspect_procs else ", ".join(f"{x['comm']}(pid={x['pid']})" for x in suspect_procs[:8]))},
        {"id": "proxy.ports", "name": "常见代理监听端口", "category": "security", "status": port_st, "value": f"{len(open_proxy_ports)} 个" if not port_note else "不可得", "detail": port_note or ("无监听" if not open_proxy_ports else "开放端口：" + ", ".join(x["port"] for x in open_proxy_ports))},
        {"id": "proxy.env", "name": "代理环境变量", "category": "security", "status": env_st, "value": f"{len(proxy_envs)} 个", "detail": "未设置" if not proxy_envs else ", ".join(x["env"] for x in proxy_envs)},
    ],
    "suspect_processes": suspect_procs,
    "proxy_ports": open_proxy_ports,
    "proxy_envs": proxy_envs,
}
finish(payload)
emit(["## 主机违规代理与内网穿透巡检", "", "- %s" % summary], payload)
PY
