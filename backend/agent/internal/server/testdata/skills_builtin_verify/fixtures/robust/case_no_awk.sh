#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 移除 awk：6 包不得因缺少该命令崩溃或产出非法 JSON
path_without awk
all_pkgs
ok
