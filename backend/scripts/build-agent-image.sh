#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DOOPS_RELEASE_TAG:-}" ]]; then
  echo "DOOPS_RELEASE_TAG is required" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "${REPO_ROOT}/runtime-versions.env"

: "${CNB_DOCKER_REGISTRY:?CNB_DOCKER_REGISTRY is required}"
: "${CNB_REPO_SLUG_LOWERCASE:?CNB_REPO_SLUG_LOWERCASE is required}"
: "${CNB_COMMIT:?CNB_COMMIT is required}"

BASE_IMAGE="${DOOPS_AGENT_BASE_IMAGE_OVERRIDE:-${DOOPS_AGENT_BASE_IMAGE}}"
APP_IMAGE="${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}:${DOOPS_RELEASE_TAG}"

if [[ ! "${BASE_IMAGE}" =~ @sha256:[0-9a-f]{64}$ ]]; then
  echo "DOOPS agent base image must be pinned by digest: ${BASE_IMAGE}" >&2
  exit 1
fi

docker pull "${DO_AGENT_IMAGE}"
ACTUAL_DOAGENT_VERSION="$(docker run --rm --entrypoint /usr/local/bin/do-agent "${DO_AGENT_IMAGE}" --version | awk '{print $NF}')"
test "${ACTUAL_DOAGENT_VERSION}" = "${DO_AGENT_VERSION}"
ACTUAL_DOAGENT_REVISION="$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${DO_AGENT_IMAGE}")"
test "${ACTUAL_DOAGENT_REVISION}" = "${DO_AGENT_REVISION}"

docker pull "${BASE_IMAGE}"
docker build \
  --platform linux/amd64 \
  --build-arg DOOPS_AGENT_BASE_IMAGE="${BASE_IMAGE}" \
  --build-arg DO_AGENT_IMAGE="${DO_AGENT_IMAGE}" \
  --build-arg DO_AGENT_VERSION="${DO_AGENT_VERSION}" \
  --build-arg DO_AGENT_REVISION="${DO_AGENT_REVISION}" \
  --build-arg DO_AGENT_INDEX_DIGEST="${DO_AGENT_INDEX_DIGEST}" \
  --build-arg DO_AGENT_PLATFORM="${DO_AGENT_PLATFORM}" \
  --build-arg DO_AGENT_PLATFORM_DIGEST="${DO_AGENT_PLATFORM_DIGEST}" \
  --build-arg DOOPS_SOURCE_COMMIT="${CNB_COMMIT}" \
  --label org.opencontainers.image.revision="${CNB_COMMIT}" \
  --label org.opencontainers.image.version="${DOOPS_RELEASE_TAG}" \
  -f "${REPO_ROOT}/Dockerfile" \
  -t "${APP_IMAGE}" \
  "${REPO_ROOT}"

ACTUAL_BASE_IMAGE="$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.base.name" }}' "${APP_IMAGE}")"
test "${ACTUAL_BASE_IMAGE}" = "${BASE_IMAGE}"
ACTUAL_SOURCE_COMMIT="$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${APP_IMAGE}")"
test "${ACTUAL_SOURCE_COMMIT}" = "${CNB_COMMIT}"
ACTUAL_DOAGENT_IMAGE="$(docker inspect --format '{{ index .Config.Labels "org.l8ai.doagent.image" }}' "${APP_IMAGE}")"
test "${ACTUAL_DOAGENT_IMAGE}" = "${DO_AGENT_IMAGE}"
ACTUAL_DOAGENT_REVISION="$(docker inspect --format '{{ index .Config.Labels "org.l8ai.doagent.revision" }}' "${APP_IMAGE}")"
test "${ACTUAL_DOAGENT_REVISION}" = "${DO_AGENT_REVISION}"
docker run --rm --entrypoint /app/doops-agent "${APP_IMAGE}" -help >/dev/null
docker run --rm --entrypoint /app/doops-gateway "${APP_IMAGE}" -help >/dev/null
test "$(
  docker run --rm --entrypoint /app/doops-gateway "${APP_IMAGE}" version
)" = "doops-gateway source-commit=${CNB_COMMIT}"
ACTUAL_DOAGENT_VERSION="$(docker run --rm --entrypoint /usr/local/bin/do-agent "${APP_IMAGE}" --version | awk '{print $NF}')"
test "${ACTUAL_DOAGENT_VERSION}" = "${DO_AGENT_VERSION}"
docker run --rm --entrypoint /usr/local/bin/buildctl "${APP_IMAGE}" --version
docker run --rm --entrypoint /usr/bin/python3 "${APP_IMAGE}" -c 'import yaml'
docker run --rm --entrypoint /usr/local/bin/helm "${APP_IMAGE}" version --short
docker run --rm --entrypoint /usr/bin/ssh "${APP_IMAGE}" -V ||
  { echo "Agent image is missing the required ssh client" >&2; exit 1; }
if docker run --rm --entrypoint /bin/sh "${APP_IMAGE}" \
  -c 'command -v sshd >/dev/null 2>&1'; then
  echo "Agent image must not contain an SSH server" >&2
  exit 1
fi
docker push "${APP_IMAGE}"

echo "Published Agent image: ${APP_IMAGE}"
