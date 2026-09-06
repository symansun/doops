#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
One-click upgrade for a Helm-managed doops-agent.

The laptop is the release controller. The live agent only runs a foreground
Helm mutation with --wait=false so the current connection can exit before the
pod is replaced. kubectl set-image, docker restart, release-runners, --bg Helm,
and doops upgrade without --dry-run are forbidden.

Usage:
  bash scripts/upgrade-managed-agent.sh \
    --target gw-edu-coder \
    --session agent-upgrade-v1.3.0 \
    --release doops-agent-live \
    --namespace kz-ops \
    --version v1.3.0

  bash scripts/upgrade-managed-agent.sh \
    --target gw-edu-coder \
    --session agent-upgrade-<digest> \
    --release doops-agent-live \
    --namespace kz-ops \
    --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest>

Required:
  --target NAME       Configured DoOps target that already runs this Helm release.
  --session NAME      Isolated session used for push, helm, and reconnect checks.
  --release NAME      Existing Helm release name.
  --namespace NAME    Kubernetes namespace of that release.

Image (exactly one):
  --version vX.Y.Z    Resolve docker.cnb.cool/l8ai/ai/doops.sh:vX.Y.Z to a digest.
  --image REPO@sha256:<digest>
                      Immutable registry digest. Overrides --version.

Options:
  --chart-dir PATH    Local chart directory. Default: deploy/helm/doops-agent.
  --reconnect-timeout SEC
                      Seconds to wait for Gateway reconnect. Default: 180.
  --dry-run           Preflight only. Do not push chart or mutate Helm.
  -h, --help          Show this help.

The live workload command must already contain /app/sandbox-entrypoint.sh.
Non-Helm or custom-entrypoint agents (for example a ConfigMap /scripts/entrypoint.sh)
must be recovered with matching values before this script can adopt them.
EOF
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=doops-release-image.sh
source "${SCRIPT_DIR}/doops-release-image.sh"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TARGET=""
SESSION=""
RELEASE=""
NAMESPACE=""
VERSION=""
IMAGE=""
CHART_DIR="${REPO_ROOT}/deploy/helm/doops-agent"
RECONNECT_TIMEOUT="180"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      TARGET="${2:-}"
      shift 2
      ;;
    --session)
      SESSION="${2:-}"
      shift 2
      ;;
    --release)
      RELEASE="${2:-}"
      shift 2
      ;;
    --namespace)
      NAMESPACE="${2:-}"
      shift 2
      ;;
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --image)
      IMAGE="${2:-}"
      shift 2
      ;;
    --chart-dir)
      CHART_DIR="${2:-}"
      shift 2
      ;;
    --reconnect-timeout)
      RECONNECT_TIMEOUT="${2:-}"
      shift 2
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

require_value() {
  local name="$1"
  local value="$2"
  if [[ -z "${value}" ]]; then
    echo "Error: ${name} is required" >&2
    usage >&2
    exit 2
  fi
}

require_value "--target" "${TARGET}"
require_value "--session" "${SESSION}"
require_value "--release" "${RELEASE}"
require_value "--namespace" "${NAMESPACE}"

if [[ -n "${IMAGE}" && -n "${VERSION}" ]]; then
  echo "Error: pass only one of --image or --version" >&2
  exit 2
fi
if [[ -n "${IMAGE}" ]]; then
  doops_parse_image_digest "${IMAGE}"
elif [[ -n "${VERSION}" ]]; then
  doops_resolve_release_tag "${VERSION}"
else
  echo "Error: --image or --version is required" >&2
  usage >&2
  exit 2
fi

if [[ ! "${RECONNECT_TIMEOUT}" =~ ^[1-9][0-9]*$ ]]; then
  echo "Error: --reconnect-timeout must be a positive integer" >&2
  exit 2
fi
if [[ ! -f "${CHART_DIR}/Chart.yaml" || ! -f "${CHART_DIR}/templates/deployment.yaml" ]]; then
  echo "Error: --chart-dir must be a doops-agent Helm chart" >&2
  exit 1
fi
if ! grep -q '/app/sandbox-entrypoint.sh' "${CHART_DIR}/templates/deployment.yaml"; then
  echo "Error: candidate chart is missing sandbox-entrypoint.sh" >&2
  exit 1
fi
command -v doops >/dev/null || {
  echo "Error: doops CLI is required on PATH" >&2
  exit 1
}
command -v rsync >/dev/null || {
  echo "Error: rsync is required to stage the Helm chart" >&2
  exit 1
}

doops_cmd() {
  doops -session "${SESSION}" "$@"
}

echo "Target: ${TARGET}"
echo "Release: ${RELEASE} / ${NAMESPACE}"
echo "Image: ${DOOPS_IMAGE}"
echo "Session: ${SESSION}"

echo "==> checking target is online"
doops_cmd targets --target "${TARGET}" >/dev/null

echo "==> preflight: Helm release and sandbox-entrypoint"
doops_cmd exec --target "${TARGET}" --cmd "$(cat <<EOF
set -eu
if ! helm status $(printf '%q' "${RELEASE}") -n $(printf '%q' "${NAMESPACE}") >/dev/null; then
  echo "Error: Helm release ${RELEASE} was not found in ${NAMESPACE}" >&2
  echo "This script only upgrades an existing Helm-managed doops-agent." >&2
  exit 1
fi
python3 - $(printf '%q' "${RELEASE}") $(printf '%q' "${NAMESPACE}") <<'PY'
import json, subprocess, sys
release, namespace = sys.argv[1], sys.argv[2]
raw = subprocess.check_output(
    ["helm", "get", "values", release, "-n", namespace, "-o", "json"],
    text=True,
)
values = json.loads(raw)
workload = ((values.get("workload") or {}).get("name") or "").strip()
if not workload:
    raise SystemExit("Error: Helm values are missing workload.name")
deploy = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "deploy", workload, "-o", "json"],
    text=True,
))
container = deploy["spec"]["template"]["spec"]["containers"][0]
command = " ".join(container.get("command") or [])
args = " ".join(container.get("args") or [])
entry = command + " " + args
if "/app/sandbox-entrypoint.sh" not in entry:
    raise SystemExit(
        "Error: live command is not sandbox-entrypoint.sh; refusing to replace a custom entrypoint"
    )
print("workload=" + workload)
print("current_image=" + container.get("image", ""))
PY
EOF
)"

if [[ "${DRY_RUN}" == true ]]; then
  echo "Dry-run complete. No chart was pushed and Helm was not mutated."
  exit 0
fi

stage="$(mktemp -d)"
trap 'rm -rf "${stage}"' EXIT
mkdir -p "${stage}/deploy/helm"
rsync -a "${CHART_DIR}/" "${stage}/deploy/helm/doops-agent/"

echo "==> pushing deploy/helm/doops-agent"
doops_cmd push --target "${TARGET}" --src "${stage}"

echo "==> pulling image then Helm upgrade --install --wait=false"
# Foreground Helm is required. --wait=false lets this exec return before the agent pod dies.
# Do not add kubectl apply/set-image, --atomic, reused Helm values, or background Helm.
doops_cmd exec --target "${TARGET}" --cmd "$(cat <<EOF
set -eu
cd /root/ws/$(printf '%q' "${SESSION}")
if [[ ! -f deploy/helm/doops-agent/Chart.yaml ]]; then
  echo "Error: chart was not pushed to /root/ws/${SESSION}/deploy/helm/doops-agent" >&2
  exit 1
fi
if command -v nerdctl >/dev/null 2>&1; then
  nerdctl -n k8s.io pull $(printf '%q' "${DOOPS_IMAGE}") || nerdctl pull $(printf '%q' "${DOOPS_IMAGE}") || true
fi
if command -v docker >/dev/null 2>&1; then
  docker pull $(printf '%q' "${DOOPS_IMAGE}") || true
fi
python3 - $(printf '%q' "${RELEASE}") $(printf '%q' "${NAMESPACE}") $(printf '%q' "${DOOPS_IMAGE_REPOSITORY}") $(printf '%q' "${DOOPS_IMAGE_DIGEST}") <<'PY'
import json, subprocess, sys
release, namespace, repository, digest = sys.argv[1:5]
values = json.loads(subprocess.check_output(
    ["helm", "get", "values", release, "-n", namespace, "-o", "json"],
    text=True,
))
image = values.setdefault("image", {})
image.pop("tag", None)
image["repository"] = repository
image["digest"] = digest
with open("/tmp/doops-agent-upgrade-values.json", "w", encoding="utf-8") as fh:
    json.dump(values, fh)
PY
helm upgrade --install $(printf '%q' "${RELEASE}") deploy/helm/doops-agent \
  -n $(printf '%q' "${NAMESPACE}") \
  -f /tmp/doops-agent-upgrade-values.json \
  --timeout 5m \
  --wait=false
echo HELM_MUTATION_SUBMITTED
EOF
)" || true

echo "==> waiting for Gateway reconnect"
deadline=$((SECONDS + RECONNECT_TIMEOUT))
until doops -session "${SESSION}-wait" targets --target "${TARGET}" >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then
    echo "Error: target ${TARGET} did not reconnect within ${RECONNECT_TIMEOUT}s" >&2
    exit 1
  fi
  sleep 5
done

echo "==> verifying live image"
doops -session "${SESSION}-verify" exec --target "${TARGET}" --cmd "$(cat <<EOF
set -eu
python3 - $(printf '%q' "${RELEASE}") $(printf '%q' "${NAMESPACE}") $(printf '%q' "${DOOPS_IMAGE}") <<'PY'
import json, subprocess, sys
release, namespace, expected = sys.argv[1:4]
values = json.loads(subprocess.check_output(
    ["helm", "get", "values", release, "-n", namespace, "-o", "json"],
    text=True,
))
workload = values["workload"]["name"]
deploy = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "deploy", workload, "-o", "json"],
    text=True,
))
got = deploy["spec"]["template"]["spec"]["containers"][0]["image"]
if got != expected:
    raise SystemExit("Error: live image %s != %s" % (got, expected))
print("verified_image=" + got)
print("workload=" + workload)
PY
/app/doops-agent -h >/tmp/doops-agent-help.txt 2>&1 || true
if ! grep -q -- '-edge' /tmp/doops-agent-help.txt; then
  echo "Error: upgraded binary is missing -edge flags" >&2
  exit 1
fi
echo "edge_flag=documented"
hostname
sha256sum /app/doops-agent
EOF
)"

echo "Upgrade complete: ${TARGET} -> ${DOOPS_IMAGE}"
