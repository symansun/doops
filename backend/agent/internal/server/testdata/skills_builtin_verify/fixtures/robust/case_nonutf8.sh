#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# df 输出混入非 UTF-8 字节：修复前 text=True 严格解码抛 UnicodeDecodeError 被吞 → 整段磁盘数据静默丢失
{
  printf '/dev/vda1 1000000 400000 600000 40%% /\n'
  printf '/dev/vdb1 2000000 100000 1900000 5%% /mnt/data\377\376\n'
} > /tmp/df.out
make_shim df <<'SH'
cat /tmp/df.out
SH
require_shim_path
run_pkg inspect-vm-host
ok
