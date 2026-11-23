#!/bin/sh
# SB-02 能力夹具：/proc/diskstats 不可读时的降级路径（不得编造 0 值，也不得静默通过）
# 期望：check host.disk_io status=warning 且 detail 点名 /proc/diskstats；metrics 里不得出现 disk_io_*
# 需 --privileged：bind mount 空文件覆盖 /proc/diskstats
# shellcheck source=/dev/null
. /f/robust/_lib.sh

: > /tmp/empty
bind_diskstats /tmp/empty
run_pkg inspect-vm-host
ok
