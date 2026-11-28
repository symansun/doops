#!/bin/sh
# SB-02 能力夹具：k8s.metrics 正向采集（kubectl shim，无需真实集群 / metrics-server）
# 输入：top nodes 两行 1500m/3Gi + 500m/1Gi；top pods -A 两行 250m/512Mi + 50m/128Mi
# 期望：k8s.metrics check=pass；节点 CPU 合计 2000m、内存合计 4Gi=4294967296B；
#       Pod CPU 合计 300m、内存合计 640Mi=671088640B、有用量 Pod=2；集群自身状态仍 pass
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

cat > /tmp/k8s/topnodes <<'OUT'
gw-test   1500m   38%   3Gi   40%
node-2    500m    12%   1Gi   13%
OUT
cat > /tmp/k8s/toppods <<'OUT'
default   web-1   250m   512Mi
kube-system   dns-1   50m   128Mi
OUT
make_shim kubectl <<'SH'
#!/bin/sh
args="$*"
case "$args" in
  *"top nodes"*) cat /tmp/k8s/topnodes ;;
  *"top pods"*) cat /tmp/k8s/toppods ;;
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
