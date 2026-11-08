#!/usr/bin/env bash
# nginx 巡检示例：通过 doops 在目标节点检查 nginx 运行状态、配置语法与近期错误日志。
set -euo pipefail

TARGET=""
SESSION="nginx-patrol"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)  TARGET="${2:-}"; shift 2 ;;
    --session) SESSION="${2:-}"; shift 2 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$TARGET" ]] || { echo "必须指定 --target <gateway-target>" >&2; exit 2; }

run() {
  echo "==> $1"
  doops -session "$SESSION" exec --target "$TARGET" --cmd "$2"
}

run "nginx 进程状态" \
  'systemctl is-active nginx 2>/dev/null || pgrep -x nginx >/dev/null && echo nginx-running || { echo nginx-NOT-running; exit 1; }'

run "配置语法检查 (nginx -t)" \
  'nginx -t 2>&1'

run "监听端口" \
  '(ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null) | grep -E ":80|:443" || echo "warn: 未发现 80/443 监听"'

run "错误日志尾部 (最近 20 行)" \
  'tail -n 20 /var/log/nginx/error.log 2>/dev/null || echo "warn: 无 error.log"'

echo "✅ nginx 巡检完成 (target=$TARGET)"
