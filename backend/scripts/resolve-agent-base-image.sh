#!/usr/bin/env bash
# Reuse the Agent base image whose inputs match this checkout, building it only
# when Dockerfile.base.light or the pinned runtime versions change. A base that
# is rebuilt on every release gets new layer digests, so every target would
# re-download ~200MB even when nothing below the doops layers changed.
set -euo pipefail

: "${DOOPS_BASE_IMAGE_OUTPUT:?DOOPS_BASE_IMAGE_OUTPUT is required}"
: "${DOOPS_IMAGE_REGISTRY:?DOOPS_IMAGE_REGISTRY is required}"
: "${DOOPS_IMAGE_REPOSITORY:?DOOPS_IMAGE_REPOSITORY is required}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE_REPOSITORY="${DOOPS_IMAGE_REGISTRY}/${DOOPS_IMAGE_REPOSITORY}/base-light"

# DOOPS_AGENT_BASE_IMAGE is the output of this resolution, not one of its inputs.
INPUT_HASH="$(
  {
    cat "${REPO_ROOT}/Dockerfile.base.light"
    grep -v '^DOOPS_AGENT_BASE_IMAGE=' "${REPO_ROOT}/runtime-versions.env"
  } | sha256sum | cut -c1-16
)"
BASE_TAG="inputs-${INPUT_HASH}"
BASE_IMAGE="${BASE_REPOSITORY}:${BASE_TAG}"

if docker pull --platform linux/amd64 "${BASE_IMAGE}" >/dev/null 2>&1; then
  BASE_DIGEST_REF="$(
    docker inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "${BASE_IMAGE}" |
      awk -v prefix="${BASE_REPOSITORY}@" 'index($0, prefix) == 1 { print; exit }'
  )"
  if [[ ! "${BASE_DIGEST_REF}" =~ @sha256:[0-9a-f]{64}$ ]]; then
    echo "Existing Agent base ${BASE_IMAGE} did not resolve to an immutable digest" >&2
    exit 1
  fi
  mkdir -p "$(dirname "${DOOPS_BASE_IMAGE_OUTPUT}")"
  umask 077
  printf '%s\n' "${BASE_DIGEST_REF}" >"${DOOPS_BASE_IMAGE_OUTPUT}"
  echo "Reusing Agent base image: ${BASE_DIGEST_REF}"
  exit 0
fi

echo "No Agent base for inputs ${INPUT_HASH}; building ${BASE_IMAGE}"
DOOPS_BASE_RELEASE_TAG="${BASE_TAG}" bash "${REPO_ROOT}/scripts/build-agent-base-image.sh"
