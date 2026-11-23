#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  printf '```json\n{"status":"warning","summary":"缺少 python3","metrics":[],"checks":[{"id":"app.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"节点缺少 python3"}]}\n```\n'
  exit 0
fi

python3 - <<'PY'
import json
import os
import shutil
import socket
import ssl
import subprocess
import time
import urllib.request
from datetime import datetime, timezone
from urllib.parse import urlparse

def run(cmd, timeout=8):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=timeout)
        return r.returncode, r.stdout.strip()
    except Exception as e:
        return 1, str(e)

listeners = []
if shutil.which("ss"):
    rc, out = run(["ss", "-tlnH"])
    if rc == 0:
        for line in out.splitlines():
            cols = line.split()
            if len(cols) >= 4:
                listeners.append({"state": cols[0], "local_address": cols[3]})
elif shutil.which("netstat"):
    rc, out = run(["netstat", "-tln"])
    if rc == 0:
        for line in out.splitlines():
            if "LISTEN" in line:
                cols = line.split()
                if len(cols) >= 4:
                    listeners.append({"state": "LISTEN", "local_address": cols[3]})

checks = [
    {"id": "app.listeners", "name": "本机 TCP 监听服务", "category": "availability", "status": "pass", "value": f"{len(listeners)} 个端口", "detail": f"共发现 {len(listeners)} 个监听地址"}
]
metrics = [
    {"key": "tcp_listen_ports", "label": "TCP 监听端口数", "value": len(listeners), "unit": "", "status": "pass"}
]

if shutil.which("nginx"):
    rc, out = run(["nginx", "-t"])
    nst = "pass" if rc == 0 else "critical"
    checks.append({"id": "app.nginx_config", "name": "Nginx 配置语法", "category": "config", "status": nst, "value": "valid" if rc == 0 else "invalid", "detail": out[:200] or "nginx -t 完成"})

urls = [u.strip() for u in os.environ.get("DOOPS_INSPECT_URLS", "").split(",") if u.strip()]
endpoints = []
for u in urls[:12]:
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
        err = str(e)[:160]
    st = "critical" if (err or code >= 500) else ("warning" if (code >= 400 or (cert_days is not None and cert_days < 14)) else "pass")
    endpoints.append({"url": u, "status_code": code, "latency_ms": lat, "cert_days_left": cert_days, "security_headers": sec_headers, "status": st, "error": err})
    checks.append({"id": f"app.http.{len(endpoints)}", "name": f"HTTP 探活 {u}", "category": "availability", "status": st, "value": f"HTTP {code} · {lat}ms" if code else "失败", "detail": err or f"安全头：{', '.join(sec_headers) or '未配置'}"})

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
print(f"## 应用服务与 Web 巡检\n\n- {summary}\n\n```json\n{json.dumps(payload, ensure_ascii=False, indent=2)}\n```")
PY