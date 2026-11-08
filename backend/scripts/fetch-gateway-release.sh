#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Download the CI-built doops-gateway binary from a GitHub release and verify it.

Usage:
  bash scripts/fetch-gateway-release.sh \
    --release-tag <release-tag> \
    --source-commit <40-character-commit-sha> \
    [--output bin/doops-gateway]
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE_REPO="${DOOPS_RELEASE_REPO:-symansun/doops}"
ASSET="doops-gateway-linux-amd64"
OUTPUT="${REPO_ROOT}/bin/doops-gateway"
SOURCE_COMMIT=""
RELEASE_TAG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --release-tag)
      RELEASE_TAG="${2:-}"
      shift 2
      ;;
    --source-commit)
      SOURCE_COMMIT="${2:-}"
      shift 2
      ;;
    --output)
      OUTPUT="${2:-}"
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

if [[ -z "${RELEASE_TAG}" ]]; then
  echo "Error: --release-tag is required" >&2
  exit 2
fi
if [[ ! "${SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Error: --source-commit must be a 40-character commit SHA" >&2
  exit 2
fi
command -v curl >/dev/null || {
  echo "Error: curl is required to download the release asset" >&2
  exit 1
}

TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TEMP_DIR}"' EXIT
base="https://github.com/${RELEASE_REPO}/releases/download/${RELEASE_TAG}"
curl --fail --location --silent --show-error --output "${TEMP_DIR}/checksums.txt" "${base}/checksums.txt"
curl --fail --location --silent --show-error --output "${TEMP_DIR}/${ASSET}" "${base}/${ASSET}"

expected="$(awk -v file="${ASSET}" '$2 == file { print $1 }' "${TEMP_DIR}/checksums.txt")"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${TEMP_DIR}/${ASSET}" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "${TEMP_DIR}/${ASSET}" | awk '{print $1}')"
fi
if [[ -z "${expected}" || "${actual}" != "${expected}" ]]; then
  echo "Error: ${ASSET} checksum mismatch for ${RELEASE_TAG}" >&2
  exit 1
fi
for marker in "${RELEASE_TAG}" "${SOURCE_COMMIT}"; do
  if ! grep -aFq "${marker}" "${TEMP_DIR}/${ASSET}"; then
    echo "Error: ${ASSET} does not embed ${marker}" >&2
    exit 1
  fi
done

mkdir -p "$(dirname "${OUTPUT}")"
install -m 0755 "${TEMP_DIR}/${ASSET}" "${OUTPUT}"
echo "Fetched CI Gateway binary: ${OUTPUT} release=${RELEASE_TAG} commit=${SOURCE_COMMIT}"
