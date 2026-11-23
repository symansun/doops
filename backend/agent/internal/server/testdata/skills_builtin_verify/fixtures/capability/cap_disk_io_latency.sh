#!/bin/sh
# SB-02 能力夹具：host.disk_io 延迟告警路径（对齐 monitor_engine_types.go:20 DiskLatencyWarnMs=50ms）
# 输入 sda 读 1000 次耗时 60000ms + 写 1000 次耗时 60000ms => 平均 60ms，必须 warning（不是 pass，也不静默 0）
# 需 --privileged：bind mount 覆盖 /proc/diskstats
# shellcheck source=/dev/null
. /f/robust/_lib.sh

cat > /tmp/diskstats <<'DS'
   8       0 sda 1000 0 204800 60000 1000 0 409600 60000 0 2000 2000
DS
bind_diskstats /tmp/diskstats
run_pkg inspect-vm-host
ok
