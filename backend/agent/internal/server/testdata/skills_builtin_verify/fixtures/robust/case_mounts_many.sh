#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 5000 个挂载点：载荷必须被裁剪并显式报告 output.size，JSON 仍完整合法
{
  printf '/dev/vda1 1000000 400000 600000 40%% /\n'
  awk 'BEGIN { for (i = 0; i < 5000; i++) printf "/dev/loop%d %d 100 900 10%% /mnt/l%d\n", i, 2000000 + i, i }'
} > /tmp/df.out
make_shim df <<'SH'
cat /tmp/df.out
SH
require_shim_path
run_pkg inspect-vm-host
ok
