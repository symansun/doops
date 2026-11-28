#!/usr/bin/env sh
# SB-01 夹具 D（容器内，无需特权）：回归 QA-01 D4。
# 模拟 Docker Desktop 形态："/"（overlay）与 "/w"（宿主盘 bind，virtiofs）都非块设备源，
# st_dev 不同、三元组不相等，仅容量/用量近似。断言按较大者计一次、总量不虚增，并给出
# host.disk_dedup 不确定性告警（不静默）。
set -eu

SLUG_DIR=/w/inspect-vm-host
if [ "$#" -ge 1 ]; then
  SLUG_DIR="$1"
fi
SHIM=/tmp/sb01_shim_d
OUT=/tmp/sb01_shim_d_output.md
rm -rf "$SHIM"
mkdir -p "$SHIM"
cat > "$SHIM/df" <<'SH'
#!/bin/sh
if [ "$1" = "-P" ]; then
  printf '%s\n' 'Filesystem 1024-blocks Used Available Capacity Mounted on' 'overlay 953000 400000 553000 42% /' 'virtiofs 969000 410000 559000 42% /w'
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

by = {m.get("mount"): m for m in (payload.get("mounts") or [])}
assert len(by) == 2, "受控 df 两行应全部采集: %s" % sorted(by)
assert by["/w"].get("shared_volume") is False, "较大卷应为代表卷: %s" % by["/w"]
assert by["/"].get("shared_volume") is True, "近似同盘未合并（会虚增容量）: %s" % by["/"]
assert by["/"].get("dedupe_reason"), "近似合并缺少原因"

metrics = {m.get("key"): m.get("value") for m in (payload.get("metrics") or [])}
assert metrics.get("disk_total_bytes") == 969000 * 1024, "容量虚增: %s" % metrics.get("disk_total_bytes")
assert payload.get("host", {}).get("disk_unique_volumes") == 1, payload.get("host")
assert payload.get("status") == "warning", "近似去重应给出 warning，实际 %s" % payload.get("status")
ids = [c.get("id") for c in (payload.get("checks") or [])]
assert "host.disk_dedup" in ids, "缺少去重不确定性检查项: %s" % ids
print("DEDUP_APPROX_OK disk_total_bytes=%d unique=%d" % (metrics["disk_total_bytes"], payload["host"]["disk_unique_volumes"]))
PY
