#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# /proc/meminfo 为空文件：内存水位不可得必须 warning 且 detail 说明缺哪一步
: > /tmp/empty
mount --bind /tmp/empty /proc/meminfo 2>/dev/null || { echo "ROBUST_FAIL $CASE mount-proc-meminfo"; exit 1; }
run_pkg inspect-vm-host
ok
