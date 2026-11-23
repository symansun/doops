#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 应用服务与 Web 巡检

- **综合结论**：warning — 缺少 python3，未能采集

```json
{"status":"warning","summary":"缺少 python3，未采集应用与 Web 指标","metrics":[],"checks":[{"id":"app.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3，监听端口与 HTTP 探活均未执行"}]}
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import re
import shutil
import socket
import ssl
import subprocess
import time
import urllib.request
from datetime import datetime, timezone
from urllib.parse import urlparse

MAX_PAYLOAD_BYTES = 200 * 1024  # spec L28 单条产物硬上限 256 KiB，预留保护余量
TRIM_LIST_KEYS = ("listeners", "endpoints", "checks")

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

def run(cmd, timeout=8):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=timeout)
        return r.returncode, r.stdout.strip()
    except Exception as e:
        return 1, str(e)

listeners = []
listener_tool = "ss" if shutil.which("ss") else ("netstat" if shutil.which("netstat") else None)
listener_note = ""
if listener_tool == "ss":
    rc, out = run(["ss", "-tlnH"])
    if rc == 0:
        for line in out.splitlines():
            cols = line.split()
            if len(cols) >= 4:
                listeners.append({"state": cols[0], "local_address": cols[3]})
    else:
        listener_note = "ss 执行失败：%s" % out[:160]
elif listener_tool == "netstat":
    rc, out = run(["netstat", "-tln"])
    if rc == 0:
        for line in out.splitlines():
            if "LISTEN" in line:
                cols = line.split()
                if len(cols) >= 4:
                    listeners.append({"state": "LISTEN", "local_address": cols[3]})
    else:
        listener_note = "netstat 执行失败：%s" % out[:160]
else:
    listener_note = "未找到 ss 或 netstat，无法枚举本机 TCP 监听端口"

listener_st = "pass" if (listener_tool and not listener_note) else "warning"
checks = [
    {"id": "app.listeners", "name": "本机 TCP 监听服务", "category": "availability", "status": listener_st,
     "value": ("%d 个端口" % len(listeners)) if listener_st == "pass" else "不可得",
     "detail": ("共发现 %d 个监听地址（工具 %s）" % (len(listeners), listener_tool)) if not listener_note else listener_note}
]
metrics = []
if listener_st == "pass":
    metrics.append({"key": "tcp_listen_ports", "label": "TCP 监听端口数", "value": len(listeners), "unit": "", "status": "pass"})

if shutil.which("nginx"):
    rc, out = run(["nginx", "-t"])
    nst = "pass" if rc == 0 else "critical"
    checks.append({"id": "app.nginx_config", "name": "Nginx 配置语法", "category": "config", "status": nst, "value": "valid" if rc == 0 else "invalid", "detail": out[:200] or "nginx -t 完成"})

URL_CRED = re.compile(r"://[^/@]*@")

def redact_url(value):
    return URL_CRED.sub("://", str(value or ""))

urls = [u.strip() for u in os.environ.get("DOOPS_INSPECT_URLS", "").split(",") if u.strip()]
endpoints = []
for u in urls[:12]:
    safe_u = redact_url(u)
    t0 = time.time()
    code, lat, err, cert_days = 0, 0.0, "", None
    sec_headers = []
    try:
        req = urllib.request.Request(u, headers={"User-Agent": "DoOps-Inspect/1.0"})
        with urllib.request.urlopen(req, timeout=8) as resp:
            code = resp.status
            lat = round((time.time() - t0) * 1000.0, 1)
            for h in ("Strict-Transport-Security", "X-Content-Type-Options"):
                if resp.headers.get(h):
                    sec_headers.append(h)
        parsed = urlparse(u)
        if parsed.scheme == "https" and parsed.hostname:
            ctx = ssl.create_default_context()
            with socket.create_connection((parsed.hostname, parsed.port or 443), timeout=5) as sock:
                with ctx.wrap_socket(sock, server_hostname=parsed.hostname) as ssock:
                    cert = ssock.getpeercert()
                    not_after = cert.get("notAfter")
                    if not_after:
                        exp = datetime.strptime(not_after, "%b %d %H:%M:%S %Y %Z").replace(tzinfo=timezone.utc)
                        cert_days = round((exp - datetime.now(timezone.utc)).total_seconds() / 86400.0, 1)
    except Exception as e:
        lat = round((time.time() - t0) * 1000.0, 1)
        err = redact_url(str(e))[:160]
    st = "critical" if (err or code >= 500) else ("warning" if (code >= 400 or (cert_days is not None and cert_days < 14)) else "pass")
    endpoints.append({"url": safe_u, "status_code": code, "latency_ms": lat, "cert_days_left": cert_days, "security_headers": sec_headers, "status": st, "error": err})
    checks.append({"id": f"app.http.{len(endpoints)}", "name": f"HTTP 探活 {safe_u}", "category": "availability", "status": st, "value": f"HTTP {code} · {lat}ms" if code else "失败", "detail": err or f"安全头：{', '.join(sec_headers) or '未配置'}"})

overall = "critical" if any(c["status"] == "critical" for c in checks) else ("warning" if any(c["status"] == "warning" for c in checks) else "pass")
summary = f"监听 {len(listeners)} 个 TCP 端口" + (f"，探测 {len(endpoints)} 个 HTTP 端点" if endpoints else "")

payload = {
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "metrics": metrics,
    "checks": checks,
    "listeners": listeners[:40],
    "endpoints": endpoints,
}
finish(payload)
print(f"## 应用服务与 Web 巡检\n\n- {summary}\n\n```json\n{json.dumps(payload, ensure_ascii=False, indent=2)}\n```")
PY
