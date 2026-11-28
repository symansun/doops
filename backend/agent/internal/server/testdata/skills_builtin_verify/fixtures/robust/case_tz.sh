#!/bin/sh
# shellcheck source=/dev/null
. /f/robust/_lib.sh
# TZ=Asia/Tokyo：时间字段随环境变化但产物结构不变
run_pkg inspect-vm-host TZ=Asia/Tokyo
run_pkg inspect-k8s-cluster TZ=Asia/Tokyo
ok
