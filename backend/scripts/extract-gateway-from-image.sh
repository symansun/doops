#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Extract the CI-built doops-gateway binary from an immutable Agent image.

Usage:
  bash scripts/extract-gateway-from-image.sh \
    --image registry.example/doops@sha256:<digest> \
    --source-commit <40-character-commit-sha> \
    [--release-tag <release-tag>] \
    [--output bin/doops-gateway]
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE=""
OUTPUT="${REPO_ROOT}/bin/doops-gateway"
PLATFORM="${DOOPS_IMAGE_PLATFORM:-linux/amd64}"
SOURCE_COMMIT=""
RELEASE_TAG=""

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
    --source-commit)
      SOURCE_COMMIT="${2:-}"
      shift 2
      ;;
    --release-tag)
      RELEASE_TAG="${2:-}"
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
if [[ ! "${SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Error: --source-commit must be a 40-character commit SHA" >&2
  exit 2
fi
command -v docker >/dev/null || {
  echo "Error: docker is required to pull the CI image" >&2
  exit 1
}

mkdir -p "$(dirname "${OUTPUT}")"
docker pull --platform "${PLATFORM}" "${IMAGE}"
image_commit="$(
  docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' \
    "${IMAGE}"
)"
if [[ "${image_commit}" != "${SOURCE_COMMIT}" ]]; then
  echo "Error: CI image source commit mismatch: got ${image_commit}, want ${SOURCE_COMMIT}" >&2
  exit 1
fi
image_release="$(
  docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' \
    "${IMAGE}"
)"
if [[ -z "${RELEASE_TAG}" ]]; then
  RELEASE_TAG="${image_release}"
fi
if [[ -z "${RELEASE_TAG}" || "${image_release}" != "${RELEASE_TAG}" ]]; then
  echo "Error: CI image release tag mismatch: got ${image_release:-<empty>}, want ${RELEASE_TAG:-<empty>}" >&2
  exit 1
fi
container_id="$(docker create --platform "${PLATFORM}" "${IMAGE}")"
trap 'docker rm -f "${container_id}" >/dev/null 2>&1 || true' EXIT
docker cp "${container_id}:/app/doops-gateway" "${OUTPUT}"
chmod 0755 "${OUTPUT}"
docker run --rm --platform "${PLATFORM}" --entrypoint /app/doops-gateway "${IMAGE}" -help >/dev/null
test "$(
  docker run --rm --platform "${PLATFORM}" --entrypoint /app/doops-gateway "${IMAGE}" version
)" = "release=${RELEASE_TAG} commit=${SOURCE_COMMIT}"

echo "Extracted CI Gateway binary: ${OUTPUT} release=${RELEASE_TAG} commit=${SOURCE_COMMIT}"
