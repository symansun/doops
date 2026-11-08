#!/usr/bin/env bash
# 证书到期巡检示例：通过 doops 在目标节点检查一组域名的 TLS 证书剩余天数。
set -euo pipefail

TARGET=""
SESSION="cert-expiry-check"
WARN_DAYS="30"
DOMAINS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)    TARGET="${2:-}"; shift 2 ;;
    --session)   SESSION="${2:-}"; shift 2 ;;
    --warn-days) WARN_DAYS="${2:-}"; shift 2 ;;
    --domains)   DOMAINS="${2:-}"; shift 2 ;;   # 逗号分隔，如 example.com,api.example.com
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$TARGET" ]]  || { echo "必须指定 --target <gateway-target>" >&2; exit 2; }
[[ -n "$DOMAINS" ]] || { echo "必须指定 --domains a.com,b.com" >&2; exit 2; }

# 在目标节点上逐域名检查证书剩余天数；小于 WARN_DAYS 时该域名行标记 WARN，整体退出码为 1。
REMOTE_SCRIPT='
warn_days='"${WARN_DAYS}"'
rc=0
for d in '"$(printf '%s' "$DOMAINS" | tr ',' ' ')"'; do
  end=$(echo | openssl s_client -servername "$d" -connect "$d:443" 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
  if [[ -z "$end" ]]; then
    echo "ERROR $d 无法获取证书"
    rc=1
    continue
  fi
  end_ts=$(date -d "$end" +%s 2>/dev/null || date -j -f "%b %e %T %Y %Z" "$end" +%s)
  days=$(( (end_ts - $(date +%s)) / 86400 ))
  if (( days < warn_days )); then
    echo "WARN  $d 剩余 ${days} 天 (到期: $end)"
    rc=1
  else
    echo "OK    $d 剩余 ${days} 天 (到期: $end)"
  fi
done
exit $rc
'

echo "==> 证书到期巡检 (warn<${WARN_DAYS}天): ${DOMAINS}"
doops -session "$SESSION" exec --target "$TARGET" --cmd "$REMOTE_SCRIPT"
