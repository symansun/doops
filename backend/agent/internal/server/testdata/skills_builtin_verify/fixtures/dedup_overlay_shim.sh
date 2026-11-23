#!/usr/bin/env sh
# SB-01 红线 2 夹具 B（容器内执行）：spec 的规范场景——overlay 根与底层块设备
# /dev/vda1 是同一存储，容量/用量三元组完全一致。GNU df 会按设备名隐藏同名重复行，
# 故本夹具用受控 df 输出（只替换 df，其余采集保持真实）驱动 inspect.sh，
# 断言同卷只计入一次 disk_total_bytes、且给出 dedupe_reason。
# 依赖：python3；无需特权。
set -eu

SLUG_DIR=/w/inspect-vm-host
if [ "$#" -ge 1 ]; then
  SLUG_DIR="$1"
fi
SHIM=/tmp/sb01_shim
OUT=/tmp/sb01_shim_output.md
rm -rf "$SHIM"
mkdir -p "$SHIM"
cat > "$SHIM/df" <<'SH'
#!/bin/sh
if [ "$1" = "-P" ]; then
  printf '%s\n' 'Filesystem 1024-blocks Used Available Capacity Mounted on' 'overlay 1000000 400000 600000 40% /' '/dev/vda1 1000000 400000 600000 40% /data' '/dev/sdb1 2000000 100000 1900000 5% /data2'
  exit 0
fi
exec /bin/df "$@"
SH
chmod +x "$SHIM/df"

PATH="$SHIM:$PATH" sh "$SLUG_DIR/scripts/inspect.sh" > "$OUT"

python3 - "$OUT" <<'PY'
import json
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
payload = json.loads(re.findall(r"```json\n(.*?)```", text, re.S)[-1])
assert payload.get("status") in ("pass", "warning", "critical"), payload.get("status")

mounts = payload.get("mounts") or []
assert len(mounts) == 3, "受控 df 的三行应全部采集: %s" % mounts
by = {m.get("mount"): m for m in mounts}
assert by["/data"].get("shared_volume") is True, "/dev/vda1 未与 overlay 去重"
assert by["/data"].get("dedupe_reason"), "去重项缺少原因"
assert by["/"].get("shared_volume") is False, "overlay 根不应被判为重复"
assert by["/data2"].get("shared_volume") is False, "不同卷被误合并: %s" % by["/data2"]

metrics = {m.get("key"): m.get("value") for m in (payload.get("metrics") or [])}
assert metrics.get("disk_total_bytes") == 3000000 * 1024, metrics.get("disk_total_bytes")
saved = 4000000 * 1024 - metrics["disk_total_bytes"]
assert saved == 1000000 * 1024, "去重未生效: saved=%s" % saved
assert payload.get("host", {}).get("disk_unique_volumes") == 2, payload.get("host")
assert payload.get("output_bytes", 0) < 256 * 1024
print("DEDUP_OVERLAY_OK disk_total_bytes=%d naive=%d saved=%d" % (metrics["disk_total_bytes"], 4000000 * 1024, saved))
PY
