#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 移除 df：vm-host 必须以 warning 明确报告缺口（不按 0 静默通过）
path_without df
run_pkg inspect-vm-host
ok
