#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DOOPS_RELEASE_TAG:-}" ]]; then
  echo "DOOPS_RELEASE_TAG is required" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "${REPO_ROOT}/runtime-versions.env"

: "${DOOPS_SOURCE_COMMIT:?DOOPS_SOURCE_COMMIT is required}"
: "${DOOPS_DO_AGENT_IMAGE:?DOOPS_DO_AGENT_IMAGE is required}"
[[ "${DOOPS_DO_AGENT_IMAGE}" == ghcr.io/*@"${DO_AGENT_INDEX_DIGEST}" ]] || { echo "Runtime must use the verified GHCR digest in runtime-versions.env" >&2; exit 1; }
DOOPS_AGENT_IMAGE_REPOSITORY="${DOOPS_AGENT_IMAGE_REPOSITORY:-ghcr.io/symansun/doops-agent}"
if [[ ! "${DOOPS_SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "DOOPS_SOURCE_COMMIT must be a full 40-character commit SHA" >&2
  exit 1
fi

BASE_IMAGE="${DOOPS_AGENT_BASE_IMAGE_OVERRIDE:-${DOOPS_AGENT_BASE_IMAGE:-}}"
if [[ -z "${BASE_IMAGE}" ]]; then
  echo "DOOPS_AGENT_BASE_IMAGE or DOOPS_AGENT_BASE_IMAGE_OVERRIDE is required" >&2
  exit 1
fi
APP_IMAGE="${DOOPS_AGENT_IMAGE_REPOSITORY}:${DOOPS_RELEASE_TAG}"

source "${REPO_ROOT}/scripts/immutable-image.sh"
if doops_existing_release_image "${APP_IMAGE}" "${DOOPS_SOURCE_COMMIT}" "${DOOPS_RELEASE_TAG}"; then
  docker pull "${APP_IMAGE}"
  test "$(docker run --rm --entrypoint /app/doops-agent "${APP_IMAGE}" -version)" = "release=${DOOPS_RELEASE_TAG} commit=${DOOPS_SOURCE_COMMIT}"
  echo "Reusing verified immutable image: ${APP_IMAGE}"
  exit 0
else
  image_probe_result="$?"
  [[ "${image_probe_result}" == "1" ]] || exit "${image_probe_result}"
fi


if [[ ! "${BASE_IMAGE}" =~ @sha256:[0-9a-f]{64}$ ]]; then
  echo "DOOPS agent base image must be pinned by digest: ${BASE_IMAGE}" >&2
  exit 1
fi

docker pull "${DOOPS_DO_AGENT_IMAGE}"
ACTUAL_DOAGENT_VERSION="$(docker run --rm --entrypoint /usr/local/bin/do-agent "${DOOPS_DO_AGENT_IMAGE}" --version | awk '{print $NF}')"
test "${ACTUAL_DOAGENT_VERSION}" = "${DO_AGENT_VERSION}"
ACTUAL_DOAGENT_REVISION="$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${DOOPS_DO_AGENT_IMAGE}")"
test "${ACTUAL_DOAGENT_REVISION}" = "${DO_AGENT_REVISION}"

docker pull "${BASE_IMAGE}"
docker build \
  --platform linux/amd64 \
  --build-arg DOOPS_AGENT_BASE_IMAGE="${BASE_IMAGE}" \
  --build-arg DO_AGENT_IMAGE="${DOOPS_DO_AGENT_IMAGE}" \
  --build-arg DO_AGENT_VERSION="${DO_AGENT_VERSION}" \
  --build-arg DO_AGENT_REVISION="${DO_AGENT_REVISION}" \
  --build-arg DO_AGENT_INDEX_DIGEST="${DO_AGENT_INDEX_DIGEST}" \
  --build-arg DO_AGENT_PLATFORM="${DO_AGENT_PLATFORM}" \
  --build-arg DO_AGENT_PLATFORM_DIGEST="${DO_AGENT_PLATFORM_DIGEST}" \
  --build-arg DOOPS_RELEASE_TAG="${DOOPS_RELEASE_TAG}" \
  --build-arg DOOPS_SOURCE_COMMIT="${DOOPS_SOURCE_COMMIT}" \
  --label org.opencontainers.image.revision="${DOOPS_SOURCE_COMMIT}" \
  --label org.opencontainers.image.version="${DOOPS_RELEASE_TAG}" \
  -f "${REPO_ROOT}/Dockerfile" \
  -t "${APP_IMAGE}" \
  "${REPO_ROOT}"

ACTUAL_BASE_IMAGE="$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.base.name" }}' "${APP_IMAGE}")"
test "${ACTUAL_BASE_IMAGE}" = "${BASE_IMAGE}"
ACTUAL_SOURCE_COMMIT="$(docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "${APP_IMAGE}")"
test "${ACTUAL_SOURCE_COMMIT}" = "${DOOPS_SOURCE_COMMIT}"
ACTUAL_DOAGENT_IMAGE="$(docker inspect --format '{{ index .Config.Labels "org.l8ai.doagent.image" }}' "${APP_IMAGE}")"
test "${ACTUAL_DOAGENT_IMAGE}" = "${DOOPS_DO_AGENT_IMAGE}"
ACTUAL_DOAGENT_REVISION="$(docker inspect --format '{{ index .Config.Labels "org.l8ai.doagent.revision" }}' "${APP_IMAGE}")"
test "${ACTUAL_DOAGENT_REVISION}" = "${DO_AGENT_REVISION}"
docker run --rm --entrypoint /app/doops-agent "${APP_IMAGE}" -help >/dev/null
test "$(
  docker run --rm --entrypoint /app/doops-agent "${APP_IMAGE}" -version
)" = "release=${DOOPS_RELEASE_TAG} commit=${DOOPS_SOURCE_COMMIT}"
if docker run --rm --entrypoint /bin/sh "${APP_IMAGE}" -c 'test -e /app/doops-gateway'; then
  echo "Agent image must not contain the Gateway binary; it ships as a release asset" >&2
  exit 1
fi
ACTUAL_DOAGENT_VERSION="$(docker run --rm --entrypoint /usr/local/bin/do-agent "${APP_IMAGE}" --version | awk '{print $NF}')"
test "${ACTUAL_DOAGENT_VERSION}" = "${DO_AGENT_VERSION}"
docker run --rm --entrypoint /usr/local/bin/buildctl "${APP_IMAGE}" --version
docker run --rm --entrypoint /usr/bin/skopeo "${APP_IMAGE}" --version
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
if [[ "${DOOPS_PUBLISH_LATEST:-}" == "1" && "${DOOPS_RELEASE_TAG}" != "latest" ]]; then
  latest_image="${DOOPS_AGENT_IMAGE_REPOSITORY}:latest"
  docker tag "${APP_IMAGE}" "${latest_image}"
  docker push "${latest_image}"
  echo "Published Agent image latest: ${latest_image} from ${DOOPS_RELEASE_TAG}"
fi

echo "Published Agent image: ${APP_IMAGE}"
