#!/bin/sh
# SB-02 能力夹具：metrics-server 不可用时的降级路径
# 输入：get 子命令正常（集群可巡检），top 一律 exit 1 且 stderr = "Metrics API not available"
# 期望：check k8s.metrics status=warning 且 detail 写明原因；不得出现任何用量指标（不编造 0）；
#       集群健康结论 status 保持 pass（用量能力缺失不改变集群健康判定）
# shellcheck source=/dev/null
. /f/robust/_lib.sh

mkdir -p /tmp/k8s
cat > /tmp/k8s/nodes.json <<'JSON'
{"items":[
 {"metadata":{"name":"gw-test"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"node-2"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}
JSON
cat > /tmp/k8s/pods.json <<'JSON'
{"items":[
 {"metadata":{"namespace":"default","name":"web-1"},"status":{"phase":"Running","containerStatuses":[{"restartCount":0}]}}]}
JSON
printf '{"items":[]}\n' > /tmp/k8s/empty.json

make_shim kubectl <<'SH'
#!/bin/sh
args="$*"
case "$args" in
  *"top nodes"*|*"top pods"*) echo "error: Metrics API not available" >&2; exit 1 ;;
  *"get nodes"*) cat /tmp/k8s/nodes.json ;;
  *"get pods"*) cat /tmp/k8s/pods.json ;;
  *"get deployments,statefulsets"*) cat /tmp/k8s/empty.json ;;
  *"get pvc"*) cat /tmp/k8s/empty.json ;;
  *"get events"*) cat /tmp/k8s/empty.json ;;
  *) echo "unexpected kubectl args: $args" >&2; exit 1 ;;
esac
SH
require_shim_path
run_pkg inspect-k8s-cluster
ok
