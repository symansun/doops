#!/usr/bin/env sh
# SB-01 夹具 C（容器内，需 --privileged）：回归 QA-01 D3。
# 独立 loop 设备上再叠 overlay：overlay 的 statfs 报底层设备原始容量（65536KiB），
# lowerdir 的 ext4 报可用容量（56037KiB），st_dev 不同、source 字面量不同、三元组不相等，
# 但两者确为同一卷。断言：只计一次（不得 128MiB 翻倍）、不得并入 "/"、并给出不确定性告警。
set -eu

SLUG_DIR=/w/inspect-vm-host
if [ "$#" -ge 1 ]; then
  SLUG_DIR="$1"
fi
IMG=/tmp/sb01_ld.img
OUT=/tmp/sb01_ld_output.md

dd if=/dev/zero of="$IMG" bs=1M count=64 status=none
mkfs.ext4 -q "$IMG"
mkdir -p /mnt/sb01-a /dev/shm/sb01-up /dev/shm/sb01-work /mnt/sb01-ov
mount -o loop "$IMG" /mnt/sb01-a
mount -t overlay overlay -o lowerdir=/mnt/sb01-a,upperdir=/dev/shm/sb01-up,workdir=/dev/shm/sb01-work /mnt/sb01-ov

sh "$SLUG_DIR/scripts/inspect.sh" > "$OUT"

python3 - "$OUT" <<'PY'
import json
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
payload = json.loads(re.findall(r"```json\n(.*?)```", text, re.S)[-1])
assert payload.get("status") in ("pass", "warning", "critical"), payload.get("status")
assert "/mnt/sb01-ov" in open("/proc/mounts", encoding="utf-8").read(), "overlay 夹具未生效"

by = {m.get("mount"): m for m in (payload.get("mounts") or [])}
assert "/mnt/sb01-ov" in by and "/mnt/sb01-a" in by, "两挂载点都应采集: %s" % sorted(by)
assert by["/mnt/sb01-ov"].get("shared_volume") is False, "代表卷不应被判重复: %s" % by["/mnt/sb01-ov"]
assert by["/mnt/sb01-a"].get("shared_volume") is True, "loop 卷未与 overlay 去重（D3 回归）: %s" % by["/mnt/sb01-a"]
reason = by["/mnt/sb01-a"].get("dedupe_reason") or ""
assert "/mnt/sb01-ov" in reason, "去重对象错误（不得并入 /）: %s" % reason

metrics = {m.get("key"): m.get("value") for m in (payload.get("metrics") or [])}
reported_gib = float(metrics["disk_total_bytes"]) / (1024 ** 3)
calc_gib = sum(float(m.get("total_gib") or 0) for m in by.values() if not m.get("shared_volume"))
assert abs(calc_gib - reported_gib) <= 0.02 * max(1, len(by)), (calc_gib, reported_gib)
naive_gib = sum(float(m.get("total_gib") or 0) for m in by.values())
saved_gib = naive_gib - reported_gib
assert saved_gib >= 0.03, "同卷被重复计入（红线 2）: naive=%s reported=%s" % (naive_gib, reported_gib)
assert payload.get("host", {}).get("disk_unique_volumes") == len([m for m in by.values() if not m.get("shared_volume")])
ids = [c.get("id") for c in (payload.get("checks") or [])]
assert "host.disk_dedup" in ids, "近似去重缺少告警检查项: %s" % ids
print("DEDUP_LOWERDIR_OK reported_gib=%.2f naive_gib=%.2f saved_gib=%.2f unique=%d" % (reported_gib, naive_gib, saved_gib, payload["host"]["disk_unique_volumes"]))
PY

umount /mnt/sb01-ov 2>/dev/null || true
umount /mnt/sb01-a 2>/dev/null || true
