#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Deploy doops-gateway to a gateway host over SSH.

This script is intentionally SSH-only. Do not deploy doops-gateway through a
doops target; targets are doops-agent execution contexts and can point at the
wrong root filesystem.

Usage:
  bash scripts/deploy-gateway.sh --host <gateway-ssh-host> --user ubuntu

Options:
  --host HOST             Gateway host or IP. Required.
  --user USER             SSH user. Default: ubuntu.
  --port PORT             SSH port. Default: 22.
  --binary PATH           Local doops-gateway binary. Default: bin/doops-gateway.
  --remote-bin PATH       Remote binary path. Default: /usr/local/bin/doops-gateway.
  --db PATH               Gateway SQLite DB. Default: /var/lib/doops-gateway/gateway.db.
  --gateway-port PORT     Gateway listen port. Default: 42222.
  --verify-url URL        Public Gateway URL used for health and target checks.
                          Default: https://doops.l8ai.cn.
  --verify-target NAME    Configured route to verify without exporting its token.
                          Repeat for every affected online target on this Gateway.
  --verify-session NAME   Required session name for --verify-target.
                          Real deploys require both verification options.
  --source-commit SHA     Exact 40-character source commit embedded in the
                          candidate Gateway binary. Required for real deploys.
  --allow-open-agent-registration
                          Remove DOOPS_REQUIRE_AGENT_ENROLLMENT and write
                          DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=1 to the remote
                          environment file (open registration is the default for
                          targets without a confirmed enrollment).
  --require-agent-enrollment
                          Write DOOPS_REQUIRE_AGENT_ENROLLMENT=1 and remove
                          open-registration switches; the Gateway rejects Agents
                          that do not present their target enrollment token.
                          Default when neither flag is given: leave the remote
                          environment file unchanged.
  --allow-unenrolled-credential-delivery
                          Remove DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY and
                          write DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=1
                          (enabled by default in open mode; cannot be combined
                          with --require-agent-enrollment).
  --require-enrolled-credential-delivery
                          Write DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1 and
                          remove DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY so
                          unenrolled Agents cannot receive credentials.
  --readiness-timeout SECONDS
                          Seconds to wait for the restarted Gateway to answer
                          /health locally before the deploy is rolled back.
                          Raise it for a first start that must migrate a large
                          database. Default: 30.
  --no-build              Do not run scripts/build-gateway.sh before deploy.
  --dry-run               Print the plan and verify SSH, but do not modify remote host.
  -h, --help              Show this help.

Required local capabilities:
  ssh, scp, curl, sha256sum, strings.

Required remote capabilities:
  sudo, systemctl, sha256sum, pgrep, curl, python3.
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST=""
SSH_USER="ubuntu"
SSH_PORT="22"
LOCAL_BIN="${REPO_ROOT}/bin/doops-gateway"
REMOTE_BIN="/usr/local/bin/doops-gateway"
DB_PATH="/var/lib/doops-gateway/gateway.db"
GATEWAY_PORT="42222"
VERIFY_URL="https://doops.l8ai.cn"
VERIFY_TARGETS=()
VERIFY_SESSION=""
SOURCE_COMMIT=""
AGENT_REGISTRATION="preserve" # preserve | open | required
UNENROLLED_CREDENTIALS="preserve" # preserve | open | required
READINESS_TIMEOUT="30"
BUILD=true
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)
      HOST="${2:-}"
      shift 2
      ;;
    --user)
      SSH_USER="${2:-}"
      shift 2
      ;;
    --port)
      SSH_PORT="${2:-}"
      shift 2
      ;;
    --binary)
      LOCAL_BIN="${2:-}"
      shift 2
      ;;
    --remote-bin)
      REMOTE_BIN="${2:-}"
      shift 2
      ;;
    --db)
      DB_PATH="${2:-}"
      shift 2
      ;;
    --gateway-port)
      GATEWAY_PORT="${2:-}"
      shift 2
      ;;
    --verify-url)
      VERIFY_URL="${2:-}"
      shift 2
      ;;
    --verify-target)
      if [[ -z "${2:-}" || "$2" == --* ]]; then
        echo "Error: --verify-target requires a configured route name" >&2
        exit 2
      fi
      VERIFY_TARGETS+=("$2")
      shift 2
      ;;
    --verify-session)
      VERIFY_SESSION="${2:-}"
      shift 2
      ;;
    --source-commit)
      SOURCE_COMMIT="${2:-}"
      shift 2
      ;;
    --allow-open-agent-registration)
      AGENT_REGISTRATION="open"
      shift
      ;;
    --require-agent-enrollment)
      AGENT_REGISTRATION="required"
      shift
      ;;
    --allow-unenrolled-credential-delivery)
      UNENROLLED_CREDENTIALS="open"
      shift
      ;;
    --require-enrolled-credential-delivery)
      UNENROLLED_CREDENTIALS="required"
      shift
      ;;
    --readiness-timeout)
      READINESS_TIMEOUT="${2:-}"
      shift 2
      ;;
    --no-build)
      BUILD=false
      shift
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${HOST}" ]]; then
  echo "Error: --host is required" >&2
  usage >&2
  exit 2
fi

if [[ "${HOST}" == *"/"* || "${HOST}" == *":"* || "${HOST}" == *","* ]]; then
  cat >&2 <<EOF
Error: --host must be a real SSH host/IP, not a doops target or cluster/instance.
Gateway deployment must not go through a doops target.
EOF
  exit 2
fi

if [[ ! "${READINESS_TIMEOUT}" =~ ^[1-9][0-9]*$ ]]; then
  echo "Error: --readiness-timeout must be a positive integer" >&2
  exit 2
fi

if [[ "${AGENT_REGISTRATION}" == "required" && "${UNENROLLED_CREDENTIALS}" == "open" ]]; then
  echo "Error: --allow-unenrolled-credential-delivery cannot be combined with --require-agent-enrollment" >&2
  exit 2
fi

REQUIRED_COMMANDS=(ssh scp curl sha256sum strings)
if (( ${#VERIFY_TARGETS[@]} )); then
  REQUIRED_COMMANDS+=(doops)
fi
for command in "${REQUIRED_COMMANDS[@]}"; do
  command -v "${command}" >/dev/null || {
    echo "Error: required local command is missing: ${command}" >&2
    exit 1
  }
done

if [[ "${DRY_RUN}" != "true" ]]; then
  if [[ ${#VERIFY_TARGETS[@]} -eq 0 || -z "${VERIFY_SESSION}" ]]; then
    echo "Error: a real Gateway deployment requires --verify-target and --verify-session" >&2
    exit 2
  fi
  if [[ ! "${SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Error: a real Gateway deployment requires --source-commit <40-character-commit-sha>" >&2
    exit 2
  fi
fi

if [[ "${BUILD}" == "true" ]]; then
  bash "${REPO_ROOT}/scripts/build-gateway.sh"
fi

if [[ ! -x "${LOCAL_BIN}" ]]; then
  echo "Error: local gateway binary is missing or not executable: ${LOCAL_BIN}" >&2
  exit 1
fi
if [[ -n "${SOURCE_COMMIT}" ]]; then
  if ! LC_ALL=C strings -a "${LOCAL_BIN}" | grep -Fxx "${SOURCE_COMMIT}" >/dev/null; then
    echo "Error: Gateway binary does not contain source commit ${SOURCE_COMMIT}" >&2
    echo "Extract the binary from the immutable CI image for this commit; do not reuse a local build." >&2
    exit 1
  fi
fi

LOCAL_SHA="$(sha256sum "${LOCAL_BIN}" | awk '{print $1}')"
DEPLOY_ID="${LOCAL_SHA:0:12}-$$-$(date -u +%Y%m%dT%H%M%SZ)"
REMOTE_TMP="/tmp/doops-gateway-deploy-${DEPLOY_ID}"
REMOTE_LOCK="/run/lock/doops-gateway-deploy.lock"
SSH_CONTROL_PATH="/tmp/doops-gateway-ssh-${DEPLOY_ID}"
SSH_OPTIONS=(-o ControlMaster=auto -o ControlPersist=30 -o "ControlPath=${SSH_CONTROL_PATH}")
SSH=(ssh "${SSH_OPTIONS[@]}" -p "${SSH_PORT}" "${SSH_USER}@${HOST}")
SCP=(scp "${SSH_OPTIONS[@]}" -P "${SSH_PORT}")
STATE_DIR="$(mktemp -d)"
trap 'rm -rf "${STATE_DIR}"' EXIT
BASELINE_TARGETS="${STATE_DIR}/baseline-targets"
CURRENT_TARGETS="${STATE_DIR}/current-targets"

fetch_target_keys() {
  local destination="$1" route key
  : >"${destination}" || return 1
  for route in "${VERIFY_TARGETS[@]}"; do
    # The CLI filters by route and prints a human-readable footer after its row.
    # Require one identifiable row, never count the footer as another Agent.
    if ! key=$(doops -session "${VERIFY_SESSION}" targets --target "$route" |
      awk '$1 == "CLUSTER" && $2 == "INSTANCE" && $3 == "BUSY" {
             header = 1; has_enrollment = ($7 == "ENROLLED"); next
           }
           header && /^-+$/ { rows = 1; next }
           rows && $1 == "ACTIVE/QUEUED" { rows = 0; next }
           # Newer CLIs annotate an unenrolled row. Only associate a warning
           # with the target we actually read; never parse it as a target.
           rows && $1 == "NOT" && $2 == "ENROLLED:" {
             if (!has_enrollment || n != 1 || $3 != key || warned++) invalid = 1
             else print "NOT ENROLLED: " key > "/dev/stderr"
             next
           }
           rows && NF {
             if ($3 != "true" && $3 != "false") invalid = 1
             n++; key = $1 "/" $2
           }
           END { if (invalid || n != 1) exit 1; print key }'); then
      echo "Cannot verify one online target for route: $route" >&2
      return 1
    fi
    printf '%s\n' "$key" >>"${destination}" || return 1
  done
}

baseline_targets_present() {
  # Same route order on both reads: even swapping the identities must fail.
  cmp -s "${BASELINE_TARGETS}" "${CURRENT_TARGETS}"
}

wait_for_gateway_state() {
  local deadline_seconds="$1"
  local attempt
  for attempt in $(seq 1 "${deadline_seconds}"); do
    if curl --noproxy '*' -fsS "${VERIFY_URL%/}/health" >/dev/null &&
      fetch_target_keys "${CURRENT_TARGETS}" &&
      baseline_targets_present; then
      return 0
    fi
    sleep 1
  done
  echo "Missing baseline targets after ${deadline_seconds}s:" >&2
  diff -u "${BASELINE_TARGETS}" "${CURRENT_TARGETS}" >&2 || true
  return 1
}

wait_for_local_gateway() {
  local deadline_seconds="$1"
  local attempt
  local health_code
  local targets_code
  for attempt in $(seq 1 "${deadline_seconds}"); do
    health_code="$(curl -sS -o /tmp/doops-gateway-health.out -w '%{http_code}' \
      http://127.0.0.1:${GATEWAY_PORT}/health || true)"
    targets_code="$(curl -sS -o /tmp/doops-gateway-targets.out -w '%{http_code}' \
      http://127.0.0.1:${GATEWAY_PORT}/v1/targets || true)"
    if [[ "${health_code}" = "200" && "${targets_code}" = "401" ]]; then
      echo "health_http=200"
      echo "unauthenticated_targets_http=401"
      return 0
    fi
    sleep 1
  done
  echo "Gateway host-local readiness failed after ${deadline_seconds}s" >&2
  echo "health_http=${health_code:-unknown}" >&2
  echo "unauthenticated_targets_http=${targets_code:-unknown}" >&2
  return 1
}

echo "Gateway deploy plan"
echo "  SSH host:      ${SSH_USER}@${HOST}:${SSH_PORT}"
echo "  Local binary:  ${LOCAL_BIN}"
echo "  Local sha256:  ${LOCAL_SHA}"
echo "  Remote binary: ${REMOTE_BIN}"
echo "  Gateway DB:    ${DB_PATH}"
echo "  Gateway port:  ${GATEWAY_PORT}"
echo "  Verify URL:    ${VERIFY_URL}"
if [[ -n "${SOURCE_COMMIT}" ]]; then
  echo "  Source commit: ${SOURCE_COMMIT}"
fi
if (( ${#VERIFY_TARGETS[@]} )); then
  printf '  Verify route: %s\n' "${VERIFY_TARGETS[@]}"
fi

"${SSH[@]}" "set -e
hostname
id
store_backend=''
if sudo -n test -r /etc/doops-gateway.env; then
  store_backend=\$(sudo -n awk -F= '/^DOOPS_GATEWAY_STORE=/{print \$2; exit}' /etc/doops-gateway.env | tr -d '\r')
fi
if [[ \"\$store_backend\" = 'mysql' ]]; then
  data_dir=\$(sudo -n awk -F= '/^DOOPS_GATEWAY_DATA_DIR=/{print \$2; exit}' /etc/doops-gateway.env | tr -d '\r')
  data_dir=\"\${data_dir:-/var/lib/doops-gateway}\"
  sudo -n test -d \"\$data_dir\"
  echo \"gateway_store=mysql data_dir=\$data_dir\"
else
  test -r '${DB_PATH}'
  test -s '${DB_PATH}'
  ls -lh '${DB_PATH}'
fi
command -v sudo >/dev/null
command -v systemctl >/dev/null
command -v sha256sum >/dev/null
command -v python3 >/dev/null
sudo -n systemctl is-active --quiet doops-gateway
sudo -n systemctl cat doops-gateway.service >/dev/null
"

if [[ "${DRY_RUN}" == "true" ]]; then
  echo "Dry run complete. Remote host prerequisites verified."
  exit 0
fi

fetch_target_keys "${BASELINE_TARGETS}"
if [[ ! -s "${BASELINE_TARGETS}" ]]; then
  echo "Error: authenticated Gateway baseline contains no online targets" >&2
  exit 1
fi
if [[ $(LC_ALL=C sort -u "${BASELINE_TARGETS}" | wc -l) -ne ${#VERIFY_TARGETS[@]} ]]; then
  echo "Error: verification routes must identify distinct online targets" >&2
  exit 1
fi
echo "  Baseline targets: ${#VERIFY_TARGETS[@]}"
REMOTE_CURRENT_SHA="$("${SSH[@]}" "sudo -n sha256sum '${REMOTE_BIN}'" | awk '{print $1}')"
test -n "${REMOTE_CURRENT_SHA}"
REMOTE_BACKUP="${REMOTE_BIN}.rollback-${REMOTE_CURRENT_SHA:0:12}-$(date -u +%Y%m%dT%H%M%SZ)"
REMOTE_LOCK_ACQUIRED=false

release_remote_lock() {
  if [[ "${REMOTE_LOCK_ACQUIRED}" != "true" ]]; then
    return
  fi
  "${SSH[@]}" "set -e
owner=\$(sudo -n cat '${REMOTE_LOCK}/owner' 2>/dev/null || true)
if [[ \"\$owner\" = '${DEPLOY_ID}' ]]; then
  sudo -n rm -rf '${REMOTE_LOCK}'
fi
"
}

"${SSH[@]}" "set -e
sudo -n mkdir '${REMOTE_LOCK}'
if ! printf '%s\n' '${DEPLOY_ID}' | sudo -n tee '${REMOTE_LOCK}/owner' >/dev/null; then
  sudo -n rmdir '${REMOTE_LOCK}' || true
  exit 1
fi
"
REMOTE_LOCK_ACQUIRED=true
trap 'status=$?; release_remote_lock || true; rm -rf "${STATE_DIR}"; exit $status' EXIT

echo "[1/6] Uploading gateway binary over SSH/SCP"
"${SSH[@]}" "rm -rf '${REMOTE_TMP}' && mkdir -p '${REMOTE_TMP}'"
"${SCP[@]}" "${LOCAL_BIN}" "${SSH_USER}@${HOST}:${REMOTE_TMP}/doops-gateway"

echo "[2/6] Verifying uploaded binary"
"${SSH[@]}" "set -e; chmod +x '${REMOTE_TMP}/doops-gateway'; got=\$(sha256sum '${REMOTE_TMP}/doops-gateway' | awk '{print \$1}'); echo uploaded_sha256=\$got; test \"\$got\" = '${LOCAL_SHA}'; '${REMOTE_TMP}/doops-gateway' -h >/dev/null"

ROLLBACK_ARMED=false
rollback_gateway() {
  if [[ "${ROLLBACK_ARMED}" != "true" ]]; then
    return
  fi
  echo "Gateway verification failed; restoring ${REMOTE_BACKUP}" >&2
  "${SSH[@]}" "set -e
sudo -n install -m 0755 '${REMOTE_BACKUP}' '${REMOTE_BIN}'
sudo -n systemctl restart doops-gateway
sudo -n systemctl is-active --quiet doops-gateway
"
  wait_for_gateway_state 60 || true
}
trap 'status=$?; if [[ $status -ne 0 ]]; then rollback_gateway; fi; release_remote_lock || true; rm -rf "${STATE_DIR}"; exit $status' EXIT

echo "[3/6] Backing up and replacing remote binary"
"${SSH[@]}" "set -euo pipefail
sudo -n cp '${REMOTE_BIN}' '${REMOTE_BACKUP}'
echo backup='${REMOTE_BACKUP}'
"
ROLLBACK_ARMED=true
"${SSH[@]}" "set -euo pipefail
sudo -n install -m 0755 '${REMOTE_TMP}/doops-gateway' '${REMOTE_BIN}'
sudo -n sha256sum '${REMOTE_BIN}'

if ! {
  sudo -n systemctl show doops-gateway --property=Environment --value |
    grep -q 'DOOPS_GATEWAY_SECRET_KEY=' ||
  sudo -n systemctl show doops-gateway --property=EnvironmentFiles --value |
    grep -q '/etc/doops-gateway.env';
}; then
  legacy_secret='$(dirname "${DB_PATH}")/gateway.secret'
  if sudo -n test -s \"\$legacy_secret\"; then
    secret=\$(sudo -n cat \"\$legacy_secret\")
  else
    encrypted_rows=\$(sudo -n python3 -c '
import sqlite3
import sys

connection = sqlite3.connect(f\"file:{sys.argv[1]}?mode=ro\", uri=True)

def encrypted_rows(table, column):
    exists = connection.execute(
        \"SELECT 1 FROM sqlite_master WHERE type = ? AND name = ?\",
        (\"table\", table),
    ).fetchone()
    if not exists:
        return 0
    columns = {row[1] for row in connection.execute(f\"PRAGMA table_info({table})\")}
    if column not in columns:
        return 0
    return connection.execute(
        f\"SELECT count(*) FROM {table} WHERE {column} <> ?\",
        (\"\",),
    ).fetchone()[0]

print(
    encrypted_rows(\"git_repos\", \"password_ciphertext\")
    + encrypted_rows(\"credential_versions\", \"payload_ciphertext\")
)
' '${DB_PATH}')
    if [[ \"\$encrypted_rows\" != '0' ]]; then
      echo 'gateway encrypted data exists but no legacy secret key is available' >&2
      exit 1
    fi
    secret=\$(python3 -c 'import secrets; print(secrets.token_hex(32))')
  fi
  test -n \"\$secret\"
  printf 'DOOPS_GATEWAY_SECRET_KEY=%s\n' \"\$secret\" |
    sudo -n tee /etc/doops-gateway.env >/dev/null
  sudo -n chmod 0600 /etc/doops-gateway.env
  sudo -n mkdir -p /etc/systemd/system/doops-gateway.service.d
  printf '[Service]\nEnvironmentFile=/etc/doops-gateway.env\n' |
    sudo -n tee /etc/systemd/system/doops-gateway.service.d/credential-key.conf >/dev/null
  sudo -n systemctl daemon-reload
fi
"

echo "[3.5/6] Configuring Agent registration policy (registration=${AGENT_REGISTRATION}, credentials=${UNENROLLED_CREDENTIALS})"
if [[ "${AGENT_REGISTRATION}" == "required" && "${UNENROLLED_CREDENTIALS}" == "open" ]]; then
  echo '--allow-unenrolled-credential-delivery cannot be combined with --require-agent-enrollment' >&2
  exit 2
fi
if [[ "${AGENT_REGISTRATION}" != "preserve" || "${UNENROLLED_CREDENTIALS}" != "preserve" ]]; then
  "${SSH[@]}" "set -euo pipefail
env_file=/etc/doops-gateway.env
if ! sudo -n test -f \"\$env_file\"; then
  sudo -n install -m 0600 /dev/null \"\$env_file\"
fi
if [[ '${AGENT_REGISTRATION}' == 'open' ]]; then
  sudo -n sed -i '/^DOOPS_REQUIRE_AGENT_ENROLLMENT=/d' \"\$env_file\"
  if sudo -n grep -q '^DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=' \"\$env_file\"; then
    sudo -n sed -i 's/^DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=.*/DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=1/' \"\$env_file\"
  else
    printf 'DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=1\n' | sudo -n tee -a \"\$env_file\" >/dev/null
  fi
  sudo -n chmod 0600 \"\$env_file\"
  sudo -n grep -q '^DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=1$' \"\$env_file\"
  ! sudo -n grep -q '^DOOPS_REQUIRE_AGENT_ENROLLMENT=' \"\$env_file\"
  echo 'agent_registration=open'
elif [[ '${AGENT_REGISTRATION}' == 'required' ]]; then
  sudo -n sed -i '/^DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=/d' \"\$env_file\"
  sudo -n sed -i '/^DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=/d' \"\$env_file\"
  if sudo -n grep -q '^DOOPS_REQUIRE_AGENT_ENROLLMENT=' \"\$env_file\"; then
    sudo -n sed -i 's/^DOOPS_REQUIRE_AGENT_ENROLLMENT=.*/DOOPS_REQUIRE_AGENT_ENROLLMENT=1/' \"\$env_file\"
  else
    printf 'DOOPS_REQUIRE_AGENT_ENROLLMENT=1\n' | sudo -n tee -a \"\$env_file\" >/dev/null
  fi
  sudo -n chmod 0600 \"\$env_file\"
  sudo -n grep -q '^DOOPS_REQUIRE_AGENT_ENROLLMENT=1$' \"\$env_file\"
  echo 'agent_registration=required'
fi
if [[ '${UNENROLLED_CREDENTIALS}' == 'open' ]]; then
  sudo -n sed -i '/^DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=/d' \"\$env_file\"
  if sudo -n grep -q '^DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=' \"\$env_file\"; then
    sudo -n sed -i 's/^DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=.*/DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=1/' \"\$env_file\"
  else
    printf 'DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=1\n' | sudo -n tee -a \"\$env_file\" >/dev/null
  fi
  sudo -n chmod 0600 \"\$env_file\"
  sudo -n grep -q '^DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=1$' \"\$env_file\"
  echo 'unenrolled_credential_delivery=open'
elif [[ '${UNENROLLED_CREDENTIALS}' == 'required' ]]; then
  sudo -n sed -i '/^DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=/d' \"\$env_file\"
  if sudo -n grep -q '^DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=' \"\$env_file\"; then
    sudo -n sed -i 's/^DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=.*/DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1/' \"\$env_file\"
  else
    printf 'DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1\n' | sudo -n tee -a \"\$env_file\" >/dev/null
  fi
  sudo -n chmod 0600 \"\$env_file\"
  sudo -n grep -q '^DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1$' \"\$env_file\"
  echo 'unenrolled_credential_delivery=required'
fi
"
fi

echo "[4/6] Restarting the systemd-owned gateway"
"${SSH[@]}" "set -euo pipefail
sudo -n mkdir -p /etc/systemd/system/doops-gateway.service.d
printf '[Service]\nKillSignal=SIGTERM\nTimeoutStopSec=150\n' |
  sudo -n tee /etc/systemd/system/doops-gateway.service.d/graceful-shutdown.conf >/dev/null
sudo -n systemctl daemon-reload
sudo -n systemctl restart doops-gateway
sudo -n systemctl is-active --quiet doops-gateway
sleep 2
pid=\$(pgrep -f '^${REMOTE_BIN} serve' | head -1)
test -n \"\$pid\"
echo pid=\$pid
sudo sha256sum /proc/\$pid/exe '${REMOTE_BIN}'
test \"\$(sudo sha256sum /proc/\$pid/exe | awk '{print \$1}')\" = '${LOCAL_SHA}'
"

echo "[5/6] Verifying host-local HTTP endpoint"
"${SSH[@]}" "set -e
GATEWAY_PORT='${GATEWAY_PORT}'
$(declare -f wait_for_local_gateway)
wait_for_local_gateway "${READINESS_TIMEOUT}"
"

echo "[6/6] Waiting for every baseline Agent to reconnect"
wait_for_gateway_state "${READINESS_TIMEOUT}"

"${SSH[@]}" "rm -rf '${REMOTE_TMP}'
ls -1t '${REMOTE_BIN}'.rollback-* 2>/dev/null | tail -n +6 | while IFS= read -r old_rb; do
  sudo -n rm -f \"\$old_rb\"
done || true"
ROLLBACK_ARMED=false
release_remote_lock
REMOTE_LOCK_ACQUIRED=false
trap 'rm -rf "${STATE_DIR}"' EXIT
echo "Gateway deploy complete: ${HOST}:${GATEWAY_PORT}"
