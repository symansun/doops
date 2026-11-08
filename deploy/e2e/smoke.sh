#!/usr/bin/env bash
# DoOps E2E: real Gateway + Agent + CLI login -> exec -> persisted command audit.
# Usage: bash deploy/e2e/smoke.sh [--mode process|compose]
# Default process mode requires Linux, Go, Python 3, curl, and root or passwordless
# sudo to run the Agent with its real /root/.doops/audit path. Compose mode is an
# explicit alternative requiring Docker and Docker Compose; modes never switch.
# Each run owns a unique session/topology. Failure logs are retained after removing
# temporary credentials; success removes its processes, audit file and workdir.

set -euo pipefail
umask 077

MODE="process"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode) [[ $# -ge 2 ]] || { echo '--mode needs process or compose' >&2; exit 2; }; MODE="$2"; shift 2 ;;
    --mode=*) MODE="${1#*=}"; shift ;;
    -h|--help) sed -n '2,8s/^# \{0,1\}//p' "$0"; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
case "$MODE" in
  process) [[ "$(uname -s)" == Linux ]] || { echo 'process mode requires Linux; select --mode compose explicitly on other hosts' >&2; exit 2; } ;;
  compose) ;;
  *) echo 'Supported modes are process and compose' >&2; exit 2 ;;
esac
for dependency in go python3 curl; do
  command -v "$dependency" >/dev/null 2>&1 || { echo "Required command is missing: $dependency" >&2; exit 2; }
done

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
E2E_DIR="${REPO_ROOT}/deploy/e2e"
WORKDIR="$(mktemp -d /tmp/doops-e2e.XXXXXXXX)"
PRIVATE_DIR="${WORKDIR}/private"
LOG_DIR="${WORKDIR}/logs"
mkdir "${PRIVATE_DIR}" "${LOG_DIR}" "${WORKDIR}/bin"
DOOPS_BIN="${WORKDIR}/bin/doops"
SESSION="e2e-smoke-${WORKDIR##*.}"
PROJECT="$(printf '%s' "$SESSION" | tr '[:upper:]' '[:lower:]')"
CLUSTER="e2e"
INSTANCE="agent-1"
EXPECT_STRING="doops-e2e-ok"
AUDIT_FILE="/root/.doops/audit/${SESSION}.log"
export DOOPS_CONFIG="${PRIVATE_DIR}/config.json"
export DOOPS_CLI_CALL_TIMEOUT=20s
GATEWAY_PID=""
AGENT_LAUNCHER_PID=""
STACK_STARTED=false
AUDIT_PREPARED=false
AUDIT_DIR_CREATED=false
DOOPS_DIR_CREATED=false
PRIVILEGED=()
COMPOSE=(docker compose --project-name "$PROJECT" -f "${E2E_DIR}/docker-compose.yml")

log() { printf '[e2e] %s\n' "$*"; }
fail() { printf '[e2e][FAIL] %s\n' "$*" >&2; exit 1; }

redact_logs() {
  python3 - "$PRIVATE_DIR" "$LOG_DIR" <<'PY'
import json
from pathlib import Path
import sys
private, logs = map(Path, sys.argv[1:])
secrets = []
for name, key in (("login-request.json", "password"), ("login-response.json", "token")):
    path = private / name
    if path.exists():
        try:
            value = json.loads(path.read_text()).get(key)
        except (ValueError, AttributeError):
            continue
        if isinstance(value, str) and value:
            secrets.append(value)
for path in logs.iterdir():
    if path.is_file():
        content = path.read_text(errors="replace")
        for value in secrets:
            content = content.replace(value, "[REDACTED]")
        path.write_text(content)
PY
}

cleanup() {
  local code=$? agent_pid="" i
  trap - EXIT
  set +e
  if [[ "$MODE" == compose && "$STACK_STARTED" == true ]]; then
    "${COMPOSE[@]}" ps >"${LOG_DIR}/compose-ps.log" 2>&1
    "${COMPOSE[@]}" logs --no-color gateway >"${LOG_DIR}/gateway.log" 2>&1
    "${COMPOSE[@]}" logs --no-color agent >"${LOG_DIR}/agent.log" 2>&1
    "${COMPOSE[@]}" down -v --remove-orphans >"${LOG_DIR}/compose-cleanup.log" 2>&1 || code=1
    if docker image inspect "$E2E_IMAGE" >/dev/null 2>&1; then
      docker image rm "$E2E_IMAGE" >>"${LOG_DIR}/compose-cleanup.log" 2>&1 || code=1
    fi
  elif [[ "$MODE" == process ]]; then
    if [[ -f "${WORKDIR}/agent.pid" ]]; then
      agent_pid="$(cat "${WORKDIR}/agent.pid")"
      [[ "$agent_pid" =~ ^[0-9]+$ ]] && "${PRIVILEGED[@]}" kill "$agent_pid" 2>/dev/null
    fi
    [[ -n "$GATEWAY_PID" ]] && kill "$GATEWAY_PID" 2>/dev/null
    for i in {1..30}; do
      [[ -z "$AGENT_LAUNCHER_PID" ]] || kill -0 "$AGENT_LAUNCHER_PID" 2>/dev/null || break
      sleep 0.1
    done
    if [[ -n "$AGENT_LAUNCHER_PID" ]] && kill -0 "$AGENT_LAUNCHER_PID" 2>/dev/null; then
      [[ "$agent_pid" =~ ^[0-9]+$ ]] && "${PRIVILEGED[@]}" kill -KILL "$agent_pid" 2>/dev/null
    fi
    [[ -n "$AGENT_LAUNCHER_PID" ]] && wait "$AGENT_LAUNCHER_PID" 2>/dev/null
    [[ -n "$GATEWAY_PID" ]] && wait "$GATEWAY_PID" 2>/dev/null
    if [[ "$AUDIT_PREPARED" == true ]]; then
      "${PRIVILEGED[@]}" cat "$AUDIT_FILE" >"${LOG_DIR}/audit.log" 2>/dev/null
      "${PRIVILEGED[@]}" rm -f -- "$AUDIT_FILE"
    fi
    [[ "$AUDIT_DIR_CREATED" == true ]] && "${PRIVILEGED[@]}" rmdir /root/.doops/audit 2>/dev/null
    [[ "$DOOPS_DIR_CREATED" == true ]] && "${PRIVILEGED[@]}" rmdir /root/.doops 2>/dev/null
    "${PRIVILEGED[@]}" rm -rf -- "${WORKDIR}/ws"
  fi
  if ! redact_logs; then
    echo '[e2e][FAIL] Log redaction failed; logs will not be exposed' >&2
    rm -rf -- "$LOG_DIR"
    code=1
  fi
  rm -rf -- "$PRIVATE_DIR" "${WORKDIR:?}/bin"
  if [[ $code -ne 0 ]]; then
    for file in "${LOG_DIR}"/*; do
      [[ -f "$file" ]] || continue
      printf '\n[e2e] %s\n' "${file##*/}" >&2
      cat "$file" >&2
    done
    printf '[e2e] Failure logs retained: %s\n' "$LOG_DIR" >&2
  else
    rm -rf -- "$WORKDIR"
  fi
  exit "$code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [[ -n "${GITHUB_ENV:-}" ]]; then
  printf 'DOOPS_E2E_LOG_DIR=%s\n' "$LOG_DIR" >>"$GITHUB_ENV"
fi

python3 - "$PRIVATE_DIR" "$CLUSTER" "$INSTANCE" "$SESSION" <<'PY'
import json
from pathlib import Path
import secrets
import sys
private = Path(sys.argv[1])
credentials = {"username": "e2e", "password": "E2e-" + secrets.token_urlsafe(24)}
(private / "seed.json").write_text(json.dumps({**credentials, "cluster": sys.argv[2], "instance": sys.argv[3]}))
(private / "login-request.json").write_text(json.dumps({**credentials, "name": sys.argv[4]}))
PY

read -r GATEWAY_PORT AGENT_PORT < <(python3 - <<'PY'
import socket
with socket.socket() as gateway, socket.socket() as agent:
    gateway.bind(("127.0.0.1", 0))
    agent.bind(("127.0.0.1", 0))
    print(gateway.getsockname()[1], agent.getsockname()[1])
PY
)

build_cli() {
  log 'Building CLI'
  (cd "${REPO_ROOT}/backend/cli" && CGO_ENABLED=0 go build -o "$DOOPS_BIN" ./cli/doops) >"${LOG_DIR}/build-cli.log" 2>&1
}

wait_gateway_health() {
  local i
  for ((i=0; i<60; i++)); do
    if curl --fail --silent --show-error --max-time 2 "http://127.0.0.1:${GATEWAY_PORT}/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail 'Gateway health check timed out'
}

cli_add_and_login() {
  log 'Adding target and authenticating through the real Gateway'
  "$DOOPS_BIN" -session "$SESSION" add --name e2e \
    --gateway "http://127.0.0.1:${GATEWAY_PORT}" --cluster "$CLUSTER" --instance "$INSTANCE" >"${LOG_DIR}/add.log" 2>&1
  curl --fail --silent --show-error --max-time 15 --header 'Content-Type: application/json' \
    --data-binary "@${PRIVATE_DIR}/login-request.json" \
    --output "${PRIVATE_DIR}/login-response.json" \
    "http://127.0.0.1:${GATEWAY_PORT}/v1/auth/login" 2>"${LOG_DIR}/http-login.log"
  python3 - "$PRIVATE_DIR" <<'PY'
import json
from pathlib import Path
import sys
private = Path(sys.argv[1])
response = json.loads((private / "login-response.json").read_text())
token = response.get("token")
if not isinstance(token, str) or not token.strip():
    raise SystemExit("Gateway login did not return a token")
(private / "token").write_text(token)
PY
  "$DOOPS_BIN" -session "$SESSION" login --target e2e --token-stdin \
    <"${PRIVATE_DIR}/token" >"${LOG_DIR}/login.log" 2>&1
  grep -q 'Successfully cached gateway token' "${LOG_DIR}/login.log" || fail 'CLI login did not cache the validated token'
}

wait_agent_online() {
  local i
  for ((i=0; i<45; i++)); do
    if "$DOOPS_BIN" -session "$SESSION" targets --target e2e >"${LOG_DIR}/targets.log" 2>&1 &&
      grep -q "${CLUSTER}/${INSTANCE}" "${LOG_DIR}/targets.log"; then
      return 0
    fi
    sleep 1
  done
  fail 'Agent did not become visible through its exact Gateway grant'
}

cli_exec_assert() {
  log "Executing echo ${EXPECT_STRING} in session ${SESSION}"
  "$DOOPS_BIN" -session "$SESSION" exec --target e2e --cmd "echo ${EXPECT_STRING}" 2>&1 | tee "${LOG_DIR}/exec.log"
  grep -Fxq "$EXPECT_STRING" "${LOG_DIR}/exec.log" || fail 'Actual command output is missing'
}

assert_audit() {
  grep -Fxq "echo ${EXPECT_STRING}" "${LOG_DIR}/audit.log" || fail 'Command is missing from the persisted audit'
  grep -Eq '^# \[.*\] exit=0$' "${LOG_DIR}/audit.log" || fail 'Successful exit is missing from the persisted audit'
  log 'Persisted command audit verified'
}

run_process() {
  if [[ "$(id -u)" != 0 ]]; then
    command -v sudo >/dev/null 2>&1 || fail 'Non-root process mode requires sudo'
    sudo -n true || fail 'Process mode requires passwordless sudo for the Agent audit path'
    PRIVILEGED=(sudo -n)
  fi
  log 'Building Gateway, Agent and private seed helper'
  (cd "${REPO_ROOT}/backend/gateway" && CGO_ENABLED=0 go build -o "${WORKDIR}/bin/doops-gateway" . &&
    CGO_ENABLED=0 go build -o "${WORKDIR}/bin/doops-e2e-seed" "${E2E_DIR}/seed.go") >"${LOG_DIR}/build-gateway.log" 2>&1
  (cd "${REPO_ROOT}/backend/agent" && CGO_ENABLED=0 go build -o "${WORKDIR}/bin/doops-agent" ./cmd/agent) >"${LOG_DIR}/build-agent.log" 2>&1
  "${WORKDIR}/bin/doops-e2e-seed" "${PRIVATE_DIR}/gateway.db" <"${PRIVATE_DIR}/seed.json" >"${LOG_DIR}/seed.log" 2>&1
  "${PRIVILEGED[@]}" test -d /root/.doops || DOOPS_DIR_CREATED=true
  "${PRIVILEGED[@]}" test -d /root/.doops/audit || AUDIT_DIR_CREATED=true
  "${PRIVILEGED[@]}" mkdir -p -m 0700 /root/.doops /root/.doops/audit
  "${PRIVILEGED[@]}" sh -s -- "$AUDIT_FILE" <<'SH'
set -Ceu
umask 077
: > "$1"
SH
  AUDIT_PREPARED=true
  "${WORKDIR}/bin/doops-gateway" serve -port "$GATEWAY_PORT" -db "${PRIVATE_DIR}/gateway.db" >"${LOG_DIR}/gateway.log" 2>&1 &
  GATEWAY_PID=$!
  wait_gateway_health
  "${PRIVILEGED[@]}" sh -s -- "${WORKDIR}/agent.pid" env DOOPS_WORKSPACE_ROOT="${WORKDIR}/ws" \
    "${WORKDIR}/bin/doops-agent" -listen 127.0.0.1 -port "$AGENT_PORT" \
    -gateway-url "ws://127.0.0.1:${GATEWAY_PORT}" -cluster "$CLUSTER" -instance "$INSTANCE" \
    >"${LOG_DIR}/agent.log" 2>&1 <<'SH' &
set -eu
printf '%s\n' "$$" > "$1"
chmod 0644 "$1"
shift
exec "$@"
SH
  AGENT_LAUNCHER_PID=$!
  build_cli
  cli_add_and_login
  wait_agent_online
  cli_exec_assert
  # Successful execution audits live here; blocked-command audits remain in the workspace.
  "${PRIVILEGED[@]}" cat "$AUDIT_FILE" >"${LOG_DIR}/audit.log"
  assert_audit
}

run_compose() {
  command -v docker >/dev/null 2>&1 || fail 'Compose mode requires Docker'
  docker compose version >/dev/null 2>&1 || fail 'Compose mode requires Docker Compose'
  export E2E_GATEWAY_PORT="$GATEWAY_PORT" E2E_CLUSTER="$CLUSTER" E2E_INSTANCE="$INSTANCE"
  export E2E_IMAGE="${PROJECT}:local"
  STACK_STARTED=true
  log "Building isolated E2E test image ${E2E_IMAGE}"
  "${COMPOSE[@]}" build --quiet gateway >"${LOG_DIR}/compose-build.log" 2>&1
  "${COMPOSE[@]}" run --rm --no-deps -T --entrypoint /app/doops-e2e-seed gateway /data/gateway.db \
    <"${PRIVATE_DIR}/seed.json" >"${LOG_DIR}/seed.log" 2>&1
  "${COMPOSE[@]}" up -d gateway agent >"${LOG_DIR}/compose-start.log" 2>&1
  wait_gateway_health
  build_cli
  cli_add_and_login
  wait_agent_online
  cli_exec_assert
  "${COMPOSE[@]}" exec -T agent cat "$AUDIT_FILE" >"${LOG_DIR}/audit.log"
  assert_audit
}

log "Mode=${MODE} session=${SESSION} workdir=${WORKDIR}"
case "$MODE" in
  process) run_process ;;
  compose) run_compose ;;
esac
log 'E2E passed: Gateway login -> CLI exec -> persisted successful audit'
