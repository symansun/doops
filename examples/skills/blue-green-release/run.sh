#!/usr/bin/env bash
# 平滑发布/灰度示例（blue-green-release）：双容器 + nginx upstream 切换，实现零停机发布。
#
# 拓扑假设（单节点最简拓扑，多节点同理按 target 逐个执行）：
#   用户 -> nginx(:LB_PORT) -> upstream { blue(:BLUE_PORT) | green(:GREEN_PORT) }
#   两个容器 ${APP}-blue / ${APP}-green 常驻，nginx upstream 只指向活跃槽位。
#
# 发布流程：
#   1. 读状态文件确认活跃槽位（blue/green），新版本部署到 standby 槽位；
#   2. standby 槽位本地健康检查（不经过 LB，未接流量）；
#   3. 可选灰度：--canary <percent> 让新槽位按权重接部分流量，观察 --canary-seconds；
#   4. 全量切换：upstream 只指向新槽位，nginx -t && reload（平滑，不断连接）；
#   5. 经 LB 端口二次健康检查，写状态文件，旧槽位容器保留供秒级回滚。
#
# 回滚（--rollback）：upstream 切回旧槽位并 reload；旧容器若已停会先 docker start。
#
# 用法：
#   发布：bash run.sh --target prod --image registry.example.com/myapp --version v1.4.2 \
#           [--app myapp] [--canary 10] [--canary-seconds 60]
#   回滚：bash run.sh --target prod --rollback [--app myapp]
#
# 前置条件：
#   - 本机已 doops login 到 --target；生产 target 建议配 gateway 审批管控
#     （本脚本的变更类 exec 会自动触发审批闸，按提示 --approval 重放即可，
#      也可直接嵌入 release-flow 示例作为其 deploy 命令）；
#   - 目标节点已装 docker 与 nginx，nginx 已配置 server 块代理到
#     upstream ${APP}（见 README 的初始化段）；
#   - 状态目录 /var/lib/bluegreen 可写（首次运行自动创建，默认活跃槽位 blue）。
set -euo pipefail

TARGET=""
IMAGE=""
VERSION=""
APP="myapp"
BLUE_PORT=8081
GREEN_PORT=8082
CONTAINER_PORT=8080
LB_PORT=80
HEALTH_PATH="/health"
STATE_DIR="/var/lib/bluegreen"
UPSTREAM_DIR="/etc/nginx/conf.d"
CANARY=0
CANARY_SECONDS=30
SESSION="blue-green"
ROLLBACK="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)         TARGET="${2:-}"; shift 2 ;;
    --image)          IMAGE="${2:-}"; shift 2 ;;
    --version)        VERSION="${2:-}"; shift 2 ;;
    --app)            APP="${2:-}"; shift 2 ;;
    --blue-port)      BLUE_PORT="${2:-}"; shift 2 ;;
    --green-port)     GREEN_PORT="${2:-}"; shift 2 ;;
    --container-port) CONTAINER_PORT="${2:-}"; shift 2 ;;
    --lb-port)        LB_PORT="${2:-}"; shift 2 ;;
    --health-path)    HEALTH_PATH="${2:-}"; shift 2 ;;
    --canary)         CANARY="${2:-}"; shift 2 ;;
    --canary-seconds) CANARY_SECONDS="${2:-}"; shift 2 ;;
    --session)        SESSION="${2:-}"; shift 2 ;;
    --rollback)       ROLLBACK="true"; shift ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$TARGET" ]] || { echo "必须指定 --target <gateway-target>" >&2; exit 2; }
if [[ "$ROLLBACK" == "false" ]]; then
  [[ "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "--version 必须是 tag（如 v1.4.2）" >&2; exit 2; }
  [[ -n "$IMAGE" ]] || { echo "必须指定 --image <registry/repo>" >&2; exit 2; }
fi

[[ "$CANARY" =~ ^[0-9]+$ ]] || { echo "--canary 必须是 0-99 的整数（0=跳过灰度直接全量）" >&2; exit 2; }
[[ "$CANARY" -lt 100 ]] || { echo "--canary 必须小于 100（100 与直接全量等价，省略即可）" >&2; exit 2; }
[[ "$CANARY_SECONDS" =~ ^[0-9]+$ ]] || { echo "--canary-seconds 必须是非负整数" >&2; exit 2; }

STATE_FILE="${STATE_DIR}/${APP}"
UPSTREAM_CONF="${UPSTREAM_DIR}/${APP}-upstream.conf"

remote() { # 在目标节点执行命令（变更类，可能触发审批闸）
  doops -session "$SESSION" exec --target "$TARGET" --cmd "$1"
}

remote_out() { # 捕获远端 stdout 最后一行（过滤 CLI 的 TARGETING 提示）
  remote "$1" 2>/dev/null | tail -1 | tr -d '[:space:]'
}

slot_port() { [[ "$1" == "blue" ]] && echo "$BLUE_PORT" || echo "$GREEN_PORT"; }

current_slot() {
  local s
  s="$(remote_out "cat '${STATE_FILE}' 2>/dev/null || echo blue")"
  [[ "$s" == "green" ]] && echo "green" || echo "blue"
}

# 生成 upstream 配置：$1=blue 权重（0=下线），$2=green 权重
write_upstream() {
  local bw="$1" gw="$2" body=""
  [[ "$bw" -gt 0 ]] && body="  server 127.0.0.1:${BLUE_PORT} weight=${bw};"
  if [[ "$gw" -gt 0 ]]; then
    [[ -n "$body" ]] && body="${body}"$'\n'
    body="${body}  server 127.0.0.1:${GREEN_PORT} weight=${gw};"
  fi
  remote "printf 'upstream ${APP} {\n${body}\n}\n' > '${UPSTREAM_CONF}' && nginx -t && nginx -s reload"
}

wait_healthy() { # $1=port $2=描述
  remote "for i in \$(seq 1 30); do curl -fsS --max-time 3 'http://127.0.0.1:$1${HEALTH_PATH}' >/dev/null 2>&1 && { echo '$2 healthy'; exit 0; }; sleep 1; done; echo '$2 NOT healthy' >&2; exit 1"
}

# ---------------------------------------------------------------------------
# 回滚模式
# ---------------------------------------------------------------------------
if [[ "$ROLLBACK" == "true" ]]; then
  ACTIVE="$(current_slot)"
  [[ "$ACTIVE" == "blue" ]] && PREV="green" || PREV="blue"
  PREV_PORT="$(slot_port "$PREV")"
  echo "==> 回滚：当前活跃=${ACTIVE}，切回 ${PREV}(:${PREV_PORT})"
  remote "docker start '${APP}-${PREV}' 2>/dev/null || true"
  wait_healthy "$PREV_PORT" "旧槽位 ${PREV}"
  if [[ "$PREV" == "blue" ]]; then write_upstream 1 0; else write_upstream 0 1; fi
  remote "mkdir -p '${STATE_DIR}' && echo '${PREV}' > '${STATE_FILE}'"
  wait_healthy "$LB_PORT" "LB(:${LB_PORT})"
  echo "✅ 已回滚到 ${PREV} 槽位"
  exit 0
fi

# ---------------------------------------------------------------------------
# 发布模式
# ---------------------------------------------------------------------------
ACTIVE="$(current_slot)"
[[ "$ACTIVE" == "blue" ]] && STANDBY="green" || STANDBY="blue"
STANDBY_PORT="$(slot_port "$STANDBY")"
ACTIVE_PORT="$(slot_port "$ACTIVE")"

echo "==> [1/5] 当前活跃=${ACTIVE}(:${ACTIVE_PORT})，部署 ${IMAGE}:${VERSION} 到 standby=${STANDBY}(:${STANDBY_PORT})"
remote "docker pull '${IMAGE}:${VERSION}' && { docker rm -f '${APP}-${STANDBY}' 2>/dev/null || true; }"
remote "docker run -d --name '${APP}-${STANDBY}' --restart unless-stopped -p 127.0.0.1:${STANDBY_PORT}:${CONTAINER_PORT} '${IMAGE}:${VERSION}'"

echo "==> [2/5] standby 槽位健康检查（未接流量）"
wait_healthy "$STANDBY_PORT" "standby ${STANDBY}"

if [[ "$CANARY" -gt 0 && "$CANARY" -lt 100 ]]; then
  echo "==> [3/5] 灰度：新槽位权重 ${CANARY}%，观察 ${CANARY_SECONDS}s"
  if [[ "$ACTIVE" == "blue" ]]; then write_upstream "$((100 - CANARY))" "$CANARY"; else write_upstream "$CANARY" "$((100 - CANARY))"; fi
  sleep "$CANARY_SECONDS"
  wait_healthy "$LB_PORT" "灰度期 LB(:${LB_PORT})"
else
  echo "==> [3/5] （未配置 --canary，跳过灰度直接全量）"
fi

echo "==> [4/5] 全量切换到 ${STANDBY} 槽位"
if [[ "$STANDBY" == "blue" ]]; then write_upstream 1 0; else write_upstream 0 1; fi

echo "==> [5/5] 经 LB 验证 + 写状态文件（旧槽位 ${ACTIVE} 保留供回滚）"
remote "mkdir -p '${STATE_DIR}' && echo '${STANDBY}' > '${STATE_FILE}'"
wait_healthy "$LB_PORT" "LB(:${LB_PORT})"

echo "✅ 发布完成：${IMAGE}:${VERSION} -> ${STANDBY} 槽位（旧槽位 ${ACTIVE} 保留）"
echo "   回滚命令：bash $0 --target ${TARGET} --app ${APP} --rollback"
