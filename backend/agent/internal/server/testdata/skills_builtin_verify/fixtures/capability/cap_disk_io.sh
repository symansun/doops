#!/bin/sh
# SB-02 能力夹具：host.disk_io 正向采集（/proc/diskstats 可控输入）
# 断言口径：分区计数（sda1/nvme0n1p1）与虚拟设备（loop0/dm-0）必须排除，只累计整盘：
#   reads=4000、writes=6000、read_bytes=419430400（819200 扇区 * 512）、write_bytes=629145600、
#   write 最高平均延迟 0.67ms（sda 2000/3000 次）
# 需 --privileged：用 bind mount 覆盖 /proc/diskstats（与 /proc/meminfo 场景同法）
# shellcheck source=/dev/null
. /f/robust/_lib.sh

cat > /tmp/diskstats <<'DS'
   8       0 sda 1000 0 204800 500 2000 0 409600 1500 0 2000 2000
   8       1 sda1 500 0 102400 200 1000 0 204800 700 0 900 900
   7       0 loop0 10 0 80 1 10 0 80 1 0 2 2
 253       0 dm-0 300 0 2400 40 300 0 2400 40 0 80 80
 259       0 nvme0n1 3000 0 614400 900 4000 0 819200 2400 0 3300 3300
 259       1 nvme0n1p1 1000 0 204800 300 1000 0 204800 600 0 900 900
DS
bind_diskstats /tmp/diskstats
run_pkg inspect-vm-host
ok
