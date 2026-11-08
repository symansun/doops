#!/usr/bin/env bash
# 日志收集打包示例：通过 doops 在目标节点打包指定日志目录，放入会话工作区后 pull 回本机。
set -euo pipefail

TARGET=""
SESSION="log-collect"
LOG_DIR="/var/log"
OUT_DIR="./logs-collected"
MTIME="-1"   # find -mtime：最近 1 天内修改过的 .log

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)  TARGET="${2:-}"; shift 2 ;;
    --session) SESSION="${2:-}"; shift 2 ;;
    --dir)     LOG_DIR="${2:-}"; shift 2 ;;
    --out)     OUT_DIR="${2:-}"; shift 2 ;;
    --mtime)   MTIME="${2:-}"; shift 2 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$TARGET" ]] || { echo "必须指定 --target <gateway-target>" >&2; exit 2; }

STAMP="$(date +%Y%m%d-%H%M%S)"
ARCHIVE="doops-logs-${STAMP}.tar.gz"
# 会话工作区默认 /root/ws/<session>（agent 端 DOOPS_WORKSPACE_ROOT 可改；
# 若目标改过该环境变量，请同步修改下面的 WS 路径）。
WS="/root/ws/${SESSION}"

echo "==> 在目标节点打包 ${LOG_DIR} (mtime ${MTIME}) -> ${WS}/${ARCHIVE}"
doops -session "$SESSION" exec --target "$TARGET" --cmd "
set -e
mkdir -p '${WS}'
cd '${LOG_DIR}'
files=\$(find . -maxdepth 2 -name '*.log' -mtime ${MTIME} 2>/dev/null || true)
if [[ -z \"\$files\" ]]; then echo 'no recent .log files'; exit 3; fi
tar -czf '${WS}/${ARCHIVE}' \$files
ls -lh '${WS}/${ARCHIVE}'
"

echo "==> 拉回本机 ${OUT_DIR}/ (doops pull 整个会话工作区)"
mkdir -p "$OUT_DIR"
doops -session "$SESSION" pull --target "$TARGET" --dest "$OUT_DIR"

echo "==> 清理目标节点工作区中的归档"
doops -session "$SESSION" exec --target "$TARGET" --cmd "rm -f '${WS}/${ARCHIVE}'"

echo "✅ 日志已收集到 ${OUT_DIR}/${ARCHIVE}（在工作区拉取结果中）"
