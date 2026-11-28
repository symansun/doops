#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# 移除 python3：6 包必须走 sh 降级分支，输出合法 JSON 且 status 非 pass
path_without python3
all_pkgs
ok
