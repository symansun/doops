#!/usr/bin/env sh
set -eu

# 无 python3 时的降级路径：仍输出协议合规载荷（status=warning，不伪造 0 值）
if ! command -v python3 >/dev/null 2>&1; then
  HOST=$(hostname 2>/dev/null || echo unknown)
  KERN=$(uname -sr 2>/dev/null || echo unknown)
  # JSON 字符串转义：反斜杠与双引号，避免主机名/内核串破坏 JSON
  esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | tr -d '\r\n'; }
  HOST_J=$(esc "$HOST")
  KERN_J=$(esc "$KERN")
  printf '## 虚机与物理机系统巡检（%s）\n\n' "$HOST"
  cat <<'MD'
- **综合结论**：`warning` — 缺少 python3，仅输出基础系统信息

```json
MD
  printf '{"status":"warning","summary":"缺少 python3，仅输出基础系统信息","host":{"hostname":"%s","kernel":"%s"},"metrics":[],"checks":[{"id":"runtime.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3，CPU/内存/磁盘/进程指标均未采集"}]}\n' "$HOST_J" "$KERN_J"
  cat <<'MD'
```
MD
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
MAX_PAYLOAD_BYTES = 200 * 1024  # spec L28 单条产物硬上限 256 KiB，预留保护余量
TRIM_LIST_KEYS = ("mounts", "sysctl", "charts")

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

def read_text(path):
    try:
        with open(path, "r", encoding="utf-8", errors="ignore") as f:
            return f.read().strip()
    except Exception:
        return ""

def run_cmd(cmd, timeout=5):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", errors="replace", timeout=timeout)
        return r.stdout if r.returncode == 0 else ""
    except Exception:
        return ""

def parse_os_release():
    if platform.system() == "Darwin":
        ver = run_cmd(["sw_vers", "-productVersion"]).strip()
        return ("macOS %s" % ver).strip()
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
    """按设备/文件系统去重共享存储（spec 红线：同盘多挂载点不得重复累加）。

    去重优先级（每条都基于可核验的设备身份，禁止把 df 的 source 文本当设备唯一键：
    overlay 的 source 字面量都是 "overlay"，用文本判等会把任意第二个 overlay 误并入 /）：
      1. st_dev 相同的同一文件系统（bind mount / 同一设备多次挂载）
      2. source 是真实块设备路径（/dev/...）且完全相同
      3. 容量/用量三元组完全一致，且至少一侧 source 不是 /dev/ 块设备
         （容器 overlay 与其底层文件系统指向同一卷）
      4. 两侧 source 都不是 /dev/ 块设备，容量与已用都在 5% 容差内
         → 判为疑似同一底层文件系统，按较大者计一次并记入「去重不确定性」（不静默虚增）
    """
    out = run_cmd(["df", "-P", "-k"])
    skip_fs = {"tmpfs", "devtmpfs", "shm", "proc", "sysfs", "cgroup", "cgroup2", "devfs", "map", "squashfs", "nsfs"}
    skip_mount_prefixes = ("/proc", "/sys", "/dev", "/run/secrets", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf")
    rows = []
    for line in out.splitlines()[1:]:
        parts = line.split()
        if len(parts) < 6:
            continue
        src, blk_s, used_s, avail_s, _cap, mnt = parts[0], parts[1], parts[2], parts[3], parts[4], " ".join(parts[5:])
        if src.lower() in skip_fs or any(mnt == p or mnt.startswith(p + "/") for p in skip_mount_prefixes):
            continue
        if not (blk_s.isdigit() and used_s.isdigit() and avail_s.isdigit()):
            continue
        tb = int(blk_s) * 1024
        ub = int(used_s) * 1024
        ab = int(avail_s) * 1024
        if tb <= 0:
            continue
        try:
            st_dev = os.stat(mnt).st_dev
        except Exception:
            st_dev = None
        rows.append({"src": src, "mnt": mnt, "tb": tb, "ub": ub, "ab": ab,
                     "pct": round((ub / tb) * 100.0, 2), "st_dev": st_dev})
    # 大容量优先成为代表卷：近似去重时保留较大的一份，避免把宿主根盘误判成小视图
    rows.sort(key=lambda r: r["tb"], reverse=True)

    def _is_dev(s):
        return s.startswith("/dev/")

    mounts = []
    seen_dev = {}
    seen_src = {}
    seen_triple = {}
    kept = []
    total_bytes = 0
    used_bytes = 0
    max_pct = 0.0
    worst_mount = "/"
    uncertain = []

    for r in rows:
        src, mnt, tb, ub, ab = r["src"], r["mnt"], r["tb"], r["ub"], r["ab"]
        pct, st_dev = r["pct"], r["st_dev"]
        dev_key = "dev:%s" % st_dev if st_dev is not None else None
        src_key = "src:%s" % src if src and src != "none" else None
        triple_key = "size:%d:%d:%d" % (tb, ub, ab)
        reason = None
        if dev_key and dev_key in seen_dev:
            reason = "同一文件系统（st_dev=%s 相同，已由 %s 计入）" % (st_dev, seen_dev[dev_key])
        elif src_key and _is_dev(src) and src_key in seen_src:
            reason = "同一块设备 %s（已由 %s 计入）" % (src, seen_src[src_key])
        elif triple_key in seen_triple and (not _is_dev(src) or not _is_dev(seen_triple[triple_key][1])):
            reason = "容量/用量完全一致且非块设备源，与 %s 视为同一卷" % seen_triple[triple_key][0]
        else:
            tol_ub = max(int(tb * 0.05), 64 * 1024 * 1024)
            for k in kept:
                # 只有至少一侧是非 /dev/ 虚拟源（overlay、virtiofs 等）才允许近似归并：
                # overlay 的 statfs 常报底层设备原始容量（如 65536KiB），而其 lowerdir 的
                # ext4 报可用容量（如 56037KiB），三元组不完全相等但确为同一卷（QA-01 D3）。
                # 两个真实块设备之间永不近似合并，避免把两块同容量盘误当一卷。
                if _is_dev(src) and _is_dev(k["src"]):
                    continue
                if abs(k["tb"] - tb) <= max(int(k["tb"] * 0.05), 64 * 1024 * 1024) and abs(k["ub"] - ub) <= tol_ub:
                    reason = "疑似同一底层文件系统（容量/已用差异 <=5%%，已由 %s 计入）" % k["mnt"]
                    uncertain.append("%s(t=%sGiB)≈%s(t=%sGiB)" % (mnt, round(tb / (1024 ** 3), 2), k["mnt"], round(k["tb"] / (1024 ** 3), 2)))
                    break
        if reason is None:
            if dev_key:
                seen_dev[dev_key] = mnt
            if src_key and _is_dev(src):
                seen_src[src_key] = mnt
            seen_triple.setdefault(triple_key, (mnt, src))
            kept.append({"src": src, "mnt": mnt, "tb": tb, "ub": ub})
            total_bytes += tb
            used_bytes += ub
        if pct >= max_pct:
            max_pct = pct
            worst_mount = mnt
        mounts.append({
            "mount": mnt,
            "source": src,
            "total_gib": round(tb / (1024 ** 3), 2),
            "used_gib": round(ub / (1024 ** 3), 2),
            "used_pct": pct,
            "shared_volume": reason is not None,
            "dedupe_reason": reason or "",
        })
    return mounts, total_bytes, used_bytes, round(max_pct, 2), worst_mount, uncertain

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
    """返回 (总进程数, 僵尸数)；任一为 None 表示未能采集，调用方必须显式告警。"""
    out = run_cmd(["ps", "-eo", "stat="])
    if out.strip():
        lines = [ln.strip() for ln in out.splitlines() if ln.strip()]
        return len(lines), sum(1 for ln in lines if ln.startswith("Z"))
    fallback = run_cmd(["ps", "-ef"])
    if fallback.strip():
        lines = [ln for ln in fallback.splitlines()[1:] if ln.strip()]
        return len(lines), None
    return None, None

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
kernel_info = "%s %s" % (platform.system(), platform.release())
in_container = os.path.exists("/.dockerenv")
cores, cpu_pct, load1 = collect_cpu()
mem_total, mem_used, mem_pct, slab_bytes = collect_memory()
mounts, disk_total, disk_used, disk_pct, worst_mount, dedup_uncertain = collect_mounts_dedup()
uptime_sec = collect_uptime()
proc_total, zombies = collect_processes()
sysctl_rows = collect_sysctl()

checks = []
def add_check(cid, name, cat, st, val, detail):
    checks.append({"id": cid, "name": name, "category": cat, "status": st, "value": val, "detail": detail})

if uptime_sec is not None:
    up_st = "pass" if uptime_sec >= 300 else "warning"
    scope_note = "（容器共享宿主内核 uptime）" if in_container else ""
    add_check("host.uptime", "运行与重启 (M1)", "availability", up_st, "%ds" % int(uptime_sec), "运行时长 %d 秒%s" % (int(uptime_sec), scope_note))

missing_bits = []
if cpu_pct is None:
    missing_bits.append("CPU 使用率不可得")
if mem_pct is None:
    missing_bits.append("内存使用率不可得")
cpu_st = "critical" if (cpu_pct or 0) >= CPU_CRIT else ("warning" if (cpu_pct or 0) >= CPU_WARN else "pass")
mem_st = "critical" if (mem_pct or 0) >= MEM_CRIT else ("warning" if (mem_pct or 0) >= MEM_WARN else "pass")
res_st = "critical" if "critical" in (cpu_st, mem_st) else ("warning" if "warning" in (cpu_st, mem_st) else "pass")
if missing_bits and res_st == "pass":
    res_st = "warning"
add_check(
    "host.cpu_memory",
    "CPU 与内存水位 (M2)",
    "resource",
    res_st,
    "CPU %s%% / 内存 %s%%" % (cpu_pct if cpu_pct is not None else "-", mem_pct if mem_pct is not None else "-"),
    "%d 核 · Load1m %s · 内存 %s/%s GiB%s" % (cores, load1 if load1 is not None else "-", round(mem_used / (1024 ** 3), 2), round(mem_total / (1024 ** 3), 2), ("；" + "、".join(missing_bits)) if missing_bits else ""),
)

unique_vols = sum(1 for m in mounts if not m["shared_volume"])
if mounts:
    disk_st = "critical" if disk_pct >= DISK_CRIT else ("warning" if disk_pct >= DISK_WARN else "pass")
    dedup_note = ""
    if dedup_uncertain:
        dedup_note = "；近似去重 %d 对：%s（容器内无法证明为同一底层设备，已按较大者计一次，不虚增容量）" % (len(dedup_uncertain), "，".join(dedup_uncertain[:3]))
        if disk_st == "pass":
            disk_st = "warning"
    add_check(
        "host.disk",
        "磁盘容量（按卷去重）",
        "resource",
        disk_st,
        "%s %s%%" % (worst_mount, disk_pct),
        "共 %d 个挂载点（去重后 %d 块独立卷），去重总容量 %s GiB，已用 %s GiB%s" % (len(mounts), unique_vols, round(disk_total / (1024 ** 3), 2), round(disk_used / (1024 ** 3), 2), dedup_note),
    )
    if dedup_uncertain:
        add_check("host.disk_dedup", "磁盘去重不确定性", "resource", "warning", "%d 对" % len(dedup_uncertain), "以下挂载点容量/用量近似但无法证明为同一底层设备，已按较大者计一次：" + "，".join(dedup_uncertain[:5]))
else:
    disk_st = "warning"
    add_check("host.disk", "磁盘容量（按卷去重）", "resource", "warning", "未采集", "df 不可用或未采集到可去重的挂载点，磁盘容量未知（不按 0 静默通过）")

if proc_total is None:
    proc_st = "warning"
    add_check("host.processes", "进程健康 (M3)", "security", "warning", "unknown", "ps 不可用或输出为空，进程数与僵尸数未能采集（不按 0 静默通过）")
elif zombies is None:
    proc_st = "warning"
    add_check("host.processes", "进程健康 (M3)", "security", "warning", "%d 进程 / 僵尸未知" % proc_total, "%d 个进程；ps 不支持 stat 字段，僵尸进程数未知" % proc_total)
else:
    proc_st = "warning" if zombies > ZOMBIE_WARN else "pass"
    add_check("host.processes", "进程健康 (M3)", "security", proc_st, "%d 进程 / %d 僵尸" % (proc_total, zombies), "总进程 %d，僵尸进程 %d" % (proc_total, zombies))

if sysctl_rows:
    failed_sys = [r for r in sysctl_rows if r["status"] != "pass"]
    sys_st = "warning" if failed_sys else "pass"
    detail = "全部内核安全参数符合基线" if not failed_sys else "偏离项：" + ", ".join("%s=%s(期望%s)" % (r["param"], r["value"], r["expected"]) for r in failed_sys)
    add_check("host.sysctl", "内核参数合规 (M4)", "compliance", sys_st, "%d/%d 合规" % (len(sysctl_rows) - len(failed_sys), len(sysctl_rows)), detail)

acct_hashes = {p: sha256_file(p) for p in ("/etc/passwd", "/etc/group") if os.path.exists(p)}
if acct_hashes:
    add_check("host.auth_baseline", "账户文件基线 (M5)", "security", "pass", "%d 个指纹" % len(acct_hashes), ", ".join("%s:%s" % (k, v) for k, v in acct_hashes.items()))

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
summary = "主机 %s（%s）：通过 %d 项" % (hostname, os_info, pass_cnt) + (", 告警 %d 项" % warn_cnt if warn_cnt else "") + (", 严重 %d 项" % crit_cnt if crit_cnt else "")

metrics = []
if cpu_pct is not None:
    metrics.append({"key": "cpu_percent", "label": "CPU 使用率", "value": cpu_pct, "unit": "%", "status": cpu_st})
if mem_pct is not None:
    metrics.append({"key": "memory_percent", "label": "内存使用率", "value": mem_pct, "unit": "%", "status": mem_st})
if mounts:
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
if proc_total is not None:
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
                {"label": "磁盘 (%s)" % worst_mount, "value": disk_pct, "status": disk_st},
            ],
        }
    ],
    "checks": checks,
    "mounts": mounts,
    "sysctl": sysctl_rows,
}
payload = finish(payload)

md_lines = [
    "## 虚机与物理机系统巡检（%s）" % hostname,
    "",
    "- **综合结论**：`%s` — %s" % (overall, summary),
    "- **系统信息**：%s / %s（%d 核%s）" % (os_info, kernel_info, cores, " · 容器环境" if in_container else ""),
    "- **资源水位**：CPU `%s%%` · 内存 `%s%%`（%s/%s GiB）· 磁盘最高 `%s %s%%`（去重总容量 `%s GiB`）" % (cpu_pct if cpu_pct is not None else "-", mem_pct if mem_pct is not None else "-", round(mem_used / (1024 ** 3), 2), round(mem_total / (1024 ** 3), 2), worst_mount, disk_pct, round(disk_total / (1024 ** 3), 2)),
    "",
    "```json",
    json.dumps(payload, ensure_ascii=False, indent=2),
    "```",
]
print("\n".join(md_lines))
PY
