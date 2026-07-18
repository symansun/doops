#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DOOPS_RELEASE_TAG:-}" ]]; then
  echo "DOOPS_RELEASE_TAG is required" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

: "${CNB_DOCKER_REGISTRY:?CNB_DOCKER_REGISTRY is required}"
: "${CNB_REPO_SLUG_LOWERCASE:?CNB_REPO_SLUG_LOWERCASE is required}"
: "${CNB_COMMIT:?CNB_COMMIT is required}"

WEB_IMAGE="${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}/web:${DOOPS_RELEASE_TAG}"

docker build \
  --platform linux/amd64 \
  --label org.opencontainers.image.revision="${CNB_COMMIT}" \
  --label org.opencontainers.image.version="${DOOPS_RELEASE_TAG}" \
  -f "${REPO_ROOT}/../Dockerfile.web" \
  -t "${WEB_IMAGE}" \
  "${REPO_ROOT}/.."

test "$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${WEB_IMAGE}")" = "${CNB_COMMIT}"
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

echo "Published Web image: ${WEB_IMAGE}"
