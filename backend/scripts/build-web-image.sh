#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DOOPS_RELEASE_TAG:-}" ]]; then
  echo "DOOPS_RELEASE_TAG is required" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

: "${DOOPS_SOURCE_COMMIT:?DOOPS_SOURCE_COMMIT is required}"
DOOPS_WEB_IMAGE_REPOSITORY="${DOOPS_WEB_IMAGE_REPOSITORY:-ghcr.io/symansun/doops-web}"

WEB_IMAGE="${DOOPS_WEB_IMAGE_REPOSITORY}:${DOOPS_RELEASE_TAG}"

source "${REPO_ROOT}/scripts/immutable-image.sh"
if doops_existing_release_image "${WEB_IMAGE}" "${DOOPS_SOURCE_COMMIT}" "${DOOPS_RELEASE_TAG}"; then
  docker pull "${WEB_IMAGE}"
  echo "Reusing verified immutable image: ${WEB_IMAGE}"
  exit 0
else
  image_probe_result="$?"
  [[ "${image_probe_result}" == "1" ]] || exit "${image_probe_result}"
fi


docker build \
  --platform linux/amd64 \
  --label org.opencontainers.image.revision="${DOOPS_SOURCE_COMMIT}" \
  --label org.opencontainers.image.version="${DOOPS_RELEASE_TAG}" \
  -f "${REPO_ROOT}/../Dockerfile.web" \
  -t "${WEB_IMAGE}" \
  "${REPO_ROOT}/.."

test "$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${WEB_IMAGE}")" = "${DOOPS_SOURCE_COMMIT}"
test "$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "${WEB_IMAGE}")" = "${DOOPS_RELEASE_TAG}"

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
if [[ "${DOOPS_PUBLISH_LATEST:-}" == "1" && "${DOOPS_RELEASE_TAG}" != "latest" ]]; then
  latest_image="${DOOPS_WEB_IMAGE_REPOSITORY}:latest"
  docker tag "${WEB_IMAGE}" "${latest_image}"
  docker push "${latest_image}"
  echo "Published Web image latest: ${latest_image} from ${DOOPS_RELEASE_TAG}"
fi

echo "Published Web image: ${WEB_IMAGE}"
