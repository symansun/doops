#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# LC_ALL=ja_JP.UTF-8（宿主未安装该 locale）：不得崩溃、不得输出非法 JSON
all_pkgs LC_ALL=ja_JP.UTF-8 LANG=ja_JP.UTF-8
ok
