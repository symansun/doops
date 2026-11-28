#!/usr/bin/env sh
set -eu
# 目标身份安全编码：cluster/instance 名仅允许常见字符集，其余一律 null（不编造）
tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

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
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少 python3，仅输出基础系统信息","host":{"hostname":"%s","kernel":"%s"},"metrics":[],"checks":[{"id":"runtime.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3，CPU/内存/磁盘/进程指标均未采集"}]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")" "$HOST_J" "$KERN_J"
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
import shutil
import socket
import subprocess
import time
from datetime import datetime, timezone

CPU_WARN, CPU_CRIT = 80.0, 90.0
MEM_WARN, MEM_CRIT = 80.0, 90.0
DISK_WARN, DISK_CRIT = 85.0, 95.0
ZOMBIE_WARN = 5
DISK_IO_WARN_MS, DISK_IO_CRIT_MS = 50.0, 200.0  # 对齐 monitor_engine_types.go:20 DiskLatencyWarnMs 默认 50ms
GPU_MEM_WARN, GPU_MEM_CRIT = 92.0, 98.0
GPU_TEMP_WARN, GPU_TEMP_CRIT = 85.0, 92.0
COMPACT_JSON_THRESHOLD_BYTES = 12 * 1024  # 超过 12 KiB 切换紧凑单行 JSON，兼顾 AI 工具 16 KiB 缓冲
MAX_PAYLOAD_BYTES = 200 * 1024  # spec L28 单条产物硬上限 256 KiB，预留保护余量
HARD_CAP_BYTES = 256 * 1024  # spec L28 单条产物硬上限（整条 stdout，含 Markdown 正文）
TRIM_LIST_KEYS = ("mounts", "sysctl", "charts", "disk_io", "top_cpu_processes", "top_mem_processes", "gpus", "network_interfaces")

def _target_identity():
    """消费平台注入的目标身份（monitor_engine_runner.go:938-953）；缺失时保持 null，不编造。"""
    return {
        "cluster": os.environ.get("DOOPS_TARGET_CLUSTER") or None,
        "instance": os.environ.get("DOOPS_TARGET_INSTANCE") or None,
    }

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
    compact_text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    if len(compact_text.encode("utf-8")) > COMPACT_JSON_THRESHOLD_BYTES:
        json_text = compact_text
    else:
        pretty_text = json.dumps(payload, ensure_ascii=False, indent=2)
        json_text = compact_text if len(pretty_text.encode("utf-8")) > COMPACT_JSON_THRESHOLD_BYTES else pretty_text
    if len(json_text.encode("utf-8")) > HARD_CAP_BYTES:
        json_text = compact_text
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

def detect_view_scope():
    """区分宿主机原生视角、容器 hostPID=true 视角与纯容器隔离视角（用 /proc/self/cgroup 而非 /proc/1/cgroup）。"""
    in_container = (
        os.path.exists("/.dockerenv")
        or os.path.exists("/run/.containerenv")
        or os.path.exists("/var/run/secrets/kubernetes.io/serviceaccount")
        or os.path.isdir("/host/proc")
    )
    if not in_container and platform.system() == "Linux":
        cg = read_text("/proc/self/cgroup")
        if any(tok in cg for tok in ("docker", "kubepods", "containerd", "crio")):
            in_container = True
    if not in_container:
        return False, True, "宿主机原生视角"
    pid1_raw = ""
    try:
        with open("/proc/1/cmdline", "rb") as f:
            pid1_raw = f.read().decode("utf-8", errors="ignore")
    except Exception:
        pid1_raw = ""
    pid1_parts = [p for p in pid1_raw.split("\x00") if p]
    pid1_base = os.path.basename(pid1_parts[0]) if pid1_parts else ""
    pid1_joined = " ".join(pid1_parts)
    if pid1_base in ("systemd", "init") and "sandbox-entrypoint" not in pid1_joined and "doops-agent" not in pid1_joined:
        return True, True, "容器(hostPID=true，宿主机进程可见)"
    return True, False, "纯容器视角(未开hostPID，仅容器内进程可见)"

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

VIRTUAL_NET_PREFIXES = (
    "lo", "veth", "docker", "br-", "cni", "flannel", "cali",
    "virbr", "kube-ipvs", "kube-dummy", "nodelocaldns", "tunl",
    "cilium", "dummy", "lxc", "podman", "vxlan", "geneve",
    "gre", "gretap", "erspan", "ip_vti", "ip6_vti", "ip6gre", "ip6tnl", "sit", "teql",
)

def read_net_dev(prefer_host_ns=False):
    """读取网卡累计字节与错包计数；hostPID 容器优先读 /proc/1/net/dev 获取宿主机真实物理网卡。"""
    if platform.system() != "Linux":
        return None
    raw = ""
    if prefer_host_ns:
        raw = read_text("/proc/1/net/dev")
    if not raw:
        raw = read_text("/proc/net/dev")
    if not raw:
        return None
    stats = {}
    for line in raw.splitlines()[2:]:
        if ":" not in line:
            continue
        iface, rest = line.split(":", 1)
        iface = iface.strip()
        parts = rest.split()
        if len(parts) < 12:
            continue
        try:
            rx_bytes = int(parts[0])
            rx_errs = int(parts[2])
            rx_drop = int(parts[3])
            tx_bytes = int(parts[8])
            tx_errs = int(parts[10])
            tx_drop = int(parts[11])
        except ValueError:
            continue
        stats[iface] = (rx_bytes, tx_bytes, rx_errs + tx_errs, rx_drop + tx_drop)
    return stats

def collect_cpu_and_network(prefer_host_net=False):
    """在同一个 250ms 窗口内同时采样 CPU (/proc/stat) 与物理网卡 (/proc/net/dev)，避免叠加等待耗时。"""
    cores = os.cpu_count() or 1
    cpu_pct = None
    net_rows = []
    net_rx_bps = None
    net_tx_bps = None
    net_total_bps = None
    net_err_delta = 0
    net_note = ""

    if platform.system() == "Linux":
        t0 = read_cpu_ticks()
        n0 = read_net_dev(prefer_host_ns=prefer_host_net)
        ts0 = time.monotonic()
        time.sleep(0.25)
        ts1 = time.monotonic()
        t1 = read_cpu_ticks()
        n1 = read_net_dev(prefer_host_ns=prefer_host_net)
        dt_sec = max(0.05, ts1 - ts0)
        if t0 and t1 and t1[0] > t0[0]:
            dt = t1[0] - t0[0]
            di = max(0, t1[1] - t0[1])
            cpu_pct = round(max(0.0, min(100.0, ((dt - di) / dt) * 100.0)), 2)
        if n0 is not None and n1 is not None:
            non_lo = [k for k in n1.keys() if k != "lo" and k in n0]
            physical = [k for k in non_lo if not k.startswith(VIRTUAL_NET_PREFIXES)]
            selected = physical if physical else non_lo
            if not selected:
                net_note = "仅检测到 loopback 接口（lo）"
            else:
                rx_sum = 0.0
                tx_sum = 0.0
                for iface in sorted(selected):
                    r0, x0, e0, d0 = n0[iface]
                    r1, x1, e1, d1 = n1[iface]
                    rx_rate = round(max(0, r1 - r0) / dt_sec, 1)
                    tx_rate = round(max(0, x1 - x0) / dt_sec, 1)
                    errs = max(0, (e1 - e0) + (d1 - d0))
                    rx_sum += rx_rate
                    tx_sum += tx_rate
                    net_err_delta += errs
                    net_rows.append({
                        "interface": iface,
                        "rx_bytes_per_sec": rx_rate,
                        "tx_bytes_per_sec": tx_rate,
                        "errors_delta": errs,
                        "virtual": iface.startswith(VIRTUAL_NET_PREFIXES),
                    })
                net_rows.sort(key=lambda r: -(r["rx_bytes_per_sec"] + r["tx_bytes_per_sec"]))
                net_rows = net_rows[:16]
                net_rx_bps = round(rx_sum, 1)
                net_tx_bps = round(tx_sum, 1)
                net_total_bps = round(rx_sum + tx_sum, 1)
        else:
            net_note = "/proc/net/dev 不可读"
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
        net_note = "非 Linux 平台未采样 /proc/net/dev"
    load1 = None
    try:
        load1 = round(os.getloadavg()[0], 2)
    except Exception:
        pass
    return cores, cpu_pct, load1, (net_rows, net_rx_bps, net_tx_bps, net_total_bps, net_err_delta, net_note)

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
    skip_fs = {"tmpfs", "devtmpfs", "shm", "proc", "sysfs", "cgroup", "cgroup2", "devfs", "map", "squashfs", "nsfs", "iso9660", "romfs"}
    skip_mount_prefixes = (
        "/proc", "/sys", "/dev", "/run/secrets", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf",
        "/snap", "/host/snap",
        "/run/containerd", "/host/run/containerd",
        "/var/lib/containerd", "/host/var/lib/containerd",
        "/var/lib/docker/overlay2", "/host/var/lib/docker/overlay2",
        "/var/lib/kubelet/pods", "/host/var/lib/kubelet/pods",
    )
    mount_fstypes = {}
    if platform.system() == "Linux":
        for mline in read_text("/proc/mounts").splitlines():
            mp = mline.split()
            if len(mp) >= 3:
                mount_fstypes[mp[1].replace("\\040", " ")] = mp[2].lower()
    rows = []
    for line in out.splitlines()[1:]:
        parts = line.split()
        if len(parts) < 6:
            continue
        src, blk_s, used_s, avail_s, _cap, mnt = parts[0], parts[1], parts[2], parts[3], parts[4], " ".join(parts[5:])
        fstype = mount_fstypes.get(mnt, "")
        if src.lower() in skip_fs or fstype in skip_fs or any(mnt == p or mnt.startswith(p + "/") for p in skip_mount_prefixes):
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

def collect_top_processes():
    """仅采集进程短名 comm（绝不采集命令行参数，避免泄漏凭据），排除巡检自身 PID，返回 (top_cpu, top_mem)。"""
    out = run_cmd(["ps", "-eo", "pid=,pcpu=,pmem=,comm="])
    if not out.strip():
        return [], []
    my_pid = os.getpid()
    parent_pid = os.getppid()
    rows = []
    for line in out.splitlines():
        parts = line.strip().split(None, 3)
        if len(parts) < 4:
            continue
        try:
            pid = int(parts[0])
            pcpu = round(float(parts[1]), 1)
            pmem = round(float(parts[2]), 1)
        except ValueError:
            continue
        if pid in (my_pid, parent_pid):
            continue
        comm = parts[3].strip()[:48]
        if not comm or comm == "ps":
            continue
        rows.append({"pid": pid, "cpu_pct": pcpu, "mem_pct": pmem, "comm": comm})
    top_cpu = sorted(rows, key=lambda r: (-r["cpu_pct"], -r["mem_pct"]))[:5]
    top_mem = sorted(rows, key=lambda r: (-r["mem_pct"], -r["cpu_pct"]))[:5]
    return top_cpu, top_mem

def _parse_float_opt(raw):
    s = (raw or "").strip()
    if not s or s.upper() in ("N/A", "[N/A]", "NONE", "[NOT SUPPORTED]"):
        return None
    try:
        return float(s)
    except ValueError:
        return None

def collect_gpus(allow_host_nsenter=False):
    """探测 NVIDIA GPU 利用率/显存/温度/功耗/UUID；hostPID 容器可经只读 nsenter -t 1 -m 调用宿主 nvidia-smi；无 GPU 时返回空列表。"""
    query_args = [
        "--query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,uuid",
        "--format=csv,noheader,nounits",
    ]
    out = ""
    nvsmi = shutil.which("nvidia-smi")
    if not nvsmi:
        for cand in ("/usr/local/nvidia/bin/nvidia-smi", "/usr/bin/nvidia-smi"):
            if os.path.isfile(cand) and os.access(cand, os.X_OK):
                nvsmi = cand
                break
    if nvsmi:
        out = run_cmd([nvsmi] + query_args, timeout=5)
    if not out.strip() and allow_host_nsenter:
        nsenter = shutil.which("nsenter")
        if nsenter:
            out = run_cmd([nsenter, "-t", "1", "-m", "--", "nvidia-smi"] + query_args, timeout=5)
    if not out.strip():
        return []
    gpus = []
    for line in out.splitlines():
        parts = [p.strip() for p in line.split(",")]
        if len(parts) < 6:
            continue
        idx = _parse_float_opt(parts[0])
        name = parts[1][:64] if parts[1] else "NVIDIA GPU"
        util = _parse_float_opt(parts[2])
        mem_used = _parse_float_opt(parts[3])
        mem_total = _parse_float_opt(parts[4])
        temp = _parse_float_opt(parts[5])
        power = _parse_float_opt(parts[6]) if len(parts) > 6 else None
        raw_uuid = parts[7].strip() if len(parts) > 7 else ""
        uuid_ok = (
            len(raw_uuid) == 40
            and raw_uuid.startswith("GPU-")
            and all(raw_uuid[pos] == "-" for pos in (12, 17, 22, 27))
            and all(ch in "0123456789abcdef" for i, ch in enumerate(raw_uuid[4:]) if (i + 4) not in (12, 17, 22, 27))
        )
        gpu_uuid = raw_uuid if uuid_ok else ""
        mem_pct = round((mem_used / mem_total) * 100.0, 2) if (mem_used is not None and mem_total and mem_total > 0) else None
        gpu_row = {
            "index": int(idx) if idx is not None else len(gpus),
            "name": name,
            "util_pct": round(util, 1) if util is not None else None,
            "mem_used_mib": int(round(mem_used)) if mem_used is not None else None,
            "mem_total_mib": int(round(mem_total)) if mem_total is not None else None,
            "mem_pct": mem_pct,
            "temp_c": round(temp, 1) if temp is not None else None,
            "power_w": round(power, 1) if power is not None else None,
        }
        if gpu_uuid:
            gpu_row["uuid"] = gpu_uuid
        gpus.append(gpu_row)
    return gpus[:32]

def fmt_bytes(n):
    """人类可读字节数（检查项 detail 用，不做单位换算精度承诺）。"""
    v = float(n)
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if v < 1024 or unit == "TiB":
            return "%s%s" % (int(v) if unit == "B" else round(v, 2), unit)
        v /= 1024
    return "%dB" % n

# /proc/diskstats 只暴露计数器，虚拟设备与分区计数器会重复计量，必须显式排除（不猜容量）
VIRTUAL_DEV_PREFIXES = ("loop", "ram", "fd", "sr", "zram", "nbd", "dm-", "md", "drbd")

def _is_virtual_dev(name):
    return name.startswith(VIRTUAL_DEV_PREFIXES)

def _is_partition_name(name, names):
    """分区计数器是整盘计数的子集：sda1/sda、nvme0n1p1/nvme0n1、vda1/vda。"""
    for parent in names:
        if parent == name or not name.startswith(parent):
            continue
        rest = name[len(parent):]
        if rest.isdigit() or (rest[:1] == "p" and rest[1:].isdigit()):
            return True
    return False

def collect_disk_io():
    """返回 (per_device_rows, reason)；reason 非空表示未采到物理块设备计数器，调用方必须显式告警。

    字段口径（/proc/diskstats 第 4 段起）：读完成数、读扇区数、读耗时 ms、写完成数、写扇区数、写耗时 ms；
    扇区固定 512 字节。分区与虚拟设备在采集阶段排除，避免同一物理盘重复计量（红线 2 同源问题）。
    """
    if platform.system() != "Linux":
        return [], "/proc/diskstats 仅 Linux 内核提供，当前平台无块设备计数器，磁盘 I/O 与延迟未知（不按 0 静默通过）"
    raw = read_text("/proc/diskstats")
    if not raw:
        return [], "/proc/diskstats 不可读（容器未暴露 proc 或无权限），磁盘 I/O 与延迟未知（不按 0 静默通过）"
    rows = []
    for line in raw.splitlines():
        parts = line.split()
        if len(parts) < 14:
            continue
        try:
            reads, sectors_read, ms_read = int(parts[3]), int(parts[5]), int(parts[6])
            writes, sectors_written, ms_write = int(parts[7]), int(parts[9]), int(parts[10])
        except ValueError:
            continue
        ios = reads + writes
        rows.append({
            "device": parts[2],
            "reads": reads,
            "writes": writes,
            "read_bytes": sectors_read * 512,
            "write_bytes": sectors_written * 512,
            "latency_ms": round((ms_read + ms_write) / float(ios), 2) if ios > 0 else None,
        })
    names = set(r["device"] for r in rows)
    kept = [r for r in rows if not _is_virtual_dev(r["device"]) and not _is_partition_name(r["device"], names)]
    if not kept:
        return [], ("/proc/diskstats 只含虚拟设备或分区计数器（已排除 loop/ram/dm-/md-/sr/zram/nbd 与分区），"
                    "容器内看不到物理块设备，磁盘 I/O 与延迟未知（不按 0 静默通过）")
    kept.sort(key=lambda r: -(r["read_bytes"] + r["write_bytes"]))
    return kept[:50], ""

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
in_container, host_pid, view_scope = detect_view_scope()
cores, cpu_pct, load1, net_info = collect_cpu_and_network(prefer_host_net=(in_container and host_pid))
net_rows, net_rx_bps, net_tx_bps, net_total_bps, net_err_delta, net_note = net_info
mem_total, mem_used, mem_pct, slab_bytes = collect_memory()
mounts, disk_total, disk_used, disk_pct, worst_mount, dedup_uncertain = collect_mounts_dedup()
uptime_sec = collect_uptime()
proc_total, zombies = collect_processes()
top_cpu_procs, top_mem_procs = collect_top_processes()
gpus = collect_gpus(allow_host_nsenter=(in_container and host_pid))
sysctl_rows = collect_sysctl()

checks = []
def add_check(cid, name, cat, st, val, detail):
    checks.append({"id": cid, "name": name, "category": cat, "status": st, "value": val, "detail": detail})

if uptime_sec is not None:
    up_st = "pass" if uptime_sec >= 300 else "warning"
    scope_note = "（%s，共享宿主内核 uptime）" % view_scope if in_container else ""
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

disk_io_rows, disk_io_missing = collect_disk_io()
io_reads = io_writes = io_read_bytes = io_write_bytes = None
io_lat = None
if disk_io_missing:
    disk_io_st = "warning"
    add_check("host.disk_io", "磁盘 I/O 与延迟 (M6)", "resource", "warning", "未采集", disk_io_missing)
else:
    io_reads = sum(r["reads"] for r in disk_io_rows)
    io_writes = sum(r["writes"] for r in disk_io_rows)
    io_read_bytes = sum(r["read_bytes"] for r in disk_io_rows)
    io_write_bytes = sum(r["write_bytes"] for r in disk_io_rows)
    io_lats = [r["latency_ms"] for r in disk_io_rows if r["latency_ms"] is not None]
    io_lat = round(max(io_lats), 2) if io_lats else None
    disk_io_st = "critical" if (io_lat or 0) >= DISK_IO_CRIT_MS else ("warning" if (io_lat or 0) >= DISK_IO_WARN_MS else "pass")
    top_io = [
        "%s 读 %d 次/%s、写 %d 次/%s、平均延迟 %s ms" % (
            r["device"], r["reads"], fmt_bytes(r["read_bytes"]), r["writes"], fmt_bytes(r["write_bytes"]),
            r["latency_ms"] if r["latency_ms"] is not None else "-",
        )
        for r in disk_io_rows[:3]
    ]
    add_check(
        "host.disk_io",
        "磁盘 I/O 与延迟 (M6)",
        "resource",
        disk_io_st,
        "%d 个块设备 / 最高延迟 %s ms" % (len(disk_io_rows), io_lat if io_lat is not None else "-"),
        "物理块设备 %d 个（已排除虚拟设备与分区计数器）：%s" % (len(disk_io_rows), "；".join(top_io)),
    )

if net_total_bps is not None:
    net_st = "warning" if net_err_delta > 0 else "pass"
    top_if = [
        "%s(收 %s/s, 发 %s/s%s)" % (
            r["interface"], fmt_bytes(r["rx_bytes_per_sec"]), fmt_bytes(r["tx_bytes_per_sec"]),
            ", 错/丢包+%d" % r["errors_delta"] if r["errors_delta"] > 0 else "",
        )
        for r in net_rows[:3]
    ]
    add_check(
        "host.network",
        "物理网卡吞吐 (M7)",
        "resource",
        net_st,
        "%s/s" % fmt_bytes(net_total_bps),
        "活跃网卡 %d 张（收 %s/s，发 %s/s）：%s" % (len(net_rows), fmt_bytes(net_rx_bps), fmt_bytes(net_tx_bps), "；".join(top_if)),
    )
elif net_note and platform.system() == "Linux":
    add_check("host.network", "物理网卡吞吐 (M7)", "resource", "pass" if "loopback" in net_note else "warning", "无物理网卡" if "loopback" in net_note else "未采集", net_note)

gpu_avg_util = None
gpu_mem_pct = None
gpu_temp_max = None
gpu_st = "pass"
if gpus:
    utils = [g["util_pct"] for g in gpus if g["util_pct"] is not None]
    temps = [g["temp_c"] for g in gpus if g["temp_c"] is not None]
    mem_used_sum = sum(g["mem_used_mib"] for g in gpus if g["mem_used_mib"] is not None and g["mem_total_mib"])
    mem_total_sum = sum(g["mem_total_mib"] for g in gpus if g["mem_used_mib"] is not None and g["mem_total_mib"])
    gpu_avg_util = round(sum(utils) / len(utils), 2) if utils else None
    gpu_mem_pct = round((mem_used_sum / float(mem_total_sum)) * 100.0, 2) if mem_total_sum > 0 else None
    gpu_temp_max = round(max(temps), 1) if temps else None
    if (gpu_temp_max is not None and gpu_temp_max >= GPU_TEMP_CRIT) or (gpu_mem_pct is not None and gpu_mem_pct >= GPU_MEM_CRIT):
        gpu_st = "critical"
    elif (gpu_temp_max is not None and gpu_temp_max >= GPU_TEMP_WARN) or (gpu_mem_pct is not None and gpu_mem_pct >= GPU_MEM_WARN):
        gpu_st = "warning"
    gpu_briefs = [
        "GPU%d(%s): 算力 %s%%, 显存 %s/%s MiB, 温度 %s°C" % (
            g["index"], g["name"],
            g["util_pct"] if g["util_pct"] is not None else "-",
            g["mem_used_mib"] if g["mem_used_mib"] is not None else "-",
            g["mem_total_mib"] if g["mem_total_mib"] is not None else "-",
            g["temp_c"] if g["temp_c"] is not None else "-",
        )
        for g in gpus[:4]
    ]
    add_check(
        "host.gpu",
        "GPU 算力与显存 (M8)",
        "resource",
        gpu_st,
        "%d 卡 · 均算力 %s%% · 显存 %s%%" % (len(gpus), gpu_avg_util if gpu_avg_util is not None else "-", gpu_mem_pct if gpu_mem_pct is not None else "-"),
        "最高温度 %s°C；%s" % (gpu_temp_max if gpu_temp_max is not None else "-", "；".join(gpu_briefs)),
    )

if proc_total is None:
    proc_st = "warning"
    add_check("host.processes", "进程健康 (M3)", "security", "warning", "unknown", "ps 不可用或输出为空，进程数与僵尸数未能采集（不按 0 静默通过）")
elif zombies is None:
    proc_st = "warning"
    add_check("host.processes", "进程健康 (M3)", "security", "warning", "%d 进程 / 僵尸未知" % proc_total, "%d 个进程（%s）；ps 不支持 stat 字段，僵尸进程数未知" % (proc_total, view_scope))
else:
    proc_st = "warning" if zombies > ZOMBIE_WARN else "pass"
    add_check("host.processes", "进程健康 (M3)", "security", proc_st, "%d 进程 / %d 僵尸" % (proc_total, zombies), "总进程 %d，僵尸进程 %d（%s）" % (proc_total, zombies, view_scope))

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
metrics.append({"key": "cpu_total_cores", "label": "CPU 逻辑核数", "value": cores, "unit": "", "status": "pass"})
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
if disk_io_rows:
    metrics.append({"key": "disk_io_reads", "label": "磁盘读次数", "value": io_reads, "unit": "", "status": "pass"})
    metrics.append({"key": "disk_io_writes", "label": "磁盘写次数", "value": io_writes, "unit": "", "status": "pass"})
    metrics.append({"key": "disk_io_read_bytes", "label": "磁盘读字节", "value": io_read_bytes, "unit": "B", "status": "pass"})
    metrics.append({"key": "disk_io_write_bytes", "label": "磁盘写字节", "value": io_write_bytes, "unit": "B", "status": "pass"})
    if io_lat is not None:
        metrics.append({"key": "disk_io_latency_ms", "label": "磁盘最高平均延迟", "value": io_lat, "unit": "ms", "status": disk_io_st})
if net_total_bps is not None:
    metrics.append({"key": "net_bytes_per_sec", "label": "网卡总吞吐", "value": net_total_bps, "unit": "B/s", "status": "warning" if net_err_delta > 0 else "pass"})
    metrics.append({"key": "net_rx_bytes_per_sec", "label": "网卡入流量", "value": net_rx_bps, "unit": "B/s", "status": "pass"})
    metrics.append({"key": "net_tx_bytes_per_sec", "label": "网卡出流量", "value": net_tx_bps, "unit": "B/s", "status": "pass"})
if gpus:
    metrics.append({"key": "gpu_count", "label": "GPU 数量", "value": len(gpus), "unit": "", "status": "pass"})
    if gpu_avg_util is not None:
        metrics.append({"key": "gpu_percent", "label": "GPU 平均利用率", "value": gpu_avg_util, "unit": "%", "status": "pass"})
    if gpu_mem_pct is not None:
        gpu_mem_st = "critical" if gpu_mem_pct >= GPU_MEM_CRIT else ("warning" if gpu_mem_pct >= GPU_MEM_WARN else "pass")
        metrics.append({"key": "gpu_memory_percent", "label": "GPU 显存使用率", "value": gpu_mem_pct, "unit": "%", "status": gpu_mem_st})
    if gpu_temp_max is not None:
        gpu_temp_st = "critical" if gpu_temp_max >= GPU_TEMP_CRIT else ("warning" if gpu_temp_max >= GPU_TEMP_WARN else "pass")
        metrics.append({"key": "gpu_temp_max_c", "label": "GPU 最高温度", "value": gpu_temp_max, "unit": "°C", "status": gpu_temp_st})

chart_series = [
    {"label": "CPU", "value": cpu_pct if cpu_pct is not None else 0, "status": cpu_st},
    {"label": "内存", "value": mem_pct if mem_pct is not None else 0, "status": mem_st},
    {"label": "磁盘 (%s)" % worst_mount, "value": disk_pct, "status": disk_st},
]
if gpus:
    if gpu_avg_util is not None:
        chart_series.append({"label": "GPU 算力 (%d卡)" % len(gpus), "value": gpu_avg_util, "status": "pass"})
    if gpu_mem_pct is not None:
        gpu_mem_st = "critical" if gpu_mem_pct >= GPU_MEM_CRIT else ("warning" if gpu_mem_pct >= GPU_MEM_WARN else "pass")
        chart_series.append({"label": "GPU 显存", "value": gpu_mem_pct, "status": gpu_mem_st})

payload = {
    "target": _target_identity(),
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "host": {
        "hostname": hostname,
        "os": os_info,
        "kernel": kernel_info,
        "cpu_cores": cores,
        "in_container": in_container,
        "host_pid": host_pid,
        "view_scope": view_scope,
        "disk_unique_volumes": unique_vols,
        "gpu_count": len(gpus),
    },
    "metrics": metrics,
    "charts": [
        {
            "title": "主机核心资源水位",
            "type": "bar",
            "unit": "%",
            "max": 100,
            "series": chart_series,
        }
    ],
    "checks": checks,
    "mounts": mounts,
    "sysctl": sysctl_rows,
    "disk_io": disk_io_rows,
    "network_interfaces": net_rows,
    "gpus": gpus,
    "top_cpu_processes": top_cpu_procs,
    "top_mem_processes": top_mem_procs,
}
payload = finish(payload)

md_lines = [
    "## 虚机与物理机系统巡检（%s）" % hostname,
    "",
    "- **综合结论**：`%s` — %s" % (overall, summary),
    "- **系统信息**：%s / %s（%d 核 · %s）" % (os_info, kernel_info, cores, view_scope),
    "- **资源水位**：CPU `%s%%`（Load1m `%s`）· 内存 `%s%%`（%s/%s GiB）· 磁盘最高 `%s %s%%`（去重总容量 `%s GiB`）" % (
        cpu_pct if cpu_pct is not None else "-",
        load1 if load1 is not None else "-",
        mem_pct if mem_pct is not None else "-",
        round(mem_used / (1024 ** 3), 2),
        round(mem_total / (1024 ** 3), 2),
        worst_mount,
        disk_pct,
        round(disk_total / (1024 ** 3), 2),
    ),
    "- **磁盘 I/O**：%s" % (
        "未采集（%s）" % disk_io_missing if disk_io_missing else
        "%d 个物理块设备 · 读 %d 次/%s · 写 %d 次/%s · 最高平均延迟 %s ms" % (len(disk_io_rows), io_reads, fmt_bytes(io_read_bytes), io_writes, fmt_bytes(io_write_bytes), io_lat if io_lat is not None else "-")
    ),
]
if net_total_bps is not None:
    md_lines.append("- **网络吞吐**：总 `%s/s`（收 `%s/s` · 发 `%s/s` · %d 张活跃网卡）" % (fmt_bytes(net_total_bps), fmt_bytes(net_rx_bps), fmt_bytes(net_tx_bps), len(net_rows)))
if gpus:
    md_lines.append("- **NVIDIA GPU**：`%d` 卡 · 均算力 `%s%%` · 显存 `%s%%` · 最高温度 `%s°C`" % (
        len(gpus),
        gpu_avg_util if gpu_avg_util is not None else "-",
        gpu_mem_pct if gpu_mem_pct is not None else "-",
        gpu_temp_max if gpu_temp_max is not None else "-",
    ))
emit(md_lines, payload)
PY
