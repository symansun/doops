#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 移除 free：6 包不得因缺少该命令崩溃或产出非法 JSON
path_without free
all_pkgs
ok
