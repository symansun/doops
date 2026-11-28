#!/usr/bin/env sh
set -eu
tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 外部暴露面与安全基线扫描

- **综合结论**：warning — 缺少 python3，未能执行扫描

```json
MD
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少 python3，未执行暴露面扫描","metrics":[],"checks":[{"id":"surface.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"目标环境未检测到 python3"}],"coverage_gaps":[{"module":"http-scanner","reason":"python3 不可用","impact":"未执行任何暴露面扫描"}]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")"
  cat <<'MD'
```
MD
  exit 0
fi

python3 - <<'PY'
import hashlib
import json
import os
import re
import shutil
import ssl
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from urllib.parse import urljoin, urlparse

MAX_PAYLOAD_BYTES = 200 * 1024
HARD_CAP_BYTES = 256 * 1024
MAX_REQUEST_BUDGET = 140

def _env_bool(name, default):
    raw = os.environ.get(name, "").strip().lower()
    if not raw:
        return default
    return raw in ("1", "true", "yes", "on")

def _env_int(name, default, lo, hi):
    try:
        v = int(float(os.environ.get(name, "").strip() or default))
        return max(lo, min(hi, v))
    except Exception:
        return default

CHECK_HEADERS = _env_bool("DOOPS_INPUT_CHECK_SECURITY_HEADERS", True)
CHECK_PATHS = _env_bool("DOOPS_INPUT_CHECK_SENSITIVE_PATHS", True)
DETECT_LOGIN = _env_bool("DOOPS_INPUT_DETECT_LOGIN_SURFACES", True)
DELAY_MS = _env_int("DOOPS_INPUT_REQUEST_DELAY_MS", 400, 50, 3000)
MAX_URLS = _env_int("DOOPS_INPUT_MAX_URLS", 12, 1, 12)
RUN_OPTIONAL_NUCLEI = _env_bool("DOOPS_INPUT_RUN_OPTIONAL_NUCLEI", False)

URL_CRED_RE = re.compile(r"://[^/@]*@")

def redact_url(u):
    return URL_CRED_RE.sub("://", str(u or ""))

def target_identity():
    return {
        "cluster": os.environ.get("DOOPS_TARGET_CLUSTER") or None,
        "instance": os.environ.get("DOOPS_TARGET_INSTANCE") or None,
    }

SECURITY_HEADERS = (
    ("content-security-policy", "medium", "missing-csp-header", "缺少 Content-Security-Policy 响应头"),
    ("strict-transport-security", "medium", "missing-hsts-header", "HTTPS 站点缺少 Strict-Transport-Security 响应头"),
    ("x-frame-options", "low", "missing-security-header", "缺少 X-Frame-Options 防点击劫持头"),
    ("x-content-type-options", "low", "missing-security-header", "缺少 X-Content-Type-Options 响应头"),
    ("referrer-policy", "low", "missing-security-header", "缺少 Referrer-Policy 响应头"),
    ("permissions-policy", "low", "missing-security-header", "缺少 Permissions-Policy 响应头"),
)

# Each entry: (path, rule_name, body_regex, secret_sensitive, expects_html)
SENSITIVE_PATHS = (
    ("/.git/config", "git-config-exposure", re.compile(r"\[core\]|repositoryformatversion", re.I), True, False),
    ("/.git/HEAD", "git-head-exposure", re.compile(r"^\s*ref:\s*refs/", re.I), True, False),
    ("/.env", "dotenv-exposure", re.compile(r"(?m)^[A-Z][A-Z0-9_]{2,}\s*=.+", re.M), True, False),
    ("/swagger-ui.html", "swagger-ui-exposure", re.compile(r"swagger-ui|swaggerui", re.I), False, True),
    ("/swagger-ui/index.html", "swagger-ui-exposure", re.compile(r"swagger-ui|swaggerui", re.I), False, True),
    ("/v3/api-docs", "openapi-docs-exposure", re.compile(r'"openapi"\s*:|"paths"\s*:', re.I), False, False),
    ("/openapi.json", "openapi-docs-exposure", re.compile(r'"openapi"\s*:|"swagger"\s*:', re.I), False, False),
    ("/server-status", "apache-status-exposure", re.compile(r"apache server status|server uptime", re.I), False, True),
    ("/actuator/env", "spring-actuator-exposure", re.compile(r'"activeProfiles"|"propertySources"', re.I), True, False),
)

LOGIN_RE = re.compile(r"(登录|统一认证|sign\s*in|log\s*in|single\s*sign-?on|cas\s+central)", re.I)
TITLE_RE = re.compile(r"<title[^>]*>(.*?)</title>", re.I | re.S)
VERSION_BANNER_RE = re.compile(r"/\d+\.\d+")

class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    pass

def fetch_http(url, timeout=6.0):
    t0 = time.time()
    req = urllib.request.Request(url, headers={"User-Agent": "DoOps-SurfaceScan/1.0 (read-only low-speed)"})
    ctx = ssl.create_default_context()
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
            raw = resp.read(16384)
            headers = {k.lower(): v for k, v in resp.headers.items()}
            text = raw.decode("utf-8", errors="replace")
            return {
                "status": resp.status,
                "final_url": redact_url(resp.geturl()),
                "headers": headers,
                "body": text,
                "body_hash": hashlib.sha256(raw).hexdigest()[:16],
                "content_type": headers.get("content-type", ""),
                "latency_ms": round((time.time() - t0) * 1000.0, 1),
                "error": "",
            }
    except urllib.error.HTTPError as he:
        try:
            raw = he.read(4096)
        except Exception:
            raw = b""
        headers = {k.lower(): v for k, v in (he.headers.items() if he.headers else [])}
        return {
            "status": he.code,
            "final_url": redact_url(url),
            "headers": headers,
            "body": raw.decode("utf-8", errors="replace"),
            "body_hash": hashlib.sha256(raw).hexdigest()[:16],
            "content_type": headers.get("content-type", ""),
            "latency_ms": round((time.time() - t0) * 1000.0, 1),
            "error": "",
        }
    except Exception as e:
        return {
            "status": 0,
            "final_url": redact_url(url),
            "headers": {},
            "body": "",
            "body_hash": "",
            "content_type": "",
            "latency_ms": round((time.time() - t0) * 1000.0, 1),
            "error": redact_url(str(e))[:160],
        }

def emit(intro, payload):
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if len(text.encode("utf-8")) > MAX_PAYLOAD_BYTES:
        payload["findings"] = (payload.get("findings") or [])[:40]
        payload["resources"] = (payload.get("resources") or [])[:20]
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    if len(text.encode("utf-8")) > HARD_CAP_BYTES:
        payload["findings"] = (payload.get("findings") or [])[:15]
        payload["resources"] = (payload.get("resources") or [])[:10]
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    print("\n".join(intro) + "\n\n```json\n" + text + "\n```")

raw_urls = [u.strip() for u in os.environ.get("DOOPS_INSPECT_URLS", "").split(",") if u.strip()]
urls = []
for u in raw_urls:
    p = urlparse(u)
    if p.scheme in ("http", "https") and p.hostname and p.username is None and p.password is None:
        urls.append(u)
urls = urls[:MAX_URLS]

checks = []
findings = []
coverage_gaps = []
resources = []

missing_csp_sites = 0
missing_headers_total = 0
exposed_paths_total = 0
login_surfaces_total = 0
banner_leak_total = 0
unreachable_sites = 0
requests_sent = 0

if RUN_OPTIONAL_NUCLEI:
    if not shutil.which("nuclei"):
        coverage_gaps.append({
            "module": "nuclei-allowlist",
            "reason": "目标未安装 nuclei 二进制",
            "impact": "未执行只读 CVE 模板扫描，当前结果仅覆盖安全头与敏感路径基线，不等同于无已知漏洞",
        })
    else:
        coverage_gaps.append({
            "module": "nuclei-allowlist",
            "reason": "定时/轻量巡检路径默认不触发宽模板 Nuclei 扫描，须通过受控 allowlist 计划按需执行",
            "impact": "保持低速非破坏性边界",
        })

if not urls:
    coverage_gaps.append({
        "module": "website-catalog",
        "reason": "当前目标尚未在资产目录登记可扫描的 website 站点（DOOPS_INSPECT_URLS 为空）",
        "impact": "未对任何 HTTP/HTTPS 端点执行暴露面评估；请在资产目录「网站」登记后重试",
    })
    checks.append({
        "id": "surface.scope",
        "name": "暴露面扫描资产范围",
        "category": "security",
        "status": "warning",
        "value": "0 个站点",
        "detail": "请先在目标资产目录登记 website 站点（支持 http/https）",
    })
else:
    for idx, u in enumerate(urls, 1):
        if requests_sent >= MAX_REQUEST_BUDGET:
            coverage_gaps.append({
                "module": "request-budget",
                "reason": f"达到单次低速请求预算上限（{MAX_REQUEST_BUDGET}）",
                "impact": f"剩余 {len(urls) - idx + 1} 个站点延后扫描",
            })
            break
        safe_u = redact_url(u)
        parsed = urlparse(u)
        root_res = fetch_http(u)
        requests_sent += 1

        if root_res["status"] == 0:
            unreachable_sites += 1
            resources.append({
                "url": safe_u,
                "status_code": 0,
                "missing_headers": 0,
                "exposed_paths": 0,
                "server_banner": "unreachable",
                "status": "critical",
            })
            findings.append({
                "id": f"SURF-DOWN-{idx:02d}",
                "severity": "high",
                "root_cause": "confirmed-outage",
                "title": f"站点不可达：{safe_u}",
                "asset": safe_u,
                "evidence": root_res["error"] or "connection failed",
                "recommendation": "检查站点进程、反向代理、TLS 证书与防火墙可达性。",
                "priority": "P1",
            })
            checks.append({"id": f"surface.site.{idx}", "name": f"暴露面基线 {safe_u}", "category": "security", "status": "critical", "value": "不可达", "detail": root_res["error"]})
            continue

        site_missing = 0
        site_exposed = 0
        hdrs = root_res["headers"]

        if CHECK_HEADERS:
            for hname, sev, rc_key, title in SECURITY_HEADERS:
                if hname == "strict-transport-security" and parsed.scheme != "https":
                    continue
                if not hdrs.get(hname):
                    site_missing += 1
                    missing_headers_total += 1
                    if hname == "content-security-policy":
                        missing_csp_sites += 1
                    findings.append({
                        "id": f"SURF-HDR-{len(findings)+1:03d}",
                        "severity": sev,
                        "root_cause": rc_key,
                        "title": f"{title}（{safe_u}）",
                        "asset": safe_u,
                        "evidence": f"GET / HTTP {root_res['status']}; header {hname} absent",
                        "recommendation": f"在反向代理（Nginx/Ingress）中为兼容路径补充 {hname} 响应头。",
                        "priority": "P2" if sev == "medium" else "P3",
                    })

        banner = hdrs.get("server") or hdrs.get("x-powered-by") or "隐藏"
        if VERSION_BANNER_RE.search(banner):
            banner_leak_total += 1
            findings.append({
                "id": f"SURF-VER-{banner_leak_total:03d}",
                "severity": "low",
                "root_cause": "server-version-banner",
                "title": f"响应头暴露服务端精确版本号（{banner[:48]}）",
                "asset": safe_u,
                "evidence": f"server/x-powered-by={banner[:64]}",
                "recommendation": "在 Nginx 配置 server_tokens off 并移除 X-Powered-By 头。",
                "priority": "P3",
            })

        if DETECT_LOGIN:
            tm = TITLE_RE.search(root_res["body"])
            title_txt = re.sub(r"\s+", " ", tm.group(1)).strip()[:80] if tm else ""
            if LOGIN_RE.search(title_txt) or LOGIN_RE.search(root_res["body"][:2048]):
                login_surfaces_total += 1
                findings.append({
                    "id": f"SURF-AUTH-{login_surfaces_total:03d}",
                    "severity": "info",
                    "root_cause": "login-surface",
                    "title": f"发现公开可访问认证/登录入口（{safe_u}）",
                    "asset": safe_u,
                    "evidence": f"HTTP {root_res['status']}; title={title_txt or '(无标题)'}",
                    "recommendation": "确认已启用限速、验证码、强口令策略、登录审计与管理员 MFA。",
                    "priority": "P4",
                })

        if CHECK_PATHS:
            origin = f"{parsed.scheme}://{parsed.netloc}/"
            root_hash = root_res["body_hash"]
            root_is_html = "text/html" in root_res["content_type"].lower()
            for path, rule_name, pattern, is_secret_path, expects_html in SENSITIVE_PATHS:
                if requests_sent >= MAX_REQUEST_BUDGET:
                    break
                time.sleep(DELAY_MS / 1000.0)
                probe_url = urljoin(origin, path)
                res = fetch_http(probe_url, timeout=5.0)
                requests_sent += 1
                # Anti-false-positive guard: require 2xx, non-identical SPA fallback body,
                # reject HTML fallback pages for non-HTML endpoints, and match structural signature.
                if not (200 <= res["status"] < 300):
                    continue
                if root_hash and res["body_hash"] == root_hash:
                    continue
                if not expects_html and "text/html" in res["content_type"].lower() and "<html" in res["body"][:512].lower():
                    continue
                if pattern.search(res["body"]):
                    site_exposed += 1
                    exposed_paths_total += 1
                    # CRITICAL SECURITY BOUNDARY: NEVER echo response body for secret-sensitive hits (.env / .git / actuator)!
                    findings.append({
                        "id": f"SURF-EXP-{exposed_paths_total:03d}",
                        "severity": "critical" if is_secret_path else "high",
                        "root_cause": rule_name,
                        "title": f"疑似敏感路径公开暴露：{path}",
                        "asset": redact_url(probe_url),
                        "evidence": f"HTTP {res['status']}; rule={rule_name}; content_type={res['content_type'][:40] or 'unknown'} (已抑制响应正文防凭据泄露)",
                        "recommendation": f"立即在网关/Nginx 层阻断 {path} 访问，并核查是否需要轮换可能泄露的凭据。",
                        "priority": "P1",
                    })

        site_status = "critical" if site_exposed > 0 else ("warning" if site_missing > 0 or VERSION_BANNER_RE.search(banner) else "pass")
        resources.append({
            "url": safe_u,
            "status_code": root_res["status"],
            "missing_headers": site_missing,
            "exposed_paths": site_exposed,
            "server_banner": banner[:36],
            "status": site_status,
        })
        checks.append({
            "id": f"surface.site.{idx}",
            "name": f"暴露面基线 {safe_u}",
            "category": "security",
            "status": site_status,
            "value": f"HTTP {root_res['status']} · 缺头 {site_missing} · 暴露 {site_exposed}",
            "detail": f"指纹={banner[:32]}，已发低速只读探针（总预算 {requests_sent}/{MAX_REQUEST_BUDGET}）",
        })

risk_total = exposed_paths_total + unreachable_sites + missing_csp_sites + banner_leak_total + (1 if missing_headers_total > missing_csp_sites else 0)
actionable_findings = len([f for f in findings if f["severity"] in ("critical", "high", "medium", "low")])

metrics = [
    {"key": "surface_risk_total", "label": "风险发现合计", "value": actionable_findings, "unit": "项", "status": "critical" if exposed_paths_total or unreachable_sites else ("warning" if actionable_findings else "pass")},
    {"key": "surface_urls_scanned", "label": "扫描站点数", "value": len(resources), "unit": "个", "status": "pass"},
    {"key": "surface_exposed_paths", "label": "敏感路径命中", "value": exposed_paths_total, "unit": "处", "status": "critical" if exposed_paths_total else "pass"},
    {"key": "surface_missing_csp", "label": "缺少 CSP 站点", "value": missing_csp_sites, "unit": "个", "status": "warning" if missing_csp_sites else "pass"},
    {"key": "surface_missing_headers", "label": "安全头缺失总数", "value": missing_headers_total, "unit": "项", "status": "warning" if missing_headers_total else "pass"},
    {"key": "surface_login_surfaces", "label": "公开登录入口", "value": login_surfaces_total, "unit": "个", "status": "pass"},
]

charts = [
    {
        "title": "暴露面风险分类统计",
        "type": "bar",
        "unit": "项",
        "series": [
            {"label": "敏感路径暴露", "value": exposed_paths_total, "status": "critical" if exposed_paths_total else "pass"},
            {"label": "缺少 CSP 站点", "value": missing_csp_sites, "status": "warning" if missing_csp_sites else "pass"},
            {"label": "其他安全头缺失", "value": max(0, missing_headers_total - missing_csp_sites), "status": "warning" if missing_headers_total > missing_csp_sites else "pass"},
            {"label": "版本指纹暴露", "value": banner_leak_total, "status": "warning" if banner_leak_total else "pass"},
            {"label": "公开登录入口", "value": login_surfaces_total, "status": "pass"},
        ],
    }
]

overall = "critical" if (exposed_paths_total > 0 or unreachable_sites > 0) else ("warning" if (actionable_findings > 0 or coverage_gaps) else "pass")
summary = f"低速只读扫描 {len(resources)} 个站点（{requests_sent} 次请求），检出 {actionable_findings} 项安全基线发现、{exposed_paths_total} 处敏感路径暴露" + (f"（含 {len(coverage_gaps)} 项覆盖缺口）" if coverage_gaps else "")

payload = {
    "target": target_identity(),
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "metrics": metrics,
    "checks": checks,
    "charts": charts,
    "resources": resources,
    "findings": findings,
    "coverage_gaps": coverage_gaps,
}
emit(["## 外部暴露面与安全基线扫描", "", f"- **综合结论**：{overall} — {summary}"], payload)
PY
