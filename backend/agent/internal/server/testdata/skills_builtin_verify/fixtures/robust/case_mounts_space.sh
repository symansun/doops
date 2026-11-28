#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 挂载路径含空格与中文：df -P 的分列必须完整保留路径（parts[5:] 拼接）
{
  printf '/dev/vda1 1000000 400000 600000 40%% /\n'
  printf '/dev/vdb1 2000000 100000 1900000 5%% /mnt/我的 数据\n'
} > /tmp/df.out
make_shim df <<'SH'
cat /tmp/df.out
SH
require_shim_path
run_pkg inspect-vm-host
ok
