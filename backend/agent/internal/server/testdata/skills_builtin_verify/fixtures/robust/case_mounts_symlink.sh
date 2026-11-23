#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 符号链接自环作为挂载路径：os.stat 失败不得崩溃、不得丢整条产物
mkdir -p /tmp/lp
ln -sf a /tmp/lp/a
{
  printf '/dev/vda1 1000000 400000 600000 40%% /\n'
  printf '/dev/vdb1 2000000 100000 1900000 5%% /tmp/lp/a\n'
} > /tmp/df.out
make_shim df <<'SH'
cat /tmp/df.out
SH
require_shim_path
run_pkg inspect-vm-host
ok
