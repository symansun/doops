#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DOOPS_IMAGE_TAG:-}" ]]; then
  echo "DOOPS_IMAGE_TAG is required" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

: "${DOOPS_IMAGE_REGISTRY:?DOOPS_IMAGE_REGISTRY is required}"
: "${DOOPS_IMAGE_REPOSITORY:?DOOPS_IMAGE_REPOSITORY is required}"
: "${DOOPS_SOURCE_COMMIT:?DOOPS_SOURCE_COMMIT is required}"
if [[ ! "${DOOPS_SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "DOOPS_SOURCE_COMMIT must be a full 40-character commit SHA" >&2
  exit 1
fi

WEB_IMAGE="${DOOPS_IMAGE_REGISTRY}/${DOOPS_IMAGE_REPOSITORY}/web:${DOOPS_IMAGE_TAG}"

docker build \
  --platform linux/amd64 \
  --label org.opencontainers.image.revision="${DOOPS_SOURCE_COMMIT}" \
  --label org.opencontainers.image.version="${DOOPS_IMAGE_TAG}" \
  -f "${REPO_ROOT}/../Dockerfile.web" \
  -t "${WEB_IMAGE}" \
  "${REPO_ROOT}/.."

test "$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${WEB_IMAGE}")" = "${DOOPS_SOURCE_COMMIT}"
test "$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "${WEB_IMAGE}")" = "${DOOPS_IMAGE_TAG}"

container="$(docker run -d --rm \
  -e DOOPS_CONSOLE_ALLOWED_GATEWAYS=https://doops.invalid \
  "${WEB_IMAGE}")"
trap 'docker rm -f "${container}" >/dev/null 2>&1 || true' EXIT
for _ in $(seq 1 30); do
  if docker exec "${container}" node -e \
    'fetch("http://127.0.0.1:3000").then((r) => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))'; then
    break
  fi
  sleep 1
done
docker exec "${container}" node -e \
  'fetch("http://127.0.0.1:3000").then((r) => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))'
docker rm -f "${container}" >/dev/null
trap - EXIT

docker push "${WEB_IMAGE}"
if [[ "${DOOPS_PUBLISH_LATEST:-}" == "1" && "${DOOPS_IMAGE_TAG}" != "latest" ]]; then
  latest_image="${DOOPS_IMAGE_REGISTRY}/${DOOPS_IMAGE_REPOSITORY}/web:latest"
  docker tag "${WEB_IMAGE}" "${latest_image}"
  docker push "${latest_image}"
  echo "Published Web image latest: ${latest_image} from ${DOOPS_IMAGE_TAG}"
fi

echo "Published Web image: ${WEB_IMAGE}"
