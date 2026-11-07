#!/usr/bin/env bash
# Shared helpers for resolving the official DoOps Agent image to an immutable digest.
# Source this file; do not execute it.

DOOPS_RELEASE_IMAGE_REPOSITORY="${DOOPS_RELEASE_IMAGE_REPOSITORY:-ghcr.io/symansun/doops}"

doops_release_image_usage_error() {
  echo "Error: $1" >&2
  return 2
}

doops_parse_image_digest() {
  local image="$1"
  if [[ ! "${image}" =~ ^([^@]+)@sha256:([a-f0-9]{64})$ ]]; then
    doops_release_image_usage_error "--image must be repository@sha256:<64-hex>"
    return 2
  fi
  DOOPS_IMAGE_REPOSITORY="${BASH_REMATCH[1]}"
  DOOPS_IMAGE_DIGEST="sha256:${BASH_REMATCH[2]}"
  DOOPS_IMAGE="${DOOPS_IMAGE_REPOSITORY}@${DOOPS_IMAGE_DIGEST}"
}

doops_resolve_release_tag() {
  local tag="$1"
  local repository="${2:-${DOOPS_RELEASE_IMAGE_REPOSITORY}}"
  local pulled digest

  if [[ ! "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    doops_release_image_usage_error "--version must look like v1.2.32"
    return 2
  fi
  if ! command -v docker >/dev/null 2>&1; then
    echo "Error: docker is required to resolve --version ${tag} to a registry digest" >&2
    return 1
  fi

  docker pull --platform "${DOOPS_IMAGE_PLATFORM:-linux/amd64}" "${repository}:${tag}" >/dev/null
  pulled="$(docker image inspect --format '{{index .RepoDigests 0}}' "${repository}:${tag}")"
  digest="${pulled##*@}"
  if [[ ! "${digest}" =~ ^sha256:[a-f0-9]{64}$ ]]; then
    echo "Error: could not resolve ${repository}:${tag} to an immutable digest" >&2
    return 1
  fi
  DOOPS_IMAGE_REPOSITORY="${repository}"
  DOOPS_IMAGE_DIGEST="${digest}"
  DOOPS_IMAGE="${repository}@${digest}"
}
