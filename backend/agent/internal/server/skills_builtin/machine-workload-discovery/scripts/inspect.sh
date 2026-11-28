#!/usr/bin/env sh
set -eu

tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 机器部署分析与自动打标报告

- **综合结论**：warning — 缺少 python3，未能执行部署挖掘

MD
  cat <<'MD'
```json
MD
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少 python3，未完成部署分析与标签挖掘","metrics":[],"checks":[{"id":"discovery.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3，进程、容器与应用元数据均未采集"}],"workloads":[],"suggested_tags":[]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")"
  cat <<'MD'
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import platform
import re
import shutil
import subprocess
import zipfile
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024
HARD_CAP_BYTES = 256 * 1024
TRIM_LIST_KEYS = ("workloads", "sites", "suggested_tags", "checks")

def _env_bool(name, default):
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    return raw.strip().lower() in ("1", "true", "yes", "on")

SCAN_CONTAINERS = _env_bool("DOOPS_INPUT_SCAN_CONTAINERS", True)
SCAN_WEBROOTS = _env_bool("DOOPS_INPUT_SCAN_WEBROOTS", True)
INCLUDE_INSTALLED_RUNTIMES = _env_bool("DOOPS_INPUT_INCLUDE_INSTALLED_RUNTIMES", False)

def target_identity():
    return {
        "cluster": os.environ.get("DOOPS_TARGET_CLUSTER") or None,
        "instance": os.environ.get("DOOPS_TARGET_INSTANCE") or None,
    }

def run_cmd(cmd, timeout=5):
    try:
        r = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=timeout,
        )
        return r.returncode, (r.stdout or "").strip()
    except Exception:
        return 1, ""

def sanitize_text(s, limit=180):
    if not s:
        return ""
    s = re.sub(r"://[^/\s@]+@", "://***@", s)
    s = re.sub(r"(?i)(--?(?:password|passwd|pass|pwd|secret|token|api[-_]?key|access[-_]?key)[=\s]+)\S+", r"\1***", s)
    s = re.sub(r"(?i)\b((?:password|passwd|secret|token|apikey|api_key)\s*[=:]\s*)\S+", r"\1***", s)
    s = " ".join(s.split())
    return s[:limit] + ("..." if len(s) > limit else "")

def major_minor(ver):
    if not ver or ver == "unknown":
        return ""
    m = re.search(r"(\d+)(?:\.(\d+))?", str(ver))
    if not m:
        return ""
    maj, mn = m.group(1), m.group(2)
    return f"{maj}.{mn}" if mn is not None else maj

def java_major(ver):
    if not ver or ver == "unknown":
        return ""
    m = re.search(r'"?(?:1\.)?(\d+)(?:\.\d+)?', str(ver))
    return m.group(1) if m else ""

# Match Gateway machine_inventory_store.go:
# ^[\p{L}\p{N}][\p{L}\p{M}\p{N}._-]*(?:=[\p{L}\p{N}][\p{L}\p{M}\p{N}._-]*)?$
# lowercase, <= 32 chars, no credential words, max 20 tags.
SENSITIVE_TAG_WORDS = (
    "password", "passwd", "secret", "token", "apikey", "api_key",
    "private", "credential", "bearer", "accesskey", "auth",
)

def slug_token(raw, max_len=20):
    s = str(raw or "").strip().lower()
    s = re.sub(r"[^a-z0-9._-]+", "-", s)
    s = re.sub(r"[-._]{2,}", "-", s).strip("-._")
    if not s or not s[0].isalnum():
        return ""
    return s[:max_len].rstrip("-._")

def make_valid_tag(key, val):
    k = slug_token(key, 12)
    v = slug_token(val, 24)
    if not k or not v:
        return ""
    tag = f"{k}={v}"
    if len(tag) > 32:
        avail = 32 - len(k) - 1
        v2 = v[:avail].rstrip("-._")
        if not v2 or not v2[0].isalnum():
            return ""
        tag = f"{k}={v2}"
    low = tag.lower()
    if any(w in low for w in SENSITIVE_TAG_WORDS):
        return ""
    if not re.match(r"^[a-z0-9][a-z0-9._-]*=[a-z0-9][a-z0-9._-]*$", tag):
        return ""
    return tag

# --- Data structures ---
workloads = []
workload_seen = {}
sites = []
coverage_gaps = []
roles = set()

def add_workload(category, name, version, runtime_stack, run_mode, ports, evidence, tag_key=None, tag_val=None, confidence="high", source="process"):
    norm_name = str(name or "").strip()
    if not norm_name:
        return
    ver = str(version or "unknown").strip() or "unknown"
    dedup = (category, norm_name.lower(), run_mode)
    port_str = ",".join(sorted({str(p) for p in (ports or []) if str(p).isdigit()})) if isinstance(ports, (list, set, tuple)) else str(ports or "")
    ev = sanitize_text(evidence, 160)
    if dedup in workload_seen:
        idx = workload_seen[dedup]
        cur = workloads[idx]
        if cur["version"] == "unknown" and ver != "unknown":
            cur["version"] = ver
        if port_str:
            merged_ports = sorted({p for p in (cur["ports"].split(",") + port_str.split(",")) if p and p.isdigit()})
            cur["ports"] = ",".join(merged_ports)
        if tag_key and tag_val and not cur.get("_tag_val"):
            cur["_tag_key"] = tag_key
            cur["_tag_val"] = tag_val
        return
    workload_seen[dedup] = len(workloads)
    workloads.append({
        "category": category,
        "name": norm_name,
        "version": ver,
        "runtime_stack": runtime_stack or "native",
        "run_mode": run_mode,
        "ports": port_str or "—",
        "evidence": ev,
        "status": "pass",
        "_tag_key": tag_key,
        "_tag_val": tag_val,
        "_confidence": confidence,
        "_source": source,
    })

# --- 1. OS detection ---
os_id, os_ver, os_pretty = "", "", ""
if os.path.isfile("/etc/os-release"):
    try:
        with open("/etc/os-release", "r", encoding="utf-8", errors="replace") as f:
            for line in f:
                line = line.strip()
                if line.startswith("ID="):
                    os_id = line.split("=", 1)[1].strip().strip('"').lower()
                elif line.startswith("VERSION_ID="):
                    os_ver = line.split("=", 1)[1].strip().strip('"')
                elif line.startswith("PRETTY_NAME="):
                    os_pretty = line.split("=", 1)[1].strip().strip('"')
    except Exception:
        pass
if not os_id:
    os_id = platform.system().lower()
    os_ver = major_minor(platform.release())
os_tag_val = f"{os_id}-{major_minor(os_ver)}" if (os_id and major_minor(os_ver)) else os_id

# --- 2. Listening ports mapped to PID ---
pid_ports = {}
port_tool = None
if shutil.which("ss"):
    port_tool = "ss"
    rc, out = run_cmd(["ss", "-tlnp"], timeout=5)
    if rc == 0:
        for line in out.splitlines():
            pm = re.search(r":(\d+)\s", line)
            if not pm:
                continue
            port = pm.group(1)
            for pidm in re.finditer(r"pid=(\d+)", line):
                pid_ports.setdefault(pidm.group(1), set()).add(port)
elif shutil.which("netstat"):
    port_tool = "netstat"
    rc, out = run_cmd(["netstat", "-tlnp"], timeout=5)
    if rc == 0:
        for line in out.splitlines():
            pm = re.search(r":(\d+)\s", line)
            pidm = re.search(r"\s(\d+)/", line)
            if pm and pidm:
                pid_ports.setdefault(pidm.group(1), set()).add(pm.group(1))
else:
    coverage_gaps.append("未检测到 ss/netstat，无法关联进程监听端口（不影响进程与元数据识别）")

# --- 3. Local version helper cache ---
ver_cache = {}
def probe_binary_version(key, cmd, regex):
    if key in ver_cache:
        return ver_cache[key]
    if not shutil.which(cmd[0]):
        ver_cache[key] = "unknown"
        return "unknown"
    rc, out = run_cmd(cmd, timeout=4)
    if out:
        m = re.search(regex, out, re.I)
        if m:
            ver_cache[key] = m.group(1)
            return m.group(1)
    ver_cache[key] = "unknown"
    return "unknown"

def detect_java_version(exe_hint=None):
    bins = [exe_hint] if (exe_hint and os.path.isfile(exe_hint)) else []
    bins.append("java")
    for b in bins:
        if not b:
            continue
        v = probe_binary_version("java:" + b, [b, "-version"], r'(?:version\s+|openjdk\s+|java\s+)"?([0-9][0-9._-]+)"?')
        if v != "unknown":
            return v
    return "unknown"

def detect_python_version(exe_hint=None):
    bins = [exe_hint] if (exe_hint and os.path.isfile(exe_hint)) else []
    bins.extend(["python3", "python"])
    for b in bins:
        if not b:
            continue
        v = probe_binary_version("py:" + b, [b, "--version"], r"Python\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)")
        if v != "unknown":
            return v
    return "unknown"

def detect_php_version():
    for b in ("php", "php-fpm", "php-fpm8.3", "php-fpm8.2", "php-fpm8.1", "php-fpm7.4"):
        v = probe_binary_version("php:" + b, [b, "-v"], r"PHP\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)")
        if v != "unknown":
            return v
    return "unknown"

def detect_node_version():
    return probe_binary_version("node", ["node", "-v"], r"v?([0-9]+\.[0-9]+(?:\.[0-9]+)?)")

def detect_go_version():
    return probe_binary_version("go", ["go", "version"], r"go([0-9]+\.[0-9]+(?:\.[0-9]+)?)")

def detect_dotnet_version():
    return probe_binary_version("dotnet", ["dotnet", "--version"], r"([0-9]+\.[0-9]+(?:\.[0-9]+)?)")

def detect_ruby_version():
    return probe_binary_version("ruby", ["ruby", "-v"], r"ruby\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)")

# --- 4. Process table fingerprinting (ps first, pure-Python /proc fallback for minimal images) ---
my_pid = str(os.getpid())
ppid = str(os.getppid())
procs = []
ps_ok = False
if shutil.which("ps"):
    ps_rc, ps_out = run_cmd(["ps", "-eo", "pid=,comm=,args="], timeout=6)
    if ps_rc == 0:
        ps_ok = True
        for line in ps_out.splitlines():
            parts = line.strip().split(None, 2)
            if len(parts) < 2:
                continue
            pid, comm = parts[0], parts[1]
            args = parts[2] if len(parts) > 2 else comm
            if pid in (my_pid, ppid) or "inspect.sh" in args or "machine-workload-discovery" in args:
                continue
            procs.append((pid, comm, args))
if not ps_ok and os.path.isdir("/proc"):
    try:
        for entry in os.listdir("/proc"):
            if not entry.isdigit() or entry in (my_pid, ppid):
                continue
            try:
                raw_cmd = open(f"/proc/{entry}/cmdline", "rb").read(8192)
                raw_comm = open(f"/proc/{entry}/comm", "r", encoding="utf-8", errors="replace").read().strip()
            except Exception:
                continue
            argv = [p.decode("utf-8", errors="replace") for p in raw_cmd.split(b"\x00") if p]
            args = " ".join(argv) if argv else raw_comm
            comm = raw_comm or (os.path.basename(argv[0]) if argv else "")
            if not comm or "inspect.sh" in args or "machine-workload-discovery" in args:
                continue
            procs.append((entry, comm, args))
        ps_ok = True
    except Exception:
        pass
if not ps_ok:
    coverage_gaps.append("无法通过 ps 或 /proc 采集运行中进程")

# Database / Middleware / Web process signatures
DB_PROCS = [
    ("mariadb", r"\bmariadbd\b", ["mariadbd", "--version"], r"Ver\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "database"),
    ("mysql", r"\bmysqld\b", ["mysqld", "--version"], r"Ver\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "database"),
    ("postgresql", r"\b(?:postgres|postmaster)\b", ["postgres", "--version"], r"([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "database"),
    ("redis", r"\bredis-server\b", ["redis-server", "-v"], r"v=([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "cache"),
    ("valkey", r"\bvalkey-server\b", ["valkey-server", "-v"], r"v=([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "cache"),
    ("mongodb", r"\bmongod\b", ["mongod", "--version"], r"db version v([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "database"),
    ("clickhouse", r"\bclickhouse-server\b", ["clickhouse-server", "--version"], r"version\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "database"),
    ("elasticsearch", r"org\.elasticsearch\.bootstrap|elasticsearch", [], "", "database"),
    ("opensearch", r"org\.opensearch\.bootstrap|opensearch", [], "", "database"),
    ("dm", r"\bdmserver\b", [], "", "database"),
    ("kingbase", r"\bkingbase\b", [], "", "database"),
    ("tidb", r"\btidb-server\b", ["tidb-server", "-V"], r"v([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "database"),
    ("influxdb", r"\binfluxd\b", ["influxd", "version"], r"v?([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "database"),
    ("minio", r"\bminio\b", ["minio", "--version"], r"RELEASE\.([0-9-]+)", "storage"),
]

WEB_MQ_PROCS = [
    ("web", "openresty", r"\bopenresty\b", ["openresty", "-v"], r"openresty/([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "web"),
    ("web", "nginx", r"\bnginx\b", ["nginx", "-v"], r"nginx/([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "web"),
    ("web", "apache", r"\b(?:apache2|httpd)\b", ["apache2", "-v"], r"Apache/([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "web"),
    ("web", "caddy", r"\bcaddy\b", ["caddy", "version"], r"v([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "web"),
    ("web", "haproxy", r"\bhaproxy\b", ["haproxy", "-v"], r"version\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "web"),
    ("web", "traefik", r"\btraefik\b", ["traefik", "version"], r"Version:\s*([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "web"),
    ("middleware", "rabbitmq", r"\bbeam\.smp\b.*rabbit|\brabbitmq\b", ["rabbitmqctl", "version"], r"([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "mq"),
    ("middleware", "kafka", r"kafka\.Kafka", [], "", "mq"),
    ("middleware", "rocketmq", r"org\.apache\.rocketmq", [], "", "mq"),
    ("middleware", "nats", r"\bnats-server\b", ["nats-server", "-v"], r"v([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "mq"),
    ("middleware", "emqx", r"\bemqx\b", [], "", "mq"),
    ("middleware", "nacos", r"nacos-server|com\.alibaba\.nacos", [], "", "middleware"),
    ("middleware", "etcd", r"\betcd\b", ["etcd", "--version"], r"etcd Version:\s*([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "k8s-node"),
    ("middleware", "zookeeper", r"QuorumPeerMain", [], "", "middleware"),
    ("middleware", "prometheus", r"\bprometheus\b", ["prometheus", "--version"], r"version\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "monitor"),
    ("middleware", "grafana", r"\bgrafana(?:-server)?\b", ["grafana-server", "-v"], r"Version\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "monitor"),
    ("middleware", "zabbix", r"\bzabbix_server\b", ["zabbix_server", "-V"], r"([0-9]+\.[0-9]+(?:\.[0-9]+)?)", "monitor"),
]

active_cwds = set()
active_runtimes = set()

def inspect_jar_manifest(jar_path):
    title, ver, boot_ver = "", "", ""
    if not jar_path or not os.path.isfile(jar_path):
        return title, ver, boot_ver
    try:
        with zipfile.ZipFile(jar_path, "r") as zf:
            if "META-INF/MANIFEST.MF" in zf.namelist():
                raw = zf.read("META-INF/MANIFEST.MF").decode("utf-8", errors="replace")
                for ln in raw.splitlines():
                    if ln.startswith("Implementation-Title:"):
                        title = ln.split(":", 1)[1].strip()
                    elif ln.startswith("Implementation-Version:"):
                        ver = ln.split(":", 1)[1].strip()
                    elif ln.startswith("Spring-Boot-Version:"):
                        boot_ver = ln.split(":", 1)[1].strip()
    except Exception:
        pass
    return title, ver, boot_ver

for pid, comm, args in procs:
    low = (comm + " " + args).lower()
    ports = pid_ports.get(pid, set())
    try:
        cwd = os.readlink(f"/proc/{pid}/cwd")
        if cwd and cwd not in ("/", "/root", "/tmp", "/var/tmp"):
            active_cwds.add(cwd)
    except Exception:
        cwd = ""

    # K8s node / AI GPU role hints
    if re.search(r"\bkubelet\b|\bkube-apiserver\b", low):
        roles.add("k8s-node")
    if re.search(r"\bvllm\b|\bollama\b|\btritonserver\b|\bcomfyui\b", low):
        roles.add("ai-gpu")

    # Databases
    matched_db = False
    for name, pat, vcmd, vreg, role_name in DB_PROCS:
        if re.search(pat, args, re.I) or re.search(pat, comm, re.I):
            ver = probe_binary_version(name, vcmd, vreg) if vcmd else "unknown"
            stack = "java" if name in ("elasticsearch", "opensearch") else "native"
            add_workload("database", name, ver, stack, "process", ports, f"pid={pid} {comm}",
                         tag_key="db", tag_val=f"{name}-{major_minor(ver)}" if major_minor(ver) else name,
                         confidence="high", source="process")
            roles.add(role_name)
            if stack == "java":
                active_runtimes.add("java")
            matched_db = True
            break
    if matched_db:
        continue

    # Web & Middleware
    for cat, name, pat, vcmd, vreg, role_name in WEB_MQ_PROCS:
        if re.search(pat, args, re.I) or re.search(pat, comm, re.I):
            ver = probe_binary_version(name, vcmd, vreg) if vcmd else "unknown"
            if name == "apache" and ver == "unknown":
                ver = probe_binary_version("httpd", ["httpd", "-v"], vreg)
            stack = "java" if name in ("kafka", "rocketmq", "nacos", "zookeeper") else "native"
            tk = "web" if cat == "web" else ("mq" if role_name == "mq" else "app")
            add_workload(cat, name, ver, stack, "process", ports, f"pid={pid} {comm}",
                         tag_key=tk, tag_val=f"{name}-{major_minor(ver)}" if major_minor(ver) else name,
                         confidence="high", source="process")
            roles.add(role_name)
            if stack == "java":
                active_runtimes.add("java")
            break

    # Java runtime + Tomcat / JAR apps
    if comm == "java" or re.search(r"(^|/)java(\s|$)", args):
        active_runtimes.add("java")
        jver = detect_java_version()
        jmaj = java_major(jver)
        add_workload("runtime", "java", jver, "java", "process", ports, f"pid={pid} java",
                     tag_key="runtime", tag_val=f"java-{jmaj}" if jmaj else "java",
                     confidence="high", source="process")
        if "catalina" in low or "tomcat" in low:
            roles.add("web")
            tver = "unknown"
            m = re.search(r"tomcat[-_]?(\d+\.\d+(?:\.\d+)?)", low)
            if m:
                tver = m.group(1)
            add_workload("web", "tomcat", tver, "java", "process", ports, f"pid={pid} catalina",
                         tag_key="web", tag_val=f"tomcat-{major_minor(tver)}" if major_minor(tver) else "tomcat",
                         confidence="high", source="process")
        jm = re.search(r"-jar\s+([^\s'\"]+\.jar)", args)
        if jm:
            jar_ref = jm.group(1)
            jar_path = jar_ref if os.path.isabs(jar_ref) else (os.path.join(cwd, jar_ref) if cwd else jar_ref)
            title, mver, boot_ver = inspect_jar_manifest(jar_path)
            base = os.path.basename(jar_ref)[:-4]
            bm = re.match(r"^([a-zA-Z0-9_.-]+?)[-_]v?(\d+\.\d+(?:\.\d+)?(?:[-._A-Za-z0-9]*)?)$", base)
            app_name = title or (bm.group(1) if bm else base)
            app_ver = mver or (bm.group(2) if bm else "unknown")
            ev = f"jar={os.path.basename(jar_ref)}" + (f" (spring-boot {boot_ver})" if boot_ver else "")
            if app_name.lower() not in ("nacos-server", "elasticsearch", "opensearch"):
                roles.add("app")
                add_workload("business_app", app_name, app_ver, "java", "process", ports, ev,
                             tag_key="app", tag_val=f"{app_name}-{major_minor(app_ver)}" if major_minor(app_ver) else app_name,
                             confidence="high", source="jar-manifest")

    # Python business / server processes
    if re.search(r"\b(gunicorn|uvicorn|celery|uwsgi|daphne|hypercorn|odoo|vllm|airflow|superset|streamlit|gradio)\b", low) or (
        re.match(r"^python3?(\.\d+)?$", comm) and (ports or re.search(r"(manage\.py|app\.py|main\.py|server\.py|wsgi|asgi|flask|fastapi|django)", low))
    ):
        active_runtimes.add("python")
        pver = detect_python_version()
        add_workload("runtime", "python", pver, "python", "process", ports, f"pid={pid} {comm}",
                     tag_key="runtime", tag_val=f"python-{major_minor(pver)}" if major_minor(pver) else "python",
                     confidence="high", source="process")
        roles.add("app")
        for fw in ("odoo", "vllm", "airflow", "superset", "django", "fastapi", "flask", "celery"):
            if fw in low:
                add_workload("business_app", fw, "unknown", "python", "process", ports, f"pid={pid} {sanitize_text(args, 90)}",
                             tag_key="app", tag_val=fw, confidence="medium", source="process")
                break

    # PHP processes
    if re.search(r"\bphp(?:-fpm\d*(?:\.\d+)?)?\b", comm) or "php-fpm" in low:
        active_runtimes.add("php")
        phpv = detect_php_version()
        add_workload("runtime", "php", phpv, "php", "process", ports, f"pid={pid} {comm}",
                     tag_key="runtime", tag_val=f"php-{major_minor(phpv)}" if major_minor(phpv) else "php",
                     confidence="high", source="process")
        roles.add("app")

    # Node.js / Bun processes
    if comm in ("node", "nodejs", "bun", "deno", "next-server") or "next-server" in low or re.search(r"\bpm2\b", low):
        active_runtimes.add("node")
        nver = detect_node_version()
        nmaj = major_minor(nver).split(".")[0] if major_minor(nver) else ""
        add_workload("runtime", "node", nver, "node", "process", ports, f"pid={pid} {comm}",
                     tag_key="runtime", tag_val=f"node-{nmaj}" if nmaj else "node",
                     confidence="high", source="process")
        roles.add("app")

    # .NET / Ruby processes
    if comm == "dotnet" or re.search(r"\bdotnet\b.*\.dll", low):
        active_runtimes.add("dotnet")
        dver = detect_dotnet_version()
        add_workload("runtime", "dotnet", dver, "dotnet", "process", ports, f"pid={pid} dotnet",
                     tag_key="runtime", tag_val=f"dotnet-{major_minor(dver)}" if major_minor(dver) else "dotnet",
                     confidence="high", source="process")
        roles.add("app")
    if comm in ("ruby", "puma", "unicorn") or "rails" in low:
        active_runtimes.add("ruby")
        rver = detect_ruby_version()
        add_workload("runtime", "ruby", rver, "ruby", "process", ports, f"pid={pid} {comm}",
                     tag_key="runtime", tag_val=f"ruby-{major_minor(rver)}" if major_minor(rver) else "ruby",
                     confidence="high", source="process")
        roles.add("app")

# --- 5. Optional container image fingerprinting ---
CONTAINER_RULES = [
    ("database", "mysql", r"(?:^|/)(?:mysql|percona)(?:$|:)", "db", "database"),
    ("database", "mariadb", r"(?:^|/)mariadb(?:$|:)", "db", "database"),
    ("database", "postgresql", r"(?:^|/)(?:postgres|postgresql|postgis|timescaledb|pgvector)(?:$|:)", "db", "database"),
    ("database", "redis", r"(?:^|/)(?:redis|valkey)(?:$|:)", "db", "cache"),
    ("database", "mongodb", r"(?:^|/)(?:mongo|mongodb)(?:$|:)", "db", "database"),
    ("database", "elasticsearch", r"(?:^|/)(?:elasticsearch|opensearch)(?:$|:)", "db", "database"),
    ("database", "clickhouse", r"(?:^|/)clickhouse", "db", "database"),
    ("database", "milvus", r"(?:^|/)milvus", "db", "database"),
    ("database", "minio", r"(?:^|/)minio(?:$|:)", "db", "storage"),
    ("web", "nginx", r"(?:^|/)(?:nginx|openresty|tengine)(?:$|:)", "web", "web"),
    ("web", "apache", r"(?:^|/)(?:httpd|apache)(?:$|:)", "web", "web"),
    ("web", "caddy", r"(?:^|/)caddy(?:$|:)", "web", "web"),
    ("web", "traefik", r"(?:^|/)traefik(?:$|:)", "web", "web"),
    ("middleware", "rabbitmq", r"(?:^|/)rabbitmq(?:$|:)", "mq", "mq"),
    ("middleware", "kafka", r"(?:^|/)kafka(?:$|:)", "mq", "mq"),
    ("middleware", "nacos", r"(?:^|/)nacos", "app", "middleware"),
    ("business_app", "gitlab", r"(?:^|/)gitlab", "app", "app"),
    ("business_app", "harbor", r"(?:^|/)harbor", "app", "app"),
    ("business_app", "jenkins", r"(?:^|/)jenkins", "app", "app"),
    ("business_app", "gitea", r"(?:^|/)gitea", "app", "app"),
    ("business_app", "wordpress", r"(?:^|/)wordpress", "app", "app"),
    ("business_app", "grafana", r"(?:^|/)grafana", "app", "monitor"),
    ("business_app", "prometheus", r"(?:^|/)prometheus", "app", "monitor"),
    ("business_app", "dify", r"(?:^|/)dify", "app", "ai-gpu"),
    ("business_app", "ollama", r"(?:^|/)ollama", "app", "ai-gpu"),
    ("business_app", "vllm", r"(?:^|/)vllm", "app", "ai-gpu"),
    ("business_app", "jumpserver", r"(?:^|/)jumpserver|jms_", "app", "app"),
    ("business_app", "halo", r"(?:^|/)halo", "app", "app"),
    ("business_app", "odoo", r"(?:^|/)odoo", "app", "app"),
]

def parse_image_ref(image):
    clean = re.sub(r"@sha256:[a-f0-9]+$", "", str(image or "").strip())
    if ":" in clean.split("/")[-1]:
        repo, tag = clean.rsplit(":", 1)
    else:
        repo, tag = clean, "latest"
    short = repo.split("/")[-1]
    ver = "unknown"
    vm = re.search(r"(\d+\.\d+(?:\.\d+)?)", tag)
    if vm:
        ver = vm.group(1)
    elif tag and tag not in ("latest", "stable", "alpine", "slim", "main", "master"):
        ver = tag[:20]
    return repo, short, ver, tag

container_cli = None
if SCAN_CONTAINERS:
    for cli in ("docker", "nerdctl", "podman"):
        if shutil.which(cli):
            rc, out = run_cmd([cli, "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Ports}}"], timeout=6)
            if rc == 0:
                container_cli = cli
                for line in out.splitlines():
                    parts = line.split("\t")
                    if len(parts) < 2:
                        continue
                    cname, image = parts[0].strip(), parts[1].strip()
                    cports_raw = parts[2] if len(parts) > 2 else ""
                    cports = re.findall(r":(\d+)->", cports_raw)
                    repo, short, ver, tag = parse_image_ref(image)
                    matched = False
                    for cat, name, pat, tk, rname in CONTAINER_RULES:
                        if re.search(pat, repo, re.I):
                            stack = "php" if name == "wordpress" else ("python" if name in ("dify", "vllm", "odoo") else "container")
                            add_workload(cat, name, ver, stack, "container", cports, f"{cli}:{cname} ({short}:{tag})",
                                         tag_key=tk, tag_val=f"{name}-{major_minor(ver)}" if major_minor(ver) else name,
                                         confidence="high", source=f"{cli}-image")
                            roles.add(rname)
                            matched = True
                            break
                    if not matched and short and not re.match(r"^(pause|coredns|kube-|calico|flannel|cilium|doops-agent)", short, re.I):
                        add_workload("business_app", short, ver, "container", "container", cports, f"{cli}:{cname} ({short}:{tag})",
                                     tag_key="app", tag_val=f"{short}-{major_minor(ver)}" if major_minor(ver) else short,
                                     confidence="medium", source=f"{cli}-image")
                        roles.add("app")
                break
            else:
                coverage_gaps.append(f"检测到 {cli} 但无法读取容器列表（可能无权限或 daemon 未运行）")
                break

# --- 6. Safe webroot & Nginx metadata discovery (NEVER read .env / configs with secrets) ---
scan_dirs = set(active_cwds)
if SCAN_WEBROOTS:
    for ng_dir in ("/etc/nginx/conf.d", "/etc/nginx/sites-enabled"):
        if not os.path.isdir(ng_dir):
            continue
        try:
            for fn in sorted(os.listdir(ng_dir))[:40]:
                fp = os.path.join(ng_dir, fn)
                if not os.path.isfile(fp):
                    continue
                try:
                    raw = open(fp, "r", encoding="utf-8", errors="replace").read(32768)
                except Exception:
                    continue
                snames = re.findall(r"^\s*server_name\s+([^;#]+);", raw, re.M)
                roots = re.findall(r"^\s*root\s+([^;#]+);", raw, re.M)
                proxies = re.findall(r"^\s*proxy_pass\s+https?://([^/;\s]+)", raw, re.M)
                domains = []
                for sn in snames:
                    for tok in sn.split():
                        if tok not in ("_", "localhost", "127.0.0.1") and not tok.startswith("~"):
                            domains.append(tok.strip())
                for rt in roots:
                    rpath = rt.strip().strip("'\"")
                    if os.path.isdir(rpath):
                        scan_dirs.add(rpath)
                if domains or proxies:
                    sites.append({
                        "config": fn,
                        "domains": sorted(set(domains))[:8],
                        "roots": [r.strip().strip("'\"") for r in roots[:3]],
                        "upstreams": sorted(set(sanitize_text(p, 60) for p in proxies))[:5],
                    })
        except Exception:
            pass

    for base_root in ("/var/www", "/opt", "/srv", "/data/apps"):
        if not os.path.isdir(base_root):
            continue
        try:
            entries = sorted(os.listdir(base_root))[:30]
            for name in entries:
                p = os.path.join(base_root, name)
                if os.path.isdir(p) and name not in ("html", "lost+found", "containerd", "cni", "rh"):
                    scan_dirs.add(p)
            if os.path.isdir(os.path.join(base_root, "html")):
                scan_dirs.add(os.path.join(base_root, "html"))
        except Exception:
            pass

def inspect_app_dir(d):
    if not d or not os.path.isdir(d):
        return
    # 6a. WordPress
    wp_ver_file = os.path.join(d, "wp-includes", "version.php")
    if os.path.isfile(wp_ver_file):
        try:
            txt = open(wp_ver_file, "r", encoding="utf-8", errors="replace").read(4096)
            m = re.search(r"\$wp_version\s*=\s*['\"]([^'\"]+)['\"]", txt)
            ver = m.group(1) if m else "unknown"
            add_workload("business_app", "wordpress", ver, "php", "webroot", [], f"dir={d}",
                         tag_key="app", tag_val=f"wordpress-{major_minor(ver)}" if major_minor(ver) else "wordpress",
                         confidence="high", source="webroot-manifest")
            active_runtimes.add("php")
            roles.add("app")
            return
        except Exception:
            pass

    # 6b. package.json (Node.js / Next.js / Vue / NestJS / Strapi)
    pkg_json = os.path.join(d, "package.json")
    if os.path.isfile(pkg_json):
        try:
            data = json.loads(open(pkg_json, "r", encoding="utf-8", errors="replace").read(65536))
            if isinstance(data, dict):
                name = str(data.get("name") or os.path.basename(d)).strip()
                ver = str(data.get("version") or "unknown").strip()
                deps = {}
                for dk in ("dependencies", "devDependencies"):
                    if isinstance(data.get(dk), dict):
                        deps.update(data[dk])
                fw = ""
                for candidate, label in (("next", "next.js"), ("nuxt", "nuxt"), ("@nestjs/core", "nestjs"), ("@strapi/strapi", "strapi"), ("express", "express"), ("vue", "vue"), ("react", "react")):
                    if candidate in deps:
                        fw = label
                        break
                ev = f"package.json ({d})" + (f" · {fw}" if fw else "")
                add_workload("business_app", name, ver, "node", "webroot", [], ev,
                             tag_key="app", tag_val=f"{name}-{major_minor(ver)}" if major_minor(ver) else name,
                             confidence="high", source="package.json")
                roles.add("app")
        except Exception:
            pass

    # 6c. composer.json (PHP / Laravel / ThinkPHP / Symfony / Drupal)
    comp_json = os.path.join(d, "composer.json")
    if os.path.isfile(comp_json):
        try:
            data = json.loads(open(comp_json, "r", encoding="utf-8", errors="replace").read(65536))
            if isinstance(data, dict):
                raw_name = str(data.get("name") or os.path.basename(d)).strip()
                name = raw_name.split("/")[-1]
                ver = str(data.get("version") or "unknown").strip()
                req = data.get("require") if isinstance(data.get("require"), dict) else {}
                fw = ""
                for candidate, label in (("laravel/framework", "laravel"), ("topthink/framework", "thinkphp"), ("drupal/core", "drupal"), ("symfony/framework-bundle", "symfony")):
                    if candidate in req:
                        fw = label
                        if ver == "unknown" and isinstance(req.get(candidate), str):
                            vm = re.search(r"(\d+\.\d+)", req[candidate])
                            if vm:
                                ver = vm.group(1)
                        break
                ev = f"composer.json ({d})" + (f" · {fw}" if fw else "")
                add_workload("business_app", name, ver, "php", "webroot", [], ev,
                             tag_key="app", tag_val=f"{name}-{major_minor(ver)}" if major_minor(ver) else name,
                             confidence="high", source="composer.json")
                active_runtimes.add("php")
                roles.add("app")
        except Exception:
            pass

    # 6d. pyproject.toml (Python apps — safe regex extraction of [project] name & version only)
    pyproj = os.path.join(d, "pyproject.toml")
    if os.path.isfile(pyproj):
        try:
            txt = open(pyproj, "r", encoding="utf-8", errors="replace").read(32768)
            nm = re.search(r'(?m)^\s*name\s*=\s*["\']([^"\']+)["\']', txt)
            vm = re.search(r'(?m)^\s*version\s*=\s*["\']([^"\']+)["\']', txt)
            if nm:
                name = nm.group(1).strip()
                ver = vm.group(1).strip() if vm else "unknown"
                add_workload("business_app", name, ver, "python", "webroot", [], f"pyproject.toml ({d})",
                             tag_key="app", tag_val=f"{name}-{major_minor(ver)}" if major_minor(ver) else name,
                             confidence="high", source="pyproject.toml")
                active_runtimes.add("python")
                roles.add("app")
        except Exception:
            pass

for d in sorted(scan_dirs)[:50]:
    inspect_app_dir(d)

# --- 7. Optional installed-but-idle runtimes ---
if INCLUDE_INSTALLED_RUNTIMES:
    for rname, fn, fmt_fn in (
        ("java", detect_java_version, lambda v: f"java-{java_major(v)}" if java_major(v) else "java"),
        ("python", detect_python_version, lambda v: f"python-{major_minor(v)}" if major_minor(v) else "python"),
        ("php", detect_php_version, lambda v: f"php-{major_minor(v)}" if major_minor(v) else "php"),
        ("node", detect_node_version, lambda v: f"node-{major_minor(v).split('.')[0]}" if major_minor(v) else "node"),
        ("go", detect_go_version, lambda v: f"go-{major_minor(v)}" if major_minor(v) else "go"),
        ("dotnet", detect_dotnet_version, lambda v: f"dotnet-{major_minor(v)}" if major_minor(v) else "dotnet"),
        ("ruby", detect_ruby_version, lambda v: f"ruby-{major_minor(v)}" if major_minor(v) else "ruby"),
    ):
        if rname in active_runtimes:
            continue
        ver = fn()
        if ver != "unknown":
            add_workload("runtime", rname, ver, rname, "installed", [], f"本机已安装 {rname} {ver}",
                         tag_key="runtime", tag_val=fmt_fn(ver), confidence="medium", source="installed-binary")

# Ensure PHP version runtime row exists if discovered via webroot
if "php" in active_runtimes and not any(w["category"] == "runtime" and w["name"] == "php" for w in workloads):
    phpv = detect_php_version()
    add_workload("runtime", "php", phpv, "php", "webroot", [], "由站点应用栈推断",
                 tag_key="runtime", tag_val=f"php-{major_minor(phpv)}" if major_minor(phpv) else "php",
                 confidence="medium", source="webroot-manifest")

# --- 8. Build prioritized CMDB-compliant suggested_tags (max 20, <= 32 chars, zero secrets) ---
suggested_tags = []
seen_tags = set()

def push_tag(key, val, category, confidence, source, evidence):
    if len(suggested_tags) >= 20:
        return
    tag = make_valid_tag(key, val)
    if not tag or tag in seen_tags:
        return
    seen_tags.add(tag)
    suggested_tags.append({
        "tag": tag,
        "category": category,
        "confidence": confidence,
        "source": source,
        "evidence": sanitize_text(evidence, 120),
    })

# Priority 1: Host roles
for r in sorted(roles):
    push_tag("role", r, "role", "high", "inferred", f"根据活跃工作负载推断主机角色 {r}")

# Priority 2: OS
if os_tag_val:
    push_tag("os", os_tag_val, "os", "high", "os-release", os_pretty or os_tag_val)

# Priority 3: Databases -> Business apps -> Active runtimes -> Web/Middleware -> Installed runtimes
priority_order = {"database": 1, "business_app": 2, "runtime": 3, "web": 4, "middleware": 5}
ordered_workloads = sorted(
    workloads,
    key=lambda w: (
        0 if w["run_mode"] != "installed" else 1,
        priority_order.get(w["category"], 9),
        w["name"].lower(),
    ),
)
for w in ordered_workloads:
    tk = w.get("_tag_key")
    tv = w.get("_tag_val")
    if tk and tv:
        push_tag(tk, tv, w["category"], w.get("_confidence", "high"), w.get("_source", "process"),
                 f"{w['name']} {w['version']} ({w['run_mode']})")

# Strip internal fields before serializing
clean_workloads = [
    {k: v for k, v in w.items() if not k.startswith("_")}
    for w in ordered_workloads
]

db_count = sum(1 for w in clean_workloads if w["category"] == "database")
rt_count = sum(1 for w in clean_workloads if w["category"] == "runtime")
app_count = sum(1 for w in clean_workloads if w["category"] == "business_app")
web_mw_count = sum(1 for w in clean_workloads if w["category"] in ("web", "middleware"))

overall = "warning" if coverage_gaps else "pass"
summary = (
    f"识别 {len(clean_workloads)} 项部署组件（数据库 {db_count}、语言运行时 {rt_count}、业务系统 {app_count}、Web/中间件 {web_mw_count}），"
    f"挖掘出 {len(suggested_tags)} 个符合机器台账规范的推荐标签"
)
if coverage_gaps:
    summary += f"；存在 {len(coverage_gaps)} 项覆盖缺口"

metrics = [
    {"key": "workloads_total", "label": "已识别部署项", "value": len(clean_workloads), "unit": "项", "status": "pass"},
    {"key": "databases_detected", "label": "数据库与存储", "value": db_count, "unit": "个", "status": "pass"},
    {"key": "runtimes_detected", "label": "语言运行时", "value": rt_count, "unit": "种", "status": "pass"},
    {"key": "apps_detected", "label": "业务系统/应用", "value": app_count, "unit": "个", "status": "pass"},
    {"key": "suggested_tags_count", "label": "挖掘推荐标签", "value": len(suggested_tags), "unit": "个", "status": "pass"},
]

checks = [
    {
        "id": "discovery.os",
        "name": "操作系统识别",
        "category": "ops",
        "status": "pass",
        "value": os_pretty or os_tag_val or "linux",
        "detail": f"内核 {platform.system()} {platform.release()} · 推荐标签 os={os_tag_val}" if os_tag_val else platform.platform(),
    },
    {
        "id": "discovery.databases",
        "name": "数据库与数据存储识别",
        "category": "ops",
        "status": "pass",
        "value": f"{db_count} 个",
        "detail": "未发现运行中数据库服务" if db_count == 0 else ", ".join(f"{w['name']}({w['version']})" for w in clean_workloads if w["category"] == "database")[:240],
    },
    {
        "id": "discovery.runtimes",
        "name": "语言运行时识别",
        "category": "ops",
        "status": "pass",
        "value": f"{rt_count} 种",
        "detail": "未发现活跃语言运行时（可开启 INCLUDE_INSTALLED_RUNTIMES 盘点已安装环境）" if rt_count == 0 else ", ".join(f"{w['name']}({w['version']})" for w in clean_workloads if w["category"] == "runtime")[:240],
    },
    {
        "id": "discovery.apps",
        "name": "业务系统与应用指纹",
        "category": "ops",
        "status": "pass",
        "value": f"{app_count} 个",
        "detail": "未识别到标准应用清单（可用「AI 深度业务系统语义识别」进一步研判自定义目录）" if app_count == 0 else ", ".join(f"{w['name']}({w['version']})" for w in clean_workloads if w["category"] == "business_app")[:240],
    },
    {
        "id": "discovery.tags_compliance",
        "name": "机器台账标签合规校验",
        "category": "ops",
        "status": "pass",
        "value": f"{len(suggested_tags)} / 20 个",
        "detail": "全部推荐标签已通过小写、<=32 字符、key=value 字符集与防凭据泄露校验，可直接预览并同步至机器台账",
    },
]
if coverage_gaps:
    checks.append({
        "id": "discovery.coverage_gaps",
        "name": "采集覆盖缺口",
        "category": "runtime",
        "status": "warning",
        "value": f"{len(coverage_gaps)} 项",
        "detail": "；".join(coverage_gaps),
    })

charts = [
    {
        "title": "部署组件分类分布",
        "type": "bar",
        "unit": "项",
        "series": [
            {"label": "数据库/存储", "value": db_count, "status": "pass"},
            {"label": "语言运行时", "value": rt_count, "status": "pass"},
            {"label": "业务系统/应用", "value": app_count, "status": "pass"},
            {"label": "Web/中间件", "value": web_mw_count, "status": "pass"},
        ],
    }
]

payload = {
    "target": target_identity(),
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "applied_inputs": {
        "SCAN_CONTAINERS": SCAN_CONTAINERS,
        "SCAN_WEBROOTS": SCAN_WEBROOTS,
        "INCLUDE_INSTALLED_RUNTIMES": INCLUDE_INSTALLED_RUNTIMES,
    },
    "metrics": metrics,
    "checks": checks,
    "charts": charts,
    "workloads": clean_workloads[:120],
    "suggested_tags": suggested_tags,
    "sites": sites[:40],
    "coverage_gaps": coverage_gaps,
}

def _nbytes(obj):
    return len(json.dumps(obj, ensure_ascii=False).encode("utf-8"))

original = _nbytes(payload)
if original > MAX_PAYLOAD_BYTES:
    for limit in (40, 15, 5):
        for k in TRIM_LIST_KEYS:
            if isinstance(payload.get(k), list) and len(payload[k]) > limit:
                payload[k] = payload[k][:limit]
        if _nbytes(payload) <= MAX_PAYLOAD_BYTES:
            break
    payload["checks"].append({
        "id": "output.size",
        "name": "输出体积保护",
        "category": "runtime",
        "status": "warning",
        "value": f"{original}B",
        "detail": f"原始产物超过 {MAX_PAYLOAD_BYTES}B，已裁剪超长列表",
    })
payload["output_bytes"] = _nbytes(payload)

json_text = json.dumps(payload, ensure_ascii=False, indent=2)
if len(json_text.encode("utf-8")) > HARD_CAP_BYTES:
    json_text = json.dumps(payload, ensure_ascii=False)

tag_preview = ", ".join("`" + t["tag"] + "`" for t in suggested_tags) if suggested_tags else "暂无"
intro = [
    "## 机器部署分析与自动打标报告",
    "",
    f"- **综合状态**：`{overall}`",
    f"- **挖掘摘要**：{summary}",
    f"- **推荐标签**：{tag_preview}",
]
print("\n".join(intro) + "\n\n```json\n" + json_text + "\n```")
PY
