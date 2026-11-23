#!/usr/bin/env sh
# SB-01 红线 2 夹具 A（容器内执行）：同一 ext4 卷以 loop + bind 两次挂载，
# 用真实 /proc/mounts 证明夹具生效，并断言该卷在 disk_total_bytes 中只计一次。
# 依赖：mkfs.ext4、mount、python3；需以 --privileged 运行。
set -eu

SLUG_DIR=/w/inspect-vm-host
if [ "$#" -ge 1 ]; then
  SLUG_DIR="$1"
fi
IMG=/tmp/sb01_dedup.img
OUT=/tmp/sb01_dedup_output.md

dd if=/dev/zero of="$IMG" bs=1M count=64 status=none
mkfs.ext4 -q "$IMG"
mkdir -p /mnt/sb01-a /mnt/sb01-b
mount -o loop "$IMG" /mnt/sb01-a
mount --bind /mnt/sb01-a /mnt/sb01-b

sh "$SLUG_DIR/scripts/inspect.sh" > "$OUT"

python3 - "$OUT" <<'PY'
import json
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
blocks = re.findall(r"```json\n(.*?)```", text, re.S)
assert blocks, "输出缺少 json 代码块"
payload = json.loads(blocks[-1])
assert payload.get("status") in ("pass", "warning", "critical"), payload.get("status")

raw = open("/proc/mounts", encoding="utf-8").read()
assert "/mnt/sb01-a" in raw and "/mnt/sb01-b" in raw, "夹具挂载点未生效"

mounts = payload.get("mounts") or []
pairs = [m for m in mounts if str(m.get("mount", "")).startswith("/mnt/sb01-")]
assert len(pairs) >= 1, "未采集到夹具挂载点: %s" % mounts
nonshared = [m for m in pairs if not m.get("shared_volume")]
assert len(nonshared) == 1, "同一卷被重复计入 disk_total_bytes: %s" % pairs

metrics = {m.get("key"): m.get("value") for m in (payload.get("metrics") or [])}
assert "disk_total_bytes" in metrics, "缺少 disk_total_bytes"
calc_gib = sum(float(m.get("total_gib") or 0) for m in mounts if not m.get("shared_volume"))
reported_gib = float(metrics["disk_total_bytes"]) / (1024 ** 3)
assert abs(calc_gib - reported_gib) <= 0.02 * max(1, len(mounts)), (calc_gib, reported_gib)
assert payload.get("output_bytes", 0) < 256 * 1024, "单条产物超过 256 KiB"
print("DEDUP_MOUNT_OK fixture_rows=%d nonshared=%d disk_total_bytes=%d" % (len(pairs), len(nonshared), metrics["disk_total_bytes"]))
PY

umount /mnt/sb01-b || true
umount /mnt/sb01-a || true
