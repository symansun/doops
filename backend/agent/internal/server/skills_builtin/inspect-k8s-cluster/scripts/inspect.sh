#!/usr/bin/env sh
set -eu

if ! command -v kubectl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  printf '```json\n{"status":"skipped","summary":"节点未配置 kubectl 或 python3，跳过集群直采","metrics":[],"checks":[{"id":"k8s.client","name":"kubectl 客户端","category":"availability","status":"skipped","value":"missing","detail":"未找到 kubectl 或 python3"}]}\n```\n'
  exit 0
fi

python3 - <<'PY'
import json
import subprocess
from datetime import datetime, timezone

def kget(args):
    try:
        r = subprocess.run(["kubectl", "--request-timeout=10s"] + args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=15)
        if r.returncode != 0:
            return None, r.stderr.strip() or f"exit {r.returncode}"
        return json.loads(r.stdout), ""
    except Exception as e:
        return None, str(e)

nodes_doc, err = kget(["get", "nodes", "-o", "json"])
if nodes_doc is None:
    payload = {
        "status": "warning",
        "summary": f"无法连接 Kubernetes API：{err[:160]}",
        "collected_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "metrics": [{"key": "k8s_nodes_total", "label": "节点总数", "value": 0, "unit": "", "status": "warning"}],
        "checks": [{"id": "k8s.apiserver", "name": "Kubernetes API 连通性", "category": "availability", "status": "warning", "value": "unreachable", "detail": err[:240]}],
    }
    print(f"## Kubernetes 集群巡检\n\n- 无法连接集群 API：`{err[:160]}`\n\n```json\n{json.dumps(payload, ensure_ascii=False, indent=2)}\n```")
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

checks = [
    {"id": "k8s.nodes", "name": "节点 Ready 状态", "category": "availability", "status": node_st, "value": f"{len(nodes)-len(not_ready_nodes)}/{len(nodes)} Ready", "detail": "全部节点就绪" if not not_ready_nodes else "异常节点：" + ", ".join(not_ready_nodes[:10])},
    {"id": "k8s.pods", "name": "异常与高重启 Pod", "category": "workload", "status": pod_st, "value": f"{len(bad_pods)}/{len(pods)} 异常", "detail": "未发现异常 Pod" if not bad_pods else f"发现 {len(bad_pods)} 个异常或高重启 Pod"},
    {"id": "k8s.workloads", "name": "控制器副本就绪", "category": "workload", "status": wl_st, "value": f"{len(unready_wl)} 个未就绪", "detail": "全部 Deployment/StatefulSet 副本一致" if not unready_wl else f"{len(unready_wl)} 个控制器副本不足"},
    {"id": "k8s.pvc", "name": "PVC 绑定状态", "category": "storage", "status": pvc_st, "value": f"{len(unbound_pvcs)} 未绑定", "detail": "全部 PVC 已 Bound" if not unbound_pvcs else ", ".join(unbound_pvcs[:8])},
    {"id": "k8s.events", "name": "Warning 事件数", "category": "events", "status": "warning" if warn_events > 20 else "pass", "value": str(warn_events), "detail": f"集群当前 Warning 事件 {warn_events} 条"},
]
summary = f"K8s 集群：{len(nodes)} 节点（{len(not_ready_nodes)} 异常），{len(pods)} Pod（{len(bad_pods)} 异常），{len(unready_wl)} 控制器未就绪，{warn_events} 条 Warning 事件"

payload = {
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
}
print(f"## Kubernetes 集群巡检\n\n- {summary}\n\n```json\n{json.dumps(payload, ensure_ascii=False, indent=2)}\n```")
PY