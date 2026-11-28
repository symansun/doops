#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# /proc/loadavg 置空：负载不可得必须降级并说明（bind 源必须是文件，不能是目录）
: > /tmp/empty
mount --bind /tmp/empty /proc/loadavg 2>/dev/null || { echo "ROBUST_FAIL $CASE mount-proc-loadavg"; exit 1; }
run_pkg inspect-vm-host
ok
