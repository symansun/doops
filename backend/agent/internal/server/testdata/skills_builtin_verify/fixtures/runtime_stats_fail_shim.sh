#!/usr/bin/env sh
# SB-01 夹具 F（容器内，无需特权）：container-runtime 异常路径。
# docker info 通过但 docker stats 失败：必须显式 warning 且不得伪造 containers_running=0。
set -eu

SLUG_DIR=/w/inspect-container-runtime
if [ "$#" -ge 1 ]; then
  SLUG_DIR="$1"
fi
SHIM=/tmp/sb01_shim_rt_fail
OUT=/tmp/sb01_shim_rt_fail_output.md
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
    echo "Cannot connect to the Docker daemon" >&2
    exit 1
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
assert payload["status"] == "warning", payload["status"]
keys = [m["key"] for m in payload["metrics"]]
assert "containers_running" not in keys, "stats 失败时不得伪造容器数: %s" % keys
checks = {c["id"]: c for c in payload["checks"]}
assert checks["container.stats"]["status"] == "warning", checks
assert checks["container.stats"]["value"] == "failed", checks
assert payload["runtime"] == "docker", payload["runtime"]
assert payload["containers"] == [], payload["containers"]
print("RUNTIME_STATS_FAIL_OK metrics=%d" % len(keys))
PY
