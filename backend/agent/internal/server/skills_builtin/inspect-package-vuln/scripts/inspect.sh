#!/usr/bin/env sh
set -eu

if ! command -v python3 >/dev/null 2>&1; then
  printf '```json\n{"status":"warning","summary":"缺少 python3","metrics":[],"checks":[{"id":"pkg.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"节点缺少 python3"}]}\n```\n'
  exit 0
fi

python3 - <<'PY'
import json
import os
import platform
import shutil
import subprocess
from datetime import datetime, timezone

KEY_PKGS = ("openssl", "openssh-server", "openssh-client", "curl", "sudo", "nginx", "ca-certificates", "bash")

def run(cmd, timeout=8):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, timeout=timeout)
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
rb_st = "warning" if reboot_required else "pass"
overall = "warning" if "warning" in (up_st, rb_st) else "pass"
summary = f"包管理器 {pkg_mgr}，已盘点 {len(packages)} 个核心组件" + (f"，本地待升级包 {upgradable} 个" if upgradable is not None else "") + ("，需要重启以生效补丁" if reboot_required else "")

metrics = [
    {"key": "key_packages_tracked", "label": "已盘点核心组件", "value": len(packages), "unit": "", "status": "pass"},
]
if upgradable is not None:
    metrics.append({"key": "upgradable_packages", "label": "本地待升级包数", "value": upgradable, "unit": "", "status": up_st})

checks = [
    {"id": "pkg.inventory", "name": "核心组件版本盘点", "category": "security", "status": "pass", "value": f"{len(packages)} 个组件", "detail": f"内核 {platform.system()} {platform.release()} · 包管理器 {pkg_mgr}"},
    {"id": "pkg.upgradable", "name": "本地缓存待升级补丁", "category": "security", "status": up_st if upgradable is not None else "skipped", "value": f"{upgradable} 个" if upgradable is not None else "未缓存", "detail": "仅读取本地缓存索引，未联网更新"},
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
print(f"## 主机软件包与补丁基线巡检\n\n- {summary}\n\n```json\n{json.dumps(payload, ensure_ascii=False, indent=2)}\n```")
PY