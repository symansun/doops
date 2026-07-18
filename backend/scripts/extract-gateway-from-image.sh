#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Extract the CI-built doops-gateway binary from an immutable Agent image.

Usage:
  bash scripts/extract-gateway-from-image.sh \
    --image registry.example/doops@sha256:<digest> \
    [--output bin/doops-gateway]
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE=""
OUTPUT="${REPO_ROOT}/bin/doops-gateway"
PLATFORM="${DOOPS_IMAGE_PLATFORM:-linux/amd64}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --image)
      IMAGE="${2:-}"
      shift 2
      ;;
    --output)
      OUTPUT="${2:-}"
      shift 2
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

if [[ ! "${IMAGE}" =~ @sha256:[a-f0-9]{64}$ ]]; then
  echo "Error: --image must use an immutable sha256 registry digest" >&2
  exit 2
fi
command -v docker >/dev/null || {
  echo "Error: docker is required to pull the CI image" >&2
  exit 1
}

mkdir -p "$(dirname "${OUTPUT}")"
docker pull --platform "${PLATFORM}" "${IMAGE}"
container_id="$(docker create --platform "${PLATFORM}" "${IMAGE}")"
trap 'docker rm -f "${container_id}" >/dev/null 2>&1 || true' EXIT
docker cp "${container_id}:/app/doops-gateway" "${OUTPUT}"
chmod 0755 "${OUTPUT}"
docker run --rm --platform "${PLATFORM}" --entrypoint /app/doops-gateway "${IMAGE}" -help >/dev/null

echo "Extracted CI Gateway binary: ${OUTPUT}"
