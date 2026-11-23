#!/usr/bin/env sh
# SB-01 夹具 E（容器内，无需特权）：container-runtime 正常路径。
# docker stats shim 返回两条记录：一条带 MemPerc，一条只有 MemUsage used/limit（验证解析口径）。
set -eu

SLUG_DIR=/w/inspect-container-runtime
if [ "$#" -ge 1 ]; then
  SLUG_DIR="$1"
fi
SHIM=/tmp/sb01_shim_rt
OUT=/tmp/sb01_shim_rt_output.md
rm -rf "$SHIM"
mkdir -p "$SHIM"
cat > "$SHIM/docker" <<'SH'
#!/bin/sh
case "$1" in
  info)
    echo "Server Version: sb01-shim"
    exit 0
    ;;
  stats)
    printf '%s\n' '{"ID":"a1","Name":"web","CPUPerc":"10.00%","MemPerc":"75.00%","MemUsage":"768MiB / 1GiB","PIDs":"12"}' '{"ID":"b2","Name":"db","CPUPerc":"5.00%","MemUsage":"512MiB / 1GiB","PIDs":"20"}'
    exit 0
    ;;
esac
exit 0
SH
chmod +x "$SHIM/docker"

PATH="$SHIM:$PATH" sh "$SLUG_DIR/scripts/inspect.sh" > "$OUT"

python3 - "$OUT" <<'PY'
import json
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
payload = json.loads(re.findall(r"```json\n(.*?)```", text, re.S)[-1])
assert payload["status"] == "pass", payload["status"]
metrics = {m["key"]: m["value"] for m in payload["metrics"]}
assert metrics["containers_running"] == 2, metrics
assert abs(float(metrics["container_max_mem_percent"]) - 75.0) < 0.01, metrics
by = {c["name"]: c for c in payload["containers"]}
assert abs(float(by["web"]["mem_percent"]) - 75.0) < 0.01, by["web"]
assert abs(float(by["db"]["mem_percent"]) - 50.0) < 0.01, by["db"]
assert by["db"]["mem_limit_bytes"] == 1024 ** 3, by["db"]
assert by["db"]["status"] == "pass", by["db"]
print("RUNTIME_STATS_OK containers=%d max_mem=%s" % (len(by), metrics["container_max_mem_percent"]))
PY
