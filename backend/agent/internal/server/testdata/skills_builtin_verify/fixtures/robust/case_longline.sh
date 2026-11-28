#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 200000 字符的单行挂载路径，且它是使用率最高的挂载点：该超长串会进入 Markdown 正文而不只是
# 被裁剪的 payload，用于验证「整条产物（Markdown + JSON）256 KiB 硬上限」这条修复路径（DEFECT-2）。
_long=$(python3 -c 'print("x" * 200000)')
{
  printf '/dev/vda1 1000000 100 999900 1%% /\n'
  printf '/dev/vdb1 2000000 1999999 1 99%% /mnt/%s\n' "$_long"
} > /tmp/df.out
make_shim df <<'SH'
cat /tmp/df.out
SH
require_shim_path
run_pkg inspect-vm-host
ok
