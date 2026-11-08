#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Deploy the Next.js DoOps console to the audited Gateway host boundary.

Usage:
  bash backend/scripts/deploy-web-console.sh \
    --host 192.0.2.10 \
    --image docker.cnb.cool/l8ai/ai/doops.sh/web@sha256:<digest>

Options:
  --host HOST         Gateway SSH host or IP. Required.
  --user USER         SSH user. Default: ubuntu.
  --port PORT         SSH port. Default: 22.
  --image IMAGE       Immutable Web image digest. Required.
  --gateway URL       Allowed Gateway origin. Required for a real deploy.
  --dry-run           Verify host, Docker, current container, and image only.
EOF
}

HOST=""
SSH_USER="ubuntu"
SSH_PORT="22"
IMAGE=""
GATEWAY=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="${2:-}"; shift 2 ;;
    --user) SSH_USER="${2:-}"; shift 2 ;;
    --port) SSH_PORT="${2:-}"; shift 2 ;;
    --image) IMAGE="${2:-}"; shift 2 ;;
    --gateway) GATEWAY="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "${HOST}" || "${HOST}" == *[/:,]* ]]; then
  echo "--host must be a real SSH host or IP" >&2
  exit 2
fi
if [[ ! "${IMAGE}" =~ @sha256:[a-f0-9]{64}$ ]]; then
  echo "--image must use an immutable sha256 registry digest" >&2
  exit 2
fi
if [[ -z "${GATEWAY}" ]]; then
  echo "--gateway is required for a real deploy" >&2
  exit 2
fi
if [[ ! "${GATEWAY}" =~ ^https://[^/?#]+$ ]]; then
  echo "--gateway must be an HTTPS origin without a path" >&2
  exit 2
fi

deploy_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
candidate="doops-web-candidate-${deploy_id}"
backup="doops-web-rollback-${deploy_id}"
SSH=(ssh -o BatchMode=yes -p "${SSH_PORT}" "${SSH_USER}@${HOST}")

echo "Web console deploy plan"
echo "  host: ${SSH_USER}@${HOST}:${SSH_PORT}"
echo "  image: ${IMAGE}"
echo "  allowed Gateway: ${GATEWAY}"

"${SSH[@]}" "set -e
hostname
sudo -n docker inspect doops-web >/dev/null
sudo -n docker pull --platform linux/amd64 '${IMAGE}' >/dev/null
sudo -n docker image inspect '${IMAGE}' >/dev/null
"

if [[ "${DRY_RUN}" == "true" ]]; then
  echo "Dry run complete."
  exit 0
fi

"${SSH[@]}" bash -s -- "${IMAGE}" "${GATEWAY}" "${candidate}" "${backup}" <<'REMOTE'
set -euo pipefail
image="$1"
gateway="$2"
candidate="$3"
backup="$4"
committed=false

cleanup() {
  sudo -n docker rm -f "$candidate" >/dev/null 2>&1 || true
  if [[ "$committed" != "true" ]] && sudo -n docker inspect "$backup" >/dev/null 2>&1; then
    sudo -n docker rm -f doops-web >/dev/null 2>&1 || true
    sudo -n docker rename "$backup" doops-web
    sudo -n docker start doops-web >/dev/null
  fi
}
trap cleanup EXIT

sudo -n docker run -d --name "$candidate" \
  -e "DOOPS_CONSOLE_ALLOWED_GATEWAYS=$gateway" \
  -p 127.0.0.1:3001:3000 \
  "$image" >/dev/null
for _ in $(seq 1 30); do
  code="$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3001/ || true)"
  [[ "$code" == "200" ]] && break
  sleep 1
done
test "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3001/)" = "200"
sudo -n docker rm -f "$candidate" >/dev/null

sudo -n docker stop doops-web >/dev/null
sudo -n docker rename doops-web "$backup"
sudo -n docker run -d --name doops-web --restart unless-stopped \
  -e "DOOPS_CONSOLE_ALLOWED_GATEWAYS=$gateway" \
  -p 127.0.0.1:3000:3000 \
  "$image" >/dev/null
for _ in $(seq 1 30); do
  code="$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3000/ || true)"
  [[ "$code" == "200" ]] && break
  sleep 1
done
test "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:3000/)" = "200"
test "$(sudo -n docker inspect --format '{{.Config.Image}}' doops-web)" = "$image"
test "$(sudo -n docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' doops-web |
  awk -F= '$1 == "DOOPS_CONSOLE_ALLOWED_GATEWAYS" {print $2}')" = "$gateway"

committed=true
trap - EXIT
echo "Web console deployed; rollback container=$backup"
REMOTE
