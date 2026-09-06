#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
One-click first install of a Helm-managed doops-agent.

This script talks to Kubernetes from the operator machine. It does not use a
doops target, because a new cluster has no agent yet. Provide a complete values
file that already contains gateway, workload, doagentSettings, nodeSelector,
volumeMounts, and volumes. The script only overlays the immutable image digest.

Usage:
  bash scripts/install-managed-agent.sh \
    --release doops-agent \
    --namespace doops-system \
    --values /path/to/complete-values.yaml \
    --version v1.3.0

  bash scripts/install-managed-agent.sh \
    --release doops-agent \
    --namespace doops-system \
    --values /path/to/complete-values.yaml \
    --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest>

Required:
  --release NAME
  --namespace NAME
  --values FILE         Complete Helm values. Must not reuse previous Helm values.

Image (exactly one):
  --version vX.Y.Z
  --image REPO@sha256:<digest>

Options:
  --chart-dir PATH      Local chart directory. Default: deploy/helm/doops-agent.
  --kubeconfig PATH     Kubeconfig used by helm/kubectl. Default: $KUBECONFIG.
  --timeout DURATION    Helm --wait timeout. Default: 5m.
  --dry-run             Render/preflight only.
  -h, --help            Show this help.

The doagent Settings Secret named in values.doagentSettings.secretName must
already exist in the namespace. This script does not create Secrets.
EOF
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=doops-release-image.sh
source "${SCRIPT_DIR}/doops-release-image.sh"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RELEASE=""
NAMESPACE=""
VALUES=""
VERSION=""
IMAGE=""
CHART_DIR="${REPO_ROOT}/deploy/helm/doops-agent"
KUBECONFIG_PATH="${KUBECONFIG:-}"
TIMEOUT="5m"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --release)
      RELEASE="${2:-}"
      shift 2
      ;;
    --namespace)
      NAMESPACE="${2:-}"
      shift 2
      ;;
    --values)
      VALUES="${2:-}"
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
    --kubeconfig)
      KUBECONFIG_PATH="${2:-}"
      shift 2
      ;;
    --timeout)
      TIMEOUT="${2:-}"
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

require_value "--release" "${RELEASE}"
require_value "--namespace" "${NAMESPACE}"
require_value "--values" "${VALUES}"

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

if [[ ! -f "${VALUES}" ]]; then
  echo "Error: --values file does not exist: ${VALUES}" >&2
  exit 1
fi
if [[ ! -f "${CHART_DIR}/Chart.yaml" ]]; then
  echo "Error: --chart-dir must be a doops-agent Helm chart" >&2
  exit 1
fi
if ! grep -q '/app/sandbox-entrypoint.sh' "${CHART_DIR}/templates/deployment.yaml"; then
  echo "Error: candidate chart is missing sandbox-entrypoint.sh" >&2
  exit 1
fi
command -v helm >/dev/null || {
  echo "Error: helm is required on PATH" >&2
  exit 1
}
command -v kubectl >/dev/null || {
  echo "Error: kubectl is required on PATH" >&2
  exit 1
}

export KUBECONFIG="${KUBECONFIG_PATH:-${KUBECONFIG:-}}"

helm_args=(
  upgrade --install "${RELEASE}" "${CHART_DIR}"
  --namespace "${NAMESPACE}"
  --create-namespace
  --values "${VALUES}"
  --set-string "image.repository=${DOOPS_IMAGE_REPOSITORY}"
  --set-string "image.digest=${DOOPS_IMAGE_DIGEST}"
  --timeout "${TIMEOUT}"
  --wait
)

echo "Release: ${RELEASE} / ${NAMESPACE}"
echo "Image: ${DOOPS_IMAGE}"
echo "Values: ${VALUES}"

if [[ "${DRY_RUN}" == true ]]; then
  helm "${helm_args[@]}" --dry-run=server
  echo "Dry-run complete. Helm was not mutated."
  exit 0
fi

helm "${helm_args[@]}"
workload="$(helm get values "${RELEASE}" -n "${NAMESPACE}" -o json | python3 -c 'import json,sys; print(json.load(sys.stdin)["workload"]["name"])')"
kubectl -n "${NAMESPACE}" rollout status "deployment/${workload}" --timeout "${TIMEOUT}"

echo "Install complete: ${RELEASE} -> ${DOOPS_IMAGE}"
