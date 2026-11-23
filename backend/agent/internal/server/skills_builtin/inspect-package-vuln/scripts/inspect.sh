#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 主机软件包与补丁基线巡检

- **综合结论**：warning — 缺少 python3，未能采集

```json
{"status":"warning","summary":"缺少 python3，未采集软件包与补丁基线","metrics":[],"checks":[{"id":"pkg.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"未检测到 python3，包清单与补丁基线均未采集"}]}
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import platform
import shutil
import subprocess
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024  # spec L28 单条产物硬上限 256 KiB，预留保护余量
HARD_CAP_BYTES = 256 * 1024  # spec L28 单条产物硬上限（整条 stdout，含 Markdown 正文）
TRIM_LIST_KEYS = ("packages", "checks")

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

KEY_PKGS = ("openssl", "openssh-server", "openssh-client", "curl", "sudo", "nginx", "ca-certificates", "bash")

def run(cmd, timeout=8):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", errors="replace", timeout=timeout)
        return r.returncode, r.stdout.strip()
    except Exception:
        return 1, ""

pkg_mgr = "unknown"
packages = []
upgradable = None

if shutil.which("dpkg-query"):
    pkg_mgr = "dpkg/apt"
    for pkg in KEY_PKGS:
        rc, out = run(["dpkg-query", "-W", "-f=${Version}", pkg])
        if rc == 0 and out:
            packages.append({"package": pkg, "version": out, "manager": pkg_mgr})
    if shutil.which("apt"):
        rc, out = run(["apt", "list", "--upgradable"], timeout=10)
        if rc == 0:
            lines = [ln for ln in out.splitlines() if "/" in ln and not ln.startswith("Listing")]
            upgradable = len(lines)
elif shutil.which("rpm"):
    pkg_mgr = "rpm"
    for pkg in KEY_PKGS:
        rc, out = run(["rpm", "-q", "--qf", "%{VERSION}-%{RELEASE}", pkg])
        if rc == 0 and out and "not installed" not in out:
            packages.append({"package": pkg, "version": out, "manager": pkg_mgr})
elif shutil.which("apk"):
    pkg_mgr = "apk"
    rc, out = run(["apk", "info", "-v"])
    if rc == 0:
        for ln in out.splitlines():
            for pkg in KEY_PKGS:
                if ln.startswith(pkg + "-"):
                    packages.append({"package": pkg, "version": ln[len(pkg)+1:], "manager": pkg_mgr})
                    break

reboot_required = os.path.exists("/var/run/reboot-required")
up_st = "warning" if (upgradable is not None and upgradable > 0) else "pass"
up_avail_st = "pass" if upgradable is not None else "warning"
inv_st = "pass" if pkg_mgr != "unknown" else "warning"
rb_st = "warning" if reboot_required else "pass"
overall = "warning" if "warning" in (inv_st, up_avail_st, up_st, rb_st) else "pass"
summary = f"包管理器 {pkg_mgr}，已盘点 {len(packages)} 个核心组件" + (f"，本地待升级包 {upgradable} 个" if upgradable is not None else "") + ("，需要重启以生效补丁" if reboot_required else "")

metrics = [
    {"key": "key_packages_tracked", "label": "已盘点核心组件", "value": len(packages), "unit": "", "status": inv_st},
]
if upgradable is not None:
    metrics.append({"key": "upgradable_packages", "label": "本地待升级包数", "value": upgradable, "unit": "", "status": up_st})

checks = [
    {"id": "pkg.inventory", "name": "核心组件版本盘点", "category": "security", "status": inv_st, "value": f"{len(packages)} 个组件" if inv_st == "pass" else "不可得", "detail": (f"内核 {platform.system()} {platform.release()} · 包管理器 {pkg_mgr} · 已盘点 {len(packages)} 个核心组件") if inv_st == "pass" else "未找到 dpkg-query / rpm / apk，无法盘点软件包"},
    {"id": "pkg.upgradable", "name": "本地缓存待升级补丁", "category": "security", "status": up_st if upgradable is not None else "warning", "value": f"{upgradable} 个" if upgradable is not None else "不可得", "detail": "口径：仅统计本地包索引中标记为可升级的条目，未联网刷新索引、未做 CVE 漏洞比对；" + ("可升级 %d 个" % upgradable if upgradable is not None else "本机包管理器未提供可升级查询，需人工核查")},
    {"id": "pkg.reboot", "name": "补丁重启需求", "category": "security", "status": rb_st, "value": "需要重启" if reboot_required else "无需重启", "detail": "/var/run/reboot-required " + ("存在" if reboot_required else "不存在")},
]

payload = {
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "kernel": f"{platform.system()} {platform.release()}",
    "package_manager": pkg_mgr,
    "reboot_required": reboot_required,
    "metrics": metrics,
    "checks": checks,
    "packages": packages,
}
finish(payload)
emit(["## 主机软件包与补丁基线巡检", "", "- %s" % summary], payload)
PY
