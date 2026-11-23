#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# LC_ALL=C：6 包在 C 区域设置下仍必须输出合法 JSON 与三值 status
all_pkgs LC_ALL=C LANG=C
ok
