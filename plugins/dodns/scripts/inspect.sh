#!/usr/bin/env sh
set -eu
tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## DNS 基础设施治理巡检

- **综合结论**：warning — 缺少 python3，未能采集 DNS 基线

```json
MD
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少 python3，未采集 DNS 指标","metrics":[],"checks":[{"id":"dns.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"目标环境未检测到 python3"}],"coverage_gaps":[{"module":"dns-wire","reason":"python3 不可用","impact":"未执行任何 DNS 检查"}]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")"
  cat <<'MD'
```
MD
  exit 0
fi

python3 - <<'PY'
import glob
import json
import os
import random
import re
import shutil
import socket
import struct
import subprocess
import time
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024
HARD_CAP_BYTES = 256 * 1024

QTYPE = {"A": 1, "NS": 2, "CNAME": 5, "SOA": 6, "PTR": 12, "MX": 15, "TXT": 16, "AAAA": 28, "AXFR": 252}
TYPE_NAME = {v: k for k, v in QTYPE.items()}

def _env_bool(name, default):
    raw = os.environ.get(name, "").strip().lower()
    if not raw:
        return default
    return raw in ("1", "true", "yes", "on")

def _env_float(name, default, lo, hi):
    try:
        v = float(os.environ.get(name, "").strip() or default)
        return max(lo, min(hi, v))
    except Exception:
        return default

CHECK_OPEN_RECURSION = _env_bool("DOOPS_INPUT_CHECK_OPEN_RECURSION", True)
CHECK_AXFR = _env_bool("DOOPS_INPUT_CHECK_AXFR", True)
LATENCY_WARN_MS = _env_float("DOOPS_INPUT_LATENCY_WARN_MS", 200.0, 20.0, 5000.0)
QUERY_SCOPE = os.environ.get("DOOPS_INPUT_QUERY_SCOPE", "local_and_catalog").strip().lower()
if QUERY_SCOPE not in ("local_and_catalog", "local_only", "catalog_only"):
    QUERY_SCOPE = "local_and_catalog"

def target_identity():
    return {
        "cluster": os.environ.get("DOOPS_TARGET_CLUSTER") or None,
        "instance": os.environ.get("DOOPS_TARGET_INSTANCE") or None,
    }

def run_rc(cmd, timeout=6):
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
        if len(raw) > 63:
            raise ValueError("label too long")
        out += bytes([len(raw)]) + raw
    return out + b"\0"

def read_name(data, offset, depth=0):
    if depth > 12:
        return "", offset
    labels = []
    jumped = False
    orig_next = offset
    while offset < len(data):
        length = data[offset]
        if length == 0:
            offset += 1
            break
        if (length & 0xC0) == 0xC0:
            if offset + 1 >= len(data):
                break
            ptr = ((length & 0x3F) << 8) | data[offset + 1]
            if not jumped:
                orig_next = offset + 2
                jumped = True
            sub, _ = read_name(data, ptr, depth + 1)
            if sub:
                labels.append(sub)
            break
        offset += 1
        labels.append(data[offset : offset + length].decode("ascii", errors="replace"))
        offset += length
    return ".".join(labels), (orig_next if jumped else offset)

def decode_rdata(data, rtype, rdata_offset, rdlen):
    rdata = data[rdata_offset : rdata_offset + rdlen]
    if rtype == 1 and rdlen == 4:
        return ".".join(str(b) for b in rdata)
    if rtype == 28 and rdlen == 16:
        return socket.inet_ntop(socket.AF_INET6, rdata)
    if rtype in (2, 5, 12):
        name, _ = read_name(data, rdata_offset)
        return name
    if rtype == 15 and rdlen >= 3:
        pref = struct.unpack("!H", rdata[:2])[0]
        exch, _ = read_name(data, rdata_offset + 2)
        return f"{pref} {exch}"
    if rtype == 16:
        pos = 0
        chunks = []
        while pos < len(rdata):
            ln = rdata[pos]
            pos += 1
            chunks.append(rdata[pos : pos + ln].decode("utf-8", errors="replace"))
            pos += ln
        return "".join(chunks)
    if rtype == 6:
        mname, next_off = read_name(data, rdata_offset)
        rname, next_off = read_name(data, next_off)
        if next_off + 20 <= len(data):
            serial = struct.unpack("!I", data[next_off : next_off + 4])[0]
            return f"{mname} {rname} serial={serial}"
        return f"{mname} {rname}"
    return rdata.hex()[:64]

def build_dns_query(name, qtype="A", rd=True):
    txid = random.randrange(65536)
    flags = 0x0100 if rd else 0x0000
    pkt = struct.pack("!HHHHHH", txid, flags, 1, 0, 0, 0)
    pkt += encode_name(name)
    pkt += struct.pack("!HH", QTYPE[qtype], 1)
    return txid, pkt

def parse_dns_response(data):
    if len(data) < 12:
        raise ValueError("short response")
    txid, flags, qdcount, ancount, nscount, arcount = struct.unpack("!HHHHHH", data[:12])
    rcode = flags & 0xF
    ra = bool(flags & 0x0080)
    aa = bool(flags & 0x0400)
    offset = 12
    for _ in range(qdcount):
        _, offset = read_name(data, offset)
        offset += 4
    answers = []
    for _ in range(ancount):
        _, offset = read_name(data, offset)
        if offset + 10 > len(data):
            break
        rtype, rclass, ttl, rdlen = struct.unpack("!HHIH", data[offset : offset + 10])
        offset += 10
        val = decode_rdata(data, rtype, offset, rdlen)
        offset += rdlen
        answers.append({"type": TYPE_NAME.get(rtype, str(rtype)), "ttl": ttl, "value": val})
    return {"id": txid, "rcode": rcode, "ra": ra, "aa": aa, "answers": answers, "ancount": ancount}

def udp_dns_query(server, name, qtype="A", port=53, rd=True, timeout=2.5):
    t0 = time.time()
    _, pkt = build_dns_query(name, qtype=qtype, rd=rd)
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(timeout)
    try:
        sock.sendto(pkt, (server, port))
        data, _ = sock.recvfrom(4096)
        parsed = parse_dns_response(data)
        parsed["latency_ms"] = round((time.time() - t0) * 1000.0, 1)
        return parsed, ""
    except Exception as e:
        return None, str(e)[:160]
    finally:
        sock.close()

def tcp_axfr_probe(server, zone, port=53, timeout=2.5):
    _, pkt = build_dns_query(zone, qtype="AXFR", rd=False)
    frame = struct.pack("!H", len(pkt)) + pkt
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(timeout)
    try:
        sock.connect((server, port))
        sock.sendall(frame)
        hdr = sock.recv(2)
        if len(hdr) < 2:
            return False, "closed"
        length = struct.unpack("!H", hdr)[0]
        buf = b""
        while len(buf) < min(length, 4096):
            chunk = sock.recv(min(length, 4096) - len(buf))
            if not chunk:
                break
            buf += chunk
        parsed = parse_dns_response(buf)
        exposed = parsed["rcode"] == 0 and parsed["ancount"] > 0
        return exposed, f"rcode={parsed['rcode']} answers={parsed['ancount']}"
    except Exception as e:
        return False, str(e)[:120]
    finally:
        sock.close()

def discover_system_resolvers():
    resolvers = []
    override = os.environ.get("DOOPS_DNS_TEST_SERVER", "").strip()
    if override:
        parts = override.split(":")
        host = parts[0]
        port = int(parts[1]) if len(parts) > 1 and parts[1].isdigit() else 53
        return [(host, port)]
    if os.path.isfile("/etc/resolv.conf"):
        try:
            with open("/etc/resolv.conf", "r", encoding="utf-8", errors="replace") as f:
                for line in f:
                    line = line.strip()
                    if line.startswith("nameserver"):
                        toks = line.split()
                        if len(toks) >= 2 and re.match(r"^\d+\.\d+\.\d+\.\d+$", toks[1]):
                            resolvers.append((toks[1], 53))
        except Exception:
            pass
    return resolvers[:2]

def discover_local_bind_zones():
    zones = []
    seen = set()
    conf_dir = os.environ.get("DOOPS_DNS_BIND_DIR", "/etc/bind")
    for pattern in (os.path.join(conf_dir, "named.conf.local"), os.path.join(conf_dir, "named.conf")):
        if not os.path.isfile(pattern):
            continue
        try:
            text = open(pattern, "r", encoding="utf-8", errors="replace").read()
            for m in re.finditer(r'zone\s+"([A-Za-z0-9.-]+)"\s*(?:IN\s*)?\{([^}]*)\}', text, re.S):
                zname = m.group(1).strip().rstrip(".").lower()
                if not zname or zname in ("localhost", "127.in-addr.arpa", "0.in-addr.arpa", "255.in-addr.arpa", "."):
                    continue
                if zname in seen:
                    continue
                fm = re.search(r'file\s+"([^"]+)"', m.group(2))
                zfile = fm.group(1) if fm else ""
                seen.add(zname)
                zones.append({"zone": zname, "file": zfile})
        except Exception:
            pass
    for zpath in sorted(glob.glob(os.path.join(conf_dir, "zones", "db.*"))):
        base = os.path.basename(zpath)[3:].strip().lower()
        if base and base not in seen and not base.endswith(".bak"):
            seen.add(base)
            zones.append({"zone": base, "file": zpath})
    return zones[:24]

def emit(intro, payload):
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if len(text.encode("utf-8")) > MAX_PAYLOAD_BYTES:
        payload["resources"] = (payload.get("resources") or [])[:30]
        payload["findings"] = (payload.get("findings") or [])[:30]
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
latencies = []

open_recursion_hits = 0
axfr_exposed_hits = 0
query_failed_hits = 0
config_error_hits = 0

# 1. Local BIND / Unbound listeners & services
dns_ports = []
listener_tool = "ss" if shutil.which("ss") else ("netstat" if shutil.which("netstat") else None)
if listener_tool == "ss":
    rc, out = run_rc(["ss", "-lntup"])
    if rc == 0:
        for line in out.splitlines():
            if re.search(r":(53|5353)\b", line):
                dns_ports.append(line.strip()[:120])
elif listener_tool == "netstat":
    rc, out = run_rc(["netstat", "-lnup"])
    if rc == 0:
        for line in out.splitlines():
            if re.search(r":(53|5353)\b", line):
                dns_ports.append(line.strip()[:120])

local_bind_zones = discover_local_bind_zones() if QUERY_SCOPE != "catalog_only" else []
has_bind_conf = os.path.isfile(os.path.join(os.environ.get("DOOPS_DNS_BIND_DIR", "/etc/bind"), "named.conf"))
has_local_dns_daemon = bool(dns_ports or local_bind_zones or has_bind_conf)

if QUERY_SCOPE != "catalog_only":
    if has_local_dns_daemon:
        checks.append({
            "id": "dns.local.listeners",
            "name": "本机 DNS 服务与 53/5353 监听",
            "category": "availability",
            "status": "pass",
            "value": f"{len(dns_ports)} 监听 / {len(local_bind_zones)} Zone",
            "detail": "; ".join(dns_ports[:4]) if dns_ports else f"发现 {len(local_bind_zones)} 个本机 BIND Zone 定义",
        })
    else:
        checks.append({
            "id": "dns.local.listeners",
            "name": "本机 DNS 服务探测",
            "category": "availability",
            "status": "pass",
            "value": "非 DNS 服务端节点",
            "detail": "本机未监听 53/5353 且未发现 /etc/bind 配置，跳过 BIND/Unbound 服务检查",
        })

    if has_bind_conf:
        if shutil.which("named-checkconf"):
            rc, out = run_rc(["named-checkconf", os.path.join(os.environ.get("DOOPS_DNS_BIND_DIR", "/etc/bind"), "named.conf")])
            st = "pass" if rc == 0 else "critical"
            if rc != 0:
                config_error_hits += 1
                findings.append({
                    "id": "DNS-CFG-001",
                    "severity": "critical",
                    "root_cause": "bind-config-invalid",
                    "title": "BIND 主配置语法校验失败",
                    "asset": "named.conf",
                    "evidence": out[:200] or f"named-checkconf rc={rc}",
                    "recommendation": "执行 named-checkconf 定位语法错误并先修复后再 reload。",
                    "priority": "P1",
                })
            checks.append({"id": "dns.bind.checkconf", "name": "BIND 主配置语法", "category": "config", "status": st, "value": "valid" if rc == 0 else "invalid", "detail": out[:200] or "named-checkconf 通过"})
        else:
            coverage_gaps.append({"module": "named-checkconf", "reason": "目标未安装 bind9utils (named-checkconf)", "impact": "未对本机 named.conf 执行静态语法校验"})

    for z in local_bind_zones:
        zname, zfile = z["zone"], z["file"]
        if zfile and os.path.isfile(zfile) and shutil.which("named-checkzone"):
            rc, out = run_rc(["named-checkzone", zname, zfile])
            if rc != 0:
                config_error_hits += 1
                findings.append({
                    "id": f"DNS-ZONE-{len(findings)+1:03d}",
                    "severity": "high",
                    "root_cause": "bind-zone-invalid",
                    "title": f"Zone {zname} 语法校验失败",
                    "asset": zname,
                    "evidence": out[:200] or f"named-checkzone rc={rc}",
                    "recommendation": "核对 SOA/记录格式并在重载前通过 named-checkzone 校验。",
                    "priority": "P1",
                })

    if os.path.isfile("/etc/unbound/unbound.conf"):
        if shutil.which("unbound-checkconf"):
            rc, out = run_rc(["unbound-checkconf"])
            st = "pass" if rc == 0 else "critical"
            if rc != 0:
                config_error_hits += 1
                findings.append({
                    "id": "DNS-UNBOUND-001",
                    "severity": "high",
                    "root_cause": "unbound-config-invalid",
                    "title": "Unbound 递归器配置语法校验失败",
                    "asset": "/etc/unbound/unbound.conf",
                    "evidence": out[:200],
                    "recommendation": "运行 unbound-checkconf 修复配置并确保仅监听私有地址。",
                    "priority": "P1",
                })
            checks.append({"id": "dns.unbound.checkconf", "name": "Unbound 配置语法", "category": "config", "status": st, "value": "valid" if rc == 0 else "invalid", "detail": out[:200] or "unbound-checkconf 通过"})
        else:
            coverage_gaps.append({"module": "unbound-checkconf", "reason": "目标未安装 unbound-checkconf", "impact": "未校验 /etc/unbound/unbound.conf"})

# 2. Open recursion & AXFR security checks (pure Python DNS wire)
local_test_override = os.environ.get("DOOPS_DNS_TEST_SERVER", "").strip()
local_dns_target = None
if local_test_override:
    parts = local_test_override.split(":")
    local_dns_target = (parts[0], int(parts[1]) if len(parts) > 1 and parts[1].isdigit() else 53)
elif any(":53 " in p or p.endswith(":53") for p in dns_ports):
    local_dns_target = ("127.0.0.1", 53)

if CHECK_OPEN_RECURSION and local_dns_target and QUERY_SCOPE != "catalog_only":
    resp, err = udp_dns_query(local_dns_target[0], "example.com", qtype="A", port=local_dns_target[1], rd=True, timeout=2.0)
    if resp is not None:
        if resp["ra"] and resp["rcode"] == 0:
            open_recursion_hits += 1
            findings.append({
                "id": "DNS-SEC-001",
                "severity": "high",
                "root_cause": "dns-open-recursion",
                "title": "DNS 服务开启了开放递归响应（RA=1）",
                "asset": f"{local_dns_target[0]}:{local_dns_target[1]}",
                "evidence": f"RD=1 query example.com returned rcode=0 RA=1 answers={resp['ancount']}",
                "recommendation": "权威 BIND 节点应显式配置 recursion no，并将递归服务隔离在私有地址（如 127.0.0.1:5353）。",
                "priority": "P1",
            })
            checks.append({"id": "dns.security.open_recursion", "name": "开放递归安全检查", "category": "security", "status": "critical", "value": "RA=1 开放", "detail": f"{local_dns_target[0]}:{local_dns_target[1]} 返回递归应答"})
        else:
            checks.append({"id": "dns.security.open_recursion", "name": "开放递归安全检查", "category": "security", "status": "pass", "value": "已关闭递归", "detail": f"RA={resp['ra']} rcode={resp['rcode']}"})
    else:
        coverage_gaps.append({"module": "open-recursion-probe", "reason": f"本机 DNS 探测超时或不可达：{err}", "impact": "无法确认 53 端口是否关闭递归"})

if CHECK_AXFR and local_dns_target and local_bind_zones:
    for z in local_bind_zones[:8]:
        exposed, detail = tcp_axfr_probe(local_dns_target[0], z["zone"], port=local_dns_target[1])
        if exposed:
            axfr_exposed_hits += 1
            findings.append({
                "id": f"DNS-AXFR-{axfr_exposed_hits:03d}",
                "severity": "high",
                "root_cause": "dns-axfr-exposed",
                "title": f"Zone {z['zone']} 允许未受限 AXFR 区域传送",
                "asset": z["zone"],
                "evidence": f"{local_dns_target[0]}:{local_dns_target[1]} AXFR {detail}",
                "recommendation": "在 named.conf 中配置 allow-transfer { none; } 或限定受 TSIG 保护的辅节点 IP。",
                "priority": "P1",
            })
    checks.append({
        "id": "dns.security.axfr",
        "name": "AXFR 区域传送暴露检查",
        "category": "security",
        "status": "critical" if axfr_exposed_hits else "pass",
        "value": f"{axfr_exposed_hits} 个暴露" if axfr_exposed_hits else "无暴露",
        "detail": f"已探测 {min(len(local_bind_zones), 8)} 个本机 Zone",
    })

# 3. Domain resolution & latency probes (local zones + DOOPS_CATALOG_DNS_ZONES + DOOPS_INSPECT_DOMAINS)
catalog_zones = [d.strip().lower().rstrip(".") for d in (os.environ.get("DOOPS_PLUGIN_DNS_ZONES", "") or os.environ.get("DOOPS_CATALOG_DNS_ZONES", "")).split(",") if d.strip()]
catalog_domains = [d.strip().lower().rstrip(".") for d in os.environ.get("DOOPS_INSPECT_DOMAINS", "").split(",") if d.strip()]
probe_targets = []
seen_dom = set()
if QUERY_SCOPE != "catalog_only":
    for z in local_bind_zones:
        if z["zone"] not in seen_dom:
            seen_dom.add(z["zone"])
            probe_targets.append((z["zone"], "zone"))
if QUERY_SCOPE != "local_only":
    for d in catalog_zones:
        if re.match(r"^[a-z0-9.-]+$", d) and not re.match(r"^\d+\.\d+\.\d+\.\d+$", d) and d not in seen_dom:
            seen_dom.add(d)
            probe_targets.append((d, "catalog_zone"))
    for d in catalog_domains:
        if re.match(r"^[a-z0-9.-]+$", d) and not re.match(r"^\d+\.\d+\.\d+\.\d+$", d) and d not in seen_dom:
            seen_dom.add(d)
            probe_targets.append((d, "catalog"))

resolvers = discover_system_resolvers()
if not probe_targets:
    coverage_gaps.append({
        "module": "dns-resolution",
        "reason": "未发现本机 BIND Zone，且当前目标网站资产目录为空（DOOPS_INSPECT_DOMAINS 为空）",
        "impact": "尚未对具体业务域名执行解析延迟与一致性巡检；请在资产目录登记网站或切换到 DNS 节点执行",
    })
    checks.append({
        "id": "dns.resolution.scope",
        "name": "域名解析覆盖范围",
        "category": "availability",
        "status": "warning",
        "value": "0 个域名",
        "detail": "无本机 Zone 且无目录网站域名，待补充巡检范围",
    })
else:
    srv_host, srv_port = local_dns_target if local_dns_target else (resolvers[0] if resolvers else ("", 0))
    for dom, kind in probe_targets[:24]:
        lat_ms = 0.0
        ans_text = ""
        err = ""
        st = "pass"
        used_srv = f"{srv_host}:{srv_port}" if srv_host else "system"
        if srv_host:
            resp, err = udp_dns_query(srv_host, dom, qtype="A", port=srv_port, rd=(kind != "zone" or local_dns_target is None), timeout=2.5)
            if resp is not None:
                lat_ms = resp["latency_ms"]
                latencies.append(lat_ms)
                vals = [a["value"] for a in resp["answers"][:3]]
                ans_text = ", ".join(vals) if vals else f"RCODE={resp['rcode']} (无 A 记录)"
                if resp["rcode"] != 0:
                    st = "critical"
                    query_failed_hits += 1
                    findings.append({
                        "id": f"DNS-QRY-{query_failed_hits:03d}",
                        "severity": "high",
                        "root_cause": "dns-resolution-failed",
                        "title": f"域名 {dom} 解析返回异常 RCODE={resp['rcode']}",
                        "asset": dom,
                        "evidence": f"server={used_srv} rcode={resp['rcode']} latency={lat_ms}ms",
                        "recommendation": "核查该域名的权威 NS 委派、A/CNAME 记录与上级解析器可达性。",
                        "priority": "P1",
                    })
                elif lat_ms >= LATENCY_WARN_MS:
                    st = "warning"
                    findings.append({
                        "id": f"DNS-LAT-{len(findings)+1:03d}",
                        "severity": "medium",
                        "root_cause": "dns-high-latency",
                        "title": f"域名 {dom} 解析延迟偏高（{lat_ms} ms）",
                        "asset": dom,
                        "evidence": f"server={used_srv} latency={lat_ms}ms threshold={LATENCY_WARN_MS}ms",
                        "recommendation": "核查上游转发链路、本地 Unbound 缓存命中率或网络丢包。",
                        "priority": "P2",
                    })
            else:
                st = "critical"
                ans_text = f"超时/失败: {err[:60]}"
                query_failed_hits += 1
                findings.append({
                    "id": f"DNS-QRY-{query_failed_hits:03d}",
                    "severity": "high",
                    "root_cause": "dns-resolution-failed",
                    "title": f"域名 {dom} DNS 探测超时或失败",
                    "asset": dom,
                    "evidence": f"server={used_srv} error={err}",
                    "recommendation": "确认目标网络 53/udp 与 53/tcp 出向/入向安全组策略以及解析器存活。",
                    "priority": "P1",
                })
        else:
            t0 = time.time()
            try:
                infos = socket.getaddrinfo(dom, None, proto=socket.IPPROTO_TCP)
                lat_ms = round((time.time() - t0) * 1000.0, 1)
                latencies.append(lat_ms)
                ips = sorted({i[4][0] for i in infos})[:3]
                ans_text = ", ".join(ips)
                if lat_ms >= LATENCY_WARN_MS:
                    st = "warning"
            except Exception as e:
                lat_ms = round((time.time() - t0) * 1000.0, 1)
                st = "critical"
                ans_text = str(e)[:80]
                query_failed_hits += 1
        resources.append({
            "name": dom,
            "kind": "Zone" if kind == "zone" else "目录域名",
            "server": used_srv,
            "answer": ans_text,
            "latency_ms": lat_ms,
            "status": st,
        })
        checks.append({
            "id": f"dns.resolve.{len(resources)}",
            "name": f"解析校验 {dom}",
            "category": "availability",
            "status": st,
            "value": f"{lat_ms}ms" if st != "critical" else "失败",
            "threshold": f"< {int(LATENCY_WARN_MS)}ms",
            "detail": ans_text,
        })

# 4. Optional managed AliDNS runtime opaque credential readiness check (zero secret echo)
alidns_cred_file = os.environ.get("DOOPS_CRED_FILE_ALIDNS_CREDENTIAL", "").strip()
if alidns_cred_file:
    try:
        with open(alidns_cred_file, "r", encoding="utf-8") as cf:
            cred_obj = json.load(cf)
        if isinstance(cred_obj, dict):
            has_id = bool(str(cred_obj.get("ALIYUN_ACCESS_KEY_ID", "")).strip())
            has_sec = bool(str(cred_obj.get("ALIYUN_ACCESS_KEY_SECRET", "")).strip())
            key_names = sorted(k for k in cred_obj.keys() if isinstance(k, str))
            checks.append({
                "id": "dns.alidns.credential",
                "name": "AliDNS 运行时托管凭据就绪度",
                "category": "security",
                "status": "pass" if (has_id and has_sec) else "warning",
                "value": "已就绪" if (has_id and has_sec) else "缺少必要键",
                "detail": f"运行时 0600 凭据文件已物化，键名：{', '.join(key_names[:6]) or '空'}（值不回显）",
            })
        else:
            checks.append({
                "id": "dns.alidns.credential",
                "name": "AliDNS 运行时托管凭据就绪度",
                "category": "security",
                "status": "warning",
                "value": "格式异常",
                "detail": "托管凭据内容不是键值 JSON 对象",
            })
    except Exception:
        checks.append({
            "id": "dns.alidns.credential",
            "name": "AliDNS 运行时托管凭据就绪度",
            "category": "security",
            "status": "warning",
            "value": "不可读",
            "detail": "无法读取运行时凭据文件",
        })

risk_total = open_recursion_hits + axfr_exposed_hits + query_failed_hits + config_error_hits
max_latency = round(max(latencies), 1) if latencies else 0.0

metrics = [
    {"key": "dns_risk_total", "label": "DNS 风险项合计", "value": risk_total, "unit": "项", "status": "critical" if risk_total >= 3 else ("warning" if risk_total >= 1 else "pass")},
    {"key": "dns_zones_total", "label": "核验域名/Zone", "value": len(resources), "unit": "个", "status": "pass"},
    {"key": "dns_latency_max_ms", "label": "最高解析延迟", "value": max_latency, "unit": "ms", "status": "critical" if max_latency >= 1000 else ("warning" if max_latency >= LATENCY_WARN_MS else "pass")},
    {"key": "dns_open_recursion", "label": "开放递归告警", "value": open_recursion_hits, "unit": "项", "status": "critical" if open_recursion_hits else "pass"},
    {"key": "dns_axfr_exposed", "label": "AXFR 暴露数", "value": axfr_exposed_hits, "unit": "个", "status": "critical" if axfr_exposed_hits else "pass"},
    {"key": "dns_query_failed", "label": "解析失败数", "value": query_failed_hits, "unit": "个", "status": "critical" if query_failed_hits >= 2 else ("warning" if query_failed_hits == 1 else "pass")},
]

charts = [
    {
        "title": "DNS 风险分类分布",
        "type": "bar",
        "unit": "项",
        "series": [
            {"label": "开放递归", "value": open_recursion_hits, "status": "critical" if open_recursion_hits else "pass"},
            {"label": "AXFR 暴露", "value": axfr_exposed_hits, "status": "critical" if axfr_exposed_hits else "pass"},
            {"label": "配置语法错", "value": config_error_hits, "status": "critical" if config_error_hits else "pass"},
            {"label": "解析失败", "value": query_failed_hits, "status": "warning" if query_failed_hits else "pass"},
        ],
    }
]

overall = "critical" if any(c["status"] == "critical" for c in checks) else ("warning" if any(c["status"] == "warning" for c in checks) or coverage_gaps else "pass")
summary = f"核验 {len(resources)} 个域名/Zone，检出 {risk_total} 项 DNS 风险，最高延迟 {max_latency}ms" + (f"（含 {len(coverage_gaps)} 项覆盖说明）" if coverage_gaps else "")

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
emit(["## DNS 基础设施治理巡检", "", f"- **综合结论**：{overall} — {summary}"], payload)
PY
