#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DOOPS_BASE_RELEASE_TAG:-}" ]]; then
  echo "DOOPS_BASE_RELEASE_TAG is required" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "${REPO_ROOT}/runtime-versions.env"

: "${DOOPS_SOURCE_COMMIT:?DOOPS_SOURCE_COMMIT is required}"
: "${DOOPS_DO_AGENT_IMAGE:?DOOPS_DO_AGENT_IMAGE is required}"
[[ "${DOOPS_DO_AGENT_IMAGE}" == ghcr.io/*@"${DO_AGENT_INDEX_DIGEST}" ]] || { echo "Runtime must use the verified GHCR digest in runtime-versions.env" >&2; exit 1; }
DOOPS_BASE_IMAGE_REPOSITORY="${DOOPS_BASE_IMAGE_REPOSITORY:-ghcr.io/symansun/doops-base-light}"

BASE_REPOSITORY="${DOOPS_BASE_IMAGE_REPOSITORY}"
BASE_IMAGE="${BASE_REPOSITORY}:${DOOPS_BASE_RELEASE_TAG}"

docker pull "${DOOPS_DO_AGENT_IMAGE}"
ACTUAL_DOAGENT_VERSION="$(
  docker run --rm --entrypoint /usr/local/bin/do-agent "${DOOPS_DO_AGENT_IMAGE}" --version |
    awk '{print $NF}'
)"
test "${ACTUAL_DOAGENT_VERSION}" = "${DO_AGENT_VERSION}"
ACTUAL_DOAGENT_REVISION="$(
  docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' \
    "${DOOPS_DO_AGENT_IMAGE}"
)"
test "${ACTUAL_DOAGENT_REVISION}" = "${DO_AGENT_REVISION}"

docker build \
  --platform linux/amd64 \
  --build-arg DO_AGENT_IMAGE="${DOOPS_DO_AGENT_IMAGE}" \
  --build-arg DO_AGENT_VERSION="${DO_AGENT_VERSION}" \
  --build-arg DO_AGENT_REVISION="${DO_AGENT_REVISION}" \
  --build-arg DO_AGENT_INDEX_DIGEST="${DO_AGENT_INDEX_DIGEST}" \
  --build-arg DO_AGENT_PLATFORM="${DO_AGENT_PLATFORM}" \
  --build-arg DO_AGENT_PLATFORM_DIGEST="${DO_AGENT_PLATFORM_DIGEST}" \
  --label org.opencontainers.image.revision="${DOOPS_SOURCE_COMMIT}" \
  --label org.opencontainers.image.version="${DOOPS_BASE_RELEASE_TAG}" \
  -f "${REPO_ROOT}/Dockerfile.base.light" \
  -t "${BASE_IMAGE}" \
  "${REPO_ROOT}"

docker run --rm --entrypoint /usr/local/bin/do-agent "${BASE_IMAGE}" --version
docker run --rm --entrypoint /usr/local/bin/buildctl "${BASE_IMAGE}" --version
docker run --rm --entrypoint /usr/bin/skopeo "${BASE_IMAGE}" --version
docker run --rm --entrypoint /usr/local/bin/kubectl "${BASE_IMAGE}" version --client=true
docker run --rm --entrypoint /usr/bin/python3 "${BASE_IMAGE}" -c 'import yaml'
docker run --rm --entrypoint /usr/local/bin/helm "${BASE_IMAGE}" version --short
docker run --rm --entrypoint /usr/bin/ssh "${BASE_IMAGE}" -V
if docker run --rm --entrypoint /bin/sh "${BASE_IMAGE}" \
  -c 'command -v sshd >/dev/null 2>&1'; then
  echo "Agent base image must not contain an SSH server" >&2
  exit 1
fi

docker push "${BASE_IMAGE}"
docker pull "${BASE_IMAGE}"
BASE_DIGEST_REF="$(
  docker inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "${BASE_IMAGE}" |
    awk -v prefix="${BASE_REPOSITORY}@" 'index($0, prefix) == 1 { print; exit }'
)"
if [[ ! "${BASE_DIGEST_REF}" =~ @sha256:[0-9a-f]{64}$ ]]; then
  echo "Published Agent base image did not resolve to an immutable digest" >&2
  exit 1
fi

if [[ -n "${DOOPS_BASE_IMAGE_OUTPUT:-}" ]]; then
  mkdir -p "$(dirname "${DOOPS_BASE_IMAGE_OUTPUT}")"
  umask 077
  printf '%s\n' "${BASE_DIGEST_REF}" >"${DOOPS_BASE_IMAGE_OUTPUT}"
fi

echo "Published Agent base image: ${BASE_DIGEST_REF}"
