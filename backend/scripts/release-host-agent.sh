#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' 'Release an existing standalone Docker Agent from its external SSH host controller.' \
    'Required: --target NAME --session NAME --host HOST --user USER --port PORT' \
    '          --container NAME --cluster NAME --instance NAME --image REPO@sha256:DIGEST' \
    '          --source-commit SHA --release TAG' \
    'Optional: --ssh-control-path PATH (existing authenticated socket), --dry-run' \
    'No password arguments, protocol fallback, temporary Agent identity or Kubernetes migration.'
}

TARGET="" SESSION="" HOST="" USER_NAME="" PORT="" CONTAINER="" CLUSTER="" INSTANCE=""
IMAGE="" SOURCE_COMMIT="" RELEASE="" CONTROL_PATH="" DRY_RUN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --target) TARGET="${2:-}"; shift 2 ;;
    --session) SESSION="${2:-}"; shift 2 ;;
    --host) HOST="${2:-}"; shift 2 ;;
    --user) USER_NAME="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --container) CONTAINER="${2:-}"; shift 2 ;;
    --cluster) CLUSTER="${2:-}"; shift 2 ;;
    --instance) INSTANCE="${2:-}"; shift 2 ;;
    --image) IMAGE="${2:-}"; shift 2 ;;
    --source-commit) SOURCE_COMMIT="${2:-}"; shift 2 ;;
    --release) RELEASE="${2:-}"; shift 2 ;;
    --ssh-control-path) CONTROL_PATH="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'Unknown release option: %s\n' "$1" >&2; exit 2 ;;
  esac
done
for value in "$TARGET" "$SESSION" "$USER_NAME" "$CONTAINER" "$CLUSTER" "$INSTANCE"; do
  [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$ ]] || { usage >&2; exit 2; }
done
[[ "$HOST" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*$ ]] || { echo 'Explicit SSH host required' >&2; exit 2; }
[[ "$PORT" =~ ^[0-9]{1,5}$ ]] && (( 10#$PORT > 0 && 10#$PORT <= 65535 )) || { echo 'Explicit SSH --port required (1..65535); do not guess port 22' >&2; exit 2; }
[[ "$IMAGE" =~ ^[A-Za-z0-9./:_-]+@sha256:[a-f0-9]{64}$ ]] || { echo 'Immutable image required' >&2; exit 2; }
[[ "$SOURCE_COMMIT" =~ ^[a-f0-9]{40}$ && "$RELEASE" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Exact image source/release required' >&2; exit 2; }
for command in doops ssh scp sha256sum; do
  command -v "$command" >/dev/null || { echo "Missing release tool: $command" >&2; exit 1; }
done
route="$(doops list | awk -v name="$TARGET" -v key="$CLUSTER/$INSTANCE" '$1 == name {for (i=2;i<=NF;i++) if ($i==key) print key}')"
[[ "$route" == "$CLUSTER/$INSTANCE" ]] || { echo 'Configured DoOps target identity mismatch' >&2; exit 1; }

SSH_OPTIONS=(-p "$PORT" -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=10)
SCP_OPTIONS=(-P "$PORT" -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=10)
if [[ -n "$CONTROL_PATH" ]]; then
  [[ -S "$CONTROL_PATH" ]] || { echo 'SSH control socket unavailable' >&2; exit 1; }
  SSH_OPTIONS+=(-S "$CONTROL_PATH")
  SCP_OPTIONS+=(-o "ControlPath=$CONTROL_PATH")
fi
SSH=(ssh "${SSH_OPTIONS[@]}" "$USER_NAME@$HOST")
remote="$("${SSH[@]}" 'mktemp -d /tmp/doops-host-release.XXXXXX')"
[[ "$remote" =~ ^/tmp/doops-host-release\.[A-Za-z0-9]+$ ]] || { echo 'Invalid remote staging directory' >&2; exit 1; }
source="$(cd "$(dirname "$0")" && pwd)/release-host-agent.py"
scp "${SCP_OPTIONS[@]}" "$source" "$USER_NAME@$HOST:$remote/release-host-agent.py" >/dev/null
hash="$(sha256sum "$source" | awk '{print $1}')"
"${SSH[@]}" "test \"\$(sha256sum '$remote/release-host-agent.py' | awk '{print \$1}')\" = '$hash'"

host_release() {
  "${SSH[@]}" "if [ \"\$(id -u)\" = 0 ]; then executor=python3; else executor='sudo -n python3'; fi
\$executor '$remote/release-host-agent.py' --controller-id '${remote##*/}' --container '$CONTAINER' --session '$SESSION' --cluster '$CLUSTER' --instance '$INSTANCE' --image '$IMAGE' --source-commit '$SOURCE_COMMIT' --release '$RELEASE' $*"
}
cleanup() { "${SSH[@]}" "rm -f '$remote/release-host-agent.py'; rmdir '$remote'" >/dev/null 2>&1 || true; }
trap cleanup EXIT
if [[ "$DRY_RUN" == true ]]; then
  host_release --dry-run
  exit 0
fi
verified=false
rollback_on_failure() {
  if [[ "$verified" != true ]]; then
    echo 'Release acceptance failed; restoring retained old container externally' >&2
    host_release --rollback || { echo 'Host rollback failed; external intervention required' >&2; return 1; }
  fi
}
trap 'status=$?; rollback_on_failure || status=1; cleanup; exit "$status"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
host_release

verify_rpc() (
  export DOOPS_CLI_CALL_TIMEOUT=15s
  local output stdout_marker stderr_marker bg_marker
  doops -session "$SESSION" capabilities --target "$TARGET" --require structuredSSH >/dev/null || return 1
  doops -session "$SESSION" capabilities --target "$TARGET" --require executionLifecycle >/dev/null || return 1
  output="$(doops -session "$SESSION" exec --target "$TARGET" --cmd '/app/doops-agent -version')" || return 1
  grep -Fx "release=$RELEASE commit=$SOURCE_COMMIT" <<< "$output" >/dev/null || return 1
  stdout_marker="host-release-$SESSION-stdout"
  stderr_marker="host-release-$SESSION-stderr"
  output="$(doops -session "$SESSION" exec --target "$TARGET" --cmd "printf '%s\\n' '$stdout_marker'; printf '%s\\n' '$stderr_marker' >&2")" || return 1
  grep -Fx "$stdout_marker" <<< "$output" >/dev/null || return 1
  grep -Fx "$stderr_marker" <<< "$output" >/dev/null || return 1
  bg_marker="host-release-$SESSION-background"
  output="$(doops -session "$SESSION" exec --bg --target "$TARGET" --cmd "printf '%s\\n' '$bg_marker'")" || return 1
  grep -Fx "$bg_marker" <<< "$output" >/dev/null || return 1
  grep -Fx 'ExitCode: 0' <<< "$output" >/dev/null || return 1
  # Exercise structured argv without passing host credentials into the Agent.
  if output="$(doops -session "$SESSION" ssh --target "$TARGET" --host root@127.0.0.1 --option=Port=1 --option=BatchMode=yes --option=StrictHostKeyChecking=yes --option=ConnectTimeout=3 -- printf "$stdout_marker" 2>&1)"; then
    return 1
  fi
  grep -F 'connect to host 127.0.0.1 port 1: Connection refused' <<< "$output" >/dev/null || return 1
  grep -F 'Error: remote error: execution_outcome_unknown' <<< "$output" >/dev/null || return 1
  output="$(doops -session "$SESSION" exec --target "$TARGET" --cmd "printf '%s\\n' '$stdout_marker'")" || return 1
  grep -Fx "$stdout_marker" <<< "$output" >/dev/null
)

deadline=$((SECONDS + 120))
while (( SECONDS < deadline )); do
  target_state="$(doops -session "$SESSION" targets --target "$TARGET" 2>/dev/null || true)"
  if printf '%s\n' "$target_state" | awk -v cluster="$CLUSTER" -v instance="$INSTANCE" -v commit="$SOURCE_COMMIT" '$1==cluster && $2==instance {for(i=3;i<=NF;i++) if($i==commit) found=1} END {exit !found}'; then
    if verify_rpc; then
      verified=true
    fi
    break
  fi
  sleep 2
done
[[ "$verified" == true ]] || { echo 'Gateway handoff or functional verification failed' >&2; exit 1; }
echo "host_release_complete target=$TARGET identity=$CLUSTER/$INSTANCE release=$RELEASE commit=$SOURCE_COMMIT"
