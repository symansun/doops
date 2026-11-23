#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  HOST=$(hostname 2>/dev/null || echo unknown)
  KERN=$(uname -sr 2>/dev/null || echo unknown)
  printf '## 虚机与物理机系统巡检\n\n- 主机：%s\n- 内核：%s\n\n```json\n{"status":"warning","summary":"缺少 python3，仅输出基础系统信息","host":{"hostname":"%s","kernel":"%s"},"metrics":[],"checks":[{"id":"runtime.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3"}]}\n```\n' "$HOST" "$KERN" "$HOST" "$KERN"
  exit 0
fi

python3 - <<'PY'
import hashlib
import json
import os
import platform
import socket
import subprocess
import time
from datetime import datetime, timezone

CPU_WARN, CPU_CRIT = 80.0, 90.0
MEM_WARN, MEM_CRIT = 80.0, 90.0
DISK_WARN, DISK_CRIT = 85.0, 95.0
ZOMBIE_WARN = 5

def read_text(path):
    try:
        with open(path, "r", encoding="utf-8", errors="ignore") as f:
            return f.read().strip()
    except Exception:
        return ""

def run_cmd(cmd, timeout=5):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, timeout=timeout)
        return r.stdout if r.returncode == 0 else ""
    except Exception:
        return ""

def parse_os_release():
    if platform.system() == "Darwin":
        ver = run_cmd(["sw_vers", "-productVersion"]).strip()
        return f"macOS {ver}".strip()
    raw = read_text("/etc/os-release")
    for line in raw.splitlines():
        if line.startswith("PRETTY_NAME="):
            return line.split("=", 1)[1].strip().strip('"')
    return platform.platform()

def read_cpu_ticks():
    raw = read_text("/proc/stat")
    for line in raw.splitlines():
        if line.startswith("cpu "):
            parts = [int(x) for x in line.split()[1:9] if x.isdigit()]
            if len(parts) >= 4:
                total = sum(parts)
                idle = parts[3] + (parts[4] if len(parts) > 4 else 0)
                return total, idle
    return None

def collect_cpu():
    cores = os.cpu_count() or 1
    cpu_pct = None
    if platform.system() == "Linux":
        t0 = read_cpu_ticks()
        time.sleep(0.25)
        t1 = read_cpu_ticks()
        if t0 and t1 and t1[0] > t0[0]:
            dt = t1[0] - t0[0]
            di = max(0, t1[1] - t0[1])
            cpu_pct = round(max(0.0, min(100.0, ((dt - di) / dt) * 100.0)), 2)
    else:
        out = run_cmd(["ps", "-A", "-o", "%cpu="])
        vals = []
        for line in out.splitlines():
            try:
                vals.append(float(line.strip()))
            except ValueError:
                pass
        if vals:
            cpu_pct = round(min(100.0, sum(vals) / max(1, cores)), 2)
    load1 = None
    try:
        load1 = round(os.getloadavg()[0], 2)
    except Exception:
        pass
    return cores, cpu_pct, load1

def collect_memory():
    if platform.system() == "Linux":
        info = {}
        for line in read_text("/proc/meminfo").splitlines():
            if ":" in line:
                k, v = line.split(":", 1)
                toks = v.strip().split()
                if toks and toks[0].isdigit():
                    info[k.strip()] = int(toks[0]) * 1024
        total = info.get("MemTotal", 0)
        avail = info.get("MemAvailable", info.get("MemFree", 0))
        if total > 0:
            used = max(0, total - avail)
            pct = round((used / total) * 100.0, 2)
            return total, used, pct, info.get("Slab", 0)
    elif platform.system() == "Darwin":
        raw_total = run_cmd(["sysctl", "-n", "hw.memsize"]).strip()
        if raw_total.isdigit():
            total = int(raw_total)
            vm = run_cmd(["vm_stat"])
            page_size = 4096
            for line in vm.splitlines():
                if "page size of" in line:
                    for tok in line.split():
                        if tok.isdigit():
                            page_size = int(tok)
                            break
            pages = {}
            for line in vm.splitlines():
                if ":" in line:
                    k, v = line.split(":", 1)
                    v = v.strip().rstrip(".")
                    if v.isdigit():
                        pages[k.strip()] = int(v)
            used_pages = pages.get("Pages active", 0) + pages.get("Pages wired down", 0) + pages.get("Pages occupied by compressor", 0)
            used = min(total, used_pages * page_size)
            pct = round((used / total) * 100.0, 2) if total > 0 else 0.0
            return total, used, pct, 0
    return 0, 0, None, 0

def collect_mounts_dedup():
    """Collect real mounts and deduplicate shared backing volumes (e.g. overlay + /dev/vda1)."""
    out = run_cmd(["df", "-P", "-k"])
    skip_fs = {"tmpfs", "devtmpfs", "shm", "proc", "sysfs", "cgroup", "cgroup2", "devfs", "map", "squashfs", "nsfs"}
    skip_mount_prefixes = ("/proc", "/sys", "/dev", "/run/secrets", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf")
    mounts = []
    seen_sources = set()
    seen_vol_sig = set()
    total_bytes = 0
    used_bytes = 0
    max_pct = 0.0
    worst_mount = "/"

    for line in out.splitlines()[1:]:
        parts = line.split()
        if len(parts) < 6:
            continue
        fs, blk_s, used_s, avail_s, _cap, mnt = parts[0], parts[1], parts[2], parts[3], parts[4], " ".join(parts[5:])
        if fs.lower() in skip_fs or any(mnt == p or mnt.startswith(p + "/") for p in skip_mount_prefixes):
            continue
        if not (blk_s.isdigit() and used_s.isdigit() and avail_s.isdigit()):
            continue
        tb = int(blk_s) * 1024
        ub = int(used_s) * 1024
        ab = int(avail_s) * 1024
        if tb <= 0:
            continue
        pct = round((ub / tb) * 100.0, 2)
        # Deduplicate same backing disk: identical source OR identical (total_bytes, avail_bytes) within 64 MiB bucket
        vol_sig = (tb, ab // (64 * 1024 * 1024))
        deduped = (fs in seen_sources) or (vol_sig in seen_vol_sig)
        if not deduped:
            seen_sources.add(fs)
            seen_vol_sig.add(vol_sig)
            total_bytes += tb
            used_bytes += ub
        if pct >= max_pct:
            max_pct = pct
            worst_mount = mnt
        mounts.append({
            "mount": mnt,
            "source": fs,
            "total_gib": round(tb / (1024 ** 3), 2),
            "used_gib": round(ub / (1024 ** 3), 2),
            "used_pct": pct,
            "shared_volume": deduped,
        })
    return mounts, total_bytes, used_bytes, round(max_pct, 2), worst_mount

def collect_uptime():
    if platform.system() == "Linux":
        raw = read_text("/proc/uptime")
        if raw:
            try:
                return round(float(raw.split()[0]), 1)
            except ValueError:
                pass
    return None

def collect_processes():
    out = run_cmd(["ps", "-eo", "stat="])
    lines = [ln.strip() for ln in out.splitlines() if ln.strip()]
    zombies = sum(1 for ln in lines if ln.startswith("Z"))
    return len(lines), zombies

def collect_sysctl():
    if platform.system() != "Linux":
        return []
    targets = [
        ("kernel.kptr_restrict", lambda v: v.isdigit() and int(v) >= 1, ">=1"),
        ("kernel.dmesg_restrict", lambda v: v.isdigit() and int(v) >= 1, ">=1"),
        ("kernel.randomize_va_space", lambda v: v == "2", "2"),
        ("fs.protected_hardlinks", lambda v: v == "1", "1"),
        ("fs.protected_symlinks", lambda v: v == "1", "1"),
        ("net.ipv4.tcp_syncookies", lambda v: v == "1", "1"),
    ]
    rows = []
    for key, ok_fn, expect in targets:
        p = "/proc/sys/" + key.replace(".", "/")
        val = read_text(p)
        if not val:
            continue
        rows.append({"param": key, "value": val, "expected": expect, "status": "pass" if ok_fn(val) else "warning"})
    return rows

def sha256_file(path):
    try:
        h = hashlib.sha256()
        with open(path, "rb") as f:
            h.update(f.read())
        return h.hexdigest()[:16]
    except Exception:
        return ""

hostname = socket.gethostname()
os_info = parse_os_release()
kernel_info = f"{platform.system()} {platform.release()}"
in_container = os.path.exists("/.dockerenv")
cores, cpu_pct, load1 = collect_cpu()
mem_total, mem_used, mem_pct, slab_bytes = collect_memory()
mounts, disk_total, disk_used, disk_pct, worst_mount = collect_mounts_dedup()
uptime_sec = collect_uptime()
proc_total, zombies = collect_processes()
sysctl_rows = collect_sysctl()

checks = []
def add_check(cid, name, cat, st, val, detail):
    checks.append({"id": cid, "name": name, "category": cat, "status": st, "value": val, "detail": detail})

if uptime_sec is not None:
    up_st = "pass" if uptime_sec >= 300 else "warning"
    scope_note = "（容器共享宿主内核 uptime）" if in_container else ""
    add_check("host.uptime", "运行与重启 (M1)", "availability", up_st, f"{int(uptime_sec)}s", f"运行时长 {int(uptime_sec)} 秒{scope_note}")

cpu_st = "critical" if (cpu_pct or 0) >= CPU_CRIT else ("warning" if (cpu_pct or 0) >= CPU_WARN else "pass")
mem_st = "critical" if (mem_pct or 0) >= MEM_CRIT else ("warning" if (mem_pct or 0) >= MEM_WARN else "pass")
res_st = "critical" if "critical" in (cpu_st, mem_st) else ("warning" if "warning" in (cpu_st, mem_st) else "pass")
add_check(
    "host.cpu_memory",
    "CPU 与内存水位 (M2)",
    "resource",
    res_st,
    f"CPU {cpu_pct if cpu_pct is not None else '-'}% / 内存 {mem_pct if mem_pct is not None else '-'}%",
    f"{cores} 核 · Load1m {load1 if load1 is not None else '-'} · 内存 {round(mem_used/(1024**3), 2)}/{round(mem_total/(1024**3), 2)} GiB",
)

disk_st = "critical" if disk_pct >= DISK_CRIT else ("warning" if disk_pct >= DISK_WARN else "pass")
unique_vols = sum(1 for m in mounts if not m["shared_volume"])
add_check(
    "host.disk",
    "磁盘容量（按卷去重）",
    "resource",
    disk_st,
    f"{worst_mount} {disk_pct}%",
    f"共 {len(mounts)} 个挂载点（去重后 {unique_vols} 块独立卷），去重总容量 {round(disk_total/(1024**3), 2)} GiB，已用 {round(disk_used/(1024**3), 2)} GiB",
)

proc_st = "warning" if zombies > ZOMBIE_WARN else "pass"
add_check("host.processes", "进程健康 (M3)", "security", proc_st, f"{proc_total} 进程 / {zombies} 僵尸", f"总进程 {proc_total}，僵尸进程 {zombies}")

if sysctl_rows:
    failed_sys = [r for r in sysctl_rows if r["status"] != "pass"]
    sys_st = "warning" if failed_sys else "pass"
    detail = "全部内核安全参数符合基线" if not failed_sys else "偏离项：" + ", ".join(f"{r['param']}={r['value']}(期望{r['expected']})" for r in failed_sys)
    add_check("host.sysctl", "内核参数合规 (M4)", "compliance", sys_st, f"{len(sysctl_rows)-len(failed_sys)}/{len(sysctl_rows)} 合规", detail)

acct_hashes = {p: sha256_file(p) for p in ("/etc/passwd", "/etc/group") if os.path.exists(p)}
if acct_hashes:
    add_check("host.auth_baseline", "账户文件基线 (M5)", "security", "pass", f"{len(acct_hashes)} 个指纹", ", ".join(f"{k}:{v}" for k, v in acct_hashes.items()))

overall = "pass"
for c in checks:
    if c["status"] == "critical":
        overall = "critical"
        break
    if c["status"] == "warning":
        overall = "warning"

warn_cnt = sum(1 for c in checks if c["status"] == "warning")
crit_cnt = sum(1 for c in checks if c["status"] == "critical")
pass_cnt = sum(1 for c in checks if c["status"] == "pass")
summary = f"主机 {hostname}（{os_info}）：通过 {pass_cnt} 项" + (f"，告警 {warn_cnt} 项" if warn_cnt else "") + (f"，严重 {crit_cnt} 项" if crit_cnt else "")

metrics = []
if cpu_pct is not None:
    metrics.append({"key": "cpu_percent", "label": "CPU 使用率", "value": cpu_pct, "unit": "%", "status": cpu_st})
if mem_pct is not None:
    metrics.append({"key": "memory_percent", "label": "内存使用率", "value": mem_pct, "unit": "%", "status": mem_st})
metrics.append({"key": "disk_percent", "label": "磁盘最高占比", "value": disk_pct, "unit": "%", "status": disk_st})
if load1 is not None:
    metrics.append({"key": "load_1m", "label": "1 分钟负载", "value": load1, "unit": "", "status": "pass"})
if uptime_sec is not None:
    metrics.append({"key": "uptime_seconds", "label": "运行时长", "value": uptime_sec, "unit": "s", "status": "pass"})
if mem_total > 0:
    metrics.append({"key": "memory_total_bytes", "label": "内存总量", "value": mem_total, "unit": "B", "status": "pass"})
    metrics.append({"key": "memory_used_bytes", "label": "内存已用", "value": mem_used, "unit": "B", "status": mem_st})
if disk_total > 0:
    metrics.append({"key": "disk_total_bytes", "label": "磁盘去重总量", "value": disk_total, "unit": "B", "status": "pass"})
    metrics.append({"key": "disk_used_bytes", "label": "磁盘去重已用", "value": disk_used, "unit": "B", "status": disk_st})
metrics.append({"key": "process_count", "label": "进程总数", "value": proc_total, "unit": "", "status": proc_st})

payload = {
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "host": {
        "hostname": hostname,
        "os": os_info,
        "kernel": kernel_info,
        "cpu_cores": cores,
        "in_container": in_container,
        "disk_unique_volumes": unique_vols,
    },
    "metrics": metrics,
    "charts": [
        {
            "title": "主机核心资源水位",
            "type": "bar",
            "unit": "%",
            "max": 100,
            "series": [
                {"label": "CPU", "value": cpu_pct if cpu_pct is not None else 0, "status": cpu_st},
                {"label": "内存", "value": mem_pct if mem_pct is not None else 0, "status": mem_st},
                {"label": f"磁盘 ({worst_mount})", "value": disk_pct, "status": disk_st},
            ],
        }
    ],
    "checks": checks,
    "mounts": mounts,
    "sysctl": sysctl_rows,
}

md_lines = [
    f"## 虚机与物理机系统巡检（{hostname}）",
    "",
    f"- **综合结论**：`{overall}` — {summary}",
    f"- **系统信息**：{os_info} / {kernel_info}（{cores} 核" + (" · 容器环境" if in_container else "") + "）",
    f"- **资源水位**：CPU `{cpu_pct if cpu_pct is not None else '-'}%` · 内存 `{mem_pct if mem_pct is not None else '-'}%`（{round(mem_used/(1024**3),2)}/{round(mem_total/(1024**3),2)} GiB）· 磁盘最高 `{worst_mount} {disk_pct}%`（去重总容量 `{round(disk_total/(1024**3),2)} GiB`）",
    "",
    "```json",
    json.dumps(payload, ensure_ascii=False, indent=2),
    "```",
]
print("\n".join(md_lines))
PY