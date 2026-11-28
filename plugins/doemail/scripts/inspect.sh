#!/usr/bin/env sh
set -eu
tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 邮件服务与域治理巡检

- **综合结论**：warning — 缺少 python3，未能采集邮件基线

```json
MD
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少 python3，未采集邮件指标","metrics":[],"checks":[{"id":"mail.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"目标环境未检测到 python3"}],"coverage_gaps":[{"module":"mail-inspector","reason":"python3 不可用","impact":"未执行任何邮件巡检"}]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")"
  cat <<'MD'
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import random
import re
import shutil
import socket
import ssl
import struct
import subprocess
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024
HARD_CAP_BYTES = 256 * 1024
QTYPE = {"A": 1, "NS": 2, "CNAME": 5, "PTR": 12, "MX": 15, "TXT": 16}

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

CHECK_DNS_AUTH = _env_bool("DOOPS_INPUT_CHECK_DNS_AUTH", True)
CHECK_SMTP_IMAPS = _env_bool("DOOPS_INPUT_CHECK_SMTP_IMAPS", True)
CHECK_K8S = _env_bool("DOOPS_INPUT_CHECK_K8S_RUNTIME", True)
CERT_WARN_DAYS = _env_int("DOOPS_INPUT_CERT_WARN_DAYS", 14, 3, 90)

def target_identity():
    return {
        "cluster": os.environ.get("DOOPS_TARGET_CLUSTER") or None,
        "instance": os.environ.get("DOOPS_TARGET_INSTANCE") or None,
    }

def run_rc(cmd, timeout=8):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, encoding="utf-8", errors="replace", timeout=timeout)
        return r.returncode, (r.stdout or "").strip()
    except Exception as e:
        return 1, str(e)

def encode_name(name):
    out = b""
    for part in name.strip().rstrip(".").split("."):
        if not part:
            continue
        raw = part.encode("ascii", errors="strict")
        out += bytes([len(raw)]) + raw
    return out + b"\0"

def read_name(data, offset, depth=0):
    if depth > 12:
        return "", offset
    labels = []
    jumped = False
    orig_next = offset
    while offset < len(data):
        ln = data[offset]
        if ln == 0:
            offset += 1
            break
        if (ln & 0xC0) == 0xC0:
            if offset + 1 >= len(data):
                break
            ptr = ((ln & 0x3F) << 8) | data[offset + 1]
            if not jumped:
                orig_next = offset + 2
                jumped = True
            sub, _ = read_name(data, ptr, depth + 1)
            if sub:
                labels.append(sub)
            break
        offset += 1
        labels.append(data[offset : offset + ln].decode("ascii", errors="replace"))
        offset += ln
    return ".".join(labels), (orig_next if jumped else offset)

def dns_query(server, port, name, qtype, timeout=2.0):
    pkt = struct.pack("!HHHHHH", random.randrange(65536), 0x0100, 1, 0, 0, 0)
    pkt += encode_name(name)
    pkt += struct.pack("!HH", QTYPE[qtype], 1)
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(timeout)
    try:
        sock.sendto(pkt, (server, port))
        data, _ = sock.recvfrom(4096)
        if len(data) < 12:
            return []
        _, flags, qdc, anc, _, _ = struct.unpack("!HHHHHH", data[:12])
        if (flags & 0xF) != 0:
            return []
        off = 12
        for _ in range(qdc):
            _, off = read_name(data, off)
            off += 4
        out = []
        for _ in range(anc):
            _, off = read_name(data, off)
            if off + 10 > len(data):
                break
            rt, _, _, rdlen = struct.unpack("!HHIH", data[off : off + 10])
            off += 10
            rdata = data[off : off + rdlen]
            if rt == 1 and rdlen == 4:
                out.append(".".join(str(b) for b in rdata))
            elif rt == 15 and rdlen >= 3:
                exch, _ = read_name(data, off + 2)
                out.append(exch.rstrip("."))
            elif rt == 16:
                pos, parts = 0, []
                while pos < len(rdata):
                    l = rdata[pos]
                    pos += 1
                    parts.append(rdata[pos : pos + l].decode("utf-8", errors="replace"))
                    pos += l
                out.append("".join(parts))
            elif rt == 12:
                ptr, _ = read_name(data, off)
                out.append(ptr.rstrip("."))
            off += rdlen
        return out
    except Exception:
        return []
    finally:
        sock.close()

def get_resolver():
    override = os.environ.get("DOOPS_DNS_TEST_SERVER", "").strip()
    if override:
        parts = override.split(":")
        return parts[0], int(parts[1]) if len(parts) > 1 and parts[1].isdigit() else 53
    if os.path.isfile("/etc/resolv.conf"):
        try:
            for line in open("/etc/resolv.conf", "r", encoding="utf-8", errors="replace"):
                toks = line.strip().split()
                if len(toks) >= 2 and toks[0] == "nameserver" and re.match(r"^\d+\.\d+\.\d+\.\d+$", toks[1]):
                    return toks[1], 53
        except Exception:
            pass
    return "", 0

def extract_base_domain(host):
    parts = [p for p in host.strip().lower().rstrip(".").split(".") if p]
    if len(parts) < 2 or re.match(r"^\d+\.\d+\.\d+\.\d+$", host):
        return ""
    if len(parts) >= 3 and parts[-1] in ("cn", "uk", "jp", "au") and parts[-2] in ("com", "org", "edu", "gov", "net", "co"):
        return ".".join(parts[-3:])
    return ".".join(parts[-2:])

def probe_tcp(host, port, use_tls=False, timeout=2.5):
    try:
        with socket.create_connection((host, port), timeout=timeout) as sock:
            if not use_tls:
                return True, None, ""
            ctx = ssl.create_default_context()
            with ctx.wrap_socket(sock, server_hostname=host) as ssock:
                cert = ssock.getpeercert()
                not_after = cert.get("notAfter") if cert else None
                days = None
                if not_after:
                    exp = datetime.strptime(not_after, "%b %d %H:%M:%S %Y %Z").replace(tzinfo=timezone.utc)
                    days = round((exp - datetime.now(timezone.utc)).total_seconds() / 86400.0, 1)
                return True, days, ""
    except Exception as e:
        return False, None, str(e)[:120]

def emit(intro, payload):
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if len(text.encode("utf-8")) > MAX_PAYLOAD_BYTES:
        payload["resources"] = (payload.get("resources") or [])[:25]
        payload["findings"] = (payload.get("findings") or [])[:25]
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    if len(text.encode("utf-8")) > HARD_CAP_BYTES:
        payload["resources"] = (payload.get("resources") or [])[:12]
        payload["findings"] = (payload.get("findings") or [])[:12]
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    print("\n".join(intro) + "\n\n```json\n" + text + "\n```")

checks = []
findings = []
coverage_gaps = []
resources = []

k8s_unready = 0
endpoints_up = 0
cert_days_list = []
dns_checks_total = 0
dns_checks_pass = 0
discovered_mail_hosts = []
discovered_domains = []

# 1. Kubernetes mail runtime discovery
if CHECK_K8S:
    if shutil.which("kubectl"):
        rc, out = run_rc(["kubectl", "get", "pods", "-A", "-o", "json"])
        if rc == 0:
            try:
                data = json.loads(out)
                mail_pods = []
                mail_ns = set()
                for item in data.get("items", []):
                    meta = item.get("metadata", {})
                    name = meta.get("name", "")
                    ns = meta.get("namespace", "")
                    spec = item.get("spec", {})
                    imgs = " ".join(c.get("image", "") for c in spec.get("containers", []))
                    if "mailserver" in imgs.lower() or "mail" in name.lower() or "mail" in ns.lower():
                        mail_pods.append(item)
                        mail_ns.add(ns)
                        for c in spec.get("containers", []):
                            for ev in c.get("env", []) or []:
                                if ev.get("name") in ("OVERRIDE_HOSTNAME", "MAIL_HOSTNAME", "DOMAINNAME") and ev.get("value"):
                                    val = str(ev["value"]).strip().lower()
                                    if "." in val and not val.startswith("$"):
                                        discovered_mail_hosts.append(val)
                                        bd = extract_base_domain(val)
                                        if bd:
                                            discovered_domains.append(bd)
                if not mail_pods:
                    coverage_gaps.append({"module": "k8s-mail-runtime", "reason": "集群中未发现 mailserver 相关 Pod", "impact": "跳过 K8s 邮件工作负载检查"})
                else:
                    for p in mail_pods[:12]:
                        meta = p.get("metadata", {})
                        ns, name = meta.get("namespace", ""), meta.get("name", "")
                        phase = p.get("status", {}).get("phase", "Unknown")
                        ready = all(cs.get("ready") for cs in p.get("status", {}).get("containerStatuses", [])) and phase == "Running"
                        st = "pass" if ready else "critical"
                        if not ready:
                            k8s_unready += 1
                            findings.append({
                                "id": f"MAIL-K8S-{k8s_unready:03d}",
                                "severity": "critical",
                                "root_cause": "mail-pod-unready",
                                "title": f"邮件 Pod 未就绪：{ns}/{name}",
                                "asset": f"{ns}/{name}",
                                "evidence": f"phase={phase} ready={ready}",
                                "recommendation": "检查 kubectl describe pod 与容器日志，核对 PVC 挂载和证书卷。",
                                "priority": "P1",
                            })
                        resources.append({"name": f"{ns}/{name}", "kind": "K8s Pod", "detail": f"phase={phase}", "status": st})
                        checks.append({"id": f"mail.k8s.pod.{ns}.{name}", "name": f"邮件 Pod {ns}/{name}", "category": "availability", "status": st, "value": phase, "detail": "Running & Ready" if ready else "未就绪"})
                    for ns in sorted(mail_ns):
                        prc, pout = run_rc(["kubectl", "-n", ns, "get", "pvc", "-o", "json"])
                        if prc == 0:
                            pdata = json.loads(pout)
                            for pvc in pdata.get("items", []):
                                pname = pvc.get("metadata", {}).get("name", "")
                                pphase = pvc.get("status", {}).get("phase", "Unknown")
                                if pphase != "Bound":
                                    k8s_unready += 1
                                    findings.append({
                                        "id": f"MAIL-PVC-{k8s_unready:03d}",
                                        "severity": "high",
                                        "root_cause": "mail-pvc-unbound",
                                        "title": f"邮件持久卷未绑定：{ns}/{pname}",
                                        "asset": f"{ns}/{pname}",
                                        "evidence": f"pvc phase={pphase}",
                                        "recommendation": "核查 StorageClass 与卷供给状态，防止邮件数据丢失。",
                                        "priority": "P1",
                                    })
                                resources.append({"name": f"{ns}/{pname}", "kind": "K8s PVC", "detail": f"phase={pphase}", "status": "pass" if pphase == "Bound" else "critical"})
            except Exception as e:
                coverage_gaps.append({"module": "k8s-mail-runtime", "reason": f"解析 kubectl 输出失败：{str(e)[:80]}", "impact": "未完成 K8s 邮件运行时解析"})
        else:
            coverage_gaps.append({"module": "k8s-mail-runtime", "reason": "目标未连通 Kubernetes API", "impact": "跳过 K8s Pod/PVC 邮件运行时检查"})
    else:
        coverage_gaps.append({"module": "k8s-mail-runtime", "reason": "目标未安装 kubectl", "impact": "跳过 K8s Pod/PVC 检查"})

# Derive unique domains from DOOPS_CATALOG_MAIL_DOMAINS + K8s discovery + DOOPS_INSPECT_DOMAINS
catalog_mail_domains = [d.strip().lower().rstrip(".") for d in (os.environ.get("DOOPS_PLUGIN_MAIL_DOMAINS", "") or os.environ.get("DOOPS_CATALOG_MAIL_DOMAINS", "")).split(",") if d.strip()]
env_domains = [d.strip().lower().rstrip(".") for d in os.environ.get("DOOPS_INSPECT_DOMAINS", "").split(",") if d.strip()]
domains = []
seen = set()
for raw in catalog_mail_domains + discovered_domains + [extract_base_domain(d) for d in env_domains]:
    if raw and raw not in seen:
        seen.add(raw)
        domains.append(raw)
domains = domains[:12]

# 2. Mail domain DNS auth checks (MX / SPF / DMARC / DKIM)
res_host, res_port = get_resolver()
mx_hosts = list(dict.fromkeys(discovered_mail_hosts))
if CHECK_DNS_AUTH:
    if not domains:
        coverage_gaps.append({"module": "mail-dns-auth", "reason": "未发现待核验的邮件域名（K8s 与网站目录均为空）", "impact": "未执行 MX/SPF/DMARC/DKIM 检查"})
    elif not res_host:
        coverage_gaps.append({"module": "mail-dns-auth", "reason": "未在 /etc/resolv.conf 发现 IPv4 解析器", "impact": "未执行邮件 DNS 认证记录校验"})
    else:
        for dom in domains:
            mx = dns_query(res_host, res_port, dom, "MX")
            txts = dns_query(res_host, res_port, dom, "TXT")
            dmarc = dns_query(res_host, res_port, f"_dmarc.{dom}", "TXT")
            dkim = dns_query(res_host, res_port, f"default._domainkey.{dom}", "TXT")
            has_mx = len(mx) > 0
            has_spf = any("v=spf1" in t.lower() for t in txts)
            has_dmarc = any("v=dmarc1" in t.lower() for t in dmarc)
            has_dkim = any("v=dkim1" in t.lower() for t in dkim)
            if has_mx:
                mx_hosts.append(mx[0])
            for ok, code, label, sev in (
                (has_mx, "missing-mail-mx", f"邮件域 {dom} 缺少 MX 记录", "high"),
                (has_spf, "missing-mail-spf", f"邮件域 {dom} 缺少 SPF 记录", "medium"),
                (has_dmarc, "missing-mail-dmarc", f"邮件域 {dom} 缺少 DMARC 记录", "medium"),
                (has_dkim, "missing-mail-dkim", f"邮件域 {dom} 未检出默认 selector DKIM 记录", "low"),
            ):
                dns_checks_total += 1
                if ok:
                    dns_checks_pass += 1
                else:
                    findings.append({
                        "id": f"MAIL-DNS-{len(findings)+1:03d}",
                        "severity": sev,
                        "root_cause": code,
                        "title": label,
                        "asset": dom,
                        "evidence": f"resolver={res_host}:{res_port} check={code}",
                        "recommendation": "参照 DoEmail 域名接入清单补齐 MX/SPF/DKIM/DMARC 及 rDNS 记录。",
                        "priority": "P1" if sev == "high" else ("P2" if sev == "medium" else "P3"),
                    })
            score_ok = sum(1 for x in (has_mx, has_spf, has_dmarc, has_dkim) if x)
            st = "pass" if score_ok == 4 else ("critical" if not has_mx else "warning")
            detail = f"MX={'OK' if has_mx else '缺'} · SPF={'OK' if has_spf else '缺'} · DMARC={'OK' if has_dmarc else '缺'} · DKIM={'OK' if has_dkim else '缺'}"
            resources.append({"name": dom, "kind": "邮件域 DNS", "detail": detail, "status": st})
            checks.append({"id": f"mail.dns.{dom}", "name": f"邮件域认证 {dom}", "category": "config", "status": st, "value": f"{score_ok}/4 通过", "detail": detail})

# 3. SMTP / Submission / IMAPS port & TLS certificate probes
test_mail_host = os.environ.get("DOOPS_MAIL_TEST_HOST", "").strip()
if test_mail_host:
    mx_hosts.insert(0, test_mail_host)
mx_hosts = list(dict.fromkeys([h for h in mx_hosts if h]))[:4]

if CHECK_SMTP_IMAPS:
    if not mx_hosts:
        coverage_gaps.append({"module": "smtp-imaps-probe", "reason": "未发现可探测的邮件主机（无 MX 且无 K8s 邮件主机）", "impact": "未执行 25/587/993 端口与 TLS 证书探活"})
    else:
        for host in mx_hosts:
            for port, label, use_tls in ((25, "SMTP", False), (587, "Submission", False), (993, "IMAPS", True)):
                ok, days, err = probe_tcp(host, port, use_tls=use_tls)
                if ok:
                    endpoints_up += 1
                    st = "pass"
                    if days is not None:
                        cert_days_list.append(days)
                        if days < 0:
                            st = "critical"
                            findings.append({"id": f"MAIL-CERT-{len(findings)+1:03d}", "severity": "critical", "root_cause": "tls-expiry", "title": f"IMAPS 证书已过期：{host}:993", "asset": f"{host}:993", "evidence": f"tls_expiry_days={days}", "recommendation": "立即更新邮件服务 TLS 证书并重载。", "priority": "P1"})
                        elif days < CERT_WARN_DAYS:
                            st = "warning"
                            findings.append({"id": f"MAIL-CERT-{len(findings)+1:03d}", "severity": "medium", "root_cause": "tls-expiry", "title": f"IMAPS 证书即将过期（剩余 {days} 天）：{host}:993", "asset": f"{host}:993", "evidence": f"tls_expiry_days={days}", "recommendation": "尽快完成证书续期与热重载。", "priority": "P2"})
                    detail = f"{label} 可达" + (f" · 证书剩余 {days} 天" if days is not None else "")
                else:
                    st = "warning"
                    detail = f"{label} 不可达：{err}"
                    findings.append({"id": f"MAIL-PORT-{len(findings)+1:03d}", "severity": "medium", "root_cause": "mail-port-unreachable", "title": f"邮件端口不可达：{host}:{port} ({label})", "asset": f"{host}:{port}", "evidence": err, "recommendation": "检查节点安全组、出口 25 封禁策略与邮件容器端口映射。", "priority": "P2"})
                resources.append({"name": f"{host}:{port}", "kind": label, "detail": detail, "status": st})
                checks.append({"id": f"mail.port.{host}.{port}", "name": f"{label} 探活 {host}:{port}", "category": "availability", "status": st, "value": "可达" if ok else "不可达", "detail": detail})

if not checks:
    gap_summary = "；".join(f"{g['module']}: {g['reason']}" for g in coverage_gaps[:3]) or "当前目标未发现 K8s 邮件运行时或已登记邮件域"
    checks.append({
        "id": "mail.scope",
        "name": "邮件服务巡检覆盖范围",
        "category": "availability",
        "status": "warning",
        "value": f"{len(coverage_gaps)} 项缺口",
        "detail": gap_summary,
    })

dns_pass_pct = round((dns_checks_pass / dns_checks_total) * 100.0, 1) if dns_checks_total > 0 else 0.0
cert_min_days = round(min(cert_days_list), 1) if cert_days_list else 0.0
risk_total = len([f for f in findings if f["severity"] in ("critical", "high", "medium", "low")])

metrics = [
    {"key": "mail_risk_total", "label": "邮件风险合计", "value": risk_total, "unit": "项", "status": "critical" if k8s_unready or risk_total >= 3 else ("warning" if risk_total >= 1 else "pass")},
    {"key": "mail_domains_checked", "label": "核验邮件域", "value": len(domains), "unit": "个", "status": "pass"},
    {"key": "mail_dns_pass_pct", "label": "DNS 认证通过率", "value": dns_pass_pct, "unit": "%", "status": "pass" if dns_checks_total == 0 or dns_pass_pct >= 75 else "warning"},
    {"key": "mail_endpoints_up", "label": "存活邮件端口", "value": endpoints_up, "unit": "个", "status": "pass"},
    {"key": "mail_k8s_unready", "label": "异常 Pod/PVC", "value": k8s_unready, "unit": "个", "status": "critical" if k8s_unready else "pass"},
]
if cert_days_list:
    metrics.append({"key": "mail_cert_min_days", "label": "最短证书剩余", "value": cert_min_days, "unit": "天", "status": "critical" if cert_min_days < 0 else ("warning" if cert_min_days < CERT_WARN_DAYS else "pass")})

charts = [
    {
        "title": "邮件治理状态分布",
        "type": "bar",
        "unit": "项",
        "series": [
            {"label": "核验域名", "value": len(domains), "status": "pass"},
            {"label": "存活端口", "value": endpoints_up, "status": "pass"},
            {"label": "风险发现", "value": risk_total, "status": "critical" if risk_total >= 3 else ("warning" if risk_total >= 1 else "pass")},
            {"label": "异常 Pod/PVC", "value": k8s_unready, "status": "critical" if k8s_unready else "pass"},
        ],
    }
]

overall = "critical" if any(c["status"] == "critical" for c in checks) else ("warning" if any(c["status"] == "warning" for c in checks) or coverage_gaps else "pass")
summary = f"核验 {len(domains)} 个邮件域（DNS 通过率 {dns_pass_pct}%）、{endpoints_up} 个存活端口，检出 {risk_total} 项风险" + (f"（含 {len(coverage_gaps)} 项覆盖缺口）" if coverage_gaps else "")

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
emit(["## 邮件服务与域治理巡检", "", f"- **综合结论**：{overall} — {summary}"], payload)
PY
