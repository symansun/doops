#!/usr/bin/env bash
# DoOps E2E 冒烟：gateway + agent + CLI 全链路 login -> exec -> 审计落盘。
#
# 模式：
#   --mode compose  （默认，需 docker + docker compose）容器化拓扑，见 deploy/e2e/docker-compose.yml
#   --mode process  裸机进程拓扑（无 docker 环境 / CI 使用），直接 go build 后启动 gateway/agent 进程
#   --mode auto     有 docker 用 compose，否则降级 process
#
# 脚本可重复执行；失败时自动 dump gateway / agent / CLI 三方日志并以非零退出。

set -euo pipefail

MODE="auto"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      MODE="$2"; shift 2 ;;
    --mode=*)
      MODE="${1#*=}"; shift ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)
      echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
E2E_DIR="${REPO_ROOT}/deploy/e2e"
COMPOSE_FILE="${E2E_DIR}/docker-compose.yml"
PROJECT="doops-e2e"

CLUSTER="e2e"
INSTANCE="agent-1"
E2E_USER="e2e"
# 演示环境专用的一次性密码，仅存在于冒烟生命周期内，非真实凭证。
E2E_PASSWORD="E2e-smoke-12345"
SESSION="e2e-smoke"
EXPECT_STRING="doops-e2e-ok"

WORKDIR="$(mktemp -d /tmp/doops-e2e.XXXXXX)"
DOOPS_BIN="${WORKDIR}/doops"
export DOOPS_CONFIG="${WORKDIR}/config.json"

GATEWAY_PID=""
AGENT_PID=""
STACK_STARTED="false"

log() { printf '\033[96m[e2e]\033[0m %s\n' "$*"; }
fail() { printf '\033[91m[e2e][FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

port_in_use() { (echo >/dev/tcp/127.0.0.1/"$1") 2>/dev/null; }

pick_port() {
  local p
  for p in "$@"; do
    if ! port_in_use "$p"; then echo "$p"; return 0; fi
  done
  return 1
}

GATEWAY_PORT="$(pick_port 42222 43222 44222 45222)" || fail "没有可用的 gateway 端口"
AGENT_PORT="$(pick_port 42223 43223 44223 45223)" || fail "没有可用的 agent 端口"

COMPOSE=(docker compose --project-name "$PROJECT" -f "$COMPOSE_FILE")

# ---------------------------------------------------------------------------
# 清理与失败日志
# ---------------------------------------------------------------------------
dump_logs() {
  echo "==================== CLI add 输出 ===================="
  cat "${WORKDIR}/add.log" 2>/dev/null || true
  echo "==================== CLI login 输出 =================="
  cat "${WORKDIR}/login.log" 2>/dev/null || true
  echo "==================== CLI exec 输出 ==================="
  cat "${WORKDIR}/exec.out" 2>/dev/null || true
  if [[ "$MODE" == "compose" ]]; then
    echo "==================== docker compose ps =============="
    "${COMPOSE[@]}" ps || true
    echo "==================== gateway 日志 ===================="
    "${COMPOSE[@]}" logs --no-color gateway 2>&1 || true
    echo "==================== agent 日志 ======================"
    "${COMPOSE[@]}" logs --no-color agent 2>&1 || true
  else
    echo "==================== gateway 日志 ===================="
    cat "${WORKDIR}/gateway.log" 2>/dev/null || true
    echo "==================== agent 日志 ======================"
    cat "${WORKDIR}/agent.log" 2>/dev/null || true
  fi
}

cleanup() {
  local code=$?
  set +e
  if [[ $code -ne 0 ]]; then
    echo "==================== 冒烟失败，dump 现场 =============" >&2
    dump_logs >&2
  fi
  if [[ "$MODE" == "compose" ]]; then
    if [[ "$STACK_STARTED" == "true" ]]; then
      "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1
    fi
  else
    [[ -n "$AGENT_PID" ]] && kill "$AGENT_PID" 2>/dev/null
    [[ -n "$GATEWAY_PID" ]] && kill "$GATEWAY_PID" 2>/dev/null
    wait 2>/dev/null
  fi
  rm -rf "$WORKDIR"
  exit $code
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# 公共步骤
# ---------------------------------------------------------------------------
build_cli() {
  log "构建 CLI -> ${DOOPS_BIN}"
  (cd "${REPO_ROOT}/skills/doops-cli" && CGO_ENABLED=0 go build -o "$DOOPS_BIN" ./cli/doops)
}

wait_gateway_health() {
  log "等待 gateway /health (127.0.0.1:${GATEWAY_PORT}) ..."
  local i
  for i in $(seq 1 60); do
    if curl -fsS "http://127.0.0.1:${GATEWAY_PORT}/health" >/dev/null 2>&1; then
      log "gateway 已就绪"
      return 0
    fi
    sleep 1
  done
  fail "gateway 健康检查超时"
}

cli_add_and_login() {
  log "CLI add target (gateway=http://127.0.0.1:${GATEWAY_PORT} ${CLUSTER}/${INSTANCE})"
  "$DOOPS_BIN" add --name e2e \
    --gateway "http://127.0.0.1:${GATEWAY_PORT}" \
    --cluster "$CLUSTER" --instance "$INSTANCE" \
    --token bootstrap-placeholder >"${WORKDIR}/add.log" 2>&1

  log "CLI login (username=${E2E_USER})"
  "$DOOPS_BIN" login --target e2e \
    --username "$E2E_USER" --password "$E2E_PASSWORD" >"${WORKDIR}/login.log" 2>&1 \
    || fail "doops login 失败"
  grep -q "Successfully cached gateway token" "${WORKDIR}/login.log" || fail "login 输出不符合预期"
}

wait_agent_online() {
  log "等待 agent 在 gateway 上线 (${CLUSTER}/${INSTANCE}) ..."
  local i
  for i in $(seq 1 45); do
    if "$DOOPS_BIN" targets --target e2e 2>/dev/null | grep -q "$INSTANCE"; then
      log "agent 已上线"
      return 0
    fi
    sleep 1
  done
  fail "等待 agent 上线超时"
}

cli_exec_assert() {
  log "CLI exec: echo ${EXPECT_STRING} (session=${SESSION})"
  "$DOOPS_BIN" -session "$SESSION" exec --target e2e --cmd "echo ${EXPECT_STRING}" 2>&1 | tee "${WORKDIR}/exec.out"
  grep -q "$EXPECT_STRING" "${WORKDIR}/exec.out" || fail "exec 输出中未找到 ${EXPECT_STRING}"
  log "exec 输出断言通过"
}

assert_audit_content() {
  local content="$1"
  echo "$content" | grep -q "echo ${EXPECT_STRING}" || fail "审计日志缺少命令记录"
  echo "$content" | grep -q "exit=0" || fail "审计日志缺少 exit=0 记录"
  log "审计落盘断言通过"
}

# ---------------------------------------------------------------------------
# compose 模式
# ---------------------------------------------------------------------------
compose_seed() {
  log "播种 gateway SQLite：创建用户 / 授权 / 签发 agent-token"
  local seed_out
  seed_out="$("${COMPOSE[@]}" run --rm --no-deps -T \
    -e E2E_USER="$E2E_USER" -e E2E_PASSWORD="$E2E_PASSWORD" \
    -e E2E_CLUSTER="$CLUSTER" -e E2E_INSTANCE="$INSTANCE" \
    gateway sh -c '
      set -e
      /app/doops-gateway user create -name "$E2E_USER" -password "$E2E_PASSWORD" -db /data/gateway.db
      /app/doops-gateway grant -user "$E2E_USER" -cluster "$E2E_CLUSTER" -instance "$E2E_INSTANCE" -db /data/gateway.db
      /app/doops-gateway agent-token create -cluster "$E2E_CLUSTER" -instance "$E2E_INSTANCE" -db /data/gateway.db
    ')" || fail "gateway 数据播种失败"
  AGENT_TOKEN="$(printf '%s\n' "$seed_out" | sed -n 's/^agent_token=//p' | tail -1)"
  [[ -n "$AGENT_TOKEN" ]] || fail "未解析到 agent_token"
}

run_compose() {
  command -v docker >/dev/null 2>&1 || fail "compose 模式需要 docker"
  docker compose version >/dev/null 2>&1 || fail "compose 模式需要 docker compose"

  export E2E_GATEWAY_PORT="$GATEWAY_PORT"
  export E2E_CLUSTER="$CLUSTER"
  export E2E_INSTANCE="$INSTANCE"

  log "构建 E2E 镜像（doops-e2e:local）"
  "${COMPOSE[@]}" build --quiet

  # 幂等：先清掉可能残留的上一轮 stack
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  STACK_STARTED="true"

  compose_seed

  export AGENT_TOKEN
  log "启动 gateway + agent"
  "${COMPOSE[@]}" up -d gateway agent

  wait_gateway_health
  build_cli
  cli_add_and_login
  wait_agent_online
  cli_exec_assert

  log "断言 agent 容器内审计日志 /root/ws/${SESSION}/.doops-audit-log"
  local audit
  audit="$("${COMPOSE[@]}" exec -T agent cat "/root/ws/${SESSION}/.doops-audit-log" 2>/dev/null)" \
    || fail "读取 agent 容器内审计日志失败"
  assert_audit_content "$audit"
}

# ---------------------------------------------------------------------------
# process 模式（无 docker：直接起进程，CI 使用）
# ---------------------------------------------------------------------------
run_process() {
  log "构建 gateway / agent 二进制"
  (cd "${REPO_ROOT}/agent" \
    && CGO_ENABLED=0 go build -o "${WORKDIR}/doops-gateway" ./cmd/gateway \
    && CGO_ENABLED=0 go build -o "${WORKDIR}/doops-agent" ./cmd/agent)

  local DB="${WORKDIR}/gateway.db"
  log "播种 gateway SQLite：创建用户 / 授权 / 签发 agent-token"
  "${WORKDIR}/doops-gateway" user create -name "$E2E_USER" -password "$E2E_PASSWORD" -db "$DB" >/dev/null
  "${WORKDIR}/doops-gateway" grant -user "$E2E_USER" -cluster "$CLUSTER" -instance "$INSTANCE" -db "$DB" >/dev/null
  AGENT_TOKEN="$("${WORKDIR}/doops-gateway" agent-token create -cluster "$CLUSTER" -instance "$INSTANCE" -db "$DB" | sed -n 's/^agent_token=//p')"
  [[ -n "$AGENT_TOKEN" ]] || fail "未解析到 agent_token"

  log "启动 gateway 进程 (127.0.0.1:${GATEWAY_PORT})"
  "${WORKDIR}/doops-gateway" serve -port "$GATEWAY_PORT" -db "$DB" \
    >"${WORKDIR}/gateway.log" 2>&1 &
  GATEWAY_PID=$!

  wait_gateway_health

  log "启动 agent 进程 (注册到 gateway, cluster=${CLUSTER} instance=${INSTANCE})"
  DOOPS_WORKSPACE_ROOT="${WORKDIR}/ws" \
  DOOPS_AGENT_TOKEN_PATH="${WORKDIR}/agent-token" \
    "${WORKDIR}/doops-agent" -port "$AGENT_PORT" \
    -gateway-url "ws://127.0.0.1:${GATEWAY_PORT}" \
    -cluster "$CLUSTER" -instance "$INSTANCE" \
    -agent-token "$AGENT_TOKEN" \
    >"${WORKDIR}/agent.log" 2>&1 &
  AGENT_PID=$!

  build_cli
  cli_add_and_login
  wait_agent_online
  cli_exec_assert

  log "断言 agent 工作区审计日志 ${WORKDIR}/ws/${SESSION}/.doops-audit-log"
  local audit_file="${WORKDIR}/ws/${SESSION}/.doops-audit-log"
  [[ -f "$audit_file" ]] || fail "审计日志不存在: ${audit_file}"
  assert_audit_content "$(cat "$audit_file")"
}

# ---------------------------------------------------------------------------
# 入口
# ---------------------------------------------------------------------------
if [[ "$MODE" == "auto" ]]; then
  if docker info >/dev/null 2>&1; then
    MODE="compose"
  else
    MODE="process"
  fi
fi
log "模式: ${MODE}  工作目录: ${WORKDIR}  gateway 端口: ${GATEWAY_PORT}"

case "$MODE" in
  compose) run_compose ;;
  process) run_process ;;
  *) fail "未知模式: ${MODE}（可选 auto|compose|process）" ;;
esac

log "✅ E2E 冒烟通过：login -> exec(${EXPECT_STRING}) -> 审计落盘 全链路验证成功"
