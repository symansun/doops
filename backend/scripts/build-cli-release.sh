#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE_DIR="${DOOPS_CLI_RELEASE_DIR:-${REPO_ROOT}/dist/cli}"
BUILDER_IMAGE="docker.io/library/golang:1.24-alpine@sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TEMP_DIR}"' EXIT

command -v docker >/dev/null 2>&1 || {
  echo "docker is required for the immutable CLI release builder" >&2
  exit 1
}

docker run --rm \
  --platform linux/amd64 \
  -e DOOPS_CLI_VERSION="${DOOPS_CLI_VERSION:-dev}" \
  -e DOOPS_CLI_COMMIT="${DOOPS_CLI_COMMIT:-unknown}" \
  -v "${REPO_ROOT}:/workspace:ro" \
  -v "${TEMP_DIR}:/out" \
  -w /workspace \
  "${BUILDER_IMAGE}" \
  sh -ceu '
    apk add --no-cache bash git >/dev/null
    DOOPS_CLI_OUT=/out/doops-host \
    DOOPS_CLI_RELEASE_DIR=/out/release \
      bash scripts/build-cli.sh --all
  '

mkdir -p "${RELEASE_DIR}"
for artifact in doops-darwin-amd64 doops-darwin-arm64 doops-linux-amd64 doops-linux-arm64; do
  install -m 0755 "${TEMP_DIR}/release/${artifact}" "${RELEASE_DIR}/${artifact}"
done
install -m 0644 "${TEMP_DIR}/release/checksums.txt" "${RELEASE_DIR}/checksums.txt"

echo "CLI release artifacts built at ${RELEASE_DIR}"
