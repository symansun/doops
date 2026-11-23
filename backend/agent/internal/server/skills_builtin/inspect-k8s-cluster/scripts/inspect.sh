#!/usr/bin/env sh
set -eu
# 目标身份安全编码：cluster/instance 名仅允许常见字符集，其余一律 null（不编造）
tgtj() { case "${1:-}" in ''|*[!A-Za-z0-9_.:@/-]*) printf 'null';; *) printf '"%s"' "$1";; esac; }

MISSING=""
if ! command -v kubectl >/dev/null 2>&1; then
  MISSING="kubectl"
fi
if ! command -v python3 >/dev/null 2>&1; then
  if [ -n "$MISSING" ]; then
    MISSING="$MISSING, python3"
  else
    MISSING="python3"
  fi
fi

if [ -n "$MISSING" ]; then
  printf '## Kubernetes 集群巡检\n\n- **综合结论**：warning — 缺少依赖（%s），未直采集群\n\n' "$MISSING"
  cat <<'MD'
```json
MD
  printf '{"target":{"cluster":%s,"instance":%s},"status":"warning","summary":"缺少依赖（%s），未直采 Kubernetes 集群","metrics":[],"checks":[{"id":"k8s.client","name":"采集依赖","category":"availability","status":"warning","value":"missing","detail":"未找到 %s，无法执行 kubectl 采集"}]}\n' "$(tgtj "${DOOPS_TARGET_CLUSTER:-}")" "$(tgtj "${DOOPS_TARGET_INSTANCE:-}")" "$MISSING" "$MISSING"
  cat <<'MD'
```
MD
  exit 0
fi

python3 - <<'PY'
import json
import os
import subprocess
from datetime import datetime, timezone

MAX_PAYLOAD_BYTES = 200 * 1024  # spec L28 单条产物硬上限 256 KiB，预留保护余量
HARD_CAP_BYTES = 256 * 1024  # spec L28 单条产物硬上限（整条 stdout，含 Markdown 正文）
TRIM_LIST_KEYS = ("abnormal_pods", "unready_workloads", "checks", "node_usage")

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

def kget(args):
    try:
        r = subprocess.run(["kubectl", "--request-timeout=10s"] + args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace", timeout=15)
        if r.returncode != 0:
            return None, r.stderr.strip() or f"exit {r.returncode}"
        return json.loads(r.stdout), ""
    except Exception as e:
        return None, str(e)

def krun(args, timeout=25):
    """kubectl 文本子命令（top 只支持表格输出）；失败返回 (None, 原因)。"""
    try:
        r = subprocess.run(["kubectl", "--request-timeout=10s"] + args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace", timeout=timeout)
        if r.returncode != 0:
            return None, r.stderr.strip() or "exit %d" % r.returncode
        return r.stdout, ""
    except Exception as e:
        return None, str(e)

def parse_cpu_millicores(tok):
    tok = tok.strip()
    if tok.endswith("n"):
        return int(round(float(tok[:-1]) / 1e6))
    if tok.endswith("u"):
        return int(round(float(tok[:-1]) / 1e3))
    if tok.endswith("m"):
        return int(round(float(tok[:-1])))
    return int(round(float(tok) * 1000))

def parse_mem_bytes(tok):
    tok = tok.strip()
    units = (("Ki", 1024), ("Mi", 1024 ** 2), ("Gi", 1024 ** 3), ("Ti", 1024 ** 4), ("K", 1000), ("M", 1000 ** 2), ("G", 1000 ** 3), ("T", 1000 ** 4))
    for suf, mult in units:
        if tok.endswith(suf):
            return int(round(float(tok[:-len(suf)]) * mult))
    return int(float(tok))

def collect_usage():
    """metrics-server 实时用量（kubectl top）。返回 (nodes, pods_agg, reason)；reason 说明缺哪一步。"""
    out, err = krun(["top", "nodes", "--no-headers"])
    if out is None:
        return [], None, "kubectl top nodes 失败：%s（metrics-server 未安装或当前身份无权限）" % err[:160]
    nodes_use = []
    for line in out.splitlines():
        parts = line.split()
        if len(parts) < 4:
            continue
        try:
            nodes_use.append({
                "node": parts[0],
                "cpu_millicores": parse_cpu_millicores(parts[1]),
                "cpu_percent": float(parts[2].rstrip("%")) if len(parts) > 2 else None,
                "memory_bytes": parse_mem_bytes(parts[3]),
                "memory_percent": float(parts[4].rstrip("%")) if len(parts) > 4 else None,
            })
        except ValueError:
            continue
    if not nodes_use:
        return [], None, "kubectl top nodes 输出无法解析（metrics-server 返回格式异常）"
    pout, perr = krun(["top", "pods", "-A", "--no-headers"])
    if pout is None:
        return nodes_use, None, "kubectl top pods 失败：%s（节点用量已采集，Pod 用量缺失）" % perr[:160]
    agg = {"with_usage": 0, "cpu_millicores": 0, "memory_bytes": 0}
    for line in pout.splitlines():
        parts = line.split()
        if len(parts) < 4:
            continue
        try:
            agg["cpu_millicores"] += parse_cpu_millicores(parts[2])
            agg["memory_bytes"] += parse_mem_bytes(parts[3])
            agg["with_usage"] += 1
        except ValueError:
            continue
    return nodes_use, agg, ""

nodes_doc, err = kget(["get", "nodes", "-o", "json"])
if nodes_doc is None:
    payload = {
        "target": _target_identity(),
        "status": "warning",
        "summary": f"无法连接 Kubernetes API：{err[:160]}",
        "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "metrics": [{"key": "k8s_nodes_total", "label": "节点总数", "value": 0, "unit": "", "status": "warning"}],
        "checks": [{"id": "k8s.apiserver", "name": "Kubernetes API 连通性", "category": "availability", "status": "warning", "value": "unreachable", "detail": err[:240]}],
    }
    finish(payload)
    emit(["## Kubernetes 集群巡检", "", "- 无法连接集群 API：`%s`" % err[:160]], payload)
    raise SystemExit(0)

nodes = nodes_doc.get("items") or []
not_ready_nodes = []
for n in nodes:
    name = (n.get("metadata") or {}).get("name", "")
    conds = {c.get("type"): c.get("status") for c in ((n.get("status") or {}).get("conditions") or [])}
    if conds.get("Ready") != "True":
        not_ready_nodes.append(name)

pods_doc, _ = kget(["get", "pods", "-A", "-o", "json"])
pods = (pods_doc or {}).get("items") or []
bad_pods = []
for p in pods:
    meta = p.get("metadata") or {}
    st = p.get("status") or {}
    phase = st.get("phase", "")
    restarts = sum(int(cs.get("restartCount") or 0) for cs in (st.get("containerStatuses") or []))
    reason = ""
    for cs in (st.get("containerStatuses") or []):
        w = (cs.get("state") or {}).get("waiting") or {}
        if w.get("reason"):
            reason = w.get("reason")
            break
    if phase not in ("Running", "Succeeded") or restarts >= 5 or reason in ("CrashLoopBackOff", "OOMKilled", "ImagePullBackOff", "ErrImagePull"):
        bad_pods.append({"namespace": meta.get("namespace", ""), "pod": meta.get("name", ""), "phase": phase, "restarts": restarts, "reason": reason or phase, "status": "critical" if reason in ("CrashLoopBackOff", "OOMKilled") else "warning"})

wl_doc, _ = kget(["get", "deployments,statefulsets", "-A", "-o", "json"])
unready_wl = []
for w in ((wl_doc or {}).get("items") or []):
    desired = int((w.get("spec") or {}).get("replicas") or 0)
    ready = int((w.get("status") or {}).get("readyReplicas") or 0)
    if desired > 0 and ready < desired:
        meta = w.get("metadata") or {}
        unready_wl.append({"kind": w.get("kind", ""), "namespace": meta.get("namespace", ""), "name": meta.get("name", ""), "ready": f"{ready}/{desired}", "status": "warning"})

pvc_doc, _ = kget(["get", "pvc", "-A", "-o", "json"])
unbound_pvcs = [f"{(x.get('metadata') or {}).get('namespace','')}/{(x.get('metadata') or {}).get('name','')}" for x in ((pvc_doc or {}).get("items") or []) if (x.get("status") or {}).get("phase") != "Bound"]

ev_doc, _ = kget(["get", "events", "-A", "--field-selector", "type=Warning", "-o", "json"])
warn_events = len((ev_doc or {}).get("items") or [])

node_st = "critical" if not_ready_nodes else "pass"
pod_st = "critical" if any(x["status"] == "critical" for x in bad_pods) else ("warning" if bad_pods else "pass")
wl_st = "warning" if unready_wl else "pass"
pvc_st = "warning" if unbound_pvcs else "pass"
overall = "critical" if "critical" in (node_st, pod_st) else ("warning" if "warning" in (node_st, pod_st, wl_st, pvc_st) else "pass")

# metrics-server 实时用量（k8s.metrics）：不可用时只记 check 原因，不改集群健康结论（overall 只由四类资源状态决定）
usage_nodes, usage_pods, usage_err = collect_usage()
usage_metrics = []
if usage_nodes:
    usage_metrics.append({"key": "k8s_node_cpu_millicores", "label": "节点 CPU 合计", "value": sum(n["cpu_millicores"] for n in usage_nodes), "unit": "m", "status": "pass"})
    usage_metrics.append({"key": "k8s_node_memory_bytes", "label": "节点内存合计", "value": sum(n["memory_bytes"] for n in usage_nodes), "unit": "B", "status": "pass"})
if usage_pods:
    usage_metrics.append({"key": "k8s_pod_cpu_millicores", "label": "Pod CPU 合计", "value": usage_pods["cpu_millicores"], "unit": "m", "status": "pass"})
    usage_metrics.append({"key": "k8s_pod_memory_bytes", "label": "Pod 内存合计", "value": usage_pods["memory_bytes"], "unit": "B", "status": "pass"})
    usage_metrics.append({"key": "k8s_pods_with_usage", "label": "有用量的 Pod 数", "value": usage_pods["with_usage"], "unit": "", "status": "pass"})
if usage_err:
    usage_chk = {"id": "k8s.metrics", "name": "metrics-server 实时用量", "category": "resource", "status": "warning", "value": "unavailable", "detail": usage_err[:240]}
else:
    usage_top = "；".join("%s CPU %dm/%s%%、内存 %s/%s%%" % (n["node"], n["cpu_millicores"], n["cpu_percent"] if n["cpu_percent"] is not None else "-", n["memory_bytes"], n["memory_percent"] if n["memory_percent"] is not None else "-") for n in usage_nodes[:3])
    usage_chk = {"id": "k8s.metrics", "name": "metrics-server 实时用量", "category": "resource", "status": "pass", "value": "%d 节点 / %d Pod" % (len(usage_nodes), (usage_pods or {}).get("with_usage", 0)), "detail": usage_top}

checks = [
    {"id": "k8s.nodes", "name": "节点 Ready 状态", "category": "availability", "status": node_st, "value": f"{len(nodes)-len(not_ready_nodes)}/{len(nodes)} Ready", "detail": "全部节点就绪" if not not_ready_nodes else "异常节点：" + ", ".join(not_ready_nodes[:10])},
    {"id": "k8s.pods", "name": "异常与高重启 Pod", "category": "workload", "status": pod_st, "value": f"{len(bad_pods)}/{len(pods)} 异常", "detail": "未发现异常 Pod" if not bad_pods else f"发现 {len(bad_pods)} 个异常或高重启 Pod"},
    {"id": "k8s.workloads", "name": "控制器副本就绪", "category": "workload", "status": wl_st, "value": f"{len(unready_wl)} 个未就绪", "detail": "全部 Deployment/StatefulSet 副本一致" if not unready_wl else f"{len(unready_wl)} 个控制器副本不足"},
    {"id": "k8s.pvc", "name": "PVC 绑定状态", "category": "storage", "status": pvc_st, "value": f"{len(unbound_pvcs)} 未绑定", "detail": "全部 PVC 已 Bound" if not unbound_pvcs else ", ".join(unbound_pvcs[:8])},
    {"id": "k8s.events", "name": "Warning 事件数", "category": "events", "status": "warning" if warn_events > 20 else "pass", "value": str(warn_events), "detail": f"集群当前 Warning 事件 {warn_events} 条"},
    usage_chk,
]
summary = f"K8s 集群：{len(nodes)} 节点（{len(not_ready_nodes)} 异常），{len(pods)} Pod（{len(bad_pods)} 异常），{len(unready_wl)} 控制器未就绪，{warn_events} 条 Warning 事件"

payload = {
    "target": _target_identity(),
    "status": overall,
    "summary": summary,
    "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "metrics": [
        {"key": "k8s_nodes_total", "label": "节点总数", "value": len(nodes), "unit": "", "status": node_st},
        {"key": "k8s_nodes_not_ready", "label": "异常节点数", "value": len(not_ready_nodes), "unit": "", "status": node_st},
        {"key": "k8s_pods_total", "label": "Pod 总数", "value": len(pods), "unit": "", "status": "pass"},
        {"key": "k8s_pods_abnormal", "label": "异常 Pod 数", "value": len(bad_pods), "unit": "", "status": pod_st},
        {"key": "k8s_warning_events", "label": "Warning 事件数", "value": warn_events, "unit": "", "status": "warning" if warn_events > 20 else "pass"},
    ],
    "checks": checks,
    "abnormal_pods": bad_pods[:30],
    "unready_workloads": unready_wl[:20],
    "node_usage": usage_nodes[:50],
}
payload["metrics"].extend(usage_metrics)
finish(payload)
usage_line = "- 实时用量：%s" % ("未采集（%s）" % usage_err[:160] if usage_err else "%d 节点 / %d Pod 有用量数据，节点 CPU 合计 %dm、内存合计 %s" % (len(usage_nodes), (usage_pods or {}).get("with_usage", 0), sum(n["cpu_millicores"] for n in usage_nodes), sum(n["memory_bytes"] for n in usage_nodes)))
emit(["## Kubernetes 集群巡检", "", "- %s" % summary, usage_line], payload)
PY