#!/usr/bin/env sh
set -eu
tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

if ! command -v python3 >/dev/null 2>&1; then
  cat <<'MD'
## 主机安全与合规治理巡检

- **综合结论**：warning — 缺少 python3，未能采集安全基线

```json
MD
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少 python3，未采集主机安全基线","metrics":[],"checks":[{"id":"sec.python3","name":"采集依赖","category":"runtime","status":"warning","value":"missing","detail":"目标环境未检测到 python3"}]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")"
  cat <<'MD'
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import re
import shutil
import subprocess
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024
HARD_CAP_BYTES = 256 * 1024

def _env_bool(name, default):
    raw = os.environ.get(name, "").strip().lower()
    if not raw:
        return default
    return raw in ("1", "true", "yes", "on")

CHECK_PROXY = _env_bool("DOOPS_INPUT_CHECK_PROXY", True)
CHECK_UPDATES = _env_bool("DOOPS_INPUT_CHECK_UPDATES", True)
SENSITIVITY = os.environ.get("DOOPS_INPUT_SENSITIVITY", "standard").strip().lower()
if SENSITIVITY not in ("standard", "strict"):
    SENSITIVITY = "standard"

MINER_PATTERNS = (
    "xmrig", "minerd", "cpuminer", "nbminer", "t-rex", "lolminer",
    "gminer", "phoenixminer", "ethminer", "kdevtmpfsi", "kinsing", "cryptonight",
)
PROXY_NAMES = (
    "clash", "mihomo", "v2ray", "xray", "sing-box",
    "frpc", "frps", "nps", "npc", "trojan", "ss-local", "shadowsocks", "ngrok",
)
PROXY_PORTS = {"1080", "3128", "7890", "7891", "8118", "10808", "20170"}

def target_identity():
    return {
        "cluster": os.environ.get("DOOPS_TARGET_CLUSTER") or None,
        "instance": os.environ.get("DOOPS_TARGET_INSTANCE") or None,
    }

def run_rc(cmd, timeout=8):
    try:
        r = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", errors="replace", timeout=timeout)
        return r.returncode, r.stdout or ""
    except Exception:
        return 1, ""

def emit(intro, payload):
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if len(text.encode("utf-8")) > MAX_PAYLOAD_BYTES:
        payload["findings"] = (payload.get("findings") or [])[:40]
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    if len(text.encode("utf-8")) > HARD_CAP_BYTES:
        payload["findings"] = (payload.get("findings") or [])[:15]
        text = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    print("\n".join(intro) + "\n\n```json\n" + text + "\n```")

findings = []
mining_suspects = []
proxy_suspects = []
ssh_issues = []
pending_updates = 0
updates_note = ""

my_pid = str(os.getpid())
ps_rc, ps_out = run_rc(["ps", "-eo", "pid=,comm=,args="])
ps_note = "" if ps_rc == 0 else f"ps 执行失败（rc={ps_rc}）"

for line in ps_out.splitlines():
    parts = line.strip().split(None, 2)
    if len(parts) < 2:
        continue
    pid, comm = parts[0], parts[1]
    args = parts[2] if len(parts) > 2 else comm
    if pid == my_pid or "inspect.sh" in args:
        continue
    low = (comm + " " + args).lower()
    for kw in MINER_PATTERNS:
        if re.search(rf"(^|[/\s_-]){re.escape(kw)}($|[/\s_.-])", low):
            item = {"category": "mining", "severity": "critical", "pid": pid, "comm": comm, "reason": f"命中挖矿特征 {kw}"}
            mining_suspects.append(item)
            findings.append(item)
            break
    else:
        if SENSITIVITY == "strict" and (args.startswith("/tmp/") or args.startswith("/dev/shm/") or " /tmp/." in args):
            item = {"category": "mining", "severity": "warning", "pid": pid, "comm": comm, "reason": "临时目录直接执行的进程（严格模式告警）"}
            mining_suspects.append(item)
            findings.append(item)

    if CHECK_PROXY:
        for kw in PROXY_NAMES:
            if re.search(rf"(^|[/\s_-]){re.escape(kw)}($|[/\s_.-])", low):
                item = {"category": "proxy", "severity": "critical", "pid": pid, "comm": comm, "reason": f"命中违规代理/穿透进程 {kw}"}
                proxy_suspects.append(item)
                findings.append(item)
                break

if CHECK_PROXY:
    port_cmd = ["ss", "-tlnH"] if shutil.which("ss") else (["netstat", "-tln"] if shutil.which("netstat") else None)
    if port_cmd:
        prc, pout = run_rc(port_cmd)
        if prc == 0:
            seen_ports = set()
            for line in pout.splitlines():
                for m in re.findall(r":(\d+)\b", line):
                    if m in PROXY_PORTS and m not in seen_ports:
                        seen_ports.add(m)
                        item = {"category": "proxy", "severity": "warning", "pid": "-", "comm": "listener", "reason": f"常见代理端口 {m} 处于监听"}
                        proxy_suspects.append(item)
                        findings.append(item)

# SSH hardening baseline (strictly read-only).
sshd_cfg = "/etc/ssh/sshd_config"
if os.path.isfile(sshd_cfg):
    try:
        with open(sshd_cfg, "r", encoding="utf-8", errors="ignore") as f:
            for raw_line in f:
                line = raw_line.split("#", 1)[0].strip()
                if not line:
                    continue
                kv = line.split(None, 1)
                if len(kv) != 2:
                    continue
                key, val = kv[0].lower(), kv[1].strip().lower()
                if key == "permitrootlogin" and val == "yes":
                    issue = {"category": "ssh", "severity": "warning", "pid": "-", "comm": "sshd", "reason": "PermitRootLogin 为 yes（建议改为 prohibit-password 或 no）"}
                    ssh_issues.append(issue)
                    findings.append(issue)
                elif SENSITIVITY == "strict" and key == "passwordauthentication" and val == "yes":
                    issue = {"category": "ssh", "severity": "warning", "pid": "-", "comm": "sshd", "reason": "PasswordAuthentication 为 yes（严格模式建议改用密钥认证）"}
                    ssh_issues.append(issue)
                    findings.append(issue)
    except Exception:
        pass

# Pending security updates (strictly read-only, bounded timeout).
if CHECK_UPDATES:
    if shutil.which("apt-get"):
        rc, out = run_rc(["apt-get", "-s", "-o", "Debug::NoLocking=true", "upgrade"], timeout=10)
        if rc == 0:
            pending_updates = sum(1 for ln in out.splitlines() if ln.startswith("Inst "))
        else:
            updates_note = f"apt-get 模拟检查失败（rc={rc}）"
    elif shutil.which("dnf"):
        rc, out = run_rc(["dnf", "-q", "check-update", "--security"], timeout=10)
        if rc in (0, 100):
            pending_updates = sum(1 for ln in out.splitlines() if ln.strip() and not ln.startswith("Last metadata"))
        else:
            updates_note = f"dnf 安全更新检查失败（rc={rc}）"
    elif shutil.which("yum"):
        rc, out = run_rc(["yum", "-q", "check-update"], timeout=10)
        if rc in (0, 100):
            pending_updates = sum(1 for ln in out.splitlines() if ln.strip() and not ln.startswith("Loaded plugins"))
        else:
            updates_note = f"yum 更新检查失败（rc={rc}）"
    else:
        updates_note = "未检测到 apt-get / dnf / yum，跳过补丁统计"

mining_st = "critical" if any(x["severity"] == "critical" for x in mining_suspects) else ("warning" if (mining_suspects or ps_note) else "pass")
proxy_st = "pass" if not CHECK_PROXY else ("critical" if any(x["severity"] == "critical" for x in proxy_suspects) else ("warning" if proxy_suspects else "pass"))
ssh_st = "warning" if ssh_issues else "pass"
upd_st = "pass" if not CHECK_UPDATES else ("critical" if pending_updates >= 50 else ("warning" if pending_updates >= 10 else "pass"))

risk_total = len(mining_suspects) + len(proxy_suspects) + len(ssh_issues) + (1 if upd_st in ("warning", "critical") else 0)
statuses = [mining_st, proxy_st, ssh_st, upd_st]
overall = "critical" if "critical" in statuses else ("warning" if "warning" in statuses else "pass")

summary = (
    f"共检出 {risk_total} 项安全风险（挖矿线索 {len(mining_suspects)}、代理线索 {len(proxy_suspects)}、SSH 基线 {len(ssh_issues)}、待更新包 {pending_updates}）"
    if risk_total > 0
    else f"主机安全基线通过（灵敏度：{SENSITIVITY}，待更新软件包 {pending_updates} 个）"
)

metrics = [
    {"key": "sec_risk_total", "label": "风险项合计", "value": risk_total, "unit": "项", "status": overall},
    {"key": "sec_mining_suspects", "label": "疑似挖矿进程", "value": len(mining_suspects), "unit": "个", "status": mining_st},
    {"key": "sec_proxy_suspects", "label": "违规代理线索", "value": len(proxy_suspects), "unit": "条", "status": proxy_st},
    {"key": "sec_ssh_issues", "label": "SSH 基线缺口", "value": len(ssh_issues), "unit": "项", "status": ssh_st},
    {"key": "sec_pending_updates", "label": "待更新软件包", "value": pending_updates, "unit": "个", "status": upd_st},
]

checks = [
    {"id": "sec.mining", "name": "挖矿与异常临时进程扫描", "category": "security", "status": mining_st, "value": f"{len(mining_suspects)} 个", "detail": ps_note or ("未发现挖矿或异常临时进程" if not mining_suspects else "; ".join(f"{x['comm']}(pid={x['pid']}): {x['reason']}" for x in mining_suspects[:6]))},
    {"id": "sec.proxy", "name": "违规代理与穿透检测", "category": "security", "status": proxy_st, "value": "已跳过" if not CHECK_PROXY else f"{len(proxy_suspects)} 条", "detail": "已按输入参数关闭代理检测" if not CHECK_PROXY else ("未发现违规代理进程或监听端口" if not proxy_suspects else "; ".join(x["reason"] for x in proxy_suspects[:6]))},
    {"id": "sec.ssh", "name": "SSH 加固配置基线", "category": "security", "status": ssh_st, "value": f"{len(ssh_issues)} 项缺口", "detail": "未发现 SSH 弱配置或未安装 sshd" if not ssh_issues else "; ".join(x["reason"] for x in ssh_issues)},
    {"id": "sec.updates", "name": "待更新系统补丁", "category": "security", "status": upd_st, "value": "已跳过" if not CHECK_UPDATES else f"{pending_updates} 个", "detail": "已按输入参数关闭补丁检查" if not CHECK_UPDATES else (updates_note or f"当前待更新软件包共 {pending_updates} 个")},
]

charts = [
    {
        "title": "安全风险分类计数",
        "type": "bar",
        "unit": "项",
        "series": [
            {"label": "挖矿线索", "value": len(mining_suspects), "status": mining_st},
            {"label": "违规代理", "value": len(proxy_suspects), "status": proxy_st},
            {"label": "SSH 基线", "value": len(ssh_issues), "status": ssh_st},
            {"label": "待更新包", "value": pending_updates, "status": upd_st},
        ],
    }
]

payload = {
    "target": target_identity(),
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "applied_inputs": {
        "CHECK_PROXY": CHECK_PROXY,
        "CHECK_UPDATES": CHECK_UPDATES,
        "SENSITIVITY": SENSITIVITY,
    },
    "metrics": metrics,
    "checks": checks,
    "charts": charts,
    "findings": findings[:60],
}

intro = [
    "## 主机安全与合规治理报告",
    "",
    f"- **综合状态**：`{overall}`",
    f"- **结论摘要**：{summary}",
    f"- **执行参数**：灵敏度 `{SENSITIVITY}` · 代理检测 `{'开启' if CHECK_PROXY else '关闭'}` · 补丁检查 `{'开启' if CHECK_UPDATES else '关闭'}`",
]
emit(intro, payload)
PY
